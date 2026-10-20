package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

const (
	zoeyUsernamesFile     = "config/zoey-usernames.json"
	assignedUsernamesFile = "data/assigned-usernames.json"
	takenUsernamesFile    = "data/taken-usernames.json"
)

func defaultZoeyUsernames() []string {
	return []string{
		"cosmiczoey", "lillyzoey", "zoeybunny", "lillybully", "lilzoey",
		"zoeycosmic", "bunnyzoey", "lillybunny", "cosmiclilly", "lil-zoey",
		"zoey-bunny", "cosmic-zoey", "lillyglow", "zoeyorbit", "zoeynova",
		"lillyhaze", "bunny-zoey", "lilcosmicbunny", "zoeylil", "lillystar",
		"spacezoey", "zoeymoon", "lillynova", "lilzoeybunny", "nova-zoey",
		"zoeyhaze", "lillyorbit", "justzoeybun", "moonyzoey", "starzoey",
		"lillysoft", "zoeyglow", "lil-cosmic", "lillybloom", "tinyzoey",
		"hazezoey", "lillywild", "zoeyfromspace", "cosmicbunzoey", "lillymoon",
		"zoeystardust", "lillycosmicx", "bunzoey", "zoeylilac", "softzoey",
		"lillyvelvet", "zoeypetal", "cosmiczoeyy", "lillydreamz", "zoeyangel",
		"lilbunzoey", "zoeyskies", "lillynight", "zoeywhisper", "peachzoey",
		"lillyspark", "zoeycloud", "mintzoey", "lillyheartz", "zoeydarling",
		"foxzoey", "lillysugar", "zoeykit", "starlillyz", "zoeybelle",
		"lillymist", "zoeyfairy", "cometzoey", "lillykiss", "zoeybabe",
		"lunazoeyy", "lillycrush", "zoeyhoney", "plushzoey", "lillygloww",
		"zoeypixie", "aurora-zoey", "lillycosmix", "zoeybliss", "sweetzoeybun",
	}
}

func zoeyUsernamePool() []string {
	raw, err := os.ReadFile(zoeyUsernamesFile)
	if err != nil {
		return defaultZoeyUsernames()
	}
	var names []string
	if json.Unmarshal(raw, &names) != nil || len(names) == 0 {
		return defaultZoeyUsernames()
	}
	var out []string
	seen := map[string]bool{}
	for _, n := range names {
		n = normalizeBlogName(n)
		if n == "" || seen[n] || looksNumberedClone(n) {
			continue
		}
		seen[n] = true
		out = append(out, n)
	}
	if len(out) == 0 {
		return defaultZoeyUsernames()
	}
	return out
}

func normalizeBlogName(s string) string {
	s = strings.ToLower(strings.TrimSpace(strings.TrimPrefix(s, "@")))
	s = strings.TrimSuffix(s, ".tumblr.com")
	return s
}

func looksNumberedClone(name string) bool {
	n := normalizeBlogName(name)
	if n == "" {
		return false
	}
	i := len(n)
	for i > 0 && n[i-1] >= '0' && n[i-1] <= '9' {
		i--
	}
	if i == len(n) || i == 0 {
		return false
	}
	base := n[:i]
	return strings.Contains(base, "cosmic") || strings.Contains(base, "lilly") || strings.Contains(base, "zoey")
}

func usernameAlreadyOnBrand(name string) bool {
	n := normalizeBlogName(name)
	if n == "" {
		return false
	}
	compact := strings.ReplaceAll(n, "-", "")
	for _, token := range []string{"zoey", "zoeey", "zooey", "cosmic", "lilly", "lily", "bunny", "lilcosmic"} {
		if strings.Contains(n, token) || strings.Contains(compact, token) {
			return true
		}
	}
	for _, p := range zoeyUsernamePool() {
		if n == p || strings.HasPrefix(n, p) {
			return true
		}
	}
	return false
}

func usernameIsZoeyStyle(name string) bool {
	return usernameAlreadyOnBrand(name)
}

func validTumblrUsername(s string) bool {
	if s == "" || len(s) > 32 {
		return false
	}
	for _, r := range s {
		if r >= 'a' && r <= 'z' || r >= '0' && r <= '9' || r == '-' {
			continue
		}
		return false
	}
	return true
}

func usernameVariants(base string) []string {
	base = normalizeBlogName(base)
	var out []string
	seen := map[string]bool{}
	add := func(s string) {
		s = normalizeBlogName(s)
		if !validTumblrUsername(s) || seen[s] {
			return
		}
		seen[s] = true
		out = append(out, s)
	}
	add(base)
	if r := []rune(base); len(r) > 0 {
		last := string(r[len(r)-1])
		add(base + last)
		add(base + last + last)
	}
	for _, extra := range []string{"y", "x", "z", "s", "ie", "yy"} {
		add(base + extra)
	}
	if r := []rune(base); len(r) >= 2 {
		add(string(r[:len(r)-1]) + string(r[len(r)-2]) + string(r[len(r)-1]))
	}
	for _, n := range []string{"7", "4", "9", "8", "0"} {
		add(base + n)
	}
	return out
}

func loadNameMap(path string) map[string]string {
	out := map[string]string{}
	raw, err := os.ReadFile(path)
	if err != nil {
		return out
	}
	_ = json.Unmarshal(raw, &out)
	return out
}

func saveNameMap(path string, m map[string]string) {
	_ = os.MkdirAll(filepath.Dir(path), 0755)
	raw, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return
	}
	_ = os.WriteFile(path, append(raw, '\n'), 0644)
}

func markUsernameTaken(name string) {
	name = normalizeBlogName(name)
	if name == "" {
		return
	}
	taken := loadStringSet(takenUsernamesFile)
	taken[name] = true
	_ = saveStringSet(takenUsernamesFile, taken)
}

func usedZoeyUsernames(exceptAccount string) map[string]bool {
	used := loadStringSet(takenUsernamesFile)
	assigned := loadNameMap(assignedUsernamesFile)
	for id, name := range assigned {
		if id == exceptAccount {
			continue
		}
		if n := normalizeBlogName(name); n != "" {
			used[n] = true
		}
	}
	for _, a := range allAccounts() {
		if a.ID == exceptAccount {
			continue
		}
		if n := normalizeBlogName(peekAccountAuth(a.ID).Blog); n != "" {
			used[n] = true
		}
	}
	return used
}

func pickZoeyUsername(accountID, current string) string {
	accountID = accountSlug(accountID)
	current = normalizeBlogName(current)
	assigned := loadNameMap(assignedUsernamesFile)
	if want := normalizeBlogName(assigned[accountID]); want != "" && !loadStringSet(takenUsernamesFile)[want] {
		return want
	}
	if usernameAlreadyOnBrand(current) {
		assigned[accountID] = current
		saveNameMap(assignedUsernamesFile, assigned)
		return current
	}
	used := usedZoeyUsernames(accountID)
	used[current] = true
	for _, cand := range append(zoeyUsernamePool(), generatedZoeyUsernames()...) {
		if used[cand] {
			continue
		}
		assigned[accountID] = cand
		saveNameMap(assignedUsernamesFile, assigned)
		return cand
	}
	return ""
}

