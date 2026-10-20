package main

import "testing"

func TestDeadTextOnlyCountsOnAccountPages(t *testing.T) {
	yes := []string{"https://www.tumblr.com/", "https://www.tumblr.com/login", "https://www.tumblr.com/dashboard", "https://www.tumblr.com/settings/account", "https://www.tumblr.com/terminated?x=1"}
	no := []string{"https://www.tumblr.com/messaging", "https://www.tumblr.com/messaging/conversation/123", "https://www.tumblr.com/someblog", "https://www.tumblr.com/someblog/123/post", "https://example.com/login", "https://someblog.tumblr.com/", "https://someblog.tumblr.com/login"}
	for _, u := range yes {
		if !deadTextCountsOn(u) {
			t.Errorf("%s should count", u)
		}
	}
	for _, u := range no {
		if deadTextCountsOn(u) {
			t.Errorf("%s must not count — that text is about other people there", u)
		}
	}
}

func TestTerminatedURLIsThePageNotALink(t *testing.T) {
	if !isTumblrTerminatedURL("https://www.tumblr.com/terminated") {
		t.Fatal("real terminated page")
	}
	if isTumblrTerminatedURL("https://www.tumblr.com/someblog/123/terminated-account-story") {
		t.Fatal("a post slug is not the terminated page")
	}
}

func TestSidFromCookieHeader(t *testing.T) {
	if got := sidFromCookieHeader("a=1; sid=abc123; b=2"); got != "abc123" {
		t.Fatalf("got %q", got)
	}
	if sidFromCookieHeader("a=1; xsid=nope") != "" {
		t.Fatal("must match the sid cookie exactly")
	}
}
