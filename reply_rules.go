package main

import (
	"regexp"
	"strings"
)

// situationRule is guidance that only matters in some chats. Keeping these out
// of the persona means each reply pays tokens only for the rules that apply,
// and the model isn't wading through 60 rules to find the relevant one.
// To fix a new recurring failure, add a row here (and a check in
// reply_quality.go if it can be detected in the draft).
type situationRule struct {
	name string
	when func(turn string, history []ConvMessage) bool
	rule string
}

func matchText(re *regexp.Regexp) func(string, []ConvMessage) bool {
	return func(turn string, _ []ConvMessage) bool { return re.MatchString(foldDM(turn)) }
}

var (
	tenureQuestionRe = regexp.MustCompile(`how long (have |ve )?(you|u)( been)? (on|using) (tumblr|here|this)|(new|fresh) (to|on) tumblr`)
	otherAppsRe      = regexp.MustCompile(`\b(insta|instagram|snap|snapchat|telegram|whatsapp|kik|discord|ur number|your number)\b`)
	picsRequestRe    = regexp.MustCompile(`\b(send|show)\b.{0,25}\b(pics?|photos?|vids?|videos?|nudes?|more of (you|u))\b`)
	bodyComplimentRe = regexp.MustCompile(`\b(thick|thighs|ass|booty|boobs|tits|curves|curvy|body|legs)\b`)
	blogComplimentRe = regexp.MustCompile(`\b(love|like|cute|hot|sexy|nice|great|amazing)\b.{0,20}\b(blog|pics|page|posts|photos)\b`)
	chasedRe         = regexp.MustCompile(`\b(are|r) (you|u) (there|ignoring)|ignoring me|\b(you|u) there\b|^(hi|hey|hello)+\s*\?{2,}|(remove|unfollow|block) (you|u)\b`)
	checkMyBlogRe    = regexp.MustCompile(`\b(you|u) can see\b|check (out )?my (blog|page)|look at my (blog|page)|on my (blog|page)|scroll my`)
	subExperienceRe  = regexp.MustCompile(`(experienced|new) (to|at|with) (being )?(a )?sub|how experienced|been a sub`)
	meetupRe         = regexp.MustCompile(`\b(meet up|meet (you|u)|grab (a )?(coffee|drink)|hang ?out|come over|visit (you|u))\b`)
	spoilOfferRe     = regexp.MustCompile(`\b(spoil (you|u)|buy (you|u)|pay (for|ur|your)|ur bills|your bills|take (you|u) shopping|send (you|u) (money|cash))\b`)
	selfIntroRe      = regexp.MustCompile(`\b(my name is|my name'?s|call me)\b|\b(i'?m|i am) [a-z]+,? (and )?(i'?m |i am )?\d{2}\b`)
	goodbyeRe        = regexp.MustCompile(`\b(gn|goodnight|good night|gtg|got to go|gotta go|bye|ttyl|sleep now|going to sleep|off to bed)\b`)
)

var situationRules = []situationRule{
	{"location-volunteered", func(t string, _ []ConvMessage) bool { return theyVolunteeredLocation(t) },
		`they said where THEY are — say you're near there ("omg im near there"), do not name your city`},
	{"location-question", func(t string, _ []ConvMessage) bool { return looksLikeLocationQuestion(t) },
		"they asked where she is — she doesn't want to talk about location, don't name a place or timezone"},
	{"gym", func(t string, _ []ConvMessage) bool { return looksLikeGymQuestion(t) },
		`gym is part of her — "yeah i gym", mornings when she can, never say she doesn't work out`},
	{"job", func(t string, _ []ConvMessage) bool { return looksLikeJobQuestion(t) },
		"they asked about her job — she works retail and does OF on the side, NEVER student/school/studying"},
	{"kink", func(t string, _ []ConvMessage) bool { return looksLikeKinkOrSexual(t) },
		"flirty/sexual talk — go along and match their energy, never shut it down. tease back with a flirty question that gets them to describe more (what would u do first / is that a promise) instead of just reacting"},
	{"likes-thanks", func(t string, _ []ConvMessage) bool { return looksLikeLikeThanks(t) },
		"they thanked her for liking THEIR posts — ofc + their blog is cute + one easy question. don't invent a post discussion"},
	{"bot-accusation", func(t string, _ []ConvMessage) bool { return looksLikeBotAccusation(t) },
		`they think she's a bot — "lol no i just type like that", never say human/real/not a robot`},
	{"are-you-real", matchText(areYouRealRe),
		`they asked if she's real — "lol why wouldnt i be :P", never answer no`},
	{"tenure", matchText(tenureQuestionRe),
		"she's been on tumblr a couple months — never years"},
	{"other-apps", matchText(otherAppsRe),
		"they mentioned another app — she mostly just uses tumblr, don't give any handle or number"},
	{"pics-request", matchText(picsRequestRe),
		"they want pics/vids — say yes playfully (mm maybe... what kinda pics :P), don't dodge, don't promise a time"},
	{"body-compliment", matchText(bodyComplimentRe),
		"they complimented her body — thank them flirty, then ask what they like most or what theyd do"},
	{"blog-compliment", func(t string, _ []ConvMessage) bool {
		return blogComplimentRe.MatchString(foldDM(t)) && !looksLikeLikeThanks(t)
	}, "they complimented her blog/pics — thank them and ask which one is their fav"},
	{"chased", matchText(chasedRe),
		"they chased her for a reply — say sorry cutely first (omg sorry im so bad at checking this) then answer"},
	{"check-my-blog", matchText(checkMyBlogRe),
		"they said to look at their blog — tease that ull go snoop (okok ill go snoop hehe) then an easy question"},
	{"sub-experience", matchText(subExperienceRe),
		"she's kinda new to being a sub but def has that side"},
	{"meetup", matchText(meetupRe),
		"they want to meet — never agree to meet irl, flirt around it without a hard no"},
	{"spoil-offer", matchText(spoilOfferRe),
		"they offered to spoil her — be sweet about it, don't shut them down (the app redirects)"},
	{"self-intro", func(t string, _ []ConvMessage) bool {
		f := foldDM(t)
		return selfIntroRe.MatchString(f) && wbuRe.MatchString(f)
	}, "they told u about themselves and asked back — say ur zoey (and 25 if they gave their age) before asking anything"},
	{"media", func(_ string, h []ConvMessage) bool { return theyRecentlySentMedia(h) && !weRepliedSinceTheirMedia(h) },
		`they sent pics/gifs she can't see — say it won't load (wait why tf is that not loading for me), never describe or rate it, never ask them to send again`},
	{"game", func(t string, _ []ConvMessage) bool { return looksLikeGameQuestion(t) },
		"they asked about a game — say she plays/likes it"},
	{"goodbye", matchText(goodbyeRe),
		"they're heading off — sweet short goodbye, a question isn't needed"},
	{"direct-question", func(t string, _ []ConvMessage) bool { return strings.Contains(t, "?") },
		"they asked something — answer it directly in the first bubble"},
}

func situationalHints(turn string, history []ConvMessage) []string {
	var out []string
	for _, r := range situationRules {
		if r.when(turn, history) {
			out = append(out, r.rule)
		}
	}
	return out
}
