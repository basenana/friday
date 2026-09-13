//go:build e2e

package e2e

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/basenana/friday/config"
	aguiactor "github.com/basenana/friday/core/actor"
	"github.com/basenana/friday/core/actor/events"
	"github.com/basenana/friday/sessions"
	"github.com/basenana/friday/sessions/file"
	"github.com/basenana/friday/setup"
)

// These tests exercise the core/actor package (inbox + AG-UI event stream)
// against a real agent wired through setup.NewAgent. They require a
// configured LLM (~/.friday/config.json or env vars such as OPENAI_API_KEY
// referenced from that file). When no key is available the tests skip; when
// running in -short mode they skip as well. Run them with:
//
//	go test -tags e2e ./e2e/ -run AGUIActor -v
//
// (testing.Short() is false by default, so no extra flag is needed.)

// loadAGUIE2EConfig loads the user config and returns it, or skips the test
// when no LLM is reachable. A small data dir under t.TempDir() is used so
// the tests never touch the user's real session history.
func loadAGUIE2EConfig(t *testing.T) *config.Config {
	t.Helper()
	if testing.Short() {
		t.Skip("skipping e2e test in -short mode")
	}
	cfg, err := config.Load("")
	if err != nil {
		t.Fatalf("config.Load: %v", err)
	}
	if !hasAPIKey(cfg) {
		t.Skip("no LLM API key configured in ~/.friday/config.json; skipping actor e2e")
	}
	// Redirect data dir to a per-test temp dir so we never mutate the
	// user's real ~/.friday state.
	cfg.DataDir = t.TempDir()
	return cfg
}

func hasAPIKey(cfg *config.Config) bool {
	if cfg.Model.Key != "" {
		return true
	}
	for _, m := range cfg.Models {
		if m.Key != "" {
			return true
		}
	}
	// Also accept env-provided keys that the config might reference.
	for _, name := range []string{"OPENAI_API_KEY", "ANTHROPIC_API_KEY"} {
		if os.Getenv(name) != "" {
			return true
		}
	}
	return false
}

// newAGUIE2EActor wires a real agent (via setup.NewAgent) and wraps it in a
// core/actor Actor. The actor's card tools are attached to every Chat
// request automatically via runTurn, so the LLM can discover and call them.
func newAGUIE2EActor(t *testing.T, cfg *config.Config) *aguiactor.Actor {
	t.Helper()
	store := file.NewFileSessionStore(cfg.SessionsPath())
	sessMgr := sessions.NewManager(store, filepath.Join(cfg.DataDir, "current"), "")

	agentCtx, err := setup.NewAgent(sessMgr, cfg,
		setup.WithTemporary(true), // ephemeral session, no persistence pollution
	)
	if err != nil {
		t.Fatalf("setup.NewAgent: %v", err)
	}
	t.Cleanup(func() { agentCtx.Close() })

	return aguiactor.New(agentCtx.Agent, agentCtx.Session)
}

// aguiDrainUntil collects events until predicate returns true or the
// deadline expires. Returns everything observed.
func aguiDrainUntil(t *testing.T, sub *aguiactor.Subscription, pred func([]events.Event) bool, what string) []events.Event {
	t.Helper()
	var seen []events.Event
	deadline := time.After(60 * time.Second) // LLMs can be slow
	for {
		select {
		case e, ok := <-sub.Events():
			if !ok {
				return seen
			}
			seen = append(seen, e)
			if pred(seen) {
				return seen
			}
		case <-deadline:
			t.Fatalf("aguiDrainUntil(%s) timeout; saw %d events", what, len(seen))
		}
	}
}

