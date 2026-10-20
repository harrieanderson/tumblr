package main

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

func runListFollowers() error {
	skipChromeRecovery = true
	nativeChromeClicks = false
	accs := loggedInUniqueAccounts(20)
	if len(accs) == 0 {
		accs = enabledAccounts()
	} else {
		fmt.Printf("Listing followers on the first %d logged-in blogs.\n\n", len(accs))
	}
	seen := map[string]bool{}
	var unique []string
	live, dead := 0, 0
	for _, a := range accs {
		path := filepath.Join(profilesAuthDir, a.ID, "auth.json")
		auth, err := loadAuthFrom(path)
		if err != nil || !authHasSID(auth) {
			fmt.Printf("======== %s  no cookies ========\n\n", a.ID)
			dead++
			continue
		}
		client := newHumanClient()
		if err := refreshCSRF(client, &auth); err != nil {
			fmt.Printf("======== %s  session dead ========\n\n", a.ID)
			dead++
			continue
		}
		blogs, err := listAccountBlogsDetailed(client, auth)
		if err != nil {
			fmt.Printf("======== %s  %v ========\n\n", a.ID, err)
			dead++
			continue
		}
		live++
		for _, b := range blogs {
			id := b.UUID
			if id == "" {
				id = b.Name
			}
			names, err := fetchBlogFollowerNames(client, auth, id)
			label := b.Name
			if b.Primary {
				label += " (primary)"
			}
			if err != nil {
				fmt.Printf("======== %s  @%s  error: %v ========\n\n", a.ID, label, err)
				continue
			}
			fmt.Printf("======== %s  @%s  %d followers ========\n", a.ID, label, len(names))
			if len(names) == 0 {
				fmt.Println("(none)")
			}
			for _, n := range names {
				fmt.Println(n)
				k := strings.ToLower(n)
				if n != "" && !seen[k] {
					seen[k] = true
					unique = append(unique, n)
				}
			}
			fmt.Println()
			time.Sleep(80 * time.Millisecond)
		}
	}
	sort.Strings(unique)
	fmt.Println("======== unique names across accounts ========")
	for _, n := range unique {
		fmt.Println(n)
	}
	fmt.Printf("\nAccounts with session: %d   skipped: %d   unique followers: %d\n", live, dead, len(unique))
	return nil
}

func fetchBlogFollowerNames(client *http.Client, auth Auth, blogID string) ([]string, error) {
	blogID = strings.TrimSpace(blogID)
	if blogID == "" {
		return nil, fmt.Errorf("no blog id")
	}
	var out []string
	seen := map[string]bool{}
	offset := 0
	for page := 0; page < 40; page++ {
		u, err := url.Parse("https://www.tumblr.com/api/v2/blog/" + url.PathEscape(blogID) + "/followers")
		if err != nil {
			return out, err
		}
		q := u.Query()
		q.Set("limit", "50")
		q.Set("offset", fmt.Sprintf("%d", offset))
		u.RawQuery = q.Encode()
		req, err := http.NewRequest("GET", u.String(), nil)
		if err != nil {
			return out, err
		}
		setCommonHeaders(req, auth)
		req.Header.Set("X-CSRF", auth.CSRF)
		req.Header.Set("Referer", "https://www.tumblr.com/settings/blog/"+url.PathEscape(blogID))
		resp, err := client.Do(req)
		if err != nil {
			return out, err
		}
		body, err := io.ReadAll(resp.Body)
		resp.Body.Close()
		if err != nil {
			return out, err
		}
		takeCSRF(resp.Header, &auth)
		if resp.StatusCode != http.StatusOK {
			return out, fmt.Errorf("%s: %s", resp.Status, truncate(string(body), 80))
		}
		var raw map[string]any
		if err := json.Unmarshal(body, &raw); err != nil {
			return out, err
		}
		response, _ := raw["response"].(map[string]any)
		if response == nil {
			break
		}
		users, _ := response["users"].([]any)
		if len(users) == 0 {
			break
		}
		got := 0
		for _, el := range users {
			m, ok := el.(map[string]any)
			if !ok {
				continue
			}
			n := strings.TrimSpace(firstNonEmpty(
				anyToString(m["name"]),
				anyToString(m["blog_name"]),
				anyToString(nestedString(m, "blog", "name")),
			))
			if n == "" {
				continue
			}
			got++
			k := strings.ToLower(n)
			if seen[k] {
				continue
			}
			seen[k] = true
			out = append(out, n)
		}
		if got == 0 {
			break
		}
		offset += got
		time.Sleep(80 * time.Millisecond)
	}
	return out, nil
}

