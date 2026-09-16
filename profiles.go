package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"
)

const (
	activeAccountFile        = "config/active-account.json"
	profilesAuthDir          = "config/profiles"
	profilesDataDir          = "data/profiles"
	ageVerifyAccountsFile    = "config/age-verify-accounts.json"
	setupProfileAccountsFile = "config/setup-profile-accounts.json"
)

type Account struct {
	ID       string `json:"id"`
	Email    string `json:"email"`
	Password string `json:"password"`
	Enabled  *bool  `json:"enabled,omitempty"`
}

type credsFile struct {
	Email         string    `json:"email"`
	Password      string    `json:"password"`
	AIAPIKey      string    `json:"aiApiKey"`
	GroqAPIKey    string    `json:"groqApiKey"`
	GeminiAPIKey  string    `json:"geminiApiKey"`
	OpenRouterKey string    `json:"openrouterApiKey"`
	Accounts      []Account `json:"accounts"`
}

type activeAccountState struct {
	ID string `json:"id"`
}

var (
	accountMu       sync.Mutex
	activeAccountID string
	runAllAccounts  bool
	slugSafe        = regexp.MustCompile(`[^a-z0-9]+`)
	managedBlogs    sync.Map // lowercase blog name → login account id
)

type badLoginError struct {
	Email string
	ID    string
}

func (e badLoginError) Error() string {
	return fmt.Sprintf("Tumblr rejected email/password for %s", e.Email)
}

func isBadLogin(err error) bool {
	var b badLoginError
	return errors.As(err, &b)
}

func discardDeadLogin(email string) error {
	email = strings.TrimSpace(email)
	if email == "" {
		return nil
	}
	id := accountSlug(email)
	fmt.Println("Discarding", email, "— password was wrong. Removed from the project.")

	raw, err := os.ReadFile(credentialsFile)
	if err != nil {
		return err
	}
	var obj map[string]json.RawMessage
	if err := json.Unmarshal(raw, &obj); err != nil {
		return err
	}
	for k := range obj {
		if strings.EqualFold(strings.TrimSpace(k), email) {
			delete(obj, k)
		}
	}
	out, err := json.MarshalIndent(obj, "", "  ")
	if err != nil {
		return err
	}
	if err := os.WriteFile(credentialsFile, append(out, '\n'), 0600); err != nil {
		return err
	}
	_ = os.RemoveAll(filepath.Join(profilesAuthDir, id))
	_ = os.RemoveAll(filepath.Join(chromeProfilesDir(), id))
	_ = os.RemoveAll(filepath.Join(profilesDataDir, id))
	return nil
}

func (a Account) isEnabled() bool {
	return a.Enabled == nil || *a.Enabled
}

func accountSlug(email string) string {
	email = strings.TrimSpace(strings.ToLower(email))
	local := email
	if i := strings.Index(email, "@"); i >= 0 {
		local = email[:i]
	}
	s := slugSafe.ReplaceAllString(local, "")
	if s == "" {
		s = "account"
	}
	return s
}

func isReservedCredKey(key string) bool {
	switch strings.ToLower(strings.TrimSpace(key)) {
	case "email", "password", "aiapikey", "groqapikey", "geminiapikey", "openrouterapikey",
		"accounts", "logins", "passwords":
		return true
	default:
		return false
	}
}

func loadCredsFile() (credsFile, error) {
	raw, err := os.ReadFile(credentialsFile)
	if err != nil {
		return credsFile{}, err
	}
	var c credsFile
	if err := json.Unmarshal(raw, &c); err != nil {
		return credsFile{}, err
	}
	if dict := parseLoginDict(raw); len(dict) > 0 {
		c.Accounts = dict
	}
	return c, nil
}

func parseLoginDict(raw []byte) []Account {
	dec := json.NewDecoder(bytes.NewReader(raw))
	tok, err := dec.Token()
	if err != nil || tok != json.Delim('{') {
		return nil
	}
	return collectLoginPairs(dec)
}

func collectLoginPairs(dec *json.Decoder) []Account {
	enabled := true
	var out []Account
	for dec.More() {
		keyTok, err := dec.Token()
		if err != nil {
			return out
		}
		key, _ := keyTok.(string)
		var raw json.RawMessage
		if err := dec.Decode(&raw); err != nil {
			return out
		}
		lower := strings.ToLower(strings.TrimSpace(key))
		if lower == "logins" || lower == "passwords" {
			if len(raw) > 0 && raw[0] == '{' {
				sub := json.NewDecoder(bytes.NewReader(raw))
				t, err := sub.Token()
				if err == nil && t == json.Delim('{') {
					out = append(out, collectLoginPairs(sub)...)
				}
			}
			continue
		}
		if isReservedCredKey(key) {
			continue
		}
		var pass string
		if json.Unmarshal(raw, &pass) != nil {
			continue
		}
		email := strings.TrimSpace(key)
		pass = strings.TrimSpace(pass)
		if email == "" || pass == "" {
			continue
		}
		out = append(out, Account{
			ID:       accountSlug(email),
			Email:    email,
			Password: pass,
			Enabled:  &enabled,
		})
	}
	return out
}

