package main

import (
	"encoding/json"
	"fmt"
	"math/rand"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// Trail / rare-NSFW reblog policy: audience still comes from NSFW seeds
// (likes + follows on engagers), but the public reblog trail stays mostly
// soft (nature / animals / girly). NSFW reblogs are capped hard per account.

type trailReblogConfig struct {
	Enabled    bool     `json:"enabled"`
	Queries    []string `json:"queries"`
	SeedBlogs  []string `json:"seedBlogs"`
	PerSession int      `json:"perSession"`
	Chance     float64  `json:"chance"`
}

type nsfwReblogConfig struct {
	// MaxCount NSFW reblogs allowed inside WindowHours (per account).
	// Default: 1 per week (168h), chance-gated ("maybe").
	MaxCount    int     `json:"maxCount"`
	WindowHours int     `json:"windowHours"`
	Chance      float64 `json:"chance"`
	// SearchQueries used by the weekly session reblog (not engager posts).
	SearchQueries []string `json:"searchQueries,omitempty"`
}

type reblogLedgerEntry struct {
	At       string `json:"at"`
	Account  string `json:"account"`
	Blog     string `json:"blog"`
	PostID   string `json:"postId"`
	Category string `json:"category"` // trail_sfw | nsfw_rare | engager_legacy
	Source   string `json:"source,omitempty"`
	Summary  string `json:"summary,omitempty"`
	RunID    string `json:"runId,omitempty"`
}

const reblogLedgerFile = "reblog-ledger.jsonl"

func defaultTrailReblogConfig() trailReblogConfig {
	return trailReblogConfig{
		Enabled: true,
		Queries: []string{
			"cats", "kittens", "cute cats", "catblr",
			"nature", "forest", "flowers", "cottagecore",
			"soft girl", "girly aesthetic", "pink aesthetic",
			"coquette", "balletcore", "sunset", "ocean waves",
			"puppies", "bunnies", "cottage garden",
		},
		SeedBlogs:  nil,
		PerSession: 2,
		Chance:     0.85,
	}
}

func defaultNSFWReblogConfig() nsfwReblogConfig {
	return nsfwReblogConfig{
		MaxCount:    1,
		WindowHours: 168, // once a week
		Chance:      0.15, // maybe — rolled per reach session / engager attempt
		SearchQueries: []string{
			"nsfw", "lewd", "thirst", "smut", "gay nsfw",
		},
	}
}

const nsfwReblogWindowDefaultHours = 168

func reblogLedgerPath(accountID string) string {
	accountID = accountSlug(accountID)
	if accountID == "" {
		return filepath.Join("data", reblogLedgerFile)
	}
	return filepath.Join(profilesDataDir, accountID, reblogLedgerFile)
}

func appendReblogLedger(accountID string, e reblogLedgerEntry) {
	e.Account = accountSlug(accountID)
	if e.At == "" {
		e.At = time.Now().Format(time.RFC3339)
	}
	path := reblogLedgerPath(e.Account)
	_ = os.MkdirAll(filepath.Dir(path), 0o755)
	raw, err := json.Marshal(e)
	if err != nil {
		return
	}
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return
	}
	defer f.Close()
	_, _ = f.Write(append(raw, '\n'))
}

func loadReblogLedger(accountID string) []reblogLedgerEntry {
	raw, err := os.ReadFile(reblogLedgerPath(accountID))
	if err != nil {
		return nil
	}
	var out []reblogLedgerEntry
	for _, line := range strings.Split(string(raw), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		var e reblogLedgerEntry
		if json.Unmarshal([]byte(line), &e) == nil && e.PostID != "" {
			out = append(out, e)
		}
	}
	return out
}

func nsfwReblogsInWindow(accountID string, window time.Duration) int {
	cut := time.Now().Add(-window)
	n := 0
	for _, e := range loadReblogLedger(accountID) {
		if e.Category != "nsfw_rare" && e.Category != "engager_legacy" {
			continue
		}
		t, err := time.Parse(time.RFC3339, e.At)
		if err != nil || t.Before(cut) {
			continue
		}
		n++
	}
	return n
}

func canNSFWReblog(accountID string, cfg nsfwReblogConfig) bool {
	if cfg.MaxCount <= 0 {
		return false
	}
	hours := cfg.WindowHours
	if hours <= 0 {
		hours = nsfwReblogWindowDefaultHours
	}
	return nsfwReblogsInWindow(accountID, time.Duration(hours)*time.Hour) < cfg.MaxCount
}

func nsfwReblogCfgOrDefault(cfg nsfwReblogConfig) nsfwReblogConfig {
	if cfg.MaxCount == 0 && cfg.WindowHours == 0 && cfg.Chance == 0 && len(cfg.SearchQueries) == 0 {
		return defaultNSFWReblogConfig()
	}
	if cfg.WindowHours <= 0 {
		cfg.WindowHours = nsfwReblogWindowDefaultHours
	}
	if cfg.Chance < 0 {
		cfg.Chance = 0
	}
	if len(cfg.SearchQueries) == 0 {
		cfg.SearchQueries = defaultNSFWReblogConfig().SearchQueries
	}
	return cfg
}

