package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"math/rand"
	"net/http"
	"net/url"
	"os"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

type MessageConfig struct {
	PinnedMention      string   `json:"pinnedMention"`
	PinnedMentionAfter int      `json:"pinnedMentionAfter"`
	Enabled            bool     `json:"enabled"`
	MaxReplies         int      `json:"maxReplies"`
	DelaySeconds       int      `json:"delaySeconds"`
	DelayJitterSeconds int      `json:"delayJitterSeconds"`
	OnlyUnread         bool     `json:"onlyUnread"`
	DryRun             bool     `json:"dryRun"`
	WatchIdleSeconds   int      `json:"watchIdleSeconds"`
	WatchIdleJitter    int      `json:"watchIdleJitterSeconds"`
	ReplyTemplates     []string `json:"replyTemplates"`
	ColdOpenEnabled    bool     `json:"coldOpenEnabled"`
	ColdOpenMax        int      `json:"coldOpenMax"`
	ColdOpenTemplates  []string `json:"coldOpenTemplates"`
	AI                 AIConfig `json:"ai"`
}

var (
	messagesDryRun     bool
	messagesWatchOnce  bool
	messagesWatchHours float64
	messageUILog       func(string)
)

func mlogf(format string, args ...any) {
	s := strings.TrimRight(fmt.Sprintf(format, args...), "\n")
	if messageUILog != nil {
		for _, line := range strings.Split(s, "\n") {
			messageUILog(line)
		}
		return
	}
	fmt.Println(s)
}

const messageConfigFile = "config/messages.json"

func defaultMessageConfig() MessageConfig {
	return MessageConfig{
		Enabled:            true,
		MaxReplies:         0, // 0 = no cap — reply to everyone who needs one
		DelaySeconds:       20,
		DelayJitterSeconds: 17,
		OnlyUnread:         false,
		WatchIdleSeconds:   120,
		WatchIdleJitter:    120,
		ReplyTemplates: []string{
			"hey! how's your day going?",
			"hi :) saw your blog, love the vibe",
			"heyy what's up",
			"hi! hope you're having a good one",
		},
		ColdOpenEnabled: false,
		ColdOpenMax:     2,
		ColdOpenTemplates: []string{
			"hey! liked your posts — how are you?",
			"hi :) your blog's cute, what's been good lately?",
		},
		AI: defaultAIConfig(),
	}
}

func loadMessageConfig() MessageConfig {
	cfg := defaultMessageConfig()
	if raw, err := os.ReadFile(messageConfigFile); err == nil {
		_ = json.Unmarshal(raw, &cfg)
	}
	// MaxReplies <= 0 means unlimited (do not clamp back to 5).
	if cfg.DelaySeconds <= 0 {
		cfg.DelaySeconds = 20
	}
	if cfg.WatchIdleSeconds <= 0 {
		cfg.WatchIdleSeconds = 120
	}
	if cfg.WatchIdleJitter < 0 {
		cfg.WatchIdleJitter = 0
	}
	if len(cfg.ReplyTemplates) == 0 {
		cfg.ReplyTemplates = defaultMessageConfig().ReplyTemplates
	}
	cfg.AI.normalize()
	return cfg
}

func repliesCapReached(replied, max int) bool {
	return max > 0 && replied >= max
}

func messagesWatchDeadline(watch bool) time.Time {
	if !watch {
		return time.Time{}
	}
	if !sessionUntil.IsZero() {
		return sessionUntil
	}
	if messagesWatchHours > 0 {
		return time.Now().Add(time.Duration(messagesWatchHours * float64(time.Hour)))
	}
	// Live send with no -for: keep going for a work day.
	return time.Now().Add(9 * time.Hour)
}

func messagesShouldWatch() bool {
	if messagesWatchOnce {
		return false
	}
	if messagesWatchHours > 0 {
		return true
	}
	if !sessionUntil.IsZero() {
		return true
	}
	// Live replies watch by default so you can leave for work.
	// Dry run stays one-shot unless hours/-for was set.
	return !messagesDryRun
}

func sleepMessagesWatch(idle time.Duration, deadline time.Time) bool {
	if idle < time.Second {
		idle = time.Second
	}
	end := time.Now().Add(idle)
	if !deadline.IsZero() && end.After(deadline) {
		end = deadline
	}
	for time.Now().Before(end) {
		if sessionExpired() {
			return false
		}
		if !deadline.IsZero() && !time.Now().Before(deadline) {
			return false
		}
		slice := 2 * time.Second
		if left := time.Until(end); left < slice {
			slice = left
		}
		if slice <= 0 {
			break
		}
		time.Sleep(slice)
	}
	return deadline.IsZero() || time.Now().Before(deadline)
}

func messagesWatchIdle(cfg MessageConfig, replied int) time.Duration {
	sec := cfg.WatchIdleSeconds
	if replied > 0 {
		sec = 45
	}
	if cfg.WatchIdleJitter > 0 {
		sec += rand.Intn(cfg.WatchIdleJitter + 1)
	}
	if sec < 20 {
		sec = 20
	}
	return time.Duration(sec) * time.Second
}

// messagesIdleReachFor is how long a quiet inbox spends on reach before DMs again.
const messagesIdleReachFor = 5 * time.Minute

// messagesBetweenChatReachMin is the shortest between-chat gap worth a reach nibble.
const messagesBetweenChatReachMin = 18

// runMessagesIdleReach does a short follow/like burst when messaging has nobody to answer.
// Uses a local stopAfter so parallel messaging accounts don't clobber the shared -for deadline.
// Returns true if a burst was attempted (caller should skip the idle sleep and recheck inbox).
func runMessagesIdleReach(acc Account, deadline time.Time) bool {
	until := time.Now().Add(messagesIdleReachFor)
	if !deadline.IsZero() && until.After(deadline) {
		until = deadline
	}
	if time.Until(until) < 45*time.Second {
		return false
	}
	label := acc.ID
	if n := assignedBlogName(acc.ID); n != "" {
		label = "@" + n
	}
	mlogf("[%s] Inbox quiet — ~%s reach, then check DMs again…", label, messagesIdleReachFor.Round(time.Minute))
	logFn := func(s string) { mlogf("%s", s) }
	// Cap follows/likes so a 5m window does useful work without trying a full session.
	if err := runFollowLikersOnUntil(acc, 8, 3, until, logFn); err != nil {
		mlogf("[%s] Idle reach: %v", label, err)
	}
	// Reach can return at once (another run holds this account's reach lock,
	// or it is not logged in); wait out the window so the loop doesn't spin.
	if left := time.Until(until); left > 0 {
		time.Sleep(left)
	}
	return true
}

// sleepOrReachBetweenChats fills the gap after a sent reply with a tiny reach burst
// on the same session, then sleeps any leftover time so pace stays similar.
func sleepOrReachBetweenChats(client *http.Client, auth *Auth, accountID string, waitSec int) {
	if waitSec < 1 {
		waitSec = 1
	}
	until := time.Now().Add(time.Duration(waitSec) * time.Second)
	if waitSec < messagesBetweenChatReachMin || accountID == "" || client == nil || auth == nil {
		time.Sleep(time.Duration(waitSec) * time.Second)
		return
	}
	release, _, ok := tryLockAccount(accountID, "reach")
	if !ok {
		time.Sleep(time.Duration(waitSec) * time.Second)
		return
	}
	defer release()
	mlogf("  …%ds between chats — short reach", waitSec)
	files := filesForAccount(accountID)
	cfg := loadFollowConfig()
	cfg.stopAfter = until
	cfg.MaxFollows = 2
	cfg.MaxEngages = 4
	cfg.LikePinned = true
	cfg.LikePostsMin = 1
	cfg.LikePostsMax = 2
	cfg.LikeExtraPosts = 1
	// Keep gather cheap so most of the gap is actual likes/follows, not seed scans.
	cfg.SeedPostsPerBlog = 1
	seeds := normalizeSeedBlogs(cfg.SeedBlogs)
	if len(seeds) > 5 {
		rand.Shuffle(len(seeds), func(i, j int) { seeds[i], seeds[j] = seeds[j], seeds[i] })
		cfg.SeedBlogs = seeds[:5]
	}
	// Skip tag search in tiny windows — seed likers/rebloggers only.
	if waitSec < 45 {
		cfg.Queries = nil
		cfg.SeedOnly = true
	}
	already, err := loadFollowedFile(files.Followed)
	if err != nil {
		already = map[string]bool{}
	}
	logFn := func(s string) { mlogf("%s", s) }
	_, _ = engageTagLikers(client, auth, cfg, already, cfg.MaxFollows, files, logFn)
	_ = saveFollowedFile(files.Followed, already)
	if left := time.Until(until); left > time.Second {
		time.Sleep(left)
	}
}

