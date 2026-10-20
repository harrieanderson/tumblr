package main

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/chromedp/chromedp"
)

const profileSetupFile = "config/profile.json"

// setupCodeStamp is printed in the GUI log so Harrie can see this process
// actually loaded the latest Go. If the log does not show it, an old GUI is
// still bound to port 8787.
const setupCodeStamp = "HARRIE-SETUP-21k"

var (
	setupUILog      func(string)
	setupSkipCh     = make(chan struct{}, 4)
	setupSkipped    bool
	skipSetupAgeKey bool
)

func setupSay(format string, args ...any) {
	s := fmt.Sprintf(format, args...)
	fmt.Println(s)
	_ = os.Stdout.Sync()
	if setupUILog != nil {
		setupUILog(s)
	}
}

func requestSetupSkip() {
	select {
	case setupSkipCh <- struct{}{}:
	default:
	}
}

type profileSetupConfig struct {
	Title         string `json:"title"`
	Description   string `json:"description"`
	Language      string `json:"language"`
	BirthdayYear  int    `json:"birthdayYear"`
	BirthdayMonth int    `json:"birthdayMonth"`
	BirthdayDay   int    `json:"birthdayDay"`
	Accent        string `json:"accent"`
	Background    string `json:"background"`
}

func loadProfileSetupConfig() profileSetupConfig {
	cfg := profileSetupConfig{
		Title:         "Zoey 🌷",
		Description:   "I'm 25 btw :P · 18+ only Instaaaa: https://www.instagram.com/lil.cosmic.bunny.o7",
		Language:      "en",
		BirthdayYear:  2000,
		BirthdayMonth: 12,
		BirthdayDay:   29,
		Accent:        "#000000",
		Background:    "#7C3AED",
	}
	raw, err := os.ReadFile(profileSetupFile)
	if err != nil {
		return cfg
	}
	_ = json.Unmarshal(raw, &cfg)
	if strings.TrimSpace(cfg.Title) == "" {
		cfg.Title = "Zoey 🌷"
	}
	if strings.TrimSpace(cfg.Description) == "" {
		cfg.Description = "I'm 25 btw :P · 18+ only Instaaaa: https://www.instagram.com/lil.cosmic.bunny.o7"
	}
	if strings.TrimSpace(cfg.Language) == "" {
		cfg.Language = "en"
	}
	if cfg.BirthdayYear <= 0 {
		cfg.BirthdayYear = 2000
	}
	if cfg.BirthdayMonth <= 0 {
		cfg.BirthdayMonth = 12
	}
	if cfg.BirthdayDay <= 0 {
		cfg.BirthdayDay = 29
	}
	if strings.TrimSpace(cfg.Accent) == "" {
		cfg.Accent = "#000000"
	}
	if strings.TrimSpace(cfg.Background) == "" {
		cfg.Background = "#7C3AED"
	}
	return cfg
}

func usableBlogName(name string) bool {
	name = strings.TrimSpace(name)
	if name == "" || strings.ContainsAny(name, ":/") {
		return false
	}
	if strings.EqualFold(name, "YOUR_BLOG_NAME") {
		return false
	}
	upper, lower := false, false
	for _, r := range name {
		switch {
		case r >= 'A' && r <= 'Z':
			upper = true
		case r >= 'a' && r <= 'z':
			lower = true
		}
	}
	// Tumblr UUIDs are long mixed-case tokens, with or without a hyphen.
	if len(name) >= 20 && upper && lower {
		return false
	}
	return true
}

var setupBlogName string
var ageKeyHaveClicked bool
var setupBirthdayApplied bool
var setupAgeVerified bool

type setupLookStatus struct {
	Title    string
	Bio      string
	Avatar   string
	TitleOK  bool
	BioOK    bool
	AvatarOK bool
}

func (s setupLookStatus) OK() bool {
	return s.TitleOK && s.BioOK
}

func (s setupLookStatus) Complete() bool {
	return s.OK() && s.AvatarOK
}

type setupLookCheck struct {
	TitleOK      bool
	BioOK        bool
	AvatarOK     bool
	BannerOK     bool
	UsernameOK   bool
	AccentOK     bool
	BackgroundOK bool
}

func (c setupLookCheck) essentialDone() bool {
	// Banner is best-effort: some blogs never expose "Edit blog header image".
	return c.TitleOK && c.BioOK && c.AvatarOK && c.UsernameOK && c.AccentOK && c.BackgroundOK
}

func (c setupLookCheck) Done() bool {
	return c.essentialDone() && c.BannerOK
}

func (c setupLookCheck) report() string {
	return fmt.Sprintf("title=%s bio=%s avatar=%s banner=%s username=%s colors=%s",
		setupFlag(c.TitleOK), setupFlag(c.BioOK), setupFlag(c.AvatarOK),
		setupFlag(c.BannerOK), setupFlag(c.UsernameOK),
		setupFlag(c.AccentOK && c.BackgroundOK))
}

func wantedTitleSet(title string) bool {
	return strings.Contains(strings.ToLower(title), "zoey")
}

func wantedBioSet(desc string) bool {
	d := strings.ToLower(desc)
	hasIG := strings.Contains(d, "lil.cosmic") || strings.Contains(d, "instagram.com/lil")
	return hasIG && strings.Contains(d, "25 btw")
}

func avatarLooksSet(url string) bool {
	u := strings.ToLower(strings.TrimSpace(url))
	if u == "" {
		return false
	}
	if strings.Contains(u, "default_avatar") || strings.Contains(u, "default_avatars") {
		return false
	}
	if strings.Contains(u, "assets.tumblr.com") {
		return false
	}
	return true
}

type zoeyLookRow struct {
	ID           string `json:"id"`
	Blog         string `json:"blog"`
	Status       string `json:"status"` // full | essential | partial | need | skip
	UsernameOK   bool   `json:"usernameOK"`
	TitleOK      bool   `json:"titleOK"`
	BioOK        bool   `json:"bioOK"`
	AvatarOK     bool   `json:"avatarOK"`
	BannerOK     bool   `json:"bannerOK"`
	AccentOK     bool   `json:"accentOK"`
	BackgroundOK bool   `json:"backgroundOK"`
	Detail       string `json:"detail"`
	Followers    int    `json:"followers,omitempty"`
}

func runCheckZoeySetup() error {
	skipChromeRecovery = true
	nativeChromeClicks = false
	cfg := loadProfileSetupConfig()
	accs := enabledAccounts()
	if want := strings.TrimSpace(os.Getenv("TUMBLR_CHECK_IDS")); want != "" {
		allow := map[string]bool{}
		for _, id := range strings.Split(want, ",") {
			id = accountSlug(strings.TrimSpace(id))
			if id != "" {
				allow[id] = true
			}
		}
		var filtered []Account
		for _, a := range accs {
			if allow[a.ID] {
				filtered = append(filtered, a)
				delete(allow, a.ID)
			}
		}
		for id := range allow {
			if a, ok := findAccount(id); ok {
				filtered = append(filtered, a)
			} else {
				fmt.Println("Skip unknown account:", id)
			}
		}
		accs = filtered
	}
	if len(accs) == 0 {
		return fmt.Errorf("no enabled accounts")
	}
	fmt.Printf("Checking Zoey look (username/title/bio/pic/banner/purple) on %d account(s)…\n", len(accs))

	var rows []zoeyLookRow
	counts := map[string]int{}
	var needIDs []string

	for i, a := range accs {
		r := checkAccountZoeyLook(a, cfg)
		rows = append(rows, r)
		counts[r.Status]++
		if r.Status == "need" || r.Status == "partial" || r.Status == "essential" {
			needIDs = append(needIDs, a.ID)
		}
		fmt.Printf("%3d/%d  %-24s  %-9s  %s\n", i+1, len(accs), a.ID, strings.ToUpper(r.Status), r.Detail)
		time.Sleep(80 * time.Millisecond)
	}

	full := make([]map[string]any, 0)
	partial := make([]map[string]any, 0)
	for _, r := range rows {
		item := map[string]any{"id": r.ID, "blog": r.Blog, "followers": r.Followers, "detail": r.Detail}
		switch r.Status {
		case "full":
			full = append(full, item)
		case "essential", "partial":
			partial = append(partial, map[string]any{
				"id": r.ID, "blog": r.Blog, "status": r.Status, "followers": r.Followers,
				"username": r.UsernameOK, "title": r.TitleOK, "bio": r.BioOK, "avatar": r.AvatarOK,
				"banner": r.BannerOK, "accent": r.AccentOK, "background": r.BackgroundOK, "detail": r.Detail,
			})
		}
	}

	report := map[string]any{
		"at":       time.Now().Format(time.RFC3339),
		"counts":   counts,
		"full":     full,
		"partial":  partial,
		"need":     needIDs,
		"accounts": rows,
	}
	raw, err := json.MarshalIndent(report, "", "  ")
	if err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join("config", "zoey-setup-status.json"), append(raw, '\n'), 0644); err != nil {
		return err
	}
	fullOnly := map[string]any{"at": time.Now().Format(time.RFC3339), "count": len(full), "accounts": full}
	if rawFull, err := json.MarshalIndent(fullOnly, "", "  "); err == nil {
		_ = os.WriteFile(filepath.Join("config", "zoey-fully-setup.json"), append(rawFull, '\n'), 0644)
	}

	fmt.Println()
	fmt.Printf("full=%d  essential(no banner)=%d  partial=%d  need=%d  skip=%d\n",
		counts["full"], counts["essential"], counts["partial"], counts["need"], counts["skip"])
	fmt.Println("Wrote config/zoey-setup-status.json and config/zoey-fully-setup.json")
	return nil
}

func checkAccountZoeyLook(a Account, cfg profileSetupConfig) zoeyLookRow {
	out := zoeyLookRow{ID: a.ID, Status: "skip"}
	path := filepath.Join(profilesAuthDir, a.ID, "auth.json")
	auth, err := loadAuthFrom(path)
	if err != nil {
		out.Detail = "no auth file"
		return out
	}
	client := newHumanClient()
	var name string
	if authHasSID(auth) {
		if err := refreshCSRF(client, &auth); err != nil {
			_ = err
		} else if blogs, err := listAccountBlogsDetailed(client, auth); err == nil {
			for _, b := range blogs {
				if b.Primary {
					if usableBlogName(b.Name) {
						name = b.Name
					} else if usableBlogName(b.UUID) {
						name = b.UUID
					}
					break
				}
			}
			if name == "" {
				for _, b := range blogs {
					if usableBlogName(b.Name) {
						name = b.Name
						break
					}
				}
			}
		}
	}
	if name == "" && usableBlogName(auth.Blog) {
		name = auth.Blog
	}
	if name == "" {
		if n := strings.TrimSpace(loadFollowerLog(a.ID).Latest.Primary); usableBlogName(n) {
			name = n
		}
	}
	if name == "" {
		if !authHasSID(auth) {
			out.Detail = "no cookies / no blog name"
		} else {
			out.Detail = "session dead / no blog name"
		}
		return out
	}
	out.Blog = name
	p, err := fetchBlogProfile(client, auth, name)
	if err != nil && strings.HasPrefix(name, "t:") && blogConfigured(auth.Blog) {
		name = auth.Blog
		out.Blog = name
		p, err = fetchBlogProfile(client, auth, name)
	}
	if err != nil {
		out.Detail = "@" + name + " fetch-failed"
		return out
	}
	theme := savedThemeStatus(p, cfg)
	out.UsernameOK = usernameAlreadyOnBrand(name)
	out.TitleOK = wantedTitleSet(p.Title)
	out.BioOK = wantedBioSet(p.Description)
	out.AvatarOK = avatarLooksSet(p.Avatar)
	out.BannerOK = theme.BannerOK
	out.AccentOK = theme.AccentOK
	out.BackgroundOK = theme.BackgroundOK
	out.Followers = p.Followers
	if fl := loadFollowerLog(a.ID).Latest.Followers; fl > out.Followers {
		out.Followers = fl
	}

	c := setupLookCheck{
		TitleOK: out.TitleOK, BioOK: out.BioOK, AvatarOK: out.AvatarOK,
		BannerOK: out.BannerOK, UsernameOK: out.UsernameOK,
		AccentOK: out.AccentOK, BackgroundOK: out.BackgroundOK,
	}
	switch {
	case c.Done():
		out.Status = "full"
	case c.essentialDone():
		out.Status = "essential"
	case out.UsernameOK || out.TitleOK || out.BioOK || out.AvatarOK || out.BannerOK || out.AccentOK || out.BackgroundOK:
		out.Status = "partial"
	default:
		out.Status = "need"
	}
	out.Detail = fmt.Sprintf("@%s user=%s title=%s bio=%s pic=%s banner=%s purple=%s accent=%s",
		name,
		setupFlag(out.UsernameOK), setupFlag(out.TitleOK), setupFlag(out.BioOK),
		setupFlag(out.AvatarOK), setupFlag(out.BannerOK),
		setupFlag(out.BackgroundOK), setupFlag(out.AccentOK))
	return out
}

func accountHasZoeyLook(a Account) (bool, string) {
	r := checkAccountZoeyLook(a, loadProfileSetupConfig())
	ok := r.Status == "full" || r.Status == "essential"
	return ok, r.Detail
}

func lookStatusFromAPI(blog string) setupLookStatus {
	blog = strings.TrimSpace(blog)
	if blog == "" {
		return setupLookStatus{}
	}
	auth, err := loadAuth()
	if err != nil {
		return setupLookStatus{}
	}
	p, err := fetchBlogProfile(newHumanClient(), auth, blog)
	if err != nil {
		return setupLookStatus{}
	}
	return setupLookStatus{
		Title:    p.Title,
		Bio:      p.Description,
		Avatar:   p.Avatar,
		TitleOK:  wantedTitleSet(p.Title),
		BioOK:    wantedBioSet(p.Description),
		AvatarOK: avatarLooksSet(p.Avatar),
	}
}

func mergeLookStatus(api, page setupLookStatus) setupLookStatus {
	if page.TitleOK {
		api.TitleOK = true
		if strings.TrimSpace(api.Title) == "" {
			api.Title = page.Title
		}
	}
	if page.BioOK {
		api.BioOK = true
		if strings.TrimSpace(api.Bio) == "" {
			api.Bio = page.Bio
		}
	}
	if page.AvatarOK {
		api.AvatarOK = true
		if strings.TrimSpace(api.Avatar) == "" {
			api.Avatar = page.Avatar
		}
	}
	return api
}

func currentLookStatus(blog string) setupLookStatus {
	return mergeLookStatus(lookStatusFromAPI(blog), lookStatusFromPage())
}

type setupChecklist struct {
	TitleOK    bool
	BioOK      bool
	AvatarOK   bool
	BirthdayOK bool
	CookiesOK  bool
	AgeOK      bool
}

func (c setupChecklist) Ready() bool {
	return c.TitleOK && c.BioOK && c.AvatarOK && c.BirthdayOK && c.CookiesOK && c.AgeOK
}

func sessionBirthdayOK(st setupPageStatus) bool {
	return st.Birthday == "set" || st.Birthday == "locked" || setupBirthdayApplied
}

func sessionAgeOK(st setupPageStatus) bool {
	return st.Verified || setupAgeVerified
}

func setupFlag(ok bool) string {
	if ok {
		return "ok"
	}
	return "no"
}

func ageKeyHandsOff() bool {
	return findWindowsSecurityHWND() != 0
}

const chromeReadBlogNameJS = `(() => {
	const skip = /^(www|settings|dashboard|explore|login|register|blog|search|inbox|likes|following|activity|tagged|new|reblog|account|apps)$/i;
	const bad = (n) => !n || n.indexOf(':') >= 0 || skip.test(n);
	const fromPath = (path) => {
		let m = (path || '').match(/^\/settings\/blog\/([^/?#]+)/);
		if (m && !bad(m[1])) return decodeURIComponent(m[1]);
		m = (path || '').match(/^\/blog\/view\/([^/?#]+)/);
		if (m && !bad(m[1])) return decodeURIComponent(m[1]);
		return '';
	};
	try {
		const n = fromPath(location.pathname);
		if (n) return n;
	} catch (e) {}
	for (const a of document.querySelectorAll('a[href*="/settings/blog/"]')) {
		try {
			const u = new URL(a.href, location.origin);
			const n = fromPath(u.pathname);
			if (n) return n;
		} catch (e) {}
	}
	return '';
})()`

func blogNameFromURL(raw string) string {
	u := strings.TrimSpace(raw)
	if i := strings.Index(u, "://"); i >= 0 {
		u = u[i+3:]
		if j := strings.Index(u, "/"); j >= 0 {
			u = u[j:]
		} else {
			return ""
		}
	}
	if i := strings.IndexAny(u, "?#"); i >= 0 {
		u = u[:i]
	}
	for _, prefix := range []string{"/settings/blog/", "/blog/view/"} {
		idx := strings.Index(u, prefix)
		if idx < 0 {
			continue
		}
		rest := u[idx+len(prefix):]
		if j := strings.IndexAny(rest, "/"); j >= 0 {
			rest = rest[:j]
		}
		if usableBlogName(rest) {
			return rest
		}
	}
	return ""
}

func firstTumblrPageTarget() (chromeJSONTarget, error) {
	list, err := listChromeTargets()
	if err != nil {
		return chromeJSONTarget{}, err
	}
	if t := preferredChromePageTarget(list); t.ID != "" {
		return t, nil
	}
	return chromeJSONTarget{}, fmt.Errorf("no chrome page target")
}

func evalBlogNameOnTarget(t chromeJSONTarget) string {
	var name string
	_ = withChromeTargetCDP(t, func(call cdpCall) error {
		raw, err := call("Runtime.evaluate", map[string]any{
			"expression":    chromeReadBlogNameJS,
			"returnByValue": true,
		})
		if err != nil {
			return err
		}
		name = cdpEvalString(raw)
		return nil
	})
	return strings.TrimSpace(name)
}

func discoverBlogNameInChrome() string {
	if list, err := listChromeTargets(); err == nil {
		for _, t := range list {
			if t.Type != "page" {
				continue
			}
			if n := blogNameFromURL(t.URL); usableBlogName(n) {
				return n
			}
		}
	}
	t, err := firstTumblrPageTarget()
	if err != nil {
		return ""
	}
	if n := evalBlogNameOnTarget(t); usableBlogName(n) {
		return n
	}
	opened, err := openOrNavigateTab("https://www.tumblr.com/settings")
	if err != nil {
		return ""
	}
	if n := evalBlogNameOnTarget(opened); usableBlogName(n) {
		return n
	}
	if list, err := listChromeTargets(); err == nil {
		for _, x := range list {
			if n := blogNameFromURL(x.URL); usableBlogName(n) {
				return n
			}
		}
	}
	return ""
}

func resolveSetupBlogName(auth Auth, apiName string) string {
	id := ""
	if acc, ok := currentAccount(); ok {
		id = acc.ID
	}
	return pickSetupBlogName(id, auth, apiName, loadFollowerLog(id))
}

