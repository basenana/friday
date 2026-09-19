package tui

import (
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"charm.land/bubbles/v2/spinner"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"github.com/basenana/friday/core/providers"
)

// TestPresentationOnlyMessagesDoNotRebuildTranscript pins the dirty-flag
// contract: cursor blink, spinner ticks, and mouse wheel frames reuse the
// previous viewport content; only real transcript changes rebuild it.
func TestPresentationOnlyMessagesDoNotRebuildTranscript(t *testing.T) {
	m, _, _ := newTestModel(t)
	_, _ = m.Update(tea.WindowSizeMsg{Width: 80, Height: 40})
	m.appendBlock(chatBlock{kind: blockUser, content: "hello"})
	_ = m.View().Content
	rebuilds := m.transcriptRebuilds
	_, _ = m.Update(tea.MouseClickMsg{}) // presentation-only message
	_ = m.View().Content
	if m.transcriptRebuilds != rebuilds {
		t.Fatalf("presentation-only frame rebuilt transcript: %d -> %d", rebuilds, m.transcriptRebuilds)
	}
	m.appendBlock(chatBlock{kind: blockUser, content: "again"})
	_ = m.View().Content
	if m.transcriptRebuilds != rebuilds+1 {
		t.Fatalf("content change did not rebuild transcript: %d -> %d", rebuilds, m.transcriptRebuilds)
	}
}

type fakeClock struct{ t time.Time }

func (c *fakeClock) Now() time.Time          { return c.t }
func (c *fakeClock) advance(d time.Duration) { c.t = c.t.Add(d) }

// TestStreamingRebuildIsThrottled pins the streaming throttle: deltas within
// the interval reuse the previous build, the window elapsing catches up, and
// flushStreaming forces an immediate rebuild.
func TestStreamingRebuildIsThrottled(t *testing.T) {
	m, _, _ := newTestModel(t)
	_, _ = m.Update(tea.WindowSizeMsg{Width: 80, Height: 40})
	clock := &fakeClock{t: time.Now()}
	m.now = clock.Now

	m.appendStreamContent(blockAssistant, "part one ")
	_ = m.View().Content
	rebuilds := m.transcriptRebuilds

	m.appendStreamContent(blockAssistant, "part two ")
	_ = m.View().Content
	if m.transcriptRebuilds != rebuilds {
		t.Fatalf("streaming rebuild not throttled within interval: %d -> %d", rebuilds, m.transcriptRebuilds)
	}

	clock.advance(streamRebuildInterval + time.Millisecond)
	_ = m.View().Content
	if m.transcriptRebuilds != rebuilds+1 {
		t.Fatalf("throttle window elapsed but no rebuild: %d", m.transcriptRebuilds)
	}

	m.appendStreamContent(blockAssistant, "final ")
	m.flushStreaming(false)
	rebuilds = m.transcriptRebuilds
	_ = m.View().Content
	if m.transcriptRebuilds != rebuilds+1 {
		t.Fatalf("flush did not force rebuild: %d -> %d", rebuilds, m.transcriptRebuilds)
	}
	if view := m.View().Content; !containsPlain(view, "final") {
		t.Fatalf("flushed view is missing final content: %q", truncateForLog(m.View().Content))
	}
}

// TestSpinnerReArmsOnlyWhenChainIsDead pins the spinner arm contract:
// ticks are armed on idle→busy transitions only, an idle tick kills the
// chain, and running event batches re-arm exactly once.
func TestSpinnerReArmsOnlyWhenChainIsDead(t *testing.T) {
	m, _, _ := newTestModel(t)
	if cmd := m.armSpinner(); cmd == nil {
		t.Fatal("first arm while chain is dead must return a tick")
	}
	if cmd := m.armSpinner(); cmd != nil {
		t.Fatal("arm while ticking must return nil")
	}
	// A tick processed while idle kills the chain.
	tick := m.spinner.Tick().(spinner.TickMsg)
	_, _ = m.Update(tick)
	if m.spinnerTicking {
		t.Fatal("idle tick must mark the spinner chain dead")
	}
	if cmd := m.armSpinner(); cmd == nil {
		t.Fatal("arm after chain death must return a tick")
	}
	// Batches while running re-arm exactly once.
	_, _ = m.Update(tick) // kill the chain again
	m.running = true
	_, _ = m.Update(actorEventsMsg{token: m.subscriptionToken})
	if !m.spinnerTicking {
		t.Fatal("running batch must re-arm the spinner chain")
	}
	_, _ = m.Update(actorEventsMsg{token: m.subscriptionToken})
	if cmd := m.armSpinner(); cmd != nil {
		t.Fatal("second running batch must not re-arm again")
	}
}

