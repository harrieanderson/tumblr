package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Anyone who says or shows they are under 18 is blocked for good across every
// account: no replies, no follows, no likes. Until someone has said they are
// 18+, replies stay non-sexual, ask their age, and never funnel. When in doubt
// we treat them as a minor — a wrongly blocked adult costs nothing.
const (
	minorsFile        = "data/minors.json"
	agesFile          = "data/ages.json"
	bioCheckedFile    = "data/bio-checked.json"
	bioRecheckAfter   = 7 * 24 * time.Hour
	maxAgeAsks        = 2
	askAgeFromZoeyTur = 1
)

type minorRecord struct {
	At      time.Time `json:"at"`
	Account string    `json:"account"`
	Why     string    `json:"why"`
}

type ageRecord struct {
	Age int       `json:"age"`
	At  time.Time `json:"at"`
}

var ageGateMu sync.Mutex

func blogKey(blog string) string {
	return strings.ToLower(strings.TrimPrefix(strings.TrimSpace(blog), "@"))
}

func loadJSONMap[T any](path string) map[string]T {
	m := map[string]T{}
	if raw, err := os.ReadFile(path); err == nil {
		_ = json.Unmarshal(raw, &m)
	}
	return m
}

func saveJSONMap[T any](path string, m map[string]T) {
	raw, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return
	}
	_ = os.MkdirAll(filepath.Dir(path), 0o755)
	_ = os.WriteFile(path, append(raw, '\n'), 0o644)
}

func isBlockedMinor(blog string) bool {
	k := blogKey(blog)
	if k == "" {
		return false
	}
	ageGateMu.Lock()
	defer ageGateMu.Unlock()
	_, ok := loadJSONMap[minorRecord](minorsFile)[k]
	return ok
}

// blockMinor records them permanently. Dry runs still block: this list only
// ever stops us talking to someone, so writing it is always safe.
func blockMinor(blog, account, why string) {
	k := blogKey(blog)
	if k == "" {
		return
	}
	ageGateMu.Lock()
	defer ageGateMu.Unlock()
	m := loadJSONMap[minorRecord](minorsFile)
	if _, ok := m[k]; ok {
		return
	}
	m[k] = minorRecord{At: time.Now(), Account: account, Why: why}
	saveJSONMap(minorsFile, m)
	ages := loadJSONMap[ageRecord](agesFile)
	if _, ok := ages[k]; ok {
		delete(ages, k)
		saveJSONMap(agesFile, ages)
	}
}

func rememberAdultAge(blog string, age int) {
	k := blogKey(blog)
	if k == "" || age < 18 {
		return
	}
	ageGateMu.Lock()
	defer ageGateMu.Unlock()
	if _, minor := loadJSONMap[minorRecord](minorsFile)[k]; minor {
		return
	}
	m := loadJSONMap[ageRecord](agesFile)
	if r, ok := m[k]; ok && r.Age == age {
		return
	}
	m[k] = ageRecord{Age: age, At: time.Now()}
	saveJSONMap(agesFile, m)
}

func rememberedAdultAge(blog string) (int, bool) {
	k := blogKey(blog)
	if k == "" {
		return 0, false
	}
	ageGateMu.Lock()
	defer ageGateMu.Unlock()
	r, ok := loadJSONMap[ageRecord](agesFile)[k]
	return r.Age, ok && r.Age >= 18
}

// --- spotting a minor in what they write ---

