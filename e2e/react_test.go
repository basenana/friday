//go:build e2e

package e2e

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/basenana/friday/core/api"
	"github.com/basenana/friday/core/types"
	"github.com/basenana/friday/sandbox"
)

// TestReact_FsWriteRead verifies the agent can use fs_write then fs_read in
// sequence and produce the correct content on disk.
func TestReact_FsWriteRead(t *testing.T) {
	cfg := loadConfig(t)
	agent, sess, workdir := newAgentWithTools(t, cfg, "chat")
	ctx, cancel := context.WithTimeout(context.Background(), cfg.TestTimeout())
	defer cancel()

	resp := agent.Chat(ctx, &api.Request{
		Session:     sess,
		UserMessage: `Create a file named test.txt in the current directory with the exact content "hello friday" (no trailing newline beyond what you write), then read it back and tell me what it contains.`,
	})
	content, _ := collectResponse(t, ctx, resp)
	_ = content

	// Ground truth: the file must exist on disk.
	data, err := os.ReadFile(filepath.Join(workdir, "test.txt"))
	if err != nil {
		t.Fatalf("expected test.txt to exist: %v", err)
	}
	if !strings.Contains(strings.ToLower(string(data)), "hello friday") {
		t.Errorf("file content = %q, want it to contain 'hello friday'", truncate(string(data), 200))
	}
	// Protocol: at least fs_write and fs_read were called.
	assertHistoryHasToolCall(t, sess, "fs_write")
	assertHistoryHasToolCall(t, sess, "fs_read")
}

// TestReact_FsEdit verifies the agent can edit a specific line in a file.
func TestReact_FsEdit(t *testing.T) {
	cfg := loadConfig(t)

	withRetry(t, cfg, func(attempt int) error {
		agent, sess, workdir := newAgentWithTools(t, cfg, "chat")
		ctx, cancel := context.WithTimeout(context.Background(), cfg.TestTimeout())
		defer cancel()

		target := filepath.Join(workdir, "lines.txt")
		if err := os.WriteFile(target, []byte("line1\nline2\nline3\n"), 0644); err != nil {
			return err
		}

		resp := agent.Chat(ctx, &api.Request{
			Session: sess,
			UserMessage: `Edit the file lines.txt: replace every occurrence of "line2" with "modified". Do not add or remove other lines.`,
		})
		collectResponse(t, ctx, resp)

		data, err := os.ReadFile(target)
		if err != nil {
			return err
		}
		if !strings.Contains(string(data), "modified") {
			return errAssertion{msg: "expected lines.txt to contain 'modified', got " + truncate(string(data), 200)}
		}
		if strings.Contains(string(data), "line2") {
			return errAssertion{msg: "expected line2 to be replaced, got " + truncate(string(data), 200)}
		}
		if !historyHasToolCall(sess, "fs_edit", 1) {
			return errAssertion{msg: "fs_edit not called"}
		}
		return nil
	})
}

// TestReact_FsList verifies the agent can list directory contents.
func TestReact_FsList(t *testing.T) {
	cfg := loadConfig(t)

	withRetry(t, cfg, func(attempt int) error {
		agent, sess, workdir := newAgentWithTools(t, cfg, "chat")
		ctx, cancel := context.WithTimeout(context.Background(), cfg.TestTimeout())
		defer cancel()

		for _, n := range []string{"alpha.txt", "beta.txt", "gamma.txt"} {
			if err := os.WriteFile(filepath.Join(workdir, n), []byte("x"), 0644); err != nil {
				return err
			}
		}

		resp := agent.Chat(ctx, &api.Request{
			Session:     sess,
			UserMessage: "List the files in the current working directory and report every file name you see.",
		})
		content, _ := collectResponse(t, ctx, resp)

		if !historyHasToolCall(sess, "fs_list", 1) {
			return errAssertion{msg: "fs_list not called"}
		}
		for _, f := range []string{"alpha", "beta", "gamma"} {
			if !strings.Contains(strings.ToLower(content), strings.ToLower(f)) {
				return errAssertion{msg: "expected response to mention " + f + ", got " + truncate(content, 300)}
			}
		}
		return nil
	})
}

