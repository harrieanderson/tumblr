package main

import (
	"fmt"
	"os"
	"strings"
	"testing"
	"time"
)

func TestFillAIPersonaReplacesBlogAndLegacyLilly(t *testing.T) {
	got := fillAIPersona("You are Zoey (Tumblr blog {blog})", "zoeyofc")
	if got != "You are Zoey (Tumblr blog zoeyofc)" {
		t.Fatalf("placeholder: %q", got)
	}

	got = fillAIPersona("You are Lilly (Tumblr blog lillycosmic), hi", "zoeynovaa")
	if got != "You are Zoey (Tumblr blog zoeynovaa), hi" {
		t.Fatalf("legacy lilly: %q", got)
	}

	got = fillAIPersona("", "zoeyofc")
	if !strings.Contains(got, "You are Zoey") || !strings.Contains(got, "zoeyofc") {
		t.Fatalf("default persona: %q", got)
	}
	if !strings.Contains(got, "25") || strings.Contains(got, "around 20") {
		t.Fatalf("age should be 25: %q", got)
	}
	if strings.Contains(got, "UK girl") || strings.Contains(strings.ToLower(got), "you're a uk") {
		t.Fatal("persona should not advertise she is UK")
	}
	assertCorePersona(t, got)
}

// The core persona only carries facts and voice; situational rules are added
// per reply by situationalHints and checked in TestSituationRulesFire.
func assertCorePersona(t *testing.T, p string) {
	t.Helper()
	l := strings.ToLower(p)
	for _, need := range []string{":3", "retail", "of on the side", "couple months", "human", "meet irl", "last bubble", "parrot", "output only the bubbles"} {
		if !strings.Contains(l, need) {
			t.Fatalf("persona missing %q: %q", need, p)
		}
	}
	if strings.Contains(p, "beacons.ai/lil.cosmic.bunny.o7") {
		t.Fatal("persona should not include the beacons url")
	}
	if len(p) > 3500 {
		t.Fatalf("core persona grew to %d chars — put situational rules in reply_rules.go", len(p))
	}
}

func TestSituationRulesFire(t *testing.T) {
	cases := map[string]string{
		"where are you from":                     "location",
		"im from texas":                          "near there",
		"do you go to the gym":                   "gym",
		"what do you do for work":                "retail",
		"thanks for the likes":                   "their blog is cute",
		"how long have you been on tumblr":       "couple months",
		"add my snap":                            "just uses tumblr",
		"My name is Mourad, 31. What about you?": "ur zoey",
		"are you a bot":                          "i just type like that",
		"gn babe":                                "goodbye",
	}
	for msg, want := range cases {
		hints := strings.Join(situationalHints(msg, []ConvMessage{{Text: msg}}), " | ")
		if !strings.Contains(hints, want) {
			t.Errorf("%q: want a rule mentioning %q, got %q", msg, want, hints)
		}
	}
	if h := situationalHints("hey how was ur day", []ConvMessage{{Text: "hey how was ur day"}}); len(h) != 0 {
		t.Errorf("plain small talk should add no rules, got %q", h)
	}
}
func TestSpiceDMReplyLeavesExistingFlavor(t *testing.T) {
	if got := spiceDMReply("hehehe omfg", nil); !strings.Contains(got, "hehehe") {
		t.Fatalf("hehehe should stay: %q", got)
	}
	if got := spiceDMReply("wait 😭😭", nil); got != "wait" {
		t.Fatalf("cry stripped: %q", got)
	}
	if spiceDMReply("", nil) != "" {
		t.Fatal("empty should stay empty")
	}
}

func TestCalmDMReplyDropsAnimatedBits(t *testing.T) {
	got := calmDMReply("thanks! 😘 still finding my vibe 🖤✨ hehehe")
	if strings.ContainsAny(got, "😘✨🖤") {
		t.Fatalf("still animated: %q", got)
	}
	if !strings.Contains(got, "hehehe") {
		t.Fatalf("hehehe should stay: %q", got)
	}
}

func TestCleanDMTextDropsTumblrChrome(t *testing.T) {
	raw := "| she/her | UK |\nNot following each other\nHi"
	if got := cleanDMText(raw); got != "Hi" {
		t.Fatalf("got %q", got)
	}
}

func TestIsGreetingOnly(t *testing.T) {
	for _, s := range []string{"hi", "omg hii hehe", "hi 😭😭", "hi hehe😭😭"} {
		if !isGreetingOnly(s) {
			t.Fatalf("should be greeting: %q", s)
		}
	}
	if isGreetingOnly("lmao no?? i just type fast") {
		t.Fatal("real reply should not count as greeting")
	}
}

func TestFallbackReplyAnswersLatest(t *testing.T) {
	if got := fallbackReply("Are you a bot"); !strings.Contains(strings.ToLower(got), "no") {
		t.Fatalf("bot: %q", got)
	}
	if got := fallbackReply("Why are you crying"); strings.Contains(got, "hi") {
		t.Fatalf("crying fallback said hi: %q", got)
	}
	if got := fallbackReply("Why are you crying"); strings.Contains(strings.ToLower(got), "uk") {
		t.Fatalf("crying fallback leaked location: %q", got)
	}
}

func TestWeAlreadyGreeted(t *testing.T) {
	h := []ConvMessage{
		{FromSelf: false, Text: "Hi"},
		{FromSelf: true, Text: "omg hii hehe 😭😭"},
		{FromSelf: false, Text: "Are you a bot"},
	}
	if !weAlreadyGreeted(h) {
		t.Fatal("expected greeted")
	}
	if lastUserText(h) != "Are you a bot" {
		t.Fatalf("latest: %q", lastUserText(h))
	}
	if !needsFollowUpReply(append(h, ConvMessage{FromSelf: true, Text: "hi hehe😭😭"})) {
		t.Fatal("greeting after a question should still get a real reply")
	}
}

