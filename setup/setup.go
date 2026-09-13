package setup

import (
	"context"
	"fmt"
	"os"
	"strings"

	coderagents "github.com/basenana/friday/coder/agents"
	"github.com/basenana/friday/coder/filetools"
	coderloop "github.com/basenana/friday/coder/loop"
	"github.com/basenana/friday/config"
	"github.com/basenana/friday/core/agents"
	"github.com/basenana/friday/core/api"
	"github.com/basenana/friday/core/collaboration"
	"github.com/basenana/friday/core/contextmgr"
	"github.com/basenana/friday/core/logger"
	"github.com/basenana/friday/core/planning"
	"github.com/basenana/friday/core/providers"
	coreSession "github.com/basenana/friday/core/session"
	"github.com/basenana/friday/core/subagents"
	"github.com/basenana/friday/core/tools"
	"github.com/basenana/friday/memory"
	"github.com/basenana/friday/proposals"
	"github.com/basenana/friday/sandbox"
	"github.com/basenana/friday/sessions"
	"github.com/basenana/friday/skills"
	"github.com/basenana/friday/teams"
	"github.com/basenana/friday/workspace"
)

type AgentContext struct {
	Client      providers.Client
	Workspace   *workspace.Workspace
	Lifecycle   sessions.SessionLifecycle
	Session     *coreSession.Session
	Agent       agents.Agent
	Memory      *memory.MemorySystem
	TaskManager *sandbox.TaskManager
}

type Option func(*options)

type options struct {
	sessionID      string
	isolate        bool
	temporary      bool
	verbose        bool
	extraTools     []*tools.Tool
	providerClient providers.Client
	lifecycle      sessions.SessionLifecycle
	workdir        string
}

type SessionManager interface {
	SetLLM(llm providers.Client)
	GetOrCreateByID(sessionID string, opts ...coreSession.Option) (*coreSession.Session, bool, error)
	GetOrCreateDetachedByID(sessionID string, opts ...coreSession.Option) (*coreSession.Session, bool, error)
	CreateIsolated(opts ...coreSession.Option) (*coreSession.Session, string, error)
	CreateTemporary(opts ...coreSession.Option) (*coreSession.Session, string, error)
	GetOrCreateCurrent(opts ...coreSession.Option) (*coreSession.Session, string, bool, error)
}

func WithSessionID(id string) Option {
	return func(o *options) {
		o.sessionID = id
	}
}

func WithIsolate(v bool) Option {
	return func(o *options) {
		o.isolate = v
	}
}

func WithTemporary(v bool) Option {
	return func(o *options) {
		o.temporary = v
	}
}

func WithVerbose(v bool) Option {
	return func(o *options) {
		o.verbose = v
	}
}

func WithExtraTools(t []*tools.Tool) Option {
	return func(o *options) {
		o.extraTools = t
	}
}

// WithProviderClient supplies a pre-built providers.Client so setup
// does not construct a second one. Useful when a caller has already
// built a client for another consumer and wants to share the same
// authenticated transport with the agent.
func WithProviderClient(c providers.Client) Option {
	return func(o *options) {
		o.providerClient = c
	}
}

// WithWorkdir pins all filesystem and process tools to one canonical runtime
// root. Interactive project callers should always set it explicitly.
func WithWorkdir(workdir string) Option {
	return func(o *options) { o.workdir = workdir }
}

// NewAgentWithLifecycle builds an agent around an already-selected root
// lifecycle. The agent cannot use services to switch or enumerate roots;
// services are retained only for metadata/planning persistence capabilities.
func NewAgentWithLifecycle(lifecycle sessions.SessionLifecycle, services SessionManager, cfg *config.Config, opts ...Option) (*AgentContext, error) {
	opts = append(opts, func(o *options) { o.lifecycle = lifecycle })
	return NewAgent(services, cfg, opts...)
}