// TestReact_BashExec verifies the agent invokes the bash tool and reports
// its output.
func TestReact_BashExec(t *testing.T) {
	cfg := loadConfig(t)

	withRetry(t, cfg, func(attempt int) error {
		agent, sess, _ := newAgentWithTools(t, cfg, "chat")
		ctx, cancel := context.WithTimeout(context.Background(), cfg.TestTimeout())
		defer cancel()

		resp := agent.Chat(ctx, &api.Request{
			Session:     sess,
			UserMessage: "Run the shell command `echo hello_world` and tell me exactly what it printed.",
		})
		content, _ := collectResponse(t, ctx, resp)

		if !strings.Contains(strings.ToLower(content), "hello_world") {
			return errAssertion{msg: "response missing hello_world: " + truncate(content, 200)}
		}
		if !historyHasToolCall(sess, "bash", 1) {
			return errAssertion{msg: "bash tool not called"}
		}
		return nil
	})
}

// TestReact_BashPermDenied verifies that a denied command (sudo) is blocked
// and the agent recovers gracefully.
func TestReact_BashPermDenied(t *testing.T) {
	cfg := loadConfig(t)
	agent, sess, _ := newAgentWithTools(t, cfg, "chat")
	ctx, cancel := context.WithTimeout(context.Background(), cfg.TestTimeout())
	defer cancel()

	resp := agent.Chat(ctx, &api.Request{
		Session:     sess,
		UserMessage: "Run the command `sudo echo test` and report what happened. If it fails, explain why.",
	})
	content, _ := collectResponse(t, ctx, resp)

	// The agent should not crash; it should mention something about denied /
	// not allowed / permission / sudo in its output.
	assertNotEmpty(t, content)
	lower := strings.ToLower(content)
	if !strings.Contains(lower, "deni") &&
		!strings.Contains(lower, "not allowed") &&
		!strings.Contains(lower, "permission") &&
		!strings.Contains(lower, "sudo") &&
		!strings.Contains(lower, "fail") {
		t.Errorf("expected response to mention denial/permission/failure, got %q", truncate(content, 300))
	}
}

// TestReact_BashTimeout verifies that a long-running command times out
// gracefully.
func TestReact_BashTimeout(t *testing.T) {
	cfg := loadConfig(t)
	// Build an executor with a 2s default timeout so long commands are killed.
	workdir := t.TempDir()
	sc := sandboxConfig(cfg)
	sc.Sandbox.Defaults.Timeout = "2s"
	exec := sandbox.NewExecutor(sc)
	client := newClient(t, cfg, "chat")
	allTools := newBashFsTools(t, exec, workdir)
	sess := newTestSession(t, client)
	agent := newReactAgent(t, client, agentOpts{Tools: allTools, MaxLoops: 15})

	ctx, cancel := context.WithTimeout(context.Background(), cfg.TestTimeout())
	defer cancel()

	resp := agent.Chat(ctx, &api.Request{
		Session:     sess,
		UserMessage: "Run the command `sleep 100` and tell me what happened.",
	})
	content, _ := collectResponse(t, ctx, resp)

	assertNotEmpty(t, content)
}

// TestReact_OutputTruncation verifies the agent handles large command output.
func TestReact_OutputTruncation(t *testing.T) {
	cfg := loadConfig(t)
	agent, sess, _ := newAgentWithTools(t, cfg, "chat")
	ctx, cancel := context.WithTimeout(context.Background(), cfg.TestTimeout())
	defer cancel()

	resp := agent.Chat(ctx, &api.Request{
		Session:     sess,
		UserMessage: "Run `seq 1 2000` and summarise the output. Don't print all the numbers; just tell me the range.",
	})
	content, _ := collectResponse(t, ctx, resp)

	assertNotEmpty(t, content)
	assertHistoryHasToolCall(t, sess, "bash")
}

