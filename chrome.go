package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/chromedp/cdproto/network"
	"github.com/chromedp/chromedp"
)

const (
	chromeDataDir   = "chrome-data"
	debugPort       = "9333"
	credentialsFile = "config/credentials.json"
)

type Credentials struct {
	Email    string `json:"email"`
	Password string `json:"password"`
}

func loadCredentials() (Credentials, error) {
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

func chromePath() (string, error) {
	candidates := []string{
		os.Getenv("PROGRAMFILES") + `\Google\Chrome\Application\chrome.exe`,
		os.Getenv("PROGRAMFILES(X86)") + `\Google\Chrome\Application\chrome.exe`,
		os.Getenv("LOCALAPPDATA") + `\Google\Chrome\Application\chrome.exe`,
	}
	for _, p := range candidates {
		if p == "" {
			continue
		}
		if _, err := os.Stat(p); err == nil {
			return p, nil
		}
	}
	return "", fmt.Errorf("chrome.exe not found")
}

func profileDir() (string, error) {
	dir, err := filepath.Abs(chromeDataDir)
	if err != nil {
		return "", err
	}
	return dir, os.MkdirAll(dir, 0755)
}

// loginWithRealChrome opens the bot Chrome profile and logs into Tumblr.
// If config/credentials.json exists, it fills email/password automatically.
// Skips the form (and Enter) when the profile is already logged in.
func loginWithRealChrome(auth *Auth) error {
	dir, err := profileDir()
	if err != nil {
		return err
	}
	chrome, err := chromePath()
	if err != nil {
		return err
	}

	creds, credErr := loadCredentials()
	auto := credErr == nil

	fmt.Println("Profile folder:", dir)
	fmt.Println()
	if auto {
		fmt.Println("Found config/credentials.json — auto-login enabled.")
	} else {
		fmt.Println("No credentials file — will reuse an existing Chrome session if present.")
		fmt.Println("Otherwise put email/password in config/credentials.json")
	}
	fmt.Println()

	// Start logged out so -login can switch accounts cleanly.
	if auto {
		fmt.Println("Logging out any previous session first…")
	}

	cmd := exec.Command(chrome,
		"--user-data-dir="+dir,
		"--remote-debugging-port="+debugPort,
		"--remote-debugging-address=127.0.0.1",
		"--no-first-run",
		"--no-default-browser-check",
		"--new-window",
		"https://www.tumblr.com/logout",
	)
	if err := cmd.Start(); err != nil {
		return err
	}

	if err := waitForDebugPort(20 * time.Second); err != nil {
		_ = cmd.Process.Kill()
		return fmt.Errorf("%w (is another Chrome already using debug port %s?)", err, debugPort)
	}

	// Already logged in? Just sync cookies — no form, no Enter.
	if err := waitForTumblrSession(8 * time.Second); err == nil {
		fmt.Println("Already logged in — syncing cookies…")
	} else if auto {
		fmt.Println("Not logged in — auto-filling Tumblr login…")
		if err := autoFillTumblrLogin(creds); err != nil {
			fmt.Println("Auto-login failed:", err)
			fmt.Println("Waiting up to 2 minutes for you to finish login in Chrome (no Enter needed)…")
			if err := waitForTumblrSession(2 * time.Minute); err != nil {
				return fmt.Errorf("login did not complete: %w", err)
			}
		} else {
			fmt.Println("Login submitted — waiting for session…")
			if err := waitForTumblrSession(90 * time.Second); err != nil {
				fmt.Println("Waiting a bit longer for captcha/2FA in Chrome (no Enter needed)…")
				if err := waitForTumblrSession(2 * time.Minute); err != nil {
					return fmt.Errorf("login did not complete: %w", err)
				}
			}
		}
	} else {
		fmt.Println("Waiting up to 3 minutes for login in Chrome (no Enter needed)…")
		if err := waitForTumblrSession(3 * time.Minute); err != nil {
			return fmt.Errorf("login did not complete: %w", err)
		}
	}

	cookies, err := cookiesFromDebugPort()
	if err != nil {
		return err
	}

	sid := cookieValue(cookies, "sid")
	if sid == "" {
		fmt.Println("Cookies seen for tumblr.com:")
		for _, c := range cookies {
			if strings.Contains(c.Domain, "tumblr.com") {
				fmt.Println(" -", c.Name, "(domain:", c.Domain+")")
			}
		}
		return fmt.Errorf("still no sid cookie — login did not complete")
	}

	auth.Cookie = buildTumblrCookieHeader(cookies)
	persistMaturePref(auth)
	if err := saveAuth(*auth); err != nil {
		return err
	}
	fmt.Println("Synced cookies.")

	client := newHumanClient()
	if err := refreshCSRF(client, auth); err != nil {
		fmt.Println("CSRF refresh warning:", err)
	}
	if name, err := syncAuthIdentity(client, auth); err != nil {
		fmt.Println("Could not detect blog identity yet:", err)
		fmt.Println("You can set config/auth.json \"blog\" manually to your t:… UUID")
	} else {
		fmt.Println("Active blog:", name, "("+auth.Blog+")")
	}
	return nil
}

func autoFillTumblrLogin(creds Credentials) error {
	allocCtx, cancel := chromedp.NewRemoteAllocator(context.Background(), "http://127.0.0.1:"+debugPort)
	defer cancel()

	ctx, cancel := chromedp.NewContext(allocCtx)
	defer cancel()
	ctx, cancel = context.WithTimeout(ctx, 90*time.Second)
	defer cancel()

	return chromedp.Run(ctx,
		chromedp.Navigate("https://www.tumblr.com/login"),
		chromedp.WaitReady("body", chromedp.ByQuery),
		chromedp.Sleep(2*time.Second),
		chromedp.ActionFunc(func(ctx context.Context) error {
			if cookieOK(ctx) {
				return nil
			}
			var href string
			_ = chromedp.Location(&href).Do(ctx)
			if strings.Contains(href, "/dashboard") {
				return nil
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

func fillTumblrLoginFormJS(ctx context.Context, creds Credentials) error {
	// Wait for the email field (type=text name=email on current Tumblr UI).
	deadline := time.Now().Add(25 * time.Second)
	for {
		var ready bool
		_ = chromedp.Evaluate(`!!document.querySelector('input[name="email"], input[placeholder="Email"], input[aria-label="email"]')`, &ready).Do(ctx)
		if ready {
			break
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("email field never appeared (already logged in, or page blocked)")
		}
		time.Sleep(500 * time.Millisecond)
	}

	emailJSON, _ := json.Marshal(creds.Email)
	passJSON, _ := json.Marshal(creds.Password)
	script := fmt.Sprintf(`(() => {
		const setNative = (el, value) => {
			const proto = window.HTMLInputElement.prototype;
			const desc = Object.getOwnPropertyDescriptor(proto, 'value');
			desc.set.call(el, value);
			el.dispatchEvent(new Event('input', { bubbles: true }));
			el.dispatchEvent(new Event('change', { bubbles: true }));
		};
		const email = document.querySelector('input[name="email"], input[placeholder="Email"], input[aria-label="email"]');
		const pass = document.querySelector('input[name="password"], input[type="password"], input[aria-label="password"]');
		if (!email) return 'no-email';
		if (!pass) return 'no-password';
		email.focus();
		setNative(email, %s);
		pass.focus();
		setNative(pass, %s);
		const btn = document.querySelector('button[type="submit"][aria-label="Log in"], button[type="submit"]')
			|| [...document.querySelectorAll('button')].find(b => /log\s*in/i.test(b.innerText||b.getAttribute('aria-label')||''));
		if (btn) { btn.click(); return 'ok'; }
		pass.dispatchEvent(new KeyboardEvent('keydown', { key: 'Enter', bubbles: true }));
		return 'ok-enter';
	})()`, string(emailJSON), string(passJSON))

	var result string
	if err := chromedp.Evaluate(script, &result).Do(ctx); err != nil {
		return err
	}
	if strings.HasPrefix(result, "no-") {
		return fmt.Errorf("login form: %s", result)
	}
	return nil
}

func waitForTumblrSession(timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		cookies, err := cookiesFromDebugPort()
		if err == nil && cookieValue(cookies, "sid") != "" {
			return nil
		}
		time.Sleep(2 * time.Second)
	}
	return fmt.Errorf("timed out waiting for sid cookie")
}

func waitForDebugPort(timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	url := "http://127.0.0.1:" + debugPort + "/json/version"
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
	return fmt.Errorf("chrome debug port %s not ready", debugPort)
}

func cookiesFromDebugPort() ([]*network.Cookie, error) {
	allocCtx, cancel := chromedp.NewRemoteAllocator(context.Background(), "http://127.0.0.1:"+debugPort)
	defer cancel()

	ctx, cancel := chromedp.NewContext(allocCtx)
	defer cancel()
	ctx, cancel = context.WithTimeout(ctx, 45*time.Second)
	defer cancel()

	var cookies []*network.Cookie
	err := chromedp.Run(ctx,
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
	return cookies, err
}

// syncCookiesFromChrome reuses the chrome-data profile headlessly and
// refreshes auth.json from whatever session is stored there.
func syncCookiesFromChrome(auth *Auth) error {
	if err := waitForDebugPort(500 * time.Millisecond); err == nil {
		cookies, err := cookiesFromDebugPort()
		if err == nil && cookieValue(cookies, "sid") != "" {
			auth.Cookie = buildTumblrCookieHeader(cookies)
			persistMaturePref(auth)
			fmt.Println("Synced cookies from open bot Chrome.")
			return nil
		}
	}

	dir, err := profileDir()
	if err != nil {
		return err
	}

	opts := append(chromedp.DefaultExecAllocatorOptions[:],
		chromedp.UserDataDir(dir),
		chromedp.Flag("headless", true),
		chromedp.Flag("disable-gpu", true),
		chromedp.Flag("enable-automation", false),
		chromedp.Flag("disable-blink-features", "AutomationControlled"),
		chromedp.Flag("no-first-run", true),
		chromedp.Flag("no-default-browser-check", true),
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
		return fmt.Errorf("no Tumblr sid cookie found — run: go run . -login")
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