func NewAgent(sessionMgr SessionManager, cfg *config.Config, opts ...Option) (*AgentContext, error) {
	options := &options{}
	for _, opt := range opts {
		opt(options)
	}

	var client providers.Client
	if options.providerClient != nil {
		client = options.providerClient
	} else {
		c, err := CreateProviderClient(cfg)
		if err != nil {
			return nil, fmt.Errorf("create provider client: %w", err)
		}
		client = c
	}

	if options.lifecycle == nil {
		sessionMgr.SetLLM(client)
	}

	ws := workspace.NewWorkspace(cfg.WorkspacePath(), cfg.MemoryPath())
	var err error
	if err = ws.EnsureDir(""); err != nil {
		return nil, fmt.Errorf("create workspace: %w", err)
	}

	fileState := workspace.NewFileState(cfg.StatePath())
	sessionOpts := []coreSession.Option{coreSession.WithState(fileState)}

	var sess *coreSession.Session
	lifecycle := options.lifecycle
	if lifecycle != nil {
		sess = lifecycle.Current()
	} else {
		switch {
		case options.sessionID != "":
			sess, _, err = sessionMgr.GetOrCreateByID(options.sessionID, sessionOpts...)
		case options.isolate:
			sess, _, err = sessionMgr.CreateIsolated(sessionOpts...)
		case options.temporary:
			sess, _, err = sessionMgr.CreateTemporary(sessionOpts...)
		default:
			sess, _, _, err = sessionMgr.GetOrCreateCurrent(sessionOpts...)
		}
		if err == nil {
			if provider, ok := sessionMgr.(interface{ GetStore() sessions.Store }); ok {
				lifecycle = sessions.BindLifecycle(sess, client, provider.GetStore())
			}
		}
	}
	if err != nil {
		return nil, fmt.Errorf("get/create session: %w", err)
	}

	if options.verbose {
		sessionType := ""
		if options.isolate {
			sessionType = " (isolated)"
		}
		if options.temporary {
			sessionType = " (temporary)"
		}
		if len(sess.History) == 0 {
			fmt.Printf("Created new session%s\n", sessionType)
		} else {
			fmt.Printf("Using session%s (loaded %d messages)\n", sessionType, len(sess.History))
		}
	}

	memSys := memory.NewMemorySystem(cfg.MemoryPath(), cfg.Memory.Days)
	if err = memSys.EnsureTodayMemory(); err != nil {
		fmt.Fprintf(os.Stderr, "Warning: failed to ensure memory log: %v\n", err)
	}

	loaded, err := ws.Load(workspace.WithMemoryDays(cfg.Memory.Days))
	if err != nil {
		return nil, fmt.Errorf("load workspace content: %w", err)
	}

	planningHook := planning.New(planning.Option{ModeProvider: collaborationProvider(sessionMgr)})
	collaborationHook := collaboration.NewHook(collaborationProvider(sessionMgr), cfg.Collaboration.Plan.ReasoningEffort)
	skillLoader := skills.NewLoader(ws.SkillsPath())
	if err := skillLoader.Load(); err != nil {
		fmt.Fprintf(os.Stderr, "Warning: failed to load skills: %v\n", err)
	}
	skillRegistry := skills.NewRegistry(skillLoader)
	skillHook := skills.NewHook(skillRegistry)

	// Team system: provides team_load/team_list/team_comment tools and the
	// active-team system prompt hint. The registry is shared with the proposal
	// strategy so proposal_run can pick the active team.
	teamLoader := teams.NewLoader(cfg.TeamsPath())
	if err := teamLoader.Load(); err != nil {
		fmt.Fprintf(os.Stderr, "Warning: failed to load teams: %v\n", err)
	}
	teamRegistry := teams.NewRegistry(teamLoader)
	teamRegistry.Refresh()
	teamHook := teams.NewHook(teamRegistry, cfg.TeamsPath())

	approvedPlanHook := planning.NewApprovedPlanContextHook(planRepositoryFromManager(sessionMgr))
	loopHook := coderloop.NewHook()
	contextHook := contextmgr.New(client, contextmgr.Config{
		ContextWindow:      cfg.Model.ContextWindow,
		SessionMemoryStore: sessionMemoryStoreFromManager(sessionMgr),
		ReservedTokens:     approvedPlanHook.ReservedTokens,
	})
	refocusHook := contextmgr.NewRefocusHook()

	workdir := options.workdir
	if workdir == "" {
		workdir, _ = os.Getwd()
	}

	var allTools []*tools.Tool
	sandboxCfg := cfg.Sandbox
	if sandboxCfg == nil {
		sandboxCfg = sandbox.DefaultConfig()
	}
	sandboxExec := sandbox.NewExecutor(sandboxCfg)
	fileHook, err := filetools.New(sandboxExec, workdir)
	if err != nil {
		return nil, fmt.Errorf("create file tools: %w", err)
	}
	_ = fileHook.BeforeAgent(context.Background(), sess, nil)
	allTools = append(allTools, fileHook.Tools()...)
	imageTool := sandbox.NewImageTool(sandboxExec, workdir, newImageAnalyzer(cfg))
	allTools = append(allTools, imageTool)
	bashTool := sandbox.NewBashTool(sandboxExec, workdir)
	allTools = append(allTools, bashTool)
	pollWaitTool := sandbox.NewPollWaitTool(sandboxExec, workdir)
	allTools = append(allTools, pollWaitTool)
	taskManager := sandbox.NewTaskManager(sandboxExec)
	bgTools := sandbox.NewBackgroundTaskTools(taskManager, workdir)
	allTools = append(allTools, bgTools...)

	if len(options.extraTools) > 0 {
		allTools = append(allTools, options.extraTools...)
	}

	// Trace wraps the real handler (innermost) so every invocation —
	// including each retry attempt — is observed with its evidence; retry
	// sits outside and injects that evidence into the context. Registering
	// both here wraps the shared tool set exactly once.
	allTools = wrapToolsWithInvocationRetry(
		wrapToolsWithTrace(allTools, toolTraceSinkForConfig(cfg)),
		defaultToolInvocationPolicy,
	)

	agent := agents.New(client, agents.Option{
		SystemPrompt: workspace.ComposeSystemPrompt(loaded),
		Tools:        allTools,
	})

	// Build subagents via coder/agents factory: each agent gets its own
	// (possibly overridden) provider client and a tool set filtered by the
	// spec's ToolPolicy. The explorer reuses the main system prompt so forked
	// sessions share the same cache prefix.
	factory := coderagents.NewClientFactory(client, cfg.PrimaryModel(), CreateProviderClientFromModel)
	exploreSpec := coderagents.ExplorerSpec(cfg.AgentModel(coderagents.NameExplorer))
	exploreSpec.SystemPrompt = workspace.ComposeSystemPrompt(loaded)
	exploreAgent, err := factory.BuildAgent(exploreSpec, allTools)
	if err != nil {
		return nil, fmt.Errorf("build explore agent: %w", err)
	}

	// Build the named expert agents (planner, reviewer, advisor) for run_task.
	expertSpecs := []*coderagents.AgentSpec{
		coderagents.PlannerSpec(cfg.AgentModel(coderagents.NamePlanner)),
		coderagents.ReviewerSpec(cfg.AgentModel(coderagents.NameReviewer)),
		coderagents.AdvisorSpec(cfg.AgentModel(coderagents.NameAdvisor)),
	}
	expertAgents, err := factory.BuildExpertAgents(expertSpecs, allTools)
	if err != nil {
		return nil, fmt.Errorf("build expert agents: %w", err)
	}

	subagentHook := subagents.NewHook(client, subagents.Option{
		SelfAgent: &subagents.ExpertAgent{
			Name:  coderagents.NameExplorer,
			Agent: exploreAgent,
		},
		// Do not pass request-level tool overrides into forked subagents.
		// Their filtered agent-level tool set is the authority; otherwise
		// request tools would re-expand privileges at runtime.
		ExpertAgents:  expertAgents,
		SessionForker: lifecycle,
	})

	sharedHooks := []coreSession.Hook{
		planningHook,
		skillHook,
		teamHook,
		fileHook,
		// Memory must be injected before the context manager runs so its
		// projection accounts for the extra per-request messages.
		newMemoryHook(ws),
		contextHook,
		refocusHook,
		approvedPlanHook,
		subagentHook,
		// Keep collaboration instructions last so Plan Mode remains the
		// highest-precedence request-scoped behavioral contract.
		collaborationHook,
		planning.TerminalHook{ModeProvider: collaborationProvider(sessionMgr)},
	}
	replaceSessionHooks(sess, sharedHooks...)

	// Proposal system: a RunnerFactory picks SingleAgent vs Team strategy at
	// call time based on whether the agent supplied a `team` argument. The
	// factory closes over client + tools + session manager so the proposal
	// package stays free of agent-construction concerns.
	proposalLoader := proposals.NewLoader(cfg.ProposalsPath())
	proposalRunnerFactory := buildProposalRunnerFactory(
		client, allTools, sharedHooks, lifecycle, teamRegistry, cfg, loaded,
	)
	sess.RegisterHook(proposals.NewHook(proposalLoader, proposalRunnerFactory))
	// Loop is last so its stable autonomous-work contract has the final
	// system-prompt position while active. It is otherwise a no-op.
	sess.RegisterHook(loopHook)

	return &AgentContext{
		Client:      client,
		Workspace:   ws,
		Lifecycle:   lifecycle,
		Session:     sess,
		Agent:       agent,
		Memory:      memSys,
		TaskManager: taskManager,
	}, nil
}

