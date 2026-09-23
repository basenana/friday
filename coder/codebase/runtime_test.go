package codebase

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	eventbus "github.com/hyponet/eventbus/bus"

	"github.com/basenana/friday/coder/project"
	"github.com/basenana/friday/core/actor/events"
	"github.com/basenana/friday/core/providers"
	"github.com/basenana/friday/core/providers/fallback"
	"github.com/basenana/friday/core/tools"
	"github.com/basenana/friday/core/types"
	"github.com/basenana/friday/sandbox"
	"github.com/basenana/friday/sessions"
	sessionfile "github.com/basenana/friday/sessions/file"
)

type runtimeTestClient struct{}

func (runtimeTestClient) Completion(context.Context, providers.Request) providers.Response {
	response := providers.NewCommonResponse()
	close(response.Stream)
	close(response.Err)
	return response
}
func (runtimeTestClient) CompletionNonStreaming(context.Context, providers.Request) (string, error) {
	return "", nil
}
func (runtimeTestClient) StructuredPredict(context.Context, providers.Request, any) error { return nil }

func newRuntimeTestFixture(t *testing.T) (*Runtime, *project.Manager) {
	t.Helper()
	data := t.TempDir()
	root := t.TempDir()
	projectStore := project.NewFileStore(filepath.Join(data, "projects"))
	proj, err := project.Open(root, projectStore)
	if err != nil {
		t.Fatal(err)
	}
	sessionStore := sessionfile.NewFileSessionStore(filepath.Join(data, "sessions"))
	base := sessions.NewManager(sessionStore, filepath.Join(data, "current"), "")
	manager := project.NewManager(proj, base)
	pool := fallback.NewModelPool([]fallback.ModelEntry{{Name: "test", Client: runtimeTestClient{}}})
	cfg := sandbox.DefaultConfig()
	cfg.Sandbox.Enabled = false
	runtime, err := New(Options{DataDir: data, Project: proj, ProjectManager: manager, ModelPool: pool, Bus: eventbus.NewBus(), Sandbox: cfg})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = runtime.Close() })
	return runtime, manager
}

func TestEnsureIndexSessionRemovesRootWhenSessionPointerCannotBeWritten(t *testing.T) {
	runtime, manager := newRuntimeTestFixture(t)
	if err := runtime.store.ensureLayout(); err != nil {
		t.Fatal(err)
	}
	if err := runtime.ensureRunner(); err != nil {
		t.Fatal(err)
	}
	spec, err := loadSpec(runtime.store.specPath(), runtime.opts.ModelPool)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(runtime.store.sessionPath(), 0o700); err != nil {
		t.Fatal(err)
	}
	if _, err := runtime.ensureIndexSession(context.Background(), spec); err == nil {
		t.Fatal("expected SESSION write failure")
	}
	items, err := manager.List(true)
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 0 {
		t.Fatalf("orphaned Index roots: %+v", items)
	}
}

func TestCodebaseProjectResourceWriterUsesDedicatedClonedPolicy(t *testing.T) {
	runtime, _ := newRuntimeTestFixture(t)
	if err := runtime.store.ensureLayout(); err != nil {
		t.Fatal(err)
	}
	sharedWrite := append([]string(nil), runtime.opts.Sandbox.Sandbox.Filesystem.Write...)
	if err := runtime.ensureRunner(); err != nil {
		t.Fatal(err)
	}
	if len(runtime.opts.Sandbox.Sandbox.Filesystem.Write) != len(sharedWrite) {
		t.Fatalf("shared sandbox write roots mutated: %#v", runtime.opts.Sandbox.Sandbox.Filesystem.Write)
	}
	var writeTool *tools.Tool
	for _, tool := range runtime.runner.indexTools {
		if tool.Name == sandbox.FsWriteToolName {
			writeTool = tool
			break
		}
	}
	if writeTool == nil {
		t.Fatal("Codebase runner is missing fs_write")
	}
	target := filepath.Join(runtime.store.dir, "knowledge", "shared.md")
	result, err := writeTool.Handler(context.Background(), &tools.Request{Arguments: map[string]interface{}{
		"path": target, "content": "shared knowledge",
	}})
	if err != nil || result == nil || result.IsError {
		t.Fatalf("Codebase write result = %#v, %v", result, err)
	}
	if got, err := os.ReadFile(target); err != nil || string(got) != "shared knowledge" {
		t.Fatalf("Codebase write = %q, %v", got, err)
	}
}