func TestIsWeakReply(t *testing.T) {
	if !isWeakReply("lol 😅") {
		t.Fatal("lol 😅 should be a weak reply")
	}
	if isWeakReply("no?? i just type weird, what do u post") {
		t.Fatal("real reply should not be weak")
	}
}

func TestThinRepliesGetRejected(t *testing.T) {
	for _, s := range []string{"20", "haha thanks"} {
		if !isTooThinReply(s) {
			t.Fatalf("should be thin: %q", s)
		}
	}
	if isTooThinReply("just on my phone. you?") {
		t.Fatal("short real reply should be fine")
	}
	if isTooThinReply("ok that's actually stuck in my head now. the stare is too real") {
		t.Fatal("meme reaction should be fine")
	}
}

func TestAgeQuestionFallbackIs25(t *testing.T) {
	q := "first tip, age in bio. how old are you"
	if !looksLikeAgeQuestion(q) {
		t.Fatal("should detect age question")
	}
	got := fallbackReply(q)
	if !strings.Contains(got, "25") || strings.Contains(got, "20") {
		t.Fatalf("age fallback: %q", got)
	}
	msgs := buildChatMessages(defaultAIConfig(), "x-killer-instinct-x", []ConvMessage{
		{FromSelf: false, Text: q},
	})
	if !strings.Contains(msgs[1].Content, "she is 25") {
		t.Fatalf("prompt missing age: %q", msgs[1].Content)
	}
}

func TestLoadedMessagesConfigIs25(t *testing.T) {
	cfg := loadMessageConfig()
	p := strings.ToLower(cfg.AI.Persona)
	if !strings.Contains(p, "25") || strings.Contains(p, "around 20") {
		t.Fatalf("config/messages.json still has old age: %q", cfg.AI.Persona)
	}
	if strings.Contains(p, "uk girl") {
		t.Fatal("config persona should not advertise she is UK")
	}
	assertCorePersona(t, cfg.AI.Persona)
}
func TestLocationPolicy(t *testing.T) {
	if !looksLikeLocationQuestion("where are you from") || !looksLikeLocationQuestion("what timezone") {
		t.Fatal("should detect location questions")
	}
	if theyVolunteeredLocation("where are you from") {
		t.Fatal("a question is not them volunteering")
	}
	if !theyVolunteeredLocation("i'm in nyc") || !theyVolunteeredLocation("im from texas") {
		t.Fatal("should detect them saying where they are")
	}
	if theyVolunteeredLocation("i'm in bed") || theyVolunteeredLocation("im in love") {
		t.Fatal("mood/bed should not count as a place")
	}
	if got := fallbackReply("where are you from"); strings.Contains(strings.ToLower(got), "uk") || strings.Contains(strings.ToLower(got), "london") {
		t.Fatalf("asked location, should not name a place: %q", got)
	}
	if !strings.Contains(strings.ToLower(fallbackReply("where are you from")), "wanna say") {
		t.Fatalf("asked location, should deflect: %q", fallbackReply("where are you from"))
	}
	if got := fallbackReply("i'm in new york"); !strings.Contains(got, "near there") {
		t.Fatalf("they volunteered, should play close: %q", got)
	}
	if got := fallbackReply("i'm in nyc, where are you"); !strings.Contains(got, "near there") || strings.Contains(strings.ToLower(got), "london") {
		t.Fatalf("both: %q", got)
	}
	if !soundsLikeGaveOwnLocation("yeah uk, you called it") || !soundsLikeGaveOwnLocation("i'm in the uk so it's pretty late here") {
		t.Fatal("should catch her volunteering UK")
	}
	if soundsLikeGaveOwnLocation("omg im near there") || soundsLikeGaveOwnLocation("dont really wanna say where i am, what you up to instead") {
		t.Fatal("deflect / near-there should be allowed")
	}
	if locationReplyOK("i'm in nyc", "haha nice") {
		t.Fatal("must claim to be nearby when they said where they are")
	}
	if !locationReplyOK("i'm in nyc", "omg im near there") {
		t.Fatal("near there should pass")
	}
	if locationReplyOK("where are you", "yeah uk, you called it") {
		t.Fatal("must not confirm UK when asked")
	}
}

func TestGymIsPartOfZoey(t *testing.T) {
	if !looksLikeGymQuestion("how often do you workout") || !looksLikeGymQuestion("you gym?") {
		t.Fatal("should detect gym questions")
	}
	if looksLikeGymQuestion("how often do you work nights") {
		t.Fatal("work schedule is not gym")
	}
	got := fallbackReply("how often do you workout")
	if !strings.Contains(strings.ToLower(got), "gym") {
		t.Fatalf("gym fallback should mention gym: %q", got)
	}
	if strings.Contains(strings.ToLower(got), "never") || strings.Contains(strings.ToLower(got), "hug") {
		t.Fatalf("gym fallback denied gym: %q", got)
	}
	if gymReplyOK("i dont workout rn im too tired lol but i need a hug sooo bad") {
		t.Fatal("denied-gym reply should fail")
	}
	if !gymReplyOK("yeah i gym\nnot every day tho. you?") {
		t.Fatal("yeah i gym should pass")
	}
	h := []ConvMessage{
		{FromSelf: false, Text: "how often do you workout?"},
		{FromSelf: false, Text: "zoeyyyyy"},
	}
	if !looksLikeGymQuestion(lastUserBundle(h, 3)) {
		t.Fatal("zoeyyyyy after a gym ask should still count as gym")
	}
	msgs := buildChatMessages(defaultAIConfig(), "x-killer-instinct-x", h)
	if !strings.Contains(msgs[1].Content, "yeah i gym") {
		t.Fatalf("prompt missing gym hint: %q", msgs[1].Content)
	}
}

