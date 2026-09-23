package tui

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/basenana/friday/bus"
	codercmds "github.com/basenana/friday/coder/commands"
	coderloop "github.com/basenana/friday/coder/loop"
	"github.com/basenana/friday/core/actor/events"
)

func TestLinkedWorktreeRejectsOrdinarySessionMutationActions(t *testing.T) {
	tests := []struct {
		name   string
		action func(*model) codercmds.Action
	}{
		{name: "clear", action: func(*model) codercmds.Action { return codercmds.ClearSessionAction{SessionID: "replacement-session"} }},
		{name: "open resume", action: func(*model) codercmds.Action { return codercmds.OpenResumeAction{} }},
		{name: "resume", action: func(m *model) codercmds.Action {
			lifecycle, err := m.sessMgr.CreateRoot(context.Background(), nil)
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = lifecycle.Close() })
			return codercmds.ResumeSessionAction{Target: lifecycle.RootID()}
		}},
		{name: "delete", action: func(*model) codercmds.Action { return codercmds.DeleteSessionAction{} }},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m, _, _ := newLoadedProjectTestModel(t)
			m.worktreeMode = true
			m.projectMgr = nil
			m.worktreeRuntime = &worktreeRuntime{id: "retained-runtime", main: false}
			oldSessionID, oldRegistry, oldFeed := m.sessionID, m.registry, m.feed
			oldRuntime := m.worktreeRuntime

			handled, cmd := m.applySessionAction(tt.action(m))
			if !handled || cmd != nil {
				t.Fatalf("handled=%v cmd=%v, want handled with no command", handled, cmd)
			}
			if m.sessionID != oldSessionID || m.registry != oldRegistry || m.feed != oldFeed || m.worktreeRuntime != oldRuntime {
				t.Fatal("rejected session action changed the retained worktree binding")
			}
			if m.selector != nil || m.commandConfirm != nil {
				t.Fatalf("rejected session action opened UI state: selector=%#v confirmation=%#v", m.selector, m.commandConfirm)
			}
			if len(m.messages) == 0 || m.messages[len(m.messages)-1].kind != blockError || m.messages[len(m.messages)-1].content != "session command unavailable in worktree mode; use /worktree or /select" {
				t.Fatalf("rejection message = %#v", m.messages)
			}
		})
	}
}

func TestMainWorktreeAllowsOrdinarySessionMutationActions(t *testing.T) {
	fixture := newWorktreeRuntimeTestFixture(t)
	active, err := fixture.supervisor.Activate(context.Background(), fixture.mainID)
	if err != nil {
		t.Fatal(err)
	}
	if !active.main {
		t.Fatal("fixture main runtime is not marked main")
	}

	t.Run("clear", func(t *testing.T) {
		m := newWorktreeRuntimeTestModel(t, fixture, active)
		handled, cmd := m.applySessionAction(codercmds.ClearSessionAction{SessionID: "ignored"})
		if !handled || cmd == nil || lastBlockIsError(m) {
			t.Fatalf("main /clear handled=%v cmd=%v messages=%#v", handled, cmd, m.messages)
		}
	})

	t.Run("resume", func(t *testing.T) {
		lifecycle, err := fixture.sessions.CreateRoot(context.Background(), nil)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = lifecycle.Close() })
		m := newWorktreeRuntimeTestModel(t, fixture, active)
		handled, cmd := m.applySessionAction(codercmds.ResumeSessionAction{Target: lifecycle.RootID()})
		if !handled || cmd == nil || lastBlockIsError(m) {
			t.Fatalf("main /resume handled=%v cmd=%v messages=%#v", handled, cmd, m.messages)
		}
	})

	t.Run("delete", func(t *testing.T) {
		m := newWorktreeRuntimeTestModel(t, fixture, active)
		handled, cmd := m.applySessionAction(codercmds.DeleteSessionAction{})
		if !handled || cmd != nil || m.commandConfirm == nil || lastBlockIsError(m) {
			t.Fatalf("main /delete handled=%v cmd=%v confirm=%#v messages=%#v", handled, cmd, m.commandConfirm, m.messages)
		}
	})
}

