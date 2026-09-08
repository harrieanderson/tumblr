package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
)

func createPost(client *http.Client, auth Auth, text string) error {
	url := "https://www.tumblr.com/api/v2/blog/" + auth.Blog + "/posts"

	payload := map[string]any{
		"layout": []map[string]any{
			{"type": "rows", "display": []map[string]any{{"blocks": []int{0}}}},
		},
		"state":      "published",
		"hide_trail": false,
		"content": []map[string]any{
			{"type": "text", "text": text},
		},
		"tags":                       "",
		"has_community_label":        false,
		"community_label_categories": []any{},
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return err
	}

	req, err := http.NewRequest("POST", url, bytes.NewReader(body))
	if err != nil {
		return err
	}
	setCommonHeaders(req, auth)
	req.Header.Set("Content-Type", "application/json; charset=utf8")
	req.Header.Set("X-CSRF", auth.CSRF)
	req.Header.Set("Referer", "https://www.tumblr.com/new/text")
	req.Header.Set("sec-ch-ua", `"Chromium";v="152", "Not?A_Brand";v="24", "Google Chrome";v="152"`)
	req.Header.Set("sec-ch-ua-mobile", "?0")
	req.Header.Set("sec-ch-ua-platform", `"Windows"`)

	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return err
	}

	fmt.Println("Status:", resp.Status)
	fmt.Println(string(respBody))

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("post failed: %s — %s", resp.Status, string(respBody))
	}
	return nil
}
