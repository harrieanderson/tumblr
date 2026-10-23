package main

import (
	"os"
	"strings"
	"testing"
)

func TestPreferredPageTargetKeepsLoginOverConsent(t *testing.T) {
	got := preferredChromePageTarget([]chromeJSONTarget{
		{ID: "consent", Type: "page", URL: "https://www.tumblr.com/privacy/consent"},
		{ID: "login", Type: "page", URL: "https://www.tumblr.com/login?redirect_to=/dashboard"},
	})
	if got.ID != "login" {
		t.Fatalf("login tab must win over cookie-consent, got %+v", got)
	}
}

func TestLoginFillDoesNotPretendSuccess(t *testing.T) {
	src, err := os.ReadFile("chrome.go")
	if err != nil {
		t.Fatal(err)
	}
	s := string(src)
	if !strings.Contains(s, `return fmt.Errorf("still on login page")`) {
		t.Fatal("login fill must error if Tumblr is still on /login")
	}
	if !strings.Contains(s, "misses >= 5") {
		t.Fatal("session wait must require several debug-port misses before treating Chrome as closed")
	}
	// A captcha stalls the debug port; five fast polls is not proof Chrome died.
	if !strings.Contains(s, "time.Since(firstMiss) > 30*time.Second") {
		t.Fatal("Chrome must look gone for a sustained period, not just a few polls")
	}
	if !strings.Contains(s, "func portAlive(") {
		t.Fatal("liveness needs a patient probe, separate from fast free-port picking")
	}
	for _, fn := range []string{"func recoverLiveDebugPort(", "func attachAccountDebugPort("} {
		i := strings.Index(s, fn)
		if i < 0 {
			t.Fatal("missing " + fn)
		}
		body := s[i:]
		if j := strings.Index(body[len(fn):], "\nfunc "); j >= 0 {
			body = body[:len(fn)+j]
		}
		if strings.Contains(body, "portInUse(") {
			t.Fatal(fn + " must not use the 200ms probe — that killed Chrome mid-captcha")
		}
		if !strings.Contains(body, "portAlive(") {
			t.Fatal(fn + " must use the patient probe")
		}
	}
	if !strings.Contains(s, "loginWaitBudget") {
		t.Fatal("login wait must stay open for captcha instead of closing Chrome early")
	}
	idx := strings.Index(s, "func waitForTumblrSession")
	if idx < 0 {
		t.Fatal("missing waitForTumblrSession")
	}
	waitFn := s[idx:]
	if i, j := strings.Index(waitFn, "chromeLooksLoggedIn"), strings.Index(waitFn, "cookiesFromDebugPort"); i < 0 || j < 0 || i > j {
		t.Fatal("session wait must treat a live dashboard as logged in before polling sid")
	}
}

func TestInvisibleRecaptchaDoesNotBlockLoginRefill(t *testing.T) {
	if !strings.Contains(tumblrCaptchaVisibleJS, "vis(f, 100, 40)") {
		t.Fatal("anchor reCAPTCHA checkbox must be detected at widget size")
	}
	if !strings.Contains(tumblrCaptchaVisibleJS, "vis(f, 120, 60)") {
		t.Fatal("a captcha challenge iframe must be visibly challenge-sized")
	}
	if !strings.Contains(tumblrCaptchaVisibleJS, "complete the captcha") {
		t.Fatal("visible challenge copy should still be detected")
	}
	if !strings.Contains(tumblrCaptchaVisibleJS, "check the box above") {
		t.Fatal("Tumblr 'Check the box above to continue' must count as captcha")
	}
	src, err := os.ReadFile("chrome.go")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(src), "clickRecaptchaCheckboxIfPresent") {
		t.Fatal("login must try the I'm-not-a-robot checkbox then wait")
	}
}

func TestEditAppearanceRequiresConfirmedEditor(t *testing.T) {
	for _, name := range []string{"chrome_look.go", "profile_setup.go"} {
		src, err := os.ReadFile(name)
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(string(src), "no appearance changes were made") {
			t.Fatalf("%s: an unconfirmed editor must stop before changing appearance", name)
		}
	}
}