func TestJobIsRetailAndOF(t *testing.T) {
	if !looksLikeJobQuestion("what do you do for work") || !looksLikeJobQuestion("whats your job") {
		t.Fatal("should detect job questions")
	}
	if !looksLikeJobQuestion("what do u do") || !looksLikeJobQuestion("are you a student") {
		t.Fatal("should detect what-do-u-do / student asks")
	}
	if looksLikeJobQuestion("how often do you workout") {
		t.Fatal("gym must not count as job")
	}
	got := fallbackReply("what do you do for work?")
	l := strings.ToLower(got)
	if !strings.Contains(l, "retail") || !(strings.Contains(l, "of") || strings.Contains(l, "onlyfans")) {
		t.Fatalf("job fallback should say retail + OF: %q", got)
	}
	if jobReplyOK("ugh studying for finals rn") {
		t.Fatal("finals fiction must fail jobReplyOK")
	}
	if !jobReplyOK(jobFallbackReply()) {
		t.Fatal("retail+OF fallback should pass")
	}
	if factsOK("what do you do", "studying for my finals lol") {
		t.Fatal("factsOK must reject finals answer to job ask")
	}
	msgs := buildChatMessages(defaultAIConfig(), "zoeyofc", []ConvMessage{
		{FromSelf: false, Text: "what do you do for work?"},
	})
	if !strings.Contains(strings.ToLower(msgs[1].Content), "retail") {
		t.Fatalf("prompt missing job hint: %q", msgs[1].Content)
	}
}

func TestLooksLikeMoneyAsk(t *testing.T) {
	if !looksLikeMoneyAsk("all i need is $300 to finish dentistry school") {
		t.Fatal("should skip donation ask")
	}
	if !looksLikeMoneyAsk("Please try to support me. We were displaced and living in a tent.") {
		t.Fatal("aid/support beg must be treated as money ask")
	}
	if looksLikeMoneyAsk("what album you been playing") {
		t.Fatal("normal chat is not a money ask")
	}
	spoil := "Darling, you got any shopping plans or bills to clear?"
	if looksLikeMoneyAsk(spoil) {
		t.Fatal("spoil/shopping offer must not be treated as them asking us for money")
	}
	if !looksLikeSpoilOffer(spoil) {
		t.Fatal("shopping/bills offer should trigger spoil funnel")
	}
	if looksLikeSpoilOffer("all i need is $300") {
		t.Fatal("donation beg is not a spoil offer")
	}
	h := []ConvMessage{{FromSelf: false, Text: "im 34"}, {FromSelf: false, Text: spoil}}
	if !shouldSendBeacons(h, "paschal66") {
		t.Fatal("spoil offer should instantly send beacons")
	}
	got := applyBeaconsFunnel(h, "paschal66", "nah i dont really do that kind of thing")
	if hasBeaconsLink(got) || !looksLikePinNudge(got) {
		t.Fatalf("spoil funnel should point at the pinned post, no link: %q", got)
	}
	if strings.Contains(foldDM(got), "dont really do that") {
		t.Fatalf("must not shut down spoil offer: %q", got)
	}
	spoilSet := map[string]bool{}
	for _, p := range beaconsSpoilPresets {
		spoilSet[fillBeaconsName(p, "")] = true
	}
	for i := 0; i < 20; i++ {
		if msg := beaconsRedirectMessage("", h); !spoilSet[msg] {
			t.Fatalf("spoil redirect should use spoil presets: %q", msg)
		}
	}
}

func TestSplitDMBubblesDoubleTexts(t *testing.T) {
	got := splitDMBubbles("okay\nare u doing much rn?")
	if len(got) != 2 || got[0] != "okay" || got[1] != "are u doing much rn?" {
		t.Fatalf("newline split: %#v", got)
	}
	got = splitDMBubbles("hehehe omfg\nwhat were u thinking aboutttt")
	if len(got) != 2 {
		t.Fatalf("clingy double: %#v", got)
	}
	got = splitDMBubbles("just on my phone. you?")
	if len(got) != 2 || got[0] != "just on my phone." || got[1] != "you?" {
		t.Fatalf("sentence split: %#v", got)
	}
	got = splitDMBubbles("omg im near there, i dont really like saying exactly where tho")
	if len(got) != 2 || !strings.Contains(got[0], "near there") {
		t.Fatalf("comma split: %#v", got)
	}
	if n := len(splitDMBubbles("im here now\nand usually i just work very early, like 5am starts\nbut sometimes they get long\nextra")); n != 3 {
		t.Fatalf("cap at 3, got %d", n)
	}
}