func accountsFromCreds(c credsFile) []Account {
	if len(c.Accounts) > 0 {
		out := make([]Account, 0, len(c.Accounts))
		seen := map[string]int{}
		for _, a := range c.Accounts {
			a.Email = strings.TrimSpace(a.Email)
			a.Password = strings.TrimSpace(a.Password)
			if a.Email == "" {
				continue
			}
			if a.ID == "" {
				a.ID = accountSlug(a.Email)
			}
			a.ID = accountSlug(a.ID)
			if n := seen[a.ID]; n > 0 {
				a.ID = fmt.Sprintf("%s%d", a.ID, n+1)
			}
			seen[a.ID]++
			out = append(out, a)
		}
		return out
	}
	email := strings.TrimSpace(c.Email)
	pass := strings.TrimSpace(c.Password)
	if email == "" || pass == "" {
		return nil
	}
	enabled := true
	return []Account{{
		ID:       "default",
		Email:    email,
		Password: pass,
		Enabled:  &enabled,
	}}
}

func enabledAccounts() []Account {
	c, err := loadCredsFile()
	if err != nil {
		return nil
	}
	var out []Account
	for _, a := range accountsFromCreds(c) {
		if a.isEnabled() {
			out = append(out, a)
		}
	}
	return out
}

func allAccounts() []Account {
	c, err := loadCredsFile()
	if err != nil {
		return nil
	}
	return accountsFromCreds(c)
}

func findAccount(id string) (Account, bool) {
	id = accountSlug(id)
	for _, a := range allAccounts() {
		if a.ID == id {
			return a, true
		}
	}
	return Account{}, false
}

func rememberManagedBlog(accountID, blog string) {
	blog = strings.ToLower(strings.TrimSpace(strings.TrimPrefix(blog, "@")))
	if blog == "" {
		return
	}
	if accountID == "" {
		accountID = "ours"
	}
	managedBlogs.Store(blog, accountID)
}

func managedByUs(blog string) (accountID string, ok bool) {
	v, ok := managedBlogs.Load(strings.ToLower(strings.TrimSpace(strings.TrimPrefix(blog, "@"))))
	if !ok {
		return "", false
	}
	id, _ := v.(string)
	return id, true
}

func rememberBlogsFromAuth(client *http.Client, auth *Auth) {
	accID := accountIDFromAuth(*auth)
	names, primary, err := listAccountBlogs(client, *auth)
	if err != nil {
		if name, e2 := syncAuthIdentity(client, auth); e2 == nil {
			rememberManagedBlog(accID, name)
		}
		return
	}
	for _, n := range names {
		rememberManagedBlog(accID, n)
	}
	rememberManagedBlog(accID, primary)
}

func currentAccount() (Account, bool) {
	accountMu.Lock()
	id := activeAccountID
	accountMu.Unlock()
	if id == "" {
		return Account{}, false
	}
	return findAccount(id)
}

func usingIsolatedProfiles() bool {
	c, err := loadCredsFile()
	if err != nil {
		return false
	}
	return len(c.Accounts) > 0
}

func authFilePath() string {
	acc, ok := currentAccount()
	if !ok || !usingIsolatedProfiles() {
		if acc.ID == "default" || !ok {
			return authFile
		}
	}
	if !ok {
		return authFile
	}
	return filepath.Join(profilesAuthDir, acc.ID, "auth.json")
}

func chromeDataRoot() string {
	if p := strings.TrimSpace(os.Getenv("TUMBLR_CHROME_DATA")); p != "" {
		return p
	}
	if local := strings.TrimSpace(os.Getenv("LOCALAPPDATA")); local != "" {
		return filepath.Join(local, "tumblr", "chrome-data")
	}
	return chromeDataDir
}

func chromeProfilesDir() string {
	return filepath.Join(chromeDataRoot(), "profiles")
}

func chromeProfilePath() string {
	acc, ok := currentAccount()
	if !ok || !usingIsolatedProfiles() {
		return chromeDataRoot()
	}
	return filepath.Join(chromeProfilesDir(), acc.ID)
}

