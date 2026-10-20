package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
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

var chromeVersionDirRe = regexp.MustCompile(`^(\d+)\.\d+\.\d+\.\d+$`)

// Headless Chrome announces itself as "HeadlessChrome" in its user agent,
// which Tumblr's login treats as a bot; send the normal desktop one instead.
func headlessUserAgent(chrome string) string {
	major := "140"
	if entries, err := os.ReadDir(filepath.Dir(chrome)); err == nil {
		for _, e := range entries {
			if m := chromeVersionDirRe.FindStringSubmatch(e.Name()); e.IsDir() && m != nil {
				major = m[1]
			}
		}
	}
	return "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/" + major + ".0.0.0 Safari/537.36"
}

func headlessChromeArgs(chrome string) []string {
	return []string{
		"--headless=new",
		"--window-size=1280,900",
		"--mute-audio",
		"--user-agent=" + headlessUserAgent(chrome),
	}
}
