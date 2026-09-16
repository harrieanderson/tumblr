package main

import (
	"fmt"
	"net/http"
	"os"
	"strings"
	"time"
)

const (
	delayBetweenPosts = 1 * time.Minute
	keepAliveEvery    = 10 * time.Minute
)

func main() {
	if err := prepareWorkspace(); err != nil {
		fmt.Println("Setup error:", err)
		return
	}
	keepMachineAwake()
	if os.Getenv("TUMBLR_HAVE_AGEKEY") == "1" {
		port := os.Getenv("TUMBLR_DEBUG_PORT")
		if err := clickHaveAgeKeyOnPort(port); err != nil {
			fmt.Println(err)
		}
		return
	}
	if pin := strings.TrimSpace(os.Getenv("TUMBLR_AGEKEY_PIN")); pin != "" {
		port := os.Getenv("TUMBLR_DEBUG_PORT")
		if err := submitAgeKeyPasskeyPIN(port, pin); err != nil {
			fmt.Println(err)
		}
		return
	}
	if os.Getenv("TUMBLR_FIX_LOOK") == "1" {
		port := os.Getenv("TUMBLR_DEBUG_PORT")
		if acc := strings.TrimSpace(os.Getenv("TUMBLR_ACCOUNT")); acc != "" {
			if err := setActiveAccount(acc); err != nil {
				fmt.Println(err)
				return
			}
		}
		if err := applyBlogLookOnPort(port); err != nil {
			fmt.Println(err)
		}
		return
	}
	if os.Getenv("TUMBLR_DISMISS") == "1" {
		port := os.Getenv("TUMBLR_DEBUG_PORT")
		if strings.TrimSpace(port) != "" {
			liveDebugPort = port
		}
		dismissOverlaysOnPort()
		return
	}
	if os.Getenv("TUMBLR_CLICK_OK") == "1" {
		port := os.Getenv("TUMBLR_DEBUG_PORT")
		if acc := strings.TrimSpace(os.Getenv("TUMBLR_ACCOUNT")); acc != "" {
			if err := setActiveAccount(acc); err != nil {
				fmt.Println(err)
				return
			}
		}
		if err := clickAgeConfirmOnPort(port); err != nil {
			fmt.Println(err)
		}
		return
	}
	if os.Getenv("TUMBLR_FINISH") == "1" {
		if acc := strings.TrimSpace(os.Getenv("TUMBLR_ACCOUNT")); acc != "" {
			if err := setActiveAccount(acc); err != nil {
				fmt.Println(err)
				return
			}
		}
		if err := finishAccountSettings(); err != nil {
			fmt.Println(err)
		}
		return
	}
	if os.Getenv("TUMBLR_MATURE") == "1" {
		port := os.Getenv("TUMBLR_DEBUG_PORT")
		if acc := strings.TrimSpace(os.Getenv("TUMBLR_ACCOUNT")); acc != "" {
			if err := setActiveAccount(acc); err != nil {
				fmt.Println(err)
				return
			}
		}
		if err := applyMatureContentOnPort(port); err != nil {
			fmt.Println(err)
		}
		return
	}

	loginOnly := false
	guiOnly := false
	sessionOnly := false
	scrapeOnly := false
	followOnly := false
	photoOnly := false
	humanizeOnly := false
	messagesOnly := false
	messagesReply := false
	postsOnly := false
	photoFile := ""
	photoCaption := ""
	debugSearch := ""
	seedScan := false
	probeMsg := false
	likeNsfwOnly := false
	safeModeOff := false
	setAvatarsOnly := false
	setupProfileOnly := false
	whoamiOnly := false
	activityOnly := false
	interactivePost := false
	accountFlag := ""
	parallelN := 0
	args := os.Args[1:]
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "-login":
			loginOnly = true
		case "-gui":
			guiOnly = true
		case "-session":
			sessionOnly = true
		case "-scrape":
			scrapeOnly = true
		case "-follow":
			followOnly = true
		case "-messages":
			messagesOnly = true
		case "-messages-reply":
			messagesReply = true
		case "-photo":
			photoOnly = true
		case "-humanize":
			humanizeOnly = true
		case "-posts":
			postsOnly = true
		case "-seed-scan":
			seedScan = true
		case "-probe-msg":
			probeMsg = true
		case "-like-nsfw":
			likeNsfwOnly = true
		case "-safe-mode-off":
			safeModeOff = true
		case "-set-avatars":
			setAvatarsOnly = true
		case "-setup-profile":
			setupProfileOnly = true
		case "-whoami":
			whoamiOnly = true
		case "-activity":
			activityOnly = true
		case "-post":
			interactivePost = true
		case "-account":
			if i+1 < len(args) {
				accountFlag = args[i+1]
				i++
			}
		case "-from":
			if i+1 < len(args) {
				accountStartFrom = args[i+1]
				i++
			}
		case "-for":
			if i+1 < len(args) {
				d, err := parseForArg(args[i+1])
				if err != nil {
					fmt.Println(err)
					return
				}
				setSessionFor(d)
				i++
			}
		case "-parallel":
			if i+1 < len(args) && !strings.HasPrefix(args[i+1], "-") {
				fmt.Sscanf(args[i+1], "%d", &parallelN)
				i++
			} else {
				parallelN = 5
			}
		case "-photo-file":
			if i+1 < len(args) {
				photoFile = args[i+1]
				i++
			}
		case "-caption":
			if i+1 < len(args) {
				photoCaption = args[i+1]
				i++
			}
		case "-debug-search":
			if i+1 < len(args) {
				debugSearch = args[i+1]
				i++
			} else {
				debugSearch = "nsfw"
			}
		}
	}

	if err := applyAccountFlag(accountFlag); err != nil {
		fmt.Println("Account error:", err)
		return
	}

	auth, err := loadAuth()
	if err != nil {
		fmt.Println("Error loading auth:", err)
		return
	}

	if loginOnly || setupProfileOnly {
		nativeChromeClicks = true
	}
	if followOnly || likeNsfwOnly {
		// Reach may open one Chrome at a time to mint a new cookie, but never
		// steal the mouse. Activity/whoami stay HTTP-only.
		quietChromeLogin = true
		nativeChromeClicks = false
	}
	if activityOnly || whoamiOnly {
		skipChromeRecovery = true
		nativeChromeClicks = false
	}

	if loginOnly {
		loginAll := runAllAccounts || (accountFlag == "" && len(enabledAccounts()) > 1)
		var loginErr error
		if loginAll {
			loginErr = loginAllAccounts()
		} else {
			loginErr = loginWithRealChrome(&auth)
		}
		if loginErr != nil {
			fmt.Println("Login/sync failed:", loginErr)
			return
		}
		fmt.Println("Login OK — ready. Run go run . for a session, or go run . -gui")
		return
	}

	if acc, ok := currentAccount(); ok {
		fmt.Println("Active account:", acc.ID, "("+acc.Email+")")
	}

	if !blogConfigured(auth.Blog) && !guiOnly && !runAllAccounts && !likeNsfwOnly && !safeModeOff && !setAvatarsOnly && !setupProfileOnly && !whoamiOnly && !activityOnly && !interactivePost && photoFile == "" {
		fmt.Println("This account is not logged in yet. Run: go run . -login")
		if acc, ok := currentAccount(); ok {
			fmt.Println("  or just this profile: go run . -login -account", acc.ID)
		}
		return
	}

	if guiOnly {
		_ = auth
		runGUI()
		return
	}

	if debugSearch != "" {
		runDebugSearch(debugSearch)
		return
	}

	if seedScan {
		seeds := []string{"lujuria-zen", "toetobrate", "arhaicna"}
		// Optional: go run . -seed-scan blog1 blog2 ...
		rest := []string{}
		for _, a := range args {
			if strings.HasPrefix(a, "-") {
				continue
			}
			rest = append(rest, a)
		}
		if len(rest) > 0 {
			seeds = rest
		}
		runSeedScan(seeds)
		return
	}

	if probeMsg {
		runProbeMsg()
		return
	}

	if scrapeOnly {
		runScrape()
		return
	}

	if followOnly {
		if parallelN <= 0 && (runAllAccounts || accountFlag == "") {
			parallelN = 0
		}
		runReachPass := func() {
			if parallelN > 0 {
				accs := loggedInUniqueAccounts(parallelN)
				fmt.Printf("Parallel reach on %d accounts (NSFW-tag likers)\n", len(accs))
				_ = runForAccountsParallel("follow", accs, func(a Account) error {
					return runFollowLikersOn(a, 0, 0, nil)
				})
				return
			}
			if runAllAccounts {
				_ = runForEachAccount("follow", func() error {
					runFollowLikers()
					return nil
				})
				return
			}
			runFollowLikers()
		}
		runReachPass()
		for !sessionUntil.IsZero() && !sessionExpired() {
			left := sessionRemaining()
			if left < 2*time.Minute {
				break
			}
			fmt.Printf("Time left %s — another reach pass.\n", left.Round(time.Minute))
			runReachPass()
		}
		if sessionExpired() {
			fmt.Println("Reach time is up.")
		}
		return
	}

	if messagesOnly {
		if runAllAccounts {
			_ = runForEachAccount("messages", func() error {
				runMessages()
				return nil
			})
			return
		}
		runMessages()
		return
	}

	if messagesReply {
		if runAllAccounts {
			_ = runForEachAccount("messages-reply", func() error {
				runMessagesReply()
				return nil
			})
			return
		}
		runMessagesReply()
		return
	}

	if whoamiOnly {
		one := func() error { return runWhoami() }
		if runAllAccounts || accountFlag == "" {
			_ = runForEachAccount("whoami", one)
			return
		}
		if err := one(); err != nil {
			fmt.Println("Whoami:", err)
		}
		return
	}

	if activityOnly {
		one := func() error { return runActivityCheck() }
		if runAllAccounts || accountFlag == "" {
			_ = runForEachAccount("activity", one)
			return
		}
		if err := one(); err != nil {
			fmt.Println("Activity:", err)
		}
		return
	}

	if safeModeOff {
		off := func() error {
			return openSettingsForAgeVerify()
		}
		if accountFlag != "" && !runAllAccounts {
			if err := off(); err != nil {
				fmt.Println("Settings:", err)
			}
			return
		}
		accs := ageVerifyAccountList()
		if runAllAccounts {
			accs = enabledAccounts()
		}
		_ = runForEachAccountList("age verify / settings", accs, off)
		return
	}

	if setAvatarsOnly {
		setOne := func() error {
			return runSetAvatars()
		}
		if runAllAccounts || accountFlag == "" {
			_ = runForEachAccount("set avatar", setOne)
			return
		}
		if err := setOne(); err != nil {
			fmt.Println("Avatar:", err)
		}
		return
	}

	if setupProfileOnly {
		setup := func() error {
			return runSetupProfile()
		}
		if accountFlag != "" && !runAllAccounts {
			if err := setup(); err != nil {
				fmt.Println("Profile setup:", err)
			}
			return
		}
		accs := setupProfileAccountList()
		if runAllAccounts {
			accs = enabledAccounts()
		}
		_ = runForEachAccountList("setup profile", accs, setup)
		return
	}

	if likeNsfwOnly {
		count := 3
		likeSome := func() error {
			return guiLikeNSFW(newLogHub(), count)
		}
		if runAllAccounts || accountFlag == "" {
			_ = runForEachAccount("like nsfw posts", likeSome)
			return
		}
		if err := likeSome(); err != nil {
			fmt.Println("Like failed:", err)
		}
		return
	}

	if humanizeOnly || sessionOnly || len(args) == 0 || (accountFlag != "" && !loginOnly && !guiOnly && !scrapeOnly && !followOnly && !photoOnly && !messagesOnly && !messagesReply && !postsOnly && !likeNsfwOnly && !safeModeOff && !setAvatarsOnly && !setupProfileOnly && !whoamiOnly && !activityOnly && !interactivePost && photoFile == "" && debugSearch == "" && !seedScan && !probeMsg) {
		if runAllAccounts {
			_ = runForEachAccount("reach session", func() error {
				runHumanize()
				return nil
			})
			return
		}
		runHumanize()
		return
	}

	if len(args) >= 2 && args[0] == "-dump-blog" {
		runDumpBlog(args[1])
		return
	}

	if interactivePost {
		skipChromeRecovery = true
		nativeChromeClicks = false
		if err := runInteractivePhotoPost(); err != nil {
			fmt.Println("Post:", err)
		}
		return
	}

	if photoFile != "" {
		skipChromeRecovery = true
		nativeChromeClicks = false
		photoCaption = strings.ReplaceAll(photoCaption, `\n`, "\n")
		var err error
		if accountFlag != "" && !runAllAccounts {
			err = postPhotoOnAllBlogs(photoFile, photoCaption, map[string]string{})
		} else {
			err = publishMediaOnUniqueBlogs(photoFile, photoCaption)
		}
		if err != nil {
			fmt.Println("Photo:", err)
		}
		return
	}

	if photoOnly {
		runPhotoQueue()
		return
	}

	if postsOnly {
		runTextQueue()
		return
	}

	fmt.Println("Unknown flags. Try: go run .")
	fmt.Println("  (no flags)  reach session (seed blogs → engagers)")
	fmt.Println("  -messages        print Messages inbox (names + contents)")
	fmt.Println("  -messages-reply  auto-reply unread DMs (AI if configured)")
	fmt.Println("  -follow          likers on OF-girl posts + daddy tags: like them, sometimes follow")
	fmt.Println("  -for 60         stop after 60 minutes (or -for 1h); use with -follow")
	fmt.Println("  -parallel N      run N logged-in accounts at once (use with -follow; default 5 if flag has no number)")
	fmt.Println("  -gui        open GUI")
	fmt.Println("  -post       pick a pic from pics/posted, type a caption, post on every blog")
	fmt.Println("  -posts      text queue")
	fmt.Println("  -photo      photo queue")
	fmt.Println("  -like-nsfw     like a few NSFW posts on every account (saved cookies)")
	fmt.Println("  -set-avatars   set profile pics from pics/profile_pic on every account")
	fmt.Println("  -setup-profile avatar + Zoey name/bio + birthday + AgeKey (see setup-profile.bat)")
	fmt.Println("  -from N        start at account N (1-based) or ID; use with -setup-profile")
	fmt.Println("  -safe-mode-off open Settings (new accounts in config/age-verify-accounts.json, or -account all)")
	fmt.Println("  -login      log into the active account (or all, if several are configured)")
	fmt.Println("  -account ID   use one profile (or -account all)")
}