func trailReblogCountSince(accountID string, since time.Time) int {
	n := 0
	for _, e := range loadReblogLedger(accountID) {
		if e.Category != "trail_sfw" {
			continue
		}
		t, err := time.Parse(time.RFC3339, e.At)
		if err != nil || t.Before(since) {
			continue
		}
		n++
	}
	return n
}

// maybeReblogEngagerPost: rare NSFW trail item from someone we engaged.
// Shares the weekly NSFW quota with maybeWeeklyNSFWReblog.
func maybeReblogEngagerPost(client *http.Client, auth *Auth, cfg FollowConfig, blog string, reblogged map[string]bool, id string, logFn func(string)) bool {
	nsfw := nsfwReblogCfgOrDefault(cfg.NSFWReblog)
	if !canNSFWReblog(id, nsfw) {
		accLog(id, logFn, "   skip NSFW reblog — weekly quota used (1/%dh)", nsfw.WindowHours)
		return false
	}
	chance := nsfw.Chance
	if chance <= 0 {
		chance = 0.15
	}
	if rand.Float64() >= chance {
		return false
	}
	n, err := reblogFromBlog(client, auth, blog, 1, reblogged)
	if err != nil || n <= 0 {
		return false
	}
	appendReblogLedger(id, reblogLedgerEntry{
		Blog:     normalizeBlogName(blog),
		PostID:   "engager:" + normalizeBlogName(blog) + ":" + time.Now().Format("20060102"),
		Category: "nsfw_rare",
		Source:   "engager:" + normalizeBlogName(blog),
		Summary:  "weekly NSFW reblog from engager",
		RunID:    reachRunID(id),
	})
	accLog(id, logFn, "   weekly NSFW reblog from %s (quota %d/%dh)", blog, nsfwReblogsInWindow(id, time.Duration(nsfw.WindowHours)*time.Hour), nsfw.WindowHours)
	return true
}

// maybeWeeklyNSFWReblog: once per week (tracked), maybe reblog one NSFW search post.
// Called once per reach session so the public trail occasionally looks thirsty.
func maybeWeeklyNSFWReblog(client *http.Client, auth *Auth, cfg FollowConfig, reblogged map[string]bool, id string, logFn func(string)) bool {
	nsfw := nsfwReblogCfgOrDefault(cfg.NSFWReblog)
	if nsfw.MaxCount <= 0 {
		return false
	}
	if !canNSFWReblog(id, nsfw) {
		accLog(id, logFn, "weekly NSFW reblog: skip — already used this week (%dh window)", nsfw.WindowHours)
		return false
	}
	chance := nsfw.Chance
	if chance <= 0 {
		chance = 0.15
	}
	if rand.Float64() >= chance {
		accLog(id, logFn, "weekly NSFW reblog: skipped this session (chance %.0f%%)", chance*100)
		return false
	}

	queries := append([]string{}, nsfw.SearchQueries...)
	if len(queries) == 0 {
		queries = defaultNSFWReblogConfig().SearchQueries
	}
	rand.Shuffle(len(queries), func(i, j int) { queries[i], queries[j] = queries[j], queries[i] })

	var posts []ScrapedPost
	for _, q := range queries {
		found, err := scrapeRecentPosts(client, *auth, q, 12)
		if err != nil || len(found) == 0 {
			found, err = scrapeTopPosts(client, *auth, q, 12)
		}
		if err != nil || len(found) == 0 {
			continue
		}
		posts = found
		accLog(id, logFn, "weekly NSFW reblog: search %q (%d posts)", q, len(found))
		break
	}
	if len(posts) == 0 {
		accLog(id, logFn, "weekly NSFW reblog: no posts found")
		return false
	}
	rand.Shuffle(len(posts), func(i, j int) { posts[i], posts[j] = posts[j], posts[i] })

	for _, p := range posts {
		if p.ID == "" || reblogged[p.ID] {
			continue
		}
		if p.ReblogKey == "" {
			if enriched, err := fetchSinglePost(client, *auth, p); err == nil {
				p = enriched
			}
		}
		if p.ReblogKey == "" {
			continue
		}
		accLog(id, logFn, "weekly NSFW reblog @%s — %s", p.Blog, truncate(p.Summary, 48))
		if err := reblogPost(client, *auth, p); err != nil {
			accLog(id, logFn, "  weekly NSFW reblog fail: %v", err)
			continue
		}
		reblogged[p.ID] = true
		appendReblogLedger(id, reblogLedgerEntry{
			Blog:     normalizeBlogName(p.Blog),
			PostID:   p.ID,
			Category: "nsfw_rare",
			Source:   "weekly_search",
			Summary:  truncate(p.Summary, 80),
			RunID:    reachRunID(id),
		})
		accLog(id, logFn, "weekly NSFW reblog OK — next eligible in ~%dh (ledger)", nsfw.WindowHours)
		return true
	}
	accLog(id, logFn, "weekly NSFW reblog: no usable post")
	return false
}

