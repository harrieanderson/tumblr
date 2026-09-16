package main

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
)

func runDebugSearch(query string) {
	auth, err := loadAuth()
	if err != nil {
		fmt.Println(err)
		return
	}
	client := &http.Client{}
	if err := ensureSession(client, &auth); err != nil {
		fmt.Println("session:", err)
		return
	}

	_ = os.MkdirAll("scraped", 0755)

	type trial struct {
		label string
		url   string
	}
	trials := []trial{
		{"search_top_1d", searchURL(query, "top", "1")},
		{"search_recent", searchURL(query, "recent", "")},
		{"search_top_365d", searchURL(query, "top", "365")},
		{"tagged_www", "https://www.tumblr.com/api/v2/tagged?tag=" + url.QueryEscape(query) + "&limit=10&npf=true"},
		{"tagged_api", "https://api.tumblr.com/v2/tagged?tag=" + url.QueryEscape(query) + "&limit=10&api_key=" + url.QueryEscape(auth.Bearer)},
	}

	for _, t := range trials {
		req, err := http.NewRequest("GET", t.url, nil)
		if err != nil {
			fmt.Println(t.label, "err:", err)
			continue
		}
		setCommonHeaders(req, auth)
		req.Header.Set("X-CSRF", auth.CSRF)
		req.Header.Set("Referer", "https://www.tumblr.com/search/"+url.PathEscape(query))

		resp, err := client.Do(req)
		if err != nil {
			fmt.Println(t.label, "req err:", err)
			continue
		}
		body, _ := io.ReadAll(resp.Body)
		resp.Body.Close()

		path := "scraped/debug_" + t.label + ".json"
		_ = os.WriteFile(path, body, 0644)

		posts, _ := parseSearchTimeline(body)
		if len(posts) == 0 {
			posts, _ = extractPostsLoose(body)
		}
		fmt.Printf("%s -> %s (%d bytes) posts=%d preview=%s\n",
			t.label, resp.Status, len(body), len(posts), truncate(string(body), 120))
	}

	if strings.Contains(auth.Cookie, "cl_pref=block") {
		fmt.Println("\nNOTE: cookie has cl_pref=block — Tumblr may be hiding mature content for this session.")
		fmt.Println("Turn off Safe Mode / content blocking in Tumblr settings, then go run . -login again.")
	}

	// Probe whether relaxing cl_pref in-request unlocks NSFW.
	fmt.Println("\nRetrying search_recent with cl_pref softened...")
	soft := auth
	soft.Cookie = strings.ReplaceAll(soft.Cookie, "cl_pref=block", "cl_pref=show")
	req, _ := http.NewRequest("GET", searchURL(query, "recent", ""), nil)
	setCommonHeaders(req, soft)
	req.Header.Set("X-CSRF", soft.CSRF)
	resp, err := client.Do(req)
	if err != nil {
		fmt.Println("soft retry err:", err)
		return
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	_ = os.WriteFile("scraped/debug_soft_clpref.json", body, 0644)
	posts, _ := parseSearchTimeline(body)
	if len(posts) == 0 {
		posts, _ = extractPostsLoose(body)
	}
	fmt.Printf("soft_clpref -> %s (%d bytes) posts=%d preview=%s\n",
		resp.Status, len(body), len(posts), truncate(string(body), 120))
}

func searchURL(query, mode, days string) string {
	u, _ := url.Parse("https://www.tumblr.com/api/v2/timeline/search")
	q := u.Query()
	q.Set("query", query)
	q.Set("mode", mode)
	q.Set("limit", "10")
	q.Set("reblog_info", "true")
	if days != "" {
		q.Set("days", days)
	}
	u.RawQuery = q.Encode()
	return u.String()
}

func extractPostsLoose(body []byte) ([]ScrapedPost, error) {
	var raw map[string]any
	if err := json.Unmarshal(body, &raw); err != nil {
		return nil, err
	}
	response := raw["response"]
	var list []any
	switch v := response.(type) {
	case []any:
		list = v
	case map[string]any:
		if posts, ok := v["posts"].([]any); ok {
			list = posts
		}
	}
	var out []ScrapedPost
	for _, el := range list {
		if p, ok := extractPost(el); ok {
			out = append(out, p)
		}
	}
	return out, nil
}