type Conversation struct {
	ID                  string
	CanSend             bool
	Unread              int
	OtherName           string
	OtherUUID           string
	LastMessageText     string
	LastMessageFromSelf bool
	Messages            []ConvMessage
}

type ConvMessage struct {
	FromSelf bool
	FromName string
	Text     string
	TS       string
}

type conversationAPI struct {
	ID                  string `json:"id"`
	CanSend             bool   `json:"canSend"`
	UnreadMessagesCount int    `json:"unreadMessagesCount"`
	Participants        []struct {
		Name  string `json:"name"`
		UUID  string `json:"uuid"`
		Admin bool   `json:"admin"`
	} `json:"participants"`
	Messages struct {
		Data []struct {
			Type        string `json:"type"`
			TS          string `json:"ts"`
			Participant string `json:"participant"`
			Message     string `json:"message"`
			Content     struct {
				Text string `json:"text"`
			} `json:"content"`
		} `json:"data"`
	} `json:"messages"`
}

func runMessages() {
	auth, err := loadAuth()
	if err != nil {
		fmt.Println("Error loading auth:", err)
		return
	}
	if err := syncCookiesFromChrome(&auth); err != nil {
		if !authHasSID(auth) {
			fmt.Println("Cookie sync failed — trying auto-login…")
			if err := loginWithRealChrome(&auth); err != nil {
				fmt.Println("Login failed:", err)
				return
			}
		}
	}
	client := &http.Client{Timeout: 45 * time.Second}
	if err := ensureSession(client, &auth); err != nil {
		fmt.Println("Session dead:", err)
		return
	}
	if err := printInbox(client, auth); err != nil {
		fmt.Println("Inbox error:", err)
	}
}

func preferredMessagingBlog(accountID, syncedName string) string {
	if n := assignedBlogName(accountID); usableBlogName(n) {
		return n
	}
	if usableBlogName(syncedName) {
		return syncedName
	}
	return strings.TrimSpace(syncedName)
}

// prepareMessagesReplySession loads auth for the active account.
func prepareMessagesReplySession() (client *http.Client, auth Auth, blogName string, cfg MessageConfig, accountID string, ok bool) {
	acc, _ := currentAccount()
	return prepareMessagesReplySessionFor(acc)
}

// prepareMessagesReplySessionFor loads auth for a specific account (parallel-safe).
func prepareMessagesReplySessionFor(acc Account) (client *http.Client, auth Auth, blogName string, cfg MessageConfig, accountID string, ok bool) {
	// Same as reach: may open Chrome quietly to mint a cookie, never steal the mouse.
	quietChromeLogin = true
	nativeChromeClicks = false

	accountID = accountSlug(acc.ID)
	var err error
	if accountID != "" {
		auth, err = loadAuthFrom(filesForAccount(accountID).AuthPath)
	} else {
		auth, err = loadAuth()
	}
	if err != nil {
		mlogf("Error loading auth: %v", err)
		return nil, Auth{}, "", MessageConfig{}, accountID, false
	}
	cfg = loadMessageConfig()
	if cfg.AI.NonExplicit && !authHasSID(auth) {
		mlogf("[%s] Skipped: no saved login session", accountID)
		return nil, Auth{}, "", MessageConfig{}, accountID, false
	}
	if !cfg.AI.NonExplicit {
		if err := syncCookiesFromChrome(&auth); err != nil {
			if !authHasSID(auth) {
				mlogf("Cookie sync failed — recovering a working cookie (like reach)…")
				client = newAuthClient(&auth)
				if err := recoverSession(client, &auth, true); err != nil {
					mlogf("Login failed: %v", err)
					return nil, Auth{}, "", MessageConfig{}, accountID, false
				}
			}
		}
	}
	client = newAuthClient(&auth)
	if err := ensureSession(client, &auth); err != nil {
		mlogf("Session dead — recovering a working cookie (like reach)…")
		if err := recoverSession(client, &auth, true); err != nil {
			mlogf("Session dead: %v", err)
			return nil, Auth{}, "", MessageConfig{}, accountID, false
		}
	}
	blogName, err = verifyAccountBlog(client, &auth)
	if err != nil {
		mlogf("[%s] Messaging blocked: %v", accountID, err)
		return nil, Auth{}, "", MessageConfig{}, accountID, false
	}
	cfg = loadMessageConfig()
	if messagesDryRun {
		cfg.DryRun = true
	}
	cfg.AI.Persona = fillAIPersona(cfg.AI.Persona, blogName)
	label := accountID
	if n := assignedBlogName(accountID); n != "" {
		label = n
	}
	if usableBlogName(blogName) {
		mlogf("[%s] Replying as Zoey / %s", label, blogName)
	} else if label != "" {
		mlogf("[%s] Messaging session ready", label)
	}
	if cfg.AI.Enabled {
		mlogf("[%s] AI replies on (%s / %s)", label, cfg.AI.Provider, cfg.AI.Model)
	}
	return client, auth, blogName, cfg, accountID, true
}

// runMessagesReplyOnePass does a single inbox burst on the active account.
func runMessagesReplyOnePass() int {
	client, auth, blogName, cfg, accountID, ok := prepareMessagesReplySession()
	if !ok {
		return 0
	}
	n, err := messageInboxBurst(client, &auth, cfg, blogName, accountID)
	if err != nil {
		mlogf("Messages error: %v", err)
		return 0
	}
	if cfg.DryRun {
		mlogf("Pass done — drafted %d (not sent)", n)
	} else {
		mlogf("Pass done — sent %d", n)
	}
	return n
}

func runMessagesReply() {
	acc, _ := currentAccount()
	runMessagesReplyOnAccount(acc)
}

