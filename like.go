package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"math/rand"
	"net/http"
	"net/url"
	"strings"
	"time"
)

type LikeTarget struct {
	ID        string
	ReblogKey string
	Pinned    bool
	Summary   string
}

// likePinnedAndRecent likes a blog's pinned post (if any) plus up to extra recent posts.
func likePinnedAndRecent(client *http.Client, auth *Auth, blog string, cfg FollowConfig) {
	if !cfg.LikePinned && cfg.LikeExtraPosts <= 0 {
		return
	}

	targets, err := postsToLike(client, *auth, blog, cfg)
	if err != nil {
		fmt.Println("   like fetch fail:", err)
		return
	}
	if len(targets) == 0 {
		fmt.Println("   no posts to like")
		return
	}

	delay := cfg.LikeDelaySeconds
	if delay <= 0 {
		delay = 14
	}
	jitter := cfg.LikeDelayJitterSeconds
	if jitter < 0 {
		jitter = 0
	}

	for i, t := range targets {
		label := "post"
		if t.Pinned {
			label = "pinned"
		}
		fmt.Printf("   liking %s %s (%s)\n", label, t.ID, truncate(t.Summary, 40))
		if cfg.DryRun {
			continue
		}
		if err := likePost(client, *auth, t.ID, t.ReblogKey); err != nil {
			fmt.Println("    like fail:", err)
			if isUnauthorized(err) {
				if err := ensureSession(client, auth); err == nil {
					_ = likePost(client, *auth, t.ID, t.ReblogKey)
				}
			}
			continue
		}
		if i < len(targets)-1 {
			wait := delay
			if jitter > 0 {
				wait += rand.Intn(jitter + 1)
			}
			time.Sleep(time.Duration(wait) * time.Second)
		}
	}
}

func postsToLike(client *http.Client, auth Auth, blog string, cfg FollowConfig) ([]LikeTarget, error) {
	rawPosts, err := fetchBlogPostsRaw(client, auth, blog, 8)
	if err != nil {
		return nil, err
	}

	var pinned *LikeTarget
	var others []LikeTarget

	for _, m := range rawPosts {
		id := anyToString(m["id"])
		if id == "" {
			id = anyToString(m["idString"])
		}
		key := anyToString(m["reblogKey"])
		if key == "" {
			key = anyToString(m["reblog_key"])
		}
		if id == "" || key == "" {
			continue
		}
		summary := anyToString(m["summary"])
		if summary == "" {
			summary = extractTextFromContent(m["content"])
		}
		t := LikeTarget{ID: id, ReblogKey: key, Summary: summary, Pinned: isPinnedPost(m)}
		if t.Pinned && pinned == nil {
			cp := t
			pinned = &cp
			continue
		}
		others = append(others, t)
	}

	var out []LikeTarget
	if cfg.LikePinned && pinned != nil {
		out = append(out, *pinned)
	}
	extra := cfg.LikeExtraPosts
	if extra < 0 {
		extra = 0
	}
	for i := 0; i < len(others) && i < extra; i++ {
		out = append(out, others[i])
	}
	// If no pin but likePinned requested, still like extras from recent.
	if len(out) == 0 && extra > 0 {
		for i := 0; i < len(others) && i < extra; i++ {
			out = append(out, others[i])
		}
	}
	return out, nil
}

func isPinnedPost(m map[string]any) bool {
	for _, k := range []string{"isPinned", "is_pinned", "pinned"} {
		switch v := m[k].(type) {
		case bool:
			if v {
				return true
			}
		case string:
			if strings.EqualFold(v, "true") || v == "1" {
				return true
			}
		}
	}
	return false
}

func fetchBlogPostsRaw(client *http.Client, auth Auth, blog string, limit int) ([]map[string]any, error) {
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

	var raw map[string]any
	if err := json.Unmarshal(body, &raw); err != nil {
		return nil, err
	}
	response, _ := raw["response"].(map[string]any)
	if response == nil {
		return nil, fmt.Errorf("missing response")
	}

	var list []any
	if posts, ok := response["posts"].([]any); ok {
		list = posts
	} else if timeline, ok := response["timeline"].(map[string]any); ok {
		if els, ok := timeline["elements"].([]any); ok {
			list = els
		}
	}

	out := make([]map[string]any, 0, len(list))
	for _, el := range list {
		if m, ok := el.(map[string]any); ok {
			out = append(out, m)
		}
	}
	return out, nil
}

func likePost(client *http.Client, auth Auth, postID, reblogKey string) error {
	payload, _ := json.Marshal(map[string]string{
		"id":         postID,
		"reblog_key": reblogKey,
	})
	req, err := http.NewRequest("POST", "https://www.tumblr.com/api/v2/user/like", bytes.NewReader(payload))
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
