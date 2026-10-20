package main

import (
	"strings"
	"testing"
)

func TestMinorClaimsAlwaysTrip(t *testing.T) {
	minors := []string{
		"im 16", "I'm 17", "i am only 15", "im just 14 lol", "i m 13",
		"16 years old", "15yo", "17 y/o", "14 yrs old", "im 16 years old too",
		"16m", "17 f", "15 male here", "16 boy", "m16", "f/17", "male 15", "16/m",
		"age 16", "aged 15", "age: 14",
		"16 here", "17 and bored", "15 lol",
		"im sixteen", "i am seventeen", "fifteen years old",
		"im a teen", "I'm a teenager", "im still a minor", "under 18", "underage", "not 18 yet",
		"almost 18", "turning 18 in march", "till im 18", "jailbait", "as a minor",
		"im in high school", "i'm in 10th grade", "im in year 10", "still in highschool",
		"i go to high school", "11th grader", "doing my gcses",
		"asl? 16 m uk",
		"jake, 15", "im dave, 16", "my name is sam - 14",
	}
	for _, s := range minors {
		if !messageClaimsUnder18(s) {
			t.Errorf("must flag as under 18: %q", s)
		}
		h := chat("you: hii", "them: "+s)
		if !theyClaimUnderage(h) {
			t.Errorf("chat must stop for %q", s)
		}
		if _, ok := theirStatedAge(h); ok {
			t.Errorf("a minor claim must never yield an adult age: %q", s)
		}
	}
}

func TestAdultChatDoesNotTrip(t *testing.T) {
	adults := []string{
		"im 25", "I'm 34 and single", "im 6ft", "im 5 mins away", "im 2 hours from u",
		"i'm 7 inches", "im 19 years old", "my son is 7 years old", "my daughter is 16 years old and annoying",
		"what are you up to", "i work 9 to 5", "i have 3 kids", "im in my 30s",
		"minors dni", "i went to high school in ohio",
	}
	for _, s := range adults {
		if messageClaimsUnder18(s) {
			t.Errorf("adult message wrongly flagged: %q", s)
		}
	}
}

func TestBareNumberAfterAgeQuestion(t *testing.T) {
	h := chat("them: hey", "you: hii whats ur name and how old r u btw :3", "them: 16")
	if !theyClaimUnderage(h) {
		t.Fatal(`"16" after "how old r u" must block`)
	}
	h = chat("them: hey", "you: how old r u btw :3", "them: 29 wbu")
	if theyClaimUnderage(h) {
		t.Fatal("29 is an adult")
	}
	if age, ok := theirStatedAge(h); !ok || age != 29 {
		t.Fatalf("want age 29, got %d %v", age, ok)
	}
	h = chat("them: hey", "you: whats ur name and how old r u btw :3", "them: im dave, 16")
	if !theyClaimUnderage(h) {
		t.Fatal(`"im dave, 16" after the age question must block`)
	}
	h = chat("them: hey", "you: how old r u", "them: lol older than u think, 15")
	if !theyClaimUnderage(h) {
		t.Fatal("any under-18 number in the answer must block")
	}
	h = chat("them: hey", "you: whats ur fav movie", "them: 12 angry men")
	if theyClaimUnderage(h) {
		t.Fatal("a number not answering an age question should not trip")
	}
}

func TestAdultAgeForms(t *testing.T) {
	for s, want := range map[string]int{
		"im 32":           32,
		"28 years old":    28,
		"45m":             45,
		"m/38 here":       38,
		"age 41":          41,
		"Mark, 34. you?":  34,
		"I'm 22 lol":      22,
		"im 6ft and 30yo": 30,
		"my son is 19 yo": 0,
	} {
		got, ok := theirStatedAge(chat("you: hii", "them: "+s))
		if want == 0 {
			if ok {
				t.Errorf("%q: want no age, got %d", s, got)
			}
			continue
		}
		if !ok || got != want {
			t.Errorf("%q: want %d, got %d %v", s, want, got, ok)
		}
	}
}

