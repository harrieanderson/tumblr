package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestClampGUIWorkers(t *testing.T) {
	if got := clampGUIWorkers(0); got != guiDefaultWorkers {
		t.Fatalf("default: got %d", got)
	}
	if got := clampGUIWorkers(-3); got != guiDefaultWorkers {
		t.Fatalf("negative: got %d", got)
	}
	if got := clampGUIWorkers(3); got != 3 {
		t.Fatalf("ok: got %d", got)
	}
	if got := clampGUIWorkers(99); got != guiMaxWorkers {
		t.Fatalf("cap: got %d want %d", got, guiMaxWorkers)
	}
}

func TestProfileLocksExclusive(t *testing.T) {
	p := newProfileLocks()
	if err := p.tryLock("acc1", "login"); err != nil {
		t.Fatal(err)
	}
	if err := p.tryLock("acc1", "reach"); err == nil {
		t.Fatal("second lock on same profile should fail")
	}
	if err := p.tryLock("acc2", "reach"); err != nil {
		t.Fatal(err)
	}
	p.unlock("acc1")
	if err := p.tryLock("acc1", "recover"); err != nil {
		t.Fatal(err)
	}
	ids := p.lockedIDs()
	if len(ids) != 2 {
		t.Fatalf("locked ids=%v", ids)
	}
	p.unlockAll()
	if len(p.lockedIDs()) != 0 {
		t.Fatal("unlockAll left locks")
	}
}

func TestGUIEmergencyStop(t *testing.T) {
	srv := &guiServer{hub: newLogHub(), locks: newProfileLocks()}
	if !srv.tryStart("test job") {
		t.Fatal("tryStart failed")
	}
	if srv.stopRequested() {
		t.Fatal("should not be stopped yet")
	}
	req := httptest.NewRequest(http.MethodPost, "/api/stop", nil)
	w := httptest.NewRecorder()
	srv.handleStop(w, req)
	if w.Code != 200 {
		t.Fatalf("status %d", w.Code)
	}
	if !srv.stopRequested() {
		t.Fatal("stop flag not set")
	}
	snap := srv.controlSnapshot()
	if snap["stop"] != true || snap["busy"] != true {
		t.Fatalf("snapshot=%v", snap)
	}
	srv.done(nil)
	if srv.stopRequested() || srv.busy {
		t.Fatal("done should clear stop + busy")
	}
}

func TestGUIStatusIncludesControl(t *testing.T) {
	srv := &guiServer{hub: newLogHub(), locks: newProfileLocks(), workers: guiDefaultWorkers}
	_ = srv.locks.tryLock("demo", "login")
	req := httptest.NewRequest(http.MethodGet, "/api/status", nil)
	w := httptest.NewRecorder()
	srv.handleStatus(w, req)
	if w.Code != 200 {
		t.Fatalf("status %d body=%s", w.Code, w.Body.String())
	}
	var body map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{"busy", "job", "workers", "maxWorkers", "stop", "locked", "lockedIds", "headless"} {
		if _, ok := body[key]; !ok {
			t.Fatalf("missing %s in %v", key, body)
		}
	}
	if int(body["maxWorkers"].(float64)) != guiMaxWorkers {
		t.Fatalf("maxWorkers=%v", body["maxWorkers"])
	}
}

func TestGuiHTMLHasControlPanel(t *testing.T) {
	for _, needle := range []string{
		`id="btnStop"`,
		`id="btnRecover"`,
		`id="jobPill"`,
		`/api/stop`,
		`/api/recover`,
		`needsInteractiveLogin`,
		`Local control panel over real Chrome profiles`,
	} {
		if !strings.Contains(guiHTML, needle) {
			t.Fatalf("gui HTML missing %s", needle)
		}
	}
}

func TestHandleRecoverRequiresSelection(t *testing.T) {
	srv := &guiServer{hub: newLogHub(), locks: newProfileLocks()}
	req := httptest.NewRequest(http.MethodPost, "/api/recover", strings.NewReader(`{}`))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	srv.handleRecover(w, req)
	if w.Code != 400 {
		t.Fatalf("want 400, got %d body=%s", w.Code, w.Body.String())
	}
}
