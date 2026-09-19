package tui

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	eventbus "github.com/hyponet/eventbus/bus"

	"github.com/basenana/friday/bus"
	"github.com/basenana/friday/coder/commands"
	"github.com/basenana/friday/config"
	"github.com/basenana/friday/core/actor/events"
	"github.com/basenana/friday/core/collaboration"
	"github.com/basenana/friday/core/planning"
	"github.com/basenana/friday/core/providers"
	coresession "github.com/basenana/friday/core/session"
	"github.com/basenana/friday/core/types"
	"github.com/basenana/friday/sessions"
	sessionfile "github.com/basenana/friday/sessions/file"
	sessionusage "github.com/basenana/friday/sessions/usage"
)

func TestAlternateScreenMode(t *testing.T) {
	t.Setenv("ZELLIJ", "")
	if !useAlternateScreen("auto") || !useAlternateScreen("always") || useAlternateScreen("never") {
		t.Fatal("unexpected alternate-screen mode selection")
	}
	t.Setenv("ZELLIJ", "1")
	if useAlternateScreen("auto") {
		t.Fatal("auto should preserve Zellij scrollback")
	}
}

func TestTerminalSafeStripsControlSequences(t *testing.T) {
	got := terminalSafe("ok\x1b[31mred\x1b[0m\x00")
	if got != "okred" {
		t.Fatalf("terminalSafe = %q", got)
	}
}

type menuTestCommand struct {
	name        string
	aliases     []string
	description string
}

func (c menuTestCommand) Name() string        { return c.name }
func (c menuTestCommand) Aliases() []string   { return c.aliases }
func (c menuTestCommand) Description() string { return c.description }
func (menuTestCommand) Execute(*commands.Context) (*commands.Result, error) {
	return &commands.Result{}, nil
}

func menuLabels(items []menuItem) []string {
	if len(items) == 0 {
		return nil
	}
	labels := make([]string, len(items))
	for i, item := range items {
		labels[i] = item.label
	}
	return labels
}

func menuLabelIndex(items []menuItem, label string) int {
	for i, item := range items {
		if item.label == label {
			return i
		}
	}
	return -1
}

func TestSlashPopupFiltersAndCompletes(t *testing.T) {
	for _, input := range []string{"/comp", "/COM"} {
		t.Run(input, func(t *testing.T) {
			m, _, _ := newTestModel(t)
			m.textarea.SetValue(input)
			m.refreshMenu()
			if m.menu.mode != menuCommands || len(m.menu.items) != 1 || m.menu.items[0].label != "/compact" {
				t.Fatalf("unexpected menu: %#v", m.menu)
			}
			m.acceptMenuSelection(false)
			if got := m.textarea.Value(); got != "/compact " {
				t.Fatalf("completion = %q", got)
			}
		})
	}
}

func TestSlashPopupMatchesNamesAndAliasesByPrefixOnly(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  []string
	}{
		{name: "canonical middle substring", input: "/omp", want: nil},
		{name: "description text", input: "/conversation", want: nil},
		{name: "alias prefix", input: "/EXI", want: []string{"/quit"}},
		{name: "alias middle substring", input: "/xit", want: nil},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m, _, _ := newTestModel(t)
			m.textarea.SetValue(tt.input)
			m.refreshMenu()
			if got := menuLabels(m.menu.items); !reflect.DeepEqual(got, tt.want) {
				t.Fatalf("labels for %q = %v, want %v", tt.input, got, tt.want)
			}
		})
	}
}

func TestSlashPopupRanksCanonicalMatchesBeforeAliasMatches(t *testing.T) {
	m, _, _ := newTestModel(t)
	m.textarea.SetValue("/e")
	m.refreshMenu()
	canonical := menuLabelIndex(m.menu.items, "/effort")
	alias := menuLabelIndex(m.menu.items, "/quit")
	if canonical < 0 || alias < 0 || canonical >= alias {
		t.Fatalf("canonical prefix must precede alias prefix: %v", menuLabels(m.menu.items))
	}
}

func TestSlashPopupRanksExactCanonicalMatchFirst(t *testing.T) {
	m, _, _ := newTestModel(t)
	m.cmdRegistry.Register(menuTestCommand{name: "model-info"})
	m.textarea.SetValue("/MODEL")
	m.refreshMenu()
	if got := menuLabels(m.menu.items); len(got) < 2 || got[0] != "/model" || got[1] != "/model-info" {
		t.Fatalf("exact canonical match must be first: %v", got)
	}
}

func TestSlashPopupSortsSameRankCaseInsensitively(t *testing.T) {
	m, _, _ := newTestModel(t)
	for _, name := range []string{"zzBeta", "zzalpha", "ZZalpha"} {
		m.cmdRegistry.Register(menuTestCommand{name: name})
	}
	m.textarea.SetValue("/zz")
	m.refreshMenu()
	want := []string{"/ZZalpha", "/zzalpha", "/zzBeta"}
	if got := menuLabels(m.menu.items); !reflect.DeepEqual(got, want) {
		t.Fatalf("same-rank labels = %v, want %v", got, want)
	}
}

func TestSlashPopupDeduplicatesCanonicalAndAliasMatches(t *testing.T) {
	m, _, _ := newTestModel(t)
	m.cmdRegistry.Register(menuTestCommand{name: "echo", aliases: []string{"echo-alias"}})
	m.textarea.SetValue("/echo")
	m.refreshMenu()
	if got := menuLabels(m.menu.items); !reflect.DeepEqual(got, []string{"/echo"}) {
		t.Fatalf("canonical and alias match produced duplicates: %v", got)
	}
}

func TestSlashPopupEmptyQueryShowsCanonicalCommandsOnce(t *testing.T) {
	m, _, _ := newTestModel(t)
	m.cmdRegistry.Register(menuTestCommand{name: "echo", aliases: []string{"echo-alias"}})
	m.textarea.SetValue("/")
	m.refreshMenu()
	counts := make(map[string]int)
	for _, item := range m.menu.items {
		counts[item.label]++
	}
	if counts["/echo"] != 1 || counts["/echo-alias"] != 0 {
		t.Fatalf("empty query expanded aliases: %v", menuLabels(m.menu.items))
	}
}

func TestSlashPopupAliasCompletesCanonicalName(t *testing.T) {
	m, _, _ := newTestModel(t)
	m.textarea.SetValue("/exit")
	m.refreshMenu()
	if got := menuLabels(m.menu.items); !reflect.DeepEqual(got, []string{"/quit"}) {
		t.Fatalf("alias labels = %v", got)
	}
	m.acceptMenuSelection(false)
	if got := m.textarea.Value(); got != "/quit " {
		t.Fatalf("alias completion = %q", got)
	}
}

func TestTabQueuesWhileRunning(t *testing.T) {
	m, _, _ := newTestModel(t)
	m.running = true
	m.textarea.SetValue("follow up")
	got, _ := m.updateKey(tea.KeyPressMsg{Code: tea.KeyTab})
	model := got.(*model)
	if len(model.queued) != 1 || model.queued[0].text != "follow up" {
		t.Fatalf("queue = %#v", model.queued)
	}
	if model.textarea.Value() != "" {
		t.Fatal("composer was not cleared")
	}
}

