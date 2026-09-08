package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"math/rand"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"
)

type FollowConfig struct {
	Queries             []string `json:"queries"`
	BlogSearchQueries   []string `json:"blogSearchQueries"`
	SeedBlogs           []string `json:"seedBlogs"`
	MaxPosts            int      `json:"maxPosts"`
	MaxFollows          int      `json:"maxFollows"`
	DelaySeconds        int      `json:"delaySeconds"`
	DelayJitterSeconds  int      `json:"delayJitterSeconds"`
	RequireActiveDays   int      `json:"requireActiveDays"`
	PreferMale          bool     `json:"preferMale"`
	SkipIfFemaleSignals bool     `json:"skipIfFemaleSignals"`
	IncludeRebloggers   bool     `json:"includeRebloggers"`
	PreferRebloggers    bool     `json:"preferRebloggers"`
	EngageBeforeFollow  bool     `json:"engageBeforeFollow"`
	ReblogTheirPosts    int      `json:"reblogTheirPosts"`
	LikePinned          bool     `json:"likePinned"`
	LikeExtraPosts      int      `json:"likeExtraPosts"`
	LikeDelaySeconds    int      `json:"likeDelaySeconds"`
	SeedPostsPerBlog    int      `json:"seedPostsPerBlog"`
	BlogsPerSearch      int      `json:"blogsPerSearch"`
	SeedOnly            bool     `json:"seedOnly"`
	MinNotes            int      `json:"minNotes"`
	MaxNotes            int      `json:"maxNotes"`
	DryRun              bool     `json:"dryRun"`
}

type Engager struct {
	Blog string
	Kind string // "reblog" or "like"
}

const (
	followConfigFile = "config/follow.json"
	followedFile     = "data/followed.json"
)

func runFollowLikers() {
	auth, err := loadAuth()
	if err != nil {
		fmt.Println("Error loading auth:", err)
		return
	}

	cfg := loadFollowConfig()
	client := &http.Client{Timeout: 30 * time.Second}
	if err := ensureSession(client, &auth); err != nil {
		fmt.Println("Session error:", err)
		fmt.Println("Try: go run . -login")
		return
	}

	already, err := loadFollowed()
	if err != nil {
		fmt.Println("Error loading followed list:", err)
		return
	}

	fmt.Println("Reach engage: like → reblog their post → soft-follow (rebloggers first)...")
	n, skipped := followEngagersBurst(client, &auth, cfg, already, cfg.MaxFollows)
	_ = saveFollowed(already)
	fmt.Printf("\nDone. Engaged/followed %d blogs (%d skipped). Total tracked: %d\n",
		n, skipped, len(already))
	if cfg.DryRun {
		fmt.Println("(dryRun was true — set dryRun:false in config/follow.json to actually follow)")
	}
}

func loadFollowConfig() FollowConfig {
	cfg := FollowConfig{
		BlogSearchQueries:   []string{"brat", "onlyfans", "fansly", "thirst trap"},
		Queries:             []string{"nsfw", "horny", "thirst trap", "onlyfans"},
		SeedBlogs:           nil,
		MaxPosts:            5,
		MaxFollows:          8,
		DelaySeconds:        55,
		DelayJitterSeconds:  40,
		RequireActiveDays:   21,
		PreferMale:          true,
		SkipIfFemaleSignals: true,
		IncludeRebloggers:   true,
		PreferRebloggers:    true,
		EngageBeforeFollow:  true,
		ReblogTheirPosts:    1,
		LikePinned:          true,
		LikeExtraPosts:      2,
		LikeDelaySeconds:    10,
		SeedPostsPerBlog:    5,
		BlogsPerSearch:      15,
		SeedOnly:            true,
		MinNotes:            5,
		MaxNotes:            2000,
	}
	if raw, err := os.ReadFile(followConfigFile); err == nil {
		_ = json.Unmarshal(raw, &cfg)
	}
	if cfg.MaxPosts <= 0 {
		cfg.MaxPosts = 5
	}
	if cfg.MaxFollows <= 0 {
		cfg.MaxFollows = 8
	}
	if cfg.DelaySeconds <= 0 {
		cfg.DelaySeconds = 55
	}
	if cfg.DelayJitterSeconds < 0 {
		cfg.DelayJitterSeconds = 0
	}
	if cfg.LikeExtraPosts <= 0 {
		cfg.LikeExtraPosts = 2
	}
	if cfg.ReblogTheirPosts < 0 {
		cfg.ReblogTheirPosts = 0
	}
	if cfg.SeedPostsPerBlog <= 0 {
		cfg.SeedPostsPerBlog = 5
	}
	if cfg.BlogsPerSearch <= 0 {
		cfg.BlogsPerSearch = 15
	}
	if len(cfg.BlogSearchQueries) == 0 && len(cfg.Queries) > 0 {
		cfg.BlogSearchQueries = append([]string{}, cfg.Queries...)
	}
	if cfg.MinNotes < 0 {
		cfg.MinNotes = 0
	}
	if cfg.MaxNotes > 0 && cfg.MaxNotes < cfg.MinNotes {
		cfg.MaxNotes = cfg.MinNotes
	}
	return cfg
}