func applySetupLook(cfg profileSetupConfig) {
	if onTumblrLoginPage() {
		recoverSetupLogin()
	}
	if !usableBlogName(setupBlogName) {
		setupSay("No @blog yet — opening blog settings to read the name.")
		if _, err := openOrNavigateTab("https://www.tumblr.com/settings/blog"); err != nil {
			setupSay("Blog settings: %s", err)
		} else {
			time.Sleep(2 * time.Second)
		}
		if n := discoverBlogNameInChrome(); usableBlogName(n) {
			setupBlogName = n
			setupSay("Blog: %s", n)
		} else if auth2, err := loadAuth(); err == nil {
			setupBlogName = resolveSetupBlogName(auth2, "")
		}
	}
	pics, err := listProfilePics()
	if err != nil {
		setupSay("Pics: %s", err)
		return
	}
	pic := picForCurrentAccount(pics)
	if !usableBlogName(setupBlogName) {
		setupSay("Still no @blog name after opening settings — cannot edit appearance yet.")
		return
	}
	const lookTries = 3
	var check setupLookCheck
	// The avatar and banner editors either work first time or Tumblr is
	// blocking them (explicit-flagged blogs, hidden header editor); redoing
	// them on every retry only burns minutes per account.
	avatarTries, themeTries := 0, 0
	for try := 1; try <= lookTries; try++ {
		if try == 1 {
			setupSay("Leaving dashboard — opening blog settings")
		} else {
			setupSay("Look not set yet (%s) — retrying %d/%d", check.report(), try, lookTries)
		}
		name, ok := openVerifiedBlogSettings(setupBlogName)
		if !ok {
			setupSay("Could not open blog settings for this account — skipping the look step.")
			return
		}
		setupBlogName = name
		if try == 1 {
			check = refreshSetupLookCheck(setupBlogName, cfg)
			if check.Done() {
				setupSay("Look already set (%s) — skipping appearance edits.", check.report())
				openFinishedBlogSettings(setupBlogName)
				return
			}
			setupSay("Look needs work: %s", check.report())
		}
		needAvatar := ((!check.TitleOK || !check.BioOK) && avatarTries < 2) || (!check.AvatarOK && avatarTries < 1)
		needTheme := (!check.BannerOK || !check.AccentOK || !check.BackgroundOK) && themeTries < 1
		needUser := !check.UsernameOK
		if !needAvatar && !needTheme && !needUser {
			break
		}
		if needAvatar {
			avatarTries++
			setupSay("Opening profile appearance for @%s", setupBlogName)
			if err := applyBlogLookInChrome(pic, setupBlogName, cfg.Title, cfg.Description); err != nil {
				setupSay("Avatar/title/bio: %s", err)
				if err2 := applyBlogLookOnPort(liveDebugPort); err2 != nil {
					setupSay("Avatar fallback: %s", err2)
				}
			}
		}
		if needTheme || needUser {
			if needTheme {
				themeTries++
			}
			if err := applyBlogLookParts(setupBlogName, cfg, needTheme); err != nil {
				setupSay("Blog look: %s", err)
			}
		}
		if n := discoverBlogNameInChrome(); usableBlogName(n) {
			setupBlogName = n
		}
		time.Sleep(2 * time.Second)
		check = refreshSetupLookCheck(setupBlogName, cfg)
		setupSay("Look check: %s", check.report())
		if check.Done() {
			setupSay("Look confirmed: profile pic, banner, title, bio, and username are set.")
			openFinishedBlogSettings(setupBlogName)
			return
		}
		if check.essentialDone() {
			setupSay("Look confirmed (no banner on this blog — Tumblr hid the header editor): %s", check.report())
			openFinishedBlogSettings(setupBlogName)
			return
		}
		// Only banner missing: don't burn the remaining retries on a header
		// editor Tumblr never shows for this account.
		if check.TitleOK && check.BioOK && check.AvatarOK && check.UsernameOK &&
			check.AccentOK && check.BackgroundOK && !check.BannerOK {
			setupSay("Banner unavailable on this blog — continuing.")
			openFinishedBlogSettings(setupBlogName)
			return
		}
	}
	setupSay("Look still incomplete after retries (%s).", check.report())
}

func openFinishedBlogSettings(blog string) {
	blog = normalizeBlogName(strings.TrimSpace(blog))
	if !usableBlogName(blog) {
		if n := discoverBlogNameInChrome(); usableBlogName(n) {
			blog = n
			setupBlogName = n
		}
	}
	if !usableBlogName(blog) {
		setupSay("No @blog to open for the finished settings page.")
		return
	}
	dest := "https://www.tumblr.com/settings/blog/" + blog
	setupSay("Opening finished blog settings: %s", dest)
	t, err := openOrNavigateTab(dest)
	if err != nil {
		setupSay("Final blog settings: %s", err)
		return
	}
	focusChromeTarget(t)
	time.Sleep(1500 * time.Millisecond)
	dismissOverlaysOnPort()
	if err := applyAnyoneCanMessageOnTarget(t); err != nil {
		setupSay("Open DMs: %s", err)
	}
}

// chromeSetAnyoneCanMessageJS turns OFF "Only allow messages from Tumblrs you follow"
// so strangers can start DMs. Returns a short status string.
const chromeSetAnyoneCanMessageJS = `(() => {
	const vis = (el) => {
		if (!el || !el.getBoundingClientRect) return false;
		const r = el.getBoundingClientRect();
		const s = window.getComputedStyle ? getComputedStyle(el) : {};
		return r.width > 2 && r.height > 2 && s.visibility !== 'hidden' && s.display !== 'none';
	};
	const textOf = (el) => (el.innerText || el.textContent || el.getAttribute('aria-label') || '').replace(/\s+/g, ' ').trim();
	const fire = (el) => {
		if (!el) return;
		el.scrollIntoView({block:'center', inline:'nearest'});
		try { el.focus(); } catch (e) {}
		const opts = {bubbles:true, cancelable:true, view:window, composed:true};
		try { el.dispatchEvent(new PointerEvent('pointerdown', Object.assign({pointerId:1, pointerType:'mouse', isPrimary:true}, opts))); } catch (e) {}
		try { el.dispatchEvent(new MouseEvent('mousedown', opts)); } catch (e) {}
		try { el.dispatchEvent(new PointerEvent('pointerup', Object.assign({pointerId:1, pointerType:'mouse', isPrimary:true}, opts))); } catch (e) {}
		try { el.dispatchEvent(new MouseEvent('mouseup', opts)); } catch (e) {}
		try { el.dispatchEvent(new MouseEvent('click', opts)); } catch (e) {}
		try { el.click(); } catch (e) {}
	};
	const isOn = (el) => {
		if (!el) return false;
		const aria = (el.getAttribute('aria-checked') || el.getAttribute('aria-pressed') || '').toLowerCase();
		if (aria === 'true') return true;
		if (aria === 'false') return false;
		if (el.type === 'checkbox') return !!el.checked;
		return !!(el.className && /checked|selected|is-on|isOn/i.test(String(el.className)));
	};
	const sel = 'button[role="switch"], [role="switch"], input[type=checkbox], [aria-checked], [role="checkbox"], [aria-pressed]';
	const re = /only allow messages from (tumblrs )?you follow/i;
	const reShort = /only allow messages/i;
	let control = null;
	for (const el of document.querySelectorAll(sel)) {
		if (!vis(el)) continue;
		const meta = (el.getAttribute('aria-label') || textOf(el) || '');
		if (re.test(meta) || reShort.test(meta)) { control = el; break; }
	}
	if (!control) {
		for (const lab of document.querySelectorAll('label, span, div, p, h2, h3, h4, dt, strong, legend')) {
			const t = textOf(lab);
			if ((!re.test(t) && !reShort.test(t)) || t.length > 120) continue;
			let n = lab;
			for (let i = 0; i < 8 && n; i++, n = n.parentElement) {
				const kids = [...n.querySelectorAll(sel)].filter(vis);
				if (kids.length === 1) { control = kids[0]; break; }
				const lr = lab.getBoundingClientRect();
				const right = kids.find(el => {
					const r = el.getBoundingClientRect();
					return r.left >= lr.right - 24 && Math.abs((r.top + r.bottom) / 2 - (lr.top + lr.bottom) / 2) < 64;
				});
				if (right) { control = right; break; }
			}
			if (control) break;
		}
	}
	if (!control) return 'missing';
	if (!isOn(control)) return 'already-off';
	fire(control);
	return isOn(control) ? 'clicked-still-on' : 'off';
})()`

func applyAnyoneCanMessageOnTarget(t chromeJSONTarget) error {
	if t.ID == "" {
		return fmt.Errorf("no tab")
	}
	var result string
	for attempt := 0; attempt < 4; attempt++ {
		dismissOverlaysOnPort()
		if err := evalJSONOnChromeTarget(t, chromeSetAnyoneCanMessageJS, &result); err != nil {
			return err
		}
		setupSay("Open DMs (anyone can message): %s", result)
		if result == "already-off" || result == "off" {
			return nil
		}
		if result == "missing" {
			// Messaging section sits lower on /settings/blog/{name} — scroll and retry.
			_ = evalJSONOnChromeTarget(t, `(() => {
				const nodes = [...document.querySelectorAll('h1,h2,h3,h4,legend,div,span,p,strong')];
				const hit = nodes.find(el => /messaging/i.test((el.innerText||el.textContent||'').trim()) && (el.innerText||'').trim().length < 40);
				if (hit) { hit.scrollIntoView({block:'center'}); return 'messaging'; }
				window.scrollBy(0, Math.min(900, document.body.scrollHeight));
				return 'scrolled';
			})()`, &result)
			time.Sleep(800 * time.Millisecond)
			continue
		}
		time.Sleep(900 * time.Millisecond)
	}
	if result == "missing" {
		return fmt.Errorf("messaging toggle not found")
	}
	if result != "already-off" && result != "off" {
		return fmt.Errorf("%s", result)
	}
	return nil
}

func refreshSetupLookCheck(blog string, cfg profileSetupConfig) setupLookCheck {
	if _, ok := appearancePageTarget(); !ok && usableBlogName(blog) {
		if _, err := openOrNavigateTab("https://www.tumblr.com/settings/blog/" + blog); err != nil {
			setupSay("Look check page: %s", err)
		} else {
			time.Sleep(1500 * time.Millisecond)
		}
	}
	return readSetupLookCheck(blog, cfg)
}

func readSetupLookCheck(blog string, cfg profileSetupConfig) setupLookCheck {
	look := currentLookStatus(blog)
	c := setupLookCheck{
		TitleOK:    look.TitleOK,
		BioOK:      look.BioOK,
		AvatarOK:   look.AvatarOK,
		UsernameOK: usernameAlreadyOnBrand(blog),
	}
	t, ok := appearancePageTarget()
	if !ok {
		if page, pageOK := tumblrPageTarget(); pageOK && strings.Contains(page.URL, "/settings/blog/") {
			t, ok = page, true
		}
	}
	if ok {
		st := readBlogLookStatus(t, cfg, "")
		c.BannerOK = st.BannerOK
		c.AccentOK = st.AccentOK
		c.BackgroundOK = st.BackgroundOK
		if st.UserOK {
			c.UsernameOK = true
		}
	}
	if auth, err := loadAuth(); err == nil {
		if p, err := fetchBlogProfile(newHumanClient(), auth, blog); err == nil {
			st := savedThemeStatus(p, cfg)
			c.AccentOK, c.BackgroundOK, c.BannerOK = st.AccentOK, st.BackgroundOK, st.BannerOK
		}
	}
	return c
}

func pickSetupBlogName(id string, auth Auth, apiName string, log followerLog) string {
	if n := strings.TrimSpace(apiName); usableBlogName(n) {
		return n
	}
	// Only trust the follower log when a snapshot actually saw that blog;
	// with no snapshot it falls back to the same rename wishlist below.
	if n, followers := bestAccountBlogStats(id, log); usableBlogName(n) && followers > 0 {
		return n
	}
	if n := strings.TrimSpace(auth.Blog); usableBlogName(n) {
		return n
	}
	if n := discoverBlogNameInChrome(); usableBlogName(n) {
		return n
	}
	// Do not use assigned-usernames.json here — that is the rename wishlist.
	// Opening /settings/blog/<wishlist> bounces away and skips the look step.
	return ""
}

// openVerifiedBlogSettings opens the blog settings page and confirms Tumblr
// kept us there. A name this account does not own redirects away, so the real
// name is read back off the page rather than trusting the one we asked for.
func openVerifiedBlogSettings(name string) (string, bool) {
	for attempt := 0; attempt < 2; attempt++ {
		dest := "https://www.tumblr.com/settings/blog/" + name
		setupSay("Opening %s", dest)
		t, err := openOrNavigateTab(dest)
		if err != nil {
			setupSay("Appearance page: %s", err)
			return name, false
		}
		t = leaveCustomizeTheme(t, name)
		landed := ""
		deadline := time.Now().Add(8 * time.Second)
		for {
			if landed = blogNameFromURL(t.URL); landed != "" {
				break
			}
			if time.Now().After(deadline) {
				break
			}
			time.Sleep(500 * time.Millisecond)
			if list, err := listChromeTargets(); err == nil {
				for _, x := range list {
					if x.ID == t.ID {
						t = x
					}
				}
			}
		}
		if landed != "" {
			if !strings.EqualFold(landed, normalizeBlogName(name)) {
				setupSay("Tumblr opened @%s instead — using that.", landed)
			}
			return landed, true
		}
		setupSay("@%s is not a blog on this account — Tumblr sent us to %s", name, t.URL)
		real := discoverBlogNameInChrome()
		if !usableBlogName(real) {
			if auth2, err := loadAuth(); err == nil {
				if n, err2 := syncAuthIdentity(newHumanClient(), &auth2); err2 == nil && usableBlogName(n) {
					real = n
				} else {
					real = resolveSetupBlogName(auth2, "")
				}
			}
		}
		if !usableBlogName(real) || strings.EqualFold(normalizeBlogName(real), normalizeBlogName(name)) {
			return name, false
		}
		setupSay("Real blog is @%s — retrying.", real)
		name = real
	}
	return name, false
}

func runSetupProfile() error {
	cfg := loadProfileSetupConfig()
	pics, err := listProfilePics()
	if err != nil {
		return err
	}
	pic := picForCurrentAccount(pics)

	prevClicks := nativeChromeClicks
	nativeChromeClicks = false
	defer func() { nativeChromeClicks = prevClicks }()

	auth, err := loadAuth()
	if err != nil {
		return err
	}
	setupSkipped = false
	ageKeyHaveClicked = false
	setupBirthdayApplied = false
	setupAgeVerified = false
	setupSay("Using %s — if this line is missing, the GUI you clicked is an old process. Ctrl+C and run: go run . -gui", setupCodeStamp)
	setupSay("Logging in, then account settings first (English, birthday, AgeKey), then appearance.")
	cmd, err := startChromeLoggedInOpts(&auth, false, true)
	if err != nil {
		return err
	}
	if !setupChromeStillOpen() {
		return fmt.Errorf("Chrome closed right after login — the window must stay open for setup")
	}
	_ = cmd
	setupSay("Chrome stays open for setup.")
	defer func() {
		if skipSetupAgeKey {
			setupSay("Closing Chrome so the next account can start (AgeKey left for you to do manually).")
			stopChrome(cmd)
			return
		}
		setupSay("Chrome stays open so you can check the profile. Close the window when you're done.")
		for setupChromeStillOpen() {
			select {
			case <-setupSkipCh:
				setupSay("Skip from GUI — close this Chrome window to start the next account.")
			default:
			}
			time.Sleep(time.Second)
		}
	}()

	name := ""
	if strings.TrimSpace(auth.Cookie) != "" {
		n, err := syncAuthIdentity(newHumanClient(), &auth)
		if err != nil || !usableBlogName(n) {
			maybeFillBlog(newHumanClient(), &auth)
			name = resolveSetupBlogName(auth, n)
		} else {
			name = n
		}
	} else {
		name = resolveSetupBlogName(auth, "")
	}
	setupBlogName = name
	if usableBlogName(name) {
		setupSay("Blog: %s", name)
	} else {
		setupSay("Could not detect @blog yet — still running account settings, then look.")
	}
	setupSay("Avatar: %s", filepath.ToSlash(pic))
	setupSay("Title: %s", cfg.Title)

	if onTumblrLoginPage() {
		recoverSetupLogin()
	}
	dismissOverlaysOnPort()
	if !usableBlogName(setupBlogName) {
		if n := discoverBlogNameInChrome(); usableBlogName(n) {
			setupBlogName = n
			setupSay("Blog: %s", n)
		}
	}

	setupSay("1/5 English language")
	if _, err := waitForAccountSettingsPage(); err != nil {
		setupSay("Settings page: %s", err)
	}
	if err := applyEnglishOnPort(); err != nil {
		setupSay("Language: %s", err)
	}

	setupSay("2/5 Birthday (29 Dec 2000)")
	if err := applyBirthdayOnSettings(); err != nil {
		setupSay("Birthday: %s", err)
		setupSay("In Chrome, open Settings → set birthday 29 Dec 2000.")
	} else {
		setupBirthdayApplied = true
	}

	if skipSetupAgeKey {
		setupSay("3/5 Age verification skipped — do AgeKey manually later.")
	} else {
		setupSay("3/5 Age verification (AgeKey). Type the passcode in Windows Security if it appears.")
		if err := applyAgeKeyInChrome(); err != nil {
			setupSay("AgeKey: %s", err)
		} else if st := readSetupPageStatus(); st.Verified {
			setupAgeVerified = true
		}
		if err := waitUntilAgeVerified(); err != nil {
			setupSay("AgeKey wait: %s", err)
		}
		if setupSkipped {
			setupSay("AgeKey skipped from GUI — still doing mature + appearance on this account.")
			setupSkipped = false
		}
		setupSay("AgeKey finished — waiting until Tumblr unlocks Show mature content.")
		if err := waitForMatureContentReady(); err != nil {
			setupSay("Mature unlock wait: %s", err)
		}
	}

	setupSay("4/5 Show mature content, do not blur mature content")
	if err := applyMatureContentInChrome(); err != nil {
		setupSay("Mature content: %s", err)
	}

	setupSay("5/5 Profile picture, banner, colours, username")
	applySetupLook(cfg)

	if cookies, err := cookiesFromDebugPort(); err == nil && cookieValue(cookies, "sid") != "" {
		auth.Cookie = buildTumblrCookieHeader(cookies)
		persistMaturePref(&auth)
		setupSay("Saved session cookies.")
	}
	// Always land on /settings/blog/{name} and turn OFF follower-only DMs.
	if usableBlogName(setupBlogName) {
		openFinishedBlogSettings(setupBlogName)
	}
	return nil
}

const chromePageDumpJS = `(() => {
	const vis = (el) => {
		if (!el) return false;
		const s = getComputedStyle(el);
		const r = el.getBoundingClientRect();
		return s.display !== 'none' && s.visibility !== 'hidden' && r.width > 1 && r.height > 1;
	};
	const textOf = (el) => (el.innerText || el.getAttribute('aria-label') || el.getAttribute('placeholder') || '').replace(/\s+/g, ' ').trim().slice(0, 80);
	const uniq = (arr) => [...new Set(arr.filter(Boolean))];
	return JSON.stringify({
		href: location.href,
		headings: uniq([...document.querySelectorAll('h1,h2,h3')].filter(vis).map(textOf)).slice(0, 12),
		buttons: uniq([...document.querySelectorAll('button, a, [role="button"]')].filter(vis).map(textOf).filter(t => t.length > 0 && t.length < 48)).slice(0, 50),
		labels: uniq([...document.querySelectorAll('label')].filter(vis).map(textOf)).slice(0, 20),
		inputs: [...document.querySelectorAll('input, textarea, select, [contenteditable="true"]')].slice(0, 40).map(el => ({
			tag: el.tagName,
			type: el.type || '',
			name: el.name || '',
			aria: el.getAttribute('aria-label') || '',
			role: el.getAttribute('role') || '',
			vis: vis(el)
		})),
		iframes: [...document.querySelectorAll('iframe')].map(f => {
			const s = f.src || '';
			try { const u = new URL(s, location.href); return u.origin + u.pathname; } catch (e) { return 'iframe'; }
		}),
		body: (document.body.innerText || '').replace(/\s+/g, ' ').trim().slice(0, 1200)
	});
})()`