func TestBeaconsFunnelAfterLongChat(t *testing.T) {
	clearDMReplySet("zz-funnel-short")
	clearDMReplySet("zz-funnel-long")
	t.Cleanup(func() {
		clearDMReplySet("zz-funnel-short")
		clearDMReplySet("zz-funnel-long")
	})
	short := []ConvMessage{
		{FromSelf: false, Text: "hi"},
		{FromSelf: true, Text: "heyy :3"},
		{FromSelf: false, Text: "whats up"},
	}
	if shouldSendBeacons(short, "zz-funnel-short") {
		t.Fatal("short chat should not funnel")
	}
	if applyBeaconsFunnel(short, "zz-funnel-short", "just on my phone rn") != "just on my phone rn" {
		t.Fatal("short chat should stay unchanged")
	}
	if !looksLikeLinkAsk("got an of?") || !shouldSendBeacons([]ConvMessage{{FromSelf: false, Text: "whats your onlyfans"}}, "") {
		t.Fatal("link ask should funnel even if short")
	}
	compliment := "I like your Tumblr and your insta is pretty cute too"
	if looksLikeLinkAsk(compliment) || shouldSendBeacons([]ConvMessage{{FromSelf: false, Text: compliment}}, "neardisorder140") {
		t.Fatal("complimenting her insta must not instantly funnel to beacons")
	}
	if !looksLikeLinkAsk("whats your insta?") || !looksLikeLinkAsk("do you have an instagram") {
		t.Fatal("real insta asks should still funnel")
	}
	if !looksLikeLinkAsk("Want to talk to you off of here") {
		t.Fatal(`"off of here" should soft-funnel to beacons`)
	}
	if len(beaconsRedirectPresets) != 6 {
		t.Fatalf("want 6 beacons presets, got %d", len(beaconsRedirectPresets))
	}
	if len(softPinNudgePresets) != 6 {
		t.Fatalf("want 6 pin nudge presets, got %d", len(softPinNudgePresets))
	}
	for i, p := range append(append([]string{}, beaconsRedirectPresets...), beaconsSpoilPresets...) {
		if hasBeaconsLink(p) || !looksLikePinNudge(p) {
			t.Fatalf("preset %d must point at the pinned post, not the link: %q", i, p)
		}
		if strings.Contains(foldDM(p), "cannot send") || strings.Contains(foldDM(p), "pinky promise") {
			t.Fatalf("preset %d still uses old botty pitch: %q", i, p)
		}
		filled := fillBeaconsName(p, "Mike")
		if strings.Contains(filled, "{name}") {
			t.Fatalf("preset %d left placeholder: %q", i, filled)
		}
		anon := fillBeaconsName(p, "")
		if strings.Contains(anon, "{name}") {
			t.Fatalf("preset %d left placeholder after anon fill: %q", i, anon)
		}
	}
	for i, p := range softPinNudgePresets {
		if hasBeaconsLink(p) {
			t.Fatalf("pin nudge %d must not include beacons link: %q", i, p)
		}
		if !looksLikePinNudge(p) {
			t.Fatalf("pin nudge %d not detected: %q", i, p)
		}
	}
	// Wholesome long chat must NOT auto-funnel.
	var wholesome []ConvMessage
	for i := 0; i < funnelAfterZoeyReplies+2; i++ {
		wholesome = append(wholesome,
			ConvMessage{FromSelf: false, Text: "hey this is a real line from them"},
			ConvMessage{FromSelf: true, Text: "yeah true hehe"},
			ConvMessage{FromSelf: true, Text: "what about u"},
		)
	}
	if shouldSendBeacons(wholesome, "zz-funnel-wholesome") {
		t.Fatal("wholesome long chat must not auto-funnel")
	}
	// Active kink Q&A must answer, not dump a link over "any others?"
	kinkQ := []ConvMessage{
		{FromSelf: false, Text: "May I ask your biggest kinks?"},
		{FromSelf: true, Text: "omg that is so hot hehe :P"},
		{FromSelf: true, Text: "i love being told what to do :3"},
		{FromSelf: false, Text: "Good. Any others?"},
	}
	for i := 0; i < funnelAfterZoeyReplies; i++ {
		kinkQ = append([]ConvMessage{
			{FromSelf: false, Text: "youre so hot"},
			{FromSelf: true, Text: "aww stop :3"},
			{FromSelf: true, Text: "hehe"},
		}, kinkQ...)
	}
	if shouldSendBeacons(kinkQ, "alex-kink") {
		t.Fatal(`"Good. Any others?" must not trigger a beacons dump`)
	}
	gotKink := applyBeaconsFunnel(kinkQ, "alex-kink", "mmm also being restrained is so hot :3\nwhat about u")
	if hasBeaconsLink(gotKink) || looksLikePinNudge(gotKink) {
		t.Fatalf("must answer kink follow-up, not pitch: %q", gotKink)
	}
	for i, p := range softPinNudgePresets {
		fold := foldDM(p)
		if hasBeaconsLink(p) {
			t.Fatalf("soft pin %d has link: %q", i, p)
		}
		for _, bad := range []string{"easier to show", "spicier stuff isnt", "beacons.ai"} {
			if strings.Contains(fold, bad) {
				t.Fatalf("soft pin %d too abrupt (%q): %q", i, bad, p)
			}
		}
	}
	// Mid kink preference ("I love ass more") must keep chatting — no abrupt beacons dump.
	nick := []ConvMessage{}
	for i := 0; i < funnelAfterZoeyReplies; i++ {
		nick = append(nick,
			ConvMessage{FromSelf: false, Text: "youre so hot"},
			ConvMessage{FromSelf: true, Text: "hehe stop :3"},
			ConvMessage{FromSelf: true, Text: "whats ur name"},
		)
	}
	nick = append(nick,
		ConvMessage{FromSelf: false, Text: "I'm nick"},
		ConvMessage{FromSelf: true, Text: "omg hi nick :3"},
		ConvMessage{FromSelf: true, Text: "sooo much better than that handle lol"},
		ConvMessage{FromSelf: false, Text: "I love ass more"},
	)
	if theyAreSharingKinkPrefs("I love ass more") != true {
		t.Fatal("I love ass more should count as kink pref sharing")
	}
	if shouldSendBeacons(nick, "nicklovespussy123-blog") {
		t.Fatal("kink pref mid-chat must not soft-funnel yet")
	}
	gotNick := applyBeaconsFunnel(nick, "nicklovespussy123-blog", "hehehe so do i nick :3\nwbu")
	if hasBeaconsLink(gotNick) || looksLikePinNudge(gotNick) {
		t.Fatalf("must answer nick's preference, not pitch: %q", gotNick)
	}
	// Spicy chat past threshold, no open question → soft pin nudge after answer.
	spicy := []ConvMessage{{FromSelf: false, Text: "im 31"}}
	for i := 0; i < funnelAfterZoeyReplies; i++ {
		spicy = append(spicy,
			ConvMessage{FromSelf: false, Text: "youre so sexy i want you"},
			ConvMessage{FromSelf: true, Text: "omg stop it :3"},
			ConvMessage{FromSelf: true, Text: "youre bad"},
		)
	}
	spicy = append(spicy, ConvMessage{FromSelf: false, Text: "mm yeah"})
	if !shouldSendBeacons(spicy, "zz-funnel-spicy") {
		t.Fatal("spicy long chat should soft-funnel")
	}
	got := applyBeaconsFunnel(spicy, "zz-funnel-spicy", "hehe yeah same rn\nwbu")
	if hasBeaconsLink(got) {
		t.Fatalf("spicy soft funnel must not dump beacons link: %q", got)
	}
	if !looksLikePinNudge(got) {
		t.Fatalf("missing pin nudge: %q", got)
	}
	if !strings.Contains(foldDM(got), "hehe yeah same") {
		t.Fatalf("must keep her answer then soft-nudge pin: %q", got)
	}
	if strings.Contains(foldDM(got), "cannot send") || strings.Contains(got, "😭😭😭") {
		t.Fatalf("must not use old cry pitch: %q", got)
	}
	waitingChat := append(spicy, ConvMessage{FromSelf: false, Text: "Ok I'm waiting"})
	if nameMentionedInText("Ok I'm waiting") != "" || theirFirstName(waitingChat, "brianhose") == "Waiting" {
		t.Fatal(`"I'm waiting" must not be treated as a first name`)
	}
	// "I'm waiting" is a content expectation — no pitch this turn.
	if shouldSendBeacons(waitingChat, "brianhose") {
		t.Fatal("waiting follow-up should be answered, not pitched over")
	}
	alreadyPin := append(spicy, ConvMessage{FromSelf: true, Text: "btw do u like my pinned post or is it too nooby :3"})
	if shouldSendBeacons(alreadyPin, "") {
		t.Fatal("should not send the pin nudge twice")
	}
	already := append(spicy, ConvMessage{FromSelf: true, Text: legacyBeaconsLinkMsg})
	if shouldSendBeacons(already, "") {
		t.Fatal("should not send the link twice")
	}
	if shouldStopReplying(already, "mike") {
		t.Fatal("right after beacons, still allow one follow-up reply")
	}
	followed := append(already,
		ConvMessage{FromSelf: false, Text: "ok whats that link"},
		ConvMessage{FromSelf: true, Text: "just easier to chat there :3"},
		ConvMessage{FromSelf: true, Text: "hit me when ur on"},
	)
	if !shouldStopReplying(followed, "mike") {
		t.Fatal("after beacons + one follow-up, forget them forever")
	}
	if repliesAfterBeacons(followed) < 1 {
		t.Fatalf("expected a post-beacons reply turn, got %d", repliesAfterBeacons(followed))
	}
}

