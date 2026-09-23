package tui

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/basenana/friday/bus"
	codebasepkg "github.com/basenana/friday/coder/codebase"
	codercmds "github.com/basenana/friday/coder/commands"
	coderloop "github.com/basenana/friday/coder/loop"
	projectpkg "github.com/basenana/friday/coder/project"
	"github.com/basenana/friday/config"
	"github.com/basenana/friday/core/actor/events"
	"github.com/basenana/friday/core/providers"
	coresession "github.com/basenana/friday/core/session"
	"github.com/basenana/friday/core/tools"
	"github.com/basenana/friday/core/types"
	"github.com/basenana/friday/sessions"
	sessionfile "github.com/basenana/friday/sessions/file"
	fridayworktree "github.com/basenana/friday/worktree"
)

type worktreeRuntimeTestFixture struct {
	cfg        *config.Config
	sessions   *sessions.Manager
	store      *sessionfile.FileSessionStore
	service    *fridayworktree.Service
	supervisor *worktreeRuntimeSupervisor
	mainID     string
}

type failNextWorktreeMetadataUpdateStore struct {
	fridayworktree.Store
	err      error
	failNext bool
}

func (s *failNextWorktreeMetadataUpdateStore) UpdateMetadata(id string, update func(*fridayworktree.Metadata) error) error {
	if s.failNext {
		s.failNext = false
		return s.err
	}
	return s.Store.UpdateMetadata(id, update)
}

type hookProbeRequest struct{ tools []*tools.Tool }

func (r *hookProbeRequest) GetUserMessage() string       { return "probe" }
func (r *hookProbeRequest) SetUserMessage(string)        {}
func (r *hookProbeRequest) GetAgentMessage() string      { return "" }
func (r *hookProbeRequest) SetAgentMessage(string)       {}
func (r *hookProbeRequest) GetTools() []*tools.Tool      { return r.tools }
func (r *hookProbeRequest) AppendTools(v ...*tools.Tool) { r.tools = append(r.tools, v...) }
func (r *hookProbeRequest) hasTool(name string) bool {
	for _, tool := range r.tools {
		if tool != nil && tool.Name == name {
			return true
		}
	}
	return false
}

func newWorktreeRuntimeTestFixture(t *testing.T) *worktreeRuntimeTestFixture {
	t.Helper()
	repo := newWorktreeRuntimeGitRepo(t)
	data := t.TempDir()
	cfg := config.DefaultConfig()
	cfg.DataDir = data
	cfg.Workspace = filepath.Join(data, "workspace")
	cfg.Memory.Enabled = false
	store := sessionfile.NewFileSessionStore(cfg.SessionsPath())
	sessMgr := sessions.NewManager(store, filepath.Join(data, "current"), "")
	service, err := fridayworktree.Open(context.Background(), repo, filepath.Join(t.TempDir(), "linked"), "friday/", data)
	if err != nil {
		t.Fatal(err)
	}
	current, err := service.Current(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if err := service.Associate(current.Path, current.Branch, service.ProjectIdentity().ID, ""); err != nil {
		t.Fatal(err)
	}
	worktreeStore, err := fridayworktree.NewStore(cfg.ProjectsPath(), service.ProjectIdentity().ID)
	if err != nil {
		t.Fatal(err)
	}
	mainMeta, err := worktreeStore.Get(current.Path)
	if err != nil {
		t.Fatal(err)
	}
	supervisor, err := newWorktreeRuntimeSupervisor(service, sessMgr, cfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := supervisor.Close(); err != nil {
			t.Errorf("close supervisor: %v", err)
		}
	})
	return &worktreeRuntimeTestFixture{
		cfg: cfg, sessions: sessMgr, store: store, service: service, supervisor: supervisor, mainID: mainMeta.ID,
	}
}

