package main

import (
	"os"
	"strings"
	"testing"
)

func TestTidyReplyDropsEarlyNameAsk(t *testing.T) {
	h := []ConvMessage{{FromSelf: false, Text: "Hi"}}
	got := tidyReplyExtras(h, "zz-tidy-name", "hiii :3\nwhats ur name btw")
	if strings.Contains(foldDM(got), "name") {
		t.Fatalf("first-message name ask should be dropped: %q", got)
	}
	if !strings.Contains(got, "hiii") || !strings.Contains(got, "?") && !strings.Contains(got, "what u up to") && !strings.Contains(got, "hows ur day") {
		t.Fatalf("should keep hi and add an easy question: %q", got)
	}
}

func TestTidyReplyDropsUnpromptedGym(t *testing.T) {
	h := []ConvMessage{{FromSelf: false, Text: "Whats up beautiful"}, {FromSelf: false, Text: "What you up to"}}
	got := tidyReplyExtras(h, "zz-tidy-gym", "aww ur sweet :3\njust in bed on my phone maybe hitting the gym later\nhows ur sunday")
	if strings.Contains(strings.ToLower(got), "gym") {
		t.Fatalf("unprompted gym should be stripped: %q", got)
	}
	if !strings.Contains(got, "in bed on my phone") {
		t.Fatalf("rest of the bubble should survive: %q", got)
	}
	asked := []ConvMessage{{FromSelf: false, Text: "do you go to the gym?"}}
	if got := tidyReplyExtras(asked, "zz-tidy-gym2", "yeah i gym mornings :3"); !strings.Contains(got, "gym") {
		t.Fatalf("gym answer to a gym question must stay: %q", got)
	}
}

func TestMediaTemplateSkipsTextQuestion(t *testing.T) {
	h := []ConvMessage{{FromSelf: false, Text: "Y did u repost my comment"}, {FromSelf: false, Text: "(image)"}}
	if !turnHasTextQuestion(h) {
		t.Fatal("question alongside an image should not get the canned media reply")
	}
	only := []ConvMessage{{FromSelf: false, Text: "WHAT A PAGE!"}, {FromSelf: false, Text: "(image)"}}
	if turnHasTextQuestion(only) {
		t.Fatal("exclamation + image is fine for the media reaction")
	}
}

func TestMediaReactionDoesNotGuessContent(t *testing.T) {
	compliment := []ConvMessage{{FromSelf: false, Text: "WHAT A PAGE!"}, {FromSelf: false, Text: "(image)"}}
	got := mediaReactionReplyFor(compliment, "")
	if strings.Contains(got, "hot asf") || !strings.Contains(got, "thank u") {
		t.Fatalf("compliment + image should thank, not describe: %q", got)
	}
	if got := mediaReactionReplyFor([]ConvMessage{{FromSelf: false, Text: "(image)"}}, ""); got != "wait why tf is that not loading for me" {
		t.Fatalf("bare image should get the not-loading line: %q", got)
	}
	again := []ConvMessage{
		{FromSelf: false, Text: "(image)"},
		{FromSelf: true, Text: "wait why tf is that not loading for me"},
		{FromSelf: false, Text: "(gif)"},
	}
	if got := mediaReactionReplyFor(again, ""); strings.Contains(got, "why tf is that not loading") || !strings.Contains(got, "load") {
		t.Fatalf("second pic should get a different not-loading line: %q", got)
	}
}

func TestTidyReplyDropsUnchasedSorryAndHandle(t *testing.T) {
	h := []ConvMessage{{FromSelf: false, Text: "Hi, i like u"}, {FromSelf: false, Text: "Can i send my sex videos"}}
	got := tidyReplyExtras(h, "hyp-james23", "omg sorry im so bad at checking this\nmm what kinda vids, hyp-james23? :P")
	if strings.Contains(got, "sorry") || strings.Contains(got, "hyp-james23") {
		t.Fatalf("unchased sorry and raw username should go: %q", got)
	}
	chased := []ConvMessage{{FromSelf: false, Text: "hi....?"}}
	if got := tidyReplyExtras(chased, "muttt28", "omg sorry im so bad at checking this\nhows ur evening"); !strings.Contains(got, "sorry") {
		t.Fatalf("sorry is right when they chased: %q", got)
	}
}

