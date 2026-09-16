package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"time"

	"github.com/chromedp/chromedp"
)

const profilePicsDir = "pics/profile_pic"

var avatarPicSeq atomic.Int32

func listProfilePics() ([]string, error) {
	entries, err := os.ReadDir(profilePicsDir)
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", profilePicsDir, err)
	}
	var out []string
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		ext := strings.ToLower(filepath.Ext(e.Name()))
		switch ext {
		case ".jpg", ".jpeg", ".png", ".gif", ".webp":
			out = append(out, filepath.Join(profilePicsDir, e.Name()))
		}
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("no images in %s/", profilePicsDir)
	}
	return out, nil
}

func nextProfilePic(pics []string) string {
	i := int(avatarPicSeq.Add(1) - 1)
	return pics[i%len(pics)]
}

func picForCurrentAccount(pics []string) string {
	acc, ok := currentAccount()
	if !ok {
		return nextProfilePic(pics)
	}
	for i, a := range allAccounts() {
		if a.ID == acc.ID {
			return pics[i%len(pics)]
		}
	}
	return nextProfilePic(pics)
}

func runSetAvatars() error {
	pics, err := listProfilePics()
	if err != nil {
		return err
	}
	path := picForCurrentAccount(pics)
	abs, err := filepath.Abs(path)
	if err != nil {
		return err
	}
	fmt.Println("Setting avatar from", filepath.ToSlash(path))

	auth, err := loadAuth()
	if err != nil {
		return err
	}
	cmd, err := startChromeLoggedIn(&auth)
	if err != nil {
		return err
	}
	defer func() {
		stopChrome(cmd)
		time.Sleep(2 * time.Second)
	}()

	name, err := syncAuthIdentity(newHumanClient(), &auth)
	if err != nil || name == "" {
		maybeFillBlog(newHumanClient(), &auth)
		name = strings.TrimPrefix(auth.Blog, "t:")
	}
	if name == "" {
		return fmt.Errorf("could not detect blog name")
	}
	fmt.Println("Blog:", name)
	return setAvatarInChrome(abs, name)
}

func setAvatarInChrome(imagePath, blogName string) error {
	ctx, cancel, err := withBotChrome(90 * time.Second)
	if err != nil {
		return err
	}
	defer cancel()

	settings := "https://www.tumblr.com/settings/blog/" + blogName
	var href, clicked, saved string
	var nfiles int64

	err = chromedp.Run(ctx,
		chromedp.Navigate(settings),
		chromedp.WaitReady("body", chromedp.ByQuery),
		chromedp.Sleep(2*time.Second),
		chromedp.Location(&href),
		chromedp.ActionFunc(func(ctx context.Context) error {
			if strings.Contains(href, "/login") {
				return fmt.Errorf("still on login page")
			}
			return chromedp.Evaluate(`(() => {
				const hit = (re) => {
					const el = [...document.querySelectorAll('button, a, [role="button"]')]
						.find(b => re.test((b.innerText || b.getAttribute('aria-label') || '').replace(/\s+/g, ' ').trim()));
					if (!el) return '';
					el.click();
					return (el.innerText || el.getAttribute('aria-label') || '').trim();
				};
				return hit(/edit appearance/i) || hit(/^appearance$/i) || hit(/change (your )?avatar/i) || '';
			})()`, &clicked).Do(ctx)
		}),
		chromedp.Sleep(2*time.Second),
		chromedp.Evaluate(`document.querySelectorAll('input[type=file]').length`, &nfiles),
		chromedp.ActionFunc(func(ctx context.Context) error {
			if nfiles > 0 {
				return nil
			}
			_ = chromedp.Evaluate(`(() => {
				const el = document.querySelector('[aria-label*="avatar" i], [aria-label*="profile photo" i], button[aria-label*="edit" i]');
				if (el) el.click();
				return true;
			})()`, &nfiles).Do(ctx)
			return chromedp.Sleep(1500 * time.Millisecond).Do(ctx)
		}),
		chromedp.Evaluate(`document.querySelectorAll('input[type=file]').length`, &nfiles),
		chromedp.ActionFunc(func(ctx context.Context) error {
			if nfiles == 0 {
				return fmt.Errorf("no file picker on %s", href)
			}
			fmt.Println("File inputs:", nfiles)
			return chromedp.SetUploadFiles(`input[type=file]`, []string{imagePath}, chromedp.ByQuery).Do(ctx)
		}),
		chromedp.Sleep(2*time.Second),
		chromedp.ActionFunc(func(ctx context.Context) error {
			return chromedp.Evaluate(`(() => {
				const labels = [];
				for (const re of [/apply/i, /^done$/i, /update/i, /^save$/i, /save changes/i]) {
					const el = [...document.querySelectorAll('button, [role="button"]')]
						.find(b => re.test((b.innerText || b.getAttribute('aria-label') || '').replace(/\s+/g, ' ').trim()));
					if (el) {
						el.click();
						labels.push((el.innerText || el.getAttribute('aria-label') || '').trim());
					}
				}
				return labels.join(" | ");
			})()`, &saved).Do(ctx)
		}),
		chromedp.Sleep(2*time.Second),
	)
	if err != nil {
		return err
	}
	if clicked != "" {
		fmt.Println("Clicked:", clicked)
	}
	if saved != "" {
		fmt.Println("Clicked:", saved)
	}
	fmt.Println("Page:", href)
	return nil
}