const chromeDismissOverlaysJS = `(() => {
	const vis = (el) => {
		if (!el || !el.getBoundingClientRect) return false;
		const r = el.getBoundingClientRect();
		const s = getComputedStyle(el);
		return r.width > 2 && r.height > 2 && s.visibility !== 'hidden' && s.display !== 'none';
	};
	const textOf = (el) => (el.innerText || el.value || el.getAttribute('aria-label') || '').replace(/\s+/g, ' ').trim();
	const cookieish = (t) => /cookie|cookies|consentimiento|consent|privacy|privacidad|gdpr|we use cookies|we value your privacy|this (site|website) uses|accept all|i agree|qc-cmp|onetrust|didomi/i.test(t);
	const cookieBtn = /^(i agree!?|agree|i accept|acepto!?|aceptar( todas( las cookies)?)?|accetto|j'accepte|согласен!?|accept( all( cookies)?)?|allow all( cookies)?|permitir todo(s)?|got it|continue|continuar|confirm(ar)?|ok|okay)$/i;
	const cookieSkip = /manage|settings|reject|decline|necessary|customize|customise|learn more|more info|cookie settings|do not sell/i;
	const okBtn = /^(ok|okay|got it|dismiss)$/i;
	const leakish = (t) => /data breach|password manager|change your password|compromised password|found in a data|check your passwords|check passwords|leaked password|unsafe password|password was found|passwords were found|password leak|google found/i.test(t);
	const buttons = [];
	const visit = (root) => {
		if (!root || !root.querySelectorAll) return;
		for (const el of root.querySelectorAll('button, a, [role="button"], input[type=button], input[type=submit]')) {
			if (vis(el)) buttons.push(el);
		}
		for (const el of root.querySelectorAll('*')) {
			if (el.shadowRoot) visit(el.shadowRoot);
		}
	};
	visit(document);
	for (const f of document.querySelectorAll('iframe')) {
		try { if (f.contentDocument) visit(f.contentDocument); } catch (e) {}
	}
	const ctxOf = (el) => {
		let n = el, t = '';
		for (let i = 0; i < 8 && n; i++, n = n.parentElement) t += ' ' + textOf(n).slice(0, 240);
		return t;
	};
	const savePwBtn = /^(never|no,? thanks!?|not now)$/i;
	const saveish = (t) => /save password|google password manager|passwords are saved/i.test(t);
	const savePage = (document.body && document.body.innerText) || '';
	if (/current password/i.test(savePage) && /new password|confirm new password/i.test(savePage)) {
		const cancel = buttons.find(b => /^(cancel|cancelar|annuler|abbrechen)$/i.test(textOf(b)));
		if (cancel) { cancel.click(); return 'password-cancel'; }
	}
	for (const b of buttons) {
		const t = textOf(b);
		if (!savePwBtn.test(t) || t.length > 24) continue;
		if (saveish(ctxOf(b)) || saveish(savePage)) { b.click(); return t; }
	}
	const leakBtn = /^(ok|okay|got it|dismiss|close|not now|never)$/i;
	for (const b of buttons) {
		const t = textOf(b);
		if (!leakBtn.test(t) || t.length > 16) continue;
		if (leakish(ctxOf(b))) { b.click(); return t; }
	}
	const rgbOf = (el) => {
		const m = (getComputedStyle(el).backgroundColor || '').match(/[\d.]+/g);
		if (!m || m.length < 3) return null;
		return {r:+m[0], g:+m[1], b:+m[2], a: m.length > 3 ? +m[3] : 1};
	};
	const purpleFill = (el) => {
		const c = rgbOf(el);
		if (!c || c.a < 0.35) return false;
		return c.b > 90 && c.r > 40 && (c.b - c.g) > 25 && c.g < 170;
	};
	const blazeish = (t) => /blaze/i.test(t) && !/tumblr premium|get premium now|ad-free|go premium|early (feature )?access|doubled daily limits|tumblrmart/i.test(t);
	const premiumish = (t) => /tumblr premium|get premium now|ad-free|go premium|early (feature )?access|doubled daily limits|free monthly tumblrmart|billed annually/i.test(t);
	const premiumSkipBtn = /get premium|go premium|yearly|monthly|save \d+%/i;
	const closePremium = (root) => {
		const labeled = [...root.querySelectorAll('button, a, [role="button"], [aria-label]')]
			.find(el => {
				if (!vis(el)) return false;
				const lab = (el.getAttribute('aria-label') || textOf(el)).replace(/\s+/g,' ').trim();
				if (!/^(close|dismiss|×|x)$/i.test(lab)) return false;
				return !premiumSkipBtn.test(textOf(el));
			});
		if (labeled) return labeled;
		const exact = [...root.querySelectorAll('button, a, [role="button"]')]
			.find(el => vis(el) && /^close$/i.test(textOf(el)));
		if (exact) return exact;
		const box = root.getBoundingClientRect ? root.getBoundingClientRect() : {top: 0, right: innerWidth};
		let best = null, bestScore = 1e9;
		for (const el of root.querySelectorAll('button, [role="button"]')) {
			if (!vis(el) || premiumSkipBtn.test(textOf(el))) continue;
			const r = el.getBoundingClientRect();
			if (r.width > 56 || r.height > 56) continue;
			if (r.top < box.top - 8 || r.top > box.top + 90) continue;
			if (r.left < box.right - 100) continue;
			const score = r.width * r.height + (box.right - r.right);
			if (score < bestScore) { best = el; bestScore = score; }
		}
		return best;
	};
	for (const dlg of [...document.querySelectorAll('[role="dialog"], [aria-modal="true"]')].filter(vis)) {
		if (!premiumish(textOf(dlg))) continue;
		const closer = closePremium(dlg);
		if (closer) { closer.click(); return 'premium-close'; }
	}
	const pageText = (document.body && document.body.innerText) || '';
	if (premiumish(pageText) || /\/premium/i.test(location.pathname)) {
		const closer = closePremium(document);
		if (closer) { closer.click(); return 'premium-close'; }
	}
	const blazePrimary = (root) => {
		const btns = [...root.querySelectorAll('button, a, [role="button"]')].filter(el => {
			if (!vis(el)) return false;
			const r = el.getBoundingClientRect();
			const t = textOf(el);
			if (r.width < 72 || r.height < 30 || r.height > 84) return false;
			if (/^×$|^x$|^close$|^dismiss$/i.test(t) || t.length > 80) return false;
			if (premiumSkipBtn.test(t)) return false;
			return true;
		});
		const filled = btns.filter(purpleFill);
		if (filled.length) return filled[0];
		btns.sort((a,b) => {
			const lum = (el) => {
				const c = rgbOf(el);
				if (!c || c.a < 0.35) return 999;
				return 0.3*c.r + 0.59*c.g + 0.11*c.b;
			};
			return lum(a) - lum(b);
		});
		return btns[0] || null;
	};
	for (const dlg of [...document.querySelectorAll('[role="dialog"], [aria-modal="true"]')].filter(vis)) {
		const dt = textOf(dlg);
		if (premiumish(dt) || !blazeish(dt)) continue;
		const btn = blazePrimary(dlg);
		if (btn) { btn.click(); return textOf(btn) || 'blaze-ok'; }
	}
	if (blazeish(pageText) && !premiumish(pageText)) {
		const btn = blazePrimary(document);
		if (btn && blazeish(ctxOf(btn))) { btn.click(); return textOf(btn) || 'blaze-ok'; }
	}
	for (const el of document.querySelectorAll('[aria-label]')) {
		if (!vis(el)) continue;
		const t = (el.getAttribute('aria-label') || '').replace(/\s+/g, ' ').trim();
		if (!/^(close|dismiss)$/i.test(t)) continue;
		if (premiumish(ctxOf(el))) { el.click(); return 'premium-close'; }
	}
	for (const el of document.querySelectorAll('[aria-label]')) {
		if (!vis(el)) continue;
		const t = (el.getAttribute('aria-label') || '').replace(/\s+/g, ' ').trim();
		if (!/^(close|dismiss|ok|okay|got it)$/i.test(t)) continue;
		if (leakish(ctxOf(el))) { el.click(); return t; }
	}
	for (const b of buttons) {
		const t = textOf(b);
		if (!t || t.length > 48 || cookieSkip.test(t) || !cookieBtn.test(t)) continue;
		const ctx = ctxOf(b) + ' ' + t;
		if (/agekey|verify your age|create my agekey|use your selfie|windows security|tumblr premium|get premium now|blaze/i.test(ctx)) continue;
		if (cookieish(ctx)) { b.click(); return t; }
	}
	for (const b of buttons) {
		const t = textOf(b);
		if (!okBtn.test(t)) continue;
		const dlg = b.closest('[role="dialog"], [aria-modal="true"]');
		if (!dlg) continue;
		const dt = textOf(dlg);
		if (/agekey|verify your age|create my agekey|use your selfie|tumblr premium|get premium now|blaze/i.test(dt)) continue;
		b.click();
		return t;
	}
	return '';
})()`

const chromeClickCookieJS = `(() => {
	const vis = (el) => {
		if (!el || !el.getBoundingClientRect) return false;
		const r = el.getBoundingClientRect();
		const s = getComputedStyle(el);
		return r.width > 2 && r.height > 2 && s.visibility !== 'hidden' && s.display !== 'none';
	};
	const textOf = (el) => (el.innerText || el.value || el.getAttribute('aria-label') || '').replace(/\s+/g, ' ').trim();
	const cookieish = (t) => /cookie|cookies|consentimiento|consent|privacy|privacidad|gdpr|we use cookies|we value your privacy|this (site|website) uses|accept all|i agree|qc-cmp|onetrust|didomi/i.test(t);
	const cookieBtn = /^(i agree!?|agree|i accept|acepto!?|aceptar( todas( las cookies)?)?|accetto|j'accepte|согласен!?|accept( all( cookies)?)?|allow all( cookies)?|permitir todo(s)?|got it|continue|continuar|confirm(ar)?|ok|okay)$/i;
	const cookieSkip = /manage|settings|reject|decline|necessary|customize|customise|learn more|more info|cookie settings|do not sell|agekey|selfie/i;
	const buttons = [];
	const visit = (root) => {
		if (!root || !root.querySelectorAll) return;
		for (const el of root.querySelectorAll('button, a, [role="button"], input[type=button], input[type=submit]')) {
			if (vis(el)) buttons.push(el);
		}
		for (const el of root.querySelectorAll('*')) {
			if (el.shadowRoot) visit(el.shadowRoot);
		}
	};
	visit(document);
	for (const f of document.querySelectorAll('iframe')) {
		try { if (f.contentDocument) visit(f.contentDocument); } catch (e) {}
	}
	const ctxOf = (el) => {
		let n = el, t = '';
		for (let i = 0; i < 8 && n; i++, n = n.parentElement) t += ' ' + textOf(n).slice(0, 240);
		return t;
	};
	const page = (document.body && document.body.innerText || '').replace(/\s+/g, ' ');
	for (const b of buttons) {
		const t = textOf(b);
		if (!t || t.length > 48 || cookieSkip.test(t) || !cookieBtn.test(t)) continue;
		const ctx = ctxOf(b) + ' ' + t + ' ' + page.slice(0, 400);
		if (/agekey|verify your age|create my agekey|use your selfie/i.test(ctx)) continue;
		if (cookieish(ctx) || cookieish(page.slice(0, 800))) {
			b.click();
			return t;
		}
	}
	return '';
})()`

const chromeClickPrivacyConsentJS = `(() => {
	const textOf = (el) => (el.innerText || el.textContent || el.value || el.getAttribute('aria-label') || '').replace(/\s+/g, ' ').trim();
	const skip = /learn more|decline|reject|manage|settings|necessary only|non-essential/i;
	const exact = /^\s*i agree!?\s*$/i;
	const loose = /i agree/i;
	const find = (doc, ox, oy) => {
		if (!doc || !doc.querySelectorAll) return null;
		const nodes = [...doc.querySelectorAll('button, a, [role="button"], input[type=button], input[type=submit]')];
		let el = nodes.find(e => exact.test(textOf(e)));
		if (!el) el = nodes.find(e => loose.test(textOf(e)) && !skip.test(textOf(e)));
		if (!el) {
			for (const n of doc.querySelectorAll('span, div, p')) {
				const t = textOf(n);
				if (!exact.test(t) || t.length > 16) continue;
				el = n.closest('button, a, [role="button"]') || n;
				break;
			}
		}
		if (el) {
			try { el.scrollIntoView({ block: 'center', inline: 'center' }); } catch (e) {}
			const btn = el.closest('button, a, [role="button"], input[type=submit]') || el;
			try { btn.click(); } catch (e) {}
			const r = btn.getBoundingClientRect();
			return { found: true, text: textOf(btn) || textOf(el), x: ox + r.left + r.width / 2, y: oy + r.top + r.height / 2 };
		}
		for (const f of doc.querySelectorAll('iframe')) {
			const fr = f.getBoundingClientRect();
			try {
				const hit = find(f.contentDocument, ox + fr.left, oy + fr.top);
				if (hit) return hit;
			} catch (e) {}
		}
		return null;
	};
	return find(document, 0, 0) || { found: false, text: '', x: 0, y: 0 };
})()`

const chromeSaveAppearanceJS = `(() => {
	const vis = (el) => el && el.getBoundingClientRect().width > 2 && el.getBoundingClientRect().height > 2;
	const textOf = (el) => (el.innerText || el.getAttribute('aria-label') || '').replace(/\s+/g, ' ').trim();
	const skip = /password|agekey|verify age|cookie|consent|never|not now/i;
	const labels = [];
	const clickRe = (re) => {
		const el = [...document.querySelectorAll('button, [role="button"]')].find(b => vis(b) && re.test(textOf(b)) && !skip.test(textOf(b)));
		if (!el) return;
		el.click();
		labels.push(textOf(el));
	};
	clickRe(/^(apply|use this|use photo|set avatar|done)$/i);
	clickRe(/^(save|save changes)$/i);
	return labels.join(' | ');
})()`

const chromeAppearanceErrorJS = `(() => {
	const vis = (el) => {
		if (!el) return false;
		const r = el.getBoundingClientRect();
		return r.width > 2 && r.height > 2;
	};
	const texts = [...document.querySelectorAll('[role="alert"], [class*="toast"], [class*="snack"], [class*="error"], [class*="banner"]')]
		.filter(vis)
		.map(el => (el.innerText || '').replace(/\s+/g, ' ').trim())
		.filter(Boolean);
	const hit = texts.find(t => /something went wrong|please try again|couldn.?t (save|upload)|failed to (save|upload)/i.test(t));
	if (hit) return hit.slice(0, 120);
	const body = ((document.body && document.body.innerText) || '').replace(/\s+/g, ' ');
	const m = body.match(/something went wrong.? please try again/i);
	return m ? m[0] : '';
})()`

const chromeMatureToggleStatusJS = `(() => {
	const vis = (el) => {
		if (!el || !el.getBoundingClientRect) return false;
		const r = el.getBoundingClientRect();
		const s = window.getComputedStyle ? getComputedStyle(el) : {};
		return r.width > 2 && r.height > 2 && s.visibility !== 'hidden' && s.display !== 'none';
	};
	const textOf = (el) => (el.innerText || el.textContent || el.getAttribute('aria-label') || el.getAttribute('aria-labelledby') || '').replace(/\s+/g, ' ').trim();
	const isOn = (el) => {
		if (!el) return false;
		const aria = (el.getAttribute('aria-checked') || el.getAttribute('aria-pressed') || '').toLowerCase();
		if (aria === 'true') return true;
		if (aria === 'false') return false;
		if (el.type === 'checkbox') return !!el.checked;
		return !!(el.className && /checked|selected|is-on|isOn/i.test(el.className));
	};
	const sel = 'button[role="switch"], [role="switch"], input[type=checkbox], [aria-checked], [role="checkbox"], [aria-pressed]';
	const controlFor = (re) => {
		const labelled = [...document.querySelectorAll(sel)].find(el => {
			if (!vis(el)) return false;
			const meta = (el.getAttribute('aria-label') || textOf(el) || '').toLowerCase();
			if (/two-factor|2fa|password|email|language|birthday|passkey|age.?key|verify age/i.test(meta)) return false;
			return re.test(el.getAttribute('aria-label') || textOf(el));
		});
		if (labelled) return labelled;
		for (const lab of document.querySelectorAll('label, span, div, p, h2, h3, h4, dt, td, strong, legend, [id]')) {
			const t = textOf(lab);
			if (!re.test(t) || t.length > 80) continue;
			if (/two-factor|2fa|password|email|language|birthday|passkey|age.?key|verify age/i.test(t.toLowerCase())) continue;
			let n = lab;
			for (let i = 0; i < 8 && n; i++, n = n.parentElement) {
				const kids = [...n.querySelectorAll(sel)].filter(vis);
				if (kids.length === 1) return kids[0];
				const lr = lab.getBoundingClientRect();
				const right = kids.find(el => {
					const r = el.getBoundingClientRect();
					return r.left >= lr.right - 24 && Math.abs((r.top + r.bottom) / 2 - (lr.top + lr.bottom) / 2) < 56;
				});
				if (right) return right;
			}
		}
		return null;
	};
	const show = controlFor(/show mature content/i) ||
		controlFor(/^show mature/i) ||
		controlFor(/show (nsfw|explicit)/i) ||
		controlFor(/contenido (para adultos|maduro|sensible)/i);
	if (!show) return JSON.stringify({state:'missing', on:false});
	if (show.disabled || show.getAttribute('aria-disabled') === 'true') {
		return JSON.stringify({state:'disabled', on:false});
	}
	return JSON.stringify({state:'ready', on:!!isOn(show)});
})()`