func TestNameDetectionIsStrict(t *testing.T) {
	notNames := []string{
		"im tired", "Im tired", "I'm Tired", "im hard", "I'm so horny", "im home now",
		"Im Bored", "I'm Available", "im down", "IM HORNY", "This is Amazing",
		"call me later", "call me daddy", "Ok I'm waiting", "i'm in bed", "I'm Single, you?",
	}
	for _, s := range notNames {
		if n := nameMentionedInText(s); n != "" {
			t.Errorf("%q must not be a name, got %q", s, n)
		}
		if n := theirFirstName([]ConvMessage{{FromSelf: false, Text: s}}, ""); n != "" {
			t.Errorf("%q must not be a name in history, got %q", s, n)
		}
	}
	for s, want := range map[string]string{
		"my name is jake btw":        "Jake",
		"Hey I'm Dave, how are you?": "Dave",
		"call me tyler":              "Tyler",
		"Hi im Marcus":               "Marcus",
	} {
		if got := nameMentionedInText(s); got != want {
			t.Errorf("%q: want %q got %q", s, want, got)
		}
	}
	ask := func(answer string) string {
		return theirFirstName([]ConvMessage{
			{FromSelf: false, Text: "hey"},
			{FromSelf: true, Text: "whats ur name btw :3"},
			{FromSelf: false, Text: answer},
		}, "")
	}
	for s, want := range map[string]string{"dave": "Dave", "its dave :)": "Dave", "Dave wbu?": "Dave", "im sam, you?": "Sam"} {
		if got := ask(s); got != want {
			t.Errorf("name answer %q: want %q got %q", s, want, got)
		}
	}
	for _, s := range []string{"why?", "guess", "im tired lol", "haha", "not telling", "hbu", "u first"} {
		if got := ask(s); got != "" {
			t.Errorf("name answer %q must not be a name, got %q", s, got)
		}
	}
}

func TestNameUsedAboutTwiceNotBackToBack(t *testing.T) {
	h := []ConvMessage{
		{FromSelf: true, Text: "whats ur name btw :3"},
		{FromSelf: false, Text: "dave"},
	}
	if !wantNameThisReply(h, "Dave") {
		t.Fatal("first reply after learning the name should use it")
	}
	h = append(h, ConvMessage{FromSelf: true, Text: "cute name dave :3"}, ConvMessage{FromSelf: false, Text: "thanks"})
	if wantNameThisReply(h, "Dave") {
		t.Fatal("never back to back")
	}
	h = append(h, ConvMessage{FromSelf: true, Text: "hehe"}, ConvMessage{FromSelf: false, Text: "so what u doing"})
	if !wantNameThisReply(h, "Dave") {
		t.Fatal("second use allowed after a gap")
	}
	h = append(h, ConvMessage{FromSelf: true, Text: "in bed dave"}, ConvMessage{FromSelf: false, Text: "nice"},
		ConvMessage{FromSelf: true, Text: "mhm"}, ConvMessage{FromSelf: false, Text: "hot"})
	if wantNameThisReply(h, "Dave") {
		t.Fatal("stop after about two uses")
	}
	if got := stripNameIfOverused("omg dave that's hot\nwhat would u do", "Dave"); strings.Contains(got, "dave") {
		t.Fatalf("overused name should be stripped: %q", got)
	}
}

func TestNameGoesAtBubbleEnd(t *testing.T) {
	if got := nameAtBubbleEnd("work was sooo long today :((", "dave"); got != "work was sooo long today dave :((" {
		t.Fatalf("got %q", got)
	}
	if got := withNameOnce("im so tired from work\nhows ur day going?", "Dave"); !strings.HasPrefix(got, "im so tired from work dave\n") {
		t.Fatalf("got %q", got)
	}
}

func TestSwapGenericStatusForWorkLine(t *testing.T) {
	if got := swapGenericStatus([]ConvMessage{{FromSelf: false, Text: "u like it rough?"}}, "", "not much gets me going like that"); got != "not much gets me going like that" {
		t.Fatalf("only swap when they asked how she is: %q", got)
	}
	howru := []ConvMessage{{FromSelf: false, Text: "How are you doing"}}
	got := swapGenericStatus(howru, "Dave", "i'm good, just chillin in bed on my phone :3\nhow's your day going?")
	if genericStatusRe.MatchString(got) || !workTiredMarkRe.MatchString(got) || !strings.Contains(got, "dave") {
		t.Fatalf("generic status should become a work line with name: %q", got)
	}
	if !strings.Contains(got, "how's your day going?") {
		t.Fatalf("question bubble must survive: %q", got)
	}
	used := []ConvMessage{{FromSelf: true, Text: "these 5am shifts are killing me"}, {FromSelf: false, Text: "wyd"}}
	if got := swapGenericStatus(used, "", "just chillin rn"); got != "just chillin rn" {
		t.Fatalf("work line only once per chat: %q", got)
	}
}