var (
	minorNum     = `(1[0-7]|[1-9])`
	teenNum      = `(1[2-7])`
	minorWordNum = `(twelve|thirteen|fourteen|fifteen|sixteen|seventeen)`

	// "im 16", "i am only 15" — the word after is checked so "im 6ft" or
	// "im 5 mins away" do not trip.
	reIAmNum = regexp.MustCompile(`\b(?:i\s*am|i'm|im|i\s*m)\s*(?:only\s*|just\s*|like\s*|barely\s*|still\s*)?(\d{1,2})\b\s*([a-z'"%]*)`)
	// "16 years old", "15yo", "17 y/o"
	reNumYearsOld = regexp.MustCompile(`\b` + minorNum + `\s*(?:years?\s*old|yrs?\s*old|yr\s*old|y\s*/\s*o|y\.o\.?|yo)\b`)
	// "16m", "17 f", "16 male", "15 boy"
	reNumSex = regexp.MustCompile(`\b` + teenNum + `\s*(?:m|f|male|female|boy|girl|guy|nb|ftm|mtf)\b`)
	// "m16", "f/17", "m 15"
	reSexNum = regexp.MustCompile(`\b(?:m|f|male|female)\s*/?\s*` + teenNum + `\b`)
	// "16/m"
	reNumSlashSex = regexp.MustCompile(`\b` + teenNum + `\s*/\s*(?:m|f)\b`)
	// "age 16", "aged 15", "age: 14"
	reAgeNum = regexp.MustCompile(`\bage[d:\s-]*\s*` + minorNum + `\b`)
	// "16 here", "17 and bored"
	reNumHere = regexp.MustCompile(`\b` + teenNum + `\s+(?:here|lol|lmao|haha|and\s+(?:horny|bored|single|lonely|curious))\b`)
	// spelled out
	reIAmWord   = regexp.MustCompile(`\b(?:i'?m|im|i\s*am|age|aged)\s+(?:only\s+|just\s+)?` + minorWordNum + `\b`)
	reWordYears = regexp.MustCompile(`\b` + minorWordNum + `\s*(?:years?|yrs?|y\s*/\s*o|yo)\b`)
	// "im a teen", "im a minor", "underage", "not 18 yet", "turning 18"
	reMinorSelf = regexp.MustCompile(`\b(?:i'?m|im|i\s*am)\s*(?:a\s*|still\s*a\s*|only\s*a\s*|just\s*a\s*)?(?:teen(?:ager)?|minor|kid|high\s*schooler|highschooler|freshman|sophomore)\b` +
		`|\bunder\s*(?:18|eighteen)\b|\bunder\s*age\b|\bunderage\b|\bnot\s*(?:18|eighteen)\b` +
		`|\b(?:almost|nearly)\s*(?:18|eighteen)\b|\bturn(?:ing|s)?\s*(?:18|eighteen)\b` +
		`|\b(?:till|until|til|when)\s*i'?m\s*(?:18|eighteen)\b|\bjail\s*bait\b` +
		`|\bas\s*a\s*minor\b|\bminor\s*here\b`)
	// school still attended
	reSchool = regexp.MustCompile(`\b(?:i'?m|im|i\s*am|still)\s*(?:in|at)\s*(?:high\s*school|highschool|middle\s*school|junior\s*high|secondary\s*school|sixth\s*form|year\s*(?:7|8|9|10|11|12|13)|(?:7|8|9|10|11|12)(?:th)?\s*grade|grade\s*(?:7|8|9|10|11|12))\b` +
		`|\bi\s*(?:go|attend)\s*(?:to\s*)?(?:high|middle)\s*school\b` +
		`|\b(?:7|8|9|10|11|12)th\s*grader\b|\bdoing\s*(?:my\s*)?gcses?\b`)

	reSomeoneElse = regexp.MustCompile(`\bmy\s+(?:son|daughter|kid|kids|child|children|brother|sister|bro|sis|nephew|niece|cousin|stepson|stepdaughter|dog|cat|puppy|pet|baby)\b`)
	unitAfterAge  = map[string]bool{
		"ft": true, "foot": true, "feet": true, "'": true, "\"": true, "inch": true, "inches": true, "in": true,
		"cm": true, "mm": true, "min": true, "mins": true, "minute": true, "minutes": true, "hr": true, "hrs": true,
		"hour": true, "hours": true, "mi": true, "mile": true, "miles": true, "km": true, "k": true, "%": true,
		"am": true, "pm": true, "th": true, "st": true, "nd": true, "rd": true, "x": true, "times": true,
		"days": true, "day": true, "weeks": true, "week": true, "months": true, "month": true, "o'clock": true,
		"oclock": true, "lbs": true, "lb": true, "kg": true, "stone": true, "people": true, "of": true,
	}
)