// TestResizeSettlesBeforeFullRerender pins the resize debounce: width and
// layout apply immediately, blocks stay rendered at the old width until the
// resize settles, only the matching settle token re-renders, and a stale
// settle token is ignored.
func TestResizeSettlesBeforeFullRerender(t *testing.T) {
	m, _, _ := newTestModel(t)
	_, _ = m.Update(tea.WindowSizeMsg{Width: 80, Height: 40})
	m.appendBlock(chatBlock{kind: blockUser, content: strings.Repeat("word ", 60)})
	_ = m.View().Content
	rebuilds := m.transcriptRebuilds

	resized, _ := m.Update(tea.WindowSizeMsg{Width: 60, Height: 30})
	rm := resized.(*model)
	if rm.width != 60 || rm.height != 30 {
		t.Fatalf("resize did not apply immediately: %dx%d", rm.width, rm.height)
	}
	view := rm.View().Content
	if rm.transcriptRebuilds != rebuilds {
		t.Fatalf("resize rebuilt transcript before settling: %d -> %d", rebuilds, rm.transcriptRebuilds)
	}
	_ = view
	// The cached block rendering still targets the old width (the viewport
	// clips overflow horizontally, so the visible view is clipped, not
	// re-wrapped).
	if maxLineWidth(rm.messages[0].rendered) <= 60 {
		t.Fatal("expected pre-settle blocks to keep old-width rendering")
	}

	settled, _ := rm.Update(resizeSettledMsg{token: rm.resizeToken})
	sm := settled.(*model)
	_ = sm.View().Content
	if sm.transcriptRebuilds != rebuilds+1 {
		t.Fatalf("settled resize did not rebuild: %d", sm.transcriptRebuilds)
	}
	if maxLineWidth(sm.messages[0].rendered) > 60 {
		t.Fatalf("settled blocks exceed new width: %d", maxLineWidth(sm.messages[0].rendered))
	}

	// A stale settle token (superseded by a newer resize) is ignored.
	supertoken := sm.resizeToken
	newer, _ := sm.Update(tea.WindowSizeMsg{Width: 100, Height: 40})
	nm := newer.(*model)
	if nm.resizeToken == supertoken {
		t.Fatal("expected resize token to advance")
	}
	ignored, _ := nm.Update(resizeSettledMsg{token: supertoken})
	im := ignored.(*model)
	_ = im.View().Content
	if im.transcriptRebuilds != nm.transcriptRebuilds {
		t.Fatalf("stale settle token triggered rebuild: %d -> %d", nm.transcriptRebuilds, im.transcriptRebuilds)
	}
}

// TestClientRuntimeInfoCachedWithTTL pins the runtime info cache: fresh
// entries are served without touching the registry, invalidation forces a
// recompute, and the TTL expiry recomputes as well.
func TestClientRuntimeInfoCachedWithTTL(t *testing.T) {
	m, _, _ := newTestModel(t)
	clock := &fakeClock{t: time.Now()}
	m.now = clock.Now

	sentinel := providers.ClientRuntimeInfo{Model: "sentinel-model", Effort: "high"}
	m.runtimeInfoCache = sentinel
	m.runtimeInfoAt = clock.Now()
	if got := m.clientRuntimeInfo(); got != sentinel {
		t.Fatalf("cached runtime info = %#v, want sentinel %#v", got, sentinel)
	}

	m.invalidateRuntimeInfo()
	if got := m.clientRuntimeInfo(); got == sentinel {
		t.Fatal("runtime info still served from cache after invalidation")
	}

	m.runtimeInfoCache = sentinel
	m.runtimeInfoAt = clock.Now()
	clock.advance(runtimeInfoTTL + time.Millisecond)
	if got := m.clientRuntimeInfo(); got == sentinel {
		t.Fatal("runtime info still served from cache after TTL expiry")
	}
}