func TestMainWorktreeSessionCommandsRebindRuntime(t *testing.T) {
	t.Run("clear keeps the previous session active", func(t *testing.T) {
		fixture := newWorktreeRuntimeTestFixture(t)
		active, err := fixture.supervisor.Activate(context.Background(), fixture.mainID)
		if err != nil {
			t.Fatal(err)
		}
		oldSession := active.sessionID
		m := newWorktreeRuntimeTestModel(t, fixture, active)
		_, cmd := m.applySessionAction(codercmds.ClearSessionAction{})
		m = applyPreparedWorktreeCommand(t, m, cmd)
		if m.sessionID == oldSession {
			t.Fatal("/clear did not bind a new main-worktree session")
		}
		if active, err := fixture.sessions.IsActive(oldSession); err != nil || !active {
			t.Fatalf("previous session active=%v err=%v", active, err)
		}
		assertWorktreeSession(t, fixture, m.sessionID)
	})

	t.Run("resume binds the selected session", func(t *testing.T) {
		fixture := newWorktreeRuntimeTestFixture(t)
		active, err := fixture.supervisor.Activate(context.Background(), fixture.mainID)
		if err != nil {
			t.Fatal(err)
		}
		target, err := fixture.sessions.CreateRoot(context.Background(), nil)
		if err != nil {
			t.Fatal(err)
		}
		targetID := target.RootID()
		if err := target.Close(); err != nil {
			t.Fatal(err)
		}
		m := newWorktreeRuntimeTestModel(t, fixture, active)
		_, cmd := m.applySessionAction(codercmds.ResumeSessionAction{Target: targetID})
		m = applyPreparedWorktreeCommand(t, m, cmd)
		if m.sessionID != targetID {
			t.Fatalf("/resume session=%q want=%q", m.sessionID, targetID)
		}
		assertWorktreeSession(t, fixture, targetID)
	})

	t.Run("delete replaces and removes the current session", func(t *testing.T) {
		fixture := newWorktreeRuntimeTestFixture(t)
		active, err := fixture.supervisor.Activate(context.Background(), fixture.mainID)
		if err != nil {
			t.Fatal(err)
		}
		oldSession := active.sessionID
		m := newWorktreeRuntimeTestModel(t, fixture, active)
		m.applySessionAction(codercmds.DeleteSessionAction{})
		updated, cmd := m.updateCommandConfirmation(tea.KeyPressMsg{Code: 'y', Text: "y"})
		m = updated.(*model)
		m = applyPreparedWorktreeCommand(t, m, cmd)
		if m.sessionID == oldSession {
			t.Fatal("/delete did not install a replacement session")
		}
		if exists, err := fixture.sessions.Exists(oldSession); err != nil || exists {
			t.Fatalf("deleted session exists=%v err=%v", exists, err)
		}
		assertWorktreeSession(t, fixture, m.sessionID)
	})
}

func applyPreparedWorktreeCommand(t *testing.T, m *model, cmd tea.Cmd) *model {
	t.Helper()
	prepared := worktreePreparedFromCommand(t, cmd)
	if prepared.err != nil {
		t.Fatal(prepared.err)
	}
	updated, _ := m.Update(prepared)
	return updated.(*model)
}

func assertWorktreeSession(t *testing.T, fixture *worktreeRuntimeTestFixture, want string) {
	t.Helper()
	meta, err := fixture.supervisor.store.Get(fixture.mainID)
	if err != nil {
		t.Fatal(err)
	}
	if meta.SessionID != want {
		t.Fatalf("main worktree session=%q want=%q", meta.SessionID, want)
	}
}

func lastBlockIsError(m *model) bool {
	return len(m.messages) > 0 && m.messages[len(m.messages)-1].kind == blockError
}

func TestReviewActionInOrdinarySSHShowsLocalVSCodeInstructions(t *testing.T) {
	fixture := newWorktreeRuntimeTestFixture(t)
	runtime, err := fixture.supervisor.Activate(context.Background(), fixture.mainID)
	if err != nil {
		t.Fatal(err)
	}
	m := newWorktreeRuntimeTestModel(t, fixture, runtime)
	m.cfg.Editor.RemoteAuthority = "ssh-remote+dev"
	t.Setenv("SSH_CONNECTION", "client server")
	t.Setenv("SSH_TTY", "")
	t.Setenv("VSCODE_IPC_HOOK_CLI", "")

	handled, cmd := m.applySessionAction(codercmds.ReviewWorktreeAction{})
	if !handled || cmd != nil {
		t.Fatalf("review action handled=%v cmd=%v", handled, cmd)
	}
	if !transcriptContains(m.messages, "vscode://vscode-remote/ssh-remote+dev") {
		t.Fatalf("review instructions missing from transcript: %#v", m.messages)
	}
}