func runMessagesReplyOnAccount(acc Account) {
	quietChromeLogin = true
	nativeChromeClicks = false
	client, auth, blogName, cfg, accountID, ok := prepareMessagesReplySessionFor(acc)
	if !ok {
		return
	}
	if cfg.DryRun {
		mlogf("Dry run — drafting replies, not sending.")
	}

	watch := messagesShouldWatch()
	deadline := messagesWatchDeadline(watch)
	if watch {
		keepMachineAwake()
		if sessionUntil.IsZero() {
			sessionUntil = deadline
		} else {
			deadline = sessionUntil
		}
		label := accountID
		if n := assignedBlogName(accountID); n != "" {
			label = "@" + n
		}
		mlogf("[%s] Watching for new DMs until %s", label, deadline.Format("15:04"))
	}

	total := 0
	round := 0
	for {
		if watch && !cfg.DryRun {
			if until, out := aiOutUntil(cfg.AI); out {
				mlogf("AI out of quota until %s — reach only, no DM checks till then", until.Format("15:04"))
				if !runMessagesIdleReach(acc, deadline) && !sleepMessagesWatch(time.Until(until), deadline) {
					mlogf("Watch window finished — stopping.")
					break
				}
				if sessionExpired() || (!deadline.IsZero() && !time.Now().Before(deadline)) {
					mlogf("Watch window finished — stopping.")
					break
				}
				continue
			}
		}
		round++
		if round > 1 {
			if err := ensureSession(client, &auth); err != nil {
				mlogf("Session dead: %v — recovering a working cookie (like reach)…", err)
				if rec := recoverSession(client, &auth, true); rec != nil {
					mlogf("Could not collect a new cookie: %v", rec)
					if !watch {
						return
					}
					// Do not kill the watcher — try again after idle (reach keeps going too).
					idle := messagesWatchIdle(cfg, 0)
					mlogf("Will retry cookie refresh after %s…", idle.Round(time.Second))
					if !sleepMessagesWatch(idle, deadline) {
						mlogf("Watch window finished — stopping.")
						break
					}
					continue
				}
				client = newAuthClient(&auth)
				if err := ensureSession(client, &auth); err != nil {
					mlogf("Session still dead: %v", err)
					if !watch {
						return
					}
					idle := messagesWatchIdle(cfg, 0)
					mlogf("Will retry after %s…", idle.Round(time.Second))
					if !sleepMessagesWatch(idle, deadline) {
						mlogf("Watch window finished — stopping.")
						break
					}
					continue
				}
				mlogf("Got a fresh session — continuing")
			}
			if usableBlogName(blogName) {
				_, _ = pinMessagingBlog(client, &auth, blogName)
			}
			mlogf("Inbox check #%d…", round)
		}
		n, err := messageInboxBurst(client, &auth, cfg, blogName, accountID)
		if err != nil {
			mlogf("Messages error: %v", err)
			if isEmailVerifyRequired(err) {
				mlogf("Stopping this account — verify the Tumblr login email, then rerun.")
				return
			}
			if isCookieProblem(err) {
				mlogf("Cookie/session failed mid-pass — recovering a working cookie…")
				if rec := recoverSession(client, &auth, true); rec != nil {
					mlogf("Could not collect a new cookie: %v", rec)
					if !watch {
						return
					}
					mlogf("Will retry after idle wait.")
				} else {
					client = newAuthClient(&auth)
					mlogf("Got a fresh session — will continue next check")
				}
			}
			if !watch {
				return
			}
			mlogf("Will retry after idle wait.")
		} else {
			total += n
			if cfg.DryRun {
				mlogf("Pass done — drafted %d (total %d, not sent)", n, total)
			} else {
				mlogf("Pass done — sent %d (total %d)", n, total)
			}
		}
		if !watch {
			break
		}
		if sessionExpired() || (!deadline.IsZero() && !time.Now().Before(deadline)) {
			mlogf("Watch window finished — stopping.")
			break
		}
		// Quiet inbox: do a short reach burst to pull in new people, then check DMs again.
		if err == nil && n == 0 && !cfg.DryRun {
			if ran := runMessagesIdleReach(acc, deadline); ran {
				continue
			}
		}
		idle := messagesWatchIdle(cfg, n)
		mlogf("No more to answer right now — next check in %s…", idle.Round(time.Second))
		if !sleepMessagesWatch(idle, deadline) {
			mlogf("Watch window finished — stopping.")
			break
		}
	}
	if cfg.DryRun {
		mlogf("Done — drafted %d replies (not sent)", total)
	} else {
		mlogf("Done — sent %d replies", total)
	}
}

// runMessagesReplyRoundRobin runs several accounts at once (each watches in its own goroutine).
func runMessagesReplyRoundRobin(accs []Account) {
	runMessagesReplyParallel(accs)
}

func runMessagesReplyParallel(accs []Account) {
	if len(accs) == 0 {
		runMessagesReply()
		return
	}
	if len(accs) == 1 {
		runMessagesReplyOnAccount(accs[0])
		return
	}

	cfg := loadMessageConfig()
	if messagesDryRun {
		cfg.DryRun = true
		mlogf("Dry run — drafting replies, not sending.")
	}
	watch := messagesShouldWatch()
	deadline := messagesWatchDeadline(watch)
	if watch {
		keepMachineAwake()
		if sessionUntil.IsZero() {
			sessionUntil = deadline
		} else {
			deadline = sessionUntil
		}
	}
	names := make([]string, 0, len(accs))
	for _, a := range accs {
		if n := assignedBlogName(a.ID); n != "" {
			names = append(names, "@"+n)
		} else {
			names = append(names, a.ID)
		}
	}
	if watch {
		mlogf("Watching DMs on %d accounts in parallel (%s) until %s — Ctrl+C to stop.",
			len(accs), strings.Join(names, ", "), deadline.Format("15:04"))
	} else {
		mlogf("Replying on %d accounts in parallel (%s)", len(accs), strings.Join(names, ", "))
	}

	_ = runForAccountsParallelLimited("messages-reply", accs, 3, func(a Account) error {
		runMessagesReplyOnAccount(a)
		return nil
	})
}

func printInbox(client *http.Client, auth Auth) error {
	convs, err := listAllConversations(client, auth)
	if err != nil {
		return err
	}
	if len(convs) == 0 {
		fmt.Println("Inbox is empty.")
		return nil
	}

	fmt.Printf("Messages  ·  %d conversations\n", len(convs))
	fmt.Println(strings.Repeat("─", 48))

	for i, c := range convs {
		// Prefer fuller history when available.
		msgs, err := fetchConversationMessages(client, auth, c.ID, 40, true)
		if err == nil && len(msgs) > 0 {
			c.Messages = msgs
		}

		unread := ""
		if c.Unread > 0 {
			unread = fmt.Sprintf("  unread %d", c.Unread)
		}
		who := c.OtherName
		if who == "" {
			who = "(unknown)"
		}
		fmt.Printf("\n%d  @%s%s\n", i+1, who, unread)
		if len(c.Messages) == 0 {
			if c.LastMessageText != "" {
				whoLast := "them"
				if c.LastMessageFromSelf {
					whoLast = "you "
				}
				fmt.Printf("   %s  %s\n", whoLast, c.LastMessageText)
			} else {
				fmt.Println("   (no messages)")
			}
			continue
		}
		for a, b := 0, len(c.Messages)-1; a < b; a, b = a+1, b-1 {
			c.Messages[a], c.Messages[b] = c.Messages[b], c.Messages[a]
		}
		start := 0
		if len(c.Messages) > 10 {
			start = len(c.Messages) - 10
		}
		for _, m := range c.Messages[start:] {
			label := "them"
			if m.FromSelf {
				label = "you "
			}
			text := cleanDMText(m.Text)
			if text == "" {
				text = "(non-text)"
			}
			for i, line := range wrapDMLines(text, 56) {
				if i == 0 {
					fmt.Printf("   %s  %s\n", label, line)
				} else {
					fmt.Printf("         %s\n", line)
				}
			}
		}
	}
	fmt.Println()
	return nil
}

func messageUnix(m ConvMessage) int64 {
	s := strings.TrimSpace(m.TS)
	if s == "" {
		return 0
	}
	n, err := strconv.ParseInt(s, 10, 64)
	if err != nil {
		f, err := strconv.ParseFloat(s, 64)
		if err != nil {
			return 0
		}
		n = int64(f)
	}
	return n
}

func latestMessage(msgs []ConvMessage) (ConvMessage, bool) {
	if len(msgs) == 0 {
		return ConvMessage{}, false
	}
	best := 0
	bestTS := messageUnix(msgs[0])
	stamped := 0
	for i, m := range msgs {
		ts := messageUnix(m)
		if ts > 0 {
			stamped++
		}
		if ts > bestTS {
			bestTS = ts
			best = i
		}
	}
	if stamped == 0 {
		return msgs[len(msgs)-1], true
	}
	return msgs[best], true
}

func orderHistoryOldestFirst(msgs []ConvMessage) []ConvMessage {
	if len(msgs) < 2 {
		return msgs
	}
	out := append([]ConvMessage(nil), msgs...)
	stamped := 0
	for _, m := range out {
		if messageUnix(m) > 0 {
			stamped++
		}
	}
	if stamped >= 2 {
		sort.SliceStable(out, func(i, j int) bool {
			return messageUnix(out[i]) < messageUnix(out[j])
		})
		return out
	}
	for a, b := 0, len(out)-1; a < b; a, b = a+1, b-1 {
		out[a], out[b] = out[b], out[a]
	}
	return out
}

func conversationMessagesURL(auth Auth, conversationID string, limit int, preserveLastRead bool, beforeTS string) string {
	if limit <= 0 {
		limit = 30
	}
	u, _ := url.Parse("https://www.tumblr.com/api/v2/conversations/messages")
	q := u.Query()
	q.Set("conversation_id", conversationID)
	q.Set("participant", auth.Blog)
	q.Set("limit", fmt.Sprintf("%d", limit))
	if preserveLastRead {
		q.Set("preserve_last_read_ts", "true")
	}
	if strings.TrimSpace(beforeTS) != "" {
		q.Set("before", strings.TrimSpace(beforeTS))
	}
	q.Set("fields[blogs]", "?avatar,name,?uuid,url")
	u.RawQuery = q.Encode()
	return u.String()
}