// TestUserInputDispatchesAsynchronously pins the async dispatch contract:
// submissions dispatch off the update loop, follow-up submissions chain to
// preserve order, and dispatch errors surface as error blocks with the
// optimistic running state rolled back.
func TestUserInputDispatchesAsynchronously(t *testing.T) {
	m, _, _ := newTestModel(t)
	_, _ = m.Update(tea.WindowSizeMsg{Width: 80, Height: 40})
	m.textarea.SetValue("first message")
	updated, cmd := m.submitComposer()
	um := updated.(*model)
	if !um.dispatching {
		t.Fatal("submit did not mark dispatching")
	}
	if cmd == nil {
		t.Fatal("submit returned no dispatch command")
	}
	if !um.running {
		t.Fatal("submit did not set optimistic running state")
	}

	// A second submission while dispatching chains instead of racing.
	um.textarea.SetValue("second message")
	updated2, cmd2 := um.submitComposer()
	um2 := updated2.(*model)
	if !um2.dispatching || len(um2.pendingDispatch) != 1 {
		t.Fatalf("second submit did not chain: dispatching=%v pending=%d", um2.dispatching, len(um2.pendingDispatch))
	}
	if cmd2 != nil {
		t.Fatal("chained submit must not return its own dispatch command")
	}

	// Executing the dispatch command produces the completion message; the
	// update handler drains the chain by dispatching the pending input.
	var dispatched inputDispatchedMsg
	switch msg := cmd().(type) {
	case inputDispatchedMsg:
		dispatched = msg
	case tea.BatchMsg:
		for _, sub := range msg {
			if m, ok := sub().(inputDispatchedMsg); ok {
				dispatched = m
			}
		}
	default:
		t.Fatalf("dispatch command produced %T", msg)
	}
	if dispatched.err != nil {
		t.Fatalf("dispatch failed: %v", dispatched.err)
	}
	updated3, next := um2.Update(dispatched)
	um3 := updated3.(*model)
	if !um3.dispatching || next == nil {
		t.Fatalf("chain did not continue: dispatching=%v next=%v", um3.dispatching, next != nil)
	}
	updated4, _ := um3.Update(next())
	um4 := updated4.(*model)
	if um4.dispatching || len(um4.pendingDispatch) != 0 {
		t.Fatalf("chain did not drain: dispatching=%v pending=%d", um4.dispatching, len(um4.pendingDispatch))
	}

	// Error path: running rolls back and an error block appears.
	um4.running = true
	updated5, _ := um4.Update(inputDispatchedMsg{err: errors.New("dispatch boom")})
	um5 := updated5.(*model)
	if um5.dispatching || um5.running {
		t.Fatalf("error path did not roll back: dispatching=%v running=%v", um5.dispatching, um5.running)
	}
	last := um5.messages[len(um5.messages)-1]
	if last.kind != blockError || !strings.Contains(last.content, "dispatch boom") {
		t.Fatalf("error path produced %#v", last)
	}
}

// execCmds executes a (possibly batched) tea.Cmd so asynchronous work such
// as the user-input dispatch actually runs in tests.
func execCmds(cmd tea.Cmd) {
	if cmd == nil {
		return
	}
	switch msg := cmd().(type) {
	case tea.BatchMsg:
		for _, sub := range msg {
			execCmds(sub)
		}
	}
}

// flushDispatch executes the dispatch command returned by a submit and
// drains the dispatch chain so every submitted input reaches the actor
// inbox before assertions run.
func flushDispatch(t *testing.T, m *model, cmd tea.Cmd) {
	t.Helper()
	for {
		execCmds(cmd)
		if !m.dispatching {
			return
		}
		updated, next := m.Update(inputDispatchedMsg{})
		m = updated.(*model)
		cmd = next
	}
}

func containsPlain(s, substr string) bool {
	return strings.Contains(s, substr)
}

