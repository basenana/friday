package codebase

import (
	"context"
	"fmt"
	"os"
	"strings"

	"github.com/basenana/friday/coder/filetools"
	"github.com/basenana/friday/core/agents"
	"github.com/basenana/friday/core/api"
	"github.com/basenana/friday/core/contextmgr"
	"github.com/basenana/friday/core/promptcontext"
	"github.com/basenana/friday/core/providers"
	"github.com/basenana/friday/core/providers/fallback"
	coresession "github.com/basenana/friday/core/session"
	"github.com/basenana/friday/core/tools"
	"github.com/basenana/friday/core/types"
	"github.com/basenana/friday/sandbox"
	"github.com/basenana/friday/sessions"
	"github.com/basenana/friday/sessions/usage"
)

const (
	stableIndexMessage          = "Perform one Codebase maintenance pass using the request-local repository evidence. Update INDEX.md and knowledge/ when useful."
	maxCodebaseQueryOutputRunes = 16_000
)

type runner struct {
	pool         *fallback.ModelPool
	indexTools   []*tools.Tool
	contextTools []*tools.Tool
	exec         *sandbox.Executor
	fileHook     *filetools.Hook
	codebaseDir  string
}

func newRunner(pool *fallback.ModelPool, cfg *sandbox.Config, projectRoot, codebaseDir string) (*runner, error) {
	if pool == nil {
		return nil, fmt.Errorf("Codebase model pool is required")
	}
	if err := os.MkdirAll(codebaseDir, 0o700); err != nil {
		return nil, err
	}
	cloned := cloneSandboxConfig(cfg)
	cloned.Sandbox.Filesystem.Write = append(cloned.Sandbox.Filesystem.Write, codebaseDir)
	if err := cloned.Validate(); err != nil {
		return nil, fmt.Errorf("validate Codebase sandbox: %w", err)
	}
	exec := sandbox.NewExecutor(cloned)
	fileHook, err := filetools.New(exec, projectRoot)
	if err != nil {
		return nil, fmt.Errorf("create Codebase filesystem tools: %w", err)
	}
	all := []*tools.Tool{sandbox.NewBashTool(exec, projectRoot, nil)}
	all = append(all, fileHook.Tools()...)
	readOnly := contextTools(all)
	readOnly = append(readOnly, newReadOnlyGitTool(exec, projectRoot))
	return &runner{pool: pool, indexTools: all, contextTools: readOnly, exec: exec, fileHook: fileHook, codebaseDir: codebaseDir}, nil
}

func contextTools(all []*tools.Tool) []*tools.Tool {
	out := make([]*tools.Tool, 0, 4)
	for _, tool := range all {
		if tool == nil {
			continue
		}
		switch tool.Name {
		case sandbox.FsReadToolName, sandbox.FsListToolName, sandbox.FsFindToolName, sandbox.FsSearchToolName:
			out = append(out, tool)
		}
	}
	return out
}

func queryOutputLimit(caller int64) int64 {
	if caller > 0 && caller < maxCodebaseQueryOutputRunes {
		return caller
	}
	return maxCodebaseQueryOutputRunes
}

func cloneSandboxConfig(src *sandbox.Config) *sandbox.Config {
	if src == nil {
		src = sandbox.DefaultConfig()
	}
	dst := *src
	dst.Permissions.Allow = append([]string(nil), src.Permissions.Allow...)
	dst.Permissions.Deny = append([]string(nil), src.Permissions.Deny...)
	dst.Sandbox.Filesystem.ReadOnly = append([]string(nil), src.Sandbox.Filesystem.ReadOnly...)
	dst.Sandbox.Filesystem.Deny = append([]string(nil), src.Sandbox.Filesystem.Deny...)
	dst.Sandbox.Filesystem.Write = append([]string(nil), src.Sandbox.Filesystem.Write...)
	dst.Sandbox.Filesystem.Protected = append([]string(nil), src.Sandbox.Filesystem.Protected...)
	dst.Sandbox.Network.Allow = append([]string(nil), src.Sandbox.Network.Allow...)
	return &dst
}

func (r *runner) client(mode modeSpec) providers.Client {
	policy := fallback.NewSessionPolicy(providers.ClientPolicy{PreferredModel: mode.Model, Effort: mode.Effort})
	return r.pool.NewClient(policy, providers.ClientPolicy{})
}

type contextUsageProxy struct{ root *coresession.Session }

func (h contextUsageProxy) AfterModelCall(ctx context.Context, _ *coresession.Session, req providers.Request, stats *coresession.ModelCallStats) error {
	return (usage.Hook{}).AfterModelCall(ctx, h.root, req, stats)
}

func (r *runner) runContext(ctx context.Context, root *coresession.Session, history []types.Message, spec Spec, metadata string) (string, error) {
	const automaticMaxTokens int64 = 1200
	maxTokens := min(spec.Context.MaxOutputTokens, automaticMaxTokens)
	return r.runContextProvider(ctx, root, history, spec, automaticContextSystemPrompt(spec, r.codebaseDir), metadata, maxTokens, 0)
}