func TestRunningEnterQueuesActorInputWithoutInterruptingCurrentRun(t *testing.T) {
	m, _, _ := newTestModel(t)
	inbox := make(chan bus.Envelope, 1)
	preempts := make(chan bus.Envelope, 1)
	inboxID := m.registry.Bus().SubscribeSerial([]string{bus.TopicInbox(m.sessionID)}, func(env bus.Envelope) {
		if env.Name == bus.InboxUserText {
			inbox <- env
		}
	}, eventbus.SerialConfig{Overflow: eventbus.OverflowBlock})
	preemptID := m.registry.Bus().SubscribeSerial([]string{bus.TopicPreempt(m.sessionID)}, func(env bus.Envelope) {
		preempts <- env
	}, eventbus.SerialConfig{Overflow: eventbus.OverflowBlock})
	defer m.registry.Bus().Unsubscribe(inboxID)
	defer m.registry.Bus().Unsubscribe(preemptID)

	startedAt := time.Unix(123, 0)
	m.running = true
	m.currentRunID = "run-a"
	m.runStartedAt = startedAt
	m.runActivity = "running tool-a"
	m.messages = []chatBlock{
		{kind: blockAssistant, content: "streaming"},
		{kind: blockReasoning, content: "thinking"},
		{kind: blockToolCall, id: "tool-a", toolName: "tool-a", pending: true},
	}
	m.textBlock = 0
	m.reasonBlock = 1
	m.toolCalls = map[string]int{"tool-a": 2}
	m.textarea.SetValue("follow up")

	got, cmd := m.submitComposer()
	m = got.(*model)
	if cmd == nil {
		t.Fatal("running Enter returned no spinner command")
	}
	if len(m.queued) != 0 {
		t.Fatalf("running Enter used local queue: %#v", m.queued)
	}
	if !m.running || m.currentRunID != "run-a" || !m.runStartedAt.Equal(startedAt) || m.runActivity != "running tool-a" {
		t.Fatalf("current run state changed: running=%v run=%q started=%v activity=%q", m.running, m.currentRunID, m.runStartedAt, m.runActivity)
	}
	if m.textBlock != 0 || m.reasonBlock != 1 || !reflect.DeepEqual(m.toolCalls, map[string]int{"tool-a": 2}) {
		t.Fatalf("stream trackers changed: text=%d reasoning=%d tools=%v", m.textBlock, m.reasonBlock, m.toolCalls)
	}
	for i := 0; i < 3; i++ {
		if m.messages[i].interrupted {
			t.Fatalf("current block %d was marked interrupted", i)
		}
	}
	if !m.messages[2].pending {
		t.Fatal("current tool block was no longer pending")
	}
	userCount := 0
	for _, message := range m.messages {
		if message.kind == blockUser && message.content == "follow up" {
			userCount++
		}
	}
	if userCount != 1 {
		t.Fatalf("follow-up user blocks = %d, want 1", userCount)
	}
	flushDispatch(t, m, cmd)

	select {
	case env := <-inbox:
		var body bus.UserTextInput
		if err := events.DecodePayload(env.Event, &body); err != nil {
			t.Fatal(err)
		}
		if body.Text != "follow up" || body.TurnID == "" {
			t.Fatalf("actor input = %+v", body)
		}
	case <-time.After(time.Second):
		t.Fatal("running Enter did not publish Actor input")
	}
	select {
	case env := <-preempts:
		t.Fatalf("running Enter published preempt: %+v", env)
	case <-time.After(50 * time.Millisecond):
	}
}

func TestConsecutiveRunningEnterInputsHaveDistinctOrderedTurnIDs(t *testing.T) {
	m, _, _ := newTestModel(t)
	inbox := make(chan bus.Envelope, 2)
	id := m.registry.Bus().SubscribeSerial([]string{bus.TopicInbox(m.sessionID)}, func(env bus.Envelope) {
		if env.Name == bus.InboxUserText {
			inbox <- env
		}
	}, eventbus.SerialConfig{Buffer: 2, Overflow: eventbus.OverflowBlock})
	defer m.registry.Bus().Unsubscribe(id)

	m.running = true
	var dispatch tea.Cmd
	for _, text := range []string{"second", "third"} {
		m.textarea.SetValue(text)
		got, cmd := m.submitComposer()
		m = got.(*model)
		if cmd != nil {
			dispatch = cmd
		}
	}
	flushDispatch(t, m, dispatch)

	var inputs []bus.UserTextInput
	for len(inputs) < 2 {
		select {
		case env := <-inbox:
			var body bus.UserTextInput
			if err := events.DecodePayload(env.Event, &body); err != nil {
				t.Fatal(err)
			}
			inputs = append(inputs, body)
		case <-time.After(time.Second):
			t.Fatalf("timed out waiting for inputs: %+v", inputs)
		}
	}
	if inputs[0].Text != "second" || inputs[1].Text != "third" {
		t.Fatalf("input order = %+v", inputs)
	}
	if inputs[0].TurnID == "" || inputs[1].TurnID == "" || inputs[0].TurnID == inputs[1].TurnID {
		t.Fatalf("turn ids = %q, %q", inputs[0].TurnID, inputs[1].TurnID)
	}
}

func TestEscPreemptsCurrentRunOnceAndPreservesLocalInput(t *testing.T) {
	m, _, _ := newTestModel(t)
	preempts := make(chan bus.Envelope, 2)
	id := m.registry.Bus().SubscribeSerial([]string{bus.TopicPreempt(m.sessionID)}, func(env bus.Envelope) {
		preempts <- env
	}, eventbus.SerialConfig{Buffer: 2, Overflow: eventbus.OverflowBlock})
	defer m.registry.Bus().Unsubscribe(id)

	m.running = true
	m.currentRunID = "run-a"
	m.textarea.SetValue("not submitted")
	m.queued = []pendingInput{{text: "/compact"}}
	got, _ := m.updateKey(tea.KeyPressMsg{Code: tea.KeyEsc})
	m = got.(*model)
	if !m.running || !m.cancelling {
		t.Fatalf("Esc state: running=%v cancelling=%v", m.running, m.cancelling)
	}
	if m.textarea.Value() != "not submitted" || len(m.queued) != 1 {
		t.Fatalf("Esc cleared local input: composer=%q queue=%#v", m.textarea.Value(), m.queued)
	}

	select {
	case env := <-preempts:
		var body bus.PreemptInput
		if err := events.DecodePayload(env.Event, &body); err != nil {
			t.Fatal(err)
		}
		if body.Scope != string(bus.PreemptCurrent) {
			t.Fatalf("preempt scope = %q, want %q", body.Scope, bus.PreemptCurrent)
		}
	case <-time.After(time.Second):
		t.Fatal("Esc did not publish preempt")
	}

	m.handleActorEvent(events.NewEvent(events.KindRunFinished, "stale-run").WithPayload(events.RunFinishedData{StopReason: "end_turn"}))
	if !m.cancelling {
		t.Fatal("stale terminal event cleared current cancellation state")
	}
	got, _ = m.updateKey(tea.KeyPressMsg{Code: tea.KeyEsc})
	m = got.(*model)
	select {
	case env := <-preempts:
		t.Fatalf("repeated Esc published another preempt: %+v", env)
	case <-time.After(50 * time.Millisecond):
	}
}

func TestRunningSlashCommandsRespectRunPolicy(t *testing.T) {
	m, _, _ := newTestModel(t)
	m.running = true
	m.textarea.SetValue("/status")
	got, _ := m.submitComposer()
	m = got.(*model)
	if len(m.queued) != 0 {
		t.Fatalf("immediate status was queued: queued=%v", m.queued)
	}
	if len(m.messages) == 0 || !strings.Contains(m.messages[len(m.messages)-1].content, "Friday status") {
		t.Fatalf("status did not execute immediately: %#v", m.messages)
	}

	m.textarea.SetValue("/plan design auth")
	got, _ = m.submitComposer()
	m = got.(*model)
	if len(m.queued) != 1 || m.queued[0].text != "/plan design auth" {
		t.Fatalf("deferred plan command was not queued: %#v", m.queued)
	}

	m.textarea.SetValue("/compact")
	got, _ = m.submitComposer()
	m = got.(*model)
	if len(m.queued) != 2 || m.queued[1].text != "/compact" {
		t.Fatalf("deferred compact command was not queued: %#v", m.queued)
	}
}

func TestPlanModeCommandsAndShortcut(t *testing.T) {
	m, mgr, _ := newTestModel(t)
	if _, _ = m.handleSlash("/plan"); m.mode != collaboration.ModePlan {
		t.Fatalf("mode after /plan = %q", m.mode)
	}
	runtimeState, _ := mgr.Runtime(m.sessionID)
	if runtimeState.Mode != collaboration.ModePlan {
		t.Fatalf("persisted mode = %q", runtimeState.Mode)
	}
	if _, _ = m.handleSlash("/plan off"); m.mode != collaboration.ModeDefault {
		t.Fatalf("mode after /plan off = %q", m.mode)
	}
	got, _ := m.updateKey(tea.KeyPressMsg{Code: tea.KeyTab, Mod: tea.ModShift})
	if got.(*model).mode != collaboration.ModePlan {
		t.Fatalf("Shift+Tab mode = %q", got.(*model).mode)
	}
}

func TestResumeCurrentSessionIsNoop(t *testing.T) {
	m, _, _ := newTestModel(t)
	token := m.subscriptionToken
	got, _ := m.handleSlash("/resume " + m.sessionID)
	m = got.(*model)
	if m.sessionID != "session-initial" || m.subscriptionToken != token {
		t.Fatalf("current resume changed session: id=%q token=%d", m.sessionID, m.subscriptionToken)
	}
}