// messageClaimsUnder18 reports whether one of their messages says they are a
// minor. Talking about someone else's age ("my 16 year old") still counts as
// a hit unless it clearly is about another person.
func messageClaimsUnder18(s string) bool {
	l := foldDM(s)
	if l == "" {
		return false
	}
	if reMinorSelf.MatchString(l) || reSchool.MatchString(l) || reIAmWord.MatchString(l) {
		return true
	}
	for _, re := range []*regexp.Regexp{reNumYearsOld, reWordYears} {
		for _, loc := range re.FindAllStringIndex(l, -1) {
			start := loc[0] - 30
			if start < 0 {
				start = 0
			}
			if !reSomeoneElse.MatchString(l[start:loc[0]]) {
				return true
			}
		}
	}
	for _, re := range []*regexp.Regexp{reNumSex, reSexNum, reNumSlashSex, reAgeNum, reNumHere} {
		if re.MatchString(l) {
			return true
		}
	}
	if n, ok := nameCommaAge(l); ok && n < 18 {
		return true
	}
	for _, m := range reIAmNum.FindAllStringSubmatch(l, -1) {
		n, _ := strconv.Atoi(m[1])
		if n <= 0 || n >= 18 {
			continue
		}
		if unitAfterAge[strings.Trim(m[2], ".,!?")] {
			continue
		}
		return true
	}
	return false
}

var (
	anyNumRe = regexp.MustCompile(`\b(\d{1,3})\b`)
	// "jake, 15", "im dave, 31", "my name is mark - 40"
	reNameCommaAge = regexp.MustCompile(`(?:^|\b(?:im|i'm|i am|my name is|names|name's|its|it's)\s+)[a-z]{2,15}\s*[,.\-&]\s*(?:and\s*)?(\d{1,2})\b`)
)

// answersAgeQuestion reads their reply to "how old r u". Any number in it
// counts; if one is under 18 that is the answer, so "im dave, 16" blocks.
func answersAgeQuestion(prevSelf, theirs string) (int, bool) {
	if !askedAgeIn(prevSelf) {
		return 0, false
	}
	age := 0
	for _, m := range anyNumRe.FindAllStringSubmatch(foldDM(theirs), -1) {
		n, _ := strconv.Atoi(m[1])
		if n <= 0 || n > 99 {
			continue
		}
		if n < 18 {
			return n, true
		}
		if age == 0 {
			age = n
		}
	}
	return age, age > 0
}

func nameCommaAge(l string) (int, bool) {
	m := reNameCommaAge.FindStringSubmatch(l)
	if m == nil {
		return 0, false
	}
	n, _ := strconv.Atoi(m[1])
	return n, n > 0
}

func askedAgeIn(selfText string) bool {
	l := foldDM(selfText)
	return looksLikeAgeQuestion(l) || strings.Contains(l, "how old r u") || strings.Contains(l, "how old are u") || strings.Contains(l, "asl")
}

// theyClaimUnderage: any inbound message saying they are under 18 — hard stop.
func theyClaimUnderage(history []ConvMessage) bool {
	return underageEvidence(history) != ""
}

// underageEvidence returns the first message of theirs that says they are
// under 18, or "".
func underageEvidence(history []ConvMessage) string {
	lastSelf := ""
	for _, m := range history {
		if m.FromSelf {
			lastSelf = m.Text
			continue
		}
		if messageClaimsUnder18(m.Text) {
			return m.Text
		}
		if n, ok := answersAgeQuestion(lastSelf, m.Text); ok && n < 18 {
			return m.Text
		}
	}
	return ""
}