func TestPersistStatusFailureDoesNotCommitInMemory(t *testing.T) {
	runtime, _ := newRuntimeTestFixture(t)
	if err := runtime.store.ensureLayout(); err != nil {
		t.Fatal(err)
	}
	runtime.mu.Lock()
	runtime.initialized = true
	runtime.status = defaultStatus(StateIdle)
	runtime.mu.Unlock()
	if err := os.Remove(runtime.store.statusPath()); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(runtime.store.statusPath(), 0o700); err != nil {
		t.Fatal(err)
	}
	candidate := runtime.Status()
	candidate.State = StateIndexing
	if err := runtime.persistStatus(candidate); err == nil {
		t.Fatal("expected STATUS write failure")
	}
	if got := runtime.Status().State; got != StateIdle {
		t.Fatalf("failed persistence committed state %q", got)
	}
}

func TestIndexCommitRejectsInvalidatedSpec(t *testing.T) {
	runtime, _ := newRuntimeTestFixture(t)
	runtime.mu.Lock()
	runtime.status = defaultStatus(StateError)
	runtime.status.OperationID = "operation"
	runtime.specErr = context.Canceled
	runtime.mu.Unlock()
	if _, err := runtime.indexCommitStatus("operation"); !errors.Is(err, context.Canceled) {
		t.Fatalf("commit err=%v, want invalid spec error", err)
	}
}

func TestPersistStatusCommitsWrittenTimestamp(t *testing.T) {
	runtime, _ := newRuntimeTestFixture(t)
	if err := runtime.store.ensureLayout(); err != nil {
		t.Fatal(err)
	}
	runtime.mu.Lock()
	runtime.initialized = true
	runtime.status = defaultStatus(StateIdle)
	runtime.mu.Unlock()

	candidate := runtime.Status()
	candidate.State = StateDegraded
	if err := runtime.persistStatus(candidate); err != nil {
		t.Fatal(err)
	}
	persisted, err := runtime.store.loadStatus()
	if err != nil {
		t.Fatal(err)
	}
	if runtime.Status().UpdatedAt.IsZero() || !runtime.Status().UpdatedAt.Equal(persisted.UpdatedAt) {
		t.Fatalf("memory UpdatedAt=%v disk UpdatedAt=%v", runtime.Status().UpdatedAt, persisted.UpdatedAt)
	}
}

