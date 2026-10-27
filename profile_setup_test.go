package main

import (
	"os"
	"strings"
	"testing"
	"time"
)

func TestRunSetupProfileEnglishFirstAgeKeyLast(t *testing.T) {
	src, err := os.ReadFile("profile_setup.go")
	if err != nil {
		t.Fatal(err)
	}
	s := string(src)
	markers := []string{
		`setupSay("1/5 English language")`,
		`setupSay("2/5 Birthday (29 Dec 2000)")`,
		`setupSay("3/5 Age verification (AgeKey). Type the passcode in Windows Security if it appears.")`,
		`setupSay("4/5 Show mature content, do not blur mature content")`,
		`setupSay("5/5 Profile picture, banner, colours, username")`,
	}
	prev := -1
	for _, m := range markers {
		i := strings.Index(s, m)
		if i < 0 {
			t.Fatalf("missing setup step %s", m)
		}
		if i < prev {
			t.Fatalf("setup step out of order: %s", m)
		}
		prev = i
	}
	look := strings.Index(s, `setupSay("5/5 Profile picture, banner, colours, username")`)
	age := strings.Index(s, `setupSay("3/5 Age verification (AgeKey). Type the passcode in Windows Security if it appears.")`)
	if look < age {
		t.Fatal("appearance must wait until English, birthday, and AgeKey are done")
	}
	if !strings.Contains(s, "Leaving dashboard") {
		t.Fatal("look step must leave the dashboard and open blog settings")
	}
	if !strings.Contains(s, "applyBlogLookInChrome") {
		t.Fatal("look step must use the chromedp Edit appearance path that used to work")
	}
	if strings.Contains(s, "Name, bio, and avatar already set.") && strings.Contains(s, "return nil") {
		start := strings.Index(s, "func applyBlogLookInChrome")
		end := strings.Index(s[start+1:], "\nfunc ")
		body := s[start : start+1+end]
		if strings.Contains(body, "already set.") && strings.Contains(body, "return nil") {
			t.Fatal("do not skip Edit appearance just because title/bio/avatar already look set")
		}
	}
	if !strings.Contains(s, "trySetHeaderFileOnTarget") {
		t.Fatal("chromedp look must set the banner from the header pencil")
	}
	src2, err := os.ReadFile("profile_setup.go")
	if err != nil {
		t.Fatal(err)
	}
	click := string(src2)
	if !strings.Contains(click, `Editar apariencia`) {
		t.Fatal("Edit appearance xpath must include Spanish")
	}
	if !strings.Contains(click, "editorOpen()") {
		t.Fatal("Edit appearance must wait until the editor actually opens")
	}
	start := strings.Index(click, "func clickEditAppearance")
	if start < 0 {
		t.Fatal("missing clickEditAppearance")
	}
	end := strings.Index(click[start+1:], "\nfunc ")
	if end < 0 {
		t.Fatal("could not bound clickEditAppearance")
	}
	body := click[start : start+1+end]
	if strings.Contains(body, "chromeClickTextJS") || strings.Contains(body, "chromeClickEditAppearanceJS") {
		t.Fatal("greedy shortest-label clicker opened Edit theme; use chromedp exact labels")
	}
	if !strings.Contains(body, "chromeClickLabelWait") {
		t.Fatal("look must click Edit appearance by exact label, like the week-ago chromedp path")
	}
	saveStart := strings.Index(click, "func confirmAvatarAndSave")
	if saveStart < 0 {
		t.Fatal("missing confirmAvatarAndSave")
	}
	saveEnd := strings.Index(click[saveStart+1:], "\nfunc ")
	if saveEnd < 0 {
		t.Fatal("could not bound confirmAvatarAndSave")
	}
	saveBody := click[saveStart : saveStart+1+saveEnd]
	if strings.Contains(saveBody, "MouseClickXY") {
		t.Fatal("coordinate save clicks missed Done/Save")
	}
	if !strings.Contains(saveBody, "chromeSaveAppearanceJS") {
		t.Fatal("Done then Save must be the original JS click")
	}
	if strings.Contains(saveBody, "for i := 0; i < 5") {
		t.Fatal("do not hammer Save — Tumblr shows Something went wrong")
	}
	if !strings.Contains(click, "chromeAppearanceErrorJS") || !strings.Contains(click, "something went wrong") {
		t.Fatal("must notice Tumblr's Something went wrong toast")
	}
	lookStart := strings.Index(click, "func applyBlogLookInChrome")
	lookEnd := strings.Index(click[lookStart+1:], "\nfunc ")
	lookBody := click[lookStart : lookStart+1+lookEnd]
	if strings.Contains(lookBody, "setLastFileInput") {
		t.Fatal("avatar must not reuse the header file input")
	}
	if !strings.Contains(lookBody, "setLookImageOnTarget") || !strings.Contains(lookBody, "chromeAvatarBoxJS") {
		t.Fatal("avatar must go through the avatar pencil chooser")
	}
}