func TestBioMinorSignals(t *testing.T) {
	minors := []BlogProfile{
		{Description: "16 | he/him | gamer"},
		{Description: "jake • 15 • uk"},
		{Title: "minor", Description: "just vibes"},
		{Description: "17 y/o just here for memes"},
		{Description: "born 2011, love anime"},
		{Description: "im a minor so dont be weird"},
		{Description: "im a minor pls dni if younger than 12"},
		{Description: "Kerstin| 17 |Canada"},
		{Description: "girl | 17 and lost in life. dni if under 18"},
		{Description: "I'm underage so block all porn blogs"},
		{Description: "she/her | minor | partaker in fandoms"},
	}
	for _, p := range minors {
		if !bioSuggestsMinor(p) {
			t.Errorf("bio should flag minor: %+v", p)
		}
	}
	adults := []BlogProfile{
		{Description: "minors dni | 18+ only | he/him"},
		{Description: "30 | he/him | dad of 2"},
		{Description: "dni if minor. nsfw"},
		{Description: "est 2016 photography"},
		{Description: ""},
		{Description: "MDNI | nsfw | 34"},
		{Description: "Same rules as before, under 18 bye bye and this new page will be NSFW"},
		{Description: "Christ is King! No under 18"},
		{Description: "This is an adult blog. If you are not 18+ or uncomfortable please leave now"},
		{Description: "if your not 18&older don't message me"},
		{Description: "55+ White male. No one under 18 - EVER."},
		{Description: "Adults Only, under 18 go away now. 40/He/Him"},
		{Description: "Old white dude (50 something so not a dang minor)"},
		{Description: "NSFW ...Pups under 18, kindly leave. Thank you"},
		{Description: "If you underage please leave.40M with a fun side"},
		{Description: "minor/profic or proship dni!!! he/him"},
		{Description: "This blog is 18+ and I do not condone any minors on my blog"},
	}
	for _, p := range adults {
		if bioSuggestsMinor(p) {
			t.Errorf("adult bio wrongly flagged: %+v", p)
		}
	}
}

func TestMinorBlockIsPermanentAndGlobal(t *testing.T) {
	t.Chdir(t.TempDir())
	rememberAdultAge("someguy", 16)
	if _, ok := rememberedAdultAge("someguy"); ok {
		t.Fatal("never remember an under-18 age as adult")
	}
	rememberAdultAge("someguy", 25)
	blockMinor("SomeGuy", "acct1", "said under 18 in chat")
	if !isBlockedMinor("@someguy") {
		t.Fatal("block must be case/@-insensitive")
	}
	if _, ok := rememberedAdultAge("someguy"); ok {
		t.Fatal("blocking must wipe any stored adult age")
	}
	rememberAdultAge("someguy", 30)
	if _, ok := rememberedAdultAge("someguy"); ok {
		t.Fatal("a blocked minor can never become a confirmed adult")
	}
	h := chat("them: im 30 now promise", "you: hii")
	if confirmedAdult("someguy", h) {
		t.Fatal("blocked list wins over a later adult claim")
	}
	if !shouldStopReplying(h, "someguy") {
		t.Fatal("never reply to a blocked minor")
	}
	if n := enqueueLikers([]string{"someguy", "other"}, ScrapedPost{Blog: "x", ID: "1"}); n != 1 {
		t.Fatalf("blocked minor must not be queued for reach, got %d", n)
	}
}

func TestGateHoldsFunnelAndAsksAgeUntilAdult(t *testing.T) {
	t.Chdir(t.TempDir())
	h := chat("them: hey cutie", "you: hiii :3", "them: wanna see something")
	out := gateReplyOnAge("acct", "newguy", h, "hehe maybe\ncheck my pinned post ;)")
	if looksLikePinNudge(out) || hasBeaconsLink(out) {
		t.Fatalf("no funnel before age is known: %q", out)
	}
	if !strings.Contains(out, "how old r u") || !strings.Contains(out, "name") {
		t.Fatalf("should ask name and age: %q", out)
	}

	h = chat("them: hey", "you: hii whats ur name and how old r u btw :3", "them: im dave, 31")
	out = gateReplyOnAge("acct", "dave", h, "hiii dave\ncheck my pinned post ;)")
	if !looksLikePinNudge(out) {
		t.Fatalf("adult may get the funnel: %q", out)
	}
	if strings.Contains(out, "how old") {
		t.Fatalf("do not re-ask a known adult: %q", out)
	}

	h = chat("them: hey", "you: hii how old r u btw :3", "them: lol why", "you: just curious", "them: wbu")
	out = gateReplyOnAge("acct", "dodger", h, "im 25 hehe")
	if !strings.Contains(out, "never said how old") {
		t.Fatalf("ask once more if they dodged: %q", out)
	}
}