const chromeSetMatureTogglesJS = `(() => {
	const vis = (el) => {
		if (!el || !el.getBoundingClientRect) return false;
		const r = el.getBoundingClientRect();
		const s = window.getComputedStyle ? getComputedStyle(el) : {};
		return r.width > 2 && r.height > 2 && s.visibility !== 'hidden' && s.display !== 'none';
	};
	const textOf = (el) => (el.innerText || el.textContent || el.getAttribute('aria-label') || el.getAttribute('aria-labelledby') || '').replace(/\s+/g, ' ').trim();
	const fire = (el) => {
		if (!el) return;
		el.scrollIntoView({block:'center', inline:'nearest'});
		try { el.focus(); } catch (e) {}
		const opts = {bubbles:true, cancelable:true, view:window, composed:true};
		try { el.dispatchEvent(new PointerEvent('pointerdown', Object.assign({pointerId:1, pointerType:'mouse', isPrimary:true}, opts))); } catch (e) {}
		try { el.dispatchEvent(new MouseEvent('mousedown', opts)); } catch (e) {}
		try { el.dispatchEvent(new PointerEvent('pointerup', Object.assign({pointerId:1, pointerType:'mouse', isPrimary:true}, opts))); } catch (e) {}
		try { el.dispatchEvent(new MouseEvent('mouseup', opts)); } catch (e) {}
		try { el.dispatchEvent(new MouseEvent('click', opts)); } catch (e) {}
		try { el.click(); } catch (e) {}
	};
	const isOn = (el) => {
		if (!el) return false;
		const aria = (el.getAttribute('aria-checked') || el.getAttribute('aria-pressed') || '').toLowerCase();
		if (aria === 'true') return true;
		if (aria === 'false') return false;
		if (el.type === 'checkbox') return !!el.checked;
		return !!(el.className && /checked|selected|is-on|isOn/i.test(el.className));
	};
	const sel = 'button[role="switch"], [role="switch"], input[type=checkbox], [aria-checked], [role="checkbox"], [aria-pressed]';
	const controlFor = (re) => {
		const labelled = [...document.querySelectorAll(sel)].find(el => {
			if (!vis(el)) return false;
			const meta = (el.getAttribute('aria-label') || textOf(el) || '').toLowerCase();
			if (/two-factor|2fa|password|email|language|birthday|passkey|age.?key|verify age/i.test(meta)) return false;
			return re.test(el.getAttribute('aria-label') || textOf(el));
		});
		if (labelled) return labelled;
		for (const lab of document.querySelectorAll('label, span, div, p, h2, h3, h4, dt, td, strong, legend, [id]')) {
			const t = textOf(lab);
			if (!re.test(t) || t.length > 80) continue;
			if (/two-factor|2fa|password|email|language|birthday|passkey|age.?key|verify age/i.test(t.toLowerCase())) continue;
			let n = lab;
			for (let i = 0; i < 8 && n; i++, n = n.parentElement) {
				const kids = [...n.querySelectorAll(sel)].filter(vis);
				if (kids.length === 1) return kids[0];
				const lr = lab.getBoundingClientRect();
				const right = kids.find(el => {
					const r = el.getBoundingClientRect();
					return r.left >= lr.right - 24 && Math.abs((r.top + r.bottom) / 2 - (lr.top + lr.bottom) / 2) < 56;
				});
				if (right) return right;
			}
		}
		return null;
	};
	const heading = [...document.querySelectorAll('h1,h2,h3,h4,legend,span,div')].find(el => vis(el) && /content you see|mature content|filtering/i.test(textOf(el)) && textOf(el).length < 60);
	if (heading) heading.scrollIntoView({block:'center'});
	const setWant = (el, wantOn, name) => {
		if (!el) return name + ':missing';
		const meta = (el.getAttribute('aria-label') || textOf(el) || '').toLowerCase();
		if (/two-factor|2fa|password|email|language|birthday|passkey|age.?key|verify age/i.test(meta)) {
			return name + ':missing';
		}
		if (el.disabled || el.getAttribute('aria-disabled') === 'true') return name + ':disabled';
		if (isOn(el) === wantOn) return name + ':' + (wantOn ? 'on' : 'off');
		fire(el);
		if (el.type === 'checkbox') {
			try { el.checked = wantOn; el.dispatchEvent(new Event('input', {bubbles:true})); el.dispatchEvent(new Event('change', {bubbles:true})); } catch (e) {}
		}
		return name + ':' + (isOn(el) === wantOn ? (wantOn ? 'on' : 'off') : 'clicked');
	};
	const show = setWant(
		controlFor(/show mature content/i) ||
		controlFor(/^show mature/i) ||
		controlFor(/show (nsfw|explicit)/i) ||
		controlFor(/contenido (para adultos|maduro|sensible)/i),
		true, 'show');
	const blur = setWant(
		controlFor(/blur mature content/i) ||
		controlFor(/blur sensitive( (images|content))?/i) ||
		controlFor(/blur (nsfw|explicit)/i) ||
		controlFor(/^blur mature/i) ||
		controlFor(/blur images/i),
		false, 'blur');
	try { document.cookie = 'cl_pref=show; path=/; domain=.tumblr.com'; } catch (e) {}
	let extra = '';
	if (/missing|disabled|clicked/.test(show + ' ' + blur)) {
		extra = ' dump:' + JSON.stringify([...document.querySelectorAll(sel)]
			.filter(vis)
			.map(el => ((el.getAttribute('aria-label') || textOf(el)).replace(/\s+/g, ' ').trim().slice(0, 48) + '=' + isOn(el)))
			.slice(0, 16));
	} else {
		const save = [...document.querySelectorAll('button')]
			.find(b => vis(b) && /^(save|save changes)$/i.test(textOf(b)) && !b.disabled && b.getAttribute('aria-disabled') !== 'true');
		if (save) { fire(save); extra = ' saved'; }
	}
	return show + ' ' + blur + extra;
})()`

const chromeClickAgeConfirmJS = `(() => {
	const vis = (el) => {
		if (!el || !el.getBoundingClientRect) return false;
		const r = el.getBoundingClientRect();
		const s = getComputedStyle(el);
		return r.width > 2 && r.height > 2 && s.visibility !== 'hidden' && s.display !== 'none';
	};
	const textOf = (el) => (el.innerText || el.value || el.getAttribute('aria-label') || '').replace(/\s+/g, ' ').trim();
	const btnRe = /^(ok|okay|okey|accept|got it|continue|done|confirm|i understand|allow|yes)$/i;
	const dialogs = [...document.querySelectorAll('[role="dialog"], [aria-modal="true"]')].filter(vis);
	const roots = dialogs.length ? dialogs : [document];
	for (const root of roots) {
		const t = textOf(root);
		if (root !== document && !/birthday|age|agekey|verified|confirm/i.test(t)) continue;
		const btns = [...root.querySelectorAll('button, a, [role="button"], input[type=button], input[type=submit]')]
			.filter(vis)
			.map(el => ({el, t: textOf(el)}))
			.filter(x => x.t && x.t.length < 28 && btnRe.test(x.t) && !/verify age|create my agekey|delete/i.test(x.t));
		if (!btns[0]) continue;
		btns.sort((a,b) => {
			const score = (s) => {
				if (/^(okay|ok|accept|confirm)$/i.test(s)) return 0;
				if (/continue|done|got it/i.test(s)) return 1;
				return 2;
			};
			return score(a.t) - score(b.t) || a.t.length - b.t.length;
		});
		const el = btns[0].el;
		el.scrollIntoView({block:'center', inline:'nearest'});
		const r = el.getBoundingClientRect();
		el.click();
		return {t: btns[0].t, x: r.left + r.width/2, y: r.top + r.height/2};
	}
	return {t:'', x:0, y:0};
})()`

const chromeAgeStatusJS = `(() => {
	const vis = (el) => {
		if (!el) return false;
		const r = el.getBoundingClientRect();
		return r.width > 2 && r.height > 2;
	};
	const textOf = (el) => (el.innerText || el.getAttribute('aria-label') || '').replace(/\s+/g, ' ').trim();
	const b = (document.body && document.body.innerText || '').replace(/\s+/g, ' ');
	const buttons = [...document.querySelectorAll('button, a, [role="button"]')]
		.filter(vis)
		.map(textOf)
		.filter(t => t && t.length < 36);
	const save = [...document.querySelectorAll('button')].find(b => vis(b) && /^(save|guardar|enregistrer|speichern|сохранить)$/i.test((b.innerText || '').trim()));
	const m = b.match(/Birthday.{0,140}/i);
	const bdaySelects = [...document.querySelectorAll('select')].filter(vis).filter(el => /month|day|year|месяц|день|год/i.test((el.getAttribute('aria-label') || '') + ' ' + (el.name || '')));
	const verifyBtn = [...document.querySelectorAll('button, a, [role="button"]')].some(el => vis(el) && /verify (your )?age|подтвердить возраст/i.test(textOf(el)));
	const wanted = /December 29, 2000|29 December 2000|29\s+Dec(?:ember)?\s+2000/i.test(b);
	let birthday = 'unset';
	if (wanted) birthday = 'set';
	else {
		const dateCtl = [...document.querySelectorAll('select, button, [role="combobox"]')].filter(vis).filter(el => /month|day|year|mes|día|año|месяц|день|год/i.test((el.getAttribute('aria-label') || el.innerText || el.name || '')));
		const usable = dateCtl.filter(el => !el.disabled && el.getAttribute('aria-disabled') !== 'true');
		if (dateCtl.length >= 3 && usable.length === 0) birthday = 'locked';
	}
	const cookieish = /cookie|cookies|consentimiento|consent|privacy|privacidad|gdpr|we use cookies|we value your privacy|this (site|website) uses/i;
	const cookieBtnRe = /^(i agree!?|agree|i accept|acepto!?|aceptar( todas( las cookies)?)?|accetto|j'accepte|согласен!?|accept( all( cookies)?)?|allow all( cookies)?|permitir todo(s)?)$/i;
	const cookieBanner = [...document.querySelectorAll('iframe')].some(f => vis(f) && /consent|quantcast|onetrust|didomi|sourcepoint|privacy-mgmt|privacymanager|cookiebot|cmp\.|gdpr|\/privacy/i.test(f.src || f.id || f.className || ''))
		|| !!document.querySelector('#onetrust-banner-sdk, #onetrust-consent-sdk, .qc-cmp2-container, .qc-cmp-ui, [id*="sp_message"], [class*="cookie-banner"], [id*="cookie-banner"]')
		|| (cookieish.test(b) && buttons.some(t => cookieBtnRe.test(t)));
	const cookiesOK = !cookieBanner && /(?:^|; )(euconsent|addtl_consent)/i.test(document.cookie);
	return JSON.stringify({
		href: location.pathname,
		birthday: birthday,
		verified: (
			/Age Group.{0,160}Verified/i.test(b) ||
			/Verified.{0,60}Age Group/i.test(b) ||
			/\bage group\b.{0,80}\b(adult|18\+)/i.test(b) ||
			/successfully verified (your )?age/i.test(b) ||
			/age verification.{0,40}(complete|verified)/i.test(b)
		) && !verifyBtn,
		verifyAge: verifyBtn,
		ageKeyFrame: [...document.querySelectorAll('iframe')].some(f => /k-id|agekey/i.test(f.src || '')),
		verifyPicker: (/verify your age/i.test(b) && /use your agekey/i.test(b))
			|| [...document.querySelectorAll('[role="dialog"], [aria-modal="true"]')].some(el => vis(el) && /use your agekey/i.test(textOf(el))),
		birthdayConfirm: /confirming that your birthday|you can only confirm your birthday|only confirm your birthday/i.test(b),
		leak: /change your password|check your passwords|data breach|compromised password|found in a data|password leak|leaked password/i.test(b),
		cookieBanner: cookieBanner,
		cookiesOK: cookiesOK,
		save: save ? ('yes:' + save.disabled + ':' + (save.getAttribute('aria-disabled') || '')) : 'none',
		snippet: m ? m[0] : '',
		buttons: [...new Set(buttons)].slice(0, 20)
	});
})()`

const chromeClickVerifyAgeJS = `(() => {
	const vis = (el) => el && el.getBoundingClientRect().width > 2 && el.getBoundingClientRect().height > 2;
	const el = [...document.querySelectorAll('button, a, [role="button"]')].find(b => vis(b) && /verify (your )?age|подтвердить возраст/i.test((b.innerText || b.getAttribute('aria-label') || '').replace(/\s+/g, ' ').trim()));
	if (!el) return '';
	el.click();
	return (el.innerText || '').replace(/\s+/g, ' ').trim();
})()`

const chromeClickAgeKeyJS = `(() => {
	const vis = (el) => el && el.getBoundingClientRect().width > 2 && el.getBoundingClientRect().height > 2;
	const tryRoot = (root) => {
		const cands = [...root.querySelectorAll('button, a, [role="button"], [tabindex], div, span, li')]
			.filter(vis)
			.map(el => ({el, t: (el.innerText || el.getAttribute('aria-label') || '').replace(/\s+/g, ' ').trim()}))
			.filter(x => x.t && /age\s*key/i.test(x.t) && !/selfie|photo id|use your id/i.test(x.t));
		cands.sort((a,b) => a.t.length - b.t.length);
		if (!cands[0]) return '';
		cands[0].el.scrollIntoView({block:'center', inline:'nearest'});
		cands[0].el.click();
		return cands[0].t.slice(0, 80);
	};
	let t = tryRoot(document);
	if (t) return t;
	for (const f of document.querySelectorAll('iframe')) {
		try {
			if (f.contentDocument) {
				t = tryRoot(f.contentDocument);
				if (t) return t;
			}
		} catch (e) {}
	}
	return '';
})()`

const chromeSetEnglishJS = `(() => (async () => {
	const sleep = (ms) => new Promise(r => setTimeout(r, ms));
	const vis = (el) => {
		if (!el || !el.getBoundingClientRect) return false;
		const r = el.getBoundingClientRect();
		const s = getComputedStyle(el);
		return r.width > 2 && r.height > 2 && s.visibility !== 'hidden' && s.display !== 'none';
	};
	const textOf = (el) => (el.innerText || el.getAttribute('aria-label') || '').replace(/\s+/g, ' ').trim();
	const setNative = (el, value) => {
		if (!el) return false;
		el.focus();
		const proto = window.HTMLSelectElement.prototype;
		const desc = Object.getOwnPropertyDescriptor(proto, 'value') || Object.getOwnPropertyDescriptor(window.HTMLInputElement.prototype, 'value');
		desc.set.call(el, value);
		el.dispatchEvent(new Event('input', { bubbles: true }));
		el.dispatchEvent(new Event('change', { bubbles: true }));
		return true;
	};
	const isLangSelect = (sel) => {
		const blob = [...sel.options].map(o => (o.value + ' ' + o.text).toLowerCase()).join(' | ');
		return /english|\ben\b|en_us|en-us/.test(blob) && /deutsch|français|francais|español|espanol|italiano|日本語/.test(blob);
	};
	const englishOpt = (sel) => [...sel.options].find(o => {
		const v = (o.value || '').toLowerCase();
		const t = (o.text || '').replace(/\s+/g, ' ').trim().toLowerCase();
		return t === 'english' || t === 'inglés' || t === 'ingles' || v === 'en' || v === 'en_us' || v === 'en-us' || v.startsWith('en_') || v.startsWith('en-');
	});
	let hits = [];
	for (const sel of document.querySelectorAll('select')) {
		if (!isLangSelect(sel)) continue;
		const opt = englishOpt(sel);
		if (!opt) continue;
		if (sel.value === opt.value) {
			hits.push('already:' + opt.value);
			continue;
		}
		setNative(sel, opt.value);
		hits.push(opt.value || opt.text);
	}
	const alreadyEnglish = [...document.querySelectorAll('button, [role="combobox"], select')].some(el => vis(el) && /^english$/i.test(textOf(el)) && !(el.closest && el.closest('.skiptranslate, [class*="goog-te"]')));
	if (!hits.length && alreadyEnglish) hits.push('already:en');
	if (!hits.length) {
		const lab = [...document.querySelectorAll('label,h2,h3,h4,span,div,p')].find(el => vis(el) && /^(language|idioma|langue|sprache|язык)$/i.test(textOf(el)) && !(el.closest && el.closest('.skiptranslate, [class*="goog-te"]')));
		let control = null;
		for (let n = lab, i = 0; n && i < 8; i++, n = n.parentElement) {
			const cs = [...n.querySelectorAll('select, button, [role="combobox"], [aria-haspopup="listbox"]')].filter(el => vis(el) && !/password|translate|google/i.test(textOf(el)));
			if (cs[0]) { control = cs[0]; break; }
		}
		if (control && !/^english$/i.test(textOf(control))) {
			control.scrollIntoView({block:'center', inline:'nearest'});
			control.click();
			await sleep(280);
			const opt = [...document.querySelectorAll('[role="option"], [role="menuitem"], li, button, span')].filter(vis).find(el => {
				const t = textOf(el);
				if (el.closest && el.closest('.skiptranslate, [class*="goog-te"]')) return false;
				return t && t.length < 24 && /^(english|inglés|ingles)$/i.test(t);
			});
			if (opt) { opt.click(); hits.push('english'); }
			else hits.push('lang-open');
		}
	}
	if (hits.some(h => h && !h.startsWith('already:') && h !== 'lang-open' && h !== 'no-lang-select')) {
		const save = [...document.querySelectorAll('button')].find(b => vis(b) && /^(save|guardar|enregistrer|speichern|сохранить)$/i.test((b.innerText || '').trim()) && !b.disabled);
		if (save) { save.click(); hits.push('saved'); }
	}
	return hits.join(',') || 'no-lang-select';
})())()`

func chromeSetBirthdayFieldsJS(year, month, day int) string {
	return fmt.Sprintf(`(() => {
		const setNative = (el, value) => {
			if (!el) return false;
			el.focus();
			const proto = el.tagName === 'SELECT' ? window.HTMLSelectElement.prototype : window.HTMLInputElement.prototype;
			const desc = Object.getOwnPropertyDescriptor(proto, 'value') || Object.getOwnPropertyDescriptor(window.HTMLInputElement.prototype, 'value');
			const last = el.value;
			desc.set.call(el, value);
			if (el._valueTracker) el._valueTracker.setValue(last);
			el.dispatchEvent(new Event('input', { bubbles: true }));
			el.dispatchEvent(new Event('change', { bubbles: true }));
			return true;
		};
		const y = String(%d), m = String(%d), d = String(%d);
		const wantM = Number(m);
		const monthName = {
			1: /^(jan|ene|янв)/i,
			2: /^(feb|фев)/i,
			3: /^(mar|мар)/i,
			4: /^(apr|abr|avr|апр)/i,
			5: /^(may|mai|мая|май)/i,
			6: /^(jun|juin|июн)/i,
			7: /^(jul|juil|июл)/i,
			8: /^(aug|ago|août|авг)/i,
			9: /^(sep|сен)/i,
			10: /^(oct|okt|окт)/i,
			11: /^(nov|ноя)/i,
			12: /^(dec|dic|dez|déc|дек)/i
		}[wantM];
		const byAria = (re) => [...document.querySelectorAll('select')].find(el => re.test(el.getAttribute('aria-label') || el.name || ''));
		const monthEl = byAria(/month|месяц|mes|mois|monat/i);
		const dayEl = byAria(/^(day|день|día|jour|tag)$/i) || byAria(/day|день/i);
		const yearEl = byAria(/year|год|año|année|jahr/i);
		let got = [];
		if (monthEl) {
			const opt = [...monthEl.options].find(o => {
				const v = String(o.value || '').replace(/^0+/, '');
				if (Number(v) === wantM) return true;
				const t = (o.text || '').replace(/\s+/g, ' ').trim();
				return monthName && monthName.test(t);
			});
			if (opt) { setNative(monthEl, opt.value); got.push('month:' + opt.value); }
		}
		if (dayEl) {
			const opt = [...dayEl.options].find(o => o.value === d || o.value === d.padStart(2,'0') || o.text.trim() === d);
			if (opt) { setNative(dayEl, opt.value); got.push('day'); }
		}
		if (yearEl) {
			const opt = [...yearEl.options].find(o => o.value === y || o.text.trim() === y);
			if (opt) { setNative(yearEl, opt.value); got.push('year'); }
		}
		return got.join(',') || 'no-fields';
	})()`, year, month, day)
}