func TestSetupLookCheckRequiresBannerAndUsername(t *testing.T) {
	partial := setupLookCheck{TitleOK: true, BioOK: true, AvatarOK: true}
	if partial.Done() {
		t.Fatal("title/bio/avatar alone is not done — banner and username must be set too")
	}
	partial.BannerOK = true
	if partial.Done() {
		t.Fatal("banner without username is not done")
	}
	partial.UsernameOK = true
	partial.AccentOK = true
	partial.BackgroundOK = true
	if !partial.Done() {
		t.Fatal("title, bio, avatar, banner, and username must count as done")
	}
	if !strings.Contains(partial.report(), "banner=ok") || !strings.Contains(partial.report(), "username=ok") {
		t.Fatal("look report must show banner and username")
	}
}

func TestBatchReadyRequiresColours(t *testing.T) {
	prev := skipSetupAgeKey
	skipSetupAgeKey = true
	defer func() { skipSetupAgeKey = prev }()
	partial := setupLookCheck{TitleOK: true, UsernameOK: true, AvatarOK: true}
	if partial.batchReady() {
		t.Fatal("batchReady must not pass without Zoey colours")
	}
	partial.AccentOK = true
	if partial.batchReady() {
		t.Fatal("background colour is required too")
	}
	partial.BackgroundOK = true
	if !partial.batchReady() {
		t.Fatal("title+username+avatar+colours should be batchReady")
	}
}

func TestApplySetupLookRetriesUntilChecksPass(t *testing.T) {
	src, err := os.ReadFile("profile_setup.go")
	if err != nil {
		t.Fatal(err)
	}
	s := string(src)
	start := strings.Index(s, "func applySetupLook")
	end := strings.Index(s[start+1:], "\nfunc ")
	if start < 0 || end < 0 {
		t.Fatal("could not bound applySetupLook")
	}
	body := s[start : start+1+end]
	if !strings.Contains(body, "retrying accent/background") {
		t.Fatal("look step must retry colours when accent/background are missing")
	}
	if !strings.Contains(body, "lookAvatarAppliedByUs") || !strings.Contains(body, "lookBannerAppliedByUs") {
		t.Fatal("look step must only skip avatar/banner when we recorded our own upload")
	}
	if !strings.Contains(body, "avatarTrusted") {
		t.Fatal("look step must distinguish page AvatarOK from Zoey-applied avatar")
	}
	if !strings.Contains(body, "refreshSetupLookCheck") || !strings.Contains(body, "check.Done()") {
		t.Fatal("look step must verify banner, avatar, username after applying")
	}
	if !strings.Contains(body, "Look confirmed") {
		t.Fatal("look step must log when checks pass")
	}
	if !strings.Contains(s, "func openFinishedBlogSettings") {
		t.Fatal("missing openFinishedBlogSettings")
	}
	if !strings.Contains(s, "Opening finished blog settings:") {
		t.Fatal("final settings nav must log the blog URL")
	}
}

func TestSetupCanSkipAgeKeyWithoutSkippingOtherSteps(t *testing.T) {
	src, err := os.ReadFile("profile_setup.go")
	if err != nil {
		t.Fatal(err)
	}
	s := string(src)
	look := strings.Index(s, `setupSay("5/5 Profile picture, banner, colours, username")`)
	mature := strings.Index(s, `setupSay("4/5 Show mature content, do not blur mature content")`)
	age := strings.Index(s, `setupSay("3/5 Age verification skipped — do AgeKey manually later.")`)
	if look < 0 || mature < 0 || age < 0 {
		t.Fatal("missing skip-AgeKey setup path")
	}
	if age > look || age > mature {
		t.Fatal("skip-AgeKey must still happen before mature and appearance")
	}
	if strings.Contains(s, `Mature content skipped (needs AgeKey)`) {
		t.Fatal("skip-AgeKey must still check/apply Show mature before appearance")
	}
	if !strings.Contains(s, "matureSettingsOK") {
		t.Fatal("settings gate must require Show mature ON / Blur OFF before appearance")
	}
	if strings.Contains(s, "English + birthday) — skipping straight to appearance") {
		t.Fatal("must not skip to appearance on English+birthday alone — mature toggles required")
	}
	mainSrc, err := os.ReadFile("main.go")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(mainSrc), `case "-skip-agekey":`) {
		t.Fatal("CLI must expose -skip-agekey")
	}
}

func TestMatureSettingsOK(t *testing.T) {
	if matureSettingsOK(matureToggleStatus{State: "ready", On: true, BlurState: "ready", BlurOn: false}) != true {
		t.Fatal("show on + blur off should be OK")
	}
	if matureSettingsOK(matureToggleStatus{State: "ready", On: true, BlurState: "missing"}) {
		t.Fatal("show on + blur missing must NOT count — blur off must be verified")
	}
	if matureSettingsOK(matureToggleStatus{State: "ready", On: true, BlurState: "disabled", BlurOn: false}) {
		t.Fatal("blur still locked/disabled must NOT count as blur off")
	}
	if matureSettingsOK(matureToggleStatus{State: "ready", On: true, BlurState: "ready", BlurOn: true}) {
		t.Fatal("blur still on must not count as ready")
	}
	if matureSettingsOK(matureToggleStatus{State: "ready", On: false, BlurState: "ready", BlurOn: false}) {
		t.Fatal("show off must not count as ready")
	}
	if matureSettingsOK(matureToggleStatus{State: "disabled", On: false}) {
		t.Fatal("locked toggle must not count as ready")
	}
	if matureTogglesSucceeded("show:on blur:missing", matureToggleStatus{State: "ready", On: true, BlurState: "missing"}) {
		t.Fatal("apply success must not treat blur:missing as blur off")
	}
	if !matureTogglesSucceeded("show:on blur:off", matureToggleStatus{State: "ready", On: true, BlurState: "ready", BlurOn: false}) {
		t.Fatal("show on + blur off should succeed")
	}
}

