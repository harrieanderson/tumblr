package main

import (
	"testing"
	"time"
)

func TestParseForArg(t *testing.T) {
	d, err := parseForArg("60")
	if err != nil || d != 60*time.Minute {
		t.Fatalf("60 minutes: %v %v", d, err)
	}
	d, err = parseForArg("1h")
	if err != nil || d != time.Hour {
		t.Fatalf("1h: %v %v", d, err)
	}
	if _, err := parseForArg("0"); err == nil {
		t.Fatal("expected error for 0")
	}
}