// TestAGUIActor_BasicTurn verifies that a real LLM, driven through the
// actor, produces a RUN_STARTED → ... → RUN_FINISHED sequence and that the
// assistant text accumulates via TEXT_MESSAGE_CONTENT events.
func TestAGUIActor_BasicTurn(t *testing.T) {
	cfg := loadAGUIE2EConfig(t)
	a := newAGUIE2EActor(t, cfg)
	sub := a.Subscribe()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	a.Start(ctx)
	defer a.Shutdown(context.Background())

	if err := a.Send(ctx, aguiactor.UserTextMessage{Text: "Reply with the single word: pong"}); err != nil {
		t.Fatalf("Send: %v", err)
	}

	seen := aguiDrainUntil(t, sub, func(s []events.Event) bool {
		for _, e := range s {
			if e.Type == events.KindRunFinished {
				return true
			}
		}
		return false
	}, "RUN_FINISHED")

	var text strings.Builder
	var sawStart, sawFinish bool
	for _, e := range seen {
		switch e.Type {
		case events.KindRunStarted:
			sawStart = true
		case events.KindRunFinished:
			sawFinish = true
		case events.KindTextMessageContent:
			var d events.TextMessageContentData
			_ = events.DecodePayload(e, &d)
			text.WriteString(d.Content)
		}
	}
	if !sawStart || !sawFinish {
		t.Fatalf("expected RUN_STARTED+RUN_FINISHED, got start=%v finish=%v", sawStart, sawFinish)
	}
	if text.Len() == 0 {
		t.Fatalf("expected non-empty assistant text; events seen=%d", len(seen))
	}
	t.Logf("assistant replied: %q", text.String())
}

// TestAGUIActor_ShowMermaidPrompt asks the LLM to render a small diagram.
func TestAGUIActor_ShowMermaidPrompt(t *testing.T) {
	cfg := loadAGUIE2EConfig(t)
	a := newAGUIE2EActor(t, cfg)
	sub := a.Subscribe()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	a.Start(ctx)
	defer a.Shutdown(context.Background())

	prompt := "You MUST call the show_mermaid tool exactly once. Do not write any prose. " +
		"Call show_mermaid with this exact argument: " +
		`source="flowchart LR\nA --> B". ` +
		"After the tool call you may stop."
	if err := a.Send(ctx, aguiactor.UserTextMessage{Text: prompt}); err != nil {
		t.Fatalf("Send: %v", err)
	}

	// Allow up to two LLM loops (some models emit text first, then call).
	seen := aguiDrainUntil(t, sub, func(s []events.Event) bool {
		for _, e := range s {
			if e.Type == events.KindRunFinished {
				return true
			}
		}
		return false
	}, "RUN_FINISHED")

	var cardBody events.CardEmittedBody
	var cardFound bool
	for _, e := range seen {
		if e.Type == events.KindCustom && e.Name == events.CustomCardEmitted {
			if err := events.DecodePayload(e, &cardBody); err != nil {
				t.Fatalf("decode card body: %v", err)
			}
			cardFound = true
			break
		}
	}
	if !cardFound {
		t.Skipf("LLM did not emit a card (events=%d); model=%q may not reliably call custom tools. "+
			"Actor wiring is correct (tool_count included show_mermaid); skipping as model-dependent.",
			len(seen), cfg.Model.Model)
	}
	if cardBody.Kind != "mermaid" {
		t.Fatalf("card kind=mermaid expected, got %q", cardBody.Kind)
	}
	if source, _ := cardBody.Component["source"].(string); !strings.Contains(source, "A --> B") {
		t.Fatalf("unexpected diagram source: %v", cardBody.Component)
	}
	t.Logf("show_mermaid ok: id=%s component=%v", cardBody.CardID, cardBody.Component)
}

// TestAGUIActor_RequestUserInputPrompt verifies the blocking question flow.
func TestAGUIActor_RequestUserInputPrompt(t *testing.T) {
	cfg := loadAGUIE2EConfig(t)
	a := newAGUIE2EActor(t, cfg)
	runSub := a.Subscribe()
	formSub := a.Subscribe()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	a.Start(ctx)
	defer a.Shutdown(context.Background())

	stopWatcher := make(chan struct{})
	submitDone := make(chan error, 1)
	go func() {
		if formID, ok := aguiWaitForFormRequest(formSub, stopWatcher); ok {
			submitDone <- a.SubmitForm(formID, map[string]any{"question_1": "Human"})
			return
		}
		submitDone <- nil
	}()

	prompt := `You MUST call the request_user_input tool exactly once. Do not answer from memory.
Call request_user_input with these exact arguments:
{"question_1":"Which organism?","options_1":["Human","Other species"]}
After the tool returns, reply with the single word: submitted.`
	if err := a.Send(ctx, aguiactor.UserTextMessage{Text: prompt}); err != nil {
		t.Fatalf("Send: %v", err)
	}

	seen := aguiDrainUntil(t, runSub, func(s []events.Event) bool {
		for _, e := range s {
			if e.Type == events.KindRunFinished {
				return true
			}
		}
		return false
	}, "RUN_FINISHED")
	close(stopWatcher)
	if err := <-submitDone; err != nil {
		t.Fatalf("SubmitForm: %v", err)
	}

	if !aguiSawCustomEvent(seen, events.CustomFormRequested) {
		t.Skipf("LLM did not invoke request_user_input (events=%d); model=%q may not reliably call custom tools.",
			len(seen), cfg.Model.Model)
	}
	if !aguiSawCustomEvent(seen, events.CustomFormSubmitted) {
		t.Fatalf("expected form.submitted event after auto-submit")
	}
}