func TestSetupBlogNameIgnoresRenameWishlist(t *testing.T) {
	// assigned-usernames.json holds the name we want to rename to. Trusting it
	// opened /settings/blog/<wishlist>, which Tumblr bounces to /explore.
	original := loadNameMap(assignedUsernamesFile)
	restore := make(map[string]string, len(original))
	for k, v := range original {
		restore[k] = v
	}
	defer saveNameMap(assignedUsernamesFile, restore)

	probe := make(map[string]string, len(original)+1)
	for k, v := range original {
		probe[k] = v
	}
	probe["zzprobe"] = "nova-zoey"
	saveNameMap(assignedUsernamesFile, probe)

	got := pickSetupBlogName("zzprobe", Auth{Blog: "realblogname"}, "", followerLog{})
	if got != "realblogname" {
		t.Fatalf("the signed-in blog must win over the rename wishlist, got %q", got)
	}
	if got := pickSetupBlogName("zzprobe", Auth{}, "apiname", followerLog{}); got != "apiname" {
		t.Fatalf("api name must win, got %q", got)
	}
}

func TestBlogSettingsBounceIsDetected(t *testing.T) {
	if blogNameFromURL("https://www.tumblr.com/explore/trending") != "" {
		t.Fatal("a bounce to /explore must not look like a blog settings page")
	}
	if blogNameFromURL("https://www.tumblr.com/dashboard") != "" {
		t.Fatal("the dashboard is not a blog settings page")
	}
	if blogNameFromURL("https://www.tumblr.com/settings/blog/nova-zoey") != "nova-zoey" {
		t.Fatal("a real blog settings URL must give the blog name")
	}
	src, err := os.ReadFile("profile_setup.go")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(src), "openVerifiedBlogSettings") {
		t.Fatal("the look step must confirm Tumblr actually opened blog settings")
	}
}

func TestWantedTitleSet(t *testing.T) {
	if !wantedTitleSet("Zoey 🌷") {
		t.Fatal("expected Zoey title")
	}
	if !wantedTitleSet(" zoey ") {
		t.Fatal("expected zoey case-insensitive")
	}
	if wantedTitleSet("Vete de aquí") {
		t.Fatal("old title should not count")
	}
}

func TestWantedBioSet(t *testing.T) {
	if !wantedBioSet("I'm 25 btw :P · 18+ only Instaaaa: https://www.instagram.com/lil.cosmic.bunny.o7") {
		t.Fatal("expected zoey bio")
	}
	if wantedBioSet("25 · 18+ only Instaaaa: https://www.instagram.com/lil.cosmic.bunny.o7") {
		t.Fatal("old 25 bio without btw should not count")
	}
	if wantedBioSet("https://instagram.com/lil.cosmic.bunny.o7") {
		t.Fatal("instagram without 25 should not count")
	}
	if wantedBioSet("Mafocopi") {
		t.Fatal("old bio should not count")
	}
}

func TestDefaultProfileIs25(t *testing.T) {
	cfg := loadProfileSetupConfig()
	if cfg.BirthdayYear != 2000 || cfg.BirthdayMonth != 12 || cfg.BirthdayDay != 29 || !strings.Contains(cfg.Description, "I'm 25 btw") {
		t.Fatalf("profile setup should be 25 on 29 Dec 2000 with btw bio: %+v", cfg)
	}
}

func TestAvatarLooksSet(t *testing.T) {
	if avatarLooksSet("") {
		t.Fatal("empty")
	}
	if avatarLooksSet("https://assets.tumblr.com/images/default_avatar/sphere_open_64.png") {
		t.Fatal("default avatar")
	}
	if !avatarLooksSet("https://64.media.tumblr.com/abc/s64x64/pic.jpg") {
		t.Fatal("custom avatar")
	}
}

func TestSessionChecksKeepBirthdayAndAge(t *testing.T) {
	prevB, prevA := setupBirthdayApplied, setupAgeVerified
	t.Cleanup(func() {
		setupBirthdayApplied = prevB
		setupAgeVerified = prevA
	})
	setupBirthdayApplied = true
	setupAgeVerified = false
	st := setupPageStatus{Birthday: "unset", Verified: false}
	if !sessionBirthdayOK(st) {
		t.Fatal("birthday set earlier this session must still count")
	}
	if sessionAgeOK(st) {
		t.Fatal("age must not count before it is verified")
	}
	setupAgeVerified = true
	if !sessionAgeOK(st) {
		t.Fatal("PIN-complete age must still count off the AgeKey page")
	}
	if !strings.Contains(chromeAgeStatusJS, "successfully verified") {
		t.Fatal("age status JS should recognize AgeKey success copy")
	}
	if !strings.Contains(chromeAgeStatusJS, "Age Group.{0,160}Verified") {
		t.Fatal("age status JS should still match Age Group Verified on account settings")
	}
}

