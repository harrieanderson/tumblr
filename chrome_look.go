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

func applyBlogLookOnTarget(t chromeJSONTarget, imagePath, title, desc string) error {
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
			const setter = Object.getOwnPropertyDescriptor(proto, 'value') || Object.getOwnPropertyDescriptor(window.HTMLInputElement.prototype, 'value');
			setter.set.call(el, value);
			el.dispatchEvent(new Event('input', { bubbles: true }));
			el.dispatchEvent(new Event('change', { bubbles: true }));
			return true;
		};
		const vis = (el) => el && el.getBoundingClientRect().width > 2 && el.getBoundingClientRect().height > 2;
		const titleEl = [...document.querySelectorAll('input, [contenteditable="true"]')].find(el => vis(el) && /title|name/i.test(el.getAttribute('aria-label') || el.name || ''));
		const descEl = [...document.querySelectorAll('textarea, [contenteditable="true"]')].find(el => vis(el) && /desc|bio/i.test(el.getAttribute('aria-label') || el.name || ''))
			|| [...document.querySelectorAll('textarea')].find(vis);
		const okTitle = setNative(titleEl, %s);
		const okDesc = setNative(descEl, %s);
		return (okTitle ? 'title ' : '') + (okDesc ? 'bio ' : '');
	})()`, string(titleJSON), string(descJSON))
	clickEditJS := `(() => {
		const vis = (el) => el && el.getBoundingClientRect().width > 2 && el.getBoundingClientRect().height > 2;
		const el = [...document.querySelectorAll('button, a, [role="button"]')].find(b => vis(b) && /edit appearance|изменить оформление/i.test((b.innerText || '').replace(/\s+/g,' ').trim()));
		if (!el) return '';
		el.click();
		return (el.innerText || '').trim();
	})()`
	avatarClickJS := `(() => {
		const el = document.querySelector('[aria-label="Edit blog avatar image"]');
		if (!el) return '';
		el.click();
		return 'Edit blog avatar image';
	})()`
	abs, err := filepath.Abs(imagePath)
	if err != nil {
		return err
	}
	return withChromeTargetCDP(t, func(call cdpCall) error {
		if _, err := call("Page.enable", nil); err != nil {
			return err
		}
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
		raw, err := call("Runtime.evaluate", map[string]any{"expression": clickEditJS, "returnByValue": true})
		if err != nil {
			return err
		}
		edit := cdpEvalString(raw)
		if edit == "" {
			return fmt.Errorf("edit appearance button not found")
		}
		fmt.Println("Clicked:", edit)
		time.Sleep(2 * time.Second)
		raw, err = call("Runtime.evaluate", map[string]any{"expression": fillJS, "returnByValue": true})
		if err != nil {
			return err
		}
		fmt.Println("Filled:", cdpEvalString(raw))
		raw, err = call("Runtime.evaluate", map[string]any{"expression": avatarClickJS, "returnByValue": true})
		if err != nil {
			return err
		}
		if avatar := cdpEvalString(raw); avatar != "" {
			fmt.Println("Clicked:", avatar)
			time.Sleep(1200 * time.Millisecond)
		}
		if _, err := call("DOM.enable", nil); err != nil {
			return err
		}
		docRaw, err := call("DOM.getDocument", map[string]any{"depth": 0})
		if err != nil {
			return err
		}
		var doc struct {
			Root struct {
				NodeID int `json:"nodeId"`
			} `json:"root"`
		}
		if err := json.Unmarshal(docRaw, &doc); err != nil {
			return err
		}
		filesRaw, err := call("DOM.querySelectorAll", map[string]any{"nodeId": doc.Root.NodeID, "selector": "input[type=file]"})
		if err != nil {
			return err
		}
		ids := cdpNodeIDs(filesRaw)
		if len(ids) == 0 {
			return fmt.Errorf("no file picker")
		}
		if _, err := call("DOM.setFileInputFiles", map[string]any{"nodeId": ids[len(ids)-1], "files": []string{abs}}); err != nil {
			return err
		}
		fmt.Println("Avatar file set")
		time.Sleep(2 * time.Second)
		for i := 0; i < 5; i++ {
			raw, err = call("Runtime.evaluate", map[string]any{"expression": chromeSaveAppearanceJS, "returnByValue": true})
			if err != nil {
				return err
			}
			if saved := cdpEvalString(raw); saved != "" {
				fmt.Println("Clicked:", saved)
			}
			time.Sleep(800 * time.Millisecond)
		}
		time.Sleep(1500 * time.Millisecond)
		return nil
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
	auth, err := loadAuth()
	if err != nil {
		return err
	}
	name, err := syncAuthIdentity(newHumanClient(), &auth)
	if err != nil || !usableBlogName(name) {
		name = resolveSetupBlogName(auth, name)
	}
	if !usableBlogName(name) {
		return fmt.Errorf("could not detect blog name")
	}
	if look := lookStatusFromAPI(name); look.Complete() {
		fmt.Println("Name, bio, and avatar already set.")
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
	}
	if t.ID == "" {
		var err error
		t, err = openChromeTab(settings)
		if err != nil {
			return err
		}
	}
	return applyBlogLookOnTarget(t, pic, cfg.Title, cfg.Description)
}
