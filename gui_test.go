package main

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestGuiAccountsRequiresSelection(t *testing.T) {
	if _, err := guiAccounts("", nil); err == nil {
		t.Fatal("empty selection should fail")
	}
}

func TestGuiAccountsUnknownID(t *testing.T) {
	_, err := guiAccounts("", []string{"this-account-does-not-exist-zzz"})
	if err == nil || !strings.Contains(err.Error(), "unknown account") {
		t.Fatalf("got %v", err)
	}
}

func TestGuiAccountsAllFlag(t *testing.T) {
	accs, err := guiAccounts("all", nil)
	if err != nil {
		t.Skip(err)
	}
	if len(accs) == 0 {
		t.Fatal("all should return enabled accounts")
	}
}

func TestOneAccountForChrome(t *testing.T) {
	if _, err := oneAccountForChrome("", nil); err == nil {
		t.Fatal("empty should fail")
	}
	accs, err := requestedAccounts("all")
	if err != nil || len(accs) < 2 {
		t.Skip("need 2+ enabled accounts")
	}
	if _, err := oneAccountForChrome("all", nil); err == nil {
		t.Fatal("all should fail — Chrome opens one profile")
	}
}

func TestRequestSetupSkipDoesNotBlock(t *testing.T) {
	requestSetupSkip()
	requestSetupSkip()
}

func TestGuiHTMLHasControls(t *testing.T) {
	for _, id := range []string{
		`id="btnReach"`,
		`id="btnSetup"`,
		`id="btnMessages"`,
		`id="msgWatch"`,
		`id="msgHours"`,
		`id="statusText"`,
		`id="acctList"`,
		`id="tallyNew"`,
		`id="photoCaption"`,
		`id="photoPin"`,
	} {
		if !strings.Contains(guiHTML, id) {
			t.Fatalf("gui HTML missing %s", id)
		}
	}
	if !strings.Contains(guiHTML, "Zoey") {
		t.Fatal("expected studio branding")
	}
	if !strings.Contains(guiHTML, `const title = a.blog ? "@" + a.blog : a.id`) {
		t.Fatal("account list should title by @blog")
	}
}

func TestGuiOpenBrowserRespectsEnv(t *testing.T) {
	t.Setenv("TUMBLR_GUI_NO_BROWSER", "1")
	if guiOpenBrowser() {
		t.Fatal("desktop GUI should not open a browser")
	}
	t.Setenv("TUMBLR_GUI_NO_BROWSER", "")
	if !guiOpenBrowser() {
		t.Fatal("empty env should open the local web GUI")
	}
}

func TestParseFormAccountIDsJSON(t *testing.T) {
	r := httptest.NewRequest(http.MethodPost, "/", strings.NewReader("accountIds=%5B%22a%22%2C%22b%22%5D"))
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	if err := r.ParseForm(); err != nil {
		t.Fatal(err)
	}
	got := parseFormAccountIDs(r)
	if len(got) != 2 || got[0] != "a" || got[1] != "b" {
		t.Fatalf("%v", got)
	}
}
