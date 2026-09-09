package main

import (
	"net/http"
	"strings"
)

func setCommonHeaders(req *http.Request, auth Auth) {
	req.Header.Set("Authorization", "Bearer "+auth.Bearer)
	req.Header.Set("Accept", "application/json;format=camelcase")
	req.Header.Set("Accept-Language", "en-GB,en-US;q=0.9,en;q=0.8")
	req.Header.Set("User-Agent", chromeUA)
	req.Header.Set("sec-ch-ua", chromeSecCHUA)
	req.Header.Set("sec-ch-ua-mobile", "?0")
	req.Header.Set("sec-ch-ua-platform", `"Windows"`)
	req.Header.Set("X-Ad-Blocker-Enabled", "0")
	req.Header.Set("X-Version", "redpop/3/0//redpop/")
	req.Header.Set("Origin", "https://www.tumblr.com")
	req.Header.Set("Sec-Fetch-Site", "same-origin")
	req.Header.Set("Sec-Fetch-Mode", "cors")
	req.Header.Set("Sec-Fetch-Dest", "empty")
	req.Header.Set("Cookie", matureCookieHeader(auth.Cookie))
}

func setHTMLHeaders(req *http.Request, auth Auth) {
	req.Header.Set("Accept", "text/html,application/xhtml+xml,application/xml;q=0.9,image/avif,image/webp,image/apng,*/*;q=0.8")
	req.Header.Set("Accept-Language", "en-GB,en-US;q=0.9,en;q=0.8")
	req.Header.Set("User-Agent", chromeUA)
	req.Header.Set("sec-ch-ua", chromeSecCHUA)
	req.Header.Set("sec-ch-ua-mobile", "?0")
	req.Header.Set("sec-ch-ua-platform", `"Windows"`)
	req.Header.Set("Sec-Fetch-Site", "same-origin")
	req.Header.Set("Sec-Fetch-Mode", "navigate")
	req.Header.Set("Sec-Fetch-Dest", "document")
	req.Header.Set("Sec-Fetch-User", "?1")
	req.Header.Set("Upgrade-Insecure-Requests", "1")
	req.Header.Set("Cookie", matureCookieHeader(auth.Cookie))
}

// matureCookieHeader forces Tumblr mature content visible for search/browse.
// New accounts often default to cl_pref=block, which makes NSFW searches look SFW.
func matureCookieHeader(cookie string) string {
	c := strings.TrimSpace(cookie)
	c = strings.ReplaceAll(c, "cl_pref=block", "cl_pref=show")
	c = strings.ReplaceAll(c, "cl_pref=hide", "cl_pref=show")
	if !strings.Contains(c, "cl_pref=") {
		if c != "" && !strings.HasSuffix(c, ";") && !strings.HasSuffix(c, "; ") {
			c += "; "
		}
		c += "cl_pref=show"
	}
	return c
}