func TestPlanHandoffPausesQueueAndCanKeepPlanning(t *testing.T) {
	m, _, _ := newTestModel(t)
	m.mode = collaboration.ModePlan
	m.latestPlan = &planning.Artifact{ID: "plan-1", SessionID: m.sessionID, Version: 1, Status: planning.ArtifactProposed, Markdown: "the plan"}
	m.running = true
	m.currentRunID = "run-1"
	m.queued = []pendingInput{{text: "queued follow-up"}}
	m.handleActorEvent(events.NewEvent(events.KindRunFinished, "run-1").WithPayload(events.RunFinishedData{StopReason: "plan_completed"}))
	if m.planHandoff == nil || m.running {
		t.Fatalf("handoff state missing: popup=%v running=%v", m.planHandoff != nil, m.running)
	}
	if _, cmd := m.dispatchNextQueued(); cmd != nil || len(m.queued) != 1 {
		t.Fatalf("queue dispatched behind handoff: cmd=%v queue=%v", cmd != nil, m.queued)
	}
	m.planHandoff.selected = 1
	got, cmd := m.updatePlanHandoff(tea.KeyPressMsg{Code: tea.KeyEnter})
	m = got.(*model)
	if m.planHandoff != nil || m.mode != collaboration.ModePlan || m.latestPlan.Status != planning.ArtifactProposed || cmd == nil {
		t.Fatalf("keep planning state: popup=%v mode=%q status=%q dispatch=%v", m.planHandoff != nil, m.mode, m.latestPlan.Status, cmd != nil)
	}
}

func TestPlanHandoffOnlyOpensForCompletedPlanRun(t *testing.T) {
	m, _, _ := newTestModel(t)
	m.mode = collaboration.ModePlan
	m.latestPlan = &planning.Artifact{ID: "old-plan", SessionID: m.sessionID, Version: 1, Status: planning.ArtifactProposed}
	m.running, m.currentRunID = true, "run-ordinary"
	m.handleActorEvent(events.NewEvent(events.KindRunFinished, "run-ordinary").WithPayload(events.RunFinishedData{StopReason: "end_turn"}))
	if m.planHandoff != nil {
		t.Fatal("an ordinary planning turn reopened the old plan handoff")
	}
}

func TestModeChangedEventUpdatesTUI(t *testing.T) {
	m, _, _ := newTestModel(t)
	m.handleActorEvent(events.NewEvent(events.KindCustom, "run").WithName(events.CustomModeChanged).WithPayload(events.ModeChangedBody{
		Mode: string(collaboration.ModePlan), Source: "agent", Reason: "needs design",
	}))
	if m.mode != collaboration.ModePlan {
		t.Fatalf("mode = %q", m.mode)
	}
	if last := m.messages[len(m.messages)-1].content; !strings.Contains(last, "mode · plan · agent") {
		t.Fatalf("mode marker = %q", last)
	}
}

func TestRunElapsedStatusAndCompletionMarker(t *testing.T) {
	m, _, _ := newTestModel(t)
	m.width = 120
	started := time.Date(2026, 9, 11, 10, 0, 0, 0, time.UTC)
	now := started.Add(68 * time.Second)
	m.now = func() time.Time { return now }
	startEvent := events.NewEvent(events.KindRunStarted, "run-time")
	startEvent.Timestamp = started
	m.handleActorEvent(startEvent)
	if status := terminalSafe(m.renderStatus()); !strings.Contains(status, "● running") || strings.Contains(status, "1m 08s") || strings.Contains(status, "friday") {
		t.Fatalf("running status should be concise and omit elapsed time and branding: %q", status)
	}

	finishEvent := events.NewEvent(events.KindRunFinished, "run-time").WithPayload(events.RunFinishedData{
		StopReason: "end_turn", DurationMs: 68_000,
	})
	finishEvent.Timestamp = now
	m.handleActorEvent(finishEvent)
	if m.running || !m.runStartedAt.IsZero() {
		t.Fatalf("run state not cleared: running=%v started=%v", m.running, m.runStartedAt)
	}
	if last := m.messages[len(m.messages)-1].content; last != "completed in 1m 08s" {
		t.Fatalf("completion marker = %q", last)
	}
	count := len(m.messages)
	m.handleActorEvent(finishEvent)
	if len(m.messages) != count {
		t.Fatal("duplicate RUN_FINISHED produced a second completion marker")
	}
}

func TestStatusExplainsRunningInboxAndCancelKeys(t *testing.T) {
	for _, loopActive := range []bool{false, true} {
		name := "normal"
		if loopActive {
			name = "loop"
		}
		t.Run(name, func(t *testing.T) {
			m, _, _ := newTestModel(t)
			m.width = 120
			m.loopActive = loopActive
			m.running = true
			status := terminalSafe(m.renderStatus())
			for _, want := range []string{"Enter/Tab send next", "Esc cancel"} {
				if !strings.Contains(status, want) {
					t.Fatalf("status missing %q: %q", want, status)
				}
			}
			for _, obsolete := range []string{"inter" + "rupt", "ste" + "er", "ste" + "ering"} {
				if strings.Contains(strings.ToLower(status), obsolete) {
					t.Fatalf("status contains obsolete %q wording: %q", obsolete, status)
				}
			}
			if loopActive && (!strings.Contains(status, "loop") || strings.Contains(status, "default")) {
				t.Fatalf("Loop status did not replace the underlying mode: %q", status)
			}
		})
	}
}

func TestRuntimeModelDisplayOmitsDefaultEffort(t *testing.T) {
	tests := []struct {
		info providers.ClientRuntimeInfo
		want string
	}{
		{info: providers.ClientRuntimeInfo{Model: "gpt-test"}, want: "gpt-test"},
		{info: providers.ClientRuntimeInfo{Model: "gpt-test", Effort: providers.ReasoningEffortDefault}, want: "gpt-test"},
		{info: providers.ClientRuntimeInfo{Model: "gpt-test", Effort: providers.ReasoningEffortHigh}, want: "gpt-test [high]"},
	}
	for _, tt := range tests {
		if got := formatRuntimeModel(tt.info); got != tt.want {
			t.Errorf("formatRuntimeModel(%+v) = %q, want %q", tt.info, got, tt.want)
		}
	}
}

func TestStatusShowsSessionClientEndpoint(t *testing.T) {
	m, _, _ := newTestModel(t)
	m.showStatus()
	status := m.messages[len(m.messages)-1].content
	if !strings.Contains(status, "- Model: `gpt-4o`") || !strings.Contains(status, "- Endpoint: `openai.") {
		t.Fatalf("status missing client runtime details: %q", status)
	}
	if strings.Contains(status, "Reasoning:") {
		t.Fatalf("status retained duplicate reasoning line: %q", status)
	}
}

func TestStatusShowsPersistedModelAndTurnUsage(t *testing.T) {
	m, _, _ := newTestModel(t)
	sess, release, err := m.acquireCurrentSession()
	if err != nil {
		t.Fatal(err)
	}
	stats := &coresession.ModelCallStats{
		Model:       "gpt-usage",
		EndpointKey: "openai.usage",
		Tokens: providers.Tokens{
			PromptTokens: 1_250, CachedPromptTokens: 1_000,
			CacheCreationTokens: 50, CompletionTokens: 250,
		},
	}
	if err := (sessionusage.Hook{}).AfterModelCall(context.Background(), sess, nil, stats); err != nil {
		release()
		t.Fatal(err)
	}
	if err := sessionusage.RecordTurn(context.Background(), sess, "end_turn", 90_000); err != nil {
		release()
		t.Fatal(err)
	}
	release()

	m.running = true
	m.runStartedAt = m.nowTime().Add(-5 * time.Second)
	m.showStatus()
	status := m.messages[len(m.messages)-1].content
	for _, want := range []string{
		"- Turns: 1 · total 1m 30s · avg 1m 30s",
		"- Current turn: running 5s",
		"### Model usage",
		"`gpt-usage` via `openai.usage`: 1 calls",
		"input 1.2K · cached 1K (80.0%) · cache write 50 · output 250 · total 1.5K",
	} {
		if !strings.Contains(status, want) {
			t.Fatalf("status missing %q: %q", want, status)
		}
	}
}

