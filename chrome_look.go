package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"path/filepath"
	"strings"
	"time"
)

func openChromeTab(pageURL string) (chromeJSONTarget, error) {
	u := debugEndpoint() + "/json/new?" + pageURL
	req, err := http.NewRequest(http.MethodPut, u, nil)
	if err != nil {
		return chromeJSONTarget{}, err
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return chromeJSONTarget{}, err
	}
	defer resp.Body.Close()
	var t chromeJSONTarget
	if err := json.NewDecoder(resp.Body).Decode(&t); err != nil {
		return chromeJSONTarget{}, err
	}
	if t.ID == "" {
		return chromeJSONTarget{}, fmt.Errorf("chrome did not open tab")
	}
	time.Sleep(2 * time.Second)
	list, err := listChromeTargets()
	if err != nil {
		return t, nil
	}
	for _, x := range list {
		if x.ID == t.ID {
			return x, nil
		}
	}
	return t, nil
}

func shouldRefuseGatedNav(tabURL, pageURL string) bool {
	return tumblrLoginURL(tabURL) && !tumblrLoginURL(pageURL)
}

func navigateChromeTab(t chromeJSONTarget, pageURL string) error {
	if shouldRefuseGatedNav(t.URL, pageURL) {
		return fmt.Errorf("still on login page")
	}
	return withChromeTargetCDPFor(t, 30*time.Second, func(call cdpCall) error {
		_, err := call("Page.navigate", map[string]any{"url": pageURL})
		return err
	})
}

func pickTabForNavigate(list []chromeJSONTarget, pageURL string) chromeJSONTarget {
	want := strings.ToLower(strings.TrimRight(strings.TrimSpace(pageURL), "/"))
	var exact, settings chromeJSONTarget
	for _, t := range list {
		if t.Type != "page" || t.ID == "" || tumblrLoginURL(t.URL) {
			continue
		}
		u := strings.ToLower(t.URL)
		if want != "" && strings.Contains(u, want) && exact.ID == "" {
			exact = t
		}
		if strings.Contains(u, "/settings/") && settings.ID == "" {
			settings = t
		}
	}
	if exact.ID != "" {
		return exact
	}
	if settings.ID != "" {
		return settings
	}
	return preferredChromePageTarget(list)
}

func closeChromeTarget(t chromeJSONTarget) {
	if strings.TrimSpace(t.ID) == "" {
		return
	}
	if list, err := listChromeTargets(); err == nil {
		pages := 0
		for _, x := range list {
			if x.Type == "page" && x.ID != "" {
				pages++
			}
		}
		if pages <= 1 {
			return
		}
	}
	req, err := http.NewRequest(http.MethodGet, debugEndpoint()+"/json/close/"+t.ID, nil)
	if err != nil {
		return
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return
	}
	resp.Body.Close()
}

func tumblrCustomizeURL(raw string) bool {
	return strings.Contains(strings.ToLower(raw), "tumblr.com/customize/")
}

func blogNameFromCustomizeURL(raw string) string {
	u := strings.ToLower(raw)
	i := strings.Index(u, "/customize/")
	if i < 0 {
		return ""
	}
	rest := u[i+len("/customize/"):]
	if j := strings.IndexAny(rest, "/?#"); j >= 0 {
		rest = rest[:j]
	}
	return rest
}

func leaveCustomizeTheme(t chromeJSONTarget, blogName string) chromeJSONTarget {
	if !tumblrCustomizeURL(t.URL) {
		return t
	}
	if !usableBlogName(blogName) {
		blogName = blogNameFromCustomizeURL(t.URL)
	}
	if !usableBlogName(blogName) {
		return t
	}
	fmt.Println("Left theme customizer — back to blog settings.")
	dest := "https://www.tumblr.com/settings/blog/" + blogName
	_ = navigateChromeTab(t, dest)
	time.Sleep(1500 * time.Millisecond)
	if list, err := listChromeTargets(); err == nil {
		for _, x := range list {
			if x.ID == t.ID {
				return x
			}
			if x.Type == "page" && strings.Contains(x.URL, "/settings/blog/"+blogName) {
				return x
			}
		}
	}
	return t
}

