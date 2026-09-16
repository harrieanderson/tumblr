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
	"path/filepath"
	"strings"
	"sync"
	"time"
)

type FollowConfig struct {
	Queries                []string `json:"queries"`
	BlogSearchQueries      []string `json:"blogSearchQueries"`
	SeedBlogs              []string `json:"seedBlogs"`
	MaxPosts               int      `json:"maxPosts"`
	MaxFollows             int      `json:"maxFollows"`
	DelaySeconds           int      `json:"delaySeconds"`
	DelayJitterSeconds     int      `json:"delayJitterSeconds"`
	RequireActiveDays      int      `json:"requireActiveDays"`
	PreferMale             bool     `json:"preferMale"`
	SkipIfFemaleSignals    bool     `json:"skipIfFemaleSignals"`
	IncludeRebloggers      bool     `json:"includeRebloggers"`
	PreferRebloggers       bool     `json:"preferRebloggers"`
	EngageBeforeFollow     bool     `json:"engageBeforeFollow"`
	ReblogTheirPosts       int      `json:"reblogTheirPosts"`
	ReblogChance           float64  `json:"reblogChance"`
	LikePinned             bool     `json:"likePinned"`
	LikeExtraPosts         int      `json:"likeExtraPosts"`
	LikePostsMin           int      `json:"likePostsMin"`
	LikePostsMax           int      `json:"likePostsMax"`
	LikeDelaySeconds       int      `json:"likeDelaySeconds"`
	LikeDelayJitterSeconds int      `json:"likeDelayJitterSeconds"`
	SeedPostsPerBlog       int      `json:"seedPostsPerBlog"`
	BlogsPerSearch         int      `json:"blogsPerSearch"`
	SeedOnly               bool     `json:"seedOnly"`
	MinNotes               int      `json:"minNotes"`
	MaxNotes               int      `json:"maxNotes"`
	DryRun                 bool     `json:"dryRun"`
	// Human-mode: sometimes browse/like without following; sometimes skip engagers.
	FollowAfterEngageChance float64 `json:"followAfterEngageChance"`
	SkipEngagerChance       float64 `json:"skipEngagerChance"`
	MaxEngages              int     `json:"maxEngages"`
}

type Engager struct {
	Blog string
	Kind string // "reblog" or "like"
}

const (
	followConfigFile = "config/follow.json"
)

func runFollowLikers() {
	acc, _ := currentAccount()
	if err := runFollowLikersOn(acc, 0, 0, nil); err != nil {
		fmt.Println("Error:", err)
	}
}

func runFollowLikersOn(acc Account, maxFollows, likePosts int, logFn func(string)) error {
	quietChromeLogin = true
	nativeChromeClicks = false
	files := filesForAccount(acc.ID)
	auth, err := loadAuthFrom(files.AuthPath)
	if err != nil {
		return fmt.Errorf("loading auth: %w", err)
	}
	if !authHasSID(auth) {
		return fmt.Errorf("not logged in")
	}

	if sessionExpired() {
		accLog(files.ID, logFn, "time is up")
		return nil
	}

	cfg := loadFollowConfig()
	cfg.PreferRebloggers = false
	cfg.IncludeRebloggers = false
	if maxFollows > 0 {
		cfg.MaxFollows = maxFollows
	}
	if likePosts > 0 {
		cfg.LikePinned = true
		cfg.LikePostsMax = likePosts
		cfg.LikePostsMin = likePosts
		if likePosts >= 4 {
			cfg.LikePostsMin = 3
		}
		cfg.LikeExtraPosts = likePosts
	}

	client := newHumanClient()
	if err := ensureSession(client, &auth); err != nil {
		return fmt.Errorf("session: %w", err)
	}
	warmSession(client, &auth)

	already, err := loadFollowedFile(files.Followed)
	if err != nil {
		return fmt.Errorf("followed list: %w", err)
	}

	accLog(files.ID, logFn, "Seed blogs + tagged posts → likes list → like them, sometimes follow")
	n, skipped := engageTagLikers(client, &auth, cfg, already, cfg.MaxFollows, files, logFn)
	_ = saveFollowedFile(files.Followed, already)
	accLog(files.ID, logFn, "Done. Followed %d (%d skipped). Shared interacted: %d", n, skipped, interactedCount())
	logOwnFollowers(client, &auth, logFn)
	if cfg.DryRun {
		accLog(files.ID, logFn, "(dryRun was true — set dryRun:false in config/follow.json to actually follow)")
	}
	return nil
}

