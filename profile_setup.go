package main

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/chromedp/chromedp"
)

const profileSetupFile = "config/profile.json"

type profileSetupConfig struct {
	Title         string `json:"title"`
	Description   string `json:"description"`
	Language      string `json:"language"`
	BirthdayYear  int    `json:"birthdayYear"`
	BirthdayMonth int    `json:"birthdayMonth"`
	BirthdayDay   int    `json:"birthdayDay"`
}

func loadProfileSetupConfig() profileSetupConfig {
	cfg := profileSetupConfig{
		Title:         "Zoey 🌷",
		Description:   "18+ only Instaaaa: https://www.instagram.com/lil.cosmic.bunny.o7",
		Language:      "en",
		BirthdayYear:  1997,
		BirthdayMonth: 10,
		BirthdayDay:   5,
	}
	raw, err := os.ReadFile(profileSetupFile)
	if err != nil {
		return cfg
	}
	_ = json.Unmarshal(raw, &cfg)
	if strings.TrimSpace(cfg.Title) == "" {
		cfg.Title = "Zoey 🌷"
	}
	if strings.TrimSpace(cfg.Language) == "" {
		cfg.Language = "en"
	}
	if cfg.BirthdayYear <= 0 {
		cfg.BirthdayYear = 1997
	}
	if cfg.BirthdayMonth <= 0 {
		cfg.BirthdayMonth = 10
	}
	if cfg.BirthdayDay <= 0 {
		cfg.BirthdayDay = 5
	}
	return cfg
}

func usableBlogName(name string) bool {
	name = strings.TrimSpace(name)
	if name == "" || strings.ContainsAny(name, ":/") {
		return false
	}
	if strings.EqualFold(name, "YOUR_BLOG_NAME") {
		return false
	}
	if len(name) >= 20 && !strings.Contains(name, "-") {
		upper, lower := false, false
		for _, r := range name {
			switch {
			case r >= 'A' && r <= 'Z':
				upper = true
			case r >= 'a' && r <= 'z':
				lower = true
			}
		}
		if upper && lower {
			return false
		}
	}
	return true
}

var setupBlogName string
var ageKeyHaveClicked bool

type setupLookStatus struct {
	Title    string
	Bio      string
	Avatar   string
	TitleOK  bool
	BioOK    bool
	AvatarOK bool
}

func (s setupLookStatus) OK() bool {
	return s.TitleOK && s.BioOK
}

func (s setupLookStatus) Complete() bool {
	return s.OK() && s.AvatarOK
}

func wantedTitleSet(title string) bool {
	return strings.Contains(strings.ToLower(title), "zoey")
}

func wantedBioSet(desc string) bool {
	d := strings.ToLower(desc)
	return strings.Contains(d, "lil.cosmic") || strings.Contains(d, "instagram.com/lil")
}

func avatarLooksSet(url string) bool {
	u := strings.ToLower(strings.TrimSpace(url))
	if u == "" {
		return false
	}
	if strings.Contains(u, "default_avatar") || strings.Contains(u, "default_avatars") {
		return false
	}
	if strings.Contains(u, "assets.tumblr.com") {
		return false
	}
	return true
}

func lookStatusFromAPI(blog string) setupLookStatus {
	blog = strings.TrimSpace(blog)
	if blog == "" {
		return setupLookStatus{}
	}
	auth, err := loadAuth()
	if err != nil {
		return setupLookStatus{}
	}
	p, err := fetchBlogProfile(newHumanClient(), auth, blog)
	if err != nil {
		return setupLookStatus{}
	}
	return setupLookStatus{
		Title:    p.Title,
		Bio:      p.Description,
		Avatar:   p.Avatar,
		TitleOK:  wantedTitleSet(p.Title),
		BioOK:    wantedBioSet(p.Description),
		AvatarOK: avatarLooksSet(p.Avatar),
	}
}

type setupChecklist struct {
	TitleOK    bool
	BioOK      bool
	AvatarOK   bool
	BirthdayOK bool
	CookiesOK  bool
	AgeOK      bool
}

func (c setupChecklist) Ready() bool {
	return c.TitleOK && c.BioOK && c.AvatarOK && c.BirthdayOK && c.CookiesOK && c.AgeOK
}

func setupFlag(ok bool) string {
	if ok {
		return "ok"
	}
	return "no"
}

func ageKeyHandsOff() bool {
	if findWindowContaining("Windows Security") != 0 {
		return true
	}
	if chromeHasTargetHost("agekey.org") || chromeHasTargetHost("agekey.com") {
		return true
	}
	return false
}

const chromeReadBlogNameJS = `(() => {
	const skip = /^(www|settings|dashboard|explore|login|register|blog|search|inbox|likes|following|activity|tagged|new|reblog|account|apps)$/i;
	const bad = (n) => !n || n.indexOf(':') >= 0 || skip.test(n);
	const fromPath = (path) => {
		let m = (path || '').match(/^\/settings\/blog\/([^/?#]+)/);
		if (m && !bad(m[1])) return decodeURIComponent(m[1]);
		m = (path || '').match(/^\/blog\/view\/([^/?#]+)/);
		if (m && !bad(m[1])) return decodeURIComponent(m[1]);
		return '';
	};
	try {
		const n = fromPath(location.pathname);
		if (n) return n;
	} catch (e) {}
	for (const a of document.querySelectorAll('a[href*="/settings/blog/"]')) {
		try {
			const u = new URL(a.href, location.origin);
			const n = fromPath(u.pathname);
			if (n) return n;
		} catch (e) {}
	}
	return '';
})()`

func blogNameFromURL(raw string) string {
	u := strings.TrimSpace(raw)
	if i := strings.Index(u, "://"); i >= 0 {
		u = u[i+3:]
		if j := strings.Index(u, "/"); j >= 0 {
			u = u[j:]
		} else {
			return ""
		}
	}
	if i := strings.IndexAny(u, "?#"); i >= 0 {
		u = u[:i]
	}
	for _, prefix := range []string{"/settings/blog/", "/blog/view/"} {
		idx := strings.Index(u, prefix)
		if idx < 0 {
			continue
		}
		rest := u[idx+len(prefix):]
		if j := strings.IndexAny(rest, "/"); j >= 0 {
			rest = rest[:j]
		}
		if usableBlogName(rest) {
			return rest
		}
	}
	return ""
}

func firstTumblrPageTarget() (chromeJSONTarget, error) {
	list, err := listChromeTargets()
	if err != nil {
		return chromeJSONTarget{}, err
	}
	var fallback chromeJSONTarget
	for _, t := range list {
		if t.Type != "page" || t.ID == "" {
			continue
		}
		if strings.HasPrefix(t.URL, "chrome://") || strings.HasPrefix(t.URL, "devtools://") {
			continue
		}
		if fallback.ID == "" {
			fallback = t
		}
		if strings.Contains(t.URL, "tumblr.com") {
			return t, nil
		}
	}
	if fallback.ID != "" {
		return fallback, nil
	}
	return chromeJSONTarget{}, fmt.Errorf("no chrome page target")
}

func evalBlogNameOnTarget(t chromeJSONTarget) string {
	var name string
	_ = withChromeTargetCDP(t, func(call cdpCall) error {
		raw, err := call("Runtime.evaluate", map[string]any{
			"expression":    chromeReadBlogNameJS,
			"returnByValue": true,
		})
		if err != nil {
			return err
		}
		name = cdpEvalString(raw)
		return nil
	})
	return strings.TrimSpace(name)
}

func discoverBlogNameInChrome() string {
	if list, err := listChromeTargets(); err == nil {
		for _, t := range list {
			if t.Type != "page" {
				continue
			}
			if n := blogNameFromURL(t.URL); usableBlogName(n) {
				return n
			}
		}
	}
	t, err := firstTumblrPageTarget()
	if err != nil {
		return ""
	}
	if n := evalBlogNameOnTarget(t); usableBlogName(n) {
		return n
	}
	_ = withChromeTargetCDP(t, func(call cdpCall) error {
		_, err := call("Page.navigate", map[string]any{"url": "https://www.tumblr.com/settings"})
		return err
	})
	time.Sleep(3 * time.Second)
	if n := evalBlogNameOnTarget(t); usableBlogName(n) {
		return n
	}
	if list, err := listChromeTargets(); err == nil {
		for _, x := range list {
			if n := blogNameFromURL(x.URL); usableBlogName(n) {
				return n
			}
		}
	}
	return ""
}

func resolveSetupBlogName(auth Auth, apiName string) string {
	if usableBlogName(apiName) {
		return strings.TrimSpace(apiName)
	}
	if n := strings.TrimSpace(auth.Blog); usableBlogName(n) {
		return n
	}
	if n := strings.TrimPrefix(strings.TrimSpace(auth.Blog), "t:"); usableBlogName(n) {
		return n
	}
	if n := discoverBlogNameInChrome(); usableBlogName(n) {
		return n
	}
	return strings.TrimSpace(apiName)
}

func runSetupProfile() error {
	cfg := loadProfileSetupConfig()
	pics, err := listProfilePics()
	if err != nil {
		return err
	}
	pic := picForCurrentAccount(pics)
	abs, err := filepath.Abs(pic)
	if err != nil {
		return err
	}

	auth, err := loadAuth()
	if err != nil {
		return err
	}
	fmt.Println("Logging in, then applying profile + AgeKey…")
	ageKeyHaveClicked = false
	cmd, err := startChromeLoggedIn(&auth)
	if err != nil {
		return err
	}
	defer func() {
		stopChrome(cmd)
		time.Sleep(2 * time.Second)
	}()

	name, err := syncAuthIdentity(newHumanClient(), &auth)
	if err != nil || !usableBlogName(name) {
		maybeFillBlog(newHumanClient(), &auth)
		name = resolveSetupBlogName(auth, name)
	}
	if !usableBlogName(name) {
		return fmt.Errorf("could not detect blog name")
	}
	setupBlogName = name
	fmt.Println("Blog:", name)
	fmt.Println("Avatar:", filepath.ToSlash(pic))
	fmt.Println("Title:", cfg.Title)
	fmt.Println("Language: English")

	if err := applyBirthdayAndAgeKeyInChrome(cfg); err != nil {
		fmt.Println("Birthday/AgeKey:", err)
		fmt.Println("In Chrome, open Settings → set birthday 5 Oct 1997 and pick AgeKey.")
	}

	if err := applyBlogLookInChrome(abs, name, cfg.Title, cfg.Description); err != nil {
		fmt.Println("Blog look:", err)
	}

	if err := applyAgeKeyInChrome(); err != nil {
		fmt.Println("AgeKey:", err)
	}

	if err := applyMatureContentInChrome(); err != nil {
		fmt.Println("Mature content:", err)
	}

	fmt.Println()
	fmt.Println("Waiting until name, bio, avatar, birthday, cookies, and AgeKey are all set.")
	fmt.Println("Then the next account starts on its own. Type the AgeKey passcode when Windows Security appears.")
	fmt.Println("Enter still skips to the next account.")
	fmt.Println()
	waitUntilSetupReady()

	if err := clickAgeConfirmOnPort(liveDebugPort); err != nil {
		fmt.Println("Age confirm:", err)
	}

	if err := applyMatureContentInChrome(); err != nil {
		fmt.Println("Mature content:", err)
	}

	if cookies, err := cookiesFromDebugPort(); err == nil && cookieValue(cookies, "sid") != "" {
		auth.Cookie = buildTumblrCookieHeader(cookies)
		persistMaturePref(&auth)
		fmt.Println("Saved session cookies.")
	}
	return nil
}