func accountDataFile(name string) string {
	acc, ok := currentAccount()
	if !ok || !usingIsolatedProfiles() {
		return filepath.Join("data", name)
	}
	return filepath.Join(profilesDataDir, acc.ID, name)
}

func filesForAccount(id string) accountFiles {
	id = strings.TrimSpace(id)
	if id == "" {
		if acc, ok := currentAccount(); ok {
			id = acc.ID
		}
	}
	if id == "" || !usingIsolatedProfiles() {
		return accountFiles{
			ID:        id,
			AuthPath:  authFilePath(),
			Followed:  filepath.Join("data", "followed.json"),
			Reblogged: filepath.Join("data", "reblogged.json"),
		}
	}
	return accountFiles{
		ID:        id,
		AuthPath:  accountAuthPath(id),
		Followed:  filepath.Join(profilesDataDir, id, "followed.json"),
		Reblogged: filepath.Join(profilesDataDir, id, "reblogged.json"),
	}
}

type accountFiles struct {
	ID        string
	AuthPath  string
	Followed  string
	Reblogged string
}

func followedPath() string {
	return accountDataFile("followed.json")
}

func rebloggedPath() string {
	return accountDataFile("reblogged.json")
}

func messagedPath() string {
	return accountDataFile("messaged.json")
}

func currentDebugPort() string {
	acc, ok := currentAccount()
	if !ok {
		return debugPort
	}
	return debugPortForAccount(acc.ID)
}

func debugPortForAccount(id string) string {
	id = strings.TrimSpace(id)
	if id == "" {
		return debugPort
	}
	for i, a := range allAccounts() {
		if a.ID == id {
			return fmt.Sprintf("%d", 9333+i)
		}
	}
	return debugPort
}

func ensureAccountDirs(id string) error {
	dirs := []string{
		filepath.Join(profilesAuthDir, id),
		filepath.Join(chromeProfilesDir(), id),
		filepath.Join(profilesDataDir, id),
	}
	for _, d := range dirs {
		if err := os.MkdirAll(d, 0755); err != nil {
			return err
		}
	}
	path := filepath.Join(profilesAuthDir, id, "auth.json")
	if _, err := os.Stat(path); os.IsNotExist(err) {
		empty := []byte("{\n  \"cookie\": \"\",\n  \"bearer\": \"\",\n  \"blog\": \"\",\n  \"csrf\": \"\"\n}\n")
		if err := os.WriteFile(path, empty, 0600); err != nil {
			return err
		}
	}
	return nil
}

