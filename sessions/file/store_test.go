package file

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/basenana/friday/core/actor/events"
	"github.com/basenana/friday/core/collaboration"
	"github.com/basenana/friday/core/contextmgr"
	corelogger "github.com/basenana/friday/core/logger"
	"github.com/basenana/friday/core/planning"
	"github.com/basenana/friday/core/types"
	"github.com/basenana/friday/sessions"
)

func TestConcurrentMetadataUpdatesAcrossStoreInstances(t *testing.T) {
	dir := t.TempDir()
	first := NewFileSessionStore(dir)
	second := NewFileSessionStore(dir)
	if _, err := first.Create("shared", nil); err != nil {
		t.Fatal(err)
	}
	mode := collaboration.ModePlan
	model := sessions.ModelSelection{Model: "gpt-test"}
	effort := "default"
	start := make(chan struct{})
	errs := make(chan error, 103)
	var wg sync.WaitGroup
	wg.Add(5)
	go func() {
		defer wg.Done()
		<-start
		errs <- first.UpdateMeta("shared", sessions.SessionMetaPatch{Mode: &mode})
	}()
	go func() {
		defer wg.Done()
		<-start
		errs <- second.UpdateMeta("shared", sessions.SessionMetaPatch{Model: &model})
	}()
	go func() {
		defer wg.Done()
		<-start
		errs <- first.UpdateMeta("shared", sessions.SessionMetaPatch{Effort: &effort})
	}()
	appendMessages := func(store *FileSessionStore, prefix string) {
		defer wg.Done()
		<-start
		for i := 0; i < 50; i++ {
			errs <- store.AppendMessages("shared", types.Message{Role: types.RoleUser, Content: fmt.Sprintf("%s-%d", prefix, i)})
		}
	}
	go appendMessages(first, "first")
	go appendMessages(second, "second")
	close(start)
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatalf("concurrent update: %v", err)
		}
	}
	meta, err := first.GetMeta("shared")
	if err != nil {
		t.Fatal(err)
	}
	if meta.Runtime.Mode != mode || meta.Runtime.Model != model || meta.Runtime.Effort != effort || meta.MessageCount != 100 {
		t.Fatalf("lost metadata update: %+v", meta)
	}
	messages, err := second.LoadMessages("shared")
	if err != nil || len(messages) != 100 {
		t.Fatalf("messages = %d, err=%v", len(messages), err)
	}
	lockInfo, err := os.Stat(filepath.Join(first.sessionDir("shared"), ".session.lock"))
	if err != nil || lockInfo.Mode().Perm() != 0o600 {
		t.Fatalf("metadata lock permissions = %v, err=%v", lockInfo, err)
	}
}

func TestConcurrentPlanProposalsAreVersionedAndSuperseded(t *testing.T) {
	dir := t.TempDir()
	stores := []*FileSessionStore{NewFileSessionStore(dir), NewFileSessionStore(dir)}
	if _, err := stores[0].Create("planning", nil); err != nil {
		t.Fatal(err)
	}
	const count = 24
	start := make(chan struct{})
	results := make(chan *planning.Artifact, count)
	errs := make(chan error, count)
	var wg sync.WaitGroup
	for i := 0; i < count; i++ {
		i := i
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			plan, err := stores[i%len(stores)].ProposePlan("planning", planning.Artifact{
				ID: fmt.Sprintf("plan-%02d", i), SessionID: "planning", Title: "Plan",
				Markdown: "## Summary\ncomplete", CreatedAt: time.Now(),
			})
			if err != nil {
				errs <- err
				return
			}
			results <- plan
		}()
	}
	close(start)
	wg.Wait()
	close(results)
	close(errs)
	for err := range errs {
		t.Fatalf("ProposePlan: %v", err)
	}
	var versions []int
	for plan := range results {
		versions = append(versions, plan.Version)
	}
	sort.Ints(versions)
	if len(versions) != count {
		t.Fatalf("proposal count = %d, want %d", len(versions), count)
	}
	for i, version := range versions {
		if version != i+1 {
			t.Fatalf("versions = %v", versions)
		}
	}
	latest, err := stores[0].LoadLatestPlan("planning")
	if err != nil || latest == nil || latest.Version != count || latest.Status != planning.ArtifactProposed {
		t.Fatalf("latest = %+v, err=%v", latest, err)
	}
	proposed := 0
	for i := 0; i < count; i++ {
		loaded, err := stores[1].LoadPlan("planning", fmt.Sprintf("plan-%02d", i))
		if err != nil {
			t.Fatal(err)
		}
		if loaded.Status == planning.ArtifactProposed {
			proposed++
			if loaded.ID != latest.ID {
				t.Fatalf("non-latest plan remained proposed: %+v", loaded)
			}
		} else if loaded.Status != planning.ArtifactSuperseded {
			t.Fatalf("old plan status = %q", loaded.Status)
		}
	}
	if proposed != 1 {
		t.Fatalf("proposed plans = %d, want 1", proposed)
	}
}

