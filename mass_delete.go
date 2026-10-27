package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

const (
	wipePageSize        = 100 // match mega-editor select limit
	wipeBulkLimit       = 100 // POST /delete_posts accepts at most 100 ids
	wipeDeleteWorkers   = 4   // fallback when bulk endpoint rejects
	wipeDefaultParallel = 10
	wipeMaxParallel     = 12
	wipeSafetyLimit     = 12000 // covers large pre-Zoey blogs (e.g. 6k+ posts)
)

var (
	megaEditorFormKeyRes = []*regexp.Regexp{
		regexp.MustCompile(`(?i)user_form_key\s*=\s*["']([^"']+)["']`),
		regexp.MustCompile(`(?i)"formKey"\s*:\s*"([^"]+)"`),
		regexp.MustCompile(`(?i)"form_key"\s*:\s*"([^"]+)"`),
		regexp.MustCompile(`(?i)name=["']form_key["']\s+value=["']([^"']+)["']`),
		regexp.MustCompile(`(?i)value=["']([^"']+)["']\s+name=["']form_key["']`),
	}
)

// wipePublishedPosts deletes published posts on the account's primary blog.
// When keepPinned is true, posts flagged is_pinned/pinned are left alone.
// Prefers mega-editor bulk DELETE (/delete_posts, up to 100 ids per request).
func wipePublishedPosts(client *http.Client, auth *Auth, accountID string, keepPinned, dryRun bool) (deleted, skipped, failed int, err error) {
	if auth == nil {
		return 0, 0, 0, fmt.Errorf("missing auth")
	}
	tag := accountSlug(accountID)
	if tag == "" {
		tag = "?"
	}
	logf := func(format string, args ...any) {
		fmt.Printf("["+tag+"] "+format+"\n", args...)
	}
	if err := ensureSession(client, auth); err != nil {
		return 0, 0, 0, err
	}
	name, err := syncAuthIdentity(client, auth)
	if err != nil || !usableBlogName(name) {
		if usableBlogName(auth.Blog) {
			name = auth.Blog
		} else {
			return 0, 0, 0, fmt.Errorf("no blog name: %v", err)
		}
	}
	blog := normalizeBlogName(name)
	formKey, src, fkErr := scrapeMegaEditorFormKey(client, auth, blog)
	if fkErr != nil {
		logf("form_key scrape: %v — will fall back to per-post delete", fkErr)
	} else {
		logf("form_key ready (%s)", src)
	}
	logf("wiping @%s (keepPinned=%v dryRun=%v bulk=%d page=%d)", blog, keepPinned, dryRun, wipeBulkLimit, wipePageSize)

	page := wipePageSize
	seen := map[string]bool{}
	emptyPages := 0
	var csrfMu sync.Mutex
	var delN, skipN, failN atomic.Int64

	for offset := 0; emptyPages < 3; {
		raw, err := fetchBlogPostsPage(client, auth, blog, page, offset)
		if err != nil {
			return int(delN.Load()), int(skipN.Load()), int(failN.Load()), err
		}
		if len(raw) == 0 {
			emptyPages++
			offset += page
			continue
		}
		emptyPages = 0

		var batch []string
		progress := false
		for _, p := range raw {
			id := postIDFromMap(p)
			if id == "" || seen[id] {
				continue
			}
			// Ads / native placements mint fresh IDs every fetch and 404 on
			// delete — skipping them avoids an infinite wipe spin.
			if wipeIgnorablePostID(id) {
				continue
			}
			seen[id] = true
			progress = true
			if keepPinned && postIsPinned(p) {
				logf("keep pinned %s", id)
				skipN.Add(1)
				continue
			}
			batch = append(batch, id)
		}
		if !progress {
			offset += page
			continue
		}

		beforeDel := delN.Load()
		formKey = wipeDeleteBatch(client, auth, blog, batch, formKey, dryRun, &csrfMu, &delN, &failN, logf)

		if dryRun {
			offset += page
		} else if delN.Load() == beforeDel {
			// Whole batch undeletable (already gone / forbidden) — advance
			// instead of resetting to 0 and spinning on the same page.
			offset += page
		} else {
			offset = 0
		}
		if delN.Load()+skipN.Load()+failN.Load() >= wipeSafetyLimit {
			return int(delN.Load()), int(skipN.Load()), int(failN.Load()), fmt.Errorf("safety stop after %d actions", wipeSafetyLimit)
		}
	}
	return int(delN.Load()), int(skipN.Load()), int(failN.Load()), nil
}