func runTextQueue() {
	auth, err := loadAuth()
	if err != nil {
		fmt.Println("Error loading config/auth.json:", err)
		return
	}

	fmt.Println("Syncing cookies from Chrome...")
	if err := syncCookiesFromChrome(&auth); err != nil {
		if authHasSID(auth) {
			fmt.Println("Chrome sync skipped:", err)
			fmt.Println("Using cookies already saved in config/auth.json")
		} else {
			fmt.Println("Cookie sync failed:", err)
			fmt.Println("Run once with: go run . -login")
			return
		}
	}

	client := &http.Client{}
	first := true

	for {
		text, err := peekPost()
		if err != nil {
			if first {
				fmt.Println("Error reading posts queue:", err)
			} else {
				fmt.Println("Queue empty — done.")
			}
			return
		}
		first = false

		if err := ensureSession(client, &auth); err != nil {
			fmt.Println("Session dead and could not recover:", err)
			fmt.Println("When you're back, run: go run . -login")
			return
		}

		fmt.Println("Posting:", text)
		if err := createPost(client, auth, text); err != nil {
			if isUnauthorized(err) {
				fmt.Println("Post got 401 — trying to recover session...")
				if err := ensureSession(client, &auth); err != nil {
					fmt.Println("Could not recover:", err)
					fmt.Println("When you're back, run: go run . -login")
					return
				}
				if err := createPost(client, auth, text); err != nil {
					fmt.Println("Error creating post:", err)
					return
				}
			} else {
				fmt.Println("Error creating post:", err)
				return
			}
		}

		if err := popPost(); err != nil {
			fmt.Println("Error updating queue:", err)
			return
		}

		if queueHasPosts() {
			fmt.Printf("Waiting %s before next post (keep-alive every %s)...\n", delayBetweenPosts, keepAliveEvery)
			if err := waitWithKeepAlive(client, &auth, delayBetweenPosts); err != nil {
				fmt.Println("Session died during wait:", err)
				fmt.Println("When you're back, run: go run . -login")
				return
			}
		}
	}
}