func runWhoami() error {
	acc, ok := currentAccount()
	id, email := "", ""
	if ok {
		id, email = acc.ID, acc.Email
	}
	auth, err := loadAuth()
	if err != nil {
		return err
	}
	if !authHasSID(auth) {
		fmt.Printf("%s  (%s)  not logged in\n", id, email)
		return fmt.Errorf("no saved cookies")
	}
	client := newAuthClient(&auth)
	if err := refreshCSRF(client, &auth); err != nil {
		if err := ensureSession(client, &auth); err != nil {
			return err
		}
	}
	blogs, err := listAccountBlogsDetailed(client, auth)
	if err != nil {
		return err
	}
	fillMissingFollowers(client, auth, blogs)
	fmt.Printf("%s  (%s)\n", id, email)
	for _, b := range blogs {
		tag := ""
		if b.Primary {
			tag = "  (primary)"
		}
		fmt.Printf("  @%s%s  %d followers\n", b.Name, tag, b.Followers)
	}
	_ = saveFollowerSnapshot(id, blogs)
	return nil
}

func listAccountBlogs(client *http.Client, auth Auth) (names []string, primary string, err error) {
	blogs, err := listAccountBlogsDetailed(client, auth)
	if err != nil {
		return nil, "", err
	}
	for _, b := range blogs {
		names = append(names, b.Name)
		if b.Primary {
			primary = b.Name
		}
	}
	if primary == "" && len(names) > 0 {
		primary = names[0]
	}
	return names, primary, nil
}

type tumblrBlog struct {
	Name      string
	UUID      string
	Primary   bool
	Followers int
}

func mapFollowers(bm map[string]any) int {
	for _, k := range []string{"followers", "followersCount", "followerCount", "followers_count"} {
		if _, ok := bm[k]; ok {
			return anyToInt(bm[k])
		}
	}
	return 0
}

func listAccountBlogsDetailed(client *http.Client, auth Auth) ([]tumblrBlog, error) {
	req, err := http.NewRequest("GET", "https://www.tumblr.com/api/v2/user/info", nil)
	if err != nil {
		return nil, err
	}
	setCommonHeaders(req, auth)
	req.Header.Set("X-CSRF", auth.CSRF)
	req.Header.Set("Referer", "https://www.tumblr.com/dashboard")
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("%s: %s", resp.Status, truncate(string(body), 120))
	}
	var raw map[string]any
	if err := json.Unmarshal(body, &raw); err != nil {
		return nil, err
	}
	response, _ := raw["response"].(map[string]any)
	user, _ := response["user"].(map[string]any)
	blogs, _ := user["blogs"].([]any)
	var out []tumblrBlog
	for _, b := range blogs {
		bm, ok := b.(map[string]any)
		if !ok {
			continue
		}
		n := anyToString(bm["name"])
		u := firstNonEmpty(anyToString(bm["uuid"]), anyToString(bm["blog_uuid"]))
		if n == "" && u == "" {
			continue
		}
		if n == "" {
			n = strings.TrimPrefix(u, "t:")
		}
		if u == "" {
			u = n
		}
		out = append(out, tumblrBlog{Name: n, UUID: u, Primary: bm["primary"] == true, Followers: mapFollowers(bm)})
	}
	if len(out) == 0 {
		if n := anyToString(user["name"]); n != "" {
			out = []tumblrBlog{{Name: n, UUID: n, Primary: true}}
		}
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("no blogs in user/info")
	}
	return out, nil
}

func fillMissingFollowers(client *http.Client, auth Auth, blogs []tumblrBlog) {
	for i, b := range blogs {
		if b.Followers > 0 {
			continue
		}
		id := b.UUID
		if id == "" {
			id = b.Name
		}
		p, err := fetchBlogProfile(client, auth, id)
		if err == nil && p.Followers > 0 {
			blogs[i].Followers = p.Followers
		}
	}
}

type followerSnapshot struct {
	At        string         `json:"at"`
	Primary   string         `json:"primary"`
	Followers int            `json:"followers"`
	Blogs     map[string]int `json:"blogs"`
}

type followerLog struct {
	Latest  followerSnapshot   `json:"latest"`
	History []followerSnapshot `json:"history"`
}

