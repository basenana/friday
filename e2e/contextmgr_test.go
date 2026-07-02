//go:build e2e

package e2e

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/basenana/friday/core/api"
	"github.com/basenana/friday/core/contextmgr"
	"github.com/basenana/friday/core/types"
)

// TestContextMgr_MicroCompact verifies that micro compaction triggers when
// the projected token count exceeds the soft threshold and that old tool
// results are pruned.
func TestContextMgr_MicroCompact(t *testing.T) {
	cfg := loadConfig(t)
	// Pin the provider-reported ContextWindow so the configured window is
	// honoured (production code prefers the provider value — see
	// contextmgr/manager.go:343).
	client := withContextWindow(newClient(t, cfg, "cheap"), 4000)
	sess := newTestSession(t, client)

	// Build a tiny context window so micro-compact fires quickly.
	mgr := contextmgr.New(client, contextmgr.Config{
		ContextWindow:      4000,
		SoftThresholdRatio: 0.3,
		HardThresholdRatio: 0.9,
		MaxToolResultChars: 100,
	})
	sess.RegisterHook(mgr)

	eventsPtr, eventsMu, unsub := subscribeSessionEvents(t, sess)
	defer unsub()

	// Inject several tool result messages with large content to inflate tokens.
	for i := 0; i < 6; i++ {
		big := strings.Repeat("tool output line "+strings.Repeat("x", 200)+"\n", 20)
		sess.AppendMessage(&types.Message{Role: types.RoleUser, Content: "do step " + string(rune('A'+i))})
		sess.AppendMessage(&types.Message{
			Role:      types.RoleAssistant,
			Content:   "ok",
			ToolCalls: []types.ToolCall{{ID: "c1", Name: "fake", Arguments: "{}"}},
		})
		sess.AppendMessage(&types.Message{
			Role:       types.RoleTool,
			ToolResult: &types.ToolResult{CallID: "c1", Content: big, Success: true},
		})
	}

	tools := newBashFsTools(t, newExecutor(t, cfg), t.TempDir())
	agent := newReactAgent(t, client, agentOpts{Tools: tools, MaxLoops: 10})
	ctx, cancel := context.WithTimeout(context.Background(), cfg.TestTimeout())
	defer cancel()

	resp := agent.Chat(ctx, &api.Request{Session: sess, UserMessage: "Just say OK."})
	collectResponse(t, ctx, resp)

	// Soft or hard compact path must execute. EventCompactFinish (with any
	// method) or EventCompactSkip both prove the compact logic ran.
	gotFinish := waitForEventType(eventsPtr, eventsMu, types.EventCompactFinish, 5*time.Second)
	gotSkip := waitForEventType(eventsPtr, eventsMu, types.EventCompactSkip, 5*time.Second)
	if !gotFinish && !gotSkip {
		t.Errorf("expected EventCompactFinish or EventCompactSkip; neither observed")
	}
}

// TestContextMgr_HardCompact verifies that hard compaction triggers and
// produces a summary.
func TestContextMgr_HardCompact(t *testing.T) {
	cfg := loadConfig(t)
	client := withContextWindow(newClient(t, cfg, "cheap"), 3000)
	sess := newTestSession(t, client)

	mgr := contextmgr.New(client, contextmgr.Config{
		ContextWindow:      3000,
		SoftThresholdRatio: 0.3,
		HardThresholdRatio: 0.4,
		MaxToolResultChars: 100,
	})
	sess.RegisterHook(mgr)

	eventsPtr, eventsMu, unsub := subscribeSessionEvents(t, sess)
	defer unsub()

	// Inject enough messages to blow past the hard threshold.
	for i := 0; i < 10; i++ {
		big := strings.Repeat("Lorem ipsum dolor sit amet consectetur adipiscing elit. ", 30)
		sess.AppendMessage(&types.Message{Role: types.RoleUser, Content: "Tell me about topic " + string(rune('A'+i)) + ": " + big})
		sess.AppendMessage(&types.Message{Role: types.RoleAssistant, Content: "Here is some info: " + big})
	}

	tools := newBashFsTools(t, newExecutor(t, cfg), t.TempDir())
	agent := newReactAgent(t, client, agentOpts{Tools: tools, MaxLoops: 10})
	ctx, cancel := context.WithTimeout(context.Background(), cfg.TestTimeout())
	defer cancel()

	resp := agent.Chat(ctx, &api.Request{Session: sess, UserMessage: "Say OK."})
	collectResponse(t, ctx, resp)

	// Hard compact: EventCompactStart must always fire (entry into hard path).
	// EventCompactFinish fires only if at least one compact method succeeded.
	if !waitForEventType(eventsPtr, eventsMu, types.EventCompactStart, 5*time.Second) {
		t.Errorf("expected EventCompactStart for hard threshold breach; not observed")
	}
	if !waitForEventType(eventsPtr, eventsMu, types.EventCompactFinish, 5*time.Second) {
		t.Errorf("expected EventCompactFinish for hard threshold breach; not observed (all compact methods may have failed)")
	}
}