func accLog(id string, extra func(string), format string, args ...any) {
	msg := fmt.Sprintf(format, args...)
	if id != "" {
		msg = "[" + id + "] " + msg
	}
	fmt.Println(msg)
	if extra != nil {
		extra(msg)
	}
}

func loadFollowConfig() FollowConfig {
	cfg := FollowConfig{
		BlogSearchQueries: []string{"brat", "onlyfans", "fansly", "thirst trap", "cnc kink", "brattysub", "big tiddy gf"},
		Queries: []string{
			"onlyfans", "fansly", "thirst trap", "bimbo slvt",
			"looking for a daddy", "looking for daddy", "dm me daddy", "daddy bf",
			"older man do it better", "older gentleman", "dm me if you want",
			"cnc kink", "cnc praise", "cnc knife play", "cnc free use", "cnc knotty",
			"submisive loser", "subby pet", "feeling subby", "bd sm slave",
			"brattysub", "bratty switch", "bratty princess", "slvtty brat", "dd/lg brat",
			"big tittis", "big tiddy goth gf", "big tiddy gf", "big tiddy anime girl", "big boosb",
		},
		SeedBlogs:               []string{"madamedesiderio", "bby-devil-girl", "fawnjaw"},
		MaxPosts:                20,
		MaxFollows:              40,
		DelaySeconds:            110,
		DelayJitterSeconds:      90,
		RequireActiveDays:       21,
		PreferMale:              true,
		SkipIfFemaleSignals:     true,
		IncludeRebloggers:       false,
		PreferRebloggers:        false,
		EngageBeforeFollow:      true,
		ReblogTheirPosts:        1,
		ReblogChance:            0.35,
		LikePinned:              true,
		LikeExtraPosts:          4,
		LikePostsMin:            3,
		LikePostsMax:            4,
		LikeDelaySeconds:        14,
		LikeDelayJitterSeconds:  18,
		SeedPostsPerBlog:        4,
		BlogsPerSearch:          8,
		SeedOnly:                false,
		MinNotes:                25,
		MaxNotes:                100000,
		FollowAfterEngageChance: 0.4,
		SkipEngagerChance:       0.15,
		MaxEngages:              100,
	}
	if raw, err := os.ReadFile(followConfigFile); err == nil {
		_ = json.Unmarshal(raw, &cfg)
	}
	if cfg.MaxPosts <= 0 {
		cfg.MaxPosts = 3
	}
	if cfg.MaxFollows <= 0 {
		cfg.MaxFollows = 40
	}
	if cfg.DelaySeconds <= 0 {
		cfg.DelaySeconds = 110
	}
	if cfg.DelayJitterSeconds < 0 {
		cfg.DelayJitterSeconds = 0
	}
	if cfg.LikeExtraPosts < 0 {
		cfg.LikeExtraPosts = 1
	}
	if cfg.LikeDelaySeconds <= 0 {
		cfg.LikeDelaySeconds = 14
	}
	if cfg.LikeDelayJitterSeconds < 0 {
		cfg.LikeDelayJitterSeconds = 0
	}
	if cfg.FollowAfterEngageChance <= 0 {
		cfg.FollowAfterEngageChance = 0.4
	}
	if cfg.SkipEngagerChance < 0 {
		cfg.SkipEngagerChance = 0
	}
	if cfg.MaxEngages <= 0 {
		cfg.MaxEngages = cfg.MaxFollows * 2
		if cfg.MaxEngages < 6 {
			cfg.MaxEngages = 6
		}
	}
	if cfg.LikePostsMax <= 0 {
		cfg.LikePostsMax = cfg.LikeExtraPosts
	}
	if cfg.LikePostsMin <= 0 {
		cfg.LikePostsMin = cfg.LikePostsMax
		if cfg.LikePostsMax >= 4 {
			cfg.LikePostsMin = 3
		}
	}
	if cfg.LikePostsMin > cfg.LikePostsMax {
		cfg.LikePostsMin = cfg.LikePostsMax
	}
	if cfg.ReblogTheirPosts < 0 {
		cfg.ReblogTheirPosts = 0
	}
	if cfg.ReblogChance < 0 {
		cfg.ReblogChance = 0
	}
	if cfg.SeedPostsPerBlog <= 0 {
		cfg.SeedPostsPerBlog = 4
	}
	if cfg.BlogsPerSearch <= 0 {
		cfg.BlogsPerSearch = 8
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

// followEngagersBurst runs seed-blog + tagged-post likers, with a shared interacted list.
func followEngagersBurst(client *http.Client, auth *Auth, cfg FollowConfig, already map[string]bool, maxFollows int) (followed int, skipped int) {
	acc, _ := currentAccount()
	files := filesForAccount(acc.ID)
	return engageTagLikers(client, auth, cfg, already, maxFollows, files, nil)
}

func engageTagLikers(client *http.Client, auth *Auth, cfg FollowConfig, already map[string]bool, maxFollows int, files accountFiles, logFn func(string)) (followed int, skipped int) {
	if maxFollows <= 0 {
		return 0, 0
	}
	cfg.IncludeRebloggers = false
	cfg.PreferRebloggers = false

	id := files.ID
	rememberBlogsFromAuth(client, auth)
	maxEngages := cfg.MaxEngages
	if maxEngages <= 0 {
		maxEngages = maxFollows * 2
	}

	seedPosts := gatherSeedBlogPosts(client, auth, cfg, id, logFn)
	tagPosts := gatherTagPosts(client, auth, cfg, id, logFn)
	posts := append(seedPosts, tagPosts...)
	if len(posts) == 0 {
		accLog(id, logFn, "No posts found on seed blogs or tags")
		return 0, 0
	}
	rand.Shuffle(len(posts), func(i, j int) { posts[i], posts[j] = posts[j], posts[i] })

	accLog(id, logFn, "Walking %d posts for likers (up to %d follows / %d touches)", len(posts), maxFollows, maxEngages)
	if sessionExpired() {
		accLog(id, logFn, "time is up")
		return followed, skipped
	}
	accLog(id, logFn, "Per liker: like their posts, sometimes follow. Shared skip list so other accounts leave them alone.")

	reblogged := loadStringSet(files.Reblogged)
	if reblogged == nil {
		reblogged = map[string]bool{}
	}
	engaged := 0
	stop := false

	for _, post := range posts {
		if stop || engaged >= maxEngages || sessionExpired() {
			break
		}
		accLog(id, logFn, "Likes on %s/%s (%d notes)…", post.Blog, post.ID, post.Notes)
		names, err := fetchNotesByMode(client, auth, post.Blog, post.ID, "likes")
		if err != nil {
			err = retryAfterCookie(client, auth, logFn, id, err, func() error {
				var e error
				names, e = fetchNotesByMode(client, auth, post.Blog, post.ID, "likes")
				return e
			})
			if err != nil {
				accLog(id, logFn, "  likes list error: %v", err)
				if isCookieProblem(err) {
					_ = saveFollowedFile(files.Followed, already)
					stop = true
					break
				}
				continue
			}
		}
		rand.Shuffle(len(names), func(i, j int) { names[i], names[j] = names[j], names[i] })
		accLog(id, logFn, "  found %d likers", len(names))

		for _, name := range names {
			if stop || engaged >= maxEngages || sessionExpired() {
				break
			}
			blog := strings.TrimSpace(name)
			key := strings.ToLower(blog)
			if blog == "" || already[key] || hasInteracted(blog) {
				continue
			}
			if strings.EqualFold(blog, post.Blog) {
				continue
			}
			if owner, ok := managedByUs(blog); ok {
				accLog(id, logFn, "  skip %s (our blog, %s)", blog, owner)
				markInteracted(blog)
				already[key] = true
				continue
			}
			if cfg.SkipEngagerChance > 0 && rand.Float64() < cfg.SkipEngagerChance {
				continue
			}

			fr := shouldFollowBlog(client, *auth, blog, cfg)
			if !fr.OK {
				accLog(id, logFn, "  skip %s (%s)", blog, fr.Reason)
				skipped++
				markInteracted(blog)
				already[key] = true
				continue
			}

			if !claimInteracted(blog) {
				accLog(id, logFn, "  skip %s (another account already touched them)", blog)
				already[key] = true
				continue
			}

			doFollow := followed < maxFollows && (cfg.FollowAfterEngageChance >= 1 || rand.Float64() < cfg.FollowAfterEngageChance)
			if doFollow {
				accLog(id, logFn, "  engage liker %s — like + follow (%s)", blog, fr.Reason)
			} else {
				accLog(id, logFn, "  engage liker %s — like only (%s)", blog, fr.Reason)
			}
			if cfg.DryRun {
				already[key] = true
				engaged++
				if doFollow {
					followed++
				}
				continue
			}

			peekBlog(client, *auth, blog)
			humanThink()
			if err := likePinnedAndRecent(client, auth, blog, cfg, id, logFn); err != nil {
				if isCookieProblem(err) {
					accLog(id, logFn, "  stopping this account: %v", err)
					_ = saveFollowedFile(files.Followed, already)
					stop = true
					break
				}
			}
			humanPause(12, 28)

			if cfg.ReblogTheirPosts > 0 {
				chance := cfg.ReblogChance
				if chance <= 0 {
					chance = 0.35
				}
				if rand.Float64() < chance {
					n, err := reblogFromBlog(client, auth, blog, cfg.ReblogTheirPosts, reblogged)
					if err != nil && isCookieProblem(err) {
						accLog(id, logFn, "  stopping this account: %v", err)
						_ = saveFollowedFile(files.Followed, already)
						stop = true
						break
					}
					if n > 0 {
						accLog(id, logFn, "   reblogged %d of their posts", n)
						_ = saveStringSet(files.Reblogged, reblogged)
					}
					humanPause(12, 28)
				}
			}

			if doFollow {
				accLog(id, logFn, "  follow %s", blog)
				if err := followBlog(client, *auth, blog); err != nil {
					err = retryAfterCookie(client, auth, logFn, id, err, func() error {
						return followBlog(client, *auth, blog)
					})
					if err != nil {
						if isFollowRejected(err) {
							accLog(id, logFn, "   Tumblr refused follow of %s — liked only", blog)
							already[key] = true
							_ = saveFollowedFile(files.Followed, already)
							engaged++
							skipped++
							continue
						}
						accLog(id, logFn, "   follow fail: %v", err)
						if isCookieProblem(err) {
							_ = saveFollowedFile(files.Followed, already)
							stop = true
							break
						}
						already[key] = true
						engaged++
						continue
					}
				}
				followed++
				_ = saveFollowedFile(files.Followed, already)
			}

			already[key] = true
			engaged++

			wait := followDelay(cfg)
			if !doFollow {
				wait = wait / 2
				minLikeWait := 40
				if paceFast() {
					minLikeWait = 12
				}
				if wait < minLikeWait {
					wait = minLikeWait
				}
			}
			accLog(id, logFn, "  waiting %ds…", wait)
			time.Sleep(time.Duration(wait) * time.Second)
		}
		if engaged < maxEngages && !stop {
			humanPause(20, 50)
		}
	}

	return followed, skipped
}

func gatherSeedBlogPosts(client *http.Client, auth *Auth, cfg FollowConfig, id string, logFn func(string)) []ScrapedPost {
	per := cfg.SeedPostsPerBlog
	if per <= 0 {
		per = 4
	}
	var posts []ScrapedPost
	seen := map[string]bool{}
	for _, seed := range normalizeSeedBlogs(cfg.SeedBlogs) {
		accLog(id, logFn, "  blog: %s", seed)
		batch, err := fetchBlogPosts(client, auth, seed, per+4)
		if err != nil {
			accLog(id, logFn, "   posts error: %v", err)
			continue
		}
		sortScrapedByNotes(batch)
		batch = filterPostsByNoteBand(batch, cfg.MinNotes, cfg.MaxNotes)
		if len(batch) > per {
			batch = batch[:per]
		}
		for _, p := range batch {
			if p.ID == "" || seen[p.ID] {
				continue
			}
			seen[p.ID] = true
			posts = append(posts, p)
		}
	}
	return posts
}

func gatherTagPosts(client *http.Client, auth *Auth, cfg FollowConfig, id string, logFn func(string)) []ScrapedPost {
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
	perTag := 3
	if cfg.SeedPostsPerBlog > 0 && cfg.SeedPostsPerBlog < perTag {
		perTag = cfg.SeedPostsPerBlog
	}
	for _, q := range cfg.Queries {
		q = strings.TrimSpace(strings.TrimPrefix(q, "#"))
		if q == "" {
			continue
		}
		accLog(id, logFn, "  tag: %s", q)
		batch, err := scrapeTaggedPosts(client, *auth, q, 20)
		if err != nil {
			accLog(id, logFn, "   tagged search error: %v", err)
		}
		if len(batch) == 0 {
			if top, err := scrapeTopPosts(client, *auth, q, 15); err == nil {
				batch = append(batch, top...)
			}
			if len(batch) == 0 {
				if recent, err := scrapeRecentPosts(client, *auth, q, 15); err == nil {
					batch = append(batch, recent...)
				}
			}
		}
		sortScrapedByNotes(batch)
		batch = filterPostsByNoteBand(batch, cfg.MinNotes, cfg.MaxNotes)
		if len(batch) > perTag {
			batch = batch[:perTag]
		}
		add(batch)
	}
	return posts
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
func reblogFromBlog(client *http.Client, auth *Auth, blog string, n int, reblogged map[string]bool) (int, error) {
	if n <= 0 {
		return 0, nil
	}
	posts, err := fetchBlogPosts(client, auth, blog, 8)
	if err != nil || len(posts) == 0 {
		fmt.Println("   reblog fetch fail:", err)
		if isCookieProblem(err) || isTransientNet(err) {
			return 0, err
		}
		return 0, nil
	}
	done := 0
	id := accountIDFromAuth(*auth)
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
			err = retryAfterCookie(client, auth, nil, id, err, func() error {
				return reblogPost(client, *auth, p)
			})
			if err != nil {
				if isCookieProblem(err) {
					return done, err
				}
				continue
			}
		}
		reblogged[p.ID] = true
		done++
		if done < n {
			time.Sleep(time.Duration(randRange(6, 14)) * time.Second)
		}
	}
	return done, nil
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
		seedPosts, err := fetchBlogPosts(client, &auth, seed, cfg.SeedPostsPerBlog)
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

func fetchBlogPosts(client *http.Client, auth *Auth, blog string, limit int) ([]ScrapedPost, error) {
	if auth == nil {
		return nil, fmt.Errorf("missing auth")
	}
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
	setCommonHeaders(req, *auth)
	req.Header.Set("X-CSRF", auth.CSRF)
	req.Header.Set("Referer", "https://www.tumblr.com/"+blog)

	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	takeCSRF(resp.Header, auth)
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
		batch, err := fetchNotesByMode(client, &auth, blog, postID, "reblogs_with_tags")
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

	batch, err := fetchNotesByMode(client, &auth, blog, postID, "likes")
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

func fetchNotesByMode(client *http.Client, auth *Auth, blog, postID, mode string) ([]string, error) {
	if auth == nil {
		return nil, fmt.Errorf("missing auth")
	}
	blogID := blog
	if !strings.Contains(blogID, ".") && !strings.HasPrefix(blogID, "t:") {
		blogID = blogID + ".tumblr.com"
	}

	var names []string
	seen := map[string]bool{}
	var before int64

	for page := 0; page < 8; page++ {
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
		setCommonHeaders(req, *auth)
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
		takeCSRF(resp.Header, auth)
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
	req.Header.Set("Referer", "https://www.tumblr.com/"+strings.TrimSuffix(blog, ".tumblr.com"))

	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	takeCSRF(resp.Header, &auth)
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("%s: %s", resp.Status, truncate(string(body), 180))
	}
	return nil
}

func isFollowRejected(err error) bool {
	if err == nil {
		return false
	}
	s := strings.ToLower(err.Error())
	if !strings.Contains(s, "400") {
		return false
	}
	return strings.Contains(s, "something broke") ||
		strings.Contains(s, "internet strangeness") ||
		strings.Contains(s, "already") ||
		strings.Contains(s, "bad request")
}

func followDelay(cfg FollowConfig) int {
	wait := cfg.DelaySeconds
	if cfg.DelayJitterSeconds > 0 {
		wait += rand.Intn(cfg.DelayJitterSeconds + 1)
	}
	minWait := 55
	if paceFast() {
		wait = (wait + 1) / 2
		minWait = 20
	}
	if wait < minWait {
		wait = minWait
	}
	if !paceFast() && rand.Float64() < 0.12 {
		wait += randRange(90, 240)
	}
	return wait
}

func loadFollowed() (map[string]bool, error) {
	acc, _ := currentAccount()
	return loadFollowedFile(filesForAccount(acc.ID).Followed)
}

func loadFollowedFile(path string) (map[string]bool, error) {
	out := map[string]bool{}
	raw, err := os.ReadFile(path)
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
	acc, _ := currentAccount()
	return saveFollowedFile(filesForAccount(acc.ID).Followed, m)
}

func saveFollowedFile(path string, m map[string]bool) error {
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
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
	return os.WriteFile(path, raw, 0644)
}

const interactedFile = "data/interacted.json"

var interactedMu sync.Mutex

func loadInteractedMap() map[string]bool {
	out := map[string]bool{}
	raw, err := os.ReadFile(interactedFile)
	if err != nil {
		return out
	}
	var list []string
	if json.Unmarshal(raw, &list) != nil {
		return out
	}
	for _, b := range list {
		out[strings.ToLower(b)] = true
	}
	return out
}

func saveInteractedMap(m map[string]bool) {
	if err := os.MkdirAll(filepath.Dir(interactedFile), 0755); err != nil {
		return
	}
	list := make([]string, 0, len(m))
	for b := range m {
		list = append(list, b)
	}
	raw, err := json.MarshalIndent(list, "", "  ")
	if err != nil {
		return
	}
	_ = os.WriteFile(interactedFile, raw, 0644)
}

func hasInteracted(blog string) bool {
	interactedMu.Lock()
	defer interactedMu.Unlock()
	return loadInteractedMap()[strings.ToLower(strings.TrimSpace(blog))]
}

func markInteracted(blog string) {
	claimInteracted(blog)
}

func claimInteracted(blog string) bool {
	key := strings.ToLower(strings.TrimSpace(blog))
	if key == "" {
		return false
	}
	interactedMu.Lock()
	defer interactedMu.Unlock()
	m := loadInteractedMap()
	if m[key] {
		return false
	}
	m[key] = true
	saveInteractedMap(m)
	return true
}

func interactedCount() int {
	interactedMu.Lock()
	defer interactedMu.Unlock()
	return len(loadInteractedMap())
}