func TestBeaconsFunnelsWhenAlreadyPastThreshold(t *testing.T) {
	// Spicy overdue chats soft-bridge; wholesome overdue chats do not.
	long := []ConvMessage{{FromSelf: false, Text: "im 31"}}
	for i := 0; i < funnelAfterZoeyReplies+3; i++ {
		long = append(long,
			ConvMessage{FromSelf: false, Text: "youre so hot i want you"},
			ConvMessage{FromSelf: true, Text: "omg stop :3"},
			ConvMessage{FromSelf: true, Text: "hehe"},
		)
	}
	long = append(long, ConvMessage{FromSelf: false, Text: "mm yeah"})
	if zoeyReplyTurns(long) < funnelAfterZoeyReplies {
		t.Fatalf("fixture turns %d", zoeyReplyTurns(long))
	}
	if !shouldSendBeacons(long, "jayagain21") {
		t.Fatalf("spicy overdue chat should soft-funnel")
	}
	got := applyBeaconsFunnel(long, "jayagain21", "haha true\nwbu")
	if hasBeaconsLink(got) {
		t.Fatalf("soft funnel must not dump beacons link: %q", got)
	}
	if !looksLikePinNudge(got) || !strings.Contains(foldDM(got), "haha true") {
		t.Fatalf("expected answer + pin nudge: %q", got)
	}
}

func TestChatBotIsSkipped(t *testing.T) {
	h := []ConvMessage{
		{FromSelf: false, Text: "Oooops, I don't understand. You'll need to type one of the letter options below."},
		{FromSelf: true, Text: "A"},
		{FromSelf: false, Text: "Oooops, I don't understand. You'll need to type one of the letter options below. Make sure you only choose one! Get help"},
	}
	if !looksLikeChatBot("kokobot", h) || !shouldStopReplying(h, "kokobot") {
		t.Fatal("kokobot menu spam must be skipped")
	}
}

