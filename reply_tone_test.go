package main

import (
	"strings"
	"testing"
)

func TestToneProblems(t *testing.T) {
	h := chat("them: You're doing just fine baby I love your blog", "you: aww thanks babe i love ur sweet words :3", "them: Hi baby")
	codes := problemCodes(replyProblems(h, "aww thanks baby that means so much :3\nwhat u up to?"))
	if !strings.Contains(codes, "misplaced-thanks") || !strings.Contains(codes, "same-opener") {
		t.Fatalf("got %q", codes)
	}

	h = chat("them: you look so cute")
	if codes := problemCodes(replyProblems(h, "aww thanks :3\nwhat made u say that?")); strings.Contains(codes, "misplaced-thanks") {
		t.Fatalf("thanks fits a compliment: %q", codes)
	}

	h = chat("them: my weeks are always hell at work")
	if codes := problemCodes(replyProblems(h, "ugh work is the worst\nwhat kinda things make u feel respected?")); !strings.Contains(codes, "therapist") {
		t.Fatalf("got %q", codes)
	}
}