// generatedZoeyUsernames backs up the hand-picked pool once every name in it
// is taken, so a blog never gets stuck with its old non-Zoey username.
func generatedZoeyUsernames() []string {
	heads := []string{"zoey", "lilly"}
	words := []string{"moon", "star", "peach", "velvet", "cloud", "honey", "cherry", "sugar",
		"dream", "petal", "glow", "kitty", "bunbun", "angel", "pixie", "lunar", "rosy", "candy",
		"mochi", "berry", "skye", "nova", "fairy", "daisy", "silk", "plum", "minty", "cosmo"}
	var out []string
	for _, w := range words {
		for _, h := range heads {
			out = append(out, h+w, w+h, h+"-"+w)
		}
	}
	return out
}

const chromeAvatarBoxJS = `(() => {
	const vis = (el) => el && el.getBoundingClientRect().width > 2 && el.getBoundingClientRect().height > 2;
	const el = [...document.querySelectorAll('[aria-label]')].find(n => vis(n) &&
		/(edit|change|editar|cambiar|bearbeiten).*(avatar|profile (photo|picture)|foto de perfil)/i.test(n.getAttribute('aria-label') || ''));
	if (!el) return {found: false, x: 0, y: 0};
	el.scrollIntoView({block: 'center', inline: 'nearest'});
	const small = [...el.querySelectorAll('button, [role="button"]')].find(n => {
		const r = n.getBoundingClientRect();
		return r.width >= 10 && r.width <= 56 && r.height >= 10 && r.height <= 56;
	});
	const hit = small || el;
	const r = hit.getBoundingClientRect();
	if (r.width < 4 || r.height < 4) return {found: false, x: 0, y: 0};
	return {found: true, x: r.left + r.width / 2, y: r.top + r.height / 2};
})()`

const chromeHeaderBoxJS = `(() => {
	const vis = (el) => el && el.getBoundingClientRect().width > 2 && el.getBoundingClientRect().height > 2;
	const el = [...document.querySelectorAll('[aria-label]')].find(n => vis(n) &&
		/(edit|change|editar|cambiar|bearbeiten).*(header|banner|cabecera|encabezado)/i.test(n.getAttribute('aria-label') || ''));
	if (!el) return {found: false, x: 0, y: 0};
	el.scrollIntoView({block: 'center', inline: 'nearest'});
	const small = [...el.querySelectorAll('button, [role="button"]')].find(n => {
		const r = n.getBoundingClientRect();
		return r.width >= 10 && r.width <= 56 && r.height >= 10 && r.height <= 56;
	});
	const hit = small || el;
	const r = hit.getBoundingClientRect();
	if (r.width < 4 || r.height < 4) return {found: false, x: 0, y: 0};
	if (hit === el && (r.width > 80 || r.height > 80)) {
		return {found: true, x: r.right - 18, y: r.top + 18};
	}
	return {found: true, x: r.left + r.width / 2, y: r.top + r.height / 2};
})()`

const chromeHeaderCenterJS = `(() => {
	const vis = (el) => el && el.getBoundingClientRect().width > 2 && el.getBoundingClientRect().height > 2;
	const el = [...document.querySelectorAll('[aria-label]')].find(n => vis(n) &&
		/(edit|change|editar|cambiar|bearbeiten).*(header|banner|cabecera|encabezado)/i.test(n.getAttribute('aria-label') || ''));
	if (!el) return {found: false, x: 0, y: 0};
	el.scrollIntoView({block: 'center', inline: 'nearest'});
	const r = el.getBoundingClientRect();
	if (r.width < 4 || r.height < 4) return {found: false, x: 0, y: 0};
	return {found: true, x: r.left + r.width / 2, y: r.top + Math.min(40, r.height / 3)};
})()`

// Tumblr can hide the header pencil when "Show header image" is off.
// Turn that toggle (and Stretch) back on before we hunt for the editor.
const chromeEnableShowHeaderJS = `(() => {
	const vis = (el) => {
		if (!el || !el.getBoundingClientRect) return false;
		const r = el.getBoundingClientRect();
		const s = getComputedStyle(el);
		return r.width > 2 && r.height > 2 && s.visibility !== 'hidden' && s.display !== 'none';
	};
	const textOf = (el) => (el.innerText || el.getAttribute('aria-label') || '').replace(/\s+/g, ' ').trim();
	const showRe = /show header image|mostrar (la )?imagen (de )?(cabecera|encabezado|portada)|afficher l['']?image d['']?en[- ]?t[eê]te|headerbild anzeigen|mostra (l['']?)?immagine (di )?(intestazione|header)/i;
	const stretchRe = /stretch header image|estirar (la )?imagen (de )?(cabecera|encabezado)|étirer l['']?image|headerbild strecken/i;
	const hits = [];
	const turnOn = (re, tag) => {
		for (const el of document.querySelectorAll('label, span, div, p, li, button')) {
			if (!vis(el) || !re.test(textOf(el)) || textOf(el).length > 64) continue;
			let root = el.closest('label') || el.parentElement;
			for (let i = 0; i < 4 && root; i++, root = root.parentElement) {
				const box = root.querySelector('input[type="checkbox"], [role="switch"], input[type="checkbox"]');
				if (!box || !vis(box)) continue;
				const on = box.checked || box.getAttribute('aria-checked') === 'true';
				if (!on) {
					box.click();
					hits.push(tag + '-on');
				} else {
					hits.push(tag + '-already');
				}
				return;
			}
			// Some rows are the clickable control themselves.
			if (/^(button|div)$/i.test(el.tagName) && el.getAttribute('role') === 'switch') {
				if (el.getAttribute('aria-checked') !== 'true') {
					el.click();
					hits.push(tag + '-switch-on');
				} else {
					hits.push(tag + '-switch-already');
				}
				return;
			}
		}
	};
	turnOn(showRe, 'show');
	turnOn(stretchRe, 'stretch');
	return hits.join(',') || 'no-toggle';
})()`

func chromeColorSwatchBoxJS(label string) string {
	lab, _ := json.Marshal(label)
	return fmt.Sprintf(`(() => {
		const vis = (el) => el && el.getBoundingClientRect().width > 2 && el.getBoundingClientRect().height > 2;
		const textOf = (el) => (el.innerText || el.getAttribute('aria-label') || '').replace(/\s+/g, ' ').trim();
		const want = %s;
		const lab = [...document.querySelectorAll('span, div, label, p, button')].find(el => vis(el) && textOf(el).toLowerCase() === String(want).toLowerCase());
		if (!lab) return {found: false, x: 0, y: 0};
		lab.scrollIntoView({block: 'center', inline: 'nearest'});
		let n = lab.parentElement;
		for (let i = 0; i < 6 && n; i++, n = n.parentElement) {
			const hits = [...n.querySelectorAll('button, [role="button"], input, div, span')].filter(el => {
				if (el === lab || !vis(el)) return false;
				const r = el.getBoundingClientRect();
				return r.width >= 14 && r.width <= 64 && r.height >= 14 && r.height <= 64;
			});
			if (hits.length) {
				const r = hits[0].getBoundingClientRect();
				return {found: true, x: r.left + r.width / 2, y: r.top + r.height / 2};
			}
		}
		const r = lab.getBoundingClientRect();
		return {found: true, x: r.right + 18, y: r.top + r.height / 2};
	})()`, string(lab))
}

