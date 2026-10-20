package main

import (
	"strings"
	"testing"
)

func TestUnansweredUserTurnKeepsStoryBeforeFinalQuestion(t *testing.T) {
	history := []ConvMessage{
		{Text: "old question already answered"},
		{FromSelf: true, Text: "thanks for explaining"},
		{Text: "My puppy keeps asking for walks."},
		{Text: "(image)"},
		{Text: "He was tugging my sleeve twenty minutes ago."},
		{Text: "Can we get to know each other?"},
	}
	turn := unansweredUserTurn(history)
	if strings.Contains(turn, "old question") || !strings.HasPrefix(turn, "My puppy") || !strings.HasSuffix(turn, "Can we get to know each other?") {
		t.Fatalf("wrong unanswered turn: %q", turn)
	}
	prompt := buildChatMessages(AIConfig{Persona: "Draft a friendly non-explicit reply."}, "test", history)[1].Content
	if !strings.Contains(prompt, "Their unanswered messages (one turn, oldest to newest):\n"+turn) {
		t.Fatal("the focused reply context dropped part of the unanswered turn")
	}
	if !strings.Contains(prompt, "must not be another hi/hey/hii") {
		t.Fatal("continuing conversations must suppress greetings even when earlier self text was not a greeting")
	}
}

func TestRemoveRepeatedReplyGreeting(t *testing.T) {
	history := []ConvMessage{{FromSelf: true, Text: "hey hey :P"}, {Text: "hii"}}
	for _, tc := range []struct{ input, want string }{
		{"hey!! :P\nhow r u doing rn?", "how r u doing rn?"},
		{"omg hii :3\nhow is your puppy?", "how is your puppy?"},
		{"hello, how is your day?", "how is your day?"},
		{"hey hey :P\nhow are you?", "how are you?"},
		{"hiii :3", ""},
		{"your puppy sounds energetic!\nwhat breed is he?", "your puppy sounds energetic!\nwhat breed is he?"},
		{"history is interesting", "history is interesting"},
	} {
		if got := removeRepeatedReplyGreeting(tc.input, history); got != tc.want {
			t.Errorf("%q => %q, want %q", tc.input, got, tc.want)
		}
	}
	if got := removeRepeatedReplyGreeting("hey! nice to meet you", []ConvMessage{{Text: "hi"}}); got != "hey! nice to meet you" {
		t.Fatalf("initial greeting changed: %q", got)
	}
}

func TestUnansweredUserTurnAfterOurReplyIsEmpty(t *testing.T) {
	history := []ConvMessage{{Text: "how is it going?"}, {FromSelf: true, Text: "fine thanks"}}
	if got := unansweredUserTurn(history); got != "" {
		t.Fatalf("already answered messages treated as pending: %q", got)
	}
}