func collaborationProvider(sessionMgr SessionManager) collaboration.ModeProvider {
	provider, _ := sessionMgr.(collaboration.ModeProvider)
	return provider
}

// Close releases all resources owned by the AgentContext.
// KillAll runs first so in-flight tasks are stopped before the session event bus is torn down.
func (ac *AgentContext) Close() {
	ac.TaskManager.KillAll()
	if ac.Lifecycle != nil {
		_ = ac.Lifecycle.Close()
	} else {
		ac.Session.Close()
	}
}

func (ac *AgentContext) Chat(ctx context.Context, message string) *api.Response {
	req := &api.Request{
		Session:     ac.Session,
		UserMessage: message,
	}
	return ac.Agent.Chat(ctx, req)
}

func sessionMemoryStoreFromManager(sessionMgr SessionManager) contextmgr.SessionMemoryStore {
	provider, ok := sessionMgr.(interface{ GetStore() sessions.Store })
	if !ok {
		return nil
	}

	store, ok := provider.GetStore().(contextmgr.SessionMemoryStore)
	if !ok {
		return nil
	}
	return store
}

func planRepositoryFromManager(sessionMgr SessionManager) planning.Repository {
	provider, ok := sessionMgr.(interface{ GetStore() sessions.Store })
	if !ok {
		return nil
	}
	repository, _ := provider.GetStore().(planning.Repository)
	return repository
}