func TestWorktreeRuntimeSupervisorRetainsBackgroundRuntimeAndRestoresTranscript(t *testing.T) {
	fixture := newWorktreeRuntimeTestFixture(t)
	ctx := context.Background()
	runtimeA, err := fixture.supervisor.Activate(ctx, fixture.mainID)
	if err != nil {
		t.Fatal(err)
	}
	runtimeB, err := fixture.supervisor.Create(ctx, "second runtime")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := fixture.supervisor.Activate(ctx, runtimeA.id); err != nil {
		t.Fatal(err)
	}

	now := time.Now()
	if err := fixture.store.AppendMessages(runtimeA.sessionID, types.Message{Role: types.RoleUser, Content: "transcript from A", Time: now}); err != nil {
		t.Fatal(err)
	}
	if err := fixture.store.AppendMessages(runtimeB.sessionID, types.Message{Role: types.RoleUser, Content: "transcript from B", Time: now.Add(time.Second)}); err != nil {
		t.Fatal(err)
	}
	loopDispatched := make(chan struct{})
	allowLoopExit := make(chan struct{})
	defer close(allowLoopExit)
	var signalDispatch sync.Once
	runtimeA.loop = coderloop.NewManager(runtimeA.bus, coderloop.WithInputDispatcher(func(bus.Envelope) error {
		signalDispatch.Do(func() { close(loopDispatched) })
		<-allowLoopExit
		return errors.New("test loop stopped")
	}))
	if err := runtimeA.loop.Start(ctx, runtimeA.lifecycle.Current(), "keep running in the background"); err != nil {
		t.Fatal(err)
	}
	select {
	case <-loopDispatched:
	case <-time.After(5 * time.Second):
		t.Fatal("background loop did not dispatch")
	}

	m := newWorktreeRuntimeTestModel(t, fixture, runtimeA)
	oldRegistry, oldLoop, oldFeed := runtimeA.registry, runtimeA.loop, m.feed
	preparedB, err := fixture.supervisor.prepareActivation(ctx, runtimeB.id, m.width, m.height, "")
	if err != nil {
		t.Fatal(err)
	}
	m.commitPreparedWorktree(worktreePreparedMsg{token: m.worktreeGeneration, runtime: preparedB})

	if m.worktreeRuntime != runtimeB || m.registry != runtimeB.registry || m.loopManager != runtimeB.loop {
		t.Fatal("foreground did not move to the selected retained runtime")
	}
	select {
	case <-oldFeed.Done():
	default:
		t.Fatal("previous foreground feed remained subscribed")
	}
	if m.feed == nil || m.feed == oldFeed {
		t.Fatal("selected worktree did not receive a new foreground feed")
	}
	if !transcriptContains(m.messages, "transcript from B") || transcriptContains(m.messages, "transcript from A") {
		t.Fatalf("selected transcript = %#v", m.messages)
	}
	if runtimeA.registry != oldRegistry || runtimeA.loop != oldLoop {
		t.Fatal("background runtime resources were replaced")
	}
	loopState, err := runtimeA.lifecycle.Current().ReadRecord(ctx, coderloop.StateNamespace)
	if err != nil {
		t.Fatal(err)
	}
	if got, want := strings.TrimSpace(string(loopState)), string(coderloop.StateActive); got != want {
		t.Fatalf("background loop state = %q, want %q; activation stopped or detached it", got, want)
	}
	_, release, err := oldRegistry.AcquireLifecycle(runtimeA.sessionID)
	if err != nil {
		t.Fatalf("previous registry was shut down during activation: %v", err)
	}
	release()

	preparedA, err := fixture.supervisor.prepareActivation(ctx, runtimeA.id, m.width, m.height, "")
	if err != nil {
		t.Fatal(err)
	}
	m.commitPreparedWorktree(worktreePreparedMsg{token: m.worktreeGeneration, runtime: preparedA})
	if m.registry != oldRegistry || m.loopManager != oldLoop {
		t.Fatal("reactivation did not reuse the retained runtime")
	}
	if !transcriptContains(m.messages, "transcript from A") || transcriptContains(m.messages, "transcript from B") {
		t.Fatalf("restored transcript = %#v", m.messages)
	}
}

