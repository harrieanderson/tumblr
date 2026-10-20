package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math/rand"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"
	"unicode/utf8"
)

var aiReplyMu sync.Mutex

type AIConfig struct {
	NonExplicit bool    `json:"nonExplicit"`
	Enabled     bool    `json:"enabled"`
	Provider    string  `json:"provider"` // groq | ollama | openrouter | gemini
	Model       string  `json:"model"`
	APIKey      string  `json:"apiKey"`
	BaseURL     string  `json:"baseURL"`
	Persona     string  `json:"persona"`
	MaxTokens   int     `json:"maxTokens"`
	Temperature float64 `json:"temperature"`
}

func defaultAIConfig() AIConfig {
	return AIConfig{
		Enabled:     true,
		Provider:    "groq",
		Model:       "qwen/qwen3.8-27b",
		Persona:     defaultPersona,
		MaxTokens:   180,
		Temperature: 0.9,
	}
}

func fillAIPersona(persona, blogName string) string {
	p := strings.TrimSpace(persona)
	if p == "" {
		p = defaultAIConfig().Persona
	}
	name := strings.TrimSpace(blogName)
	if !usableBlogName(name) {
		name = "this blog"
	}
	p = strings.ReplaceAll(p, "{blog}", name)
	p = strings.ReplaceAll(p, "lillycosmic", name)
	if strings.Contains(p, "You are Lilly") && !strings.Contains(p, "You are Zoey") {
		p = strings.ReplaceAll(p, "You are Lilly", "You are Zoey")
	}
	return p
}

func (a *AIConfig) normalize() {
	d := defaultAIConfig()
	a.Provider = strings.ToLower(strings.TrimSpace(a.Provider))
	if a.Provider == "" {
		a.Provider = d.Provider
	}
	if a.Model == "" {
		a.Model = d.Model
		if a.Provider == "ollama" {
			a.Model = "llama3.2"
		}
		if a.Provider == "gemini" {
			a.Model = "gemini-2.0-flash"
		}
	}
	if strings.TrimSpace(a.Persona) == "" {
		a.Persona = d.Persona
	}
	if a.MaxTokens <= 0 {
		a.MaxTokens = d.MaxTokens
	}
	if a.Temperature <= 0 {
		a.Temperature = d.Temperature
	}
}

func resolveAIAPIKey(cfg AIConfig) string {
	if k := strings.TrimSpace(cfg.APIKey); k != "" {
		return k
	}
	switch cfg.Provider {
	case "groq":
		if k := strings.TrimSpace(os.Getenv("GROQ_API_KEY")); k != "" {
			return k
		}
	case "openrouter":
		if k := strings.TrimSpace(os.Getenv("OPENROUTER_API_KEY")); k != "" {
			return k
		}
	case "gemini":
		if k := strings.TrimSpace(os.Getenv("GEMINI_API_KEY")); k != "" {
			return k
		}
		if k := strings.TrimSpace(os.Getenv("GOOGLE_API_KEY")); k != "" {
			return k
		}
	}
	if k := strings.TrimSpace(os.Getenv("AI_API_KEY")); k != "" {
		return k
	}
	raw, err := os.ReadFile(credentialsFile)
	if err != nil {
		return ""
	}
	var file struct {
		AIAPIKey      string `json:"aiApiKey"`
		GroqAPIKey    string `json:"groqApiKey"`
		GeminiAPIKey  string `json:"geminiApiKey"`
		OpenRouterKey string `json:"openrouterApiKey"`
	}
	if json.Unmarshal(raw, &file) != nil {
		return ""
	}
	switch cfg.Provider {
	case "groq":
		if k := strings.TrimSpace(file.GroqAPIKey); k != "" {
			return k
		}
	case "gemini":
		if k := strings.TrimSpace(file.GeminiAPIKey); k != "" {
			return k
		}
	case "openrouter":
		if k := strings.TrimSpace(file.OpenRouterKey); k != "" {
			return k
		}
	}
	return strings.TrimSpace(file.AIAPIKey)
}

func openaiCompatibleURL(cfg AIConfig) string {
	if u := strings.TrimSpace(cfg.BaseURL); u != "" {
		return strings.TrimRight(u, "/") + "/chat/completions"
	}
	switch cfg.Provider {
	case "ollama":
		return "http://127.0.0.1:11434/v1/chat/completions"
	case "openrouter":
		return "https://openrouter.ai/api/v1/chat/completions"
	default: // groq
		return "https://api.groq.com/openai/v1/chat/completions"
	}
}

type chatMsg struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

func cleanDMText(s string) string {
	s = strings.ReplaceAll(s, "\r\n", "\n")
	var keep []string
	for _, line := range strings.Split(s, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || isDMChromeLine(line) {
			continue
		}
		keep = append(keep, line)
	}
	return strings.TrimSpace(strings.Join(keep, " "))
}

func isDMChromeLine(line string) bool {
	l := strings.ToLower(strings.TrimSpace(line))
	if l == "" {
		return true
	}
	if strings.Contains(l, "not following each other") || strings.Contains(l, "following each other") {
		return true
	}
	if strings.Contains(l, "she/her") || strings.Contains(l, "he/him") || strings.Contains(l, "they/them") {
		return true
	}
	if strings.HasPrefix(l, "today ") && strings.ContainsAny(l, "0123456789") {
		return true
	}
	if strings.HasPrefix(l, "|") && strings.HasSuffix(l, "|") {
		return true
	}
	return false
}

func foldDM(s string) string {
	s = strings.ToLower(s)
	s = strings.ReplaceAll(s, "’", "'")
	s = strings.ReplaceAll(s, "‘", "'")
	s = strings.ReplaceAll(s, "`", "'")
	return s
}

func greetingCore(s string) string {
	t := foldDM(cleanDMText(s))
	t = strings.ReplaceAll(t, "😭", " ")
	t = strings.ReplaceAll(t, "😂", " ")
	t = strings.ReplaceAll(t, "💀", " ")
	t = strings.ReplaceAll(t, "😅", " ")
	t = strings.ReplaceAll(t, "😊", " ")
	t = strings.ReplaceAll(t, "🥺", " ")
	for _, extra := range []string{"hehe", "omg", "lmao", "lol", "haha", "hahaha"} {
		t = strings.ReplaceAll(t, extra, " ")
	}
	t = strings.Map(func(r rune) rune {
		switch r {
		case '!', '?', '.', ',', '~', '*', '"', '\'':
			return ' '
		}
		return r
	}, t)
	return strings.Join(strings.Fields(t), " ")
}

func isGreetingOnly(s string) bool {
	t := greetingCore(s)
	if t == "" {
		return strings.TrimSpace(s) != "" && hasZoeyFlavor(s)
	}
	switch t {
	case "hi", "hii", "hiii", "hiiii", "hey", "heyy", "heyyy", "hello", "yo", "sup", "hiya", "hii hi", "hi hi":
		return true
	}
	return false
}

func weAlreadyGreeted(history []ConvMessage) bool {
	for _, m := range history {
		if !m.FromSelf {
			continue
		}
		if isGreetingOnly(m.Text) || strings.Contains(strings.ToLower(m.Text), "omg hii") {
			return true
		}
	}
	return false
}

func lastUserBundle(history []ConvMessage, n int) string {
	if n <= 0 {
		n = 3
	}
	var parts []string
	count := 0
	for i := len(history) - 1; i >= 0 && count < n; i-- {
		if history[i].FromSelf {
			continue
		}
		t := cleanDMText(history[i].Text)
		if t == "" {
			continue
		}
		parts = append([]string{t}, parts...)
		count++
	}
	return strings.TrimSpace(strings.Join(parts, " "))
}

func looksLikeGymQuestion(s string) bool {
	l := foldDM(s)
	for _, n := range []string{
		"workout", "work out", "gym", "lifting", "do you lift", "you lift",
		"exercise", "cardio", "you gym", "go to the gym",
	} {
		if strings.Contains(l, n) {
			return true
		}
	}
	return false
}

// looksLikeJobQuestion: what she does for work / living — not gym "work out".
func looksLikeJobQuestion(s string) bool {
	l := foldDM(s)
	if l == "" || looksLikeGymQuestion(l) {
		return false
	}
	for _, n := range []string{
		"what do you do for work", "what do u do for work", "what do you do for a living",
		"what do u do for a living", "whats your job", "what's your job", "whats ur job",
		"what is your job", "where do you work", "where do u work", "where u work",
		"your job", "ur job", "do you work", "u work?", "you work?", "are you working",
		"are u working", "for a living", "occupation", "are you a student", "are u a student",
		"in college", "in uni", "what do you study", "what do u study", "you studying",
		"u studying", "still in school",
	} {
		if strings.Contains(l, n) {
			return true
		}
	}
	if strings.Contains(l, "what do you do") || strings.Contains(l, "what do u do") {
		return true
	}
	return false
}

func jobReplyOK(text string) bool {
	l := foldDM(text)
	for _, bad := range []string{
		"finals", "midterm", "midterms", "studying", "study for", "homework",
		"thesis", "college", "university", "in uni", "at uni", "im a student",
		"i'm a student", "i am a student", "in school", "at school",
	} {
		if strings.Contains(l, bad) {
			return false
		}
	}
	return true
}

func jobFallbackReply() string {
	return "i work retail rn\ndo a lil of on the side too hehe"
}

// looksLikeLikeThanks: they thanked her for likes / said glad she enjoyed posts —
// not a real post discussion. Do not invent "which was ur favorite to make".
func looksLikeLikeThanks(s string) bool {
	l := foldDM(s)
	if l == "" {
		return false
	}
	thanks := strings.Contains(l, "glad you") || strings.Contains(l, "glad u ") ||
		strings.Contains(l, "thanks") || strings.Contains(l, "thank you") ||
		strings.Contains(l, "thx") || strings.Contains(l, "ty for") ||
		strings.Contains(l, "appreciate")
	aboutPosts := strings.Contains(l, "enjoyed my") || strings.Contains(l, "enjoyed the") ||
		strings.Contains(l, "liked my") || strings.Contains(l, "like my") ||
		strings.Contains(l, "for the like") || strings.Contains(l, "for liking") ||
		strings.Contains(l, "for the likes") || strings.Contains(l, "my posts") ||
		strings.Contains(l, "my post") || strings.Contains(l, "the likes")
	return thanks && aboutPosts
}

// soundsLikeLikeThanksInterview: reply pretends they already talked about making posts.
func soundsLikeLikeThanksInterview(s string) bool {
	l := foldDM(s)
	if strings.Contains(l, "favorite") || strings.Contains(l, "favourite") {
		if strings.Contains(l, "make") || strings.Contains(l, "post") || strings.Contains(l, "one") {
			return true
		}
	}
	if strings.Contains(l, "which one") || strings.Contains(l, "which post") {
		return true
	}
	if strings.Contains(l, "so into them") || strings.Contains(l, "obsessed with them") {
		return true
	}
	return false
}

func looksLikeKinkOrSexual(s string) bool {
	l := foldDM(s)
	for _, n := range []string{
		"kink", "kinks", "fetish", "fetishes", "nsfw", "naughty", "horny",
		"what are you into", "what u into", "what you're into", "what ur into",
		"gross shit", "are you more vanilla", "more vanilla", "exploit those",
		"bdsm", "dom ", "sub ", "daddy", "mommy", "cnc", "free use",
		"roleplay", "role play", "sext", "send nudes", "nude", "onlyfans",
		"porn", "gif", "gifs", "pic", "pics", "photo", "photos",
		"degrade", "choke", "collar", "leash", "spank", "brat",
	} {
		if strings.Contains(l, n) {
			return true
		}
	}
	return false
}

func soundsLikeKinkShutDown(s string) bool {
	l := foldDM(s)
	for _, n := range []string{
		"hard no", "no thank you", "no thanks", "not into that", "not my thing",
		"im not into", "i'm not into", "thats a no", "that's a no", "pass on that",
		"dont wanna talk about that", "don't wanna talk about that",
		"not comfortable", "too far", "im good thanks", "i'm good thanks",
	} {
		if strings.Contains(l, n) {
			return true
		}
	}
	return false
}

func gymReplyOK(text string) bool {
	l := foldDM(strings.Join(splitDMBubbles(text), " "))
	denied := strings.Contains(l, "never") || strings.Contains(l, "dont workout") || strings.Contains(l, "don't workout") ||
		strings.Contains(l, "dont work out") || strings.Contains(l, "don't work out") ||
		strings.Contains(l, "i dont gym") || strings.Contains(l, "i don't gym") ||
		strings.Contains(l, "too tired") || strings.Contains(l, "need a hug") ||
		strings.Contains(l, "unless walking")
	goes := strings.Contains(l, "gym") || strings.Contains(l, "i lift") ||
		((strings.Contains(l, "work out") || strings.Contains(l, "workout")) && strings.Contains(l, "yeah"))
	return goes && !denied
}

// factsOK checks replies against the latest user message. Older messages in a
// topic bundle must not force location/gym templates onto a new subject.
func factsOK(latest, text string) bool {
	if !locationReplyOK(latest, text) {
		return false
	}
	if looksLikeGymQuestion(latest) && !gymReplyOK(text) {
		return false
	}
	if looksLikeJobQuestion(latest) && !jobReplyOK(text) {
		return false
	}
	if looksLikeKinkOrSexual(latest) && soundsLikeKinkShutDown(text) {
		return false
	}
	if looksLikeLikeThanks(latest) && soundsLikeLikeThanksInterview(text) {
		return false
	}
	return true
}

const beaconsLink = "https://beacons.ai/lil.cosmic.bunny.o7"
const funnelAfterZoeyReplies = 6
const askNameAfterZoeyReplies = 3
const repliesAllowedAfterBeacons = 1 // one normal reply after the link, then forget them

// They asked for OF/link/insta — point at the pinned post (the link lives in
// its caption). Never paste the beacons URL into DMs.
var beaconsRedirectPresets = []string{
	"hehe the spicier stuff isnt really on tumblr tho {name}\nits all on my pinned post :3",
	"aww i keep the fun pics off here\ncheck my pinned post if u wanna see {name}",
	"okok its all on my pinned post {name} hehe",
	"hehe go look at my pinned post\nthats where all that is :P",
	"yeah that lives on my pinned post more than here {name}",
	"i dont really post the spicy stuff on tumblr tbh\ncheck my pinned post :3",
}

