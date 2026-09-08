package main

import "net/http"

func setCommonHeaders(req *http.Request, auth Auth) {
	req.Header.Set("Authorization", "Bearer "+auth.Bearer)
	req.Header.Set("Accept", "application/json;format=camelcase")
	req.Header.Set("accept-language", "en-us")
	req.Header.Set("User-Agent", "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/152.0.0.0 Safari/537.36")
	req.Header.Set("X-Ad-Blocker-Enabled", "0")
	req.Header.Set("X-Version", "redpop/3/0//redpop/")
	req.Header.Set("Origin", "https://www.tumblr.com")
	req.Header.Set("Sec-Fetch-Site", "same-origin")
	req.Header.Set("Sec-Fetch-Mode", "cors")
	req.Header.Set("Sec-Fetch-Dest", "empty")
	req.Header.Set("Cookie", auth.Cookie)
}