const chromePageDumpJS = `(() => {
	const vis = (el) => {
		if (!el) return false;
		const s = getComputedStyle(el);
		const r = el.getBoundingClientRect();
		return s.display !== 'none' && s.visibility !== 'hidden' && r.width > 1 && r.height > 1;
	};
	const textOf = (el) => (el.innerText || el.getAttribute('aria-label') || el.getAttribute('placeholder') || '').replace(/\s+/g, ' ').trim().slice(0, 80);
	const uniq = (arr) => [...new Set(arr.filter(Boolean))];
	return JSON.stringify({
		href: location.href,
		headings: uniq([...document.querySelectorAll('h1,h2,h3')].filter(vis).map(textOf)).slice(0, 12),
		buttons: uniq([...document.querySelectorAll('button, a, [role="button"]')].filter(vis).map(textOf).filter(t => t.length > 0 && t.length < 48)).slice(0, 50),
		labels: uniq([...document.querySelectorAll('label')].filter(vis).map(textOf)).slice(0, 20),
		inputs: [...document.querySelectorAll('input, textarea, select, [contenteditable="true"]')].slice(0, 40).map(el => ({
			tag: el.tagName,
			type: el.type || '',
			name: el.name || '',
			aria: el.getAttribute('aria-label') || '',
			role: el.getAttribute('role') || '',
			vis: vis(el)
		})),
		iframes: [...document.querySelectorAll('iframe')].map(f => {
			const s = f.src || '';
			try { const u = new URL(s, location.href); return u.origin + u.pathname; } catch (e) { return 'iframe'; }
		}),
		body: (document.body.innerText || '').replace(/\s+/g, ' ').trim().slice(0, 1200)
	});
})()`

const chromeDismissOverlaysJS = `(() => {
	const vis = (el) => {
		if (!el || !el.getBoundingClientRect) return false;
		const r = el.getBoundingClientRect();
		const s = getComputedStyle(el);
		return r.width > 2 && r.height > 2 && s.visibility !== 'hidden' && s.display !== 'none';
	};
	const textOf = (el) => (el.innerText || el.value || el.getAttribute('aria-label') || '').replace(/\s+/g, ' ').trim();
	const cookieish = (t) => /cookie|cookies|consentimiento|consent|privacy|privacidad|gdpr|we use cookies|we value your privacy|this (site|website) uses|accept all|i agree|qc-cmp|onetrust|didomi/i.test(t);
	const cookieBtn = /^(i agree!?|agree|i accept|acepto!?|aceptar( todas( las cookies)?)?|accetto|j'accepte|согласен!?|accept( all( cookies)?)?|allow all( cookies)?|permitir todo(s)?|got it|continue|continuar|confirm(ar)?|ok|okay)$/i;
	const cookieSkip = /manage|settings|reject|decline|necessary|customize|customise|learn more|more info|cookie settings|do not sell/i;
	const okBtn = /^(ok|okay|got it|dismiss)$/i;
	const leakish = (t) => /data breach|password manager|change your password|compromised password|found in a data|check your passwords|check passwords|leaked password|unsafe password|password was found|passwords were found|password leak|google found/i.test(t);
	const buttons = [];
	const visit = (root) => {
		if (!root || !root.querySelectorAll) return;
		for (const el of root.querySelectorAll('button, a, [role="button"], input[type=button], input[type=submit]')) {
			if (vis(el)) buttons.push(el);
		}
		for (const el of root.querySelectorAll('*')) {
			if (el.shadowRoot) visit(el.shadowRoot);
		}
	};
	visit(document);
	for (const f of document.querySelectorAll('iframe')) {
		try { if (f.contentDocument) visit(f.contentDocument); } catch (e) {}
	}
	const ctxOf = (el) => {
		let n = el, t = '';
		for (let i = 0; i < 8 && n; i++, n = n.parentElement) t += ' ' + textOf(n).slice(0, 240);
		return t;
	};
	const savePwBtn = /^(never|no,? thanks!?|not now)$/i;
	const saveish = (t) => /save password|google password manager|passwords are saved/i.test(t);
	for (const b of buttons) {
		const t = textOf(b);
		if (!savePwBtn.test(t) || t.length > 24) continue;
		if (saveish(ctxOf(b))) { b.click(); return t; }
	}
	const leakBtn = /^(ok|okay|got it|dismiss|close|not now|never)$/i;
	for (const b of buttons) {
		const t = textOf(b);
		if (!leakBtn.test(t) || t.length > 16) continue;
		if (leakish(ctxOf(b))) { b.click(); return t; }
	}
	for (const el of document.querySelectorAll('[aria-label]')) {
		if (!vis(el)) continue;
		const t = (el.getAttribute('aria-label') || '').replace(/\s+/g, ' ').trim();
		if (!/^(close|dismiss|ok|okay|got it)$/i.test(t)) continue;
		if (leakish(ctxOf(el))) { el.click(); return t; }
	}
	for (const b of buttons) {
		const t = textOf(b);
		if (!t || t.length > 48 || cookieSkip.test(t) || !cookieBtn.test(t)) continue;
		const ctx = ctxOf(b) + ' ' + t;
		if (/agekey|verify your age|create my agekey|use your selfie|windows security/i.test(ctx)) continue;
		if (cookieish(ctx)) { b.click(); return t; }
	}
	for (const b of buttons) {
		const t = textOf(b);
		if (!okBtn.test(t)) continue;
		const dlg = b.closest('[role="dialog"], [aria-modal="true"]');
		if (!dlg) continue;
		const dt = textOf(dlg);
		if (/agekey|verify your age|create my agekey|use your selfie/i.test(dt)) continue;
		b.click();
		return t;
	}
	return '';
})()`

const chromeClickCookieJS = `(() => {
	const vis = (el) => {
		if (!el || !el.getBoundingClientRect) return false;
		const r = el.getBoundingClientRect();
		const s = getComputedStyle(el);
		return r.width > 2 && r.height > 2 && s.visibility !== 'hidden' && s.display !== 'none';
	};
	const textOf = (el) => (el.innerText || el.value || el.getAttribute('aria-label') || '').replace(/\s+/g, ' ').trim();
	const cookieish = (t) => /cookie|cookies|consentimiento|consent|privacy|privacidad|gdpr|we use cookies|we value your privacy|this (site|website) uses|accept all|i agree|qc-cmp|onetrust|didomi/i.test(t);
	const cookieBtn = /^(i agree!?|agree|i accept|acepto!?|aceptar( todas( las cookies)?)?|accetto|j'accepte|согласен!?|accept( all( cookies)?)?|allow all( cookies)?|permitir todo(s)?|got it|continue|continuar|confirm(ar)?|ok|okay)$/i;
	const cookieSkip = /manage|settings|reject|decline|necessary|customize|customise|learn more|more info|cookie settings|do not sell|agekey|selfie/i;
	const buttons = [];
	const visit = (root) => {
		if (!root || !root.querySelectorAll) return;
		for (const el of root.querySelectorAll('button, a, [role="button"], input[type=button], input[type=submit]')) {
			if (vis(el)) buttons.push(el);
		}
		for (const el of root.querySelectorAll('*')) {
			if (el.shadowRoot) visit(el.shadowRoot);
		}
	};
	visit(document);
	for (const f of document.querySelectorAll('iframe')) {
		try { if (f.contentDocument) visit(f.contentDocument); } catch (e) {}
	}
	const ctxOf = (el) => {
		let n = el, t = '';
		for (let i = 0; i < 8 && n; i++, n = n.parentElement) t += ' ' + textOf(n).slice(0, 240);
		return t;
	};
	const page = (document.body && document.body.innerText || '').replace(/\s+/g, ' ');
	for (const b of buttons) {
		const t = textOf(b);
		if (!t || t.length > 48 || cookieSkip.test(t) || !cookieBtn.test(t)) continue;
		const ctx = ctxOf(b) + ' ' + t + ' ' + page.slice(0, 400);
		if (/agekey|verify your age|create my agekey|use your selfie/i.test(ctx)) continue;
		if (cookieish(ctx) || cookieish(page.slice(0, 800))) {
			b.click();
			return t;
		}
	}
	return '';
})()`

const chromeSaveAppearanceJS = `(() => {
	const vis = (el) => el && el.getBoundingClientRect().width > 2 && el.getBoundingClientRect().height > 2;
	const textOf = (el) => (el.innerText || el.getAttribute('aria-label') || '').replace(/\s+/g, ' ').trim();
	const skip = /password|agekey|verify age|cookie|consent|never|not now/i;
	const labels = [];
	const clickRe = (re) => {
		const el = [...document.querySelectorAll('button, [role="button"]')].find(b => vis(b) && re.test(textOf(b)) && !skip.test(textOf(b)));
		if (!el) return;
		el.click();
		labels.push(textOf(el));
	};
	clickRe(/^(apply|use this|use photo|set avatar|done)$/i);
	clickRe(/^(save|save changes)$/i);
	return labels.join(' | ');
})()`