var (
	reAdultIAm      = regexp.MustCompile(`\b(?:i\s*am|i'm|im|i\s*m)\s*(?:only\s*|just\s*)?(\d{2})\b\s*([a-z'"%]*)`)
	reAdultYearsOld = regexp.MustCompile(`\b(\d{2})\s*(?:years?\s*old|yrs?\s*old|yr\s*old|y\s*/\s*o|y\.o\.?|yo)\b`)
	reAdultNumSex   = regexp.MustCompile(`\b(\d{2})\s*(?:m|male|guy|f|female)\b|\b(?:m|male|f|female)\s*/?\s*(\d{2})\b`)
	reAdultAge      = regexp.MustCompile(`\bage[d:\s-]*\s*(\d{2})\b`)
)

// theirStatedAge returns the adult age they gave, if any. It never returns
// an age when anything in the chat says they are under 18.
func theirStatedAge(history []ConvMessage) (int, bool) {
	if theyClaimUnderage(history) {
		return 0, false
	}
	age := 0
	lastSelf := ""
	consider := func(n int) {
		if n >= 18 && n <= 99 {
			age = n
		}
	}
	for _, m := range history {
		if m.FromSelf {
			lastSelf = m.Text
			continue
		}
		l := foldDM(m.Text)
		for _, sm := range reAdultIAm.FindAllStringSubmatch(l, -1) {
			if !unitAfterAge[strings.Trim(sm[2], ".,!?")] {
				n, _ := strconv.Atoi(sm[1])
				consider(n)
			}
		}
		for _, re := range []*regexp.Regexp{reAdultYearsOld, reAdultAge} {
			for _, loc := range re.FindAllStringSubmatchIndex(l, -1) {
				start := loc[0] - 30
				if start < 0 {
					start = 0
				}
				if reSomeoneElse.MatchString(l[start:loc[0]]) {
					continue
				}
				n, _ := strconv.Atoi(l[loc[2]:loc[3]])
				consider(n)
			}
		}
		for _, sm := range reAdultNumSex.FindAllStringSubmatch(l, -1) {
			for _, g := range sm[1:] {
				if g != "" {
					n, _ := strconv.Atoi(g)
					consider(n)
				}
			}
		}
		if n, ok := answersAgeQuestion(lastSelf, m.Text); ok {
			consider(n)
		}
		if n, ok := nameCommaAge(l); ok {
			consider(n)
		}
	}
	return age, age >= 18
}

// confirmedAdult: they told us (now or in an earlier chat) they are 18+, and
// nothing says otherwise.
func confirmedAdult(otherName string, history []ConvMessage) bool {
	if isBlockedMinor(otherName) || theyClaimUnderage(history) {
		return false
	}
	if _, ok := theirStatedAge(history); ok {
		return true
	}
	_, ok := rememberedAdultAge(otherName)
	return ok
}

func ageAsksSoFar(history []ConvMessage) (asks int, theirRepliesSinceLast int) {
	for _, m := range history {
		if m.FromSelf {
			if askedAgeIn(m.Text) {
				asks++
				theirRepliesSinceLast = 0
			}
			continue
		}
		theirRepliesSinceLast++
	}
	return asks, theirRepliesSinceLast
}

func shouldAskTheirAge(otherName string, history []ConvMessage) bool {
	if confirmedAdult(otherName, history) {
		return false
	}
	if zoeyReplyTurns(history) < askAgeFromZoeyTur {
		return false
	}
	asks, since := ageAsksSoFar(history)
	if asks >= maxAgeAsks {
		return false
	}
	return asks == 0 || since >= 2
}

var reAskBubble = regexp.MustCompile(`\?\s*$|^(?:whats|what's|what|how|hows|how's|do u|do you|did u|did you|are u|are you|r u|wbu|hbu|u ever|you ever|which|where|who|why)\b`)