func followerLogPath(id string) string {
	id = strings.TrimSpace(id)
	if id == "" || !usingIsolatedProfiles() {
		return filepath.Join("data", "followers.json")
	}
	return filepath.Join(profilesDataDir, id, "followers.json")
}

func loadFollowerLog(id string) followerLog {
	raw, err := os.ReadFile(followerLogPath(id))
	if err != nil {
		return followerLog{}
	}
	var log followerLog
	_ = json.Unmarshal(raw, &log)
	return log
}

func assignedBlogName(id string) string {
	id = accountSlug(id)
	if id == "" {
		return ""
	}
	return normalizeBlogName(loadNameMap(assignedUsernamesFile)[id])
}

func snapshotBlogCount(s followerSnapshot, blog string) (int, bool) {
	blog = normalizeBlogName(blog)
	if blog == "" {
		return 0, false
	}
	if normalizeBlogName(s.Primary) == blog && s.Followers > 0 {
		return s.Followers, true
	}
	for name, n := range s.Blogs {
		if normalizeBlogName(name) == blog && n > 0 {
			return n, true
		}
	}
	return 0, false
}

func newestFollowerSnapshots(log followerLog) []followerSnapshot {
	snaps := followerSnapshots(log)
	out := make([]followerSnapshot, 0, len(snaps))
	for i := len(snaps) - 1; i >= 0; i-- {
		out = append(out, snaps[i])
	}
	return out
}

func looksLikeStampedZoeyofc(log followerLog) bool {
	latest := normalizeBlogName(log.Latest.Primary)
	if latest != "zoeyofc" {
		return false
	}
	for _, s := range followerSnapshots(log) {
		p := normalizeBlogName(s.Primary)
		if p != "" && p != "zoeyofc" && s.Followers > log.Latest.Followers {
			return true
		}
	}
	return false
}

func bestAccountBlogStats(id string, log followerLog) (blog string, followers int) {
	assigned := assignedBlogName(id)
	snaps := newestFollowerSnapshots(log)
	if assigned != "" {
		for _, s := range snaps {
			if n, ok := snapshotBlogCount(s, assigned); ok {
				return assigned, n
			}
		}
		// A stale snapshot from another session must not relabel this login.
		return assigned, 0
	}
	stamped := looksLikeStampedZoeyofc(log)
	for _, s := range snaps {
		if s.Primary == "" || s.Followers <= 0 {
			continue
		}
		if stamped && normalizeBlogName(s.Primary) == "zoeyofc" {
			continue
		}
		return s.Primary, s.Followers
	}
	if assigned != "" {
		return assigned, 0
	}
	return log.Latest.Primary, log.Latest.Followers
}

// midFollowerAccounts picks n accounts from the middle of the follower ranking
// (by last known primary-blog count). Used for "reach + mid replies".
func midFollowerAccounts(accs []Account, n int) []Account {
	if n <= 0 || len(accs) == 0 {
		return nil
	}
	type row struct {
		a   Account
		fol int
	}
	rows := make([]row, 0, len(accs))
	for _, a := range accs {
		_, fol := bestAccountBlogStats(a.ID, loadFollowerLog(a.ID))
		rows = append(rows, row{a: a, fol: fol})
	}
	sort.SliceStable(rows, func(i, j int) bool {
		if rows[i].fol != rows[j].fol {
			return rows[i].fol < rows[j].fol
		}
		return rows[i].a.ID < rows[j].a.ID
	})
	if n >= len(rows) {
		out := make([]Account, len(rows))
		for i, r := range rows {
			out[i] = r.a
		}
		return out
	}
	start := (len(rows) - n) / 2
	out := make([]Account, n)
	for i := 0; i < n; i++ {
		out[i] = rows[start+i].a
	}
	return out
}

// accountsExcept returns accs with exclude IDs removed (order preserved).
func accountsExcept(accs, exclude []Account) []Account {
	skip := map[string]bool{}
	for _, a := range exclude {
		skip[a.ID] = true
	}
	var out []Account
	for _, a := range accs {
		if !skip[a.ID] {
			out = append(out, a)
		}
	}
	return out
}