func (ac *AgentContext) ChatWithImageRefs(ctx context.Context, message string, imageRefs ...string) *api.Response {
	if len(imageRefs) > 0 {
		message = appendImageRefsToMessage(message, imageRefs)
	}

	req := &api.Request{
		Session:     ac.Session,
		UserMessage: message,
		ImageURLs:   append([]string(nil), imageRefs...),
	}
	return ac.Agent.Chat(ctx, req)
}

func PrintResponse(resp *api.Response) {
	hasOutput := false
Waiting:
	for {
		select {
		case err := <-resp.Error():
			if err != nil {
				fmt.Fprintf(os.Stderr, "Error: %v\n", err)
				return
			}
		case delta, ok := <-resp.Deltas():
			if !ok {
				break Waiting
			}
			if !hasOutput && strings.TrimSpace(delta.Content) == "" {
				continue
			}
			hasOutput = true
			fmt.Print(delta.Content)
		}
	}
	fmt.Println()
}

func appendImageRefsToMessage(message string, imageRefs []string) string {
	var builder strings.Builder

	builder.WriteString(strings.TrimSpace(message))
	if builder.Len() > 0 {
		builder.WriteString("\n\n")
	}

	builder.WriteString("User-provided image references:\n")
	for i, ref := range imageRefs {
		fmt.Fprintf(&builder, "%d. %s\n", i+1, ref)
	}
	builder.WriteString("If you need to inspect image contents, use the image tool with the relevant image reference instead of guessing.")
	return builder.String()
}

