package main

import (
	"strings"
	"testing"
)

func TestBadLoginFillRetries(t *testing.T) {
	if badLoginFillRetries < 2 || badLoginFillRetries > 5 {
		t.Fatalf("badLoginFillRetries=%d want 2–5", badLoginFillRetries)
	}
}

func TestClearTumblrLoginFieldsJS(t *testing.T) {
	js := clearTumblrLoginFieldsJS
	for _, want := range []string{"select()", "password", "email", "deleteContentBackward"} {
		if !strings.Contains(js, want) {
			t.Fatalf("clear JS missing %q", want)
		}
	}
	fill := fillVisibleInputJS("email", "a@b.com")
	if !strings.Contains(fill, "select()") || !strings.Contains(fill, "insertText") {
		t.Fatal("fill should select+insertText so React login fields actually show text")
	}
}
