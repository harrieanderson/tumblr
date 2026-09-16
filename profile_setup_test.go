package main

import "testing"

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
	if !wantedBioSet("18+ only Instaaaa: https://www.instagram.com/lil.cosmic.bunny.o7") {
		t.Fatal("expected zoey bio")
	}
	if !wantedBioSet("https://instagram.com/lil.cosmic.bunny.o7") {
		t.Fatal("expected lil.cosmic")
	}
	if wantedBioSet("Mafocopi") {
		t.Fatal("old bio should not count")
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

func TestIsConsentTargetURL(t *testing.T) {
	if !isConsentTargetURL("https://cmp.quantcast.com/something") {
		t.Fatal("quantcast cmp")
	}
	if !isConsentTargetURL("https://www.tumblr.com/privacy/consent") {
		t.Fatal("tumblr privacy")
	}
	if isConsentTargetURL("https://auth.agekey.org/login") {
		t.Fatal("agekey is not a cookie banner")
	}
	if isConsentTargetURL("https://www.tumblr.com/settings/account") {
		t.Fatal("account settings is not a cookie banner")
	}
}