const chromeSetMatureTogglesJS = `(() => {
	const vis = (el) => el && el.getBoundingClientRect().width > 2 && el.getBoundingClientRect().height > 2;
	const isOn = (el) => {
		if (!el) return false;
		const aria = (el.getAttribute('aria-checked') || el.getAttribute('aria-pressed') || '').toLowerCase();
		if (aria === 'true') return true;
		if (aria === 'false') return false;
		if (el.type === 'checkbox') return !!el.checked;
		return el.className && /checked|selected|is-on/i.test(el.className);
	};
	const controlFor = (re) => {
		const sel = 'button[role="switch"], input[type=checkbox], [aria-checked], [role="checkbox"], [aria-pressed]';
		const labelled = [...document.querySelectorAll(sel)]
			.find(el => vis(el) && re.test(el.getAttribute('aria-label') || ''));
		if (labelled) return labelled;
		for (const lab of document.querySelectorAll('label, span, div, p, h2, h3, dt, td, strong')) {
			const t = (lab.innerText || '').replace(/\s+/g, ' ').trim();
			if (!re.test(t) || t.length > 48) continue;
			let n = lab;
			for (let i = 0; i < 6 && n; i++, n = n.parentElement) {
				const kids = [...n.querySelectorAll(sel)].filter(vis);
				if (kids.length === 1) return kids[0];
				const lr = lab.getBoundingClientRect();
				const right = kids.find(el => {
					const r = el.getBoundingClientRect();
					return r.left >= lr.right - 12 && Math.abs((r.top + r.bottom) / 2 - (lr.top + lr.bottom) / 2) < 48;
				});
				if (right) return right;
			}
		}
		return null;
	};
	const setWant = (el, wantOn, name) => {
		if (!el) return name + ':missing';
		el.scrollIntoView({block:'center', inline:'nearest'});
		if (isOn(el) === wantOn) return name + ':already';
		el.click();
		return name + ':' + (wantOn ? 'on' : 'off');
	};
	const show = setWant(controlFor(/show mature content/i) || controlFor(/^show mature/i) || controlFor(/show nsfw/i), true, 'show');
	const blur = setWant(controlFor(/blur mature content/i) || controlFor(/^blur mature/i) || controlFor(/blur nsfw/i), false, 'blur');
	let extra = '';
	if (/missing/.test(show + ' ' + blur)) {
		extra = ' dump:' + JSON.stringify([...document.querySelectorAll('button[role="switch"], input[type=checkbox], [aria-checked], [role="checkbox"]')]
			.filter(vis)
			.map(el => ((el.getAttribute('aria-label') || el.innerText || '').replace(/\\s+/g, ' ').trim().slice(0, 48) + '=' + isOn(el)))
			.slice(0, 12));
	} else {
		const save = [...document.querySelectorAll('button')]
			.find(b => vis(b) && /^save$/i.test((b.innerText || '').trim()) && !b.disabled && b.getAttribute('aria-disabled') !== 'true');
		if (save) {
			save.click();
			extra = ' saved';
		}
	}
	return show + ' ' + blur + extra;
})()`

const chromeClickAgeConfirmJS = `(() => {
	const vis = (el) => {
		if (!el || !el.getBoundingClientRect) return false;
		const r = el.getBoundingClientRect();
		const s = getComputedStyle(el);
		return r.width > 2 && r.height > 2 && s.visibility !== 'hidden' && s.display !== 'none';
	};
	const textOf = (el) => (el.innerText || el.value || el.getAttribute('aria-label') || '').replace(/\s+/g, ' ').trim();
	const btnRe = /^(ok|okay|okey|accept|got it|continue|done|confirm|i understand|allow|yes)$/i;
	const dialogs = [...document.querySelectorAll('[role="dialog"], [aria-modal="true"]')].filter(vis);
	const roots = dialogs.length ? dialogs : [document];
	for (const root of roots) {
		const t = textOf(root);
		if (root !== document && !/birthday|age|agekey|verified|confirm/i.test(t)) continue;
		const btns = [...root.querySelectorAll('button, a, [role="button"], input[type=button], input[type=submit]')]
			.filter(vis)
			.map(el => ({el, t: textOf(el)}))
			.filter(x => x.t && x.t.length < 28 && btnRe.test(x.t) && !/verify age|create my agekey|delete/i.test(x.t));
		if (!btns[0]) continue;
		btns.sort((a,b) => {
			const score = (s) => {
				if (/^(okay|ok|accept|confirm)$/i.test(s)) return 0;
				if (/continue|done|got it/i.test(s)) return 1;
				return 2;
			};
			return score(a.t) - score(b.t) || a.t.length - b.t.length;
		});
		const el = btns[0].el;
		el.scrollIntoView({block:'center', inline:'nearest'});
		const r = el.getBoundingClientRect();
		el.click();
		return {t: btns[0].t, x: r.left + r.width/2, y: r.top + r.height/2};
	}
	return {t:'', x:0, y:0};
})()`

const chromeAgeStatusJS = `(() => {
	const vis = (el) => {
		if (!el) return false;
		const r = el.getBoundingClientRect();
		return r.width > 2 && r.height > 2;
	};
	const textOf = (el) => (el.innerText || el.getAttribute('aria-label') || '').replace(/\s+/g, ' ').trim();
	const b = (document.body && document.body.innerText || '').replace(/\s+/g, ' ');
	const buttons = [...document.querySelectorAll('button, a, [role="button"]')]
		.filter(vis)
		.map(textOf)
		.filter(t => t && t.length < 36);
	const save = [...document.querySelectorAll('button')].find(b => vis(b) && /^(save|сохранить)$/i.test((b.innerText || '').trim()));
	const m = b.match(/Birthday.{0,140}/i);
	const bdaySelects = [...document.querySelectorAll('select')].filter(vis).filter(el => /month|day|year|месяц|день|год/i.test((el.getAttribute('aria-label') || '') + ' ' + (el.name || '')));
	const verifyBtn = [...document.querySelectorAll('button, a, [role="button"]')].some(el => vis(el) && /verify (your )?age|подтвердить возраст/i.test(textOf(el)));
	const wanted = /October 5, 1997|5 October 1997|5\s+Oct(?:ober)?\s+1997/i.test(b);
	let birthday = 'unset';
	if (wanted) birthday = 'set';
	else if (bdaySelects.length === 0 && /\/settings\/account/.test(location.pathname)) birthday = 'locked';
	const cookieish = /cookie|cookies|consentimiento|consent|privacy|privacidad|gdpr|we use cookies|we value your privacy|this (site|website) uses/i;
	const cookieBtnRe = /^(i agree!?|agree|i accept|acepto!?|aceptar( todas( las cookies)?)?|accetto|j'accepte|согласен!?|accept( all( cookies)?)?|allow all( cookies)?|permitir todo(s)?)$/i;
	const cookieBanner = [...document.querySelectorAll('iframe')].some(f => vis(f) && /consent|quantcast|onetrust|didomi|sourcepoint|privacy-mgmt|privacymanager|cookiebot|cmp\.|gdpr|\/privacy/i.test(f.src || f.id || f.className || ''))
		|| !!document.querySelector('#onetrust-banner-sdk, #onetrust-consent-sdk, .qc-cmp2-container, .qc-cmp-ui, [id*="sp_message"], [class*="cookie-banner"], [id*="cookie-banner"]')
		|| (cookieish.test(b) && buttons.some(t => cookieBtnRe.test(t)));
	const cookiesOK = !cookieBanner && /(?:^|; )(euconsent|addtl_consent)/i.test(document.cookie);
	return JSON.stringify({
		href: location.pathname,
		birthday: birthday,
		verified: /Age Group.{0,80}Verified/i.test(b) && !verifyBtn,
		verifyAge: verifyBtn,
		ageKeyFrame: [...document.querySelectorAll('iframe')].some(f => /k-id|agekey/i.test(f.src || '') || f.getBoundingClientRect().height > 280),
		birthdayConfirm: /confirming that your birthday|you can only confirm your birthday|only confirm your birthday/i.test(b),
		leak: /change your password|check your passwords|data breach|compromised password|found in a data|password leak|leaked password/i.test(b),
		cookieBanner: cookieBanner,
		cookiesOK: cookiesOK,
		save: save ? ('yes:' + save.disabled + ':' + (save.getAttribute('aria-disabled') || '')) : 'none',
		snippet: m ? m[0] : '',
		buttons: [...new Set(buttons)].slice(0, 20)
	});
})()`

const chromeClickVerifyAgeJS = `(() => {
	const vis = (el) => el && el.getBoundingClientRect().width > 2 && el.getBoundingClientRect().height > 2;
	const el = [...document.querySelectorAll('button, a, [role="button"]')].find(b => vis(b) && /verify (your )?age|подтвердить возраст/i.test((b.innerText || b.getAttribute('aria-label') || '').replace(/\s+/g, ' ').trim()));
	if (!el) return '';
	el.click();
	return (el.innerText || '').replace(/\s+/g, ' ').trim();
})()`

const chromeClickAgeKeyJS = `(() => {
	const vis = (el) => el && el.getBoundingClientRect().width > 2 && el.getBoundingClientRect().height > 2;
	const tryRoot = (root) => {
		const cands = [...root.querySelectorAll('button, a, [role="button"], [tabindex], div, span, li')]
			.filter(vis)
			.map(el => ({el, t: (el.innerText || el.getAttribute('aria-label') || '').replace(/\s+/g, ' ').trim()}))
			.filter(x => x.t && /age\s*key/i.test(x.t) && !/selfie|photo id|use your id/i.test(x.t));
		cands.sort((a,b) => a.t.length - b.t.length);
		if (!cands[0]) return '';
		cands[0].el.scrollIntoView({block:'center', inline:'nearest'});
		cands[0].el.click();
		return cands[0].t.slice(0, 80);
	};
	let t = tryRoot(document);
	if (t) return t;
	for (const f of document.querySelectorAll('iframe')) {
		try {
			if (f.contentDocument) {
				t = tryRoot(f.contentDocument);
				if (t) return t;
			}
		} catch (e) {}
	}
	return '';
})()`