func (r *runner) runQuery(ctx context.Context, root *coresession.Session, spec Spec, query, metadata string, maxChars int64) (string, error) {
	input := metadata + "\n\nSemantic query:\n" + strings.TrimSpace(query)
	return r.runContextProvider(ctx, root, nil, spec, queryContextSystemPrompt(spec, r.codebaseDir), input, spec.Context.MaxOutputTokens, queryOutputLimit(maxChars))
}

func (r *runner) runContextProvider(ctx context.Context, root *coresession.Session, history []types.Message, spec Spec, systemPrompt, input string, maxTokens, maxChars int64) (string, error) {
	client := r.client(spec.Context.modeSpec)
	cloned := append([]types.Message(nil), history...)
	temp := coresession.New(types.NewID(), client,
		coresession.WithHistory(cloned...),
		coresession.WithTemporary(true),
		coresession.WithHooks(contextUsageProxy{root: root}),
	)
	agent := agents.New(client, agents.Option{
		SystemPrompt: systemPrompt,
		MaxLoopTimes: spec.Context.MaxLoopTimes,
		MaxTokens:    maxTokens,
		Tools:        r.contextTools,
		Invoker:      tools.NewInvoker(),
	})
	runCtx, cancel := context.WithTimeout(ctx, spec.Context.Timeout.Duration)
	defer cancel()
	out, err := api.ReadAllContent(runCtx, agent.Chat(runCtx, &api.Request{Session: temp, AgentMessage: input}))
	if err != nil {
		return strings.TrimSpace(out), err
	}
	out = boundOutput(out, maxTokens)
	if maxChars > 0 && int64(len(out)) > maxChars {
		out = boundOutputChars(out, int(maxChars))
	}
	return out, nil
}

type requestInputHook struct {
	text         string
	approvedPlan string
}

func (h requestInputHook) BeforeModel(_ context.Context, _ *coresession.Session, req providers.Request) error {
	promptcontext.SetBlock(req, promptcontext.ApprovedPlan, h.approvedPlan)
	history := req.History()
	injected := make([]types.Message, 0, len(history)+1)
	injected = append(injected, types.Message{Role: types.RoleAgent, Content: h.text, Metadata: map[string]string{"friday.codebase": "index"}})
	injected = append(injected, history...)
	req.SetHistory(injected)
	return nil
}

func (r *runner) indexOptions(client providers.Client, payload, approvedPlan string) []coresession.Option {
	requestTokens := coresession.EstimateHistoryTokens([]types.Message{
		{Role: types.RoleAgent, Content: payload},
		{Role: types.RoleAgent, Content: approvedPlan},
	})
	return []coresession.Option{coresession.WithHooks(
		usage.Hook{},
		contextmgr.New(client, contextmgr.Config{
			ContextWindow: contextWindow(client),
			ReservedTokens: func(sess *coresession.Session) int64 {
				return r.fileHook.ReservedTokens(sess) + requestTokens
			},
		}),
		r.fileHook,
		requestInputHook{text: payload, approvedPlan: approvedPlan},
	)}
}

func (r *runner) runIndex(ctx context.Context, lifecycle sessions.SessionLifecycle, client providers.Client, spec Spec) (string, error) {
	agent := agents.New(client, agents.Option{SystemPrompt: indexSystemPrompt(spec, r.codebaseDir), MaxLoopTimes: spec.Index.MaxLoopTimes, MaxTokens: spec.Index.MaxOutputTokens, Tools: r.indexTools, Invoker: tools.NewInvoker()})
	return api.ReadAllContent(ctx, agent.Chat(ctx, &api.Request{Session: lifecycle.Current(), UserMessage: stableIndexMessage}))
}

func contextWindow(client providers.Client) int64 {
	if p, ok := client.(providers.ContextWindowProvider); ok {
		return p.ContextWindow()
	}
	return 0
}

func boundOutputChars(value string, maxChars int) string {
	value = strings.TrimSpace(value)
	if maxChars <= 0 {
		return value
	}
	runes := []rune(value)
	if len(runes) <= maxChars {
		return value
	}
	const suffix = "\n\n[truncated]"
	suffixRunes := []rune(suffix)
	keep := maxChars - len(suffixRunes)
	if keep <= 0 {
		return string(suffixRunes[:maxChars])
	}
	return string(runes[:keep]) + suffix
}

func boundOutput(value string, tokens int64) string {
	value = strings.TrimSpace(value)
	limit := int(tokens * 4)
	const marker = "\n\n[truncated by Codebase]"
	if limit > 0 && len(value) > limit {
		contentLimit := limit - len(marker)
		if contentLimit <= 0 {
			return truncateUTF8(strings.TrimSpace(marker), limit)
		}
		return strings.TrimSpace(truncateUTF8(value, contentLimit)) + marker
	}
	return value
}
