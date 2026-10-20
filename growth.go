package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

const (
	growthReportFile      = "config/growth-report.json"
	followAttributionFile = "data/follow-attribution.jsonl"
	growthParkBelowGain7d = -2
	growthWinnerGain1dMin = 1
	growthWinnerGain7dMin = 3
)

type growthTier string

const (
	growthWinner growthTier = "winner"
	growthFlat   growthTier = "flat"
	growthPark   growthTier = "park"
)

type growthAccountRow struct {
	ID         string     `json:"id"`
	Blog       string     `json:"blog"`
	Followers  int        `json:"followers"`
	Gain1d     int        `json:"gain1d"`
	Gain7d     int        `json:"gain7d"`
	Tier       growthTier `json:"tier"`
	Reason     string     `json:"reason"`
	HasSession bool       `json:"hasSession"`
	Terminated bool       `json:"terminated"`
}

type growthSeedStat struct {
	Source   string `json:"source"`
	Kind     string `json:"kind"`
	Follows  int    `json:"follows"`
	Accounts int    `json:"accounts"`
}

type growthReport struct {
	At         string             `json:"at"`
	Summary    string             `json:"summary"`
	Accounts   []growthAccountRow `json:"accounts"`
	ReachOrder []string           `json:"reachOrder"`
	Parked     []string           `json:"parked"`
	Seeds      struct {
		Promoted []string         `json:"promoted"`
		Demoted  []string         `json:"demoted"`
		Stats    []growthSeedStat `json:"stats"`
	} `json:"seeds"`
}

type followAttributionEvent struct {
	At      string `json:"at"`
	Account string `json:"account"`
	Target  string `json:"target"`
	Kind    string `json:"kind"`
	Source  string `json:"source"`
	Hour    int    `json:"hour"`
}

// fetchGrowthFollowers uses saved sessions without changing the active account.
func fetchGrowthFollowers(a Account) ([]tumblrBlog, error) {
	if accountIsTerminated(a.ID) {
		return nil, fmt.Errorf("terminated account")
	}
	auth := peekAccountAuth(a.ID)
	if !authHasSID(auth) {
		return nil, fmt.Errorf("no saved session")
	}
	client := &http.Client{Timeout: 30 * time.Second}
	if err := refreshCSRF(client, &auth); err != nil {
		return nil, err
	}
	blogs, err := listAccountBlogsDetailed(client, auth)
	if err != nil {
		return nil, err
	}
	if len(blogs) == 0 {
		return nil, fmt.Errorf("no blogs returned")
	}
	fillMissingFollowers(client, auth, blogs)
	return blogs, nil
}

func refreshGrowthFollowers(accs []Account, fetch func(Account) ([]tumblrBlog, error), logFn func(string)) {
	updated := 0
	for i, a := range accs {
		blogs, err := fetch(a)
		if err == nil && len(blogs) == 0 {
			err = fmt.Errorf("no blogs returned")
		}
		if err == nil {
			err = saveFollowerSnapshot(a.ID, blogs)
		}
		if err != nil {
			logFn(fmt.Sprintf("Followers %d/%d %s: keeping saved count (%v)", i+1, len(accs), a.ID, err))
			continue
		}
		updated++
		blog, count := bestAccountBlogStats(a.ID, loadFollowerLog(a.ID))
		logFn(fmt.Sprintf("Followers %d/%d %s: @%s %d", i+1, len(accs), a.ID, blog, count))
	}
	logFn(fmt.Sprintf("Follower counts refreshed: %d/%d accounts; %d using saved counts.", updated, len(accs), len(accs)-updated))
}

// runGrowthReport scores accounts, reorders reach priority, and nudges seed blogs.
func runGrowthReport() error {
	skipChromeRecovery = true
	nativeChromeClicks = false
	rep := buildGrowthReport(enabledAccounts(), time.Now())
	if err := saveGrowthReport(rep); err != nil {
		return err
	}
	n, err := applyGrowthSeedOrder(rep)
	if err != nil {
		fmt.Println("Seed reorder:", err)
	} else if n > 0 {
		fmt.Printf("Updated config/follow.json seed order (%d seeds touched).\n", n)
	}
	printGrowthReport(rep)
	resetReachPool()
	return nil
}

