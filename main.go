package main

import (
	"fmt"
	"net/http"
	"os"
	"path/filepath"
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
	loadHeadlessPref()
	if os.Getenv("TUMBLR_HAVE_AGEKEY") == "1" {
		nativeChromeClicks = false
		port := os.Getenv("TUMBLR_DEBUG_PORT")
		if strings.TrimSpace(port) != "" {
			liveDebugPort = port
		}
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
		nativeChromeClicks = false
		port := os.Getenv("TUMBLR_DEBUG_PORT")
		if strings.TrimSpace(port) != "" {
			liveDebugPort = port
		}
		if acc := strings.TrimSpace(os.Getenv("TUMBLR_ACCOUNT")); acc != "" {
			if err := setActiveAccount(acc); err != nil {
				fmt.Println(err)
				return
			}
		}
		if appearanceEditorOpenOnPort(port) {
			fmt.Println("Appearance editor is open — clicking Save.")
			if err := clickAppearanceSaveOnPort(port); err != nil {
				fmt.Println(err)
			}
			time.Sleep(3 * time.Second)
		}
		closeExtraSetupTabs()
		auth, err := loadAuth()
		if err == nil {
			name, _ := syncAuthIdentity(newHumanClient(), &auth)
			if !usableBlogName(name) {
				name = resolveSetupBlogName(auth, name)
			}
			if usableBlogName(name) {
				if look := lookStatusFromAPI(name); look.Complete() {
					fmt.Println("Confirmed name, bio, and avatar.")
					return
				}
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
	probeInbox := false
	postsOnly := false
	photoFile := ""
	photoCaption := ""
	photoPin := false
	photoTopFollowers := 0
	busiestN := 0
	photoFullySetup := false
	textPost := ""
	blazingothNext := false
	researchOnly := false
	researchDays := 14
	researchSync := true
	debugSearch := ""
	seedScan := false
	probeMsg := false
	probeReblog := false
	probeNotes := false
	likeNsfwOnly := false
	safeModeOff := false
	setAvatarsOnly := false
	setupProfileOnly := false
	blogLookOnly := false
	fixBioOnly := false
	openChromeOnly := false
	checkZoeyOnly := false
	wipePostsOnly := false
	wipeKeepPinned := true
	wipeDryRun := false
	listFollowersOnly := false
	whoamiOnly := false
	activityOnly := false
	growthOnly := false
	interactivePost := false
	postedOnly := false
	backlogOnly := false
	accountFlag := ""
	parallelN := 0
	args := os.Args[1:]
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "-login":
			loginOnly = true
			explicitLogin = true
		case "-gui":
			guiOnly = true
		case "-headless":
			headlessChrome = true
		case "-headless-cookies":
			// Scrape sid from chrome-data profiles headlessly; never pop a
			// visible login window (won't steal focus from a game).
			headlessCookieOnly = true
			skipChromeRecovery = true
		case "-session":
			sessionOnly = true
		case "-scrape":
			scrapeOnly = true
		case "-follow":
			followOnly = true
		case "-with-replies":
			reachWithReplies = true
		case "-messages":
			messagesOnly = true
		case "-messages-reply":
			messagesReply = true
		case "-probe-inbox":
			probeInbox = true
		case "-once":
			messagesWatchOnce = true
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
		case "-probe-reblog":
			probeReblog = true
		case "-probe-notes":
			probeNotes = true
		case "-like-nsfw":
			likeNsfwOnly = true
		case "-safe-mode-off":
			safeModeOff = true
		case "-set-avatars":
			setAvatarsOnly = true
		case "-setup-profile":
			setupProfileOnly = true
		case "-skip-agekey":
			skipSetupAgeKey = true
		case "-setup-accounts":
			if i+1 < len(args) {
				setupProfileAccountsFile = strings.TrimSpace(args[i+1])
				i++
			}
		case "-blog-look":
			blogLookOnly = true
		case "-fix-bio":
			fixBioOnly = true
		case "-open":
			openChromeOnly = true
		case "-check-zoey":
			checkZoeyOnly = true
		case "-wipe-posts":
			wipePostsOnly = true
		case "-include-pinned":
			wipeKeepPinned = false
		case "-dry-run":
			wipeDryRun = true
			messagesDryRun = true
		case "-followers":
			listFollowersOnly = true
		case "-whoami":
			whoamiOnly = true
		case "-activity":
			activityOnly = true
		case "-growth":
			growthOnly = true
		case "-post":
			interactivePost = true
		case "-posted":
			postedOnly = true
		case "-backlog":
			backlogOnly = true
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
		case "-pin":
			photoPin = true
		case "-fully-setup":
			photoFullySetup = true
		case "-busiest":
			busiestN = 5
			if i+1 < len(args) && !strings.HasPrefix(args[i+1], "-") {
				fmt.Sscanf(args[i+1], "%d", &busiestN)
				i++
			}
		case "-top-followers":
			photoTopFollowers = 25
			if i+1 < len(args) && !strings.HasPrefix(args[i+1], "-") {
				fmt.Sscanf(args[i+1], "%d", &photoTopFollowers)
				i++
			}
		case "-text":
			if i+1 >= len(args) {
				fmt.Println("-text needs the post body, e.g. -text \"hello\"")
				return
			}
			textPost = args[i+1]
			i++
		case "-blazingoth-next":
			blazingothNext = true
		case "-research":
			researchOnly = true
			if i+1 < len(args) && !strings.HasPrefix(args[i+1], "-") {
				fmt.Sscanf(args[i+1], "%d", &researchDays)
				i++
			}
		case "-no-sync":
			researchSync = false
		case "-new-pinned":
			photoFile = filepath.Join(picsPosted, "new_pinned1.jpg")
			photoCaption = zoeyNewPinnedCaption()
			photoPin = true
			// All available logged-in unique blogs (override with -top-followers / -fully-setup).
			runAllAccounts = true
		case "-debug-search":
			if i+1 < len(args) {
				debugSearch = args[i+1]
				i++
			} else {
				debugSearch = "nsfw"
			}
		}
	}

	if postedOnly {
		printPostLedger(strings.TrimSpace(accountFlag))
		return
	}
	if backlogOnly {
		printDMBacklog()
		return
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
		nativeChromeClicks = false
	}
	if blogLookOnly || fixBioOnly || openChromeOnly {
		nativeChromeClicks = false
	}
	if followOnly || likeNsfwOnly {
		// Reach may open one Chrome at a time to mint a new cookie, but never
		// steal the mouse. Activity/whoami stay HTTP-only.
		quietChromeLogin = true
		nativeChromeClicks = false
	}
	if activityOnly || whoamiOnly || checkZoeyOnly || listFollowersOnly || growthOnly || researchOnly {
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

	if openChromeOnly {
		if accountFlag == "" && !runAllAccounts {
			if acc, ok := currentAccount(); ok {
				accountFlag = acc.ID
			}
		}
		if accountFlag == "" {
			fmt.Println("Use: go run . -open -account ACCOUNTID")
			return
		}
		stop, err := loginWithRealChromeKeepOpen(&auth)
		if err != nil {
			fmt.Println("Login failed:", err)
			return
		}
		inbox := "https://www.tumblr.com/inbox"
		if _, err := openOrNavigateTab(inbox); err != nil {
			fmt.Println("Could not open Messages automatically:", err)
			fmt.Println("In Chrome, go to tumblr.com/inbox")
		} else {
			fmt.Println("Chrome is on Messages. Have a look at the DMs.")
		}
		fmt.Println("Close the Chrome window when you are done.")
		for liveDebugPort != "" && portInUse(liveDebugPort) {
			time.Sleep(500 * time.Millisecond)
		}
		if stop != nil {
			stop()
		}
		return
	}

	if acc, ok := currentAccount(); ok {
		fmt.Println("Active account:", acc.ID, "("+acc.Email+")")
	}

	if !blogConfigured(auth.Blog) && !guiOnly && !runAllAccounts && !likeNsfwOnly && !safeModeOff && !setAvatarsOnly && !setupProfileOnly && !blogLookOnly && !fixBioOnly && !openChromeOnly && !checkZoeyOnly && !wipePostsOnly && !whoamiOnly && !activityOnly && !growthOnly && !listFollowersOnly && !researchOnly && !blazingothNext && !interactivePost && photoFile == "" {
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

	if probeReblog {
		runProbeReblog()
		return
	}

	if probeNotes {
		runProbeNotes()
		return
	}

	if scrapeOnly {
		runScrape()
		return
	}

	if followOnly {
		if photoTopFollowers > 0 {
			reachPoolSize = photoTopFollowers
		}
		if reachWithReplies {
			messagesWatchOnce = true
			fmt.Println(aiBudgetSummary())
			fmt.Println("Age gate:", minorBlockSummary())
		}
		chosen := accountsFromCommaFlag(accountFlag)
		if parallelN <= 0 && (runAllAccounts || accountFlag == "") {
			parallelN = 0
		}
		runReachPass := func() {
			if len(chosen) > 1 {
				reachShardPool = chosen
				reachLikerShards = len(chosen)
				fmt.Printf("Reach on %d chosen blogs so they get likes/reblogs/follows today\n", len(chosen))
				for i, a := range chosen {
					fmt.Printf("  %d) %s\n", i+1, a.ID)
				}
				_ = runForAccountsParallel("follow", chosen, func(a Account) error {
					return runFollowLikersOn(a, 0, 0, nil)
				})
				reachShardPool = nil
				reachLikerShards = 0
				return
			}
			if parallelN > 0 {
				accs := rotateLoggedInForReach(parallelN)
				reachShardPool = accs
				reachLikerShards = len(accs)
				var until time.Time
				if slice := reachBatchSlice(reachWindow, len(reachAccountPool()), parallelN); slice > 0 {
					until = time.Now().Add(slice)
					fmt.Printf("Parallel reach on %d accounts for %s, then the next %d in the top-%d pool\n", len(accs), slice.Round(time.Minute), parallelN, reachPoolSize)
				} else {
					fmt.Printf("Parallel reach on %d accounts (top-follower pool up to %d), rotating each pass\n", len(accs), reachPoolSize)
				}
				for i, a := range accs {
					fmt.Printf("  %d) %s\n", i+1, a.ID)
				}
				_ = runForAccountsParallel("follow", accs, func(a Account) error {
					return reachTurn(a, until)
				})
				reachShardPool = nil
				reachLikerShards = 0
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
		if !sessionUntil.IsZero() {
			reachWindow = sessionRemaining()
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

	if probeInbox {
		auth, err := loadAuth()
		if err != nil {
			fmt.Println("auth:", err)
			return
		}
		client := newHumanClient()
		if err := ensureSession(client, &auth); err != nil {
			fmt.Println("session:", err)
			return
		}
		if acc, ok := currentAccount(); ok {
			if n := assignedBlogName(acc.ID); n != "" {
				_, _ = pinMessagingBlog(client, &auth, n)
			}
		}
		fmt.Println("Probing conversations pagination for", auth.Blog)
		probeConversationsPagination(client, auth)
		return
	}

	if messagesReply {
		fmt.Println(aiBudgetSummary())
		fmt.Println("Age gate:", minorBlockSummary())
		if busiestN > 0 {
			var pool []Account
			for _, a := range enabledAccounts() {
				if !accountIsTerminated(a.ID) {
					pool = append(pool, a)
				}
			}
			accs, rows := busiestInboxAccounts(pool, busiestN)
			fmt.Printf("Messages-reply on the %d busiest inboxes\n", len(accs))
			for _, r := range rows {
				for _, a := range accs {
					if a.ID == r.Account.ID {
						fmt.Printf("  %s  %d waiting\n", a.ID, r.Waiting)
					}
				}
			}
			switch len(accs) {
			case 0:
				fmt.Println("No inbox has chats waiting")
			case 1:
				runMessagesReplyOnAccount(accs[0])
			default:
				runMessagesReplyRoundRobin(accs)
			}
			return
		}
		if photoTopFollowers > 0 {
			accs := loggedInTopFollowerAccounts(photoTopFollowers)
			fmt.Printf("Messages-reply on top %d follower blogs (%d found)\n", photoTopFollowers, len(accs))
			for i, a := range accs {
				blog, fol := bestAccountBlogStats(a.ID, loadFollowerLog(a.ID))
				fmt.Printf("  %d) %s  @%s  %d followers\n", i+1, a.ID, blog, fol)
			}
			if len(accs) == 0 {
				fmt.Println("No logged-in blogs for messages-reply")
				return
			}
			if len(accs) == 1 {
				runMessagesReplyOnAccount(accs[0])
			} else {
				runMessagesReplyRoundRobin(accs)
			}
			return
		}
		if runAllAccounts {
			accs := enabledAccounts()
			if len(accs) > 1 {
				runMessagesReplyRoundRobin(accs)
			} else {
				_ = runForEachAccount("messages-reply", func() error {
					runMessagesReply()
					return nil
				})
			}
			return
		}
		if chosen := accountsFromCommaFlag(accountFlag); len(chosen) > 1 {
			runMessagesReplyRoundRobin(chosen)
			return
		}
		runMessagesReply()
		return
	}

	if researchOnly {
		if err := runResearch(researchDays, researchSync, func(s string) { fmt.Println(s) }); err != nil {
			fmt.Println("Research:", err)
		}
		return
	}

	if listFollowersOnly {
		if err := runListFollowers(); err != nil {
			fmt.Println("Followers:", err)
		}
		return
	}

	if checkZoeyOnly {
		if err := runCheckZoeySetup(); err != nil {
			fmt.Println("Zoey check:", err)
		}
		return
	}

	if wipePostsOnly {
		only := ""
		if accountFlag != "" && !runAllAccounts {
			only = accountFlag
		}
		par := parallelN
		if par <= 0 {
			par = 6
		}
		if err := runWipePosts(wipeKeepPinned, wipeDryRun, photoFullySetup, only, par); err != nil {
			fmt.Println("Wipe posts:", err)
		}
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

	if growthOnly {
		if err := runGrowthReport(); err != nil {
			fmt.Println("Growth:", err)
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

	if blogLookOnly {
		one := func() error {
			return runBlogLook()
		}
		if accountFlag != "" && !runAllAccounts {
			if err := one(); err != nil {
				fmt.Println("Blog look:", err)
			}
			return
		}
		accs := loggedInUniqueAccounts(20)
		if runAllAccounts {
			accs = enabledAccounts()
		}
		fmt.Printf("Blog look on %d account(s): username + banner + purple/black theme\n", len(accs))
		_ = runForEachAccountList("blog look", accs, one)
		return
	}

	if fixBioOnly {
		one := func() error {
			return runFixColorHashBio()
		}
		if accountFlag != "" && !runAllAccounts {
			if err := one(); err != nil {
				fmt.Println("Fix bio:", err)
			}
			return
		}
		accs := accountIDsFromFile(fixBioAccountsFile, "Fix bio hashes")
		if runAllAccounts {
			accs = enabledAccounts()
		}
		fmt.Printf("Removing colour hashes from bios on %d account(s)\n", len(accs))
		_ = runForEachAccountList("fix bio", accs, one)
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

	if humanizeOnly || sessionOnly || len(args) == 0 || (accountFlag != "" && !loginOnly && !guiOnly && !scrapeOnly && !followOnly && !photoOnly && !messagesOnly && !messagesReply && !postsOnly && !likeNsfwOnly && !safeModeOff && !setAvatarsOnly && !setupProfileOnly && !blogLookOnly && !fixBioOnly && !openChromeOnly && !whoamiOnly && !activityOnly && !growthOnly && !interactivePost && photoFile == "" && textPost == "" && debugSearch == "" && !seedScan && !probeMsg && !probeReblog && !probeNotes) {
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

	if blazingothNext {
		skipChromeRecovery = true
		nativeChromeClicks = false
		n := photoTopFollowers
		if n <= 0 {
			n = 80
		}
		runBlazingothNext(n)
		return
	}

	if textPost != "" {
		skipChromeRecovery = true
		nativeChromeClicks = false
		textPost = strings.ReplaceAll(textPost, `\n`, "\n")
		if chosen := accountsFromCommaFlag(accountFlag); len(chosen) > 0 {
			fmt.Printf("Text post on %d chosen accounts\n", len(chosen))
			fmt.Println("Text:", truncate(textPost, 120))
			if err := runForEachAccountList("text post", chosen, func() error {
				return postTextOnAssignedBlog(textPost)
			}); err != nil {
				fmt.Println("Text:", err)
			}
			return
		}
		if accountFlag != "" && !runAllAccounts {
			fmt.Println("Text:", truncate(textPost, 120))
			if err := postTextOnAssignedBlog(textPost); err != nil {
				fmt.Println("Text:", err)
			}
			return
		}
		n := photoTopFollowers
		if n <= 0 {
			n = 21
		}
		if err := publishTextOnTopFollowerBlogs(textPost, n); err != nil {
			fmt.Println("Text:", err)
		}
		return
	}

	if photoFile != "" {
		skipChromeRecovery = true
		nativeChromeClicks = false
		photoCaption = strings.ReplaceAll(photoCaption, `\n`, "\n")
		var err error
		if accountFlag != "" && !runAllAccounts {
			err = postPhotoOnAllBlogsPinned(photoFile, photoCaption, map[string]string{}, photoPin)
		} else if photoFullySetup {
			err = publishMediaOnFullySetupBlogs(photoFile, photoCaption, photoPin)
		} else if photoTopFollowers > 0 {
			err = publishMediaOnTopFollowerBlogs(photoFile, photoCaption, photoTopFollowers, photoPin)
		} else {
			err = publishMediaOnUniqueBlogsPinned(photoFile, photoCaption, photoPin)
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
	fmt.Println("  -messages-reply  auto-reply DMs and keep watching for new ones")
	fmt.Println("  -top-followers [N]  with -messages-reply: only top N blogs; with -photo/-text: post on top N")
	fmt.Println("  -busiest [N]     with -messages-reply: the N inboxes with most chats waiting (default 5)")
	fmt.Println("  -once            with -messages-reply: one inbox pass, then stop")
	fmt.Println("  -dry-run         with -messages-reply: draft replies, do not send")
	fmt.Println("  -headless        keep bot Chrome hidden (logins with a captcha will fail)")
	fmt.Println("  -backlog         list accounts by how many chats are waiting on a reply")
	fmt.Println("  -for 9h          stop watching after 9 hours (default 9h when sending)")
	fmt.Println("  -follow          seed/tag post engagers (rebloggers first, then likers): like them, sometimes follow")
	fmt.Println("  -growth          score follower gains, park losers, reorder reach + seed blogs")
	fmt.Println("  -for 60         stop after 60 minutes (or -for 1h); use with -follow")
	fmt.Println("  -parallel N      run N accounts at once from top-follower pool (~80: top 20 + next 60); use with -follow")
	fmt.Println("  -gui        local web GUI (desktop window is run.bat / gui.py)")
	fmt.Println("  -post       pick a pic from pics/posted, type a caption, post on every blog")
	fmt.Println("  -new-pinned  post+pin pics/posted/new_pinned1.jpg on all logged-in blogs (Clicky→beacons)")
	fmt.Println("  -fully-setup with -new-pinned/-photo-file/-wipe-posts: accounts in zoey-fully-setup.json")
	fmt.Println("  -wipe-posts  delete published posts on logged-in accounts (API; keeps pinned unless -include-pinned)")
	fmt.Println("  -include-pinned  with -wipe-posts: also delete pinned posts")
	fmt.Println("  -dry-run     with -wipe-posts / -messages-reply: show actions, change nothing")
	fmt.Println("  -pin         pin after photo/video publish (with -photo-file / -new-pinned)")
	fmt.Println("  -research [days]  sync followers/DM partners, then report which seeds/accounts/engage styles get followbacks (default 14 days; -no-sync to skip the sync)")
	fmt.Println("  -blazingoth-next  text-post the next unused line of blazingoth-text-posts-postable.txt (top 80, or -top-followers N)")
	fmt.Println("  -top-followers [N]  post on N highest-follower logged-in blogs (default 25)")
	fmt.Println("  -text \"…\"     plain text post (with -top-followers N, default 21)")
	fmt.Println("  -posted        who posted what, on which blogs (-account ID for one account)")
	fmt.Println("  -posts      text queue")
	fmt.Println("  -photo      photo queue")
	fmt.Println("  -like-nsfw     like a few NSFW posts on every account (saved cookies)")
	fmt.Println("  -set-avatars   set profile pics from pics/profile_pic on every account")
	fmt.Println("  -setup-profile English, birthday, AgeKey first, then pic/banner/colours/username")
	fmt.Println("  -skip-agekey   with -setup-profile: leave AgeKey for manual verification")
	fmt.Println("  -setup-accounts FILE  account id list for -setup-profile (use for parallel lanes)")
	fmt.Println("  -blog-look     username + banner + purple/black theme on the first 20 logged-in blogs")
	fmt.Println("  -fix-bio      strip #000000/#7C3AED leaked into blog descriptions")
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
// reachWithReplies (-follow -with-replies) answers each account's inbox once
// during its reach turn, so one process gives every account in the pool both.
var reachWithReplies bool

func reachTurn(a Account, until time.Time) error {
	if !reachWithReplies {
		return runFollowLikersOnUntil(a, 0, 0, until, nil)
	}
	done := make(chan struct{})
	go func() {
		defer close(done)
		runMessagesReplyOnAccount(a)
	}()
	err := runFollowLikersOnUntil(a, 0, 0, until, nil)
	<-done
	return err
}

func ensureSession(client *http.Client, auth *Auth) error {
	attachAuthJar(client, auth)
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
//     new cookie, close Chrome. Serialized by chromeLoginMu so parallel
//     workers (wipe/reach) auto-relogin one account at a time.
func recoverSession(client *http.Client, auth *Auth, forceLogin bool) error {
	chromeLoginMu.Lock()
	defer chromeLoginMu.Unlock()

	id := accountIDFromAuth(*auth)
	if accountIsTerminated(id) {
		return terminatedAccountError{ID: id}
	}

	if !forceLogin {
		if err := syncCookiesFromChromeLocked(auth); err == nil {
			attachAuthJar(client, auth)
			if err := refreshCSRF(client, auth); err == nil {
				maybeFillBlog(client, auth)
				if id != "" {
					sessionDead.Delete(id)
				}
				return nil
			}
		}
	}

	if headlessCookieOnly || skipChromeRecovery {
		return fmt.Errorf("session expired (headless cookie sync failed; run -login when you can use a visible window)")
	}

	fmt.Println("Profile cookie is dead — opening Chrome to sign in and collect a new one…")
	prevSkip := skipChromeRecovery
	skipChromeRecovery = false
	defer func() { skipChromeRecovery = prevSkip }()

	time.Sleep(3 * time.Second)
	if err := loginChromeSession(auth, true); err != nil {
		return err
	}
	attachAuthJar(client, auth)
	if err := refreshCSRF(client, auth); err != nil {
		return err
	}
	maybeFillBlog(client, auth)
	if id = accountIDFromAuth(*auth); id != "" {
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
			return fmt.Errorf("session expired (auto-relogin already failed for this account)")
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
		strings.Contains(s, "timeout exceeded") ||
		strings.Contains(s, "client.timeout") ||
		strings.Contains(s, "deadline exceeded") ||
		strings.Contains(s, "connection reset") ||
		strings.Contains(s, "forcibly closed") ||
		strings.Contains(s, "wsarecv") ||
		strings.Contains(s, "wsasend") ||
		strings.Contains(s, "broken pipe") ||
		strings.Contains(s, "connection refused") ||
		strings.Contains(s, "network is unreachable") ||
		strings.Contains(s, "tls handshake") ||
		strings.Contains(s, "unexpected eof") ||
		strings.Contains(s, "eof")
}

// refreshCSRFWithRetry retries CSRF sync a few times so a proxy blip after a
// successful Chrome login does not look like a dead session.
func refreshCSRFWithRetry(client *http.Client, auth *Auth, attempts int) error {
	if attempts < 1 {
		attempts = 1
	}
	var err error
	for i := 0; i < attempts; i++ {
		err = refreshCSRF(client, auth)
		if err == nil {
			return nil
		}
		if !isTransientNet(err) && !isCookieProblem(err) {
			// Unknown failure — still worth a short retry once.
			if i == 0 {
				time.Sleep(2 * time.Second)
				continue
			}
			return err
		}
		if isCookieProblem(err) && !isTransientNet(err) {
			return err
		}
		time.Sleep(time.Duration(2+i*2) * time.Second)
	}
	return err
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