// Spoil / shopping energy — same pinned-post pointer.
var beaconsSpoilPresets = []string{
	"ooo hehe check my pinned post for that {name}",
	"aww thats sweet\neverything for that is on my pinned post :3",
	"hehe look at my pinned post {name} its all there",
	"okok my pinned post has what u need for that :P",
	"hmm tumblr is awkward for that\ncheck my pinned post {name}",
	"hehe my pinned post has all that stuff :3",
}

// Default spicy funnel: nudge them to the pinned post (beacons lives there), no raw link.
var softPinNudgePresets = []string{
	"btw do u like my pinned post or is it too nooby {name}",
	"wait did u see the thing i pinned lol is it too much",
	"hehe curious what u think of my pinned {name}",
	"okok btw is my pinned too nooby or do u like it :3",
	"also wait be honest is my pinned cute or cringe {name}",
	"hehe did u peep my pinned yet or is it too much",
}

func fillBeaconsName(tmpl, name string) string {
	name = strings.ToLower(strings.TrimSpace(name))
	if name != "" {
		return strings.ReplaceAll(tmpl, "{name}", name)
	}
	s := tmpl
	s = strings.ReplaceAll(s, " {name}?", "?")
	s = strings.ReplaceAll(s, " {name} ", " ")
	s = strings.ReplaceAll(s, "{name} ", "")
	s = strings.ReplaceAll(s, " {name}", "")
	s = strings.ReplaceAll(s, "{name}", "")
	// collapse leftover double spaces from name removal
	for strings.Contains(s, "  ") {
		s = strings.ReplaceAll(s, "  ", " ")
	}
	return s
}

func pickBeaconsPreset(history []ConvMessage) string {
	presets := beaconsRedirectPresets
	if looksLikeSpoilOffer(lastUserBundle(history, 3)) {
		presets = beaconsSpoilPresets
	}
	if len(presets) == 0 {
		return beaconsLink
	}
	return presets[rand.Intn(len(presets))]
}

func pickSoftPinNudge() string {
	if len(softPinNudgePresets) == 0 {
		return "btw do u like my pinned post or is it too nooby :3"
	}
	return softPinNudgePresets[rand.Intn(len(softPinNudgePresets))]
}

func beaconsRedirectMessage(name string, history []ConvMessage) string {
	return strings.TrimSpace(fillBeaconsName(pickBeaconsPreset(history), name))
}

// beaconsRedirectBubble is the preset redirect (multi-bubble). Kept for call sites/tests.
func beaconsRedirectBubble(name string) string {
	return beaconsRedirectMessage(name, nil)
}

func conversationLen(history []ConvMessage) int {
	n := 0
	for _, m := range history {
		if cleanDMText(m.Text) != "" {
			n++
		}
	}
	return n
}

// zoeyReplyTurns counts how many times Zoey has sent a reply (a contiguous
// block of her bubbles = one turn), not total messages or bubbles.
func zoeyReplyTurns(history []ConvMessage) int {
	turns := 0
	inSelf := false
	for _, m := range history {
		if cleanDMText(m.Text) == "" {
			continue
		}
		if m.FromSelf {
			if !inSelf {
				turns++
				inSelf = true
			}
		} else {
			inSelf = false
		}
	}
	return turns
}

func weAlreadySentBeacons(history []ConvMessage) bool {
	for _, m := range history {
		if !m.FromSelf {
			continue
		}
		if strings.Contains(foldDM(m.Text), "beacons.ai/lil.cosmic.bunny") {
			return true
		}
	}
	return false
}

func looksLikePinNudge(s string) bool {
	l := foldDM(s)
	for _, n := range []string{
		"my pinned", "pinned post", "too nooby", "thing i pinned",
		"what i pinned", "the pinned", "like my pinned", "of my pinned",
		"is my pinned", "peep my pinned", "see my pinned",
	} {
		if strings.Contains(l, n) {
			return true
		}
	}
	return false
}

func weAlreadySentPinNudge(history []ConvMessage) bool {
	for _, m := range history {
		if m.FromSelf && looksLikePinNudge(m.Text) {
			return true
		}
	}
	return false
}

func weAlreadyFunneled(history []ConvMessage) bool {
	return weAlreadySentBeacons(history) || weAlreadySentPinNudge(history)
}

func looksLikeLinkAsk(s string) bool {
	l := foldDM(s)
	// "your insta is cute" / "like your tumblr and insta" is a compliment, not a link ask.
	if looksLikeSocialCompliment(l) {
		return false
	}
	for _, n := range []string{
		"onlyfans", "only fans", "your of", "ur of", "got an of", "have an of",
		"whats your insta", "what's your insta", "whats ur insta", "what's ur insta",
		"whats your instagram", "what's your instagram", "whats ur instagram",
		"your insta?", "ur insta?", "your instagram?", "ur instagram?",
		"got an insta", "have an insta", "got instagram", "have instagram",
		"do you have an insta", "do u have an insta", "instagram link", "insta link",
		"your page?", "ur page?", "whats your page", "what's your page", "whats ur page",
		"your link", "ur link", "drop the link", "got a link", "send me a link", "send the link",
		"beacons", "more of you", "more of u",
		"subscribe", "fanvue", "fansly",
		"off of here", "off tumblr", "talk off", "somewhere else",
		"move off", "chat elsewhere", "talk elsewhere", "dm elsewhere",
	} {
		if strings.Contains(l, n) {
			return true
		}
	}
	return false
}

// looksLikeSocialCompliment: they praise her tumblr/insta — not asking for OF/beacons.
func looksLikeSocialCompliment(l string) bool {
	hasSocial := strings.Contains(l, "insta") || strings.Contains(l, "instagram") ||
		strings.Contains(l, "tumblr") || strings.Contains(l, "your blog") || strings.Contains(l, "ur blog")
	if !hasSocial {
		return false
	}
	for _, ask := range []string{
		"?", "whats your", "what's your", "whats ur", "what's ur", "what is your",
		"got a link", "your link", "ur link", "drop the", "send me", "send the",
		"onlyfans", "only fans", "your of", "ur of", "beacons", "fanvue", "fansly",
		"where can i", "where do i", "got an of", "have an of",
	} {
		if strings.Contains(l, ask) {
			return false
		}
	}
	for _, c := range []string{
		"like your", "love your", "liked your", "like ur", "love ur",
		"cute", "pretty", "beautiful", "gorgeous", "hot", "nice", "amazing", "fire",
		"is pretty", "is cute", "looks good", "looks great",
	} {
		if strings.Contains(l, c) {
			return true
		}
	}
	return false
}

// shouldInstantBeacons: they asked for a link / OF, or offered spoil — go now.
func shouldInstantBeacons(history []ConvMessage) bool {
	topic := lastUserBundle(history, 3)
	return looksLikeLinkAsk(topic) || looksLikeSpoilOffer(topic)
}

func shouldSendBeacons(history []ConvMessage, otherName string) bool {
	return shouldSendBeaconsFor("", history, otherName)
}

func shouldSendBeaconsFor(accountID string, history []ConvMessage, otherName string) bool {
	if weAlreadyFunneled(history) {
		return false
	}
	if shouldInstantBeacons(history) {
		return true
	}
	sets := effectiveZoeyReplySetsFor(accountID, history, otherName)
	if sets < funnelAfterZoeyReplies {
		return false
	}
	// Wholesome chats (games/work/jokes) never auto-funnel on a counter alone.
	if !conversationLooksSpicy(history) {
		return false
	}
	// Hard overdue: don't let endless "one more question" stall the funnel forever
	// (unknownpleasuresof hit set 18 asking to re-send gifs).
	const funnelForceAfter = 3
	if sets >= funnelAfterZoeyReplies+funnelForceAfter {
		return true
	}
	// Mid kink Q&A ("any others?", "what else?") — answer first, pitch later.
	if theyExpectContentReply(history) {
		return false
	}
	return true
}

// theyExpectContentReply: their last line wants a real answer, not a link dump.
func theyExpectContentReply(history []ConvMessage) bool {
	last := foldDM(lastUserText(history))
	if last == "" {
		return false
	}
	// Bot accusations get a short denial — don't stall the funnel on the "?".
	if looksLikeBotAccusation(last) {
		return false
	}
	if isMediaPlaceholder(last) || looksLikeLookAtMedia(last) {
		return false
	}
	if strings.Contains(last, "?") {
		return true
	}
	for _, n := range []string{
		"any others", "what else", "and you", "your turn", "tell me more",
		"go on", "continue", "keep going", "what about you", "wbu", "hbu",
		"good. any", "okay any", "ok any", "im waiting", "i'm waiting", "ok im waiting",
		"ok i'm waiting", "still waiting",
	} {
		if strings.Contains(last, n) {
			return true
		}
	}
	// Short follow-ups in a kink thread: "Good.", "And?", "More"
	if looksLikeKinkOrSexual(lastUserBundle(history, 5)) {
		if last == "good" || last == "and" || last == "more" || last == "go on" ||
			strings.HasPrefix(last, "good ") || strings.HasPrefix(last, "and ") {
			return true
		}
	}
	// Mid kink banter — they're sharing prefs ("i love ass more"), not asking for a page.
	if theyAreSharingKinkPrefs(last) {
		return true
	}
	return false
}

// theyAreSharingKinkPrefs: flirty preference statements that still need a go-along reply.
func theyAreSharingKinkPrefs(last string) bool {
	last = foldDM(last)
	if last == "" || looksLikeLinkAsk(last) || looksLikeSpoilOffer(last) {
		return false
	}
	prefCue := false
	for _, n := range []string{
		"i love", "i like", "i prefer", "prefer", "more than", "into ",
		"im into", "i'm into", "i want", "i wanna", "obsessed with",
	} {
		if strings.Contains(last, n) {
			prefCue = true
			break
		}
	}
	if !prefCue {
		return false
	}
	if looksLikeKinkOrSexual(last) || looksLikeFlirtyHeat(last) {
		return true
	}
	for _, n := range []string{
		"ass", "butt", "booty", "tits", "boobs", "breast", "feet", "thigh",
		"cock", "dick", "pussy", "sex", "nude", "naked", "dom", "sub", "kink",
	} {
		if strings.Contains(last, n) {
			return true
		}
	}
	return false
}

// conversationLooksSpicy: recent chat already has flirty / sexual heat.
func conversationLooksSpicy(history []ConvMessage) bool {
	checked := 0
	for i := len(history) - 1; i >= 0 && checked < 12; i-- {
		if history[i].FromSelf {
			continue
		}
		t := cleanDMText(history[i].Text)
		if t == "" {
			continue
		}
		checked++
		if looksLikeKinkOrSexual(t) || looksLikeFlirtyHeat(t) {
			return true
		}
	}
	return false
}

func isMediaPlaceholder(s string) bool {
	l := foldDM(s)
	return l == "(image)" || l == "(gif)" || l == "(video)" || l == "(non-text)"
}

func theyRecentlySentMedia(history []ConvMessage) bool {
	seen := 0
	for i := len(history) - 1; i >= 0 && seen < 10; i-- {
		if history[i].FromSelf {
			continue
		}
		seen++
		if isMediaPlaceholder(history[i].Text) {
			return true
		}
	}
	return false
}

func looksLikeLookAtMedia(s string) bool {
	l := foldDM(s)
	if l == "" {
		return false
	}
	for _, n := range []string{
		"look up", "look above", "scroll up", "see above", "check above",
		"up there", "right there", "i sent", "just sent", "already sent",
		"sent them", "sent it", "sent some", "there you go", "there u go",
	} {
		if strings.Contains(l, n) {
			return true
		}
	}
	return l == "look" || l == "see" || l == "there" || l == "here"
}

func weAlreadyAskedForSend(history []ConvMessage) bool {
	for i := len(history) - 1; i >= 0 && i >= len(history)-12; i-- {
		if !history[i].FromSelf {
			continue
		}
		l := foldDM(history[i].Text)
		if strings.Contains(l, "send them") || strings.Contains(l, "send it") ||
			strings.Contains(l, "send me") || strings.Contains(l, "yeah send") ||
			strings.Contains(l, "yes please send") || strings.Contains(l, "send the link") {
			return true
		}
	}
	return false
}

func asksThemToSendAgain(s string) bool {
	l := foldDM(s)
	return strings.Contains(l, "send them") || strings.Contains(l, "send it") ||
		strings.Contains(l, "send me") || strings.Contains(l, "send the link") ||
		strings.Contains(l, "yeah send") || strings.Contains(l, "yes please send")
}

// weRepliedSinceTheirMedia is true once we've answered after their latest pic,
// so their follow-up text gets a real reply instead of the same media reaction.
func weRepliedSinceTheirMedia(history []ConvMessage) bool {
	for i := len(history) - 1; i >= 0; i-- {
		if history[i].FromSelf {
			for j := i - 1; j >= 0; j-- {
				if !history[j].FromSelf && isMediaPlaceholder(history[j].Text) {
					return true
				}
			}
			return false
		}
	}
	return false
}

// dropRepeatedBubbles removes any bubble we already sent recently in this chat.
func dropRepeatedBubbles(text string, history []ConvMessage) string {
	recent := map[string]bool{}
	seen := 0
	for i := len(history) - 1; i >= 0 && seen < 12; i-- {
		if history[i].FromSelf {
			seen++
			for _, l := range strings.Split(history[i].Text, "\n") {
				if k := questionKey(l); k != "" {
					recent[k] = true
				}
			}
		}
	}
	var keep []string
	for _, l := range strings.Split(text, "\n") {
		if strings.TrimSpace(l) == "" || recent[questionKey(l)] {
			continue
		}
		keep = append(keep, l)
	}
	return strings.Join(keep, "\n")
}

// We only see "(image)" placeholders, so she says it won't load instead of
// pretending to react to something she can't see.
var mediaNotLoadingLines = []string{
	"wait why tf is that not loading for me",
	"ugh its not loading for me :((\nwhat is it",
	"hold on why wont that load for me",
	"wait tumblr wont load it for me\nwhat did u send hehe",
}

func mediaReactionReply() string {
	return mediaNotLoadingLines[0]
}

var mediaComplimentRe = regexp.MustCompile(`(?i)\b(page|blog|pretty|cute|hot|gorgeous|beautiful|sexy|stunning|love (ur|your))\b`)

