package main

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestParseCookieHeader(t *testing.T) {
	got := parseCookieHeader("sid=abc; cl_pref=block; tz=Europe/London")
	if got["sid"] != "abc" || got["cl_pref"] != "block" || got["tz"] != "Europe/London" {
		t.Fatalf("parse: %#v", got)
	}
}

func TestAuthJarSendsMatureCookiesAndKeepsSetCookie(t *testing.T) {
	auth := &Auth{Cookie: "sid=test-sid; logged_in=1; cl_pref=block"}
	var sawCookie string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		sawCookie = r.Header.Get("Cookie")
		http.SetCookie(w, &http.Cookie{Name: "pfu", Value: "from-server", Path: "/"})
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{}`))
	}))
	defer server.Close()

	client := newAuthClient(auth)
	// Point Tumblr-shaped host at the test server via a rewrite transport under the jar.
	jarT, ok := client.Transport.(*authJarTransport)
	if !ok {
		t.Fatal("expected auth jar transport")
	}
	jarT.base = rewriteHost{target: server.URL, base: http.DefaultTransport}

	req, err := http.NewRequest("GET", "https://www.tumblr.com/api/v2/user/info", nil)
	if err != nil {
		t.Fatal(err)
	}
	setCommonHeaders(req, *auth)
	resp, err := client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()

	if !strings.Contains(sawCookie, "sid=test-sid") {
		t.Fatalf("missing sid in request cookie: %q", sawCookie)
	}
	if !strings.Contains(sawCookie, "cl_pref=show") {
		t.Fatalf("mature pref not forced: %q", sawCookie)
	}
	if strings.Contains(sawCookie, "cl_pref=block") {
		t.Fatalf("blocked pref should be replaced: %q", sawCookie)
	}
	if !strings.Contains(auth.Cookie, "pfu=from-server") {
		t.Fatalf("Set-Cookie not absorbed into auth: %q", auth.Cookie)
	}
	if !strings.Contains(auth.Cookie, "sid=test-sid") {
		t.Fatalf("sid lost after Set-Cookie: %q", auth.Cookie)
	}
}

func TestAuthJarReseedsAfterExternalCookieUpdate(t *testing.T) {
	auth := &Auth{Cookie: "sid=old"}
	client := newAuthClient(auth)
	var saw string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		saw = r.Header.Get("Cookie")
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()
	jarT := client.Transport.(*authJarTransport)
	jarT.base = rewriteHost{target: server.URL, base: http.DefaultTransport}

	auth.Cookie = "sid=new-from-chrome; logged_in=1"
	req, _ := http.NewRequest("GET", "https://www.tumblr.com/dashboard", nil)
	resp, err := client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if !strings.Contains(saw, "sid=new-from-chrome") {
		t.Fatalf("jar did not reseed from auth: %q", saw)
	}
}

func TestAttachAuthJarIdempotent(t *testing.T) {
	auth := &Auth{Cookie: "sid=a"}
	c := newHumanClient()
	attachAuthJar(c, auth)
	first := c.Transport
	attachAuthJar(c, auth)
	if c.Transport != first {
		t.Fatal("reattach should reseed, not wrap again")
	}
}

// rewriteHost sends requests to target while leaving the original URL host
// intact so tumblrCookieURL still matches.
type rewriteHost struct {
	target string
	base   http.RoundTripper
}

func (r rewriteHost) RoundTrip(req *http.Request) (*http.Response, error) {
	clone := req.Clone(req.Context())
	u, err := http.NewRequest(req.Method, r.target+req.URL.RequestURI(), req.Body)
	if err != nil {
		return nil, err
	}
	clone.URL = u.URL
	clone.Host = u.URL.Host
	base := r.base
	if base == nil {
		base = http.DefaultTransport
	}
	return base.RoundTrip(clone)
}