func saveFollowerSnapshot(id string, blogs []tumblrBlog) error {
	if len(blogs) == 0 {
		return nil
	}
	snap := followerSnapshot{
		At:    time.Now().Format(time.RFC3339),
		Blogs: map[string]int{},
	}
	for _, b := range blogs {
		if b.Name == "" {
			continue
		}
		snap.Blogs[b.Name] = b.Followers
		if b.Primary {
			snap.Primary = b.Name
			snap.Followers = b.Followers
		}
	}
	if snap.Primary == "" && len(blogs) > 0 {
		snap.Primary = blogs[0].Name
		snap.Followers = blogs[0].Followers
	}
	log := loadFollowerLog(id)
	log.Latest = snap
	log.History = append(log.History, snap)
	if len(log.History) > 80 {
		log.History = log.History[len(log.History)-80:]
	}
	path := followerLogPath(id)
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		return err
	}
	raw, err := json.MarshalIndent(log, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, raw, 0644)
}

func parseFollowerAt(at string) time.Time {
	at = strings.TrimSpace(at)
	if at == "" {
		return time.Time{}
	}
	if t, err := time.Parse(time.RFC3339, at); err == nil {
		return t
	}
	if t, err := time.Parse(time.RFC3339Nano, at); err == nil {
		return t
	}
	return time.Time{}
}

func followerSnapshots(log followerLog) []followerSnapshot {
	out := append([]followerSnapshot(nil), log.History...)
	if log.Latest.At == "" && log.Latest.Followers == 0 && log.Latest.Primary == "" {
		return out
	}
	if len(out) == 0 {
		return []followerSnapshot{log.Latest}
	}
	last := out[len(out)-1]
	if last.At != log.Latest.At || last.Followers != log.Latest.Followers || last.Primary != log.Latest.Primary {
		out = append(out, log.Latest)
	}
	return out
}

func snapshotCount(s followerSnapshot, blog string) (int, bool) {
	blog = strings.TrimSpace(blog)
	if blog != "" && s.Blogs != nil {
		if n, ok := s.Blogs[blog]; ok {
			return n, true
		}
	}
	if blog == "" || s.Primary == "" || strings.EqualFold(blog, s.Primary) {
		if s.At != "" || s.Followers > 0 {
			return s.Followers, true
		}
	}
	return 0, false
}

func accountDailyNewFollowers(log followerLog, now time.Time) int {
	if now.IsZero() {
		now = time.Now()
	}
	now = now.Local()
	startToday := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, now.Location())
	startYest := startToday.AddDate(0, 0, -1)
	snaps := followerSnapshots(log)
	if len(snaps) == 0 {
		return 0
	}
	latest := log.Latest
	if latest.At == "" && latest.Followers == 0 {
		latest = snaps[len(snaps)-1]
	}
	blog := strings.TrimSpace(latest.Primary)
	cur, ok := snapshotCount(latest, blog)
	if !ok {
		cur = latest.Followers
	}
	var lastYest, firstToday *followerSnapshot
	for i := range snaps {
		s := &snaps[i]
		t := parseFollowerAt(s.At)
		if t.IsZero() {
			continue
		}
		t = t.In(now.Location())
		if !t.Before(startYest) && t.Before(startToday) {
			lastYest = s
		}
		if !t.Before(startToday) && firstToday == nil {
			cp := snaps[i]
			firstToday = &cp
		}
	}
	baseline := firstToday
	if lastYest != nil {
		baseline = lastYest
	}
	if baseline == nil {
		return 0
	}
	old, ok := snapshotCount(*baseline, blog)
	if !ok {
		return 0
	}
	d := cur - old
	if d < 0 {
		return 0
	}
	return d
}

type followerTally struct {
	TodayNew int    `json:"todayNew"`
	Total    int    `json:"total"`
	Accounts int    `json:"accounts"`
	Date     string `json:"date"`
}

func logOwnFollowers(client *http.Client, auth *Auth, logFn func(string)) {
	if auth == nil {
		return
	}
	id := accountIDFromAuth(*auth)
	prev := loadFollowerLog(id).Latest
	blogs, err := listAccountBlogsDetailed(client, *auth)
	if err != nil {
		accLog(id, logFn, "followers: %v", err)
		return
	}
	fillMissingFollowers(client, *auth, blogs)
	_ = saveFollowerSnapshot(id, blogs)
	for _, b := range blogs {
		tag := b.Name
		if b.Primary {
			tag += " (primary)"
		}
		delta := ""
		if prev.Blogs != nil {
			if old, ok := prev.Blogs[b.Name]; ok {
				d := b.Followers - old
				if d > 0 {
					delta = fmt.Sprintf("  %+d since last check", d)
				} else if d < 0 {
					delta = fmt.Sprintf("  %d since last check", d)
				}
			}
		}
		accLog(id, logFn, "  followers @%s: %d%s", tag, b.Followers, delta)
	}
}