func keepSetupTab(url string) bool {
	u := strings.ToLower(url)
	if strings.Contains(u, "/premium") {
		return false
	}
	if strings.Contains(u, "/customize/") {
		return false
	}
	if strings.Contains(u, "tumblr.com") {
		return true
	}
	if strings.Contains(u, "/privacy/consent") {
		return true
	}
	if strings.Contains(u, "agekey") || strings.Contains(u, "k-id.com") {
		return true
	}
	return false
}

func closeExtraSetupTabs() {
	list, err := listChromeTargets()
	if err != nil {
		return
	}
	var keep, extra []chromeJSONTarget
	for _, t := range list {
		if t.Type != "page" || t.ID == "" {
			continue
		}
		if strings.HasPrefix(t.URL, "chrome://") || strings.HasPrefix(t.URL, "devtools://") {
			continue
		}
		if keepSetupTab(t.URL) {
			keep = append(keep, t)
			continue
		}
		if strings.Contains(strings.ToLower(t.URL), "tumblr.com") {
			extra = append(extra, t)
		}
	}
	if len(keep) == 0 {
		return
	}
	for _, t := range extra {
		fmt.Println("Closing extra tab.")
		closeChromeTarget(t)
	}
}

func openOrNavigateTab(pageURL string) (chromeJSONTarget, error) {
	pageURL = strings.TrimSpace(pageURL)
	list, err := listChromeTargets()
	if err != nil {
		return chromeJSONTarget{}, err
	}
	t := pickTabForNavigate(list, pageURL)
	if t.ID != "" && tumblrLoginURL(t.URL) {
		if !reloginIfOnLoginPage() {
			return chromeJSONTarget{}, fmt.Errorf("still on login page")
		}
		closeLeftoverLoginTabs()
		list, err = listChromeTargets()
		if err != nil {
			return chromeJSONTarget{}, fmt.Errorf("still on login page")
		}
		t = pickTabForNavigate(list, pageURL)
		if t.ID == "" || tumblrLoginURL(t.URL) {
			return chromeJSONTarget{}, fmt.Errorf("still on login page")
		}
	}
	if t.ID == "" {
		if onTumblrLoginPage() {
			return chromeJSONTarget{}, fmt.Errorf("still on login page")
		}
		return openChromeTab(pageURL)
	}
	want := strings.ToLower(strings.TrimRight(pageURL, "/"))
	if !strings.Contains(strings.ToLower(t.URL), want) {
		if err := navigateChromeTab(t, pageURL); err != nil {
			return chromeJSONTarget{}, err
		}
		// Brief settle — keep settings hops snappy for batch setup.
		deadline := time.Now().Add(800 * time.Millisecond)
		for time.Now().Before(deadline) {
			if list2, err := listChromeTargets(); err == nil {
				for _, x := range list2 {
					if x.ID == t.ID && strings.Contains(strings.ToLower(x.URL), want) {
						t = x
						goto navigated
					}
				}
			}
			time.Sleep(100 * time.Millisecond)
		}
	navigated:
	}
	if list, err := listChromeTargets(); err == nil {
		for _, x := range list {
			if x.ID != t.ID {
				continue
			}
			if tumblrLoginURL(x.URL) {
				if reloginIfOnLoginPage() {
					closeLeftoverLoginTabs()
					if list, err := listChromeTargets(); err == nil {
						pref := preferredChromePageTarget(list)
						if pref.ID != "" && !tumblrLoginURL(pref.URL) {
							return pref, nil
						}
					}
				}
				return x, fmt.Errorf("tumblr bounced back to login")
			}
			return x, nil
		}
	}
	return t, nil
}