// TestReact_ToolError verifies that a tool error (file not found) is
// communicated back by the agent without crashing.
func TestReact_ToolError(t *testing.T) {
	cfg := loadConfig(t)
	agent, sess, _ := newAgentWithTools(t, cfg, "chat")
	ctx, cancel := context.WithTimeout(context.Background(), cfg.TestTimeout())
	defer cancel()

	resp := agent.Chat(ctx, &api.Request{
		Session:     sess,
		UserMessage: "Read the file /tmp/friday_e2e_nonexistent_xxx.txt and tell me what's in it. If it doesn't exist, say so.",
	})
	content, _ := collectResponse(t, ctx, resp)

	assertNotEmpty(t, content)
	lower := strings.ToLower(content)
	if !strings.Contains(lower, "not exist") &&
		!strings.Contains(lower, "no such file") &&
		!strings.Contains(lower, "not found") &&
		!strings.Contains(lower, "unable") &&
		!strings.Contains(lower, "couldn't") &&
		!strings.Contains(lower, "cannot") {
		t.Errorf("expected response to mention file not found, got %q", truncate(content, 300))
	}
}

// TestReact_MultiStep verifies the agent chains multiple tool calls.
func TestReact_MultiStep(t *testing.T) {
	cfg := loadConfig(t)
	agent, sess, workdir := newAgentWithTools(t, cfg, "chat")
	ctx, cancel := context.WithTimeout(context.Background(), cfg.TestTimeout())
	defer cancel()

	resp := agent.Chat(ctx, &api.Request{
		Session:     sess,
		UserMessage: "Create three files named a.txt, b.txt, c.txt in the current directory, each containing the word ok. Then list the directory and tell me how many .txt files exist.",
	})
	content, _ := collectResponse(t, ctx, resp)

	// Ground truth: three files exist.
	for _, n := range []string{"a.txt", "b.txt", "c.txt"} {
		assertFileExists(t, filepath.Join(workdir, n))
	}
	assertHistoryHasToolCallN(t, sess, "fs_write", 3)
	_ = content
}

// TestReact_Image verifies the image analysis tool with a vision model.
func TestReact_Image(t *testing.T) {
	cfg := loadConfig(t)
	if _, ok := cfg.Models["image"]; !ok {
		t.Skip("image model not configured")
	}
	client := newClient(t, cfg, "chat")
	workdir := t.TempDir()
	exec := newExecutor(t, cfg)
	tm := newAllTools(t, cfg, exec, workdir) // includes image tool
	sess := newTestSession(t, client)
	agent := newReactAgent(t, client, agentOpts{Tools: tm, MaxLoops: 15})

	pngPath := makeTestPNG(t)
	ctx, cancel := context.WithTimeout(context.Background(), cfg.TestTimeout())
	defer cancel()

	resp := agent.Chat(ctx, &api.Request{
		Session: sess,
		UserMessage: "There is an image at " + pngPath + " . Use the image tool to look at it and describe the dominant colour and shape you see.",
	})
	content, _ := collectResponse(t, ctx, resp)

	assertNotEmpty(t, content)
	assertHistoryHasToolCall(t, sess, "image")
	lower := strings.ToLower(content)
	if !strings.Contains(lower, "red") && !strings.Contains(lower, "circle") {
		t.Errorf("expected description of red circle, got %q", truncate(content, 300))
	}
}

// TestReact_BackgroundTask verifies the background_task + wait_task lifecycle.
func TestReact_BackgroundTask(t *testing.T) {
	cfg := loadConfig(t)
	client := newClient(t, cfg, "chat")
	workdir := t.TempDir()
	exec := newExecutor(t, cfg)
	bgTools := sandbox.NewBackgroundTaskTools(sandbox.NewTaskManager(exec), workdir)
	allTools := append(newBashFsTools(t, exec, workdir), bgTools...)
	sess := newTestSession(t, client)
	agent := newReactAgent(t, client, agentOpts{Tools: allTools, MaxLoops: 15})

	ctx, cancel := context.WithTimeout(context.Background(), cfg.TestTimeout())
	defer cancel()

	resp := agent.Chat(ctx, &api.Request{
		Session:     sess,
		UserMessage: "Start a background task that runs `sleep 2` then wait for it to finish and report its status.",
	})
	content, _ := collectResponse(t, ctx, resp)

	assertHistoryHasToolCall(t, sess, "background_task")
	assertNotEmpty(t, content)
}