func withAgeAsk(text string, alsoName bool, asksSoFar int) string {
	ask := "how old r u btw :3"
	if alsoName {
		ask = "whats ur name and how old r u btw :3"
	}
	if asksSoFar > 0 {
		ask = "wait u never said how old u are lol"
	}
	var keep []string
	for _, b := range splitDMBubbles(text) {
		l := foldDM(b)
		if askedAgeIn(b) || (alsoName && (strings.Contains(l, "ur name") || strings.Contains(l, "your name"))) {
			continue
		}
		keep = append(keep, b)
	}
	var statements []string
	for _, b := range keep {
		if !reAskBubble.MatchString(foldDM(b)) {
			statements = append(statements, b)
		}
	}
	if len(statements) > 0 {
		keep = statements
	}
	if len(keep) >= 3 {
		keep = keep[:2]
	}
	return strings.TrimSpace(strings.Join(append(keep, ask), "\n"))
}

var (
	// Clearly sexual: one hit anywhere in the recent chat keeps it gated.
	reSexualDM = regexp.MustCompile(`\b(?:sex|sexy|sexual|horny|naughty|nudes?|naked|dick|cock|pussy|boobs?|tits|titties|cum|cumming|kinky?|kinkiest|kinks|fetish\w*|nsfw|porn|fuck\s*me|wanna\s*fuck|make\s*love|making\s*love|self\s*play|play(?:ing)?\s*with\s*my\s*self|touch(?:ing)?\s*my\s*self|jerk(?:ing)?\s*off|masturbat\w*|moan\w*|lust\w*|spic(?:y|ier|iest)|turned\s*on|turn\s*me\s*on|hard\s*for|wet\s*for|so\s*wet|undress\w*|stripp?\w*|lingerie|thong|panties|onlyfans|only\s*fans|fansly|booty` +
		`|nipples?|lick\w*|suck\w*|daddy|mommy|spank\w*|chok(?:e|ing)\s*(?:you|u|me)|grind\w*|ride\s*(?:you|u|me)|bend\s*(?:you|u|me)|hold\s*(?:you|u|me)\s*close|what\s*(?:are\s*)?(?:you|u)\s*wearing|pics?\s*of\s*(?:you|u)|send\s*(?:me\s*)?(?:a\s*)?(?:pic|nude)` +
		`|on\s*(?:ur|your|my)\s*knees|kneel\w*|beg\s*for|good\s*(?:little\s*)?girl|bad\s*girl|little\s*girl|bratty|ddlg|dd\s*/\s*lg|daddy\s*/\s*lg|submissive|obey|punish\w*|dream(?:t|ed|ing)?\s*(?:of|about)\s*(?:you|u)|bdsm|degrad\w*|collar|leash|roleplay|role\s*play|sext\w*|cnc|free\s*use|in\s*bed\s*with)\b`)
	// Fine in a normal chat, but Zoey should not say them to someone whose age
	// we do not know.
	reSuggestiveSelf = regexp.MustCompile(`\b(?:ass|thick|curves|waist|hips|body|admire\s*more|the\s*good\s*stuff|not\s*(?:really\s*)?on\s*tumblr|other\s*site|link|pinned|bite|biting|teas\w*|squirm\w*|kiss\w*|melt\w*|tongue|lips|chest|bra|on\s*top|wrapped\s*around|shower|bath|in\s*bed|toes|feet|foot|piggies|proof|explore|cuddl\w*|seductive|tempting|mischief|whisper\w*|wild|dangerous(?:ly)?|kneel\w*|knees|brat\w*|pics?|photos?|send\s*u)\b`)
	ageDeflections   = []string{"lol slow down there :P", "hehe easy tiger :P", "omg ur so forward lol"}
)

func looksSexualDM(s string) bool {
	return reSexualDM.MatchString(foldDM(s))
}

func zoeyBubbleTooSuggestive(s string) bool {
	return looksSexualDM(s) || reSuggestiveSelf.MatchString(foldDM(s)) || looksLikeKinkOrSexual(s)
}

// theyWentSexual: any of the last few messages on either side is sexual — the
// chat stays flagged even if the newest line on its own sounds tame.
func theyWentSexual(history []ConvMessage) bool {
	for i, seen := len(history)-1, 0; i >= 0 && seen < 10; i, seen = i-1, seen+1 {
		if looksSexualDM(history[i].Text) {
			return true
		}
	}
	return false
}