func TestSetupChecklistReady(t *testing.T) {
	cl := setupChecklist{TitleOK: true, BioOK: true, AvatarOK: true, BirthdayOK: true, CookiesOK: true, AgeOK: true}
	if !cl.Ready() {
		t.Fatal("all true should be ready")
	}
	cl.AvatarOK = false
	if cl.Ready() {
		t.Fatal("missing avatar should not be ready")
	}
	cl.AvatarOK = true
	cl.AgeOK = false
	if cl.Ready() {
		t.Fatal("unverified age should not be ready")
	}
}

func TestUsableBlogNameRejectsUUID(t *testing.T) {
	if usableBlogName("t:6v-oEtvdikN0JUwrhoDkcw") {
		t.Fatal("uuid with t: prefix")
	}
	if usableBlogName("6v-oEtvdikN0JUwrhoDkcw") {
		t.Fatal("stripped tumblr uuid is not a blog name")
	}
	if !usableBlogName("support89997854") {
		t.Fatal("normal blog name")
	}
	if !usableBlogName("lilly-cosmic1") {
		t.Fatal("hyphenated blog name")
	}
}

func TestPickSetupBlogNameSkipsUUIDAndStampedZoeyofc(t *testing.T) {
	log := followerLog{
		Latest: followerSnapshot{Primary: "zoeyofc", Followers: 34, Blogs: map[string]int{"zoeyofc": 34}},
		History: []followerSnapshot{
			{Primary: "lillycosmic3", Followers: 99, Blogs: map[string]int{"lillycosmic3": 99}},
			{Primary: "zoeyofc", Followers: 34, Blogs: map[string]int{"zoeyofc": 34}},
		},
	}
	got := pickSetupBlogName("not-in-assigned-map", Auth{Blog: "t:6v-oEtvdikN0JUwrhoDkcw"}, "", log)
	if got != "lillycosmic3" {
		t.Fatalf("got %q", got)
	}
}

func TestSetupChromeStillOpenNeedsPort(t *testing.T) {
	prev := liveDebugPort
	liveDebugPort = ""
	t.Cleanup(func() { liveDebugPort = prev })
	if setupChromeStillOpen() {
		t.Fatal("empty debug port is not an open Chrome")
	}
}

func TestUseAgeKeyHitJS(t *testing.T) {
	if !strings.Contains(chromeUseAgeKeyHitJS, "use your agekey") {
		t.Fatal("AgeKey hit JS should find Use your AgeKey")
	}
	if !strings.Contains(chromeUseAgeKeyHitJS, "el.click()") {
		t.Fatal("AgeKey hit JS must click the card, not only return coordinates")
	}
	if !strings.Contains(chromeUseAgeKeyHitJS, "use your selfie") {
		t.Fatal("AgeKey hit JS must ignore the selfie row")
	}
	if strings.Contains(chromeUseAgeKeyHitJS, "length < 120") {
		t.Fatal("120-char cap drops the AgeKey card description")
	}
}

func TestBirthdayAsyncJSPicksCustomDropdowns(t *testing.T) {
	js := chromeSetBirthdayAsyncJS(2000, 12, 29)
	if !strings.Contains(js, "role=\"option\"") {
		t.Fatal("async birthday JS should click listbox options")
	}
	if !strings.Contains(js, "12: /^(dec|dic|dez|déc|дек)/i") {
		t.Fatal("December must be month 12")
	}
	if !strings.Contains(js, "await sleep") {
		t.Fatal("async birthday JS must wait for the dropdown")
	}
}

func TestEnglishJSPicksCombobox(t *testing.T) {
	if !strings.Contains(chromeSetEnglishJS, "english") {
		t.Fatal("english JS should pick English")
	}
	if !strings.Contains(chromeSetEnglishJS, "role=\"combobox\"") {
		t.Fatal("english JS should handle combobox language menus")
	}
	if !strings.Contains(chromeSetEnglishJS, "idioma") {
		t.Fatal("english JS should find the language row on a Spanish settings page")
	}
	if !strings.Contains(chromeSetEnglishJS, "guardar") {
		t.Fatal("english JS should save with Guardar on a Spanish settings page")
	}
	if !strings.Contains(chromeSetBirthdayAsyncJS(2000, 12, 29), "locked") {
		t.Fatal("birthday JS must stop when Tumblr disables the date dropdowns")
	}
	js := chromeSetBirthdayAsyncJS(2000, 12, 29)
	if strings.Contains(js, "can[\\u2019']t change your birthday") || strings.Contains(js, "can't change your birthday") {
		t.Fatal("help text You can't change your birthday after entering it is not a locked date")
	}
	if !strings.Contains(chromeSetBirthdayAsyncJS(2000, 12, 29), "dated.length >= 3") {
		t.Fatal("birthday JS must not click the first three buttons on account settings (that hits Change password)")
	}
	if !strings.Contains(chromeDismissOverlaysJS, "password-cancel") {
		t.Fatal("overlays must Cancel a Change password dialog")
	}
	if !strings.Contains(chromeSetEnglishJS, "goog-te") {
		t.Fatal("English picker must ignore Google Translate tabs")
	}
}