func TestWorktreeModeArchiveOpensConfirmation(t *testing.T) {
	fixture := newWorktreeRuntimeTestFixture(t)
	active, err := fixture.supervisor.Activate(context.Background(), fixture.mainID)
	if err != nil {
		t.Fatal(err)
	}
	m := newWorktreeRuntimeTestModel(t, fixture, active)
	m.worktreeService = fixture.service

	handled, cmd := m.applySessionAction(codercmds.ArchiveSessionAction{})
	if !handled || cmd != nil || m.commandConfirm == nil || m.commandConfirm.action != "archive" {
		t.Fatalf("archive confirmation: handled=%v cmd=%v confirm=%#v", handled, cmd, m.commandConfirm)
	}
}

func TestConfirmingWorktreeArchivePreparesMainTransition(t *testing.T) {
	fixture := newWorktreeRuntimeTestFixture(t)
	linked, err := fixture.supervisor.Create(context.Background(), "archive from tui")
	if err != nil {
		t.Fatal(err)
	}
	m := newWorktreeRuntimeTestModel(t, fixture, linked)
	m.worktreeService = fixture.service
	m.applySessionAction(codercmds.ArchiveSessionAction{})

	updated, cmd := m.updateCommandConfirmation(tea.KeyPressMsg{Code: 'y', Text: "y"})
	m = updated.(*model)
	if cmd == nil || !m.worktreeChanging {
		t.Fatalf("confirmed archive: cmd=%v changing=%v", cmd, m.worktreeChanging)
	}
	msg := worktreeArchivePreparedFromCommand(t, cmd)
	if msg.err != nil || msg.removeID != linked.id || msg.runtime == nil || msg.runtime.runtime.id != fixture.mainID {
		t.Fatalf("archive preparation = %#v", msg)
	}
	msg.runtime.feed.Close()
}

func worktreeArchivePreparedFromCommand(t *testing.T, cmd tea.Cmd) worktreeArchivePreparedMsg {
	t.Helper()
	msg := cmd()
	if prepared, ok := msg.(worktreeArchivePreparedMsg); ok {
		return prepared
	}
	batch, ok := msg.(tea.BatchMsg)
	if !ok {
		t.Fatalf("archive command produced %T", msg)
	}
	for _, sub := range batch {
		if prepared, ok := sub().(worktreeArchivePreparedMsg); ok {
			return prepared
		}
	}
	t.Fatal("archive command did not prepare a worktree transition")
	return worktreeArchivePreparedMsg{}
}

func TestLinkedWorktreeArchiveSwitchesUIAndRemovesTab(t *testing.T) {
	fixture := newWorktreeRuntimeTestFixture(t)
	linked, err := fixture.supervisor.Create(context.Background(), "archive ui removal")
	if err != nil {
		t.Fatal(err)
	}
	m := newWorktreeRuntimeTestModel(t, fixture, linked)
	m.worktreeService = fixture.service
	m.refreshWorktreeTabs()
	m.resetWorktreeStatusFeeds()
	prepared := worktreeArchivePreparedFromCommand(t, m.archiveCurrentWorktree())

	updated, cmd := m.Update(prepared)
	m = updated.(*model)
	if m.worktreeRuntime == nil || m.worktreeRuntime.id != fixture.mainID {
		t.Fatalf("archive foreground = %#v, want main %q", m.worktreeRuntime, fixture.mainID)
	}
	removed := worktreeArchiveRemovedFromCommand(t, cmd)
	updated, _ = m.Update(removed)
	m = updated.(*model)
	if m.worktreeChanging {
		t.Fatal("archive removal left worktree transition active")
	}
	for _, tab := range m.worktreeTabs {
		if tab.ID == linked.id {
			t.Fatalf("archived linked tab remains: %#v", m.worktreeTabs)
		}
	}
}