func TestModelRetryDisplayDistinguishesFallback(t *testing.T) {
	m := &model{textBlock: -1, reasonBlock: -1, toolCalls: make(map[string]int)}
	fallbackEvent := events.NewEvent(events.KindCustom, "run-fallback").
		WithName(events.CustomModelRetry).
		WithPayload(events.CustomData{Body: map[string]any{
			"provider": "fallback", "model": "shared", "model_key": "b.example/shared",
			"previous_model": "shared", "previous_model_key": "a.example/shared",
			"attempt": "2", "max_attempts": "3",
		}})
	m.handleActorEvent(fallbackEvent)
	if got := m.messages[len(m.messages)-1].content; got != "model fallback · a.example/shared → b.example/shared · attempt 2/3" {
		t.Fatalf("fallback label = %q", got)
	}

	retryEvent := events.NewEvent(events.KindCustom, "run-retry").
		WithName(events.CustomModelRetry).
		WithPayload(events.CustomData{Body: map[string]any{
			"provider": "openai", "model": "gpt-test", "attempt": "2", "max_attempts": "3",
		}})
	m.handleActorEvent(retryEvent)
	if got := m.messages[len(m.messages)-1].content; got != "model request failed · retrying · gpt-test · attempt 2/3" {
		t.Fatalf("retry label = %q", got)
	}
}

func TestRunCompletionFallsBackToEventTimestamps(t *testing.T) {
	m, _, _ := newTestModel(t)
	started := time.Date(2026, 9, 11, 10, 0, 0, 0, time.UTC)
	startEvent := events.NewEvent(events.KindRunStarted, "old-run")
	startEvent.Timestamp = started
	m.handleActorEvent(startEvent)
	finishEvent := events.NewEvent(events.KindRunFinished, "old-run").WithPayload(events.RunFinishedData{StopReason: "cancelled"})
	finishEvent.Timestamp = started.Add(9 * time.Second)
	m.handleActorEvent(finishEvent)
	if last := m.messages[len(m.messages)-1].content; last != "cancelled after 9s" {
		t.Fatalf("fallback marker = %q", last)
	}
}

func TestRestoreProposedPlanReopensHandoff(t *testing.T) {
	m, _, _ := newTestModel(t)
	m.mode = collaboration.ModePlan
	m.latestPlan = &planning.Artifact{ID: "plan-restore", SessionID: m.sessionID, Version: 1, Status: planning.ArtifactProposed, Markdown: "## Summary\n\nrestored plan"}
	m.restorePlanHandoff()
	if m.planHandoff == nil {
		t.Fatal("proposed plan did not restore approval handoff")
	}
	if len(m.messages) == 0 || m.messages[len(m.messages)-1].kind != blockPlan {
		t.Fatal("proposed plan did not restore its complete history card")
	}
}

func TestInfoCommandsRestoreEvictedActor(t *testing.T) {
	for _, command := range []string{"/context", "/status", "/tasks"} {
		t.Run(command, func(t *testing.T) {
			m, _, _ := newTestModel(t)
			old, ok := m.registry.Get(m.sessionID)
			if !ok {
				t.Fatal("expected initial actor")
			}
			m.registry.Shutdown(m.sessionID)
			before := len(m.messages)
			got, _ := m.handleSlash(command)
			m = got.(*model)
			rebuilt, live := m.registry.Get(m.sessionID)
			if !live || rebuilt == old {
				t.Fatalf("%s did not rebuild actor: old=%p rebuilt=%p live=%v", command, old, rebuilt, live)
			}
			if command == "/tasks" {
				if m.selector == nil || m.selector.kind != selectorTasks {
					t.Fatal("/tasks did not open the task selector")
				}
				return
			}
			if len(m.messages) <= before || strings.Contains(m.messages[len(m.messages)-1].content, "unavailable") {
				t.Fatalf("%s response = %#v", command, m.messages[before:])
			}
		})
	}
}

func TestPlanHandoffCompactsAndImplementsInCurrentSession(t *testing.T) {
	m, mgr, raw := newTestModel(t)
	plan := planning.Artifact{ID: "plan-current", SessionID: m.sessionID, Version: 1, Title: "Current", Status: planning.ArtifactProposed, Markdown: "## Summary\ncurrent plan"}
	if err := mgr.SavePlan(m.sessionID, plan); err != nil {
		t.Fatal(err)
	}
	if err := mgr.SetMode(m.sessionID, collaboration.ModePlan); err != nil {
		t.Fatal(err)
	}
	before, err := raw.List()
	if err != nil {
		t.Fatal(err)
	}
	originalID := m.sessionID
	m.mode, m.latestPlan, m.planHandoff = collaboration.ModePlan, &plan, &planHandoffState{}
	oldActor, ok := m.registry.Get(originalID)
	if !ok {
		t.Fatal("expected initial actor")
	}
	m.registry.Shutdown(originalID)

	got, cmd := m.updatePlanHandoff(tea.KeyPressMsg{Code: tea.KeyEnter})
	m = got.(*model)
	if cmd == nil || !m.planCompacting || m.running {
		t.Fatalf("approval did not start compact: cmd=%v compacting=%v running=%v", cmd != nil, m.planCompacting, m.running)
	}
	rawMsg := cmd()
	msg, ok := rawMsg.(planCompactFinishedMsg)
	if !ok {
		t.Fatalf("compact command returned %T", rawMsg)
	}
	got, _ = m.Update(msg)
	m = got.(*model)
	after, err := raw.List()
	if err != nil {
		t.Fatal(err)
	}
	if m.sessionID != originalID || len(after) != len(before) {
		t.Fatalf("approval changed session: id=%q want=%q sessions=%d/%d", m.sessionID, originalID, len(after), len(before))
	}
	if rebuilt, ok := m.registry.Get(originalID); !ok || rebuilt == oldActor {
		t.Fatalf("approval did not rebuild the evicted actor: old=%p rebuilt=%p live=%v", oldActor, rebuilt, ok)
	}
	if m.planCompacting || !m.running || m.mode != collaboration.ModeDefault || m.latestPlan.Status != planning.ArtifactAccepted {
		t.Fatalf("handoff: compacting=%v running=%v mode=%q plan=%+v", m.planCompacting, m.running, m.mode, m.latestPlan)
	}
	persisted, err := raw.LoadLatestPlan(originalID)
	if err != nil || persisted == nil || persisted.Status != planning.ArtifactAccepted {
		t.Fatalf("persisted plan = %+v, err=%v", persisted, err)
	}
	meta, err := raw.GetMeta(originalID)
	if err != nil || meta.Runtime.Mode != collaboration.ModeDefault {
		t.Fatalf("persisted mode = %+v, err=%v", meta, err)
	}
	last := m.messages[len(m.messages)-1].content
	if strings.Contains(last, plan.Markdown) || last != "Implement the approved plan. Re-read relevant files as needed and verify the result." {
		t.Fatalf("implementation prompt = %q", last)
	}
}

func TestPlanHandoffFailuresRestoreProposalAndQueue(t *testing.T) {
	setup := func(t *testing.T) (*model, *sessions.Manager, *sessionfile.FileSessionStore, *faultStore, planning.Artifact) {
		t.Helper()
		m, mgr, raw, faults := newFaultTestModel(t)
		plan := planning.Artifact{ID: "plan-fault", SessionID: m.sessionID, Version: 1, Title: "Fault", Status: planning.ArtifactProposed, Markdown: "## Summary\nfault plan"}
		if err := raw.SavePlan(m.sessionID, plan); err != nil {
			t.Fatal(err)
		}
		if err := mgr.SetMode(m.sessionID, collaboration.ModePlan); err != nil {
			t.Fatal(err)
		}
		m.mode, m.latestPlan, m.planHandoff = collaboration.ModePlan, &plan, &planHandoffState{}
		m.queued = []pendingInput{{text: "keep queued"}}
		return m, mgr, raw, faults, plan
	}

	t.Run("accept save", func(t *testing.T) {
		m, _, raw, faults, plan := setup(t)
		faults.failNextSave = true
		m.finishPlanApproval(planCompactFinishedMsg{sessionID: m.sessionID, planID: plan.ID})
		persisted, _ := raw.LoadPlan(plan.SessionID, plan.ID)
		if m.planHandoff == nil || m.latestPlan.Status != planning.ArtifactProposed || persisted.Status != planning.ArtifactProposed || len(m.queued) != 1 || m.running {
			t.Fatalf("failed acceptance was not rolled back: popup=%v latest=%+v persisted=%+v queue=%v running=%v", m.planHandoff != nil, m.latestPlan, persisted, m.queued, m.running)
		}
	})

	t.Run("mode update", func(t *testing.T) {
		m, _, raw, faults, plan := setup(t)
		faults.failMode = true
		m.finishPlanApproval(planCompactFinishedMsg{sessionID: m.sessionID, planID: plan.ID})
		persisted, _ := raw.LoadPlan(plan.SessionID, plan.ID)
		if m.planHandoff == nil || m.mode != collaboration.ModePlan || m.latestPlan.Status != planning.ArtifactProposed || persisted.Status != planning.ArtifactProposed || len(m.queued) != 1 {
			t.Fatalf("mode failure was not rolled back: popup=%v mode=%q latest=%+v persisted=%+v queue=%v", m.planHandoff != nil, m.mode, m.latestPlan, persisted, m.queued)
		}
	})

	t.Run("compact", func(t *testing.T) {
		m, _, raw, _, plan := setup(t)
		m.planCompacting = true
		m.finishPlanApproval(planCompactFinishedMsg{sessionID: m.sessionID, planID: plan.ID, err: errors.New("injected compact failure")})
		persisted, _ := raw.LoadPlan(plan.SessionID, plan.ID)
		if m.planCompacting || m.planHandoff == nil || m.mode != collaboration.ModePlan || persisted.Status != planning.ArtifactProposed || len(m.queued) != 1 || m.running {
			t.Fatalf("compact failure state: compacting=%v popup=%v mode=%q persisted=%+v queue=%v running=%v", m.planCompacting, m.planHandoff != nil, m.mode, persisted, m.queued, m.running)
		}
	})
}