// TestReact_BackgroundTaskKill verifies the kill_task lifecycle.
func TestReact_BackgroundTaskKill(t *testing.T) {
	cfg := loadConfig(t)
	client := newClient(t, cfg, "chat")
	workdir := t.TempDir()
	exec := newExecutor(t, cfg)
	bgTools := sandbox.NewBackgroundTaskTools(sandbox.NewTaskManager(exec), workdir)
	allTools := append(newBashFsTools(t, exec, workdir), bgTools...)
	sess := newTestSession(t, client)
	agent := newReactAgent(t, client, agentOpts{Tools: allTools, MaxLoops: 15})

	ctx, cancel := context.WithTimeout(context.Background(), cfg.TestTimeout())
	defer cancel()

	resp := agent.Chat(ctx, &api.Request{
		Session:     sess,
		UserMessage: "Start a background task running `sleep 100`, then kill it, and tell me the final status.",
	})
	content, _ := collectResponse(t, ctx, resp)

	assertHistoryHasToolCall(t, sess, "background_task")
	assertNotEmpty(t, content)
}

// TestReact_MaxLoopsTermination verifies that when the agent reaches its
// MaxLoops limit it terminates gracefully (no crash, no hang) and emits
// EventAgentFinish. We force repeated tool calls until the cap kicks in.
func TestReact_MaxLoopsTermination(t *testing.T) {
	cfg := loadConfig(t)

	withRetry(t, cfg, func(attempt int) error {
		client := newClient(t, cfg, "chat")
		workdir := t.TempDir()
		exec := newExecutor(t, cfg)
		allTools := newBashFsTools(t, exec, workdir)
		sess := newTestSession(t, client)
		eventsPtr, eventsMu, unsub := subscribeSessionEvents(t, sess)
		defer unsub()
		// Low MaxLoops so we hit the cap quickly.
		agent := newReactAgent(t, client, agentOpts{Tools: allTools, MaxLoops: 3})

		ctx, cancel := context.WithTimeout(context.Background(), cfg.TestTimeout())
		defer cancel()

		resp := agent.Chat(ctx, &api.Request{
			Session:     sess,
			UserMessage: "Use the bash tool to run `echo loop_iteration`. Then check the output and run it again. Keep doing this until you have run it at least 10 times. Do NOT stop early.",
		})
		// collectResponse must drain the stream and return without error.
		content, _ := collectResponse(t, ctx, resp)
		_ = content

		// Agent must emit EventAgentFinish within a few seconds — this is
		// the primary MaxLoops termination signal.
		if !waitForEventType(eventsPtr, eventsMu, types.EventAgentFinish, 5*time.Second) {
			return errAssertion{msg: "EventAgentFinish not received within 5s; agent may be hung"}
		}
		// Count bash tool calls. With MaxLoops=3, the agent's react loop is
		// capped at 3 iterations, but each iteration may emit multiple
		// parallel tool calls in a single assistant message. So the total
		// tool-call count is bounded by MaxLoops × (max parallel tool calls
		// per turn). We assert a generous upper bound to catch runaway
		// loops without being sensitive to LLM parallelism choices.
		bashCalls := 0
		for _, msg := range sess.GetHistory() {
			for _, tc := range msg.ToolCalls {
				if tc.Name == "bash" {
					bashCalls++
				}
			}
		}
		if bashCalls > 30 {
			return errAssertion{msg: fmt.Sprintf("bash tool called %d times, expected <= 30 (MaxLoops runaway)", bashCalls)}
		}
		return nil
	})
}

// keep import
var _ = types.RoleUser // keep import