func TestTheirNameAskAndExtract(t *testing.T) {
	clearDMReplySet("zz-name-ask")
	t.Cleanup(func() { clearDMReplySet("zz-name-ask") })
	if got := nameMentionedInText("hey my name is jake btw"); got != "Jake" {
		t.Fatalf("extract my name is: %q", got)
	}
	if got := nameMentionedInText("call me tyler"); got != "Tyler" {
		t.Fatalf("extract call me: %q", got)
	}
	if nameMentionedInText("i'm in bed") != "" {
		t.Fatal("i'm in bed is not a name")
	}
	if nameMentionedInText("I'm fairly relieved what vibe are you after?") != "" {
		t.Fatal(`"I'm fairly relieved…" must not become name Fairly`)
	}
	if nameMentionedInText("im really into that") != "" {
		t.Fatal("im really into is not a name")
	}
	if got := nameMentionedInText("Im Dave"); got != "Dave" {
		t.Fatalf("Im Dave should extract: %q", got)
	}
	if got := nameMentionedInText("Hey I'm Dave btw"); got != "Dave" {
		t.Fatalf("Hey I'm Dave btw should extract: %q", got)
	}
	if nameMentionedInText("im dave") != "" {
		t.Fatal("lowercase im X out of the blue is too risky to trust")
	}
	if theirFirstName(nil, "brianhose") != "" || theirFirstName(nil, "kokobot") != "" {
		t.Fatal("blog handles must not be used as first names")
	}
	if nameFromBlogHandle("mike") != "Mike" {
		t.Fatal("plain first-name helper still works for tests")
	}

	var mid []ConvMessage
	for i := 0; i < askNameAfterZoeyReplies; i++ {
		mid = append(mid,
			ConvMessage{FromSelf: false, Text: "hey whats up"},
			ConvMessage{FromSelf: true, Text: "not much"},
			ConvMessage{FromSelf: true, Text: "u?"},
		)
	}
	if !shouldAskTheirName(mid, "zz-name-ask") {
		t.Fatal("should ask for name mid-chat when unknown")
	}
	got := applyBeaconsFunnel(mid, "zz-name-ask", "pretty chill rn\nwbu")
	if !strings.Contains(got, "whats ur name btw") {
		t.Fatalf("should append name ask: %q", got)
	}
	named := append(mid, ConvMessage{FromSelf: true, Text: "whats ur name btw :3"}, ConvMessage{FromSelf: false, Text: "im dave"})
	if theirFirstName(named, "zz-name-ask") != "Dave" {
		t.Fatalf("should learn dave: %q", theirFirstName(named, "zz-name-ask"))
	}
	if shouldAskTheirName(named, "zz-name-ask") {
		t.Fatal("should not ask once name is known")
	}
}

func TestSoundsLikeProtestingHuman(t *testing.T) {
	for _, s := range []string{
		"i’m literally just a weird texting human 😜 what's up?",
		"yeah i’m totally human, just a bit extra",
		"no i am a real person",
	} {
		if !soundsLikeProtestingHuman(s) {
			t.Fatalf("should catch protest: %q", s)
		}
	}
	if !soundsLikeProtestingHuman("lmao wait no i just type messy 😅 but yeah, i am. what’s up?") {
		t.Fatal("should catch yeah i am / what’s up")
	}
	if isGenericFiller("but yeah, i am. what’s up?") != true {
		t.Fatal("unicode what's up should count as filler")
	}
	if soundsLikeProtestingHuman("lol no i just type like that, what do u even post") {
		t.Fatal("normal girl reply should not count as protest")
	}
	if soundsLikeProtestingHuman("yeah i am obsessed with rdr2") || soundsLikeProtestingHuman("omg yes i am playing it again") {
		t.Fatal("game answers with yeah/yes i am must not count as human protest")
	}
	if got := sanitizeAIReply("yeah i am obsessed with rdr2\ni keep replaying it :3"); got == "" || soundsLikeBotDefense(got) {
		t.Fatalf("sanitize must not wipe a game answer into bot-defense: %q", got)
	}
	if got := sanitizeAIReply("yeah i’m totally human lol"); got != "" {
		t.Fatalf("sanitize should drop real human-protest replies, got %q", got)
	}
	if !looksLikeGameQuestion("Do you play rdr2?") {
		t.Fatal("should detect rdr2 question")
	}
	if got := fallbackReply("Do you play rdr2?"); soundsLikeBotDefense(got) || !looksLikeGameQuestion("Do you play rdr2?") {
		t.Fatalf("rdr2 fallback must not be bot-defense: %q", got)
	}
}

func TestSkipAfterBeaconsRedirect(t *testing.T) {
	src, err := os.ReadFile("messages.go")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(src), "forgetting them") && !strings.Contains(string(src), "post-beacons follow-up") {
		t.Fatal("inbox must allow one reply after beacons then forget them")
	}
	h := []ConvMessage{
		{FromSelf: false, Text: "hey"},
		{FromSelf: true, Text: legacyBeaconsLinkMsg},
		{FromSelf: false, Text: "ok whats the link for"},
	}
	if !weAlreadySentBeacons(h) || shouldSendBeacons(h, "") {
		t.Fatal("after redirect, never funnel again")
	}
	if shouldStopReplying(h, "x") {
		t.Fatal("should still answer once after the link")
	}
	h = append(h, ConvMessage{FromSelf: true, Text: "yeah just easier there :3"})
	if !shouldStopReplying(h, "x") {
		t.Fatal("after that one follow-up, forget forever")
	}
}

func TestTheyClaimUnderageStopsReplies(t *testing.T) {
	cases := []string{
		"I am 16 years old too",
		"im 16",
		"i'm 17 yo",
		"I'm a teenager",
		"under 18",
	}
	for _, s := range cases {
		if !messageClaimsUnder18(s) {
			t.Fatalf("should flag underage: %q", s)
		}
		h := []ConvMessage{
			{FromSelf: false, Text: "hey"},
			{FromSelf: true, Text: "hii"},
			{FromSelf: false, Text: s},
		}
		if !theyClaimUnderage(h) || !shouldStopReplying(h, "x") {
			t.Fatalf("must stop for %q", s)
		}
	}
	if messageClaimsUnder18("i'm 25") || messageClaimsUnder18("im 19 years old") {
		t.Fatal("adult ages must not trip")
	}
}

