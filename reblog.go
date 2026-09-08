package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
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