func buildGrowthReport(accs []Account, now time.Time) growthReport {
	rep := growthReport{
		At: now.Format(time.RFC3339),
	}
	rows := make([]growthAccountRow, 0, len(accs))
	for _, a := range accs {
		rows = append(rows, scoreGrowthAccount(a, now))
	}
	sort.SliceStable(rows, func(i, j int) bool {
		ti, tj := tierRank(rows[i].Tier), tierRank(rows[j].Tier)
		if ti != tj {
			return ti < tj
		}
		if rows[i].Gain1d != rows[j].Gain1d {
			return rows[i].Gain1d > rows[j].Gain1d
		}
		if rows[i].Gain7d != rows[j].Gain7d {
			return rows[i].Gain7d > rows[j].Gain7d
		}
		if rows[i].Followers != rows[j].Followers {
			return rows[i].Followers > rows[j].Followers
		}
		return rows[i].ID < rows[j].ID
	})
	rep.Accounts = rows
	for _, r := range rows {
		if r.Tier == growthPark {
			rep.Parked = append(rep.Parked, r.ID)
			continue
		}
		rep.ReachOrder = append(rep.ReachOrder, r.ID)
	}
	stats := seedStatsFromAttribution(now.Add(-7*24*time.Hour), now)
	rep.Seeds.Stats = stats
	rep.Seeds.Promoted, rep.Seeds.Demoted = pickSeedPromoteDemote(stats)
	winners, flat, parked := 0, 0, 0
	gain1 := 0
	for _, r := range rows {
		gain1 += r.Gain1d
		switch r.Tier {
		case growthWinner:
			winners++
		case growthPark:
			parked++
		default:
			flat++
		}
	}
	rep.Summary = fmt.Sprintf("%d accounts: %d winners, %d flat, %d parked; %+d followers (1d across fleet)",
		len(rows), winners, flat, parked, gain1)
	return rep
}

func tierRank(t growthTier) int {
	switch t {
	case growthWinner:
		return 0
	case growthFlat:
		return 1
	default:
		return 2
	}
}

func scoreGrowthAccount(a Account, now time.Time) growthAccountRow {
	row := growthAccountRow{
		ID:         a.ID,
		Terminated: accountIsTerminated(a.ID),
		HasSession: authHasSID(peekAccountAuth(a.ID)),
	}
	log := loadFollowerLog(a.ID)
	blog, fol := bestAccountBlogStats(a.ID, log)
	row.Blog = blog
	row.Followers = fol
	row.Gain1d = followerGainSince(log, blog, now.Add(-24*time.Hour), now)
	row.Gain7d = followerGainSince(log, blog, now.Add(-7*24*time.Hour), now)

	switch {
	case row.Terminated:
		row.Tier = growthPark
		row.Reason = "terminated"
	case !row.HasSession:
		row.Tier = growthPark
		row.Reason = "no session"
	case !a.isEnabled():
		row.Tier = growthPark
		row.Reason = "disabled"
	case row.Gain7d <= growthParkBelowGain7d:
		row.Tier = growthPark
		row.Reason = fmt.Sprintf("losing followers (%+d / 7d)", row.Gain7d)
	case row.Gain1d >= growthWinnerGain1dMin || row.Gain7d >= growthWinnerGain7dMin:
		row.Tier = growthWinner
		row.Reason = fmt.Sprintf("growing (%+d / 1d, %+d / 7d)", row.Gain1d, row.Gain7d)
	default:
		row.Tier = growthFlat
		row.Reason = fmt.Sprintf("flat (%+d / 1d, %+d / 7d)", row.Gain1d, row.Gain7d)
	}
	return row
}

func followerGainSince(log followerLog, blog string, since, now time.Time) int {
	_ = now
	snaps := followerSnapshots(log)
	if len(snaps) == 0 {
		return 0
	}
	countAt := func(s followerSnapshot) (int, bool) {
		if n, ok := snapshotBlogCount(s, blog); ok {
			return n, true
		}
		if blog == "" || normalizeBlogName(s.Primary) == normalizeBlogName(blog) {
			return s.Followers, true
		}
		if blog == "" {
			return s.Followers, true
		}
		return 0, false
	}
	latestN, okLatest := 0, false
	for i := len(snaps) - 1; i >= 0; i-- {
		if n, ok := countAt(snaps[i]); ok {
			latestN, okLatest = n, true
			break
		}
	}
	if !okLatest {
		return 0
	}
	baseN, okBase := 0, false
	for _, s := range snaps {
		n, ok := countAt(s)
		if !ok {
			continue
		}
		at := parseFollowerAt(s.At)
		if at.IsZero() || !at.Before(since) {
			// First snapshot in the window: only use it if we had nothing earlier.
			if !okBase {
				baseN, okBase = n, true
			}
			break
		}
		baseN, okBase = n, true // last snapshot before the window
	}
	if !okBase {
		return 0
	}
	return latestN - baseN
}

