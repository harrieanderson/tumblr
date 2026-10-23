package main

import (
	"encoding/json"
	"os"
)

// headlessChrome hides every bot Chrome window. Captchas can't be solved while
// it's on, so a login that hits one times out instead of waiting for you.
var headlessChrome = os.Getenv("TUMBLR_HEADLESS") == "1"

const headlessPrefFile = "config/headless.json"

func loadHeadlessPref() {
	raw, err := os.ReadFile(headlessPrefFile)
	if err != nil {
		return
	}
	var p struct {
		Headless bool `json:"headless"`
	}
	if json.Unmarshal(raw, &p) == nil && p.Headless {
		headlessChrome = true
	}
}

func saveHeadlessPref(on bool) error {
	headlessChrome = on
	raw, err := json.MarshalIndent(map[string]bool{"headless": on}, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(headlessPrefFile, append(raw, '\n'), 0o644)
}

// Headless Chrome announces itself as "HeadlessChrome" in its user agent,
// which Tumblr's login treats as a bot; send the shared desktop fingerprint instead.
// Lang is set by the Chrome launcher so visible and headless share one --lang.
func headlessChromeArgs(chrome string) []string {
	_ = chrome // path kept for call-site compatibility; version comes from fingerprint()
	fp := fingerprint()
	return []string{
		"--headless=new",
		fp.windowSizeArg(),
		"--mute-audio",
		fp.userAgentArg(),
	}
}