const chromeSetEnglishJS = `(() => {
	const setNative = (el, value) => {
		if (!el) return false;
		el.focus();
		const proto = window.HTMLSelectElement.prototype;
		const desc = Object.getOwnPropertyDescriptor(proto, 'value') || Object.getOwnPropertyDescriptor(window.HTMLInputElement.prototype, 'value');
		desc.set.call(el, value);
		el.dispatchEvent(new Event('input', { bubbles: true }));
		el.dispatchEvent(new Event('change', { bubbles: true }));
		return true;
	};
	const isLangSelect = (sel) => {
		const blob = [...sel.options].map(o => (o.value + ' ' + o.text).toLowerCase()).join(' | ');
		return /english|\ben\b|en_us|en-us/.test(blob) && /deutsch|français|francais|español|espanol|italiano|日本語/.test(blob);
	};
	const englishOpt = (sel) => [...sel.options].find(o => {
		const v = (o.value || '').toLowerCase();
		const t = (o.text || '').replace(/\s+/g, ' ').trim().toLowerCase();
		return t === 'english' || v === 'en' || v === 'en_us' || v === 'en-us' || v.startsWith('en_') || v.startsWith('en-');
	});
	let hits = [];
	for (const sel of document.querySelectorAll('select')) {
		if (!isLangSelect(sel)) continue;
		const opt = englishOpt(sel);
		if (!opt) continue;
		if (sel.value === opt.value) {
			hits.push('already:' + opt.value);
			continue;
		}
		setNative(sel, opt.value);
		hits.push(opt.value || opt.text);
	}
	return hits.join(',') || 'no-lang-select';
})()`

func chromeClickTextJS(pattern string, maxLen int) string {
	if maxLen <= 0 {
		maxLen = 48
	}
	return fmt.Sprintf(`(() => {
		const re = %s;
		const maxLen = %d;
		const vis = (el) => {
			if (!el) return false;
			const r = el.getBoundingClientRect();
			return r.width > 2 && r.height > 2;
		};
		const cands = [...document.querySelectorAll('button, a, [role="button"], [tabindex], label, span, div')]
			.filter(vis)
			.map(el => ({el, t: (el.innerText || el.getAttribute('aria-label') || '').replace(/\s+/g, ' ').trim()}))
			.filter(x => x.t && x.t.length <= maxLen && re.test(x.t));
		cands.sort((a,b) => a.t.length - b.t.length);
		if (!cands[0]) return '';
		cands[0].el.scrollIntoView({block:'center', inline:'nearest'});
		cands[0].el.click();
		return cands[0].t;
	})()`, pattern, maxLen)
}

func dumpChromeControls(ctx context.Context, label string) {
	var out string
	if err := chromedp.Evaluate(chromePageDumpJS, &out).Do(ctx); err != nil {
		fmt.Println("---", label, "dump failed:", err)
		return
	}
	fmt.Println("---", label, "---")
	fmt.Println(out)
	saveChromeShot(ctx, "setup-"+strings.ReplaceAll(strings.ToLower(label), " ", "-")+".png")
}

func saveChromeShot(ctx context.Context, name string) {
	var buf []byte
	if err := chromedp.FullScreenshot(&buf, 80).Do(ctx); err != nil {
		return
	}
	_ = os.MkdirAll("scraped", 0755)
	path := filepath.Join("scraped", name)
	if err := os.WriteFile(path, buf, 0644); err != nil {
		return
	}
	fmt.Println("Screenshot:", path)
}

func xpathQuote(s string) string {
	if !strings.Contains(s, `'`) {
		return `'` + s + `'`
	}
	return `"` + s + `"`
}

func chromeClickLabel(ctx context.Context, label string) error {
	return chromeClickLabelWait(ctx, label, 2*time.Second)
}

func chromeClickLabelWait(ctx context.Context, label string, wait time.Duration) error {
	xp := `//*[normalize-space()=` + xpathQuote(label) + `]`
	runCtx, cancel := context.WithTimeout(ctx, wait)
	defer cancel()
	return chromedp.Run(runCtx, chromedp.Click(xp, chromedp.BySearch, chromedp.NodeVisible))
}

func dismissChromeNags(ctx context.Context) {
	if ageKeyHandsOff() {
		return
	}
	for i := 0; i < 8; i++ {
		if nag := dismissChromeLeakDialog(); nagWasDismissed(nag) {
			fmt.Println("Dismissed:", nag)
			time.Sleep(200 * time.Millisecond)
			continue
		}
		var overlay string
		_ = chromedp.Evaluate(chromeDismissOverlaysJS, &overlay).Do(ctx)
		if overlay == "" {
			_ = chromedp.Evaluate(chromeClickCookieJS, &overlay).Do(ctx)
		}
		if overlay != "" {
			fmt.Println("Dismissed:", overlay)
			time.Sleep(350 * time.Millisecond)
			continue
		}
		if hit := clickCookieAcceptOnConsentTargets(); hit != "" {
			fmt.Println("Dismissed cookies:", hit)
			time.Sleep(400 * time.Millisecond)
			continue
		}
		if nag := dismissNativePasswordNag(); nagWasDismissed(nag) {
			fmt.Println("Dismissed:", nag)
			time.Sleep(250 * time.Millisecond)
			continue
		}
		if cookieBannerOpen() {
			time.Sleep(300 * time.Millisecond)
			continue
		}
		return
	}
}

func dismissOverlaysOnPort() {
	if ageKeyHandsOff() {
		return
	}
	for i := 0; i < 8; i++ {
		if nag := dismissChromeLeakDialog(); nagWasDismissed(nag) {
			fmt.Println("Dismissed:", nag)
			time.Sleep(200 * time.Millisecond)
			continue
		}
		var overlay string
		if err := evalOnTumblrPage(chromeDismissOverlaysJS, &overlay); err == nil && overlay != "" {
			fmt.Println("Dismissed:", overlay)
			time.Sleep(350 * time.Millisecond)
			continue
		}
		if err := evalOnTumblrPage(chromeClickCookieJS, &overlay); err == nil && overlay != "" {
			fmt.Println("Dismissed cookies:", overlay)
			time.Sleep(350 * time.Millisecond)
			continue
		}
		if hit := clickCookieAcceptOnConsentTargets(); hit != "" {
			fmt.Println("Dismissed cookies:", hit)
			time.Sleep(400 * time.Millisecond)
			continue
		}
		if nag := dismissNativePasswordNag(); nagWasDismissed(nag) {
			fmt.Println("Dismissed:", nag)
			time.Sleep(250 * time.Millisecond)
			continue
		}
		if cookieBannerOpen() {
			time.Sleep(300 * time.Millisecond)
			continue
		}
		return
	}
}

func clickUseAgeKey(ctx context.Context) string {
	if hit := clickAgeKeyIframeCoords(ctx); hit != "" {
		return hit
	}
	if hit := clickUseAgeKeyOnKidTarget(); hit != "" {
		return hit
	}
	for _, label := range []string{"Use your AgeKey", "AgeKey"} {
		if chromeClickLabelWait(ctx, label, 800*time.Millisecond) == nil {
			return label
		}
	}
	var hit string
	_ = chromedp.Evaluate(chromeClickAgeKeyJS, &hit).Do(ctx)
	return hit
}

func clickAgeKeyIframeCoords(ctx context.Context) string {
	var box struct {
		X float64 `json:"x"`
		Y float64 `json:"y"`
		W float64 `json:"w"`
		H float64 `json:"h"`
	}
	js := `(() => {
		const frames = [...document.querySelectorAll('iframe')].map(el => {
			const r = el.getBoundingClientRect();
			return {el, src: el.src || '', x: r.left, y: r.top, w: r.width, h: r.height};
		}).filter(f => f.w > 240 && f.h > 280);
		const kid = frames.find(f => /k-id|agekey/i.test(f.src)) || [...frames].sort((a,b) => b.h - a.h)[0];
		if (!kid) return {x:0,y:0,w:0,h:0};
		kid.el.scrollIntoView({block:'center', inline:'nearest'});
		const r = kid.el.getBoundingClientRect();
		return {x: r.left, y: r.top, w: r.width, h: r.height};
	})()`
	if err := chromedp.Evaluate(js, &box).Do(ctx); err != nil || box.W < 200 || box.H < 280 {
		return ""
	}
	x := box.X + box.W*0.5
	// Three stacked methods; AgeKey is the bottom card, above the lock.
	for _, frac := range []float64{0.70, 0.76} {
		y := box.Y + box.H*frac
		if err := chromedp.MouseClickXY(x, y).Do(ctx); err != nil {
			continue
		}
		time.Sleep(600 * time.Millisecond)
	}
	fmt.Println("Clicked AgeKey card in k-ID window")
	return "Use your AgeKey"
}

func clickAlreadyHaveAgeKey(ctx context.Context) string {
	if hit := clickHaveAgeKeyOnKidTarget(); hit != "" {
		return hit
	}
	if chromeHasTargetHost("agekey.org") || chromeHasTargetHost("agekey.com") {
		return clickHaveAgeKeyIframeCoords(ctx)
	}
	return ""
}

func clickHaveAgeKeyIframeCoords(ctx context.Context) string {
	var box struct {
		X float64 `json:"x"`
		Y float64 `json:"y"`
		W float64 `json:"w"`
		H float64 `json:"h"`
	}
	js := `(() => {
		const frames = [...document.querySelectorAll('iframe')].map(el => {
			const r = el.getBoundingClientRect();
			return {el, src: el.src || '', x: r.left, y: r.top, w: r.width, h: r.height};
		}).filter(f => f.w > 240 && f.h > 280);
		const kid = frames.find(f => /agekey/i.test(f.src)) || frames.find(f => /k-id/i.test(f.src)) || [...frames].sort((a,b) => b.h - a.h)[0];
		if (!kid) return {x:0,y:0,w:0,h:0};
		kid.el.scrollIntoView({block:'center', inline:'nearest'});
		const r = kid.el.getBoundingClientRect();
		return {x: r.left, y: r.top, w: r.width, h: r.height};
	})()`
	if err := chromedp.Run(ctx, chromedp.Evaluate(js, &box)); err != nil || box.W < 200 || box.H < 280 {
		return ""
	}
	x := box.X + box.W*0.5
	// Link sits under the blue Create button. Stay below ~0.84 so we do not hit Create.
	for _, frac := range []float64{0.90, 0.93} {
		y := box.Y + box.H*frac
		_ = chromedp.Run(ctx, chromedp.MouseClickXY(x, y))
		time.Sleep(400 * time.Millisecond)
	}
	return "Already have an AgeKey?"
}