const chromeLookFromPageJS = `(() => {
	const avatarURLs = [];
	const addAvatar = (u) => {
		u = (u || '').trim();
		if (u && /^(blob:|https?:)/i.test(u)) avatarURLs.push(u);
	};
	const avatarNeedle = /avatar|profile (photo|picture)|foto de perfil/i;
	const avatarControl = [...document.querySelectorAll('[aria-label]')]
		.find(el => avatarNeedle.test(el.getAttribute('aria-label') || ''));
	if (avatarControl) {
		let n = avatarControl;
		for (let i = 0; i < 6 && n; i++, n = n.parentElement) {
			for (const img of n.querySelectorAll('img')) addAvatar(img.currentSrc || img.src);
			const bg = getComputedStyle(n).backgroundImage || '';
			for (const m of bg.matchAll(/url\(["']?([^"')]+)["']?\)/g)) addAvatar(m[1]);
			if (avatarURLs.length) break;
		}
	}
	if (!avatarURLs.length) {
		for (const img of document.querySelectorAll('img')) {
			const meta = (img.alt || '') + ' ' + (img.getAttribute('aria-label') || '') + ' ' + (img.className || '');
			if (avatarNeedle.test(meta)) addAvatar(img.currentSrc || img.src);
		}
	}
	// Never use the banner as proof that the avatar was changed.
	const avatar = avatarURLs.find(u => /blob:|media\.tumblr\.com/i.test(u) && !/default_avatar|default_header|assets\.tumblr\.com/i.test(u)) || '';
	const titleEl = document.querySelector('input[aria-label="Blog title"]');
	const heading = [...document.querySelectorAll('h1,h2,h3')].map(h => (h.innerText || '').replace(/\s+/g,' ').trim()).find(t => /zoey/i.test(t)) || '';
	const title = (titleEl && titleEl.value) || heading;
	const bioEl = document.querySelector('textarea[aria-label="Blog description"]');
	const body = (document.body && document.body.innerText) || '';
	const bio = (bioEl && bioEl.value) || (/(lil\.cosmic|instagram\.com\/lil)/i.test(body) ? 'lil.cosmic' : '');
	return {title: title || '', bio: bio || '', avatar: avatar};
})()`

func lookStatusFromPage() setupLookStatus {
	t, ok := appearancePageTarget()
	if !ok {
		return setupLookStatus{}
	}
	var raw struct {
		Title  string `json:"title"`
		Bio    string `json:"bio"`
		Avatar string `json:"avatar"`
	}
	if err := evalJSONOnChromeTarget(t, chromeLookFromPageJS, &raw); err != nil {
		return setupLookStatus{}
	}
	return setupLookStatus{
		Title:    raw.Title,
		Bio:      raw.Bio,
		Avatar:   raw.Avatar,
		TitleOK:  wantedTitleSet(raw.Title),
		BioOK:    wantedBioSet(raw.Bio),
		AvatarOK: avatarLooksSet(raw.Avatar),
	}
}

func cdpNodeIDs(raw json.RawMessage) []int {
	var wrap struct {
		NodeIds []int `json:"nodeIds"`
	}
	_ = json.Unmarshal(raw, &wrap)
	return wrap.NodeIds
}

func cdpEvalString(raw json.RawMessage) string {
	var wrap struct {
		Result struct {
			Value json.RawMessage `json:"value"`
		} `json:"result"`
	}
	if json.Unmarshal(raw, &wrap) != nil {
		return ""
	}
	var s string
	if json.Unmarshal(wrap.Result.Value, &s) == nil {
		return s
	}
	return strings.Trim(string(wrap.Result.Value), `"`)
}

func cdpEvalBool(raw json.RawMessage) bool {
	var wrap struct {
		Result struct {
			Value json.RawMessage `json:"value"`
		} `json:"result"`
	}
	if json.Unmarshal(raw, &wrap) != nil {
		return false
	}
	var b bool
	if json.Unmarshal(wrap.Result.Value, &b) == nil {
		return b
	}
	return strings.TrimSpace(string(wrap.Result.Value)) == "true"
}

