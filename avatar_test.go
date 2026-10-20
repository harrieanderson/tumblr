package main

import (
	"os"
	"strings"
	"testing"
)

func TestAvatarSaveWaitsBetweenDoneAndSave(t *testing.T) {
	src, err := os.ReadFile("avatar.go")
	if err != nil {
		t.Fatal(err)
	}
	s := string(src)
	if !strings.Contains(s, "cdpSetImageFile(call, chromeAvatarBoxJS") {
		t.Fatal("avatar upload must open the dedicated avatar chooser")
	}
	if !strings.Contains(s, "clickAppearanceSaveOnTarget(t)") {
		t.Fatal("avatar upload must click crop Done, wait, then click editor Save")
	}
	if strings.Contains(s, "for (const re of [/apply/i, /^done$/i") {
		t.Fatal("Done and Save cannot be clicked in one JS turn")
	}
}

func TestPageAvatarCheckDoesNotUseBanner(t *testing.T) {
	if strings.Contains(chromeLookFromPageJS, "for (const el of document.querySelectorAll('*'))") {
		t.Fatal("the page check must not count a banner/background as the avatar")
	}
	if !strings.Contains(chromeLookFromPageJS, "avatarControl") {
		t.Fatal("avatar verification must stay scoped to the avatar control")
	}
}

func TestAvatarAndBannerFilesCannotCrossWire(t *testing.T) {
	if _, err := imageFromDir("pics/profile_pic/profile_pic2.jpg", profilePicsDir, "profile picture"); err != nil {
		t.Fatal(err)
	}
	if _, err := imageFromDir("pics/background_pics/IMG_4631.jpg", backgroundPicsDir, "banner"); err != nil {
		t.Fatal(err)
	}
	if _, err := imageFromDir("pics/background_pics/IMG_4631.jpg", profilePicsDir, "profile picture"); err == nil {
		t.Fatal("a background image must never be accepted as the profile picture")
	}
	if _, err := imageFromDir("pics/profile_pic/profile_pic2.jpg", backgroundPicsDir, "banner"); err == nil {
		t.Fatal("a profile picture must never be accepted as the banner")
	}
}

func TestFileChooserNeverFallsBackToArbitraryInput(t *testing.T) {
	src, err := os.ReadFile("chrome_look.go")
	if err != nil {
		t.Fatal(err)
	}
	s := string(src)
	if strings.Contains(s, "func cdpSetFileOnNthInput") {
		t.Fatal("generic nth file-input fallback can put the profile picture in the banner")
	}
	if !strings.Contains(s, "refusing to use an unrelated file input") {
		t.Fatal("a missing dedicated chooser must fail safely")
	}
}
