package main

import (
	"fmt"
	"io"
	"math/rand"
	"net/http"
	"os"
	"strings"
	"time"
)

// Chrome client hints — keep UA / sec-ch-ua in sync (desktop Windows Chrome).
const (
	chromeUA      = "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/152.0.0.0 Safari/537.36"
	chromeSecCHUA = `"Chromium";v="152", "Not?A_Brand";v="24", "Google Chrome";v="152"`
)

func newHumanClient() *http.Client {
	return &http.Client{
		Timeout: 45 * time.Second,
		Transport: &http.Transport{
			Proxy:                 http.ProxyFromEnvironment,
			MaxIdleConns:          20,
			IdleConnTimeout:       90 * time.Second,
			TLSHandshakeTimeout:   15 * time.Second,
			ExpectContinueTimeout: 1 * time.Second,
		},
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			if len(via) >= 8 {
				return fmt.Errorf("too many redirects")
			}
			return nil
		},
	}
}

func paceFast() bool {
	v := strings.TrimSpace(os.Getenv("TUMBLR_FAST"))
	return v == "1" || strings.EqualFold(v, "true") || strings.EqualFold(v, "yes")
}

// humanPause sleeps a random duration with light jitter (seconds).
func humanPause(minSec, maxSec int) {
	if sessionExpired() {
		return
	}
	if maxSec < minSec {
		minSec, maxSec = maxSec, minSec
	}
	if paceFast() {
		minSec = (minSec + 1) / 2
		maxSec = (maxSec + 1) / 2
	}
	if minSec < 1 {
		minSec = 1
	}
	sec := minSec
	if maxSec > minSec {
		sec = minSec + rand.Intn(maxSec-minSec+1)
	}
	if !paceFast() && rand.Float64() < 0.16 {
		sec += randRange(20, 80)
	}
	d := time.Duration(sec) * time.Second
	if rem := sessionRemaining(); rem > 0 && d > rem {
		d = rem
	}
	time.Sleep(d)
}

func humanThink() {
	humanPause(3, 12)
}

// persistMaturePref writes cl_pref=show into the saved cookie string so it
// survives reloads and matches what a real NSFW-browsing user would have.
func persistMaturePref(auth *Auth) {
	auth.Cookie = matureCookieHeader(auth.Cookie)
	if !strings.Contains(auth.Cookie, "tz=") {
		auth.Cookie += "; tz=Europe/London"
	}
	_ = saveAuth(*auth)
}

// warmSession mimics opening Tumblr: dashboard loads, short scrolls, no follows.
func warmSession(client *http.Client, auth *Auth) {
	fmt.Println("Warming up session (human open-app ritual)…")
	persistMaturePref(auth)
	_ = ensureSession(client, auth)
	if name, err := syncAuthIdentity(client, auth); err == nil && name != "" {
		fmt.Println("  signed in as:", name)
		rememberManagedBlog(accountIDFromAuth(*auth), name)
	}
	rememberBlogsFromAuth(client, auth)
	logOwnFollowers(client, auth, nil)
	_ = browseDashboard(client, *auth)
	humanPause(4, 12)
	_ = browseDashboard(client, *auth)
	if rand.Float64() < 0.55 {
		humanPause(6, 18)
		_ = browseDashboard(client, *auth)
	}
	fmt.Println("Warm-up done — starting slow session.")
	humanPause(5, 14)
}

// peekBlog loads a blog page the way a person would before liking/following.
func peekBlog(client *http.Client, auth Auth, blog string) {
	u := "https://www.tumblr.com/" + strings.TrimPrefix(blog, "@")
	req, err := http.NewRequest("GET", u, nil)
	if err != nil {
		return
	}
	setHTMLHeaders(req, auth)
	req.Header.Set("Referer", "https://www.tumblr.com/dashboard")
	resp, err := client.Do(req)
	if err != nil {
		return
	}
	defer resp.Body.Close()
	_, _ = io.CopyN(io.Discard, resp.Body, 256*1024)
}
