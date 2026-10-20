package main

import (
	"fmt"
	"os"
	"strings"
	"testing"
	"time"
)

func TestTumblrConversationsBeforeCursorUsesSeconds(t *testing.T) {
	if got := tumblrConversationsBeforeCursor("1790112639629"); got != "1790112639" {
		t.Fatalf("ms→sec: %q", got)
	}
	if got := tumblrConversationsBeforeCursor("1790112639"); got != "1790112639" {
		t.Fatalf("sec passthrough: %q", got)
	}
	if got := tumblrConversationsBeforeCursor("  "); got != "" {
		t.Fatalf("empty: %q", got)
	}
}

func TestParseConversationsBodyReadsLinksNextBefore(t *testing.T) {
	body := []byte(`{"response":{"conversations":[{"id":"c1","canSend":true,"unreadMessagesCount":1,"participants":[{"name":"them","uuid":"t:other"},{"name":"me","uuid":"t:me"}],"messages":{"data":[{"ts":"1790112639629","participant":"t:other","message":"hi"}]}}],"links":{"next":{"href":"/v2/conversations?before=1790112639&limit=20","method":"GET","queryParams":{"before":"1790112639","limit":"20"}}}}}`)
	convs, next, err := parseConversationsBodyWithCursor(body, Auth{Blog: "t:me"})
	if err != nil {
		t.Fatal(err)
	}
	if len(convs) != 1 || convs[0].OtherName != "them" {
		t.Fatalf("%+v", convs)
	}
	if next != "1790112639" {
		t.Fatalf("next cursor=%q", next)
	}
}

func TestMediaPlaceholderFromType(t *testing.T) {
	if got := mediaPlaceholderFromType("IMAGE"); got != "(image)" {
		t.Fatalf("IMAGE: %q", got)
	}
	if got := mediaPlaceholderFromType("GIF"); got != "(gif)" {
		t.Fatalf("GIF: %q", got)
	}
	if got := mediaPlaceholderFromType("VIDEO"); got != "(video)" {
		t.Fatalf("VIDEO: %q", got)
	}
	if got := mediaPlaceholderFromType("TEXT"); got != "(non-text)" {
		t.Fatalf("TEXT empty-path should still yield non-text placeholder: %q", got)
	}
	m := map[string]any{"type": "IMAGE"}
	if got := mediaPlaceholderFromMessage(m); got != "(image)" {
		t.Fatalf("message map: %q", got)
	}
	m2 := map[string]any{"content": map[string]any{"image": map[string]any{"url": "x"}}}
	if got := mediaPlaceholderFromMessage(m2); got != "(image)" {
		t.Fatalf("content.image: %q", got)
	}
}

func TestConversationMessagesURLMarksReadWhenNotPreserving(t *testing.T) {
	auth := Auth{Blog: "t:abc"}
	keep := conversationMessagesURL(auth, "conv1", 20, true, "")
	if !strings.Contains(keep, "preserve_last_read_ts=true") {
		t.Fatalf("preserve true: %s", keep)
	}
	open := conversationMessagesURL(auth, "conv1", 20, false, "")
	if strings.Contains(open, "preserve_last_read_ts") {
		t.Fatalf("opening a chat should omit preserve_last_read_ts: %s", open)
	}
	if !strings.Contains(open, "conversation_id=conv1") {
		t.Fatalf("missing id: %s", open)
	}
	paged := conversationMessagesURL(auth, "conv1", 100, true, "1700000000")
	if !strings.Contains(paged, "before=1700000000") || !strings.Contains(paged, "limit=100") {
		t.Fatalf("paging params missing: %s", paged)
	}
}

func TestZoeyReplyTurnsUndercountsWhenHistoryTruncated(t *testing.T) {
	// Mimic a long spicy work-session chat: each exchange is them×8 + zoey×2 = 10 bubbles.
	// A single 80-message fetch only sees ~8 zoey sets and undercounts the real total.
	var full []ConvMessage
	for i := 0; i < 25; i++ {
		for j := 0; j < 8; j++ {
			full = append(full, ConvMessage{FromSelf: false, Text: fmt.Sprintf("youre so hot them %d-%d", i, j), TS: fmt.Sprintf("%06d", i*10+j)})
		}
		full = append(full,
			ConvMessage{FromSelf: true, Text: fmt.Sprintf("zoey a %d", i), TS: fmt.Sprintf("%06d", i*10+8)},
			ConvMessage{FromSelf: true, Text: fmt.Sprintf("zoey b %d", i), TS: fmt.Sprintf("%06d", i*10+9)},
		)
	}
	if got := zoeyReplyTurns(full); got != 25 {
		t.Fatalf("full history turns=%d want 25", got)
	}
	const window = 80
	if len(full) <= window {
		t.Fatalf("fixture too short: %d", len(full))
	}
	truncated := full[len(full)-window:]
	got := zoeyReplyTurns(truncated)
	if got >= zoeyReplyTurns(full) {
		t.Fatalf("80-message window should undercount, got %d (full had %d)", got, zoeyReplyTurns(full))
	}
	t.Logf("confirmed: 80-message window only sees %d zoey sets (full had 25)", got)
	if !shouldSendBeacons(full, "") {
		t.Fatal("full long history must funnel")
	}
	// truncated still has ~8 sets — enough to funnel at >=5, which is why we page full history
	if got < funnelAfterZoeyReplies {
		t.Fatalf("fixture expected truncated >= funnel threshold, got %d", got)
	}
}