// namedNotLoadingLine uses their name when we know it: "ughh dave theyre not fking loading 😭".
func namedNotLoadingLine(history []ConvMessage, name string) string {
	if name == "" || !wantNameThisReply(history, name) {
		return ""
	}
	pics := 0
	for i := len(history) - 1; i >= 0 && !history[i].FromSelf; i-- {
		if isMediaPlaceholder(history[i].Text) {
			pics++
		}
	}
	l := "ughh " + strings.ToLower(name) + " its not fking loading 😭"
	if pics > 1 {
		l = "ughh " + strings.ToLower(name) + " theyre not fking loading 😭"
	}
	if dropRepeatedBubbles(l, history) != l {
		return ""
	}
	return l
}

func mediaReactionReplyFor(history []ConvMessage, name string) string {
	line := namedNotLoadingLine(history, name)
	for _, l := range mediaNotLoadingLines {
		if line != "" {
			break
		}
		if dropRepeatedBubbles(l, history) == l {
			line = l
			break
		}
	}
	if line == "" {
		line = "hehe ur so bad :P\nwhat is it tho"
	}
	for i := len(history) - 1; i >= 0 && !history[i].FromSelf; i-- {
		t := history[i].Text
		if isMediaPlaceholder(t) {
			continue
		}
		if mediaComplimentRe.MatchString(t) {
			return "omg thank u :3\n" + line
		}
	}
	return line
}

func looksLikeFlirtyHeat(s string) bool {
	l := foldDM(s)
	for _, n := range []string{
		"horny", "sexy", " sex", "nude", "nudes", "pics of you", "pic of you",
		"your body", "ur body", "those legs", "your legs", "ur legs",
		"your ass", "ur ass", "boobs", "tits", "wanna fuck", "want you",
		"wanna see more", "see more of you", "show me more", "send me something",
		"so hot", "so sexy", "youre hot", "you're hot", "ur hot", "youre sexy",
		"you're sexy", "so beautiful", "sensual", "depraved", "on your knees",
		"thinking about u", "thinking about you", "make me blush", "turn me on",
		"into depraved", "eating pussy", "go down on", "biggest kink", "your kinks",
	} {
		if strings.Contains(l, n) {
			return true
		}
	}
	return false
}

// withSoftPinBridge keeps a short answer to them, then nudges the pinned post.
func withSoftPinBridge(text, name string) string {
	bridge := strings.TrimSpace(fillBeaconsName(pickSoftPinNudge(), name))
	text = strings.TrimSpace(text)
	if text == "" || looksLikePinNudge(text) || hasBeaconsLink(text) {
		return bridge
	}
	bubbles := splitDMBubbles(text)
	if len(bubbles) == 0 {
		return bridge
	}
	out := []string{bubbles[0]}
	for _, b := range splitDMBubbles(bridge) {
		b = strings.TrimSpace(b)
		if b != "" {
			out = append(out, b)
		}
	}
	if len(out) > 3 {
		out = out[:3]
	}
	return strings.Join(out, "\n")
}

// effectiveZoeyReplySets is the higher of history turns and the saved per-person
// counter (so long work-session chats still funnel even if inbox history is thin).
func effectiveZoeyReplySets(history []ConvMessage, otherName string) int {
	return effectiveZoeyReplySetsFor("", history, otherName)
}

func effectiveZoeyReplySetsFor(accountID string, history []ConvMessage, otherName string) int {
	turns := zoeyReplyTurns(history)
	saved := dmReplySetsForAccount(accountID, otherName)
	if turns > saved {
		return turns
	}
	return saved
}

// repliesAfterBeacons counts Zoey reply turns that came after the beacons link.
func repliesAfterBeacons(history []ConvMessage) int {
	idx := -1
	for i, m := range history {
		if !m.FromSelf {
			continue
		}
		if strings.Contains(foldDM(m.Text), "beacons.ai/lil.cosmic.bunny") {
			idx = i
		}
	}
	if idx < 0 {
		return 0
	}
	return zoeyReplyTurns(history[idx+1:])
}

// shouldStopReplying: after the beacons link, allow one more reply, then never again.
// Chatbots are skipped entirely.
func shouldStopReplying(history []ConvMessage, otherName string) bool {
	if looksLikeChatBot(otherName, history) {
		return true
	}
	if theyClaimUnderage(history) || isBlockedMinor(otherName) {
		return true
	}
	if !weAlreadySentBeacons(history) {
		return false
	}
	return repliesAfterBeacons(history) >= repliesAllowedAfterBeacons
}

func looksLikeChatBot(otherName string, history []ConvMessage) bool {
	n := foldDM(otherName)
	if n == "staff" || n == "tumblr" {
		return true
	}
	for _, bot := range []string{"kokobot", "koko bot", "chatbot", "helpbot", "supportbot", "tumblrbot"} {
		if strings.Contains(n, bot) {
			return true
		}
	}
	var recent []string
	for i := len(history) - 1; i >= 0 && len(recent) < 4; i-- {
		if !history[i].FromSelf {
			recent = append(recent, history[i].Text)
		}
	}
	if looksLikeSpamLink(strings.Join(recent, "\n")) {
		return true
	}
	hits := 0
	for i := len(history) - 1; i >= 0 && hits < 3; i-- {
		if history[i].FromSelf {
			continue
		}
		l := foldDM(history[i].Text)
		if strings.Contains(l, "type one of the letter options") ||
			strings.Contains(l, "oooops, i don't understand") ||
			strings.Contains(l, "oops, i don't understand") ||
			(strings.Contains(l, "make sure you only choose one") && strings.Contains(l, "get help")) {
			hits++
		}
	}
	return hits >= 2
}

var (
	reOutsideLink = regexp.MustCompile(`(?i)\b(?:https?://|www\.)?[a-z0-9-]+\.(?:com|net|org|xyz|site|online|live|club|top|me|io|ly|link|app|info|biz|cc|ru)/\S+`)
	reSpamPitch   = regexp.MustCompile(`(?i)\b(?:escorts?|horny\s*(?:moms|milfs|girls|singles|women)|hook\s*up|hookups|sex\s*dating|adult\s*dating|meet\s*(?:local|singles|girls)|click\s*(?:here|the\s*link)|free\s*(?:cam|sex|nudes)|cam\s*girls?|xxx|crypto|bitcoin|forex|investment\s*plan|whatsapp\s*me|telegram\s*me)\b`)
)

// looksLikeSpamLink: a promo bot dropping an outside link with a spam pitch.
func looksLikeSpamLink(s string) bool {
	return reOutsideLink.MatchString(s) && reSpamPitch.MatchString(s) && !strings.Contains(strings.ToLower(s), "tumblr.com")
}

func hasBeaconsLink(s string) bool {
	return strings.Contains(foldDM(s), "beacons.ai/lil.cosmic.bunny")
}

var nameStopWords = map[string]bool{
	"hey": true, "hi": true, "hello": true, "good": true, "morning": true, "night": true,
	"here": true, "just": true, "from": true, "into": true, "down": true, "over": true,
	"back": true, "love": true, "like": true, "what": true, "when": true, "where": true,
	"who": true, "why": true, "how": true, "the": true, "and": true, "but": true,
	"not": true, "you": true, "your": true, "mine": true, "yours": true, "baby": true,
	"babe": true, "daddy": true, "mommy": true, "sir": true, "miss": true, "bot": true,
	"human": true, "real": true, "true": true, "fine": true, "okay": true, "ok": true,
	"bed": true, "mood": true, "town": true, "city": true, "nyc": true, "uk": true,
	// "i'm waiting" / "i'm going" etc. are not names
	"waiting": true, "going": true, "coming": true, "looking": true, "trying": true,
	"doing": true, "getting": true, "feeling": true, "thinking": true, "working": true,
	"sleeping": true, "watching": true, "listening": true, "talking": true, "texting": true,
	"sending": true, "leaving": true, "staying": true, "horny": true, "busy": true,
	"free": true, "alone": true, "bored": true, "tired": true, "hungry": true, "ready": true,
	"so": true, "still": true, "also": true,
	// "i'm fairly relieved" / "i'm really into" — adverbs/hedges, not names
	"fairly": true, "really": true, "pretty": true, "very": true, "quite": true,
	"rather": true, "kinda": true, "kind": true, "sorta": true, "sort": true,
	"actually": true, "basically": true, "literally": true, "seriously": true,
	"totally": true, "super": true, "already": true, "almost": true, "only": true,
	"even": true, "maybe": true, "probably": true, "definitely": true, "currently": true,
	"usually": true, "always": true, "never": true, "too": true, "more": true,
	"less": true, "such": true, "glad": true, "happy": true, "sad": true,
	"relieved": true, "excited": true, "nervous": true, "curious": true,
}

// imNameIntroTrailingOK: "I'm Dave" / "I'm Dave btw" ok; "I'm fairly relieved…" not a name.
func imNameIntroTrailingOK(afterWord string) bool {
	rest := strings.TrimSpace(afterWord)
	rest = strings.TrimLeft(rest, ".,!?;:xX~-")
	rest = strings.TrimSpace(rest)
	if rest == "" {
		return true
	}
	word := rest
	if i := strings.IndexAny(rest, " \t\n.,!?;:/"); i >= 0 {
		word = rest[:i]
	}
	word = foldDM(strings.Trim(word, "\"'`"))
	switch word {
	case "btw", "here", "tho", "though", "haha", "hehe", "lol", "lmao",
		"and", "x", "xx", "xxx":
		return true
	default:
		return false
	}
}

func looksLikeFirstName(s string) bool {
	s = strings.TrimSpace(s)
	if len(s) < 2 || len(s) > 14 {
		return false
	}
	lower := strings.ToLower(s)
	if nameStopWords[lower] {
		return false
	}
	for _, r := range s {
		if (r < 'a' || r > 'z') && (r < 'A' || r > 'Z') && r != '\'' {
			return false
		}
	}
	return true
}

func normalizeFirstName(s string) string {
	s = strings.TrimSpace(s)
	s = strings.Trim(s, ".,!?\"'`")
	if i := strings.IndexAny(s, " \t\n/"); i >= 0 {
		s = s[:i]
	}
	if !looksLikeFirstName(s) {
		return ""
	}
	lower := strings.ToLower(s)
	return strings.ToUpper(lower[:1]) + lower[1:]
}

func nameFromBlogHandle(handle string) string {
	h := strings.TrimSpace(strings.TrimPrefix(handle, "@"))
	if h == "" {
		return ""
	}
	// plain first-name handles only (mike, jake) — skip bloggy names
	if looksLikeFirstName(h) {
		return normalizeFirstName(h)
	}
	return ""
}

// Words people put after "i'm" that are never names ("im hard", "im home").
var notNameWords = map[string]bool{
	"hard": true, "wet": true, "drunk": true, "high": true, "home": true, "new": true,
	"old": true, "single": true, "married": true, "taken": true, "straight": true, "gay": true,
	"bi": true, "sure": true, "sorry": true, "great": true, "well": true, "sick": true,
	"cold": true, "hot": true, "warm": true, "late": true, "early": true, "awake": true,
	"up": true, "out": true, "off": true, "on": true, "in": true, "at": true, "done": true,
	"shy": true, "lonely": true, "broke": true, "stuck": true, "lost": true, "confused": true,
	"jealous": true, "jk": true, "joking": true, "kidding": true, "serious": true, "honest": true,
	"hungover": true, "naked": true, "nude": true, "wild": true, "crazy": true, "weird": true,
	"lucky": true, "blessed": true, "good": true, "bad": true, "ill": true, "sleepy": true,
	"exhausted": true, "dead": true, "dying": true, "obsessed": true, "addicted": true,
	"a": true, "an": true, "the": true, "your": true, "ur": true, "yours": true, "urs": true,
	"all": true, "about": true, "big": true, "small": true, "short": true, "tall": true,
	"cute": true, "sexy": true, "ugly": true, "fat": true, "thin": true, "fit": true, "strong": true,
	"here": true, "there": true, "back": true, "now": true, "gonna": true, "gunna": true,
	"literally": true, "legit": true, "fr": true, "tbh": true, "ngl": true, "sub": true, "dom": true,
	"no": true, "yes": true, "yeah": true, "yep": true, "nope": true, "nah": true, "lol": true,
	"haha": true, "hehe": true, "lmao": true, "secret": true, "guess": true, "idk": true,
	"why": true, "nobody": true, "anonymous": true, "anon": true, "later": true, "whatever": true,
	"anything": true, "something": true, "nothing": true, "that": true, "this": true, "it": true,
	"me": true, "him": true, "her": true, "them": true, "male": true, "female": true, "man": true,
	"guy": true, "boy": true, "girl": true, "dude": true, "bro": true, "bud": true, "buddy": true,
	"honey": true, "sweetie": true, "sweetheart": true, "darling": true, "king": true, "master": true,
	"papi": true, "pet": true, "puppy": true, "slut": true, "whore": true, "princess": true,
	"amazing": true, "awesome": true, "available": true, "open": true, "cool": true, "alright": true,
	"lazy": true, "thirsty": true, "shook": true, "speechless": true, "game": true, "straightforward": true,
	"hbu": true, "wbu": true, "ty": true, "thanks": true, "thank": true, "thx": true, "nice": true,
	"same": true, "wow": true, "omg": true, "hmm": true, "mhm": true, "whats": true, "first": true,
	"clean": true, "safe": true, "different": true, "normal": true, "average": true,
}

func isNameWord(w string) bool {
	l := strings.ToLower(strings.Trim(w, "'"))
	if nameStopWords[l] || notNameWords[l] {
		return false
	}
	if strings.HasSuffix(l, "ing") || strings.HasSuffix(l, "ed") {
		return false
	}
	return looksLikeFirstName(l)
}

var (
	nameTrailingRe = regexp.MustCompile(`(?i)^[\s,.!~]*(btw|here|tho|though|lol|haha|hehe|lmao|x+|and (you|u)|wbu|hbu|what about (you|u)|what'?s (yours|urs)|(you|u)\s*\?|urs\s*\?|yours\s*\?|:\)|:3|:p|:d|;\)|\s)*[\s?!.]*$`)
	statedNameRe   = regexp.MustCompile(`(?i)\b(?:my name is|my name'?s|my names)\s+([a-z']+)`)
	callMeRe       = regexp.MustCompile(`(?i)\b(?:you can |u can )?call me\s+([a-z']+)(.*)$`)
	introNameRe    = regexp.MustCompile(`(?i)^(?:(?:hi+|hey+|hello+|yo|heya)[\s,!.]*)?(?:i'?m|i am|name'?s)\s+([a-z']+)(.*)$`)
	clauseBreakRe  = regexp.MustCompile(`^\s*[,.!]`)
	nameAnswerRe   = regexp.MustCompile(`(?i)^(?:(?:hi+|hey+|hello+|haha|lol|oh)[\s,!.]*)?(?:(?:my name is|my name'?s|my names|name'?s|it'?s|its|i'?m|i am|(?:you can |u can )?call me)\s+)?([a-z']+)(.*)$`)
)

