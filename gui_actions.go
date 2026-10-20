package main

import (
	"encoding/json"
	"fmt"
	"math/rand"
	"net/http"
	"os"
	"strings"
	"time"
)

func guiAuth(hub *logHub) (Auth, *http.Client, error) {
	if acc, ok := currentAccount(); ok {
		hub.broadcast("Using account " + acc.ID + " (" + acc.Email + ")")
	}
	auth, err := loadAuth()
	if err != nil {
		return Auth{}, nil, err
	}
	if !authHasSID(auth) {
		if err := syncCookiesFromChrome(&auth); err != nil {
			return Auth{}, nil, fmt.Errorf("not logged in: %w (run tumblr.exe -login first)", err)
		}
	} else {
		hub.broadcast("Using saved cookies")
	}
	client := &http.Client{Timeout: 45 * time.Second}
	if err := ensureSession(client, &auth); err != nil {
		return Auth{}, nil, err
	}
	name, err := verifyAccountBlog(client, &auth)
	if err != nil {
		return Auth{}, nil, err
	}
	hub.broadcast("Verified account blog: @" + name)
	return auth, client, nil
}

func guiFollowAndLike(hub *logHub, maxFollows, likePosts int) error {
	acc, _ := currentAccount()
	return runFollowLikersOn(acc, maxFollows, likePosts, hub.broadcast)
}

func guiLikeNSFW(hub *logHub, count int) error {
	auth, client, err := guiAuth(hub)
	if err != nil {
		return err
	}
	persistMaturePref(&auth)

	queries := []string{"nsfw", "lewd", "thirst", "gay nsfw", "smut"}
	var posts []ScrapedPost
	for _, q := range queries {
		hub.broadcast("Searching: " + q)
		found, err := scrapeRecentPosts(client, auth, q, count*3)
		if err != nil || len(found) == 0 {
			found, err = scrapeTopPosts(client, auth, q, count*3)
		}
		if err != nil {
			hub.broadcast("  search fail: " + err.Error())
			continue
		}
		if len(found) > 0 {
			posts = found
			break
		}
		hub.broadcast("  empty — trying next query")
	}
	if len(posts) == 0 {
		return fmt.Errorf("no NSFW posts found — check Safe Mode / mature content")
	}

	liked := 0
	seen := map[string]bool{}
	for _, p := range posts {
		if liked >= count {
			break
		}
		if p.ID == "" || seen[p.ID] {
			continue
		}
		seen[p.ID] = true
		if p.ReblogKey == "" {
			if enriched, e2 := fetchSinglePost(client, auth, p); e2 == nil {
				p = enriched
			}
		}
		if p.ReblogKey == "" {
			continue
		}
		hub.broadcast(fmt.Sprintf("Liking %s/%s — %s", p.Blog, p.ID, truncate(p.Summary, 40)))
		if err := likePost(client, auth, p.ID, p.ReblogKey); err != nil {
			if isDailyLikeLimit(err) {
				markLikeLimited(accountIDFromAuth(auth))
				hub.broadcast("  daily like limit reached — no more likes for this account today")
				break
			}
			hub.broadcast("  like fail: " + err.Error())
			continue
		}
		liked++
		time.Sleep(time.Duration(randRange(4, 12)) * time.Second)
	}
	if liked == 0 {
		return fmt.Errorf("could not like any NSFW post")
	}
	hub.broadcast(fmt.Sprintf("Liked %d NSFW posts", liked))
	return nil
}

func guiTextPost(hub *logHub, text string) error {
	auth, client, err := guiAuth(hub)
	if err != nil {
		return err
	}
	hub.broadcast("Posting text…")
	if err := createPost(client, auth, text); err != nil {
		return err
	}
	hub.broadcast("Text post published")
	return nil
}

func guiPhotoPost(hub *logHub, path, caption string, pin bool) error {
	auth, client, err := guiAuth(hub)
	if err != nil {
		return err
	}
	hub.broadcast("Posting photo…")
	if caption != "" {
		hub.broadcast("Caption: " + truncate(caption, 80))
	}
	if pin {
		hub.broadcast("Will pin this post on the blog")
	}
	if err := createPhotoPostPinned(client, auth, path, caption, pin); err != nil {
		return err
	}
	if pin {
		hub.broadcast("Photo post published and pinned")
	} else {
		hub.broadcast("Photo post published")
	}
	return nil
}