func cdpEnsureEditAppearance(call cdpCall) error {
	openRaw, _ := call("Runtime.evaluate", map[string]any{"expression": chromeAppearanceEditorOpenJS, "returnByValue": true})
	if cdpEvalBool(openRaw) {
		return nil
	}
	x, y, ok, err := cdpEvalBox(call, chromeEditAppearanceBoxJS)
	if err != nil {
		return err
	}
	if !ok {
		return fmt.Errorf("edit appearance button not found")
	}
	if err := cdpDispatchClick(call, x, y); err != nil {
		return err
	}
	fmt.Println("Clicked: Edit appearance through Chrome DevTools")
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		hrefRaw, _ := call("Runtime.evaluate", map[string]any{"expression": "location.href", "returnByValue": true})
		if tumblrCustomizeURL(cdpEvalString(hrefRaw)) {
			return fmt.Errorf("opened theme customizer instead of appearance")
		}
		openRaw, _ = call("Runtime.evaluate", map[string]any{"expression": chromeAppearanceEditorOpenJS, "returnByValue": true})
		if cdpEvalBool(openRaw) {
			return nil
		}
		time.Sleep(250 * time.Millisecond)
	}
	return fmt.Errorf("edit appearance did not open — no appearance changes were made")
}

func cdpDispatchClick(call cdpCall, x, y float64) error {
	for _, typ := range []string{"mouseMoved", "mousePressed", "mouseReleased"} {
		buttons := 0
		if typ == "mousePressed" {
			buttons = 1
		}
		if _, err := call("Input.dispatchMouseEvent", map[string]any{
			"type":        typ,
			"x":           x,
			"y":           y,
			"button":      "left",
			"buttons":     buttons,
			"clickCount":  1,
			"pointerType": "mouse",
		}); err != nil {
			return err
		}
	}
	return nil
}

func cdpEvalBox(call cdpCall, expr string) (float64, float64, bool, error) {
	raw, err := call("Runtime.evaluate", map[string]any{
		"expression":    expr,
		"returnByValue": true,
		"userGesture":   true,
	})
	if err != nil {
		return 0, 0, false, err
	}
	var wrap struct {
		Result struct {
			Value json.RawMessage `json:"value"`
		} `json:"result"`
	}
	var box struct {
		Found bool    `json:"found"`
		X     float64 `json:"x"`
		Y     float64 `json:"y"`
	}
	if json.Unmarshal(raw, &wrap) != nil {
		return 0, 0, false, nil
	}
	_ = json.Unmarshal(wrap.Result.Value, &box)
	return box.X, box.Y, box.Found && box.X >= 1 && box.Y >= 1, nil
}

func cdpSetFileAfterClick(call cdpCall, boxJS, abs, label string) error {
	fileChooserMu.Lock()
	lastFileChooserBackend = 0
	fileChooserMu.Unlock()
	x, y, ok, err := cdpEvalBox(call, boxJS)
	if err != nil {
		return err
	}
	if !ok {
		return fmt.Errorf("%s button not found", label)
	}
	fmt.Println("Clicked:", label)
	if err := cdpDispatchClick(call, x, y); err != nil {
		return err
	}
	return cdpSetFileFromOpenedChooser(call, abs, label)
}

func cdpSetFileAfterJSClick(call cdpCall, clickJS, abs, label string) error {
	fileChooserMu.Lock()
	lastFileChooserBackend = 0
	fileChooserMu.Unlock()
	raw, err := call("Runtime.evaluate", map[string]any{
		"expression": clickJS, "returnByValue": true, "userGesture": true,
	})
	if err != nil {
		return err
	}
	hit := cdpEvalString(raw)
	if hit == "" {
		return fmt.Errorf("%s menu item not found", label)
	}
	fmt.Println("Clicked:", hit)
	return cdpSetFileFromOpenedChooser(call, abs, label)
}

func cdpSetFileFromOpenedChooser(call cdpCall, abs, label string) error {
	var backend int
	for i := 0; i < 16; i++ {
		fileChooserMu.Lock()
		backend = lastFileChooserBackend
		fileChooserMu.Unlock()
		if backend != 0 {
			break
		}
		if _, err := call("Runtime.evaluate", map[string]any{"expression": "1", "returnByValue": true}); err != nil {
			return err
		}
		time.Sleep(250 * time.Millisecond)
	}
	fileChooserMu.Lock()
	if lastFileChooserBackend != 0 {
		backend = lastFileChooserBackend
		lastFileChooserBackend = 0
	}
	fileChooserMu.Unlock()
	if backend == 0 {
		// Never fall back to an arbitrary input[type=file]. Tumblr keeps a
		// header input in the editor even while changing the avatar, so that
		// fallback uploaded the profile photo as the banner.
		return fmt.Errorf("%s file chooser did not open; refusing to use an unrelated file input", label)
	}
	if _, err := call("DOM.setFileInputFiles", map[string]any{"backendNodeId": backend, "files": []string{abs}}); err != nil {
		return err
	}
	fmt.Println(label, "file set")
	time.Sleep(2 * time.Second)
	return nil
}