func TestModelSelectionPersistsWithoutRebuildingActor(t *testing.T) {
	m, mgr, _ := newTestModelWithConfig(t, func(cfg *config.Config) {
		cfg.Model = &config.ModelConfig{Provider: "openai", Model: "gpt-4o", ContextWindow: 128000, ReasoningEffort: "low"}
		cfg.Models = []config.ModelConfig{
			{Provider: "openai", Model: "gpt-alt", ContextWindow: 32000, ReasoningEffort: "high"},
		}
	})
	actorBefore, ok := m.registry.Get(m.sessionID)
	if !ok {
		t.Fatal("session actor is not running")
	}
	m.applyModel(m.cfg.Models[0].Model)
	runtimeState, _ := mgr.Runtime(m.sessionID)
	if runtimeState.Model.Model != "gpt-alt" || effectiveEffort(m) != "high" {
		t.Fatalf("selected runtime=%+v effort=%q", runtimeState, effectiveEffort(m))
	}
	if actorAfter, ok := m.registry.Get(m.sessionID); !ok || actorAfter != actorBefore {
		t.Fatal("model selection rebuilt the session actor")
	}
	m.showStatus()
	if status := m.messages[len(m.messages)-1].content; !strings.Contains(status, "32000") || !strings.Contains(status, "Model: `gpt-alt [high]`") {
		t.Fatalf("status did not use complete selected model config: %q", status)
	}
	m.cfg.Models = append(m.cfg.Models, config.ModelConfig{Provider: "unsupported", Model: "broken"})
	m.applyModel("broken")
	runtimeState, _ = mgr.Runtime(m.sessionID)
	if runtimeState.Model.Model != "broken" {
		t.Fatalf("selected model was not persisted: %+v", runtimeState)
	}
	if err := mgr.UpdateMeta(m.sessionID, sessions.SessionMetaPatch{Runtime: &sessions.SessionRuntime{Mode: collaboration.ModeDefault, Model: sessions.ModelSelection{Model: "removed"}}}); err != nil {
		t.Fatal(err)
	}
	if err := normalizeSessionModel(mgr, m.cfg, m.sessionID); err != nil {
		t.Fatal(err)
	}
	runtimeState, _ = mgr.Runtime(m.sessionID)
	if runtimeState.Model.Model != "" {
		t.Fatalf("invalid saved model was not cleared: %+v", runtimeState)
	}
}

func TestModelSelectorGroupsEndpointsByExactModelName(t *testing.T) {
	m, _, _ := newTestModel(t)
	m.cfg.Model = &config.ModelConfig{Provider: "openai", BaseURL: "https://a.example/v1", Model: "shared"}
	m.cfg.Models = []config.ModelConfig{
		{Provider: "openai", BaseURL: "https://b.example/v1", Model: "shared"},
		{Provider: "openai", BaseURL: "https://a.example/v1", Model: "other"},
	}
	m.openModelSelector()
	if m.selector == nil || len(m.selector.items) != 2 {
		t.Fatalf("selector = %#v", m.selector)
	}
	if m.selector.items[0].value != "shared" || !strings.Contains(m.selector.items[0].description, "2 endpoints") {
		t.Fatalf("grouped item = %+v", m.selector.items[0])
	}
	if _, err := m.resolveModel("openai/shared"); err == nil {
		t.Fatal("provider/model syntax should not resolve")
	}
}

func TestEffortSelectionPersistsAndExplicitEffortSurvivesPlanMode(t *testing.T) {
	m, mgr, _ := newTestModel(t)
	m.applyEffort("default")
	runtimeState, _ := mgr.Runtime(m.sessionID)
	if runtimeState.Effort != "default" || effectiveEffort(m) != "default" {
		t.Fatalf("default effort runtime=%+v effective=%q", runtimeState, effectiveEffort(m))
	}
	m.applyEffort("high")
	m.mode = collaboration.ModePlan
	if got := effectiveEffort(m); got != providers.ReasoningEffortHigh {
		t.Fatalf("Plan Mode effort = %q, want explicit high", got)
	}
}

func TestNewSessionInheritsCurrentRuntimePolicy(t *testing.T) {
	m, mgr, _ := newTestModel(t)
	want := sessions.SessionRuntime{
		Mode:   collaboration.ModePlan,
		Model:  sessions.ModelSelection{Model: m.cfg.PrimaryModel().Model},
		Effort: "high",
	}
	if err := mgr.UpdateMeta(m.sessionID, sessions.SessionMetaPatch{Runtime: &want}); err != nil {
		t.Fatal(err)
	}
	if _, err := m.switchSession("inherited-runtime"); err != nil {
		t.Fatal(err)
	}
	got, err := mgr.Runtime("inherited-runtime")
	if err != nil {
		t.Fatal(err)
	}
	if got != want {
		t.Fatalf("new Session runtime = %+v, want %+v", got, want)
	}
}