func reachRunID(accountID string) string {
	return accountSlug(accountID) + "-" + time.Now().Format("2006-01-02")
}

// runTrailReblogs pulls soft SFW posts and reblogs a few onto the account.
func runTrailReblogs(client *http.Client, auth *Auth, cfg FollowConfig, reblogged map[string]bool, id string, logFn func(string)) int {
	trail := cfg.TrailReblog
	if !trail.Enabled && len(trail.Queries) == 0 && len(trail.SeedBlogs) == 0 {
		trail = defaultTrailReblogConfig()
	}
	if !trail.Enabled {
		return 0
	}
	per := trail.PerSession
	if per <= 0 {
		per = 2
	}
	chance := trail.Chance
	if chance <= 0 {
		chance = 0.85
	}
	if rand.Float64() > chance {
		accLog(id, logFn, "trail reblog: skipped this session (chance)")
		return 0
	}

	posts := gatherTrailPosts(client, *auth, trail, 12)
	if len(posts) == 0 {
		accLog(id, logFn, "trail reblog: no soft posts found")
		return 0
	}
	rand.Shuffle(len(posts), func(i, j int) { posts[i], posts[j] = posts[j], posts[i] })

	done := 0
	for _, p := range posts {
		if done >= per || cfg.timeUp() {
			break
		}
		if p.ID == "" || reblogged[p.ID] {
			continue
		}
		if p.ReblogKey == "" {
			if enriched, err := fetchSinglePost(client, *auth, p); err == nil {
				p = enriched
			}
		}
		if p.ReblogKey == "" {
			continue
		}
		accLog(id, logFn, "trail reblog @%s — %s", p.Blog, truncate(p.Summary, 48))
		if err := reblogPost(client, *auth, p); err != nil {
			accLog(id, logFn, "  trail reblog fail: %v", err)
			continue
		}
		reblogged[p.ID] = true
		appendReblogLedger(id, reblogLedgerEntry{
			Blog:     normalizeBlogName(p.Blog),
			PostID:   p.ID,
			Category: "trail_sfw",
			Source:   p.Source,
			Summary:  truncate(p.Summary, 80),
			RunID:    reachRunID(id),
		})
		done++
		humanPause(8, 18)
	}
	if done > 0 {
		accLog(id, logFn, "trail reblogs this session: %d (SFW ledger total 24h: %d)", done, trailReblogCountSince(id, time.Now().Add(-24*time.Hour)))
	}
	return done
}

func gatherTrailPosts(client *http.Client, auth Auth, trail trailReblogConfig, limit int) []ScrapedPost {
	seen := map[string]bool{}
	var out []ScrapedPost
	add := func(batch []ScrapedPost, kind, source string) {
		for _, p := range batch {
			if p.ID == "" || seen[p.ID] {
				continue
			}
			if looksNSFWTrailText(p.Summary) || looksNSFWTrailText(p.Blog) {
				continue
			}
			seen[p.ID] = true
			p.SourceKind = kind
			p.Source = source
			out = append(out, p)
			if len(out) >= limit {
				return
			}
		}
	}

	queries := append([]string{}, trail.Queries...)
	if len(queries) == 0 {
		queries = defaultTrailReblogConfig().Queries
	}
	rand.Shuffle(len(queries), func(i, j int) { queries[i], queries[j] = queries[j], queries[i] })
	for _, q := range queries {
		if len(out) >= limit {
			break
		}
		batch, err := scrapeTaggedPosts(client, auth, q, 8)
		if err != nil {
			batch, err = scrapeRecentPosts(client, auth, q, 8)
		}
		if err == nil {
			add(batch, "trail", q)
		}
	}
	for _, seed := range normalizeSeedBlogs(trail.SeedBlogs) {
		if len(out) >= limit {
			break
		}
		batch, err := fetchBlogPosts(client, &auth, seed, 6)
		if err == nil {
			add(batch, "trail_seed", seed)
		}
	}
	return out
}

func looksNSFWTrailText(s string) bool {
	s = strings.ToLower(s)
	for _, bad := range []string{
		"nsfw", "nude", "naked", "porn", "onlyfans", "of ", "sex", "xxx",
		"bdsm", "daddy", "slut", "whore", "cock", "pussy", "boob", "tit",
		"hentai", "lewd", "18+", "explicit",
	} {
		if strings.Contains(s, bad) {
			return true
		}
	}
	return false
}

func reblogPolicySummary(accountID string) string {
	nsfw := defaultNSFWReblogConfig()
	inWin := nsfwReblogsInWindow(accountID, time.Duration(nsfw.WindowHours)*time.Hour)
	trail24 := trailReblogCountSince(accountID, time.Now().Add(-24*time.Hour))
	return fmt.Sprintf("reblog ledger: NSFW %d/%d per week (%dh), SFW trail %d in 24h", inWin, nsfw.MaxCount, nsfw.WindowHours, trail24)
}