// followEngagersBurst runs the reach pipeline (like → reblog their post → soft-follow).
func followEngagersBurst(client *http.Client, auth *Auth, cfg FollowConfig, already map[string]bool, maxFollows int) (followed int, skipped int) {
	return engageForReachBurst(client, auth, cfg, already, maxFollows)
}

// engageForReachBurst searches Tumblr's Blogs tab for each query, then for EVERY
// blog in those results: open their posts → like/reblog/soft-follow people who
// engaged those posts. Optional seedBlogs are processed first if listed.
func engageForReachBurst(client *http.Client, auth *Auth, cfg FollowConfig, already map[string]bool, maxFollows int) (followed int, skipped int) {
	if maxFollows <= 0 {
		return 0, 0
	}

	manualSeeds := normalizeSeedBlogs(cfg.SeedBlogs)
	searchQueries := cfg.BlogSearchQueries
	if len(searchQueries) == 0 {
		searchQueries = cfg.Queries
	}

	fmt.Println("Discovering blogs via Tumblr blog search…")
	discovered := discoverBlogsFromQueries(client, *auth, searchQueries, cfg.BlogsPerSearch)

	// Manual seeds first (optional), then every blog returned by search, in rank order.
	seen := map[string]bool{}
	var targets []string
	addTarget := func(name string) {
		key := strings.ToLower(name)
		if name == "" || seen[key] {
			return
		}
		seen[key] = true
		targets = append(targets, name)
	}
	for _, s := range manualSeeds {
		addTarget(s)
	}
	for _, s := range discovered {
		addTarget(s)
	}

	if len(targets) == 0 {
		fmt.Println("No blogs found from blogSearchQueries / seedBlogs — edit config/follow.json")
		return 0, 0
	}

	fmt.Printf("Will walk %d blogs (search results + optional seeds), up to %d follows\n", len(targets), maxFollows)
	fmt.Println("Per blog: their posts → engagers (rebloggers first) → like → reblog theirs → soft-follow")
	fmt.Printf("Pipeline: like %d → reblog %d of theirs → soft-follow\n",
		cfg.LikeExtraPosts+boolToInt(cfg.LikePinned), cfg.ReblogTheirPosts)

	reblogged := loadStringSet("data/reblogged.json")
	stop := false

	processPosts := func(sourceLabel string, posts []ScrapedPost) {
		for _, post := range posts {
			if stop || followed >= maxFollows {
				return
			}
			fmt.Printf("\n[%s] engagers of %s/%s (%d notes)...\n", sourceLabel, post.Blog, post.ID, post.Notes)
			targetsEg, err := fetchEngagersRanked(client, *auth, post.Blog, post.ID, cfg)
			if err != nil {
				fmt.Println("  notes error:", err)
				continue
			}
			fmt.Printf("  found %d ranked engagers\n", len(targetsEg))

			for _, eg := range targetsEg {
				if stop || followed >= maxFollows {
					return
				}
				blog := eg.Blog
				key := strings.ToLower(blog)
				if already[key] {
					continue
				}
				if strings.EqualFold(blog, post.Blog) {
					continue
				}

				fr := shouldFollowBlog(client, *auth, blog, cfg)
				if !fr.OK {
					fmt.Printf("  skip %s (%s)\n", blog, fr.Reason)
					skipped++
					already[key] = true
					continue
				}

				fmt.Printf("  engage %s [%s] (%s)\n", blog, eg.Kind, fr.Reason)
				if cfg.DryRun {
					already[key] = true
					followed++
					continue
				}

				if cfg.EngageBeforeFollow {
					likePinnedAndRecent(client, auth, blog, cfg)
					time.Sleep(time.Duration(randRange(5, 14)) * time.Second)

					if cfg.ReblogTheirPosts > 0 {
						n := reblogFromBlog(client, auth, blog, cfg.ReblogTheirPosts, reblogged)
						if n > 0 {
							_ = saveStringSet("data/reblogged.json", reblogged)
						}
						time.Sleep(time.Duration(randRange(8, 20)) * time.Second)
					}
				}

				fmt.Printf("  soft-follow: %s\n", blog)
				if err := followBlog(client, *auth, blog); err != nil {
					fmt.Println("   follow fail:", err)
					if isUnauthorized(err) {
						if err := ensureSession(client, auth); err != nil {
							fmt.Println("Session dead — run go run . -login")
							_ = saveFollowed(already)
							stop = true
							return
						}
						if err := followBlog(client, *auth, blog); err != nil {
							fmt.Println("   retry fail:", err)
							continue
						}
					} else {
						continue
					}
				}

				if !cfg.EngageBeforeFollow {
					likePinnedAndRecent(client, auth, blog, cfg)
				}

				already[key] = true
				followed++
				_ = saveFollowed(already)

				wait := followDelay(cfg)
				fmt.Printf("  waiting %ds...\n", wait)
				time.Sleep(time.Duration(wait) * time.Second)
			}
		}
	}

	for _, blog := range targets {
		if stop || followed >= maxFollows {
			break
		}
		fmt.Printf("\n======== Blog from search: %s ========\n", blog)
		seedPosts, err := fetchBlogPosts(client, *auth, blog, cfg.SeedPostsPerBlog)
		if err != nil {
			fmt.Println("  posts error:", err)
			continue
		}
		if len(seedPosts) == 0 {
			fmt.Println("  no posts found")
			continue
		}
		sortScrapedByNotes(seedPosts)
		seedPosts = filterPostsByNoteBand(seedPosts, cfg.MinNotes, cfg.MaxNotes)
		if len(seedPosts) > cfg.MaxPosts {
			seedPosts = seedPosts[:cfg.MaxPosts]
		}
		fmt.Printf("  using %d of their posts as engagement sources\n", len(seedPosts))
		processPosts(blog, seedPosts)
	}

	if !stop && !cfg.SeedOnly && followed < maxFollows {
		fmt.Println("\n======== Fallback: tag search posts ========")
		extra := gatherSearchPosts(client, *auth, cfg)
		processPosts("search", extra)
	}

	return followed, skipped
}

