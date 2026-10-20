package main

import (
	"fmt"
	"regexp"
	"strings"
)

// replyProblem is one checkable way a draft fails the persona. fix is written
// to the model on the retry, so it says what to do, not what went wrong.
type replyProblem struct {
	code string
	fix  string
}

var (
	questionStartRe = regexp.MustCompile(`^(what|whats|how|hows|do|does|did|would|wanna|are|r|is|can|could|have|where|when|which|who|why|u|ur|should|will|any|so what|so how|and what|tell me|show me)\b`)
	teaseRe         = regexp.MustCompile(`\b(is that a promise|and then what|prove it|go on|tell me more|show me|dare u|bet u|or nah|u sure|right\?|wbu|hbu)\b`)
	wordRe          = regexp.MustCompile(`[a-z0-9']+`)
	narrationRe     = regexp.MustCompile(`\b(sounds|looks|seems) (so+ |really |super |kinda |sooo+ )?(intense|hot|wild|fun|amazing|nice|good|sexy)\b|\b(that|this) (image|scene|pic) is\b`)
	spelledAgeRe    = regexp.MustCompile(`\btwenty[ -]?five\b`)
)

// Common words that make any two texts overlap; a copied phrase has to carry
// at least two words outside this set.
var parrotStopwords = map[string]bool{
	"i": true, "im": true, "u": true, "you": true, "ur": true, "your": true, "me": true, "my": true,
	"a": true, "an": true, "the": true, "and": true, "or": true, "to": true, "of": true, "in": true,
	"on": true, "it": true, "its": true, "is": true, "that": true, "this": true, "so": true, "be": true,
	"do": true, "for": true, "with": true, "what": true, "just": true, "like": true, "we": true, "are": true,
	"was": true, "have": true, "at": true, "but": true, "if": true, "too": true, "lol": true, "yeah": true,
	"make": true, "made": true, "get": true, "got": true, "would": true, "can": true,
}

func bubbleLines(text string) []string {
	var out []string
	for _, l := range strings.Split(text, "\n") {
		if l = strings.TrimSpace(l); l != "" {
			out = append(out, l)
		}
	}
	return out
}

func endsWithQuestionOrTease(bubble string) bool {
	l := foldDM(bubble)
	return strings.Contains(l, "?") || questionStartRe.MatchString(l) || teaseRe.MatchString(l)
}

func dmWords(s string) []string {
	return wordRe.FindAllString(strings.ReplaceAll(foldDM(s), "'", ""), -1)
}

// copiedPhrase finds a run of 3+ words the draft lifted from their turn, or
// two long uncommon words in a row ("horniness energy").
func copiedPhrase(theirs, draft string) string {
	src := " " + strings.Join(dmWords(theirs), " ") + " "
	words := dmWords(draft)
	for i := 0; i+2 <= len(words); i++ {
		a, b := words[i], words[i+1]
		if len(a) >= 5 && len(b) >= 5 && !parrotStopwords[a] && !parrotStopwords[b] &&
			strings.Contains(src, " "+a+" "+b+" ") {
			return a + " " + b
		}
	}
	for n := 5; n >= 3; n-- {
		for i := 0; i+n <= len(words); i++ {
			run := words[i : i+n]
			content := 0
			for _, w := range run {
				if !parrotStopwords[w] && len(w) > 2 {
					content++
				}
			}
			if content < 2 {
				continue
			}
			phrase := strings.Join(run, " ")
			if strings.Contains(src, " "+phrase+" ") {
				return phrase
			}
		}
	}
	return ""
}

