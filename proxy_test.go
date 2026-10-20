package main

import (
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"
)

func testProxyCfg(urls ...string) ProxyConfig {
	cfg := defaultProxyConfig()
	cfg.Enabled = true
	cfg.StickyMinutes = 30
	cfg.MinRequestGapMs = 0
	cfg.PerProxyMaxConcurrent = 4
	cfg.PerAccountRPM = 100
	cfg.PerProxyRPM = 100
	cfg.Backoff.JitterPercent = 0
	for i, u := range urls {
		cfg.Proxies = append(cfg.Proxies, ProxyEntry{
			Label: string(rune('a' + i)),
			URL:   u,
		})
	}
	return cfg
}

func TestPickProxyJugglesAccountsAcrossPool(t *testing.T) {
	r := newProxyRouter(testProxyCfg(
		"http://user:pass@p1.example:8080",
		"http://user:pass@p2.example:8080",
		"http://user:pass@p3.example:8080",
	))

	seen := map[string]string{}
	for _, acc := range []string{"acc1", "acc2", "acc3", "acc4", "acc5", "acc6"} {
		ep, err := r.pickProxy(acc)
		if err != nil || ep == nil {
			t.Fatalf("pick %s: %v %v", acc, ep, err)
		}
		seen[acc] = ep.id
	}

	used := map[string]int{}
	for _, id := range seen {
		used[id]++
	}
	if len(used) < 2 {
		t.Fatalf("expected accounts spread across proxies, got %#v", seen)
	}
	for _, n := range used {
		if n > 3 {
			t.Fatalf("load uneven: %#v", used)
		}
	}
}

func TestPickProxyStickyThenRebalanceAfterCooldown(t *testing.T) {
	r := newProxyRouter(testProxyCfg(
		"http://p1.example:8080",
		"http://p2.example:8080",
	))

	ep1, _ := r.pickProxy("zoey1")
	ep1b, _ := r.pickProxy("zoey1")
	if ep1.id != ep1b.id {
		t.Fatalf("sticky broken: %s vs %s", ep1.id, ep1b.id)
	}

	r.reportResult("zoey1", ep1.id, http.StatusTooManyRequests, nil, http.Header{
		"Retry-After": []string{"30"},
	})

	ep2, _ := r.pickProxy("zoey1")
	if ep2.id == ep1.id {
		t.Fatalf("expected reassignment after cooldown, still on %s", ep1.id)
	}
}

func TestBackoffHonorsRetryAfterCap(t *testing.T) {
	r := newProxyRouter(testProxyCfg("http://p1.example:8080"))
	r.cfg.RespectRetryAfter = true
	r.cfg.Backoff.InitialSeconds = 5
	r.cfg.Backoff.MaxSeconds = 60
	r.cfg.Backoff.JitterPercent = 0

	d := r.backoffDuration(1, 45*time.Second)
	if d < 45*time.Second || d > 60*time.Second {
		t.Fatalf("expected Retry-After-driven wait ~45s, got %s", d)
	}

	d2 := r.backoffDuration(1, 5*time.Minute)
	if d2 != 60*time.Second {
		t.Fatalf("expected cap at maxSeconds, got %s", d2)
	}
}

func TestParseRetryAfter(t *testing.T) {
	if d := parseRetryAfter(http.Header{"Retry-After": []string{"12"}}); d != 12*time.Second {
		t.Fatalf("seconds: got %s", d)
	}
	if d := parseRetryAfter(nil); d != 0 {
		t.Fatalf("nil: got %s", d)
	}
}

func TestRedactProxyURL(t *testing.T) {
	got := redactProxyURL("http://alice:secret@host:8080")
	if got == "" || containsAny(got, "secret", "alice") {
		t.Fatalf("credentials leaked: %q", got)
	}
}

func TestProxyFuncDisabledFallsBackToEnv(t *testing.T) {
	// Ensure disabled router does not force a proxy URL.
	r := newProxyRouter(defaultProxyConfig())
	if r.enabled() {
		t.Fatal("default should be disabled")
	}
}

func TestProxyIDOmitsCredentials(t *testing.T) {
	id := proxyIDFor("us-1", "http://alice:secret@host.example:8080")
	if containsAny(id, "alice", "secret") {
		t.Fatalf("id leaked creds: %q", id)
	}
	u, _ := url.Parse("http://alice:secret@host.example:8080")
	if u.User == nil {
		t.Fatal("parse sanity")
	}
}

func containsAny(s string, parts ...string) bool {
	for _, p := range parts {
		if p != "" && strings.Contains(s, p) {
			return true
		}
	}
	return false
}