func TestGateNeverSexualBeforeAdult(t *testing.T) {
	t.Chdir(t.TempDir())
	h := chat("them: morning", "you: still feeling foggy?", "them: Usually a little self play helps haha")
	out := gateReplyOnAge("acct", "foggy", h, "hehe the fastest way to wake up :P\ni do it too when the fog is thick")
	if strings.Contains(out, "i do it too") || !strings.Contains(out, "how old") {
		t.Fatalf("deflect and ask when they go sexual: %q", out)
	}

	h = chat("them: hi", "you: hey :3", "them: nice day")
	out = gateReplyOnAge("acct", "chris", h, "hehe the spicier stuff isnt really on tumblr tho\nwhat u up to")
	if strings.Contains(out, "spicier") || !strings.Contains(out, "what u up to") {
		t.Fatalf("drop sexual bubbles before age is known: %q", out)
	}

	h = chat("them: First thing I'd do is bite your nipple", "you: oh?", "them: Hehe, I'd just hold you close and keep teasing you")
	out = gateReplyOnAge("acct", "vibe", h, "mm holding me close sounds dangerously effective :3\nid probably just melt into u")
	if strings.Contains(out, "melt") || !strings.Contains(out, "how old") {
		t.Fatalf("earlier sexual line keeps the chat gated: %q", out)
	}

	h = chat("you: whats the first naughty thing ull make me do", "them: Hmm probably make you get on your knees and beg for it")
	out = gateReplyOnAge("acct", "knees", h, "mm kneeling for u sounds kinda cute :3")
	if strings.Contains(out, "kneeling") || !strings.Contains(out, "how old") {
		t.Fatalf("knees/beg must be gated: %q", out)
	}

	h = chat("them: You should! I'm looking forward to seeing those lil piggies")
	out = gateReplyOnAge("acct", "toes", h, "lil piggies?? ur so weird nate :P\nhehe i might just have to send proof later")
	if strings.Contains(out, "proof") || strings.Contains(out, "piggies") {
		t.Fatalf("no feet/proof talk before age is known: %q", out)
	}

	h = chat("them: my battery inflated", "them: It's dangerous", "you: oh no", "them: got a new one")
	out = gateReplyOnAge("acct", "battery", h, "glad u got a new one :3")
	if strings.Contains(out, "slow down") || strings.Contains(out, "forward") || strings.Contains(out, "easy tiger") {
		t.Fatalf("innocent chat must not get deflected: %q", out)
	}

	out = withAgeAsk("hehe ty\nwhats ur blog all about", true, 0)
	if strings.Contains(out, "blog all about") || strings.Count(out, "\n") != 1 {
		t.Fatalf("one question per message: %q", out)
	}

	h = chat("them: hey", "you: how old r u btw :3", "them: horny",
		"you: wait u never said how old u are lol", "them: wanna fuck",
		"you: wait u never said how old u are lol", "them: show me ur body")
	if out := gateReplyOnAge("acct", "pushy", h, "lol"); out != "" {
		t.Fatalf("stop replying after asks run out and they stay sexual: %q", out)
	}

	h = chat("them: hey", "you: how old r u btw :3", "them: 29", "them: ur so sexy")
	if out := gateReplyOnAge("acct", "adult", h, "hehe thx babe"); out != "hehe thx babe" {
		t.Fatalf("adults untouched: %q", out)
	}
}
