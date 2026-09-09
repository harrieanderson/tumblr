package main

import (
	"fmt"
	"net/http"
	"regexp"
	"strings"
	"time"
)

var reUSUK = regexp.MustCompile(`(?i)\b(usa|u\.s\.a|united states|american|nyc|new york|los angeles|\bla\b|chicago|texas|florida|california|uk|u\.k|united kingdom|britain|british|england|scotland|wales|london|manchester|birmingham|edinburgh|glasgow|dublin|ireland|irish|canada|canadian|toronto|vancouver|sydney|melbourne|australia|aussie)\b`)

func runSeedScan(blogs []string) {
	auth, err := loadAuth()
	if err != nil {
		fmt.Println(err)
		return
	}
	_ = syncCookiesFromChrome(&auth)
	client := &http.Client{Timeout: 45 * time.Second}
	if err := ensureSession(client, &auth); err != nil {
		fmt.Println("Session dead:", err)
		return
	}

	tagCount := map[string]int{}
	fmt.Println("=== Seed blogs (content snapshot) ===")
	for _, blog := range blogs {
		p, err := fetchBlogProfile(client, auth, blog)
		if err != nil {
			fmt.Printf("\n%s — profile error: %v\n", blog, err)
			continue
		}
		fmt.Printf("\n%s — %s\n  bio: %s\n", blog, p.Title, truncate(p.Description, 160))
		rawPosts, err := fetchBlogPostsRaw(client, auth, blog, 12)
		if err != nil {
			fmt.Println("  posts error:", err)
			continue
		}
		for i, post := range rawPosts {
			if i >= 8 {
				break
			}
			id := anyToString(post["id"])
			notes := anyToInt(post["note_count"])
			if notes == 0 {
				notes = anyToInt(post["noteCount"])
			}
			tags := extractTagStrings(post)
			for _, t := range tags {
				tagCount[strings.ToLower(t)]++
			}
			summary := anyToString(post["summary"])
			if summary == "" {
				summary = anyToString(post["slug"])
			}
			fmt.Printf("  post %s notes=%d tags=%v  %s\n", id, notes, tags, truncate(summary, 60))
		}
	}

	fmt.Println("\n=== Top tags across seeds ===")
	type kv struct {
		k string
		v int
	}
	var ranked []kv
	for k, v := range tagCount {
		ranked = append(ranked, kv{k, v})
	}
	for i := 0; i < len(ranked); i++ {
		for j := i + 1; j < len(ranked); j++ {
			if ranked[j].v > ranked[i].v {
				ranked[i], ranked[j] = ranked[j], ranked[i]
			}
		}
	}
	queries := []string{}
	for i, t := range ranked {
		if i >= 12 {
			break
		}
		fmt.Printf("  #%d %s (%d)\n", i+1, t.k, t.v)
		if t.k != "" {
			queries = append(queries, t.k)
		}
	}
	extra := []string{
		"thirst trap", "horny on main", "onlyfans", "smut blog",
		"nsfw", "daddy's girl", "brat", "uk onlyfans", "usa onlyfans",
		"london nsfw", "nyc nsfw", "british onlyfans", "american girl onlyfans",
	}
	for _, q := range extra {
		queries = append(queries, q)
	}

	fmt.Println("\n=== Similar blogs (US/UK/EN bio signals preferred) ===")
	seen := map[string]bool{}
	for _, b := range blogs {
		seen[strings.ToLower(b)] = true
	}
	var usuk []string
	var other []string

	for _, q := range queries {
		if len(usuk) >= 20 {
			break
		}
		names, err := searchBlogs(client, auth, q, 12)
		if err != nil {
			fmt.Printf("  search %q fail: %v\n", q, err)
			continue
		}
		fmt.Printf("  search %q → %d blogs\n", q, len(names))
		for _, name := range names {
			key := strings.ToLower(name)
			if seen[key] {
				continue
			}
			seen[key] = true
			p, err := fetchBlogProfile(client, auth, name)
			bio := ""
			if err == nil {
				bio = strings.TrimSpace(p.Title + " | " + p.Description)
			}
			loc := reUSUK.FindString(bio)
			line := fmt.Sprintf("%s — %s", name, truncate(bio, 100))
			if loc != "" {
				fmt.Printf("  ✓ [%s] %s\n", loc, line)
				usuk = append(usuk, name)
			} else {
				other = append(other, name)
				if len(other) <= 15 {
					fmt.Printf("    · %s\n", line)
				}
			}
		}
	}

	fmt.Println("\n=== Suggested seedBlogs (US/UK-ish first) ===")
	suggested := append([]string{}, blogs...)
	for _, n := range usuk {
		suggested = append(suggested, n)
		if len(suggested) >= 18 {
			break
		}
	}
	for _, n := range other {
		if len(suggested) >= 12 {
			break
		}
		suggested = append(suggested, n)
	}
	for _, n := range suggested {
		fmt.Println(" ", n)
	}
	fmt.Println("\nCopy these into config/follow.json seedBlogs, then: go run . -follow")
}

func extractTagStrings(post map[string]any) []string {
	var out []string
	add := func(s string) {
		s = strings.TrimSpace(strings.TrimPrefix(s, "#"))
		if s == "" {
			return
		}
		out = append(out, s)
	}
	switch t := post["tags"].(type) {
	case []any:
		for _, x := range t {
			add(anyToString(x))
		}
	case []string:
		for _, x := range t {
			add(x)
		}
	}
	sum := anyToString(post["summary"])
	for _, part := range strings.Fields(sum) {
		if strings.HasPrefix(part, "#") && len(part) > 1 {
			add(part)
		}
	}
	return out
}
