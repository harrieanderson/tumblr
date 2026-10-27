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
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

const (
	activeAccountFile        = "config/active-account.json"
	profilesAuthDir          = "config/profiles"
	profilesDataDir          = "data/profiles"
	ageVerifyAccountsFile = "config/age-verify-accounts.json"
	fixBioAccountsFile    = "config/fix-bio-accounts.json"
	zoeyFullySetupFile       = "config/zoey-fully-setup.json"
)

// setupProfileAccountsFile is overridable with -setup-accounts for parallel lanes.
var setupProfileAccountsFile = "config/setup-profile-accounts.json"

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

// passwordChangeRequiredError is Tumblr's login banner "Please change your
// password." The stored password cannot get a session until the user resets it.
type passwordChangeRequiredError struct {
	Email string
	ID    string
}

func (e passwordChangeRequiredError) Error() string {
	id := strings.TrimSpace(e.ID)
	if id == "" {
		id = accountSlug(e.Email)
	}
	if id == "" {
		return "Tumblr requires a password change"
	}
	return "Tumblr requires a password change for " + id
}

func isPasswordChangeRequired(err error) bool {
	var p passwordChangeRequiredError
	return errors.As(err, &p)
}

const badLoginsFile = "config/bad-logins.json"
const discardedAccountsFile = "config/discarded-accounts.json"

// explicitLogin is set for -login runs, which may retry a rejected account.
var explicitLogin bool

func loadBadLogins() map[string]string {
	seen := map[string]string{}
	if raw, err := os.ReadFile(badLoginsFile); err == nil {
		_ = json.Unmarshal(raw, &seen)
	}
	return seen
}

// recentBadLogin stops automatic re-logins from hammering an account Tumblr
// just rejected; repeated failures are what gets an account locked.
func recentBadLogin(id string) (time.Time, bool) {
	t, err := time.Parse(time.RFC3339, loadBadLogins()[id])
	if err != nil || time.Since(t) > 24*time.Hour {
		return time.Time{}, false
	}
	return t, true
}

func clearBadLogin(id string) {
	seen := loadBadLogins()
	if _, ok := seen[id]; !ok {
		return
	}
	delete(seen, id)
	if out, err := json.MarshalIndent(seen, "", "  "); err == nil {
		_ = os.WriteFile(badLoginsFile, append(out, '\n'), 0o644)
	}
}

// discardDeadLogin removes the account when Tumblr says the email/password is
// wrong. Credentials go, auth.json is cleared, and the id is noted in
// discarded-accounts.json (Chrome profile dir left on disk).
func discardDeadLogin(email string) error {
	email = strings.TrimSpace(email)
	id := accountSlug(email)
	if id == "" {
		if acc, ok := currentAccount(); ok {
			email = acc.Email
			id = acc.ID
		}
	}
	if id == "" {
		return nil
	}
	removed, err := removeCredentialByID(id)
	if err != nil {
		fmt.Println("Could not remove", id, "from credentials:", err)
	} else if removed != "" {
		fmt.Println("Discarded", id, "— Tumblr said email/password is wrong. Removed from credentials.json.")
		email = removed
	} else {
		fmt.Println("Discarded", id, "— Tumblr said email/password is wrong.")
	}
	clearAuthSessionFile(id)
	clearBadLogin(id)
	return recordDiscardedAccount(id, email, "incorrect_email_or_password")
}

type discardedAccount struct {
	Email  string `json:"email,omitempty"`
	Reason string `json:"reason"`
	At     string `json:"at"`
}

func loadDiscardedAccounts() map[string]discardedAccount {
	out := map[string]discardedAccount{}
	raw, err := os.ReadFile(discardedAccountsFile)
	if err != nil {
		return out
	}
	_ = json.Unmarshal(raw, &out)
	return out
}