func chromeFillOpenHexJS(color string) string {
	c, _ := json.Marshal(color)
	return fmt.Sprintf(`(() => {
		const value = %s;
		const vis = (el) => {
			if (!el) return false;
			const r = el.getBoundingClientRect();
			return r.width > 8 && r.height > 8;
		};
		const setNative = (el) => {
			if (!el || (el.type || '').toLowerCase() === 'color') return false;
			if ((el.tagName || '').toLowerCase() === 'textarea') return false;
			if (/title|description|bio|user/i.test(el.getAttribute('aria-label') || el.name || el.placeholder || '')) return false;
			el.focus();
			if (el.select) el.select();
			const proto = window.HTMLInputElement.prototype;
			const desc = Object.getOwnPropertyDescriptor(proto, 'value');
			desc.set.call(el, value);
			el.dispatchEvent(new Event('input', { bubbles: true }));
			el.dispatchEvent(new Event('change', { bubbles: true }));
			el.dispatchEvent(new InputEvent('input', { bubbles: true, data: value, inputType: 'insertText' }));
			return true;
		};
		const inputs = [...document.querySelectorAll('input[type="text"], input:not([type])')].filter(vis);
		const el = inputs.find(i => /^#?[0-9a-f]{3,8}$/i.test((i.value || '').trim()) || /hex/i.test(i.getAttribute('aria-label') || i.placeholder || i.name || ''));
		if (!setNative(el)) return '';
		return 'hex';
	})()`, string(c))
}

// "Edit theme" opens /customize/<blog>, which is a different editor. It must
// never be used as a fallback for Tumblr's profile "Edit appearance" button.
const chromeEditAppearanceWantJS = `/^(edit appearance|customi[sz]e appearance|editar apariencia|personalizar apariencia|modificar apariencia|изменить оформление)$/i`

const chromeClickEditAppearanceJS = `(() => {
	window.scrollTo(0, 0);
	const vis = (el) => {
		if (!el) return false;
		const s = getComputedStyle(el);
		if (s.display === 'none' || s.visibility === 'hidden' || Number(s.opacity) === 0) return false;
		const r = el.getBoundingClientRect();
		return r.width > 2 && r.height > 2;
	};
	const textOf = (el) => (el.innerText || el.getAttribute('aria-label') || '').replace(/\s+/g,' ').trim();
	const hrefOf = (el) => {
		const a = (el.getAttribute && el.getAttribute('href')) || '';
		if (a) return a;
		const p = el.closest && el.closest('a');
		return (p && p.getAttribute('href')) || '';
	};
	const badHref = (el) => /\/customize\//i.test(hrefOf(el));
	const editorOpen = () => {
		if (document.querySelector('[aria-label="Edit blog avatar image"]')) return true;
		if (document.querySelector('[aria-label="Edit blog header image"]')) return true;
		return false;
	};
	if (editorOpen()) return 'already';
	const want = ` + chromeEditAppearanceWantJS + `;
	const cands = [...document.querySelectorAll('button, a, [role="button"], [tabindex], label, span, div, p, [aria-label]')]
		.filter(vis)
		.map(el => ({el, t: textOf(el)}))
		.filter(x => x.t && x.t.length <= 40 && want.test(x.t) && !badHref(x.el));
	cands.sort((a, b) => a.t.length - b.t.length);
	let el = cands[0] && cands[0].el;
	if (el && el.closest) {
		const btn = el.closest('button, [role="button"], a');
		if (btn && vis(btn) && !badHref(btn)) el = btn;
	}
	if (!el) return '';
	el.scrollIntoView({block: 'center', inline: 'nearest'});
	el.focus();
	el.click();
	return (el.innerText || el.getAttribute('aria-label') || 'Edit appearance').trim();
})()`

const chromeEditAppearanceBoxJS = `(() => {
	window.scrollTo(0, 0);
	const vis = (el) => {
		if (!el) return false;
		const s = getComputedStyle(el);
		if (s.display === 'none' || s.visibility === 'hidden' || Number(s.opacity) === 0) return false;
		const r = el.getBoundingClientRect();
		return r.width > 2 && r.height > 2;
	};
	const textOf = (el) => (el.innerText || el.getAttribute('aria-label') || '').replace(/\s+/g,' ').trim();
	const hrefOf = (el) => {
		const a = (el.getAttribute && el.getAttribute('href')) || '';
		if (a) return a;
		const p = el.closest && el.closest('a');
		return (p && p.getAttribute('href')) || '';
	};
	const badHref = (el) => /\/customize\//i.test(hrefOf(el));
	const want = ` + chromeEditAppearanceWantJS + `;
	const cands = [...document.querySelectorAll('button, a, [role="button"], [tabindex], label, span, div, p, [aria-label]')]
		.filter(vis)
		.map(el => ({el, t: textOf(el)}))
		.filter(x => x.t && x.t.length <= 40 && want.test(x.t) && !badHref(x.el));
	cands.sort((a, b) => a.t.length - b.t.length);
	let el = cands[0] && cands[0].el;
	if (el && el.closest) {
		const btn = el.closest('button, [role="button"], a');
		if (btn && vis(btn) && !badHref(btn)) el = btn;
	}
	if (!el) return {found: false, x: 0, y: 0};
	el.scrollIntoView({block: 'center', inline: 'nearest'});
	const r = el.getBoundingClientRect();
	return {found: true, x: r.left + r.width / 2, y: r.top + r.height / 2};
})()`

func chromeSetThemeColorsJS(accent, background string) string {
	a, _ := json.Marshal(accent)
	b, _ := json.Marshal(background)
	return fmt.Sprintf(`(() => {
		const vis = (el) => el && el.getBoundingClientRect().width > 2 && el.getBoundingClientRect().height > 2;
		const textOf = (el) => (el.innerText || el.getAttribute('aria-label') || '').replace(/\s+/g, ' ').trim();
		const setNative = (el, value) => {
			if (!el) return false;
			el.focus();
			el.click();
			const proto = el.tagName === 'TEXTAREA' ? window.HTMLTextAreaElement.prototype : window.HTMLInputElement.prototype;
			const desc = Object.getOwnPropertyDescriptor(proto, 'value') || Object.getOwnPropertyDescriptor(window.HTMLInputElement.prototype, 'value');
			desc.set.call(el, value);
			el.dispatchEvent(new Event('input', { bubbles: true }));
			el.dispatchEvent(new Event('change', { bubbles: true }));
			el.dispatchEvent(new InputEvent('input', { bubbles: true, data: value, inputType: 'insertText' }));
			return true;
		};
		const clickLabel = (label) => {
			const lab = [...document.querySelectorAll('span, div, label, p, button')].find(el => vis(el) && new RegExp('^' + label + '$', 'i').test(textOf(el)));
			if (!lab) return;
			lab.click();
			let n = lab.parentElement;
			for (let i = 0; i < 5 && n; i++, n = n.parentElement) {
				const hit = n.querySelector('input[type="color"]') || [...n.querySelectorAll('button, [role="button"]')].find(vis);
				if (hit) { hit.click(); return; }
			}
		};
		const allColors = [...document.querySelectorAll('input[type="color"]')];
		const byName = (n) => allColors.find(el => (el.name || '').toLowerCase() === n);
		const setNamed = (n, value, fallback) => {
			const el = byName(n) || fallback;
			if (setNative(el, value)) return true;
			clickLabel(n.charAt(0).toUpperCase() + n.slice(1));
			return setNative(byName(n) || el, value);
		};
		let out = [];
		if (setNamed('accent', %s, allColors[0])) out.push('accent');
		if (setNamed('background', %s, allColors[1] || allColors[0])) out.push('background');
		return out.join(' ');
	})()`, string(a), string(b))
}