func saveActiveAccountID(id string) error {
	if err := os.MkdirAll("config", 0755); err != nil {
		return err
	}
	raw, err := json.MarshalIndent(activeAccountState{ID: id}, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(activeAccountFile, raw, 0644)
}

func setActiveAccount(id string) error {
	id = strings.TrimSpace(id)
	if id == "" || strings.EqualFold(id, "all") {
		return fmt.Errorf("pick a specific account id")
	}
	acc, ok := findAccount(id)
	if !ok {
		return fmt.Errorf("unknown account %q — check config/credentials.json", id)
	}
	if usingIsolatedProfiles() {
		if err := ensureAccountDirs(acc.ID); err != nil {
			return err
		}
	}
	accountMu.Lock()
	activeAccountID = acc.ID
	accountMu.Unlock()
	return saveActiveAccountID(acc.ID)
}

func initAccounts() error {
	accs := allAccounts()
	if len(accs) == 0 {
		return nil
	}
	if usingIsolatedProfiles() {
		for _, a := range accs {
			if err := ensureAccountDirs(a.ID); err != nil {
				return err
			}
		}
	}

	want := ""
	if raw, err := os.ReadFile(activeAccountFile); err == nil {
		var st activeAccountState
		if json.Unmarshal(raw, &st) == nil {
			want = strings.TrimSpace(st.ID)
		}
	}
	if want != "" {
		if _, ok := findAccount(want); ok {
			return setActiveAccount(want)
		}
	}
	for _, a := range accs {
		if a.isEnabled() {
			return setActiveAccount(a.ID)
		}
	}
	return setActiveAccount(accs[0].ID)
}

func accountAuthPath(id string) string {
	if !usingIsolatedProfiles() {
		return authFile
	}
	return filepath.Join(profilesAuthDir, id, "auth.json")
}

func peekAccountAuth(id string) Auth {
	path := accountAuthPath(id)
	raw, err := os.ReadFile(path)
	if err != nil {
		return Auth{}
	}
	var auth Auth
	_ = json.Unmarshal(raw, &auth)
	return auth
}

type accountPublic struct {
	ID        string `json:"id"`
	Email     string `json:"email"`
	Enabled   bool   `json:"enabled"`
	Blog      string `json:"blog"`
	Followers int    `json:"followers"`
	LoggedIn  bool   `json:"loggedIn"`
	Active    bool   `json:"active"`
}

func publicAccounts() []accountPublic {
	cur, _ := currentAccount()
	var out []accountPublic
	for _, a := range allAccounts() {
		auth := peekAccountAuth(a.ID)
		blog := strings.TrimSpace(auth.Blog)
		if strings.EqualFold(blog, "YOUR_BLOG_NAME") {
			blog = ""
		}
		snap := loadFollowerLog(a.ID).Latest
		if snap.Primary != "" && (blog == "" || strings.HasPrefix(blog, "t:")) {
			blog = snap.Primary
		}
		out = append(out, accountPublic{
			ID:        a.ID,
			Email:     a.Email,
			Enabled:   a.isEnabled(),
			Blog:      blog,
			Followers: snap.Followers,
			LoggedIn:  authHasSID(auth),
			Active:    a.ID == cur.ID,
		})
	}
	return out
}

func loginAllAccounts() error {
	accs := enabledAccounts()
	if len(accs) == 0 {
		auth, err := loadAuth()
		if err != nil {
			return err
		}
		return loginWithRealChrome(&auth)
	}
	seen := map[string]string{}
	var first error
	for i, a := range accs {
		fmt.Printf("\n=== Login %d/%d: %s (%s) ===\n", i+1, len(accs), a.ID, a.Email)
		if err := setActiveAccount(a.ID); err != nil {
			fmt.Println("Skip:", err)
			if first == nil {
				first = err
			}
			continue
		}
		auth, err := loadAuth()
		if err != nil {
			auth = Auth{}
		}
		if err := loginWithRealChrome(&auth); err != nil {
			if isBadLogin(err) {
				fmt.Println("Skipping this account.")
				continue
			}
			fmt.Println("Login failed:", err)
			if first == nil {
				first = err
			}
			continue
		}
		auth, _ = loadAuth()
		id := strings.ToLower(strings.TrimSpace(auth.Blog))
		name := id
		if client := newHumanClient(); authHasSID(auth) {
			if n, err := syncAuthIdentity(client, &auth); err == nil && n != "" {
				name = n
				id = strings.ToLower(n)
			}
		}
		if prev, ok := seen[id]; ok && id != "" {
			fmt.Printf("COLLISION: %s logged in as @%s — same Tumblr as %s. Cookies were not isolated.\n", a.ID, name, prev)
			if first == nil {
				first = fmt.Errorf("%s shares Tumblr @%s with %s", a.ID, name, prev)
			}
			continue
		}
		if id != "" {
			seen[id] = a.ID
			fmt.Println("Isolated session:", a.ID, "→ @"+name)
		}
	}
	fmt.Printf("\nIsolated Tumblr sessions: %d / %d\n", len(seen), len(accs))
	return first
}

func loggedInUniqueAccounts(limit int) []Account {
	var out []Account
	seen := map[string]string{}
	for _, a := range enabledAccounts() {
		auth := peekAccountAuth(a.ID)
		if !authHasSID(auth) {
			fmt.Println("Skip", a.ID, "— not logged in")
			continue
		}
		blog := strings.ToLower(strings.TrimSpace(auth.Blog))
		if blog != "" {
			if prev, ok := seen[blog]; ok {
				fmt.Println("Skip", a.ID, "— same Tumblr as", prev)
				continue
			}
			seen[blog] = a.ID
		}
		out = append(out, a)
		if limit > 0 && len(out) >= limit {
			break
		}
	}
	return out
}

func runForAccountsParallel(label string, accs []Account, fn func(Account) error) error {
	if len(accs) == 0 {
		return fmt.Errorf("no logged-in accounts to run")
	}
	var wg sync.WaitGroup
	var errMu sync.Mutex
	var first error
	for i, a := range accs {
		a := a
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			fmt.Printf("\n======== %s: %s (%d/%d) parallel ========\n", label, a.ID, i+1, len(accs))
			if err := fn(a); err != nil {
				fmt.Printf("[%s] Error: %v\n", a.ID, err)
				errMu.Lock()
				if first == nil {
					first = err
				}
				errMu.Unlock()
			}
		}(i)
		if i+1 < len(accs) {
			gap := 12 + i*6
			if paceFast() {
				gap = 2 + i
			}
			time.Sleep(time.Duration(gap) * time.Second)
		}
	}
	wg.Wait()
	return first
}

// accountStartFrom is a 1-based index or account ID from -from.
var accountStartFrom string

