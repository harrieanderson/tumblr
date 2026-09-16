package main

import (
	"bufio"
	"context"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/chromedp/chromedp"
)

func openSettingsForAgeVerify() error {
	auth, err := loadAuth()
	if err != nil {
		return err
	}

	fmt.Println("Logging in, then opening Settings…")
	cmd, err := startChromeLoggedIn(&auth)
	if err != nil {
		return err
	}
	defer func() {
		stopChrome(cmd)
		time.Sleep(2 * time.Second)
	}()

	if err := goToSettings(); err != nil {
		fmt.Println("Could not open Settings automatically:", err)
		fmt.Println("In Chrome, go to tumblr.com/settings/account")
	} else {
		fmt.Println("Logged in. Chrome should be on Settings.")
	}

	fmt.Println()
	fmt.Println("Scan your face / verify your age in Chrome.")
	fmt.Println("When you are done, press Enter here for the next account.")
	fmt.Println()
	waitForEnter()

	if cookies, err := cookiesFromDebugPort(); err == nil && cookieValue(cookies, "sid") != "" {
		auth.Cookie = buildTumblrCookieHeader(cookies)
		persistMaturePref(&auth)
		fmt.Println("Saved session cookies.")
	}
	return nil
}

func goToSettings() error {
	ctx, cancel, err := withBotChrome(45 * time.Second)
	if err != nil {
		return err
	}
	defer cancel()

	return chromedp.Run(ctx,
		chromedp.Navigate("https://www.tumblr.com/settings/account"),
		chromedp.WaitReady("body", chromedp.ByQuery),
		chromedp.Sleep(2*time.Second),
		chromedp.ActionFunc(func(ctx context.Context) error {
			var href string
			_ = chromedp.Location(&href).Do(ctx)
			if strings.Contains(href, "/login") {
				return fmt.Errorf("still on login page")
			}
			return nil
		}),
	)
}

func waitForEnter() {
	waitForEnterPrompt("Press Enter when the face scan is done… ")
}

func stdinIsConsole() bool {
	fi, err := os.Stdin.Stat()
	if err != nil {
		return false
	}
	return fi.Mode()&os.ModeCharDevice != 0
}

func waitForEnterPrompt(msg string) {
	fmt.Println(msg)
	if stdinIsConsole() {
		fmt.Println("Chrome stays open. Press Enter when you are done.")
		_, _ = bufio.NewReader(os.Stdin).ReadString('\n')
		return
	}
	fmt.Println("No keyboard attached — close the Chrome window when you are done.")
	for {
		if liveDebugPort == "" || !portInUse(liveDebugPort) {
			fmt.Println("Chrome closed.")
			return
		}
		time.Sleep(500 * time.Millisecond)
	}
}