func TestAgeStatusDoesNotLockWhenBirthdayHeadingExists(t *testing.T) {
	if !strings.Contains(chromeAgeStatusJS, "usable.length === 0") {
		t.Fatal("birthday is locked only when the date dropdowns exist and are disabled")
	}
	js := chromeAgeStatusJS
	if strings.Contains(js, "can't change your birthday") || strings.Contains(js, `can[\u2019']t change your birthday`) {
		t.Fatal("help text You can't change your birthday after entering it is not a locked date")
	}
}

func TestBirthdayMonthJSPicksDecemberNotOctober(t *testing.T) {
	js := chromeSetBirthdayFieldsJS(2000, 12, 29)
	if !strings.Contains(js, "12: /^(dec|dic|dez|déc|дек)/i") {
		t.Fatal("December must be month 12")
	}
	if strings.Contains(js, "/oct|окт/") {
		t.Fatal("must not match October as a fallback for every month")
	}
	if !strings.Contains(js, "Number(v) === wantM") {
		t.Fatal("must match month by numeric value first")
	}
}

func TestAnyoneCanMessageToggleTurnsOffFollowerOnly(t *testing.T) {
	if !strings.Contains(chromeSetAnyoneCanMessageJS, "only allow messages from") {
		t.Fatal("open-DM JS must match the follower-only messaging label")
	}
	if !strings.Contains(chromeSetAnyoneCanMessageJS, "already-off") {
		t.Fatal("open-DM JS must no-op when the toggle is already off")
	}
	if !strings.Contains(chromeSetAnyoneCanMessageJS, "pointerdown") {
		t.Fatal("open-DM JS must fire a real click, not only el.click()")
	}
	src, err := os.ReadFile("profile_setup.go")
	if err != nil {
		t.Fatal(err)
	}
	body := string(src)
	if !strings.Contains(body, "applyAnyoneCanMessageOnTarget") {
		t.Fatal("setup must apply the open-DM toggle")
	}
	if !strings.Contains(body, "openFinishedBlogSettings") {
		t.Fatal("setup must open /settings/blog/{name} after look")
	}
	open := strings.Index(body, "func openFinishedBlogSettings")
	apply := strings.Index(body, "applyAnyoneCanMessageOnTarget(t)")
	if open < 0 || apply < open {
		t.Fatal("finished blog settings must turn off follower-only DMs")
	}
}

func TestMatureTogglesUnblurSensitiveImages(t *testing.T) {
	if !strings.Contains(chromeSetMatureTogglesJS, "blur mature content") {
		t.Fatal("mature JS should turn off blur mature content")
	}
	if !strings.Contains(chromeSetMatureTogglesJS, "show mature content") {
		t.Fatal("mature JS should turn on show mature content")
	}
	if !strings.Contains(chromeSetMatureTogglesJS, "content you see") {
		t.Fatal("mature JS should scroll to Content you see")
	}
	if !strings.Contains(chromeSetMatureTogglesJS, "pointerdown") {
		t.Fatal("mature JS must fire a real click, not only el.click()")
	}
	if !strings.Contains(chromeSetMatureTogglesJS, `setWant(blurEl || findBlur(), false, 'blur')`) &&
		!strings.Contains(chromeSetMatureTogglesJS, `setWant(findBlur(), false, 'blur')`) {
		t.Fatal("blur toggle must be set off (after Show mature unlocks it)")
	}
	if !strings.Contains(chromeSetMatureTogglesJS, "Turn Show on first") &&
		!strings.Contains(chromeSetMatureTogglesJS, "re-find") {
		t.Fatal("must re-find blur after turning Show mature on")
	}
	if !strings.Contains(chromeSetMatureTogglesJS, `setWant(findShow(), true, 'show')`) {
		t.Fatal("show mature must be set on")
	}
	if !strings.Contains(chromeSetMatureTogglesJS, "content labels") {
		t.Fatal("mature JS should scroll to Content labels")
	}
	if !strings.Contains(chromeSetMatureTogglesJS, "two-factor") {
		t.Fatal("mature JS must ignore two-factor switches")
	}
	src, err := os.ReadFile("profile_setup.go")
	if err != nil {
		t.Fatal(err)
	}
	body := string(src)
	if !strings.Contains(body, "waitForMatureContentReady") {
		t.Fatal("setup must wait until Tumblr unlocks mature toggles after AgeKey")
	}
	if !strings.Contains(body, "matureShowSucceeded") {
		t.Fatal("mature apply must require show:on, not a bare click")
	}
	age := strings.Index(body, `setupSay("3/5 Age verification (AgeKey). Type the passcode in Windows Security if it appears.")`)
	wait := strings.Index(body, "waitForMatureContentReady()")
	mature := strings.Index(body, `setupSay("4/5 Show mature content, do not blur mature content")`)
	if age < 0 || wait < 0 || mature < 0 || !(age < wait && wait < mature) {
		t.Fatal("AgeKey → unlock wait → mature click order is wrong")
	}
	if strings.Contains(body, "pinSeen && findWindowsSecurityHWND() == 0") {
		t.Fatal("PIN dialog closing must not mark AgeKey verified before Tumblr unlocks mature")
	}
}