func TestFetchAllConversationMessagesUsedForCounting(t *testing.T) {
	src, err := os.ReadFile("messages.go")
	if err != nil {
		t.Fatal(err)
	}
	body := string(src)
	if !strings.Contains(body, "fetchAllConversationMessages") {
		t.Fatal("inbox replies must page the full DM history for set counting")
	}
	if !strings.Contains(body, `q.Set("before"`) {
		t.Fatal("message fetch must support before= pagination")
	}
	if !strings.Contains(body, "listAllConversations") {
		t.Fatal("inbox must page through all conversations, not one batch")
	}
	if !strings.Contains(body, "runMessagesReplyParallel") && !strings.Contains(body, "runMessagesReplyRoundRobin") {
		t.Fatal("multi-account messaging must run accounts in parallel")
	}
	if strings.Contains(body, `pinMessagingBlog(client, &auth, "zoeyofc")`) {
		t.Fatal("must not hardcode zoeyofc — pin each account's assigned Zoey blog")
	}
	if repliesCapReached(5, 0) {
		t.Fatal("maxReplies 0 must mean unlimited")
	}
	if !repliesCapReached(5, 5) || repliesCapReached(4, 5) {
		t.Fatal("positive maxReplies must cap")
	}
}

func TestPreferredMessagingBlogUsesAssignedName(t *testing.T) {
	cases := []struct {
		id, want string
	}{
		{"assistantiism", "spacezoey"},
		{"mohamedaarabe3", "lillyhaze"},
		{"583943680", "cosmiczoeyy"},
	}
	for _, tc := range cases {
		if assignedBlogName(tc.id) != tc.want {
			t.Skipf("assigned-usernames.json missing %s → %s", tc.id, tc.want)
		}
		if got := preferredMessagingBlog(tc.id, "other"); got != tc.want {
			t.Fatalf("%s: want %s, got %q", tc.id, tc.want, got)
		}
	}
	if got := preferredMessagingBlog("", "fallbackblog"); got != "fallbackblog" {
		t.Fatalf("synced fallback: %q", got)
	}
}

func TestLatestMessageUsesNewestTimestamp(t *testing.T) {
	in := []ConvMessage{
		{Text: "old them", TS: "100", FromSelf: false},
		{Text: "new us", TS: "200", FromSelf: true},
		{Text: "mid them", TS: "150", FromSelf: false},
	}
	got, ok := latestMessage(in)
	if !ok || !got.FromSelf || got.Text != "new us" {
		t.Fatalf("%+v", got)
	}
}

func TestOrderHistoryOldestFirstUsesTimestamps(t *testing.T) {
	in := []ConvMessage{
		{Text: "new", TS: "200", FromSelf: true},
		{Text: "old", TS: "100", FromSelf: false},
	}
	out := orderHistoryOldestFirst(in)
	if out[0].Text != "old" || out[1].Text != "new" {
		t.Fatalf("%+v", out)
	}
}

func TestWrapDMLines(t *testing.T) {
	got := wrapDMLines("hey this is a reasonably long tumblr dm that should wrap", 20)
	if len(got) < 2 {
		t.Fatalf("expected wrap, got %#v", got)
	}
	if wrapDMLines("hi", 40)[0] != "hi" {
		t.Fatal("short line")
	}
}