// TestStreamedDeltasBufferIncrementally pins the wip-buffer contract: many
// streamed deltas keep the full content accessible (lazy materialization),
// and breakStreamSegments materializes content into the block.
func TestStreamedDeltasBufferIncrementally(t *testing.T) {
	m, _, _ := newTestModel(t)
	_, _ = m.Update(tea.WindowSizeMsg{Width: 80, Height: 40})

	m.appendStreamContent(blockAssistant, "HEAD ")
	for i := 0; i < 500; i++ {
		m.appendStreamContent(blockAssistant, fmt.Sprintf("delta-%d ", i))
	}
	m.appendStreamContent(blockAssistant, "TAILMARK")

	idx := m.textBlock
	if idx < 0 {
		t.Fatal("no streaming block allocated")
	}
	// While still streaming, rendering must see the full buffered content.
	if rendered := m.renderBlock(&m.messages[idx]); !containsPlain(rendered, "TAILMARK") {
		t.Fatalf("rendered streaming block is missing tail marker: %q", truncateForLog(rendered))
	}
	// The un-materialized block must not have copied content per delta.
	if !containsPlain(m.messages[idx].content, "HEAD ") {
		t.Fatalf("streaming block lost head content: %q", m.messages[idx].content)
	}

	m.breakStreamSegments()
	b := &m.messages[idx]
	if b.wip.Len() != 0 {
		t.Fatalf("wip not materialized on segment break: %d bytes pending", b.wip.Len())
	}
	for _, marker := range []string{"HEAD ", "delta-0 ", "delta-499 ", "TAILMARK"} {
		if !containsPlain(b.content, marker) {
			t.Fatalf("materialized content missing %q: len=%d", marker, len(b.content))
		}
	}
}

// TestApplyProjectionMarksTranscriptDirty pins the session-switch contract:
// /clear and /resume swap the transcript wholesale via applyProjection, so
// the next View must rebuild instead of showing the previous session's
// blocks from the cached viewport content.
func TestApplyProjectionMarksTranscriptDirty(t *testing.T) {
	m, _, _ := newTestModel(t)
	_, _ = m.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	m.appendBlock(chatBlock{kind: blockUser, content: "old session marker"})
	_ = m.View().Content
	rebuilds := m.transcriptRebuilds

	m.applyProjection(transcriptProjection{})
	view := m.View().Content
	if containsPlain(view, "old session marker") {
		t.Fatalf("view kept previous session content after applyProjection")
	}
	if m.transcriptRebuilds != rebuilds+1 {
		t.Fatalf("applyProjection did not rebuild transcript: %d -> %d", rebuilds, m.transcriptRebuilds)
	}
}

// TestFrameHeightMatchesTerminal pins the layout invariant: every rendered
// frame must be exactly as tall as the terminal. If any part (activity line,
// menus, queue, composer) is not reserved in layout()'s height budget, the
// input box sinks and the status line gets pushed off-screen.
func TestFrameHeightMatchesTerminal(t *testing.T) {
	const termHeight = 24
	states := []struct {
		name   string
		mutate func(t *testing.T, m *model)
	}{
		{"idle", nil},
		{"running", func(t *testing.T, m *model) { m.running = true }},
		{"plan compacting", func(t *testing.T, m *model) { m.planCompacting = true }},
		{"manual compacting", func(t *testing.T, m *model) { m.manualCompacting = true }},
		{"slash menu with matches", func(t *testing.T, m *model) {
			m.textarea.SetValue("/")
			m.refreshMenu()
			if len(m.menu.items) == 0 {
				t.Fatal("test model has no slash commands to match")
			}
		}},
		{"menu without matches", func(t *testing.T, m *model) {
			m.textarea.SetValue("/zzzz-no-such-command")
			m.refreshMenu()
			if len(m.menu.items) != 0 {
				t.Fatal("expected no menu matches for unmatched prefix")
			}
		}},
		{"queued input", func(t *testing.T, m *model) {
			m.queued = []pendingInput{{text: "next"}}
		}},
		{"menu with long descriptions", func(t *testing.T, m *model) {
			items := make([]menuItem, 10)
			for i := range items {
				items[i] = menuItem{
					value:       "/cmd",
					label:       "/cmd",
					description: strings.Repeat("d", 150),
				}
			}
			m.menu = menuState{mode: menuCommands, items: items}
		}},
		{"queue with long text", func(t *testing.T, m *model) {
			m.queued = []pendingInput{{text: strings.Repeat("q", 200)}}
		}},
		{"running with menu and queue", func(t *testing.T, m *model) {
			m.running = true
			m.queued = []pendingInput{{text: "next"}}
			m.textarea.SetValue("/")
			m.refreshMenu()
		}},
	}
	for _, state := range states {
		t.Run(state.name, func(t *testing.T) {
			m, _, _ := newTestModel(t)
			_, _ = m.Update(tea.WindowSizeMsg{Width: 100, Height: termHeight})
			if state.mutate != nil {
				state.mutate(t, m)
			}
			if h := lipgloss.Height(m.View().Content); h != termHeight {
				t.Errorf("frame height = %d, want %d (input/status pushed off)", h, termHeight)
			}
		})
	}
}