// ensureSession refreshes CSRF, and if that fails, recovers a working cookie
// (profile copy, then a real Chrome login if that profile is logged out).
func ensureSession(client *http.Client, auth *Auth) error {
	if err := refreshCSRF(client, auth); err != nil {
		fmt.Println("CSRF/cookie failed — recovering a working session…")
		if err := recoverSession(client, auth, false); err != nil {
			return err
		}
	}
	maybeFillBlog(client, auth)
	return nil
}

// recoverSession collects a working cookie:
//  1. copy whatever sid is still in this account’s Chrome profile (no window)
//  2. if Tumblr rejected that too, open that account’s Chrome, sign in, save a
//     new cookie, close Chrome. Reach does this one account at a time and
//     never moves the mouse. skipChromeRecovery (activity/whoami) stops at 1.
func recoverSession(client *http.Client, auth *Auth, forceLogin bool) error {
	chromeLoginMu.Lock()
	defer chromeLoginMu.Unlock()

	if !forceLogin {
		if err := syncCookiesFromChromeLocked(auth); err == nil {
			if err := refreshCSRF(client, auth); err == nil {
				maybeFillBlog(client, auth)
				if id := accountIDFromAuth(*auth); id != "" {
					sessionDead.Delete(id)
				}
				return nil
			}
		}
	}

	if skipChromeRecovery {
		return fmt.Errorf("session expired (no live cookie in profile; run -login)")
	}

	fmt.Println("Profile cookie is dead — opening Chrome to sign in and collect a new one…")
	time.Sleep(3 * time.Second)
	if err := loginChromeSession(auth, true); err != nil {
		return err
	}
	if err := refreshCSRF(client, auth); err != nil {
		return err
	}
	maybeFillBlog(client, auth)
	if id := accountIDFromAuth(*auth); id != "" {
		sessionDead.Delete(id)
	}
	return nil
}