func TestPlanArtifactRoundTripAndStatusTransition(t *testing.T) {
	dir := t.TempDir()
	store := NewFileSessionStore(dir)
	if _, err := store.Create("planning", nil); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC().Truncate(time.Second)
	plan := planning.Artifact{
		ID: "plan-1", SessionID: "planning", Version: 1, Title: "First",
		Markdown: "## Summary\nDone", Status: planning.ArtifactProposed, CreatedAt: now,
	}
	if err := store.SavePlan("planning", plan); err != nil {
		t.Fatal(err)
	}
	loaded, err := store.LoadPlan("planning", plan.ID)
	if err != nil {
		t.Fatal(err)
	}
	if loaded.ID != plan.ID || loaded.Status != planning.ArtifactProposed || !loaded.CreatedAt.Equal(now) {
		t.Fatalf("loaded plan = %+v", loaded)
	}
	latest, err := store.LoadLatestPlan("planning")
	if err != nil || latest == nil || latest.ID != plan.ID {
		t.Fatalf("latest plan = %+v, err=%v", latest, err)
	}

	acceptedAt := now.Add(time.Minute)
	plan.Status = planning.ArtifactAccepted
	plan.AcceptedAt = &acceptedAt
	if err := store.SavePlan("planning", plan); err != nil {
		t.Fatal(err)
	}
	loaded, err = store.LoadPlan("planning", plan.ID)
	if err != nil || loaded.Status != planning.ArtifactAccepted || loaded.AcceptedAt == nil || !loaded.AcceptedAt.Equal(acceptedAt) {
		t.Fatalf("accepted plan = %+v, err=%v", loaded, err)
	}
	info, err := os.Stat(store.planPath("planning", plan.ID))
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("plan mode = %o", info.Mode().Perm())
	}
	if leftovers, _ := filepath.Glob(filepath.Join(store.plansDir("planning"), ".friday-*.tmp")); len(leftovers) != 0 {
		t.Fatalf("atomic-write leftovers: %v", leftovers)
	}
}

func TestSavePlanRejectsMismatchedIdentity(t *testing.T) {
	store := NewFileSessionStore(t.TempDir())
	if err := store.SavePlan("session", planning.Artifact{ID: "plan", SessionID: "other"}); err == nil {
		t.Fatal("expected mismatched session identity to fail")
	}
	if _, err := store.LoadPlan("session", "../../escape"); err == nil {
		t.Fatal("expected unsafe plan ID to fail")
	}
}

func TestLegacySessionMetadataUsesRuntimeDefaults(t *testing.T) {
	dir := t.TempDir()
	store := NewFileSessionStore(dir)
	if err := os.MkdirAll(store.sessionDir("legacy"), 0o755); err != nil {
		t.Fatal(err)
	}
	legacy := `{"id":"legacy","created_at":"2025-01-01T00:00:00Z","updated_at":"2025-01-01T00:00:00Z","message_count":0}`
	if err := os.WriteFile(store.metaPath("legacy"), []byte(legacy), 0o644); err != nil {
		t.Fatal(err)
	}
	mgr := sessions.NewManager(store, filepath.Join(dir, "current"), "")
	runtimeState, err := mgr.Runtime("legacy")
	if err != nil {
		t.Fatal(err)
	}
	if runtimeState.Mode != collaboration.ModeDefault || runtimeState.Model.Model != "" {
		t.Fatalf("legacy runtime = %+v", runtimeState)
	}
}

