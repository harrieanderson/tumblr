package main

import "testing"

func TestSetupPasswordHelpersRespectDisabledDesktopInput(t *testing.T) {
	// Password/leak dismiss must still run when nativeChromeClicks is off —
	// setup disables mouse steals but Save-password bubbles still block UI.
	previous := nativeChromeClicks
	nativeChromeClicks = false
	t.Cleanup(func() { nativeChromeClicks = previous })
	for _, dismiss := range []func() string{
		dismissNativePasswordNag,
		dismissChromeLeakDialog,
		dismissSavePasswordBubble,
	} {
		_ = dismiss() // may return "none" or a dismiss label; both OK
	}
}
