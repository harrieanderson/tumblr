package main

import (
	"fmt"
	"net/http"
	"os"
	"strings"
	"time"
)

const (
	delayBetweenPosts = 1 * time.Minute
	keepAliveEvery    = 10 * time.Minute
)

func main() {
	if err := prepareWorkspace(); err != nil {
		fmt.Println("Setup error:", err)
		return
	}

	loginOnly := false
	guiOnly := false
	sessionOnly := false
	scrapeOnly := false
	followOnly := false
	photoOnly := false
	humanizeOnly := false
	postsOnly := false
	photoFile := ""
	photoCaption := ""
	debugSearch := ""
	args := os.Args[1:]
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "-login":
			loginOnly = true
		case "-gui":
			guiOnly = true
		case "-session":
			sessionOnly = true
		case "-scrape":
			scrapeOnly = true
		case "-follow":
			followOnly = true
		case "-photo":
			photoOnly = true
		case "-humanize":
			humanizeOnly = true
		case "-posts":
			postsOnly = true
		case "-photo-file":
			if i+1 < len(args) {
				photoFile = args[i+1]
				i++
			}
		case "-caption":
			if i+1 < len(args) {
				photoCaption = args[i+1]
				i++
			}
		case "-debug-search":
			if i+1 < len(args) {
				debugSearch = args[i+1]
				i++
			} else {
				debugSearch = "nsfw"
			}
		}
	}

	auth, err := loadAuth()
	if err != nil {
		fmt.Println("Error loading config/auth.json:", err)
		return
	}

	if loginOnly {
		if err := loginWithRealChrome(&auth); err != nil {
			fmt.Println("Login/sync failed:", err)
		}
		return
	}

	if !blogConfigured(auth.Blog) && !guiOnly {
		fmt.Println("Set your Tumblr blog name in config/auth.json, or run: go run . -login")
		return
	}

	if guiOnly {
		_ = auth
		runGUI()
		return
	}

	if debugSearch != "" {
		runDebugSearch(debugSearch)
		return
	}

	if scrapeOnly {
		runScrape()
		return
	}

	if followOnly {
		runFollowLikers()
		return
	}

	if humanizeOnly || sessionOnly || len(args) == 0 {
		runHumanize()
		return
	}

	if len(args) >= 2 && args[0] == "-dump-blog" {
		runDumpBlog(args[1])
		return
	}

	if photoFile != "" {
		runOnePhoto(photoFile, photoCaption)
		return
	}

	if photoOnly {
		runPhotoQueue()
		return
	}

	if postsOnly {
		runTextQueue()
		return
	}

	fmt.Println("Unknown flags. Try: go run .")
	fmt.Println("  (no flags)  reach session (seed blogs → engagers)")
	fmt.Println("  -follow     reach engage only")
	fmt.Println("  -gui        open GUI")
	fmt.Println("  -posts      text queue")
	fmt.Println("  -photo      photo queue")
	fmt.Println("  -login      Chrome login sync")
}

func runTextQueue() {
	auth, err := loadAuth()
	if err != nil {
		fmt.Println("Error loading config/auth.json:", err)
		return
	}

	fmt.Println("Syncing cookies from Chrome...")
	if err := syncCookiesFromChrome(&auth); err != nil {
		if authHasSID(auth) {
			fmt.Println("Chrome sync skipped:", err)
			fmt.Println("Using cookies already saved in config/auth.json")
		} else {
			fmt.Println("Cookie sync failed:", err)
			fmt.Println("Run once with: go run . -login")
			return
		}
	}

	client := &http.Client{}
	first := true

	for {
		text, err := peekPost()
		if err != nil {
			if first {
				fmt.Println("Error reading posts queue:", err)
			} else {
				fmt.Println("Queue empty — done.")
			}
			return
		}
		first = false

		if err := ensureSession(client, &auth); err != nil {
			fmt.Println("Session dead and could not recover:", err)
			fmt.Println("When you're back, run: go run . -login")
			return
		}

		fmt.Println("Posting:", text)
		if err := createPost(client, auth, text); err != nil {
			if isUnauthorized(err) {
				fmt.Println("Post got 401 — trying to recover session...")
				if err := ensureSession(client, &auth); err != nil {
					fmt.Println("Could not recover:", err)
					fmt.Println("When you're back, run: go run . -login")
					return
				}
				if err := createPost(client, auth, text); err != nil {
					fmt.Println("Error creating post:", err)
					return
				}
			} else {
				fmt.Println("Error creating post:", err)
				return
			}
		}

		if err := popPost(); err != nil {
			fmt.Println("Error updating queue:", err)
			return
		}

		if queueHasPosts() {
			fmt.Printf("Waiting %s before next post (keep-alive every %s)...\n", delayBetweenPosts, keepAliveEvery)
			if err := waitWithKeepAlive(client, &auth, delayBetweenPosts); err != nil {
				fmt.Println("Session died during wait:", err)
				fmt.Println("When you're back, run: go run . -login")
				return
			}
		}
	}
}

