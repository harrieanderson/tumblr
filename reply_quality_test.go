package main

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

func chat(lines ...string) []ConvMessage {
	var h []ConvMessage
	for _, l := range lines {
		self := strings.HasPrefix(l, "you: ")
		h = append(h, ConvMessage{FromSelf: self, Text: strings.TrimPrefix(strings.TrimPrefix(l, "you: "), "them: ")})
	}
	return h
}

func hasProblem(ps []replyProblem, code string) bool {
	for _, p := range ps {
		if p.code == code {
			return true
		}
	}
	return false
}

// Replies that went out on 2026-09-29 and read badly.
func TestReplyProblemsCatchRealMisses(t *testing.T) {
	biker := chat("you: would u be going slow or fast rn",
		"them: Very slow and deep .. want to make you shake .. every stroke slow and deep .. then make you cum and fill you up")
	ps := replyProblems(biker, "omg that sounds sooo intense :3\nyeah i love it when its slow and deep making me shake")
	if !hasProblem(ps, "parrot") || !hasProblem(ps, "flat-ending") {
		t.Fatalf("biker reply should be parrot + flat, got %s", problemCodes(ps))
	}

	mourad := chat("you: what do u like to chat about first?", "them: My name is Mourad and I am 31 years old. What about you?")
	ps = replyProblems(mourad, "aww hey mourad nice to meet ya :3 i just woke up from a nap lol\nalsooo what kinda stuff makes a perfect day for u")
	if !hasProblem(ps, "ignored-intro") {
		t.Fatalf("mourad reply ignored his question, got %s", problemCodes(ps))
	}

	lenChat := chat("them: Well, it would be breeding, and full use anytime, wherever and however I want", "them: And I am Len, how about you?")
	if ps := replyProblems(lenChat, "len im zoey :3\nbreeding sounds intense i love that\nwhat does full use look like to u"); !hasProblem(ps, "narrates") {
		t.Fatalf("'sounds intense' is narration, got %s", problemCodes(ps))
	}

	dante := chat("them: It gets me pretty excited, because this horniness energy, like if it's uncontrollable", "them: What about you?")
	if ps := replyProblems(dante, "zoey here :3\ni love how u dig the nonstop horniness energy lol\nwhats the dirtiest thing ud do?"); !hasProblem(ps, "parrot") {
		t.Fatalf("'horniness energy' is copied, got %s", problemCodes(ps))
	}

	sexy := chat("you: im good just bored on my break", "them: You are so sexy.")
	if ps := replyProblems(sexy, "hehe stoppp ur so sweet :P\nmakes my break way better"); !hasProblem(ps, "flat-ending") {
		t.Fatalf("statement ending should be flat, got %s", problemCodes(ps))
	}
}

func TestReplyProblemsPassGoodReplies(t *testing.T) {
	good := map[string][]ConvMessage{
		"mm maybe... what would u start with :P\nwhats ur name btw": chat("them: Do you want to do sextin 🤤"),
		"omg stoppp ur so bad :P\nand then what":                    chat("them: id put ur legs behind ur ears and fill u up"),
		"hiii mark im zoey :3\nwhat are u up to tonight":            chat("them: My name is Mark, 34. what about you?"),
		"aww night babe :3\nsleep well":                             chat("them: gn i gotta sleep now"),
	}
	for reply, h := range good {
		if ps := replyProblems(h, reply); len(ps) != 0 {
			t.Errorf("%q flagged: %s", reply, problemCodes(ps))
		}
	}
}

func TestCritiqueNamesEachFix(t *testing.T) {
	ps := []replyProblem{{"parrot", "don't repeat their words"}, {"flat-ending", "end with a question"}}
	got := critiquePersona("PERSONA", "draft text", ps, "")
	for _, want := range []string{"PERSONA", "draft text", "don't repeat their words", "end with a question"} {
		if !strings.Contains(got, want) {
			t.Fatalf("critique missing %q: %q", want, got)
		}
	}
}

func TestDailyLimitResetParsesGroqMessage(t *testing.T) {
	msg := "429 Too Many Requests: Rate limit reached for model `qwen/qwen3.8-27b` in organization `org_x` service tier `on_demand` on tokens per day (TPD): Limit 200000, Used 199100, Requested 2950. Please try again in 1h2m3.5s. Need more tokens?"
	until, ok := dailyLimitReset(msg)
	if !ok {
		t.Fatal("per-day limit not detected")
	}
	if d := time.Until(until); d < time.Hour || d > 70*time.Minute {
		t.Fatalf("reset should be ~1h3m away, got %v", d)
	}
	if _, ok := dailyLimitReset("429: Rate limit reached ... on tokens per minute (TPM): try again in 3s"); ok {
		t.Fatal("per-minute limits must not block a model for the day")
	}
}

func TestAccountLockBlocksSecondHolder(t *testing.T) {
	id := "locktest-" + time.Now().Format("150405.000")
	defer os.RemoveAll(filepath.Join(profilesDataDir, id))
	release, _, ok := tryLockAccount(id, "messaging")
	if !ok {
		t.Fatal("first lock should succeed")
	}
	// Pretend another live process (this test's parent) owns it.
	raw := []byte(`{"pid":` + strconv.Itoa(os.Getppid()) + `,"since":"` + time.Now().Format(time.RFC3339) + `"}`)
	if err := os.WriteFile(accountLockPath(id, "messaging"), raw, 0o644); err != nil {
		t.Fatal(err)
	}
	if _, holder, ok := tryLockAccount(id, "messaging"); ok || holder != os.Getppid() {
		t.Fatalf("second run should be refused (ok=%v holder=%d)", ok, holder)
	}
	_ = os.Remove(accountLockPath(id, "messaging"))
	release()
}

func TestAIOutUntil(t *testing.T) {
	t.Chdir(t.TempDir())
	t.Setenv("AI_DISABLE_OPENROUTER", "1")
	cfg := AIConfig{Enabled: true, Provider: "groq", Model: "qwen/qwen3.8-27b", APIKey: "x"}
	if _, out := aiOutUntil(cfg); out {
		t.Fatal("nothing blocked yet")
	}
	soon := time.Now().Add(10 * time.Minute)
	blockAIModel("groq", "qwen/qwen3.8-27b", soon.Add(time.Hour))
	if _, out := aiOutUntil(cfg); out {
		t.Fatal("gpt-oss-120b still has budget")
	}
	blockAIModel("groq", "openai/gpt-oss-120b", soon)
	until, out := aiOutUntil(cfg)
	if !out || !until.Equal(soon) {
		t.Fatalf("want out until %v (soonest), got %v %v", soon, until, out)
	}
}
