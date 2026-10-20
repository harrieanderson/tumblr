package main

import (
	"encoding/json"
	"net/http"
	"os"
	"sync"
	"testing"
	"time"
)

type accountMappingAuditRow struct {
	ID       string
	Email    string
	Expected string
	Blogs    []tumblrBlog
	Error    string
}

func TestAccountMappingAudit(t *testing.T) {
	if os.Getenv("TUMBLR_MAPPING_AUDIT") != "1" {
		t.Skip("manual read-only account audit")
	}
	accs := allAccounts()
	rows := make([]accountMappingAuditRow, len(accs))
	sem := make(chan struct{}, 4)
	var wg sync.WaitGroup
	for i, a := range accs {
		wg.Add(1)
		go func(i int, a Account) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()
			row := accountMappingAuditRow{ID: a.ID, Email: a.Email, Expected: assignedBlogName(a.ID)}
			defer func() { rows[i] = row }()
			auth, err := loadAuthFrom(accountAuthPath(a.ID))
			if err != nil || !authHasSID(auth) {
				row.Error = "no saved session"
				return
			}
			client := &http.Client{Timeout: 20 * time.Second}
			if err = refreshCSRF(client, &auth); err != nil {
				row.Error = err.Error()
				return
			}
			row.Blogs, err = listAccountBlogsDetailed(client, auth)
			if err != nil {
				row.Error = err.Error()
				return
			}
			if _, err = selectVerifiedAccountBlog(a.ID, auth.Blog, row.Expected, row.Blogs); err != nil {
				row.Error = err.Error()
			}
		}(i, a)
	}
	wg.Wait()
	raw, _ := json.MarshalIndent(rows, "", "  ")
	if err := os.WriteFile("account-mapping-audit.json", raw, 0600); err != nil {
		t.Fatal(err)
	}
	good := 0
	for _, r := range rows {
		if r.Error == "" {
			good++
		} else {
			t.Logf("%s expected @%s: %s", r.ID, r.Expected, r.Error)
		}
	}
	t.Logf("Verified %d/%d saved account mappings; report: account-mapping-audit.json", good, len(rows))
}

func TestRepairAccountMappings(t *testing.T) {
	if os.Getenv("TUMBLR_MAPPING_REPAIR") != "1" {
		t.Skip("manual account session repair")
	}
	if err := initAccounts(); err != nil {
		t.Fatal(err)
	}
	nativeChromeClicks = false
	quietChromeLogin = true
	for _, id := range []string{"adrisan2009", "patatasensual7u7", "contact"} {
		t.Run(id, func(t *testing.T) {
			auth, err := loadAuthFrom(accountAuthPath(id))
			if err != nil {
				t.Fatal(err)
			}
			// Force the login form for this exact stored login, not whichever
			// session its browser profile happens to contain.
			if err := loginChromeSession(&auth, true); err != nil {
				t.Fatal(err)
			}
			client := &http.Client{Timeout: 30 * time.Second}
			blogs, err := listAccountBlogsDetailed(client, auth)
			if err != nil {
				t.Fatal(err)
			}
			name, err := verifyAccountBlog(client, &auth)
			if err != nil {
				// After a fresh credential login, a primary handle not assigned
				// to any other login can safely replace a stale assigned handle.
				for _, b := range blogs {
					if !b.Primary {
						continue
					}
					if e := rejectForeignBlogSession(&auth, b.Name); e != nil {
						t.Fatal(e)
					}
					mapping := loadNameMap(assignedUsernamesFile)
					mapping[id] = b.Name
					saveNameMap(assignedUsernamesFile, mapping)
					auth.Blog = b.UUID
					name = b.Name
					break
				}
				if name == "" {
					t.Fatal(err)
				}
			}
			if err := saveAuth(auth); err != nil {
				t.Fatal(err)
			}
			if err := saveFollowerSnapshot(id, blogs); err != nil {
				t.Fatal(err)
			}
			t.Logf("REPAIRED %s -> @%s", id, name)
		})
	}
}