func worktreeArchiveRemovedFromCommand(t *testing.T, cmd tea.Cmd) worktreeArchiveRemovedMsg {
	t.Helper()
	if cmd == nil {
		t.Fatal("archive transition did not return a removal command")
	}
	msg := cmd()
	if removed, ok := msg.(worktreeArchiveRemovedMsg); ok {
		return removed
	}
	batch, ok := msg.(tea.BatchMsg)
	if !ok {
		t.Fatalf("archive transition produced %T", msg)
	}
	results := make(chan tea.Msg, len(batch))
	for _, sub := range batch {
		go func(run tea.Cmd) { results <- run() }(sub)
	}
	for range batch {
		select {
		case result := <-results:
			if removed, ok := result.(worktreeArchiveRemovedMsg); ok {
				return removed
			}
		case <-time.After(5 * time.Second):
			t.Fatal("timed out waiting for worktree removal")
		}
	}
	t.Fatal("archive transition did not remove the linked worktree")
	return worktreeArchiveRemovedMsg{}
}

func TestOrdinaryModeRejectsWorktreeAndSelectOpensSessionSelector(t *testing.T) {
	m, _, _ := newLoadedProjectTestModel(t)
	oldID := m.sessionID
	handled, _ := m.applySessionAction(codercmds.CreateWorktreeAction{})
	if !handled || m.sessionID != oldID || len(m.messages) == 0 || m.messages[len(m.messages)-1].content != "worktree requires a Git repository" {
		t.Fatalf("ordinary /worktree changed session: handled=%v old=%q new=%q messages=%#v", handled, oldID, m.sessionID, m.messages)
	}
	handled, _ = m.applySessionAction(codercmds.SelectWorktreeAction{})
	if !handled || m.selector == nil || m.selector.kind != selectorResume {
		t.Fatalf("ordinary /select did not open the session selector: handled=%v selector=%#v", handled, m.selector)
	}
}

func TestBareWorktreeWaitsForRequirement(t *testing.T) {
	m, _, _ := newLoadedProjectTestModel(t)
	m.worktreeMode = true
	handled, _ := m.applySessionAction(codercmds.CreateWorktreeAction{})
	if !handled || !m.worktreeRequirement {
		t.Fatalf("bare /worktree did not enter requirement mode: handled=%v mode=%v", handled, m.worktreeRequirement)
	}
	if len(m.messages) == 0 || m.messages[len(m.messages)-1].content != "new worktree · describe the requirement" {
		t.Fatalf("bare /worktree did not show requirement prompt: %#v", m.messages)
	}
	if got := m.textarea.Placeholder; got != "Describe the requirement for the new worktree…" {
		t.Fatalf("bare /worktree placeholder = %q", got)
	}
}

func TestBareWorktreeMessageRunsInCurrentRuntime(t *testing.T) {
	m, _, _ := newLoadedProjectTestModel(t)
	m.worktreeMode = true
	m.textarea.SetValue("inspect the main checkout")
	_, cmd := m.submitComposer()
	if cmd == nil || m.worktreeChanging {
		t.Fatalf("ordinary message started worktree creation: cmd=%v changing=%v", cmd, m.worktreeChanging)
	}
	if len(m.promptHistory) != 1 || m.promptHistory[0] != "inspect the main checkout" {
		t.Fatalf("ordinary message history = %#v", m.promptHistory)
	}
}

func TestBareWorktreeCommandSelectsMainCheckout(t *testing.T) {
	fixture := newWorktreeRuntimeTestFixture(t)
	linked, err := fixture.supervisor.Create(context.Background(), "linked startup source")
	if err != nil {
		t.Fatal(err)
	}
	fixture.service.SetCurrentCanonicalPath(linked.workdir)
	target, err := defaultWorktreeTarget(context.Background(), fixture.service)
	if err != nil {
		t.Fatal(err)
	}
	if target != fixture.mainID {
		t.Fatalf("default worktree target = %q, want main %q", target, fixture.mainID)
	}
}

func TestRunWorktreeActivatesMainWorktree(t *testing.T) {
	fixture := newWorktreeRuntimeTestFixture(t)
	linked, err := fixture.supervisor.Create(context.Background(), "recent linked worktree")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := fixture.supervisor.Activate(context.Background(), linked.id); err != nil {
		t.Fatal(err)
	}
	active, err := activateDefaultWorktree(context.Background(), fixture.supervisor, fixture.service)
	if err != nil {
		t.Fatal(err)
	}
	if active.id != fixture.mainID {
		t.Fatalf("startup worktree = %q, want main %q", active.id, fixture.mainID)
	}
}