func TestChatTranscriptAndBotFallback(t *testing.T) {
	h := []ConvMessage{
		{FromSelf: false, Text: "| she/her | UK |\nNot following each other\nHi"},
		{FromSelf: true, Text: "omg hii hehe 😭😭"},
		{FromSelf: false, Text: "Why are you crying"},
		{FromSelf: true, Text: "hi 😭😭"},
		{FromSelf: false, Text: "You just said hi"},
		{FromSelf: true, Text: "hi hehe😭😭"},
		{FromSelf: false, Text: "Are you a bot"},
		{FromSelf: true, Text: "lol 😅"},
		{FromSelf: false, Text: "So yes you are"},
	}
	tr := chatTranscript(h)
	if !strings.Contains(tr, "them: Hi") || strings.Contains(tr, "she/her") {
		t.Fatalf("transcript chrome: %q", tr)
	}
	if !strings.Contains(tr, "them: So yes you are") || !strings.Contains(tr, "you: lol 😅") {
		t.Fatalf("transcript missing turns: %q", tr)
	}
	if lastUserText(h) != "So yes you are" {
		t.Fatalf("latest %q", lastUserText(h))
	}
	got := fallbackReply("So yes you are")
	if strings.Contains(strings.ToLower(got), "hi") || isWeakReply(got) || soundsLikeProtestingHuman(got) {
		t.Fatalf("fallback still botty: %q", got)
	}
	if got := sanitizeAIReply("yeah i’m totally human lol"); soundsLikeProtestingHuman(got) || strings.Contains(strings.ToLower(got), "human") {
		t.Fatalf("sanitize left human-talk: %q", got)
	}
	if !lastUserLooksSerious(h) {
		t.Fatal("so yes you are should look serious")
	}
	msgs := buildChatMessages(defaultAIConfig(), "imabitofa", h)
	if len(msgs) != 2 || !strings.Contains(msgs[1].Content, "Their unanswered messages (one turn, oldest to newest):\nSo yes you are") {
		t.Fatalf("prompt: %#v", msgs)
	}
}

func TestKinkTalkGoesAlong(t *testing.T) {
	kink := "May i tell u that this is a kink or a fetish i would love to explore with u?"
	if !looksLikeKinkOrSexual(kink) {
		t.Fatal("should detect kink ask")
	}
	if !looksLikeKinkOrSexual("what gross shit are you into or are you more vanilla") {
		t.Fatal("should detect kink list ask")
	}
	if !soundsLikeKinkShutDown("omg no thank you :/\nthat is a hard no from me") {
		t.Fatal("hard no must count as shut-down")
	}
	if factsOK(kink, "omg no thank you\nthat is a hard no from me") {
		t.Fatal("hard no must fail factsOK on kink")
	}
	got := fallbackReply(kink)
	if soundsLikeKinkShutDown(got) || isGreetingOnly(got) {
		t.Fatalf("kink fallback must lean in: %q", got)
	}
	msgs := buildChatMessages(defaultAIConfig(), "pinkeract", []ConvMessage{
		{FromSelf: false, Text: kink},
	})
	if !strings.Contains(msgs[1].Content, "kink") || !strings.Contains(msgs[1].Content, "go along") {
		t.Fatalf("prompt missing kink go-along: %q", msgs[1].Content)
	}
}

func TestFactsOKUsesLatestNotOldLocation(t *testing.T) {
	// Older NY mention must not force "near there" when they ask something else.
	ritual := `So I found your one post endearing…. Care to tell me what a few of "the million lonely ritualistic things you need to do" :)`
	good := "omg mostly skincare and making tea\nwhat about u"
	if !factsOK(ritual, good) {
		t.Fatal("answering the ritual ask should be fine even if they mentioned NY earlier")
	}
	if factsOK("i'm in new york", "haha nice") {
		t.Fatal("when they volunteer a place, reply still needs near-there")
	}
}

func TestLikeThanksReplyNotInterview(t *testing.T) {
	them := "Glad you enjoyed my posts"
	if !looksLikeLikeThanks(them) {
		t.Fatal("expected like-thanks detect")
	}
	bad := "omg yes i was so into them :3\nwhat was ur favorite one to make"
	if !soundsLikeLikeThanksInterview(bad) {
		t.Fatal("expected interview detect")
	}
	if factsOK(them, bad) {
		t.Fatal("factsOK must reject interview reply")
	}
	fb := fallbackReply(them)
	if soundsLikeLikeThanksInterview(fb) {
		t.Fatalf("fallback still interviews: %q", fb)
	}
}

func TestMediaPlaceholdersAndLookUpGuards(t *testing.T) {
	if !isMediaPlaceholder("(gif)") || !isMediaPlaceholder("(image)") || !isMediaPlaceholder("(non-text)") {
		t.Fatal("expected media placeholders")
	}
	if !looksLikeLookAtMedia("look up") || !looksLikeLookAtMedia("I already sent them") {
		t.Fatal("expected look-up detect")
	}
	h := []ConvMessage{
		{FromSelf: false, Text: "(gif)"},
		{FromSelf: false, Text: "(gif)"},
		{FromSelf: true, Text: "omg send them pls"},
		{FromSelf: false, Text: "look up"},
	}
	if !theyRecentlySentMedia(h) || !weAlreadyAskedForSend(h) {
		t.Fatal("expected recent media + prior send ask")
	}
	if asksThemToSendAgain("yeah send them") != true {
		t.Fatal("expected send-again detect")
	}
	if got := fallbackReply("look up"); !strings.Contains(strings.ToLower(got), "load") {
		t.Fatalf("look-up fallback should react to media: %q", got)
	}
	if got := fallbackReply("(gif)"); !strings.Contains(strings.ToLower(got), "load") {
		t.Fatalf("gif placeholder fallback should react: %q", got)
	}
}

