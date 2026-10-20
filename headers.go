package main

import (
	"net/http"
	"strings"
)

// Chrome desktop Accept-Encoding — transport decodes these in chrome_transport.go.
const chromeAcceptEncoding = "gzip, deflate, br, zstd"

func setCommonHeaders(req *http.Request, auth Auth) {
	// Chrome/redpop order matters for fingerprinting. Clear then set in sequence
	// so Go 1.22+ serializes insertion order on the wire.
	clearRequestHeaders(req)
	setHeaderOrder(req,
		"Authorization", "Bearer "+auth.Bearer,
		"sec-ch-ua-platform", `"Windows"`,
		"X-Ad-Blocker-Enabled", "0",
		"X-Version", "redpop/3/0//redpop/",
		"sec-ch-ua", chromeSecCHUA,
		"sec-ch-ua-mobile", "?0",
		"User-Agent", chromeUA,
		"Accept", "application/json;format=camelcase",
		"Origin", "https://www.tumblr.com",
		"Sec-Fetch-Site", "same-origin",
		"Sec-Fetch-Mode", "cors",
		"Sec-Fetch-Dest", "empty",
		"Accept-Encoding", chromeAcceptEncoding,
		"Accept-Language", "en-GB,en-US;q=0.9,en;q=0.8",
		"Cookie", matureCookieHeader(auth.Cookie),
		"Priority", "u=1, i",
	)
	// Callers often set Content-Type / Referer / X-CSRF after this.
}

func setHTMLHeaders(req *http.Request, auth Auth) {
	clearRequestHeaders(req)
	setHeaderOrder(req,
		"Upgrade-Insecure-Requests", "1",
		"User-Agent", chromeUA,
		"Accept", "text/html,application/xhtml+xml,application/xml;q=0.9,image/avif,image/webp,image/apng,*/*;q=0.8,application/signed-exchange;v=b3;q=0.7",
		"sec-ch-ua", chromeSecCHUA,
		"sec-ch-ua-mobile", "?0",
		"sec-ch-ua-platform", `"Windows"`,
		"Sec-Fetch-Site", "same-origin",
		"Sec-Fetch-Mode", "navigate",
		"Sec-Fetch-User", "?1",
		"Sec-Fetch-Dest", "document",
		"Accept-Encoding", chromeAcceptEncoding,
		"Accept-Language", "en-GB,en-US;q=0.9,en;q=0.8",
		"Cookie", matureCookieHeader(auth.Cookie),
		"Priority", "u=0, i",
	)
}

// setHeaderOrder sets headers in pairs (key, value...). Empty values are skipped
// so callers can fill Referer / Content-Type afterward without blanks on the wire.
func setHeaderOrder(req *http.Request, kv ...string) {
	if req.Header == nil {
		req.Header = make(http.Header)
	}
	for i := 0; i+1 < len(kv); i += 2 {
		k, v := kv[i], kv[i+1]
		if k == "" || v == "" {
			continue
		}
		req.Header.Set(k, v)
	}
}

func clearRequestHeaders(req *http.Request) {
	req.Header = make(http.Header)
}

// takeCSRF copies a rotated token from any Tumblr response. GET notes/posts
// keep working after a rotate; likes/follows 401 until we pick this up.
func takeCSRF(h http.Header, auth *Auth) {
	if auth == nil || h == nil {
		return
	}
	csrf := strings.TrimSpace(h.Get("X-CSRF"))
	if csrf == "" {
		csrf = strings.TrimSpace(h.Get("X-Csrf"))
	}
	if csrf != "" {
		auth.CSRF = csrf
	}
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

// commonHeaderOrder is the insertion sequence used by setCommonHeaders (tests).
var commonHeaderOrder = []string{
	"Authorization",
	"sec-ch-ua-platform",
	"X-Ad-Blocker-Enabled",
	"X-Version",
	"sec-ch-ua",
	"sec-ch-ua-mobile",
	"User-Agent",
	"Accept",
	"Origin",
	"Sec-Fetch-Site",
	"Sec-Fetch-Mode",
	"Sec-Fetch-Dest",
	"Accept-Encoding",
	"Accept-Language",
	"Cookie",
	"Priority",
}
