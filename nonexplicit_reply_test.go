package main

import (
	"encoding/json"
	"os"
	"strings"
	"testing"
	"time"
)

func TestCasualReplyPreservesEmojiAndBubbles(t *testing.T) {
	text := "oo i've been good\nomfg do u think so? ur so nice 😭"
	if got := sanitizeConfiguredReply(AIConfig{NonExplicit: true}, text); got != text {
		t.Fatalf("%q", got)
	}
}

func TestCasualPromptEvaluation(t *testing.T) {
	if os.Getenv("TUMBLR_CASUAL_EVAL") != "1" {
		t.Skip("manual synthetic draft evaluation")
	}
	cases := []struct {
		Name    string
		History []ConvMessage
	}{
		{"new greeting", []ConvMessage{{Text: "hey"}}},
		{"ongoing greeting", []ConvMessage{{FromSelf: true, Text: "hey!"}, {Text: "hiii"}}},
		{"Spanish", []ConvMessage{{Text: "Hablas español?"}}},
		{"compliment and question", []ConvMessage{{Text: "How have you been lately!"}, {Text: "Also you look adorable on your blog"}}},
		{"puppy story", []ConvMessage{{Text: "My puppy keeps tugging at my sleeve to go walking."}, {Text: "(image)"}, {Text: "Can we get to know each other?"}}},
		{"assumed hobby", []ConvMessage{{Text: "Heyyy I see you're a gamer"}}},
		{"unsupported prior location", []ConvMessage{{FromSelf: true, Text: "omg im near michigan"}, {Text: "Less than a five hour drive?"}}},
		{"serious news", []ConvMessage{{Text: "My dog is ill and I'm really worried"}}},
	}
	cfg := loadMessageConfig().AI
	cfg.NonExplicit = true
	type result struct {
		Case  string
		Reply string
		Error string
	}
	var results []result
	for _, c := range cases {
		reply, err := generateAIReply(cfg, "test", c.History)
		r := result{Case: c.Name, Reply: reply}
		if err != nil {
			r.Error = err.Error()
			t.Error(err)
		}
		results = append(results, r)
		t.Logf("%s: %s", c.Name, reply)
		time.Sleep(5 * time.Second)
	}
	raw, _ := json.MarshalIndent(results, "", "  ")
	if err := os.WriteFile("casual-prompt-evaluation.json", raw, 0600); err != nil {
		t.Fatal(err)
	}
}

func TestCasualPinMentionContextAndOnce(t *testing.T) {
	cfg := MessageConfig{PinnedMentionAfter: 6, PinnedMention: "also omg have u seen my pinned post? does it look nooby 😭😭😭 im so fking new to tumblr idk how it works"}
	var h []ConvMessage
	for i := 0; i < 5; i++ {
		h = append(h, ConvMessage{Text: "hello"}, ConvMessage{FromSelf: true, Text: "how is it going"})
	}
	h = append(h, ConvMessage{Text: "you look adorable on your blog"})
	got := appendCasualPinMention(cfg, h, "omfg ur so nice 😭")
	if !strings.Contains(got, "\nalso since u mentioned my blog, omg have u seen my pinned post?") {
		t.Fatal(got)
	}
	h = append(h, ConvMessage{FromSelf: true, Text: got}, ConvMessage{Text: "your blog is cool"})
	if got := appendCasualPinMention(cfg, h, "thanks"); got != "thanks" {
		t.Fatal("repeated mention", got)
	}
	h = h[:10]
	h = append(h, ConvMessage{Text: "my dog is ill"})
	if got := appendCasualPinMention(cfg, h, "i hope he's okay"); got != "i hope he's okay" {
		t.Fatal("off-topic redirect", got)
	}
	if got := appendCasualPinMention(cfg, []ConvMessage{{Text: "love your blog"}}, "thanks"); got != "thanks" {
		t.Fatal("mention too early")
	}
}

func TestCasualPromptUsesWholeTurn(t *testing.T) {
	msgs := buildChatMessages(AIConfig{NonExplicit: true}, "test", []ConvMessage{{Text: "How have you been lately!"}, {Text: "Also you look adorable on your blog"}})
	if !strings.Contains(msgs[1].Content, "How have you been lately!\nAlso you look adorable on your blog") {
		t.Fatal(msgs)
	}
	if strings.Contains(msgs[1].Content, "near there") {
		t.Fatal("legacy prompting leaked")
	}
}

func TestCasualReplyLiveStyle(t *testing.T) {
	if os.Getenv("TUMBLR_CASUAL_STYLE_TEST") != "1" {
		t.Skip("manual synthetic provider test")
	}
	cfg := loadMessageConfig().AI
	cfg.NonExplicit = true
	h := []ConvMessage{{FromSelf: true, Text: "what have u been up to"}, {Text: "How have you been lately!"}, {Text: "Also you look adorable on your blog"}}
	text, err := generateAIReply(cfg, "test", h)
	if err != nil {
		t.Fatal(err)
	}
	t.Log("STYLE DRAFT:", text)
	if text == "" {
		t.Fatal("empty draft")
	}
}