func retryAfterCookie(client *http.Client, auth *Auth, logFn func(string), id string, err error, again func() error) error {
	if err == nil {
		return nil
	}
	if isTransientNet(err) {
		time.Sleep(2 * time.Second)
		if againErr := again(); againErr == nil || !isTransientNet(againErr) {
			if againErr == nil || !isCookieProblem(againErr) {
				return againErr
			}
			err = againErr
		} else {
			time.Sleep(4 * time.Second)
			if againErr := again(); againErr == nil || !isCookieProblem(againErr) {
				return againErr
			}
			err = againErr
		}
	}
	if !isCookieProblem(err) {
		return err
	}
	idKey := strings.TrimSpace(id)
	if idKey == "" {
		idKey = accountIDFromAuth(*auth)
	}
	if idKey != "" {
		if _, dead := sessionDead.Load(idKey); dead {
			return fmt.Errorf("session expired (run -login for this account)")
		}
	}

	// 401 + logout:false is usually a rotated CSRF. GET still works; POST does not.
	accLog(id, logFn, "401 — refreshing CSRF (no Chrome)…")
	if csrfErr := refreshCSRF(client, auth); csrfErr != nil {
		accLog(id, logFn, "CSRF refresh failed: %v", csrfErr)
	} else if againErr := again(); againErr == nil {
		accLog(id, logFn, "CSRF refresh worked — continuing")
		return nil
	} else if !isCookieProblem(againErr) {
		return againErr
	}

	accLog(id, logFn, "Cookie/session failed — recovering a working cookie…")
	if rec := recoverSession(client, auth, false); rec != nil {
		accLog(id, logFn, "Could not collect a new cookie: %v", rec)
		if idKey != "" {
			sessionDead.Store(idKey, true)
		}
		return rec
	}
	if idKey != "" {
		sessionDead.Delete(idKey)
	}
	accLog(id, logFn, "Got a fresh session — retrying")
	return again()
}