func capitalizedName(w string) bool {
	r := []rune(w)
	if len(r) < 2 || r[0] < 'A' || r[0] > 'Z' {
		return false
	}
	return strings.ToUpper(w) != w
}

// nameMentionedInText only trusts clear self-introductions: "my name is dave",
// "call me dave", or a capitalized "Hi I'm Dave" standing on its own.
func nameMentionedInText(s string) string {
	t := asciiTextingPunct.Replace(strings.TrimSpace(cleanDMText(s)))
	if m := statedNameRe.FindStringSubmatch(t); m != nil && isNameWord(m[1]) {
		return normalizeFirstName(m[1])
	}
	if m := callMeRe.FindStringSubmatch(t); m != nil && isNameWord(m[1]) && nameTrailingRe.MatchString(m[2]) {
		return normalizeFirstName(m[1])
	}
	if m := introNameRe.FindStringSubmatch(t); m != nil && capitalizedName(m[1]) && isNameWord(m[1]) &&
		(nameTrailingRe.MatchString(m[2]) || clauseBreakRe.MatchString(m[2])) {
		return normalizeFirstName(m[1])
	}
	return ""
}

func askedTheirName(s string) bool {
	l := foldDM(s)
	return strings.Contains(l, "ur name") || strings.Contains(l, "your name") || strings.Contains(l, "what do i call you")
}

// nameAnswer reads their reply to our "whats ur name": "dave", "its dave :)",
// "Dave wbu". Lowercase is fine here because they are answering the question.
func nameAnswer(s string) string {
	t := asciiTextingPunct.Replace(strings.TrimSpace(cleanDMText(s)))
	m := nameAnswerRe.FindStringSubmatch(t)
	if m == nil || !isNameWord(m[1]) || !nameTrailingRe.MatchString(m[2]) {
		return ""
	}
	return normalizeFirstName(m[1])
}

func theirFirstName(history []ConvMessage, blogHandle string) string {
	_ = blogHandle // never trust tumblr handles like brianhose / kokobot as first names
	for i := len(history) - 1; i >= 0; i-- {
		if !history[i].FromSelf || !askedTheirName(history[i].Text) {
			continue
		}
		for j := i + 1; j < len(history) && !history[j].FromSelf; j++ {
			if n := nameAnswer(history[j].Text); n != "" {
				return n
			}
		}
		break
	}
	for i := len(history) - 1; i >= 0; i-- {
		if history[i].FromSelf {
			continue
		}
		if n := nameMentionedInText(history[i].Text); n != "" {
			return n
		}
	}
	return ""
}

var nameUseRes sync.Map

func nameUseRe(name string) *regexp.Regexp {
	key := strings.ToLower(name)
	if re, ok := nameUseRes.Load(key); ok {
		return re.(*regexp.Regexp)
	}
	re := regexp.MustCompile(`(?i)\b` + regexp.QuoteMeta(key) + `\b`)
	nameUseRes.Store(key, re)
	return re
}

func timesWeUsedTheirName(history []ConvMessage, name string) int {
	if name == "" {
		return 0
	}
	re := nameUseRe(name)
	n := 0
	for _, m := range history {
		if m.FromSelf && re.MatchString(m.Text) {
			n++
		}
	}
	return n
}

func usedNameInLastReply(history []ConvMessage, name string) bool {
	if name == "" {
		return false
	}
	re := nameUseRe(name)
	i := len(history) - 1
	for i >= 0 && !history[i].FromSelf {
		i--
	}
	for ; i >= 0 && history[i].FromSelf; i-- {
		if re.MatchString(history[i].Text) {
			return true
		}
	}
	return false
}

// wantNameThisReply: use a confirmed name about twice, never back to back.
func wantNameThisReply(history []ConvMessage, name string) bool {
	return name != "" && timesWeUsedTheirName(history, name) < 2 && !usedNameInLastReply(history, name)
}

func weAlreadyAskedName(history []ConvMessage) bool {
	for _, m := range history {
		if !m.FromSelf {
			continue
		}
		l := foldDM(m.Text)
		if strings.Contains(l, "ur name") || strings.Contains(l, "your name") ||
			strings.Contains(l, "whats your name") || strings.Contains(l, "what's your name") ||
			strings.Contains(l, "whats ur name") || strings.Contains(l, "what do i call you") {
			return true
		}
	}
	return false
}

func shouldAskTheirName(history []ConvMessage, blogHandle string) bool {
	if theirFirstName(history, blogHandle) != "" {
		return false
	}
	if weAlreadyAskedName(history) || weAlreadyFunneled(history) {
		return false
	}
	// Don't name-ask mid porn-gif dump / "look up" moments.
	if theyRecentlySentMedia(history) || looksLikeLookAtMedia(lastUserText(history)) {
		return false
	}
	n := zoeyReplyTurns(history)
	return n >= askNameAfterZoeyReplies && n < funnelAfterZoeyReplies
}

func stripBeaconsBubbles(text string) string {
	var keep []string
	for _, b := range splitDMBubbles(text) {
		if hasBeaconsLink(b) || strings.Contains(foldDM(b), "if u wanna see more of me") {
			continue
		}
		keep = append(keep, b)
	}
	return strings.Join(keep, "\n")
}

func withNameOnce(text, name string) string {
	name = strings.TrimSpace(name)
	if name == "" || strings.TrimSpace(text) == "" {
		return text
	}
	if strings.Contains(foldDM(text), foldDM(name)) {
		return text
	}
	bubbles := splitDMBubbles(text)
	if len(bubbles) == 0 {
		return text
	}
	i := 0
	for j, b := range bubbles {
		if !strings.Contains(b, "?") {
			i = j
			break
		}
	}
	bubbles[i] = nameAtBubbleEnd(bubbles[i], strings.ToLower(name))
	return strings.Join(bubbles, "\n")
}

var bubbleTailRe = regexp.MustCompile(`(\s*(:3|:P|:p|:\(+|:\)+|:/|;\)|xD|hehe+|lol)|[\s!?.~]+)*$`)

// nameAtBubbleEnd: "these 5am shifts are killing me dave :((" — the name goes
// before any trailing kaomoji / punctuation.
func nameAtBubbleEnd(b, name string) string {
	loc := bubbleTailRe.FindStringIndex(b)
	head, tail := b, ""
	if loc != nil {
		head, tail = b[:loc[0]], b[loc[0]:]
	}
	head = strings.TrimRight(head, " ,")
	if head == "" {
		return name + " " + strings.TrimSpace(b)
	}
	if tail != "" && !strings.HasPrefix(tail, " ") && !strings.ContainsAny(tail[:1], "!?.~") {
		tail = " " + tail
	}
	return head + " " + name + tail
}

var (
	genericStatusRe = regexp.MustCompile(`(?i)\b(chill(in|ing|in')?|just vibing|vibin|lounging|nothing much|not much|scrolling|same old)\b`)
	workTiredLines  = []string{
		"im so fking tired from work{name}",
		"these 5am shifts are killing me{name}",
		"work literally drained me today{name} :((",
		"so tired from work rn{name}",
	}
	workTiredMarkRe = regexp.MustCompile(`(?i)tired from work|shifts are killing|drained me today`)
	howAreYouRe     = regexp.MustCompile(`how (are|r) (you|u)|\bhru\b|\bwyd\b|what'?s up|\bsup\b|what (are|r) (you|u) (up to|doing)|what (you|u) (up to|doing)|how'?s (your|ur) day|how (is|was) (your|ur) day|how (you|u) doing|how have (you|u) been|how'?s it going|how'?s everything`)
	areYouRealRe    = regexp.MustCompile(`\b(are|r) (you|u) (even )?real\b|\bis this (even )?real\b|\b(you|u) real\b`)
	wbuRe           = regexp.MustCompile(`\b(wbu|hbu|wby|and (you|u)|what about (you|u)|(you|u)\s*\?*\s*$)`)
)

// dmStatusLines vary what Zoey says she's doing so every chat isn't "in bed on my phone".
var dmStatusLines = []string{
	"just got out the shower",
	"on my break at work",
	"about to make food",
	"watching a show",
	"doing my makeup",
	"eating snacks on the couch",
	"folding laundry",
	"lying around bored",
	"just woke up from a nap",
	"painting my nails",
}

// dmStatusFor keeps one status per chat so she stays consistent with that person.
func dmStatusFor(who string, history []ConvMessage) string {
	h := 0
	for _, r := range strings.ToLower(who) {
		h = h*31 + int(r)
	}
	if h < 0 {
		h = -h
	}
	return dmStatusLines[h%len(dmStatusLines)]
}

// swapGenericStatus replaces a filler "just chillin" bubble with a tired-from-work
// line, once per chat so it doesn't become her catchphrase.
func swapGenericStatus(history []ConvMessage, name, text string) string {
	if !howAreYouRe.MatchString(foldDM(unansweredUserTurn(history))) {
		return text
	}
	for _, m := range history {
		if m.FromSelf && workTiredMarkRe.MatchString(m.Text) {
			return text
		}
	}
	bubbles := strings.Split(text, "\n")
	for i, b := range bubbles {
		if strings.Contains(b, "?") || !genericStatusRe.MatchString(b) {
			continue
		}
		n := ""
		if name != "" {
			n = " " + strings.ToLower(name)
		}
		bubbles[i] = strings.ReplaceAll(workTiredLines[rand.Intn(len(workTiredLines))], "{name}", n)
		return strings.Join(bubbles, "\n")
	}
	return text
}

var questionLeadRe = regexp.MustCompile(`(?i)^(how|hows|how's|what|whats|what's|wbu|hbu|do|did|are|where|which|who|would|will|can)\b`)

func isQuestionBubble(b string) bool {
	return strings.Contains(b, "?") || questionLeadRe.MatchString(strings.TrimSpace(b))
}

func questionKey(b string) string {
	l := strings.ReplaceAll(foldDM(b), "'", "")
	l = strings.Map(func(r rune) rune {
		if r >= 'a' && r <= 'z' || r == ' ' {
			return r
		}
		return ' '
	}, l)
	var words []string
	for _, w := range strings.Fields(l) {
		switch w {
		case "p", "d", "hehe", "lol", "omg", "so", "far", "rn", "today":
			continue
		case "your":
			w = "ur"
		}
		words = append(words, w)
	}
	return strings.Join(words, " ")
}

// alsoOnRepeatQuestion: a second question in the same reply, or one we already
// asked earlier in the chat, reads as "alsooo hows ur day going".
func alsoOnRepeatQuestion(history []ConvMessage, text string) string {
	asked := map[string]bool{}
	for _, m := range history {
		if !m.FromSelf {
			continue
		}
		for _, b := range strings.Split(m.Text, "\n") {
			if isQuestionBubble(b) {
				if k := questionKey(b); len(k) >= 6 {
					asked[k] = true
				}
			}
		}
	}
	bubbles := strings.Split(text, "\n")
	sawQuestion := false
	for i, b := range bubbles {
		if !isQuestionBubble(b) {
			continue
		}
		repeat := asked[questionKey(b)]
		if i > 0 && (sawQuestion || repeat) && !strings.HasPrefix(foldDM(b), "also") {
			b = strings.TrimSpace(b)
			for _, lead := range []string{"and ", "so ", "but ", "omg ", "oh ", "wait "} {
				if strings.HasPrefix(foldDM(b), lead) {
					b = b[len(lead):]
				}
			}
			if questionLeadRe.MatchString(b) {
				bubbles[i] = "alsooo " + b
			}
		}
		sawQuestion = true
	}
	return strings.Join(bubbles, "\n")
}

func withNameAsk(text string) string {
	if strings.TrimSpace(text) == "" {
		return "whats ur name btw :3"
	}
	l := foldDM(text)
	if strings.Contains(l, "ur name") || strings.Contains(l, "your name") {
		return text
	}
	bubbles := splitDMBubbles(text)
	if len(bubbles) >= 3 {
		bubbles[2] = "whats ur name btw :3"
		return strings.Join(bubbles, "\n")
	}
	return strings.TrimSpace(text) + "\nwhats ur name btw :3"
}

func applyBeaconsFunnel(history []ConvMessage, otherName, text string) string {
	return applyBeaconsFunnelFor("", history, otherName, text)
}

var (
	earlyNameAskRe  = regexp.MustCompile(`(?i)\s*(what'?s|what is)\s+(ur|your)\s+name(\s+btw)?\s*\?*\s*(:3|:p|hehe)?`)
	unpromptedGymRe = regexp.MustCompile(`(?i)[,\s]*(and |then |maybe |prob |probably )?(hitting|hit|going to|heading to|off to) the gym( later| soon| after| rn)?`)
	easyOpenQs      = []string{"hows ur day going :3", "what u up to rn", "hows ur day been :3"}
)