func replyProblems(history []ConvMessage, text string) []replyProblem {
	bubbles := bubbleLines(text)
	if len(bubbles) == 0 {
		return nil
	}
	turn := unansweredUserTurn(history)
	if turn == "" {
		turn = lastUserText(history)
	}
	fturn := foldDM(turn)
	ftext := foldDM(text)
	var out []replyProblem

	if turn != "" && !goodbyeRe.MatchString(fturn) && !endsWithQuestionOrTease(bubbles[len(bubbles)-1]) {
		out = append(out, replyProblem{"flat-ending",
			"the last bubble must be a short question or tease that follows from what they said"})
	}
	if p := copiedPhrase(turn, text); p != "" {
		out = append(out, replyProblem{"parrot",
			fmt.Sprintf(`don't repeat their words back ("%s") — react in ur own words`, p)})
	}
	if m := narrationRe.FindString(ftext); m != "" {
		out = append(out, replyProblem{"narrates",
			fmt.Sprintf(`don't narrate ("%s") — flirt back or tease instead`, m)})
	}
	if selfIntroRe.MatchString(fturn) && wbuRe.MatchString(fturn) && !strings.Contains(ftext, "zoey") {
		out = append(out, replyProblem{"ignored-intro",
			"they told u their name and asked about u — tell them ur zoey first"})
	}
	if looksLikeAgeQuestion(turn) && !strings.Contains(ftext, "25") && !spelledAgeRe.MatchString(ftext) {
		out = append(out, replyProblem{"ignored-age", "they asked ur age — say 25"})
	}
	if thanksRe.MatchString(foldDM(bubbles[0])) && !complimentRe.MatchString(fturn) {
		out = append(out, replyProblem{"misplaced-thanks",
			"they did not compliment u this time — don't thank them, answer what they actually said"})
	}
	if o := openerOf(bubbles[0]); o != "" {
		for _, prev := range recentZoeyOpeners(history, 3) {
			if prev == o {
				out = append(out, replyProblem{"same-opener",
					fmt.Sprintf(`u already started a recent reply with "%s" — open differently`, o)})
				break
			}
		}
	}
	if therapistRe.MatchString(ftext) {
		out = append(out, replyProblem{"therapist",
			"no therapist/interview questions (feel respected, valued, heard, safe) — ask something light and specific"})
	}
	if len(bubbles) > 3 {
		out = append(out, replyProblem{"too-many-bubbles", "use at most 3 short bubbles"})
	}
	for _, b := range bubbles {
		if len(strings.Fields(b)) > 22 {
			out = append(out, replyProblem{"long-bubble", "keep every bubble under ~15 words"})
			break
		}
	}
	return out
}

var (
	thanksRe     = regexp.MustCompile(`^(aww+ |aw+ |omg )?(thank|thanks|thx|ty|tysm)\b`)
	complimentRe = regexp.MustCompile(`\b(cute|pretty|beautiful|gorgeous|hot|sexy|stunning|lovely|adorable|love (ur|your)|like (ur|your)|nice (blog|pics?|page)|great (blog|taste)|cool blog|good girl|amazing|fine|thank|thanks|thx|ty)\b`)
	therapistRe  = regexp.MustCompile(`\bfeel (respected|valued|heard|safe|appreciated|seen|validated)\b|\bhow does that make (u|you) feel\b`)
)

// openerOf is the first two words of a bubble, the part that sounds
// repetitive when every reply starts the same way.
func openerOf(bubble string) string {
	w := dmWords(bubble)
	if len(w) < 2 {
		return ""
	}
	return w[0] + " " + w[1]
}

func recentZoeyOpeners(history []ConvMessage, n int) []string {
	var out []string
	for i := len(history) - 1; i >= 0 && len(out) < n; i-- {
		if history[i].FromSelf && (i == 0 || !history[i-1].FromSelf) {
			if o := openerOf(history[i].Text); o != "" {
				out = append(out, o)
			}
		}
	}
	return out
}

func problemCodes(ps []replyProblem) string {
	codes := make([]string, len(ps))
	for i, p := range ps {
		codes[i] = p.code
	}
	return strings.Join(codes, ",")
}

// critiquePersona tells the retry exactly what the first draft got wrong.
func critiquePersona(persona, draft string, ps []replyProblem, extra string) string {
	var b strings.Builder
	b.WriteString(persona)
	b.WriteString("\n\nYour previous draft for this reply was:\n")
	b.WriteString(draft)
	b.WriteString("\nWrite a better one. Fix these:")
	for _, p := range ps {
		b.WriteString("\n- " + p.fix)
	}
	if extra != "" {
		b.WriteString("\n- " + extra)
	}
	return b.String()
}