// cdpSetImageFile supports Tumblr's two image-edit flows. Some controls open
// the chooser directly; others first open a menu containing "Upload photo".
// Both paths remain tied to the pencil that was explicitly clicked.
func cdpSetImageFile(call cdpCall, boxJS, abs, label string) error {
	directErr := cdpSetFileAfterClick(call, boxJS, abs, label)
	if directErr == nil {
		return nil
	}
	if strings.Contains(directErr.Error(), "button not found") {
		return directErr
	}
	menuErr := cdpSetFileAfterJSClick(call, chromeUploadPhotoJS, abs, label)
	if menuErr == nil {
		return nil
	}
	return fmt.Errorf("%s; then upload menu: %v", directErr, menuErr)
}

func setLookImageOnTarget(t chromeJSONTarget, boxJS, abs, label string) error {
	return withChromeTargetCDPFor(t, 45*time.Second, func(call cdpCall) error {
		if _, err := call("Page.enable", nil); err != nil {
			return err
		}
		_, _ = call("Page.setInterceptFileChooserDialog", map[string]any{"enabled": true})
		return cdpSetImageFile(call, boxJS, abs, label)
	})
}

func trySetHeaderFileOnTarget(t chromeJSONTarget, headerAbs string) error {
	return withChromeTargetCDPFor(t, 45*time.Second, func(call cdpCall) error {
		if _, err := call("Page.enable", nil); err != nil {
			return err
		}
		_, _ = call("Page.setInterceptFileChooserDialog", map[string]any{"enabled": true})
		return trySetHeaderFile(call, headerAbs)
	})
}

func applyBlogLookOnTarget(t chromeJSONTarget, imagePath, title, desc string) error {
	fillJS := chromeFillTitleBioJS(title, desc)
	abs, err := imageFromDir(imagePath, profilePicsDir, "profile picture")
	if err != nil {
		return err
	}
	return withChromeTargetCDPFor(t, 90*time.Second, func(call cdpCall) error {
		if _, err := call("Page.enable", nil); err != nil {
			return err
		}
		_, _ = call("Page.setInterceptFileChooserDialog", map[string]any{"enabled": true})
		time.Sleep(1500 * time.Millisecond)
		for i := 0; i < 6; i++ {
			if nag := dismissChromeLeakDialog(); nagWasDismissed(nag) {
				fmt.Println("Dismissed:", nag)
				time.Sleep(200 * time.Millisecond)
				continue
			}
			raw, err := call("Runtime.evaluate", map[string]any{"expression": chromeDismissOverlaysJS, "returnByValue": true})
			if err != nil {
				return err
			}
			overlay := cdpEvalString(raw)
			if overlay == "" {
				if nag := dismissNativePasswordNag(); nagWasDismissed(nag) {
					fmt.Println("Dismissed:", nag)
					time.Sleep(250 * time.Millisecond)
					continue
				}
				break
			}
			fmt.Println("Dismissed:", overlay)
			time.Sleep(250 * time.Millisecond)
		}
		if err := cdpEnsureEditAppearance(call); err != nil {
			return err
		}
		time.Sleep(800 * time.Millisecond)
		raw, err := call("Runtime.evaluate", map[string]any{"expression": fillJS, "returnByValue": true})
		if err != nil {
			return err
		}
		fmt.Println("Filled:", cdpEvalString(raw))
		return cdpSetImageFile(call, chromeAvatarBoxJS, abs, "Edit blog avatar image")
	})
}