func TestSetupBlogNameIgnoresRenameWishlistOnly(t *testing.T) {
	original := loadNameMap(assignedUsernamesFile)
	restore := make(map[string]string, len(original))
	for k, v := range original {
		restore[k] = v
	}
	defer saveNameMap(assignedUsernamesFile, restore)

	probe := make(map[string]string, len(original)+1)
	for k, v := range original {
		probe[k] = v
	}
	probe["zzprobe-empty"] = "wishlist-only-name"
	saveNameMap(assignedUsernamesFile, probe)

	got := pickSetupBlogName("zzprobe-empty", Auth{}, "", followerLog{})
	if got != "" {
		t.Fatalf("wishlist-only must not become the look blog, got %q", got)
	}
}

func TestLoginFillDoneIgnoresSidOnLoginPage(t *testing.T) {
	login := "https://www.tumblr.com/login?redirect_to=%2Fdashboard&source=login_register_required"
	if loginFillDone(login, true) {
		t.Fatal("a leftover sid on login_register_required is not a finished login")
	}
	if loginFillDone("https://www.tumblr.com/login?redirect_to=/dashboard", true) {
		t.Fatal("redirect_to=/dashboard is still the login page")
	}
	if loginFillDone("", true) {
		t.Fatal("empty href is not a finished login")
	}
	if !loginFillDone("https://www.tumblr.com/dashboard", false) {
		t.Fatal("dashboard means login finished")
	}
	if !loginFillDone("https://www.tumblr.com/settings/account", true) {
		t.Fatal("account settings means login finished")
	}
}

func TestRecoverSetupLoginDoesNotOpenGatedPage(t *testing.T) {
	src, err := os.ReadFile("chrome.go")
	if err != nil {
		t.Fatal(err)
	}
	s := string(src)
	if strings.Contains(s, "leaving the login redirect for settings") {
		t.Fatal("must not navigate a /login tab to settings — Tumblr bounces to login_register_required")
	}
	if !strings.Contains(s, "reloginIfOnLoginPage") {
		t.Fatal("login screen should sign in again")
	}
	if !strings.Contains(s, "filling the form even if a sid cookie exists") {
		t.Fatal("a leftover sid must not skip the login form")
	}
	src2, err := os.ReadFile("chrome_look.go")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(src2), "tumblr bounced back to login") {
		t.Fatal("opening settings from a login tab must sign in again, not eat the bounce")
	}
}

func TestChromeLooksLoggedInOnDashboard(t *testing.T) {
	login := "https://www.tumblr.com/login?redirect_to=%2Fdashboard&source=login_register_required"
	if chromeURLLooksLoggedIn(login) {
		t.Fatal("login_register_required is not logged in")
	}
	if !chromeURLLooksLoggedIn("https://www.tumblr.com/dashboard") {
		t.Fatal("dashboard must count as logged in so setup does not sit there waiting for sid")
	}
	if !chromeURLLooksLoggedIn("https://www.tumblr.com/settings/blog/bloodseakerz") {
		t.Fatal("blog settings must count as logged in")
	}
	if !chromeURLLooksLoggedIn("https://www.tumblr.com/blog/view/behemont") {
		t.Fatal("blog view must count as logged in")
	}
}

func TestLooksLikeTumblrLoginPage(t *testing.T) {
	login := "https://www.tumblr.com/login?redirect_to=%2Fdashboard&source=login_register_required"
	if !looksLikeTumblrLoginPage(login, "") {
		t.Fatal("login redirect must be treated as login")
	}
	if !looksLikeTumblrLoginPage("https://www.tumblr.com/dashboard", "Log in Continue with Google Sign up") {
		t.Fatal("login form copy must be treated as login")
	}
	if looksLikeTumblrLoginPage("https://www.tumblr.com/settings/account", "Birthday Language Age Group Account") {
		t.Fatal("account settings is not the login page")
	}
}

func TestKeepSetupTab(t *testing.T) {
	if !keepSetupTab("https://www.tumblr.com/settings/blog/support89997854") {
		t.Fatal("settings blog")
	}
	if !keepSetupTab("https://www.tumblr.com/dashboard") {
		t.Fatal("do not close the dashboard — that is the page the user is watching")
	}
	if !keepSetupTab("https://www.tumblr.com/") {
		t.Fatal("do not close the home tab")
	}
	if !keepSetupTab("https://www.tumblr.com/login?redirect_to=%2Fdashboard&source=login_register_required") {
		t.Fatal("do not close the login tab while setup is using it")
	}
	if keepSetupTab("https://www.tumblr.com/customize/sims4bbgc?redirect_to=/settings/blog/sims4bbgc") {
		t.Fatal("theme customizer must not be kept")
	}
	if keepSetupTab("https://www.tumblr.com/premium") {
		t.Fatal("premium nag tab can close")
	}
}

