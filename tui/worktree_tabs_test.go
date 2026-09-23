package tui

import (
	"context"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	eventbus "github.com/hyponet/eventbus/bus"

	fridaybus "github.com/basenana/friday/bus"
	"github.com/basenana/friday/core/actor/events"
	"github.com/charmbracelet/x/ansi"
)

func TestWorktreeTabsKeepActiveVisibleWhenOverflowing(t *testing.T) {
	tabs := []worktreeTab{
		{ID: "one", Label: "one", Status: worktreeTabIdle},
		{ID: "two", Label: "two", Status: worktreeTabRunning},
		{ID: "three", Label: "three", Status: worktreeTabCompleted},
		{ID: "four", Label: "four", Status: worktreeTabFailed},
	}

	rendered, hits := renderWorktreeTabs(tabs, "four", 22)
	plain := ansi.Strip(rendered)
	if !strings.Contains(plain, "four") {
		t.Fatalf("active tab missing from overflowed row: %q", rendered)
	}
	if !strings.Contains(plain, "…") {
		t.Fatalf("overflow marker missing: %q", rendered)
	}
	if len(hits) == 0 || hits[len(hits)-1].ID != "four" {
		t.Fatalf("click targets do not include active tab: %#v", hits)
	}
	if got := strings.Count(ansi.Strip(rendered), "\n") + 1; got != 3 {
		t.Fatalf("bordered tab row height = %d, want 3: %q", got, rendered)
	}
	if plain := ansi.Strip(rendered); !(strings.Contains(plain, "╭") || strings.Contains(plain, "╔")) || !(strings.Contains(plain, "╰") || strings.Contains(plain, "╚")) {
		t.Fatalf("tab border missing: %q", plain)
	}
}

func TestWorktreeTabsTruncateAnOversizedActiveLabel(t *testing.T) {
	tabs := []worktreeTab{
		{ID: "one", Label: "a-very-long-worktree-branch", Status: worktreeTabRunning},
		{ID: "two", Label: "two", Status: worktreeTabIdle},
	}
	rendered, hits := renderWorktreeTabs(tabs, "one", 12)
	if got := lipgloss.Width(rendered); got > 12 {
		t.Fatalf("tab row width = %d, want <= 12: %q", got, rendered)
	}
	if len(hits) != 1 || hits[0].End > 12 {
		t.Fatalf("oversized click target: %#v", hits)
	}
}

func TestWorktreeTabStatusTracksLifecycleAndUnreadResults(t *testing.T) {
	status := worktreeTabIdle
	status = nextWorktreeTabStatus(status, events.NewEvent(events.KindRunStarted, "run-1"))
	if status != worktreeTabRunning {
		t.Fatalf("run start status = %q", status)
	}

	waiting := events.NewEvent(events.KindRunFinished, "run-1").WithPayload(events.RunFinishedData{
		StopReason: "end_turn",
		Interrupts: []events.Interrupt{{Type: "form", ID: "form-1"}},
	})
	status = nextWorktreeTabStatus(status, waiting)
	if status != worktreeTabWaiting {
		t.Fatalf("interrupt status = %q", status)
	}

	status = nextWorktreeTabStatus(status, events.NewEvent(events.KindCustom, "run-1").WithName(events.CustomFormSubmitted))
	status = nextWorktreeTabStatus(status, events.NewEvent(events.KindRunFinished, "run-1").WithPayload(events.RunFinishedData{StopReason: "end_turn"}))
	if status != worktreeTabCompleted {
		t.Fatalf("successful finish status = %q", status)
	}
	status = nextWorktreeTabStatus(worktreeTabRunning, events.NewEvent(events.KindRunFinished, "run-2").WithPayload(events.RunFinishedData{StopReason: "cancelled"}))
	if status != worktreeTabInterrupted {
		t.Fatalf("cancelled finish status = %q", status)
	}
	status = nextWorktreeTabStatus(worktreeTabRunning, events.NewEvent(events.KindRunError, "run-3"))
	if status != worktreeTabFailed {
		t.Fatalf("run error status = %q", status)
	}
}

func TestWorktreeTabHitUsesRenderedCellBounds(t *testing.T) {
	tabs := []worktreeTab{
		{ID: "one", Label: "one", Status: worktreeTabIdle},
		{ID: "two", Label: "two", Status: worktreeTabRunning},
	}
	_, hits := renderWorktreeTabs(tabs, "one", 80)
	if len(hits) != 2 {
		t.Fatalf("hit targets = %#v", hits)
	}
	for _, hit := range hits {
		if got := worktreeTabAt(hits, hit.Start); got != hit.ID {
			t.Fatalf("hit at %d = %q, want %q", hit.Start, got, hit.ID)
		}
		if got := worktreeTabAt(hits, hit.End-1); got != hit.ID {
			t.Fatalf("hit at %d = %q, want %q", hit.End-1, got, hit.ID)
		}
	}
	if got := worktreeTabAt(hits, hits[len(hits)-1].End); got != "" {
		t.Fatalf("outside hit = %q", got)
	}
}

