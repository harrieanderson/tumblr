package main

import (
	"encoding/json"
	"fmt"
	"io"
	"math/rand"
	"net/http"
	"os"
	"strings"
	"time"
)

type HumanizeCategory struct {
	Name    string   `json:"name"`
	Weight  int      `json:"weight"`
	Queries []string `json:"queries"`
}

type HumanizeConfig struct {
	DurationMinutes  int                `json:"durationMinutes"`
	ScrollMinSeconds int                `json:"scrollMinSeconds"`
	ScrollMaxSeconds int                `json:"scrollMaxSeconds"`
	IdleChance       float64            `json:"idleChance"`
	IdleMinSeconds   int                `json:"idleMinSeconds"`
	IdleMaxSeconds   int                `json:"idleMaxSeconds"`
	LikeChance       float64            `json:"likeChance"`
	ReblogChance     float64            `json:"reblogChance"`
	FollowChance     float64            `json:"followChance"`
	MessageChance    float64            `json:"messageChance"`
	FollowBurstMin   int                `json:"followBurstMin"`
	FollowBurstMax   int                `json:"followBurstMax"`
	MaxLikes         int                `json:"maxLikes"`
	MaxReblogs       int                `json:"maxReblogs"`
	MaxFollows       int                `json:"maxFollows"`
	Categories       []HumanizeCategory `json:"categories"`
}

const humanizeConfigFile = "config/humanize.json"

func runHumanize() {
	cfg := defaultHumanizeConfig()
	if raw, err := os.ReadFile(humanizeConfigFile); err == nil {
		_ = json.Unmarshal(raw, &cfg)
	}
	normalizeHumanizeConfig(&cfg)

	auth, err := loadAuth()
	if err != nil {
		fmt.Println("Error loading auth:", err)
		return
	}
	if err := syncCookiesFromChrome(&auth); err != nil {
		if !authHasSID(auth) {
			fmt.Println("Cookie sync failed — trying auto-login…")
			if err := loginWithRealChrome(&auth); err != nil {
				fmt.Println("Login failed:", err)
				return
			}
		} else {
			fmt.Println("Using saved cookies")
		}
	}

	client := newHumanClient()
	if err := ensureSession(client, &auth); err != nil {
		fmt.Println("Session dead — trying auto-login…")
		if err := loginWithRealChrome(&auth); err != nil {
			fmt.Println("Login failed:", err)
			return
		}
		if err := ensureSession(client, &auth); err != nil {
			fmt.Println("Session still dead:", err)
			return
		}
	}
	warmSession(client, &auth)

	followCfg := loadFollowConfig()
	// Cap session follows even lower than follow.json for brand-new human pacing.
	if followCfg.MaxFollows > cfg.MaxFollows {
		followCfg.MaxFollows = cfg.MaxFollows
	}
	already, err := loadFollowed()
	if err != nil {
		fmt.Println("Error loading followed list:", err)
		return
	}

	duration := time.Duration(cfg.DurationMinutes) * time.Minute
	deadline := time.Now().Add(duration)

	fmt.Printf("Human session for ~%s (slow NSFW scour + cover browsing)\n", duration)
	fmt.Println("Pace: lots of idle, few follows, peek blogs before engage, DMs off")

	likes, reblogs, follows := 0, 0, 0
	seen := map[string]bool{}
	rebloggedFile := loadStringSet("data/reblogged.json")
	noFollowUntil := time.Now().Add(8 * time.Minute) // warm browsing only

	for time.Now().Before(deadline) {
		if err := ensureSession(client, &auth); err != nil {
			fmt.Println("Session dead:", err)
			return
		}

		roll := rand.Float64()
		switch {
		case roll < cfg.IdleChance:
			doIdle(client, auth, cfg)

		case follows < cfg.MaxFollows && time.Now().After(noFollowUntil) &&
			roll < cfg.IdleChance+cfg.FollowChance:
			burst := randRange(cfg.FollowBurstMin, cfg.FollowBurstMax)
			left := cfg.MaxFollows - follows
			if burst > left {
				burst = left
			}
			fmt.Printf("\n--- Soft reach (up to %d follow) ---\n", burst)
			n, skipped := followEngagersBurst(client, &auth, followCfg, already, burst)
			follows += n
			_ = saveFollowed(already)
			fmt.Printf("--- Reach done (+%d, skipped %d)  [follows=%d likes=%d reblogs=%d] ---\n",
				n, skipped, follows, likes, reblogs)
			humanPause(90, 210)

		case roll < cfg.IdleChance+cfg.FollowChance+cfg.MessageChance:
			if cfg.MessageChance <= 0 {
				continue
			}
			fmt.Println("\n--- Checking Messages inbox ---")
			msgCfg := loadMessageConfig()
			n, err := messageInboxBurst(client, &auth, msgCfg)
			if err != nil {
				fmt.Println("  messages fail:", err)
				if isForbidden(err) || isUnauthorized(err) {
					fmt.Println("  Tumblr is blocking the DM API right now (soft 403).")
					fmt.Println("  Skipping further inbox checks this session.")
					cfg.MessageChance = 0
				}
			} else {
				fmt.Printf("--- Messages done (+%d replies) ---\n", n)
			}
			humanPause(40, 100)

		default:
			l, r := browseAndEngage(client, auth, cfg, deadline, seen, rebloggedFile, likes, reblogs)
			likes, reblogs = l, r
			wait := randRange(cfg.ScrollMinSeconds, cfg.ScrollMaxSeconds)
			fmt.Printf("Scrolling on… (%ds)  [follows=%d likes=%d reblogs=%d]\n", wait, follows, likes, reblogs)
			_ = browseDashboard(client, auth)
			if rand.Float64() < 0.35 {
				humanPause(3, 10)
				_ = browseDashboard(client, auth)
			}
			time.Sleep(time.Duration(wait) * time.Second)
		}
	}

	_ = saveFollowed(already)
	fmt.Printf("\nSession done. follows=%d likes=%d reblogs=%d\n", follows, likes, reblogs)
}