func chromeSetBirthdayAsyncJS(year, month, day int) string {
	return fmt.Sprintf(`(() => (async () => {
		const sleep = (ms) => new Promise(r => setTimeout(r, ms));
		const vis = (el) => {
			if (!el || !el.getBoundingClientRect) return false;
			const r = el.getBoundingClientRect();
			const s = getComputedStyle(el);
			return r.width > 2 && r.height > 2 && s.visibility !== 'hidden' && s.display !== 'none';
		};
		const dateCtl = [...document.querySelectorAll('select, button, [role="combobox"]')].filter(vis).filter(el => /month|day|year|mes|día|año|месяц|день|год/i.test((el.getAttribute('aria-label') || el.innerText || el.name || '')));
		const usable = dateCtl.filter(el => !el.disabled && el.getAttribute('aria-disabled') !== 'true');
		if (dateCtl.length >= 3 && usable.length === 0) {
			return 'locked';
		}
		const textOf = (el) => (el.innerText || el.getAttribute('aria-label') || el.getAttribute('placeholder') || '').replace(/\s+/g, ' ').trim();
		const setNative = (el, value) => {
			if (!el) return false;
			el.focus();
			const proto = el.tagName === 'SELECT' ? window.HTMLSelectElement.prototype : window.HTMLInputElement.prototype;
			const desc = Object.getOwnPropertyDescriptor(proto, 'value') || Object.getOwnPropertyDescriptor(window.HTMLInputElement.prototype, 'value');
			const last = el.value;
			desc.set.call(el, value);
			if (el._valueTracker) el._valueTracker.setValue(last);
			el.dispatchEvent(new Event('input', { bubbles: true }));
			el.dispatchEvent(new Event('change', { bubbles: true }));
			return true;
		};
		const y = String(%d), m = String(%d), d = String(%d);
		const wantM = Number(m);
		const monthName = {
			1: /^(jan|ene|янв)/i,
			2: /^(feb|фев)/i,
			3: /^(mar|мар)/i,
			4: /^(apr|abr|avr|апр)/i,
			5: /^(may|mai|мая|май)/i,
			6: /^(jun|juin|июн)/i,
			7: /^(jul|juil|июл)/i,
			8: /^(aug|ago|août|авг)/i,
			9: /^(sep|сен)/i,
			10: /^(oct|okt|окт)/i,
			11: /^(nov|ноя)/i,
			12: /^(dec|dic|dez|déc|дек)/i
		}[wantM];
		const byAria = (re) => [...document.querySelectorAll('select')].find(el => re.test(el.getAttribute('aria-label') || el.name || ''));
		let got = [];
		const monthEl = byAria(/month|месяц|mes|mois|monat/i);
		const dayEl = byAria(/^(day|день|día|jour|tag)$/i) || byAria(/day|день/i);
		const yearEl = byAria(/year|год|año|année|jahr/i);
		if (monthEl) {
			const opt = [...monthEl.options].find(o => {
				const v = String(o.value || '').replace(/^0+/, '');
				if (Number(v) === wantM) return true;
				const t = (o.text || '').replace(/\s+/g, ' ').trim();
				return monthName && monthName.test(t);
			});
			if (opt) { setNative(monthEl, opt.value); got.push('month:' + opt.value); }
		}
		if (dayEl) {
			const opt = [...dayEl.options].find(o => o.value === d || o.value === d.padStart(2,'0') || o.text.trim() === d);
			if (opt) { setNative(dayEl, opt.value); got.push('day'); }
		}
		if (yearEl) {
			const opt = [...yearEl.options].find(o => o.value === y || o.text.trim() === y);
			if (opt) { setNative(yearEl, opt.value); got.push('year'); }
		}
		const pickOpen = async (re) => {
			await sleep(260);
			const opt = [...document.querySelectorAll('[role="option"], [role="menuitem"], li, button, span, div')].filter(vis).find(el => {
				const t = textOf(el);
				if (/password|translate|google|verify age|two-factor/i.test(t)) return false;
				if (el.closest && el.closest('.skiptranslate, [class*="goog-te"]')) return false;
				return t && t.length < 22 && re.test(t);
			});
			if (!opt) return '';
			opt.click();
			return textOf(opt);
		};
		if (got.length < 3) {
			const skipCtl = (el) => /password|security|verify age|language|idioma|two-factor|2fa|translate|google/i.test(textOf(el) + ' ' + (el.getAttribute('aria-label') || ''));
			const lab = [...document.querySelectorAll('h2,h3,h4,legend,label,span,div,p')].find(el => vis(el) && /^(birthday|date of birth|fecha de nacimiento|anniversaire|geburtstag)$/i.test(textOf(el)));
			let controls = [];
			for (let n = lab, i = 0; n && i < 8; i++, n = n.parentElement) {
				const cs = [...n.querySelectorAll('select, button, [role="combobox"], [aria-haspopup="listbox"]')].filter(el => vis(el) && !skipCtl(el));
				const dated = cs.filter(el => /month|day|year|mes|día|año|месяц|день|год/i.test(textOf(el) + ' ' + (el.getAttribute('aria-label') || '')));
				if (dated.length >= 3) { controls = dated.slice(0, 3); break; }
			}
			if (!controls.length) {
				controls = [...document.querySelectorAll('select, button, [role="combobox"], [aria-haspopup="listbox"]')]
					.filter(el => vis(el) && /month|day|year|mes|día|año|месяц|день|год/i.test(textOf(el) + ' ' + (el.getAttribute('aria-label') || '')))
					.slice(0, 3);
			}
			const monthRe = monthName || /./;
			const dayRe = new RegExp('^(0?' + d + ')$');
			const yearRe = new RegExp('^' + y + '$');
			if (controls[0] && !got.some(x => x.startsWith('month'))) {
				controls[0].scrollIntoView({block:'center', inline:'nearest'});
				controls[0].click();
				const p = await pickOpen(monthRe);
				got.push(p ? 'month:' + p : 'month-open');
			}
			if (controls[1] && !got.includes('day')) {
				controls[1].scrollIntoView({block:'center', inline:'nearest'});
				controls[1].click();
				const p = await pickOpen(dayRe);
				got.push(p ? 'day:' + p : 'day-open');
			}
			if (controls[2] && !got.includes('year')) {
				controls[2].scrollIntoView({block:'center', inline:'nearest'});
				controls[2].click();
				const p = await pickOpen(yearRe);
				got.push(p ? 'year:' + p : 'year-open');
			}
		}
		return got.join(',') || 'no-fields';
	})())()`, year, month, day)
}

func chromeClickTextJS(pattern string, maxLen int) string {
	if maxLen <= 0 {
		maxLen = 48
	}
	return fmt.Sprintf(`(() => {
		const re = %s;
		const maxLen = %d;
		const vis = (el) => {
			if (!el) return false;
			const r = el.getBoundingClientRect();
			return r.width > 2 && r.height > 2;
		};
		const cands = [...document.querySelectorAll('button, a, [role="button"], [tabindex], label, span, div')]
			.filter(vis)
			.map(el => ({el, t: (el.innerText || el.getAttribute('aria-label') || '').replace(/\s+/g, ' ').trim()}))
			.filter(x => x.t && x.t.length <= maxLen && re.test(x.t) && !/password|translate|google|verify age|two-factor/i.test(x.t));
		cands.sort((a,b) => a.t.length - b.t.length);
		if (!cands[0]) return '';
		cands[0].el.scrollIntoView({block:'center', inline:'nearest'});
		cands[0].el.click();
		return cands[0].t;
	})()`, pattern, maxLen)
}

func dumpChromeControls(ctx context.Context, label string) {
	var out string
	if err := chromedp.Evaluate(chromePageDumpJS, &out).Do(ctx); err != nil {
		fmt.Println("---", label, "dump failed:", err)
		return
	}
	fmt.Println("---", label, "---")
	fmt.Println(out)
	saveChromeShot(ctx, "setup-"+strings.ReplaceAll(strings.ToLower(label), " ", "-")+".png")
}

func saveChromeShot(ctx context.Context, name string) {
	var buf []byte
	if err := chromedp.FullScreenshot(&buf, 80).Do(ctx); err != nil {
		return
	}
	_ = os.MkdirAll("scraped", 0755)
	path := filepath.Join("scraped", name)
	if err := os.WriteFile(path, buf, 0644); err != nil {
		return
	}
	fmt.Println("Screenshot:", path)
}

func xpathQuote(s string) string {
	if !strings.Contains(s, `'`) {
		return `'` + s + `'`
	}
	return `"` + s + `"`
}

func chromeClickLabel(ctx context.Context, label string) error {
	return chromeClickLabelWait(ctx, label, 2*time.Second)
}

func chromeClickLabelWait(ctx context.Context, label string, wait time.Duration) error {
	xp := `//*[normalize-space()=` + xpathQuote(label) + `]`
	runCtx, cancel := context.WithTimeout(ctx, wait)
	defer cancel()
	return chromedp.Run(runCtx, chromedp.Click(xp, chromedp.BySearch, chromedp.NodeVisible))
}

func dismissChromeNags(ctx context.Context) {
	if ageKeyHandsOff() {
		return
	}
	var href string
	_ = chromedp.Location(&href).Do(ctx)
	if strings.Contains(strings.ToLower(href), "/privacy/consent") {
		_ = chromeClickLabelWait(ctx, "I Agree!", 2*time.Second)
		if hit := clickPrivacyConsentAgree(); hit != "" {
			fmt.Println("Dismissed cookies:", hit)
			time.Sleep(500 * time.Millisecond)
		}
	}
	for i := 0; i < 8; i++ {
		if nag := dismissChromeLeakDialog(); nagWasDismissed(nag) {
			fmt.Println("Dismissed:", nag)
			time.Sleep(200 * time.Millisecond)
			continue
		}
		var overlay string
		_ = chromedp.Evaluate(chromeDismissOverlaysJS, &overlay).Do(ctx)
		if overlay == "" {
			_ = chromedp.Evaluate(chromeClickCookieJS, &overlay).Do(ctx)
		}
		if overlay != "" {
			fmt.Println("Dismissed:", overlay)
			time.Sleep(350 * time.Millisecond)
			continue
		}
		if hit := clickCookieAcceptOnConsentTargets(); hit != "" {
			fmt.Println("Dismissed cookies:", hit)
			time.Sleep(400 * time.Millisecond)
			continue
		}
		if nag := dismissNativePasswordNag(); nagWasDismissed(nag) {
			fmt.Println("Dismissed:", nag)
			time.Sleep(250 * time.Millisecond)
			continue
		}
		if cookieBannerOpen() {
			time.Sleep(300 * time.Millisecond)
			continue
		}
		return
	}
}

const chromePremiumCloseHitJS = `(() => {
	const vis = (el) => {
		if (!el) return false;
		const s = getComputedStyle(el);
		const r = el.getBoundingClientRect();
		return s.display !== 'none' && s.visibility !== 'hidden' && r.width > 4 && r.height > 4;
	};
	const textOf = (el) => (el.innerText || el.getAttribute('aria-label') || '').replace(/\s+/g, ' ').trim();
	const premiumish = (t) => /tumblr premium|get premium now|ad-free|go premium|early (feature )?access|doubled daily limits|free monthly tumblrmart|billed annually/i.test(t);
	const blazeish = (t) => /blaze/i.test(t) && !premiumish(t);
	const premiumSkipBtn = /get premium|go premium|yearly|monthly|save \d+%/i;
	const rgbOf = (el) => {
		const m = (getComputedStyle(el).backgroundColor || '').match(/[\d.]+/g);
		if (!m || m.length < 3) return null;
		return {r:+m[0], g:+m[1], b:+m[2], a: m.length > 3 ? +m[3] : 1};
	};
	const purpleFill = (el) => {
		const c = rgbOf(el);
		if (!c || c.a < 0.35) return false;
		return c.b > 90 && c.r > 40 && (c.b - c.g) > 25 && c.g < 170;
	};
	const blazePrimary = (root) => {
		const btns = [...root.querySelectorAll('button, a, [role="button"]')].filter(el => {
			if (!vis(el)) return false;
			const r = el.getBoundingClientRect();
			const t = textOf(el);
			if (r.width < 72 || r.height < 30 || r.height > 84) return false;
			if (/^×$|^x$|^close$|^dismiss$/i.test(t) || t.length > 80) return false;
			if (premiumSkipBtn.test(t)) return false;
			return true;
		});
		const filled = btns.filter(purpleFill);
		if (filled.length) return filled[0];
		btns.sort((a,b) => {
			const lum = (el) => {
				const c = rgbOf(el);
				if (!c || c.a < 0.35) return 999;
				return 0.3*c.r + 0.59*c.g + 0.11*c.b;
			};
			return lum(a) - lum(b);
		});
		return btns[0] || null;
	};
	const closeOf = (root) => {
		const labeled = [...root.querySelectorAll('button, a, [role="button"], [aria-label]')]
			.find(el => {
				if (!vis(el)) return false;
				const lab = (el.getAttribute('aria-label') || textOf(el)).replace(/\s+/g,' ').trim();
				if (!/^(close|dismiss|×|x)$/i.test(lab)) return false;
				return !premiumSkipBtn.test(textOf(el));
			});
		if (labeled) return labeled;
		const exact = [...root.querySelectorAll('button, a, [role="button"]')]
			.find(el => vis(el) && /^close$/i.test(textOf(el)));
		if (exact) return exact;
		const box = root.getBoundingClientRect ? root.getBoundingClientRect() : {top: 0, right: innerWidth};
		let best = null, bestScore = 1e9;
		for (const el of root.querySelectorAll('button')) {
			if (!vis(el) || premiumSkipBtn.test(textOf(el))) continue;
			const r = el.getBoundingClientRect();
			if (r.width > 56 || r.height > 56) continue;
			if (r.top < box.top - 8 || r.top > box.top + 90) continue;
			if (r.left < box.right - 100) continue;
			const score = r.width * r.height + (box.right - r.right);
			if (score < bestScore) { best = el; bestScore = score; }
		}
		return best;
	};
	for (const dlg of [...document.querySelectorAll('[role="dialog"], [aria-modal="true"]')].filter(vis)) {
		if (!premiumish(textOf(dlg))) continue;
		const el = closeOf(dlg);
		if (!el) continue;
		const r = el.getBoundingClientRect();
		return {found: true, text: 'premium-close', x: r.left + r.width / 2, y: r.top + r.height / 2};
	}
	const page = (document.body && document.body.innerText || '');
	if (premiumish(page) || /\/premium/i.test(location.pathname)) {
		const el = closeOf(document);
		if (el) {
			const r = el.getBoundingClientRect();
			return {found: true, text: 'premium-close', x: r.left + r.width / 2, y: r.top + r.height / 2};
		}
	}
	for (const dlg of [...document.querySelectorAll('[role="dialog"], [aria-modal="true"]')].filter(vis)) {
		const dt = textOf(dlg);
		if (premiumish(dt) || !blazeish(dt)) continue;
		const el = blazePrimary(dlg);
		if (!el) continue;
		el.click();
		const r = el.getBoundingClientRect();
		return {found: true, text: textOf(el) || 'blaze-ok', x: r.left + r.width / 2, y: r.top + r.height / 2};
	}
	return {found: false, text: '', x: 0, y: 0};
})()`

func dismissPremiumOnPort() string {
	if ageKeyHandsOff() {
		return ""
	}
	list, err := listChromeTargets()
	if err != nil {
		return ""
	}
	clicked := ""
	for _, t := range list {
		if (t.Type != "page" && t.Type != "iframe") || t.ID == "" {
			continue
		}
		if !strings.Contains(strings.ToLower(t.URL), "tumblr.com") {
			continue
		}
		var b struct {
			Found bool    `json:"found"`
			Text  string  `json:"text"`
			X     float64 `json:"x"`
			Y     float64 `json:"y"`
		}
		if err := evalJSONOnChromeTarget(t, chromePremiumCloseHitJS, &b); err != nil || !b.Found {
			continue
		}
		_ = mouseClickOnChromeTarget(t, b.X, b.Y)
		clicked = b.Text
		break
	}
	if clicked != "" {
		time.Sleep(250 * time.Millisecond)
	}
	if list, err = listChromeTargets(); err == nil {
		for _, t := range list {
			if t.Type != "page" || t.ID == "" {
				continue
			}
			if !strings.Contains(strings.ToLower(t.URL), "tumblr.com") || !strings.Contains(strings.ToLower(t.URL), "/premium") {
				continue
			}
			// Escape first in case the modal is still up, then leave /premium entirely.
			var esc string
			_ = evalJSONOnChromeTarget(t, `(() => { try { document.dispatchEvent(new KeyboardEvent('keydown',{key:'Escape',bubbles:true})); } catch(e) {} return 'esc'; })()`, &esc)
			_ = navigateChromeTab(t, "https://www.tumblr.com/settings/account")
			if clicked == "" {
				clicked = "premium-page"
			}
			break
		}
	}
	closeExtraSetupTabs()
	return clicked
}

func cdpDismissOverlays(call cdpCall) string {
	raw, _ := call("Runtime.evaluate", map[string]any{"expression": chromeDismissOverlaysJS, "returnByValue": true, "userGesture": true})
	if s := cdpEvalString(raw); s != "" {
		return s
	}
	raw, err := call("Runtime.evaluate", map[string]any{"expression": chromePremiumCloseHitJS, "returnByValue": true, "userGesture": true})
	if err != nil {
		return ""
	}
	var wrap struct {
		Result struct {
			Value json.RawMessage `json:"value"`
		} `json:"result"`
	}
	var box struct {
		Found bool    `json:"found"`
		Text  string  `json:"text"`
		X     float64 `json:"x"`
		Y     float64 `json:"y"`
	}
	if json.Unmarshal(raw, &wrap) != nil {
		return ""
	}
	_ = json.Unmarshal(wrap.Result.Value, &box)
	if !box.Found || box.X < 1 || box.Y < 1 {
		return ""
	}
	_ = cdpDispatchClick(call, box.X, box.Y)
	if strings.TrimSpace(box.Text) == "" {
		return "premium-close"
	}
	return box.Text
}

func dismissOverlaysOnPort() {
	if ageKeyHandsOff() {
		return
	}
	for i := 0; i < 8; i++ {
		if nag := dismissChromeLeakDialog(); nagWasDismissed(nag) {
			fmt.Println("Dismissed:", nag)
			time.Sleep(200 * time.Millisecond)
			continue
		}
		if nag := dismissSavePasswordBubble(); nagWasDismissed(nag) {
			fmt.Println("Dismissed:", nag)
			time.Sleep(250 * time.Millisecond)
			continue
		}
		if hit := dismissPremiumOnPort(); hit != "" {
			fmt.Println("Dismissed:", hit)
			time.Sleep(350 * time.Millisecond)
			continue
		}
		var overlay string
		if err := evalOnTumblrPage(chromeDismissOverlaysJS, &overlay); err == nil && overlay != "" {
			fmt.Println("Dismissed:", overlay)
			time.Sleep(350 * time.Millisecond)
			continue
		}
		if err := evalOnTumblrPage(chromeClickCookieJS, &overlay); err == nil && overlay != "" {
			fmt.Println("Dismissed cookies:", overlay)
			time.Sleep(350 * time.Millisecond)
			continue
		}
		if hit := clickCookieAcceptOnConsentTargets(); hit != "" {
			fmt.Println("Dismissed cookies:", hit)
			time.Sleep(400 * time.Millisecond)
			continue
		}
		if cookieBannerOpen() {
			time.Sleep(300 * time.Millisecond)
			continue
		}
		return
	}
}

