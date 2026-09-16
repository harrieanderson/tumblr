package main

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/chromedp/cdproto/browser"
	"github.com/chromedp/cdproto/network"
	"github.com/chromedp/cdproto/target"
	"github.com/chromedp/chromedp"
	"github.com/gobwas/ws"
	"github.com/gobwas/ws/wsutil"
)

const (
	chromeDataDir   = "chrome-data"
	debugPort       = "9333"
	credentialsFile = "config/credentials.json"
)

// liveDebugPort is the port of the Chrome we just launched. Empty means do not
// attach to whatever happens to be listening on 9333.
var liveDebugPort string

// liveChromeCtx stays attached for the whole Chrome process. Cancelling a
// chromedp WithTargetID context closes that tab; if it is the last tab, Chrome
// exits. Cookie polls must not do that.
var (
	liveChromeCtx      context.Context
	liveChromeCancel   context.CancelFunc
	liveChromePort     string
	liveTargetID       target.ID
	liveChromeAccount  string
	chromeLoginMu      sync.Mutex
	skipChromeRecovery bool
	// quietChromeLogin: reach may open Chrome to mint a new cookie, but must
	// not steal the mouse or wait for you to finish captcha by hand.
	quietChromeLogin bool
	sessionDead      sync.Map
)

type Credentials struct {
	Email    string `json:"email"`
	Password string `json:"password"`
}

func loadCredentials() (Credentials, error) {
	if acc, ok := currentAccount(); ok {
		email := strings.TrimSpace(acc.Email)
		pass := strings.TrimSpace(acc.Password)
		if email != "" && pass != "" {
			return Credentials{Email: email, Password: pass}, nil
		}
	}
	raw, err := os.ReadFile(credentialsFile)
	if err != nil {
		return Credentials{}, err
	}
	var c Credentials
	if err := json.Unmarshal(raw, &c); err != nil {
		return Credentials{}, err
	}
	c.Email = strings.TrimSpace(c.Email)
	c.Password = strings.TrimSpace(c.Password)
	if c.Email == "" || c.Password == "" {
		return Credentials{}, fmt.Errorf("email/password empty in %s", credentialsFile)
	}
	return c, nil
}

func loadCredentialsFor(auth *Auth) (Credentials, error) {
	if auth != nil {
		if id := accountIDFromAuth(*auth); id != "" {
			if acc, ok := findAccount(id); ok {
				email := strings.TrimSpace(acc.Email)
				pass := strings.TrimSpace(acc.Password)
				if email != "" && pass != "" {
					return Credentials{Email: email, Password: pass}, nil
				}
			}
		}
	}
	return loadCredentials()
}