// gateReplyOnAge runs last on every outgoing reply. Until they have said they
// are 18+, nothing sexual, no pin nudge and no link goes out, and we ask their
// age. If they turn sexual first we deflect and ask, even past maxAgeAsks.
func gateReplyOnAge(accountID, otherName string, history []ConvMessage, text string) string {
	if confirmedAdult(otherName, history) {
		return text
	}
	var keep []string
	for _, b := range splitDMBubbles(text) {
		if hasBeaconsLink(b) || looksLikePinNudge(b) || zoeyBubbleTooSuggestive(b) {
			continue
		}
		keep = append(keep, b)
	}
	asks, _ := ageAsksSoFar(history)
	needName := theirFirstName(history, otherName) == "" && !weAlreadyAskedName(history)
	if theyWentSexual(history) {
		if asks > maxAgeAsks {
			return ""
		}
		return withAgeAsk(ageDeflections[len(history)%len(ageDeflections)], needName, asks)
	}
	text = strings.Join(keep, "\n")
	if shouldAskTheirAge(otherName, history) {
		text = withAgeAsk(text, needName, asks)
	}
	return text
}

// --- their blog bio ---

var (
	reBioTeenToken = regexp.MustCompile(`(?:^|[\s|•·/,~\-—])` + teenNum + `\s*(?:$|[|•·/,~\-—]|\s*(?:he|she|they|it|y\s*/\s*o|yo|yrs|m\b|f\b|♂|♀))`)
	reBioMinorWord = regexp.MustCompile(`(?:^|[\s|•·/,~\-—(])minor(?:$|[\s|•·/,~\-—)])`)
	reBioBornYear  = regexp.MustCompile(`\b(?:born\s*(?:in\s*)?|b\.\s*)(20\d\d)\b`)
	adultOnlyBits  = []string{"minors dni", "minors do not interact", "minors don't interact", "minors dont interact",
		"dni minors", "dni if minor", "dni if you're a minor", "dni if ur a minor", "no minors", "minors block",
		"minors fuck off", "minors go away", "minors stay away", "minors please leave", "minors leave", "not for minors"}
)

// Bios that keep minors out ("MDNI", "no one under 18", "if you're under 18
// leave", "not a minor") belong to safety-minded adults, so those rule
// phrases are cut out before the generic "under 18 / minor" checks run.
var (
	reBioSelfMinor = regexp.MustCompile(`\b(?:i'?m|im|i\s*am)\s*(?:a\s*|still\s*a\s*|only\s*a\s*|just\s*a\s*)(?:teen(?:ager)?|minor|kid)\b` +
		`|\b(?:i'?m|im|i\s*am)\s*(?:still\s*|only\s*|just\s*)?(?:underage|under\s*18)\b|\b(?:ran|run)\s*by\s*a\s*minor\b|\bminor\s*here\b`)
	reBioTeenAnd  = regexp.MustCompile(`(?:^|[\s|•·/,~\-—])` + teenNum + `\s+and\s+[a-z]`)
	bioMinorRules = []*regexp.Regexp{
		regexp.MustCompile(`\bm\s*dni\b|\bmdni\b`),
		regexp.MustCompile(`\bminors?\b[^|.!?\n]{0,30}?\bd[ni]n?i\b`),
		regexp.MustCompile(`\bd[ni]n?i\b[^|.!?\n]{0,20}?\b(?:minors?|under\s*(?:age|18|21)|underage)\b`),
		regexp.MustCompile(`\bif\s+(?:you|u|ur|your|you're|youre|yourself)\b[^|.!?\n]{0,30}?\b(?:under\s*(?:the\s*age\s*of\s*)?(?:age|18|21|eighteen)|underage|minors?|not\s*(?:18|21)|younger\s*than\s*18)\b`),
		regexp.MustCompile(`\bif\s+(?:not|under)\s*(?:18|21)\b`),
		regexp.MustCompile(`\b(?:no|any|anyone|nobody|noone|no\s*one|people|pups|kids)\s*(?:one\s*)?under\s*(?:the\s*age\s*of\s*)?(?:18|21)(?:'?s)?\b`),
		regexp.MustCompile(`\bunder\s*(?:18|21|age)(?:'?s)?\b[\s,.!:;)\-]*(?:no\b|bye|go\b|get\b|gtfo|leave|dni|do\s*not|don'?t|dont|stay|will\b|kindly|please|pls|not\s*allowed|f+\s*off|fuck|block|move|keep)`),
		regexp.MustCompile(`\bnot\s*(?:18|21)\b[\s,.!:;\-]*(?:go\b|get\b|leave|stay|gtfo|or\s*(?:older|over))`),
		regexp.MustCompile(`\b(?:suitable|viewed|for|allowed)\s+(?:by\s+|for\s+)?(?:anyone\s+|anybody\s+)?(?:under|below)\s*(?:the\s*age\s*of\s*)?(?:18|21)\b`),
		regexp.MustCompile(`\bnot\s+(?:a\s+)?(?:[a-z]+\s+){0,2}?minors?\b`),
		regexp.MustCompile(`\bno\s+minors?\b|\bminors?\s+(?:are\s+)?(?:not\s+)?(?:allowed|authori[sz]ed|welcome|will|do\b|don'?t|dont|stay|go\b|leave|please|pls|fuck|gtfo|keep|get|block|should)`),
		regexp.MustCompile(`\b(?:condone|tolerate|allow)\s+(?:any\s+)?minors?\b|\binto\s+underage\b|\b(?:you|yourself)\b[^|.!?\n]{0,40}?\bunderage\b`),
	}
)