func TestRunWorktreeDoesNotRequestRequirement(t *testing.T) {
	fixture := newWorktreeRuntimeTestFixture(t)
	active, err := activateDefaultWorktree(context.Background(), fixture.supervisor, fixture.service)
	if err != nil {
		t.Fatal(err)
	}
	m := newWorktreeRuntimeTestModel(t, fixture, active)
	if m.worktreeRequirement {
		t.Fatal("default startup requested a new worktree requirement")
	}
}

func TestWorktreeChangeBlocksQueuedDispatch(t *testing.T) {
	m, _, _ := newLoadedProjectTestModel(t)
	m.queued = []pendingInput{{text: "next prompt"}}
	m.worktreeChanging = true
	if m.canDispatchQueued() {
		t.Fatal("queued input can dispatch during a worktree change")
	}
}

func TestWorktreeNavigationBypassesQueueAndRetainsActiveRuntime(t *testing.T) {
	tests := []struct {
		name       string
		command    func(*worktreeRuntimeTestFixture, *worktreeRuntime) string
		wantCreate bool
	}{
		{
			name:       "worktree",
			command:    func(_ *worktreeRuntimeTestFixture, _ *worktreeRuntime) string { return "/worktree navigation target" },
			wantCreate: true,
		},
		{
			name: "select",
			command: func(fixture *worktreeRuntimeTestFixture, _ *worktreeRuntime) string {
				target, err := fixture.supervisor.Create(context.Background(), "existing navigation target")
				if err != nil {
					t.Fatal(err)
				}
				return "/select " + target.id
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			fixture := newWorktreeRuntimeTestFixture(t)
			active, err := fixture.supervisor.Activate(context.Background(), fixture.mainID)
			if err != nil {
				t.Fatal(err)
			}
			command := tt.command(fixture, active)
			if _, err := fixture.supervisor.Activate(context.Background(), active.id); err != nil {
				t.Fatal(err)
			}

			started := make(chan struct{})
			allowExit := make(chan struct{})
			defer close(allowExit)
			var dispatchOnce sync.Once
			active.loop = coderloop.NewManager(active.bus, coderloop.WithInputDispatcher(func(bus.Envelope) error {
				dispatchOnce.Do(func() { close(started) })
				<-allowExit
				return errors.New("test loop stopped")
			}))
			if err := active.loop.Start(context.Background(), active.lifecycle.Current(), "continue in background"); err != nil {
				t.Fatal(err)
			}
			select {
			case <-started:
			case <-time.After(5 * time.Second):
				t.Fatal("active worktree loop did not dispatch")
			}

			m := newWorktreeRuntimeTestModel(t, fixture, active)
			m.running = true
			m.textarea.SetValue(command)
			updated, cmd := m.submitComposer()
			m = updated.(*model)
			if cmd == nil {
				t.Fatal("navigation command was not dispatched immediately")
			}
			if len(m.queued) != 0 {
				t.Fatalf("navigation command entered queue: %#v", m.queued)
			}

			prepared := worktreePreparedFromCommand(t, cmd)
			updated, _ = m.Update(prepared)
			m = updated.(*model)
			if m.worktreeRuntime == active {
				t.Fatal("navigation did not activate a different worktree runtime")
			}
			if len(m.queued) != 0 {
				t.Fatalf("activation retained a navigation queue entry: %#v", m.queued)
			}
			state, err := active.lifecycle.Current().ReadRecord(context.Background(), coderloop.StateNamespace)
			if err != nil {
				t.Fatal(err)
			}
			if got := string(state); got != string(coderloop.StateActive) {
				t.Fatalf("background runtime state = %q, want %q", got, coderloop.StateActive)
			}
			if tt.wantCreate && m.worktreeRuntime.id == active.id {
				t.Fatal("/worktree did not create a new runtime")
			}
		})
	}
}

