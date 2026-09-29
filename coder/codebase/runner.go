package codebase

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

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
	projectRoot  string
	codebaseDir  string
}

func newRunner(pool *fallback.ModelPool, cfg *sandbox.Config, projectRoot, codebaseDir string) (*runner, error) {
	if pool == nil {
		return nil, fmt.Errorf("Codebase model pool is required")
	}
	if err := os.MkdirAll(codebaseDir, 0o700); err != nil {
		return nil, err
	}
	cloned := sandbox.CloneConfig(cfg)
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
	return &runner{pool: pool, indexTools: all, contextTools: readOnly, exec: exec, fileHook: fileHook, projectRoot: projectRoot, codebaseDir: codebaseDir}, nil
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

func (r *runner) client(mode modeSpec) providers.Client {
	policy := fallback.NewSessionPolicy(providers.ClientPolicy{PreferredModel: mode.Model, Effort: mode.Effort})
	return r.pool.NewClient(policy, providers.ClientPolicy{})
}

// maxTokensWarning is written into the assistant content by core/agents/react.go
// when a model stream is interrupted for exceeding the configured output budget.
// It is the only in-band truncation signal available to this package; ModelCalls
// and CompletionTokensLast are logged alongside it because the wording can change.
const maxTokensWarning = "response interrupted because the model exceeded the configured max tokens"

const maxOutsideKBSamples = 3

// runStats reports one Codebase Provider run: what it consumed and whether it was
// cut short, so timeouts, loop-limit exits and prompt-level scope violations stay
// observable in the run log.
type runStats struct {
	ModelCalls           int
	ToolCalls            int
	OutsideKBReads       int
	OutsideKBSamples     []string
	PromptTokensLast     int64
	CompletionTokensLast int64
	OutputChars          int
	Truncated            bool
	LoopLimit            bool
}

// finalizeRunStats completes a run from the raw provider output.
func finalizeRunStats(stats runStats, raw string, maxLoopTimes int) runStats {
	stats.Truncated = strings.Contains(raw, maxTokensWarning)
	stats.LoopLimit = stats.ModelCalls >= maxLoopTimes
	return stats
}

// contextUsageProxy forwards usage accounting to the root Session and accumulates
// the run metrics of the temporary Codebase Provider Session.
type contextUsageProxy struct {
	root        *coresession.Session
	projectRoot string
	codebaseDir string

	mu    sync.Mutex
	stats runStats
}

func (h *contextUsageProxy) AfterModelCall(ctx context.Context, _ *coresession.Session, req providers.Request, stats *coresession.ModelCallStats) error {
	h.mu.Lock()
	h.stats.ModelCalls++
	h.stats.PromptTokensLast = stats.Tokens.PromptTokens
	h.stats.CompletionTokensLast = stats.Tokens.CompletionTokens
	h.mu.Unlock()
	return (usage.Hook{}).AfterModelCall(ctx, h.root, req, stats)
}

func (h *contextUsageProxy) AfterTool(_ context.Context, _ *coresession.Session, payload coresession.ToolPayload) error {
	h.mu.Lock()
	defer h.mu.Unlock()
	for _, execution := range payload.Executions {
		h.stats.ToolCalls++
		h.noteOutsideKBRead(execution.Call)
	}
	return nil
}

// noteOutsideKBRead counts read-only tool calls that left the Markdown knowledge
// base, which is the measured signal for whether the automatic Context contract
// keeps the provider on the knowledge base instead of the live repository.
func (h *contextUsageProxy) noteOutsideKBRead(call providers.ToolCall) {
	switch call.Name {
	case sandbox.FsReadToolName, sandbox.FsListToolName, sandbox.FsFindToolName, sandbox.FsSearchToolName:
	default:
		return
	}
	target := toolPathArgument(call.Arguments)
	if withinKnowledgeBase(h.projectRoot, h.codebaseDir, target) {
		return
	}
	h.stats.OutsideKBReads++
	if len(h.stats.OutsideKBSamples) < maxOutsideKBSamples {
		h.stats.OutsideKBSamples = append(h.stats.OutsideKBSamples, strings.TrimSpace(call.Name+" "+target))
	}
}

func (h *contextUsageProxy) snapshot() runStats {
	h.mu.Lock()
	defer h.mu.Unlock()
	stats := h.stats
	stats.OutsideKBSamples = append([]string(nil), h.stats.OutsideKBSamples...)
	return stats
}

// toolPathArgument returns the location argument of a read-only filesystem call.
func toolPathArgument(arguments string) string {
	if strings.TrimSpace(arguments) == "" {
		return ""
	}
	var parsed map[string]any
	if err := json.Unmarshal([]byte(arguments), &parsed); err != nil {
		return ""
	}
	for _, key := range []string{"path", "directory"} {
		if value, ok := parsed[key].(string); ok && strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}

// withinKnowledgeBase reports whether a tool path argument stays inside the
// knowledge base. Relative paths resolve against the project root, which is the
// working directory of the Codebase tools.
func withinKnowledgeBase(projectRoot, codebaseDir, target string) bool {
	if strings.TrimSpace(codebaseDir) == "" {
		return false
	}
	if strings.TrimSpace(target) == "" {
		target = "."
	}
	if !filepath.IsAbs(target) {
		target = filepath.Join(projectRoot, target)
	}
	rel, err := filepath.Rel(codebaseDir, filepath.Clean(target))
	if err != nil {
		return false
	}
	return rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}

func (r *runner) runContext(ctx context.Context, root *coresession.Session, history []types.Message, spec Spec, metadata string) (string, runStats, error) {
	return r.runContextProvider(ctx, root, history, spec, automaticContextSystemPrompt(spec, r.codebaseDir), metadata, spec.Context.MaxOutputTokens, 0)
}

func (r *runner) runQuery(ctx context.Context, root *coresession.Session, spec Spec, query, metadata string, maxChars int64) (string, runStats, error) {
	input := metadata + "\n\nSemantic query:\n" + strings.TrimSpace(query)
	return r.runContextProvider(ctx, root, nil, spec, queryContextSystemPrompt(spec, r.codebaseDir), input, spec.Context.MaxOutputTokens, queryOutputLimit(maxChars))
}

// contextRunTimeout bounds one Context Provider run. The total budget scales
// with the configured loop limit: max_loop_times x agents.PerLoopBudget (3m).
// The user's only tuning knob is max_loop_times; the per-loop budget is a code
// constant shared repo-wide. Note that the codebase_context_query tool stays
// under the tools.Invoker 30m hard limit, so when max_loop_times x 3m exceeds
// 30m the query path is intentionally cut short there.
func contextRunTimeout(spec Spec) time.Duration {
	return time.Duration(spec.Context.MaxLoopTimes) * agents.PerLoopBudget
}

func (r *runner) runContextProvider(ctx context.Context, root *coresession.Session, history []types.Message, spec Spec, systemPrompt, input string, maxTokens, maxChars int64) (string, runStats, error) {
	client := r.client(spec.Context)
	cloned := append([]types.Message(nil), history...)
	proxy := &contextUsageProxy{root: root, projectRoot: r.projectRoot, codebaseDir: r.codebaseDir}
	temp := coresession.New(types.NewID(), client,
		coresession.WithHistory(cloned...),
		coresession.WithTemporary(true),
		coresession.WithHooks(proxy),
	)
	agent := agents.New(client, agents.Option{
		SystemPrompt: systemPrompt,
		MaxLoopTimes: spec.Context.MaxLoopTimes,
		MaxTokens:    maxTokens,
		Tools:        r.contextTools,
		Invoker:      tools.NewInvoker(),
	})
	runCtx, cancel := context.WithTimeout(ctx, contextRunTimeout(spec))
	defer cancel()
	raw, err := api.ReadAllContent(runCtx, agent.Chat(runCtx, &api.Request{Session: temp, AgentMessage: input}))
	stats := finalizeRunStats(proxy.snapshot(), raw, spec.Context.MaxLoopTimes)
	if err != nil {
		stats.OutputChars = len(strings.TrimSpace(raw))
		return strings.TrimSpace(raw), stats, err
	}
	out := boundOutput(raw, maxTokens)
	if maxChars > 0 && int64(len(out)) > maxChars {
		out = boundOutputChars(out, int(maxChars))
	}
	stats.OutputChars = len(out)
	return out, stats, nil
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
