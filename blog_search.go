package main

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
)

// searchBlogs returns blog names from Tumblr's Blogs search tab, in rank order.
// Example: query "brat" → first result often madamedesiderio, then more blogs.
func searchBlogs(client *http.Client, auth Auth, query string, limit int) ([]string, error) {
	if limit <= 0 {
		limit = 20
	}
	u, _ := url.Parse("https://www.tumblr.com/api/v2/timeline/search")
	q := u.Query()
	q.Set("query", query)
	q.Set("timeline_type", "blog")
	q.Set("limit", fmt.Sprintf("%d", limit))
	u.RawQuery = q.Encode()

	req, err := http.NewRequest("GET", u.String(), nil)
	if err != nil {
		return nil, err
	}
	setCommonHeaders(req, auth)
	req.Header.Set("X-CSRF", auth.CSRF)
	req.Header.Set("Referer", "https://www.tumblr.com/search/"+url.PathEscape(query)+"/blog")

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
	return parseBlogSearchTimeline(body)
}

func parseBlogSearchTimeline(body []byte) ([]string, error) {
	var raw map[string]any
	if err := json.Unmarshal(body, &raw); err != nil {
		return nil, err
	}
	response, _ := raw["response"].(map[string]any)
	if response == nil {
		return nil, fmt.Errorf("missing response")
	}

	seen := map[string]bool{}
	var names []string
	add := func(name string) {
		name = strings.TrimSpace(name)
		if name == "" {
			return
		}
		key := strings.ToLower(name)
		if seen[key] {
			return
		}
		seen[key] = true
		names = append(names, name)
	}

	// Preferred: timeline elements with embedded blog resources.
	if timeline, ok := response["timeline"].(map[string]any); ok {
		if els, ok := timeline["elements"].([]any); ok {
			for _, el := range els {
				m, ok := el.(map[string]any)
				if !ok {
					continue
				}
				if res, ok := m["resources"].([]any); ok {
					for _, r := range res {
						rm, ok := r.(map[string]any)
						if !ok {
							continue
						}
						add(anyToString(rm["name"]))
					}
				}
				if b, ok := m["blog"].(map[string]any); ok {
					add(anyToString(b["name"]))
				}
				add(anyToString(m["blogName"]))
			}
		}
	}

	// Fallback: /search/blogs style payload.
	if blogs, ok := response["blogs"].([]any); ok {
		for _, b := range blogs {
			bm, ok := b.(map[string]any)
			if !ok {
				continue
			}
			add(anyToString(bm["name"]))
		}
	}

	return names, nil
}

// discoverBlogsFromQueries runs Tumblr blog-search for each query and returns
// all unique blogs in search-rank order (first results first).
func discoverBlogsFromQueries(client *http.Client, auth Auth, queries []string, perQuery int) []string {
	seen := map[string]bool{}
	var out []string
	for _, q := range queries {
		q = strings.TrimSpace(q)
		if q == "" {
			continue
		}
		fmt.Printf("  blog search: %q\n", q)
		names, err := searchBlogs(client, auth, q, perQuery)
		if err != nil {
			fmt.Println("   blog search error:", err)
			continue
		}
		if len(names) == 0 {
			fmt.Println("   no blogs found")
			continue
		}
		fmt.Printf("   found %d blogs (first: %s)\n", len(names), names[0])
		for _, name := range names {
			key := strings.ToLower(name)
			if seen[key] {
				continue
			}
			seen[key] = true
			out = append(out, name)
		}
	}
	return out
}
