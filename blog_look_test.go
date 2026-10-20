package main

import (
	"os"
	"strings"
	"testing"
)

func TestThemeLooksDone(t *testing.T) {
	st := blogLookPageStatus{BannerOK: true}
	if themeLooksDone(st, true) {
		t.Fatal("banner must not hide missing colors")
	}
	st.BackgroundOK = true
	if themeLooksDone(st, true) {
		t.Fatal("black accent is required too")
	}
	st.AccentOK = true
	if themeLooksDone(st, false) {
		t.Fatal("must confirm the upload succeeded")
	}
	if !themeLooksDone(st, true) {
		t.Fatal("verified banner and both colors should pass")
	}
}

func TestSetupCompletionRequiresBothColors(t *testing.T) {
	st := setupLookCheck{TitleOK: true, BioOK: true, AvatarOK: true, BannerOK: true, UsernameOK: true}
	if st.Done() {
		t.Fatal("missing colors must not count as complete")
	}
	st.BackgroundOK = true
	if st.Done() {
		t.Fatal("missing accent must not count as complete")
	}
	st.AccentOK = true
	if !st.Done() {
		t.Fatal("all confirmed settings should pass")
	}
}
func TestLooksNumberedClone(t *testing.T) {
	if !looksNumberedClone("lillycosmic1") || !looksNumberedClone("lilly-cosmic2") {
		t.Fatal("numbered cosmic clones")
	}
	if looksNumberedClone("cosmiczoey") || looksNumberedClone("lillyzoey") || looksNumberedClone("lil-zoey") {
		t.Fatal("plain zoey names are not clones")
	}
}

func TestPickZoeyUsernameSkipsUsed(t *testing.T) {
	pool := zoeyUsernamePool()
	if len(pool) < 20 {
		t.Fatal("need a pool of usernames")
	}
	for _, n := range pool {
		if looksNumberedClone(n) {
			t.Fatal("pool has numbered clone", n)
		}
	}
	if !usernameAlreadyOnBrand("lillycosmic1") || !usernameAlreadyOnBrand("lilly-cosmic2") || !usernameAlreadyOnBrand("cosmiczoey") {
		t.Fatal("zoey/cosmic names should be skipped")
	}
	if usernameAlreadyOnBrand("aisparkshorts") || usernameAlreadyOnBrand("huanmie73") {
		t.Fatal("unrelated names should be renamed")
	}
}

func TestUsernameVariants(t *testing.T) {
	v := usernameVariants("cosmiczoey")
	if len(v) < 4 || v[0] != "cosmiczoey" {
		t.Fatal(v)
	}
	joined := strings.Join(v, " ")
	if !strings.Contains(joined, "cosmiczoeyy") {
		t.Fatal("should double the last letter", v)
	}
	js := chromeUsernameStatusJS("cosmiczoey")
	if !strings.Contains(js, "good one") {
		t.Fatal("status JS should detect That's a good one, but it's taken")
	}
	if !strings.Contains(js, "/settings/blog/") {
		t.Fatal("ok must be the settings URL, not typed body text")
	}
	if strings.Contains(js, "t.indexOf") || strings.Contains(js, "tumblr.com/'+") {
		t.Fatal("must not treat typed username text as success")
	}
}

func TestShouldFillUsernameField(t *testing.T) {
	if shouldFillUsernameField("") || shouldFillUsernameField("missing") {
		t.Fatal("must not fill username until the username editor is open")
	}
	if !shouldFillUsernameField("change") || !shouldFillUsernameField("already") ||
		!shouldFillUsernameField("pencil") || !shouldFillUsernameField("row-pencil") {
		t.Fatal("fill only after Change username / existing username field")
	}
}

func TestUsernameFillDoesNotTouchQuestionsTitle(t *testing.T) {
	js := chromeFillUsernameJS("lilzoeybunny")
	if strings.Contains(js, "!skip.test") {
		t.Fatal("must not fall back to any unlabeled text input")
	}
	if !strings.Contains(js, "question") {
		t.Fatal("username fill must skip Questions page title")
	}
	openJS := chromeOpenUsernameJS()
	if strings.Contains(openJS, "el.querySelector('svg')") {
		t.Fatal("must not click random SVG pencils on the settings page")
	}
	if !strings.Contains(openJS, "sameRow") || !strings.Contains(openJS, "row-pencil") {
		t.Fatal("the unlabeled pencil must be found only inside the exact Username row")
	}
}

func TestEditAppearanceDoesNotOpenThemeCustomizer(t *testing.T) {
	if strings.Contains(chromeEditAppearanceWantJS, "edit theme") {
		t.Fatal("Edit theme is Tumblr's /customize editor, not profile appearance")
	}
	if strings.Contains(chromeClickEditAppearanceJS, `/appearance|customize/i.test(a.getAttribute('href')`) {
		t.Fatal("must not click a /customize/ href")
	}
	if !strings.Contains(chromeClickEditAppearanceJS, "customize") || !strings.Contains(chromeClickEditAppearanceJS, "badHref") {
		t.Fatal("edit appearance JS must skip /customize/ links")
	}
	if !strings.Contains(chromeEditAppearanceBoxJS, "customize") || !strings.Contains(chromeEditAppearanceBoxJS, "badHref") {
		t.Fatal("coordinate click must skip /customize/ links")
	}
	if blogNameFromCustomizeURL("https://www.tumblr.com/customize/sims4bbgc?redirect_to=/settings/blog/sims4bbgc") != "sims4bbgc" {
		t.Fatal("customize URL blog name")
	}
	if !tumblrCustomizeURL("https://www.tumblr.com/customize/sims4bbgc?redirect_to=/settings/blog/sims4bbgc") {
		t.Fatal("customize URL")
	}
}