func TestPickReplyTextReactsToMediaInsteadOfReAsk(t *testing.T) {
	cfg := MessageConfig{AI: AIConfig{Enabled: false}, ReplyTemplates: []string{"hey"}}
	h := []ConvMessage{
		{FromSelf: false, Text: "want some gifs?"},
		{FromSelf: true, Text: "yeah send them"},
		{FromSelf: false, Text: "(gif)"},
		{FromSelf: false, Text: "(gif)"},
		{FromSelf: false, Text: "look up"},
	}
	got, src := pickReplyTextFor(cfg, "acct", "unknownpleasuresof", h)
	if src != "template" && src != "beacons" {
		t.Fatalf("src=%q", src)
	}
	l := strings.ToLower(got)
	if asksThemToSendAgain(got) {
		t.Fatalf("must not re-ask after media: %q", got)
	}
	if strings.Contains(l, "no eyes") || strings.Contains(l, "dont have eyes") {
		t.Fatalf("must not claim no eyes: %q", got)
	}
	if !strings.Contains(l, "load") && !hasBeaconsLink(got) && !looksLikePinNudge(got) {
		t.Fatalf("expected media reaction (or overdue funnel): %q", got)
	}
}

func TestBotDefenseNotUsedOnMediaDump(t *testing.T) {
	if !soundsLikeBotDefense("lol no i just type like that\nwhat do u even post") {
		t.Fatal("expected bot-defense detect")
	}
	if soundsLikeBotDefense("omg wait i see them hehe :P") {
		t.Fatal("media reaction is not bot-defense")
	}
	cfg := MessageConfig{AI: AIConfig{Enabled: false}}
	h := []ConvMessage{
		{FromSelf: false, Text: "(image)"},
		{FromSelf: false, Text: "Are you a bot"},
	}
	got, _ := pickReplyTextFor(cfg, "", "unknownpleasuresof", h)
	l := strings.ToLower(got)
	if strings.Contains(l, "what do u even post") {
		t.Fatalf("bot reply after media must not pivot to what do u post: %q", got)
	}
	if asksThemToSendAgain(got) {
		t.Fatalf("bot reply must not re-ask for send: %q", got)
	}
}

func TestFunnelForceAfterSpicyOverdue(t *testing.T) {
	var h []ConvMessage
	for i := 0; i < funnelAfterZoeyReplies+4; i++ {
		h = append(h,
			ConvMessage{FromSelf: false, Text: "sending more porn gifs"},
			ConvMessage{FromSelf: true, Text: fmt.Sprintf("hot set %d", i)},
			ConvMessage{FromSelf: true, Text: "more pls"},
		)
	}
	h = append(h, ConvMessage{FromSelf: false, Text: "(gif)"})
	if !conversationLooksSpicy(h) {
		t.Fatal("fixture should look spicy")
	}
	if zoeyReplyTurns(h) < funnelAfterZoeyReplies+3 {
		t.Fatalf("sets=%d want >= %d", zoeyReplyTurns(h), funnelAfterZoeyReplies+3)
	}
	if !shouldSendBeacons(h, "acct-force") {
		t.Fatal("spicy + overdue sets should force funnel")
	}
	// Before force window, a real kink follow-up stalls the funnel.
	var early []ConvMessage
	for i := 0; i < funnelAfterZoeyReplies; i++ {
		early = append(early,
			ConvMessage{FromSelf: false, Text: "i love ass more"},
			ConvMessage{FromSelf: true, Text: fmt.Sprintf("same %d", i)},
		)
	}
	early = append(early, ConvMessage{FromSelf: false, Text: "any others?"})
	if !theyExpectContentReply(early) {
		t.Fatal("kink follow-up should expect a real answer")
	}
	if shouldSendBeacons(early, "acct-early") {
		t.Fatal("should not funnel yet while they expect content at the threshold")
	}
}

func TestShouldAskNameSkipsMediaMoments(t *testing.T) {
	var h []ConvMessage
	for i := 0; i < askNameAfterZoeyReplies+1; i++ {
		h = append(h,
			ConvMessage{FromSelf: false, Text: "hey"},
			ConvMessage{FromSelf: true, Text: "yo"},
			ConvMessage{FromSelf: true, Text: "whats up"},
		)
	}
	h = append(h,
		ConvMessage{FromSelf: false, Text: "(gif)"},
		ConvMessage{FromSelf: false, Text: "look up"},
	)
	if zoeyReplyTurns(h) < askNameAfterZoeyReplies {
		t.Fatalf("fixture turns=%d", zoeyReplyTurns(h))
	}
	if shouldAskTheirName(h, "unknownpleasuresof") {
		t.Fatal("must not name-ask mid gif dump")
	}
}

// Chats from before the pinned-post switch still carry the raw link.
var legacyBeaconsLinkMsg = "hehe its easier off here\n" + beaconsLink

func TestOpenRouterCooldown(t *testing.T) {
	if d, ok := openRouterCooldown(fmt.Errorf(`429 Too Many Requests: {"error":{"message":"Rate limit exceeded: free-models-per-min. "}}`)); !ok || d != time.Minute {
		t.Fatalf("per-min: got %v %v", d, ok)
	}
	if d, ok := openRouterCooldown(fmt.Errorf("429: Rate limit exceeded: free-models-per-day")); !ok || d <= 0 || d > 24*time.Hour {
		t.Fatalf("per-day: got %v %v", d, ok)
	}
	if _, ok := openRouterCooldown(fmt.Errorf("429 upstream provider busy")); ok {
		t.Fatal("upstream 429 should just try the next model")
	}
}
