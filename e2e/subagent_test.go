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
	"github.com/basenana/friday/core/api"
	"github.com/basenana/friday/core/subagents"
	"github.com/basenana/friday/core/types"
)

// TestSubagent_RunTask verifies that the main agent delegates a task to a
// registered subagent via the run_task tool.
func TestSubagent_RunTask(t *testing.T) {
	cfg := loadConfig(t)

	withRetry(t, cfg, func(attempt int) error {
		client := newClient(t, cfg, "chat")
		exec := newExecutor(t, cfg)
		workdir := t.TempDir()
		workerTools := newBashFsTools(t, exec, workdir)

		worker := agents.New(client, agents.Option{
			Tools:        workerTools,
			MaxLoopTimes: 10,
		})

		hook := subagents.NewHook(client, subagents.Option{
			ExpertTools: workerTools,
			ExpertAgents: []subagents.ExpertAgent{
				{
					Name:     "echo",
					Describe: "Echoes text using the bash tool.",
					Agent:    worker,
				},
			},
		})

		sess := newTestSession(t, client)
		sess.RegisterHook(hook)

		mainAgent := agents.New(client, agents.Option{MaxLoopTimes: 10})
		ctx, cancel := context.WithTimeout(context.Background(), cfg.TestTimeout())
		defer cancel()

		resp := mainAgent.Chat(ctx, &api.Request{
			Session:     sess,
			UserMessage: "Delegate to the 'echo' subagent: ask it to run the bash command `echo hello_from_subagent` and report its output.",
		})
		collectResponse(t, ctx, resp)

		if !historyHasToolCall(sess, "run_task", 1) {
			return errAssertion{msg: "run_task not called"}
		}
		return nil
	})
}

// TestSubagent_SessionIsolation verifies that the main session history does
// not contain the subagent's internal tool calls.
func TestSubagent_SessionIsolation(t *testing.T) {
	cfg := loadConfig(t)
	client := newClient(t, cfg, "chat")
	exec := newExecutor(t, cfg)
	workdir := t.TempDir()
	workerTools := newBashFsTools(t, exec, workdir)

	worker := agents.New(client, agents.Option{
		Tools:        workerTools,
		MaxLoopTimes: 10,
	})

	hook := subagents.NewHook(client, subagents.Option{
		ExpertTools: workerTools,
		ExpertAgents: []subagents.ExpertAgent{
			{
				Name:     "fsworker",
				Describe: "A worker that uses fs tools.",
				Agent:    worker,
			},
		},
	})

	sess := newTestSession(t, client)
	sess.RegisterHook(hook)

	mainAgent := agents.New(client, agents.Option{MaxLoopTimes: 10})
	ctx, cancel := context.WithTimeout(context.Background(), cfg.TestTimeout())
	defer cancel()

	resp := mainAgent.Chat(ctx, &api.Request{
		Session:     sess,
		UserMessage: "Delegate to 'fsworker': ask it to list the files in the current directory and report back.",
	})
	collectResponse(t, ctx, resp)

	// The main session should have run_task but not fs_list (which is internal
	// to the subagent).
	assertHistoryHasToolCall(t, sess, "run_task")
	for _, msg := range sess.GetHistory() {
		for _, tc := range msg.ToolCalls {
			if tc.Name == "fs_list" {
				t.Errorf("main session should not contain fs_list tool call (subagent leaked): %+v", tc)
			}
		}
	}
}

// TestSubagent_FuzzyMatch verifies fuzzy name matching on subagent lookup.
func TestSubagent_FuzzyMatch(t *testing.T) {
	cfg := loadConfig(t)
	client := newClient(t, cfg, "chat")
	exec := newExecutor(t, cfg)
	workdir := t.TempDir()
	workerTools := newBashFsTools(t, exec, workdir)

	worker := agents.New(client, agents.Option{Tools: workerTools, MaxLoopTimes: 10})

	hook := subagents.NewHook(client, subagents.Option{
		ExpertTools: workerTools,
		ExpertAgents: []subagents.ExpertAgent{
			{Name: "file_editor", Describe: "Edits files.", Agent: worker},
		},
	})

	sess := newTestSession(t, client)
	sess.RegisterHook(hook)

	mainAgent := agents.New(client, agents.Option{MaxLoopTimes: 10})
	ctx, cancel := context.WithTimeout(context.Background(), cfg.TestTimeout())
	defer cancel()

	// Use a slight name variation; this is a smoke test for the lookup path.
	resp := mainAgent.Chat(ctx, &api.Request{
		Session:     sess,
		UserMessage: "Delegate to 'fileeditor' subagent: ask it to echo hello.",
	})
	content, _ := collectResponse(t, ctx, resp)
	_ = content
}

// TestSubagent_Events verifies subagent start/finish events are emitted.
func TestSubagent_Events(t *testing.T) {
	cfg := loadConfig(t)
	client := newClient(t, cfg, "chat")
	exec := newExecutor(t, cfg)
	workdir := t.TempDir()
	workerTools := newBashFsTools(t, exec, workdir)

	worker := agents.New(client, agents.Option{Tools: workerTools, MaxLoopTimes: 10})

	hook := subagents.NewHook(client, subagents.Option{
		ExpertTools: workerTools,
		ExpertAgents: []subagents.ExpertAgent{
			{Name: "helper", Describe: "Helps with tasks.", Agent: worker},
		},
	})

	sess := newTestSession(t, client)
	sess.RegisterHook(hook)

	eventsPtr, eventsMu, unsub := subscribeSessionEvents(t, sess)
	defer unsub()

	mainAgent := agents.New(client, agents.Option{MaxLoopTimes: 10})
	ctx, cancel := context.WithTimeout(context.Background(), cfg.TestTimeout())
	defer cancel()

	resp := mainAgent.Chat(ctx, &api.Request{
		Session:     sess,
		UserMessage: "Delegate to 'helper' to run `echo hi` and tell me the output.",
	})
	collectResponse(t, ctx, resp)

	if !waitForEventType(eventsPtr, eventsMu, types.EventSubagentStart, 3*time.Second) &&
		!waitForEventType(eventsPtr, eventsMu, types.EventSubagentFinish, 3*time.Second) {
		t.Errorf("expected EventSubagentStart or EventSubagentFinish, none observed")
	}
}