func waitWithKeepAlive(client *http.Client, auth *Auth, total time.Duration) error {
	deadline := time.Now().Add(total)
	for {
		remaining := time.Until(deadline)
		if remaining <= 0 {
			return nil
		}

		sleep := keepAliveEvery
		if sleep > remaining {
			sleep = remaining
		}
		time.Sleep(sleep)

		if time.Now().After(deadline) || time.Until(deadline) < time.Second {
			return nil
		}

		fmt.Println("Keep-alive ping...")
		if err := ensureSession(client, auth); err != nil {
			return err
		}
	}
}

func isUnauthorized(err error) bool {
	return isCookieProblem(err)
}

func isCookieProblem(err error) bool {
	if err == nil {
		return false
	}
	s := strings.ToLower(err.Error())
	switch {
	case strings.Contains(s, "401"):
		return true
	case strings.Contains(s, "unauthorized"):
		return true
	case strings.Contains(s, "not logged"):
		return true
	case strings.Contains(s, "please log in"), strings.Contains(s, "please login"):
		return true
	case strings.Contains(s, "logged out"):
		return true
	case strings.Contains(s, "csrf"):
		return true
	case strings.Contains(s, "no sid"), strings.Contains(s, "no tumblr sid"):
		return true
	case strings.Contains(s, "cookie may be expired"):
		return true
	case strings.Contains(s, "session expired"):
		return true
	case strings.Contains(s, "chrome login skipped"):
		return true
	default:
		return false
	}
}