const chromeAppearanceSaveHitJS = `(() => {
	const vis = (el) => {
		if (!el) return false;
		const s = getComputedStyle(el);
		const r = el.getBoundingClientRect();
		return s.display !== 'none' && s.visibility !== 'hidden' && r.width > 2 && r.height > 2;
	};
	const textOf = (el) => (el.innerText || el.getAttribute('aria-label') || '').replace(/\s+/g, ' ').trim();
	const skip = /password|agekey|verify age|cookie|consent|never|not now/i;
	const els = [...document.querySelectorAll('button, [role="button"], a')].filter(vis);
	const pick = (re) => els.find(b => re.test(textOf(b)) && !skip.test(textOf(b)));
	const el = pick(/^(apply|use this|use photo|set avatar|done)$/i) || pick(/^(save|save changes)$/i);
	if (!el) return {found: false, text: '', x: 0, y: 0};
	el.scrollIntoView({block: 'center', inline: 'nearest'});
	const r = el.getBoundingClientRect();
	return {found: true, text: textOf(el), x: r.left + r.width / 2, y: r.top + r.height / 2};
})()`

const chromeAppearanceEditorOpenJS = `(() => {
	const vis = (el) => {
		if (!el) return false;
		const s = getComputedStyle(el);
		if (s.display === 'none' || s.visibility === 'hidden') return false;
		const r = el.getBoundingClientRect();
		return r.width > 2 && r.height > 2;
	};
	if (document.querySelector('[aria-label="Edit blog avatar image"]')) return true;
	if (document.querySelector('[aria-label="Edit blog header image"]')) return true;
	const pencil = [...document.querySelectorAll('[aria-label]')].some(el => vis(el) &&
		/(edit|editar|cambiar|bearbeiten).*(avatar|header|cabecera|encabezado|banner)/i.test(el.getAttribute('aria-label') || ''));
	if (pencil) return true;
	const field = [...document.querySelectorAll('input, textarea')].some(el => vis(el) &&
		/blog (title|description)|t[ií]tulo del blog|descripci[oó]n del blog|blogtitel/i.test(el.getAttribute('aria-label') || ''));
	if (field) return true;
	return [...document.querySelectorAll('input[type=file]')].some(vis);
})()`

func appearancePageTarget() (chromeJSONTarget, bool) {
	list, err := listChromeTargets()
	if err != nil {
		return chromeJSONTarget{}, false
	}
	for _, t := range list {
		if t.Type == "page" && strings.Contains(t.URL, "/settings/blog/") {
			return t, true
		}
	}
	return tumblrPageTarget()
}

func appearanceEditorOpenOnPort(port string) bool {
	port = strings.TrimSpace(port)
	if port != "" {
		liveDebugPort = port
	}
	t, ok := appearancePageTarget()
	if !ok {
		return false
	}
	var open bool
	if err := evalJSONOnChromeTarget(t, chromeAppearanceEditorOpenJS, &open); err != nil {
		return false
	}
	return open
}

func clickAppearanceSaveOnPort(port string) error {
	port = strings.TrimSpace(port)
	if port != "" {
		liveDebugPort = port
	}
	t, ok := appearancePageTarget()
	if !ok {
		return fmt.Errorf("no appearance tab")
	}
	return clickAppearanceSaveOnTarget(t)
}

