package main

import (
	"fmt"
	"os"
	"strings"
	"testing"
)

// The exact body Tumblr returned when alisonvictorm ran out of likes.
const dailyLikeLimitBody = `429 Too Many Requests: {"meta":{"status":429,"msg":"Limit Exceeded","xTumblrUserId":"430952343"},"errors":[{"title":"Limit Exceeded","code":1040,"detail":"You've exceeded your daily like limit. Please tr...`

func TestIsDailyLikeLimit(t *testing.T) {
	if !isDailyLikeLimit(fmt.Errorf("%s", dailyLikeLimitBody)) {
		t.Fatal("the real 429 body must be recognised as the daily like cap")
	}
	if isDailyLikeLimit(nil) {
		t.Fatal("nil is not a limit")
	}
	if isDailyLikeLimit(fmt.Errorf("500 Internal Server Error: nope")) {
		t.Fatal("a server error is not the like cap")
	}
	if isDailyLikeLimit(fmt.Errorf("401 Unauthorized: bad cookie")) {
		t.Fatal("a dead session is not the like cap")
	}
}

func TestLikeLimitIsRememberedForTheDay(t *testing.T) {
	if _, err := os.Stat(likeLimitFile); err == nil {
		t.Skip("real like-limit file present; not touching it")
	}
	defer os.Remove(likeLimitFile)

	const id = "zzprobeaccount"
	if likeLimitReached(id) {
		t.Fatal("should start clean")
	}
	markLikeLimited(id)
	if !likeLimitReached(id) {
		t.Fatal("hitting the cap must stick so a restart today does not retry")
	}
	if likeLimitReached("someotheraccount") {
		t.Fatal("the cap is per account, not global")
	}

	raw, err := os.ReadFile(likeLimitFile)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(raw), likeLimitDay()) {
		t.Fatal("the note must be dated so it expires tomorrow")
	}
}

func TestLikeCapStopsLikesButNotFollows(t *testing.T) {
	src, err := os.ReadFile("like.go")
	if err != nil {
		t.Fatal(err)
	}
	s := string(src)
	// Returning nil leaves follow.go free to keep following this account.
	if !strings.Contains(s, "markLikeLimited(logID)") {
		t.Fatal("the like loop must record the cap")
	}
	if strings.Contains(s, `accLog(logID, logFn, "    like fail: %v", err)
			if isDailyLikeLimit(err)`) {
		t.Fatal("the 429 body must not be logged before the cap is detected")
	}
}