func runActivityCheck() error {
	skipChromeRecovery = true
	acc, ok := currentAccount()
	id := ""
	if ok {
		id = acc.ID
	}
	auth, err := loadAuth()
	if err != nil {
		return err
	}
	if !authHasSID(auth) {
		fmt.Printf("%-28s  not logged in\n", id)
		return nil
	}
	client := newAuthClient(&auth)
	if err := refreshCSRF(client, &auth); err != nil {
		fmt.Printf("%-28s  session dead\n", id)
		return nil
	}

	prev := loadFollowerLog(id).Latest
	blogs, err := listAccountBlogsDetailed(client, auth)
	if err != nil {
		fmt.Printf("%-28s  user/info failed: %v\n", id, err)
		return nil
	}
	fillMissingFollowers(client, auth, blogs)

	primary := ""
	followers := 0
	for _, b := range blogs {
		if b.Primary || primary == "" {
			primary = b.Name
			followers = b.Followers
			if b.Primary {
				break
			}
		}
	}
	delta := 0
	deltaKnown := false
	if prev.Blogs != nil {
		if old, ok := prev.Blogs[primary]; ok {
			delta = followers - old
			deltaKnown = true
		} else if prev.Followers > 0 || prev.Primary == primary {
			delta = followers - prev.Followers
			deltaKnown = true
		}
	}
	deltaTxt := "n/a"
	if deltaKnown {
		deltaTxt = fmt.Sprintf("%+d", delta)
	}

	unreadNotes, inbox, msgs := fetchActivityCounts(client, auth)
	since := time.Now().Add(-8 * time.Hour)
	summary := summarizeNotifications(client, auth, blogs, since)

	fmt.Printf("%-28s  @%-28s  followers %d (%s)  unread notes=%d inbox=%d msgs=%d  %s\n",
		id, primary, followers, deltaTxt, unreadNotes, inbox, msgs, summary)
	_ = saveFollowerSnapshot(id, blogs)
	return nil
}

func fetchActivityCounts(client *http.Client, auth Auth) (notes, inbox, msgs int) {
	u := "https://www.tumblr.com/api/v2/user/counts?unread=true&inbox=true&unread_messages=true&blog_notification_counts=true"
	req, err := http.NewRequest("GET", u, nil)
	if err != nil {
		return
	}
	setCommonHeaders(req, auth)
	req.Header.Set("X-CSRF", auth.CSRF)
	req.Header.Set("Referer", "https://www.tumblr.com/dashboard")
	resp, err := client.Do(req)
	if err != nil {
		return
	}
	defer resp.Body.Close()
	takeCSRF(resp.Header, &auth)
	body, err := io.ReadAll(resp.Body)
	if err != nil || resp.StatusCode != http.StatusOK {
		return
	}
	var raw map[string]any
	if json.Unmarshal(body, &raw) != nil {
		return
	}
	response, _ := raw["response"].(map[string]any)
	if response == nil {
		return
	}
	inbox = anyToInt(response["inbox"])
	msgs = anyToInt(response["unread_messages"])
	if msgs == 0 {
		msgs = anyToInt(response["unreadMessages"])
	}
	notes = anyToInt(response["notifications"])
	if notes == 0 {
		if n, ok := response["notifications"].(map[string]any); ok {
			notes = anyToInt(n["unread"])
		}
	}
	if blogs, ok := response["blogs"].(map[string]any); ok {
		for _, v := range blogs {
			bm, ok := v.(map[string]any)
			if !ok {
				continue
			}
			notes += anyToInt(bm["notifications"])
			notes += anyToInt(bm["notification"])
			if n, ok := bm["notifications"].(map[string]any); ok {
				notes += anyToInt(n["unread"])
			}
		}
	}
	return notes, inbox, msgs
}