func chromeBlogLookStatusJS(accent, background, wantUser string) string {
	a, _ := json.Marshal(strings.ToLower(accent))
	b, _ := json.Marshal(strings.ToLower(background))
	u, _ := json.Marshal(strings.ToLower(wantUser))
	return fmt.Sprintf(`(() => {
		const wantAccent = %s;
		const wantBg = %s;
		const wantUser = %s;
		const vis = (el) => el && el.getBoundingClientRect().width > 2 && el.getBoundingClientRect().height > 2;
		const textOf = (el) => (el.innerText || el.getAttribute('aria-label') || '').replace(/\s+/g, ' ').trim();
		const parseHex = (h) => {
			h = String(h || '').replace('#','').toLowerCase();
			if (h.length === 3) h = h[0]+h[0]+h[1]+h[1]+h[2]+h[2];
			if (!/^[0-9a-f]{6}$/.test(h)) return null;
			return [parseInt(h.slice(0,2),16), parseInt(h.slice(2,4),16), parseInt(h.slice(4,6),16)];
		};
		const parseRGB = (s) => {
			s = String(s || '');
			const m = /rgba?\(\s*(\d+)\s*,\s*(\d+)\s*,\s*(\d+)/i.exec(s);
			if (m) return [+m[1], +m[2], +m[3]];
			return parseHex(s);
		};
		const near = (got, want) => {
			const a = parseRGB(got), b = parseHex(want);
			if (!a || !b) return false;
			return Math.abs(a[0]-b[0]) < 28 && Math.abs(a[1]-b[1]) < 28 && Math.abs(a[2]-b[2]) < 28;
		};
		const body = (document.body && document.body.innerText) || '';
		const taken = /that'?s a good one, but it'?s taken/i.test(body) ||
			[...document.querySelectorAll('span, div, p')].some(el => {
				const s = textOf(el);
				return s.length > 8 && s.length < 90 && /taken/i.test(s) && /good one|not available|already/i.test(s);
			});
		const swatchColor = (label) => {
			const colors = [...document.querySelectorAll('input[type="color"]')];
			const input = colors.find(el => (el.name || '').toLowerCase() === label.toLowerCase()) || colors[label === 'Accent' ? 0 : 1];
			if (input && input.value) return input.value;
			const lab = [...document.querySelectorAll('span, div, label, p')].find(el => vis(el) && new RegExp('^' + label + '$', 'i').test(textOf(el)));
			if (!lab) return '';
			let n = lab.parentElement;
			for (let i = 0; i < 5 && n; i++, n = n.parentElement) {
				for (const el of n.querySelectorAll('button, [role="button"], div, span, input')) {
					if (!vis(el) || el === lab) continue;
					const r = el.getBoundingClientRect();
					if (r.width < 10 || r.width > 72 || r.height < 10 || r.height > 72) continue;
					const c = getComputedStyle(el).backgroundColor;
					if (c && c !== 'rgba(0, 0, 0, 0)' && c !== 'transparent') return c;
				}
			}
			return '';
		};
		const swatchAccent = swatchColor('Accent');
		const swatchBg = swatchColor('Background');
		let previewBg = '';
		const title = [...document.querySelectorAll('h1, h2, h3, input')].find(el => /zoey/i.test(el.innerText || el.value || ''));
		if (title) {
			let n = title.parentElement;
			for (let i = 0; i < 10 && n; i++, n = n.parentElement) {
				const c = getComputedStyle(n).backgroundColor;
				if (near(c, wantBg)) { previewBg = c; break; }
			}
		}
		let header = '';
		const add = (u) => {
			u = (u || '').trim();
			if (!u || header) return;
			if (!/^(blob:|https?:)/i.test(u)) return;
			if (/default_header|assets\.tumblr\.com|default_avatar/i.test(u)) return;
			header = u;
		};
		const headerBtn = document.querySelector('[aria-label="Edit blog header image"]');
		if (headerBtn) {
			let n = headerBtn;
			for (let i = 0; i < 8 && n; i++, n = n.parentElement) {
				for (const img of n.querySelectorAll('img')) add(img.currentSrc || img.src);
				const bg = getComputedStyle(n).backgroundImage || '';
				const m = /url\(["']?([^"')]+)["']?\)/.exec(bg);
				if (m) add(m[1]);
			}
		}
		for (const img of document.querySelectorAll('img')) {
			const r = img.getBoundingClientRect();
			if (r.width > 220 && r.height > 70 && r.top < 520) add(img.currentSrc || img.src);
		}
		const bannerOK = !!header && /blob:|media\.tumblr\.com/i.test(header);
		const href = (location.pathname || '').toLowerCase();
		const userOK = !!(wantUser && href.indexOf('/settings/blog/' + wantUser) >= 0);
		return {
			taken: taken,
			href: location.href || '',
			accent: swatchAccent,
			background: swatchBg,
			swatchAccent: swatchAccent,
			swatchBg: swatchBg,
			previewBg: previewBg,
			header: header,
			accentOK: near(swatchAccent, wantAccent),
			backgroundOK: near(swatchBg, wantBg) || near(previewBg, wantBg),
			bannerOK: bannerOK,
			userOK: userOK
		};
	})()`, string(a), string(b), string(u))
}