func clickAppearanceSaveOnTarget(t chromeJSONTarget) error {
	if toast := appearanceErrorOnTarget(t); toast != "" {
		return fmt.Errorf("%s", toast)
	}
	return withChromeTargetCDPFor(t, 25*time.Second, func(call cdpCall) error {
		buttonBox := func(pattern string) string {
			return `(() => {
                const el = [...document.querySelectorAll('button, [role="button"]')].find(el => {
                    const r = el.getBoundingClientRect();
                    return r.width > 2 && r.height > 2 && !el.disabled && el.getAttribute('aria-disabled') !== 'true' &&
                      ` + pattern + `.test((el.innerText || el.getAttribute('aria-label') || '').trim());
                });
                if (!el) return {found:false};
                el.scrollIntoView({block:'center'});
                const r=el.getBoundingClientRect();
                return {found:true,x:r.left+r.width/2,y:r.top+r.height/2};
            })()`
		}
		x, y, ok, err := cdpEvalBox(call, buttonBox(`/^(apply|use this|use photo|set avatar|done)$/i`))
		if err != nil {
			return err
		}
		// Crop / header panel often has "Show header image" — turn it on before Confirm.
		if raw, e := call("Runtime.evaluate", map[string]any{"expression": chromeEnableShowHeaderJS, "returnByValue": true}); e == nil {
			if s := cdpEvalString(raw); s != "" && s != "no-toggle" {
				fmt.Println("Header toggle:", s)
			}
		}
		if ok {
			if err := cdpDispatchClick(call, x, y); err != nil {
				return err
			}
			fmt.Println("Confirmed photo crop")
			time.Sleep(time.Second)
		}
		deadline := time.Now().Add(12 * time.Second)
		for time.Now().Before(deadline) {
			x, y, ok, err = cdpEvalBox(call, buttonBox(`/^(save|save changes)$/i`))
			if err != nil {
				return err
			}
			if ok {
				if err := cdpDispatchClick(call, x, y); err != nil {
					return err
				}
				fmt.Println("Clicked: Save appearance")
				time.Sleep(2 * time.Second)
				if toast := appearanceErrorOnTarget(t); toast != "" {
					return fmt.Errorf("%s", toast)
				}
				return nil
			}
			time.Sleep(300 * time.Millisecond)
		}
		return fmt.Errorf("appearance Save button did not become available after crop")
	})
}
func applyBlogLookOnPort(port string) error {
	port = strings.TrimSpace(port)
	if port == "" {
		port = "9344"
	}
	liveDebugPort = port
	cfg := loadProfileSetupConfig()
	pics, err := listProfilePics()
	if err != nil {
		return err
	}
	pic := picForCurrentAccount(pics)
	name := strings.TrimSpace(setupBlogName)
	if !usableBlogName(name) {
		auth, err := loadAuth()
		if err != nil {
			return err
		}
		if strings.TrimSpace(auth.Cookie) != "" {
			if n, err := syncAuthIdentity(newHumanClient(), &auth); err == nil {
				name = n
			}
		}
		if !usableBlogName(name) {
			name = resolveSetupBlogName(auth, name)
		}
	}
	if !usableBlogName(name) {
		return fmt.Errorf("could not detect blog name")
	}
	if look := currentLookStatus(name); look.Complete() {
		fmt.Println("Name, bio, and avatar already set.")
		closeExtraSetupTabs()
		return nil
	}
	settings := "https://www.tumblr.com/settings/blog/" + name
	fmt.Println("Opening appearance tab for", name)
	fmt.Println("Avatar:", filepath.ToSlash(pic))
	var t chromeJSONTarget
	if list, err := listChromeTargets(); err == nil {
		for _, x := range list {
			if x.Type == "page" && strings.Contains(x.URL, "/settings/blog/"+name) {
				t = x
				break
			}
		}
		if t.ID == "" {
			for _, x := range list {
				if x.Type == "page" && strings.Contains(x.URL, "/settings/blog/") {
					t = x
					break
				}
			}
		}
	}
	if t.ID == "" || !strings.Contains(t.URL, "/settings/blog/") {
		var err error
		t, err = openOrNavigateTab(settings)
		if err != nil {
			return err
		}
	}
	t = leaveCustomizeTheme(t, name)
	closeExtraSetupTabs()
	if err := applyBlogLookOnTarget(t, pic, cfg.Title, cfg.Description); err != nil {
		return err
	}
	if err := clickAppearanceSaveOnTarget(t); err != nil {
		fmt.Println("Save:", err)
	}
	deadline := time.Now().Add(12 * time.Second)
	for time.Now().Before(deadline) {
		look := currentLookStatus(name)
		if look.AvatarOK {
			fmt.Println("Confirmed name, bio, and avatar.")
			closeExtraSetupTabs()
			return nil
		}
		time.Sleep(1500 * time.Millisecond)
	}
	look := currentLookStatus(name)
	fmt.Printf("After save: name=%s bio=%s avatar=%s\n", setupFlag(look.TitleOK), setupFlag(look.BioOK), setupFlag(look.AvatarOK))
	closeExtraSetupTabs()
	return nil
}