// tidyReplyExtras drops a name ask before the chat has earned one and gym
// filler nobody asked about, keeping the rest of her reply.
func tidyReplyExtras(history []ConvMessage, otherName, text string) string {
	if strings.TrimSpace(text) == "" {
		return text
	}
	topic := foldDM(lastUserBundle(history, 3))
	gymOK := strings.Contains(topic, "gym") || looksLikeGymQuestion(topic)
	nameOK := shouldAskTheirName(history, otherName)
	chased := theyChasedForReply(history)
	shutDown := weShutThemDown(history)
	droppedHard := false
	var handleRe *regexp.Regexp
	if strings.ContainsAny(otherName, "0123456789-_") {
		handleRe = regexp.MustCompile(`(?i)[,\s]*` + regexp.QuoteMeta(otherName) + `\b`)
	}
	droppedName := false
	var keep []string
	for _, line := range strings.Split(text, "\n") {
		line = strings.TrimSpace(line)
		if !nameOK && earlyNameAskRe.MatchString(line) {
			line = strings.TrimSpace(earlyNameAskRe.ReplaceAllString(line, ""))
			droppedName = true
		}
		if !gymOK && strings.Contains(strings.ToLower(line), "gym") {
			line = strings.TrimSpace(unpromptedGymRe.ReplaceAllString(line, ""))
			if strings.Contains(strings.ToLower(line), "gym") {
				line = ""
			}
		}
		if !chased && sorryCheckingRe.MatchString(line) {
			continue
		}
		if (!shutDown && takeBackRe.MatchString(line)) || meetUpRe.MatchString(line) {
			droppedHard = true
			continue
		}
		if handleRe != nil {
			line = strings.TrimSpace(handleRe.ReplaceAllString(line, ""))
		}
		if cleanDMText(line) == "" || line == ":3" || line == ":P" {
			continue
		}
		keep = append(keep, line)
	}
	if len(keep) == 0 {
		if droppedHard {
			return ""
		}
		return text
	}
	if droppedName && zoeyReplyTurns(history) < 2 && !strings.Contains(strings.Join(keep, " "), "?") && len(keep) < 3 {
		keep = append(keep, easyOpenQs[rand.Intn(len(easyOpenQs))])
	}
	return strings.Join(keep, "\n")
}

var (
	takeBackRe      = regexp.MustCompile(`(?i)ignore (me|that|the emoji|my last)|i was being weird`)
	shutDownRe      = regexp.MustCompile(`(?i)keep it clean|please don'?t|not into that|hard no|no thank you|that'?s a no|keep (things|it) cute|not too intense|too much for me`)
	meetUpRe        = regexp.MustCompile(`(?i)meet ?up|meet (irl|in person|sometime|u|you)|hang ?out|grab (a )?(coffee|drink)|for (a )?coffee|pop over|come over (to|here|mine|sometime)`)
	sorryCheckingRe = regexp.MustCompile(`(?i)^(omg |aww )?sorry,? i'?m s?o+ bad at checking`)
	bareHiChaseRe   = regexp.MustCompile(`(?i)^(h+i+|hey+|hello+)\s*\.*\?+`)
)

// weShutThemDown: one of her earlier messages turned down flirty talk, so a
// playful take-back ("omg ignore me i was being weird") actually makes sense.
func weShutThemDown(history []ConvMessage) bool {
	shut := false
	for _, m := range history {
		if !m.FromSelf {
			continue
		}
		if takeBackRe.MatchString(m.Text) {
			return false
		}
		if shutDownRe.MatchString(m.Text) {
			shut = true
		}
	}
	return shut
}

// theyChasedForReply: they pinged again or complained about being ignored,
// so an apology for slow replies makes sense.
func theyChasedForReply(history []ConvMessage) bool {
	msgs := 0
	for i := len(history) - 1; i >= 0 && !history[i].FromSelf; i-- {
		msgs++
		l := foldDM(strings.TrimSpace(history[i].Text))
		if bareHiChaseRe.MatchString(l) {
			return true
		}
		for _, n := range []string{"are you there", "are u there", "you there", "u there", "remove you", "unfollow", "ignor", "respond", "reply to me"} {
			if strings.Contains(l, n) {
				return true
			}
		}
	}
	return msgs >= 5
}

// turnHasTextQuestion: their unanswered turn asks something in words, so a
// canned media reaction would ignore it.
func turnHasTextQuestion(history []ConvMessage) bool {
	for _, line := range strings.Split(unansweredUserTurn(history), "\n") {
		if isMediaPlaceholder(line) {
			continue
		}
		l := foldDM(line)
		if strings.Contains(l, "?") {
			return true
		}
		first := strings.Fields(l)
		if len(first) < 3 || strings.HasSuffix(l, "!") {
			continue
		}
		switch first[0] {
		case "why", "y", "what", "who", "how", "can", "do", "did", "are", "is", "where", "when", "would", "will":
			return true
		}
	}
	return false
}

func applyBeaconsFunnelFor(accountID string, history []ConvMessage, otherName, text string) string {
	text = tidyReplyExtras(history, otherName, strings.TrimSpace(text))
	name := theirFirstName(history, otherName)
	adult := confirmedAdult(otherName, history)
	if adult && shouldInstantBeacons(history) && !weAlreadySentBeacons(history) && !pinPointerInLastReply(history) {
		return beaconsRedirectMessage(name, history)
	}
	// Spicy long chat: keep her answer, soft-nudge the pinned post — never wipe their Q.
	if adult && shouldSendBeaconsFor(accountID, history, otherName) {
		return withSoftPinBridge(text, name)
	}
	if text == "" {
		return text
	}
	nameNow := wantNameThisReply(history, name) &&
		(!theyRecentlySentMedia(history) || strings.Contains(text, "not fking loading"))
	nameForSwap := ""
	if nameNow {
		nameForSwap = name
	}
	text = swapGenericStatus(history, nameForSwap, text)
	text = alsoOnRepeatQuestion(history, text)
	if shouldAskTheirName(history, otherName) {
		return withNameAsk(text)
	}
	if nameNow {
		return withNameOnce(text, name)
	}
	if name != "" && !nameNow {
		text = stripNameIfOverused(text, name)
	}
	return text
}

// stripNameIfOverused keeps the AI from saying their name every message.
func stripNameIfOverused(text, name string) string {
	re := regexp.MustCompile(`(?i)[,\s]*\b` + regexp.QuoteMeta(strings.ToLower(name)) + `\b`)
	var keep []string
	for _, b := range strings.Split(text, "\n") {
		b = strings.TrimSpace(re.ReplaceAllString(b, ""))
		if b != "" {
			keep = append(keep, b)
		}
	}
	if len(keep) == 0 {
		return text
	}
	return strings.Join(keep, "\n")
}

// replyFunnelSource tags whether the reply carried a hard link or pin nudge.
func replyFunnelSource(out, fallback string) string {
	if hasBeaconsLink(out) {
		return "beacons"
	}
	if looksLikePinNudge(out) {
		return "pin"
	}
	return fallback
}

func lastUserText(history []ConvMessage) string {
	for i := len(history) - 1; i >= 0; i-- {
		if history[i].FromSelf {
			continue
		}
		if t := cleanDMText(history[i].Text); t != "" {
			return t
		}
	}
	return ""
}

func lastSelfText(history []ConvMessage) string {
	for i := len(history) - 1; i >= 0; i-- {
		if !history[i].FromSelf {
			continue
		}
		if t := cleanDMText(history[i].Text); t != "" {
			return t
		}
	}
	return ""
}

func soundsLikeProtestingHuman(s string) bool {
	l := foldDM(s)
	for _, n := range []string{
		"texting human", "i'm human", "i am human", "totally human",
		"real person", "not a robot", "not a bot", "no robot", "robot vibe", "robot vibes",
		"i'm a real", "just a human", "literally human", "i'm actually human", "i'm real",
		"i am real", "im real", "yeah i'm real",
		"just a girl texting", "no bot", "i'm not a bot", "i am not a bot",
		"as an ai", "as a language model", "i'm an ai", "i am an ai", "i'm a bot", "i am a bot",
	} {
		if strings.Contains(l, n) {
			return true
		}
	}
	if strings.Contains(l, "robot") {
		return true
	}
	// "yeah i am" / "yes i am" only when affirming humanity — NOT "yeah i am obsessed with rdr2"
	return humanAffirmationOnly(l)
}

// humanAffirmationOnly is true for bare "yeah i am" / "yes i am" (optionally about
// being human/real), not "yeah i am playing" / "yes i am obsessed".
func humanAffirmationOnly(l string) bool {
	for _, p := range []string{"yeah i am", "yeah, i am", "yes i am", "yes, i am"} {
		idx := strings.Index(l, p)
		if idx < 0 {
			continue
		}
		rest := strings.TrimSpace(l[idx+len(p):])
		rest = strings.TrimLeft(rest, ".,!?;: ")
		if rest == "" {
			return true
		}
		for _, ok := range []string{
			"human", "real", "a person", "a real", "though", "tho", "lol", "haha",
			"but yeah", "whats up", "what's up", "what up",
		} {
			if strings.HasPrefix(rest, ok) {
				return true
			}
		}
	}
	if strings.Contains(l, "i am.") && (strings.Contains(l, "human") || strings.Contains(l, "real") || strings.Contains(l, "person")) {
		return true
	}
	return false
}

func looksLikeBotAccusation(s string) bool {
	l := foldDM(s)
	for _, n := range []string{
		"you are a bot", "you're a bot", "ure a bot", "u a bot", "are you a bot",
		"are u a bot", "is this a bot", "bot account", "so yes you are",
		"are you real", "are u real", "you real?", "u real?",
		"are you human", "are u human", "script", "chatgpt", "ai girl",
	} {
		if strings.Contains(l, n) {
			return true
		}
	}
	// bare "bot" only when clearly accusing, not random words
	if strings.Contains(l, "bot") && (strings.Contains(l, "you") || strings.Contains(l, "u ") || strings.Contains(l, "this")) {
		return true
	}
	return false
}

func looksLikeGameQuestion(s string) bool {
	l := foldDM(s)
	for _, n := range []string{
		"rdr2", "red dead", "skyrim", "stardew", "gta", "minecraft", "fortnite",
		"do you play", "do u play", "you play", "u play", "have you played", "have u played",
		"favorite game", "favourite game", "fav game", "what games",
	} {
		if strings.Contains(l, n) {
			return true
		}
	}
	return false
}

func isGenericFiller(s string) bool {
	l := foldDM(s)
	for _, n := range []string{
		"how's your day", "hows your day", "how is your day", "how's ur day",
		"what's up", "whats up", "how are you", "how r u",
	} {
		if strings.Contains(l, n) {
			return true
		}
	}
	return false
}

func isWeakReply(s string) bool {
	if isGreetingOnly(s) {
		return true
	}
	t := greetingCore(s)
	switch t {
	case "", "yeah", "yea", "ok", "okay", "k", "mhm", "same":
		return true
	}
	return isTooThinReply(s)
}

func looksLikeAgeQuestion(s string) bool {
	l := foldDM(s)
	if l == "" {
		return false
	}
	if strings.Contains(l, "how old") || strings.Contains(l, "ur age") || strings.Contains(l, "your age") {
		return true
	}
	if strings.Contains(l, "age") && (strings.Contains(l, "?") || strings.Contains(l, "you")) {
		return true
	}
	return false
}

