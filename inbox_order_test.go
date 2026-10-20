package main

import (
	"strings"
	"testing"
)

func TestLongestWaitingFirst(t *testing.T) {
	conv := func(id string, fromSelf bool, ts string) Conversation {
		return Conversation{ID: id, LastMessageFromSelf: fromSelf, Messages: []ConvMessage{{TS: ts}}}
	}
	got := longestWaitingFirst([]Conversation{
		conv("new", false, "1759500000"),
		conv("ours", true, "1759400000"),
		conv("old", false, "1758000000"),
		conv("mid", false, "1759000000"),
	})
	var ids []string
	for _, c := range got {
		ids = append(ids, c.ID)
	}
	if strings.Join(ids, ",") != "old,mid,new,ours" {
		t.Fatalf("order %v", ids)
	}
}

func TestSpamLinkBotSkipped(t *testing.T) {
	h := chat("them: hi", "them: Better than escorts - these horny moms do it for free", "them: chatxu.com/jf67L")
	if !looksLikeChatBot("tammymidsweekmilgleavet", h) {
		t.Fatal("escort link bot should be skipped")
	}
	h = chat("them: check out my art", "them: myportfolio.com/work")
	if looksLikeChatBot("artist", h) {
		t.Fatal("a normal link is not spam")
	}
}

func TestSlowRepliesAnswer(t *testing.T) {
	for _, s := range []string{
		"why dont you reply", "why do u take so long", "ur replies are so slow", "you never reply",
		"why are you ignoring me", "u ghosting me?", "took u forever lol", "why so slow", "where did you go",
		"left me on read", "Why didn't you answer me",
	} {
		if !askedWhySlowReplies(s) {
			t.Errorf("should catch %q", s)
		}
	}
	for _, s := range []string{
		"reply when ur free", "i answered the door", "slow mornings are the best", "my wifi is slow",
		"what did you reply to him", "hey whats up",
	} {
		if askedWhySlowReplies(s) {
			t.Errorf("false hit %q", s)
		}
	}

	t.Chdir(t.TempDir())
	h := chat("them: hey", "you: hii :3", "them: why do u take so long to reply")
	out, src := pickReplyTextFor(MessageConfig{}, "acct", "slowpoke", h)
	if src != "slow-replies" || !strings.Contains(out, "so slow on here") {
		t.Fatalf("src=%q out=%q", src, out)
	}
	if strings.Contains(out, "pinned") || !strings.Contains(out, "how old") {
		t.Fatalf("no pinned pointer before age is known, ask instead: %q", out)
	}

	h = chat("them: hey", "you: how old r u btw :3", "them: 29", "you: nice", "them: why do u take so long to reply")
	out, _ = pickReplyTextFor(MessageConfig{}, "acct", "adultslow", h)
	if !strings.Contains(out, "my pinned") {
		t.Fatalf("adults get the pinned pointer: %q", out)
	}
}