func normalizeSeedBlogs(seeds []string) []string {
	seen := map[string]bool{}
	var out []string
	for _, s := range seeds {
		s = strings.TrimSpace(s)
		s = strings.TrimPrefix(s, "https://www.tumblr.com/")
		s = strings.TrimPrefix(s, "http://www.tumblr.com/")
		s = strings.TrimPrefix(s, "www.tumblr.com/")
		s = strings.Split(s, "/")[0]
		s = strings.TrimSuffix(s, ".tumblr.com")
		s = strings.TrimSpace(s)
		if s == "" {
			continue
		}
		key := strings.ToLower(s)
		if seen[key] {
			continue
		}
		seen[key] = true
		out = append(out, s)
	}
	return out
}

func boolToInt(v bool) int {
	if v {
		return 1
	}
	return 0
}

// reblogFromBlog reblogs up to n recent posts from a blog (skips already-reblogged).
func reblogFromBlog(client *http.Client, auth *Auth, blog string, n int, reblogged map[string]bool) int {
	if n <= 0 {
		return 0
	}
	posts, err := fetchBlogPosts(client, *auth, blog, 8)
	if err != nil || len(posts) == 0 {
		fmt.Println("   reblog fetch fail:", err)
		return 0
	}
	done := 0
	for _, p := range posts {
		if done >= n {
			break
		}
		if p.ID == "" || reblogged[p.ID] {
			continue
		}
		if p.ReblogKey == "" {
			if enriched, e2 := fetchSinglePost(client, *auth, p); e2 == nil {
				p = enriched
			}
		}
		if p.ReblogKey == "" {
			continue
		}
		fmt.Printf("   reblogging their post %s — %s\n", p.ID, truncate(p.Summary, 40))
		if err := reblogPost(client, *auth, p); err != nil {
			fmt.Println("    reblog fail:", err)
			continue
		}
		reblogged[p.ID] = true
		done++
		if done < n {
			time.Sleep(time.Duration(randRange(6, 14)) * time.Second)
		}
	}
	return done
}