func TestIsCannotPostLoginBody(t *testing.T) {
	if !isCannotPostLoginBody("Cannot POST /login") {
		t.Fatal("expected express login miss")
	}
	if !isCannotPostLoginBody("<title>Error</title>\n<pre>Cannot POST /login</pre>") {
		t.Fatal("expected html error page")
	}
	if isCannotPostLoginBody("Log in to Tumblr") {
		t.Fatal("normal login page should not match")
	}
}

func TestTumblrCannotPostLoginJS(t *testing.T) {
	if !strings.Contains(tumblrCannotPostLoginJS, "cannot post") {
		t.Fatal("detector JS should look for Cannot POST /login")
	}
	if !strings.Contains(tumblrLoginHydratedJS, "__react") {
		t.Fatal("hydration wait should look for React on the login button")
	}
}

func TestEditAppearanceDoesNotRetryClickDuringWait(t *testing.T) {
	src, err := os.ReadFile("chrome_look.go")
	if err != nil {
		t.Fatal(err)
	}
	s := string(src)
	start := strings.Index(s, "func cdpEnsureEditAppearance")
	if start < 0 {
		t.Fatal("missing cdpEnsureEditAppearance")
	}
	end := strings.Index(s[start+1:], "\nfunc ")
	if end < 0 {
		t.Fatal("could not bound cdpEnsureEditAppearance")
	}
	body := s[start : start+1+end]
	if strings.Count(body, "chromeClickEditAppearanceJS") > 1 {
		t.Fatal("retry-clicking during the wait opened Edit theme")
	}
	saveStart := strings.Index(s, "func clickAppearanceSaveOnTarget")
	if saveStart < 0 {
		t.Fatal("missing clickAppearanceSaveOnTarget")
	}
	saveEnd := strings.Index(s[saveStart+1:], "\nfunc ")
	if saveEnd < 0 {
		t.Fatal("could not bound clickAppearanceSaveOnTarget")
	}
	saveBody := s[saveStart : saveStart+1+saveEnd]
	if !strings.Contains(saveBody, "Confirmed photo crop") || !strings.Contains(saveBody, "Clicked: Save appearance") || !strings.Contains(saveBody, "deadline") {
		t.Fatal("crop confirmation must be followed by a separate bounded wait for Save")
	}
	if strings.Contains(saveBody, "mouseClickOnChromeTarget") {
		t.Fatal("coordinate save clicks missed Done/Save")
	}
}

func TestAppearanceSaveHitPicksApplyThenSave(t *testing.T) {
	if !strings.Contains(chromeAppearanceSaveHitJS, "apply") || !strings.Contains(chromeAppearanceSaveHitJS, "save") {
		t.Fatal("save-hit JS should locate Apply then Save")
	}
	if !strings.Contains(chromeAppearanceEditorOpenJS, "Edit blog avatar image") {
		t.Fatal("editor-open JS should detect the avatar editor")
	}
	if strings.Contains(chromeAppearanceEditorOpenJS, "save changes") {
		t.Fatal("editor-open must not treat Save as the editor")
	}
}

func TestDumpLiveAppearance(t *testing.T) {
	if os.Getenv("TUMBLR_DUMP") != "1" {
		t.Skip("live dump")
	}
	port := strings.TrimSpace(os.Getenv("TUMBLR_DEBUG_PORT"))
	if port == "" {
		port = "9342"
	}
	liveDebugPort = port
	nativeChromeClicks = true
	tpage, ok := appearancePageTarget()
	if !ok {
		t.Fatal("no appearance tab")
	}
	var dump string
	if err := evalJSONOnChromeTarget(tpage, chromePageDumpJS, &dump); err != nil {
		t.Fatal(err)
	}
	t.Log(dump)
	var av string
	js := `(() => {
		const imgs = [...document.querySelectorAll('img')].slice(0, 8).map(i => (i.src || '').slice(0, 120));
		const files = document.querySelectorAll('input[type=file]').length;
		const save = [...document.querySelectorAll('button, [role="button"]')].map(b => (b.innerText || b.getAttribute('aria-label') || '').replace(/\s+/g,' ').trim()).filter(Boolean).slice(0, 30);
		return JSON.stringify({files, imgs, save, href: location.href});
	})()`
	if err := evalJSONOnChromeTarget(tpage, js, &av); err != nil {
		t.Fatal(err)
	}
	t.Log(av)
	saveTargetShot(tpage, "live-appearance.png")
}

