package main

import (
	"fmt"
	"math/rand"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"time"
)

const (
	profilePicsDir    = "pics/profile_pic"
	backgroundPicsDir = "pics/background_pics"
)

var avatarPicSeq atomic.Int32

func listImagesInDir(dir string) ([]string, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", dir, err)
	}
	var out []string
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		ext := strings.ToLower(filepath.Ext(e.Name()))
		switch ext {
		case ".jpg", ".jpeg", ".png", ".gif", ".webp":
			out = append(out, filepath.Join(dir, e.Name()))
		}
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("no images in %s/", dir)
	}
	return out, nil
}

func listProfilePics() ([]string, error) {
	return listImagesInDir(profilePicsDir)
}

func listBannerPics() ([]string, error) {
	return listImagesInDir(backgroundPicsDir)
}

func imageFromDir(path, dir, label string) (string, error) {
	abs, err := filepath.Abs(path)
	if err != nil {
		return "", err
	}
	root, err := filepath.Abs(dir)
	if err != nil {
		return "", err
	}
	rel, err := filepath.Rel(root, abs)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("%s must come from %s, got %s", label, filepath.ToSlash(dir), filepath.ToSlash(path))
	}
	return abs, nil
}

func nextProfilePic(pics []string) string {
	i := int(avatarPicSeq.Add(1) - 1)
	return pics[i%len(pics)]
}

func picForAccountIndex(pics []string, accountID string) string {
	if len(pics) == 0 {
		return ""
	}
	if accountID != "" {
		for i, a := range allAccounts() {
			if a.ID == accountID {
				return pics[i%len(pics)]
			}
		}
	}
	return nextProfilePic(pics)
}

func bannerForCurrentAccount(pics []string) string {
	if len(pics) == 0 {
		return ""
	}
	return pics[rand.Intn(len(pics))]
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
	abs, err := imageFromDir(imagePath, profilePicsDir, "profile picture")
	if err != nil {
		return err
	}

	settings := "https://www.tumblr.com/settings/blog/" + blogName
	t, err := openOrNavigateTab(settings)
	if err != nil {
		return err
	}
	t = leaveCustomizeTheme(t, blogName)

	var lastErr error
	for attempt := 1; attempt <= 2; attempt++ {
		err = withChromeTargetCDPFor(t, 90*time.Second, func(call cdpCall) error {
			if _, err := call("Page.enable", nil); err != nil {
				return err
			}
			if _, err := call("Page.setInterceptFileChooserDialog", map[string]any{"enabled": true}); err != nil {
				return err
			}
			if hit := cdpDismissOverlays(call); hit != "" {
				fmt.Println("Dismissed:", hit)
			}
			if err := cdpEnsureEditAppearance(call); err != nil {
				return err
			}
			return cdpSetImageFile(call, chromeAvatarBoxJS, abs, "Edit blog avatar image")
		})
		if err != nil {
			lastErr = err
			fmt.Println("Avatar attempt:", err)
			continue
		}
		if err := clickAppearanceSaveOnTarget(t); err != nil {
			lastErr = err
			fmt.Println("Avatar save:", err)
			continue
		}
		time.Sleep(2 * time.Second)
		if currentLookStatus(blogName).AvatarOK {
			fmt.Println("Confirmed avatar from", filepath.ToSlash(imagePath))
			return nil
		}
		lastErr = fmt.Errorf("avatar did not confirm after save")
		fmt.Println("Avatar:", lastErr)
	}
	return lastErr
}