func fetchConversationMessages(client *http.Client, auth Auth, conversationID string, limit int, preserveLastRead bool) ([]ConvMessage, error) {
	return fetchConversationMessagesBefore(client, auth, conversationID, limit, preserveLastRead, "")
}

// fetchAllConversationMessages pages backward until Tumblr runs out, so reply-set
// counts are not capped by a single 80-message window.
func fetchAllConversationMessages(client *http.Client, auth Auth, conversationID string, preserveLastRead bool) ([]ConvMessage, error) {
	const pageSize = 100
	const maxPages = 25
	seen := map[string]bool{}
	var all []ConvMessage
	before := ""
	for page := 0; page < maxPages; page++ {
		batch, err := fetchConversationMessagesBefore(client, auth, conversationID, pageSize, preserveLastRead, before)
		if err != nil {
			if page == 0 {
				return nil, err
			}
			break
		}
		if len(batch) == 0 {
			break
		}
		added := 0
		oldest := ""
		for _, m := range batch {
			key := m.TS + "|" + m.Text + "|" + fmt.Sprintf("%v", m.FromSelf)
			if seen[key] {
				continue
			}
			seen[key] = true
			all = append(all, m)
			added++
			if oldest == "" || (m.TS != "" && m.TS < oldest) {
				oldest = m.TS
			}
		}
		if added == 0 || oldest == "" || oldest == before {
			break
		}
		before = oldest
		if len(batch) < pageSize {
			break
		}
	}
	return orderHistoryOldestFirst(all), nil
}

func fetchConversationMessagesBefore(client *http.Client, auth Auth, conversationID string, limit int, preserveLastRead bool, beforeTS string) ([]ConvMessage, error) {
	req, err := http.NewRequest("GET", conversationMessagesURL(auth, conversationID, limit, preserveLastRead, beforeTS), nil)
	if err != nil {
		return nil, err
	}
	setCommonHeaders(req, auth)
	req.Header.Set("X-CSRF", auth.CSRF)
	req.Header.Set("Referer", "https://www.tumblr.com/messages")

	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("%s: %s", resp.Status, truncate(string(body), 160))
	}

	var raw map[string]any
	if err := json.Unmarshal(body, &raw); err != nil {
		return nil, err
	}
	response, _ := raw["response"].(map[string]any)
	if response == nil {
		return nil, fmt.Errorf("missing response")
	}
	return parseConversationMessageData(auth, response), nil
}

func parseConversationMessageData(auth Auth, response map[string]any) []ConvMessage {
	var data []any
	if msgs, ok := response["messages"].(map[string]any); ok {
		data, _ = msgs["data"].([]any)
	}
	if len(data) == 0 {
		if conv, ok := response["conversation"].(map[string]any); ok {
			if msgs, ok := conv["messages"].(map[string]any); ok {
				data, _ = msgs["data"].([]any)
			}
		}
	}
	if len(data) == 0 {
		data, _ = response["messages"].([]any)
	}

	selfIDs := map[string]bool{strings.ToLower(strings.TrimSpace(auth.Blog)): true}
	nameByID := map[string]string{strings.ToLower(auth.Blog): "you"}
	if participants, ok := response["participants"].([]any); ok {
		for _, p := range participants {
			pm, ok := p.(map[string]any)
			if !ok {
				continue
			}
			name := anyToString(pm["name"])
			uuid := anyToString(pm["uuid"])
			if uuid == "" {
				continue
			}
			lu := strings.ToLower(uuid)
			if strings.EqualFold(uuid, auth.Blog) || strings.EqualFold(name, auth.Blog) {
				selfIDs[lu] = true
				if name != "" {
					selfIDs[strings.ToLower(name)] = true
				}
				nameByID[lu] = "you"
				continue
			}
			if name != "" {
				nameByID[lu] = name
			}
		}
	}

	out := make([]ConvMessage, 0, len(data))
	for _, el := range data {
		m, ok := el.(map[string]any)
		if !ok {
			continue
		}
		part := anyToString(m["participant"])
		text := anyToString(m["message"])
		if text == "" {
			if content, ok := m["content"].(map[string]any); ok {
				text = anyToString(content["text"])
			}
		}
		if strings.TrimSpace(text) == "" {
			text = mediaPlaceholderFromMessage(m)
		}
		lp := strings.ToLower(part)
		fromSelf := selfIDs[lp] || strings.EqualFold(part, auth.Blog) || nameByID[lp] == "you"
		fromName := nameByID[lp]
		if fromName == "" {
			if fromSelf {
				fromName = "you"
			} else {
				fromName = part
			}
		}
		out = append(out, ConvMessage{
			FromSelf: fromSelf,
			FromName: fromName,
			Text:     text,
			TS:       anyToString(m["ts"]),
		})
	}
	return out
}

func mediaPlaceholderFromType(typ string) string {
	t := strings.ToUpper(strings.TrimSpace(typ))
	switch {
	case strings.Contains(t, "GIF"):
		return "(gif)"
	case strings.Contains(t, "IMAGE"), strings.Contains(t, "PHOTO"), strings.Contains(t, "IMG"):
		return "(image)"
	case strings.Contains(t, "VIDEO"):
		return "(video)"
	case t != "" && t != "TEXT":
		return "(non-text)"
	default:
		return "(non-text)"
	}
}

func mediaPlaceholderFromMessage(m map[string]any) string {
	if m == nil {
		return "(non-text)"
	}
	if typ := anyToString(m["type"]); typ != "" {
		return mediaPlaceholderFromType(typ)
	}
	if content, ok := m["content"].(map[string]any); ok {
		if content["image"] != nil || content["images"] != nil || anyToString(content["media"]) != "" {
			return "(image)"
		}
		if typ := anyToString(content["type"]); typ != "" {
			return mediaPlaceholderFromType(typ)
		}
	}
	return "(non-text)"
}

func openConversationLikeHuman(client *http.Client, auth Auth, c Conversation) {
	chromePeekConversation(c.OtherName)
	if _, err := fetchConversationMessages(client, auth, c.ID, 20, false); err != nil {
		fmt.Printf("  open chat → %s fail: %v\n", c.OtherName, err)
		return
	}
	fmt.Printf("  opened chat → %s\n", c.OtherName)
}

func chromePeekConversation(otherName string) {
	want := strings.ToLower(strings.TrimSpace(otherName))
	if want == "" || !attachAccountDebugPort() {
		return
	}
	list, err := listChromeTargets()
	if err != nil {
		return
	}
	var page chromeJSONTarget
	for _, t := range list {
		if t.Type != "page" {
			continue
		}
		u := strings.ToLower(t.URL)
		if !strings.Contains(u, "tumblr.com") {
			continue
		}
		page = t
		if strings.Contains(u, "/inbox") || strings.Contains(u, "/messaging") || strings.Contains(u, "/messages") {
			break
		}
	}
	if page.ID == "" {
		return
	}
	js := fmt.Sprintf(`(() => {
		const want = %s;
		const nodes = [...document.querySelectorAll('a,button,[role="listitem"],[role="option"],li,div')];
		for (const el of nodes) {
			const text = (el.innerText || '').trim();
			if (!text) continue;
			const first = text.split('\n')[0].replace(/\s+/g,' ').trim().toLowerCase();
			if (first !== want) continue;
			const hit = el.closest('a,button,[role="listitem"],[role="option"]') || el;
			hit.click();
			return 'clicked';
		}
		return 'notfound';
	})()`, strconv.Quote(want))
	var result string
	_ = evalJSONOnChromeTarget(page, js, &result)
}