func TestMessagesWatchDefaults(t *testing.T) {
	prevDry, prevOnce, prevHours := messagesDryRun, messagesWatchOnce, messagesWatchHours
	prevUntil := sessionUntil
	t.Cleanup(func() {
		messagesDryRun, messagesWatchOnce, messagesWatchHours = prevDry, prevOnce, prevHours
		sessionUntil = prevUntil
	})

	messagesDryRun = false
	messagesWatchOnce = false
	messagesWatchHours = 0
	sessionUntil = time.Time{}
	if !messagesShouldWatch() {
		t.Fatal("live send should watch by default")
	}
	d := messagesWatchDeadline(true)
	if d.IsZero() || d.Before(time.Now().Add(8*time.Hour)) {
		t.Fatalf("default watch should be ~9h, got %v", d)
	}

	messagesDryRun = true
	if messagesShouldWatch() {
		t.Fatal("dry run without hours should be one-shot")
	}

	messagesWatchHours = 3
	if !messagesShouldWatch() {
		t.Fatal("dry run with watch hours should still watch")
	}

	messagesWatchOnce = true
	if messagesShouldWatch() {
		t.Fatal("-once must disable watch")
	}

	cfg := defaultMessageConfig()
	idle := messagesWatchIdle(cfg, 0)
	if idle < 2*time.Minute {
		t.Fatalf("quiet idle too short: %s", idle)
	}
	busy := messagesWatchIdle(cfg, 2)
	if busy > 3*time.Minute {
		t.Fatalf("after replies idle should be shorter: %s", busy)
	}
	if messagesIdleReachFor != 5*time.Minute {
		t.Fatalf("idle reach should be 5m, got %s", messagesIdleReachFor)
	}
}

func TestIsEmailVerifyRequired(t *testing.T) {
	err := fmt.Errorf(`403 Forbidden: {"meta":{"status":403},"errors":[{"code":7001,"detail":"Requesting user must verify their email address before starting new conversations"}]}`)
	if !isEmailVerifyRequired(err) {
		t.Fatal("expected email-verify detection")
	}
	if isEmailVerifyRequired(fmt.Errorf("403 Forbidden: soft csrf")) {
		t.Fatal("normal forbidden should not count as email verify")
	}
}

func TestMessagesWatchLoopInSource(t *testing.T) {
	src, err := os.ReadFile("messages.go")
	if err != nil {
		t.Fatal(err)
	}
	body := string(src)
	if !strings.Contains(body, "Watching for new DMs until") {
		t.Fatal("messages reply must keep watching for new DMs")
	}
	if !strings.Contains(body, "Inbox check #") {
		t.Fatal("watch loop must re-check the inbox")
	}
	if !strings.Contains(body, "noteDMReplySet") {
		t.Fatal("must count reply sets per person")
	}
	if !strings.Contains(body, "shouldStopReplying") || !strings.Contains(body, "post-beacons follow-up") {
		t.Fatal("must allow one reply after beacons then stop")
	}
	if !strings.Contains(body, "fetchAllConversationMessages") {
		t.Fatal("must load full conversation history when counting sets")
	}
	if !strings.Contains(body, "isEmailVerifyRequired") {
		t.Fatal("must detect unverified-email DM blocks")
	}
	if !strings.Contains(body, "recoverSession") || !strings.Contains(body, "quietChromeLogin = true") {
		t.Fatal("messaging must recover cookies like reach (quiet Chrome + recoverSession)")
	}
	if !strings.Contains(body, "retryAfterCookie") {
		t.Fatal("DM sends must retryAfterCookie on session death")
	}
	if !strings.Contains(body, "Will retry cookie refresh after") {
		t.Fatal("watch must keep going when cookie refresh fails once")
	}
	if !strings.Contains(body, "runMessagesIdleReach") || !strings.Contains(body, "messagesIdleReachFor") {
		t.Fatal("quiet inbox must run a short reach burst then recheck DMs")
	}
	if !strings.Contains(body, "sleepOrReachBetweenChats") {
		t.Fatal("between-chat wait must do a short reach burst")
	}
	if !strings.Contains(body, "warmMessagesInbox") || !strings.Contains(body, "www.tumblr.com/messages") {
		t.Fatal("must warm tumblr.com/messages before listing inbox")
	}
	if !strings.Contains(body, `q.Set("before"`) || !strings.Contains(body, "conversationActivityTS") {
		t.Fatal("inbox must page with before=timestamp, not only the first 20")
	}
	if !strings.Contains(body, "needsReply") || !strings.Contains(body, "c.Unread > 0") {
		t.Fatal("unread badge must force a reply attempt")
	}
	if strings.Contains(body, "if c.LastMessageFromSelf && !follow {\n\t\t\ttheyLast = false") {
		t.Fatal("must not let inbox preview override full history theyLast")
	}
	html, err := os.ReadFile("gui_html.go")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(html), `id="msgWatch"`) || !strings.Contains(string(html), `id="msgHours"`) {
		t.Fatal("GUI must expose watch controls")
	}
}