func clickUseAgeKey(ctx context.Context) string {
	if hit := clickAgeKeyCardOnTumblrPage(); hit != "" {
		return hit
	}
	if hit := clickUseAgeKeyOnKidTarget(); hit != "" {
		return hit
	}
	if hit := clickAgeKeyIframeCoords(ctx); hit != "" {
		return hit
	}
	for _, label := range []string{"Use your AgeKey", "AgeKey"} {
		if chromeClickLabelWait(ctx, label, 800*time.Millisecond) == nil {
			return label
		}
	}
	var box struct {
		Found bool   `json:"found"`
		Text  string `json:"text"`
	}
	_ = chromedp.Evaluate(chromeUseAgeKeyHitJS, &box).Do(ctx)
	if box.Found && strings.TrimSpace(box.Text) != "" {
		return box.Text
	}
	return ""
}

func clickAgeKeyIframeCoords(ctx context.Context) string {
	var box struct {
		X float64 `json:"x"`
		Y float64 `json:"y"`
		W float64 `json:"w"`
		H float64 `json:"h"`
	}
	js := `(() => {
		const frames = [...document.querySelectorAll('iframe')].map(el => {
			const r = el.getBoundingClientRect();
			return {el, src: el.src || '', x: r.left, y: r.top, w: r.width, h: r.height};
		}).filter(f => f.w > 240 && f.h > 280);
		const kid = frames.find(f => /k-id|agekey/i.test(f.src));
		if (!kid) return {x:0,y:0,w:0,h:0};
		kid.el.scrollIntoView({block:'center', inline:'nearest'});
		const r = kid.el.getBoundingClientRect();
		return {x: r.left, y: r.top, w: r.width, h: r.height};
	})()`
	if err := chromedp.Evaluate(js, &box).Do(ctx); err != nil || box.W < 200 || box.H < 280 {
		return ""
	}
	x := box.X + box.W*0.5
	// Three stacked methods; AgeKey is the bottom card, above the lock.
	for _, frac := range []float64{0.70, 0.76} {
		y := box.Y + box.H*frac
		if err := chromedp.MouseClickXY(x, y).Do(ctx); err != nil {
			continue
		}
		time.Sleep(600 * time.Millisecond)
	}
	fmt.Println("Clicked AgeKey card in k-ID window")
	return "Use your AgeKey"
}

func clickAlreadyHaveAgeKey(ctx context.Context) string {
	if hit := clickHaveAgeKeyOnKidTarget(); hit != "" {
		time.Sleep(1 * time.Second)
		if listed := clickListedAgeKeyOnKidTarget(); listed != "" {
			hit = listed
		}
		if osHit := pickAgeKeyPasskeySoon(); osHit != "" && osHit != "pin-prompt" {
			fmt.Println("Selected passkey:", osHit)
		}
		return hit
	}
	if chromeHasTargetHost("agekey.org") || chromeHasTargetHost("agekey.com") {
		if listed := clickListedAgeKeyOnKidTarget(); listed != "" {
			if osHit := pickAgeKeyPasskeySoon(); osHit != "" && osHit != "pin-prompt" {
				fmt.Println("Selected passkey:", osHit)
			}
			return listed
		}
		hit := clickHaveAgeKeyIframeCoords(ctx)
		if hit != "" {
			if osHit := pickAgeKeyPasskeySoon(); osHit != "" && osHit != "pin-prompt" {
				fmt.Println("Selected passkey:", osHit)
			}
		}
		return hit
	}
	return ""
}

func clickHaveAgeKeyIframeCoords(ctx context.Context) string {
	var box struct {
		X float64 `json:"x"`
		Y float64 `json:"y"`
		W float64 `json:"w"`
		H float64 `json:"h"`
	}
	js := `(() => {
		const frames = [...document.querySelectorAll('iframe')].map(el => {
			const r = el.getBoundingClientRect();
			return {el, src: el.src || '', x: r.left, y: r.top, w: r.width, h: r.height};
		}).filter(f => f.w > 240 && f.h > 280);
		const kid = frames.find(f => /agekey/i.test(f.src)) || frames.find(f => /k-id/i.test(f.src));
		if (!kid) return {x:0,y:0,w:0,h:0};
		kid.el.scrollIntoView({block:'center', inline:'nearest'});
		const r = kid.el.getBoundingClientRect();
		return {x: r.left, y: r.top, w: r.width, h: r.height};
	})()`
	if err := chromedp.Run(ctx, chromedp.Evaluate(js, &box)); err != nil || box.W < 200 || box.H < 280 {
		return ""
	}
	x := box.X + box.W*0.5
	// Link sits under the blue Create button. Stay below ~0.84 so we do not hit Create.
	for _, frac := range []float64{0.90, 0.93} {
		y := box.Y + box.H*frac
		_ = chromedp.Run(ctx, chromedp.MouseClickXY(x, y))
		time.Sleep(400 * time.Millisecond)
	}
	return "Already have an AgeKey?"
}

func submitAgeKeyPasskeyPIN(port, pin string) error {
	port = strings.TrimSpace(port)
	if port == "" {
		port = "9343"
	}
	liveDebugPort = port
	if hwnd := findWindowContaining("Windows Security"); hwnd != 0 {
		fmt.Println("Passkey dialog already open.")
		return typeIntoPasskeyDialogWait(pin, 500*time.Millisecond)
	}
	if chrome := findWindowContaining("Tumblr"); chrome != 0 {
		fmt.Println("Focusing Chrome so the passkey prompt can return.")
		focusWindow(chrome)
		time.Sleep(1200 * time.Millisecond)
	}
	if hwnd := findWindowContaining("Windows Security"); hwnd != 0 {
		fmt.Println("Passkey dialog already open.")
		return typeIntoPasskeyDialogWait(pin, 2*time.Second)
	}
	if chromeHasTargetHost("agekey.org") || chromeHasTargetHost("agekey.com") {
		if hit := clickHaveAgeKeyOnKidTarget(); hit != "" {
			fmt.Println("Clicked:", hit)
			time.Sleep(1500 * time.Millisecond)
		}
	}
	if err := typeIntoPasskeyDialogWait(pin, 8*time.Second); err == nil {
		return nil
	}
	return fmt.Errorf("Windows Security PIN box is still on screen — type it by hand and press Enter")
}

func clickHaveAgeKeyOnPort(port string) error {
	port = strings.TrimSpace(port)
	if port == "" {
		port = "9343"
	}
	liveDebugPort = port
	if !chromeHasTargetHost("agekey.org") && !chromeHasTargetHost("agekey.com") {
		hit := clickAgeKeyCardOnTumblrPage()
		if hit == "" {
			hit = clickUseAgeKeyOnKidTarget()
		}
		if hit == "" {
			return fmt.Errorf("could not click Use your AgeKey")
		}
		fmt.Println("Clicked AgeKey:", hit)
		fmt.Println("Waiting for AgeKey page…")
		if !waitForChromeTargetHost("agekey.org", 18*time.Second) && !chromeHasTargetHost("agekey.com") {
			return fmt.Errorf("AgeKey page did not open")
		}
		time.Sleep(1500 * time.Millisecond)
	}
	for i := 0; i < 8; i++ {
		if hit := clickHaveAgeKeyOnKidTarget(); hit != "" {
			fmt.Println("Clicked:", hit)
			return nil
		}
		time.Sleep(700 * time.Millisecond)
	}
	return fmt.Errorf("could not click Already have an AgeKey?")
}

func clickVerifyAge(ctx context.Context) string {
	for _, label := range []string{"Подтвердить возраст", "Verify Age", "Verify your age", "Verify age"} {
		if chromeClickLabelWait(ctx, label, 1500*time.Millisecond) == nil {
			return label
		}
	}
	var hit string
	_ = chromedp.Evaluate(chromeClickTextJS(`/подтвердить возраст|verify (your )?age|age verification/i`, 40), &hit).Do(ctx)
	return hit
}

func confirmBirthdayDialog(ctx context.Context) bool {
	var hit struct {
		T string  `json:"t"`
		X float64 `json:"x"`
		Y float64 `json:"y"`
	}
	_ = chromedp.Evaluate(chromeClickAgeConfirmJS, &hit).Do(ctx)
	if hit.T != "" {
		fmt.Println("Clicked:", hit.T)
		time.Sleep(1200 * time.Millisecond)
		return true
	}
	body := strings.ToLower(chromePageText(ctx))
	if !strings.Contains(body, "confirming that your birthday") &&
		!strings.Contains(body, "only confirm your birthday") &&
		!strings.Contains(body, "you can only confirm") {
		return false
	}
	for _, label := range []string{"Okay", "OK", "Accept", "Confirm", "Continue", "Got it", "Подтвердить"} {
		if chromeClickLabelWait(ctx, label, 2*time.Second) == nil {
			fmt.Println("Clicked:", label)
			time.Sleep(1200 * time.Millisecond)
			return true
		}
	}
	return false
}

func clickEditAppearance(ctx context.Context) error {
	editorOpen := func() bool {
		var open bool
		_ = chromedp.Evaluate(chromeAppearanceEditorOpenJS, &open).Do(ctx)
		return open
	}
	if editorOpen() {
		return nil
	}
	// Chrome's Save-password bubble sits on top of Edit appearance and makes
	// the click a no-op; dismiss native + in-page password nags first.
	if nag := dismissSavePasswordBubble(); nagWasDismissed(nag) {
		fmt.Println("Dismissed:", nag)
	}
	dismissChromeNags(ctx)
	clicked := ""
	for _, label := range []string{
		"Edit appearance",
		"Customize appearance",
		"Editar apariencia",
		"Personalizar apariencia",
		"Modificar apariencia",
		"Изменить оформление",
	} {
		if chromeClickLabelWait(ctx, label, 4*time.Second) == nil {
			clicked = label
			fmt.Println("Clicked:", label)
			break
		}
	}
	if clicked == "" {
		runCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
		defer cancel()
		if chromedp.Run(runCtx, chromedp.Click(`[aria-label="Edit appearance"]`, chromedp.ByQuery, chromedp.NodeVisible)) == nil {
			clicked = "Edit appearance"
			fmt.Println("Clicked: Edit appearance")
		}
	}
	if clicked == "" {
		return fmt.Errorf("edit appearance button not found")
	}
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		var href string
		_ = chromedp.Location(&href).Do(ctx)
		if tumblrCustomizeURL(href) {
			return fmt.Errorf("opened theme customizer instead of appearance")
		}
		if editorOpen() {
			return nil
		}
		time.Sleep(250 * time.Millisecond)
	}
	if editorOpen() {
		return nil
	}
	// Tumblr renames these controls per language, so a missed "is it open"
	// check must not stop us from typing into the fields that are there.
	return fmt.Errorf("edit appearance did not open — no appearance changes were made")
}

func chromePageText(ctx context.Context) string {
	var body string
	_ = chromedp.Evaluate(`(document.body && document.body.innerText) || ''`, &body).Do(ctx)
	return body
}

func setEnglishLanguage(ctx context.Context) {
	var out string
	if err := chromedp.Evaluate(chromeSetEnglishJS, &out).Do(ctx); err != nil {
		fmt.Println("Language:", err)
		return
	}
	fmt.Println("Language:", out)
}

func reloadAccountSettings(ctx context.Context) error {
	if err := chromedpNavigateLoggedIn(ctx, "https://www.tumblr.com/settings/account"); err != nil {
		return err
	}
	if err := chromedp.WaitReady("body", chromedp.ByQuery).Do(ctx); err != nil {
		return err
	}
	time.Sleep(2 * time.Second)
	dismissChromeNags(ctx)
	return nil
}

func chromedpNavigateLoggedIn(ctx context.Context, pageURL string) error {
	var href string
	if err := chromedp.Location(&href).Do(ctx); err != nil {
		return err
	}
	if tumblrLoginURL(href) {
		return fmt.Errorf("still on login page")
	}
	return chromedp.Navigate(pageURL).Do(ctx)
}

func applyBlogLookInChrome(imagePath, blogName, title, desc string) error {
	if look := lookStatusFromAPI(blogName); look.Complete() {
		fmt.Println("Name, bio, and avatar already set — still opening appearance for banner and save.")
	}

	headerAbs := ""
	if banners, err := listBannerPics(); err != nil {
		fmt.Println("Banner:", err)
	} else if p := bannerForCurrentAccount(banners); p != "" {
		abs, err := imageFromDir(p, backgroundPicsDir, "banner")
		if err != nil {
			fmt.Println("Banner:", err)
		} else {
			headerAbs = abs
			fmt.Println("Banner:", filepath.ToSlash(p))
		}
	}

	ctx, cancel, err := withBotChrome(120 * time.Second)
	if err != nil {
		return err
	}
	defer cancel()

	settings := "https://www.tumblr.com/settings/blog/" + blogName
	fillJS := chromeFillTitleBioJS(title, desc)
	avatarAbs, err := imageFromDir(imagePath, profilePicsDir, "profile picture")
	if err != nil {
		return err
	}

	var href, clicked, filled, saved string
	err = chromedp.Run(ctx,
		chromedp.ActionFunc(func(ctx context.Context) error {
			return chromedpNavigateLoggedIn(ctx, settings)
		}),
		chromedp.WaitReady("body", chromedp.ByQuery),
		chromedp.Sleep(3*time.Second),
		chromedp.Location(&href),
		chromedp.ActionFunc(func(ctx context.Context) error {
			if strings.Contains(href, "/login") {
				return fmt.Errorf("still on login page")
			}
			if !strings.Contains(href, "/settings/blog/") {
				if err := chromedpNavigateLoggedIn(ctx, settings); err != nil {
					return err
				}
				_ = chromedp.WaitReady("body", chromedp.ByQuery).Do(ctx)
				time.Sleep(2 * time.Second)
				_ = chromedp.Location(&href).Do(ctx)
			}
			dismissChromeNags(ctx)
			dumpChromeControls(ctx, "blog settings")
			// Language is set on /settings/account earlier; running it on blog
			// settings hits the theme language picker and can return a non-string.
			if err := clickEditAppearance(ctx); err != nil {
				return err
			}
			clicked = "Edit appearance"
			for i := 0; i < 8; i++ {
				time.Sleep(700 * time.Millisecond)
				t := chromePageText(ctx)
				if strings.Contains(t, "Accent") || strings.Contains(t, "Background") || strings.Contains(t, "Edit blog avatar") {
					return nil
				}
				var n int64
				_ = chromedp.Evaluate(`document.querySelectorAll('input[type=file]').length`, &n).Do(ctx)
				if n > 0 {
					return nil
				}
			}
			return nil
		}),
		chromedp.Location(&href),
		chromedp.ActionFunc(func(ctx context.Context) error {
			dumpChromeControls(ctx, "appearance")
			dismissChromeNags(ctx)
			if err := chromedp.Evaluate(fillJS, &filled).Do(ctx); err != nil {
				return err
			}
			if !strings.Contains(filled, "title") || !strings.Contains(filled, "bio") {
				dismissChromeNags(ctx)
				_ = chromedp.Evaluate(fillJS, &filled).Do(ctx)
			}
			return nil
		}),
		chromedp.Sleep(1500*time.Millisecond),
	)
	if err != nil {
		return err
	}
	if clicked != "" {
		fmt.Println("Clicked:", clicked)
	}
	if filled != "" {
		fmt.Println("Filled:", filled)
	} else {
		fmt.Println("Filled: (no title/bio fields)")
	}

	t, ok := appearancePageTarget()
	if !ok {
		return fmt.Errorf("no appearance tab after Edit appearance")
	}
	if err := setLookImageOnTarget(t, chromeAvatarBoxJS, avatarAbs, "Edit blog avatar image"); err != nil {
		fmt.Println("Avatar:", err)
	} else if hit := confirmAvatarAndSaveOnTarget(t); hit != "" {
		saved = hit
		fmt.Println("Clicked:", hit)
	}
	if headerAbs != "" {
		if err := trySetHeaderFileOnTarget(t, headerAbs); err != nil {
			fmt.Println("Banner:", err)
		} else if hit := confirmAvatarAndSaveOnTarget(t); hit != "" {
			if saved != "" {
				saved += " | "
			}
			saved += hit
			fmt.Println("Clicked:", hit)
		}
	}

	time.Sleep(1200 * time.Millisecond)
	look := lookStatusFromAPI(blogName)
	switch {
	case look.Complete():
		fmt.Println("Confirmed name, bio, and avatar.")
	case look.OK() && !look.AvatarOK:
		fmt.Println("Name/bio set, avatar not saved yet.")
	case look.Title == "" && look.Bio == "":
		fmt.Println("Could not confirm name/bio over HTTP yet.")
	default:
		fmt.Printf("Name/bio not confirmed yet (title=%q bio=%q avatar=%v)\n", look.Title, truncate(look.Bio, 80), look.AvatarOK)
	}
	return nil
}

func confirmAvatarAndSaveOnTarget(t chromeJSONTarget) string {
	if toast := appearanceErrorOnTarget(t); toast != "" {
		fmt.Println("Tumblr:", toast)
		return ""
	}
	var hit string
	_ = evalJSONOnChromeTarget(t, chromeSaveAppearanceJS, &hit)
	hit = strings.TrimSpace(hit)
	time.Sleep(2 * time.Second)
	if toast := appearanceErrorOnTarget(t); toast != "" {
		fmt.Println("Tumblr:", toast)
	}
	return hit
}

func appearanceErrorOnTarget(t chromeJSONTarget) string {
	var toast string
	_ = evalJSONOnChromeTarget(t, chromeAppearanceErrorJS, &toast)
	return strings.TrimSpace(toast)
}

func confirmAvatarAndSave(ctx context.Context) string {
	var toast string
	_ = chromedp.Run(ctx, chromedp.Evaluate(chromeAppearanceErrorJS, &toast))
	if strings.TrimSpace(toast) != "" {
		fmt.Println("Tumblr:", strings.TrimSpace(toast))
		return ""
	}
	var hit string
	_ = chromedp.Run(ctx, chromedp.Evaluate(chromeSaveAppearanceJS, &hit))
	time.Sleep(2 * time.Second)
	toast = ""
	_ = chromedp.Run(ctx, chromedp.Evaluate(chromeAppearanceErrorJS, &toast))
	if strings.TrimSpace(toast) != "" {
		fmt.Println("Tumblr:", strings.TrimSpace(toast))
	}
	return strings.TrimSpace(hit)
}

func setLastFileInput(ctx context.Context, imagePath string) error {
	return setFileInputAt(ctx, imagePath, `(//input[@type="file"])[last()]`)
}

func setFirstFileInput(ctx context.Context, imagePath string) error {
	return setFileInputAt(ctx, imagePath, `(//input[@type="file"])[1]`)
}