func wrapDMLines(s string, width int) []string {
	s = strings.TrimSpace(strings.Join(strings.Fields(s), " "))
	if s == "" {
		return []string{""}
	}
	if width < 20 {
		width = 20
	}
	words := strings.Fields(s)
	var lines []string
	var cur strings.Builder
	for _, w := range words {
		if cur.Len() == 0 {
			cur.WriteString(w)
			continue
		}
		if cur.Len()+1+len(w) > width {
			lines = append(lines, cur.String())
			cur.Reset()
			cur.WriteString(w)
			continue
		}
		cur.WriteByte(' ')
		cur.WriteString(w)
	}
	if cur.Len() > 0 {
		lines = append(lines, cur.String())
	}
	return lines
}

func logDMCard(asBlog, other, src string, history []ConvMessage, draft string, dry bool) {
	if other == "" {
		other = "?"
	}
	kind := "send"
	if dry {
		kind = "draft"
	}
	whoAs := strings.TrimSpace(asBlog)
	if whoAs == "" {
		whoAs = "zoey"
	}
	mlogf("")
	mlogf("────────────────────────────────────────")
	mlogf("  %s  ·  @%s  →  @%s  ·  %s", kind, whoAs, other, src)
	mlogf("")
	start := 0
	if len(history) > 8 {
		start = len(history) - 8
	}
	shown := 0
	for _, m := range history[start:] {
		text := cleanDMText(m.Text)
		if text == "" {
			continue
		}
		shown++
		label := "them"
		if m.FromSelf {
			label = "you "
		}
		lines := wrapDMLines(text, 54)
		for i, line := range lines {
			if i == 0 {
				mlogf("  %s  %s", label, line)
			} else {
				mlogf("        %s", line)
			}
		}
	}
	if shown == 0 {
		mlogf("  (no chat history)")
	}
	mlogf("")
	mlogf("  %s", kind)
	// Same newline-only split as sendConversationBubbles, so the card shows
	// exactly the bubbles that get sent.
	var bubbles []string
	for _, line := range strings.Split(strings.ReplaceAll(draft, "\r\n", "\n"), "\n") {
		if line = strings.TrimSpace(line); line != "" {
			bubbles = append(bubbles, line)
		}
	}
	if len(bubbles) == 0 {
		mlogf("  zoey  %s", strings.TrimSpace(draft))
	}
	for _, bubble := range bubbles {
		lines := wrapDMLines(bubble, 54)
		for i, line := range lines {
			if i == 0 {
				mlogf("  zoey  %s", line)
			} else {
				mlogf("        %s", line)
			}
		}
	}
	mlogf("────────────────────────────────────────")
}

func messageInboxBurst(client *http.Client, auth *Auth, cfg MessageConfig, asBlog, accountID string) (int, error) {
	if !cfg.Enabled {
		return 0, nil
	}
	release, holder, ok := tryLockAccount(accountID, "messaging")
	if !ok {
		mlogf("  skip — another run (pid %d) is already replying on this account", holder)
		return 0, nil
	}
	defer release()
	// Same surface the browser uses — warms messaging cookies / CSRF before listing.
	warmMessagesInbox(client, auth)
	convs, err := listAllConversations(client, *auth)
	if err != nil {
		return 0, err
	}
	mlogf("Inbox  ·  %d conversations", len(convs))
	if len(convs) > 20 {
		mlogf("  (loaded %d threads across pages)", len(convs))
	}
	if cfg.MaxReplies > 0 {
		mlogf("Reply cap  ·  %d this pass", cfg.MaxReplies)
	}
	convs = longestWaitingFirst(convs)

	replied := 0
	marked := 0
	pending := 0
	for _, c := range convs {
		if repliesCapReached(replied, cfg.MaxReplies) {
			break
		}
		if !c.CanSend {
			continue
		}
		if isBlockedMinor(c.OtherName) {
			mlogf("  skip @%s — under 18, never replying", c.OtherName)
			if !cfg.DryRun {
				recordDMEvent(dmEvent{Account: accountID, Partner: c.OtherName, ConvID: c.ID, Event: "skip", Reason: "under 18 (already blocked)"})
			}
			continue
		}
		skipEvent := func(reason string) {
			if !cfg.DryRun {
				recordDMEvent(dmEvent{Account: accountID, Partner: c.OtherName, ConvID: c.ID, Event: "skip", Reason: reason})
			}
		}

		history := c.Messages
		if msgs, err := fetchAllConversationMessages(client, *auth, c.ID, cfg.DryRun); err == nil && len(msgs) > 0 {
			history = msgs
		} else if msgs, err := fetchConversationMessages(client, *auth, c.ID, 80, cfg.DryRun); err == nil && len(msgs) > 0 {
			history = orderHistoryOldestFirst(msgs)
		}
		cleaned := history[:0]
		for _, m := range history {
			m.Text = cleanDMText(m.Text)
			if m.Text == "" {
				// Keep media bubbles so we don't re-ask "send them" after they already did.
				if !m.FromSelf {
					m.Text = "(non-text)"
				} else {
					continue
				}
			}
			cleaned = append(cleaned, m)
		}
		history = cleaned

		if said := underageEvidence(history); said != "" {
			blockMinor(c.OtherName, accountID, fmt.Sprintf("said under 18 in chat: %q", said))
			mlogf("  skip @%s — said they are under 18 (%q), blocked on every account for good", c.OtherName, said)
			skipEvent("under 18 (said so in chat)")
			continue
		}
		if age, ok := theirStatedAge(history); ok {
			rememberAdultAge(c.OtherName, age)
		}

		turns := zoeyReplyTurns(history)
		syncDMReplySetsAtLeastFor(accountID, c.OtherName, turns)
		sets := effectiveZoeyReplySetsFor(accountID, history, c.OtherName)
		if shouldStopReplying(history, c.OtherName) {
			reason := "done with this chat"
			if looksLikeChatBot(c.OtherName, history) {
				reason = "chatbot — not replying"
			} else if theyClaimUnderage(history) {
				reason = "they said under 18 — stopping"
			} else if weAlreadySentBeacons(history) {
				reason = "already sent beacons + one follow-up — forgetting them"
			}
			mlogf("  skip @%s — %s (sets=%d history_turns=%d)", c.OtherName, reason, sets, turns)
			if c.Unread > 0 {
				skipEvent(reason)
			}
			if !cfg.DryRun && !repliesCapReached(marked, cfg.MaxReplies) && (c.Unread > 0 || attachAccountDebugPort()) {
				openConversationLikeHuman(client, *auth, c)
				marked++
			}
			continue
		}

		latest, ok := latestMessage(history)
		theyLast := ok && !latest.FromSelf
		if !ok {
			// No usable history — fall back to inbox preview only.
			theyLast = !c.LastMessageFromSelf
		}
		follow := needsFollowUpReply(history)
		// Trust full history over the inbox preview. Preview LastMessageFromSelf
		// used to force-skip threads that still needed a reply.
		if !ok && c.LastMessageFromSelf && !follow {
			theyLast = false
		}
		// The unread badge often sticks after we reply, so only trust it when
		// the full history could not be loaded; otherwise we double-text.
		needsReply := theyLast || follow || (!ok && c.Unread > 0)
		if cfg.OnlyUnread && c.Unread <= 0 && !needsReply {
			continue
		}
		if !needsReply {
			if !cfg.DryRun && !repliesCapReached(marked, cfg.MaxReplies) && attachAccountDebugPort() {
				openConversationLikeHuman(client, *auth, c)
				marked++
			}
			continue
		}
		pending++
		if alreadyAnswered(c.ID, latest) {
			mlogf("  skip @%s — already replied to their last message, Tumblr hasn't caught up yet", c.OtherName)
			continue
		}
		if looksLikeMoneyAsk(lastUserText(history)) {
			mlogf("  skip @%s — money/donation ask", c.OtherName)
			skipEvent("money/donation ask")
			continue
		}
		if checkMinorBio(client, *auth, c.OtherName, accountID) {
			mlogf("  skip @%s — blog bio says under 18, blocked on every account for good", c.OtherName)
			skipEvent("under 18 (blog bio)")
			continue
		}
		if !cfg.DryRun {
			chromePeekConversation(c.OtherName)
			fmt.Printf("  opened chat → %s\n", c.OtherName)
			time.Sleep(time.Duration(600+rand.Intn(900)) * time.Millisecond)
		}
		text, src := pickReplyTextFor(cfg, accountID, c.OtherName, history)
		if src == "ai-exhausted" {
			mlogf("  stop — %s. The rest of this inbox waits for the next run.", aiBudgetSummary())
			skipEvent("AI budget used up")
			break
		}
		if strings.TrimSpace(text) == "" {
			continue
		}
		logDMCard(asBlog, c.OtherName, src, history, text, cfg.DryRun)
		phase := fmt.Sprintf("set %d (funnel at >=%d)", sets+1, funnelAfterZoeyReplies)
		if weAlreadySentBeacons(history) {
			phase = "post-beacons follow-up (last reply)"
		} else if weAlreadySentPinNudge(history) {
			phase = "post-pin follow-up"
		} else if shouldInstantBeacons(history) {
			phase = fmt.Sprintf("set %d — sending beacons redirect (>=%d)", sets, funnelAfterZoeyReplies)
		} else if shouldSendBeaconsFor(accountID, history, c.OtherName) {
			phase = fmt.Sprintf("set %d — pin nudge (>=%d)", sets, funnelAfterZoeyReplies)
		}
		mlogf("  @%s %s (history turns %d, counted sets %d)", c.OtherName, phase, turns, sets)
		if cfg.DryRun {
			replied++
			// Space dry-run AI calls so Groq ITPM (7k/min) is not blown in seconds.
			if cfg.AI.Enabled {
				time.Sleep(4 * time.Second)
			}
			continue
		}
		if err := sendConversationBubbles(client, *auth, c.ID, text); err != nil {
			fmt.Println("   send fail:", err)
			recordDMEvent(dmEvent{Account: accountID, Partner: c.OtherName, ConvID: c.ID, Event: "send_fail", Reason: truncate(err.Error(), 80)})
			if isEmailVerifyRequired(err) {
				mlogf("  stop — Tumblr says this account must verify its email before DMing (check the inbox for the login email)")
				return replied, err
			}
			if isCookieProblem(err) {
				err = retryAfterCookie(client, auth, messageUILog, accountID, err, func() error {
					if asBlog != "" {
						_, _ = pinMessagingBlog(client, auth, asBlog)
					}
					return sendConversationBubbles(client, *auth, c.ID, text)
				})
				if err != nil {
					fmt.Println("   retry fail:", err)
					if isEmailVerifyRequired(err) {
						mlogf("  stop — Tumblr says this account must verify its email before DMing (check the inbox for the login email)")
						return replied, err
					}
					if isCookieProblem(err) {
						return replied, err
					}
					continue
				}
			} else if isForbidden(err) {
				// Soft Tumblr glitch — one quick retry, no Chrome.
				time.Sleep(2 * time.Second)
				if err := sendConversationBubbles(client, *auth, c.ID, text); err != nil {
					fmt.Println("   retry fail:", err)
					if isEmailVerifyRequired(err) {
						mlogf("  stop — Tumblr says this account must verify its email before DMing (check the inbox for the login email)")
						return replied, err
					}
					continue
				}
			} else {
				continue
			}
		}
		recordDMEvent(dmReplyEvent(accountID, c, history, text, src, sets+1))
		noteDMReplySetFor(accountID, c.OtherName)
		noteAnswered(c.ID, latest)
		replied++
		chromePeekConversation(c.OtherName)
		_, _ = fetchConversationMessages(client, *auth, c.ID, 5, false)
		wait := cfg.DelaySeconds
		if cfg.DelayJitterSeconds > 0 {
			wait += rand.Intn(cfg.DelayJitterSeconds + 1)
		}
		if cfg.DryRun {
			fmt.Printf("  waiting %ds…\n", wait)
			time.Sleep(time.Duration(wait) * time.Second)
		} else if cfg.AI.NonExplicit {
			time.Sleep(time.Duration(wait) * time.Second)
		} else {
			sleepOrReachBetweenChats(client, auth, accountID, wait)
		}
	}

	if pending > 0 {
		mlogf("Needed replies this pass  ·  %d (sent %d)", pending, replied)
	}

	if cfg.ColdOpenEnabled && !repliesCapReached(replied, cfg.MaxReplies) {
		left := cfg.ColdOpenMax
		if cfg.MaxReplies > 0 {
			remain := cfg.MaxReplies - replied
			if left > remain {
				left = remain
			}
		}
		n, err := coldOpenMessages(client, auth, cfg, left, accountID)
		replied += n
		if err != nil {
			return replied, err
		}
	}
	return replied, nil
}