func gatherTargetPosts(client *http.Client, auth Auth, cfg FollowConfig) []ScrapedPost {
	seen := map[string]bool{}
	var posts []ScrapedPost

	add := func(batch []ScrapedPost) {
		for _, p := range batch {
			if p.Blog == "" || p.ID == "" || seen[p.ID] {
				continue
			}
			seen[p.ID] = true
			posts = append(posts, p)
		}
	}

	for _, seed := range normalizeSeedBlogs(cfg.SeedBlogs) {
		fmt.Println("  seed blog posts:", seed)
		seedPosts, err := fetchBlogPosts(client, auth, seed, cfg.SeedPostsPerBlog)
		if err != nil {
			fmt.Println("   seed error:", err)
			continue
		}
		add(seedPosts)
	}

	if !cfg.SeedOnly {
		add(gatherSearchPosts(client, auth, cfg))
	}

	sortScrapedByNotes(posts)
	posts = filterPostsByNoteBand(posts, cfg.MinNotes, cfg.MaxNotes)
	if len(posts) > cfg.MaxPosts {
		posts = posts[:cfg.MaxPosts]
	}
	return posts
}

func gatherSearchPosts(client *http.Client, auth Auth, cfg FollowConfig) []ScrapedPost {
	seen := map[string]bool{}
	var posts []ScrapedPost
	add := func(batch []ScrapedPost) {
		for _, p := range batch {
			if p.Blog == "" || p.ID == "" || seen[p.ID] {
				continue
			}
			seen[p.ID] = true
			posts = append(posts, p)
		}
	}
	for _, q := range cfg.Queries {
		fmt.Println("  search:", q)
		batch, err := scrapeTopPosts(client, auth, q, 15)
		if err != nil {
			fmt.Println("   scrape error:", err)
		}
		add(batch)
		if len(batch) == 0 {
			if recent, err := scrapeRecentPosts(client, auth, q, 15); err == nil {
				add(recent)
			}
		}
	}
	sortScrapedByNotes(posts)
	posts = filterPostsByNoteBand(posts, cfg.MinNotes, cfg.MaxNotes)
	if len(posts) > cfg.MaxPosts {
		posts = posts[:cfg.MaxPosts]
	}
	return posts
}

func filterPostsByNoteBand(posts []ScrapedPost, minNotes, maxNotes int) []ScrapedPost {
	if minNotes <= 0 && maxNotes <= 0 {
		return posts
	}
	var mid []ScrapedPost
	var fallback []ScrapedPost
	for _, p := range posts {
		fallback = append(fallback, p)
		if minNotes > 0 && p.Notes < minNotes {
			continue
		}
		if maxNotes > 0 && p.Notes > maxNotes {
			continue
		}
		mid = append(mid, p)
	}
	// Mid-size posts = better mutual chance than mega-viral dumps.
	if len(mid) > 0 {
		return mid
	}
	return fallback
}