func TestWorktreeTabRoundTripRestoresLiveConfirmationForm(t *testing.T) {
	fixture := newWorktreeRuntimeTestFixture(t)
	ctx := context.Background()
	runtimeA, err := fixture.supervisor.Activate(ctx, fixture.mainID)
	if err != nil {
		t.Fatal(err)
	}
	runtimeB, err := fixture.supervisor.Create(ctx, "confirmation switch target")
	if err != nil {
		t.Fatal(err)
	}
	actorA, err := runtimeA.registry.GetOrCreate(runtimeA.sessionID)
	if err != nil {
		t.Fatal(err)
	}
	var requestInput *tools.Tool
	for _, tool := range actorA.Tools() {
		if tool != nil && tool.Name == "request_user_input" {
			requestInput = tool
			break
		}
	}
	if requestInput == nil {
		t.Fatal("request_user_input tool is unavailable")
	}
	formDone := make(chan struct{})
	go func() {
		defer close(formDone)
		_, _ = requestInput.Handler(ctx, &tools.Request{SessionID: runtimeA.sessionID, Arguments: map[string]any{
			"question_1": "Apply this change?",
			"options_1":  []any{"Yes", "No"},
		}})
	}()
	var formID string
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if pending := runtimeA.registry.SessionPendingForms(runtimeA.sessionID); len(pending) > 0 {
			formID = pending[0].FormID
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if formID == "" {
		t.Fatal("live confirmation form was not registered")
	}
	defer func() {
		_ = actorA.CancelForm(formID)
		select {
		case <-formDone:
		case <-time.After(time.Second):
			t.Error("confirmation tool did not exit")
		}
	}()

	m := newWorktreeRuntimeTestModel(t, fixture, runtimeA)
	preparedB, err := fixture.supervisor.prepareActivation(ctx, runtimeB.id, m.width, m.height, "")
	if err != nil {
		t.Fatal(err)
	}
	m.commitPreparedWorktree(worktreePreparedMsg{token: m.worktreeGeneration, runtime: preparedB})
	if m.form != nil {
		t.Fatal("target worktree inherited the previous worktree form")
	}

	preparedA, err := fixture.supervisor.prepareActivation(ctx, runtimeA.id, m.width, m.height, "")
	if err != nil {
		t.Fatal(err)
	}
	m.commitPreparedWorktree(worktreePreparedMsg{token: m.worktreeGeneration, runtime: preparedA})
	if m.form == nil || m.form.id != formID {
		t.Fatalf("restored form = %#v, want %q", m.form, formID)
	}
}

func TestPreparedWorktreeActivationCapturesEventsBeforeCommitAndRestoresRunningState(t *testing.T) {
	fixture := newWorktreeRuntimeTestFixture(t)
	ctx := context.Background()
	runtimeA, err := fixture.supervisor.Activate(ctx, fixture.mainID)
	if err != nil {
		t.Fatal(err)
	}
	runtimeB, err := fixture.supervisor.Create(ctx, "event handoff target")
	if err != nil {
		t.Fatal(err)
	}
	m := newWorktreeRuntimeTestModel(t, fixture, runtimeA)
	prepared, err := fixture.supervisor.prepareActivation(ctx, runtimeB.id, m.width, m.height, "")
	if err != nil {
		t.Fatal(err)
	}
	if prepared.feed == nil {
		t.Fatal("activation did not subscribe before building the projection")
	}
	evt := events.NewEvent(events.KindRunStarted, "background-run")
	runtimeB.bus.Publish(bus.TopicRun(runtimeB.sessionID, "started"), bus.Envelope{
		Event: evt, Topic: bus.TopicRun(runtimeB.sessionID, "started"), Session: runtimeB.sessionID,
	})
	select {
	case got := <-prepared.feed.Events():
		if got.RunID != evt.RunID {
			t.Fatalf("captured event run = %q, want %q", got.RunID, evt.RunID)
		}
	case <-time.After(time.Second):
		t.Fatal("event published during activation preparation was lost")
	}
	prepared.running = true
	m.commitPreparedWorktree(worktreePreparedMsg{token: m.worktreeGeneration, runtime: prepared})
	if !m.running {
		t.Fatal("reactivated worktree lost its in-flight running state")
	}
	if m.feed != prepared.feed {
		t.Fatal("commit replaced the gap-free prepared feed")
	}
}

func TestWorktreeRuntimeSupervisorActivationFailurePreservesForeground(t *testing.T) {
	fixture := newWorktreeRuntimeTestFixture(t)
	ctx := context.Background()
	runtimeA, err := fixture.supervisor.Activate(ctx, fixture.mainID)
	if err != nil {
		t.Fatal(err)
	}
	m := newWorktreeRuntimeTestModel(t, fixture, runtimeA)
	oldFeed := m.feed

	store, err := fridayworktree.NewStore(fixture.cfg.ProjectsPath(), fixture.service.ProjectIdentity().ID)
	if err != nil {
		t.Fatal(err)
	}
	missingPath := filepath.Join(t.TempDir(), "missing-checkout")
	meta, err := store.Ensure(fridayworktree.Metadata{
		ID: "missing-worktree", Name: "missing-worktree", Path: missingPath, Branch: "friday/missing-worktree",
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := fixture.supervisor.Activate(ctx, meta.ID); err == nil {
		t.Fatal("activation of a missing checkout succeeded")
	}
	if fixture.supervisor.active != runtimeA {
		t.Fatal("failed activation replaced the supervisor foreground")
	}
	if m.worktreeRuntime != runtimeA || m.registry != runtimeA.registry || m.feed != oldFeed {
		t.Fatal("failed activation changed the TUI foreground")
	}
	select {
	case <-oldFeed.Done():
		t.Fatal("failed activation closed the current UI feed")
	default:
	}
	_, release, err := runtimeA.registry.AcquireLifecycle(runtimeA.sessionID)
	if err != nil {
		t.Fatalf("failed activation stopped the current runtime: %v", err)
	}
	release()
}

func TestWorktreeRuntimeSupervisorConcurrentActivationReusesRuntime(t *testing.T) {
	fixture := newWorktreeRuntimeTestFixture(t)
	ctx := context.Background()
	want, err := fixture.supervisor.Activate(ctx, fixture.mainID)
	if err != nil {
		t.Fatal(err)
	}

	const workers = 12
	got := make([]*worktreeRuntime, workers)
	errs := make([]error, workers)
	var wg sync.WaitGroup
	for i := range got {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			got[i], errs[i] = fixture.supervisor.Activate(ctx, fixture.mainID)
		}(i)
	}
	wg.Wait()
	for i, err := range errs {
		if err != nil {
			t.Fatalf("worker %d: %v", i, err)
		}
		if got[i] != want {
			t.Fatalf("worker %d received a different runtime", i)
		}
	}
	if len(fixture.supervisor.runtimes) != 1 {
		t.Fatalf("runtime count = %d, want 1", len(fixture.supervisor.runtimes))
	}
}

func TestWorktreeRuntimeSupervisorReplacesRuntimeWhenSessionReferenceChanges(t *testing.T) {
	fixture := newWorktreeRuntimeTestFixture(t)
	ctx := context.Background()
	stale, err := fixture.supervisor.Activate(ctx, fixture.mainID)
	if err != nil {
		t.Fatal(err)
	}
	loopDispatched := make(chan struct{})
	allowLoopExit := make(chan struct{})
	defer close(allowLoopExit)
	stale.loop = coderloop.NewManager(stale.bus, coderloop.WithInputDispatcher(func(bus.Envelope) error {
		close(loopDispatched)
		<-allowLoopExit
		return errors.New("test loop stopped")
	}))
	if err := stale.loop.Start(ctx, stale.lifecycle.Current(), "replacement closure"); err != nil {
		t.Fatal(err)
	}
	select {
	case <-loopDispatched:
	case <-time.After(5 * time.Second):
		t.Fatal("obsolete loop did not start")
	}
	replacementLifecycle, err := fixture.sessions.CreateRoot(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	replacementID := replacementLifecycle.RootID()
	if err := replacementLifecycle.Close(); err != nil {
		t.Fatal(err)
	}
	if err := fixture.supervisor.store.UpdateSession(fixture.mainID, replacementID); err != nil {
		t.Fatal(err)
	}

	replacement, err := fixture.supervisor.Activate(ctx, fixture.mainID)
	if err != nil {
		t.Fatal(err)
	}
	if replacement == stale {
		t.Fatal("activation reused a runtime bound to the previous session")
	}
	if replacement.sessionID != replacementID {
		t.Fatalf("replacement session = %q, want %q", replacement.sessionID, replacementID)
	}
	if fixture.supervisor.active != replacement || fixture.supervisor.runtimes[fixture.mainID] != replacement {
		t.Fatal("replacement runtime was not published consistently")
	}
	if _, release, err := stale.registry.AcquireLifecycle(stale.sessionID); err == nil {
		release()
		t.Fatal("obsolete same-checkout registry still accepted lifecycle acquisition after replacement committed")
	}
	state, err := stale.lifecycle.Current().ReadRecord(ctx, coderloop.StateNamespace)
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.TrimSpace(string(state)); got != string(coderloop.StateSuspended) {
		t.Fatalf("obsolete loop state = %q, want %q", got, coderloop.StateSuspended)
	}
}

func TestWorktreeRuntimeSupervisorDoesNotCloseObsoleteRuntimeBeforeReplacementCommit(t *testing.T) {
	fixture := newWorktreeRuntimeTestFixture(t)
	ctx := context.Background()
	stale, err := fixture.supervisor.Activate(ctx, fixture.mainID)
	if err != nil {
		t.Fatal(err)
	}
	replacementLifecycle, err := fixture.sessions.CreateRoot(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	replacementID := replacementLifecycle.RootID()
	if err := replacementLifecycle.Close(); err != nil {
		t.Fatal(err)
	}
	if err := fixture.supervisor.store.UpdateSession(fixture.mainID, replacementID); err != nil {
		t.Fatal(err)
	}

	prepared, err := fixture.supervisor.prepareActivation(ctx, fixture.mainID, 100, 40, "")
	if err != nil {
		t.Fatal(err)
	}
	_, release, err := stale.registry.AcquireLifecycle(stale.sessionID)
	if err != nil {
		t.Fatalf("preparing a replacement stopped the background runtime before commit: %v", err)
	}
	release()
	if prepared.runtime == stale {
		t.Fatal("replacement preparation reused stale runtime")
	}
}

func TestWorktreeRuntimeSupervisorConcurrentDifferentTargetPreparationsCommitOnlyOne(t *testing.T) {
	fixture := newWorktreeRuntimeTestFixture(t)
	ctx := context.Background()
	runtimeA, err := fixture.supervisor.Activate(ctx, fixture.mainID)
	if err != nil {
		t.Fatal(err)
	}
	runtimeB, err := fixture.supervisor.Create(ctx, "concurrent target B")
	if err != nil {
		t.Fatal(err)
	}
	runtimeC, err := fixture.supervisor.Create(ctx, "concurrent target C")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := fixture.supervisor.Activate(ctx, runtimeA.id); err != nil {
		t.Fatal(err)
	}
	m := newWorktreeRuntimeTestModel(t, fixture, runtimeA)

	ids := []string{runtimeB.id, runtimeC.id}
	prepared := make([]*preparedWorktreeRuntime, len(ids))
	errs := make([]error, len(ids))
	start := make(chan struct{})
	var wg sync.WaitGroup
	for i := range ids {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			<-start
			prepared[i], errs[i] = fixture.supervisor.prepareActivation(ctx, ids[i], m.width, m.height, "")
		}(i)
	}
	close(start)
	wg.Wait()
	for i, err := range errs {
		if err != nil {
			t.Fatalf("prepare target %d: %v", i, err)
		}
	}

	m.commitPreparedWorktree(worktreePreparedMsg{token: m.worktreeGeneration, runtime: prepared[0]})
	winner := prepared[0].runtime
	winningFeed := m.feed
	if m.worktreeRuntime != winner || fixture.supervisor.active != winner {
		t.Fatal("first prepared activation did not commit model and supervisor together")
	}
	m.commitPreparedWorktree(worktreePreparedMsg{token: m.worktreeGeneration, runtime: prepared[1]})
	if m.worktreeRuntime != winner || m.feed != winningFeed || fixture.supervisor.active != winner {
		t.Fatal("stale prepared activation displaced the committed target")
	}
	if len(m.messages) == 0 || !strings.Contains(m.messages[len(m.messages)-1].content, "stale") {
		t.Fatalf("stale commit error was not surfaced: %#v", m.messages)
	}
}

func TestWorktreeRuntimeSupervisorPreparationFailureAfterConstructionPreservesForeground(t *testing.T) {
	fixture := newWorktreeRuntimeTestFixture(t)
	ctx := context.Background()
	runtimeA, err := fixture.supervisor.Activate(ctx, fixture.mainID)
	if err != nil {
		t.Fatal(err)
	}
	runtimeB, err := fixture.supervisor.Create(ctx, "projection failure target")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := fixture.supervisor.Activate(ctx, runtimeA.id); err != nil {
		t.Fatal(err)
	}
	m := newWorktreeRuntimeTestModel(t, fixture, runtimeA)
	oldFeed := m.feed
	historyPath := filepath.Join(fixture.cfg.SessionsPath(), runtimeB.sessionID, "history.jsonl")
	if err := os.Remove(historyPath); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(historyPath, 0o755); err != nil {
		t.Fatal(err)
	}

	if _, err := fixture.supervisor.prepareActivation(ctx, runtimeB.id, m.width, m.height, ""); err == nil || !strings.Contains(err.Error(), "restore transcript") {
		t.Fatalf("preparation error = %v, want transcript restore failure", err)
	}
	if fixture.supervisor.active != runtimeA || m.worktreeRuntime != runtimeA || m.feed != oldFeed {
		t.Fatal("failure after target construction changed the foreground")
	}
	select {
	case <-oldFeed.Done():
		t.Fatal("failure after target construction closed the foreground feed")
	default:
	}
	_, release, err := runtimeB.registry.AcquireLifecycle(runtimeB.sessionID)
	if err != nil {
		t.Fatalf("preparation failure stopped an existing target runtime: %v", err)
	}
	release()
}

func TestWorktreeRuntimeSupervisorLoopAttachFailurePreservesForeground(t *testing.T) {
	fixture := newWorktreeRuntimeTestFixture(t)
	ctx := context.Background()
	runtimeA, err := fixture.supervisor.Activate(ctx, fixture.mainID)
	if err != nil {
		t.Fatal(err)
	}
	runtimeB, err := fixture.supervisor.Create(ctx, "attach failure target")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := fixture.supervisor.Activate(ctx, runtimeA.id); err != nil {
		t.Fatal(err)
	}
	m := newWorktreeRuntimeTestModel(t, fixture, runtimeA)
	oldFeed := m.feed
	wantErr := errors.New("injected loop attach failure")
	fixture.supervisor.attachRuntime = func(context.Context, *worktreeRuntime) error { return wantErr }

	if _, err := fixture.supervisor.prepareActivation(ctx, runtimeB.id, m.width, m.height, ""); !errors.Is(err, wantErr) {
		t.Fatalf("preparation error = %v, want %v", err, wantErr)
	}
	if fixture.supervisor.active != runtimeA || m.worktreeRuntime != runtimeA || m.feed != oldFeed {
		t.Fatal("loop attach failure changed the foreground")
	}
	select {
	case <-oldFeed.Done():
		t.Fatal("loop attach failure closed the foreground feed")
	default:
	}
}

func TestWorktreeRuntimeSupervisorCloseRacingPreparationInvalidatesCommit(t *testing.T) {
	fixture := newWorktreeRuntimeTestFixture(t)
	ctx := context.Background()
	runtimeA, err := fixture.supervisor.Activate(ctx, fixture.mainID)
	if err != nil {
		t.Fatal(err)
	}
	m := newWorktreeRuntimeTestModel(t, fixture, runtimeA)
	oldFeed := m.feed
	created, err := fixture.service.Create(ctx, "close race target")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = created.Rollback(context.Background()) })
	meta, err := fixture.supervisor.store.Get(created.Worktree.Path)
	if err != nil {
		t.Fatal(err)
	}

	attachStarted := make(chan struct{})
	allowAttach := make(chan struct{})
	originalAttach := fixture.supervisor.attachRuntime
	fixture.supervisor.attachRuntime = func(ctx context.Context, runtime *worktreeRuntime) error {
		if runtime.id == meta.ID {
			close(attachStarted)
			<-allowAttach
		}
		return originalAttach(ctx, runtime)
	}
	type preparationResult struct {
		prepared *preparedWorktreeRuntime
		err      error
	}
	preparedResult := make(chan preparationResult, 1)
	go func() {
		prepared, err := fixture.supervisor.prepareActivation(ctx, meta.ID, m.width, m.height, "")
		preparedResult <- preparationResult{prepared: prepared, err: err}
	}()
	<-attachStarted
	closeStarted := make(chan struct{})
	closeResult := make(chan error, 1)
	go func() {
		close(closeStarted)
		closeResult <- fixture.supervisor.Close()
	}()
	<-closeStarted
	select {
	case err := <-closeResult:
		t.Fatalf("Close completed while preparation still owned the transition: %v", err)
	case <-time.After(100 * time.Millisecond):
	}
	close(allowAttach)
	result := <-preparedResult
	if result.err != nil {
		t.Fatalf("prepare activation: %v", result.err)
	}
	if err := <-closeResult; err != nil {
		t.Fatal(err)
	}
	if err := fixture.supervisor.commitActivation(result.prepared); err == nil || !strings.Contains(err.Error(), "closed") {
		t.Fatalf("commit after Close error = %v", err)
	}
	if m.worktreeRuntime != runtimeA || m.registry != runtimeA.registry || m.feed != oldFeed {
		t.Fatal("Close racing preparation changed the model foreground")
	}
}

func TestWorktreeRuntimeSupervisorCloseSuspendsLoopsBeforeRegistryShutdown(t *testing.T) {
	fixture := newWorktreeRuntimeTestFixture(t)
	ctx := context.Background()
	runtime, err := fixture.supervisor.Activate(ctx, fixture.mainID)
	if err != nil {
		t.Fatal(err)
	}
	loopDispatched := make(chan struct{})
	allowLoopExit := make(chan struct{})
	runtime.loop = coderloop.NewManager(runtime.bus, coderloop.WithInputDispatcher(func(bus.Envelope) error {
		close(loopDispatched)
		<-allowLoopExit
		return errors.New("test loop stopped")
	}))
	if err := runtime.loop.Start(ctx, runtime.lifecycle.Current(), "close ordering"); err != nil {
		t.Fatal(err)
	}
	<-loopDispatched
	if err := fixture.supervisor.Close(); err != nil {
		t.Fatal(err)
	}
	defer close(allowLoopExit)
	if _, release, err := runtime.registry.AcquireLifecycle(runtime.sessionID); err == nil {
		release()
		t.Fatal("registry still accepted lifecycle acquisition after Close")
	}
	lifecycle, err := fixture.sessions.OpenRoot(ctx, runtime.sessionID, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer lifecycle.Close()
	loopState, err := lifecycle.Current().ReadRecord(ctx, coderloop.StateNamespace)
	if err != nil {
		t.Fatal(err)
	}
	if got, want := strings.TrimSpace(string(loopState)), string(coderloop.StateSuspended); got != want {
		t.Fatalf("loop state after Close = %q, want %q", got, want)
	}
}

func TestWorktreeRuntimeSupervisorUsesProjectResourceDirectory(t *testing.T) {
	fixture := newWorktreeRuntimeTestFixture(t)
	runtime, err := fixture.supervisor.Activate(context.Background(), fixture.mainID)
	if err != nil {
		t.Fatal(err)
	}
	want := filepath.Join(fixture.cfg.ProjectsPath(), fixture.service.ProjectIdentity().ID)
	if runtime.projectResources != want {
		t.Fatalf("project resources = %q, want %q", runtime.projectResources, want)
	}
	if info, err := os.Stat(want); err != nil || !info.IsDir() {
		t.Fatalf("project resource directory is unavailable: info=%v err=%v", info, err)
	}
}

func TestWorktreeRuntimeSupervisorOwnsOneProjectCodebaseAndAttachesEveryRuntime(t *testing.T) {
	fixture := newWorktreeRuntimeTestFixture(t)
	ctx := context.Background()
	mainMeta, err := fixture.supervisor.store.Get(fixture.mainID)
	if err != nil {
		t.Fatal(err)
	}
	project, err := projectpkg.OpenWithIdentity(mainMeta.Path, fixture.service.ProjectIdentity(), projectpkg.NewFileStore(fixture.cfg.ProjectsPath()))
	if err != nil {
		t.Fatal(err)
	}
	if err := project.SetCodebaseEnabled(true); err != nil {
		t.Fatal(err)
	}
	runtimeA, err := fixture.supervisor.Activate(ctx, fixture.mainID)
	if err != nil {
		t.Fatal(err)
	}
	shared := fixture.supervisor.codebaseRuntime
	if shared == nil || fixture.supervisor.codebaseProjectManager == nil {
		t.Fatal("worktree supervisor did not construct project-level Codebase services")
	}
	runtimeB, err := fixture.supervisor.Create(ctx, "shared codebase")
	if err != nil {
		t.Fatal(err)
	}
	if fixture.supervisor.codebaseRuntime != shared {
		t.Fatal("second worktree constructed a duplicate Codebase runtime")
	}
	if runtimeA.registry.Bus() != fixture.supervisor.projectBus || runtimeB.registry.Bus() != fixture.supervisor.projectBus {
		t.Fatal("worktree registries do not publish onto the project Codebase event bus")
	}
	for _, runtime := range []*worktreeRuntime{runtimeA, runtimeB} {
		request := &hookProbeRequest{}
		if err := runtime.lifecycle.Current().RunHooks(ctx, types.SessionHookBeforeAgent, coresession.HookPayload{AgentRequest: request}); err != nil {
			t.Fatal(err)
		}
		if !request.hasTool("codebase_context_query") {
			t.Fatalf("worktree session %s did not receive Codebase query hook", runtime.sessionID)
		}
	}
	if err := fixture.supervisor.Close(); err != nil {
		t.Fatal(err)
	}
	if err := shared.RequestIndex(ctx); err == nil || !strings.Contains(err.Error(), "closed") {
		t.Fatalf("Codebase request after supervisor shutdown = %v, want closed runtime", err)
	}
}

func TestWorktreeRuntimeContextRemainsBoundToOwningRuntimeAcrossSelection(t *testing.T) {
	fixture := newWorktreeRuntimeTestFixture(t)
	ctx := context.Background()
	runtimeA, err := fixture.supervisor.Activate(ctx, fixture.mainID)
	if err != nil {
		t.Fatal(err)
	}
	runtimeB, err := fixture.supervisor.Create(ctx, "context isolation")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := fixture.supervisor.Activate(ctx, fixture.mainID); err != nil {
		t.Fatal(err)
	}

	for _, runtime := range []*worktreeRuntime{runtimeA, runtimeB} {
		req := providers.NewRequest("")
		if err := runtime.lifecycle.Current().RunHooks(ctx, types.SessionHookBeforeModel, coresession.HookPayload{ModelRequest: req}); err != nil {
			t.Fatal(err)
		}
		if len(req.History()) == 0 {
			t.Fatalf("runtime %s has no request context", runtime.id)
		}
		content := req.History()[0].Content
		for _, want := range []string{runtime.workdir, runtime.sessionID, fixture.service.ProjectCodeRoot()} {
			if !strings.Contains(content, want) {
				t.Fatalf("runtime %s context missing %q:\n%s", runtime.id, want, content)
			}
		}
		other := runtimeA
		if runtime == runtimeA {
			other = runtimeB
		}
		if strings.Contains(content, other.sessionID) {
			t.Fatalf("runtime %s context contains other session %s", runtime.id, other.sessionID)
		}
	}
}

func TestArchiveLinkedWorktreeReturnsToMainAndKeepsBranch(t *testing.T) {
	fixture := newWorktreeRuntimeTestFixture(t)
	linked, err := fixture.supervisor.Create(context.Background(), "archive linked worktree")
	if err != nil {
		t.Fatal(err)
	}
	linkedID, linkedPath, linkedSession := linked.id, linked.workdir, linked.sessionID
	meta, err := fixture.supervisor.store.Get(linkedID)
	if err != nil {
		t.Fatal(err)
	}

	prepared, removeID, err := fixture.supervisor.prepareArchive(context.Background(), linkedID, 100, 40)
	if err != nil {
		t.Fatal(err)
	}
	if removeID != linkedID || prepared.runtime.id != fixture.mainID {
		t.Fatalf("archive target: remove=%q foreground=%q", removeID, prepared.runtime.id)
	}
	if err := fixture.supervisor.commitActivation(prepared); err != nil {
		t.Fatal(err)
	}
	prepared.feed.Close()
	if err := fixture.supervisor.removeArchived(context.Background(), removeID); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(linkedPath); !os.IsNotExist(err) {
		t.Fatalf("linked checkout still exists: %v", err)
	}
	if active, err := fixture.sessions.IsActive(linkedSession); err != nil || active {
		t.Fatalf("linked session active=%v err=%v", active, err)
	}
	if _, err := fixture.supervisor.store.Get(linkedID); err == nil {
		t.Fatal("linked metadata still exists")
	}
	porcelain := runWorktreeRuntimeGitOutput(t, fixture.service.ProjectCodeRoot(), "branch", "--list", meta.Branch)
	if !strings.Contains(porcelain, meta.Branch) {
		t.Fatalf("archived worktree branch was deleted: %q", porcelain)
	}
}

func TestArchiveMainWorktreeReplacesSessionWithoutRemovingCheckout(t *testing.T) {
	fixture := newWorktreeRuntimeTestFixture(t)
	mainRuntime, err := fixture.supervisor.Activate(context.Background(), fixture.mainID)
	if err != nil {
		t.Fatal(err)
	}
	oldSession, mainPath := mainRuntime.sessionID, mainRuntime.workdir

	prepared, removeID, err := fixture.supervisor.prepareArchive(context.Background(), fixture.mainID, 100, 40)
	if err != nil {
		t.Fatal(err)
	}
	if removeID != "" || prepared.runtime.id != fixture.mainID || prepared.runtime.sessionID == oldSession {
		t.Fatalf("main archive replacement: remove=%q runtime=%#v old=%q", removeID, prepared.runtime, oldSession)
	}
	if err := fixture.supervisor.commitActivation(prepared); err != nil {
		t.Fatal(err)
	}
	prepared.feed.Close()
	if active, err := fixture.sessions.IsActive(oldSession); err != nil || active {
		t.Fatalf("old main session active=%v err=%v", active, err)
	}
	if info, err := os.Stat(mainPath); err != nil || !info.IsDir() {
		t.Fatalf("main checkout missing after archive: info=%v err=%v", info, err)
	}
	meta, err := fixture.supervisor.store.Get(fixture.mainID)
	if err != nil {
		t.Fatal(err)
	}
	if meta.SessionID != prepared.runtime.sessionID {
		t.Fatalf("main metadata session=%q want=%q", meta.SessionID, prepared.runtime.sessionID)
	}
}

func TestArchiveMainPrepareFailurePreservesOldRuntimeAndSession(t *testing.T) {
	fixture := newWorktreeRuntimeTestFixture(t)
	mainRuntime, err := fixture.supervisor.Activate(context.Background(), fixture.mainID)
	if err != nil {
		t.Fatal(err)
	}
	oldSession := mainRuntime.sessionID
	wantErr := errors.New("injected replacement attach failure")
	fixture.supervisor.attachRuntime = func(_ context.Context, runtime *worktreeRuntime) error {
		if runtime != mainRuntime {
			return wantErr
		}
		return nil
	}

	if _, _, err := fixture.supervisor.prepareArchive(context.Background(), fixture.mainID, 100, 40); !errors.Is(err, wantErr) {
		t.Fatalf("prepare archive error = %v, want %v", err, wantErr)
	}
	if active, err := fixture.sessions.IsActive(oldSession); err != nil || !active {
		t.Fatalf("old main session active=%v err=%v", active, err)
	}
	meta, err := fixture.supervisor.store.Get(fixture.mainID)
	if err != nil {
		t.Fatal(err)
	}
	if meta.SessionID != oldSession {
		t.Fatalf("main metadata session=%q want old session %q", meta.SessionID, oldSession)
	}
	if fixture.supervisor.active != mainRuntime || fixture.supervisor.runtimes[fixture.mainID] != mainRuntime {
		t.Fatal("failed archive preparation displaced the old main runtime")
	}
	if _, release, err := mainRuntime.registry.AcquireLifecycle(oldSession); err != nil {
		t.Fatalf("old main registry is unavailable: %v", err)
	} else {
		release()
	}
}

func TestArchiveMainCommitAfterSupervisorCloseCleansReplacement(t *testing.T) {
	fixture := newWorktreeRuntimeTestFixture(t)
	if _, err := fixture.supervisor.Activate(context.Background(), fixture.mainID); err != nil {
		t.Fatal(err)
	}
	prepared, _, err := fixture.supervisor.prepareArchive(context.Background(), fixture.mainID, 100, 40)
	if err != nil {
		t.Fatal(err)
	}
	newSession := prepared.runtime.sessionID
	prepared.feed.Close()
	if err := fixture.supervisor.Close(); err != nil {
		t.Fatal(err)
	}
	if err := fixture.supervisor.commitActivation(prepared); err == nil {
		t.Fatal("commit after supervisor close succeeded")
	}
	if active, err := fixture.sessions.IsActive(newSession); err != nil || active {
		t.Fatalf("replacement session active=%v err=%v after rejected commit", active, err)
	}
}

func TestArchiveMainMetadataCommitFailureAbortsCodebaseTransition(t *testing.T) {
	fixture := newWorktreeRuntimeTestFixture(t)
	mainRuntime, err := fixture.supervisor.Activate(context.Background(), fixture.mainID)
	if err != nil {
		t.Fatal(err)
	}
	prepared, _, err := fixture.supervisor.prepareArchive(context.Background(), fixture.mainID, 100, 40)
	if err != nil {
		t.Fatal(err)
	}
	defer prepared.feed.Close()
	wantErr := errors.New("injected metadata commit failure")
	fixture.supervisor.store = &failNextWorktreeMetadataUpdateStore{Store: fixture.supervisor.store, err: wantErr, failNext: true}
	if err := fixture.supervisor.commitActivation(prepared); !errors.Is(err, wantErr) {
		t.Fatalf("commit error = %v, want %v", err, wantErr)
	}
	transition, err := fixture.supervisor.codebaseRuntime.PrepareSessionSwitch(context.Background(), mainRuntime.sessionID)
	if err != nil {
		t.Fatalf("Codebase transition remained locked after failed archive commit: %v", err)
	}
	transition.Abort()
}

func newWorktreeRuntimeTestModel(t *testing.T, fixture *worktreeRuntimeTestFixture, runtime *worktreeRuntime) *model {
	t.Helper()
	commands := codercmds.NewRegistry()
	codercmds.RegisterAll(commands)
	m := baseModelAt(fixture.sessions, runtime.registry, commands, fixture.cfg, runtime.sessionID, runtime.workdir)
	m.runtime = fixture.sessions
	m.projectMgr = nil
	m.worktreeMode = true
	m.worktreeSupervisor = fixture.supervisor
	m.worktreeRuntime = runtime
	m.codebaseRuntime = fixture.supervisor.codebaseRuntime
	if m.codebaseRuntime != nil {
		m.codebaseFeed = m.codebaseRuntime.ActivityFeed()
		m.codebaseActivities = map[string]codebasepkg.Activity{}
		t.Cleanup(m.codebaseFeed.Close)
	}
	m.loopManager = runtime.loop
	m.sessionID = runtime.sessionID
	m.feed = bus.SubscribeAgentFeed(runtime.bus, runtime.sessionID)
	m.width, m.height = 100, 40
	m.worktreeGeneration = 1
	projection, err := buildTranscriptProjection(fixture.sessions, fixture.cfg, runtime.workdir, m.width, m.height, runtime.sessionID)
	if err != nil {
		t.Fatal(err)
	}
	m.applyProjection(projection)
	t.Cleanup(m.closeFeed)
	return m
}

func transcriptContains(blocks []chatBlock, text string) bool {
	for _, block := range blocks {
		if strings.Contains(block.content, text) {
			return true
		}
	}
	return false
}

func newWorktreeRuntimeGitRepo(t *testing.T) string {
	t.Helper()
	repo := t.TempDir()
	runWorktreeRuntimeGit(t, repo, "init", "-b", "main")
	runWorktreeRuntimeGit(t, repo, "config", "user.name", "Friday Tests")
	runWorktreeRuntimeGit(t, repo, "config", "user.email", "friday@example.invalid")
	if err := os.WriteFile(filepath.Join(repo, "README.md"), []byte("initial\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	runWorktreeRuntimeGit(t, repo, "add", "README.md")
	runWorktreeRuntimeGit(t, repo, "commit", "-m", "initial")
	return repo
}

func runWorktreeRuntimeGit(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %s: %v: %s", strings.Join(args, " "), err, out)
	}
}

func runWorktreeRuntimeGitOutput(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %s: %v: %s", strings.Join(args, " "), err, out)
	}
	return string(out)
}