func coldOpenMessages(client *http.Client, auth *Auth, cfg MessageConfig, max int, accountID string) (int, error) {
	if max <= 0 || len(cfg.ColdOpenTemplates) == 0 {
		return 0, nil
	}
	already := loadStringSet(messagedPath())
	followed, _ := loadFollowed()
	var candidates []string
	for blog := range followed {
		if already[strings.ToLower(blog)] {
			continue
		}
		candidates = append(candidates, blog)
	}
	rand.Shuffle(len(candidates), func(i, j int) { candidates[i], candidates[j] = candidates[j], candidates[i] })

	sent := 0
	for _, blog := range candidates {
		if sent >= max {
			break
		}
		text := cfg.ColdOpenTemplates[rand.Intn(len(cfg.ColdOpenTemplates))]
		if cfg.DryRun {
			fmt.Printf("  [dry-run] cold DM → %s: %s\n", blog, text)
			sent++
			continue
		}
		fmt.Printf("  cold DM → %s: %s\n", blog, truncate(text, 60))
		if err := startConversationText(client, *auth, blog, text); err != nil {
			fmt.Println("   cold fail:", err)
			continue
		}
		already[strings.ToLower(blog)] = true
		_ = saveStringSet(messagedPath(), already)
		sent++
		wait := cfg.DelaySeconds + rand.Intn(cfg.DelayJitterSeconds+1)
		sleepOrReachBetweenChats(client, auth, accountID, wait)
	}
	return sent, nil
}

func listConversations(client *http.Client, auth Auth, limit int) ([]Conversation, error) {
	return listConversationsPage(client, auth, limit, "")
}

// listAllConversations pages the inbox so we don't stop after the first ~20 threads.
// Tumblr hard-caps ~20/page and ignores offset/ms timestamps — follow links.next
// before= (unix seconds), falling back to truncating the oldest preview ts.
func listAllConversations(client *http.Client, auth Auth) ([]Conversation, error) {
	const pageSize = 20
	var all []Conversation
	seen := map[string]bool{}
	before := ""
	for page := 0; page < 100; page++ {
		batch, nextBefore, err := listConversationsPageWithCursor(client, auth, pageSize, before)
		if err != nil {
			if page == 0 {
				return nil, err
			}
			break
		}
		added := 0
		pageOldest := ""
		for _, c := range batch {
			key := strings.TrimSpace(c.ID)
			if key == "" {
				key = strings.ToLower(strings.TrimSpace(c.OtherName))
			}
			if key == "" || seen[key] {
				continue
			}
			seen[key] = true
			all = append(all, c)
			added++
			if ts := conversationActivityTS(c); ts != "" {
				if pageOldest == "" || ts < pageOldest {
					pageOldest = ts
				}
			}
		}
		if len(batch) == 0 || added == 0 {
			break
		}
		next := strings.TrimSpace(nextBefore)
		if next == "" {
			next = tumblrConversationsBeforeCursor(pageOldest)
		}
		if next == "" || next == before {
			break
		}
		before = next
	}
	return all, nil
}

// Tumblr's message list can lag a minute behind a send, so a thread we just
// answered still looks like it is waiting on the next pass.
var (
	answeredMu sync.Mutex
	answered   = map[string]string{}
)

func inboundKey(m ConvMessage) string {
	return strings.TrimSpace(m.TS) + "|" + strings.TrimSpace(m.Text)
}

func alreadyAnswered(convID string, latest ConvMessage) bool {
	if convID == "" || latest.FromSelf {
		return false
	}
	answeredMu.Lock()
	defer answeredMu.Unlock()
	return answered[convID] == inboundKey(latest)
}

func noteAnswered(convID string, latest ConvMessage) {
	if convID == "" || latest.FromSelf {
		return
	}
	answeredMu.Lock()
	answered[convID] = inboundKey(latest)
	answeredMu.Unlock()
}

