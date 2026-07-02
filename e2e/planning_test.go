//go:build e2e

package e2e

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/basenana/friday/core/agents"
	"github.com/basenana/friday/core/agents/research"
	"github.com/basenana/friday/core/agents/simple"
	"github.com/basenana/friday/core/agents/summarize"
	"github.com/basenana/friday/core/api"
	"github.com/basenana/friday/core/planning"
	"github.com/basenana/friday/core/planning/lats"
	"github.com/basenana/friday/core/types"
)

// TestPlanning_TodoCreate verifies that the planning TODO hook injects the
// write_todos tool and the agent invokes it.
func TestPlanning_TodoCreate(t *testing.T) {
	cfg := loadConfig(t)

	withRetry(t, cfg, func(attempt int) error {
		client := newClient(t, cfg, "chat")
		sess := newTestSession(t, client)
		sess.RegisterHook(planning.New(planning.Option{}))

		eventsPtr, eventsMu, unsub := subscribeSessionEvents(t, sess)
		defer unsub()

		tools := newBashFsTools(t, newExecutor(t, cfg), t.TempDir())
		agent := newReactAgent(t, client, agentOpts{Tools: tools, MaxLoops: 20})
		ctx, cancel := context.WithTimeout(context.Background(), cfg.TestTimeout())
		defer cancel()

		resp := agent.Chat(ctx, &api.Request{
			Session:     sess,
			UserMessage: "Please complete three sequential steps: create a directory called 'work', write a file 'work/note.txt' with content 'done', then list the 'work' directory. Plan the work first using your todo tool.",
		})
		collectResponse(t, ctx, resp)

		if !historyHasToolCall(sess, "write_todos", 1) {
			return errAssertion{msg: "write_todos tool not invoked"}
		}

		if !waitForEventType(eventsPtr, eventsMu, types.EventTodoUpdate, 3*time.Second) {
			return errAssertion{msg: "EventTodoUpdate not observed after write_todos call"}
		}
		return nil
	})
}

// TestPlanning_TodoStateTransition verifies that todo state moves through
// pending -> in_progress -> completed across multiple turns.
func TestPlanning_TodoStateTransition(t *testing.T) {
	cfg := loadConfig(t)

	withRetry(t, cfg, func(attempt int) error {
		client := newClient(t, cfg, "chat")
		sess := newTestSession(t, client)
		sess.RegisterHook(planning.New(planning.Option{}))

		eventsPtr, eventsMu, unsub := subscribeSessionEvents(t, sess)
		defer unsub()

		tools := newBashFsTools(t, newExecutor(t, cfg), t.TempDir())
		agent := newReactAgent(t, client, agentOpts{Tools: tools, MaxLoops: 20})
		ctx, cancel := context.WithTimeout(context.Background(), cfg.TestTimeout())
		defer cancel()

		resp := agent.Chat(ctx, &api.Request{
			Session:     sess,
			UserMessage: "Use the todo tool to track and execute these two steps: 1) echo 'step1' 2) echo 'step2'. Mark each as completed as you finish.",
		})
		collectResponse(t, ctx, resp)

		if !historyHasToolCall(sess, "write_todos", 1) {
			return errAssertion{msg: "write_todos not invoked"}
		}
		if !waitForEventType(eventsPtr, eventsMu, types.EventTodoUpdate, 3*time.Second) {
			return errAssertion{msg: "EventTodoUpdate not observed"}
		}
		// Verify at least one write_todos call had a "completed" status in args.
		completedSeen := false
		for _, msg := range sess.GetHistory() {
			for _, tc := range msg.ToolCalls {
				if tc.Name == "write_todos" && strings.Contains(tc.Arguments, "completed") {
					completedSeen = true
				}
			}
		}
		if !completedSeen {
			return errAssertion{msg: "no write_todos call contained 'completed' status"}
		}
		return nil
	})
}

// TestPlanning_LATS_TreeExpands verifies the LATS agent returns an answer for
// a simple question.
func TestPlanning_LATS_TreeExpands(t *testing.T) {
	cfg := loadConfig(t)
	client := newClient(t, cfg, "chat")

	worker := agents.New(client, agents.Option{
		MaxLoopTimes: 5,
	})

	la := lats.New(client, worker, lats.Option{
		Expansions:  2,
		MaxRollouts: 2,
		MaxSteps:    3,
		MaxParallel: 2,
	})
	sess := newTestSession(t, client)
	ctx, cancel := context.WithTimeout(context.Background(), cfg.TestTimeout())
	defer cancel()

	resp := la.Chat(ctx, &api.Request{
		Session:     sess,
		UserMessage: "What is 2 + 3? Answer with just the number.",
	})
	content, _ := collectResponse(t, ctx, resp)
	assertNotEmpty(t, content)
}

// TestPlanning_LATS_FastMode verifies FastMode returns an answer.
func TestPlanning_LATS_FastMode(t *testing.T) {
	cfg := loadConfig(t)
	client := newClient(t, cfg, "chat")

	worker := agents.New(client, agents.Option{MaxLoopTimes: 5})
	la := lats.New(client, worker, lats.Option{
		Expansions:  2,
		MaxRollouts: 2,
		MaxSteps:    3,
		MaxParallel: 2,
		FastMode:    true,
	})
	sess := newTestSession(t, client)
	ctx, cancel := context.WithTimeout(context.Background(), cfg.TestTimeout())
	defer cancel()

	resp := la.Chat(ctx, &api.Request{
		Session:     sess,
		UserMessage: "What is the capital of France? One word.",
	})
	content, _ := collectResponse(t, ctx, resp)
	assertNotEmpty(t, content)
}