func chromeFillTitleBioJS(title, desc string) string {
	titleJSON, _ := json.Marshal(title)
	descJSON, _ := json.Marshal(desc)
	return fmt.Sprintf(`(() => {
		const vis = (el) => {
			if (!el) return false;
			const s = getComputedStyle(el);
			if (s.display === 'none' || s.visibility === 'hidden') return false;
			const r = el.getBoundingClientRect();
			return r.width > 2 && r.height > 2;
		};
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
			const setter = Object.getOwnPropertyDescriptor(proto, 'value') || Object.getOwnPropertyDescriptor(window.HTMLInputElement.prototype, 'value');
			const last = el.value;
			setter.set.call(el, value);
			if (el._valueTracker) el._valueTracker.setValue(last);
			el.dispatchEvent(new Event('input', { bubbles: true }));
			el.dispatchEvent(new Event('change', { bubbles: true }));
			return true;
		};
		const metaOf = (el) => (el.getAttribute('aria-label') || el.name || el.placeholder || '').replace(/\s+/g, ' ').trim();
		const rowText = (el) => {
			let t = '';
			const id = el.getAttribute('id');
			if (id) {
				const lab = document.querySelector('label[for="' + id + '"]');
				if (lab) t += ' ' + (lab.innerText || '');
			}
			let n = el.parentElement;
			for (let i = 0; i < 3 && n; i++, n = n.parentElement) {
				t += ' ' + (n.innerText || '').replace(/\s+/g, ' ').trim().slice(0, 120);
			}
			return t;
		};
		const bad = (el) => /question|ask page|pregunta|username|usuario|e-?mail|password|contrase|search|buscar|url|tag|mention|promote|redirect/i.test(metaOf(el) + ' ' + rowText(el));
		const titleWant = /^(blog title|title|blog name|t[ií]tulo( del blog)?|nombre del blog|blogtitel)$/i;
		const descWant = /^(blog description|description|bio|descripci[oó]n( del blog)?|beschreibung)$/i;
		const inputs = [...document.querySelectorAll('input[type="text"], input:not([type]), [contenteditable="true"]')].filter(vis);
		const areas = [...document.querySelectorAll('textarea')].filter(vis);
		let titleEl = document.querySelector('input[aria-label="Blog title"]');
		if (!vis(titleEl)) titleEl = null;
		if (!titleEl) titleEl = inputs.find(el => !bad(el) && titleWant.test(metaOf(el)));
		if (!titleEl) titleEl = inputs.find(el => !bad(el) && /blog title|t[ií]tulo del blog|blogtitel/i.test(metaOf(el) + ' ' + rowText(el)));
		let descEl = document.querySelector('textarea[aria-label="Blog description"]');
		if (!vis(descEl)) descEl = null;
		if (!descEl) descEl = areas.find(el => !bad(el) && descWant.test(metaOf(el)));
		if (!descEl) descEl = areas.find(el => !bad(el) && /description|bio|descripci|beschreibung/i.test(metaOf(el) + ' ' + rowText(el)));
		if (!descEl) descEl = areas.find(el => !bad(el));
		const okTitle = setNative(titleEl, %s);
		const okDesc = setNative(descEl, %s);
		if (!okTitle && !okDesc) return 'none';
		return (okTitle ? 'title ' : '') + (okDesc ? 'bio ' : '');
	})()`, string(titleJSON), string(descJSON))
}

func chromeOpenUsernameJS() string {
	return `(() => {
		window.scrollTo(0, 0);
		const vis = (el) => {
			if (!el) return false;
			const r = el.getBoundingClientRect();
			const s = getComputedStyle(el);
			return r.width > 4 && r.height > 4 && s.display !== 'none' && s.visibility !== 'hidden';
		};
		const textOf = (el) => (el.innerText || el.getAttribute('aria-label') || '').replace(/\s+/g, ' ').trim();
		const metaOf = (el) => (el.getAttribute('aria-label') || el.name || el.placeholder || '').replace(/\s+/g, ' ').trim();
		const skipMeta = /email|password|search|title|description|question|ask|mention|promote/i;
		const change = [...document.querySelectorAll('button, a, [role="button"], span, div')]
			.find(el => vis(el) && textOf(el).length <= 32 && /^(change (your )?username|cambiar nombre de usuario)$/i.test(textOf(el)));
		if (change) { change.scrollIntoView({block: 'center', inline: 'nearest'}); change.click(); return 'change'; }
		const input = [...document.querySelectorAll('input[type="text"], input:not([type])')].find(el => {
			if (!vis(el)) return false;
			const meta = metaOf(el);
			if (!meta || skipMeta.test(meta)) return false;
			return /^(username|new username|blog username|tumblr username)$/i.test(meta);
		});
		if (input) return 'already';
		const labels = [...document.querySelectorAll('div, span, label, h1, h2, h3, p')].filter(el => vis(el) && /^(username|nombre de usuario)$/i.test(textOf(el)));
		for (const lab of labels) {
			const lr = lab.getBoundingClientRect();
			let n = lab.parentElement;
			for (let i = 0; i < 6 && n; i++, n = n.parentElement) {
				const rowButtons = [...n.querySelectorAll('button, a, [role="button"], [tabindex]')].filter(el => {
					if (!vis(el) || el === lab) return false;
					if (/\/customize\//i.test(el.getAttribute('href') || '')) return false;
					const labTxt = (el.getAttribute('aria-label') || textOf(el) || '').trim();
					if (/change (your )?username|edit username|pencil/i.test(labTxt)) return true;
					const r = el.getBoundingClientRect();
					const sameRow = Math.abs((r.top + r.height / 2) - (lr.top + lr.height / 2)) < 48;
					return sameRow && r.left > lr.right && r.width <= 72 && r.height <= 72;
				});
				rowButtons.sort((a, b) => b.getBoundingClientRect().right - a.getBoundingClientRect().right);
				if (rowButtons[0]) { rowButtons[0].click(); return 'row-pencil'; }
			}
		}
		return '';
	})()`
}

func chromeFillUsernameJS(name string) string {
	want, _ := json.Marshal(name)
	w := string(want)
	return fmt.Sprintf(`(() => {
		const value = %s;
		const setNative = (el) => {
			if (!el) return false;
			el.focus();
			el.click();
			const proto = window.HTMLInputElement.prototype;
			const desc = Object.getOwnPropertyDescriptor(proto, 'value');
			desc.set.call(el, '');
			el.dispatchEvent(new Event('input', { bubbles: true }));
			if (el.select) el.select();
			document.execCommand('selectAll', false, null);
			document.execCommand('insertText', false, value);
			desc.set.call(el, value);
			el.dispatchEvent(new Event('input', { bubbles: true }));
			el.dispatchEvent(new Event('change', { bubbles: true }));
			el.dispatchEvent(new InputEvent('input', { bubbles: true, data: value, inputType: 'insertText' }));
			return true;
		};
		const vis = (el) => el && el.getBoundingClientRect().width > 2 && el.getBoundingClientRect().height > 2;
		const skip = /email|password|search|title|description|question|ask|mention|promote|url|link|tag/i;
		const metaOf = (i) => (i.getAttribute('aria-label') || i.name || i.placeholder || i.id || '').replace(/\s+/g, ' ').trim();
		const looksUser = (i) => {
			const meta = metaOf(i);
			if (!meta || skip.test(meta)) return false;
			return /^(username|new username|blog username|tumblr username)$/i.test(meta);
		};
		const inUsernameEditor = (i) => {
			let n = i.parentElement;
			for (let k = 0; k < 8 && n; k++, n = n.parentElement) {
				const txt = (n.innerText || '').replace(/\s+/g, ' ').trim();
				if (txt.length < 8 || txt.length > 400) continue;
				if (/question|ask page|mentions|promoted/i.test(txt)) continue;
				if (/username/i.test(txt) && /\bsave\b/i.test(txt) && /\bcancel\b/i.test(txt)) return true;
			}
			return false;
		};
		const inputs = [...document.querySelectorAll('input[type="text"], input:not([type])')].filter(vis);
		const el = inputs.find(looksUser) || inputs.find(inUsernameEditor);
		if (!setNative(el)) return '';
		return 'filled';
	})()`, w)
}

func chromeUsernameStatusJS(name string) string {
	want, _ := json.Marshal(strings.ToLower(name))
	return fmt.Sprintf(`(() => {
		const want = %s;
		const t = ((document.body && document.body.innerText) || '');
		if (/that'?s a good one, but it'?s taken/i.test(t)) return 'taken';
		const err = [...document.querySelectorAll('span, div, p')].some(el => {
			const s = (el.innerText || '').replace(/\s+/g, ' ').trim();
			return s.length > 8 && s.length < 90 && /taken/i.test(s) && /good one|not available|already/i.test(s);
		});
		if (err) return 'taken';
		const href = (location.pathname || '').toLowerCase();
		if (want && href.indexOf('/settings/blog/' + want) >= 0) return 'ok';
		return 'pending';
	})()`, string(want))
}