func writeEventLines(t *testing.T, store *FileSessionStore, id string, lines ...[]byte) {
	t.Helper()
	if err := os.MkdirAll(store.sessionDir(id), 0o755); err != nil {
		t.Fatal(err)
	}
	var data []byte
	for _, line := range lines {
		data = append(data, line...)
		data = append(data, '\n')
	}
	if err := os.WriteFile(store.eventsPath(id), data, 0o600); err != nil {
		t.Fatal(err)
	}
}

func marshalEvent(t *testing.T, evt events.Event) []byte {
	t.Helper()
	data, err := json.Marshal(evt)
	if err != nil {
		t.Fatal(err)
	}
	return data
}

func TestEventStoreSkipsMalformedInteriorRecord(t *testing.T) {
	store := NewFileSessionStore(t.TempDir())
	first := events.NewEvent(events.KindRunStarted, "run-1")
	second := events.NewEvent(events.KindRunFinished, "run-1")
	writeEventLines(t, store, "events", marshalEvent(t, first), []byte("not-json"), marshalEvent(t, second))

	got, err := store.LoadEvents(context.Background(), "events")
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got[0].Type != first.Type || got[1].Type != second.Type {
		t.Fatalf("events = %#v, want valid records before and after corruption", got)
	}
}

func TestEventStoreSkipsBlankAndMultipleMalformedRecords(t *testing.T) {
	store := NewFileSessionStore(t.TempDir())
	want := events.NewEvent(events.KindRunFinished, "run-2")
	writeEventLines(t, store, "events", nil, []byte("  "), []byte("bad-one"), []byte("{bad-two"), marshalEvent(t, want))

	got, err := store.LoadEvents(context.Background(), "events")
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].RunID != want.RunID {
		t.Fatalf("events = %#v, want only valid suffix", got)
	}
}

func TestEventStoreLoadsLargeRecordWithoutScannerLimit(t *testing.T) {
	store := NewFileSessionStore(t.TempDir())
	want := strings.Repeat("x", 96<<10)
	evt := events.NewEvent(events.KindTextMessageContent, "large").
		WithPayload(events.TextMessageContentData{Content: want})
	writeEventLines(t, store, "events", marshalEvent(t, evt))

	got, err := store.LoadEvents(context.Background(), "events")
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 {
		t.Fatalf("loaded %d events, want 1", len(got))
	}
	var payload events.TextMessageContentData
	if err := events.DecodePayload(got[0], &payload); err != nil {
		t.Fatal(err)
	}
	if payload.Content != want {
		t.Fatalf("large payload length = %d, want %d", len(payload.Content), len(want))
	}
}

func TestEventStoreLoadIsReadOnly(t *testing.T) {
	store := NewFileSessionStore(t.TempDir())
	valid := marshalEvent(t, events.NewEvent(events.KindRunStarted, "run"))
	if err := os.MkdirAll(store.sessionDir("events"), 0o755); err != nil {
		t.Fatal(err)
	}
	before := append(append(append(valid, '\n'), []byte("bad\n")...), []byte(`{"type":`)...)
	if err := os.WriteFile(store.eventsPath("events"), before, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := store.LoadEvents(context.Background(), "events"); err != nil {
		t.Fatal(err)
	}
	after, err := os.ReadFile(store.eventsPath("events"))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(after, before) {
		t.Fatalf("LoadEvents modified event log\nbefore: %q\nafter:  %q", before, after)
	}
}

func TestEventWriterLeaseRejectsSecondStoreAndReleasesAfterClose(t *testing.T) {
	dir := t.TempDir()
	first := NewFileSessionStore(dir)
	second := NewFileSessionStore(dir)
	sink, err := first.OpenEventSink(context.Background(), "events")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := second.OpenEventSink(context.Background(), "events"); !errors.Is(err, sessions.ErrEventWriterActive) {
		t.Fatalf("second OpenEventSink error = %v, want ErrEventWriterActive", err)
	}
	if err := sink.Close(); err != nil {
		t.Fatal(err)
	}
	next, err := second.OpenEventSink(context.Background(), "events")
	if err != nil {
		t.Fatalf("OpenEventSink after close: %v", err)
	}
	if err := next.Close(); err != nil {
		t.Fatal(err)
	}
}

func TestEventWriterCloseFlushesBeforeLeaseReleaseAndIsIdempotent(t *testing.T) {
	dir := t.TempDir()
	first := NewFileSessionStore(dir)
	second := NewFileSessionStore(dir)
	sink, err := first.OpenEventSink(context.Background(), "events")
	if err != nil {
		t.Fatal(err)
	}
	want := events.NewEvent(events.KindTextMessageContent, "run-buffered").
		WithPayload(events.TextMessageContentData{Content: "buffered"})
	if err := sink.Append(context.Background(), want); err != nil {
		t.Fatal(err)
	}
	if err := sink.Close(); err != nil {
		t.Fatal(err)
	}
	if err := sink.Close(); err != nil {
		t.Fatalf("second Close: %v", err)
	}
	next, err := second.OpenEventSink(context.Background(), "events")
	if err != nil {
		t.Fatal(err)
	}
	if err := next.Close(); err != nil {
		t.Fatal(err)
	}
	got, err := second.LoadEvents(context.Background(), "events")
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].RunID != want.RunID {
		t.Fatalf("events after lease transfer = %#v", got)
	}
}