func TestWorktreeModelBuildsTabsForEveryRetainedWorktree(t *testing.T) {
	fixture := newWorktreeRuntimeTestFixture(t)
	active, err := fixture.supervisor.Activate(context.Background(), fixture.mainID)
	if err != nil {
		t.Fatal(err)
	}
	other, err := fixture.supervisor.Create(context.Background(), "show status tabs")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := fixture.supervisor.Activate(context.Background(), active.id); err != nil {
		t.Fatal(err)
	}
	m := newWorktreeRuntimeTestModel(t, fixture, active)
	m.worktreeService = fixture.service
	m.refreshWorktreeTabs()
	if len(m.worktreeTabs) != 2 {
		t.Fatalf("tabs = %#v", m.worktreeTabs)
	}
	seen := map[string]bool{}
	for _, tab := range m.worktreeTabs {
		seen[tab.ID] = true
	}
	if !seen[active.id] || !seen[other.id] {
		t.Fatalf("retained worktrees missing from tabs: %#v", m.worktreeTabs)
	}
}

func TestWorktreeInitialWaitCommandsIncludeEveryStatusFeed(t *testing.T) {
	b := eventbus.NewBus()
	one := fridaybus.SubscribeAgentFeed(b, "one")
	two := fridaybus.SubscribeAgentFeed(b, "two")
	t.Cleanup(one.Close)
	t.Cleanup(two.Close)
	m := &model{
		worktreeStatusFeeds: map[string]*fridaybus.Feed{
			"one": one,
			"two": two,
		},
	}
	if got := len(m.initialWaitCommands()); got != 2 {
		t.Fatalf("initial status wait commands = %d, want 2", got)
	}
}

func TestWorktreeStatusEventUpdatesBackgroundTab(t *testing.T) {
	m := &model{
		worktreeMode: true,
		worktreeTabs: []worktreeTab{{ID: "one", Status: worktreeTabIdle}, {ID: "two", Status: worktreeTabIdle}},
	}
	updated, _ := m.Update(worktreeStatusMsg{worktreeID: "two", event: events.NewEvent(events.KindRunStarted, "run")})
	m = updated.(*model)
	if m.worktreeTabs[1].Status != worktreeTabRunning {
		t.Fatalf("background status = %q", m.worktreeTabs[1].Status)
	}
}

func TestWorktreeStatusRemainsWaitingWhileAnotherFormIsPending(t *testing.T) {
	m := &model{
		worktreeMode: true,
		worktreeTabs: []worktreeTab{{ID: "one", Status: worktreeTabWaiting}},
	}
	updated, _ := m.Update(worktreeStatusMsg{
		worktreeID: "one",
		event:      events.NewEvent(events.KindCustom, "run").WithName(events.CustomFormSubmitted),
		waiting:    true,
	})
	m = updated.(*model)
	if got := m.worktreeTabs[0].Status; got != worktreeTabWaiting {
		t.Fatalf("status with another pending form = %q, want waiting", got)
	}
}

func TestWorktreeTerminalStatusRemainsVisibleOnActiveTab(t *testing.T) {
	m := &model{
		worktreeMode:    true,
		worktreeRuntime: &worktreeRuntime{id: "one"},
		worktreeTabs:    []worktreeTab{{ID: "one", Status: worktreeTabRunning}},
	}
	finished := events.NewEvent(events.KindRunFinished, "run").WithPayload(events.RunFinishedData{StopReason: "end_turn"})
	updated, _ := m.Update(worktreeStatusMsg{worktreeID: "one", event: finished})
	m = updated.(*model)
	if got := m.worktreeTabs[0].Status; got != worktreeTabCompleted {
		t.Fatalf("active terminal status = %q, want completed", got)
	}
}

func TestWorktreeStatusIgnoresEventFromReplacedSessionFeed(t *testing.T) {
	m := &model{
		worktreeTabs:          []worktreeTab{{ID: "one", Status: worktreeTabIdle}},
		worktreeStatusSession: map[string]string{"one": "new-session"},
	}
	updated, cmd := m.Update(worktreeStatusMsg{
		worktreeID: "one", sessionID: "old-session", event: events.NewEvent(events.KindRunStarted, "run"),
	})
	m = updated.(*model)
	if cmd != nil || m.worktreeTabs[0].Status != worktreeTabIdle {
		t.Fatalf("stale feed changed tab: cmd=%v status=%q", cmd, m.worktreeTabs[0].Status)
	}
}

func TestInputDispatchCompletionIgnoresPreviousForeground(t *testing.T) {
	m := &model{dispatching: true, dispatchToken: 2}
	updated, cmd := m.Update(inputDispatchedMsg{token: 1, err: context.Canceled})
	m = updated.(*model)
	if cmd != nil || !m.dispatching || len(m.messages) != 0 {
		t.Fatalf("stale dispatch mutated foreground: cmd=%v dispatching=%v messages=%#v", cmd, m.dispatching, m.messages)
	}
}