const chromeUploadPhotoJS = `(() => {
	const vis = (el) => el && el.getBoundingClientRect().width > 2 && el.getBoundingClientRect().height > 2;
	const textOf = (el) => (el.innerText || el.getAttribute('aria-label') || '').replace(/\s+/g, ' ').trim();
	const el = [...document.querySelectorAll('button, a, [role="button"], label')].find(b => vis(b) &&
		/^(upload|upload (a )?(photo|image)|choose (a )?(file|photo|image)|browse|select (a )?(file|photo|image)|add (a )?(photo|image))$/i.test(textOf(b)));
	if (!el) return '';
	el.click();
	return textOf(el);
})()`

const chromeUsernameRowSaveJS = `(() => {
	const vis = (el) => el && el.getBoundingClientRect().width > 2 && el.getBoundingClientRect().height > 2;
	const textOf = (el) => (el.innerText || el.getAttribute('aria-label') || '').replace(/\s+/g, ' ').trim();
	const cancel = [...document.querySelectorAll('button, [role="button"]')].find(b => vis(b) && /^cancel$/i.test(textOf(b)));
	if (!cancel) return '';
	let n = cancel.parentElement;
	for (let i = 0; i < 5 && n; i++, n = n.parentElement) {
		const save = [...n.querySelectorAll('button, [role="button"]')].find(b => vis(b) && /^save$/i.test(textOf(b)));
		if (save) { save.click(); return 'username-save'; }
	}
	return '';
})()`

const chromeUsernameRowCancelJS = `(() => {
	const vis = (el) => el && el.getBoundingClientRect().width > 2 && el.getBoundingClientRect().height > 2;
	const textOf = (el) => (el.innerText || el.getAttribute('aria-label') || '').replace(/\s+/g, ' ').trim();
	const cancel = [...document.querySelectorAll('button, [role="button"]')].find(b => vis(b) && /^cancel$/i.test(textOf(b)));
	if (!cancel) return '';
	cancel.click();
	return 'username-cancel';
})()`

const chromeConfirmUsernameJS = `(() => {
	const vis = (el) => el && el.getBoundingClientRect().width > 2 && el.getBoundingClientRect().height > 2;
	const textOf = (el) => (el.innerText || el.getAttribute('aria-label') || '').replace(/\s+/g, ' ').trim();
	const skip = /cancel|never|not now|cookie|premium|^save$|^save changes$/i;
	const dlg = document.querySelector('[role="dialog"], [aria-modal="true"]');
	const roots = dlg ? [dlg] : [document];
	for (const root of roots) {
		const els = [...root.querySelectorAll('button, [role="button"], a')].filter(vis);
		const el = els.find(b => /change username|yes,? change|continue|confirm|done|use this/i.test(textOf(b)) && !skip.test(textOf(b)));
		if (el) { el.click(); return textOf(el); }
	}
	return '';
})()`

func openBlogSettingsTab(blogName string) (chromeJSONTarget, error) {
	settings := "https://www.tumblr.com/settings/blog/" + blogName
	fmt.Println("Opening blog settings for", blogName)
	t, err := openOrNavigateTab(settings)
	if err != nil {
		return chromeJSONTarget{}, err
	}
	t = leaveCustomizeTheme(t, blogName)
	time.Sleep(1200 * time.Millisecond)
	closeExtraSetupTabs()
	dismissOverlaysOnPort()
	return t, nil
}

func applyBlogThemeOnTarget(t chromeJSONTarget, cfg profileSetupConfig, headerPath string) error {
	headerAbs := ""
	if strings.TrimSpace(headerPath) != "" {
		abs, err := imageFromDir(headerPath, backgroundPicsDir, "banner")
		if err != nil {
			return err
		}
		headerAbs = abs
	}
	return withChromeTargetCDPFor(t, 90*time.Second, func(call cdpCall) error {
		if _, err := call("Page.enable", nil); err != nil {
			return err
		}
		_, _ = call("Page.setInterceptFileChooserDialog", map[string]any{"enabled": true})
		for i := 0; i < 6; i++ {
			if hit := cdpDismissOverlays(call); hit != "" {
				fmt.Println("Dismissed:", hit)
				time.Sleep(300 * time.Millisecond)
				continue
			}
			break
		}
		time.Sleep(400 * time.Millisecond)
		if err := cdpEnsureEditAppearance(call); err != nil {
			return err
		}
		time.Sleep(800 * time.Millisecond)
		if hit := cdpDismissOverlays(call); hit != "" {
			fmt.Println("Dismissed:", hit)
			time.Sleep(300 * time.Millisecond)
		}
		raw, err := call("Runtime.evaluate", map[string]any{"expression": chromeFillTitleBioJS(cfg.Title, cfg.Description), "returnByValue": true})
		if err != nil {
			return err
		}
		if fill := cdpEvalString(raw); fill != "" {
			fmt.Println("Filled:", fill)
		}
		for _, label := range []string{"Accent", "Background"} {
			x, y, ok, _ := cdpEvalBox(call, chromeColorSwatchBoxJS(label))
			if !ok {
				continue
			}
			fmt.Println("Clicked:", label, "swatch")
			_ = cdpDispatchClick(call, x, y)
			time.Sleep(500 * time.Millisecond)
			color := cfg.Accent
			if strings.EqualFold(label, "Background") {
				color = cfg.Background
			}
			hexRaw, _ := call("Runtime.evaluate", map[string]any{"expression": chromeFillOpenHexJS(color), "returnByValue": true, "userGesture": true})
			if hex := cdpEvalString(hexRaw); hex != "" {
				fmt.Println("Set", label, color)
			}
		}
		raw, err = call("Runtime.evaluate", map[string]any{"expression": chromeSetThemeColorsJS(cfg.Accent, cfg.Background), "returnByValue": true})
		if err != nil {
			return err
		}
		if colors := cdpEvalString(raw); colors != "" {
			fmt.Println("Colors:", colors, cfg.Accent, cfg.Background)
		} else {
			fmt.Println("Color inputs not found")
		}
		raw, err = call("Runtime.evaluate", map[string]any{"expression": chromeFillTitleBioJS(cfg.Title, cfg.Description), "returnByValue": true})
		if err != nil {
			return err
		}
		if fill := cdpEvalString(raw); fill != "" {
			fmt.Println("Re-filled:", fill)
		}
		if headerAbs == "" {
			return nil
		}
		return trySetHeaderFile(call, headerAbs)
	})
}

func trySetHeaderFile(call cdpCall, headerAbs string) error {
	enable := func() {
		raw, err := call("Runtime.evaluate", map[string]any{"expression": chromeEnableShowHeaderJS, "returnByValue": true})
		if err != nil {
			return
		}
		if s := cdpEvalString(raw); s != "" && s != "no-toggle" {
			fmt.Println("Header toggle:", s)
		}
	}
	enable()
	time.Sleep(400 * time.Millisecond)
	if err := cdpSetImageFile(call, chromeHeaderBoxJS, headerAbs, "Edit blog header image"); err == nil {
		enable() // crop panel may expose the checkbox again
		return nil
	} else {
		fmt.Println("Banner pencil:", err)
	}
	enable()
	time.Sleep(400 * time.Millisecond)
	if err := cdpSetImageFile(call, chromeHeaderCenterJS, headerAbs, "Edit blog header image"); err == nil {
		enable()
		return nil
	} else {
		fmt.Println("Banner center:", err)
	}
	return fmt.Errorf("banner chooser did not open; refusing to use the avatar input")
}