// longestWaitingFirst puts threads where they spoke last ahead of the rest,
// oldest first, so people far down the inbox get answered before anyone who
// just messaged — a reply bumps a thread to the top, and the pass can end
// before it ever reaches the bottom.
func longestWaitingFirst(convs []Conversation) []Conversation {
	out := append([]Conversation(nil), convs...)
	waiting := func(c Conversation) bool { return !c.LastMessageFromSelf || c.Unread > 0 }
	sort.SliceStable(out, func(i, j int) bool {
		wi, wj := waiting(out[i]), waiting(out[j])
		if wi != wj {
			return wi
		}
		if !wi {
			return false
		}
		ti, tj := conversationActivityTS(out[i]), conversationActivityTS(out[j])
		if ti == "" || tj == "" {
			return ti != "" && tj == ""
		}
		if len(ti) != len(tj) {
			return len(ti) < len(tj)
		}
		return ti < tj
	})
	return out
}

func conversationActivityTS(c Conversation) string {
	newest := ""
	for _, m := range c.Messages {
		ts := strings.TrimSpace(m.TS)
		if ts == "" {
			continue
		}
		if newest == "" || ts > newest {
			newest = ts
		}
	}
	return newest
}

// tumblrConversationsBeforeCursor converts a message ts to the before= cursor
// Tumblr's conversations links.next uses (unix seconds, not ms).
func tumblrConversationsBeforeCursor(ts string) string {
	ts = strings.TrimSpace(ts)
	if ts == "" {
		return ""
	}
	if len(ts) > 10 {
		return ts[:10]
	}
	return ts
}

// listConversationsOnceOffset is only used as a fallback pager when before= stalls.
func listConversationsOnceOffset(client *http.Client, auth Auth, limit, offset int) ([]Conversation, error) {
	if limit <= 0 {
		limit = 20
	}
	u, _ := url.Parse("https://www.tumblr.com/api/v2/conversations")
	q := u.Query()
	q.Set("limit", fmt.Sprintf("%d", limit))
	if offset > 0 {
		q.Set("offset", fmt.Sprintf("%d", offset))
	}
	q.Set("participant", auth.Blog)
	q.Set("fields[blogs]", "?avatar,name,?seconds_since_last_activity,url,?blog_view_url,?uuid,?theme,?description_npf,?is_adult,?primary")
	u.RawQuery = q.Encode()

	req, err := http.NewRequest("GET", u.String(), nil)
	if err != nil {
		return nil, err
	}
	setCommonHeaders(req, auth)
	req.Header.Set("X-CSRF", auth.CSRF)
	req.Header.Set("Referer", "https://www.tumblr.com/messages")

	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("%s: %s", resp.Status, truncate(string(body), 180))
	}
	return parseConversationsBody(body, auth)
}

// warmMessagesInbox hits the same Messages page the browser uses so Tumblr
// issues messaging CSRF/session state before we list conversations.
func warmMessagesInbox(client *http.Client, auth *Auth) {
	if auth == nil {
		return
	}
	for _, page := range []string{
		"https://www.tumblr.com/messages",
		"https://www.tumblr.com/messaging",
	} {
		req, err := http.NewRequest("GET", page, nil)
		if err != nil {
			continue
		}
		setCommonHeaders(req, *auth)
		if auth.CSRF != "" {
			req.Header.Set("X-CSRF", auth.CSRF)
		}
		req.Header.Set("Referer", "https://www.tumblr.com/dashboard")
		resp, err := client.Do(req)
		if err != nil {
			continue
		}
		_, _ = io.Copy(io.Discard, resp.Body)
		if csrf := resp.Header.Get("X-CSRF"); csrf != "" {
			auth.CSRF = csrf
		}
		_ = resp.Body.Close()
		break
	}
	_ = refreshCSRF(client, auth)
}

func listConversationsPage(client *http.Client, auth Auth, limit int, beforeTS string) ([]Conversation, error) {
	convs, _, err := listConversationsPageWithCursor(client, auth, limit, beforeTS)
	return convs, err
}

func listConversationsPageWithCursor(client *http.Client, auth Auth, limit int, beforeTS string) ([]Conversation, string, error) {
	convs, next, err := listConversationsOnceWithCursor(client, auth, limit, beforeTS)
	if err == nil {
		return convs, next, nil
	}
	if !isForbidden(err) && !isUnauthorized(err) {
		return nil, "", err
	}
	// Soft 403/401 on DMs is often a stale CSRF — refresh and retry once.
	if err2 := refreshCSRF(client, &auth); err2 == nil {
		convs, next, err2 = listConversationsOnceWithCursor(client, auth, limit, beforeTS)
		if err2 == nil {
			return convs, next, nil
		}
		err = err2
	}
	return nil, "", err
}

func listConversationsOnce(client *http.Client, auth Auth, limit int, beforeTS string) ([]Conversation, error) {
	convs, _, err := listConversationsOnceWithCursor(client, auth, limit, beforeTS)
	return convs, err
}

func listConversationsOnceWithCursor(client *http.Client, auth Auth, limit int, beforeTS string) ([]Conversation, string, error) {
	if limit <= 0 {
		limit = 20
	}
	u, _ := url.Parse("https://www.tumblr.com/api/v2/conversations")
	q := u.Query()
	q.Set("limit", fmt.Sprintf("%d", limit))
	if strings.TrimSpace(beforeTS) != "" {
		q.Set("before", strings.TrimSpace(beforeTS))
	}
	q.Set("participant", auth.Blog)
	q.Set("fields[blogs]", "?avatar,name,?seconds_since_last_activity,url,?blog_view_url,?uuid,?theme,?description_npf,?is_adult,?primary")
	u.RawQuery = q.Encode()

	req, err := http.NewRequest("GET", u.String(), nil)
	if err != nil {
		return nil, "", err
	}
	setCommonHeaders(req, auth)
	req.Header.Set("X-CSRF", auth.CSRF)
	req.Header.Set("Referer", "https://www.tumblr.com/messages")

	resp, err := client.Do(req)
	if err != nil {
		return nil, "", err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, "", err
	}
	if resp.StatusCode != http.StatusOK {
		return nil, "", fmt.Errorf("%s: %s", resp.Status, truncate(string(body), 180))
	}

	return parseConversationsBodyWithCursor(body, auth)
}

func parseConversationsBody(body []byte, auth Auth) ([]Conversation, error) {
	convs, _, err := parseConversationsBodyWithCursor(body, auth)
	return convs, err
}

func parseConversationsBodyWithCursor(body []byte, auth Auth) ([]Conversation, string, error) {
	var raw struct {
		Response struct {
			Conversations []conversationAPI `json:"conversations"`
			Links         struct {
				Next struct {
					QueryParams struct {
						Before string `json:"before"`
					} `json:"queryParams"`
					Href string `json:"href"`
				} `json:"next"`
			} `json:"links"`
		} `json:"response"`
	}
	if err := json.Unmarshal(body, &raw); err != nil {
		return nil, "", err
	}

	out := make([]Conversation, 0, len(raw.Response.Conversations))
	for _, c := range raw.Response.Conversations {
		conv := Conversation{
			ID:      c.ID,
			CanSend: c.CanSend,
			Unread:  c.UnreadMessagesCount,
		}
		for _, p := range c.Participants {
			if strings.EqualFold(p.UUID, auth.Blog) {
				continue
			}
			if p.Name != "" {
				conv.OtherName = p.Name
				conv.OtherUUID = p.UUID
				break
			}
		}
		if len(c.Messages.Data) > 0 {
			m := c.Messages.Data[0]
			conv.LastMessageText = m.Message
			if conv.LastMessageText == "" {
				conv.LastMessageText = m.Content.Text
			}
			conv.LastMessageFromSelf = strings.EqualFold(m.Participant, auth.Blog)
			for _, msg := range c.Messages.Data {
				text := msg.Message
				if text == "" {
					text = msg.Content.Text
				}
				if strings.TrimSpace(text) == "" {
					text = mediaPlaceholderFromType(msg.Type)
				}
				fromSelf := strings.EqualFold(msg.Participant, auth.Blog)
				fromName := conv.OtherName
				if fromSelf {
					fromName = "you"
				}
				conv.Messages = append(conv.Messages, ConvMessage{
					FromSelf: fromSelf,
					FromName: fromName,
					Text:     text,
					TS:       msg.TS,
				})
			}
		}
		out = append(out, conv)
	}

	next := strings.TrimSpace(raw.Response.Links.Next.QueryParams.Before)
	if next == "" {
		if href := strings.TrimSpace(raw.Response.Links.Next.Href); href != "" {
			if u, err := url.Parse(href); err == nil {
				next = strings.TrimSpace(u.Query().Get("before"))
			} else if u, err := url.Parse("https://www.tumblr.com" + href); err == nil {
				next = strings.TrimSpace(u.Query().Get("before"))
			}
		}
	}
	return out, next, nil
}