func doIdle(client *http.Client, auth Auth, cfg HumanizeConfig) {
	wait := randRange(cfg.IdleMinSeconds, cfg.IdleMaxSeconds)
	if rand.Float64() < 0.55 {
		fmt.Printf("Sitting still… (%ds)\n", wait)
	} else {
		fmt.Printf("Idle scrolling… (%ds)\n", wait)
		_ = browseDashboard(client, auth)
		if wait > 90 && rand.Float64() < 0.4 {
			mid := wait / 2
			time.Sleep(time.Duration(mid) * time.Second)
			_ = browseDashboard(client, auth)
			wait -= mid
		}
	}
	time.Sleep(time.Duration(wait) * time.Second)
}

func browseAndEngage(
	client *http.Client,
	auth Auth,
	cfg HumanizeConfig,
	deadline time.Time,
	seen map[string]bool,
	rebloggedFile map[string]bool,
	likes, reblogs int,
) (int, int) {
	cat := pickCategory(cfg.Categories)
	q := cat.Queries[rand.Intn(len(cat.Queries))]
	fmt.Printf("Browsing [%s] search: %s\n", cat.Name, q)

	posts, err := scrapeRecentPosts(client, auth, q, 12)
	if err != nil || len(posts) == 0 {
		posts, err = scrapeTopPosts(client, auth, q, 12)
	}
	if err != nil {
		fmt.Println("  browse fail:", err)
		time.Sleep(20 * time.Second)
		return likes, reblogs
	}

	// Niche posts: push reblogs harder (that's the dashboard reach lever).
	reblogChance := cfg.ReblogChance
	likeChance := cfg.LikeChance
	if strings.EqualFold(cat.Name, "horny") || strings.EqualFold(cat.Name, "nsfw") {
		if reblogChance < 0.45 {
			reblogChance = 0.45
		}
	}

	n := 3 + rand.Intn(5)
	if n > len(posts) {
		n = len(posts)
	}
	for i := 0; i < n && time.Now().Before(deadline); i++ {
		p := posts[rand.Intn(len(posts))]
		if p.ID == "" || seen[p.ID] {
			continue
		}
		seen[p.ID] = true

		fmt.Printf("  looking at %s/%s — %s\n", p.Blog, p.ID, truncate(p.Summary, 50))
		humanPause(6, 28)

		wantLike := likes < cfg.MaxLikes && rand.Float64() < likeChance
		wantReblog := reblogs < cfg.MaxReblogs && !rebloggedFile[p.ID] && rand.Float64() < reblogChance
		if (wantLike || wantReblog) && p.ReblogKey == "" {
			if enriched, e2 := fetchSinglePost(client, auth, p); e2 == nil {
				p = enriched
			}
		}

		if wantLike && p.ReblogKey != "" {
			fmt.Println("  liking…")
			if err := likePost(client, auth, p.ID, p.ReblogKey); err != nil {
				fmt.Println("   like fail:", err)
			} else {
				likes++
			}
			humanPause(5, 18)
		}

		if wantReblog && p.ReblogKey != "" {
			fmt.Println("  reblogging…")
			if err := reblogPost(client, auth, p); err != nil {
				fmt.Println("   reblog fail:", err)
			} else {
				reblogs++
				rebloggedFile[p.ID] = true
				_ = saveStringSet("data/reblogged.json", rebloggedFile)
			}
			humanPause(12, 40)
		}
	}
	return likes, reblogs
}

func defaultHumanizeConfig() HumanizeConfig {
	return HumanizeConfig{
		DurationMinutes:  55,
		ScrollMinSeconds: 45,
		ScrollMaxSeconds: 140,
		IdleChance:       0.40,
		IdleMinSeconds:   70,
		IdleMaxSeconds:   280,
		LikeChance:       0.18,
		ReblogChance:     0.22,
		FollowChance:     0.14,
		MessageChance:    0,
		FollowBurstMin:   1,
		FollowBurstMax:   1,
		MaxLikes:         8,
		MaxReblogs:       5,
		MaxFollows:       3,
		Categories: []HumanizeCategory{
			{Name: "horny", Weight: 75, Queries: []string{
				"nsfw", "thirst", "thirst trap", "lewd", "smut", "onlyfans", "brat", "horny",
			}},
			{Name: "cover", Weight: 25, Queries: []string{"cute", "wholesome", "aesthetic", "fashion"}},
		},
	}
}