func chromePath() (string, error) {
	var candidates []string
	if p, err := exec.LookPath("chrome.exe"); err == nil {
		candidates = append(candidates, p)
	}
	if p, err := exec.LookPath("chrome"); err == nil {
		candidates = append(candidates, p)
	}
	candidates = append(candidates,
		os.Getenv("PROGRAMFILES")+`\Google\Chrome\Application\chrome.exe`,
		os.Getenv("ProgramFiles(x86)")+`\Google\Chrome\Application\chrome.exe`,
		os.Getenv("LOCALAPPDATA")+`\Google\Chrome\Application\chrome.exe`,
		os.Getenv("PROGRAMFILES")+`\Google\Chrome Beta\Application\chrome.exe`,
		os.Getenv("LOCALAPPDATA")+`\Google\Chrome Beta\Application\chrome.exe`,
	)
	seen := map[string]bool{}
	for _, p := range candidates {
		if p == "" || strings.HasPrefix(p, `\`) || seen[p] {
			continue
		}
		seen[p] = true
		if _, err := os.Stat(p); err == nil {
			return p, nil
		}
	}
	return "", fmt.Errorf("chrome.exe not found — install Google Chrome from https://www.google.com/chrome/")
}

func profileDir() (string, error) {
	dir, err := filepath.Abs(chromeProfilePath())
	if err != nil {
		return "", err
	}
	return dir, os.MkdirAll(dir, 0755)
}

func debugEndpoint() string {
	port := strings.TrimSpace(liveDebugPort)
	if port == "" {
		port = currentDebugPort()
	}
	return "http://127.0.0.1:" + port
}

func portInUse(port string) bool {
	c, err := net.DialTimeout("tcp", "127.0.0.1:"+port, 200*time.Millisecond)
	if err != nil {
		return false
	}
	_ = c.Close()
	return true
}

func pickFreeDebugPort(preferred string) string {
	if preferred != "" && !portInUse(preferred) {
		return preferred
	}
	start := 9400
	if n, err := strconv.Atoi(preferred); err == nil && n > 0 {
		start = n + 80
	}
	for p := start; p < start+200; p++ {
		s := strconv.Itoa(p)
		if !portInUse(s) {
			return s
		}
	}
	return preferred
}

func stopChrome(cmd *exec.Cmd) {
	closeBotChrome()
	liveDebugPort = ""
	liveChromeAccount = ""
	if cmd == nil || cmd.Process == nil {
		return
	}
	if runtime.GOOS == "windows" {
		_ = exec.Command("taskkill", "/PID", strconv.Itoa(cmd.Process.Pid), "/T", "/F").Run()
		return
	}
	_ = cmd.Process.Kill()
	_ = cmd.Wait()
}

// loginWithRealChrome opens the bot Chrome profile and logs into Tumblr.
// If credentials exist, it fills email/password automatically.
// Skips the form when the profile already has a sid, unless recovery
// asked for a forced login (API already rejected that session).
func loginWithRealChrome(auth *Auth) error {
	prevQuiet := quietChromeLogin
	quietChromeLogin = false
	defer func() { quietChromeLogin = prevQuiet }()

	chromeLoginMu.Lock()
	defer chromeLoginMu.Unlock()
	return loginWithRealChromeLocked(auth)
}

func loginWithRealChromeLocked(auth *Auth) error {
	return loginChromeSession(auth, false)
}

func loginChromeSession(auth *Auth, forceForm bool) error {
	if skipChromeRecovery {
		return fmt.Errorf("Chrome login blocked during reach")
	}
	prevClicks := nativeChromeClicks
	if !quietChromeLogin {
		nativeChromeClicks = true
	}
	defer func() { nativeChromeClicks = prevClicks }()

	restore, err := activateAccountForAuth(auth)
	if err != nil {
		return err
	}
	if restore != nil {
		defer restore()
	}

	cmd, err := startChromeLoggedInMode(auth, forceForm)
	if err != nil {
		if cmd != nil {
			stopChrome(cmd)
			time.Sleep(2 * time.Second)
		}
		return err
	}
	fmt.Println("Synced cookies. Closing bot Chrome for this profile.")
	stopChrome(cmd)
	time.Sleep(2 * time.Second)
	return nil
}

func startChromeLoggedIn(auth *Auth) (*exec.Cmd, error) {
	return startChromeLoggedInMode(auth, false)
}

func startChromeLoggedInMode(auth *Auth, forceForm bool) (*exec.Cmd, error) {
	if skipChromeRecovery {
		return nil, fmt.Errorf("Chrome login blocked during reach")
	}
	id := accountIDFromAuth(*auth)
	if id == "" {
		if acc, ok := currentAccount(); ok {
			id = acc.ID
		}
	}
	dir, err := chromeDirForAccount(id)
	if err != nil {
		return nil, err
	}
	chrome, err := chromePath()
	if err != nil {
		return nil, err
	}

	detachLiveChrome()

	creds, credErr := loadCredentialsFor(auth)
	auto := credErr == nil
	preferred := debugPortForAccount(id)
	port := pickFreeDebugPort(preferred)
	liveDebugPort = port
	liveChromeAccount = id

	if creds.Email != "" {
		fmt.Println("Account:", id, "("+creds.Email+")")
	} else if acc, ok := currentAccount(); ok {
		fmt.Println("Account:", acc.ID, "("+acc.Email+")")
	}
	fmt.Println("Profile folder:", dir)
	fmt.Println("Debug port:", port)
	if port != preferred {
		fmt.Println("Port", preferred, "was already in use — using", port, "so we do not attach to another Chrome.")
	}
	fmt.Println()
	if auto {
		fmt.Println("Auto-login enabled for this account.")
	} else {
		fmt.Println("No credentials for this account — will reuse an existing Chrome session if present.")
		fmt.Println("Add email:password pairs in config/credentials.json")
	}
	fmt.Println()

	markChromeCleanExit(dir)

	startURL := "https://www.tumblr.com/login"
	if auto {
		fmt.Println("Opening Tumblr login…")
	}

	cmd := exec.Command(chrome,
		"--user-data-dir="+dir,
		"--remote-debugging-port="+port,
		"--remote-debugging-address=127.0.0.1",
		"--no-first-run",
		"--no-default-browser-check",
		"--disable-session-crashed-bubble",
		"--hide-crash-restore-bubble",
		"--disable-infobars",
		"--noerrdialogs",
		"--disable-save-password-bubble",
		"--password-store=basic",
		"--disable-features=PasswordManager,PasswordLeakDetection,PasswordCheck,PasswordManagerOnboarding,SafetyCheck,SafetyHub,SafetyTip",
		startURL,
	)
	if err := cmd.Start(); err != nil {
		liveDebugPort = ""
		return nil, err
	}
	ensurePasswordNagWatcher()

	if err := waitForDebugPort(12 * time.Second); err != nil {
		if p := readDevToolsPort(dir); p != "" {
			liveDebugPort = p
			fmt.Println("Using Chrome debug port from profile:", p)
		} else {
			stopChrome(cmd)
			return nil, fmt.Errorf("%w (is another Chrome already using debug port %s?)", err, port)
		}
	} else if p := readDevToolsPort(dir); p != "" && p != liveDebugPort {
		liveDebugPort = p
	}

	if err := waitForPageTarget(8 * time.Second); err != nil {
		stopChrome(cmd)
		return nil, err
	}
	if id, err := existingPageTargetID(); err == nil {
		closeExtraChromeTabs(id)
	}

	hasSid := waitForTumblrSession(6*time.Second) == nil
	switch {
	case hasSid && !forceForm:
		fmt.Println("Already logged in — syncing cookies…")
	case auto && forceForm:
		fmt.Println("API rejected this session — logging out of Chrome and signing in again…")
		if err := finishChromeLoginWait(cmd, reloginTumblrInChrome(creds)); err != nil {
			return nil, err
		}
	case auto:
		fmt.Println("Not logged in — auto-filling Tumblr login…")
		if err := finishChromeLoginWait(cmd, autoFillTumblrLogin(creds)); err != nil {
			return nil, err
		}
	default:
		if forceForm && hasSid {
			fmt.Println("Need a fresh login — logging out. Finish sign-in in Chrome…")
			_ = chromeLogoutOnly()
		}
		fmt.Println("Waiting up to 3 minutes for login in Chrome…")
		if err := waitForTumblrSession(3 * time.Minute); err != nil {
			stopChrome(cmd)
			return nil, fmt.Errorf("login did not complete: %w", err)
		}
	}

	cookies, err := cookiesFromDebugPort()
	if err != nil {
		stopChrome(cmd)
		return nil, err
	}

	sid := cookieValue(cookies, "sid")
	if sid == "" {
		stopChrome(cmd)
		return nil, fmt.Errorf("still no sid cookie — login did not complete")
	}

	auth.Cookie = buildTumblrCookieHeader(cookies)
	persistMaturePref(auth)
	if err := saveAuth(*auth); err != nil {
		stopChrome(cmd)
		return nil, err
	}

	client := newHumanClient()
	if err := refreshCSRF(client, auth); err != nil {
		fmt.Println("CSRF refresh warning:", err)
		if !forceForm && auto {
			fmt.Println("Saved session is dead — logging out of Chrome and signing in again…")
			if err := finishChromeLoginWait(cmd, reloginTumblrInChrome(creds)); err != nil {
				stopChrome(cmd)
				return nil, err
			}
			cookies, err = cookiesFromDebugPort()
			if err != nil {
				stopChrome(cmd)
				return nil, err
			}
			if cookieValue(cookies, "sid") == "" {
				stopChrome(cmd)
				return nil, fmt.Errorf("still no sid cookie — login did not complete")
			}
			auth.Cookie = buildTumblrCookieHeader(cookies)
			persistMaturePref(auth)
			if err := saveAuth(*auth); err != nil {
				stopChrome(cmd)
				return nil, err
			}
			if err := refreshCSRF(client, auth); err != nil {
				stopChrome(cmd)
				return nil, fmt.Errorf("Tumblr still unauthorized after login: %w", err)
			}
		} else {
			stopChrome(cmd)
			return nil, fmt.Errorf("Tumblr still unauthorized after login: %w", err)
		}
	}
	if name, err := syncAuthIdentity(client, auth); err != nil {
		maybeFillBlog(client, auth)
		if name2, err2 := syncAuthIdentity(client, auth); err2 != nil {
			fmt.Println("Could not detect blog identity yet:", err)
		} else {
			fmt.Println("Active blog:", name2, "("+auth.Blog+")")
		}
	} else {
		fmt.Println("Active blog:", name, "("+auth.Blog+")")
	}
	if nativeChromeClicks {
		for i := 0; i < 5; i++ {
			if nag := dismissSavePasswordBubble(); nagWasDismissed(nag) {
				fmt.Println("Dismissed:", nag)
				break
			}
			time.Sleep(350 * time.Millisecond)
		}
		if hwnd := findWindowContaining("Tumblr"); hwnd != 0 {
			focusWindow(hwnd)
			sendVirtualKey(vkEscape)
		}
	}
	return cmd, nil
}

func finishChromeLoginWait(cmd *exec.Cmd, fillErr error) error {
	if isBadLogin(fillErr) {
		var b badLoginError
		_ = errors.As(fillErr, &b)
		fmt.Println("Auto-login failed:", fillErr)
		stopChrome(cmd)
		time.Sleep(1 * time.Second)
		_ = discardDeadLogin(b.Email)
		return fillErr
	}
	if fillErr != nil {
		fmt.Println("Auto-login failed:", fillErr)
		waitForYou := 90 * time.Second
		if !quietChromeLogin {
			fmt.Println("Waiting up to 2 minutes for you to finish login in Chrome…")
			waitForYou = 2 * time.Minute
		} else {
			fmt.Println("Waiting up to 90s for the session without touching the mouse…")
		}
		if err := waitForTumblrSession(waitForYou); err != nil {
			stopChrome(cmd)
			return fmt.Errorf("login did not complete: %w", err)
		}
		return nil
	}
	fmt.Println("Login submitted — waiting for session…")
	if err := waitForTumblrSession(90 * time.Second); err != nil {
		if pageShowsBadLogin() {
			email := ""
			if acc, ok := currentAccount(); ok {
				email = acc.Email
			}
			fmt.Println("Tumblr says this email/password is wrong.")
			stopChrome(cmd)
			time.Sleep(1 * time.Second)
			_ = discardDeadLogin(email)
			return badLoginError{Email: email, ID: accountSlug(email)}
		}
		extra := 90 * time.Second
		if !quietChromeLogin {
			extra = 2 * time.Minute
		}
		fmt.Println("Waiting a bit longer for captcha/2FA in Chrome…")
		if err := waitForTumblrSession(extra); err != nil {
			if pageShowsBadLogin() {
				email := ""
				if acc, ok := currentAccount(); ok {
					email = acc.Email
				}
				stopChrome(cmd)
				time.Sleep(1 * time.Second)
				_ = discardDeadLogin(email)
				return badLoginError{Email: email, ID: accountSlug(email)}
			}
			stopChrome(cmd)
			return fmt.Errorf("login did not complete: %w", err)
		}
	}
	return nil
}

func pageShowsBadLogin() bool {
	ctx, cancel, err := withBotChrome(8 * time.Second)
	if err != nil {
		return false
	}
	defer cancel()
	var bad bool
	_ = chromedp.Run(ctx, chromedp.ActionFunc(func(ctx context.Context) error {
		bad = evalBoolJS(ctx, tumblrLoginErrorJS)
		return nil
	}))
	return bad
}

func clearTumblrSessionCookies(ctx context.Context) error {
	cookies, err := network.GetCookies().WithURLs([]string{
		"https://www.tumblr.com",
		"https://tumblr.com",
	}).Do(ctx)
	if err != nil {
		return err
	}
	for _, c := range cookies {
		switch strings.ToLower(c.Name) {
		case "sid", "pfg", "logged_in":
		default:
			continue
		}
		del := network.DeleteCookies(c.Name)
		if c.Domain != "" {
			del = del.WithDomain(c.Domain)
		}
		if c.Path != "" {
			del = del.WithPath(c.Path)
		}
		_ = del.Do(ctx)
	}
	return nil
}

func chromeLogoutOnly() error {
	ctx, cancel, err := withBotChrome(30 * time.Second)
	if err != nil {
		return err
	}
	defer cancel()
	return chromedp.Run(ctx,
		chromedp.Navigate("https://www.tumblr.com/logout"),
		chromedp.Sleep(2*time.Second),
		chromedp.ActionFunc(clearTumblrSessionCookies),
		chromedp.Navigate("https://www.tumblr.com/login"),
		chromedp.WaitReady("body", chromedp.ByQuery),
	)
}

func reloginTumblrInChrome(creds Credentials) error {
	ctx, cancel, err := withBotChrome(90 * time.Second)
	if err != nil {
		return err
	}
	defer cancel()

	return chromedp.Run(ctx,
		chromedp.Navigate("https://www.tumblr.com/logout"),
		chromedp.Sleep(2*time.Second),
		chromedp.ActionFunc(clearTumblrSessionCookies),
		chromedp.Navigate("https://www.tumblr.com/login"),
		chromedp.WaitReady("body", chromedp.ByQuery),
		chromedp.Sleep(1500*time.Millisecond),
		chromedp.ActionFunc(func(ctx context.Context) error {
			return fillTumblrLoginFormJS(ctx, creds)
		}),
	)
}

func autoFillTumblrLogin(creds Credentials) error {
	ctx, cancel, err := withBotChrome(90 * time.Second)
	if err != nil {
		return err
	}
	defer cancel()

	return chromedp.Run(ctx,
		chromedp.WaitReady("body", chromedp.ByQuery),
		chromedp.Sleep(1500*time.Millisecond),
		chromedp.ActionFunc(func(ctx context.Context) error {
			if cookieOK(ctx) {
				return nil
			}
			var href string
			_ = chromedp.Location(&href).Do(ctx)
			if strings.Contains(href, "/dashboard") {
				return nil
			}
			if !strings.Contains(href, "/login") {
				if err := chromedp.Navigate("https://www.tumblr.com/login").Do(ctx); err != nil {
					return err
				}
				_ = chromedp.WaitReady("body", chromedp.ByQuery).Do(ctx)
				_ = chromedp.Sleep(1500 * time.Millisecond).Do(ctx)
			}
			return fillTumblrLoginFormJS(ctx, creds)
		}),
	)
}

func cookieOK(ctx context.Context) bool {
	cookies, err := network.GetCookies().WithURLs([]string{"https://www.tumblr.com"}).Do(ctx)
	if err != nil {
		return false
	}
	return cookieValue(cookies, "sid") != ""
}

const tumblrVisibleEmailJS = `(() => {
	const vis = (el) => {
		if (!el) return false;
		const s = getComputedStyle(el);
		if (s.display === 'none' || s.visibility === 'hidden' || Number(s.opacity) === 0) return false;
		const r = el.getBoundingClientRect();
		return r.width > 2 && r.height > 2;
	};
	const nodes = [...document.querySelectorAll('input[name="email"], input[type="email"], input[placeholder="Email"], input[aria-label="email"], input[autocomplete="username"]')];
	return nodes.some(vis);
})()`

const tumblrVisiblePasswordJS = `(() => {
	const vis = (el) => {
		if (!el) return false;
		const s = getComputedStyle(el);
		if (s.display === 'none' || s.visibility === 'hidden' || Number(s.opacity) === 0) return false;
		const r = el.getBoundingClientRect();
		return r.width > 2 && r.height > 2;
	};
	const nodes = [...document.querySelectorAll('input[name="password"], input[type="password"], input[aria-label="password"], input[autocomplete="current-password"]')];
	return nodes.some(vis);
})()`

const tumblrLoginErrorJS = `(() => {
	const t = ((document.body && document.body.innerText) || '').toLowerCase();
	return /email or password (you entered )?(is|was|were) incorrect|incorrect email or password|that password is incorrect/.test(t);
})()`

func evalBoolJS(ctx context.Context, script string) bool {
	var ok bool
	_ = chromedp.Evaluate(script, &ok).Do(ctx)
	return ok
}

func waitJSTrue(ctx context.Context, timeout time.Duration, script string) error {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if evalBoolJS(ctx, script) {
			return nil
		}
		time.Sleep(400 * time.Millisecond)
	}
	return fmt.Errorf("timed out")
}

func fillVisibleInputJS(kind, value string) string {
	val, _ := json.Marshal(value)
	sel := `input[name="email"], input[type="email"], input[placeholder="Email"], input[aria-label="email"], input[autocomplete="username"]`
	if kind == "password" {
		sel = `input[name="password"], input[type="password"], input[aria-label="password"], input[autocomplete="current-password"]`
	}
	selJSON, _ := json.Marshal(sel)
	return fmt.Sprintf(`(() => {
		const vis = (el) => {
			if (!el) return false;
			const s = getComputedStyle(el);
			if (s.display === 'none' || s.visibility === 'hidden' || Number(s.opacity) === 0) return false;
			const r = el.getBoundingClientRect();
			return r.width > 2 && r.height > 2;
		};
		const nodes = [...document.querySelectorAll(%s)].filter(vis);
		const el = nodes[0];
		if (!el) return 'missing';
		el.focus();
		const desc = Object.getOwnPropertyDescriptor(window.HTMLInputElement.prototype, 'value');
		desc.set.call(el, '');
		el.dispatchEvent(new Event('input', { bubbles: true }));
		desc.set.call(el, %s);
		el.dispatchEvent(new Event('input', { bubbles: true }));
		el.dispatchEvent(new Event('change', { bubbles: true }));
		return 'ok';
	})()`, string(selJSON), string(val))
}

func clickTumblrLoginButton(ctx context.Context) error {
	script := `(() => {
		const vis = (el) => {
			if (!el) return false;
			const s = getComputedStyle(el);
			if (s.display === 'none' || s.visibility === 'hidden' || Number(s.opacity) === 0) return false;
			const r = el.getBoundingClientRect();
			return r.width > 2 && r.height > 2;
		};
		const label = (el) => (el.innerText || el.getAttribute('aria-label') || el.value || '').replace(/\s+/g, ' ').trim();
		const btns = [...document.querySelectorAll('button, [role="button"], input[type="submit"]')].filter(vis);
		const match = btns.find(b => /^(log in|sign in|next|continue|done)$/i.test(label(b)));
		if (!match) return 'no-btn';
		match.click();
		return 'ok';
	})()`
	var result string
	if err := chromedp.Evaluate(script, &result).Do(ctx); err != nil {
		return err
	}
	if result != "ok" {
		return fmt.Errorf("login button: %s", result)
	}
	return nil
}

func fillTumblrLoginFormJS(ctx context.Context, creds Credentials) error {
	email := strings.TrimSpace(creds.Email)
	pass := creds.Password
	if email == "" || strings.TrimSpace(pass) == "" {
		return fmt.Errorf("missing email/password for this account")
	}
	fmt.Println("Filling Tumblr login for", email)

	if err := waitJSTrue(ctx, 25*time.Second, tumblrVisibleEmailJS); err != nil {
		return fmt.Errorf("email field never appeared (already logged in, or page blocked)")
	}

	var filled string
	if err := chromedp.Evaluate(fillVisibleInputJS("email", email), &filled).Do(ctx); err != nil {
		return err
	}
	if filled != "ok" {
		return fmt.Errorf("login form: no visible email field")
	}

	if !evalBoolJS(ctx, tumblrVisiblePasswordJS) {
		_ = clickTumblrLoginButton(ctx)
		if err := waitJSTrue(ctx, 20*time.Second, tumblrVisiblePasswordJS); err != nil {
			return fmt.Errorf("password field never appeared")
		}
	}

	if err := chromedp.Evaluate(fillVisibleInputJS("password", pass), &filled).Do(ctx); err != nil {
		return err
	}
	if filled != "ok" {
		return fmt.Errorf("login form: no visible password field")
	}
	if err := clickTumblrLoginButton(ctx); err != nil {
		_ = chromedp.Evaluate(`document.querySelector('input[type="password"]') && document.querySelector('input[type="password"]').dispatchEvent(new KeyboardEvent('keydown', { key: 'Enter', bubbles: true }))`, nil).Do(ctx)
	}
	deadline := time.Now().Add(8 * time.Second)
	for time.Now().Before(deadline) {
		if evalBoolJS(ctx, tumblrLoginErrorJS) {
			return badLoginError{Email: email, ID: accountSlug(email)}
		}
		var href string
		_ = chromedp.Location(&href).Do(ctx)
		if strings.Contains(href, "/dashboard") || cookieOK(ctx) {
			return nil
		}
		time.Sleep(400 * time.Millisecond)
	}
	if evalBoolJS(ctx, tumblrLoginErrorJS) {
		return badLoginError{Email: email, ID: accountSlug(email)}
	}
	return nil
}

func waitForTumblrSession(timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if liveDebugPort == "" || !portInUse(liveDebugPort) {
			return fmt.Errorf("chrome closed")
		}
		cookies, err := cookiesFromDebugPort()
		if err == nil && cookieValue(cookies, "sid") != "" {
			return nil
		}
		if err != nil && !portInUse(liveDebugPort) {
			return fmt.Errorf("chrome closed: %w", err)
		}
		time.Sleep(2 * time.Second)
	}
	return fmt.Errorf("timed out waiting for sid cookie")
}

func waitForDebugPort(timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	url := debugEndpoint() + "/json/version"
	for time.Now().Before(deadline) {
		resp, err := http.Get(url)
		if err == nil {
			resp.Body.Close()
			if resp.StatusCode == 200 {
				return nil
			}
		}
		time.Sleep(500 * time.Millisecond)
	}
	return fmt.Errorf("chrome debug port %s not ready", strings.TrimPrefix(debugEndpoint(), "http://127.0.0.1:"))
}

func readDevToolsPort(dir string) string {
	data, err := os.ReadFile(filepath.Join(dir, "DevToolsActivePort"))
	if err != nil {
		return ""
	}
	line := strings.TrimSpace(strings.SplitN(string(data), "\n", 2)[0])
	if line == "" || !portInUse(line) {
		return ""
	}
	return line
}

func cookiesFromDebugPort() ([]*network.Cookie, error) {
	ctx, cancel, err := withBotChrome(20 * time.Second)
	if err != nil {
		return nil, err
	}
	defer cancel()

	var cookies []*network.Cookie
	err = chromedp.Run(ctx,
		chromedp.ActionFunc(func(ctx context.Context) error {
			_ = network.Enable().Do(ctx)
			var err error
			cookies, err = network.GetCookies().WithURLs([]string{
				"https://www.tumblr.com",
				"https://tumblr.com",
			}).Do(ctx)
			return err
		}),
	)
	return cookies, err
}

type chromeJSONTarget struct {
	ID                   string `json:"id"`
	Type                 string `json:"type"`
	URL                  string `json:"url"`
	WebSocketDebuggerURL string `json:"webSocketDebuggerUrl"`
}

func chromeTargetWSURL(t chromeJSONTarget) string {
	if u := strings.TrimSpace(t.WebSocketDebuggerURL); u != "" {
		return u
	}
	if t.ID == "" {
		return ""
	}
	return strings.Replace(debugEndpoint(), "http://", "ws://", 1) + "/devtools/page/" + t.ID
}

// evalJSONOnChromeTarget runs JS in a Chrome target and unmarshals the value.
// It only closes the websocket, so Chrome and the tab stay open.
func evalJSONOnChromeTarget(t chromeJSONTarget, expression string, out any) error {
	return withChromeTargetCDP(t, func(call cdpCall) error {
		raw, err := call("Runtime.evaluate", map[string]any{
			"expression":    expression,
			"returnByValue": true,
			"awaitPromise":  false,
		})
		if err != nil {
			return err
		}
		var wrap struct {
			Result struct {
				Value json.RawMessage `json:"value"`
			} `json:"result"`
		}
		if err := json.Unmarshal(raw, &wrap); err != nil {
			return err
		}
		if out == nil || len(wrap.Result.Value) == 0 || string(wrap.Result.Value) == "null" {
			return nil
		}
		return json.Unmarshal(wrap.Result.Value, out)
	})
}

type cdpCall func(method string, params any) (json.RawMessage, error)

func withChromeTargetCDP(t chromeJSONTarget, fn func(cdpCall) error) error {
	wsURL := chromeTargetWSURL(t)
	if wsURL == "" {
		return fmt.Errorf("no debugger url")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	conn, br, _, err := ws.Dial(ctx, wsURL)
	if err != nil {
		return err
	}
	defer conn.Close()
	var rw io.ReadWriter = conn
	if br != nil && br.Buffered() > 0 {
		rw = struct {
			io.Reader
			io.Writer
		}{io.MultiReader(br, conn), conn}
	}
	next := 0
	call := func(method string, params any) (json.RawMessage, error) {
		next++
		id := next
		payload := map[string]any{"id": id, "method": method}
		if params != nil {
			payload["params"] = params
		}
		raw, err := json.Marshal(payload)
		if err != nil {
			return nil, err
		}
		if err := wsutil.WriteClientText(rw, raw); err != nil {
			return nil, err
		}
		deadline := time.Now().Add(6 * time.Second)
		for time.Now().Before(deadline) {
			_ = conn.SetReadDeadline(time.Now().Add(2 * time.Second))
			data, err := wsutil.ReadServerText(rw)
			if err != nil {
				return nil, err
			}
			var msg struct {
				ID     int             `json:"id"`
				Result json.RawMessage `json:"result"`
				Error  json.RawMessage `json:"error"`
			}
			if json.Unmarshal(data, &msg) != nil || msg.ID != id {
				continue
			}
			if len(msg.Error) > 0 && string(msg.Error) != "null" {
				return nil, fmt.Errorf("cdp %s: %s", method, string(msg.Error))
			}
			return msg.Result, nil
		}
		return nil, fmt.Errorf("cdp %s timeout", method)
	}
	return fn(call)
}

func mouseClickOnChromeTarget(t chromeJSONTarget, x, y float64) error {
	return withChromeTargetCDP(t, func(call cdpCall) error {
		_, err := call("Input.dispatchMouseEvent", map[string]any{
			"type": "mousePressed", "x": x, "y": y, "button": "left", "clickCount": 1,
		})
		if err != nil {
			return err
		}
		_, err = call("Input.dispatchMouseEvent", map[string]any{
			"type": "mouseReleased", "x": x, "y": y, "button": "left", "clickCount": 1,
		})
		return err
	})
}

func listChromeTargets() ([]chromeJSONTarget, error) {
	resp, err := http.Get(debugEndpoint() + "/json/list")
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	var list []chromeJSONTarget
	if err := json.NewDecoder(resp.Body).Decode(&list); err != nil {
		return nil, err
	}
	return list, nil
}

func existingPageTargetID() (target.ID, error) {
	list, err := listChromeTargets()
	if err != nil {
		return "", err
	}
	var loginID, tumblrID, fallback string
	for _, t := range list {
		if t.Type != "page" || t.ID == "" {
			continue
		}
		if strings.HasPrefix(t.URL, "chrome://") || strings.HasPrefix(t.URL, "devtools://") {
			continue
		}
		if fallback == "" {
			fallback = t.ID
		}
		if !strings.Contains(t.URL, "tumblr.com") {
			continue
		}
		if tumblrID == "" {
			tumblrID = t.ID
		}
		if strings.Contains(t.URL, "/login") && loginID == "" {
			loginID = t.ID
		}
	}
	switch {
	case loginID != "":
		return target.ID(loginID), nil
	case tumblrID != "":
		return target.ID(tumblrID), nil
	case fallback != "":
		return target.ID(fallback), nil
	default:
		return "", fmt.Errorf("no chrome page target")
	}
}

func waitForPageTarget(timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if _, err := existingPageTargetID(); err == nil {
			return nil
		}
		time.Sleep(250 * time.Millisecond)
	}
	return fmt.Errorf("chrome opened but no page tab appeared")
}

func closeExtraChromeTabs(keep target.ID) {
	list, err := listChromeTargets()
	if err != nil {
		return
	}
	keepID := string(keep)
	for _, t := range list {
		if t.Type != "page" || t.ID == "" || t.ID == keepID || t.ID == string(liveTargetID) {
			continue
		}
		if strings.Contains(strings.ToLower(t.URL), "k-id.com") {
			continue
		}
		u := debugEndpoint() + "/json/close/" + t.ID
		resp, err := http.Get(u)
		if err == nil {
			resp.Body.Close()
		}
	}
}

func clickUseAgeKeyOnKidTarget() string {
	list, err := listChromeTargets()
	if err != nil {
		return ""
	}
	clickJS := `(() => {
		const vis = (el) => {
			if (!el || !el.getBoundingClientRect) return false;
			const r = el.getBoundingClientRect();
			return r.width > 2 && r.height > 2;
		};
		const textOf = (el) => (el.innerText || el.textContent || el.getAttribute('aria-label') || '').replace(/\s+/g, ' ').trim();
		const acc = [];
		const visit = (root) => {
			if (!root || !root.querySelectorAll) return;
			for (const el of root.querySelectorAll('button, a, [role="button"], [tabindex], div, span, li, p, h1, h2, h3, label')) {
				if (!vis(el)) continue;
				const t = textOf(el);
				if (!t || t.length > 160) continue;
				if (!/use your agekey/i.test(t)) continue;
				if (/selfie|use your id|photo id|verify your age|already have|create my/i.test(t) && !/^use your agekey/i.test(t)) continue;
				acc.push({el, t});
				if (el.shadowRoot) visit(el.shadowRoot);
			}
		};
		visit(document);
		acc.sort((a,b) => a.t.length - b.t.length);
		if (!acc[0]) return {t:'', x: window.innerWidth*0.5, y: window.innerHeight*0.55};
		const el = acc[0].el.closest('button, a, [role="button"]') || acc[0].el;
		el.scrollIntoView({block:'center', inline:'nearest'});
		const r = el.getBoundingClientRect();
		el.click();
		return {t: acc[0].t.slice(0, 80), x: r.left + r.width/2, y: r.top + r.height/2};
	})()`
	for _, t := range kidOrAgeKeyTargets(list) {
		if strings.Contains(strings.ToLower(t.URL), "agekey.org") {
			continue
		}
		fmt.Println("AgeKey frame:", chromeTargetHost(t.URL))
		var box struct {
			T string  `json:"t"`
			X float64 `json:"x"`
			Y float64 `json:"y"`
		}
		if err := evalJSONOnChromeTarget(t, clickJS, &box); err != nil {
			continue
		}
		if box.X > 1 && box.Y > 1 {
			_ = mouseClickOnChromeTarget(t, box.X, box.Y)
		}
		if box.T != "" {
			return box.T
		}
	}
	return ""
}

func chromeTargetHost(raw string) string {
	u := raw
	if i := strings.Index(u, "://"); i >= 0 {
		u = u[i+3:]
	}
	if i := strings.IndexAny(u, "/?"); i >= 0 {
		u = u[:i]
	}
	if u == "" {
		return "(unknown)"
	}
	return u
}

func ageKeyFrameRank(raw string) int {
	u := strings.ToLower(raw)
	switch {
	case strings.Contains(u, "auth.agekey.org"):
		return 0
	case strings.Contains(u, "agekey.org"), strings.Contains(u, "agekey.com"):
		return 1
	case strings.Contains(u, "k-id.com"):
		return 2
	default:
		return 3
	}
}

func kidOrAgeKeyTargets(list []chromeJSONTarget) []chromeJSONTarget {
	var out []chromeJSONTarget
	for _, t := range list {
		if t.Type != "page" && t.Type != "iframe" {
			continue
		}
		u := strings.ToLower(t.URL)
		if strings.Contains(u, "k-id.com") || strings.Contains(u, "agekey") {
			out = append(out, t)
		}
	}
	sort.SliceStable(out, func(i, j int) bool {
		return ageKeyFrameRank(out[i].URL) < ageKeyFrameRank(out[j].URL)
	})
	return out
}

func ageKeyOrgTargets(list []chromeJSONTarget) []chromeJSONTarget {
	var out []chromeJSONTarget
	for _, t := range kidOrAgeKeyTargets(list) {
		u := strings.ToLower(t.URL)
		if strings.Contains(u, "agekey.org") || strings.Contains(u, "agekey.com") {
			out = append(out, t)
		}
	}
	return out
}

func chromeHasTargetHost(substr string) bool {
	list, err := listChromeTargets()
	if err != nil {
		return false
	}
	want := strings.ToLower(substr)
	for _, t := range list {
		if strings.Contains(strings.ToLower(t.URL), want) {
			return true
		}
	}
	return false
}

func isConsentTargetURL(raw string) bool {
	u := strings.ToLower(raw)
	if u == "" {
		return false
	}
	if strings.Contains(u, "agekey") || strings.Contains(u, "k-id.com") || strings.Contains(u, "chrome-extension") {
		return false
	}
	needles := []string{
		"consent", "quantcast", "onetrust", "didomi", "sourcepoint",
		"privacy-mgmt", "privacymanager", "cookiebot", "gdpr",
		"euconsent", "/privacy", "cmp.", "qc-cmp",
	}
	for _, n := range needles {
		if strings.Contains(u, n) {
			return true
		}
	}
	return false
}

func consentTargets(list []chromeJSONTarget) []chromeJSONTarget {
	var out []chromeJSONTarget
	for _, t := range list {
		if t.Type != "page" && t.Type != "iframe" {
			continue
		}
		if isConsentTargetURL(t.URL) {
			out = append(out, t)
		}
	}
	return out
}

func clickCookieAcceptOnConsentTargets() string {
	if ageKeyHandsOff() {
		return ""
	}
	if findWindowContaining("Windows Security") != 0 {
		return ""
	}
	list, err := listChromeTargets()
	if err != nil {
		return ""
	}
	for _, t := range consentTargets(list) {
		var hit string
		if err := evalJSONOnChromeTarget(t, chromeClickCookieJS, &hit); err != nil {
			continue
		}
		if hit = strings.TrimSpace(hit); hit != "" {
			return hit
		}
	}
	return ""
}

func cookieBannerOpen() bool {
	list, err := listChromeTargets()
	if err == nil && len(consentTargets(list)) > 0 {
		return true
	}
	var raw string
	if evalOnTumblrPage(chromeAgeStatusJS, &raw) != nil {
		return false
	}
	var st setupPageStatus
	if json.Unmarshal([]byte(raw), &st) != nil {
		return false
	}
	return st.CookieBanner
}

func waitForChromeTargetHost(substr string, d time.Duration) bool {
	deadline := time.Now().Add(d)
	for time.Now().Before(deadline) {
		if chromeHasTargetHost(substr) {
			return true
		}
		time.Sleep(350 * time.Millisecond)
	}
	return false
}

func tumblrPageTarget() (chromeJSONTarget, bool) {
	list, err := listChromeTargets()
	if err != nil {
		return chromeJSONTarget{}, false
	}
	for _, t := range list {
		if t.Type == "page" && strings.Contains(strings.ToLower(t.URL), "tumblr.com") {
			return t, true
		}
	}
	return chromeJSONTarget{}, false
}

func tumblrSettingsAccountTarget() (chromeJSONTarget, bool) {
	list, err := listChromeTargets()
	if err != nil {
		return chromeJSONTarget{}, false
	}
	for _, t := range list {
		if t.Type == "page" && strings.Contains(t.URL, "/settings/account") {
			return t, true
		}
	}
	return tumblrPageTarget()
}

func evalOnTumblrPage(expr string, out any) error {
	t, ok := tumblrPageTarget()
	if !ok {
		return fmt.Errorf("no tumblr page")
	}
	return evalJSONOnChromeTarget(t, expr, out)
}

func saveTargetShot(t chromeJSONTarget, name string) {
	var b64 string
	err := withChromeTargetCDP(t, func(call cdpCall) error {
		raw, err := call("Page.captureScreenshot", map[string]any{"format": "png"})
		if err != nil {
			return err
		}
		var wrap struct {
			Data string `json:"data"`
		}
		if err := json.Unmarshal(raw, &wrap); err != nil {
			return err
		}
		b64 = wrap.Data
		return nil
	})
	if err != nil || b64 == "" {
		return
	}
	buf, err := base64.StdEncoding.DecodeString(b64)
	if err != nil {
		return
	}
	_ = os.MkdirAll("scraped", 0755)
	path := filepath.Join("scraped", name)
	if err := os.WriteFile(path, buf, 0644); err != nil {
		return
	}
	fmt.Println("Screenshot:", path)
}

func clickAgeKeyCardOnTumblrPage() string {
	t, ok := tumblrPageTarget()
	if !ok {
		return ""
	}
	var box struct {
		X float64 `json:"x"`
		Y float64 `json:"y"`
		W float64 `json:"w"`
		H float64 `json:"h"`
	}
	js := `(() => {
		const frames = [...document.querySelectorAll('iframe')].map(el => {
			const r = el.getBoundingClientRect();
			return {src: el.src || '', x: r.left, y: r.top, w: r.width, h: r.height};
		}).filter(f => f.w > 240 && f.h > 280);
		const kid = frames.find(f => /k-id|agekey/i.test(f.src)) || [...frames].sort((a,b) => b.h - a.h)[0];
		if (!kid) return {x:0,y:0,w:0,h:0};
		return {x: kid.x, y: kid.y, w: kid.w, h: kid.h};
	})()`
	if err := evalJSONOnChromeTarget(t, js, &box); err != nil || box.W < 200 || box.H < 280 {
		return ""
	}
	x := box.X + box.W*0.5
	for _, frac := range []float64{0.70, 0.76} {
		_ = mouseClickOnChromeTarget(t, x, box.Y+box.H*frac)
		time.Sleep(400 * time.Millisecond)
	}
	fmt.Println("Clicked AgeKey card in k-ID window")
	return "Use your AgeKey"
}

func clickHaveAgeKeyOnKidTarget() string {
	list, err := listChromeTargets()
	if err != nil {
		return ""
	}
	clickJS := `(() => {
		const vis = (el) => {
			if (!el || !el.getBoundingClientRect) return false;
			const r = el.getBoundingClientRect();
			return r.width > 2 && r.height > 2;
		};
		const textOf = (el) => (el.innerText || el.textContent || el.getAttribute('aria-label') || '').replace(/\s+/g, ' ').trim();
		const acc = [];
		const visit = (root) => {
			if (!root || !root.querySelectorAll) return;
			for (const el of root.querySelectorAll('a, button, [role="button"], span, div, p, label')) {
				if (!vis(el)) continue;
				const t = textOf(el);
				if (/already have an agekey/i.test(t) && t.length < 64 && !/create my agekey/i.test(t)) {
					acc.push({el, t});
				}
				if (el.shadowRoot) visit(el.shadowRoot);
			}
		};
		visit(document);
		acc.sort((a,b) => a.t.length - b.t.length);
		if (!acc[0]) {
			return {t:'', x:0, y:0, body: ((document.body && document.body.innerText) || '').replace(/\s+/g,' ').slice(0, 160)};
		}
		const el = acc[0].el.closest('a, button, [role="button"]') || acc[0].el;
		el.scrollIntoView({block:'nearest', inline:'nearest'});
		const r = el.getBoundingClientRect();
		el.click();
		return {t: acc[0].t, x: r.left + r.width/2, y: r.top + r.height/2, body:''};
	})()`
	for _, t := range ageKeyOrgTargets(list) {
		fmt.Println("AgeKey frame:", chromeTargetHost(t.URL))
		var box struct {
			T    string  `json:"t"`
			X    float64 `json:"x"`
			Y    float64 `json:"y"`
			Body string  `json:"body"`
		}
		if err := evalJSONOnChromeTarget(t, clickJS, &box); err != nil {
			continue
		}
		if box.T != "" {
			if box.X > 1 && box.Y > 1 {
				_ = mouseClickOnChromeTarget(t, box.X, box.Y)
			}
			return box.T
		}
		if box.Body != "" && strings.Contains(strings.ToLower(box.Body), "already have") {
			fmt.Println("AgeKey page has the link but click missed.")
		}
	}
	return ""
}

func withBotChrome(timeout time.Duration) (context.Context, context.CancelFunc, error) {
	if err := attachLiveChrome(); err != nil {
		return nil, nil, err
	}
	ctx, cancel := context.WithTimeout(liveChromeCtx, timeout)
	return ctx, cancel, nil
}

func attachLiveChrome() error {
	if liveChromeCtx != nil && liveChromePort == liveDebugPort {
		if _, err := existingPageTargetID(); err == nil {
			return nil
		}
		detachLiveChrome()
	}
	id, err := existingPageTargetID()
	if err != nil {
		return err
	}
	allocCtx, cancelAlloc := chromedp.NewRemoteAllocator(context.Background(), debugEndpoint())
	ctx, cancelCtx := chromedp.NewContext(allocCtx, chromedp.WithTargetID(id))
	if err := chromedp.Run(ctx, chromedp.ActionFunc(func(context.Context) error { return nil })); err != nil {
		cancelCtx()
		cancelAlloc()
		return err
	}
	liveChromeCtx = ctx
	liveChromePort = liveDebugPort
	liveTargetID = id
	liveChromeCancel = func() {
		cancelCtx()
		cancelAlloc()
	}
	return nil
}

func accountIDFromAuth(auth Auth) string {
	path := strings.TrimSpace(auth.path)
	if path != "" {
		dir := filepath.Base(filepath.Dir(filepath.Clean(path)))
		if dir != "" && dir != "." && dir != "config" && dir != "profiles" {
			return dir
		}
	}
	if acc, ok := currentAccount(); ok {
		return acc.ID
	}
	return ""
}

func activateAccountForAuth(auth *Auth) (restore func(), err error) {
	id := accountIDFromAuth(*auth)
	if id == "" {
		return nil, nil
	}
	accountMu.Lock()
	prev := activeAccountID
	accountMu.Unlock()
	if prev == id {
		return nil, nil
	}
	if err := setActiveAccount(id); err != nil {
		return nil, err
	}
	return func() {
		if prev != "" {
			_ = setActiveAccount(prev)
		}
	}, nil
}

func chromeDirForAccount(id string) (string, error) {
	dir := chromeDataRoot()
	if id != "" && usingIsolatedProfiles() {
		dir = filepath.Join(chromeProfilesDir(), id)
	}
	abs, err := filepath.Abs(dir)
	if err != nil {
		return "", err
	}
	return abs, os.MkdirAll(abs, 0755)
}

func detachLiveChrome() {
	if liveChromeCancel != nil {
		liveChromeCancel()
	}
	liveChromeCtx = nil
	liveChromeCancel = nil
	liveChromePort = ""
	liveTargetID = ""
}

func closeBotChrome() {
	if liveChromeCtx != nil {
		ctx, cancel := context.WithTimeout(liveChromeCtx, 3*time.Second)
		_ = chromedp.Run(ctx, chromedp.ActionFunc(func(ctx context.Context) error {
			return browser.Close().Do(ctx)
		}))
		cancel()
		time.Sleep(400 * time.Millisecond)
	}
	detachLiveChrome()
}

func markChromeCleanExit(dir string) {
	prefs := filepath.Join(dir, "Default", "Preferences")
	if data, err := os.ReadFile(prefs); err == nil {
		var m map[string]any
		if json.Unmarshal(data, &m) == nil {
			prof, _ := m["profile"].(map[string]any)
			if prof == nil {
				prof = map[string]any{}
				m["profile"] = prof
			}
			prof["exit_type"] = "Normal"
			prof["exited_cleanly"] = true
			prof["password_manager_leak_detection"] = false
			prof["password_manager_enabled"] = false
			m["credentials_enable_service"] = false
			m["credentials_enable_autosignin"] = false
			pm, _ := m["password_manager"].(map[string]any)
			if pm == nil {
				pm = map[string]any{}
				m["password_manager"] = pm
			}
			leak, _ := pm["leak_detection"].(map[string]any)
			if leak == nil {
				leak = map[string]any{}
				pm["leak_detection"] = leak
			}
			leak["enabled"] = false
			sess, _ := m["session"].(map[string]any)
			if sess == nil {
				sess = map[string]any{}
				m["session"] = sess
			}
			sess["restore_on_startup"] = 5
			if out, err := json.Marshal(m); err == nil {
				_ = os.WriteFile(prefs, out, 0600)
			}
		}
	}
	for _, rel := range []string{
		filepath.Join("Default", "Last Session"),
		filepath.Join("Default", "Last Tabs"),
		filepath.Join("Default", "Current Session"),
		filepath.Join("Default", "Current Tabs"),
	} {
		_ = os.Remove(filepath.Join(dir, rel))
	}
	sessDir := filepath.Join(dir, "Default", "Sessions")
	if entries, err := os.ReadDir(sessDir); err == nil {
		for _, e := range entries {
			_ = os.Remove(filepath.Join(sessDir, e.Name()))
		}
	}
}

var passwordNagWatchOnce sync.Once

func ensurePasswordNagWatcher() {
	if !nativeChromeClicks {
		return
	}
	passwordNagWatchOnce.Do(func() {
		go func() {
			for {
				time.Sleep(700 * time.Millisecond)
				if liveDebugPort == "" || !portInUse(liveDebugPort) {
					continue
				}
				if findWindowContaining("Windows Security") != 0 {
					continue
				}
				if chromeHasTargetHost("agekey.org") || chromeHasTargetHost("agekey.com") {
					continue
				}
				if nag := dismissChromeLeakDialog(); nagWasDismissed(nag) {
					fmt.Println("Dismissed:", nag)
					continue
				}
				if nag := dismissNativePasswordNag(); nagWasDismissed(nag) {
					fmt.Println("Dismissed:", nag)
				}
			}
		}()
	})
}

// syncCookiesFromChrome reuses the chrome-data profile headlessly and
// refreshes auth.json from whatever session is stored there.
func syncCookiesFromChrome(auth *Auth) error {
	chromeLoginMu.Lock()
	defer chromeLoginMu.Unlock()
	return syncCookiesFromChromeLocked(auth)
}

func syncCookiesFromChromeLocked(auth *Auth) error {
	id := accountIDFromAuth(*auth)
	if liveDebugPort != "" && (id == "" || id == liveChromeAccount) {
		if err := waitForDebugPort(500 * time.Millisecond); err == nil {
			cookies, err := cookiesFromDebugPort()
			if err == nil && cookieValue(cookies, "sid") != "" {
				auth.Cookie = buildTumblrCookieHeader(cookies)
				persistMaturePref(auth)
				fmt.Println("Synced cookies from this profile’s Chrome.")
				return nil
			}
		}
	}

	dir, err := chromeDirForAccount(id)
	if err != nil {
		return err
	}

	opts := append(chromedp.DefaultExecAllocatorOptions[:],
		chromedp.UserDataDir(dir),
		chromedp.Flag("headless", "new"),
		chromedp.Flag("disable-gpu", true),
		chromedp.Flag("enable-automation", false),
		chromedp.Flag("disable-blink-features", "AutomationControlled"),
		chromedp.Flag("no-first-run", true),
		chromedp.Flag("no-default-browser-check", true),
		chromedp.Flag("hide-scrollbars", true),
		chromedp.Flag("mute-audio", true),
	)

	allocCtx, cancel := chromedp.NewExecAllocator(context.Background(), opts...)
	defer cancel()

	ctx, cancel := chromedp.NewContext(allocCtx)
	defer cancel()
	ctx, cancel = context.WithTimeout(ctx, 45*time.Second)
	defer cancel()

	var cookies []*network.Cookie
	err = chromedp.Run(ctx,
		chromedp.Navigate("https://www.tumblr.com/dashboard"),
		chromedp.Sleep(2*time.Second),
		chromedp.ActionFunc(func(ctx context.Context) error {
			var err error
			cookies, err = network.GetCookies().WithURLs([]string{
				"https://www.tumblr.com",
				"https://tumblr.com",
				"https://www.tumblr.com/dashboard",
			}).Do(ctx)
			return err
		}),
	)
	if err != nil {
		return fmt.Errorf("%w (close the bot Chrome window from -login, or just rely on saved cookies)", err)
	}

	cookieHeader := buildTumblrCookieHeader(cookies)
	if cookieHeader == "" || cookieValue(cookies, "sid") == "" {
		return fmt.Errorf("no Tumblr sid cookie found — need a Chrome login")
	}

	auth.Cookie = cookieHeader
	persistMaturePref(auth)
	if err := saveAuth(*auth); err != nil {
		return err
	}
	fmt.Println("Synced cookies from Chrome profile.")
	return nil
}

func authHasSID(auth Auth) bool {
	return strings.Contains(auth.Cookie, "sid=")
}

func cookieValue(cookies []*network.Cookie, name string) string {
	for _, c := range cookies {
		if c.Name == name && strings.Contains(c.Domain, "tumblr.com") {
			return c.Value
		}
	}
	for _, c := range cookies {
		if c.Name == name {
			return c.Value
		}
	}
	return ""
}

func buildTumblrCookieHeader(cookies []*network.Cookie) string {
	var parts []string
	seen := map[string]bool{}
	for _, c := range cookies {
		if c.Domain != "" && !strings.Contains(c.Domain, "tumblr.com") {
			continue
		}
		if c.Domain == "" && !likelyTumblrCookie(c.Name) {
			continue
		}
		if seen[c.Name] {
			continue
		}
		seen[c.Name] = true
		parts = append(parts, c.Name+"="+c.Value)
	}
	return strings.Join(parts, "; ")
}

func likelyTumblrCookie(name string) bool {
	switch name {
	case "sid", "logged_in", "pfu", "tmgioct", "tz", "cl_pref", "likes-displayMode", "redpop-editor-beta-toggled":
		return true
	default:
		return strings.HasPrefix(name, "euconsent")
	}
}