func summarizeNotifications(client *http.Client, auth Auth, blogs []tumblrBlog, since time.Time) string {
	counts := map[string]int{}
	from := map[string]int{}
	for _, b := range blogs {
		key := b.UUID
		if key == "" {
			key = b.Name
		}
		if key == "" {
			continue
		}
		items := fetchBlogNotifications(client, auth, key)
		for _, it := range items {
			if !it.at.IsZero() && it.at.Before(since) {
				continue
			}
			kind := it.kind
			if kind == "" {
				kind = "other"
			}
			counts[kind]++
			if it.from != "" && (kind == "follower" || kind == "follow" || kind == "like" || kind == "reblog" || kind == "reply") {
				from[it.from]++
			}
		}
	}
	if len(counts) == 0 {
		return "recent: none"
	}
	order := []string{"follower", "follow", "like", "reblog", "reply", "mention", "ask", "note"}
	var parts []string
	seen := map[string]bool{}
	for _, k := range order {
		if n := counts[k]; n > 0 {
			parts = append(parts, fmt.Sprintf("%s×%d", k, n))
			seen[k] = true
		}
	}
	for k, n := range counts {
		if !seen[k] && !strings.HasPrefix(k, "milestone") {
			parts = append(parts, fmt.Sprintf("%s×%d", k, n))
		}
	}
	var who []string
	for name, n := range from {
		if name == "" {
			continue
		}
		if n > 1 {
			who = append(who, fmt.Sprintf("%s×%d", name, n))
		} else {
			who = append(who, name)
		}
		if len(who) >= 6 {
			break
		}
	}
	out := "recent: " + strings.Join(parts, ", ")
	if len(who) > 0 {
		out += "  from " + strings.Join(who, ", ")
	}
	return out
}

type activityNote struct {
	kind string
	from string
	at   time.Time
}

func fetchBlogNotifications(client *http.Client, auth Auth, blogID string) []activityNote {
	u, _ := url.Parse("https://www.tumblr.com/api/v2/blog/" + url.PathEscape(blogID) + "/notifications")
	q := u.Query()
	q.Set("limit", "20")
	u.RawQuery = q.Encode()
	req, err := http.NewRequest("GET", u.String(), nil)
	if err != nil {
		return nil
	}
	setCommonHeaders(req, auth)
	req.Header.Set("X-CSRF", auth.CSRF)
	req.Header.Set("Referer", "https://www.tumblr.com/activity")
	resp, err := client.Do(req)
	if err != nil {
		return nil
	}
	defer resp.Body.Close()
	takeCSRF(resp.Header, &auth)
	body, err := io.ReadAll(resp.Body)
	if err != nil || resp.StatusCode != http.StatusOK {
		return nil
	}
	var raw map[string]any
	if json.Unmarshal(body, &raw) != nil {
		return nil
	}
	response, _ := raw["response"].(map[string]any)
	if response == nil {
		return nil
	}
	list, _ := response["notifications"].([]any)
	if list == nil {
		list, _ = response["items"].([]any)
	}
	var out []activityNote
	for _, el := range list {
		m, ok := el.(map[string]any)
		if !ok {
			continue
		}
		n := activityNote{
			kind: strings.ToLower(firstNonEmpty(
				anyToString(m["type"]),
				anyToString(m["kind"]),
				anyToString(m["notification_type"]),
			)),
			from: firstNonEmpty(
				anyToString(m["from_tumblelog_name"]),
				anyToString(m["fromTumblelogName"]),
				anyToString(nestedString(m, "from_tumblelog", "name")),
				anyToString(nestedString(m, "from", "name")),
			),
			at: parseNoteTime(m),
		}
		out = append(out, n)
	}
	return out
}

func nestedString(m map[string]any, keys ...string) string {
	var cur any = m
	for _, k := range keys {
		mm, ok := cur.(map[string]any)
		if !ok {
			return ""
		}
		cur = mm[k]
	}
	return anyToString(cur)
}

func parseNoteTime(m map[string]any) time.Time {
	for _, k := range []string{"timestamp", "time", "created", "created_at", "date"} {
		if v, ok := m[k]; ok {
			switch t := v.(type) {
			case float64:
				if t > 1e12 {
					return time.UnixMilli(int64(t))
				}
				if t > 0 {
					return time.Unix(int64(t), 0)
				}
			case string:
				if n := anyToInt(t); n > 0 {
					if n > 1e12 {
						return time.UnixMilli(int64(n))
					}
					return time.Unix(int64(n), 0)
				}
				if ts, err := time.Parse(time.RFC3339, t); err == nil {
					return ts
				}
			}
		}
	}
	return time.Time{}
}