func guiLoginAccounts(hub *logHub, accs []Account) error {
	if len(accs) == 0 {
		return fmt.Errorf("select at least one account")
	}
	prevExplicit := explicitLogin
	explicitLogin = true
	defer func() { explicitLogin = prevExplicit }()
	var first error
	for i, a := range accs {
		if a.ID == "" {
			hub.broadcast("Logging in…")
			auth, err := loadAuth()
			if err != nil {
				if first == nil {
					first = err
				}
				continue
			}
			if err := loginWithRealChrome(&auth); err != nil {
				hub.broadcast("Login failed: " + err.Error())
				if first == nil {
					first = err
				}
			} else {
				hub.broadcast("Login OK")
				hub.broadcast("Chrome closes after Login selected — cookies are saved. Run full setup to keep the window open.")
			}
			continue
		}
		hub.broadcast(fmt.Sprintf("Login %d/%d: %s", i+1, len(accs), a.ID))
		if err := setActiveAccount(a.ID); err != nil {
			hub.broadcast(err.Error())
			if first == nil {
				first = err
			}
			continue
		}
		auth, err := loadAuth()
		if err != nil {
			hub.broadcast(err.Error())
			if first == nil {
				first = err
			}
			continue
		}
		if err := loginWithRealChrome(&auth); err != nil {
			hub.broadcast("Login failed on " + a.ID + ": " + err.Error())
			if first == nil {
				first = err
			}
			continue
		}
		hub.broadcast("Login OK: " + a.ID)
		hub.broadcast("Chrome closes after Login selected — cookies are saved. Run full setup to keep the window open.")
	}
	return first
}

func guiMessagesReply(hub *logHub, dry bool, watchHours float64, once bool) error {
	prevDry := messagesDryRun
	prevHours := messagesWatchHours
	prevOnce := messagesWatchOnce
	prevLog := messageUILog
	messagesDryRun = dry
	messagesWatchHours = watchHours
	messagesWatchOnce = once
	messageUILog = hub.broadcast
	defer func() {
		messagesDryRun = prevDry
		messagesWatchHours = prevHours
		messagesWatchOnce = prevOnce
		messageUILog = prevLog
	}()
	runMessagesReply()
	return nil
}

func guiMessagesReplyMany(hub *logHub, accs []Account, dry bool, watchHours float64, once bool) error {
	prevDry := messagesDryRun
	prevHours := messagesWatchHours
	prevOnce := messagesWatchOnce
	prevLog := messageUILog
	messagesDryRun = dry
	messagesWatchHours = watchHours
	messagesWatchOnce = once
	messageUILog = hub.broadcast
	defer func() {
		messagesDryRun = prevDry
		messagesWatchHours = prevHours
		messagesWatchOnce = prevOnce
		messageUILog = prevLog
	}()
	runMessagesReplyRoundRobin(accs)
	return nil
}

func oneAccountForChrome(accountID string, accountIDs []string) (Account, error) {
	accs, err := guiAccounts(accountID, accountIDs)
	if err != nil {
		return Account{}, err
	}
	if len(accs) != 1 || accs[0].ID == "" {
		return Account{}, fmt.Errorf("tick exactly one account to open Chrome")
	}
	return accs[0], nil
}

func guiOpenAccountChrome(hub *logHub, acc Account) error {
	if acc.ID == "" {
		return fmt.Errorf("tick exactly one account to open Chrome")
	}
	if err := setActiveAccount(acc.ID); err != nil {
		return err
	}

	port := debugPortForAccount(acc.ID)
	hub.broadcast("Opening Chrome for " + acc.ID + " on port " + port)

	// Watch must always show a real window, even if Hide Chrome is on.
	prevHeadless := headlessChrome
	headlessChrome = false
	defer func() { headlessChrome = prevHeadless }()

	showInbox := func() error {
		t, err := openOrNavigateTab("https://www.tumblr.com/inbox")
		if err != nil {
			hub.broadcast("Could not open Messages automatically: " + err.Error())
			hub.broadcast("In Chrome, go to tumblr.com/inbox")
			raiseMonitorChrome(chromeJSONTarget{}, acc.ID)
			return nil
		}
		raiseMonitorChrome(t, acc.ID)
		// Windows focus rules often ignore the first raise while Edge/GUI is foreground.
		for i := 0; i < 4; i++ {
			time.Sleep(400 * time.Millisecond)
			raiseMonitorChrome(t, acc.ID)
		}
		hub.broadcast("Chrome stayed open for " + acc.ID + " (Messages). Check the taskbar if it’s behind this window.")
		return nil
	}

	if portInUse(port) {
		liveDebugPort = port
		liveChromeAccount = acc.ID
		hub.broadcast("Chrome is already open for " + acc.ID + " — bringing it to the front")
		return showInbox()
	}

	prevSkip := skipChromeRecovery
	skipChromeRecovery = false
	defer func() { skipChromeRecovery = prevSkip }()

	prevClicks := nativeChromeClicks
	nativeChromeClicks = false
	defer func() { nativeChromeClicks = prevClicks }()

	auth, err := loadAuth()
	if err != nil {
		return err
	}
	if _, err := loginWithRealChromeKeepOpen(&auth); err != nil {
		return err
	}
	hub.broadcast("Login OK for " + acc.ID + " — Chrome stays open (unlike Login, which closes it).")
	return showInbox()
}