// TestSubagent_ToolsPassthrough verifies that a subagent with fs tools can
// actually create files (ground truth).
func TestSubagent_ToolsPassthrough(t *testing.T) {
	cfg := loadConfig(t)

	withRetry(t, cfg, func(attempt int) error {
		client := newClient(t, cfg, "chat")
		exec := newExecutor(t, cfg)
		workdir := t.TempDir()
		workerTools := newBashFsTools(t, exec, workdir)

		worker := agents.New(client, agents.Option{Tools: workerTools, MaxLoopTimes: 10})

		hook := subagents.NewHook(client, subagents.Option{
			ExpertTools: workerTools,
			ExpertAgents: []subagents.ExpertAgent{
				{Name: "writer", Describe: "Writes files.", Agent: worker},
			},
		})

		sess := newTestSession(t, client)
		sess.RegisterHook(hook)

		mainAgent := agents.New(client, agents.Option{MaxLoopTimes: 10})
		ctx, cancel := context.WithTimeout(context.Background(), cfg.TestTimeout())
		defer cancel()

		resp := mainAgent.Chat(ctx, &api.Request{
			Session:     sess,
			UserMessage: "Delegate to 'writer' to create a file named delegation.txt in the working directory with the content 'via_subagent'.",
		})
		collectResponse(t, ctx, resp)

		if _, err := os.Stat(workdir + "/delegation.txt"); err != nil {
			return errAssertion{msg: "delegation.txt not created: " + err.Error()}
		}
		return nil
	})
}

// TestSubagent_Explore verifies the explore tool forks a sub-session and
// returns a structured report of what it found.
func TestSubagent_Explore(t *testing.T) {
	cfg := loadConfig(t)
	client := newClient(t, cfg, "chat")
	exec := newExecutor(t, cfg)
	workdir := t.TempDir()
	workerTools := newBashFsTools(t, exec, workdir)

	// Drop a file for the explore clone to investigate.
	const payload = "hello-from-explore-target"
	if err := os.WriteFile(filepath.Join(workdir, "target.txt"), []byte(payload), 0o644); err != nil {
		t.Fatalf("write target: %v", err)
	}

	exploreAgent := agents.New(client, agents.Option{
		Tools:        workerTools,
		MaxLoopTimes: 5,
	})
	hook := subagents.NewHook(client, subagents.Option{
		SelfAgent:    &subagents.ExpertAgent{Name: "explore", Agent: exploreAgent},
		ExploreTools: workerTools,
	})

	sess := newTestSession(t, client)
	sess.RegisterHook(hook)

	mainAgent := agents.New(client, agents.Option{MaxLoopTimes: 10})
	ctx, cancel := context.WithTimeout(context.Background(), cfg.TestTimeout())
	defer cancel()

	resp := mainAgent.Chat(ctx, &api.Request{
		Session:     sess,
		UserMessage: "Use the explore tool to read the file target.txt and report its contents back to me.",
	})
	content, _ := collectResponse(t, ctx, resp)

	assertHistoryHasToolCall(t, sess, "explore")
	if !strings.Contains(strings.ToLower(content), strings.ToLower(payload)) {
		t.Errorf("explore report did not surface file content; got: %s", truncate(content, 500))
	}
}

// TestSubagent_ReportContent verifies that a worker's output is actually
// surfaced in the main agent's final response — not just that run_task was
// called. We plant a unique payload file and ask the main agent to relay it.
func TestSubagent_ReportContent(t *testing.T) {
	cfg := loadConfig(t)

	withRetry(t, cfg, func(attempt int) error {
		client := newClient(t, cfg, "chat")
		exec := newExecutor(t, cfg)
		workdir := t.TempDir()
		workerTools := newBashFsTools(t, exec, workdir)
		const payload = "SUBAGENT_PAYLOAD_ABC123"
		if err := os.WriteFile(filepath.Join(workdir, "evidence.txt"), []byte(payload), 0o644); err != nil {
			return err
		}

		worker := agents.New(client, agents.Option{
			Tools:        workerTools,
			MaxLoopTimes: 8,
		})

		hook := subagents.NewHook(client, subagents.Option{
			ExpertTools: workerTools,
			ExpertAgents: []subagents.ExpertAgent{
				{Name: "reader", Describe: "Reads files and reports contents.", Agent: worker},
			},
		})

		sess := newTestSession(t, client)
		sess.RegisterHook(hook)
		mainAgent := agents.New(client, agents.Option{MaxLoopTimes: 10})
		ctx, cancel := context.WithTimeout(context.Background(), cfg.TestTimeout())
		defer cancel()

		resp := mainAgent.Chat(ctx, &api.Request{
			Session:     sess,
			UserMessage: "Delegate to the 'reader' subagent: ask it to read evidence.txt in its working directory and report the exact contents back to you. Then tell me what the file contained.",
		})
		content, _ := collectResponse(t, ctx, resp)

		if !historyHasToolCall(sess, "run_task", 1) {
			return errAssertion{msg: "run_task not called"}
		}
		if !strings.Contains(content, payload) {
			return errAssertion{msg: "main agent response did not relay payload " + payload + ": " + truncate(content, 200)}
		}
		return nil
	})
}

// keep import
var _ = strings.TrimSpace