// buildProposalRunnerFactory constructs a proposals.RunnerFactory. The factory
// inspects proposal.OwningTeam to pick a strategy: if a team is named, it
// loads the team + members and uses TeamStrategy; otherwise it uses
// SingleAgentStrategy against a detached proposal session.
//
// Both branches close over the same client, tool set, and session manager so
// the proposals package never needs to import agents/providers directly.
func buildProposalRunnerFactory(
	client providers.Client,
	allTools []*tools.Tool,
	sharedHooks []coreSession.Hook,
	lifecycle sessions.SessionLifecycle,
	teamRegistry *teams.Registry,
	cfg *config.Config,
	loaded *workspace.LoadedContent,
) proposals.RunnerFactory {
	agentFactory := func(systemPrompt string, tools []*tools.Tool) agents.Agent {
		return agents.New(client, agents.Option{
			SystemPrompt: systemPrompt,
			Tools:        tools,
		})
	}
	systemPrompt := workspace.ComposeSystemPrompt(loaded)

	return func(proposal *proposals.Proposal, designDoc string) (*proposals.Runner, proposals.ExecutionStrategy, error) {
		if lifecycle == nil {
			return nil, nil, fmt.Errorf("session lifecycle is unavailable")
		}
		loader := proposals.NewLoader(cfg.ProposalsPath())

		if proposal.OwningTeam != "" {
			team, ok := teamRegistry.Get(proposal.OwningTeam)
			if !ok {
				// Refresh once in case the team was added after setup.
				if err := teamRegistry.Loader().Load(); err != nil {
					return nil, nil, fmt.Errorf("load teams: %w", err)
				}
				teamRegistry.Refresh()
				team, ok = teamRegistry.Get(proposal.OwningTeam)
			}
			if !ok {
				return nil, nil, fmt.Errorf("team not found: %s", proposal.OwningTeam)
			}
			members, err := teams.LoadMembers(cfg.TeamsPath(), team.Name, team.Members)
			if err != nil {
				return nil, nil, fmt.Errorf("load members: %w", err)
			}
			sessionFactory := proposals.SessionFactory(func(proposalID, assignee string) (*coreSession.Session, error) {
				key := fmt.Sprintf("proposal/%s/member/%s", proposalID, assignee)
				resumeID := ""
				if proposal.Sessions != nil {
					resumeID = proposal.Sessions[assignee]
				}
				s, _, err := lifecycle.GetOrCreateAssociated(
					context.Background(), sessions.AssociatedSpec{Key: key, ResumeID: resumeID},
					coreSession.WithState(workspace.NewFileState(cfg.StatePath())),
				)
				if err != nil {
					return nil, err
				}
				replaceSessionHooks(s, sharedHooks...)
				if proposal.Sessions == nil {
					proposal.Sessions = map[string]string{}
				}
				proposal.Sessions[assignee] = s.ID
				return s, nil
			})
			strategy, err := proposals.NewTeamStrategy(
				client, agentFactory, team, members, allTools,
				cfg.TeamsPath(), teamRegistry, sessionFactory,
			)
			if err != nil {
				return nil, nil, fmt.Errorf("team strategy: %w", err)
			}
			return proposals.NewRunner(proposal, loader, strategy), strategy, nil
		}

		// Single-agent mode: one detached session for the whole proposal.
		proposalKey := fmt.Sprintf("proposal/%s/self", proposal.ID)
		resumeID := ""
		if proposal.Sessions != nil {
			resumeID = proposal.Sessions["self"]
		}
		proposalSession, _, err := lifecycle.GetOrCreateAssociated(
			context.Background(), sessions.AssociatedSpec{Key: proposalKey, ResumeID: resumeID},
			coreSession.WithState(workspace.NewFileState(cfg.StatePath())),
		)
		if err != nil {
			return nil, nil, fmt.Errorf("create proposal session: %w", err)
		}
		replaceSessionHooks(proposalSession, sharedHooks...)
		if proposal.Sessions == nil {
			proposal.Sessions = map[string]string{}
		}
		proposal.Sessions["self"] = proposalSession.ID
		strategy := proposals.NewSingleAgentStrategy(
			client, agentFactory, allTools, proposalSession, systemPrompt,
		)
		return proposals.NewRunner(proposal, loader, strategy), strategy, nil
	}
}

func replaceSessionHooks(sess *coreSession.Session, hooks ...coreSession.Hook) {
	sess.CleanHooks()
	for _, hook := range hooks {
		sess.RegisterHook(hook)
	}
}

// getOrCreateManagedSession is retained for legacy tests and non-lifecycle
// adapters. New agent paths use SessionLifecycle.GetOrCreateAssociated.
func getOrCreateManagedSession(sessionMgr SessionManager, sessionID string, hooks []coreSession.Hook, opts ...coreSession.Option) (*coreSession.Session, bool, error) {
	sess, created, err := sessionMgr.GetOrCreateDetachedByID(sessionID, opts...)
	if err != nil {
		return nil, false, err
	}
	replaceSessionHooks(sess, hooks...)
	return sess, created, nil
}

// toolTraceSinkForConfig returns the default tool-trace sink: trace events go
// to the logger at debug level when logging is enabled, otherwise no sink is
// installed and tracing is a no-op.
func toolTraceSinkForConfig(cfg *config.Config) func(ToolTraceEvent) {
	if cfg == nil || !cfg.Log.Enabled {
		return nil
	}
	return toolTraceLoggerSink(logger.New("tools.trace"))
}