func isDigitsOnly(s string) bool {
	s = strings.TrimSpace(s)
	if s == "" {
		return false
	}
	for _, r := range s {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}

func hasLifeDetail(s string) bool {
	l := foldDM(s)
	for _, n := range []string{
		"work", "dinner", "home", "just got", "making", "tea", "coffee",
		"shift", "hungry", "cooking", "got in", "got off", "leftover",
		"in bed", "feet hurt", "kettle", "takeaway",
	} {
		if strings.Contains(l, n) {
			return true
		}
	}
	return false
}

func looksLikeLocationQuestion(s string) bool {
	l := foldDM(s)
	for _, n := range []string{
		"where are you", "where r u", "where you from", "where u from", "where are u from",
		"where do you live", "whereabouts", "what city", "which city", "what country",
		"timezone", "time zone", "what time is it there", "what time for you",
		"are you uk", "you in the uk", "you from uk", "british isles", "across the pond",
		"where are u",
	} {
		if strings.Contains(l, n) {
			return true
		}
	}
	return false
}

func theyVolunteeredLocation(s string) bool {
	l := foldDM(s)
	for _, n := range []string{
		"i live in", "i'm from ", "im from ", "i am from ", "originally from ",
		"visiting from ", "i'm based in", "im based in", "i am based in",
	} {
		if strings.Contains(l, n) {
			return true
		}
	}
	for _, p := range []string{"i'm in ", "im in ", "i am in ", "here in ", "i'm over in ", "im over in "} {
		idx := strings.Index(l, p)
		if idx < 0 {
			continue
		}
		rest := strings.TrimSpace(l[idx+len(p):])
		if rest == "" {
			continue
		}
		for _, skip := range []string{"bed", "the mood", "my ", "a ", "love", "trouble", "shock"} {
			if strings.HasPrefix(rest, skip) {
				rest = ""
				break
			}
		}
		if rest == "" {
			continue
		}
		return true
	}
	return false
}

func soundsLikeGaveOwnLocation(s string) bool {
	l := foldDM(s)
	for _, n := range []string{
		"i'm in the uk", "im in the uk", "i'm uk", "im uk",
		"london", "england", "britain", "british", "across the pond",
		"timezone", "time zone", "pretty late here", "getting late here",
		"i'm in the uk so", "uk so it's", "uk so it is",
	} {
		if strings.Contains(l, n) {
			return true
		}
	}
	if strings.Contains(l, " uk") || strings.HasPrefix(l, "uk ") || strings.Contains(l, "uk,") || strings.Contains(l, "uk?") {
		return true
	}
	return false
}

func locationReplyOK(last, text string) bool {
	joined := strings.Join(splitDMBubbles(text), " ")
	if soundsLikeGaveOwnLocation(joined) {
		return false
	}
	if !theyVolunteeredLocation(last) {
		return true
	}
	l := foldDM(joined)
	return strings.Contains(l, "near there") || strings.Contains(l, "so close")
}

func looksLikeMoneyAsk(s string) bool {
	l := foldDM(s)
	for _, n := range []string{
		"donate", "donation", "paypal", "cashapp", "cash app", "venmo",
		"gofund", "go fund", "gofundme", "fundraiser", "crowdfund",
		"send me $", "send $", "need is $", "i need $",
		"all i need is $", "could you donate",
		"support me", "please support", "try to support", "support us",
		"help my family", "help our family", "financial support", "send money",
		"living in a tent", "we were displaced", "cost per person",
	} {
		if strings.Contains(l, n) {
			return true
		}
	}
	return false
}

// looksLikeSpoilOffer: they want to pay for her shopping/bills/etc — instant beacons funnel.
func looksLikeSpoilOffer(s string) bool {
	l := foldDM(s)
	for _, n := range []string{
		"shopping plans", "shopping plan", "bills to clear", "bills to pay",
		"any bills", "got any bills", "help with bills", "pay your bills", "pay ur bills",
		"pay your rent", "pay ur rent", "help with rent", "cover your", "cover ur",
		"spoil you", "spoil u", "spoil ya", "take care of you", "take care of u",
		"allowance", "pay for your", "pay for ur", "buy you something", "buy u something",
		"want me to buy", "can i buy you", "can i buy u", "clear your bills", "clear ur bills",
		"any shopping", "need anything paid", "help you financially", "financially",
		"sugar daddy", "i'll pay", "ill pay", "i can pay", "let me pay",
	} {
		if strings.Contains(l, n) {
			return true
		}
	}
	return false
}

func isTooThinReply(s string) bool {
	s = strings.Join(splitDMBubbles(s), " ")
	s = strings.TrimSpace(s)
	if s == "" {
		return true
	}
	core := greetingCore(s)
	if isDigitsOnly(core) {
		return true
	}
	switch core {
	case "haha thanks", "hahaha thanks", "thanks", "thank you", "aw thanks", "aww thanks",
		"lol thanks", "thanks haha", "haha thank you", "lol thank you", "that's sweet",
		"thats sweet", "thanks thats sweet", "thanks that's sweet":
		return true
	}
	if utf8.RuneCountInString(s) < 12 && !strings.Contains(s, "?") {
		return true
	}
	return false
}

func lastUserLooksSerious(history []ConvMessage) bool {
	l := strings.ToLower(lastUserText(history))
	if l == "" {
		return false
	}
	for _, needle := range []string{
		"bot", "cry", "crying", "said hi", "just said", "are you real", "again",
		"so yes", "figured", "script", " ai", "ai ",
	} {
		if strings.Contains(l, needle) {
			return true
		}
	}
	return false
}

func needsFollowUpReply(history []ConvMessage) bool {
	if !lastUserLooksSerious(history) {
		return false
	}
	self := lastSelfText(history)
	return isGreetingOnly(self) || isWeakReply(self)
}

func fallbackReply(last string) string {
	switch {
	case looksLikeAgeQuestion(last):
		return "25\nyou?"
	case theyVolunteeredLocation(last) && looksLikeLocationQuestion(last):
		return "omg im near there\ni dont really like saying exactly where tho"
	case theyVolunteeredLocation(last):
		return "omg im near there\nwait thats so close to me"
	case looksLikeLocationQuestion(last):
		return "dont really wanna say where i am\nwhat u doing rn"
	case looksLikeGymQuestion(last):
		return "yeah i gym\nnot every day tho. you?"
	case looksLikeJobQuestion(last):
		return jobFallbackReply()
	case looksLikeLookAtMedia(last) || isMediaPlaceholder(last):
		return mediaReactionReply()
	case looksLikeKinkOrSexual(last):
		return "lol wait tell me more :3\nwhat are u into"
	case looksLikeBotAccusation(last):
		return "lol no i just type like that\nhaha why"
	case looksLikeGameQuestion(last):
		return "omg yes i have\ni keep coming back to it :3"
	case looksLikeLikeThanks(last):
		return "aww ofc :3\nglad u liked that i stopped by"
	case strings.Contains(strings.ToLower(last), "cry"):
		return "thats just how i type\nwhats up"
	case strings.Contains(strings.ToLower(last), "said hi") || strings.Contains(strings.ToLower(last), "just said"):
		return "yeah i heard you\nhows ur night"
	case strings.Contains(strings.ToLower(last), "hot") || strings.Contains(strings.ToLower(last), "cute") || strings.Contains(strings.ToLower(last), "beautiful") || strings.Contains(strings.ToLower(last), "pretty") || strings.Contains(strings.ToLower(last), "gorgeous"):
		return "haha thanks\nyou having a good night?"
	case strings.Contains(strings.ToLower(last), "how are you") || strings.Contains(strings.ToLower(last), "how's your") || strings.Contains(strings.ToLower(last), "hows your") || strings.Contains(strings.ToLower(last), "what's up") || strings.Contains(strings.ToLower(last), "whats up"):
		return "pretty good\nyou?"
	default:
		return "wait yeah\nwhats been going on with you"
	}
}

func chatTranscript(history []ConvMessage) string {
	start := 0
	if len(history) > 16 {
		start = len(history) - 16
	}
	var b strings.Builder
	for _, m := range history[start:] {
		text := cleanDMText(m.Text)
		if text == "" {
			continue
		}
		if m.FromSelf {
			b.WriteString("you: ")
		} else {
			b.WriteString("them: ")
		}
		b.WriteString(text)
		b.WriteByte('\n')
	}
	return strings.TrimSpace(b.String())
}

// Keep the entire unanswered turn together: a trailing greeting or compliment
// must not hide the story or questions sent immediately before it.
func unansweredUserTurn(history []ConvMessage) string {
	start := 0
	for i := len(history) - 1; i >= 0; i-- {
		if history[i].FromSelf {
			start = i + 1
			break
		}
	}
	var parts []string
	for _, m := range history[start:] {
		if text := cleanDMText(m.Text); text != "" {
			parts = append(parts, text)
		}
	}
	return strings.Join(parts, "\n")
}

func conversationStarted(history []ConvMessage) bool {
	for _, m := range history {
		if m.FromSelf && strings.TrimSpace(m.Text) != "" {
			return true
		}
	}
	return false
}

var replyGreetingPrefix = regexp.MustCompile(`(?i)^(?:(?:omg|oh)\s+)?(?:hi+|hey+|hello+|hiya+|yo)\b[\s!,.~]*(?:(?::3|:p|:\))\s*)?`)

func removeRepeatedReplyGreeting(text string, history []ConvMessage) string {
	text = strings.TrimSpace(text)
	if !conversationStarted(history) {
		return text
	}
	for {
		next := strings.TrimSpace(replyGreetingPrefix.ReplaceAllString(text, ""))
		if next == text {
			return text
		}
		text = next
	}
}

func buildChatMessages(cfg AIConfig, otherName string, history []ConvMessage) []chatMsg {
	if cfg.NonExplicit {
		return buildCasualChatMessages(history)
	}
	who := otherName
	if who == "" {
		who = "them"
	}
	last := lastUserText(history)
	transcript := chatTranscript(history)
	var body strings.Builder
	if transcript == "" {
		body.WriteString("new chat with ")
		body.WriteString(who)
		body.WriteString(". they haven't said anything yet. say hi briefly, no crying emoji.")
	} else {
		body.WriteString("Tumblr DM thread with ")
		body.WriteString(who)
		body.WriteString(" (oldest to newest):\n")
		body.WriteString(transcript)
		body.WriteString("\n\n")
		if last != "" {
			topic := lastUserBundle(history, 3)
			body.WriteString("Their unanswered messages (one turn, oldest to newest):\n")
			turn := unansweredUserTurn(history)
			if turn == "" {
				turn = last
			}
			body.WriteString(turn)
			body.WriteString("\nWrite the next reply in 1-3 short bubbles, one per line. Address the substantive details and questions in this WHOLE turn, not only its final line. A trailing compliment or greeting does not replace the earlier story. Acknowledge a specific detail they wrote before any optional follow-up. Do not replace their topic with an unrelated age, introduction, or generic 'just chilling'. Media placeholders are not visible images; use only their written description")
			if conversationStarted(history) {
				body.WriteString(", must not be another hi/hey/hii")
			}
			if lastUserLooksSerious(history) {
				body.WriteString(", must not be only lol/lmao/😅")
			}
			if looksLikeAgeQuestion(topic) {
				body.WriteString(", she is 25 so say 25 not 20")
			} else {
				body.WriteString(", do not mention her age")
			}
			if howAreYouRe.MatchString(foldDM(turn)) || wbuRe.MatchString(foldDM(turn)) {
				body.WriteString(", if you say what you're doing right now say: " + dmStatusFor(who, history) +
					" (not in bed on my phone), then one easy question that follows from what THEY said. never call their day or answer boring")
			}
			for _, hint := range situationalHints(topic, history) {
				body.WriteString(", " + hint)
			}
			if name := theirFirstName(history, who); name != "" && wantNameThisReply(history, name) {
				body.WriteString(", their first name is " + name + " — use it once naturally in a bubble")
			} else if name != "" {
				body.WriteString(", their first name is " + name + " — do NOT say it this time")
			} else if shouldAskTheirName(history, who) {
				body.WriteString(`, you don't know their name yet — ask casually once ("whats ur name btw") after answering`)
			} else {
				body.WriteString(", you do not know their first name — do not invent one from their tumblr handle")
			}
			if age, ok := theirStatedAge(history); ok {
				body.WriteString(fmt.Sprintf(", they said they are %d", age))
			} else if _, ok := rememberedAdultAge(who); !ok {
				body.WriteString(", you do NOT know their age yet so they might be a minor — keep it friendly and wholesome, NOT sexual, NOT suggestive: no kink, bodies, feet, kneeling, teasing, 'good girl', bed or shower talk, no offering pics or proof, no pinned post. If they get sexual, laugh it off and change the subject")
			}
			body.WriteString(". You cannot send photos or videos in DMs: never say you are sending one, never say 'here it comes', never pretend one was sent or blame a glitch. If they ask for pics, say u cant send pics on here")
			body.WriteString(". Answer what they asked across the whole unanswered turn, in ur own words.")
			if !goodbyeRe.MatchString(foldDM(turn)) {
				body.WriteString(" The LAST bubble must be a short question or tease they can easily answer, never a flat statement about yourself.")
			}
		} else {
			body.WriteString("Write a soft check-in in character. Do not just say hi.")
		}
	}
	return []chatMsg{
		{Role: "system", Content: cfg.Persona},
		{Role: "user", Content: body.String()},
	}
}

func generateAIReply(cfg AIConfig, otherName string, history []ConvMessage) (string, error) {
	// One AI call at a time across parallel messaging accounts (Groq ITPM).
	aiReplyMu.Lock()
	defer aiReplyMu.Unlock()
	cfg.normalize()
	if !cfg.Enabled {
		return "", fmt.Errorf("ai disabled")
	}
	if cfg.NonExplicit {
		cfg.Temperature = 0.6
	}
	var text string
	var err error
	switch cfg.Provider {
	case "gemini":
		text, err = generateGeminiReply(cfg, otherName, history)
	case "groq", "ollama", "openrouter":
		text, err = generateOpenAICompatReply(cfg, otherName, history)
	default:
		return "", fmt.Errorf("unknown ai provider %q (use groq, ollama, openrouter, gemini)", cfg.Provider)
	}
	if err != nil {
		return "", err
	}
	text = removeRepeatedReplyGreeting(text, history)
	if cfg.NonExplicit && isGreetingOnly(text) {
		return "", fmt.Errorf("greeting-only draft rejected")
	}
	if text == "" {
		return "", fmt.Errorf("reply contained only a repeated greeting")
	}
	return text, nil
}

func shortErr(err error) string {
	s := err.Error()
	if len(s) > 120 {
		s = s[:120] + "…"
	}
	return s
}

func groqFallbackModels(model string) []string {
	// Each model has its own daily token budget; unavailable ones are skipped.
	// gpt-oss-20b is left out on purpose: its replies read broken, and skipping
	// a chat until a better model has budget beats sending those.
	order := []string{"qwen/qwen3.8-27b", "openai/gpt-oss-120b"}
	var out []string
	for _, m := range order {
		if m != model {
			out = append(out, m)
		}
	}
	return out
}

func groqReasoningPayload(payload map[string]any, model string) {
	switch {
	case strings.Contains(model, "qwen"):
		payload["reasoning_effort"] = "none"
		payload["reasoning_format"] = "hidden"
		payload["top_p"] = 0.8
	case strings.Contains(model, "gpt-oss"):
		payload["reasoning_effort"] = "low"
	}
}

func modelLooksUnavailable(status int, body string) bool {
	if status == http.StatusNotFound {
		return true
	}
	l := strings.ToLower(body)
	return strings.Contains(l, "model_not_found") || strings.Contains(l, "does not exist") || strings.Contains(l, "decommissioned") || strings.Contains(l, "model_decommissioned")
}

func isAIRateLimitError(err error) bool {
	if err == nil {
		return false
	}
	s := strings.ToLower(err.Error())
	return strings.Contains(s, "429") || strings.Contains(s, "rate limit") || strings.Contains(s, "too many requests")
}

func generateOpenAICompatReply(cfg AIConfig, otherName string, history []ConvMessage) (string, error) {
	key := resolveAIAPIKey(cfg)
	if cfg.Provider != "ollama" && key == "" {
		return "", fmt.Errorf("%s api key missing — set credentials aiApiKey or env", cfg.Provider)
	}

	if cfg.Provider == "groq" && os.Getenv("AI_FORCE_OPENROUTER") == "1" {
		return generateOpenRouterFallback(cfg, otherName, history)
	}
	models := []string{cfg.Model}
	if cfg.Provider == "groq" {
		models = append(models, groqFallbackModels(cfg.Model)...)
	}
	var lastErr error
	limited := false
	outForDay := 0
	for i, model := range models {
		if _, blocked := aiModelBlockedUntil(cfg.Provider, model); blocked {
			limited = true
			outForDay++
			continue
		}
		cfg.Model = model
		text, err := generateOpenAICompatReplyOnce(cfg, key, otherName, history)
		if err == nil {
			if i > 0 {
				fmt.Printf("   ai model fallback → %s\n", model)
			}
			return text, nil
		}
		lastErr = err
		if isAIRateLimitError(err) {
			if until, daily := dailyLimitReset(err.Error()); daily {
				blockAIModel(cfg.Provider, model, until)
				fmt.Printf("   %s daily limit reached — skipping it until %s\n", model, until.Format("15:04"))
				outForDay++
			}
			// Rate limits are per model, so try the next one before waiting.
			limited = true
			continue
		}
		if !modelLooksUnavailable(0, err.Error()) && !strings.Contains(err.Error(), "404") {
			return "", err
		}
	}
	if limited && cfg.Provider == "groq" {
		if text, err := generateOpenRouterFallback(cfg, otherName, history); err == nil {
			return text, nil
		} else if err != errNoOpenRouterKey {
			if !strings.Contains(err.Error(), "cooling down") {
				fmt.Printf("   openrouter fallback failed: %s\n", truncate(err.Error(), 160))
			}
			if outForDay == len(models) {
				return "", errAIExhausted
			}
		} else if outForDay == len(models) {
			return "", errAIExhausted
		}
	}
	if lastErr == nil {
		return "", errAIExhausted
	}
	if limited && !strings.Contains(strings.ToLower(lastErr.Error()), "per day") {
		fmt.Println("   ai rate limit on every model — waiting 20s then retrying once")
		time.Sleep(20 * time.Second)
		cfg.Model = models[0]
		if text, err := generateOpenAICompatReplyOnce(cfg, key, otherName, history); err == nil {
			return text, nil
		} else {
			lastErr = err
		}
	}
	return "", lastErr
}

// Free OpenRouter models, most reliable first; the qwen/gemma free pools are
// often rate-limited upstream.
var openRouterFreeModels = []string{
	"nvidia/nemotron-3-super-120b-a12b:free",
	"qwen/qwen3.8-27b:free",
	"google/gemma-4-31b-it:free",
}

var errNoOpenRouterKey = fmt.Errorf("openrouter api key missing")

// Free-tier limits are per key, not per model, so one hit pauses every model.
func openRouterCooldown(err error) (time.Duration, bool) {
	s := strings.ToLower(err.Error())
	switch {
	case strings.Contains(s, "free-models-per-day"):
		return time.Until(nextUTCMidnight()), true
	case strings.Contains(s, "free-models-per-min"):
		return time.Minute, true
	}
	return 0, false
}

func generateOpenRouterFallback(cfg AIConfig, otherName string, history []ConvMessage) (string, error) {
	cfg.Provider = "openrouter"
	cfg.BaseURL = ""
	key := resolveAIAPIKey(cfg)
	if key == "" || os.Getenv("AI_DISABLE_OPENROUTER") == "1" {
		return "", errNoOpenRouterKey
	}
	if until, blocked := aiModelBlockedUntil("openrouter", "*"); blocked {
		return "", fmt.Errorf("429 openrouter cooling down until %s", until.Format("15:04:05"))
	}
	var lastErr error
	for _, model := range openRouterFreeModels {
		cfg.Model = model
		text, err := generateOpenAICompatReplyOnce(cfg, key, otherName, history)
		if err == nil {
			fmt.Printf("   ai fallback → openrouter %s\n", model)
			return text, nil
		}
		lastErr = err
		if d, ok := openRouterCooldown(err); ok {
			blockAIModel("openrouter", "*", time.Now().Add(d))
			break
		}
	}
	return "", lastErr
}

var thinkBlockRe = regexp.MustCompile(`(?s)<think>.*?</think>`)

func generateOpenAICompatReplyOnce(cfg AIConfig, key, otherName string, history []ConvMessage) (string, error) {
	text, err := openAICompatChatOnce(cfg, key, buildChatMessages(cfg, otherName, history))
	if err != nil {
		return "", err
	}
	return sanitizeConfiguredReply(cfg, text), nil
}

func openAICompatChatOnce(cfg AIConfig, key string, msgs []chatMsg) (string, error) {
	payload := map[string]any{
		"model":       cfg.Model,
		"messages":    msgs,
		"temperature": cfg.Temperature,
		"max_tokens":  cfg.MaxTokens,
	}
	if cfg.Provider == "groq" {
		groqReasoningPayload(payload, cfg.Model)
	}
	if cfg.Provider == "openrouter" {
		payload["reasoning"] = map[string]any{"enabled": false, "exclude": true}
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return "", err
	}

	req, err := http.NewRequest("POST", openaiCompatibleURL(cfg), bytes.NewReader(body))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/json")
	if key != "" {
		req.Header.Set("Authorization", "Bearer "+key)
	}
	if cfg.Provider == "openrouter" {
		req.Header.Set("HTTP-Referer", "https://www.tumblr.com")
		req.Header.Set("X-Title", "tumblr-dm-bot")
	}

	client := &http.Client{Timeout: 45 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		limit := 200
		if resp.StatusCode == http.StatusTooManyRequests {
			limit = 600 // the "per day … try again in 1h2m" part sits past 200 chars
		}
		msg := fmt.Sprintf("%s: %s", resp.Status, truncate(string(raw), limit))
		if modelLooksUnavailable(resp.StatusCode, string(raw)) {
			return "", fmt.Errorf("404 %s", msg)
		}
		return "", fmt.Errorf("%s", msg)
	}

	var out struct {
		Choices []struct {
			Message struct {
				Content   string `json:"content"`
				Reasoning string `json:"reasoning"`
			} `json:"message"`
		} `json:"choices"`
		Usage struct {
			TotalTokens int `json:"total_tokens"`
		} `json:"usage"`
	}
	if err := json.Unmarshal(raw, &out); err != nil {
		return "", err
	}
	recordAIUsage(cfg.Provider, cfg.Model, out.Usage.TotalTokens)
	if len(out.Choices) == 0 {
		return "", fmt.Errorf("empty ai response")
	}
	text := strings.TrimSpace(thinkBlockRe.ReplaceAllString(out.Choices[0].Message.Content, ""))
	if text == "" {
		return "", fmt.Errorf("empty ai response")
	}
	return text, nil
}

func generateGeminiReply(cfg AIConfig, otherName string, history []ConvMessage) (string, error) {
	key := resolveAIAPIKey(cfg)
	if key == "" {
		return "", fmt.Errorf("gemini api key missing — set credentials geminiApiKey or GEMINI_API_KEY")
	}
	model := cfg.Model
	endpoint := fmt.Sprintf(
		"https://generativelanguage.googleapis.com/v1beta/models/%s:generateContent?key=%s",
		urlPathEscape(model), key,
	)

	chat := buildChatMessages(cfg, otherName, history)
	var systemText string
	var contents []map[string]any
	for _, m := range chat {
		if m.Role == "system" {
			systemText = m.Content
			continue
		}
		role := "user"
		if m.Role == "assistant" {
			role = "model"
		}
		contents = append(contents, map[string]any{
			"role": role,
			"parts": []map[string]string{
				{"text": m.Content},
			},
		})
	}
	payload := map[string]any{
		"contents": contents,
		"generationConfig": map[string]any{
			"temperature":     cfg.Temperature,
			"maxOutputTokens": cfg.MaxTokens,
		},
	}
	if systemText != "" {
		payload["systemInstruction"] = map[string]any{
			"parts": []map[string]string{{"text": systemText}},
		}
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return "", err
	}
	req, err := http.NewRequest("POST", endpoint, bytes.NewReader(body))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/json")
	client := &http.Client{Timeout: 45 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return "", fmt.Errorf("%s: %s", resp.Status, truncate(string(raw), 200))
	}
	var out struct {
		Candidates []struct {
			Content struct {
				Parts []struct {
					Text string `json:"text"`
				} `json:"parts"`
			} `json:"content"`
		} `json:"candidates"`
	}
	if err := json.Unmarshal(raw, &out); err != nil {
		return "", err
	}
	if len(out.Candidates) == 0 || len(out.Candidates[0].Content.Parts) == 0 {
		return "", fmt.Errorf("empty gemini response")
	}
	return sanitizeConfiguredReply(cfg, out.Candidates[0].Content.Parts[0].Text), nil
}

func urlPathEscape(s string) string {
	return strings.ReplaceAll(s, " ", "%20")
}

func sanitizeAIReply(s string) string {
	s = strings.TrimSpace(s)
	s = strings.Trim(s, `"'`)
	s = strings.ReplaceAll(s, "\r\n", "\n")
	bubbles := splitDMBubbles(s)
	for i, b := range bubbles {
		if utf8.RuneCountInString(b) > 160 {
			r := []rune(b)
			bubbles[i] = string(r[:157]) + "…"
		}
	}
	out := strings.Join(bubbles, "\n")
	// Never rewrite a normal answer into the bot-defense script. "yeah i am
	// obsessed with rdr2" used to match "yeah i am" and wipe the whole reply.
	if soundsLikeProtestingHuman(out) {
		return ""
	}
	return out
}

// Keeps the first n bubbles; if a dropped bubble held the only question, it
// replaces the last kept one so the reply still gives them something to answer.
func capAIBubbles(b []string, n int) []string {
	if len(b) <= n {
		return b
	}
	kept := append([]string(nil), b[:n]...)
	for _, k := range kept {
		if strings.Contains(k, "?") {
			return kept
		}
	}
	for _, rest := range b[n:] {
		if strings.Contains(rest, "?") {
			kept[n-1] = rest
			break
		}
	}
	return kept
}

func soundsLikeBotDefense(s string) bool {
	l := foldDM(s)
	return strings.Contains(l, "i just type like that") ||
		(strings.Contains(l, "what do u even post") && strings.Contains(l, "lol no"))
}

func stripBubblePrefix(s string) string {
	s = strings.TrimSpace(s)
	s = strings.Trim(s, `"'`)
	s = strings.TrimLeft(s, "-•* ")
	lower := foldDM(s)
	for _, prefix := range []string{"zoey:", "zoey —", "zoey -"} {
		if strings.HasPrefix(lower, prefix) {
			return strings.TrimSpace(s[len(prefix):])
		}
	}
	i := 0
	for i < len(s) && s[i] >= '0' && s[i] <= '9' {
		i++
	}
	if i > 0 && i < len(s) && (s[i] == '.' || s[i] == ')') {
		if rest := strings.TrimSpace(s[i+1:]); rest != "" {
			return rest
		}
	}
	return s
}

func splitDMBubbles(s string) []string {
	s = strings.TrimSpace(strings.ReplaceAll(s, "\r\n", "\n"))
	if s == "" {
		return nil
	}
	var out []string
	for _, p := range strings.Split(s, "\n") {
		p = stripBubblePrefix(strings.TrimSpace(p))
		p = calmDMReply(p)
		if p != "" {
			out = append(out, p)
		}
	}
	if len(out) == 1 {
		out = maybeSplitSingleBubble(out[0])
	}
	if len(out) > 3 {
		out = out[:3]
	}
	return out
}

func maybeSplitSingleBubble(s string) []string {
	s = strings.TrimSpace(s)
	if s == "" {
		return nil
	}
	for _, sep := range []string{"? ", "! ", ". "} {
		if i := strings.Index(s, sep); i >= 2 {
			left := strings.TrimSpace(s[:i+1])
			right := strings.TrimSpace(s[i+len(sep):])
			if left != "" && right != "" && utf8.RuneCountInString(right) >= 3 {
				rest := maybeSplitSingleBubble(right)
				out := append([]string{left}, rest...)
				if len(out) > 3 {
					out = out[:3]
				}
				return out
			}
		}
	}
	if i := strings.Index(s, ", "); i >= 2 {
		left := strings.TrimSpace(s[:i])
		right := strings.TrimSpace(s[i+2:])
		if utf8.RuneCountInString(left) >= 2 && utf8.RuneCountInString(right) >= 3 {
			return []string{left, right}
		}
	}
	return []string{s}
}

func hasZoeyFlavor(s string) bool {
	lower := strings.ToLower(s)
	return strings.Contains(s, "😭") || strings.Contains(lower, "hehe")
}

var asciiTextingPunct = strings.NewReplacer(
	"’", "'", "‘", "'", "“", `"`, "”", `"`,
	" — ", ", ", "—", ", ", " – ", ", ", "–", "-", "‑", "-",
)

// isEmojiRune covers pictographs, symbols and their joiners/variation selectors.
func isEmojiRune(r rune) bool {
	return (r >= 0x1F000 && r <= 0x1FAFF) || (r >= 0x2600 && r <= 0x27BF) ||
		r == 0x200D || r == 0xFE0F || (r >= 0x1F1E6 && r <= 0x1F1FF)
}

func calmDMReply(s string) string {
	s = strings.TrimSpace(s)
	if s == "" {
		return s
	}
	for _, extra := range []string{
		"😭", "😘", "✨", "🖤", "😜", "🥺", "💕", "💖", "💗", "🥰", "😍",
		"🔥", "💀", "😂", "😅", "😊", "😉", "💞", "💓", "🤍", "💛", "💙", "💜",
	} {
		s = strings.ReplaceAll(s, extra, "")
	}
	s = strings.Map(func(r rune) rune {
		if isEmojiRune(r) {
			return -1
		}
		return r
	}, s)
	s = asciiTextingPunct.Replace(s)
	return strings.TrimSpace(strings.Join(strings.Fields(s), " "))
}

func spiceDMReply(s string, history []ConvMessage) string {
	return strings.Join(fitBubbleBudget(splitDMBubbles(s), replyBubbleBudget(history)), "\n")
}

// How many bubbles their unanswered turn warrants: a bare "hey" usually gets
// one, a normal line two, a pile of messages or several questions three.
func replyBubbleBudget(history []ConvMessage) int {
	var msgs, words, questions int
	for i := len(history) - 1; i >= 0 && !history[i].FromSelf; i-- {
		t := strings.TrimSpace(history[i].Text)
		if t == "" || isMediaPlaceholder(t) {
			continue
		}
		msgs++
		words += len(strings.Fields(t))
		questions += strings.Count(t, "?")
	}
	switch {
	case msgs == 0:
		return 2
	case msgs >= 3 || questions >= 2 || words >= 35:
		return 3
	case msgs == 1 && words <= 4:
		if rand.Float64() < 0.6 {
			return 1
		}
		return 2
	case rand.Float64() < 0.2:
		return 1
	}
	return 2
}

func fitBubbleBudget(b []string, n int) []string {
	if len(b) <= n {
		return b
	}
	if n == 1 {
		if joined := strings.Join(b, " "); utf8.RuneCountInString(joined) <= 70 {
			return []string{joined}
		}
		n = 2
	}
	return capAIBubbles(b, n)
}

func pickReplyText(cfg MessageConfig, otherName string, history []ConvMessage) (string, string) {
	return pickReplyTextFor(cfg, "", otherName, history)
}

func pickReplyTextFor(cfg MessageConfig, accountID, otherName string, history []ConvMessage) (string, string) {
	out, src := pickReplyTextOnce(cfg, accountID, otherName, history)
	if !reviewedSource(src) || strings.TrimSpace(out) == "" {
		return out, src
	}
	ok, why := reviewDraft(cfg.AI, otherName, history, out)
	if ok {
		return out, src
	}
	fmt.Printf("   review rejected draft (%s) — rewriting once\n", why)
	retryCfg := cfg
	retryCfg.AI.Persona = withReviewerFeedback(cfg.AI.Persona, out, why)
	out2, src2 := pickReplyTextOnce(retryCfg, accountID, otherName, history)
	if !reviewedSource(src2) || strings.TrimSpace(out2) == "" {
		return out2, src2
	}
	if ok2, why2 := reviewDraft(cfg.AI, otherName, history, out2); !ok2 {
		fmt.Printf("   review rejected the rewrite too (%s) — not sending, will try again next pass\n", why2)
		return "", "held-review"
	}
	return out2, src2
}

func pickReplyTextOnce(cfg MessageConfig, accountID, otherName string, history []ConvMessage) (string, string) {
	text, src := pickReplyTextRaw(cfg, accountID, otherName, history)
	out := casualizeReply(withoutBeaconsLinks(text))
	out = spelledAgeRe.ReplaceAllString(out, "25")
	out = onlyAskNameOnce(accountID, otherName, history, out)
	if areYouRealRe.MatchString(foldDM(unansweredUserTurn(history))) {
		out = strings.ReplaceAll(out, "lol no i just type like that", "lol why wouldnt i be :P")
	}
	if trimmed := dropRepeatedBubbles(out, history); trimmed != out {
		if strings.TrimSpace(trimmed) == "" {
			fmt.Println("   draft only repeated what we already said — skipping this chat for now")
			return "", "repeat-skipped"
		}
		out = trimmed
	}
	if theyClaimUnderage(history) || isBlockedMinor(otherName) {
		return "", "minor-blocked"
	}
	if src == "ai-exhausted" || strings.TrimSpace(out) == "" {
		return out, src
	}
	return gateReplyOnAge(accountID, otherName, history, out), src
}

var nameAskedMu sync.Mutex

func nameAskedPath(accountID string) string {
	return filepath.Join("data", "profiles", accountID, "name-asked.json")
}

func loadNameAsked(accountID string) map[string]bool {
	m := map[string]bool{}
	if raw, err := os.ReadFile(nameAskedPath(accountID)); err == nil {
		_ = json.Unmarshal(raw, &m)
	}
	return m
}

// onlyAskNameOnce remembers which chats we already asked "whats ur name" in,
// since long chats scroll the original ask out of the history we fetch.
func onlyAskNameOnce(accountID, otherName string, history []ConvMessage, text string) string {
	if accountID == "" || otherName == "" || strings.TrimSpace(text) == "" {
		return text
	}
	key := strings.ToLower(otherName)
	nameAskedMu.Lock()
	defer nameAskedMu.Unlock()
	asked := loadNameAsked(accountID)
	save := func() {
		if messagesDryRun {
			return
		}
		if raw, err := json.Marshal(asked); err == nil {
			_ = os.MkdirAll(filepath.Dir(nameAskedPath(accountID)), 0755)
			_ = os.WriteFile(nameAskedPath(accountID), raw, 0644)
		}
	}
	if !asked[key] && weAlreadyAskedName(history) {
		asked[key] = true
		save()
	}
	if !earlyNameAskRe.MatchString(text) {
		return text
	}
	if !asked[key] {
		asked[key] = true
		save()
		return text
	}
	var keep []string
	for _, l := range strings.Split(text, "\n") {
		if earlyNameAskRe.MatchString(l) {
			l = strings.TrimSpace(earlyNameAskRe.ReplaceAllString(l, ""))
		}
		if l != "" {
			keep = append(keep, l)
		}
	}
	if len(keep) == 0 {
		return text
	}
	return strings.Join(keep, "\n")
}

// withoutBeaconsLinks: DMs only ever point at the pinned post, never the URL.
func withoutBeaconsLinks(text string) string {
	if !strings.Contains(strings.ToLower(text), "beacons") {
		return text
	}
	var keep []string
	for _, b := range strings.Split(text, "\n") {
		if !strings.Contains(strings.ToLower(b), "beacons") {
			keep = append(keep, b)
		}
	}
	if len(keep) == 0 {
		return fillBeaconsName(pickSoftPinNudge(), "")
	}
	return strings.Join(keep, "\n")
}

// pinPointerInLastReply: her previous reply already sent them to the pinned
// post, so a repeat ask gets a normal AI answer instead of the same preset.
func pinPointerInLastReply(history []ConvMessage) bool {
	i := len(history) - 1
	for i >= 0 && !history[i].FromSelf {
		i--
	}
	for ; i >= 0 && history[i].FromSelf; i-- {
		if looksLikePinNudge(history[i].Text) {
			return true
		}
	}
	return false
}

var (
	casualYoureRe  = regexp.MustCompile(`\byou'?re\b`)
	casualYourRe   = regexp.MustCompile(`\byour\b`)
	casualYouRe    = regexp.MustCompile(`\byou\b`)
	casualPeriodRe = regexp.MustCompile(`([^.\d])\.(\s|$)`)
	casualSpaceRe  = regexp.MustCompile(`\s{2,}`)
	casualSpaceQRe = regexp.MustCompile(`\s+(\?+)`)
	casualStrayRe  = regexp.MustCompile(`([a-z?!])[<>]+$`)
)

// casualizeReply makes bubbles read like fast phone texting: lowercase, u/ur,
// no apostrophes, commas or full stops, and only some question marks.
// Link bubbles are left alone so URLs survive.
func casualizeReply(text string) string {
	if strings.TrimSpace(text) == "" {
		return text
	}
	lines := strings.Split(text, "\n")
	for i, b := range lines {
		l := strings.ToLower(b)
		if strings.Contains(l, "http") || strings.Contains(l, "beacons") || strings.Contains(l, "www.") {
			continue
		}
		l = strings.ReplaceAll(l, "’", "'")
		l = casualYoureRe.ReplaceAllString(l, "ur")
		l = casualYourRe.ReplaceAllString(l, "ur")
		l = casualYouRe.ReplaceAllString(l, "u")
		l = strings.ReplaceAll(l, "'", "")
		l = strings.ReplaceAll(l, ",", " ")
		l = casualPeriodRe.ReplaceAllString(l, "$1$2")
		l = strings.TrimSpace(casualSpaceRe.ReplaceAllString(l, " "))
		l = casualSpaceQRe.ReplaceAllString(l, "$1")
		l = casualStrayRe.ReplaceAllString(l, "$1")
		if strings.HasSuffix(l, "?") && !strings.HasSuffix(l, "??") && rand.Float64() < 0.4 {
			l = strings.TrimSpace(strings.TrimSuffix(l, "?"))
		}
		l = strings.NewReplacer(":p", ":P", ":d", ":D", " xd", " xD").Replace(l)
		lines[i] = l
	}
	return strings.Join(lines, "\n")
}

const slowRepliesMessage = "my replies areee so slow on here sorry :(\nif u look at my pinned i acc reply more there and way more active"

var reWhySlow = regexp.MustCompile(`\b(?:why\s+(?:(?:do|does|dont|don't|do\s*not|didnt|didn't|did\s*not|are|r|is|arent|aren't|so|no|aint|ain't|havent|haven't|wont|won't)\s+)?(?:(?:you|u|she|ur|your)\s+)?(?:not\s+)?(?:reply|replying|respond|responding|answer|answering|text\s*back|slow|so\s*slow|ignor\w*|ghost\w*|take\s*so\s*long|taking\s*so\s*long)` +
	`|(?:slow|late|long)\s*(?:to\s*)?(?:repl\w*|respon\w*|answer\w*)|(?:repl\w*|respon\w*)\s*(?:are|r|is)?\s*(?:so\s*)?slow` +
	`|(?:you|u)\s*(?:never|dont|don't|do\s*not|barely|rarely|hardly)\s*(?:ever\s*)?(?:reply|respond|answer|text\s*back)` +
	`|(?:you|u)\s*(?:r|are)?\s*(?:ignoring|ghosting|ghosted|ignored)\s*me|took\s*(?:you|u)\s*(?:so\s*)?(?:long|forever|ages)` +
	`|(?:where|why)\s*(?:did|do)?\s*(?:you|u)\s*(?:go|disappear)|left\s*me\s*on\s*read|no\s*(?:reply|response|answer)\s*\?)`)

func askedWhySlowReplies(turn string) bool {
	return reWhySlow.MatchString(foldDM(turn))
}

func weAlreadyExplainedSlowReplies(history []ConvMessage) bool {
	for _, m := range history {
		if m.FromSelf && strings.Contains(foldDM(m.Text), "replies areee so slow") {
			return true
		}
	}
	return false
}

func pickReplyTextRaw(cfg MessageConfig, accountID, otherName string, history []ConvMessage) (string, string) {
	if cfg.AI.NonExplicit {
		text, err := generateAIReply(cfg.AI, otherName, history)
		if err != nil {
			mlogf("  draft skipped: %v", err)
			return "", "draft-failed"
		}
		return appendCasualPinMention(cfg, history, text), "non-explicit"
	}
	if askedWhySlowReplies(unansweredUserTurn(history)) && !weAlreadyExplainedSlowReplies(history) {
		return slowRepliesMessage, "slow-replies"
	}
	// Only skip AI when they directly asked for a link / offered spoil.
	if shouldInstantBeacons(history) && !weAlreadySentBeacons(history) && !pinPointerInLastReply(history) &&
		confirmedAdult(otherName, history) {
		return beaconsRedirectMessage(theirFirstName(history, otherName), history), "beacons"
	}
	last := lastUserText(history)
	topic := lastUserBundle(history, 3)
	latest := last
	if latest == "" {
		latest = topic
	}
	// They already dumped media (or said "look up") — never re-ask them to send.
	if !turnHasTextQuestion(history) && (looksLikeLookAtMedia(latest) || isMediaPlaceholder(latest) ||
		(theyRecentlySentMedia(history) && weAlreadyAskedForSend(history) && !looksLikeBotAccusation(latest) &&
			!weRepliedSinceTheirMedia(history))) {
		out := applyBeaconsFunnelFor(accountID, history, otherName, mediaReactionReplyFor(history, theirFirstName(history, otherName)))
		return out, replyFunnelSource(out, "template")
	}
	polish := func(text string) string {
		text = removeRepeatedReplyGreeting(text, history)
		if text == "" {
			return text
		}
		if (theyRecentlySentMedia(history) || looksLikeLookAtMedia(latest)) && asksThemToSendAgain(text) {
			return mediaReactionReply()
		}
		if strings.Contains(foldDM(text), "dont have eyes") || strings.Contains(foldDM(text), "don't have eyes") ||
			strings.Contains(foldDM(text), "i dont have eyes") || strings.Contains(foldDM(text), "no eyes") {
			if theyRecentlySentMedia(history) || looksLikeLookAtMedia(latest) {
				return mediaReactionReply()
			}
		}
		return text
	}
	if cfg.AI.Enabled {
		text, err := generateAIReply(cfg.AI, otherName, history)
		if err == nil && text != "" {
			src := cfg.AI.Provider
			if src == "" {
				src = "ai"
			}
			text = spiceDMReply(text, history)
			text = polish(text)
			if !factsOK(latest, text) {
				text = fallbackReply(latest)
			}
			// hardBad drafts are never sent; replyProblems are softer and only
			// decide whether a retry is worth one more AI call.
			hardBad := func(s string) bool {
				return strings.TrimSpace(s) == "" || isGreetingOnly(s) || isWeakReply(s) || soundsLikeProtestingHuman(s) ||
					soundsLikeKinkShutDown(s) || !factsOK(latest, s) ||
					(soundsLikeBotDefense(s) && !looksLikeBotAccusation(latest)) ||
					(lastUserLooksSerious(history) && isGenericFiller(s)) ||
					(asksThemToSendAgain(s) && theyRecentlySentMedia(history))
			}
			problems := replyProblems(history, text)
			if hardBad(text) || len(problems) > 0 {
				const rejected = 1 << 10
				best, bestN := text, len(problems)
				extra := ""
				if hardBad(text) {
					bestN = rejected
					extra = "answer their actual message with something specific — no bare greeting, filler, refusal or bot-defense line"
				}
				label := problemCodes(problems)
				if label == "" {
					label = "weak"
				}
				fmt.Printf("   draft failed checks (%s) — retrying once\n", label)
				retryCfg := cfg.AI
				retryCfg.Persona = critiquePersona(cfg.AI.Persona, text, problems, extra)
				if retry, err2 := generateAIReply(retryCfg, otherName, history); err2 == nil {
					retry = polish(spiceDMReply(retry, history))
					if !hardBad(retry) {
						if n := len(replyProblems(history, retry)); n <= bestN {
							best, bestN = retry, n
						}
					}
				}
				if bestN >= rejected {
					best = polish(fallbackReply(latest))
				}
				text = best
			}
			if weAlreadyGreeted(history) && isGreetingOnly(text) {
				out := applyBeaconsFunnelFor(accountID, history, otherName, polish(fallbackReply(latest)))
				return out, replyFunnelSource(out, src)
			}
			out := applyBeaconsFunnelFor(accountID, history, otherName, text)
			if (hasBeaconsLink(out) || looksLikePinNudge(out)) && !(hasBeaconsLink(text) || looksLikePinNudge(text)) {
				return out, replyFunnelSource(out, src)
			}
			return out, src
		}
		// Canned templates ("saw ur blog love the vibe") read as a bot mid-chat,
		// so leave the chat unanswered until the AI is back.
		if errors.Is(err, errAIExhausted) {
			return "", "ai-exhausted"
		}
		if err != nil {
			fmt.Println("   ai unavailable — skipping this chat for now:", shortErr(err))
		}
		return "", "ai-unavailable"
	}
	if weAlreadyGreeted(history) && latest != "" {
		out := applyBeaconsFunnelFor(accountID, history, otherName, polish(fallbackReply(latest)))
		return out, replyFunnelSource(out, "template")
	}
	if lastUserLooksSerious(history) && latest != "" {
		out := applyBeaconsFunnelFor(accountID, history, otherName, polish(fallbackReply(latest)))
		return out, replyFunnelSource(out, "template")
	}
	if looksLikeKinkOrSexual(latest) {
		out := applyBeaconsFunnelFor(accountID, history, otherName, polish(fallbackReply(latest)))
		return out, replyFunnelSource(out, "template")
	}
	if len(cfg.ReplyTemplates) == 0 {
		out := applyBeaconsFunnelFor(accountID, history, otherName, polish(spiceDMReply("hey! how's it going?", history)))
		return out, replyFunnelSource(out, "template")
	}
	out := applyBeaconsFunnelFor(accountID, history, otherName, polish(spiceDMReply(cfg.ReplyTemplates[rand.Intn(len(cfg.ReplyTemplates))], history)))
	return out, replyFunnelSource(out, "template")
}
