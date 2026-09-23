//go:build e2e

package e2e

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	eventbus "github.com/hyponet/eventbus/bus"

	fridaybus "github.com/basenana/friday/bus"
	"github.com/basenana/friday/coder/commands"
	coderloop "github.com/basenana/friday/coder/loop"
	"github.com/basenana/friday/core/actor/events"
	coresession "github.com/basenana/friday/core/session"
	"github.com/basenana/friday/sessions"
	sessionfile "github.com/basenana/friday/sessions/file"
	fridayworktree "github.com/basenana/friday/worktree"
)

type lifecycleRuntime struct {
	meta      fridayworktree.Metadata
	lifecycle sessions.SessionLifecycle
	loop      *coderloop.Manager
	inputs    chan fridaybus.Envelope
	closed    bool
}

func TestProjectWorktreePublicLifecycleContracts(t *testing.T) {
	ctx := context.Background()
	repo := newLifecycleGitRepository(t)
	dataDir := t.TempDir()
	service, err := fridayworktree.Open(ctx, repo, filepath.Join(t.TempDir(), "linked"), "friday/", dataDir)
	if err != nil {
		t.Fatal(err)
	}
	store, err := fridayworktree.NewStore(filepath.Join(dataDir, "projects"), service.ProjectIdentity().ID)
	if err != nil {
		t.Fatal(err)
	}
	sessionManager := sessions.NewManager(
		sessionfile.NewFileSessionStore(filepath.Join(dataDir, "sessions")),
		filepath.Join(dataDir, "current"),
		"",
	)

	createdA, err := service.Create(ctx, "lifecycle A")
	if err != nil {
		t.Fatal(err)
	}
	metaA := lifecycleMetadata(t, store, createdA.Worktree.Path)
	runtimeA, made := openLifecycleRuntime(t, store, sessionManager, metaA.ID)
	if !made {
		t.Fatal("worktree A did not allocate its session")
	}
	startLifecycleLoop(t, runtimeA, "continue lifecycle A")

	registry := commands.NewRegistry()
	commands.RegisterSessionCommands(registry)
	newAction := executeLifecycleNavigation(t, registry, "worktree", "lifecycle B")
	newWorktree, ok := newAction.(commands.CreateWorktreeAction)
	if !ok || newWorktree.Requirement != "lifecycle B" {
		t.Fatalf("/worktree action = %#v", newAction)
	}
	if active, err := runtimeA.loop.IsActive(ctx, runtimeA.lifecycle.Current()); err != nil || !active {
		t.Fatalf("worktree A loop active while /worktree runs = %t, err = %v", active, err)
	}
	createdB, err := service.Create(ctx, newWorktree.Requirement)
	if err != nil {
		t.Fatalf("execute immediate /worktree navigation: %v", err)
	}
	metaB := lifecycleMetadata(t, store, createdB.Worktree.Path)
	runtimeB, made := openLifecycleRuntime(t, store, sessionManager, metaB.ID)
	if !made {
		t.Fatal("worktree B did not allocate its session")
	}
	if runtimeA.lifecycle.RootID() == runtimeB.lifecycle.RootID() {
		t.Fatal("worktrees A and B share one session")
	}
	startLifecycleLoop(t, runtimeB, "continue lifecycle B")
	assertLifecycleLoopState(t, runtimeA, coderloop.StateActive)

	selectA := selectLifecycleNavigation(t, registry, metaA.Name)
	resolvedA, err := service.Resolve(ctx, selectA)
	if err != nil || resolvedA.ID != metaA.ID || resolvedA.SessionID != runtimeA.lifecycle.RootID() {
		t.Fatalf("select A = %#v, %v", resolvedA, err)
	}
	selectB := selectLifecycleNavigation(t, registry, metaB.Name)
	resolvedB, err := service.Resolve(ctx, selectB)
	if err != nil || resolvedB.ID != metaB.ID || resolvedB.SessionID != runtimeB.lifecycle.RootID() {
		t.Fatalf("select B = %#v, %v", resolvedB, err)
	}
	assertLifecycleLoopState(t, runtimeA, coderloop.StateActive)
	assertLifecycleLoopState(t, runtimeB, coderloop.StateActive)

	sessionA, sessionB := runtimeA.lifecycle.RootID(), runtimeB.lifecycle.RootID()
	closeLifecycleRuntime(t, runtimeA)
	closeLifecycleRuntime(t, runtimeB)
	runtimeA, made = openLifecycleRuntime(t, store, sessionManager, metaA.ID)
	if made || runtimeA.lifecycle.RootID() != sessionA {
		t.Fatalf("restarted A session = %q, made = %t; want %q, false", runtimeA.lifecycle.RootID(), made, sessionA)
	}
	runtimeB, made = openLifecycleRuntime(t, store, sessionManager, metaB.ID)
	if made || runtimeB.lifecycle.RootID() != sessionB {
		t.Fatalf("restarted B session = %q, made = %t; want %q, false", runtimeB.lifecycle.RootID(), made, sessionB)
	}
	attachLifecycleLoop(t, runtimeA)
	attachLifecycleLoop(t, runtimeB)
	assertLifecycleLoopState(t, runtimeA, coderloop.StateActive)
	assertLifecycleLoopState(t, runtimeB, coderloop.StateActive)

	closeLifecycleRuntime(t, runtimeB)
	if err := sessionManager.DeleteRoot(sessionB); err != nil {
		t.Fatal(err)
	}
	if exists, err := sessionManager.Exists(sessionB); err != nil || exists {
		t.Fatalf("deleted B session exists = %t, err = %v", exists, err)
	}
	selectB = selectLifecycleNavigation(t, registry, metaB.Name)
	repairedB, made := openLifecycleRuntime(t, store, sessionManager, selectB)
	if !made || repairedB.lifecycle.RootID() == sessionB {
		t.Fatalf("selected B replacement = %q, made = %t; old session = %q", repairedB.lifecycle.RootID(), made, sessionB)
	}
	if _, err := repairedB.lifecycle.Current().ReadRecord(ctx, coderloop.StateNamespace); !errors.Is(err, coresession.ErrRecordNotFound) {
		t.Fatalf("replacement B inherited loop state: %v", err)
	}
	replacementB := repairedB.lifecycle.RootID()
	listed, err := service.List(ctx)
	if err != nil {
		t.Fatal(err)
	}
	listedA := lifecycleListedWorktree(t, listed, metaA.ID)
	listedB := lifecycleListedWorktree(t, listed, metaB.ID)
	if listedA.SessionID != sessionA || listedB.SessionID != replacementB || listedA.Stale || listedB.Stale {
		t.Fatalf("listed worktrees A=%+v B=%+v", listedA, listedB)
	}
	for _, sessionID := range []string{listedA.SessionID, listedB.SessionID} {
		if active, err := sessionManager.IsActive(sessionID); err != nil || !active {
			t.Fatalf("listed session %q active = %t, err = %v", sessionID, active, err)
		}
	}

	closeLifecycleRuntime(t, repairedB)
	if err := service.Remove(ctx, metaB.ID, fridayworktree.RemoveOptions{Sessions: sessionManager}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(metaB.Path); !os.IsNotExist(err) {
		t.Fatalf("removed worktree B checkout still exists: %v", err)
	}
	assertLifecycleSessionArchived(t, sessionManager, replacementB)
	if branch := lifecycleGitOutput(t, repo, "branch", "--list", metaB.Branch); strings.TrimSpace(branch) == "" {
		t.Fatal("default removal deleted worktree B branch")
	}
	listed, err = service.List(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if lifecycleHasWorktree(listed, metaB.ID) {
		t.Fatal("removed worktree B remains listed")
	}

	closeLifecycleRuntime(t, runtimeA)
	if err := service.Remove(ctx, metaA.ID, fridayworktree.RemoveOptions{DeleteBranch: true, Sessions: sessionManager}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(metaA.Path); !os.IsNotExist(err) {
		t.Fatalf("removed worktree A checkout still exists: %v", err)
	}
	assertLifecycleSessionArchived(t, sessionManager, sessionA)
	if branch := lifecycleGitOutput(t, repo, "branch", "--list", metaA.Branch); strings.TrimSpace(branch) != "" {
		t.Fatalf("safely deleted worktree A branch remains: %q", branch)
	}
	listed, err = service.List(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if lifecycleHasWorktree(listed, metaA.ID) || lifecycleHasWorktree(listed, metaB.ID) {
		t.Fatalf("removed worktrees remain listed: %#v", listed)
	}
}

func newLifecycleGitRepository(t *testing.T) string {
	t.Helper()
	repo := filepath.Join(t.TempDir(), "repository")
	if err := os.MkdirAll(repo, 0o755); err != nil {
		t.Fatal(err)
	}
	lifecycleRunGit(t, repo, "init")
	lifecycleRunGit(t, repo, "config", "user.email", "friday@example.com")
	lifecycleRunGit(t, repo, "config", "user.name", "Friday E2E")
	if err := os.WriteFile(filepath.Join(repo, "README.md"), []byte("# lifecycle\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	lifecycleRunGit(t, repo, "add", "README.md")
	lifecycleRunGit(t, repo, "commit", "-m", "initial")
	return repo
}

func lifecycleRunGit(t *testing.T, dir string, args ...string) {
	t.Helper()
	_ = lifecycleGitOutput(t, dir, args...)
}

func lifecycleGitOutput(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, out)
	}
	return string(out)
}

func lifecycleMetadata(t *testing.T, store fridayworktree.Store, target string) fridayworktree.Metadata {
	t.Helper()
	meta, err := store.Get(target)
	if err != nil {
		t.Fatal(err)
	}
	return meta
}

func openLifecycleRuntime(t *testing.T, store fridayworktree.Store, manager *sessions.Manager, worktreeID string) (*lifecycleRuntime, bool) {
	t.Helper()
	meta := lifecycleMetadata(t, store, worktreeID)
	catalog := fridayworktree.NewSessionCatalog(store, meta.ID, manager)
	lifecycle, _, made, err := catalog.EnsureRoot(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	inputs := make(chan fridaybus.Envelope, 2)
	loop := coderloop.NewManager(eventbus.NewBus(), coderloop.WithInputDispatcher(func(env fridaybus.Envelope) error {
		inputs <- env
		return nil
	}))
	runtime := &lifecycleRuntime{meta: meta, lifecycle: lifecycle, loop: loop, inputs: inputs}
	t.Cleanup(func() { closeLifecycleRuntime(t, runtime) })
	return runtime, made
}

func startLifecycleLoop(t *testing.T, runtime *lifecycleRuntime, task string) {
	t.Helper()
	if err := runtime.loop.Start(context.Background(), runtime.lifecycle.Current(), task); err != nil {
		t.Fatal(err)
	}
	assertLifecyclePrompt(t, runtime.inputs, coderloop.BootstrapPrompt)
}

func attachLifecycleLoop(t *testing.T, runtime *lifecycleRuntime) {
	t.Helper()
	if err := runtime.loop.Attach(context.Background(), runtime.lifecycle.Current()); err != nil {
		t.Fatal(err)
	}
	assertLifecyclePrompt(t, runtime.inputs, coderloop.DevelopRecoveryPrompt)
}

func assertLifecyclePrompt(t *testing.T, inputs <-chan fridaybus.Envelope, want string) {
	t.Helper()
	select {
	case env := <-inputs:
		var input fridaybus.AgentTextInput
		if err := events.DecodePayload(env.Event, &input); err != nil {
			t.Fatal(err)
		}
		if input.Text != want {
			t.Fatalf("loop prompt = %q, want %q", input.Text, want)
		}
	case <-time.After(5 * time.Second):
		t.Fatalf("timed out waiting for loop prompt %q", want)
	}
}

func assertLifecycleLoopState(t *testing.T, runtime *lifecycleRuntime, want coderloop.State) {
	t.Helper()
	raw, err := runtime.lifecycle.Current().ReadRecord(context.Background(), coderloop.StateNamespace)
	if err != nil {
		t.Fatal(err)
	}
	if got := coderloop.State(strings.TrimSpace(string(raw))); got != want {
		t.Fatalf("worktree %s loop state = %q, want %q", runtime.meta.ID, got, want)
	}
}

func closeLifecycleRuntime(t *testing.T, runtime *lifecycleRuntime) {
	t.Helper()
	if runtime == nil || runtime.closed {
		return
	}
	wasActive, err := runtime.loop.IsActive(context.Background(), runtime.lifecycle.Current())
	if err != nil {
		t.Fatal(err)
	}
	runtime.loop.Close()
	if wasActive {
		assertLifecycleLoopState(t, runtime, coderloop.StateSuspended)
	}
	if err := runtime.lifecycle.Close(); err != nil {
		t.Fatal(err)
	}
	runtime.closed = true
}

func executeLifecycleNavigation(t *testing.T, registry *commands.Registry, name, rawArgs string) commands.Action {
	t.Helper()
	command, ok := registry.Lookup(name)
	if !ok {
		t.Fatalf("/%s is not registered", name)
	}
	if policy := commands.CommandMetadata(command).Policy; policy != commands.PolicyNavigation {
		t.Fatalf("/%s policy = %q, want navigation so a busy runtime does not queue it", name, policy)
	}
	result, err := command.Execute(&commands.Context{Ctx: context.Background(), RawArgs: rawArgs})
	if err != nil {
		t.Fatal(err)
	}
	if result == nil || len(result.Actions) != 1 {
		t.Fatalf("/%s result = %#v", name, result)
	}
	return result.Actions[0]
}

func selectLifecycleNavigation(t *testing.T, registry *commands.Registry, target string) string {
	t.Helper()
	action := executeLifecycleNavigation(t, registry, "select", target)
	selection, ok := action.(commands.SelectWorktreeAction)
	if !ok || selection.Target != target {
		t.Fatalf("/select action = %#v", action)
	}
	return selection.Target
}

func lifecycleListedWorktree(t *testing.T, items []fridayworktree.Worktree, id string) fridayworktree.Worktree {
	t.Helper()
	for _, item := range items {
		if item.ID == id {
			return item
		}
	}
	t.Fatalf("worktree %q missing from list: %#v", id, items)
	return fridayworktree.Worktree{}
}

func lifecycleHasWorktree(items []fridayworktree.Worktree, id string) bool {
	for _, item := range items {
		if item.ID == id {
			return true
		}
	}
	return false
}

func assertLifecycleSessionArchived(t *testing.T, manager *sessions.Manager, sessionID string) {
	t.Helper()
	if exists, err := manager.Exists(sessionID); err != nil || !exists {
		t.Fatalf("archived session %q exists = %t, err = %v", sessionID, exists, err)
	}
	if active, err := manager.IsActive(sessionID); err != nil || active {
		t.Fatalf("archived session %q active = %t, err = %v", sessionID, active, err)
	}
}
