package main

import (
	"net/http"
	"net/url"
	"sort"
	"strings"
	"sync"
)

// accountCookieJar is one cookie store per Auth (one account). Outbound
// requests get a Cookie header from this jar; Set-Cookie responses update
// both the jar and auth.Cookie, and persist when the header changes.
type accountCookieJar struct {
	mu     sync.Mutex
	auth   *Auth
	byName map[string]string
	saved  string
}

type authJarTransport struct {
	base http.RoundTripper
	jar  *accountCookieJar
}

// newAuthClient returns a human-like HTTP client whose cookies are owned by
// auth. Chrome login still mints the sid; this jar keeps it fresh for API work.
// When proxies are enabled, the account is sticky-juggled across the pool.
func newAuthClient(auth *Auth) *http.Client {
	id := ""
	if auth != nil {
		id = accountIDFromAuth(*auth)
	}
	c := newHumanClientFor(id)
	attachAuthJar(c, auth)
	return c
}

// attachAuthJar puts a per-account jar on client (or reseeds an existing one).
// Safe to call repeatedly; never shares jars across Auth pointers.
func attachAuthJar(client *http.Client, auth *Auth) {
	if client == nil || auth == nil {
		return
	}
	if t, ok := client.Transport.(*authJarTransport); ok {
		t.jar.mu.Lock()
		t.jar.auth = auth
		t.jar.reseedFromAuthLocked()
		t.jar.mu.Unlock()
		return
	}
	base := client.Transport
	if base == nil {
		base = http.DefaultTransport
	}
	jar := &accountCookieJar{auth: auth, byName: map[string]string{}}
	jar.reseedFromAuthLocked()
	jar.saved = strings.TrimSpace(auth.Cookie)
	client.Transport = &authJarTransport{base: base, jar: jar}
}

func (t *authJarTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	if t == nil {
		return http.DefaultTransport.RoundTrip(req)
	}
	base := t.base
	if base == nil {
		base = http.DefaultTransport
	}
	if req != nil && tumblrCookieURL(req.URL) && t.jar != nil {
		t.jar.mu.Lock()
		if t.jar.auth != nil {
			want := matureCookieHeader(t.jar.auth.Cookie)
			if want != t.jar.headerLocked() {
				t.jar.reseedFromAuthLocked()
			}
			if h := t.jar.headerLocked(); h != "" {
				req.Header.Set("Cookie", h)
			}
		}
		t.jar.mu.Unlock()
	}
	resp, err := base.RoundTrip(req)
	if err != nil || resp == nil || t.jar == nil {
		return resp, err
	}
	if req != nil && tumblrCookieURL(req.URL) {
		if cookies := resp.Cookies(); len(cookies) > 0 {
			t.jar.applySetCookies(cookies)
		}
	}
	return resp, nil
}

func tumblrCookieURL(u *url.URL) bool {
	if u == nil {
		return false
	}
	host := strings.ToLower(u.Hostname())
	return host == "tumblr.com" || strings.HasSuffix(host, ".tumblr.com")
}

func (j *accountCookieJar) reseedFromAuthLocked() {
	if j.auth == nil {
		j.byName = map[string]string{}
		return
	}
	j.byName = parseCookieHeader(j.auth.Cookie)
	j.byName["cl_pref"] = "show"
	if strings.TrimSpace(j.byName["tz"]) == "" {
		j.byName["tz"] = "Europe/London"
	}
	j.auth.Cookie = j.headerLocked()
}

func (j *accountCookieJar) headerLocked() string {
	if len(j.byName) == 0 {
		return ""
	}
	names := make([]string, 0, len(j.byName))
	for name, val := range j.byName {
		if strings.TrimSpace(name) == "" || strings.TrimSpace(val) == "" {
			continue
		}
		names = append(names, name)
	}
	sort.Slice(names, func(i, k int) bool {
		// sid first so logs/auth.json stay easy to scan
		if names[i] == "sid" {
			return true
		}
		if names[k] == "sid" {
			return false
		}
		return names[i] < names[k]
	})
	parts := make([]string, 0, len(names))
	for _, name := range names {
		parts = append(parts, name+"="+j.byName[name])
	}
	return strings.Join(parts, "; ")
}

func (j *accountCookieJar) applySetCookies(cookies []*http.Cookie) {
	if j == nil || len(cookies) == 0 {
		return
	}
	j.mu.Lock()
	defer j.mu.Unlock()
	if j.byName == nil {
		j.byName = map[string]string{}
	}
	changed := false
	for _, c := range cookies {
		if c == nil || strings.TrimSpace(c.Name) == "" {
			continue
		}
		if c.Domain != "" && !strings.Contains(strings.ToLower(c.Domain), "tumblr.com") {
			continue
		}
		if c.MaxAge < 0 {
			if _, ok := j.byName[c.Name]; ok {
				delete(j.byName, c.Name)
				changed = true
			}
			continue
		}
		if prev, ok := j.byName[c.Name]; !ok || prev != c.Value {
			j.byName[c.Name] = c.Value
			changed = true
		}
	}
	if !changed {
		return
	}
	j.byName["cl_pref"] = "show"
	if strings.TrimSpace(j.byName["tz"]) == "" {
		j.byName["tz"] = "Europe/London"
	}
	header := j.headerLocked()
	if j.auth != nil {
		j.auth.Cookie = header
	}
	if header == j.saved {
		return
	}
	// Persist only when we still have a session cookie — avoid wiping auth.json
	// with an empty jar after a logout-style Set-Cookie clear. Skip when path
	// is unset (tests / ephemeral Auth) so we never write the active profile.
	if j.auth == nil || j.auth.path == "" {
		j.saved = header
		return
	}
	if strings.Contains(header, "sid=") || strings.Contains(j.saved, "sid=") {
		j.saved = header
		_ = saveAuth(*j.auth)
	}
}

func parseCookieHeader(header string) map[string]string {
	out := map[string]string{}
	for _, part := range strings.Split(header, ";") {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		name, val, ok := strings.Cut(part, "=")
		name = strings.TrimSpace(name)
		if !ok || name == "" {
			continue
		}
		out[name] = strings.TrimSpace(val)
	}
	return out
}