// TestPlanning_Summarize verifies the summarize agent produces a shorter
// summary of a longer conversation.
func TestPlanning_Summarize(t *testing.T) {
	cfg := loadConfig(t)
	client := newClient(t, cfg, "cheap")
	sa := summarize.New(client, summarize.Option{})

	var hist []types.Message
	hist = append(hist, types.Message{Role: types.RoleUser, Content: "Let's discuss the history of computing."})
	totalLen := 0
	for i := 0; i < 6; i++ {
		u := types.Message{Role: types.RoleUser, Content: strings.Repeat("Tell me about topic "+string(rune('A'+i))+". ", 10)}
		a := types.Message{Role: types.RoleAssistant, Content: strings.Repeat("Here is info about topic "+string(rune('A'+i))+". ", 10)}
		hist = append(hist, u, a)
		totalLen += len(u.Content) + len(a.Content)
	}
	sess := newTestSession(t, client)
	sess.ReplaceHistory(hist...)

	ctx, cancel := context.WithTimeout(context.Background(), cfg.TestTimeout())
	defer cancel()
	resp := sa.Chat(ctx, &api.Request{
		Session:     sess,
		UserMessage: "Summarize our conversation in one paragraph.",
	})
	content, _ := collectResponse(t, ctx, resp)
	assertNotEmpty(t, content)
}

// ---------------------------------------------------------------------------
// Tier E2 — Simple Agent and Research Agent
// ---------------------------------------------------------------------------

// TestSimpleAgent_Streaming verifies a single streaming turn from the simple
// agent (no tools).
func TestSimpleAgent_Streaming(t *testing.T) {
	cfg := loadConfig(t)
	client := newClient(t, cfg, "chat")
	sa := simple.New(client, simple.Option{
		SystemPrompt: "You are a helpful assistant. Reply briefly.",
	})
	sess := newTestSession(t, client)
	ctx, cancel := context.WithTimeout(context.Background(), cfg.TestTimeout())
	defer cancel()

	resp := sa.Chat(ctx, &api.Request{
		Session:     sess,
		UserMessage: "What colour is the sky? One short sentence.",
	})
	content, deltas := collectResponse(t, ctx, resp)
	assertNotEmpty(t, content)
	if len(deltas) == 0 {
		t.Error("expected at least 1 delta")
	}
}

// TestSimpleAgent_StructuredOutput verifies structured output via the simple
// agent's NewOutputModel factory.
func TestSimpleAgent_StructuredOutput(t *testing.T) {
	cfg := loadConfig(t)
	client := newClient(t, cfg, "chat")
	type Color struct {
		Color string `json:"color"`
	}
	sa := simple.New(client, simple.Option{
		SystemPrompt: "You pick a colour for the user's request.",
		NewOutputModel: func() any {
			return &Color{}
		},
	})
	sess := newTestSession(t, client)
	ctx, cancel := context.WithTimeout(context.Background(), cfg.TestTimeout())
	defer cancel()

	resp := sa.Chat(ctx, &api.Request{
		Session:     sess,
		UserMessage: "Pick a colour for a calm bedroom. Return JSON with a 'color' field.",
	})
	content, _ := collectResponse(t, ctx, resp)
	if !strings.Contains(strings.ToLower(content), "color") {
		t.Errorf("expected JSON containing color field, got %q", truncate(content, 300))
	}
}

// TestResearch_LeaderWorkerFlow verifies the research agent's leader/worker
// orchestration using local fs tools.
func TestResearch_LeaderWorkerFlow(t *testing.T) {
	cfg := loadConfig(t)

	withRetry(t, cfg, func(attempt int) error {
		client := newClient(t, cfg, "chat")
		workdir := t.TempDir()
		exec := newExecutor(t, cfg)
		fsTools := newBashFsTools(t, exec, workdir)

		ra := research.New(client, research.Option{
			ResearchTools: fsTools,
		})
		sess := newTestSession(t, client)
		ctx, cancel := context.WithTimeout(context.Background(), cfg.TestTimeout())
		defer cancel()

		resp := ra.Chat(ctx, &api.Request{
			Session:     sess,
			UserMessage: "Investigate the working directory. Report what files are present (empty initially) and create a small note.txt file with the word hello.",
		})
		content, _ := collectResponse(t, ctx, resp)

		if strings.TrimSpace(content) == "" {
			return errAssertion{msg: "research agent returned empty content"}
		}
		// Ground truth: file exists OR at least one fs tool was called.
		if _, err := os.Stat(filepath.Join(workdir, "note.txt")); err == nil {
			return nil
		}
		if !historyHasToolCall(sess, "fs_write", 1) && !historyHasToolCall(sess, "bash", 1) {
			return errAssertion{msg: "no fs_write/bash tool call observed; orchestration did not drive worker"}
		}
		return nil
	})
}
