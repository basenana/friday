package codebase

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	eventbus "github.com/hyponet/eventbus/bus"

	"github.com/basenana/friday/bus"
	"github.com/basenana/friday/core/actor/events"
)

func TestParseCommand(t *testing.T) {
	tests := []struct {
		input string
		kind  CommandKind
		ok    bool
	}{
		{"/codebase", CommandStatus, true},
		{"/codebase index", CommandIndex, true},
		{"/codebase off", CommandOff, true},
		{"/other", 0, false},
	}
	for _, tt := range tests {
		cmd, ok, err := ParseCommand(tt.input)
		if err != nil || ok != tt.ok || (ok && cmd.Kind != tt.kind) {
			t.Fatalf("ParseCommand(%q) = %+v, %v, %v", tt.input, cmd, ok, err)
		}
	}
	for _, input := range []string{"/codebase nope", "/codebase off extra"} {
		if _, ok, err := ParseCommand(input); !ok || err == nil || err.Error() != "usage: /codebase [index|off]" {
			t.Fatalf("ParseCommand(%q) = ok %v err %v", input, ok, err)
		}
	}
}

func TestStoreSessionValidationAndCreateIfAbsent(t *testing.T) {
	s := newStore(t.TempDir(), "project")
	if err := s.ensureLayout(); err != nil {
		t.Fatal(err)
	}
	custom := []byte("custom")
	if err := os.WriteFile(s.specPath(), custom, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := s.ensureLayout(); err != nil {
		t.Fatal(err)
	}
	if got, _ := os.ReadFile(s.specPath()); string(got) != string(custom) {
		t.Fatal("user spec overwritten")
	}
	index, err := os.ReadFile(s.indexPath())
	if err != nil {
		t.Fatal(err)
	}
	for _, required := range []string{"Project Overview", "Major Modules", "Primary Interactions", "Architecture Conventions", "Knowledge Map", "Known Unknowns"} {
		if !strings.Contains(string(index), required) {
			t.Fatalf("INDEX template missing %q:\n%s", required, index)
		}
	}

	for _, id := range []string{"", " space", "a/b", "a\\b", "a\nb", strings.Repeat("x", 257)} {
		if err := s.writeSession(id); err == nil {
			t.Fatalf("accepted invalid session ID %q", id)
		}
	}
	if err := s.writeSession("session-1"); err != nil {
		t.Fatal(err)
	}
	if got, err := s.readSession(); err != nil || got != "session-1" {
		t.Fatalf("read session = %q, %v", got, err)
	}
}

func TestSessionIDBoundaryAndUTF8Bounds(t *testing.T) {
	s := newStore(t.TempDir(), "project")
	if err := s.ensureLayout(); err != nil {
		t.Fatal(err)
	}
	if err := s.writeSession(strings.Repeat("x", 255)); err != nil {
		t.Fatalf("255-byte session ID rejected: %v", err)
	}
	if err := s.writeSession(strings.Repeat("x", 256)); err == nil {
		t.Fatal("256-byte session ID accepted")
	}
	got := boundText("éé", 3)
	if got != "é" || !json.Valid([]byte(`{"value":"`+got+`"}`)) {
		t.Fatalf("invalid UTF-8 bound result %q", got)
	}
}

func TestStatusCloneDoesNotShareMutableFields(t *testing.T) {
	original := defaultStatus(StateIdle)
	original.TriggerReasons = []string{"manual"}
	original.LastRequested["manual"] = time.Now()
	clone := cloneStatus(original)
	clone.TriggerReasons[0] = "idle"
	clone.LastRequested["manual"] = time.Time{}
	if original.TriggerReasons[0] != "manual" || original.LastRequested["manual"].IsZero() {
		t.Fatalf("clone mutated original: %+v", original)
	}
}

func TestMalformedStatusIsQuarantined(t *testing.T) {
	s := newStore(t.TempDir(), "project")
	if err := s.ensureLayout(); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(s.statusPath(), []byte("broken"), 0o600); err != nil {
		t.Fatal(err)
	}
	status, err := s.loadStatus()
	if err != nil {
		t.Fatal(err)
	}
	if status.State != StateDegraded {
		t.Fatalf("status state = %q", status.State)
	}
	matches, _ := filepath.Glob(filepath.Join(s.dir, "STATUS.invalid-*.md"))
	if len(matches) != 1 {
		t.Fatalf("quarantine files = %v", matches)
	}
}

func TestActivityRetentionMatchesTerminalState(t *testing.T) {
	if got := activityRetention(ActivitySucceeded); got != 5*time.Second {
		t.Fatalf("succeeded retention=%v", got)
	}
	if got := activityRetention(ActivityCancelled); got != 5*time.Second {
		t.Fatalf("cancelled retention=%v", got)
	}
	if got := activityRetention(ActivityFailed); got != 10*time.Second {
		t.Fatalf("failed retention=%v", got)
	}
	if got := activityRetention(ActivityTimedOut); got != 10*time.Second {
		t.Fatalf("timed-out retention=%v", got)
	}
}

func TestActivityTopicsAreSafeAndSnapshotRevisionsIncrease(t *testing.T) {
	b := eventbus.NewBus()
	p := newActivityPublisher(b, "project.*.bad")
	if strings.Contains(p.contextTopic, "*.bad") || strings.Count(p.contextTopic, ".") != 3 {
		t.Fatalf("unsafe topic %q", p.contextTopic)
	}
	feed := bus.NewFeed(b, eventbus.SerialConfig{Buffer: 8, Overflow: eventbus.OverflowDropOldest}, p.contextTopic, p.indexTopic)
	defer feed.Close()

	a := Activity{OperationID: "op", Mode: ModeContext, State: ActivityRunning, StartedAt: time.Now()}
	p.publish(a)
	a.State = ActivitySucceeded
	p.publish(a)
	first := <-feed.Events()
	second := <-feed.Events()
	var got1, got2 Activity
	if err := json.Unmarshal(first.Payload, &got1); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(second.Payload, &got2); err != nil {
		t.Fatal(err)
	}
	if first.Type != events.KindCustom || got1.Revision != 1 || got2.Revision != 2 {
		t.Fatalf("activity revisions = %d, %d", got1.Revision, got2.Revision)
	}
	snapshot := p.snapshot()
	if len(snapshot) != 1 || snapshot[0].State != ActivitySucceeded || snapshot[0].Revision != 2 {
		t.Fatalf("snapshot = %+v", snapshot)
	}
}

func TestProjectLockIsExclusiveAndReacquirable(t *testing.T) {
	data := t.TempDir()
	release, err := AcquireProjectLock(data, "project", t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := AcquireProjectLock(data, "project", t.TempDir()); err == nil {
		release()
		t.Fatal("second lock unexpectedly succeeded")
	}
	release()
	release2, err := AcquireProjectLock(data, "project", t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	release2()
}