func submitAgeKeyPasskeyPIN(port, pin string) error {
	port = strings.TrimSpace(port)
	if port == "" {
		port = "9343"
	}
	liveDebugPort = port
	if hwnd := findWindowContaining("Windows Security"); hwnd != 0 {
		fmt.Println("Passkey dialog already open.")
		return typeIntoPasskeyDialogWait(pin, 500*time.Millisecond)
	}
	if chrome := findWindowContaining("Tumblr"); chrome != 0 {
		fmt.Println("Focusing Chrome so the passkey prompt can return.")
		focusWindow(chrome)
		time.Sleep(1200 * time.Millisecond)
	}
	if hwnd := findWindowContaining("Windows Security"); hwnd != 0 {
		fmt.Println("Passkey dialog already open.")
		return typeIntoPasskeyDialogWait(pin, 2*time.Second)
	}
	if chromeHasTargetHost("agekey.org") || chromeHasTargetHost("agekey.com") {
		if hit := clickHaveAgeKeyOnKidTarget(); hit != "" {
			fmt.Println("Clicked:", hit)
			time.Sleep(1500 * time.Millisecond)
		}
	}
	if err := typeIntoPasskeyDialogWait(pin, 8*time.Second); err == nil {
		return nil
	}
	return fmt.Errorf("Windows Security PIN box is still on screen — type it by hand and press Enter")
}

func clickHaveAgeKeyOnPort(port string) error {
	port = strings.TrimSpace(port)
	if port == "" {
		port = "9343"
	}
	liveDebugPort = port
	if !chromeHasTargetHost("agekey.org") && !chromeHasTargetHost("agekey.com") {
		if !chromeHasTargetHost("k-id.com") {
			dismissOverlaysOnPort()
			var verify string
			verifyJS := `(() => {
				const vis = (el) => el && el.getBoundingClientRect().width > 2 && el.getBoundingClientRect().height > 2;
				const el = [...document.querySelectorAll('button, a, [role="button"]')]
					.find(b => vis(b) && /verify age|verify your age|подтвердить возраст/i.test((b.innerText || b.getAttribute('aria-label') || '').replace(/\s+/g,' ').trim()) && (b.innerText || '').trim().length < 40);
				if (!el) return '';
				el.click();
				return (el.innerText || '').trim();
			})()`
			if err := evalOnTumblrPage(verifyJS, &verify); err == nil && verify != "" {
				fmt.Println("Clicked:", verify)
				time.Sleep(1500 * time.Millisecond)
			}
			if !waitForChromeTargetHost("k-id.com", 10*time.Second) && !chromeHasTargetHost("agekey") {
				return fmt.Errorf("age verify window did not open")
			}
			time.Sleep(800 * time.Millisecond)
		}
		hit := clickUseAgeKeyOnKidTarget()
		if hit == "" {
			hit = clickAgeKeyCardOnTumblrPage()
		}
		if hit == "" {
			return fmt.Errorf("could not click Use your AgeKey")
		}
		fmt.Println("Clicked AgeKey:", hit)
		fmt.Println("Waiting for AgeKey page…")
		if !waitForChromeTargetHost("agekey.org", 18*time.Second) && !chromeHasTargetHost("agekey.com") {
			return fmt.Errorf("AgeKey page did not open")
		}
		time.Sleep(1500 * time.Millisecond)
	}
	for i := 0; i < 8; i++ {
		if hit := clickHaveAgeKeyOnKidTarget(); hit != "" {
			fmt.Println("Clicked:", hit)
			return nil
		}
		time.Sleep(700 * time.Millisecond)
	}
	return fmt.Errorf("could not click Already have an AgeKey?")
}

func clickVerifyAge(ctx context.Context) string {
	for _, label := range []string{"Подтвердить возраст", "Verify Age", "Verify your age", "Verify age"} {
		if chromeClickLabelWait(ctx, label, 1500*time.Millisecond) == nil {
			return label
		}
	}
	var hit string
	_ = chromedp.Evaluate(chromeClickTextJS(`/подтвердить возраст|verify (your )?age|age verification/i`, 40), &hit).Do(ctx)
	return hit
}

func confirmBirthdayDialog(ctx context.Context) bool {
	var hit struct {
		T string  `json:"t"`
		X float64 `json:"x"`
		Y float64 `json:"y"`
	}
	_ = chromedp.Evaluate(chromeClickAgeConfirmJS, &hit).Do(ctx)
	if hit.T != "" {
		fmt.Println("Clicked:", hit.T)
		time.Sleep(1200 * time.Millisecond)
		return true
	}
	body := strings.ToLower(chromePageText(ctx))
	if !strings.Contains(body, "confirming that your birthday") &&
		!strings.Contains(body, "only confirm your birthday") &&
		!strings.Contains(body, "you can only confirm") {
		return false
	}
	for _, label := range []string{"Okay", "OK", "Accept", "Confirm", "Continue", "Got it", "Подтвердить"} {
		if chromeClickLabelWait(ctx, label, 2*time.Second) == nil {
			fmt.Println("Clicked:", label)
			time.Sleep(1200 * time.Millisecond)
			return true
		}
	}
	return false
}

func clickEditAppearance(ctx context.Context) error {
	for _, label := range []string{"Edit appearance", "Изменить оформление"} {
		if chromeClickLabelWait(ctx, label, 4*time.Second) == nil {
			fmt.Println("Clicked:", label)
			return nil
		}
	}
	return fmt.Errorf("edit appearance button not found")
}

func chromePageText(ctx context.Context) string {
	var body string
	_ = chromedp.Evaluate(`(document.body && document.body.innerText) || ''`, &body).Do(ctx)
	return body
}

func setEnglishLanguage(ctx context.Context) {
	var out string
	if err := chromedp.Evaluate(chromeSetEnglishJS, &out).Do(ctx); err != nil {
		fmt.Println("Language:", err)
		return
	}
	fmt.Println("Language:", out)
}

func reloadAccountSettings(ctx context.Context) error {
	if err := chromedp.Navigate("https://www.tumblr.com/settings/account").Do(ctx); err != nil {
		return err
	}
	if err := chromedp.WaitReady("body", chromedp.ByQuery).Do(ctx); err != nil {
		return err
	}
	time.Sleep(2 * time.Second)
	dismissChromeNags(ctx)
	return nil
}

func applyBlogLookInChrome(imagePath, blogName, title, desc string) error {
	if look := lookStatusFromAPI(blogName); look.Complete() {
		fmt.Println("Name, bio, and avatar already set.")
		return nil
	}

	ctx, cancel, err := withBotChrome(120 * time.Second)
	if err != nil {
		return err
	}
	defer cancel()

	settings := "https://www.tumblr.com/settings/blog/" + blogName
	titleJSON, _ := json.Marshal(title)
	descJSON, _ := json.Marshal(desc)
	fillJS := fmt.Sprintf(`(() => {
		const setNative = (el, value) => {
			if (!el) return false;
			el.focus();
			if (el.isContentEditable) {
				document.execCommand('selectAll', false, null);
				document.execCommand('insertText', false, value);
				el.dispatchEvent(new Event('input', { bubbles: true }));
				return true;
			}
			const proto = el.tagName === 'TEXTAREA' ? window.HTMLTextAreaElement.prototype : window.HTMLInputElement.prototype;
			const desc = Object.getOwnPropertyDescriptor(proto, 'value') || Object.getOwnPropertyDescriptor(window.HTMLInputElement.prototype, 'value');
			desc.set.call(el, value);
			el.dispatchEvent(new Event('input', { bubbles: true }));
			el.dispatchEvent(new Event('change', { bubbles: true }));
			return true;
		};
		const vis = (el) => {
			if (!el) return false;
			const r = el.getBoundingClientRect();
			return r.width > 2 && r.height > 2;
		};
		const byLabel = (re) => {
			for (const lab of document.querySelectorAll('label')) {
				if (!re.test((lab.innerText || '').replace(/\s+/g, ' ').trim())) continue;
				const id = lab.getAttribute('for');
				if (id) {
					const el = document.getElementById(id);
					if (vis(el)) return el;
				}
				const el = lab.querySelector('input, textarea, [contenteditable="true"]') || (lab.parentElement && lab.parentElement.querySelector('input, textarea, [contenteditable="true"]'));
				if (vis(el)) return el;
			}
			return null;
		};
		const titleEl = byLabel(/^(title|name|display name)$/i)
			|| [...document.querySelectorAll('input, [contenteditable="true"]')].find(el => vis(el) && /title|name/i.test(el.getAttribute('aria-label') || el.name || el.getAttribute('placeholder') || ''));
		const descEl = byLabel(/description|bio/i)
			|| [...document.querySelectorAll('textarea, [contenteditable="true"]')].find(el => vis(el) && /desc|bio/i.test(el.getAttribute('aria-label') || el.name || el.getAttribute('placeholder') || ''))
			|| [...document.querySelectorAll('textarea')].find(vis);
		const okTitle = setNative(titleEl, %s);
		const okDesc = setNative(descEl, %s);
		return (okTitle ? 'title ' : '') + (okDesc ? 'bio ' : '');
	})()`, string(titleJSON), string(descJSON))

	var href, clicked, filled, avatarHit, saved string
	var nfiles int64
	err = chromedp.Run(ctx,
		chromedp.Navigate(settings),
		chromedp.WaitReady("body", chromedp.ByQuery),
		chromedp.Sleep(3*time.Second),
		chromedp.Location(&href),
		chromedp.ActionFunc(func(ctx context.Context) error {
			if strings.Contains(href, "/login") {
				return fmt.Errorf("still on login page")
			}
			if !strings.Contains(href, "/settings/blog/") {
				if err := chromedp.Navigate(settings).Do(ctx); err != nil {
					return err
				}
				_ = chromedp.WaitReady("body", chromedp.ByQuery).Do(ctx)
				time.Sleep(2 * time.Second)
				_ = chromedp.Location(&href).Do(ctx)
			}
			dismissChromeNags(ctx)
			dumpChromeControls(ctx, "blog settings")
			setEnglishLanguage(ctx)
			if err := clickEditAppearance(ctx); err != nil {
				return err
			}
			clicked = "Edit appearance"
			for i := 0; i < 8; i++ {
				time.Sleep(700 * time.Millisecond)
				t := chromePageText(ctx)
				if strings.Contains(t, "Accent") || strings.Contains(t, "Background") || strings.Contains(t, "Edit blog avatar") {
					return nil
				}
				var n int64
				_ = chromedp.Evaluate(`document.querySelectorAll('input[type=file]').length`, &n).Do(ctx)
				if n > 0 {
					return nil
				}
			}
			return nil
		}),
		chromedp.Location(&href),
		chromedp.ActionFunc(func(ctx context.Context) error {
			dumpChromeControls(ctx, "appearance")
			dismissChromeNags(ctx)
			if err := chromedp.Evaluate(fillJS, &filled).Do(ctx); err != nil {
				return err
			}
			if !strings.Contains(filled, "title") || !strings.Contains(filled, "bio") {
				dismissChromeNags(ctx)
				_ = chromedp.Evaluate(fillJS, &filled).Do(ctx)
			}
			return nil
		}),
		chromedp.Sleep(1500*time.Millisecond),
		chromedp.Evaluate(`document.querySelectorAll('input[type=file]').length`, &nfiles),
		chromedp.ActionFunc(func(ctx context.Context) error {
			cctx, cancel := context.WithTimeout(ctx, 5*time.Second)
			defer cancel()
			if err := chromedp.Click(`[aria-label="Edit blog avatar image"]`, chromedp.ByQuery, chromedp.NodeVisible).Do(cctx); err == nil {
				avatarHit = "Edit blog avatar image"
				time.Sleep(1500 * time.Millisecond)
			} else if chromeClickLabelWait(ctx, "Edit blog avatar image", 3*time.Second) == nil {
				avatarHit = "Edit blog avatar image"
				time.Sleep(1500 * time.Millisecond)
			}
			_ = chromedp.Evaluate(`document.querySelectorAll('input[type=file]').length`, &nfiles).Do(ctx)
			if nfiles == 0 {
				dumpChromeControls(ctx, "avatar picker")
				return fmt.Errorf("no file picker on %s", href)
			}
			fmt.Println("File inputs:", nfiles, "avatar click:", avatarHit)
			return setLastFileInput(ctx, imagePath)
		}),
		chromedp.Sleep(2*time.Second),
		chromedp.ActionFunc(func(ctx context.Context) error {
			saved = confirmAvatarAndSave(ctx)
			return nil
		}),
		chromedp.Sleep(1500*time.Millisecond),
	)
	if err != nil {
		return err
	}
	if clicked != "" {
		fmt.Println("Clicked:", clicked)
	}
	if filled != "" {
		fmt.Println("Filled:", filled)
	} else {
		fmt.Println("Filled: (no title/bio fields)")
	}
	if saved != "" {
		fmt.Println("Clicked:", saved)
	}
	time.Sleep(1200 * time.Millisecond)
	look := lookStatusFromAPI(blogName)
	switch {
	case look.Complete():
		fmt.Println("Confirmed name, bio, and avatar.")
	case look.OK() && !look.AvatarOK:
		fmt.Println("Name/bio set, avatar not saved yet.")
	case look.Title == "" && look.Bio == "":
		fmt.Println("Could not confirm name/bio over HTTP yet.")
	default:
		fmt.Printf("Name/bio not confirmed yet (title=%q bio=%q avatar=%v)\n", look.Title, truncate(look.Bio, 80), look.AvatarOK)
	}
	return nil
}