type blogLookPageStatus struct {
	Taken        bool   `json:"taken"`
	Href         string `json:"href"`
	Accent       string `json:"accent"`
	Background   string `json:"background"`
	SwatchAccent string `json:"swatchAccent"`
	SwatchBg     string `json:"swatchBg"`
	PreviewBg    string `json:"previewBg"`
	Header       string `json:"header"`
	AccentOK     bool   `json:"accentOK"`
	BackgroundOK bool   `json:"backgroundOK"`
	BannerOK     bool   `json:"bannerOK"`
	UserOK       bool   `json:"userOK"`
}

func (s blogLookPageStatus) colorsOK() bool {
	return s.AccentOK && s.BackgroundOK
}

func (s blogLookPageStatus) lookOK() bool {
	return s.colorsOK() && s.BannerOK
}

func savedThemeStatus(p BlogProfile, cfg profileSetupConfig) blogLookPageStatus {
	header := strings.TrimSpace(p.HeaderImage)
	return blogLookPageStatus{
		Accent: p.AccentColor, Background: p.BackgroundColor, Header: header,
		AccentOK:     strings.EqualFold(p.AccentColor, cfg.Accent),
		BackgroundOK: strings.EqualFold(p.BackgroundColor, cfg.Background),
		BannerOK:     p.ShowHeaderImage && strings.HasPrefix(header, "https://") && !strings.Contains(header, "default_header"),
	}
}
func themeLooksDone(st blogLookPageStatus, themeApplied bool) bool {
	// A pre-existing custom banner (including the wrongly uploaded avatar)
	// must not make this run look successful. applyBlogThemeOnTarget only
	// returns nil after the dedicated header chooser accepted the banner.
	return themeApplied && st.BannerOK && st.colorsOK()
}

func readBlogLookStatus(t chromeJSONTarget, cfg profileSetupConfig, wantUser string) blogLookPageStatus {
	var st blogLookPageStatus
	if err := evalJSONOnChromeTarget(t, chromeBlogLookStatusJS(cfg.Accent, cfg.Background, wantUser), &st); err != nil {
		return st
	}
	return st
}

func cancelUsernameRow(t chromeJSONTarget) {
	var cancel string
	_ = evalJSONOnChromeTarget(t, chromeUsernameRowCancelJS, &cancel)
	if cancel != "" {
		fmt.Println("Clicked:", cancel)
		time.Sleep(600 * time.Millisecond)
	}
}

func shouldFillUsernameField(opened string) bool {
	switch strings.ToLower(strings.TrimSpace(opened)) {
	case "already", "change", "pencil", "row-pencil":
		return true
	default:
		return false
	}
}

func changeBlogUsernameOnTarget(t chromeJSONTarget, want string) string {
	want = normalizeBlogName(want)
	if want == "" {
		return "skip"
	}
	dismissOverlaysOnPort()
	var opened string
	if err := evalJSONOnChromeTarget(t, chromeOpenUsernameJS(), &opened); err != nil {
		fmt.Println("Username:", err)
		return "error"
	}
	if opened != "" && opened != "already" {
		fmt.Println("Clicked username:", opened)
		time.Sleep(1200 * time.Millisecond)
	}
	if !shouldFillUsernameField(opened) {
		fmt.Println("Username editor not open — leaving other fields alone.")
		return "missing"
	}
	var filled string
	if err := evalJSONOnChromeTarget(t, chromeFillUsernameJS(want), &filled); err != nil {
		fmt.Println("Username fill:", err)
		return "error"
	}
	if filled == "" {
		fmt.Println("Username field not found")
		return "missing"
	}
	fmt.Println("Username filled:", want)
	deadline := time.Now().Add(6 * time.Second)
	var status string
	for time.Now().Before(deadline) {
		_ = evalJSONOnChromeTarget(t, chromeUsernameStatusJS(want), &status)
		if status == "taken" {
			fmt.Println("Username taken:", want)
			cancelUsernameRow(t)
			return "taken"
		}
		time.Sleep(400 * time.Millisecond)
	}
	if status == "taken" {
		cancelUsernameRow(t)
		return "taken"
	}
	var save string
	_ = evalJSONOnChromeTarget(t, chromeUsernameRowSaveJS, &save)
	if save != "" {
		fmt.Println("Clicked:", save)
		time.Sleep(700 * time.Millisecond)
	}
	var confirm string
	_ = evalJSONOnChromeTarget(t, chromeConfirmUsernameJS, &confirm)
	if confirm != "" {
		fmt.Println("Clicked:", confirm)
		save = confirm
	}
	deadline = time.Now().Add(8 * time.Second)
	for time.Now().Before(deadline) {
		_ = evalJSONOnChromeTarget(t, chromeUsernameStatusJS(want), &status)
		if status == "taken" {
			fmt.Println("Username taken after save:", want)
			cancelUsernameRow(t)
			return "taken"
		}
		if status == "ok" {
			return "ok"
		}
		time.Sleep(500 * time.Millisecond)
	}
	if status == "" {
		status = "pending"
	}
	// Do not click Cancel merely because Tumblr's SPA URL is slow to update.
	// The caller checks user/info next; cancelling here undid valid renames.
	return status
}

func skipBlogLook(reason string) error {
	fmt.Println(reason, "— continuing.")
	closeExtraSetupTabs()
	return nil
}

func runBlogLook() error {
	cfg := loadProfileSetupConfig()
	auth, err := loadAuth()
	if err != nil {
		return err
	}
	fmt.Println("Updating username, banner, and theme colors…")
	nativeChromeClicks = false
	name, err := syncAuthIdentity(newHumanClient(), &auth)
	if err != nil || !usableBlogName(name) {
		return fmt.Errorf("cannot verify account identity before appearance update: %v", err)
	}
	if attachAccountDebugPort() {
		// Only reuse a browser already showing this account's exact settings page.
		list, err := listChromeTargets()
		if err != nil {
			return err
		}
		matched := false
		for _, t := range list {
			if t.Type == "page" && strings.TrimRight(t.URL, "/") == "https://www.tumblr.com/settings/blog/"+name {
				matched = true
				break
			}
		}
		if !matched {
			return fmt.Errorf("existing Chrome does not show verified settings for @%s", name)
		}
		fmt.Println("Reusing existing Chrome settings for @" + name)
	} else {
		cmd, err := startChromeLoggedIn(&auth)
		if err != nil {
			return err
		}
		defer func() { stopChrome(cmd) }()
	}
	return applyBlogLookInOpenChrome(name, cfg)
}

func applyBlogLookInOpenChrome(name string, cfg profileSetupConfig) error {
	return applyBlogLookParts(name, cfg, true)
}