func recordDiscardedAccount(id, email, reason string) error {
	id = accountSlug(id)
	if id == "" {
		id = accountSlug(email)
	}
	if id == "" {
		return nil
	}
	seen := loadDiscardedAccounts()
	seen[id] = discardedAccount{
		Email:  strings.TrimSpace(email),
		Reason: strings.TrimSpace(reason),
		At:     time.Now().Format(time.RFC3339),
	}
	raw, err := json.MarshalIndent(seen, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(discardedAccountsFile, append(raw, '\n'), 0o644)
}

// removeProfileByID drops an account from the GUI: credentials.json entry gone,
// session cleared, noted in discarded-accounts.json. Chrome profile data is left
// on disk (can reclaim later); the account simply stops showing up.
func removeProfileByID(id string) (email string, err error) {
	id = accountSlug(id)
	if id == "" {
		return "", fmt.Errorf("empty account id")
	}
	removed, err := removeCredentialByID(id)
	if err != nil {
		return "", err
	}
	if removed == "" {
		if acc, ok := findAccount(id); ok {
			removed = acc.Email
		}
	}
	if removed == "" {
		removed = id
	}
	clearAuthSessionFile(id)
	clearBadLogin(id)
	if err := recordDiscardedAccount(id, removed, "removed_from_gui"); err != nil {
		return removed, err
	}
	return removed, nil
}

// discardPasswordChangeLogin removes the email from credentials.json — Tumblr
// will not issue a session until the password is reset in a browser.
func discardPasswordChangeLogin(email string) error {
	email = strings.TrimSpace(email)
	id := accountSlug(email)
	if id == "" {
		if acc, ok := currentAccount(); ok {
			email = acc.Email
			id = acc.ID
		}
	}
	if id == "" {
		return nil
	}
	removed, err := removeCredentialByID(id)
	if err != nil {
		fmt.Println("Could not remove", id, "from credentials:", err)
	} else if removed != "" {
		fmt.Println("Discarded", id, "— Tumblr requires a password change. Removed from credentials.json.")
		email = removed
	} else {
		fmt.Println("Discarded", id, "— Tumblr requires a password change.")
	}
	clearAuthSessionFile(id)
	return recordDiscardedAccount(id, email, "password_change_required")
}

func clearAuthSessionFile(id string) {
	id = accountSlug(id)
	if id == "" {
		return
	}
	path := filepath.Join(profilesAuthDir, id, "auth.json")
	auth, err := loadAuthFrom(path)
	if err != nil {
		return
	}
	clearAuthSession(&auth)
	_ = saveAuth(auth)
}

// removeCredentialByID deletes the email→password entry for this account id.
// Returns the removed email (empty if not found).
func removeCredentialByID(id string) (string, error) {
	id = accountSlug(id)
	if id == "" {
		return "", nil
	}
	raw, err := os.ReadFile(credentialsFile)
	if err != nil {
		return "", err
	}
	var obj map[string]json.RawMessage
	if err := json.Unmarshal(raw, &obj); err != nil {
		return "", err
	}
	var removed string
	for key, val := range obj {
		if isReservedCredKey(key) {
			continue
		}
		var pass string
		if json.Unmarshal(val, &pass) != nil {
			continue
		}
		if accountSlug(key) != id {
			continue
		}
		removed = strings.TrimSpace(key)
		delete(obj, key)
		break
	}
	if removed == "" {
		return "", nil
	}
	out, err := json.MarshalIndent(obj, "", "  ")
	if err != nil {
		return removed, err
	}
	return removed, os.WriteFile(credentialsFile, append(out, '\n'), 0o600)
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
		if !a.isEnabled() {
			continue
		}
		if accountIsTerminated(a.ID) {
			continue
		}
		out = append(out, a)
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

func dmReplySetsPath() string {
	return accountDataFile("dm-reply-sets.json")
}

func dmReplySetsPathFor(accountID string) string {
	accountID = accountSlug(accountID)
	if accountID != "" && usingIsolatedProfiles() {
		return filepath.Join(profilesDataDir, accountID, "dm-reply-sets.json")
	}
	return dmReplySetsPath()
}

func loadDMReplySets() map[string]int {
	return loadDMReplySetsFor("")
}

func loadDMReplySetsFor(accountID string) map[string]int {
	out := map[string]int{}
	raw, err := os.ReadFile(dmReplySetsPathFor(accountID))
	if err != nil {
		return out
	}
	_ = json.Unmarshal(raw, &out)
	if out == nil {
		out = map[string]int{}
	}
	return out
}

func saveDMReplySets(m map[string]int) {
	saveDMReplySetsFor("", m)
}

func saveDMReplySetsFor(accountID string, m map[string]int) {
	path := dmReplySetsPathFor(accountID)
	_ = os.MkdirAll(filepath.Dir(path), 0755)
	raw, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return
	}
	_ = os.WriteFile(path, raw, 0644)
}

func dmReplySetsFor(otherName string) int {
	return dmReplySetsForAccount("", otherName)
}

func dmReplySetsForAccount(accountID, otherName string) int {
	key := strings.ToLower(strings.TrimSpace(otherName))
	if key == "" {
		return 0
	}
	return loadDMReplySetsFor(accountID)[key]
}

func syncDMReplySetsAtLeast(otherName string, atLeast int) {
	syncDMReplySetsAtLeastFor("", otherName, atLeast)
}

func syncDMReplySetsAtLeastFor(accountID, otherName string, atLeast int) {
	key := strings.ToLower(strings.TrimSpace(otherName))
	if key == "" || atLeast <= 0 {
		return
	}
	m := loadDMReplySetsFor(accountID)
	if m[key] >= atLeast {
		return
	}
	m[key] = atLeast
	saveDMReplySetsFor(accountID, m)
}

func clearDMReplySet(otherName string) {
	clearDMReplySetFor("", otherName)
}

func clearDMReplySetFor(accountID, otherName string) {
	key := strings.ToLower(strings.TrimSpace(otherName))
	if key == "" {
		return
	}
	m := loadDMReplySetsFor(accountID)
	if _, ok := m[key]; !ok {
		return
	}
	delete(m, key)
	saveDMReplySetsFor(accountID, m)
}

func noteDMReplySet(otherName string) {
	noteDMReplySetFor("", otherName)
}

func noteDMReplySetFor(accountID, otherName string) {
	key := strings.ToLower(strings.TrimSpace(otherName))
	if key == "" {
		return
	}
	m := loadDMReplySetsFor(accountID)
	m[key]++
	saveDMReplySetsFor(accountID, m)
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
		if a, ok := findAccount(want); ok && a.isEnabled() && !accountIsTerminated(a.ID) {
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
	Num       int    `json:"num"` // 1-based label in GUI order (Zoey-first, then followers)
	Email     string `json:"email"`
	Enabled   bool   `json:"enabled"`
	Blog      string `json:"blog"`
	Followers int    `json:"followers"`
	LoggedIn  bool   `json:"loggedIn"`
	Active    bool   `json:"active"`
	ZoeySetup bool   `json:"zoeySetup"` // full/essential Zoey look (or on fully-setup list)

	// Session health from auth.json (cookie jar + recovery metadata).
	NeedsInteractiveLogin bool   `json:"needsInteractiveLogin"`
	ValidatedAt           string `json:"validatedAt,omitempty"`
	ProxyID               string `json:"proxyId,omitempty"`
	RecoveryAttempts      int    `json:"recoveryAttempts,omitempty"`
}

func publicAccounts() []accountPublic {
	accs, _ := publicAccountsWithTally()
	return accs
}

func publicAccountsWithTally() ([]accountPublic, followerTally) {
	now := time.Now().Local()
	tally := followerTally{Date: now.Format("2006-01-02")}
	cur, _ := currentAccount()
	zoey := zoeySetupAccountSet()
	var out []accountPublic
	for _, a := range allAccounts() {
		auth := peekAccountAuth(a.ID)
		log := loadFollowerLog(a.ID)
		blog, followers := bestAccountBlogStats(a.ID, log)
		if !usableBlogName(blog) {
			blog = strings.TrimSpace(auth.Blog)
			if strings.HasPrefix(blog, "t:") || strings.EqualFold(blog, "YOUR_BLOG_NAME") {
				blog = ""
			}
		}
		enabled := a.isEnabled()
		out = append(out, accountPublic{
			ID:                    a.ID,
			Email:                 a.Email,
			Enabled:               enabled,
			Blog:                  blog,
			Followers:             followers,
			LoggedIn:              authHasSID(auth),
			Active:                a.ID == cur.ID,
			ZoeySetup:             zoey[a.ID],
			NeedsInteractiveLogin: auth.NeedsInteractiveLogin,
			ValidatedAt:           auth.ValidatedAt,
			ProxyID:               auth.ProxyID,
			RecoveryAttempts:      auth.RecoveryAttempts,
		})
		if enabled && (log.Latest.At != "" || followers > 0) {
			tally.Total += followers
			tally.Accounts++
			tally.TodayNew += accountDailyNewFollowers(log, now)
		}
	}
	sortAccountsByFollowers(out)
	n := 0
	for i := range out {
		if !out[i].Enabled {
			continue
		}
		n++
		out[i].Num = n
	}
	return out, tally
}

// accountLabel returns "#3 id" using the same numbering as the GUI list.
func accountLabel(id string) string {
	id = accountSlug(id)
	if id == "" {
		return "?"
	}
	if n := accountNumber(id); n > 0 {
		return fmt.Sprintf("#%d %s", n, id)
	}
	return id
}

func accountNumber(id string) int {
	id = accountSlug(id)
	if id == "" {
		return 0
	}
	for _, p := range publicAccounts() {
		if p.ID == id {
			return p.Num
		}
	}
	return 0
}

// zoeySetupAccountSet marks accounts that already have the Zoey look
// (status full/essential in zoey-setup-status.json, or listed in zoey-fully-setup.json).
func zoeySetupAccountSet() map[string]bool {
	out := map[string]bool{}
	if ids, err := loadFullySetupAccountIDs(); err == nil {
		for _, id := range ids {
			out[accountSlug(id)] = true
		}
	}
	raw, err := os.ReadFile(filepath.Join("config", "zoey-setup-status.json"))
	if err != nil {
		return out
	}
	var wrap struct {
		Accounts []struct {
			ID     string `json:"id"`
			Status string `json:"status"`
		} `json:"accounts"`
	}
	if json.Unmarshal(raw, &wrap) != nil {
		return out
	}
	for _, a := range wrap.Accounts {
		id := accountSlug(a.ID)
		if id == "" {
			continue
		}
		switch strings.ToLower(strings.TrimSpace(a.Status)) {
		case "full", "essential":
			out[id] = true
		}
	}
	return out
}

// sortAccountsByFollowers orders GUI accounts: Zoey-ified first, then
// followers desc within each group, then account id.
func sortAccountsByFollowers(accs []accountPublic) {
	sort.SliceStable(accs, func(i, j int) bool {
		if accs[i].ZoeySetup != accs[j].ZoeySetup {
			return accs[i].ZoeySetup
		}
		if accs[i].Followers != accs[j].Followers {
			return accs[i].Followers > accs[j].Followers
		}
		return strings.ToLower(accs[i].ID) < strings.ToLower(accs[j].ID)
	})
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
	start, err := accountStartIndex(accs, accountStartFrom)
	if err != nil {
		return err
	}
	if start > 0 {
		fmt.Printf("Starting at account %d/%d (%s)\n", start+1, len(accs), accs[start].ID)
	}
	seen := map[string]string{}
	var first error
	for i := start; i < len(accs); i++ {
		a := accs[i]
		fmt.Printf("\n=== Login %d/%d: %s (%s) ===\n", i+1, len(accs), a.ID, a.Email)
		var stopChromeFn func()
		if err := setActiveAccount(a.ID); err != nil {
			fmt.Println("Skip:", err)
			if first == nil {
				first = err
			}
		} else {
			auth, err := loadAuth()
			if err != nil {
				auth = Auth{}
			}
			// Headless batch: close after each login (no Enter prompts). Visible
			// keeps Chrome open so you can inspect before the next account.
			if headlessChrome {
				err = loginWithRealChrome(&auth)
			} else {
				stopChromeFn, err = loginWithRealChromeKeepOpen(&auth)
			}
			if err != nil {
				stopChromeFn = nil
				if isBadLogin(err) {
					fmt.Println("Skipping this account.")
				} else {
					fmt.Println("Login failed:", err)
					if first == nil {
						first = err
					}
				}
			} else {
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
				} else if id != "" {
					seen[id] = a.ID
					fmt.Println("Isolated session:", a.ID, "→ @"+name)
				}
			}
		}
		if headlessChrome {
			if stopChromeFn != nil {
				stopChromeFn()
			}
			time.Sleep(2 * time.Second)
		} else {
			if i+1 < len(accs) {
				waitForEnterNextAccount(accs[i+1].ID, i+2, len(accs))
			} else {
				waitForEnterNextAccount("", i+2, len(accs))
			}
			if stopChromeFn != nil {
				stopChromeFn()
			}
		}
	}
	fmt.Printf("\nIsolated Tumblr sessions: %d / %d\n", len(seen), len(accs))
	return first
}

func loggedInUniqueAccounts(limit int) []Account {
	var out []Account
	seen := map[string]string{}
	for _, a := range enabledAccounts() {
		if accountIsTerminated(a.ID) {
			fmt.Println("Skip", a.ID, "— Tumblr terminated this account")
			continue
		}
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

// loggedInTopFollowerAccounts returns up to limit logged-in enabled accounts,
// highest follower count first, deduped by Tumblr blog.
func loggedInTopFollowerAccounts(limit int) []Account {
	pubs, _ := publicAccountsWithTally() // already sorted by followers desc
	byID := map[string]Account{}
	for _, a := range enabledAccounts() {
		byID[a.ID] = a
	}
	var out []Account
	seenBlog := map[string]string{}
	for _, p := range pubs {
		if !p.Enabled || !p.LoggedIn {
			continue
		}
		if accountIsTerminated(p.ID) {
			continue
		}
		a, ok := byID[p.ID]
		if !ok {
			continue
		}
		blog := strings.ToLower(strings.TrimSpace(p.Blog))
		if blog == "" {
			auth := peekAccountAuth(p.ID)
			blog = strings.ToLower(strings.TrimSpace(auth.Blog))
		}
		if blog != "" {
			if prev, ok := seenBlog[blog]; ok {
				fmt.Println("Skip", p.ID, "— same Tumblr as", prev)
				continue
			}
			seenBlog[blog] = p.ID
		}
		out = append(out, a)
		if limit > 0 && len(out) >= limit {
			break
		}
	}
	return out
}

// loadFullySetupAccountIDs reads config/zoey-fully-setup.json (today's completed looks).
func loadFullySetupAccountIDs() ([]string, error) {
	raw, err := os.ReadFile(zoeyFullySetupFile)
	if err != nil {
		return nil, err
	}
	var wrap struct {
		Accounts []struct {
			ID string `json:"id"`
		} `json:"accounts"`
	}
	if err := json.Unmarshal(raw, &wrap); err != nil {
		return nil, err
	}
	var out []string
	seen := map[string]bool{}
	for _, a := range wrap.Accounts {
		id := strings.TrimSpace(a.ID)
		if id == "" || seen[id] {
			continue
		}
		seen[id] = true
		out = append(out, id)
	}
	return out, nil
}

// loggedInFullySetupAccounts returns logged-in enabled accounts from the fully-setup list.
func loggedInFullySetupAccounts() []Account {
	ids, err := loadFullySetupAccountIDs()
	if err != nil {
		fmt.Println("fully-setup list:", err)
		return nil
	}
	byID := map[string]Account{}
	for _, a := range enabledAccounts() {
		byID[a.ID] = a
	}
	var out []Account
	seenBlog := map[string]string{}
	for _, id := range ids {
		a, ok := byID[id]
		if !ok {
			fmt.Println("Skip", id, "— not in credentials / disabled")
			continue
		}
		if accountIsTerminated(id) {
			fmt.Println("Skip", id, "— Tumblr terminated this account")
			continue
		}
		auth := peekAccountAuth(id)
		if !authHasSID(auth) {
			fmt.Println("Skip", id, "— not logged in")
			continue
		}
		blog := strings.ToLower(strings.TrimSpace(auth.Blog))
		if blog != "" {
			if prev, ok := seenBlog[blog]; ok {
				fmt.Println("Skip", id, "— same Tumblr as", prev)
				continue
			}
			seenBlog[blog] = id
		}
		out = append(out, a)
	}
	return out
}

const reachRotateFile = "data/reach-rotate.json"

var reachPoolSize = 80 // top 20 by followers + next 60; -follow -top-followers N narrows it

var (
	reachPoolMu      sync.Mutex
	reachPoolCache   []Account
	reachLikerShards int
	reachShardPool   []Account
)

func likerShardPool() []Account {
	if len(reachShardPool) > 0 {
		return reachShardPool
	}
	if reachLikerShards > 1 {
		return reachAccountPool()
	}
	return nil
}

func resetReachPool() {
	reachPoolMu.Lock()
	reachPoolCache = nil
	reachPoolMu.Unlock()
}

// loggedInReachCandidates is the overnight reach pool: highest-follower
// logged-in blogs first (top 20 + next 60 when limit is 80).
func loggedInReachCandidates(limit int) []Account {
	return loggedInTopFollowerAccounts(limit)
}

func reachAccountPool() []Account {
	reachPoolMu.Lock()
	defer reachPoolMu.Unlock()
	if len(reachPoolCache) > 0 {
		return reachPoolCache
	}
	// Top followers only, but every account in it gets the same turns — the
	// growth tiers are not used to skip anyone.
	for _, a := range loggedInReachCandidates(0) {
		if _, bad := recentBadLogin(a.ID); bad {
			continue
		}
		reachPoolCache = append(reachPoolCache, a)
		if len(reachPoolCache) >= reachPoolSize {
			break
		}
	}
	fmt.Printf("Reach pool: top %d logged-in accounts by followers, least recently reached go first\n", len(reachPoolCache))
	return reachPoolCache
}

const (
	reachLastFile      = "data/reach-last.json"
	minReachBatchSlice = 15 * time.Minute
)

// reachWindow is the whole -for window of a reach run, split evenly across
// the batches so every account in the pool gets a turn before it ends.
var reachWindow time.Duration

func reachBatchSlice(window time.Duration, pool, perBatch int) time.Duration {
	if window <= 0 || pool <= 0 || perBatch <= 0 {
		return 0
	}
	batches := (pool + perBatch - 1) / perBatch
	slice := window / time.Duration(batches)
	if slice < minReachBatchSlice {
		slice = minReachBatchSlice
	}
	return slice
}

func loadReachLast() map[string]time.Time {
	m := map[string]time.Time{}
	if raw, err := os.ReadFile(reachLastFile); err == nil {
		_ = json.Unmarshal(raw, &m)
	}
	return m
}

func markReached(accs []Account) {
	m := loadReachLast()
	now := time.Now()
	// Staggered by position so the next pass keeps the round-robin order
	// instead of falling back to alphabetical ties.
	for i, a := range accs {
		m[a.ID] = now.Add(time.Duration(i) * time.Millisecond)
	}
	if raw, err := json.MarshalIndent(m, "", "  "); err == nil {
		_ = os.MkdirAll(filepath.Dir(reachLastFile), 0o755)
		_ = os.WriteFile(reachLastFile, append(raw, '\n'), 0o644)
	}
}

// leastRecentlyReached orders accounts by when reach last ran on them; never
// reached first, ties broken by id so the order is stable.
func leastRecentlyReached(pool []Account, last map[string]time.Time) []Account {
	out := append([]Account(nil), pool...)
	sort.SliceStable(out, func(i, j int) bool {
		ti, tj := last[out[i].ID], last[out[j].ID]
		if !ti.Equal(tj) {
			return ti.Before(tj)
		}
		return out[i].ID < out[j].ID
	})
	return out
}

func loadReachRotateOffset(mod int) int {
	if mod <= 0 {
		return 0
	}
	raw, err := os.ReadFile(reachRotateFile)
	if err != nil {
		return 0
	}
	var wrap struct {
		Offset int `json:"offset"`
	}
	if json.Unmarshal(raw, &wrap) != nil {
		return 0
	}
	off := wrap.Offset % mod
	if off < 0 {
		off += mod
	}
	return off
}

func saveReachRotateOffset(off int) {
	_ = os.MkdirAll(filepath.Dir(reachRotateFile), 0755)
	raw, err := json.MarshalIndent(map[string]int{"offset": off}, "", "  ")
	if err != nil {
		return
	}
	_ = os.WriteFile(reachRotateFile, raw, 0644)
}

func rotateAccountSlice(accs []Account, off int) []Account {
	n := len(accs)
	if n == 0 {
		return accs
	}
	off %= n
	if off < 0 {
		off += n
	}
	if off == 0 {
		out := make([]Account, n)
		copy(out, accs)
		return out
	}
	out := make([]Account, 0, n)
	out = append(out, accs[off:]...)
	out = append(out, accs[:off]...)
	return out
}

func rotateLoggedInForReach(n int) []Account {
	resetReachPool()
	pool := reachAccountPool()
	if len(pool) == 0 {
		return nil
	}
	if n <= 0 || n > len(pool) {
		n = len(pool)
	}
	picked := leastRecentlyReached(pool, loadReachLast())[:n]
	markReached(picked)
	return picked
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
			fmt.Printf("\n======== %s: %s (%d/%d) parallel ========\n", label, accountLabel(a.ID), i+1, len(accs))
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
			gap := 4
			if paceFast() {
				gap = 1
			}
			time.Sleep(time.Duration(gap) * time.Second)
		}
	}
	wg.Wait()
	return first
}

func runForAccountsParallelLimited(label string, accs []Account, limit int, fn func(Account) error) error {
	gap := 4
	if paceFast() {
		gap = 1
	}
	return runForAccountsParallelLimitedStagger(label, accs, limit, gap, fn)
}

func runForAccountsParallelLimitedStagger(label string, accs []Account, limit, staggerSec int, fn func(Account) error) error {
	if limit <= 0 || limit >= len(accs) {
		return runForAccountsParallel(label, accs, fn)
	}
	type job struct {
		i int
		a Account
	}
	jobs := make(chan job)
	var wg sync.WaitGroup
	var errMu sync.Mutex
	var first error
	for w := 0; w < limit; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := range jobs {
				fmt.Printf("\n======== %s: %s (%d/%d) parallel ========\n", label, accountLabel(j.a.ID), j.i+1, len(accs))
				if err := fn(j.a); err != nil {
					fmt.Printf("[%s] Error: %v\n", j.a.ID, err)
					errMu.Lock()
					if first == nil {
						first = err
					}
					errMu.Unlock()
				}
			}
		}()
	}
	for i, a := range accs {
		jobs <- job{i: i, a: a}
		if staggerSec > 0 && i+1 < len(accs) {
			time.Sleep(time.Duration(staggerSec) * time.Second)
		}
	}
	close(jobs)
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
		fmt.Printf("\n======== %s: %s (%d/%d) ========\n", label, accountLabel(a.ID), i+1, len(accs))
		if err := setActiveAccount(a.ID); err != nil {
			fmt.Println(err)
			if first == nil {
				first = err
			}
			continue
		}
		if err := fn(); err != nil {
			if isBadLogin(err) || isTerminated(err) {
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
	// PowerShell Set-Content -Encoding utf8 writes a UTF-8 BOM; strip it.
	raw = bytes.TrimPrefix(raw, []byte{0xEF, 0xBB, 0xBF})
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
		if accountIsTerminated(a.ID) {
			fmt.Println("Skip", a.ID, "— Tumblr closed this account")
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
	client := newAuthClient(&auth)
	if err := refreshCSRF(client, &auth); err != nil {
		if err := ensureSession(client, &auth); err != nil {
			return "", err
		}
	}
	name, err := verifyAccountBlog(client, &auth)
	if err != nil {
		return "", err
	}
	return strings.ToLower(strings.TrimSpace(name)), nil
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
	if accs := accountsFromCommaFlag(id); len(accs) > 0 {
		return setActiveAccount(accs[0].ID)
	}
	return setActiveAccount(id)
}

func accountsFromCommaFlag(flag string) []Account {
	flag = strings.TrimSpace(flag)
	if flag == "" || !strings.Contains(flag, ",") {
		return nil
	}
	var out []Account
	seen := map[string]bool{}
	for _, part := range strings.Split(flag, ",") {
		id := accountSlug(part)
		if id == "" || seen[id] {
			continue
		}
		a, ok := findAccount(id)
		if !ok || !a.isEnabled() {
			fmt.Println("Skip unknown account:", id)
			continue
		}
		if accountIsTerminated(a.ID) {
			fmt.Println("Skip", a.ID, "— Tumblr closed this account")
			continue
		}
		seen[id] = true
		out = append(out, a)
	}
	return out
}