// wipeIgnorablePostID skips feed placements that aren't real blog posts.
func wipeIgnorablePostID(id string) bool {
	id = strings.ToLower(strings.TrimSpace(id))
	if id == "" {
		return true
	}
	return strings.HasPrefix(id, "google_native_")
}

// wipeDeleteBatch deletes ids via mega-editor /delete_posts (chunks of 100).
// Returns the (possibly refreshed) formKey for the next batch. Empty formKey
// or a bulk failure falls back to per-post /post/delete workers.
func wipeDeleteBatch(
	client *http.Client,
	auth *Auth,
	blog string,
	ids []string,
	formKey string,
	dryRun bool,
	csrfMu *sync.Mutex,
	delN, failN *atomic.Int64,
	logf func(string, ...any),
) string {
	if len(ids) == 0 {
		return formKey
	}
	if dryRun {
		logf("would bulk-delete %d posts", len(ids))
		delN.Add(int64(len(ids)))
		return formKey
	}
	for start := 0; start < len(ids); start += wipeBulkLimit {
		end := start + wipeBulkLimit
		if end > len(ids) {
			end = len(ids)
		}
		chunk := ids[start:end]
		if formKey == "" {
			if k, src, err := scrapeMegaEditorFormKey(client, auth, blog); err == nil {
				formKey = k
				logf("form_key ready (%s)", src)
			}
		}
		if formKey != "" {
			err := deleteBlogPostsBulk(client, auth, blog, chunk, formKey, csrfMu)
			if err == nil {
				logf("bulk deleted %d", len(chunk))
				delN.Add(int64(len(chunk)))
				continue
			}
			// Stale form_key — rescrape once, then fall back to singles.
			if k, src, scrapeErr := scrapeMegaEditorFormKey(client, auth, blog); scrapeErr == nil && k != "" && k != formKey {
				formKey = k
				logf("form_key refreshed (%s), retry bulk", src)
				if err2 := deleteBlogPostsBulk(client, auth, blog, chunk, formKey, csrfMu); err2 == nil {
					logf("bulk deleted %d", len(chunk))
					delN.Add(int64(len(chunk)))
					continue
				} else {
					err = err2
				}
			}
			logf("bulk failed (%v) — per-post fallback for %d", err, len(chunk))
		}
		wipeDeleteBatchSingles(client, auth, blog, chunk, csrfMu, delN, failN, logf)
	}
	return formKey
}

func wipeDeleteBatchSingles(
	client *http.Client,
	auth *Auth,
	blog string,
	ids []string,
	csrfMu *sync.Mutex,
	delN, failN *atomic.Int64,
	logf func(string, ...any),
) {
	workers := wipeDeleteWorkers
	if workers > len(ids) {
		workers = len(ids)
	}
	if workers < 1 {
		workers = 1
	}
	ch := make(chan string, len(ids))
	for _, id := range ids {
		ch <- id
	}
	close(ch)

	var wg sync.WaitGroup
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for id := range ch {
				err := deleteBlogPostGuarded(client, auth, blog, id, csrfMu)
				if err != nil {
					logf("delete %s: %v", id, err)
					failN.Add(1)
					time.Sleep(250 * time.Millisecond)
					continue
				}
				logf("deleted %s", id)
				delN.Add(1)
			}
		}()
	}
	wg.Wait()
}

// scrapeMegaEditorFormKey pulls user_form_key from the classic mega-editor page
// (required by POST /delete_posts).
func scrapeMegaEditorFormKey(client *http.Client, auth *Auth, blog string) (key, source string, err error) {
	if client == nil || auth == nil {
		return "", "", fmt.Errorf("missing client/auth")
	}
	u := "https://www.tumblr.com/mega-editor/published/" + normalizeBlogName(blog)
	req, err := http.NewRequest(http.MethodGet, u, nil)
	if err != nil {
		return "", "", err
	}
	setHTMLHeaders(req, *auth)
	req.Header.Set("Referer", "https://www.tumblr.com/blog/"+normalizeBlogName(blog))
	resp, err := client.Do(req)
	if err != nil {
		return "", "", err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 2<<20))
	if err != nil {
		return "", "", err
	}
	applyCSRF(resp.Header, auth, nil)
	html := string(body)
	for i, re := range megaEditorFormKeyRes {
		if m := re.FindStringSubmatch(html); len(m) > 1 && m[1] != "" {
			return m[1], fmt.Sprintf("html#%d", i), nil
		}
	}
	// Do not fall back to X-CSRF — /delete_posts wants classic user_form_key.
	return "", "", fmt.Errorf("status=%d no form_key in %d bytes", resp.StatusCode, len(body))
}