func TestLoadEventsWhileAnotherStoreOwnsWriterIsNonMutating(t *testing.T) {
	dir := t.TempDir()
	writer := NewFileSessionStore(dir)
	reader := NewFileSessionStore(dir)
	sink, err := writer.OpenEventSink(context.Background(), "events")
	if err != nil {
		t.Fatal(err)
	}
	defer sink.Close()
	want := events.NewEvent(events.KindRunFinished, "run-flushed")
	if err := sink.Append(context.Background(), want); err != nil {
		t.Fatal(err)
	}
	before, err := os.ReadFile(writer.eventsPath("events"))
	if err != nil {
		t.Fatal(err)
	}
	got, err := reader.LoadEvents(context.Background(), "events")
	if err != nil {
		t.Fatal(err)
	}
	after, err := os.ReadFile(writer.eventsPath("events"))
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].RunID != want.RunID || !bytes.Equal(before, after) {
		t.Fatalf("read under writer lease: events=%#v modified=%t", got, !bytes.Equal(before, after))
	}
	if _, err := reader.OpenEventSink(context.Background(), "events"); !errors.Is(err, sessions.ErrEventWriterActive) {
		t.Fatalf("second writer error = %v, want ErrEventWriterActive", err)
	}
}

type captureLogger struct {
	warnings []capturedWarning
}

type capturedWarning struct {
	message string
	fields  map[string]interface{}
}

func (l *captureLogger) Named(string) corelogger.Logger        { return l }
func (l *captureLogger) With(...interface{}) corelogger.Logger { return l }
func (l *captureLogger) Info(...interface{})                   {}
func (l *captureLogger) Warn(...interface{})                   {}
func (l *captureLogger) Error(...interface{})                  {}
func (l *captureLogger) Infof(string, ...interface{})          {}
func (l *captureLogger) Warnf(string, ...interface{})          {}
func (l *captureLogger) Errorf(string, ...interface{})         {}
func (l *captureLogger) Infow(string, ...interface{})          {}
func (l *captureLogger) Errorw(string, ...interface{})         {}
func (l *captureLogger) Warnw(message string, keysAndValues ...interface{}) {
	fields := make(map[string]interface{}, len(keysAndValues)/2)
	for i := 0; i+1 < len(keysAndValues); i += 2 {
		key, _ := keysAndValues[i].(string)
		fields[key] = keysAndValues[i+1]
	}
	l.warnings = append(l.warnings, capturedWarning{message: message, fields: fields})
}

func TestEventStoreWarnsOnceWithoutLoggingMalformedRecord(t *testing.T) {
	oldLogger := corelogger.Root()
	capture := &captureLogger{}
	corelogger.SetRoot(capture)
	t.Cleanup(func() { corelogger.SetRoot(oldLogger) })

	store := NewFileSessionStore(t.TempDir())
	secret := "private-tool-output-do-not-log"
	valid := events.NewEvent(events.KindRunFinished, "run")
	writeEventLines(t, store, "session-warning", []byte(secret), []byte("also-bad"), marshalEvent(t, valid))
	if _, err := store.LoadEvents(context.Background(), "session-warning"); err != nil {
		t.Fatal(err)
	}
	if len(capture.warnings) != 1 {
		t.Fatalf("warnings = %d, want 1", len(capture.warnings))
	}
	warning := capture.warnings[0]
	if warning.fields["session_id"] != "session-warning" || warning.fields["skipped_records"] != 2 || warning.fields["first_line"] != 1 || warning.fields["first_offset"] != int64(0) || warning.fields["first_error"] == nil {
		t.Fatalf("warning = %#v", warning)
	}
	if strings.Contains(fmt.Sprint(warning), secret) {
		t.Fatalf("warning leaked malformed record: %#v", warning)
	}
}

