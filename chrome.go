package main

import (
	"context"
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
	chromeDataDir = "chrome-data"
	debugPort     = "9333"
)

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
	dir, err := filepath.Abs(chromeDataDir)
	if err != nil {
		return "", err
	}
	return dir, os.MkdirAll(dir, 0755)
}

// loginWithRealChrome opens a SEPARATE Chrome profile for the bot.
// Your everyday Chrome login does not count — you must log in in the new window.
func loginWithRealChrome(auth *Auth) error {
	dir, err := profileDir()
	if err != nil {
		return err
	}
	chrome, err := chromePath()
	if err != nil {
		return err
	}

	fmt.Println("Profile folder:", dir)
	fmt.Println()
	fmt.Println("IMPORTANT: a NEW Chrome window will open for the bot.")
	fmt.Println("Do NOT use your normal already-open Chrome tab.")
	fmt.Println("Log into Tumblr in that NEW window, wait until you see your dashboard,")
	fmt.Println("THEN come back here and press Enter.")
	fmt.Println()

	cmd := exec.Command(chrome,
		"--user-data-dir="+dir,
		"--remote-debugging-port="+debugPort,
		"--remote-debugging-address=127.0.0.1",
		"--no-first-run",
		"--no-default-browser-check",
		"--new-window",
		"https://www.tumblr.com/login",
	)
	if err := cmd.Start(); err != nil {
		return err
	}

	if err := waitForDebugPort(20 * time.Second); err != nil {
		_ = cmd.Process.Kill()
		return fmt.Errorf("%w (is another Chrome already using debug port %s?)", err, debugPort)
	}

	fmt.Println("Bot Chrome is ready. Log in there, then press Enter...")
	fmt.Scanln()

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
		return fmt.Errorf("still no sid cookie — make sure you logged in in the NEW bot Chrome window")
	}

	auth.Cookie = buildTumblrCookieHeader(cookies)
	if err := saveAuth(*auth); err != nil {
		return err
	}
	fmt.Println("Synced cookies. You can close the bot Chrome window.")
	client := &http.Client{}
	if err := refreshCSRF(client, auth); err != nil {
		fmt.Println("Could not refresh CSRF yet:", err)
	}
	maybeFillBlog(client, auth)
	return nil
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
	// Prefer the already-open bot Chrome (from -login) so we don't fight the profile lock.
	if err := waitForDebugPort(500 * time.Millisecond); err == nil {
		cookies, err := cookiesFromDebugPort()
		if err == nil && cookieValue(cookies, "sid") != "" {
			auth.Cookie = buildTumblrCookieHeader(cookies)
			if err := saveAuth(*auth); err != nil {
				return err
			}
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
	// Some Chrome builds omit domain on CDP cookies — fall back to name-only.
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
