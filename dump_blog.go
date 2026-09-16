package main

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
)

func runDumpBlog(name string) {
	auth, err := loadAuth()
	if err != nil {
		fmt.Println(err)
		return
	}
	client := &http.Client{}
	if err := ensureSession(client, &auth); err != nil {
		fmt.Println(err)
		return
	}
	u := "https://www.tumblr.com/api/v2/blog/" + name + "/info"
	req, _ := http.NewRequest("GET", u, nil)
	setCommonHeaders(req, auth)
	req.Header.Set("X-CSRF", auth.CSRF)
	resp, err := client.Do(req)
	if err != nil {
		fmt.Println(err)
		return
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	_ = os.MkdirAll("scraped", 0755)
	path := "scraped/blog_" + name + ".json"
	_ = os.WriteFile(path, body, 0644)
	fmt.Println("Status:", resp.Status)
	fmt.Println("Wrote", path)

	var raw map[string]any
	_ = json.Unmarshal(body, &raw)
	respObj, _ := raw["response"].(map[string]any)
	blog, _ := respObj["blog"].(map[string]any)
	if blog == nil {
		fmt.Println(truncate(string(body), 500))
		return
	}
	fmt.Println("name:", blog["name"])
	fmt.Println("title:", blog["title"])
	fmt.Println("description:", truncate(anyToString(blog["description"]), 200))
	if theme, ok := blog["theme"].(map[string]any); ok {
		b, _ := json.MarshalIndent(theme, "", "  ")
		fmt.Println("theme:\n", string(b))
	}
	if av, ok := blog["avatar"].([]any); ok && len(av) > 0 {
		fmt.Println("avatar[0]:", av[0])
	}
}