func saveGrowthReport(rep growthReport) error {
	if err := os.MkdirAll(filepath.Dir(growthReportFile), 0755); err != nil {
		return err
	}
	raw, err := json.MarshalIndent(rep, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(growthReportFile, append(raw, '\n'), 0644)
}

func loadGrowthReport() (growthReport, bool) {
	raw, err := os.ReadFile(growthReportFile)
	if err != nil {
		return growthReport{}, false
	}
	var rep growthReport
	if json.Unmarshal(raw, &rep) != nil || len(rep.ReachOrder)+len(rep.Parked) == 0 {
		return growthReport{}, false
	}
	return rep, true
}

func printGrowthReport(rep growthReport) {
	fmt.Println("======== growth report ========")
	fmt.Println(rep.Summary)
	fmt.Println("Saved:", growthReportFile)
	show := rep.Accounts
	if len(show) > 15 {
		show = show[:15]
	}
	for _, r := range show {
		blog := r.Blog
		if blog == "" {
			blog = "-"
		}
		fmt.Printf("  %-22s @%-16s %4d fol  %+3d/1d %+3d/7d  %s (%s)\n",
			r.ID, blog, r.Followers, r.Gain1d, r.Gain7d, r.Tier, r.Reason)
	}
	if len(rep.Accounts) > 15 {
		fmt.Printf("  … %d more in %s\n", len(rep.Accounts)-15, growthReportFile)
	}
	if len(rep.Seeds.Promoted) > 0 {
		fmt.Println("Promote seeds:", strings.Join(rep.Seeds.Promoted, ", "))
	}
	if len(rep.Seeds.Demoted) > 0 {
		fmt.Println("Demote seeds:", strings.Join(rep.Seeds.Demoted, ", "))
	}
	fmt.Println("Reach order (first 10):", strings.Join(clipStrings(rep.ReachOrder, 10), ", "))
	if len(rep.Parked) > 0 {
		fmt.Println("Parked:", strings.Join(clipStrings(rep.Parked, 12), ", "))
	}
}

func clipStrings(in []string, n int) []string {
	if len(in) <= n {
		return in
	}
	return in[:n]
}

// orderAccountsForReach keeps highest-follower blogs first (top 20 + next …),
// drops parked/terminated when dropParked, otherwise parks last.
func orderAccountsForReach(accs []Account, dropParked bool) []Account {
	if len(accs) == 0 {
		return accs
	}
	parked := map[string]bool{}
	folFromReport := map[string]int{}
	if rep, ok := loadGrowthReport(); ok && time.Since(parseFollowerAt(rep.At)) <= 36*time.Hour {
		for _, id := range rep.Parked {
			parked[id] = true
		}
		for _, r := range rep.Accounts {
			folFromReport[r.ID] = r.Followers
		}
	}
	fol := map[string]int{}
	for _, a := range accs {
		_, n := bestAccountBlogStats(a.ID, loadFollowerLog(a.ID))
		if n <= 0 {
			n = folFromReport[a.ID]
		}
		fol[a.ID] = n
	}
	var keep, park []Account
	for _, a := range accs {
		if parked[a.ID] || accountIsTerminated(a.ID) {
			if !dropParked {
				park = append(park, a)
			}
			continue
		}
		keep = append(keep, a)
	}
	sortByFollowers := func(list []Account) {
		sort.SliceStable(list, func(i, j int) bool {
			if fol[list[i].ID] != fol[list[j].ID] {
				return fol[list[i].ID] > fol[list[j].ID]
			}
			return list[i].ID < list[j].ID
		})
	}
	sortByFollowers(keep)
	if dropParked {
		return keep
	}
	sortByFollowers(park)
	return append(keep, park...)
}

func recordFollowAttribution(accountID, target, kind, source string) {
	accountID = accountSlug(accountID)
	target = normalizeBlogName(target)
	kind = strings.TrimSpace(strings.ToLower(kind))
	source = strings.TrimSpace(strings.ToLower(source))
	if accountID == "" || target == "" {
		return
	}
	if kind == "" {
		kind = "unknown"
	}
	now := time.Now()
	ev := followAttributionEvent{
		At:      now.Format(time.RFC3339),
		Account: accountID,
		Target:  target,
		Kind:    kind,
		Source:  source,
		Hour:    now.Hour(),
	}
	raw, err := json.Marshal(ev)
	if err != nil {
		return
	}
	_ = os.MkdirAll(filepath.Dir(followAttributionFile), 0755)
	f, err := os.OpenFile(followAttributionFile, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
	if err != nil {
		return
	}
	defer f.Close()
	_, _ = f.Write(append(raw, '\n'))
}

func loadFollowAttributionSince(since time.Time) []followAttributionEvent {
	raw, err := os.ReadFile(followAttributionFile)
	if err != nil {
		return nil
	}
	var out []followAttributionEvent
	for _, line := range strings.Split(string(raw), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		var ev followAttributionEvent
		if json.Unmarshal([]byte(line), &ev) != nil {
			continue
		}
		at := parseFollowerAt(ev.At)
		if !since.IsZero() && at.Before(since) {
			continue
		}
		out = append(out, ev)
	}
	return out
}

func seedStatsFromAttribution(since, now time.Time) []growthSeedStat {
	_ = now
	events := loadFollowAttributionSince(since)
	type agg struct {
		kind string
		n    int
		accs map[string]bool
	}
	by := map[string]*agg{}
	for _, ev := range events {
		if ev.Kind != "seed" && ev.Kind != "tag" {
			continue
		}
		src := strings.TrimSpace(ev.Source)
		if src == "" {
			continue
		}
		key := ev.Kind + ":" + src
		a := by[key]
		if a == nil {
			a = &agg{kind: ev.Kind, accs: map[string]bool{}}
			by[key] = a
		}
		a.n++
		a.accs[ev.Account] = true
	}
	out := make([]growthSeedStat, 0, len(by))
	for key, a := range by {
		src := key
		if i := strings.IndexByte(key, ':'); i >= 0 {
			src = key[i+1:]
		}
		out = append(out, growthSeedStat{
			Source:   src,
			Kind:     a.kind,
			Follows:  a.n,
			Accounts: len(a.accs),
		})
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].Follows != out[j].Follows {
			return out[i].Follows > out[j].Follows
		}
		return out[i].Source < out[j].Source
	})
	return out
}