func TestEventStoreRoundTripAndRepairsTruncatedTail(t *testing.T) {
	dir := t.TempDir()
	store := NewFileSessionStore(dir)
	if _, err := store.Create("events", nil); err != nil {
		t.Fatal(err)
	}
	sink, err := store.OpenEventSink(context.Background(), "events")
	if err != nil {
		t.Fatal(err)
	}
	want := events.NewEvent(events.KindRunStarted, "run-1")
	if err := sink.Append(context.Background(), want); err != nil {
		t.Fatal(err)
	}
	if err := sink.Close(); err != nil {
		t.Fatal(err)
	}
	path := store.eventsPath("events")
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("mode = %o", info.Mode().Perm())
	}
	f, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0)
	if err != nil {
		t.Fatal(err)
	}
	_, _ = f.WriteString(`{"type":`)
	_ = f.Close()
	repaired, err := store.OpenEventSink(context.Background(), "events")
	if err != nil {
		t.Fatal(err)
	}
	_ = repaired.Close()
	got, err := store.LoadEvents(context.Background(), "events")
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].RunID != want.RunID {
		t.Fatalf("events = %#v", got)
	}
}

func TestEventStoreCompactsLargeLogsAndKeepsLatestRun(t *testing.T) {
	dir := t.TempDir()
	store := NewFileSessionStore(dir)
	if _, err := store.Create("bounded", nil); err != nil {
		t.Fatal(err)
	}
	sink, err := store.OpenEventSink(context.Background(), "bounded")
	if err != nil {
		t.Fatal(err)
	}
	payload := strings.Repeat("x", 4096)
	for run := 0; run < 24; run++ {
		runID := fmt.Sprintf("run-%d", run)
		if err := sink.Append(context.Background(), events.NewEvent(events.KindRunStarted, runID)); err != nil {
			t.Fatal(err)
		}
		for i := 0; i < 100; i++ {
			evt := events.NewEvent(events.KindTextMessageContent, runID).WithPayload(events.TextMessageContentData{Content: payload})
			if err := sink.Append(context.Background(), evt); err != nil {
				t.Fatal(err)
			}
		}
		if err := sink.Append(context.Background(), events.NewEvent(events.KindRunFinished, runID)); err != nil {
			t.Fatal(err)
		}
	}
	if err := sink.Close(); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(store.eventsPath("bounded"))
	if err != nil {
		t.Fatal(err)
	}
	if info.Size() > maxEventLogBytes {
		t.Fatalf("event log size = %d, max = %d", info.Size(), maxEventLogBytes)
	}
	got, err := store.LoadEvents(context.Background(), "bounded")
	if err != nil {
		t.Fatal(err)
	}
	if len(got) == 0 || got[len(got)-1].RunID != "run-23" || got[len(got)-1].Type != events.KindRunFinished {
		t.Fatalf("latest event not retained: %#v", got[len(got)-1])
	}
}

func TestEventStoreLoadsWhileActorSinkAppends(t *testing.T) {
	store := NewFileSessionStore(t.TempDir())
	if _, err := store.Create("live-events", nil); err != nil {
		t.Fatal(err)
	}
	sink, err := store.OpenEventSink(context.Background(), "live-events")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = sink.Close() })

	const count = 200
	errs := make(chan error, 1)
	done := make(chan struct{})
	go func() {
		defer close(done)
		for i := 0; i < count; i++ {
			if err := sink.Append(context.Background(), events.NewEvent(events.KindTextMessageContent, "run-live").
				WithPayload(events.TextMessageContentData{Content: fmt.Sprint(i)})); err != nil {
				errs <- err
				return
			}
		}
	}()

	for {
		select {
		case err := <-errs:
			t.Fatal(err)
		case <-done:
			if err := sink.Close(); err != nil {
				t.Fatal(err)
			}
			loaded, err := store.LoadEvents(context.Background(), "live-events")
			if err != nil {
				t.Fatal(err)
			}
			if len(loaded) != count {
				t.Fatalf("loaded %d events, want %d", len(loaded), count)
			}
			return
		default:
			if _, err := store.LoadEvents(context.Background(), "live-events"); err != nil {
				t.Fatal(err)
			}
		}
	}
}