func normalizeHumanizeConfig(cfg *HumanizeConfig) {
	if cfg.DurationMinutes <= 0 {
		cfg.DurationMinutes = 90
	}
	if cfg.IdleChance <= 0 {
		cfg.IdleChance = 0.28
	}
	if cfg.FollowChance <= 0 {
		cfg.FollowChance = 0.24
	}
	if cfg.MessageChance <= 0 {
		cfg.MessageChance = 0.12
	}
	if cfg.FollowBurstMin <= 0 {
		cfg.FollowBurstMin = 1
	}
	if cfg.FollowBurstMax < cfg.FollowBurstMin {
		cfg.FollowBurstMax = cfg.FollowBurstMin
	}
	if cfg.MaxFollows <= 0 {
		cfg.MaxFollows = 8
	}
	if cfg.MaxLikes <= 0 {
		cfg.MaxLikes = 18
	}
	if cfg.MaxReblogs <= 0 {
		cfg.MaxReblogs = 14
	}
	if cfg.ReblogChance <= 0 {
		cfg.ReblogChance = 0.32
	}
	if cfg.LikeChance <= 0 {
		cfg.LikeChance = 0.22
	}
	if cfg.ScrollMinSeconds <= 0 {
		cfg.ScrollMinSeconds = 25
	}
	if cfg.ScrollMaxSeconds < cfg.ScrollMinSeconds {
		cfg.ScrollMaxSeconds = cfg.ScrollMinSeconds
	}
}

func pickCategory(cats []HumanizeCategory) HumanizeCategory {
	if len(cats) == 0 {
		return HumanizeCategory{Name: "cute", Queries: []string{"cute"}, Weight: 1}
	}
	total := 0
	for _, c := range cats {
		w := c.Weight
		if w <= 0 {
			w = 1
		}
		total += w
	}
	r := rand.Intn(total)
	for _, c := range cats {
		w := c.Weight
		if w <= 0 {
			w = 1
		}
		if r < w {
			if len(c.Queries) == 0 {
				c.Queries = []string{"cute"}
			}
			return c
		}
		r -= w
	}
	return cats[0]
}

func randRange(min, max int) int {
	if max < min {
		min, max = max, min
	}
	if min < 1 {
		min = 1
	}
	if max <= min {
		return min
	}
	return min + rand.Intn(max-min+1)
}

func browseDashboard(client *http.Client, auth Auth) error {
	req, err := http.NewRequest("GET", "https://www.tumblr.com/api/v2/timeline/dashboard?reblog_info=true&notes_info=false&limit=10", nil)
	if err != nil {
		return err
	}
	setCommonHeaders(req, auth)
	req.Header.Set("X-CSRF", auth.CSRF)
	req.Header.Set("Referer", "https://www.tumblr.com/dashboard")
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	io.Copy(io.Discard, resp.Body)
	return nil
}

func fetchSinglePost(client *http.Client, auth Auth, p ScrapedPost) (ScrapedPost, error) {
	blog := p.Blog
	if blog == "" {
		return p, fmt.Errorf("no blog")
	}
	u := fmt.Sprintf("https://www.tumblr.com/api/v2/blog/%s/posts?id=%s&reblog_info=true&npf=true", blog, p.ID)
	if !strings.Contains(blog, ".") && !strings.HasPrefix(blog, "t:") {
		u = fmt.Sprintf("https://www.tumblr.com/api/v2/blog/%s.tumblr.com/posts?id=%s&reblog_info=true&npf=true", blog, p.ID)
	}
	req, err := http.NewRequest("GET", u, nil)
	if err != nil {
		return p, err
	}
	setCommonHeaders(req, auth)
	req.Header.Set("X-CSRF", auth.CSRF)
	resp, err := client.Do(req)
	if err != nil {
		return p, err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return p, err
	}
	batch, err := parseSearchTimeline(body)
	if err != nil || len(batch) == 0 {
		batch, _ = extractPostsLoose(body)
	}
	if len(batch) == 0 {
		return p, fmt.Errorf("post not found")
	}
	out := batch[0]
	if out.ReblogKey == "" {
		out.ReblogKey = p.ReblogKey
	}
	if out.BlogUUID == "" {
		out.BlogUUID = p.BlogUUID
	}
	return out, nil
}

func loadStringSet(path string) map[string]bool {
	out := map[string]bool{}
	raw, err := os.ReadFile(path)
	if err != nil {
		return out
	}
	var list []string
	if json.Unmarshal(raw, &list) != nil {
		return out
	}
	for _, s := range list {
		out[s] = true
	}
	return out
}

func saveStringSet(path string, m map[string]bool) error {
	list := make([]string, 0, len(m))
	for k := range m {
		list = append(list, k)
	}
	raw, err := json.MarshalIndent(list, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, raw, 0644)
}