func pickSeedPromoteDemote(stats []growthSeedStat) (promoted, demoted []string) {
	var seeds []growthSeedStat
	for _, s := range stats {
		if s.Kind == "seed" && s.Source != "" {
			seeds = append(seeds, s)
		}
	}
	for i, s := range seeds {
		if i < 5 && s.Follows > 0 {
			promoted = append(promoted, s.Source)
		}
	}
	// Demote configured seeds with zero attributed follows in the window
	// (only when we have some attribution data at all).
	if len(seeds) == 0 {
		return promoted, demoted
	}
	cfg := loadFollowConfig()
	active := map[string]bool{}
	for _, s := range seeds {
		if s.Follows > 0 {
			active[normalizeBlogName(s.Source)] = true
		}
	}
	for _, seed := range normalizeSeedBlogs(cfg.SeedBlogs) {
		if !active[seed] {
			demoted = append(demoted, seed)
		}
	}
	if len(demoted) > 8 {
		demoted = demoted[:8]
	}
	return promoted, demoted
}

func applyGrowthSeedOrder(rep growthReport) (int, error) {
	cfg := loadFollowConfig()
	seeds := normalizeSeedBlogs(cfg.SeedBlogs)
	if len(seeds) == 0 {
		return 0, nil
	}
	promoted := map[string]bool{}
	for _, s := range rep.Seeds.Promoted {
		promoted[normalizeBlogName(s)] = true
	}
	demoted := map[string]bool{}
	for _, s := range rep.Seeds.Demoted {
		demoted[normalizeBlogName(s)] = true
	}
	if len(promoted) == 0 && len(demoted) == 0 {
		return 0, nil
	}
	var head, mid, tail []string
	seen := map[string]bool{}
	for _, s := range rep.Seeds.Promoted {
		s = normalizeBlogName(s)
		if s == "" || seen[s] {
			continue
		}
		// only keep if already in config (don't invent seeds)
		if !containsNormalized(seeds, s) {
			continue
		}
		seen[s] = true
		head = append(head, s)
	}
	for _, s := range seeds {
		if seen[s] || demoted[s] {
			continue
		}
		seen[s] = true
		mid = append(mid, s)
	}
	for _, s := range seeds {
		if !demoted[s] || seen[s] {
			continue
		}
		seen[s] = true
		tail = append(tail, s)
	}
	newSeeds := append(append(head, mid...), tail...)
	if sameStringList(seeds, newSeeds) {
		return 0, nil
	}
	return len(newSeeds), writeFollowSeedBlogs(newSeeds)
}

func containsNormalized(list []string, want string) bool {
	want = normalizeBlogName(want)
	for _, s := range list {
		if normalizeBlogName(s) == want {
			return true
		}
	}
	return false
}

func sameStringList(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func writeFollowSeedBlogs(seeds []string) error {
	path := "config/follow.json"
	raw, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	var m map[string]any
	if err := json.Unmarshal(raw, &m); err != nil {
		return err
	}
	arr := make([]any, len(seeds))
	for i, s := range seeds {
		arr[i] = s
	}
	m["seedBlogs"] = arr
	out, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, append(out, '\n'), 0644)
}