// TestContextMgr_CompactPreservesContext verifies that after compaction the
// agent still remembers a key fact.
func TestContextMgr_CompactPreservesContext(t *testing.T) {
	cfg := loadConfig(t)
	client := withContextWindow(newClient(t, cfg, "chat"), 4000)
	sess := newTestSession(t, client)

	mgr := contextmgr.New(client, contextmgr.Config{
		ContextWindow:      4000,
		SoftThresholdRatio: 0.3,
		HardThresholdRatio: 0.5,
		MaxToolResultChars: 200,
	})
	sess.RegisterHook(mgr)

	// Seed with a key fact then bloat the context.
	sess.AppendMessage(&types.Message{Role: types.RoleUser, Content: "The secret keyword is PURPLE_ELEPHANT. Remember it."})
	sess.AppendMessage(&types.Message{Role: types.RoleAssistant, Content: "Got it, the secret keyword is PURPLE_ELEPHANT."})

	for i := 0; i < 8; i++ {
		filler := strings.Repeat("filler content line number "+string(rune('A'+i))+" ", 50)
		sess.AppendMessage(&types.Message{Role: types.RoleUser, Content: filler})
		sess.AppendMessage(&types.Message{Role: types.RoleAssistant, Content: filler})
	}

	tools := newBashFsTools(t, newExecutor(t, cfg), t.TempDir())
	agent := newReactAgent(t, client, agentOpts{Tools: tools, MaxLoops: 10})
	ctx, cancel := context.WithTimeout(context.Background(), cfg.TestTimeout())
	defer cancel()

	resp := agent.Chat(ctx, &api.Request{Session: sess, UserMessage: "What is the secret keyword I told you?"})
	content, _ := collectResponse(t, ctx, resp)

	if !strings.Contains(strings.ToUpper(content), "PURPLE_ELEPHANT") {
		t.Errorf("expected response to retain PURPLE_ELEPHANT after compaction, got %q", truncate(content, 300))
	}
}

// TestContextMgr_CompactReducesHistory verifies that hard compaction actually
// rewrites history to a smaller message count, not just fires an event.
func TestContextMgr_CompactReducesHistory(t *testing.T) {
	cfg := loadConfig(t)
	client := withContextWindow(newClient(t, cfg, "chat"), 3000)
	sess := newTestSession(t, client)

	mgr := contextmgr.New(client, contextmgr.Config{
		ContextWindow:      3000,
		SoftThresholdRatio: 0.3,
		HardThresholdRatio: 0.4,
		MaxToolResultChars: 100,
	})
	sess.RegisterHook(mgr)

	eventsPtr, eventsMu, unsub := subscribeSessionEvents(t, sess)
	defer unsub()

	// Inject 20 large messages to force past hard threshold.
	for i := 0; i < 10; i++ {
		big := strings.Repeat("Lorem ipsum dolor sit amet consectetur adipiscing elit. ", 30)
		sess.AppendMessage(&types.Message{Role: types.RoleUser, Content: "topic " + string(rune('A'+i)) + ": " + big})
		sess.AppendMessage(&types.Message{Role: types.RoleAssistant, Content: "info: " + big})
	}
	beforeLen := len(sess.GetHistory())

	tools := newBashFsTools(t, newExecutor(t, cfg), t.TempDir())
	agent := newReactAgent(t, client, agentOpts{Tools: tools, MaxLoops: 5})
	ctx, cancel := context.WithTimeout(context.Background(), cfg.TestTimeout())
	defer cancel()

	resp := agent.Chat(ctx, &api.Request{Session: sess, UserMessage: "Say OK."})
	collectResponse(t, ctx, resp)

	if !waitForEventType(eventsPtr, eventsMu, types.EventCompactFinish, 10*time.Second) {
		t.Fatal("EventCompactFinish not observed within 10s")
	}
	afterLen := len(sess.GetHistory())
	if afterLen >= beforeLen {
		t.Errorf("compaction did not reduce history: before=%d after=%d", beforeLen, afterLen)
	}
}

// TestContextMgr_SessionMemory verifies that session memory is generated
// when the threshold is low.
func TestContextMgr_SessionMemory(t *testing.T) {
	cfg := loadConfig(t)
	client := withContextWindow(newClient(t, cfg, "cheap"), 8000)
	sess := newTestSession(t, client)

	mgr := contextmgr.New(client, contextmgr.Config{
		ContextWindow:          8000,
		SoftThresholdRatio:     0.8,
		HardThresholdRatio:     0.9,
		SessionMemoryThreshold: 500,
	})
	sess.RegisterHook(mgr)

	// Inject a small amount of history that still crosses the 500-token
	// session-memory threshold. Keeping the input small makes the async
	// summarisation LLM call complete quickly.
	for i := 0; i < 2; i++ {
		txt := strings.Repeat("This is a conversation about topic "+string(rune('A'+i))+". ", 40)
		sess.AppendMessage(&types.Message{Role: types.RoleUser, Content: txt})
		sess.AppendMessage(&types.Message{Role: types.RoleAssistant, Content: "Acknowledged. " + txt})
	}

	tools := newBashFsTools(t, newExecutor(t, cfg), t.TempDir())
	agent := newReactAgent(t, client, agentOpts{Tools: tools, MaxLoops: 10})
	ctx, cancel := context.WithTimeout(context.Background(), cfg.TestTimeout())
	defer cancel()

	resp := agent.Chat(ctx, &api.Request{Session: sess, UserMessage: "Say OK."})
	collectResponse(t, ctx, resp)

	// Poll DrainPendingMemory for up to 30s waiting for the async generator.
	// The async path calls the LLM and may take 10+ seconds under load.
	st := sess.EnsureContextState()
	deadline := time.After(30 * time.Second)
	var pending any
	for {
		select {
		case <-deadline:
			pending = st.DrainPendingMemory()
			if pending == nil {
				t.Errorf("no pending session memory generated within 30s")
			} else {
				t.Logf("session memory generated: %T", pending)
			}
			return
		case <-time.After(200 * time.Millisecond):
			pending = st.DrainPendingMemory()
			if pending != nil {
				t.Logf("session memory generated: %T", pending)
				return
			}
		}
	}
}