func guiFullAccountSetup(hub *logHub) error {
	prevLog := setupUILog
	setupUILog = hub.broadcast
	defer func() { setupUILog = prevLog }()

	prevSkip := skipChromeRecovery
	skipChromeRecovery = false
	defer func() { skipChromeRecovery = prevSkip }()

	prevClicks := nativeChromeClicks
	nativeChromeClicks = false
	defer func() { nativeChromeClicks = prevClicks }()

	hub.broadcast("Using " + setupCodeStamp)
	hub.broadcast("Account settings first: English → birthday 29 Dec 2000 → AgeKey. Appearance is last.")
	hub.broadcast("Chrome stays open until you close the window, then setup moves to the next account.")
	return runSetupProfile()
}

func raiseMonitorChrome(t chromeJSONTarget, accountID string) {
	if t.ID != "" {
		_ = withChromeTargetCDP(t, func(call cdpCall) error {
			_, _ = call("Page.bringToFront", nil)
			// Restore the OS window if it was minimized / stuck behind the GUI.
			raw, err := call("Browser.getWindowForTarget", map[string]any{"targetId": t.ID})
			if err != nil {
				return nil
			}
			var win struct {
				WindowID int `json:"windowId"`
			}
			if json.Unmarshal(raw, &win) != nil || win.WindowID == 0 {
				return nil
			}
			_, _ = call("Browser.setWindowBounds", map[string]any{
				"windowId": win.WindowID,
				"bounds":   map[string]any{"windowState": "normal"},
			})
			return nil
		})
	}
	needles := []string{accountID, "Tumblr", "Inbox", "Google Chrome"}
	if acc, ok := findAccount(accountID); ok {
		needles = append([]string{acc.ID, acc.Email}, needles...)
	}
	auth := peekAccountAuth(accountID)
	if usableBlogName(auth.Blog) {
		needles = append([]string{auth.Blog, "@" + auth.Blog}, needles...)
	}
	for _, needle := range needles {
		if needle == "" {
			continue
		}
		if hwnd := findWindowContaining(needle); hwnd != 0 {
			focusWindow(hwnd)
			return
		}
	}
	if hwnd := findChromeWindowForAccount(accountID); hwnd != 0 {
		focusWindow(hwnd)
	}
}

func guiReblog(hub *logHub, category string, count int) error {
	auth, client, err := guiAuth(hub)
	if err != nil {
		return err
	}
	cfg := loadHumanizeConfigFile()

	var queries []string
	switch strings.ToLower(category) {
	case "cats":
		queries = []string{"cats", "cute cats", "kitten"}
	case "nature":
		queries = []string{"nature", "landscape", "flowers"}
	case "cute":
		queries = []string{"cute", "wholesome", "aww"}
	case "horny", "nsfw":
		queries = []string{"nsfw", "thirst", "lewd"}
	default:
		cat := pickCategory(cfg.Categories)
		queries = cat.Queries
		hub.broadcast("Mix category picked: " + cat.Name)
	}
	if len(queries) == 0 {
		queries = []string{"cute"}
	}
	q := queries[rand.Intn(len(queries))]
	hub.broadcast("Reblog search: " + q)

	posts, err := scrapeRecentPosts(client, auth, q, count*3)
	if err != nil || len(posts) == 0 {
		posts, err = scrapeTopPosts(client, auth, q, count*3)
	}
	if err != nil {
		return err
	}

	reblogged := loadStringSet(rebloggedPath())
	done := 0
	for _, p := range posts {
		if done >= count {
			break
		}
		if p.ID == "" || reblogged[p.ID] {
			continue
		}
		if p.ReblogKey == "" {
			if enriched, e2 := fetchSinglePost(client, auth, p); e2 == nil {
				p = enriched
			}
		}
		if p.ReblogKey == "" {
			continue
		}
		hub.broadcast(fmt.Sprintf("Reblogging %s/%s — %s", p.Blog, p.ID, truncate(p.Summary, 40)))
		if err := reblogPost(client, auth, p); err != nil {
			hub.broadcast("  reblog fail: " + err.Error())
			continue
		}
		reblogged[p.ID] = true
		_ = saveStringSet(rebloggedPath(), reblogged)
		done++
		time.Sleep(time.Duration(randRange(8, 20)) * time.Second)
	}
	hub.broadcast(fmt.Sprintf("Reblogged %d posts", done))
	return nil
}

func loadHumanizeConfigFile() HumanizeConfig {
	cfg := defaultHumanizeConfig()
	raw, err := os.ReadFile(humanizeConfigFile)
	if err != nil {
		return cfg
	}
	_ = json.Unmarshal(raw, &cfg)
	normalizeHumanizeConfig(&cfg)
	return cfg
}