func setFileInputAt(ctx context.Context, imagePath, sel string) error {
	var n int64
	if err := chromedp.Evaluate(`document.querySelectorAll('input[type=file]').length`, &n).Do(ctx); err != nil {
		return err
	}
	if n == 0 {
		return fmt.Errorf("no file input")
	}
	return chromedp.SetUploadFiles(sel, []string{imagePath}, chromedp.BySearch).Do(ctx)
}

func applyBirthdayAndAgeKeyInChrome(cfg profileSetupConfig) error {
	ctx, cancel, err := withBotChrome(120 * time.Second)
	if err != nil {
		return err
	}
	defer cancel()

	var href, bday string
	err = chromedp.Run(ctx,
		chromedp.ActionFunc(func(ctx context.Context) error {
			return chromedpNavigateLoggedIn(ctx, "https://www.tumblr.com/settings/account")
		}),
		chromedp.WaitReady("body", chromedp.ByQuery),
		chromedp.Sleep(1*time.Second),
		chromedp.Location(&href),
		chromedp.ActionFunc(func(ctx context.Context) error {
			if tumblrLoginURL(href) {
				return fmt.Errorf("still on login page")
			}
			dismissChromeNags(ctx)
			body := chromePageText(ctx)
			if strings.Contains(body, "December 29, 2000") || strings.Contains(body, "29 December 2000") || strings.Contains(body, "декабря 2000") {
				bday = "already-set"
				fmt.Println("Birthday already on file.")
			}
			return nil
		}),
		chromedp.ActionFunc(func(ctx context.Context) error {
			if bday == "already-set" {
				return nil
			}
			return chromedp.Evaluate(chromeSetBirthdayFieldsJS(cfg.BirthdayYear, cfg.BirthdayMonth, cfg.BirthdayDay), &bday).Do(ctx)
		}),
		chromedp.Sleep(1200*time.Millisecond),
		chromedp.ActionFunc(func(ctx context.Context) error {
			if bday == "already-set" {
				return nil
			}
			var saved string
			_ = chromedp.Evaluate(`(() => {
				const vis = (el) => el && el.getBoundingClientRect().width > 2 && el.getBoundingClientRect().height > 2;
				const save = [...document.querySelectorAll('button')].find(b => vis(b) && /^(save|guardar|enregistrer|speichern|сохранить)$/i.test((b.innerText || '').trim()));
				if (!save) return '';
				save.click();
				return 'save';
			})()`, &saved).Do(ctx)
			if saved != "" {
				fmt.Println("Birthday:", saved)
			}
			return nil
		}),
		chromedp.Sleep(1500*time.Millisecond),
		chromedp.ActionFunc(func(ctx context.Context) error {
			if bday == "already-set" {
				return nil
			}
			for i := 0; i < 10; i++ {
				if confirmBirthdayDialog(ctx) {
					bday = bday + ",confirmed"
					time.Sleep(800 * time.Millisecond)
					_ = confirmBirthdayDialog(ctx)
					return nil
				}
				time.Sleep(500 * time.Millisecond)
			}
			return nil
		}),
		chromedp.Sleep(1500*time.Millisecond),
	)
	if err != nil {
		return err
	}
	if bday != "" {
		fmt.Println("Birthday:", bday)
	}
	return nil
}

func applyAgeKeyInChrome() error {
	ctx, cancel, err := withBotChrome(120 * time.Second)
	if err != nil {
		return err
	}
	defer cancel()

	var href, verify string
	err = chromedp.Run(ctx,
		chromedp.ActionFunc(func(ctx context.Context) error {
			return chromedpNavigateLoggedIn(ctx, "https://www.tumblr.com/settings/account")
		}),
		chromedp.WaitReady("body", chromedp.ByQuery),
		chromedp.Sleep(1*time.Second),
		chromedp.Location(&href),
		chromedp.ActionFunc(func(ctx context.Context) error {
			if tumblrLoginURL(href) {
				return fmt.Errorf("still on login page")
			}
			dismissChromeNags(ctx)
			body := chromePageText(ctx)
			var st setupPageStatus
			var raw string
			if chromedp.Evaluate(chromeAgeStatusJS, &raw).Do(ctx) == nil {
				_ = json.Unmarshal([]byte(raw), &st)
			}
			if st.Verified && !st.VerifyAge {
				verify = "already-verified"
				setupAgeVerified = true
				fmt.Println("Age group already verified.")
				return nil
			}
			if strings.Contains(body, "Age Group") && strings.Contains(body, "Verified") && !st.VerifyAge && !strings.Contains(body, "Verify Age") && !strings.Contains(body, "Подтвердить возраст") {
				verify = "already-verified"
				setupAgeVerified = true
				fmt.Println("Age group already verified.")
				return nil
			}
			if hit := clickVerifyAge(ctx); hit != "" {
				fmt.Println("Clicked:", hit)
				verify = hit
			}
			deadline := time.Now().Add(14 * time.Second)
			for time.Now().Before(deadline) {
				if hit := clickUseAgeKey(ctx); hit != "" {
					verify = hit
					fmt.Println("Clicked AgeKey:", hit)
					if !waitForChromeTargetHost("agekey.org", 18*time.Second) {
						fmt.Println("AgeKey page did not open — pick Use your AgeKey, then Already have an AgeKey? in Chrome.")
						saveChromeShot(ctx, "setup-agekey.png")
						return nil
					}
					time.Sleep(1500 * time.Millisecond)
					if have := clickAlreadyHaveAgeKey(ctx); have != "" {
						fmt.Println("Clicked:", have)
						ageKeyHaveClicked = true
					} else {
						fmt.Println("Could not click Already have an AgeKey? — tap it in Chrome.")
					}
					saveChromeShot(ctx, "setup-agekey.png")
					return nil
				}
				time.Sleep(400 * time.Millisecond)
			}
			dumpChromeControls(ctx, "age verify")
			return nil
		}),
	)
	if err != nil {
		return err
	}
	switch {
	case verify == "already-verified":
		fmt.Println("Age already verified on this account.")
	case strings.Contains(strings.ToLower(verify), "agekey"):
		fmt.Println("AgeKey should be open in Chrome — finish it there.")
	case verify == "":
		fmt.Println("Could not find AgeKey automatically — pick Use your AgeKey in Chrome.")
	default:
		fmt.Println("Opened age verify:", verify)
	}
	return nil
}

type setupPageStatus struct {
	Href            string `json:"href"`
	Birthday        string `json:"birthday"`
	Verified        bool   `json:"verified"`
	VerifyAge       bool   `json:"verifyAge"`
	AgeKeyFrame     bool   `json:"ageKeyFrame"`
	VerifyPicker    bool   `json:"verifyPicker"`
	BirthdayConfirm bool   `json:"birthdayConfirm"`
	Leak            bool   `json:"leak"`
	CookieBanner    bool   `json:"cookieBanner"`
	CookiesOK       bool   `json:"cookiesOK"`
}

func readSetupPageStatus() setupPageStatus {
	t, ok := tumblrSettingsAccountTarget()
	if !ok {
		t, ok = tumblrPageTarget()
	}
	if !ok {
		return setupPageStatus{}
	}
	var raw string
	if err := evalJSONOnChromeTarget(t, chromeAgeStatusJS, &raw); err != nil {
		return setupPageStatus{Href: t.URL}
	}
	var st setupPageStatus
	if json.Unmarshal([]byte(raw), &st) != nil {
		st.Href = t.URL
		st.Birthday = "unset"
	}
	return st
}

func nudgeSetupAccount(st setupPageStatus) setupPageStatus {
	if ageKeyHandsOff() {
		return st
	}
	if nag := dismissChromeLeakDialog(); nagWasDismissed(nag) {
		fmt.Println("Dismissed:", nag)
	}
	if hit := clickPrivacyConsentAgree(); hit != "" {
		fmt.Println("Dismissed cookies:", hit)
		time.Sleep(800 * time.Millisecond)
		return readSetupPageStatus()
	}
	if onTumblrConsentPage() {
		dismissOverlaysOnPort()
		return readSetupPageStatus()
	}
	dismissOverlaysOnPort()
	if nag := dismissSavePasswordBubble(); nagWasDismissed(nag) {
		fmt.Println("Dismissed:", nag)
	}
	ageDone := sessionAgeOK(st)
	ageKeyAuth := chromeHasTargetHost("agekey.org") || chromeHasTargetHost("agekey.com")
	pickerOpen := st.VerifyPicker || st.AgeKeyFrame || chromeHasTargetHost("k-id.com")
	t, ok := tumblrSettingsAccountTarget()
	if !ok {
		t, ok = tumblrPageTarget()
	}
	onAccount := ok && strings.Contains(t.URL, "/settings/account")
	if tumblrLoginURL(t.URL) || onTumblrLoginPage() {
		recoverSetupLogin()
		return readSetupPageStatus()
	}
	if !onAccount && !ageKeyAuth && !pickerOpen {
		if opened, err := openOrNavigateTab("https://www.tumblr.com/settings/account"); err == nil {
			t = opened
			time.Sleep(2 * time.Second)
		}
		st = readSetupPageStatus()
		t, ok = tumblrSettingsAccountTarget()
		pickerOpen = st.VerifyPicker || st.AgeKeyFrame || chromeHasTargetHost("k-id.com")
	}
	if !ageDone && pickerOpen && !ageKeyAuth {
		if hit := clickAgeKeyCardOnTumblrPage(); hit != "" {
			fmt.Println("Clicked AgeKey:", hit)
			time.Sleep(800 * time.Millisecond)
		} else if hit := clickUseAgeKeyOnKidTarget(); hit != "" {
			fmt.Println("Clicked AgeKey:", hit)
			time.Sleep(800 * time.Millisecond)
		}
		return readSetupPageStatus()
	}
	if ok && st.Birthday == "unset" && !ageKeyAuth && !pickerOpen {
		_ = applyBirthdayOnPort(t)
		st = readSetupPageStatus()
	}
	if !ageKeyAuth && !pickerOpen && (st.BirthdayConfirm || st.Birthday == "unset") {
		_ = clickAgeConfirmOnTargets()
		st = readSetupPageStatus()
	}
	if !ageDone && st.VerifyAge && !ageKeyAuth && !pickerOpen && t.ID != "" {
		var hit string
		_ = evalJSONOnChromeTarget(t, chromeClickVerifyAgeJS, &hit)
		if hit != "" {
			fmt.Println("Clicked:", hit)
			time.Sleep(1200 * time.Millisecond)
		}
	}
	if !ageDone && !ageKeyAuth {
		if hit := clickAgeKeyCardOnTumblrPage(); hit != "" {
			fmt.Println("Clicked AgeKey:", hit)
			time.Sleep(800 * time.Millisecond)
		} else if hit := clickUseAgeKeyOnKidTarget(); hit != "" {
			fmt.Println("Clicked AgeKey:", hit)
			time.Sleep(800 * time.Millisecond)
		}
	}
	if !ageDone && ageKeyAuth {
		if !ageKeyHaveClicked {
			if have := clickHaveAgeKeyOnKidTarget(); have != "" {
				fmt.Println("Clicked:", have)
				ageKeyHaveClicked = true
				time.Sleep(800 * time.Millisecond)
				if osHit := pickAgeKeyPasskeySoon(); osHit != "" && osHit != "pin-prompt" {
					fmt.Println("Selected passkey:", osHit)
				}
			}
		}
		if listed := clickListedAgeKeyOnKidTarget(); listed != "" {
			fmt.Println("Clicked AgeKey:", listed)
			time.Sleep(800 * time.Millisecond)
		}
	}
	return readSetupPageStatus()
}

func nudgeMatureToggles(t chromeJSONTarget) {
	var result string
	if err := evalJSONOnChromeTarget(t, chromeSetMatureTogglesJS, &result); err != nil || result == "" {
		return
	}
	if strings.Contains(result, ":on") || strings.Contains(result, ":off") || strings.Contains(result, "saved") {
		fmt.Println("Mature content:", result)
	}
}

func drainSetupSkip() {
	for {
		select {
		case <-setupSkipCh:
		default:
			return
		}
	}
}

func waitUntilAgeVerified() error {
	if sessionAgeOK(readSetupPageStatus()) {
		setupAgeVerified = true
		setupSay("Age already verified — continuing to mature content and appearance.")
		return nil
	}
	return waitSetupChecks(false)
}

func waitUntilSetupReady() error {
	return waitSetupChecks(true)
}

func setupChromeStillOpen() bool {
	return liveDebugPort != "" && portAlive(liveDebugPort)
}

func waitSetupChecks(needLook bool) error {
	drainSetupSkip()
	if needLook {
		setupSay("Chrome stays open. Type the AgeKey passcode in Windows Security if it appears. I will not enter the PIN.")
		setupSay("Setup continues on its own when name, bio, avatar, birthday, cookies, and AgeKey are set. Skip from the GUI to go to the next account.")
	} else {
		setupSay("Chrome stays open. Type the AgeKey passcode in Windows Security if it appears. I will not enter the PIN.")
		setupSay("Chrome stays open for AgeKey. Type the passcode in Windows Security if it appears. I will not enter the PIN.")
		setupSay("When AgeKey is done I turn mature content on again, then this account is finished. Skip from the GUI to go to the next account.")
	}
	closeExtraSetupTabs()

	enterCh := make(chan struct{}, 4)
	if stdinIsConsole() {
		go func() {
			r := bufio.NewReader(os.Stdin)
			for {
				_, err := r.ReadString('\n')
				if err != nil {
					return
				}
				select {
				case enterCh <- struct{}{}:
				default:
				}
			}
		}()
	}

	ignoreEnterUntil := time.Now().Add(800 * time.Millisecond)
	pinSeen := false
	matureTried := false
	var lastLookCheck, lastLookRetry, readySince, lastPasskeyHint, lastOverlayDismiss time.Time
	var last string
	for {
		if nag := dismissChromeLeakDialog(); nagWasDismissed(nag) {
			setupSay("Dismissed: %s", nag)
		}
		if onTumblrLoginPage() {
			recoverSetupLogin()
		}
		handsOff := ageKeyHandsOff()
		if findWindowsSecurityHWND() != 0 {
			if securityDialogIsPIN() {
				if !pinSeen {
					setupSay("Windows Security wants the PIN — type it yourself. I will not enter it or move the mouse.")
				}
				pinSeen = true
			} else if lastPasskeyHint.IsZero() || time.Since(lastPasskeyHint) > 6*time.Second {
				lastPasskeyHint = time.Now()
				setupSay("Windows Security is open — click AgeKey 19/09/2026 yourself. I will not move the mouse or type the PIN.")
			}
			handsOff = true
		}
		st := readSetupPageStatus()
		if st.Verified {
			setupAgeVerified = true
		}
		// PIN dialog closing is not enough — Tumblr must show Verified (or unlock
		// mature toggles) before we treat AgeKey as done and click Show mature.
		ageDone := sessionAgeOK(st)
		if !ageDone {
			if listed := clickListedAgeKeyOnKidTarget(); listed != "" {
				setupSay("Clicked listed AgeKey %s.", listed)
			}
		}
		if !handsOff {
			if lastOverlayDismiss.IsZero() || time.Since(lastOverlayDismiss) > 1500*time.Millisecond {
				lastOverlayDismiss = time.Now()
				dismissOverlaysOnPort()
			}
			closeExtraSetupTabs()
			st = nudgeSetupAccount(st)
			if st.CookieBanner || cookieBannerOpen() {
				st.CookieBanner = true
				st.CookiesOK = false
				dismissOverlaysOnPort()
			}
			if (st.Leak || leakWindowPresent()) && findWindowsSecurityHWND() == 0 {
				if nag := dismissChromeLeakDialog(); nagWasDismissed(nag) {
					fmt.Println("Dismissed:", nag)
				}
			}
		}
		var look setupLookStatus
		if needLook {
			look = currentLookStatus(setupBlogName)
		}
		ageBusy := !ageDone && (handsOff || st.VerifyPicker || st.AgeKeyFrame || chromeHasTargetHost("k-id.com") || chromeHasTargetHost("agekey.org") || chromeHasTargetHost("agekey.com"))
		if !ageDone && !handsOff && !chromeHasTargetHost("agekey.org") && !chromeHasTargetHost("agekey.com") &&
			(st.VerifyPicker || st.AgeKeyFrame || chromeHasTargetHost("k-id.com")) {
			if hit := clickAgeKeyCardOnTumblrPage(); hit != "" {
				fmt.Println("Clicked AgeKey:", hit)
				time.Sleep(800 * time.Millisecond)
			}
		}
		if needLook && !ageBusy && setupBlogName != "" && (lastLookCheck.IsZero() || time.Since(lastLookCheck) > 4*time.Second) {
			lastLookCheck = time.Now()
			closeExtraSetupTabs()
			if !look.Complete() && (lastLookRetry.IsZero() || time.Since(lastLookRetry) > 8*time.Second) {
				lastLookRetry = time.Now()
				if appearanceEditorOpenOnPort(liveDebugPort) {
					fmt.Printf("Look not complete yet (name=%s bio=%s avatar=%s) — clicking Save.\n",
						setupFlag(look.TitleOK), setupFlag(look.BioOK), setupFlag(look.AvatarOK))
					if err := clickAppearanceSaveOnPort(liveDebugPort); err != nil {
						fmt.Println("Save:", err)
					}
				} else {
					fmt.Printf("Look not complete yet (name=%s bio=%s avatar=%s) — applying on this tab.\n",
						setupFlag(look.TitleOK), setupFlag(look.BioOK), setupFlag(look.AvatarOK))
					_ = applyBlogLookOnPort(liveDebugPort)
				}
				look = currentLookStatus(setupBlogName)
			}
		}
		cookiesOK := true
		if st.CookieBanner && cookieBannerOpen() {
			cookiesOK = false
		}
		cl := setupChecklist{
			TitleOK:    look.TitleOK,
			BioOK:      look.BioOK,
			AvatarOK:   look.AvatarOK,
			BirthdayOK: sessionBirthdayOK(st),
			CookiesOK:  cookiesOK,
			AgeOK:      sessionAgeOK(st),
		}
		if needLook && st.Verified && !matureTried && !handsOff {
			matureTried = true
			if err := applyMatureContentOnPort(liveDebugPort); err != nil {
				setupSay("Mature content: %s", err)
				matureTried = false
			}
		}
		if cl.BirthdayOK {
			setupBirthdayApplied = true
		}
		if cl.AgeOK {
			setupAgeVerified = true
			cl.AgeOK = true
		}
		ready := cl.BirthdayOK && cl.CookiesOK && cl.AgeOK
		if needLook {
			ready = ready && cl.Ready()
		}
		if needLook {
			sig := fmt.Sprintf("%v|%v|%v|%v|%v|%v", cl.TitleOK, cl.BioOK, cl.AvatarOK, cl.BirthdayOK, cl.CookiesOK, cl.AgeOK)
			if sig != last {
				setupSay("Setup check: name=%s bio=%s avatar=%s birthday=%s cookies=%s age=%s",
					setupFlag(cl.TitleOK), setupFlag(cl.BioOK), setupFlag(cl.AvatarOK),
					setupFlag(cl.BirthdayOK), setupFlag(cl.CookiesOK), setupFlag(cl.AgeOK))
				last = sig
			}
		} else {
			sig := fmt.Sprintf("%v|%v|%v", cl.BirthdayOK, cl.CookiesOK, cl.AgeOK)
			if sig != last {
				setupSay("Setup check: birthday=%s cookies=%s age=%s",
					setupFlag(cl.BirthdayOK), setupFlag(cl.CookiesOK), setupFlag(cl.AgeOK))
				last = sig
			}
		}
		if ready {
			if readySince.IsZero() {
				readySince = time.Now()
			}
			if time.Since(readySince) >= 1500*time.Millisecond {
				if needLook {
					setupSay("All setup checks passed — next account.")
				} else {
					setupSay("Birthday and AgeKey are set — continuing in this Chrome.")
				}
				closeExtraSetupTabs()
				return nil
			}
		} else {
			readySince = time.Time{}
		}
		select {
		case <-enterCh:
			if time.Now().Before(ignoreEnterUntil) {
				break
			}
			setupSkipped = true
			setupSay("Next account.")
			return nil
		case <-setupSkipCh:
			setupSkipped = true
			setupSay("Skip from GUI — next account.")
			return nil
		default:
		}
		if !setupChromeStillOpen() {
			setupSay("Chrome closed before setup finished.")
			return fmt.Errorf("chrome closed before setup finished")
		}
		time.Sleep(400 * time.Millisecond)
	}
}