func TestWorktreeStatusFeedsSurviveTabRefresh(t *testing.T) {
	fixture := newWorktreeRuntimeTestFixture(t)
	active, err := fixture.supervisor.Activate(context.Background(), fixture.mainID)
	if err != nil {
		t.Fatal(err)
	}
	m := newWorktreeRuntimeTestModel(t, fixture, active)
	m.worktreeService = fixture.service
	m.refreshWorktreeTabs()
	if added := m.resetWorktreeStatusFeeds(); len(added) != 1 {
		t.Fatalf("initial status waits = %d, want 1", len(added))
	}
	first := m.worktreeStatusFeeds[active.id]
	m.refreshWorktreeTabs()
	if added := m.resetWorktreeStatusFeeds(); len(added) != 0 {
		t.Fatalf("refresh duplicated %d existing status waits", len(added))
	}
	if m.worktreeStatusFeeds[active.id] != first {
		t.Fatal("tab refresh replaced a live status feed")
	}
	m.closeWorktreeStatusFeeds()
}

func TestSelectingWorktreeDoesNotResetItsStatus(t *testing.T) {
	fixture := newWorktreeRuntimeTestFixture(t)
	active, err := fixture.supervisor.Activate(context.Background(), fixture.mainID)
	if err != nil {
		t.Fatal(err)
	}
	target, err := fixture.supervisor.Create(context.Background(), "persistent tab status")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := fixture.supervisor.Activate(context.Background(), active.id); err != nil {
		t.Fatal(err)
	}
	m := newWorktreeRuntimeTestModel(t, fixture, active)
	m.worktreeService = fixture.service
	m.refreshWorktreeTabs()
	for i := range m.worktreeTabs {
		if m.worktreeTabs[i].ID == target.id {
			m.worktreeTabs[i].Status = worktreeTabCompleted
		}
	}
	prepared, err := fixture.supervisor.prepareActivation(context.Background(), target.id, m.width, m.height, "")
	if err != nil {
		t.Fatal(err)
	}
	m.commitPreparedWorktree(worktreePreparedMsg{token: m.worktreeGeneration, runtime: prepared})

	for _, tab := range m.worktreeTabs {
		if tab.ID == target.id {
			if tab.Status != worktreeTabCompleted {
				t.Fatalf("selected tab status = %q, want completed", tab.Status)
			}
			return
		}
	}
	t.Fatal("selected worktree tab is missing")
}

func TestWorktreeCtrlArrowAndMouseClickSelectTabs(t *testing.T) {
	fixture := newWorktreeRuntimeTestFixture(t)
	active, err := fixture.supervisor.Activate(context.Background(), fixture.mainID)
	if err != nil {
		t.Fatal(err)
	}
	other, err := fixture.supervisor.Create(context.Background(), "keyboard tab target")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := fixture.supervisor.Activate(context.Background(), active.id); err != nil {
		t.Fatal(err)
	}

	t.Run("ctrl right", func(t *testing.T) {
		m := newWorktreeRuntimeTestModel(t, fixture, active)
		m.worktreeService = fixture.service
		m.refreshWorktreeTabs()
		updated, cmd := m.Update(tea.KeyPressMsg{Code: tea.KeyRight, Mod: tea.ModCtrl})
		m = updated.(*model)
		if cmd == nil || !m.worktreeChanging {
			t.Fatalf("ctrl-right did not start tab switch: cmd=%v changing=%v", cmd, m.worktreeChanging)
		}
	})

	t.Run("mouse", func(t *testing.T) {
		m := newWorktreeRuntimeTestModel(t, fixture, active)
		m.worktreeService = fixture.service
		m.width = 120
		m.refreshWorktreeTabs()
		m.renderWorktreeTabBar()
		var x int
		for _, hit := range m.worktreeTabHits {
			if hit.ID == other.id {
				x = hit.Start
			}
		}
		updated, cmd := m.Update(tea.MouseClickMsg{X: x, Y: 1, Button: tea.MouseLeft})
		m = updated.(*model)
		if cmd == nil || !m.worktreeChanging {
			t.Fatalf("click did not start tab switch: cmd=%v changing=%v", cmd, m.worktreeChanging)
		}
	})

	t.Run("mouse blocked during reconciliation", func(t *testing.T) {
		m := newWorktreeRuntimeTestModel(t, fixture, active)
		m.worktreeService = fixture.service
		m.width = 120
		m.refreshWorktreeTabs()
		m.renderWorktreeTabBar()
		m.reconciling = true
		var x int
		for _, hit := range m.worktreeTabHits {
			if hit.ID == other.id {
				x = hit.Start
			}
		}
		_, cmd := m.Update(tea.MouseClickMsg{X: x, Y: 0, Button: tea.MouseLeft})
		if cmd != nil || m.worktreeChanging {
			t.Fatalf("click switched during reconciliation: cmd=%v changing=%v", cmd, m.worktreeChanging)
		}
	})
}