func confirmAvatarAndSave(ctx context.Context) string {
	var all []string
	for i := 0; i < 5; i++ {
		var hit string
		_ = chromedp.Evaluate(chromeSaveAppearanceJS, &hit).Do(ctx)
		if hit != "" {
			all = append(all, hit)
		}
		time.Sleep(800 * time.Millisecond)
	}
	return strings.Join(all, " | ")
}

func setLastFileInput(ctx context.Context, imagePath string) error {
	var n int64
	if err := chromedp.Evaluate(`document.querySelectorAll('input[type=file]').length`, &n).Do(ctx); err != nil {
		return err
	}
	if n == 0 {
		return fmt.Errorf("no file input")
	}
	// Always send to the last file input — first one is often the header image.
	sel := fmt.Sprintf(`(//input[@type="file"])[last()]`)
	if err := chromedp.SetUploadFiles(sel, []string{imagePath}, chromedp.BySearch).Do(ctx); err != nil {
		return chromedp.SetUploadFiles(`input[type=file]`, []string{imagePath}, chromedp.ByQuery).Do(ctx)
	}
	return nil
}

func applyBirthdayAndAgeKeyInChrome(cfg profileSetupConfig) error {
	ctx, cancel, err := withBotChrome(120 * time.Second)
	if err != nil {
		return err
	}
	defer cancel()

	var href, bday string
	err = chromedp.Run(ctx,
		chromedp.Navigate("https://www.tumblr.com/settings/account"),
		chromedp.WaitReady("body", chromedp.ByQuery),
		chromedp.Sleep(3*time.Second),
		chromedp.Location(&href),
		chromedp.ActionFunc(func(ctx context.Context) error {
			if strings.Contains(href, "/login") {
				return fmt.Errorf("still on login page")
			}
			dismissChromeNags(ctx)
			setEnglishLanguage(ctx)
			if err := reloadAccountSettings(ctx); err != nil {
				return err
			}
			dumpChromeControls(ctx, "account settings")
			body := chromePageText(ctx)
			if strings.Contains(body, "October 5, 1997") || strings.Contains(body, "5 October 1997") || strings.Contains(body, "октября 1997") {
				bday = "already-set"
				fmt.Println("Birthday already on file.")
			}
			return nil
		}),
		chromedp.ActionFunc(func(ctx context.Context) error {
			if bday == "already-set" {
				return nil
			}
			script := fmt.Sprintf(`(() => {
				const setNative = (el, value) => {
					if (!el) return false;
					el.focus();
					const proto = el.tagName === 'SELECT' ? window.HTMLSelectElement.prototype : window.HTMLInputElement.prototype;
					const desc = Object.getOwnPropertyDescriptor(proto, 'value') || Object.getOwnPropertyDescriptor(window.HTMLInputElement.prototype, 'value');
					desc.set.call(el, value);
					el.dispatchEvent(new Event('input', { bubbles: true }));
					el.dispatchEvent(new Event('change', { bubbles: true }));
					return true;
				};
				const y = String(%d), m = String(%d), d = String(%d);
				const byAria = (re) => [...document.querySelectorAll('select')].find(el => re.test(el.getAttribute('aria-label') || ''));
				const month = byAria(/month|месяц/i);
				const day = byAria(/^(day|день)$/i);
				const year = byAria(/year|год/i);
				let got = [];
				if (month) {
					const opt = [...month.options].find(o => /oct|окт/i.test(o.text) || o.value === m || o.value === m.padStart(2,'0'));
					if (opt) { setNative(month, opt.value); got.push('month'); }
				}
				if (day) {
					const opt = [...day.options].find(o => o.value === d || o.value === d.padStart(2,'0') || o.text.trim() === d);
					if (opt) { setNative(day, opt.value); got.push('day'); }
				}
				if (year) {
					const opt = [...year.options].find(o => o.value === y || o.text.trim() === y);
					if (opt) { setNative(year, opt.value); got.push('year'); }
				}
				return got.join(',') || 'no-fields';
			})()`, cfg.BirthdayYear, cfg.BirthdayMonth, cfg.BirthdayDay)
			return chromedp.Evaluate(script, &bday).Do(ctx)
		}),
		chromedp.Sleep(1200*time.Millisecond),
		chromedp.ActionFunc(func(ctx context.Context) error {
			if bday == "already-set" {
				return nil
			}
			var saved string
			_ = chromedp.Evaluate(`(() => {
				const vis = (el) => el && el.getBoundingClientRect().width > 2 && el.getBoundingClientRect().height > 2;
				const save = [...document.querySelectorAll('button')].find(b => vis(b) && /^(save|сохранить)$/i.test((b.innerText || '').trim()));
				if (!save) return '';
				save.click();
				return 'save';
			})()`, &saved).Do(ctx)
			if saved != "" {
				fmt.Println("Birthday:", saved)
			}
			return nil
		}),
		chromedp.Sleep(1500*time.Millisecond),
		chromedp.ActionFunc(func(ctx context.Context) error {
			if bday == "already-set" {
				return nil
			}
			for i := 0; i < 10; i++ {
				if confirmBirthdayDialog(ctx) {
					bday = bday + ",confirmed"
					time.Sleep(800 * time.Millisecond)
					_ = confirmBirthdayDialog(ctx)
					return nil
				}
				time.Sleep(500 * time.Millisecond)
			}
			return nil
		}),
		chromedp.Sleep(1500*time.Millisecond),
	)
	if err != nil {
		return err
	}
	if bday != "" {
		fmt.Println("Birthday:", bday)
	}
	return nil
}