func TestShouldRefuseGatedNavFromLogin(t *testing.T) {
	login := "https://www.tumblr.com/login?redirect_to=%2Fdashboard&source=login_register_required"
	if !shouldRefuseGatedNav(login, "https://www.tumblr.com/settings/account") {
		t.Fatal("must not navigate a login tab to settings")
	}
	if !shouldRefuseGatedNav(login, "https://www.tumblr.com/dashboard") {
		t.Fatal("must not navigate a login tab to dashboard")
	}
	if shouldRefuseGatedNav("https://www.tumblr.com/dashboard", "https://www.tumblr.com/settings/account") {
		t.Fatal("dashboard may navigate to settings")
	}
}

func TestPickTabForNavigateSkipsLogin(t *testing.T) {
	login := chromeJSONTarget{ID: "login", Type: "page", URL: "https://www.tumblr.com/login?redirect_to=%2Fdashboard&source=login_register_required"}
	dash := chromeJSONTarget{ID: "dash", Type: "page", URL: "https://www.tumblr.com/dashboard"}
	got := pickTabForNavigate([]chromeJSONTarget{login, dash}, "https://www.tumblr.com/settings/account")
	if got.ID != "dash" {
		t.Fatalf("wanted dashboard tab, got %+v", got)
	}
}

func TestCloseChromeTargetKeepsLastTab(t *testing.T) {
	src, err := os.ReadFile("chrome_look.go")
	if err != nil {
		t.Fatal(err)
	}
	s := string(src)
	start := strings.Index(s, "func closeChromeTarget")
	end := strings.Index(s[start+1:], "\nfunc ")
	if start < 0 || end < 0 {
		t.Fatal("could not bound closeChromeTarget")
	}
	body := s[start : start+1+end]
	if !strings.Contains(body, "pages <= 1") {
		t.Fatal("must not close the last Chrome tab")
	}
}

func TestParseNetstatListeningPIDs(t *testing.T) {
	out := "" +
		"  TCP    0.0.0.0:9343           0.0.0.0:0              LISTENING       12345\n" +
		"  TCP    127.0.0.1:9343         127.0.0.1:50000        ESTABLISHED     99999\n" +
		"  TCP    0.0.0.0:19343          0.0.0.0:0              LISTENING       11111\n" +
		"  TCP    [::]:9343             [::]:0                 LISTENING       12345\n"
	got := parseNetstatListeningPIDs(out, "9343")
	if len(got) != 1 || got[0] != 12345 {
		t.Fatalf("want [12345], got %v", got)
	}
}

func TestCookieRecoveryStartsFreshChrome(t *testing.T) {
	src, err := os.ReadFile("chrome.go")
	if err != nil {
		t.Fatal(err)
	}
	s := string(src)
	for _, need := range []string{
		"func ensureFreshChromeForAccount(",
		"func killListenersOnPort(",
		"func killChromeWithUserDataDir(",
		"ensureFreshChromeForAccount(id)",
	} {
		if !strings.Contains(s, need) {
			t.Fatalf("missing %q — cookie recovery must close leftover Chrome then start fresh", need)
		}
	}
	start := strings.Index(s, "func startChromeLoggedInOpts")
	if start < 0 {
		t.Fatal("missing startChromeLoggedInOpts")
	}
	end := strings.Index(s[start+1:], "\nfunc ")
	body := s[start : start+1+end]
	if !strings.Contains(body, "ensureFreshChromeForAccount(id)") {
		t.Fatal("startChromeLoggedInOpts must kill leftover Chrome before launching")
	}
	loginStart := strings.Index(s, "func loginChromeSession")
	loginEnd := strings.Index(s[loginStart+1:], "\nfunc ")
	loginBody := s[loginStart : loginStart+1+loginEnd]
	if !strings.Contains(loginBody, "Closing bot Chrome for this profile") {
		t.Fatal("loginChromeSession must close Chrome after cookie sync")
	}
}
