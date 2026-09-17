package setup

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	coderagents "github.com/basenana/friday/coder/agents"
	"github.com/basenana/friday/coder/configtools"
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
	"github.com/basenana/friday/core/providers/fallback"
	coreSession "github.com/basenana/friday/core/session"
	"github.com/basenana/friday/core/subagents"
	"github.com/basenana/friday/core/tools"
	fridaymcp "github.com/basenana/friday/mcp"
	"github.com/basenana/friday/memory"
	"github.com/basenana/friday/sandbox"
	"github.com/basenana/friday/sessions"
	"github.com/basenana/friday/skills"
	"github.com/basenana/friday/workspace"
)

type AgentContext struct {
	Client      providers.Client
	Policy      *fallback.SessionPolicy
	Workspace   *workspace.Workspace
	Lifecycle   sessions.SessionLifecycle
	Session     *coreSession.Session
	Agent       agents.Agent
	Memory      *memory.MemorySystem
	TaskManager *sandbox.TaskManager
	mcpManager  *fridaymcp.Manager
	ownsMCP     bool
}

type Option func(*options)

type options struct {
	sessionID      string
	isolate        bool
	temporary      bool
	verbose        bool
	extraTools     []*tools.Tool
	providerClient providers.Client
	modelPool      *fallback.ModelPool
	sessionPolicy  *fallback.SessionPolicy
	skillRegistry  skills.Catalog
	agentRegistry  coderagents.SpecProvider
	mcpManager     *fridaymcp.Manager
	lifecycle      sessions.SessionLifecycle
	workdir        string
	configTools    bool
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

// WithModelPool supplies the immutable configured model catalog used when
// creating per-agent client views.
func WithModelPool(pool *fallback.ModelPool) Option {
	return func(o *options) { o.modelPool = pool }
}

// WithSessionPolicy supplies the mutable policy shared by the root client and
// every agent view in this Session.
func WithSessionPolicy(policy *fallback.SessionPolicy) Option {
	return func(o *options) { o.sessionPolicy = policy }
}

// WithSkillRegistry supplies a shared skills registry. Interactive runtimes
// use this so skill discovery, slash expansion, and agent hooks observe the
// same refreshable snapshot.
func WithSkillRegistry(registry skills.Catalog) Option {
	return func(o *options) {
		o.skillRegistry = registry
	}
}

// WithAgentRegistry supplies the process-shared catalog of disk-defined
// agents. Callers that omit it get an mtime-refreshable filesystem catalog.
func WithAgentRegistry(registry coderagents.SpecProvider) Option {
	return func(o *options) { o.agentRegistry = registry }
}

// WithMCPManager supplies a process-shared MCP manager. Its lifecycle remains
// owned by the caller (normally actor.Registry).
func WithMCPManager(manager *fridaymcp.Manager) Option {
	return func(o *options) { o.mcpManager = manager }
}

// WithWorkdir pins all filesystem and process tools to one canonical runtime
// root. Interactive project callers should always set it explicitly.
func WithWorkdir(workdir string) Option {
	return func(o *options) { o.workdir = workdir }
}

// WithConfigTools exposes agent_config and mcp_config. Interactive coder
// entrypoints enable it explicitly; automation remains read-only by default.
func WithConfigTools(enabled bool) Option {
	return func(o *options) { o.configTools = enabled }
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
	if options.sessionPolicy == nil && options.providerClient != nil {
		if view, ok := options.providerClient.(interface {
			SessionPolicy() *fallback.SessionPolicy
		}); ok {
			options.sessionPolicy = view.SessionPolicy()
		}
	}
	if options.sessionPolicy == nil {
		options.sessionPolicy = fallback.NewSessionPolicy(providers.ClientPolicy{})
	}

	var client providers.Client
	if options.providerClient != nil {
		client = options.providerClient
	} else if options.modelPool != nil {
		client = options.modelPool.NewClient(options.sessionPolicy, providers.ClientPolicy{})
	} else {
		pool, err := CreateModelPool(cfg)
		if err != nil {
			return nil, fmt.Errorf("create provider model pool: %w", err)
		}
		options.modelPool = pool
		client = pool.NewClient(options.sessionPolicy, providers.ClientPolicy{})
	}

	if options.lifecycle == nil {
		sessionMgr.SetLLM(client)
	}

	ws := workspace.NewFromConfig(cfg)
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
	if provider, ok := sessionMgr.(interface{ GetStore() sessions.Store }); ok {
		if meta, metaErr := provider.GetStore().GetMeta(sess.Root.ID); metaErr == nil && meta != nil {
			policy := providers.ClientPolicy{}
			if cfg.HasModelName(meta.Runtime.Model.Model) {
				policy.PreferredModel = meta.Runtime.Model.Model
			}
			if providers.IsValidReasoningEffort(meta.Runtime.Effort) {
				policy.Effort = meta.Runtime.Effort
			}
			options.sessionPolicy.Update(policy)
		}
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
		logger.New("setup").Warnw("failed to ensure memory log", "error", err)
	}

	loaded, err := ws.Load(workspace.WithMemoryDays(cfg.Memory.Days))
	if err != nil {
		return nil, fmt.Errorf("load workspace content: %w", err)
	}
	agentRegistry := options.agentRegistry
	if agentRegistry == nil {
		agentRegistry, err = coderagents.NewLoader(cfg.AgentPaths()...).Load()
		if err != nil {
			return nil, fmt.Errorf("load agents: %w", err)
		}
	}
	validateAgentSpec := func(spec *coderagents.AgentSpec) error {
		if spec != nil && spec.Model != "" && !cfg.HasModelName(spec.Model) {
			return fmt.Errorf("agent %q in %s selects unknown model %q", spec.Name, spec.SourcePath, spec.Model)
		}
		return nil
	}
	if registry, ok := agentRegistry.(interface {
		SetValidator(coderagents.SpecValidator) error
	}); ok {
		if err := registry.SetValidator(validateAgentSpec); err != nil {
			return nil, err
		}
	} else {
		for _, spec := range agentRegistry.List() {
			if err := validateAgentSpec(spec); err != nil {
				return nil, err
			}
		}
	}

	workdir := options.workdir
	if workdir == "" {
		workdir, _ = os.Getwd()
	}
	mcpManager := options.mcpManager
	ownsMCP := false
	if mcpManager == nil {
		mcpManager, err = fridaymcp.NewManager(fridaymcp.ManagerConfig{
			ConfigRoots: ws.MCPRoots(),
			ProjectRoot: workdir, CacheRoot: cfg.CachesPath(),
			TrustPath: filepath.Join(cfg.StatePath(), "mcp_trust.json"),
		})
		if err != nil {
			logger.New("setup").Warnw("failed to load MCP configuration", "error", err)
			mcpManager = nil
		} else {
			ownsMCP = true
			mcpManager.Warmup(context.Background())
		}
	}
	mcpHandedOff := false
	defer func() {
		if ownsMCP && !mcpHandedOff && mcpManager != nil {
			_ = mcpManager.Close()
		}
	}()

	planningHook := planning.New(planning.Option{ModeProvider: collaborationProvider(sessionMgr)})
	collaborationHook := collaboration.NewHook(collaborationProvider(sessionMgr), cfg.Collaboration.Plan.ReasoningEffort)
	skillRegistry := options.skillRegistry
	if skillRegistry == nil {
		skillLoader := skills.NewLoader(ws.SkillsPaths()...)
		if err := skillLoader.Load(); err != nil {
			logger.New("setup").Warnw("failed to load skills", "error", err)
		}
		skillRegistry = skills.NewRegistry(skillLoader)
	}
	skillHook := skills.NewHook(skillRegistry)
	var configToolsHook *configtools.Hook
	if options.configTools {
		configToolsHook = configtools.NewHook(configtools.NewFileStore(cfg.AgentPaths(), ws.MCPRoots(), cfg.HasModelName))
	}

	approvedPlanHook := planning.NewApprovedPlanContextHook(planRepositoryFromManager(sessionMgr))
	loopHook := coderloop.NewHook()
	refocusHook := contextmgr.NewRefocusHook()

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
	contextHook := contextmgr.New(client, contextmgr.Config{
		ContextWindow:      cfg.PrimaryModel().ContextWindow,
		SessionMemoryStore: sessionMemoryStoreFromManager(sessionMgr),
		ReservedTokens: func(sess *coreSession.Session) int64 {
			return fileHook.ReservedTokens(sess) + approvedPlanHook.ReservedTokens(sess)
		},
	})
	_ = fileHook.BeforeAgent(context.Background(), sess, nil)
	allTools = append(allTools, fileHook.Tools()...)
	imageTool := sandbox.NewImageTool(sandboxExec, workdir, newImageAnalyzer(cfg))
	allTools = append(allTools, imageTool)
	bashTool := sandbox.NewBashTool(sandboxExec, workdir)
	allTools = append(allTools, bashTool)
	taskManager, err := sandbox.NewPersistentTaskManager(sandboxExec, sandbox.NewSessionTaskStore(sess))
	if err != nil {
		return nil, fmt.Errorf("restore background tasks: %w", err)
	}
	bgTools := sandbox.NewBackgroundTaskTools(taskManager, workdir)
	allTools = append(allTools, bgTools...)

	if len(options.extraTools) > 0 {
		allTools = append(allTools, options.extraTools...)
	}

	// Every tool, including tools injected later by hooks, goes through this
	// shared invocation pipeline. Retry is outermost and trace records each
	// individual attempt.
	toolInvoker := tools.NewInvoker(tools.WithInvocationMiddleware(
		toolInvocationRetryMiddleware(defaultToolInvocationPolicy),
		toolTraceMiddleware(toolTraceSinkForConfig(cfg)),
	))

	workspacePrompt := workspace.ComposeSystemPrompt(loaded)
	primaryAgent := agents.New(client, agents.Option{
		SystemPrompt: workspacePrompt,
		Tools:        allTools,
		Invoker:      toolInvoker,
	})

	// Build the explorer with a filtered tool set. It reuses the main system
	// prompt so forked sessions share the same cache prefix.
	factory := coderagents.NewClientFactory(client)
	factory.SetInvoker(toolInvoker)
	exploreSpec := coderagents.ExplorerSpec()
	exploreSpec.SystemPrompt = workspace.ComposeSystemPrompt(loaded)
	exploreAgent, err := factory.BuildAgent(exploreSpec, allTools)
	if err != nil {
		return nil, fmt.Errorf("build explore agent: %w", err)
	}

	expertProvider, err := coderagents.NewExpertProvider(agentRegistry, factory, workspacePrompt, allTools, validateAgentSpec)
	if err != nil {
		return nil, fmt.Errorf("build expert agents: %w", err)
	}

	subagentHook := subagents.NewHook(client, subagents.Option{
		SelfAgent: &subagents.ExpertAgent{
			Name:  coderagents.NameExplorer,
			Agent: exploreAgent,
		},
		AgentProvider: expertProvider,
		SessionForker: lifecycle,
	})
	routedAgent := coderagents.NewDynamicRouter(primaryAgent, expertProvider)

	sharedHooks := []coreSession.Hook{
		planningHook,
		fridaymcp.NewHook(mcpManager),
		skillHook,
		// Memory must be injected before the context manager runs so its
		// projection accounts for the extra per-request messages.
		newMemoryHook(ws),
		contextHook,
		refocusHook,
		// Stable project context is rebuilt after projection so compaction
		// cannot discard it. The accepted plan is appended after it.
		fileHook,
		approvedPlanHook,
		subagentHook,
		// Keep collaboration instructions last so Plan Mode remains the
		// highest-precedence request-scoped behavioral contract.
		collaborationHook,
		planning.TerminalHook{ModeProvider: collaborationProvider(sessionMgr)},
	}
	if configToolsHook != nil {
		// Configuration tools are appended at a stable final position and are
		// inherited by forked sessions, as required by coder mode.
		sharedHooks = append(sharedHooks, configToolsHook)
	}
	replaceSessionHooks(sess, sharedHooks...)

	// Loop is last so its stable autonomous-work contract has the final
	// system-prompt position while active. It is otherwise a no-op.
	sess.RegisterHook(loopHook)

	mcpHandedOff = true
	return &AgentContext{
		Client:      client,
		Policy:      options.sessionPolicy,
		Workspace:   ws,
		Lifecycle:   lifecycle,
		Session:     sess,
		Agent:       routedAgent,
		Memory:      memSys,
		TaskManager: taskManager,
		mcpManager:  mcpManager,
		ownsMCP:     ownsMCP,
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
	if ac.ownsMCP && ac.mcpManager != nil {
		_ = ac.mcpManager.Close()
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
