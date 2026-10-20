package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// One post's likers feed every account: whoever fetches a post queues all its
// fresh engagers here, and every account pulls from the front. The shared
// interacted claim still guarantees nobody is touched twice.
const (
	likerQueueFile    = "data/liker-queue.json"
	likerQueueMax     = 5000
	likerQueueMaxAge  = 3 * 24 * time.Hour
	likerQueuePerPost = 40
)

type queuedLiker struct {
	Blog     string    `json:"blog"`
	PostBlog string    `json:"postBlog"`
	PostID   string    `json:"postId"`
	Kind     string    `json:"kind"`
	Source   string    `json:"source"`
	Added    time.Time `json:"added"`
}

func (q queuedLiker) asPost() ScrapedPost {
	return ScrapedPost{Blog: q.PostBlog, ID: q.PostID, SourceKind: q.Kind, Source: q.Source}
}

var (
	likerQueueMu sync.Mutex
	// When each source blog last handed out a liker, so takes rotate between
	// seeds instead of one big post supplying a whole run.
	likerSourceLastTaken = map[string]time.Time{}
)

func queuedLikerSource(e queuedLiker) string {
	if s := strings.ToLower(strings.TrimSpace(e.Source)); s != "" {
		return s
	}
	return strings.ToLower(strings.TrimSpace(e.PostBlog))
}

func loadLikerQueue() []queuedLiker {
	var q []queuedLiker
	if raw, err := os.ReadFile(likerQueueFile); err == nil {
		_ = json.Unmarshal(raw, &q)
	}
	return q
}

func saveLikerQueue(q []queuedLiker) {
	raw, err := json.Marshal(q)
	if err != nil {
		return
	}
	_ = os.MkdirAll(filepath.Dir(likerQueueFile), 0o755)
	_ = os.WriteFile(likerQueueFile, raw, 0o644)
}

// enqueueLikers adds engagers no account has touched yet and returns how many
// were new to the queue.
func enqueueLikers(names []string, post ScrapedPost) int {
	likerQueueMu.Lock()
	defer likerQueueMu.Unlock()
	q := loadLikerQueue()
	queued := map[string]bool{}
	for _, e := range q {
		queued[strings.ToLower(e.Blog)] = true
	}
	touched := interactedSnapshot()
	now := time.Now()
	added := 0
	for _, name := range names {
		if added >= likerQueuePerPost {
			break
		}
		blog := strings.TrimSpace(name)
		key := strings.ToLower(blog)
		if blog == "" || queued[key] || strings.EqualFold(blog, post.Blog) || touched[key] {
			continue
		}
		if _, ours := managedByUs(blog); ours {
			continue
		}
		if isBlockedMinor(blog) {
			continue
		}
		queued[key] = true
		q = append(q, queuedLiker{Blog: blog, PostBlog: post.Blog, PostID: post.ID, Kind: post.SourceKind, Source: post.Source, Added: now})
		added++
	}
	if len(q) > likerQueueMax {
		q = q[len(q)-likerQueueMax:]
	}
	saveLikerQueue(q)
	return added
}

// takeQueuedLiker drops stale entries and anyone another account has touched
// since they were queued, then pops the oldest entry from the source blog that
// was served least recently.
func takeQueuedLiker() (queuedLiker, bool) {
	likerQueueMu.Lock()
	defer likerQueueMu.Unlock()
	q := loadLikerQueue()
	if len(q) == 0 {
		return queuedLiker{}, false
	}
	touched := interactedSnapshot()
	live := q[:0]
	for _, e := range q {
		if time.Since(e.Added) > likerQueueMaxAge || touched[strings.ToLower(e.Blog)] {
			continue
		}
		live = append(live, e)
	}
	q = live
	pick := -1
	var pickLast time.Time
	seen := map[string]bool{}
	for i, e := range q {
		src := queuedLikerSource(e)
		if seen[src] {
			continue
		}
		seen[src] = true
		last := likerSourceLastTaken[src]
		if pick < 0 || last.Before(pickLast) {
			pick, pickLast = i, last
		}
	}
	if pick < 0 {
		saveLikerQueue(q)
		return queuedLiker{}, false
	}
	e := q[pick]
	q = append(q[:pick], q[pick+1:]...)
	likerSourceLastTaken[queuedLikerSource(e)] = time.Now()
	saveLikerQueue(q)
	return e, true
}

func likerQueueLen() int {
	likerQueueMu.Lock()
	defer likerQueueMu.Unlock()
	return len(loadLikerQueue())
}