func TestAlsoOnRepeatedQuestion(t *testing.T) {
	got := alsoOnRepeatQuestion(nil, "mm maybe what kinda vids? :P\nwhat do u post on ur blog?")
	if !strings.HasPrefix(strings.Split(got, "\n")[1], "alsooo what do u post") {
		t.Fatalf("stacked question should get alsooo: %q", got)
	}
	h := []ConvMessage{{FromSelf: true, Text: "hows ur day going :3"}, {FromSelf: false, Text: "lol"}}
	got = alsoOnRepeatQuestion(h, "hehe same\nhow's your day going?")
	if !strings.HasPrefix(strings.Split(got, "\n")[1], "alsooo how's your day going") {
		t.Fatalf("repeated question should get alsooo: %q", got)
	}
	if got := alsoOnRepeatQuestion(nil, "whats the vibe?\nomg i love art, so when were u thinking?"); strings.Contains(got, "alsooo") {
		t.Fatalf("alsooo only fronts a bubble that starts with the question: %q", got)
	}
	if got := alsoOnRepeatQuestion(nil, "hehe same\nwhat u up to?"); strings.Contains(got, "alsooo") {
		t.Fatalf("single fresh question stays plain: %q", got)
	}
}

func TestTakeBackOnlyAfterRealShutdown(t *testing.T) {
	fresh := []ConvMessage{{FromSelf: false, Text: "are you new to being a sub?"}}
	if got := tidyReplyExtras(fresh, "x1", "omg ignore me i was being weird lol\nkinda new to it tbh"); strings.Contains(got, "ignore me") {
		t.Fatalf("no take-back when she never shut them down: %q", got)
	}
	shut := []ConvMessage{
		{FromSelf: false, Text: "your blog makes me leak"},
		{FromSelf: true, Text: "please keep it clean for me"},
		{FromSelf: false, Text: "huh wdym"},
	}
	if got := tidyReplyExtras(shut, "x2", "omg ignore me i was being weird lol\nur locked up rn?"); !strings.Contains(got, "ignore me") {
		t.Fatalf("take-back is right after a real shutdown: %q", got)
	}
	again := append(shut, ConvMessage{FromSelf: true, Text: "omg ignore me i was being weird lol"}, ConvMessage{FromSelf: false, Text: "haha ok"})
	if got := tidyReplyExtras(again, "x3", "omg ignore me i was being weird lol\nso what u into"); strings.Contains(got, "ignore me") {
		t.Fatalf("take-back only once: %q", got)
	}
}

func TestNoMeetUpSuggestions(t *testing.T) {
	h := []ConvMessage{{FromSelf: false, Text: "im painting portraits for an exhibition"}}
	got := tidyReplyExtras(h, "x4", "sounds sooo cool :3\nalsooo would you need a pic or meet up sometime? :P")
	if strings.Contains(got, "meet") {
		t.Fatalf("meetup bubble must go: %q", got)
	}
	if got := tidyReplyExtras(h, "x5", "i wanna make u come over and over :P"); !strings.Contains(got, "come over and over") {
		t.Fatalf("sexual come over and over is not a meetup: %q", got)
	}
}

func TestCasualizeReplyDropsGrammar(t *testing.T) {
	got := casualizeReply("What would you start with, babe?\nI'm good. You're sweet :P\nmm maybe...")
	lines := strings.Split(got, "\n")
	if l := strings.TrimSuffix(lines[0], "?"); l != "what would u start with babe" {
		t.Fatalf("line 1: %q", lines[0])
	}
	if lines[1] != "im good ur sweet :P" {
		t.Fatalf("line 2: %q", lines[1])
	}
	if lines[2] != "mm maybe..." {
		t.Fatalf("ellipsis should stay: %q", lines[2])
	}
	link := "https://beacons.ai/lil.cosmic.bunny"
	if got := casualizeReply(link); got != link {
		t.Fatalf("link bubble must be untouched: %q", got)
	}
}

func TestSanitizeAIReplyAscii(t *testing.T) {
	got := sanitizeAIReply("i’m good—just in bed :3\nwhat u up to?")
	if strings.ContainsAny(got, "’—") {
		t.Fatalf("curly punctuation should be normalized: %q", got)
	}
}

func TestFitBubbleBudgetKeepsQuestion(t *testing.T) {
	b := []string{"omg that sounds intense :3", "im imagining it lol", "where would u even start?"}
	got := fitBubbleBudget(b, 2)
	if len(got) != 2 || !strings.Contains(got[1], "?") {
		t.Fatalf("want 2 bubbles keeping the question: %q", got)
	}
	if got := fitBubbleBudget([]string{"hiii :3", "hows ur day going"}, 1); len(got) != 1 || got[0] != "hiii :3 hows ur day going" {
		t.Fatalf("short reply should merge into one bubble: %q", got)
	}
}