// ensureSession refreshes CSRF, and if that fails, re-pulls cookies from the
// chrome-data profile then retries. Only fails if Tumblr fully logged that profile out.
func ensureSession(client *http.Client, auth *Auth) error {
	if err := refreshCSRF(client, auth); err != nil {
		fmt.Println("CSRF/cookie failed — re-syncing from Chrome profile...")
		if err := syncCookiesFromChrome(auth); err != nil {
			return err
		}
		if err := refreshCSRF(client, auth); err != nil {
			return err
		}
	}
	maybeFillBlog(client, auth)
	return nil
}

func waitWithKeepAlive(client *http.Client, auth *Auth, total time.Duration) error {
	deadline := time.Now().Add(total)
	for {
		remaining := time.Until(deadline)
		if remaining <= 0 {
			return nil
		}

		sleep := keepAliveEvery
		if sleep > remaining {
			sleep = remaining
		}
		time.Sleep(sleep)

		if time.Now().After(deadline) || time.Until(deadline) < time.Second {
			return nil
		}

		fmt.Println("Keep-alive ping...")
		if err := ensureSession(client, auth); err != nil {
			return err
		}
	}
}

func isUnauthorized(err error) bool {
	return err != nil && strings.Contains(err.Error(), "401")
}

func runOnePhoto(path, caption string) {
	auth, err := loadAuth()
	if err != nil {
		fmt.Println("Error loading auth:", err)
		return
	}
	if err := syncCookiesFromChrome(&auth); err != nil {
		if !authHasSID(auth) {
			fmt.Println("Cookie sync failed:", err)
			return
		}
		fmt.Println("Using saved cookies")
	}
	client := &http.Client{}
	if err := ensureSession(client, &auth); err != nil {
		fmt.Println("Session dead:", err)
		return
	}
	if caption == "" {
		caption = captionForPic(path)
	}
	fmt.Println("Posting photo:", path)
	if caption != "" {
		fmt.Println("Caption:", caption)
	}
	if err := createPhotoPost(client, auth, path, caption); err != nil {
		fmt.Println("Error:", err)
		return
	}
	fmt.Println("Done.")
}

func runPhotoQueue() {
	auth, err := loadAuth()
	if err != nil {
		fmt.Println("Error loading auth:", err)
		return
	}

	fmt.Println("Syncing cookies from Chrome...")
	if err := syncCookiesFromChrome(&auth); err != nil {
		if authHasSID(auth) {
			fmt.Println("Chrome sync skipped:", err)
			fmt.Println("Using cookies already saved in config/auth.json")
		} else {
			fmt.Println("Cookie sync failed:", err)
			fmt.Println("Run once with: go run . -login")
			return
		}
	}

	client := &http.Client{}
	first := true

	for {
		path, err := nextPic()
		if err != nil {
			if first {
				fmt.Println("Error reading pics:", err)
			} else {
				fmt.Println("Pics folder empty — done.")
			}
			return
		}
		first = false

		if err := ensureSession(client, &auth); err != nil {
			fmt.Println("Session dead:", err)
			return
		}

		caption := captionForPic(path)
		fmt.Println("Posting photo:", path)
		if caption != "" {
			fmt.Println("Caption:", caption)
		}

		if err := createPhotoPost(client, auth, path, caption); err != nil {
			fmt.Println("Error:", err)
			return
		}
		if err := markPicPosted(path); err != nil {
			fmt.Println("Posted, but failed to move file:", err)
			return
		}
		fmt.Println("Moved to", picsPosted+"/")

		if picsRemaining() {
			fmt.Printf("Waiting %s before next photo...\n", delayBetweenPosts)
			if err := waitWithKeepAlive(client, &auth, delayBetweenPosts); err != nil {
				fmt.Println("Session died during wait:", err)
				return
			}
		}
	}
}