// deleteBlogPostsBulk hits classic mega-editor POST /delete_posts with up to
// wipeBulkLimit comma-separated ids (Tumblr's UI hard-caps at 100).
func deleteBlogPostsBulk(client *http.Client, auth *Auth, blog string, ids []string, formKey string, mu *sync.Mutex) error {
	if auth == nil {
		return fmt.Errorf("missing auth")
	}
	if len(ids) == 0 {
		return nil
	}
	if len(ids) > wipeBulkLimit {
		return fmt.Errorf("bulk delete capped at %d ids (got %d)", wipeBulkLimit, len(ids))
	}
	formKey = strings.TrimSpace(formKey)
	if formKey == "" {
		return fmt.Errorf("missing form_key")
	}
	snapCSRF := func() string {
		if mu != nil {
			mu.Lock()
			defer mu.Unlock()
		}
		return auth.CSRF
	}
	form := url.Values{}
	form.Set("post_ids", strings.Join(ids, ","))
	form.Set("form_key", formKey)
	req, err := http.NewRequest(http.MethodPost, "https://www.tumblr.com/delete_posts", strings.NewReader(form.Encode()))
	if err != nil {
		return err
	}
	clearRequestHeaders(req)
	fp := fingerprint()
	setHeaderOrder(req,
		"User-Agent", fp.UA,
		"Accept", "*/*",
		"Content-Type", "application/x-www-form-urlencoded; charset=UTF-8",
		"Origin", "https://www.tumblr.com",
		"X-Requested-With", "XMLHttpRequest",
		"Sec-Fetch-Site", "same-origin",
		"Sec-Fetch-Mode", "cors",
		"Sec-Fetch-Dest", "empty",
		"Accept-Encoding", chromeAcceptEncoding,
		"Accept-Language", fp.AcceptLanguage,
		"Cookie", matureCookieHeader(auth.Cookie),
	)
	req.Header.Set("Referer", "https://www.tumblr.com/mega-editor/published/"+normalizeBlogName(blog))
	if csrf := snapCSRF(); csrf != "" {
		req.Header.Set("X-CSRF", csrf)
	}
	req.Header.Set("X-tumblr-form-key", formKey)

	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	applyCSRF(resp.Header, auth, mu)
	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return err
	}
	if resp.StatusCode >= 200 && resp.StatusCode < 300 {
		return nil
	}
	return fmt.Errorf("%s: %s", resp.Status, truncate(string(body), 160))
}

func postIsPinned(m map[string]any) bool {
	for _, k := range []string{"is_pinned", "pinned", "isPinned"} {
		switch v := m[k].(type) {
		case bool:
			if v {
				return true
			}
		case string:
			if strings.EqualFold(v, "true") || v == "1" {
				return true
			}
		case float64:
			if v != 0 {
				return true
			}
		}
	}
	return false
}

func deleteBlogPost(client *http.Client, auth *Auth, blog, postID string) error {
	return deleteBlogPostGuarded(client, auth, blog, postID, nil)
}

// deleteBlogPostGuarded posts /post/delete (mega-editor Referer). When mu is
// set, CSRF read/write is synchronized so per-account delete workers can share
// one Auth without data races.
func deleteBlogPostGuarded(client *http.Client, auth *Auth, blog, postID string, mu *sync.Mutex) error {
	if auth == nil {
		return fmt.Errorf("missing auth")
	}
	blogID := blog
	if !strings.Contains(blogID, ".") && !strings.HasPrefix(blogID, "t:") {
		blogID = blogID + ".tumblr.com"
	}
	snapAuth := func() (Auth, string) {
		if mu != nil {
			mu.Lock()
			defer mu.Unlock()
		}
		return *auth, auth.CSRF
	}
	authCopy, csrf := snapAuth()
	// Redpop / API v2 delete (form body).
	form := url.Values{}
	form.Set("id", postID)
	u := "https://www.tumblr.com/api/v2/blog/" + url.PathEscape(blogID) + "/post/delete"
	req, err := http.NewRequest(http.MethodPost, u, strings.NewReader(form.Encode()))
	if err != nil {
		return err
	}
	setCommonHeaders(req, authCopy)
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("X-CSRF", csrf)
	req.Header.Set("Referer", "https://www.tumblr.com/mega-editor/published/"+normalizeBlogName(blog))

	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	applyCSRF(resp.Header, auth, mu)
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return err
	}
	if resp.StatusCode >= 200 && resp.StatusCode < 300 {
		return nil
	}

	// Fallback: JSON DELETE-style path some redpop builds use.
	payload, _ := json.Marshal(map[string]any{"id": postID})
	u2 := "https://www.tumblr.com/api/v2/blog/" + url.PathEscape(blogID) + "/posts/" + url.PathEscape(postID)
	req2, err := http.NewRequest(http.MethodDelete, u2, bytes.NewReader(payload))
	if err != nil {
		return fmt.Errorf("%s: %s", resp.Status, truncate(string(body), 160))
	}
	authCopy, csrf = snapAuth()
	setCommonHeaders(req2, authCopy)
	req2.Header.Set("Content-Type", "application/json; charset=utf8")
	req2.Header.Set("X-CSRF", csrf)
	req2.Header.Set("Referer", "https://www.tumblr.com/mega-editor/published/"+normalizeBlogName(blog))
	resp2, err := client.Do(req2)
	if err != nil {
		return fmt.Errorf("delete %s then fallback: %v (%s)", resp.Status, err, truncate(string(body), 120))
	}
	defer resp2.Body.Close()
	applyCSRF(resp2.Header, auth, mu)
	body2, _ := io.ReadAll(resp2.Body)
	if resp2.StatusCode >= 200 && resp2.StatusCode < 300 {
		return nil
	}
	return fmt.Errorf("%s / %s: %s | %s", resp.Status, resp2.Status, truncate(string(body), 100), truncate(string(body2), 100))
}