func accountStartIndex(accs []Account, from string) (int, error) {
	from = strings.TrimSpace(from)
	if from == "" {
		return 0, nil
	}
	if n, err := strconv.Atoi(from); err == nil {
		if n < 1 || n > len(accs) {
			return 0, fmt.Errorf("-from %s is out of range (1-%d)", from, len(accs))
		}
		return n - 1, nil
	}
	want := accountSlug(from)
	for i, a := range accs {
		if a.ID == want {
			return i, nil
		}
	}
	return 0, fmt.Errorf("-from %s did not match an account", from)
}

func runForEachAccount(label string, fn func() error) error {
	return runForEachAccountList(label, enabledAccounts(), fn)
}

func runForEachAccountList(label string, accs []Account, fn func() error) error {
	if len(accs) == 0 {
		return fn()
	}
	start, err := accountStartIndex(accs, accountStartFrom)
	if err != nil {
		return err
	}
	if start > 0 {
		fmt.Printf("Starting at account %d/%d (%s)\n", start+1, len(accs), accs[start].ID)
	}
	var first error
	for i := start; i < len(accs); i++ {
		a := accs[i]
		fmt.Printf("\n======== %s: %s (%d/%d) ========\n", label, a.ID, i+1, len(accs))
		if err := setActiveAccount(a.ID); err != nil {
			fmt.Println(err)
			if first == nil {
				first = err
			}
			continue
		}
		if err := fn(); err != nil {
			if isBadLogin(err) {
				fmt.Println("Skipping this account.")
				continue
			}
			fmt.Println("Error:", err)
			if first == nil {
				first = err
			}
		}
	}
	return first
}

func ageVerifyAccountList() []Account {
	return accountIDsFromFile(ageVerifyAccountsFile, "Age-verify")
}

func setupProfileAccountList() []Account {
	accs := accountIDsFromFile(setupProfileAccountsFile, "Zoey setup")
	if len(accs) > 0 {
		return accs
	}
	return ageVerifyAccountList()
}

func accountIDsFromFile(path, label string) []Account {
	raw, err := os.ReadFile(path)
	if err != nil {
		return enabledAccounts()
	}
	var ids []string
	if json.Unmarshal(raw, &ids) != nil {
		var wrap struct {
			Accounts []string `json:"accounts"`
		}
		if json.Unmarshal(raw, &wrap) != nil || len(wrap.Accounts) == 0 {
			return enabledAccounts()
		}
		ids = wrap.Accounts
	}
	if len(ids) == 0 {
		return enabledAccounts()
	}
	var out []Account
	seen := map[string]bool{}
	for _, id := range ids {
		id = accountSlug(id)
		if id == "" || seen[id] {
			continue
		}
		a, ok := findAccount(id)
		if !ok || !a.isEnabled() {
			fmt.Println("Skip unknown account:", id)
			continue
		}
		seen[id] = true
		out = append(out, a)
	}
	if len(out) == 0 {
		return enabledAccounts()
	}
	fmt.Printf("%s: %d account(s) from %s\n", label, len(out), path)
	return out
}

func currentTumblrIdentity() (string, error) {
	auth, err := loadAuth()
	if err != nil {
		return "", err
	}
	if !authHasSID(auth) {
		return "", fmt.Errorf("no saved cookies")
	}
	client := &http.Client{Timeout: 45 * time.Second}
	if err := refreshCSRF(client, &auth); err != nil {
		if err := ensureSession(client, &auth); err != nil {
			return "", err
		}
	}
	_, primary, err := listAccountBlogs(client, auth)
	if err != nil {
		if blogConfigured(auth.Blog) {
			return strings.ToLower(auth.Blog), nil
		}
		return "", err
	}
	return strings.ToLower(strings.TrimSpace(primary)), nil
}

func runForEachUniqueBlog(label string, fn func() error) error {
	seen := map[string]string{}
	return runForEachAccount(label, func() error {
		id, err := currentTumblrIdentity()
		if err != nil {
			return err
		}
		acc, _ := currentAccount()
		if prev, ok := seen[id]; ok {
			fmt.Printf("Skip %s — already using @%s via %s\n", acc.ID, id, prev)
			return nil
		}
		seen[id] = acc.ID
		return fn()
	})
}

func applyAccountFlag(id string) error {
	id = strings.TrimSpace(id)
	if id == "" {
		return nil
	}
	if strings.EqualFold(id, "all") {
		runAllAccounts = true
		accs := enabledAccounts()
		if len(accs) == 0 {
			return fmt.Errorf("no enabled accounts in config/credentials.json")
		}
		return setActiveAccount(accs[0].ID)
	}
	runAllAccounts = false
	return setActiveAccount(id)
}