func applyAgeKeyInChrome() error {
	ctx, cancel, err := withBotChrome(120 * time.Second)
	if err != nil {
		return err
	}
	defer cancel()

	var href, verify string
	err = chromedp.Run(ctx,
		chromedp.Navigate("https://www.tumblr.com/settings/account"),
		chromedp.WaitReady("body", chromedp.ByQuery),
		chromedp.Sleep(2*time.Second),
		chromedp.Location(&href),
		chromedp.ActionFunc(func(ctx context.Context) error {
			if strings.Contains(href, "/login") {
				return fmt.Errorf("still on login page")
			}
			dismissChromeNags(ctx)
			body := chromePageText(ctx)
			var st setupPageStatus
			var raw string
			if chromedp.Evaluate(chromeAgeStatusJS, &raw).Do(ctx) == nil {
				_ = json.Unmarshal([]byte(raw), &st)
			}
			if st.Verified && !st.VerifyAge {
				verify = "already-verified"
				fmt.Println("Age group already verified.")
				return nil
			}
			if strings.Contains(body, "Age Group") && strings.Contains(body, "Verified") && !st.VerifyAge && !strings.Contains(body, "Verify Age") && !strings.Contains(body, "Подтвердить возраст") {
				verify = "already-verified"
				fmt.Println("Age group already verified.")
				return nil
			}
			if hit := clickVerifyAge(ctx); hit != "" {
				fmt.Println("Clicked:", hit)
				verify = hit
			}
			deadline := time.Now().Add(12 * time.Second)
			for time.Now().Before(deadline) {
				var hasKid bool
				_ = chromedp.Evaluate(`[...document.querySelectorAll('iframe')].some(f => /k-id|agekey/i.test(f.src || '') || f.getBoundingClientRect().height > 280)`, &hasKid).Do(ctx)
				if hasKid {
					break
				}
				time.Sleep(400 * time.Millisecond)
			}
			time.Sleep(800 * time.Millisecond)
			if hit := clickUseAgeKey(ctx); hit != "" {
				verify = hit
				fmt.Println("Clicked AgeKey:", hit)
				if !waitForChromeTargetHost("agekey.org", 18*time.Second) {
					fmt.Println("AgeKey page did not open — pick Use your AgeKey, then Already have an AgeKey? in Chrome.")
					saveChromeShot(ctx, "setup-agekey.png")
					return nil
				}
				time.Sleep(1500 * time.Millisecond)
				if have := clickAlreadyHaveAgeKey(ctx); have != "" {
					fmt.Println("Clicked:", have)
					ageKeyHaveClicked = true
				} else {
					fmt.Println("Could not click Already have an AgeKey? — tap it in Chrome.")
				}
				saveChromeShot(ctx, "setup-agekey.png")
				return nil
			}
			dumpChromeControls(ctx, "age verify")
			return nil
		}),
	)
	if err != nil {
		return err
	}
	switch {
	case verify == "already-verified":
		fmt.Println("Age already verified on this account.")
	case strings.Contains(strings.ToLower(verify), "agekey"):
		fmt.Println("AgeKey should be open in Chrome — finish it there.")
	case verify == "":
		fmt.Println("Could not find AgeKey automatically — pick Use your AgeKey in Chrome.")
	default:
		fmt.Println("Opened age verify:", verify)
	}
	return nil
}

type setupPageStatus struct {
	Href            string `json:"href"`
	Birthday        string `json:"birthday"`
	Verified        bool   `json:"verified"`
	VerifyAge       bool   `json:"verifyAge"`
	AgeKeyFrame     bool   `json:"ageKeyFrame"`
	BirthdayConfirm bool   `json:"birthdayConfirm"`
	Leak            bool   `json:"leak"`
	CookieBanner    bool   `json:"cookieBanner"`
	CookiesOK       bool   `json:"cookiesOK"`
}

func readSetupPageStatus() setupPageStatus {
	t, ok := tumblrSettingsAccountTarget()
	if !ok {
		t, ok = tumblrPageTarget()
	}
	if !ok {
		return setupPageStatus{}
	}
	var raw string
	if err := evalJSONOnChromeTarget(t, chromeAgeStatusJS, &raw); err != nil {
		return setupPageStatus{Href: t.URL}
	}
	var st setupPageStatus
	if json.Unmarshal([]byte(raw), &st) != nil {
		st.Href = t.URL
		st.Birthday = "unset"
	}
	return st
}

func nudgeSetupAccount(st setupPageStatus) setupPageStatus {
	if ageKeyHandsOff() {
		return st
	}
	if nag := dismissChromeLeakDialog(); nagWasDismissed(nag) {
		fmt.Println("Dismissed:", nag)
	}
	dismissOverlaysOnPort()
	if nag := dismissSavePasswordBubble(); nagWasDismissed(nag) {
		fmt.Println("Dismissed:", nag)
	}
	ageKeyOpen := st.AgeKeyFrame || chromeHasTargetHost("agekey.org") || chromeHasTargetHost("agekey.com") || chromeHasTargetHost("k-id.com")
	t, ok := tumblrSettingsAccountTarget()
	if !ok {
		t, ok = tumblrPageTarget()
	}
	onAccount := ok && strings.Contains(t.URL, "/settings/account")
	if !onAccount && !ageKeyOpen {
		if t.ID != "" {
			_ = withChromeTargetCDP(t, func(call cdpCall) error {
				_, err := call("Page.navigate", map[string]any{"url": "https://www.tumblr.com/settings/account"})
				return err
			})
			time.Sleep(2 * time.Second)
		} else if opened, err := openChromeTab("https://www.tumblr.com/settings/account"); err == nil {
			t = opened
			time.Sleep(2 * time.Second)
		}
		st = readSetupPageStatus()
		t, ok = tumblrSettingsAccountTarget()
	}
	if ok && st.Birthday == "unset" && !ageKeyOpen {
		_ = applyBirthdayOnPort(t)
		st = readSetupPageStatus()
	}
	if !ageKeyOpen && (st.BirthdayConfirm || st.Birthday == "unset") {
		_ = clickAgeConfirmOnTargets()
		st = readSetupPageStatus()
	}
	if !st.Verified && st.VerifyAge && !ageKeyOpen && t.ID != "" {
		var hit string
		_ = evalJSONOnChromeTarget(t, chromeClickVerifyAgeJS, &hit)
		if hit != "" {
			fmt.Println("Clicked:", hit)
			time.Sleep(1200 * time.Millisecond)
		}
	}
	if !st.Verified && !ageKeyOpen {
		if hit := clickUseAgeKeyOnKidTarget(); hit != "" {
			fmt.Println("Clicked AgeKey:", hit)
			time.Sleep(800 * time.Millisecond)
		}
	}
	if !st.Verified && !ageKeyHaveClicked && (chromeHasTargetHost("agekey.org") || chromeHasTargetHost("agekey.com")) {
		if have := clickHaveAgeKeyOnKidTarget(); have != "" {
			fmt.Println("Clicked:", have)
			ageKeyHaveClicked = true
		}
	}
	if ok && !ageKeyOpen && t.ID != "" {
		nudgeMatureToggles(t)
	}
	return readSetupPageStatus()
}

func nudgeMatureToggles(t chromeJSONTarget) {
	var result string
	if err := evalJSONOnChromeTarget(t, chromeSetMatureTogglesJS, &result); err != nil || result == "" {
		return
	}
	if strings.Contains(result, ":on") || strings.Contains(result, ":off") || strings.Contains(result, "saved") {
		fmt.Println("Mature content:", result)
	}
}

func waitUntilSetupReady() {
	fmt.Println("Press Enter for the next account.")
	fmt.Println("Chrome stays open. Enter always continues.")

	enterCh := make(chan struct{}, 4)
	if stdinIsConsole() {
		go func() {
			r := bufio.NewReader(os.Stdin)
			for {
				_, err := r.ReadString('\n')
				if err != nil {
					return
				}
				select {
				case enterCh <- struct{}{}:
				default:
				}
			}
		}()
	}

	ignoreEnterUntil := time.Now().Add(800 * time.Millisecond)
	pinSeen := false
	var lastLookCheck, lastLookRetry, readySince time.Time
	var last string
	for {
		handsOff := ageKeyHandsOff()
		if findWindowContaining("Windows Security") != 0 {
			if !pinSeen {
				fmt.Println("Windows Security is open — type your AgeKey passcode there. I will not click until you finish.")
			}
			pinSeen = true
			handsOff = true
		}
		st := readSetupPageStatus()
		if !handsOff {
			st = nudgeSetupAccount(st)
			if st.CookieBanner || cookieBannerOpen() {
				st.CookieBanner = true
				st.CookiesOK = false
				dismissOverlaysOnPort()
			}
			if (st.Leak || leakWindowPresent()) && findWindowContaining("Windows Security") == 0 {
				if nag := dismissChromeLeakDialog(); nagWasDismissed(nag) {
					fmt.Println("Dismissed:", nag)
				}
			}
		}
		look := lookStatusFromAPI(setupBlogName)
		if !handsOff && setupBlogName != "" && (lastLookCheck.IsZero() || time.Since(lastLookCheck) > 4*time.Second) {
			lastLookCheck = time.Now()
			if !look.Complete() && (lastLookRetry.IsZero() || time.Since(lastLookRetry) > 8*time.Second) {
				lastLookRetry = time.Now()
				fmt.Printf("Look not complete yet (name=%s bio=%s avatar=%s) — applying again.\n",
					setupFlag(look.TitleOK), setupFlag(look.BioOK), setupFlag(look.AvatarOK))
				_ = applyBlogLookOnPort(liveDebugPort)
				look = lookStatusFromAPI(setupBlogName)
			}
		}
		cookiesOK := !st.CookieBanner
		if !handsOff && cookieBannerOpen() {
			cookiesOK = false
		}
		cl := setupChecklist{
			TitleOK:    look.TitleOK,
			BioOK:      look.BioOK,
			AvatarOK:   look.AvatarOK,
			BirthdayOK: st.Birthday == "set" || st.Birthday == "locked",
			CookiesOK:  cookiesOK,
			AgeOK:      st.Verified,
		}
		sig := fmt.Sprintf("%v|%v|%v|%v|%v|%v", cl.TitleOK, cl.BioOK, cl.AvatarOK, cl.BirthdayOK, cl.CookiesOK, cl.AgeOK)
		if sig != last {
			fmt.Printf("Setup check: name=%s bio=%s avatar=%s birthday=%s cookies=%s age=%s\n",
				setupFlag(cl.TitleOK), setupFlag(cl.BioOK), setupFlag(cl.AvatarOK),
				setupFlag(cl.BirthdayOK), setupFlag(cl.CookiesOK), setupFlag(cl.AgeOK))
			last = sig
		}
		if cl.Ready() {
			if readySince.IsZero() {
				readySince = time.Now()
			}
			if time.Since(readySince) >= 1500*time.Millisecond {
				fmt.Println("All setup checks passed — next account.")
				return
			}
		} else {
			readySince = time.Time{}
		}
		select {
		case <-enterCh:
			if time.Now().Before(ignoreEnterUntil) {
				break
			}
			fmt.Println("Next account.")
			return
		default:
		}
		if !stdinIsConsole() {
			if liveDebugPort == "" || !portInUse(liveDebugPort) {
				fmt.Println("Chrome closed.")
				return
			}
		}
		time.Sleep(400 * time.Millisecond)
	}
}

func applyMatureContentInChrome() error {
	return applyMatureContentOnPort(liveDebugPort)
}