func fetchBlogPosts(client *http.Client, auth Auth, blog string, limit int) ([]ScrapedPost, error) {
	blogID := blog
	if !strings.Contains(blogID, ".") && !strings.HasPrefix(blogID, "t:") {
		blogID = blogID + ".tumblr.com"
	}
	u := fmt.Sprintf("https://www.tumblr.com/api/v2/blog/%s/posts?limit=%d&npf=true",
		url.PathEscape(blogID), limit)
	req, err := http.NewRequest("GET", u, nil)
	if err != nil {
		return nil, err
	}
	setCommonHeaders(req, auth)
	req.Header.Set("X-CSRF", auth.CSRF)
	req.Header.Set("Referer", "https://www.tumblr.com/"+blog)

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
		return nil, fmt.Errorf("%s: %s", resp.Status, truncate(string(body), 160))
	}

	posts, err := parseSearchTimeline(body)
	if err == nil && len(posts) > 0 {
		return posts, nil
	}
	return extractPostsLoose(body)
}

func sortScrapedByNotes(posts []ScrapedPost) {
	for i := 0; i < len(posts); i++ {
		for j := i + 1; j < len(posts); j++ {
			if posts[j].Notes > posts[i].Notes {
				posts[i], posts[j] = posts[j], posts[i]
			}
		}
	}
}

func fetchEngagers(client *http.Client, auth Auth, blog, postID string, includeReblogs bool) ([]string, error) {
	cfg := FollowConfig{IncludeRebloggers: includeReblogs, PreferRebloggers: includeReblogs}
	ranked, err := fetchEngagersRanked(client, auth, blog, postID, cfg)
	if err != nil {
		return nil, err
	}
	out := make([]string, 0, len(ranked))
	for _, e := range ranked {
		out = append(out, e.Blog)
	}
	return out, nil
}

func fetchEngagersRanked(client *http.Client, auth Auth, blog, postID string, cfg FollowConfig) ([]Engager, error) {
	var rebloggers, likers []Engager
	seen := map[string]bool{}

	if cfg.IncludeRebloggers || cfg.PreferRebloggers {
		batch, err := fetchNotesByMode(client, auth, blog, postID, "reblogs_with_tags")
		if err == nil {
			for _, name := range batch {
				key := strings.ToLower(name)
				if seen[key] {
					continue
				}
				seen[key] = true
				rebloggers = append(rebloggers, Engager{Blog: name, Kind: "reblog"})
			}
		}
	}

	batch, err := fetchNotesByMode(client, auth, blog, postID, "likes")
	if err == nil {
		for _, name := range batch {
			key := strings.ToLower(name)
			if seen[key] {
				continue
			}
			seen[key] = true
			likers = append(likers, Engager{Blog: name, Kind: "like"})
		}
	}

	var out []Engager
	if cfg.PreferRebloggers {
		out = append(out, rebloggers...)
		out = append(out, likers...)
	} else {
		out = append(out, likers...)
		out = append(out, rebloggers...)
	}
	return out, nil
}

func fetchNotesByMode(client *http.Client, auth Auth, blog, postID, mode string) ([]string, error) {
	blogID := blog
	if !strings.Contains(blogID, ".") && !strings.HasPrefix(blogID, "t:") {
		blogID = blogID + ".tumblr.com"
	}

	var names []string
	seen := map[string]bool{}
	var before int64

	for page := 0; page < 4; page++ {
		u, _ := url.Parse("https://www.tumblr.com/api/v2/blog/" + blogID + "/notes")
		q := u.Query()
		q.Set("id", postID)
		q.Set("mode", mode)
		if before > 0 {
			q.Set("before_timestamp", fmt.Sprintf("%d", before))
		}
		u.RawQuery = q.Encode()

		req, err := http.NewRequest("GET", u.String(), nil)
		if err != nil {
			return names, err
		}
		setCommonHeaders(req, auth)
		req.Header.Set("X-CSRF", auth.CSRF)
		req.Header.Set("Referer", fmt.Sprintf("https://www.tumblr.com/%s/%s", blog, postID))

		resp, err := client.Do(req)
		if err != nil {
			return names, err
		}
		body, err := io.ReadAll(resp.Body)
		resp.Body.Close()
		if err != nil {
			return names, err
		}
		if resp.StatusCode != http.StatusOK {
			return names, fmt.Errorf("%s: %s", resp.Status, truncate(string(body), 180))
		}

		batch, nextBefore, err := parseEngagerNotes(body, mode)
		if err != nil {
			return names, err
		}
		if len(batch) == 0 {
			break
		}
		for _, name := range batch {
			key := strings.ToLower(name)
			if seen[key] {
				continue
			}
			seen[key] = true
			names = append(names, name)
		}
		if nextBefore <= 0 || nextBefore == before {
			break
		}
		before = nextBefore
	}
	return names, nil
}

