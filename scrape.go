package main

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"sort"
	"strings"
	"time"
)

type ScrapedPost struct {
	ID         string `json:"id"`
	Blog       string `json:"blog"`
	BlogUUID   string `json:"blogUuid,omitempty"`
	ReblogKey  string `json:"reblogKey,omitempty"`
	URL        string `json:"url"`
	Notes      int    `json:"notes"`
	Summary    string `json:"summary"`
	Timestamp  int64  `json:"timestamp"`
}

func runScrape() {
	auth, err := loadAuth()
	if err != nil {
		fmt.Println("Error loading auth:", err)
		return
	}

	client := &http.Client{Timeout: 30 * time.Second}
	if err := ensureSession(client, &auth); err != nil {
		fmt.Println("Session error:", err)
		fmt.Println("Try: go run . -login")
		return
	}

	queries := []string{"trending", "fyp", "viral", "tumblr"}
	if raw, err := os.ReadFile("config/scrape.json"); err == nil {
		var cfg struct {
			Queries []string `json:"queries"`
		}
		if json.Unmarshal(raw, &cfg) == nil && len(cfg.Queries) > 0 {
			queries = cfg.Queries
		}
	}

	seen := map[string]bool{}
	var posts []ScrapedPost

	for _, q := range queries {
		fmt.Println("Searching top posts today for:", q)
		batch, err := scrapeTopPosts(client, auth, q, 20)
		if err != nil {
			fmt.Println("  scrape error:", err)
			continue
		}
		for _, p := range batch {
			if seen[p.ID] {
				continue
			}
			seen[p.ID] = true
			posts = append(posts, p)
		}
	}

	sort.Slice(posts, func(i, j int) bool {
		return posts[i].Notes > posts[j].Notes
	})

	if len(posts) > 30 {
		posts = posts[:30]
	}

	if err := os.MkdirAll("scraped", 0755); err != nil {
		fmt.Println("Error creating scraped/:", err)
		return
	}

	outPath := fmt.Sprintf("scraped/%s.json", time.Now().Format("2006-01-02"))
	data, err := json.MarshalIndent(posts, "", "  ")
	if err != nil {
		fmt.Println("Error encoding JSON:", err)
		return
	}
	if err := os.WriteFile(outPath, data, 0644); err != nil {
		fmt.Println("Error writing file:", err)
		return
	}

	fmt.Printf("\nSaved %d posts -> %s\n\n", len(posts), outPath)
	for i, p := range posts {
		if i >= 10 {
			break
		}
		fmt.Printf("%2d. [%d notes] %s — %s\n    %s\n", i+1, p.Notes, p.Blog, truncate(p.Summary, 80), p.URL)
	}
}

func scrapeTopPosts(client *http.Client, auth Auth, query string, limit int) ([]ScrapedPost, error) {
	return scrapeSearch(client, auth, query, "top", "1", limit)
}

func scrapeRecentPosts(client *http.Client, auth Auth, query string, limit int) ([]ScrapedPost, error) {
	return scrapeSearch(client, auth, query, "recent", "", limit)
}

func scrapeSearch(client *http.Client, auth Auth, query, mode, days string, limit int) ([]ScrapedPost, error) {
	u, _ := url.Parse("https://www.tumblr.com/api/v2/timeline/search")
	q := u.Query()
	q.Set("query", query)
	q.Set("mode", mode)
	q.Set("limit", fmt.Sprintf("%d", limit))
	q.Set("reblog_info", "true")
	if days != "" {
		q.Set("days", days)
	}
	u.RawQuery = q.Encode()

	req, err := http.NewRequest("GET", u.String(), nil)
	if err != nil {
		return nil, err
	}
	setCommonHeaders(req, auth)
	req.Header.Set("X-CSRF", auth.CSRF)
	req.Header.Set("Referer", "https://www.tumblr.com/search/"+url.PathEscape(query)+"?t=1")

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
		return nil, fmt.Errorf("%s: %s", resp.Status, truncate(string(body), 200))
	}

	return parseSearchTimeline(body)
}

func scrapeTaggedPosts(client *http.Client, auth Auth, tag string, limit int) ([]ScrapedPost, error) {
	u := fmt.Sprintf("https://www.tumblr.com/api/v2/tagged?tag=%s&limit=%d&npf=true",
		url.QueryEscape(tag), limit)
	req, err := http.NewRequest("GET", u, nil)
	if err != nil {
		return nil, err
	}
	setCommonHeaders(req, auth)
	req.Header.Set("X-CSRF", auth.CSRF)
	req.Header.Set("Referer", "https://www.tumblr.com/tagged/"+url.PathEscape(tag))

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
		return nil, fmt.Errorf("%s: %s", resp.Status, truncate(string(body), 200))
	}

	posts, err := parseSearchTimeline(body)
	if err == nil && len(posts) > 0 {
		return posts, nil
	}
	return extractPostsLoose(body)
}

