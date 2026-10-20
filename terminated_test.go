package main

import (
	"strings"
	"testing"
)

func TestIsTumblrTerminatedURL(t *testing.T) {
	if !isTumblrTerminatedURL("https://www.tumblr.com/terminated") {
		t.Fatal("expected terminated dashboard URL")
	}
	if isTumblrTerminatedURL("https://www.tumblr.com/dashboard") {
		t.Fatal("dashboard is not terminated")
	}
	if isTumblrTerminatedURL("https://example.com/terminated") {
		t.Fatal("non-tumblr URL")
	}
}

func TestIsTumblrDeadAccountText(t *testing.T) {
	if !isTumblrDeadAccountText("You have deleted your account. Learn more") {
		t.Fatal("deleted banner")
	}
	if !isTumblrDeadAccountText("This account has been terminated") {
		t.Fatal("terminated copy")
	}
	if isTumblrDeadAccountText("Log in to Tumblr") {
		t.Fatal("normal login")
	}
	if !strings.Contains(chromeDeadAccountJS, "you have deleted your account") {
		t.Fatal("login JS should detect deleted accounts")
	}
}