func TestReplyBubbleBudgetScalesWithTurn(t *testing.T) {
	busy := []ConvMessage{
		{FromSelf: false, Text: "hi...?"},
		{FromSelf: false, Text: "Please may I follow you back and may we text...?"},
		{FromSelf: false, Text: "I'd love to follow your Insta if you're more active there?"},
	}
	if n := replyBubbleBudget(busy); n != 3 {
		t.Fatalf("busy multi-question turn should allow 3, got %d", n)
	}
	for i := 0; i < 20; i++ {
		if n := replyBubbleBudget([]ConvMessage{{FromSelf: false, Text: "Hey"}}); n > 2 {
			t.Fatalf("bare hey should get 1-2 bubbles, got %d", n)
		}
	}
}

func TestNoRepeatOfMediaReactionAfterTheyExplain(t *testing.T) {
	h := []ConvMessage{
		{Text: "(image)"},
		{Text: "omg thank u :3\nwait what did u send me hehe", FromSelf: true},
		{Text: "Whoops that was supposed to be something else"},
		{Text: "Although that one is pretty dope"},
	}
	if !weRepliedSinceTheirMedia(h) {
		t.Fatal("we already reacted to their pic")
	}
	if got := dropRepeatedBubbles("omg thank u :3\nwait what did u send me hehe", h); got != "" {
		t.Fatalf("repeat bubbles should all drop, got %q", got)
	}
	if got := dropRepeatedBubbles("hehe no worries\nwhat was it meant to be", h); got != "hehe no worries\nwhat was it meant to be" {
		t.Fatalf("new bubbles must stay, got %q", got)
	}
}

func TestCasualizeStripsStrayAngle(t *testing.T) {
	if got := casualizeReply("what u up to today?>"); strings.Contains(got, ">") {
		t.Fatalf("stray > kept: %q", got)
	}
	if got := casualizeReply("hehe >.<"); !strings.Contains(got, ">.<") {
		t.Fatalf("kaomoji broken: %q", got)
	}
}

func TestNotLoadingUsesTheirName(t *testing.T) {
	h := []ConvMessage{
		{FromSelf: false, Text: "Hey im Dave"},
		{FromSelf: true, Text: "hey whats up"},
		{FromSelf: false, Text: "(image)"},
		{FromSelf: false, Text: "(image)"},
	}
	got := mediaReactionReplyFor(h, "Dave")
	if got != "ughh dave theyre not fking loading 😭" {
		t.Fatalf("named plural line expected, got %q", got)
	}
	if one := mediaReactionReplyFor(h[:3], "Dave"); one != "ughh dave its not fking loading 😭" {
		t.Fatalf("single pic should say its, got %q", one)
	}
	if final := casualizeReply(applyBeaconsFunnelFor("acct", h, "dave123", got)); !strings.Contains(final, "😭") || !strings.Contains(final, "dave") {
		t.Fatalf("pipeline must keep name and emoji: %q", final)
	}
	back := append(append([]ConvMessage{}, h...), ConvMessage{FromSelf: true, Text: "omg dave ur funny"}, ConvMessage{FromSelf: false, Text: "(gif)"})
	if got := mediaReactionReplyFor(back, "Dave"); strings.Contains(got, "dave") {
		t.Fatalf("name used last reply, must not repeat it: %q", got)
	}
}

func TestNameAskOnlyOncePerChat(t *testing.T) {
	dir := t.TempDir()
	old, _ := os.Getwd()
	defer os.Chdir(old)
	_ = os.Chdir(dir)
	h := []ConvMessage{{FromSelf: false, Text: "I do taekwondo"}}
	first := onlyAskNameOnce("acct", "jakeblog", h, "omg thats cool\nwhats ur name btw :3")
	if !strings.Contains(first, "whats ur name") {
		t.Fatalf("first ask should go out: %q", first)
	}
	second := onlyAskNameOnce("acct", "jakeblog", h, "haha nice\nwhats ur name btw :3")
	if strings.Contains(second, "name") || second != "haha nice" {
		t.Fatalf("second ask must be stripped: %q", second)
	}
	other := onlyAskNameOnce("acct", "someoneelse", h, "hey\nwhats ur name btw")
	if !strings.Contains(other, "whats ur name") {
		t.Fatalf("other chats unaffected: %q", other)
	}
}
