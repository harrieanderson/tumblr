package main

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"sync"
)

// browserFingerprint is the single stable Chrome identity shared by HTTP
// (headers + uTLS) and headless/visible Chrome launch args. One profile per
// process/installation — never randomized per request or account.
type browserFingerprint struct {
	Major           int
	UA              string
	SecCHUA         string
	SecCHUAPlatform string
	AcceptLanguage  string
	Timezone        string
	Lang            string
	Platform        string
	ViewportW       int
	ViewportH       int
}

// Fallback major when the installed Chrome version cannot be read from disk.
// Matches the previous hard-coded HTTP identity.
const fingerprintFallbackMajor = 152

var chromeVersionDirRe = regexp.MustCompile(`^(\d+)\.\d+\.\d+\.\d+$`)

var (
	fpOnce sync.Once
	fp     browserFingerprint

	// fpSnapshotFromDisk is true when init read a real Application\<ver> major
	// (not the fallback). Used by verifyLaunchedChrome to decide fail-closed.
	fpSnapshotFromDisk bool
	fpSnapshotPath     string

	// chromeUA / chromeSecCHUA are filled from the shared fingerprint so
	// existing call sites stay in sync with headless Chrome.
	chromeUA      string
	chromeSecCHUA string
)

func init() {
	_ = fingerprint()
}

func fingerprint() browserFingerprint {
	fpOnce.Do(initFingerprint)
	return fp
}

func initFingerprint() {
	path, _ := chromePath()
	fpSnapshotPath = path
	major := detectChromeMajorNear(path)
	if major <= 0 {
		major = fingerprintFallbackMajor
		fpSnapshotFromDisk = false
		fmt.Fprintf(os.Stderr, "fingerprint: WARNING could not detect installed Chrome version (path=%q); using fallback major %d\n", path, major)
	} else {
		fpSnapshotFromDisk = true
		fmt.Fprintf(os.Stderr, "fingerprint: startup snapshot Chrome major %d (from %s)\n", major, path)
	}
	fp = buildFingerprint(major)
	chromeUA = fp.UA
	chromeSecCHUA = fp.SecCHUA
}

func buildFingerprint(major int) browserFingerprint {
	if major <= 0 {
		major = fingerprintFallbackMajor
	}
	return browserFingerprint{
		Major:           major,
		UA:              fmt.Sprintf("Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/%d.0.0.0 Safari/537.36", major),
		SecCHUA:         fmt.Sprintf(`"Chromium";v="%d", "Not?A_Brand";v="24", "Google Chrome";v="%d"`, major, major),
		SecCHUAPlatform: `"Windows"`,
		AcceptLanguage:  "en-GB,en-US;q=0.9,en;q=0.8",
		Timezone:        "Europe/London",
		Lang:            "en-GB",
		Platform:        "Windows",
		ViewportW:       1280,
		ViewportH:       900,
	}
}

// detectInstalledChromeMajor reads the highest Application\<ver> sibling of
// chrome.exe so HTTP claims match the browser we actually launch.
func detectInstalledChromeMajor() int {
	path, err := chromePath()
	if err != nil {
		return 0
	}
	return detectChromeMajorNear(path)
}

// detectChromeMajorNear finds the highest N.N.N.N version directory next to
// chromeExe (typical Google Chrome Application layout on Windows).
func detectChromeMajorNear(chromeExe string) int {
	if chromeExe == "" {
		return 0
	}
	entries, err := os.ReadDir(filepath.Dir(chromeExe))
	if err != nil {
		return 0
	}
	best := 0
	for _, e := range entries {
		m := chromeVersionDirRe.FindStringSubmatch(e.Name())
		if !e.IsDir() || m == nil {
			continue
		}
		n, err := strconv.Atoi(m[1])
		if err != nil || n <= 0 {
			continue
		}
		if n > best {
			best = n
		}
	}
	return best
}

// verifyLaunchedChrome ensures the binary we are about to start matches the
// process fingerprint snapshot. Unverifiable installs keep the fallback (warn
// only). Fail closed only when both snapshot and launched majors were read
// from disk and disagree.
func verifyLaunchedChrome(chromeExe string) error {
	_ = fingerprint() // ensure snapshot exists
	launched := detectChromeMajorNear(chromeExe)
	if launched <= 0 {
		fmt.Fprintf(os.Stderr, "fingerprint: WARNING could not verify launched Chrome version at %q (snapshot major=%d fromDisk=%v)\n",
			chromeExe, fp.Major, fpSnapshotFromDisk)
		return nil
	}
	if !fpSnapshotFromDisk {
		if launched != fp.Major {
			fmt.Fprintf(os.Stderr, "fingerprint: WARNING launched Chrome major %d differs from fallback claim %d\n", launched, fp.Major)
		}
		return nil
	}
	if launched != fp.Major {
		return fmt.Errorf("fingerprint: Chrome major mismatch — snapshot=%d launched=%d path=%s (refusing to start)", fp.Major, launched, chromeExe)
	}
	return nil
}

func (p browserFingerprint) windowSizeArg() string {
	return fmt.Sprintf("--window-size=%d,%d", p.ViewportW, p.ViewportH)
}

func (p browserFingerprint) langArg() string {
	return "--lang=" + p.Lang
}

func (p browserFingerprint) userAgentArg() string {
	return "--user-agent=" + p.UA
}