func isForbidden(err error) bool {
	if err == nil {
		return false
	}
	s := err.Error()
	return strings.Contains(s, "403") || strings.Contains(strings.ToLower(s), "forbidden")
}

func isEmailVerifyRequired(err error) bool {
	if err == nil {
		return false
	}
	l := strings.ToLower(err.Error())
	return strings.Contains(l, "verify their email") ||
		strings.Contains(l, "verify your email") ||
		strings.Contains(l, `"code":7001`) ||
		strings.Contains(l, `"code": 7001`)
}

func pinMessagingBlog(client *http.Client, auth *Auth, want string) (string, error) {
	want = strings.TrimSpace(strings.ToLower(want))
	if want == "" || auth == nil {
		return "", fmt.Errorf("missing blog")
	}
	blogs, err := listAccountBlogsDetailed(client, *auth)
	if err != nil {
		return "", err
	}
	for _, b := range blogs {
		if strings.EqualFold(b.Name, want) && strings.TrimSpace(b.UUID) != "" {
			auth.Blog = b.UUID
			_ = saveAuth(*auth)
			return b.Name, nil
		}
	}
	return "", fmt.Errorf("blog %s not on this account", want)
}

func sendConversationBubbles(client *http.Client, auth Auth, conversationID, text string) error {
	// Split on newlines only. Do not re-run splitDMBubbles/calmDMReply — that
	// strips the beacons 😭😭 and can drop the redirect when there are 3 answer lines.
	var bubbles []string
	for _, line := range strings.Split(strings.ReplaceAll(text, "\r\n", "\n"), "\n") {
		line = strings.TrimSpace(line)
		if line != "" {
			bubbles = append(bubbles, line)
		}
	}
	if len(bubbles) == 0 {
		return fmt.Errorf("empty dm")
	}
	for i, b := range bubbles {
		if i > 0 {
			wait := 5 + rand.Intn(4) // 5–8s between Zoey's double-texts
			mlogf("   …%ds before next bubble", wait)
			time.Sleep(time.Duration(wait) * time.Second)
		}
		if err := sendConversationText(client, auth, conversationID, b); err != nil {
			return err
		}
	}
	return nil
}

func sendConversationText(client *http.Client, auth Auth, conversationID, text string) error {
	payload := map[string]any{
		"conversation_id": conversationID,
		"type":            "TEXT",
		"participant":     auth.Blog,
		"message":         text,
	}
	return postConversationMessage(client, auth, payload)
}

func startConversationText(client *http.Client, auth Auth, otherBlog, text string) error {
	other := strings.TrimSpace(otherBlog)
	other = strings.TrimSuffix(other, ".tumblr.com")
	payload := map[string]any{
		"participants": []string{auth.Blog, other},
		"type":         "TEXT",
		"participant":  auth.Blog,
		"message":      text,
	}
	return postConversationMessage(client, auth, payload)
}

func postConversationMessage(client *http.Client, auth Auth, payload map[string]any) error {
	body, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	u := "https://www.tumblr.com/api/v2/conversations/messages?fields[blogs]=?avatar,name,?uuid,url"
	req, err := http.NewRequest("POST", u, bytes.NewReader(body))
	if err != nil {
		return err
	}
	setCommonHeaders(req, auth)
	req.Header.Set("Content-Type", "application/json; charset=utf8")
	req.Header.Set("X-CSRF", auth.CSRF)
	req.Header.Set("Referer", "https://www.tumblr.com/messages")

	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("%s: %s", resp.Status, truncate(string(respBody), 200))
	}
	return nil
}

// probeConversationsPagination prints whether Tumblr returns older inbox pages
// (_links / before / offset). Used by -probe-inbox.
func probeConversationsPagination(client *http.Client, auth Auth) {
	warmMessagesInbox(client, &auth)
	type try struct {
		label string
		extra map[string]string
	}
	tries := []try{
		{label: "limit=20", extra: map[string]string{"limit": "20"}},
		{label: "limit=50", extra: map[string]string{"limit": "50"}},
		{label: "offset=20", extra: map[string]string{"limit": "20", "offset": "20"}},
	}
	var page1IDs map[string]bool
	var page1Oldest string
	for i := 0; i < len(tries); i++ {
		t := tries[i]
		u, _ := url.Parse("https://www.tumblr.com/api/v2/conversations")
		q := u.Query()
		q.Set("participant", auth.Blog)
		q.Set("fields[blogs]", "?avatar,name,?uuid,url")
		for k, v := range t.extra {
			q.Set(k, v)
		}
		u.RawQuery = q.Encode()
		req, err := http.NewRequest("GET", u.String(), nil)
		if err != nil {
			fmt.Println(t.label, err)
			continue
		}
		setCommonHeaders(req, auth)
		req.Header.Set("X-CSRF", auth.CSRF)
		req.Header.Set("Referer", "https://www.tumblr.com/messages")
		resp, err := client.Do(req)
		if err != nil {
			fmt.Println(t.label, err)
			continue
		}
		body, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		fmt.Printf("\n=== %s  %s  %d bytes ===\n", t.label, resp.Status, len(body))
		var raw map[string]any
		if json.Unmarshal(body, &raw) != nil {
			fmt.Println(truncate(string(body), 300))
			continue
		}
		respObj, _ := raw["response"].(map[string]any)
		if respObj == nil {
			fmt.Println("no response")
			continue
		}
		keys := make([]string, 0, len(respObj))
		for k := range respObj {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		fmt.Println("response keys:", keys)
		if links, ok := respObj["_links"]; ok {
			b, _ := json.MarshalIndent(links, "", "  ")
			fmt.Println("_links:", string(b))
		} else {
			fmt.Println("_links: (none)")
		}
		if links, ok := respObj["links"]; ok {
			b, _ := json.Marshal(links)
			fmt.Println("links raw:", truncate(string(b), 800))
		} else {
			fmt.Println("links: (none)")
		}
		convs, _ := respObj["conversations"].([]any)
		fmt.Printf("conversations: %d\n", len(convs))
		ids := map[string]bool{}
		oldest := ""
		for _, el := range convs {
			m, _ := el.(map[string]any)
			if m == nil {
				continue
			}
			id, _ := m["id"].(string)
			ids[id] = true
			ts := ""
			if msgs, ok := m["messages"].(map[string]any); ok {
				if data, ok := msgs["data"].([]any); ok && len(data) > 0 {
					if dm, ok := data[0].(map[string]any); ok {
						ts, _ = dm["ts"].(string)
					}
				}
			}
			if ts != "" && (oldest == "" || ts < oldest) {
				oldest = ts
			}
		}
		if i == 0 {
			page1IDs = ids
			page1Oldest = oldest
			fmt.Println("oldest preview ts:", oldest)
			if oldest != "" {
				sec := oldest
				if len(sec) > 10 {
					sec = sec[:10]
				}
				tries = append(tries,
					try{label: "before=ms " + oldest, extra: map[string]string{"limit": "20", "before": oldest}},
					try{label: "before=sec " + sec, extra: map[string]string{"limit": "20", "before": sec}},
				)
			}
		} else if page1IDs != nil {
			same := 0
			for id := range ids {
				if page1IDs[id] {
					same++
				}
			}
			fmt.Printf("overlap with page1: %d / %d\n", same, len(ids))
			_ = page1Oldest
		}
	}
}