func TestSkipAgeKeyClosesChromeForBatch(t *testing.T) {
	src, err := os.ReadFile("profile_setup.go")
	if err != nil {
		t.Fatal(err)
	}
	s := string(src)
	start := strings.Index(s, "func runSetupProfile")
	end := strings.Index(s[start+1:], "\nfunc ")
	if start < 0 || end < 0 {
		t.Fatal("could not bound runSetupProfile")
	}
	body := s[start : start+1+end]
	if !strings.Contains(body, "if skipSetupAgeKey") || !strings.Contains(body, "stopChrome(cmd)") {
		t.Fatal("skip-AgeKey batch setup must close Chrome so the next account can start")
	}
	if !strings.Contains(body, "Chrome stays open so you can check the profile") {
		t.Fatal("full AgeKey setup must still leave Chrome open for review")
	}
	if !strings.Contains(body, "Chrome closed right after login") {
		t.Fatal("setup must notice if Chrome dies immediately after login")
	}
}

func TestChromeLaunchDoesNotBreakAwayFromJob(t *testing.T) {
	src, err := os.ReadFile("chrome.go")
	if err != nil {
		t.Fatal(err)
	}
	s := string(src)
	if strings.Contains(s, "0x01000000") || strings.Contains(s, "CREATE_BREAKAWAY_FROM_JOB") {
		t.Fatal("BREAKAWAY_FROM_JOB makes Chrome open then instantly exit")
	}
	start := strings.Index(s, "func closeExtraChromeTabs")
	end := strings.Index(s[start+1:], "\nfunc ")
	if start < 0 || end < 0 {
		t.Fatal("could not bound closeExtraChromeTabs")
	}
	body := s[start : start+1+end]
	if !strings.Contains(body, "pages <= 1") {
		t.Fatal("must not close the last Chrome tab after login")
	}
}

func TestPreferredChromePageSkipsLogin(t *testing.T) {
	login := chromeJSONTarget{ID: "login", Type: "page", URL: "https://www.tumblr.com/login?redirect_to=%2Fdashboard&source=login_register_required"}
	settings := chromeJSONTarget{ID: "settings", Type: "page", URL: "https://www.tumblr.com/settings/account"}
	got := preferredChromePageTarget([]chromeJSONTarget{login, settings})
	if got.ID != "settings" {
		t.Fatalf("wanted settings tab, got %+v", got)
	}
}

func TestAgeKeyDateNeedlesIncludeUKFormat(t *testing.T) {
	now := time.Date(2026, 9, 19, 12, 0, 0, 0, time.UTC)
	js := chromePickListedAgeKeyJS(now)
	if !strings.Contains(js, "19/09/2026") {
		t.Fatal("AgeKey picker should look for 19/09/2026")
	}
	needles := passkeyPickerNeedles(now)
	joined := strings.Join(needles, " ")
	if !strings.Contains(joined, "2026-09-19") {
		t.Fatal("passkey picker should match AgeKey - 2026-09-19")
	}
	if !passkeyHitIsToday("AgeKey - 2026-09-19", now) {
		t.Fatal("should accept AgeKey - 2026-09-19")
	}
	if passkeyHitIsToday("AgeKey - 2026-09-11", now) {
		t.Fatal("must not pick the 2026-09-11 AgeKey")
	}
	later := time.Date(2026, 9, 20, 17, 27, 0, 0, time.Local)
	jsLater := chromePickListedAgeKeyJS(later)
	if !strings.Contains(jsLater, "19/09/2026") {
		t.Fatal("the day after, listed picker must still look for 19/09/2026")
	}
	if !strings.Contains(jsLater, "parseAllDates") || !strings.Contains(jsLater, "length !== 1") {
		t.Fatal("listed picker must click a single-date row, not the whole list")
	}
	if !strings.Contains(jsLater, "role=\"radio\"") {
		t.Fatal("listed picker must click radio rows")
	}
	want := preferredListedAgeKeyText([]string{
		"11/09/2026",
		"19/09/2026",
		"Create my AgeKey",
		"Already have an AgeKey?",
	})
	if want != "19/09/2026" {
		t.Fatalf("wanted 19/09/2026, got %q", want)
	}
	if preferredListedAgeKeyText([]string{"11/09/2026\n19/09/2026"}) != "" {
		t.Fatal("list container with both dates must not be treated as a row")
	}
	got := preferredAgeKeyLabel([]string{
		"AgeKey - 2026-09-11",
		"AgeKey - 2026-09-19",
		"iPhone, iPad, or Android device",
		"Security key",
		"Cancel",
	}, later)
	if got != "AgeKey - 2026-09-19" {
		t.Fatalf("wanted 2026-09-19, got %q", got)
	}
}