func isTransientNet(err error) bool {
	if err == nil {
		return false
	}
	s := strings.ToLower(err.Error())
	return strings.Contains(s, "no such host") ||
		strings.Contains(s, "temporary failure") ||
		strings.Contains(s, "i/o timeout") ||
		strings.Contains(s, "connection reset") ||
		strings.Contains(s, "tls handshake") ||
		strings.Contains(s, "unexpected eof")
}

func runOnePhoto(path, caption string) {
	auth, err := loadAuth()
	if err != nil {
		fmt.Println("Error loading auth:", err)
		return
	}
	if err := syncCookiesFromChrome(&auth); err != nil {
		if !authHasSID(auth) {
			fmt.Println("Cookie sync failed:", err)
			return
		}
		fmt.Println("Using saved cookies")
	}
	client := &http.Client{}
	if err := ensureSession(client, &auth); err != nil {
		fmt.Println("Session dead:", err)
		return
	}
	if caption == "" {
		caption = captionForPic(path)
	}
	fmt.Println("Posting photo:", path)
	if caption != "" {
		fmt.Println("Caption:", caption)
	}
	if err := createPhotoPost(client, auth, path, caption); err != nil {
		fmt.Println("Error:", err)
		return
	}
	fmt.Println("Done.")
}

func runPhotoQueue() {
	auth, err := loadAuth()
	if err != nil {
		fmt.Println("Error loading auth:", err)
		return
	}

	fmt.Println("Syncing cookies from Chrome...")
	if err := syncCookiesFromChrome(&auth); err != nil {
		if authHasSID(auth) {
			fmt.Println("Chrome sync skipped:", err)
			fmt.Println("Using cookies already saved in config/auth.json")
		} else {
			fmt.Println("Cookie sync failed:", err)
			fmt.Println("Run once with: go run . -login")
			return
		}
	}

	client := &http.Client{}
	first := true

	for {
		path, err := nextPic()
		if err != nil {
			if first {
				fmt.Println("Error reading pics:", err)
			} else {
				fmt.Println("Pics folder empty — done.")
			}
			return
		}
		first = false

		if err := ensureSession(client, &auth); err != nil {
			fmt.Println("Session dead:", err)
			return
		}

		caption := captionForPic(path)
		fmt.Println("Posting photo:", path)
		if caption != "" {
			fmt.Println("Caption:", caption)
		}

		if err := createPhotoPost(client, auth, path, caption); err != nil {
			fmt.Println("Error:", err)
			return
		}
		if err := markPicPosted(path); err != nil {
			fmt.Println("Posted, but failed to move file:", err)
			return
		}
		fmt.Println("Moved to", picsPosted+"/")

		if picsRemaining() {
			fmt.Printf("Waiting %s before next photo...\n", delayBetweenPosts)
			if err := waitWithKeepAlive(client, &auth, delayBetweenPosts); err != nil {
				fmt.Println("Session died during wait:", err)
				return
			}
		}
	}
}