func applyMatureContentInChrome() error {
	return applyMatureContentOnPort(liveDebugPort)
}

type matureToggleStatus struct {
	State string `json:"state"`
	On    bool   `json:"on"`
}

func readMatureToggleStatus() matureToggleStatus {
	t, err := firstTumblrPageTarget()
	if err != nil {
		return matureToggleStatus{State: "missing"}
	}
	var st matureToggleStatus
	if err := evalJSONOnChromeTarget(t, chromeMatureToggleStatusJS, &st); err != nil {
		return matureToggleStatus{State: "missing"}
	}
	if st.State == "" {
		st.State = "missing"
	}
	return st
}

func matureShowSucceeded(result string) bool {
	return strings.Contains(result, "show:on") || strings.Contains(result, "show:already")
}

// waitForMatureContentReady polls account settings until Tumblr unlocks the
// Show mature content control. AgeKey PIN alone is not enough — the toggle
// stays missing/disabled until verification lands on the account.
func waitForMatureContentReady() error {
	deadline := time.Now().Add(3 * time.Minute)
	var last string
	navigated := false
	for time.Now().Before(deadline) {
		if !setupChromeStillOpen() {
			return fmt.Errorf("chrome closed before mature content unlocked")
		}
		if findWindowsSecurityHWND() != 0 ||
			chromeHasTargetHost("agekey.org") || chromeHasTargetHost("agekey.com") || chromeHasTargetHost("k-id.com") {
			time.Sleep(500 * time.Millisecond)
			continue
		}
		if !navigated {
			if _, err := openOrNavigateTab("https://www.tumblr.com/settings/account"); err != nil {
				time.Sleep(800 * time.Millisecond)
				continue
			}
			navigated = true
			time.Sleep(900 * time.Millisecond)
		}
		st := readSetupPageStatus()
		if st.Verified {
			setupAgeVerified = true
		}
		mt := readMatureToggleStatus()
		sig := mt.State
		if sig != last {
			setupSay("Mature unlock: show toggle is %s (AgeKey verified=%s)", mt.State, setupFlag(st.Verified || setupAgeVerified))
			last = sig
		}
		if mt.State == "ready" {
			setupAgeVerified = true
			setupSay("Show mature content is unlocked — clicking it next.")
			return nil
		}
		if st.Verified && (mt.State == "missing" || mt.State == "disabled") {
			// Verified but toggle not visible yet — refresh account settings once.
			navigated = false
		}
		time.Sleep(700 * time.Millisecond)
	}
	mt := readMatureToggleStatus()
	return fmt.Errorf("mature content still %s after AgeKey wait", mt.State)
}

func applyMatureContentOnPort(port string) error {
	port = strings.TrimSpace(port)
	if port == "" {
		port = currentDebugPort()
	}
	if port == "" {
		return fmt.Errorf("no debug port")
	}
	liveDebugPort = port
	pages := []string{
		"https://www.tumblr.com/settings/account",
		"https://www.tumblr.com/settings/dashboard",
	}
	var result string
	for attempt := 0; attempt < 6; attempt++ {
		for _, page := range pages {
			t, err := openOrNavigateTab(page)
			if err != nil {
				continue
			}
			focusChromeTarget(t)
			time.Sleep(900 * time.Millisecond)
			var dismiss string
			_ = evalJSONOnChromeTarget(t, chromeDismissOverlaysJS, &dismiss)
			time.Sleep(250 * time.Millisecond)
			mt := matureToggleStatus{}
			_ = evalJSONOnChromeTarget(t, chromeMatureToggleStatusJS, &mt)
			if mt.State == "missing" || mt.State == "disabled" {
				result = "show:" + mt.State
				setupSay("Mature content not ready yet (%s) — AgeKey must finish first.", result)
				continue
			}
			if err := evalJSONOnChromeTarget(t, chromeSetMatureTogglesJS, &result); err != nil {
				return err
			}
			fmt.Println("Mature content:", page, result)
			setupSay("Mature content: %s", result)
			if matureShowSucceeded(result) {
				break
			}
			// Click registered but toggle did not stick — wait and retry.
			if strings.Contains(result, "show:clicked") {
				time.Sleep(1200 * time.Millisecond)
				_ = evalJSONOnChromeTarget(t, chromeMatureToggleStatusJS, &mt)
				if mt.State == "ready" && mt.On {
					result = "show:on"
					setupSay("Mature content: show:on (confirmed after click)")
					break
				}
			}
		}
		if matureShowSucceeded(result) {
			break
		}
		if attempt >= 1 && (strings.Contains(result, "missing") || strings.Contains(result, "disabled")) {
			break
		}
		time.Sleep(1500 * time.Millisecond)
	}
	if !matureShowSucceeded(result) {
		if strings.Contains(result, "show:missing") || strings.Contains(result, "show:disabled") || result == "" {
			return fmt.Errorf("could not find mature content switches (%s)", result)
		}
		return fmt.Errorf("show mature did not turn on (%s)", result)
	}
	_, _ = openOrNavigateTab("https://www.tumblr.com/settings/account")
	if cookies, err := cookiesFromDebugPort(); err == nil && cookieValue(cookies, "sid") != "" {
		auth, loadErr := loadAuth()
		if loadErr == nil {
			auth.Cookie = buildTumblrCookieHeader(cookies)
			persistMaturePref(&auth)
		}
	}
	return nil
}

type ageConfirmHit struct {
	T string  `json:"t"`
	X float64 `json:"x"`
	Y float64 `json:"y"`
}

func clickAgeConfirmOnPort(port string) error {
	port = strings.TrimSpace(port)
	if port == "" {
		port = currentDebugPort()
	}
	if port == "" {
		return fmt.Errorf("no debug port")
	}
	liveDebugPort = port
	t, ok := tumblrSettingsAccountTarget()
	if !ok {
		return fmt.Errorf("no tumblr tab")
	}
	if tumblrLoginURL(t.URL) || onTumblrLoginPage() {
		recoverSetupLogin()
		t, ok = tumblrSettingsAccountTarget()
		if !ok {
			return fmt.Errorf("no tumblr tab")
		}
	}
	if !strings.Contains(t.URL, "/settings/account") {
		if err := navigateChromeTab(t, "https://www.tumblr.com/settings/account"); err != nil {
			return err
		}
		time.Sleep(2500 * time.Millisecond)
		t, ok = tumblrSettingsAccountTarget()
		if !ok {
			return fmt.Errorf("settings tab gone")
		}
	}
	var status string
	_ = evalJSONOnChromeTarget(t, chromeAgeStatusJS, &status)
	fmt.Println("Age status:", status)
	saveTargetShot(t, "setup-age-confirm.png")
	clicked := clickAgeConfirmOnTargets()
	if clicked == "" {
		fmt.Println("No Okay/Accept dialog yet.")
	}
	time.Sleep(800 * time.Millisecond)
	status = ""
	_ = evalJSONOnChromeTarget(t, chromeAgeStatusJS, &status)
	fmt.Println("Age status:", status)
	if strings.Contains(status, `"birthday":"unset"`) {
		fmt.Println("Birthday not saved — setting it again.")
		if err := navigateChromeTab(t, "https://www.tumblr.com/settings/account"); err != nil {
			return err
		}
		time.Sleep(2500 * time.Millisecond)
		t, ok = tumblrSettingsAccountTarget()
		if !ok {
			return fmt.Errorf("settings tab gone")
		}
		if err := applyBirthdayOnPort(t); err != nil {
			return err
		}
		time.Sleep(800 * time.Millisecond)
		clickAgeConfirmOnTargets()
		time.Sleep(800 * time.Millisecond)
		status = ""
		_ = evalJSONOnChromeTarget(t, chromeAgeStatusJS, &status)
		fmt.Println("Age status:", status)
	}
	if strings.Contains(status, `"birthday":"unset"`) {
		return fmt.Errorf("birthday still not saved")
	}
	return nil
}

func clickAgeConfirmOnTargets() string {
	list, err := listChromeTargets()
	if err != nil {
		return ""
	}
	var targets []chromeJSONTarget
	if t, ok := tumblrSettingsAccountTarget(); ok {
		targets = append(targets, t)
	}
	targets = append(targets, kidOrAgeKeyTargets(list)...)
	seen := map[string]bool{}
	for _, t := range targets {
		if t.ID == "" || seen[t.ID] {
			continue
		}
		seen[t.ID] = true
		var hit ageConfirmHit
		if err := evalJSONOnChromeTarget(t, chromeClickAgeConfirmJS, &hit); err != nil {
			continue
		}
		if hit.X > 1 && hit.Y > 1 {
			_ = mouseClickOnChromeTarget(t, hit.X, hit.Y)
		}
		if hit.T != "" {
			fmt.Println("Clicked:", hit.T, "on", chromeTargetHost(t.URL))
			return hit.T
		}
	}
	return ""
}

func finishAccountSettings() error {
	auth, err := loadAuth()
	if err != nil {
		return err
	}
	fmt.Println("Finishing English + mature content…")
	cmd, err := startChromeLoggedIn(&auth)
	if err != nil {
		return err
	}
	defer func() {
		stopChrome(cmd)
		time.Sleep(2 * time.Second)
	}()
	dismissOverlaysOnPort()
	if err := applyEnglishOnPort(); err != nil {
		fmt.Println("Language:", err)
	}
	if err := applyMatureContentOnPort(liveDebugPort); err != nil {
		fmt.Println("Mature content:", err)
	}
	if cookies, err := cookiesFromDebugPort(); err == nil && cookieValue(cookies, "sid") != "" {
		auth.Cookie = buildTumblrCookieHeader(cookies)
		persistMaturePref(&auth)
		fmt.Println("Saved session cookies.")
	}
	return nil
}

func applyEnglishOnPort() error {
	t, ok := tumblrSettingsAccountTarget()
	if !ok || !strings.Contains(t.URL, "/settings/account") {
		opened, err := openOrNavigateTab("https://www.tumblr.com/settings/account")
		if err != nil {
			return err
		}
		t = opened
		time.Sleep(1200 * time.Millisecond)
	}
	focusChromeTarget(t)
	dismissOverlaysOnPort()
	var out string
	if err := evalPromiseOnChromeTarget(t, chromeSetEnglishJS, &out); err != nil {
		return err
	}
	setupSay("Language: %s", out)
	if strings.Contains(out, "saved") || strings.Contains(out, "english") {
		time.Sleep(1200 * time.Millisecond)
	}
	return nil
}

func waitForAccountSettingsPage() (chromeJSONTarget, error) {
	t, err := openOrNavigateTab("https://www.tumblr.com/settings/account")
	if err != nil {
		return t, err
	}
	focusChromeTarget(t)
	deadline := time.Now().Add(25 * time.Second)
	for time.Now().Before(deadline) {
		if nag := dismissSavePasswordBubble(); nagWasDismissed(nag) {
			fmt.Println("Dismissed:", nag)
		}
		if onTumblrLoginPage() {
			recoverSetupLogin()
			if nt, ok := tumblrSettingsAccountTarget(); ok {
				t = nt
			}
			time.Sleep(500 * time.Millisecond)
			continue
		}
		var text string
		_ = evalJSONOnChromeTarget(t, `(() => (document.body && document.body.innerText || '').slice(0, 4000))()`, &text)
		if looksLikeTumblrLoginPage(t.URL, text) {
			recoverSetupLogin()
			time.Sleep(500 * time.Millisecond)
			if nt, ok := tumblrSettingsAccountTarget(); ok {
				t = nt
			}
			continue
		}
		low := strings.ToLower(text)
		if strings.Contains(low, "birthday") || strings.Contains(low, "language") || strings.Contains(low, "fecha de nacimiento") || strings.Contains(low, "anniversaire") || (strings.Contains(low, "age group") && strings.Contains(low, "account")) {
			time.Sleep(600 * time.Millisecond)
			if nt, ok := tumblrSettingsAccountTarget(); ok {
				return nt, nil
			}
			return t, nil
		}
		time.Sleep(400 * time.Millisecond)
		if nt, ok := tumblrSettingsAccountTarget(); ok {
			t = nt
		}
	}
	return t, fmt.Errorf("settings page did not finish loading")
}

func applyBirthdayOnSettings() error {
	t, ok := tumblrSettingsAccountTarget()
	if !ok || !strings.Contains(t.URL, "/settings/account") {
		var err error
		t, err = waitForAccountSettingsPage()
		if err != nil && t.ID == "" {
			return err
		}
	}
	focusChromeTarget(t)
	dismissOverlaysOnPort()
	return applyBirthdayOnPort(t)
}

func applyBirthdayOnPort(t chromeJSONTarget) error {
	cfg := loadProfileSetupConfig()
	var out string
	if err := evalPromiseOnChromeTarget(t, chromeSetBirthdayAsyncJS(cfg.BirthdayYear, cfg.BirthdayMonth, cfg.BirthdayDay), &out); err != nil {
		if err2 := evalJSONOnChromeTarget(t, chromeSetBirthdayFieldsJS(cfg.BirthdayYear, cfg.BirthdayMonth, cfg.BirthdayDay), &out); err2 != nil {
			return err
		}
	}
	setupSay("Birthday fields: %s", out)
	if strings.Contains(out, "locked") {
		setupSay("Birthday dropdowns are disabled — Tumblr already locked the date.")
		return nil
	}
	if out == "" || out == "no-fields" || strings.Contains(out, "-open") {
		pickCustomBirthdayOnTarget(t, cfg)
		time.Sleep(400 * time.Millisecond)
	}
	time.Sleep(800 * time.Millisecond)
	saveJS := `(() => {
		const vis = (el) => el && el.getBoundingClientRect().width > 2 && el.getBoundingClientRect().height > 2;
		const save = [...document.querySelectorAll('button')].find(b => vis(b) && /^(save|guardar|enregistrer|speichern|сохранить)$/i.test((b.innerText || '').trim()));
		if (!save) return 'no-save';
		if (save.disabled || save.getAttribute('aria-disabled') === 'true') return 'save-disabled';
		save.click();
		return 'save';
	})()`
	var saved string
	_ = evalJSONOnChromeTarget(t, saveJS, &saved)
	if saved != "save" {
		setupSay("Birthday Save not ready (%s) — picking 29 Dec 2000 again.", saved)
		pickCustomBirthdayOnTarget(t, cfg)
		time.Sleep(600 * time.Millisecond)
		saved = ""
		_ = evalJSONOnChromeTarget(t, saveJS, &saved)
	}
	setupSay("Birthday: %s", saved)
	if saved != "save" {
		// Tumblr often hides Save when the date is already locked/set, or when
		// only custom pickers opened without a page-level Save button.
		var status string
		_ = evalJSONOnChromeTarget(t, chromeAgeStatusJS, &status)
		if strings.Contains(status, `"birthday":"set"`) || strings.Contains(status, `"birthday":"locked"`) {
			setupSay("Birthday already set/locked — continuing.")
			return nil
		}
		if strings.Contains(status, "December 29, 2000") || strings.Contains(status, "29 December 2000") {
			setupSay("Birthday text already shows 29 Dec 2000 — continuing.")
			return nil
		}
		return fmt.Errorf("birthday not saved (%s)", saved)
	}
	time.Sleep(1500 * time.Millisecond)
	for i := 0; i < 10; i++ {
		if clickAgeConfirmOnTargets() != "" {
			time.Sleep(800 * time.Millisecond)
			clickAgeConfirmOnTargets()
			return nil
		}
		time.Sleep(400 * time.Millisecond)
	}
	return nil
}

func pickCustomBirthdayOnTarget(t chromeJSONTarget, cfg profileSetupConfig) {
	month := "December"
	day := fmt.Sprintf("%d", cfg.BirthdayDay)
	year := fmt.Sprintf("%d", cfg.BirthdayYear)
	clicks := []string{
		chromeClickTextJS(`/^(month|mes|mois|monat|месяц)$/i`, 16),
		chromeClickTextJS(`/^`+month+`$|^dec$|^diciembre$|^décembre$/i`, 16),
		chromeClickTextJS(`/^(day|día|jour|tag|день)$/i`, 16),
		chromeClickTextJS(fmt.Sprintf(`/^%s$|^0%s$/`, day, day), 8),
		chromeClickTextJS(`/^(year|año|année|jahr|год)$/i`, 16),
		chromeClickTextJS(fmt.Sprintf(`/^%s$/`, year), 8),
	}
	var last string
	for _, js := range clicks {
		var hit string
		_ = evalJSONOnChromeTarget(t, js, &hit)
		if hit != "" {
			last = hit
			setupSay("Birthday pick: %s", hit)
		}
		time.Sleep(450 * time.Millisecond)
	}
	if last != "" {
		fmt.Println("Birthday custom pick:", last)
	}
}

func pickCustomBirthday(ctx context.Context, cfg profileSetupConfig) {
	month := "December"
	day := fmt.Sprintf("%d", cfg.BirthdayDay)
	year := fmt.Sprintf("%d", cfg.BirthdayYear)
	var hit string
	_ = chromedp.Evaluate(chromeClickTextJS(`/^month$/i`, 16), &hit).Do(ctx)
	time.Sleep(600 * time.Millisecond)
	_ = chromedp.Evaluate(chromeClickTextJS(`/^`+month+`$|^dec$/i`, 16), &hit).Do(ctx)
	time.Sleep(400 * time.Millisecond)
	_ = chromedp.Evaluate(chromeClickTextJS(`/^day$/i`, 16), &hit).Do(ctx)
	time.Sleep(600 * time.Millisecond)
	_ = chromedp.Evaluate(chromeClickTextJS(fmt.Sprintf(`/^%s$|^0%s$/`, day, day), 8), &hit).Do(ctx)
	time.Sleep(400 * time.Millisecond)
	_ = chromedp.Evaluate(chromeClickTextJS(`/^year$/i`, 16), &hit).Do(ctx)
	time.Sleep(600 * time.Millisecond)
	_ = chromedp.Evaluate(chromeClickTextJS(fmt.Sprintf(`/^%s$/`, year), 8), &hit).Do(ctx)
	time.Sleep(400 * time.Millisecond)
	_ = chromedp.Evaluate(chromeClickTextJS(`/^save$/i`, 16), &hit).Do(ctx)
	if hit != "" {
		fmt.Println("Birthday custom pick:", hit)
	}
}