func stripBioMinorRules(blob string) string {
	for _, bit := range adultOnlyBits {
		blob = strings.ReplaceAll(blob, bit, " ")
	}
	for _, re := range bioMinorRules {
		blob = re.ReplaceAllString(blob, " ")
	}
	return blob
}

func bioSuggestsMinor(p BlogProfile) bool {
	raw := foldDM(strings.Join([]string{p.Title, p.Description}, " | "))
	if reBioSelfMinor.MatchString(raw) || reBioTeenToken.MatchString(raw) || reBioTeenAnd.MatchString(raw) {
		return true
	}
	blob := stripBioMinorRules(raw)
	if messageClaimsUnder18(blob) || reBioTeenToken.MatchString(blob) || reBioMinorWord.MatchString(blob) {
		return true
	}
	if m := reBioBornYear.FindStringSubmatch(blob); m != nil {
		y, _ := strconv.Atoi(m[1])
		if y > time.Now().Year()-18 {
			return true
		}
	}
	return false
}

// checkMinorBio looks at their blog once a week and blocks them if the bio
// says they are under 18. Returns true when they are (or already were) blocked.
func checkMinorBio(client *http.Client, auth Auth, blog, account string) bool {
	if isBlockedMinor(blog) {
		return true
	}
	k := blogKey(blog)
	ageGateMu.Lock()
	seen := loadJSONMap[time.Time](bioCheckedFile)
	last, ok := seen[k]
	ageGateMu.Unlock()
	if ok && time.Since(last) < bioRecheckAfter {
		return false
	}
	p, err := fetchBlogProfile(client, auth, blog)
	if err != nil {
		return false
	}
	ageGateMu.Lock()
	seen = loadJSONMap[time.Time](bioCheckedFile)
	seen[k] = time.Now()
	saveJSONMap(bioCheckedFile, seen)
	ageGateMu.Unlock()
	if bioSuggestsMinor(p) {
		blockMinor(blog, account, fmt.Sprintf("bio says under 18: %q", strings.TrimSpace(p.Title+" | "+p.Description)))
		return true
	}
	return false
}

func minorBlockSummary() string {
	ageGateMu.Lock()
	defer ageGateMu.Unlock()
	return fmt.Sprintf("%d blocked as under 18", len(loadJSONMap[minorRecord](minorsFile)))
}