func TestEditAppearanceClickIsNotFooledBySave(t *testing.T) {
	if strings.Contains(chromeClickEditAppearanceJS, "save changes") && strings.Contains(chromeClickEditAppearanceJS, "return 'already'") {
		if !strings.Contains(chromeClickEditAppearanceJS, "Edit blog avatar image") {
			t.Fatal("already-open should require the appearance editor")
		}
	}
	if strings.Contains(chromeAppearanceEditorOpenJS, "\\bAccent\\b") || strings.Contains(chromeAppearanceEditorOpenJS, "input[type=\"color\"]") {
		t.Fatal("closed blog settings already show Accent/Background — that must not count as the editor")
	}
	if strings.Contains(chromeClickEditAppearanceJS, "redirect_to=") {
		t.Fatal("do not skip Edit appearance just because a settings URL has redirect_to")
	}
	if !strings.Contains(chromeClickEditAppearanceJS, "aria-label") {
		t.Fatal("must click Edit appearance by aria-label too")
	}
	if !strings.Contains(chromeEditAppearanceBoxJS, "edit appearance") {
		t.Fatal("need a coordinate fallback for Edit appearance")
	}
	if !strings.Contains(chromeClickEditAppearanceJS, "editar apariencia") {
		t.Fatal("Edit appearance must match the Spanish button too")
	}
	if !strings.Contains(chromeClickEditAppearanceJS, "[tabindex]") || !strings.Contains(chromeClickEditAppearanceJS, "span, div") {
		t.Fatal("Tumblr Edit appearance is often a span/div, not a native button")
	}
}

func TestFillTitleBioIsNotEnglishOnly(t *testing.T) {
	js := chromeFillTitleBioJS("Zoey 🌷", "I'm 25 btw :P")
	if !strings.Contains(js, "Blog title") {
		t.Fatal("still needs the plain English aria-label")
	}
	if !strings.Contains(js, "t[ií]tulo") || !strings.Contains(js, "descripci") {
		t.Fatal("a translated appearance page must still get a title and bio")
	}
	if !strings.Contains(js, "question") || !strings.Contains(js, "username") {
		t.Fatal("must never type the title into Questions page title or the username box")
	}
	if !strings.Contains(js, "'none'") {
		t.Fatal("fill must report none so the log shows nothing was set")
	}
}

func TestEditorOpenCheckSurvivesTranslation(t *testing.T) {
	if !strings.Contains(chromeAppearanceEditorOpenJS, "cabecera") {
		t.Fatal("editor-open check must accept a translated avatar/header pencil")
	}
	if !strings.Contains(chromeAppearanceEditorOpenJS, "input[type=file]") {
		t.Fatal("a visible file input also means the appearance editor is open")
	}
}

func TestThemeColorJS(t *testing.T) {
	js := chromeSetThemeColorsJS("#000000", "#7C3AED")
	if !strings.Contains(js, "#000000") || !strings.Contains(js, "#7C3AED") {
		t.Fatal("color JS should set black accent and purple background")
	}
	if !strings.Contains(chromeHeaderBoxJS, "header|banner|cabecera") {
		t.Fatal("header click JS must locate only the dedicated header control")
	}
	if !strings.Contains(chromeAvatarBoxJS, "avatar|profile") {
		t.Fatal("avatar click JS must locate only the dedicated avatar control")
	}
	st := chromeBlogLookStatusJS("#000000", "#7C3AED", "cosmiczoey")
	if !strings.Contains(st, "good one") || !strings.Contains(st, "default_header") || !strings.Contains(st, "bannerOK") {
		t.Fatal("look status should verify taken, banner, and colors")
	}
	if !strings.Contains(st, "blob:") || !strings.Contains(st, `media\.tumblr`) {
		t.Fatal("banner ok should require an uploaded media url")
	}
}

func TestBannerPicsDir(t *testing.T) {
	if _, err := os.Stat(backgroundPicsDir); err != nil {
		t.Fatal(backgroundPicsDir, err)
	}
}

func TestStripColorLeakFromBio(t *testing.T) {
	got := stripColorLeak("18+ only Instaaaa: https://www.instagram.com/lil.cosmic.bunny.o7#000000#7C3AED")
	if bioHasColorLeak(got) || !strings.Contains(got, "lil.cosmic.bunny") {
		t.Fatal(got)
	}
	got = stripColorLeak("#000000#7C3AED18+ only Instaaaa: https://www.instagram.com/lil.cosmic.bunny.o7")
	if bioHasColorLeak(got) || !strings.HasPrefix(got, "18+") {
		t.Fatal(got)
	}
	if bioHasColorLeak("18+ only Instaaaa: https://www.instagram.com/lil.cosmic.bunny.o7") {
		t.Fatal("clean bio")
	}
}

func TestSavedThemeStatus(t *testing.T) {
	cfg := profileSetupConfig{Accent: "#000000", Background: "#7C3AED"}
	p := BlogProfile{AccentColor: "#000000", BackgroundColor: "#7c3aed", HeaderImage: "https://64.media.tumblr.com/header.jpg", ShowHeaderImage: true}
	if !themeLooksDone(savedThemeStatus(p, cfg), true) {
		t.Fatal("saved colors and header should verify")
	}
	p.ShowHeaderImage = false
	if themeLooksDone(savedThemeStatus(p, cfg), true) {
		t.Fatal("hidden header must not verify")
	}
	p.ShowHeaderImage = true
	p.HeaderImage = ""
	if themeLooksDone(savedThemeStatus(p, cfg), true) {
		t.Fatal("missing header must not verify")
	}
}