func parseEngagerNotes(body []byte, mode string) ([]string, int64, error) {
	var raw map[string]any
	if err := json.Unmarshal(body, &raw); err != nil {
		return nil, 0, err
	}
	response, _ := raw["response"].(map[string]any)
	if response == nil {
		return nil, 0, fmt.Errorf("missing response")
	}

	notes, _ := response["notes"].([]any)
	wantLike := strings.Contains(mode, "like")
	wantReblog := strings.Contains(mode, "reblog")

	var names []string
	var oldest int64

	for _, n := range notes {
		m, ok := n.(map[string]any)
		if !ok {
			continue
		}
		typ := strings.ToLower(anyToString(m["type"]))
		if wantLike && typ != "" && typ != "like" {
			continue
		}
		if wantReblog && typ != "" && typ != "reblog" {
			continue
		}
		name := anyToString(m["blogName"])
		if name == "" {
			name = anyToString(m["blog_name"])
		}
		if name == "" {
			if b, ok := m["blog"].(map[string]any); ok {
				name = anyToString(b["name"])
			}
		}
		if name == "" {
			continue
		}
		names = append(names, name)

		ts := anyToInt64(m["timestamp"])
		if ts > 0 && (oldest == 0 || ts < oldest) {
			oldest = ts
		}
	}
	return names, oldest, nil
}

func followBlog(client *http.Client, auth Auth, blog string) error {
	target := blog
	if !strings.Contains(target, ".") {
		target = target + ".tumblr.com"
	}

	payload, _ := json.Marshal(map[string]string{"url": target})
	req, err := http.NewRequest("POST", "https://www.tumblr.com/api/v2/user/follow", bytes.NewReader(payload))
	if err != nil {
		return err
	}
	setCommonHeaders(req, auth)
	req.Header.Set("Content-Type", "application/json; charset=utf8")
	req.Header.Set("X-CSRF", auth.CSRF)
	req.Header.Set("Referer", "https://www.tumblr.com/dashboard")

	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("%s: %s", resp.Status, truncate(string(body), 180))
	}
	return nil
}

func followDelay(cfg FollowConfig) int {
	wait := cfg.DelaySeconds
	if cfg.DelayJitterSeconds > 0 {
		wait += rand.Intn(cfg.DelayJitterSeconds + 1)
	}
	if wait < 5 {
		wait = 5
	}
	return wait
}

func loadFollowed() (map[string]bool, error) {
	out := map[string]bool{}
	raw, err := os.ReadFile(followedFile)
	if err != nil {
		if os.IsNotExist(err) {
			return out, nil
		}
		return nil, err
	}
	var list []string
	if err := json.Unmarshal(raw, &list); err != nil {
		return nil, err
	}
	for _, b := range list {
		out[strings.ToLower(b)] = true
	}
	return out, nil
}

func saveFollowed(m map[string]bool) error {
	if err := os.MkdirAll("data", 0755); err != nil {
		return err
	}
	list := make([]string, 0, len(m))
	for b := range m {
		list = append(list, b)
	}
	raw, err := json.MarshalIndent(list, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(followedFile, raw, 0644)
}