// applyBlogLookParts sets banner + colours (when doTheme and not already
// saved) and then the username.
func applyBlogLookParts(name string, cfg profileSetupConfig, doTheme bool) error {
	if !usableBlogName(name) {
		return skipBlogLook("Could not detect blog name")
	}
	banners, err := listBannerPics()
	if err != nil {
		return err
	}
	header := bannerForCurrentAccount(banners)
	auth, err := loadAuth()
	if err != nil {
		return err
	}
	accID := ""
	if acc, ok := currentAccount(); ok {
		accID = acc.ID
	}
	want := pickZoeyUsername(accID, name)
	fmt.Println("Blog:", name)
	skipRename := usernameAlreadyOnBrand(name)
	if skipRename {
		fmt.Println("Username already Zoey/cosmic related — leaving the name.")
	} else {
		fmt.Println("New username:", want)
	}
	fmt.Println("Banner:", filepath.ToSlash(header))
	fmt.Println("Accent:", cfg.Accent, "Background:", cfg.Background)

	t, err := openBlogSettingsTab(name)
	if err != nil {
		return skipBlogLook("Could not open blog settings")
	}
	themeOK := !doTheme
	if doTheme {
		if p, err := fetchBlogProfile(newHumanClient(), auth, name); err == nil {
			if st := savedThemeStatus(p, cfg); st.BannerOK && st.AccentOK && st.BackgroundOK {
				fmt.Println("Banner and colours already saved — skipping them.")
				themeOK = true
			}
		}
	}
	colorsApplied := false
	for i := 0; i < 2 && !themeOK; i++ {
		dismissOverlaysOnPort()
		if err := applyBlogThemeOnTarget(t, cfg, header); err != nil {
			fmt.Println("Theme:", err)
			continue
		} else {
			colorsApplied = true
		}
		dismissOverlaysOnPort()
		if err := clickAppearanceSaveOnTarget(t); err != nil {
			fmt.Println("Save:", err)
			continue
		}
		// Verify persisted server values, not an unsaved preview or generic swatch.
		var st blogLookPageStatus
		deadline := time.Now().Add(10 * time.Second)
		for time.Now().Before(deadline) {
			p, err := fetchBlogProfile(newHumanClient(), auth, name)
			if err != nil {
				return fmt.Errorf("verify saved theme: %w", err)
			}
			st = savedThemeStatus(p, cfg)
			if themeLooksDone(st, colorsApplied) {
				break
			}
			time.Sleep(time.Second)
		}
		fmt.Printf("Look check: accent=%s background=%s banner=%s (accent=%s bg=%s preview=%s header=%s)\n",
			setupFlag(st.AccentOK), setupFlag(st.BackgroundOK), setupFlag(st.BannerOK),
			st.Accent, st.Background, st.PreviewBg, st.Header)
		if themeLooksDone(st, colorsApplied) {
			themeOK = true
			break
		}
	}
	if !themeOK {
		fmt.Println("Banner/colors did not confirm after retries — continuing with username and account settings.")
	}

	if !skipRename && want != "" && normalizeBlogName(want) != normalizeBlogName(name) {
		t, err = openBlogSettingsTab(name)
		if err != nil {
			return skipBlogLook("Could not reopen settings for username")
		}
		renamed := false
		for _, cand := range usernameVariants(want) {
			if usedZoeyUsernames(accID)[cand] {
				continue
			}
			dismissOverlaysOnPort()
			fmt.Println("Trying username:", cand)
			status := changeBlogUsernameOnTarget(t, cand)
			if status == "taken" {
				fmt.Println("Username taken:", cand, "— twisting it")
				markUsernameTaken(cand)
				continue
			}
			if status == "missing" || status == "error" {
				fmt.Println("Username status:", status)
				break
			}
			time.Sleep(1500 * time.Millisecond)
			if newName, err := syncAuthIdentity(newHumanClient(), &auth); err == nil && usableBlogName(newName) {
				name = newName
				setupBlogName = newName
			}
			st := readBlogLookStatus(t, cfg, cand)
			if st.UserOK || normalizeBlogName(name) == cand || status == "ok" {
				assigned := loadNameMap(assignedUsernamesFile)
				assigned[accID] = cand
				saveNameMap(assignedUsernamesFile, assigned)
				fmt.Println("Username now:", name)
				renamed = true
				break
			}
			fmt.Println("Username did not stick:", cand, "blog still", name)
		}
		if !renamed {
			fmt.Println("Username did not confirm after retries.")
		}
	}
	if !themeOK {
		return fmt.Errorf("saved banner or colors did not verify")
	}
	closeExtraSetupTabs()
	return nil
}

func bioHasColorLeak(s string) bool {
	u := strings.ToUpper(s)
	return strings.Contains(u, "#000000") || strings.Contains(u, "#7C3AED")
}

func stripColorLeak(s string) string {
	out := s
	for _, h := range []string{"#000000", "#7C3AED", "#7c3aed", "#7C3aed"} {
		out = strings.ReplaceAll(out, h, "")
	}
	return strings.TrimSpace(out)
}

const chromeReadBioJS = `(() => {
	const el = document.querySelector('textarea[aria-label="Blog description"]');
	if (el) return el.value || '';
	return ((document.body && document.body.innerText) || '');
})()`

func readAppearanceBio(t chromeJSONTarget) string {
	var bio string
	_ = evalJSONOnChromeTarget(t, chromeReadBioJS, &bio)
	return bio
}

func runFixColorHashBio() error {
	cfg := loadProfileSetupConfig()
	auth, err := loadAuth()
	if err != nil {
		return err
	}
	fmt.Println("Checking blog description for leaked colour hashes…")
	nativeChromeClicks = false
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
		return skipBlogLook("Could not detect blog name")
	}
	fmt.Println("Blog:", name)

	t, err := openBlogSettingsTab(name)
	if err != nil {
		return skipBlogLook("Could not open blog settings")
	}
	dismissOverlaysOnPort()
	var edit string
	_ = evalJSONOnChromeTarget(t, chromeClickEditAppearanceJS, &edit)
	if edit != "" {
		fmt.Println("Clicked:", edit)
		time.Sleep(1500 * time.Millisecond)
	}
	dismissOverlaysOnPort()

	bio := readAppearanceBio(t)
	if !bioHasColorLeak(bio) {
		fmt.Println("Bio is already clean.")
		closeExtraSetupTabs()
		return nil
	}
	fmt.Println("Bio had colour hashes — removing them.")
	want := stripColorLeak(bio)
	if want == "" || bioHasColorLeak(want) {
		want = cfg.Description
	}

	for i := 0; i < 3; i++ {
		dismissOverlaysOnPort()
		var opened string
		_ = evalJSONOnChromeTarget(t, chromeClickEditAppearanceJS, &opened)
		if opened != "" {
			fmt.Println("Clicked:", opened)
			time.Sleep(1200 * time.Millisecond)
		}
		var fill string
		if err := evalJSONOnChromeTarget(t, chromeFillTitleBioJS(cfg.Title, want), &fill); err != nil {
			fmt.Println("Fill:", err)
		} else if fill != "" {
			fmt.Println("Filled:", fill)
		}
		if err := clickAppearanceSaveOnTarget(t); err != nil {
			fmt.Println("Save:", err)
			continue
		}
		time.Sleep(2 * time.Second)
		bio = readAppearanceBio(t)
		if !bioHasColorLeak(bio) {
			fmt.Println("Bio hashes removed.")
			closeExtraSetupTabs()
			return nil
		}
	}
	return skipBlogLook("Bio hashes still present")
}