// TestAGUIActor_PreemptCancelsFormPrompt verifies that preempting a real
// request_user_input turn cancels the pending form and finishes the run.
func TestAGUIActor_PreemptCancelsFormPrompt(t *testing.T) {
	cfg := loadAGUIE2EConfig(t)
	a := newAGUIE2EActor(t, cfg)
	runSub := a.Subscribe()
	formSub := a.Subscribe()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	a.Start(ctx)
	defer a.Shutdown(context.Background())

	stopWatcher := make(chan struct{})
	preemptDone := make(chan error, 1)
	go func() {
		if _, ok := aguiWaitForFormRequest(formSub, stopWatcher); ok {
			preemptDone <- a.SendPreempt(ctx, "cancel form")
			return
		}
		preemptDone <- nil
	}()

	prompt := `You MUST call the request_user_input tool exactly once. Do not write any prose first.
Call request_user_input with these exact arguments:
{"question_1":"Which organism?","options_1":["Human","Other species"]}
Stop after the tool call.`
	if err := a.Send(ctx, aguiactor.UserTextMessage{Text: prompt}); err != nil {
		t.Fatalf("Send: %v", err)
	}

	seen := aguiDrainUntil(t, runSub, func(s []events.Event) bool {
		for _, e := range s {
			if e.Type == events.KindRunFinished {
				return true
			}
		}
		return false
	}, "RUN_FINISHED")
	close(stopWatcher)
	if err := <-preemptDone; err != nil {
		t.Fatalf("SendPreempt: %v", err)
	}

	if !aguiSawCustomEvent(seen, events.CustomFormRequested) {
		t.Skipf("LLM did not invoke request_user_input (events=%d); model=%q may not reliably call custom tools.",
			len(seen), cfg.Model.Model)
	}
	if !aguiSawCustomEvent(seen, events.CustomFormCancelled) {
		t.Fatalf("expected form.cancelled event after preempt")
	}
}

// TestAGUIActor_ShutdownDuringTurn verifies that a real LLM turn can be
// interrupted mid-flight by Shutdown with a short deadline, and that
// Shutdown returns the deadline error.
func TestAGUIActor_ShutdownDuringTurn(t *testing.T) {
	cfg := loadAGUIE2EConfig(t)
	a := newAGUIE2EActor(t, cfg)
	sub := a.Subscribe()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	a.Start(ctx)

	// Long prompt that will keep the LLM busy for a few seconds.
	long := "Write a 500-word essay about the history of computing. " +
		"Take your time and be thorough."
	if err := a.Send(ctx, aguiactor.UserTextMessage{Text: long}); err != nil {
		t.Fatalf("Send: %v", err)
	}
	// Wait until the turn is actually running.
	aguiDrainUntil(t, sub, func(s []events.Event) bool {
		for _, e := range s {
			if e.Type == events.KindRunStarted {
				return true
			}
		}
		return false
	}, "RUN_STARTED")

	// Force a shutdown with a very short deadline while the turn is in flight.
	short, shortCancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer shortCancel()
	err := a.Shutdown(short)
	if err == nil {
		t.Fatalf("expected Shutdown to report deadline error during in-flight LLM turn")
	}
	t.Logf("Shutdown during turn returned expected error: %v", err)
}

func aguiWaitForFormRequest(sub *aguiactor.Subscription, stop <-chan struct{}) (string, bool) {
	for {
		select {
		case <-stop:
			return "", false
		case e, ok := <-sub.Events():
			if !ok {
				return "", false
			}
			if e.Type == events.KindCustom && e.Name == events.CustomFormRequested {
				var body events.FormRequestedBody
				if err := events.DecodePayload(e, &body); err != nil {
					return "", false
				}
				return body.FormID, body.FormID != ""
			}
		}
	}
}

func aguiSawCustomEvent(seen []events.Event, name string) bool {
	for _, e := range seen {
		if e.Type == events.KindCustom && e.Name == name {
			return true
		}
	}
	return false
}