func parseSearchTimeline(body []byte) ([]ScrapedPost, error) {
	var raw map[string]any
	if err := json.Unmarshal(body, &raw); err != nil {
		return nil, err
	}

	response, _ := raw["response"].(map[string]any)
	if response == nil {
		return nil, fmt.Errorf("missing response object")
	}

	timeline, _ := response["timeline"].(map[string]any)
	elements := findElements(response, timeline)

	var posts []ScrapedPost
	for _, el := range elements {
		p, ok := extractPost(el)
		if ok {
			posts = append(posts, p)
		}
	}
	return posts, nil
}

func findElements(response, timeline map[string]any) []any {
	if timeline != nil {
		if els, ok := timeline["elements"].([]any); ok {
			return els
		}
		if objs, ok := timeline["objects"].([]any); ok {
			return objs
		}
	}
	if els, ok := response["elements"].([]any); ok {
		return els
	}
	if posts, ok := response["posts"].([]any); ok {
		return posts
	}
	// Some responses nest under resources / timeline.elements
	if resources, ok := response["timeline"].(map[string]any); ok {
		if els, ok := resources["elements"].([]any); ok {
			return els
		}
	}
	return nil
}

func extractPost(el any) (ScrapedPost, bool) {
	m, ok := el.(map[string]any)
	if !ok {
		return ScrapedPost{}, false
	}

	// Skip non-post timeline objects.
	if t, _ := m["objectType"].(string); t != "" && t != "post" {
		if _, has := m["id"]; !has {
			return ScrapedPost{}, false
		}
	}

	id := anyToString(m["id"])
	if id == "" {
		id = anyToString(m["idString"])
	}

	blog := ""
	blogUUID := ""
	if b, ok := m["blog"].(map[string]any); ok {
		blog = anyToString(b["name"])
		blogUUID = anyToString(b["uuid"])
		if blogUUID == "" {
			blogUUID = anyToString(b["blogUUID"])
		}
		if blog == "" {
			blog = blogUUID
		}
	}
	if blog == "" {
		blog = anyToString(m["blogName"])
	}

	reblogKey := anyToString(m["reblogKey"])
	if reblogKey == "" {
		reblogKey = anyToString(m["reblog_key"])
	}

	notes := anyToInt(m["noteCount"])
	if notes == 0 {
		notes = anyToInt(m["notes"])
	}

	summary := anyToString(m["summary"])
	if summary == "" {
		summary = extractTextFromContent(m["content"])
	}

	postURL := anyToString(m["postUrl"])
	if postURL == "" {
		postURL = anyToString(m["post_url"])
	}
	if postURL == "" && blog != "" && id != "" {
		postURL = fmt.Sprintf("https://www.tumblr.com/%s/%s", blog, id)
	}

	ts := anyToInt64(m["timestamp"])
	if id == "" {
		return ScrapedPost{}, false
	}

	return ScrapedPost{
		ID:        id,
		Blog:      blog,
		BlogUUID:  blogUUID,
		ReblogKey: reblogKey,
		URL:       postURL,
		Notes:     notes,
		Summary:   summary,
		Timestamp: ts,
	}, true
}

func extractTextFromContent(content any) string {
	arr, ok := content.([]any)
	if !ok {
		return ""
	}
	var parts []string
	for _, block := range arr {
		b, ok := block.(map[string]any)
		if !ok {
			continue
		}
		if anyToString(b["type"]) == "text" {
			if t := anyToString(b["text"]); t != "" {
				parts = append(parts, t)
			}
		}
	}
	return strings.Join(parts, " ")
}

func anyToString(v any) string {
	switch t := v.(type) {
	case string:
		return t
	case float64:
		return fmt.Sprintf("%.0f", t)
	case json.Number:
		return t.String()
	default:
		return ""
	}
}

func anyToInt(v any) int {
	switch t := v.(type) {
	case float64:
		return int(t)
	case int:
		return t
	case json.Number:
		i, _ := t.Int64()
		return int(i)
	case string:
		var n int
		fmt.Sscanf(t, "%d", &n)
		return n
	default:
		return 0
	}
}

func anyToInt64(v any) int64 {
	return int64(anyToInt(v))
}

func truncate(s string, n int) string {
	s = strings.ReplaceAll(s, "\n", " ")
	if len(s) <= n {
		return s
	}
	return s[:n] + "..."
}