func TestFailedWorktreeNavigationDispatchesDeferredQueueAfterOldRunFinishes(t *testing.T) {
	fixture := newWorktreeRuntimeTestFixture(t)
	active, err := fixture.supervisor.Activate(context.Background(), fixture.mainID)
	if err != nil {
		t.Fatal(err)
	}
	m := newWorktreeRuntimeTestModel(t, fixture, active)
	m.running = true
	m.currentRunID = "old-run"

	m.textarea.SetValue("/compact")
	updated, cmd := m.submitComposer()
	m = updated.(*model)
	if cmd != nil || len(m.queued) != 1 {
		t.Fatalf("deferred command was not queued: cmd=%v queue=%#v", cmd, m.queued)
	}

	m.textarea.SetValue("/select missing-worktree")
	updated, navigationCmd := m.submitComposer()
	m = updated.(*model)
	if navigationCmd == nil || !m.worktreeChanging || len(m.queued) != 1 {
		t.Fatalf("immediate navigation state: cmd=%v changing=%v queue=%#v", navigationCmd, m.worktreeChanging, m.queued)
	}

	updated, _ = m.updateActorEvents(actorEventsMsg{
		token:  m.subscriptionToken,
		events: []events.Event{events.NewEvent(events.KindRunFinished, "old-run").WithPayload(events.RunFinishedData{StopReason: "end_turn"})},
	})
	m = updated.(*model)
	if m.running || len(m.queued) != 1 {
		t.Fatalf("old run finish changed queue before navigation completed: running=%v queue=%#v", m.running, m.queued)
	}

	prepared := worktreePreparedFromCommand(t, navigationCmd)
	if prepared.err == nil {
		t.Fatal("invalid worktree selection unexpectedly prepared a runtime")
	}
	updated, dispatch := m.Update(prepared)
	m = updated.(*model)
	if m.worktreeChanging || m.worktreeRuntime != active {
		t.Fatalf("failed navigation changed retained foreground: changing=%v runtime=%#v", m.worktreeChanging, m.worktreeRuntime)
	}
	if dispatch == nil {
		t.Fatal("failed navigation left deferred queue without a dispatch command")
	}
	if _, ok := dispatch().(dispatchQueuedMsg); !ok {
		t.Fatalf("failed navigation dispatch = %T, want dispatchQueuedMsg", dispatch())
	}

	updated, _ = m.Update(dispatchQueuedMsg{})
	m = updated.(*model)
	if len(m.queued) != 0 || !m.manualCompacting {
		t.Fatalf("deferred command did not dispatch after navigation failure: queue=%#v compacting=%v", m.queued, m.manualCompacting)
	}
}

func worktreePreparedFromCommand(t *testing.T, cmd tea.Cmd) worktreePreparedMsg {
	t.Helper()
	msg := cmd()
	if prepared, ok := msg.(worktreePreparedMsg); ok {
		return prepared
	}
	batch, ok := msg.(tea.BatchMsg)
	if !ok {
		t.Fatalf("navigation command produced %T, want worktree preparation", msg)
	}
	for _, sub := range batch {
		if prepared, ok := sub().(worktreePreparedMsg); ok {
			return prepared
		}
	}
	t.Fatal("navigation command did not prepare a worktree runtime")
	return worktreePreparedMsg{}
}

func TestSelectingCurrentWorktreeClearsBootstrap(t *testing.T) {
	m, _, _ := newLoadedProjectTestModel(t)
	m.worktreeMode = true
	m.worktreeRequirement = true
	m.worktreeChanging = true
	m.worktreeGeneration = 3
	m.handleWorktreeSwitch(worktreeSwitchMsg{token: 3, path: m.workdir})
	if m.worktreeRequirement || m.worktreeChanging {
		t.Fatalf("same-worktree selection left requirement=%v changing=%v", m.worktreeRequirement, m.worktreeChanging)
	}
}

func TestWorktreeSelectorOmitsUnregisteredGitCheckout(t *testing.T) {
	fixture := newWorktreeRuntimeTestFixture(t)
	active, err := fixture.supervisor.Activate(context.Background(), fixture.mainID)
	if err != nil {
		t.Fatal(err)
	}
	external := filepath.Join(t.TempDir(), "external")
	runWorktreeRuntimeGit(t, active.workdir, "worktree", "add", "-b", "external-selector", external, "HEAD")
	t.Cleanup(func() { _ = os.RemoveAll(external) })

	m := newWorktreeRuntimeTestModel(t, fixture, active)
	m.worktreeService = fixture.service
	m.openWorktreeSelector()
	if m.selector == nil {
		t.Fatal("selector was not opened")
	}
	for _, item := range m.selector.items {
		if samePath(item.value, external) {
			t.Fatalf("selector offered unregistered checkout the supervisor cannot activate: %#v", item)
		}
	}
}
