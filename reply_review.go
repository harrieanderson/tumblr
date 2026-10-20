package main

import (
	"encoding/json"
	"fmt"
	"os"
	"regexp"
	"strings"
	"time"
)

// A second, cheap AI pass reads each drafted DM against the recent chat and
// vetoes the ones that misread them or make no sense. The rule checks in
// reply_quality.go only catch form; this catches meaning.

const reviewerPersona = `You check a Tumblr DM reply drafted for Zoey (25, flirty, casual lowercase texter) before it is sent. Be strict about sense, relaxed about style.

Reject (ok=false) when the draft:
- ignores, misreads or answers something different from what they actually said in their latest messages
- contradicts what they told her (their name, age, job, location, plans) or what Zoey said earlier
- refuses, dodges or questions info they already gave ("ur name isnt X", "not giving that away" when they shared it)
- is a non-sequitur, nonsense, or a weird phrase no real girl would text
- repeats a line or question Zoey already sent recently
- claims to see a picture when the chat only shows (image) or (non-text)
- sounds robotic, generic, preachy or like customer support

- picks a fight, mocks or calls them a liar/scammer without a clear reason
- says she is sending, has sent, or is about to send a photo/video/pic ("here it comes", "front view just for u", "did u like the pic", "my internet glitched sending it") — she cannot send media in DMs, so this is always a lie

Check EVERY bubble on its own: would the other person read it and think "huh? that makes no sense" or "I literally just told you that"? If yes for any bubble, reject.

Examples that must be rejected:
- They: "I'm Anthony James from California." Draft: "california is not a first name lol" → he gave his name (Anthony), the bubble misreads him.
- They: "Tengo 29 años" Draft: "29 is def the perfect age for a trade deal" → "trade deal" is nonsense.
- They: "John" (answering her name question) Draft: "john im not giving that away" → dodges info he just gave.
- They: "(image)" Draft: "omg ur so cute in that pic" → she cannot see it.
- They: "Hi baby" Draft: "aww thanks baby that means so much" → thanks for a compliment they did not give.
- Draft: "what kinda things make u feel respected" → therapist/interview question, not flirty texting.

Do NOT reject for: flirting or explicit talk, slang/typos/lowercase, short replies, a "how old r u" or "whats ur name" question at the end (unless they already said it), a mention of her pinned post.

Answer ONLY with JSON: {"ok": true or false, "why": "max 15 words, what is wrong"}`

var reviewJSONRe = regexp.MustCompile(`(?s)\{.*\}`)

// reviewDraft returns ok=true when the draft can be sent. If no model is
// available the draft passes unreviewed (the rule checks still ran).
func reviewDraft(cfg AIConfig, otherName string, history []ConvMessage, draft string) (ok bool, why string) {
	cfg.normalize()
	if !cfg.Enabled || (cfg.Provider != "groq" && cfg.Provider != "openrouter") {
		return true, ""
	}
	key := resolveAIAPIKey(cfg)
	recent := history
	if len(recent) > 10 {
		recent = recent[len(recent)-10:]
	}
	who := otherName
	if who == "" {
		who = "them"
	}
	user := fmt.Sprintf("Chat with %s (oldest to newest):\n%s\n\nDraft reply from Zoey:\n%s", who, chatTranscript(recent), draft)
	msgs := []chatMsg{{Role: "system", Content: reviewerPersona}, {Role: "user", Content: user}}

	cfg.Temperature = 0
	cfg.MaxTokens = 400
	type attempt struct {
		cfg AIConfig
		key string
	}
	var tries []attempt
	models := []string{cfg.Model}
	if cfg.Provider == "groq" {
		models = append(models, groqFallbackModels(cfg.Model)...)
	}
	for _, m := range models {
		if _, blocked := aiModelBlockedUntil(cfg.Provider, m); !blocked {
			c := cfg
			c.Model = m
			tries = append(tries, attempt{c, key})
		}
	}
	or := cfg
	or.Provider, or.BaseURL = "openrouter", ""
	if orKey := resolveAIAPIKey(or); orKey != "" && os.Getenv("AI_DISABLE_OPENROUTER") != "1" {
		if _, blocked := aiModelBlockedUntil("openrouter", "*"); !blocked {
			for _, m := range openRouterFreeModels {
				c := or
				c.Model = m
				tries = append(tries, attempt{c, orKey})
			}
		}
	}
	aiReplyMu.Lock()
	defer aiReplyMu.Unlock()
	for _, t := range tries {
		model := t.cfg.Model
		raw, err := openAICompatChatOnce(t.cfg, t.key, msgs)
		if err != nil {
			if until, daily := dailyLimitReset(err.Error()); daily && t.cfg.Provider == "groq" {
				blockAIModel("groq", model, until)
			}
			if d, ok := openRouterCooldown(err); ok && t.cfg.Provider == "openrouter" {
				blockAIModel("openrouter", "*", time.Now().Add(d))
				break
			}
			fmt.Printf("   review: %s failed (%s)\n", model, truncate(err.Error(), 80))
			continue
		}
		var v struct {
			OK  bool   `json:"ok"`
			Why string `json:"why"`
		}
		if m := reviewJSONRe.FindString(raw); m == "" || json.Unmarshal([]byte(m), &v) != nil {
			fmt.Printf("   review: %s gave no verdict (%q)\n", model, truncate(raw, 80))
			continue
		}
		return v.OK, strings.TrimSpace(v.Why)
	}
	fmt.Println("   review unavailable — sending on the rule checks alone")
	return true, ""
}

func reviewedSource(src string) bool {
	switch src {
	case "", "template", "slow-replies", "beacons", "minor-blocked", "repeat-skipped", "ai-exhausted", "draft-failed", "held-review":
		return false
	}
	return true
}

func withReviewerFeedback(persona, draft, why string) string {
	return persona + "\n\nA reviewer rejected your previous draft for this reply:\n" + draft +
		"\nProblem: " + why + "\nWrite a better reply that reads their latest messages correctly."
}