func TestSelectLivePasskeyIfOpen(t *testing.T) {
	lastPasskeySelect = time.Time{}
	hit := selectTodayAgeKeyPasskey()
	if findWindowsSecurityHWND() == 0 {
		t.Skip("no Windows Security dialog")
	}
	t.Log("passkey hit:", hit)
	if hit != "" && hit != "pin-prompt" {
		t.Fatalf("must not OS-click the passkey dialog, got %q", hit)
	}
}

func TestPremiumDismissJS(t *testing.T) {
	if !strings.Contains(chromeDismissOverlaysJS, "premium-close") {
		t.Fatal("overlay JS should close Tumblr Premium")
	}
	if !strings.Contains(chromeDismissOverlaysJS, "premiumSkipBtn") {
		t.Fatal("overlay JS must never click Yearly/Monthly/Get Premium")
	}
	if !strings.Contains(chromeDismissOverlaysJS, "blaze") || !strings.Contains(chromeDismissOverlaysJS, "purpleFill") {
		t.Fatal("overlay JS should click the purple Blaze button in any language")
	}
	if !strings.Contains(chromePremiumCloseHitJS, "blaze") {
		t.Fatal("premium/blaze hit JS should find the Blaze dialog")
	}
	if !strings.Contains(chromePremiumCloseHitJS, "premium-close") {
		t.Fatal("premium hit JS should find the close button")
	}
	if !strings.Contains(chromePremiumCloseHitJS, "premiumSkipBtn") {
		t.Fatal("premium hit JS must skip Yearly/Get Premium buttons")
	}
}

func TestAttachLiveChromeDoesNotCloseTab(t *testing.T) {
	src, err := os.ReadFile("chrome.go")
	if err != nil {
		t.Fatal(err)
	}
	s := string(src)
	start := strings.Index(s, "func attachLiveChrome(")
	if start < 0 {
		t.Fatal("missing attachLiveChrome")
	}
	end := strings.Index(s[start+1:], "\nfunc ")
	body := s[start : start+1+end]
	if strings.Contains(body, "cancelAlloc()") || strings.Contains(body, "cancelCtx()") {
		t.Fatal("cancelling a WithTargetID attach closes the tab and Chrome exits")
	}
	start = strings.Index(s, "func detachLiveChrome(")
	end = strings.Index(s[start+1:], "\nfunc ")
	if start < 0 || end < 0 {
		t.Fatal("could not bound detachLiveChrome")
	}
	body = s[start : start+1+end]
	if strings.Contains(body, "liveChromeCancel()") {
		t.Fatal("detach must not cancel the chromedp context — that closes the last tab")
	}
}

func TestGuiSetupLeavesChromeOpen(t *testing.T) {
	src, err := os.ReadFile("gui_actions.go")
	if err != nil {
		t.Fatal(err)
	}
	s := string(src)
	start := strings.Index(s, "func guiFullAccountSetup")
	end := strings.Index(s[start+1:], "\nfunc ")
	if start < 0 || end < 0 {
		t.Fatal("could not bound guiFullAccountSetup")
	}
	body := s[start : start+1+end]
	if strings.Contains(body, "skipSetupAgeKey = true") {
		t.Fatal("GUI setup must run AgeKey before appearance, not skip it")
	}
	if !strings.Contains(body, "setupCodeStamp") {
		t.Fatal("GUI setup must log the code stamp so a stale process is obvious")
	}
}

func TestGuiDoesNotHideOnRandomPort(t *testing.T) {
	src, err := os.ReadFile("gui.go")
	if err != nil {
		t.Fatal(err)
	}
	s := string(src)
	start := strings.Index(s, "func runGUI(")
	end := strings.Index(s[start+1:], "\nfunc ")
	if start < 0 || end < 0 {
		t.Fatal("could not bound runGUI")
	}
	body := s[start : start+1+end]
	if strings.Contains(body, `net.Listen("tcp", "127.0.0.1:0")`) {
		t.Fatal("a second GUI on a random port leaves Run full setup talking to the old process")
	}
	if !strings.Contains(body, "already in use") {
		t.Fatal("busy 8787 must tell Harrie to Ctrl+C the old GUI")
	}
}

func TestSetupCodeStampIsLogged(t *testing.T) {
	if setupCodeStamp == "" {
		t.Fatal("setupCodeStamp must be set")
	}
	src, err := os.ReadFile("profile_setup.go")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(src), "Using %s") {
		t.Fatal("runSetupProfile must print the stamp")
	}
}

func TestIsConsentTargetURL(t *testing.T) {
	if !isConsentTargetURL("https://cmp.quantcast.com/something") {
		t.Fatal("quantcast cmp")
	}
	if !isConsentTargetURL("https://www.tumblr.com/privacy/consent") {
		t.Fatal("tumblr privacy")
	}
	if !isConsentTargetURL("https://www.tumblr.com/privacy/consent/begin?v=2&redirect=%2Fdashboard") {
		t.Fatal("tumblr privacy begin")
	}
	if isConsentTargetURL("https://auth.agekey.org/login") {
		t.Fatal("agekey is not a cookie banner")
	}
	if isConsentTargetURL("https://www.tumblr.com/settings/account") {
		t.Fatal("account settings is not a cookie banner")
	}
}