func TestReplaceMessages(t *testing.T) {
	// Create temp directory
	tmpDir, err := os.MkdirTemp("", "session_test")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	store := NewFileSessionStore(tmpDir)
	if err := store.EnsureDir(); err != nil {
		t.Fatalf("failed to ensure dir: %v", err)
	}

	sessionID := "test-session-001"

	// 1. Create session
	sess, err := store.Create(sessionID, nil)
	if err != nil {
		t.Fatalf("failed to create session: %v", err)
	}

	// 2. Add some messages
	msgs := []types.Message{
		{Role: types.RoleUser, Content: "Hello"},
		{Role: types.RoleAssistant, Content: "Hi there!"},
		{Role: types.RoleUser, Content: "How are you?"},
		{Role: types.RoleAssistant, Content: "I'm doing well, thanks!"},
	}
	if err := store.AppendMessages(sessionID, msgs...); err != nil {
		t.Fatalf("failed to append messages: %v", err)
	}

	// Verify message count
	loaded, err := store.LoadMessages(sessionID)
	if err != nil {
		t.Fatalf("failed to load messages: %v", err)
	}
	if len(loaded) != 4 {
		t.Fatalf("expected 4 messages, got %d", len(loaded))
	}

	// 3. Call ReplaceMessages (simulate compact)
	compactMsgs := []types.Message{
		{Role: types.RoleSystem, Content: "Summary of previous conversation..."},
		{Role: types.RoleUser, Content: "Hello"},
		{Role: types.RoleAssistant, Content: "Hi there!"},
	}
	if err := store.ReplaceMessages(sessionID, compactMsgs...); err != nil {
		t.Fatalf("failed to replace messages: %v", err)
	}

	// 4. Verify backup file exists
	files, err := os.ReadDir(filepath.Join(tmpDir, sessionID))
	if err != nil {
		t.Fatalf("failed to read session dir: %v", err)
	}

	var hasBackup bool
	var hasHistory bool
	for _, f := range files {
		if f.Name() == "history.jsonl" {
			hasHistory = true
		}
		if len(f.Name()) > len("history_origin_") && f.Name()[:len("history_origin_")] == "history_origin_" {
			hasBackup = true
			t.Logf("Found backup file: %s", f.Name())
		}
	}

	if !hasBackup {
		t.Error("expected backup file to exist")
	}
	if !hasHistory {
		t.Error("expected history.jsonl to exist")
	}

	// 5. Verify new history.jsonl content
	newLoaded, err := store.LoadMessages(sessionID)
	if err != nil {
		t.Fatalf("failed to load new messages: %v", err)
	}
	if len(newLoaded) != 3 {
		t.Fatalf("expected 3 messages after compact, got %d", len(newLoaded))
	}

	// 6. Verify first message is system summary
	if newLoaded[0].Role != types.RoleSystem {
		t.Errorf("expected first message to be system role, got %s", newLoaded[0].Role)
	}

	t.Logf("Session ID: %s", sess.ID)
	t.Logf("Original messages: %d, After compact: %d", len(loaded), len(newLoaded))
}