func TestDiffIncludesUntrackedFileContents(t *testing.T) {
	m, _, _ := newTestModel(t)
	repo := t.TempDir()
	if out, err := exec.Command("git", "init", "-q", repo).CombinedOutput(); err != nil {
		t.Fatalf("git init: %v: %s", err, out)
	}
	if err := os.WriteFile(filepath.Join(repo, "new.txt"), []byte("untracked-payload-42\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	m.workdir = repo
	msg := m.showDiff()()
	m.Update(msg)
	if m.detail == nil || !strings.Contains(m.detail.view.GetContent(), "untracked-payload-42") {
		t.Fatalf("diff omitted untracked content: %#v", m.detail)
	}
}

func TestDiffCollectionIsBoundedAndReportsTruncation(t *testing.T) {
	repo := t.TempDir()
	if out, err := exec.Command("git", "init", "-q", repo).CombinedOutput(); err != nil {
		t.Fatalf("git init: %v: %s", err, out)
	}
	path := filepath.Join(repo, "large.txt")
	if err := os.WriteFile(path, []byte(strings.Repeat("x", 2<<20)), 0o644); err != nil {
		t.Fatal(err)
	}
	if out, err := exec.Command("git", "-C", repo, "add", "large.txt").CombinedOutput(); err != nil {
		t.Fatalf("git add: %v: %s", err, out)
	}
	content, err := collectWorkingTreeDiff(context.Background(), repo)
	if err != nil {
		t.Fatal(err)
	}
	if len(content) > 1<<20 {
		t.Fatalf("diff output size = %d, want <= 1 MiB", len(content))
	}
	if !strings.Contains(content, "output truncated at 1 MiB") {
		t.Fatalf("missing truncation marker, tail=%q", content[max(0, len(content)-100):])
	}
}

func TestShowDiffDefersWorkToTeaCommand(t *testing.T) {
	m, _, _ := newTestModel(t)
	m.workdir = t.TempDir()
	cmd := m.showDiff()
	if cmd == nil || m.detail != nil {
		t.Fatalf("showDiff executed synchronously: cmd=%v detail=%v", cmd != nil, m.detail != nil)
	}
	msg := cmd()
	if _, ok := msg.(diffLoadedMsg); !ok {
		t.Fatalf("showDiff message = %T", msg)
	}
}

func TestArchiveAndDeleteRequireConfirmation(t *testing.T) {
	m, mgr, _ := newTestModel(t)
	_, archiveID, err := mgr.CreateIsolated()
	if err != nil {
		t.Fatal(err)
	}
	m.handleSlash("/archive " + archiveID)
	if m.commandConfirm == nil {
		t.Fatal("archive did not request confirmation")
	}
	m.updateCommandConfirmation(tea.KeyPressMsg{Code: 'y', Text: "y"})
	meta, _ := mgr.GetStore().GetMeta(archiveID)
	if meta == nil || !meta.Archived {
		t.Fatalf("session not archived: %+v", meta)
	}
	_, deleteID, err := mgr.CreateIsolated()
	if err != nil {
		t.Fatal(err)
	}
	m.handleSlash("/delete " + deleteID)
	m.updateCommandConfirmation(tea.KeyPressMsg{Code: 'y', Text: "y"})
	if ok, _ := mgr.Exists(deleteID); ok {
		t.Fatal("session not deleted")
	}
}

func TestSelectorFilteringAndConditionalOtherField(t *testing.T) {
	selector := &selectorState{items: []selectorItem{{label: "alpha"}, {label: "beta"}}, query: "bet"}
	if got := selector.visibleIndexes(); len(got) != 1 || got[0] != 1 {
		t.Fatalf("filtered indexes = %v", got)
	}
	f, err := newFormState("plan-input", map[string]any{"variant": "plan_questions", "fields": []any{
		map[string]any{"name": "scope", "type": "select", "options": []any{map[string]any{"label": "A", "value": "A"}, map[string]any{"label": "Other", "value": "Other"}}},
		map[string]any{"name": "scope_other", "type": "text"},
	}}, 80)
	if err != nil {
		t.Fatal(err)
	}
	if got := f.visibleFieldIndexes(); len(got) != 1 {
		t.Fatalf("Other field initially visible: %v", got)
	}
	f.fields[0].option = 1
	if got := f.visibleFieldIndexes(); len(got) != 2 {
		t.Fatalf("Other field hidden after selection: %v", got)
	}
	m, _, _ := newTestModel(t)
	m.form = f
	m.submitForm()
	if m.form.err == "" || m.form.active != 0 {
		t.Fatalf("blank Other answer was accepted: active=%d err=%q", m.form.active, m.form.err)
	}
}

func TestQuestionFormUsesOnePageArrowNavigationAndEnterSubmission(t *testing.T) {
	f, err := newFormState("questions", map[string]any{"variant": "questions", "fields": []any{
		map[string]any{"name": "question_1", "label": "Question 1", "help": "Which scope?", "type": "select", "required": true, "options": []any{
			map[string]any{"label": "Small", "value": "Small"},
			map[string]any{"label": "Large", "value": "Large"},
			map[string]any{"label": "Write your own answer", "value": "Other"},
		}},
		map[string]any{"name": "question_1_other", "type": "text"},
		map[string]any{"name": "question_2", "label": "Question 2", "help": "Any constraints?", "type": "text", "required": true},
	}}, 80)
	if err != nil {
		t.Fatal(err)
	}
	m, _, _ := newTestModel(t)
	m.form = f
	view := terminalSafe(f.View(80))
	if !strings.Contains(view, "Question 1/2") || !strings.Contains(view, "Which scope?") || strings.Contains(view, "Any constraints?") {
		t.Fatalf("question page = %q", view)
	}

	_, _ = m.updateForm(tea.KeyPressMsg{Code: tea.KeyDown})
	_, _ = m.updateForm(tea.KeyPressMsg{Code: tea.KeyDown})
	f.editor.SetValue("Medium")
	_, _ = m.updateForm(tea.KeyPressMsg{Code: tea.KeyEnter})
	if f.active != 2 || f.fields[1].text != "Medium" {
		t.Fatalf("custom answer did not advance: active=%d other=%q", f.active, f.fields[1].text)
	}

	_, _ = m.updateForm(tea.KeyPressMsg{Code: tea.KeyLeft})
	if f.active != 0 || f.fields[1].text != "Medium" {
		t.Fatalf("left did not restore the first answer: active=%d other=%q", f.active, f.fields[1].text)
	}
	_, _ = m.updateForm(tea.KeyPressMsg{Code: tea.KeyRight})
	f.editor.SetValue("Keep compatibility")
	_, _ = m.updateForm(tea.KeyPressMsg{Code: tea.KeyEnter})
	if !f.submitting || f.err != "" {
		t.Fatalf("last Enter did not submit: submitting=%v err=%q", f.submitting, f.err)
	}
}

func TestGenericFormDoesNotApplyPlanningOtherConvention(t *testing.T) {
	f, err := newFormState("generic", map[string]any{"fields": []any{
		map[string]any{"name": "scope", "type": "select", "options": []any{map[string]any{"label": "A", "value": "A"}}},
		map[string]any{"name": "scope_other", "type": "text", "required": true},
	}}, 80)
	if err != nil {
		t.Fatal(err)
	}
	if got := f.visibleFieldIndexes(); !reflect.DeepEqual(got, []int{0, 1}) {
		t.Fatalf("generic form fields = %v, want both visible", got)
	}
	f.fields[1].text = "ordinary required value"
	m, _, _ := newTestModel(t)
	m.form = f
	_, _ = m.submitForm()
	if m.form == nil || !m.form.submitting || m.form.err != "" {
		t.Fatalf("generic foo_other submission failed: %+v", m.form)
	}
}

func TestRunFinishedExpiresStaleForm(t *testing.T) {
	m, _, _ := newTestModel(t)
	m.running = true
	m.currentRunID = "run-form"
	m.form = &formState{id: "stale"}
	m.handleActorEvent(events.NewEvent(events.KindRunFinished, "run-form"))
	if m.form != nil {
		t.Fatal("run completion left a stale form open")
	}
	if len(m.messages) == 0 || !strings.Contains(m.messages[len(m.messages)-1].content, "unfinished form expired") {
		t.Fatalf("missing stale form notice: %#v", m.messages)
	}
}

func TestCtrlJInsertsComposerNewline(t *testing.T) {
	m, _, _ := newTestModel(t)
	m.textarea.SetValue("first")
	m.textarea.CursorEnd()
	got, _ := m.updateKey(tea.KeyPressMsg{Code: 'j', Mod: tea.ModCtrl})
	if value := got.(*model).textarea.Value(); value != "first\n" {
		t.Fatalf("composer value = %q", value)
	}
}

func TestQueueRendersImageOnlyInput(t *testing.T) {
	m, _, _ := newTestModel(t)
	m.width = 80
	m.queued = []pendingInput{{images: []types.ImageContent{{Filename: "queued.png"}}}}
	if got := m.renderQueue(); !strings.Contains(got, "queued.png") {
		t.Fatalf("image-only queue = %q", got)
	}
}

func TestComposerGrowsForSoftWrappedContentWithoutFixedLimit(t *testing.T) {
	m, _, _ := newTestModel(t)
	m.width, m.height = 20, 80
	m.textarea.SetValue(strings.Repeat("界", 160))
	m.layout()
	if got := m.textarea.Height(); got <= 8 {
		t.Fatalf("soft-wrapped composer height = %d, want more than old 8-line cap", got)
	}

	m.textarea.SetValue("short")
	m.layout()
	if got := m.textarea.Height(); got != 1 {
		t.Fatalf("shrunk composer height = %d, want 1", got)
	}
}

func TestTimelinePreservesReceivedTextAndToolOrder(t *testing.T) {
	m, _, _ := newTestModel(t)
	runID := "run-order"
	m.handleActorEvent(events.NewEvent(events.KindTextMessageStart, runID))
	m.handleActorEvent(events.NewEvent(events.KindTextMessageContent, runID).WithPayload(events.TextMessageContentData{Content: "before"}))
	m.handleActorEvent(events.NewEvent(events.KindToolCallStart, runID).WithPayload(events.ToolCallStartData{ToolCallID: "tool-1", ToolName: "shell"}))
	m.handleActorEvent(events.NewEvent(events.KindToolCallArgs, runID).WithPayload(events.ToolCallArgsData{ToolCallID: "tool-1", PartialJSON: `{"cmd":"pwd"}`}))
	m.handleActorEvent(events.NewEvent(events.KindToolCallResult, runID).WithPayload(events.ToolCallResultData{ToolCallID: "tool-1", Success: true, Output: "/tmp"}))
	// The actor currently keeps one text message open across ReAct tool
	// boundaries. A later delta must still become a new visual timeline block.
	m.handleActorEvent(events.NewEvent(events.KindTextMessageContent, runID).WithPayload(events.TextMessageContentData{Content: "after"}))

	if len(m.messages) != 3 {
		t.Fatalf("timeline blocks = %#v", m.messages)
	}
	if m.messages[0].kind != blockAssistant || m.messages[0].content != "before" ||
		m.messages[1].kind != blockToolCall || m.messages[1].pending || !m.messages[1].success ||
		m.messages[2].kind != blockAssistant || m.messages[2].content != "after" {
		t.Fatalf("timeline order/state = %#v", m.messages)
	}
	if !strings.Contains(m.messages[1].toolArgs, `{"cmd":"pwd"}`) || !strings.Contains(m.messages[1].toolOutput, "/tmp") {
		t.Fatalf("tool args/output = %q / %q", m.messages[1].toolArgs, m.messages[1].toolOutput)
	}
}

func TestParallelToolResultsUpdateOriginalStartPositions(t *testing.T) {
	m, _, _ := newTestModel(t)
	runID := "run-parallel"
	for _, tc := range []events.ToolCallStartData{
		{ToolCallID: "first", ToolName: "first-tool"},
		{ToolCallID: "second", ToolName: "second-tool"},
	} {
		m.handleActorEvent(events.NewEvent(events.KindToolCallStart, runID).WithPayload(tc))
	}
	m.handleActorEvent(events.NewEvent(events.KindToolCallResult, runID).WithPayload(events.ToolCallResultData{ToolCallID: "second", Success: true, Output: "second result"}))
	m.handleActorEvent(events.NewEvent(events.KindToolCallResult, runID).WithPayload(events.ToolCallResultData{ToolCallID: "first", Success: true, Output: "first result"}))

	if len(m.messages) != 2 || m.messages[0].id != "first" || m.messages[1].id != "second" {
		t.Fatalf("tool start order changed after results: %#v", m.messages)
	}
	if m.messages[0].toolOutput != "first result" || m.messages[1].toolOutput != "second result" {
		t.Fatalf("tool results not updated in place: %#v", m.messages)
	}
}

func TestPlanHandoffRendersCompletePlanCardWithoutInternalScrolling(t *testing.T) {
	m, _, _ := newTestModel(t)
	m.width, m.height = 80, 24
	lines := make([]string, 30)
	for i := range lines {
		lines[i] = fmt.Sprintf("- plan line %02d", i+1)
	}
	markdown := "## Summary\n\n" + strings.Join(lines, "\n")
	evt := events.NewEvent(events.KindCustom, "run-plan").WithName(events.CustomPlanProposed).WithPayload(events.PlanProposedBody{
		PlanID: "plan-visible", Version: 2, Title: "Visible plan", Markdown: markdown,
	})
	before := len(m.messages)
	m.handleActorEvent(evt)
	if len(m.messages) != before+1 || m.messages[len(m.messages)-1].kind != blockPlan {
		t.Fatal("proposed plan was not added to the conversation timeline")
	}
	planCard := terminalSafe(m.renderBlock(&m.messages[len(m.messages)-1]))
	if !strings.Contains(planCard, "Visible plan") || !strings.Contains(planCard, "plan line 30") {
		t.Fatalf("plan card was truncated: %q", planCard)
	}
	m.handleActorEvent(evt)
	if len(m.messages) != before+1 {
		t.Fatal("duplicate proposal event inserted a second plan card")
	}
	m.planHandoff = &planHandoffState{}
	view := m.renderPlanHandoff()
	if !strings.Contains(view, "Approve") || !strings.Contains(view, "Request changes") {
		t.Fatalf("plan handoff missing actions: %q", view)
	}
	if !strings.Contains(view, interactiveStyle(true).Render("› Approve · implement")) ||
		!strings.Contains(view, interactiveStyle(false).Render("  Request changes")) {
		t.Fatalf("plan handoff selection styles are inconsistent: %q", view)
	}
	if strings.Contains(view, "Summary") {
		t.Fatalf("handoff still contains an internal plan viewport: %q", view)
	}
	for _, width := range []int{80, 40, 30, 20} {
		m.width, m.height = width, 24
		m.invalidateRendered()
		output := terminalSafe(m.View().Content)
		if !strings.Contains(output, "Approve") || !strings.Contains(output, "Request") || !strings.Contains(output, "changes") {
			t.Fatalf("%d-column layout hid plan actions: %q", width, output)
		}
		if lines := len(strings.Split(strings.TrimRight(output, "\n"), "\n")); lines > m.height {
			t.Fatalf("%d-column layout uses %d lines, terminal has %d", width, lines, m.height)
		}
	}
}

func TestPlanHandoffScrollsConversationWithoutChangingSelection(t *testing.T) {
	m, _, _ := newTestModel(t)
	_, _ = m.Update(tea.WindowSizeMsg{Width: 80, Height: 14})
	fillHistory(m, 24)
	m.latestPlan = &planning.Artifact{ID: "plan-scroll", Version: 1, Title: "Scrollable", Markdown: strings.Repeat("plan detail\n", 20)}
	m.ensurePlanCard(m.latestPlan)
	m.planHandoff = &planHandoffState{}
	_ = m.View()
	bottom := m.viewport.YOffset()
	if bottom == 0 {
		t.Fatal("expected scrollable plan history")
	}

	gotModel, _ := m.Update(tea.MouseWheelMsg{Button: tea.MouseWheelUp})
	m = gotModel.(*model)
	if m.viewport.YOffset() >= bottom || m.planHandoff.selected != 0 {
		t.Fatalf("wheel scroll: offset=%d bottom=%d selected=%d", m.viewport.YOffset(), bottom, m.planHandoff.selected)
	}

	beforePage := m.viewport.YOffset()
	gotModel, _ = m.Update(tea.KeyPressMsg{Code: tea.KeyPgUp})
	m = gotModel.(*model)
	if m.viewport.YOffset() >= beforePage || m.planHandoff.selected != 0 {
		t.Fatalf("page scroll: offset=%d before=%d selected=%d", m.viewport.YOffset(), beforePage, m.planHandoff.selected)
	}
}

func TestPlanHandoffOpensWhenProposalRunFinishesWithoutSpecialStopReason(t *testing.T) {
	m, _, _ := newTestModel(t)
	m.mode = collaboration.ModePlan
	m.running, m.currentRunID = true, "run-plan"
	m.handleActorEvent(events.NewEvent(events.KindCustom, "run-plan").WithName(events.CustomPlanProposed).WithPayload(events.PlanProposedBody{
		PlanID: "plan-run", Version: 1, Title: "Plan", Markdown: "## Summary\n\ncomplete plan",
	}))
	m.handleActorEvent(events.NewEvent(events.KindRunFinished, "run-plan").WithPayload(events.RunFinishedData{StopReason: "end_turn"}))
	if m.planHandoff == nil {
		t.Fatal("proposal run completion did not open approval actions")
	}
}

func TestCardPatchAndDismiss(t *testing.T) {
	m, _, _ := newTestModel(t)
	emit := events.NewEvent(events.KindCustom, "run").WithName(events.CustomCardEmitted).WithPayload(events.CardEmittedBody{
		CardID: "card-123456", Kind: "plan", Title: "Plan",
		Component: map[string]any{"steps": []any{map[string]any{"title": "one", "status": "pending"}}},
	})
	m.handleActorEvent(emit)
	update := events.NewEvent(events.KindCustom, "run").WithName(events.CustomCardUpdated).WithPayload(events.CardUpdatedBody{
		CardID: "card-123456", Patch: []interface{}{map[string]any{
			"op": "replace", "path": "/component/steps/0/status", "value": "done",
		}},
	})
	m.handleActorEvent(update)
	steps := m.cards["card-123456"].document["component"].(map[string]any)["steps"].([]any)
	if got := steps[0].(map[string]any)["status"]; got != "done" {
		t.Fatalf("patched status = %v", got)
	}
	dismiss := events.NewEvent(events.KindCustom, "run").WithName(events.CustomCardDismissed).WithPayload(events.CardDismissedBody{CardID: "card-123456"})
	m.handleActorEvent(dismiss)
	if !m.cards["card-123456"].dismissed || m.renderCard(m.cards["card-123456"]) != "" {
		t.Fatal("dismissed card remains visible")
	}
}

func TestCardPathRejectsTraversalAndEscapingSymlink(t *testing.T) {
	m, _, _ := newTestModel(t)
	root := t.TempDir()
	outside := t.TempDir()
	m.workdir = root
	if err := os.WriteFile(filepath.Join(outside, "secret.txt"), []byte("secret"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := m.safePath("../secret.txt"); err == nil {
		t.Fatal("expected traversal rejection")
	}
	if err := os.Symlink(filepath.Join(outside, "secret.txt"), filepath.Join(root, "link")); err != nil {
		t.Fatal(err)
	}
	if _, err := m.safePath("link"); err == nil {
		t.Fatal("expected escaping symlink rejection")
	}
}

func TestFormAllValueFamilies(t *testing.T) {
	m, _, _ := newTestModel(t)
	raw := map[string]any{"fields": []any{
		map[string]any{"name": "name", "type": "text", "required": true},
		map[string]any{"name": "count", "type": "number", "min": 1},
		map[string]any{"name": "enabled", "type": "boolean"},
		map[string]any{"name": "choice", "type": "select", "options": []any{map[string]any{"label": "A", "value": "a"}}},
		map[string]any{"name": "many", "type": "multiselect", "options": []any{map[string]any{"label": "A", "value": "a"}}},
		map[string]any{"name": "when", "type": "date"},
		map[string]any{"name": "items", "type": "list"},
		map[string]any{"name": "object", "type": "object"},
	}}
	f, err := newFormState("form", raw, 80)
	if err != nil {
		t.Fatal(err)
	}
	f.fields[0].text = "Friday"
	f.fields[1].text = "2"
	f.fields[2].boolean = true
	f.fields[4].multi[0] = true
	f.fields[5].text = "2026-09-10"
	f.fields[6].text = `[1,2]`
	f.fields[7].text = `{"ok":true}`
	values := map[string]any{}
	for i := range f.fields {
		value, err := m.formValue(&f.fields[i])
		if err != nil {
			t.Fatalf("field %d: %v", i, err)
		}
		values[f.fields[i].schema.Name] = value
	}
	if !reflect.DeepEqual(values["items"], []any{float64(1), float64(2)}) || values["choice"] != "a" {
		t.Fatalf("unexpected values: %#v", values)
	}
}

func TestEventLogRestoresTranscript(t *testing.T) {
	m, _, _ := newTestModel(t)
	store := m.sessMgr.GetStore().(sessions.EventStore)
	sink, err := store.OpenEventSink(context.Background(), m.sessionID)
	if err != nil {
		t.Fatal(err)
	}
	eventsToWrite := []events.Event{
		events.NewEvent(events.KindRunStarted, "turn"),
		events.NewEvent(events.KindCustom, "turn").WithName(events.CustomInputAccepted).WithPayload(events.InputAcceptedBody{TurnID: "turn", Text: "expanded instructions", DisplayText: "/writer hello"}),
		events.NewEvent(events.KindTextMessageStart, "turn"),
		events.NewEvent(events.KindTextMessageContent, "turn").WithPayload(events.TextMessageContentData{Content: "world"}),
		events.NewEvent(events.KindTextMessageEnd, "turn"),
		events.NewEvent(events.KindRunFinished, "turn").WithPayload(events.RunFinishedData{StopReason: "end_turn"}),
	}
	for _, evt := range eventsToWrite {
		if err := sink.Append(context.Background(), evt); err != nil {
			t.Fatal(err)
		}
	}
	if err := sink.Close(); err != nil {
		t.Fatal(err)
	}
	if err := m.loadTranscript(m.sessionID); err != nil {
		t.Fatal(err)
	}
	if len(m.messages) != 3 || m.messages[0].kind != blockUser || m.messages[0].content != "/writer hello" || m.messages[1].content != "world" || m.messages[2].content != "completed in <1s" {
		t.Fatalf("restored messages = %#v", m.messages)
	}
}

func TestJSONGridSourceReturnsDecodedRows(t *testing.T) {
	m, _, _ := newTestModel(t)
	root := t.TempDir()
	m.workdir = root
	path := filepath.Join(root, "rows.json")
	if err := os.WriteFile(path, []byte(`[{"name":"Ada"}]`), 0o600); err != nil {
		t.Fatal(err)
	}
	rows, err := m.loadGridSource(map[string]any{"path": path, "format": "json"})
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 1 || rows[0].(map[string]any)["name"] != "Ada" {
		t.Fatalf("rows = %#v", rows)
	}
}

func TestCardSourceLoadsOutsideView(t *testing.T) {
	m, _, _ := newTestModel(t)
	root := t.TempDir()
	m.workdir = root
	path := filepath.Join(root, "rows.json")
	if err := os.WriteFile(path, []byte(`[{"name":"Ada"}]`), 0o600); err != nil {
		t.Fatal(err)
	}
	evt := events.NewEvent(events.KindCustom, "run").WithName(events.CustomCardEmitted).WithPayload(events.CardEmittedBody{
		CardID: "table-1", Kind: "table", Component: map[string]any{
			"columns": []any{map[string]any{"key": "name", "label": "Name"}},
			"source":  map[string]any{"path": path, "format": "json"},
		},
	})
	cmd := m.handleActorEvent(evt)
	if cmd == nil || !m.cards["table-1"].sourceLoading {
		t.Fatal("source load was not scheduled")
	}
	if got := m.renderCard(m.cards["table-1"]); !strings.Contains(got, "loading source") {
		t.Fatalf("render before load = %q", got)
	}
	msg := cmd()
	m.Update(msg)
	if got := m.renderCard(m.cards["table-1"]); !strings.Contains(got, "Ada") {
		t.Fatalf("render after load = %q", got)
	}
}

func TestUntrustedPopupAndStreamingTextIsTerminalSafe(t *testing.T) {
	m, _, _ := newTestModel(t)
	osc := "\x1b]52;c;UE9XTkVE\x07"
	m.appendStreamContent(blockReasoning, "reason"+osc)
	m.appendBlock(chatBlock{kind: blockToolCall, id: "tool", toolName: "name" + osc, toolArgs: "args" + osc, toolArgsComplete: true, pending: true})
	got := joinConversationBlocks([]string{m.renderBlock(&m.messages[0]), m.renderBlock(&m.messages[1])})
	if strings.Contains(got, "UE9XTkVE") || strings.ContainsRune(got, '\a') {
		t.Fatalf("unsafe streaming output = %q", got)
	}
	f, err := newFormState("form", map[string]any{
		"title": osc, "description": osc,
		"fields": []any{map[string]any{"name": "field", "label": osc, "type": "text", "placeholder": osc, "default": osc}},
	}, 80)
	if err != nil {
		t.Fatal(err)
	}
	if got := f.View(80); strings.Contains(got, "UE9XTkVE") || strings.ContainsRune(got, '\a') {
		t.Fatalf("unsafe form output = %q", got)
	}
	if got := (&openConfirmation{target: osc}).View(80); strings.Contains(got, "UE9XTkVE") || strings.ContainsRune(got, '\a') {
		t.Fatalf("unsafe confirmation output = %q", got)
	}
}

func TestFormSubmissionFailureRetainsValuesForRetry(t *testing.T) {
	m, _, _ := newTestModel(t)
	f, err := newFormState("form-1", map[string]any{
		"fields": []any{map[string]any{"name": "answer", "type": "text"}},
	}, 80)
	if err != nil {
		t.Fatal(err)
	}
	f.editor.SetValue("keep me")
	m.form = f
	m.submitForm()
	if m.form == nil || !m.form.submitting {
		t.Fatal("form was removed before submission acknowledgement")
	}
	drop := events.NewEvent(events.KindCustom, "").WithName("status.inbox_dropped").WithPayload(map[string]any{
		"form_id": "form-1", "reason": "actor stopped",
	})
	m.handleActorEvent(drop)
	if m.form == nil || m.form.submitting || m.form.fields[0].text != "keep me" {
		t.Fatalf("form after failure = %#v", m.form)
	}
}

func TestNestedObjectFormValidation(t *testing.T) {
	m, _, _ := newTestModel(t)
	f, err := newFormState("form", map[string]any{"fields": []any{map[string]any{
		"name": "sample", "type": "object", "fields": []any{map[string]any{
			"name": "count", "type": "number", "required": true, "min": 1,
		}},
	}}}, 80)
	if err != nil {
		t.Fatal(err)
	}
	f.fields[0].text = `{"count":0}`
	if _, err := m.formValue(&f.fields[0]); err == nil || !strings.Contains(err.Error(), "at least") {
		t.Fatalf("nested validation error = %v", err)
	}
}

func TestMarkdownRendererIsReusedAtSameWidth(t *testing.T) {
	m, _, _ := newTestModel(t)
	m.width = 80
	_ = m.markdown("one")
	first := m.markdownRenderer
	_ = m.markdown("two")
	if first == nil || m.markdownRenderer != first {
		t.Fatal("markdown renderer was rebuilt without a width change")
	}
}
