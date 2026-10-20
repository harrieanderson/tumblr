package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

func reblogPost(client *http.Client, auth Auth, p ScrapedPost) error {
	if p.ID == "" || p.ReblogKey == "" {
		return fmt.Errorf("missing id/reblog_key for reblog")
	}
	parentUUID := p.BlogUUID
	if parentUUID == "" {
		parentUUID = p.Blog
		if parentUUID != "" && !strings.Contains(parentUUID, ".") && !strings.HasPrefix(parentUUID, "t:") {
			parentUUID = parentUUID + ".tumblr.com"
		}
	}

	payload := map[string]any{
		"parent_tumblelog_uuid": parentUUID,
		"parent_post_id":        p.ID,
		"reblog_key":            p.ReblogKey,
		"state":                 "published",
		"hide_trail":            false,
		"content":               []any{},
		"tags":                  "",
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return err
	}

	url := "https://www.tumblr.com/api/v2/blog/" + auth.Blog + "/posts"
	req, err := http.NewRequest("POST", url, bytes.NewReader(body))
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
	takeCSRF(resp.Header, &auth)
	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("%s: %s", resp.Status, truncate(string(respBody), 200))
	}
	fmt.Println("  reblogged", p.Blog+"/"+p.ID, "->", resp.Status)
	return nil
}

func runProbeReblog() {
	auth, err := loadAuth()
	if err != nil {
		fmt.Println(err)
		return
	}
	fmt.Println("syncing cookies from chrome…")
	if err := syncCookiesFromChrome(&auth); err != nil {
		fmt.Println("cookie sync:", err)
	}
	client := &http.Client{Timeout: 45 * time.Second}
	if err := ensureSession(client, &auth); err != nil {
		fmt.Println("session:", err)
		fmt.Println("trying login…")
		if err := loginWithRealChrome(&auth); err != nil {
			fmt.Println("login fail:", err)
			return
		}
		if err := ensureSession(client, &auth); err != nil {
			fmt.Println("session still dead:", err)
			return
		}
	}
	blog, _ := syncAuthIdentity(client, &auth)
	if usableBlogName(blog) {
		fmt.Println("as", blog)
	}

	seed := "madamedesiderio"
	if cfg := loadFollowConfig(); len(cfg.SeedBlogs) > 0 {
		seed = normalizeSeedBlogs(cfg.SeedBlogs)[0]
	}
	posts, err := fetchBlogPosts(client, &auth, seed, 5)
	if err != nil || len(posts) == 0 {
		fmt.Println("fetch posts:", err)
		return
	}
	var p ScrapedPost
	for _, cand := range posts {
		if cand.ID == "" {
			continue
		}
		if cand.ReblogKey == "" {
			if enriched, e2 := fetchSinglePost(client, auth, cand); e2 == nil {
				cand = enriched
			}
		}
		if cand.ReblogKey != "" {
			p = cand
			break
		}
	}
	if p.ID == "" || p.ReblogKey == "" {
		fmt.Println("no reblogable post on", seed)
		return
	}
	fmt.Println("target", p.Blog+"/"+p.ID)

	if err := likePost(client, auth, p.ID, p.ReblogKey); err != nil {
		fmt.Println("like: FAIL", err)
	} else {
		fmt.Println("like: OK")
	}
	if err := reblogPost(client, auth, p); err != nil {
		fmt.Println("reblog: FAIL", err)
	} else {
		fmt.Println("reblog: OK")
	}
}