func applyMatureContentOnPort(port string) error {
	port = strings.TrimSpace(port)
	if port == "" {
		port = currentDebugPort()
	}
	if port == "" {
		return fmt.Errorf("no debug port")
	}
	liveDebugPort = port
	t, ok := tumblrSettingsAccountTarget()
	if !ok || !strings.Contains(t.URL, "/settings/account") {
		opened, err := openChromeTab("https://www.tumblr.com/settings/account")
		if err != nil {
			return err
		}
		t = opened
	}
	time.Sleep(2 * time.Second)
	var dismiss string
	_ = evalJSONOnChromeTarget(t, chromeDismissOverlaysJS, &dismiss)
	time.Sleep(400 * time.Millisecond)
	var result string
	if err := evalJSONOnChromeTarget(t, chromeSetMatureTogglesJS, &result); err != nil {
		return err
	}
	fmt.Println("Mature content:", result)
	if strings.Contains(result, "missing") {
		return fmt.Errorf("could not find mature content switches")
	}
	if cookies, err := cookiesFromDebugPort(); err == nil && cookieValue(cookies, "sid") != "" {
		auth, loadErr := loadAuth()
		if loadErr == nil {
			auth.Cookie = buildTumblrCookieHeader(cookies)
			persistMaturePref(&auth)
		}
	}
	return nil
}

type ageConfirmHit struct {
	T string  `json:"t"`
	X float64 `json:"x"`
	Y float64 `json:"y"`
}

func clickAgeConfirmOnPort(port string) error {
	port = strings.TrimSpace(port)
	if port == "" {
		port = currentDebugPort()
	}
	if port == "" {
		return fmt.Errorf("no debug port")
	}
	liveDebugPort = port
	t, ok := tumblrSettingsAccountTarget()
	if !ok {
		return fmt.Errorf("no tumblr tab")
	}
	if !strings.Contains(t.URL, "/settings/account") {
		_ = withChromeTargetCDP(t, func(call cdpCall) error {
			_, err := call("Page.navigate", map[string]any{"url": "https://www.tumblr.com/settings/account"})
			return err
		})
		time.Sleep(2500 * time.Millisecond)
		t, ok = tumblrSettingsAccountTarget()
		if !ok {
			return fmt.Errorf("settings tab gone")
		}
	}
	var status string
	_ = evalJSONOnChromeTarget(t, chromeAgeStatusJS, &status)
	fmt.Println("Age status:", status)
	saveTargetShot(t, "setup-age-confirm.png")
	clicked := clickAgeConfirmOnTargets()
	if clicked == "" {
		fmt.Println("No Okay/Accept dialog yet.")
	}
	time.Sleep(800 * time.Millisecond)
	status = ""
	_ = evalJSONOnChromeTarget(t, chromeAgeStatusJS, &status)
	fmt.Println("Age status:", status)
	if strings.Contains(status, `"birthday":"unset"`) {
		fmt.Println("Birthday not saved — setting it again.")
		_ = withChromeTargetCDP(t, func(call cdpCall) error {
			_, err := call("Page.navigate", map[string]any{"url": "https://www.tumblr.com/settings/account"})
			return err
		})
		time.Sleep(2500 * time.Millisecond)
		t, ok = tumblrSettingsAccountTarget()
		if !ok {
			return fmt.Errorf("settings tab gone")
		}
		if err := applyBirthdayOnPort(t); err != nil {
			return err
		}
		time.Sleep(800 * time.Millisecond)
		clickAgeConfirmOnTargets()
		time.Sleep(800 * time.Millisecond)
		status = ""
		_ = evalJSONOnChromeTarget(t, chromeAgeStatusJS, &status)
		fmt.Println("Age status:", status)
	}
	if strings.Contains(status, `"birthday":"unset"`) {
		return fmt.Errorf("birthday still not saved")
	}
	return nil
}

func clickAgeConfirmOnTargets() string {
	list, err := listChromeTargets()
	if err != nil {
		return ""
	}
	var targets []chromeJSONTarget
	if t, ok := tumblrSettingsAccountTarget(); ok {
		targets = append(targets, t)
	}
	targets = append(targets, kidOrAgeKeyTargets(list)...)
	seen := map[string]bool{}
	for _, t := range targets {
		if t.ID == "" || seen[t.ID] {
			continue
		}
		seen[t.ID] = true
		var hit ageConfirmHit
		if err := evalJSONOnChromeTarget(t, chromeClickAgeConfirmJS, &hit); err != nil {
			continue
		}
		if hit.X > 1 && hit.Y > 1 {
			_ = mouseClickOnChromeTarget(t, hit.X, hit.Y)
		}
		if hit.T != "" {
			fmt.Println("Clicked:", hit.T, "on", chromeTargetHost(t.URL))
			return hit.T
		}
	}
	return ""
}

func finishAccountSettings() error {
	auth, err := loadAuth()
	if err != nil {
		return err
	}
	fmt.Println("Finishing English + mature content…")
	cmd, err := startChromeLoggedIn(&auth)
	if err != nil {
		return err
	}
	defer func() {
		stopChrome(cmd)
		time.Sleep(2 * time.Second)
	}()
	dismissOverlaysOnPort()
	if err := applyEnglishOnPort(); err != nil {
		fmt.Println("Language:", err)
	}
	if err := applyMatureContentOnPort(liveDebugPort); err != nil {
		fmt.Println("Mature content:", err)
	}
	if cookies, err := cookiesFromDebugPort(); err == nil && cookieValue(cookies, "sid") != "" {
		auth.Cookie = buildTumblrCookieHeader(cookies)
		persistMaturePref(&auth)
		fmt.Println("Saved session cookies.")
	}
	return nil
}

func applyEnglishOnPort() error {
	t, ok := tumblrSettingsAccountTarget()
	if !ok || !strings.Contains(t.URL, "/settings/account") {
		opened, err := openChromeTab("https://www.tumblr.com/settings/account")
		if err != nil {
			return err
		}
		t = opened
		time.Sleep(2500 * time.Millisecond)
	}
	dismissOverlaysOnPort()
	var out string
	if err := evalJSONOnChromeTarget(t, chromeSetEnglishJS, &out); err != nil {
		return err
	}
	fmt.Println("Language:", out)
	return nil
}

func applyBirthdayOnPort(t chromeJSONTarget) error {
	cfg := loadProfileSetupConfig()
	script := fmt.Sprintf(`(() => {
		const setNative = (el, value) => {
			if (!el) return false;
			el.focus();
			const proto = el.tagName === 'SELECT' ? window.HTMLSelectElement.prototype : window.HTMLInputElement.prototype;
			const desc = Object.getOwnPropertyDescriptor(proto, 'value') || Object.getOwnPropertyDescriptor(window.HTMLInputElement.prototype, 'value');
			const last = el.value;
			desc.set.call(el, value);
			if (el._valueTracker) el._valueTracker.setValue(last);
			el.dispatchEvent(new Event('input', { bubbles: true }));
			el.dispatchEvent(new Event('change', { bubbles: true }));
			return true;
		};
		const y = String(%d), m = String(%d), d = String(%d);
		const byAria = (re) => [...document.querySelectorAll('select')].find(el => re.test(el.getAttribute('aria-label') || ''));
		const month = byAria(/month|месяц/i);
		const day = byAria(/^(day|день)$/i);
		const year = byAria(/year|год/i);
		let got = [];
		if (month) {
			const opt = [...month.options].find(o => /oct|окт/i.test(o.text) || o.value === m || o.value === m.padStart(2,'0'));
			if (opt) { setNative(month, opt.value); got.push('month'); }
		}
		if (day) {
			const opt = [...day.options].find(o => o.value === d || o.value === d.padStart(2,'0') || o.text.trim() === d);
			if (opt) { setNative(day, opt.value); got.push('day'); }
		}
		if (year) {
			const opt = [...year.options].find(o => o.value === y || o.text.trim() === y);
			if (opt) { setNative(year, opt.value); got.push('year'); }
		}
		return got.join(',') || 'no-fields';
	})()`, cfg.BirthdayYear, cfg.BirthdayMonth, cfg.BirthdayDay)
	var out string
	if err := evalJSONOnChromeTarget(t, script, &out); err != nil {
		return err
	}
	fmt.Println("Birthday fields:", out)
	time.Sleep(1200 * time.Millisecond)
	saveJS := `(() => {
		const vis = (el) => el && el.getBoundingClientRect().width > 2 && el.getBoundingClientRect().height > 2;
		const save = [...document.querySelectorAll('button')].find(b => vis(b) && /^(save|сохранить)$/i.test((b.innerText || '').trim()));
		if (!save) return 'no-save';
		save.click();
		return 'save';
	})()`
	var saved string
	_ = evalJSONOnChromeTarget(t, saveJS, &saved)
	fmt.Println("Birthday:", saved)
	time.Sleep(1500 * time.Millisecond)
	for i := 0; i < 10; i++ {
		if clickAgeConfirmOnTargets() != "" {
			time.Sleep(800 * time.Millisecond)
			clickAgeConfirmOnTargets()
			return nil
		}
		time.Sleep(400 * time.Millisecond)
	}
	return nil
}

func pickCustomBirthday(ctx context.Context, cfg profileSetupConfig) {
	month := "October"
	day := fmt.Sprintf("%d", cfg.BirthdayDay)
	year := fmt.Sprintf("%d", cfg.BirthdayYear)
	var hit string
	_ = chromedp.Evaluate(chromeClickTextJS(`/^month$/i`, 16), &hit).Do(ctx)
	time.Sleep(600 * time.Millisecond)
	_ = chromedp.Evaluate(chromeClickTextJS(`/^`+month+`$|^oct$/i`, 16), &hit).Do(ctx)
	time.Sleep(400 * time.Millisecond)
	_ = chromedp.Evaluate(chromeClickTextJS(`/^day$/i`, 16), &hit).Do(ctx)
	time.Sleep(600 * time.Millisecond)
	_ = chromedp.Evaluate(chromeClickTextJS(fmt.Sprintf(`/^%s$|^0%s$/`, day, day), 8), &hit).Do(ctx)
	time.Sleep(400 * time.Millisecond)
	_ = chromedp.Evaluate(chromeClickTextJS(`/^year$/i`, 16), &hit).Do(ctx)
	time.Sleep(600 * time.Millisecond)
	_ = chromedp.Evaluate(chromeClickTextJS(fmt.Sprintf(`/^%s$/`, year), 8), &hit).Do(ctx)
	time.Sleep(400 * time.Millisecond)
	_ = chromedp.Evaluate(chromeClickTextJS(`/^save$/i`, 16), &hit).Do(ctx)
	if hit != "" {
		fmt.Println("Birthday custom pick:", hit)
	}
}