func applyCSRF(h http.Header, auth *Auth, mu *sync.Mutex) {
	if mu != nil {
		mu.Lock()
		defer mu.Unlock()
	}
	takeCSRF(h, auth)
}

func wipeOneAccount(a Account, keepPinned, dryRun bool) error {
	// loadAuthFrom sets auth.path so proxy/CSRF/saveAuth stay on this account
	// (peekAccountAuth leaves path empty and falls back to the active account).
	auth, err := loadAuthFrom(accountAuthPath(a.ID))
	if err != nil || !authHasSID(auth) {
		if sessionBlocksChromeRecovery(&auth) {
			fmt.Printf("[%s] skip: needs interactive login (run -login -account %s)\n", a.ID, a.ID)
			return nil
		}
		if headlessChrome {
			fmt.Printf("[%s] skip: no session\n", a.ID)
			return nil
		}
		fmt.Printf("[%s] no session — opening Chrome to login…\n", a.ID)
		auth = Auth{path: accountAuthPath(a.ID)}
	}
	client := newAuthClient(&auth)
	// Authenticated preflight before listing/deleting; Chrome recover only when
	// preflight fails and the account is not flagged needsInteractiveLogin.
	if err := preflightSession(client, &auth); err != nil {
		if isNeedsInteractiveLogin(err) {
			fmt.Printf("[%s] skip session: %v\n", a.ID, err)
			return nil
		}
		if !authHasSID(auth) || isCookieProblem(err) {
			if sessionBlocksChromeRecovery(&auth) {
				fmt.Printf("[%s] skip: needs interactive login (run -login -account %s)\n", a.ID, a.ID)
				return nil
			}
			if rec := recoverSession(client, &auth, !authHasSID(auth)); rec != nil {
				fmt.Printf("[%s] skip session: %v\n", a.ID, rec)
				return nil
			}
		} else {
			fmt.Printf("[%s] skip session: %v\n", a.ID, err)
			return nil
		}
	}
	del, skip, fail, err := wipePublishedPosts(client, &auth, a.ID, keepPinned, dryRun)
	// Wrong-cookie / dead session: when Chrome can be visible, force one login
	// and retry so leftover wipe lanes can actually finish.
	if err != nil && !headlessChrome && (isForeignBlogSession(err) || wipeSessionSoftSkip(err)) {
		fmt.Printf("[%s] recovering session for retry: %v\n", a.ID, err)
		if rec := recoverSession(client, &auth, true); rec == nil {
			del, skip, fail, err = wipePublishedPosts(client, &auth, a.ID, keepPinned, dryRun)
		} else {
			err = rec
		}
	}
	fmt.Printf("[%s] done deleted=%d kept=%d failed=%d\n", a.ID, del, skip, fail)
	if err != nil && wipeSessionSoftSkip(err) {
		fmt.Printf("[%s] skip session: %v\n", a.ID, err)
		return nil
	}
	return err
}

