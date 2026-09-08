package main

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"
)

type BlogProfile struct {
	Name        string
	Title       string
	Description string
	Updated     int64
	TotalPosts  int
}

var (
	reHeHim   = regexp.MustCompile(`(?i)\b(he\s*/\s*him|he\s*/\s*his|boy|guy|dude|man|male|husband|boyfriend|daddy)\b`)
	reSheHer  = regexp.MustCompile(`(?i)\b(she\s*/\s*her|girl|woman|female|wife|girlfriend|mommy|lady)\b`)
	reTheyThem = regexp.MustCompile(`(?i)\b(they\s*/\s*them)\b`)
)

func fetchBlogProfile(client *http.Client, auth Auth, blog string) (BlogProfile, error) {
	blogID := blog
	if !strings.Contains(blogID, ".") && !strings.HasPrefix(blogID, "t:") {
		blogID = blogID + ".tumblr.com"
	}

	u := "https://www.tumblr.com/api/v2/blog/" + url.PathEscape(blogID) + "/info"
	req, err := http.NewRequest("GET", u, nil)
	if err != nil {
		return BlogProfile{}, err
	}
	setCommonHeaders(req, auth)
	req.Header.Set("X-CSRF", auth.CSRF)
	req.Header.Set("Referer", "https://www.tumblr.com/"+blog)

	resp, err := client.Do(req)
	if err != nil {
		return BlogProfile{}, err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return BlogProfile{}, err
	}
	if resp.StatusCode != http.StatusOK {
		return BlogProfile{}, fmt.Errorf("%s: %s", resp.Status, truncate(string(body), 120))
	}

	var raw map[string]any
	if err := json.Unmarshal(body, &raw); err != nil {
		return BlogProfile{}, err
	}
	response, _ := raw["response"].(map[string]any)
	if response == nil {
		return BlogProfile{}, fmt.Errorf("missing response")
	}
	b, _ := response["blog"].(map[string]any)
	if b == nil {
		b = response
	}

	p := BlogProfile{
		Name:        firstNonEmpty(anyToString(b["name"]), blog),
		Title:       anyToString(b["title"]),
		Description: stripHTML(anyToString(b["description"])),
		Updated:     anyToInt64(b["updated"]),
		TotalPosts:  anyToInt(b["totalPosts"]),
	}
	if p.TotalPosts == 0 {
		p.TotalPosts = anyToInt(b["total_posts"])
	}
	if p.Updated == 0 {
		p.Updated = anyToInt64(b["updated"])
	}
	return p, nil
}

func stripHTML(s string) string {
	re := regexp.MustCompile(`<[^>]+>`)
	s = re.ReplaceAllString(s, " ")
	return strings.Join(strings.Fields(s), " ")
}

func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if strings.TrimSpace(v) != "" {
			return v
		}
	}
	return ""
}

func blogIsActive(p BlogProfile, withinDays int) bool {
	if withinDays <= 0 {
		return true
	}
	if p.Updated <= 0 {
		// Unknown activity — allow through so we don't over-filter.
		return true
	}
	cutoff := time.Now().Add(-time.Duration(withinDays) * 24 * time.Hour).Unix()
	return p.Updated >= cutoff
}

// genderGuess returns "male", "female", "unknown" from weak bio/name heuristics.
// Tumblr has no real gender field — this is best-effort only.
func genderGuess(p BlogProfile) string {
	blob := strings.ToLower(strings.Join([]string{p.Name, p.Title, p.Description}, " "))

	hasHe := reHeHim.MatchString(blob)
	hasShe := reSheHer.MatchString(blob)
	hasThey := reTheyThem.MatchString(blob)

	if hasHe && !hasShe {
		return "male"
	}
	if hasShe && !hasHe {
		return "female"
	}
	if hasThey && !hasHe && !hasShe {
		return "unknown"
	}
	if hasHe && hasShe {
		return "unknown"
	}
	return "unknown"
}

type filterResult struct {
	OK     bool
	Reason string
	Gender string
	Active bool
}

func shouldFollowBlog(client *http.Client, auth Auth, blog string, cfg FollowConfig) filterResult {
	p, err := fetchBlogProfile(client, auth, blog)
	if err != nil {
		// If info fails, don't hard-block — just note it.
		return filterResult{OK: true, Reason: "no profile (allowed)", Gender: "unknown", Active: true}
	}

	active := blogIsActive(p, cfg.RequireActiveDays)
	gender := genderGuess(p)

	if cfg.RequireActiveDays > 0 && !active {
		return filterResult{OK: false, Reason: fmt.Sprintf("inactive (last update %s)", time.Unix(p.Updated, 0).Format("2006-01-02")), Gender: gender, Active: false}
	}

	if cfg.SkipIfFemaleSignals && gender == "female" {
		return filterResult{OK: false, Reason: "female signals in bio/name", Gender: gender, Active: active}
	}

	if cfg.PreferMale && gender != "male" && gender != "unknown" {
		return filterResult{OK: false, Reason: "not male-coded", Gender: gender, Active: active}
	}

	// preferMale: unknowns are allowed (most blogs have no pronouns).
	reason := fmt.Sprintf("gender=%s active=%v", gender, active)
	return filterResult{OK: true, Reason: reason, Gender: gender, Active: active}
}