func TestSessionMemoryRoundTrip(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "session_memory_test")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	store := NewFileSessionStore(tmpDir)
	if err := store.EnsureDir(); err != nil {
		t.Fatalf("failed to ensure dir: %v", err)
	}

	sessionID := "test-session-memory-001"
	if _, err := store.Create(sessionID, nil); err != nil {
		t.Fatalf("failed to create session: %v", err)
	}

	record := &contextmgr.SessionMemoryRecord{
		GeneratedAt:    time.Now().Add(-time.Minute).UTC().Truncate(time.Second),
		LastSyncAt:     time.Now().UTC().Truncate(time.Second),
		TaskObjective:  "persist session memory",
		CurrentStatus:  "wrote session memory to disk",
		KeyDecisions:   []string{"use session_memory.json"},
		RecentWork:     []string{"added file store round-trip test"},
		PendingItems:   []string{"wire store into setup"},
		FileReferences: []string{"sessions/file/store.go"},
		ImportantCtx:   "stored alongside history.jsonl",
	}

	if err := store.WriteSessionMemory(sessionID, record); err != nil {
		t.Fatalf("WriteSessionMemory failed: %v", err)
	}

	loaded, err := store.ReadSessionMemory(sessionID)
	if err != nil {
		t.Fatalf("ReadSessionMemory failed: %v", err)
	}
	if loaded == nil {
		t.Fatal("expected persisted session memory record")
	}
	if loaded.TaskObjective != record.TaskObjective {
		t.Fatalf("expected task objective %q, got %q", record.TaskObjective, loaded.TaskObjective)
	}
	if len(loaded.FileReferences) != 1 || loaded.FileReferences[0] != "sessions/file/store.go" {
		t.Fatalf("unexpected file references: %#v", loaded.FileReferences)
	}

	if _, err := os.Stat(filepath.Join(tmpDir, sessionID, "session_memory.json")); err != nil {
		t.Fatalf("expected session_memory.json to exist: %v", err)
	}
}

func TestCalibratedMessageTokensPersistAcrossReload(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "session_tokens_test")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	store := NewFileSessionStore(tmpDir)
	if err := store.EnsureDir(); err != nil {
		t.Fatalf("failed to ensure dir: %v", err)
	}

	sessionID := "test-session-tokens-001"
	sess, err := store.Create(sessionID, nil)
	if err != nil {
		t.Fatalf("failed to create session: %v", err)
	}

	sess.AppendMessage(&types.Message{Role: types.RoleUser, Content: "Hello world", Tokens: 42})

	reloaded, err := store.Load(sessionID, nil)
	if err != nil {
		t.Fatalf("failed to reload session: %v", err)
	}

	history := reloaded.GetHistory()
	if len(history) != 1 {
		t.Fatalf("expected 1 message after reload, got %d", len(history))
	}
	if history[0].Tokens != 42 {
		t.Fatalf("expected persisted calibrated tokens=42, got %d", history[0].Tokens)
	}
}

func TestSessionRecordsPersistAcrossReload(t *testing.T) {
	ctx := context.Background()
	store := NewFileSessionStore(t.TempDir())
	sess, err := store.Create("record-session", nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := sess.UpdateRecord(ctx, "file_instructions.test", func([]byte) ([]byte, error) {
		return []byte(`{"version":1,"directories":["."]}`), nil
	}); err != nil {
		t.Fatal(err)
	}

	reloaded, err := store.Load("record-session", nil)
	if err != nil {
		t.Fatal(err)
	}
	got, err := reloaded.ReadRecord(ctx, "file_instructions.test")
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != `{"version":1,"directories":["."]}` {
		t.Fatalf("record = %s", got)
	}
	if _, err := os.Stat(filepath.Join(store.sessionDir("record-session"), "state", "file_instructions.test.json")); err != nil {
		t.Fatalf("record file: %v", err)
	}
}

func TestSessionRecordUpdatesAreSerializedAcrossStores(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	first := NewFileSessionStore(dir)
	second := NewFileSessionStore(dir)
	if _, err := first.Create("record-session", nil); err != nil {
		t.Fatal(err)
	}

	start := make(chan struct{})
	errCh := make(chan error, 2)
	var wg sync.WaitGroup
	for _, store := range []*FileSessionStore{first, second} {
		wg.Add(1)
		go func(store *FileSessionStore) {
			defer wg.Done()
			<-start
			errCh <- store.UpdateSessionRecord(ctx, "record-session", "counter", func(current []byte) ([]byte, error) {
				value := 0
				if len(current) > 0 {
					_, err := fmt.Sscanf(string(current), "%d", &value)
					if err != nil {
						return nil, err
					}
				}
				return []byte(fmt.Sprintf("%d", value+1)), nil
			})
		}(store)
	}
	close(start)
	wg.Wait()
	close(errCh)
	for err := range errCh {
		if err != nil {
			t.Fatal(err)
		}
	}
	got, err := first.ReadSessionRecord(ctx, "record-session", "counter")
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "2" {
		t.Fatalf("counter = %q, want 2", got)
	}
}