// wipeSessionSoftSkip keeps multi-account wipe moving when one account needs a
// manual captcha or still has a dead cookie after Chrome recovery.
func wipeSessionSoftSkip(err error) bool {
	if err == nil {
		return false
	}
	if isBadLogin(err) || isPasswordChangeRequired(err) || isTerminated(err) || isNeedsInteractiveLogin(err) {
		return true
	}
	s := strings.ToLower(err.Error())
	switch {
	case strings.Contains(s, "captcha"):
		return true
	case strings.Contains(s, "needs interactive login"):
		return true
	case strings.Contains(s, "csrf refresh failed"):
		return true
	case strings.Contains(s, "session expired"):
		return true
	case strings.Contains(s, "login did not complete"):
		return true
	case strings.Contains(s, "waiting for captcha"),
		strings.Contains(s, "captcha not cleared"),
		strings.Contains(s, "captcha/login blocked"):
		return true
	case strings.Contains(s, "no blog name"),
		strings.Contains(s, "missing auth"),
		strings.Contains(s, "not logged in"),
		strings.Contains(s, "skip session"):
		return true
	default:
		return false
	}
}

func wipeAccountList(fullySetup bool, onlyAccount string) ([]Account, error) {
	var accs []Account
	if fullySetup {
		accs = loggedInFullySetupAccounts()
	} else if only := strings.TrimSpace(onlyAccount); only != "" {
		a, ok := findAccount(only)
		if !ok {
			return nil, fmt.Errorf("unknown account %s", only)
		}
		accs = []Account{a}
	} else if setupProfileAccountsFile != "" && setupProfileAccountsFile != "config/setup-profile-accounts.json" {
		// -setup-accounts FILE reused for wipe retries / leftover lanes.
		accs = accountIDsFromFile(setupProfileAccountsFile, "Wipe")
	} else {
		// Every enabled account (not just the reach top-N pool).
		accs = enabledAccounts()
	}
	var ready []Account
	for _, a := range accs {
		auth, err := loadAuthFrom(accountAuthPath(a.ID))
		if err == nil && authHasSID(auth) {
			// Flagged accounts still queue — preflight may clear needsInteractiveLogin
			// if the cookie still works; Chrome recovery stays blocked.
			ready = append(ready, a)
			continue
		}
		if err == nil && sessionBlocksChromeRecovery(&auth) {
			fmt.Printf("[%s] skip: needs interactive login\n", a.ID)
			continue
		}
		if headlessChrome {
			fmt.Printf("[%s] skip: no session\n", a.ID)
			continue
		}
		// Visible pass: queue anyway — wipeOneAccount will open Chrome to mint sid.
		fmt.Printf("[%s] no session yet — will open Chrome\n", a.ID)
		ready = append(ready, a)
	}
	if len(ready) == 0 {
		return nil, fmt.Errorf("no logged-in accounts to wipe")
	}
	return ready, nil
}

func wipeParallelCap(requested, nAccounts int) int {
	parallel := requested
	if parallel <= 0 {
		parallel = wipeDefaultParallel
	}
	if parallel > wipeMaxParallel {
		parallel = wipeMaxParallel
	}
	if nAccounts > 0 && parallel > nAccounts {
		parallel = nAccounts
	}
	if parallel < 1 {
		parallel = 1
	}
	return parallel
}

func runWipePosts(keepPinned, dryRun, fullySetup bool, onlyAccount string, parallel int) error {
	// Dead sessions auto-relogin via recoverSession (serialized); keep native
	// clicks off so the login window can run without stealing the mouse focus fight.
	nativeChromeClicks = false
	prevQuiet := quietChromeLogin
	quietChromeLogin = true // shorter captcha budget; don't park the wipe pool
	defer func() { quietChromeLogin = prevQuiet }()
	boostProxyPaceForWipe()
	accs, err := wipeAccountList(fullySetup, onlyAccount)
	if err != nil {
		return err
	}
	parallel = wipeParallelCap(parallel, len(accs))
	fmt.Printf("Mass-delete published posts on %d account(s) parallel=%d bulk=%d/request page=%d (keepPinned=%v dryRun=%v)\n",
		len(accs), parallel, wipeBulkLimit, wipePageSize, keepPinned, dryRun)
	if parallel <= 1 {
		var first error
		for i, a := range accs {
			fmt.Printf("\n======== wipe posts: %s (%d/%d) ========\n", a.ID, i+1, len(accs))
			if err := wipeOneAccount(a, keepPinned, dryRun); err != nil && first == nil {
				first = err
			}
		}
		return first
	}
	// No enqueue stagger — fill all workers immediately.
	return runForAccountsParallelLimitedStagger("wipe posts", accs, parallel, 0, func(a Account) error {
		return wipeOneAccount(a, keepPinned, dryRun)
	})
}