func TestRefreshStatusPersistsInvalidSpec(t *testing.T) {
	runtime, _ := newRuntimeTestFixture(t)
	if err := runtime.store.ensureLayout(); err != nil {
		t.Fatal(err)
	}
	runtime.mu.Lock()
	runtime.initialized = true
	runtime.enabled = true
	runtime.status = defaultStatus(StateIdle)
	runtime.mu.Unlock()
	if err := os.WriteFile(runtime.store.specPath(), []byte("---\nversion: 1\nunknown: true\n---\npolicy\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	status, err := runtime.RefreshStatus()
	if err == nil || status.State != StateError {
		t.Fatalf("status=%+v err=%v", status, err)
	}
	persisted, loadErr := runtime.store.loadStatus()
	if loadErr != nil {
		t.Fatal(loadErr)
	}
	if persisted.State != StateError || persisted.LastError == "" {
		t.Fatalf("invalid spec status not persisted: %+v", persisted)
	}
}

func TestCloseCanFinishAfterAnEarlierTimeout(t *testing.T) {
	runtime, _ := newRuntimeTestFixture(t)
	if err := runtime.store.ensureLayout(); err != nil {
		t.Fatal(err)
	}
	status := defaultStatus(StateIndexing)
	if _, err := runtime.store.writeStatus(status); err != nil {
		t.Fatal(err)
	}
	runtime.mu.Lock()
	runtime.initialized = true
	runtime.status = status
	runtime.closeWait = 10 * time.Millisecond
	runtime.mu.Unlock()
	runtime.contextWG.Add(1)
	if err := runtime.Close(); err == nil {
		t.Fatal("expected close timeout")
	}
	runtime.contextWG.Done()
	runtime.mu.Lock()
	runtime.closeWait = time.Second
	runtime.mu.Unlock()
	if err := runtime.Close(); err != nil {
		t.Fatalf("second Close did not observe completed cleanup: %v", err)
	}
	data, err := os.ReadFile(runtime.store.statusPath())
	if err != nil {
		t.Fatal(err)
	}
	persisted, err := parseStatus(data)
	if err != nil {
		t.Fatal(err)
	}
	if persisted.State != StateDegraded {
		t.Fatalf("persisted state=%q, want degraded", persisted.State)
	}
}

func TestIndexSessionMutationKeepsSchedulerBlockedAcrossSessionSwitch(t *testing.T) {
	runtime, _ := newRuntimeTestFixture(t)
	runtime.mu.Lock()
	runtime.indexSessionID = "index"
	runtime.activeSessionID = "index"
	runtime.mu.Unlock()

	mutation, err := runtime.PrepareIndexSessionMutation(context.Background(), "index")
	if err != nil {
		t.Fatal(err)
	}
	transition, err := runtime.PrepareSessionSwitch(context.Background(), "chat")
	if err != nil {
		t.Fatal(err)
	}
	transition.Commit()
	runtime.mu.Lock()
	if !runtime.switching || runtime.activeSessionID != "chat" {
		t.Fatalf("switching=%v active=%q", runtime.switching, runtime.activeSessionID)
	}
	runtime.mu.Unlock()
	mutation.Commit()
	runtime.mu.Lock()
	defer runtime.mu.Unlock()
	if runtime.switching {
		t.Fatal("mutation commit did not release scheduler barrier")
	}
}

func TestPrepareSessionSwitchRejectsOverlappingTransition(t *testing.T) {
	runtime, _ := newRuntimeTestFixture(t)
	first, err := runtime.PrepareSessionSwitch(context.Background(), "first")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := runtime.PrepareSessionSwitch(context.Background(), "second"); err == nil {
		t.Fatal("overlapping transition was accepted")
	}
	first.Abort()
	third, err := runtime.PrepareSessionSwitch(context.Background(), "third")
	if err != nil {
		t.Fatalf("transition remained blocked after abort: %v", err)
	}
	third.Abort()
}

func TestBeginShutdownRejectsNewCommands(t *testing.T) {
	runtime, _ := newRuntimeTestFixture(t)
	runtime.BeginShutdown()
	if err := runtime.RequestIndex(context.Background()); err == nil {
		t.Fatal("RequestIndex accepted during shutdown")
	}
	if _, err := runtime.PrepareSessionSwitch(context.Background(), "target"); err == nil {
		t.Fatal("session transition accepted during shutdown")
	}
}

func TestBeginShutdownCancelsActiveOperations(t *testing.T) {
	runtime, _ := newRuntimeTestFixture(t)
	indexCtx, cancelIndex := context.WithCancel(context.Background())
	queryCtx, cancelQuery := context.WithCancel(context.Background())
	runtime.mu.Lock()
	runtime.indexCancel = cancelIndex
	runtime.contextCancels["query"] = cancelQuery
	runtime.mu.Unlock()

	runtime.BeginShutdown()
	select {
	case <-indexCtx.Done():
	default:
		t.Fatal("active Index was not cancelled")
	}
	select {
	case <-queryCtx.Done():
	default:
		t.Fatal("active Context/Query was not cancelled")
	}
}

func TestUnknownFinishedRunDoesNotEndActiveUserTurn(t *testing.T) {
	runtime, _ := newRuntimeTestFixture(t)
	runtime.mu.Lock()
	runtime.enabled = true
	runtime.activeSessionID = "session"
	runtime.spec.Schedule.IdleDelay.Duration = time.Hour
	runtime.mu.Unlock()

	body, err := json.Marshal(events.InputAcceptedBody{Role: types.RoleUser, Text: "question"})
	if err != nil {
		t.Fatal(err)
	}
	runtime.consumeConversationEvent("session", events.Event{Type: events.KindCustom, Name: events.CustomInputAccepted, RunID: "user", Payload: body})
	runtime.consumeConversationEvent("session", events.Event{Type: events.KindRunFinished, RunID: "unknown"})

	runtime.mu.Lock()
	defer runtime.mu.Unlock()
	if runtime.activeTurns != 1 {
		t.Fatalf("activeTurns=%d, want 1", runtime.activeTurns)
	}
	if runtime.idleTimer != nil {
		t.Fatal("idle timer started while user turn is active")
	}
}

func TestConversationTriggerRecordsLastRequested(t *testing.T) {
	runtime, _ := newRuntimeTestFixture(t)
	runtime.mu.Lock()
	runtime.enabled = true
	runtime.activeSessionID = "session"
	runtime.spec.Schedule.ConversationPairs = 1
	runtime.status.LastRequested = map[string]time.Time{}
	runtime.mu.Unlock()

	input, err := json.Marshal(events.InputAcceptedBody{Role: types.RoleUser, Text: "question"})
	if err != nil {
		t.Fatal(err)
	}
	text, err := json.Marshal(events.TextMessageContentData{Content: "answer"})
	if err != nil {
		t.Fatal(err)
	}
	finished, err := json.Marshal(events.RunFinishedData{StopReason: "end_turn"})
	if err != nil {
		t.Fatal(err)
	}
	runtime.consumeConversationEvent("session", events.Event{Type: events.KindCustom, Name: events.CustomInputAccepted, RunID: "run", Payload: input})
	runtime.consumeConversationEvent("session", events.Event{Type: events.KindTextMessageContent, RunID: "run", Payload: text})
	runtime.consumeConversationEvent("session", events.Event{Type: events.KindRunFinished, RunID: "run", Payload: finished})

	if runtime.Status().LastRequested["conversation"].IsZero() {
		t.Fatal("conversation trigger did not record LastRequested")
	}
}
