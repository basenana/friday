package codebase

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"

	eventbus "github.com/hyponet/eventbus/bus"

	"github.com/basenana/friday/bus"
	coderloop "github.com/basenana/friday/coder/loop"
	"github.com/basenana/friday/coder/project"
	"github.com/basenana/friday/core/actor/events"
	"github.com/basenana/friday/core/planning"
	"github.com/basenana/friday/core/providers/fallback"
	coresession "github.com/basenana/friday/core/session"
	"github.com/basenana/friday/core/types"
	"github.com/basenana/friday/sandbox"
	"github.com/basenana/friday/sessions"
)

type Options struct {
	DataDir        string
	Project        *project.Project
	ProjectManager *project.Manager
	ModelPool      *fallback.ModelPool
	Bus            *eventbus.Bus
	Sandbox        *sandbox.Config
}

type Runtime struct {
	opts     Options
	store    *store
	runner   *runner
	activity *activityPublisher

	ctx    context.Context
	cancel context.CancelFunc
	wg     sync.WaitGroup
	wake   chan struct{}

	commandMu          sync.Mutex
	mu                 sync.Mutex
	started            bool
	closing            bool
	closed             bool
	initialized        bool
	enabled            bool
	spec               Spec
	specErr            error
	status             Status
	pending            map[string]bool
	failureBlocked     bool
	indexCancel        context.CancelFunc
	indexDone          chan struct{}
	indexSessionID     string
	activeSessionID    string
	switching          bool
	mutationTarget     string
	hooks              map[*coresession.Session]*Hook
	conversationFeed   *bus.Feed
	conversationCancel context.CancelFunc
	conversationDone   chan struct{}
	contextCancels     map[string]context.CancelFunc
	contextWG          sync.WaitGroup
	pairs              []conversationPair
	submitted          []conversationPair
	runs               map[string]*conversationRun
	activeTurns        int
	idleTimer          *time.Timer
	closeWait          time.Duration
	closeDone          chan struct{}
	closeErr           error
}

type conversationPair struct{ User, Assistant, SessionID string }
type conversationRun struct {
	user      string
	assistant strings.Builder
	failed    bool
}

type SessionTransition struct {
	runtime         *Runtime
	target          string
	preserveBarrier bool
	once            sync.Once
}

func (t *SessionTransition) Commit() {
	if t != nil {
		t.once.Do(func() { t.runtime.commitSessionSwitch(t.target, t.preserveBarrier) })
	}
}
func (t *SessionTransition) Abort() {
	if t != nil {
		t.once.Do(func() { t.runtime.abortSessionSwitch(t.preserveBarrier) })
	}
}

type IndexSessionMutation struct {
	runtime *Runtime
	target  string
	once    sync.Once
}

func (m *IndexSessionMutation) Commit() {
	if m != nil {
		m.once.Do(func() { m.runtime.finishIndexSessionMutation(m.target) })
	}
}

func (m *IndexSessionMutation) Abort() { m.Commit() }

func New(opts Options) (*Runtime, error) {
	if opts.Project == nil || opts.ProjectManager == nil || opts.ModelPool == nil || opts.Bus == nil {
		return nil, fmt.Errorf("Codebase requires project, manager, model pool, and bus")
	}
	s := newStore(opts.DataDir, opts.Project.ID())
	ctx, cancel := context.WithCancel(context.Background())
	rt := &Runtime{opts: opts, store: s, activity: newActivityPublisher(opts.Bus, opts.Project.ID()), ctx: ctx, cancel: cancel, wake: make(chan struct{}, 1), pending: map[string]bool{}, hooks: map[*coresession.Session]*Hook{}, runs: map[string]*conversationRun{}, contextCancels: map[string]context.CancelFunc{}, closeWait: 10 * time.Second}
	return rt, nil
}

func (r *Runtime) ensureRunner() error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.runner != nil {
		r.initialized = true
		return nil
	}
	runner, err := newRunner(r.opts.ModelPool, r.opts.Sandbox, r.opts.Project.Root(), r.store.dir)
	if err != nil {
		return err
	}
	r.runner = runner
	r.initialized = true
	return nil
}

func (r *Runtime) Start(ctx context.Context, activeSessionID string) error {
	r.commandMu.Lock()
	defer r.commandMu.Unlock()
	r.mu.Lock()
	if r.closing || r.closed {
		r.mu.Unlock()
		return fmt.Errorf("Codebase runtime is closed")
	}
	if r.started {
		r.mu.Unlock()
		return nil
	}
	r.mu.Unlock()
	enabled, err := r.opts.Project.CodebaseEnabled()
	if err != nil {
		return err
	}
	r.mu.Lock()
	r.started = true
	r.activeSessionID = activeSessionID
	r.mu.Unlock()
	ready := false
	if enabled {
		if err := r.initializeEnabled(ctx); err != nil {
			r.mu.Lock()
			r.enabled = false
			r.specErr = err
			r.status = defaultStatus(StateError)
			r.status.LastError = err.Error()
			initialized := r.initialized
			status := cloneStatus(r.status)
			r.mu.Unlock()
			if initialized {
				written, writeErr := r.store.writeStatus(status)
				r.mu.Lock()
				if writeErr != nil {
					r.status.LastError = boundText(errors.Join(err, writeErr).Error(), 2000)
				} else {
					r.status = written
				}
				r.mu.Unlock()
			}
		} else {
			ready = true
			r.trigger("startup")
		}
	}
	r.wg.Add(1)
	go r.scheduler()
	if ready {
		r.startConversation(activeSessionID)
		r.resetIdle()
	}
	return nil
}

func (r *Runtime) initializeEnabled(ctx context.Context) error {
	if err := r.store.ensureLayout(); err != nil {
		return err
	}
	if err := r.ensureRunner(); err != nil {
		return err
	}
	spec, err := loadSpec(r.store.specPath(), r.opts.ModelPool)
	if err != nil {
		return err
	}
	status, err := r.store.loadStatus()
	if err != nil {
		return err
	}
	id, err := r.ensureIndexSession(ctx, spec)
	if err != nil {
		return err
	}
	r.mu.Lock()
	r.enabled = true
	r.spec = spec
	r.specErr = nil
	r.status = status
	r.indexSessionID = id
	r.mu.Unlock()
	return nil
}

func (r *Runtime) enable(ctx context.Context) error {
	if err := r.store.ensureLayout(); err != nil {
		return err
	}
	if err := r.ensureRunner(); err != nil {
		return err
	}
	spec, err := loadSpec(r.store.specPath(), r.opts.ModelPool)
	if err != nil {
		return err
	}
	status, err := r.store.loadStatus()
	if err != nil {
		return err
	}
	id, err := r.ensureIndexSession(ctx, spec)
	if err != nil {
		return err
	}
	if err := r.opts.Project.SetCodebaseEnabled(true); err != nil {
		return err
	}
	r.mu.Lock()
	r.enabled = true
	r.spec = spec
	r.specErr = nil
	r.status = status
	r.indexSessionID = id
	active := r.activeSessionID
	r.mu.Unlock()
	r.startConversation(active)
	r.resetIdle()
	return nil
}

func (r *Runtime) ensureIndexSession(ctx context.Context, spec Spec) (string, error) {
	client := r.runner.client(spec.Index)
	if id, err := r.store.readSession(); err == nil {
		if lifecycle, openErr := r.opts.ProjectManager.OpenRoot(ctx, id, client); openErr == nil {
			if closeErr := lifecycle.Close(); closeErr != nil {
				return "", fmt.Errorf("close Codebase Index session: %w", closeErr)
			}
			return id, nil
		}
	}
	lifecycle, err := r.opts.ProjectManager.CreateRoot(ctx, client)
	if err != nil {
		return "", err
	}
	id := lifecycle.RootID()
	closed := false
	cleanup := func(primary error) error {
		var closeErr error
		if !closed {
			closeErr = lifecycle.Close()
			closed = true
		}
		deleteErr := r.opts.ProjectManager.DeleteRoot(id)
		return errors.Join(primary, closeErr, deleteErr)
	}
	if _, err := r.opts.ProjectManager.Rename(id, "Codebase Index"); err != nil {
		return "", cleanup(fmt.Errorf("rename Codebase Index session: %w", err))
	}
	if err := lifecycle.Close(); err != nil {
		closed = true
		return "", cleanup(fmt.Errorf("close Codebase Index session: %w", err))
	}
	closed = true
	if err := r.store.writeSession(id); err != nil {
		return "", cleanup(fmt.Errorf("write Codebase SESSION: %w", err))
	}
	return id, nil
}

func (r *Runtime) Attach(lifecycle sessions.SessionLifecycle) error {
	if lifecycle == nil || lifecycle.Current() == nil {
		return fmt.Errorf("Codebase lifecycle is required")
	}
	root := lifecycle.Current()
	r.mu.Lock()
	if _, ok := r.hooks[root]; ok {
		r.mu.Unlock()
		return nil
	}
	h := &Hook{runtime: r, root: root}
	r.hooks[root] = h
	r.mu.Unlock()
	root.RegisterHook(h)
	return nil
}

func (r *Runtime) Detach(lifecycle sessions.SessionLifecycle) {
	if lifecycle == nil || lifecycle.Current() == nil {
		return
	}
	r.mu.Lock()
	delete(r.hooks, lifecycle.Current())
	r.mu.Unlock()
}

func (r *Runtime) PrepareSessionSwitch(ctx context.Context, targetID string) (*SessionTransition, error) {
	r.mu.Lock()
	if r.closing || r.closed {
		r.mu.Unlock()
		return nil, fmt.Errorf("Codebase runtime is closed")
	}
	preserveBarrier := r.switching && r.mutationTarget != "" && r.mutationTarget == r.indexSessionID && r.activeSessionID == r.indexSessionID && targetID != r.indexSessionID
	if r.switching && !preserveBarrier {
		r.mu.Unlock()
		return nil, fmt.Errorf("Codebase session transition already in progress")
	}
	r.switching = true
	cancel, done := r.indexCancel, r.indexDone
	indexID := r.indexSessionID
	r.mu.Unlock()
	if targetID == indexID && cancel != nil {
		cancel()
		select {
		case <-done:
		case <-ctx.Done():
			r.abortSessionSwitch(preserveBarrier)
			return nil, ctx.Err()
		}
	}
	return &SessionTransition{runtime: r, target: targetID, preserveBarrier: preserveBarrier}, nil
}

func (r *Runtime) PrepareIndexSessionMutation(ctx context.Context, targetID string) (*IndexSessionMutation, error) {
	r.mu.Lock()
	if r.closing || r.closed {
		r.mu.Unlock()
		return nil, fmt.Errorf("Codebase runtime is closed")
	}
	if targetID == "" || targetID != r.indexSessionID {
		r.mu.Unlock()
		return nil, fmt.Errorf("session %s is not the managed Codebase Index session", targetID)
	}
	if r.switching {
		r.mu.Unlock()
		return nil, fmt.Errorf("Codebase session transition already in progress")
	}
	r.switching = true
	r.mutationTarget = targetID
	cancel, done := r.indexCancel, r.indexDone
	r.mu.Unlock()
	if cancel != nil {
		cancel()
		select {
		case <-done:
		case <-ctx.Done():
			r.finishIndexSessionMutation(targetID)
			return nil, ctx.Err()
		}
	}
	return &IndexSessionMutation{runtime: r, target: targetID}, nil
}

func (r *Runtime) finishIndexSessionMutation(target string) {
	r.mu.Lock()
	if r.mutationTarget == target {
		r.mutationTarget = ""
		r.switching = false
	}
	r.mu.Unlock()
	r.signalWake()
}

func (r *Runtime) commitSessionSwitch(target string, preserveBarrier bool) {
	r.mu.Lock()
	r.activeSessionID = target
	if !preserveBarrier {
		r.switching = false
	}
	r.pairs = nil
	r.runs = map[string]*conversationRun{}
	r.activeTurns = 0
	enabled := r.enabled
	blocked := target == r.indexSessionID || preserveBarrier
	r.mu.Unlock()
	r.stopConversation()
	if enabled {
		r.startConversation(target)
		r.resetIdle()
	}
	if !blocked {
		r.signalWake()
	}
}
func (r *Runtime) abortSessionSwitch(preserveBarrier bool) {
	r.mu.Lock()
	if !preserveBarrier {
		r.switching = false
	}
	r.mu.Unlock()
	if !preserveBarrier {
		r.signalWake()
	}
}

func (r *Runtime) Enabled() bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.enabled && !r.closing && !r.closed
}
func (r *Runtime) Status() Status               { r.mu.Lock(); defer r.mu.Unlock(); return cloneStatus(r.status) }
func (r *Runtime) IndexSessionID() string       { r.mu.Lock(); defer r.mu.Unlock(); return r.indexSessionID }
func (r *Runtime) ActivityFeed() *bus.Feed      { return r.activity.feed() }
func (r *Runtime) ActivitySnapshot() []Activity { return r.activity.snapshot() }

func (r *Runtime) RequestIndex(ctx context.Context) error {
	r.commandMu.Lock()
	defer r.commandMu.Unlock()
	r.mu.Lock()
	if r.closing || r.closed {
		r.mu.Unlock()
		return fmt.Errorf("Codebase runtime is closed")
	}
	r.mu.Unlock()
	r.touchUserActivity()
	r.mu.Lock()
	enabled := r.enabled
	r.mu.Unlock()
	if !enabled {
		if err := r.enable(ctx); err != nil {
			return err
		}
	}
	if err := r.reloadSpec(); err != nil {
		return err
	}
	r.trigger("manual")
	return nil
}

func (r *Runtime) Disable(ctx context.Context) error {
	r.commandMu.Lock()
	defer r.commandMu.Unlock()
	r.mu.Lock()
	if r.closing || r.closed {
		r.mu.Unlock()
		return fmt.Errorf("Codebase runtime is closed")
	}
	r.mu.Unlock()
	if err := r.opts.Project.SetCodebaseEnabled(false); err != nil {
		return err
	}
	r.mu.Lock()
	r.enabled = false
	r.pending = map[string]bool{}
	r.pairs = nil
	r.submitted = nil
	r.runs = map[string]*conversationRun{}
	cancel := r.indexCancel
	indexDone := r.indexDone
	contextCancels := make([]context.CancelFunc, 0, len(r.contextCancels))
	for _, contextCancel := range r.contextCancels {
		contextCancels = append(contextCancels, contextCancel)
	}
	timer := r.idleTimer
	r.idleTimer = nil
	r.mu.Unlock()
	if timer != nil {
		timer.Stop()
	}
	if cancel != nil {
		cancel()
	}
	for _, contextCancel := range contextCancels {
		contextCancel()
	}
	r.stopConversation()
	if ctx == nil {
		ctx = context.Background()
	}
	contextDone := make(chan struct{})
	go func() {
		r.contextWG.Wait()
		close(contextDone)
	}()
	for _, done := range []<-chan struct{}{indexDone, contextDone} {
		if done == nil {
			continue
		}
		select {
		case <-done:
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	return nil
}

func (r *Runtime) reloadSpec() error {
	spec, err := loadSpec(r.store.specPath(), r.opts.ModelPool)
	r.mu.Lock()
	if err != nil {
		r.specErr = err
		r.status.State = StateError
		r.status.LastError = boundText(err.Error(), 2000)
		initialized := r.initialized
		status := cloneStatus(r.status)
		r.mu.Unlock()
		if initialized {
			if writeErr := r.persistStatus(status); writeErr != nil {
				return errors.Join(err, fmt.Errorf("persist invalid Codebase spec status: %w", writeErr))
			}
		}
		return err
	}
	recovered := r.specErr != nil
	r.spec = spec
	r.specErr = nil
	initialized := r.initialized
	status := cloneStatus(r.status)
	if recovered {
		status.State = StateDegraded
		status.LastError = ""
	}
	r.mu.Unlock()
	if recovered && initialized {
		if err := r.persistStatus(status); err != nil {
			return fmt.Errorf("persist recovered Codebase spec status: %w", err)
		}
	}
	return nil
}

func (r *Runtime) RefreshStatus() (Status, error) {
	r.commandMu.Lock()
	defer r.commandMu.Unlock()
	r.mu.Lock()
	if r.closing || r.closed {
		r.mu.Unlock()
		return Status{}, fmt.Errorf("Codebase runtime is closed")
	}
	enabled := r.enabled
	r.mu.Unlock()
	if !enabled {
		return r.Status(), nil
	}
	err := r.reloadSpec()
	return r.Status(), err
}

func (r *Runtime) trigger(reason string) {
	r.mu.Lock()
	if !r.enabled || r.closing || r.closed {
		r.mu.Unlock()
		return
	}
	r.requestTriggerLocked(reason)
	r.mu.Unlock()
	r.signalWake()
}

func (r *Runtime) requestTriggerLocked(reason string) {
	r.pending[reason] = true
	r.failureBlocked = false
	if r.status.LastRequested == nil {
		r.status.LastRequested = map[string]time.Time{}
	}
	r.status.LastRequested[reason] = time.Now()
}
func (r *Runtime) signalWake() {
	select {
	case r.wake <- struct{}{}:
	default:
	}
}

func (r *Runtime) scheduler() {
	defer r.wg.Done()
	for {
		select {
		case <-r.ctx.Done():
			return
		case <-r.wake:
		}
		for {
			r.mu.Lock()
			if !r.enabled || r.closing || r.closed || r.failureBlocked || r.switching || r.activeSessionID == r.indexSessionID || len(r.pending) == 0 || r.indexCancel != nil {
				r.mu.Unlock()
				break
			}
			reasons := make([]string, 0, len(r.pending))
			for reason := range r.pending {
				reasons = append(reasons, reason)
			}
			sort.Strings(reasons)
			r.pending = map[string]bool{}
			ctx, cancel := context.WithCancel(r.ctx)
			done := make(chan struct{})
			r.indexCancel = cancel
			r.indexDone = done
			r.mu.Unlock()
			err := r.runIndex(ctx, reasons)
			cancel()
			close(done)
			r.mu.Lock()
			r.indexCancel = nil
			r.indexDone = nil
			if err != nil && r.enabled && !r.closing && !r.closed {
				for _, reason := range reasons {
					r.pending[reason] = true
				}
				if !errors.Is(err, context.Canceled) {
					r.failureBlocked = true
				}
			}
			more := len(r.pending) > 0 && !r.failureBlocked
			r.mu.Unlock()
			if !more {
				break
			}
		}
	}
}

func (r *Runtime) runIndex(ctx context.Context, reasons []string) error {
	operationID := types.NewID()
	started := time.Now()
	r.activity.publish(Activity{OperationID: operationID, Mode: ModeIndex, State: ActivityRunning, Phase: "prepare", Summary: "Indexing codebase", Trigger: strings.Join(reasons, ","), StartedAt: started})
	terminal := func(state ActivityState, err error) {
		a := Activity{OperationID: operationID, Mode: ModeIndex, State: state, Summary: "Codebase index finished", Trigger: strings.Join(reasons, ","), StartedAt: started, CompletedAt: time.Now()}
		if err != nil {
			a.Error = boundText(err.Error(), 2000)
		}
		r.activity.publish(a)
	}
	if err := r.reloadSpec(); err != nil {
		terminal(ActivityFailed, err)
		return err
	}
	r.mu.Lock()
	spec := r.spec
	status := cloneStatus(r.status)
	batch := append([]conversationPair(nil), r.submitted...)
	id := r.indexSessionID
	r.mu.Unlock()
	status.State = StateIndexing
	status.OperationID = operationID
	status.OperationStartedAt = started
	status.TriggerReasons = append([]string(nil), reasons...)
	if status.LastAttempted == nil {
		status.LastAttempted = map[string]time.Time{}
	}
	for _, reason := range reasons {
		status.LastAttempted[reason] = started
	}
	if err := r.persistStatus(status); err != nil {
		err = fmt.Errorf("persist indexing status: %w", err)
		terminal(ActivityFailed, err)
		return err
	}
	git, err := collectGitState(ctx, r.runner.exec, r.opts.Project.Root(), spec.Schedule.CoveragePageSize, status)
	if err != nil {
		return r.finishIndexError(status, terminal, err)
	}
	status = observeGitState(status, git)
	approvedPlan := ""
	if len(batch) > 0 {
		plan, planErr := r.opts.ProjectManager.LoadLatestPlan(batch[0].SessionID)
		if planErr != nil {
			return r.finishIndexError(status, terminal, fmt.Errorf("load conversation Approved Plan: %w", planErr))
		}
		if plan != nil && plan.Status == planning.ArtifactAccepted {
			approvedPlan = plan.Markdown
		}
	}
	payload := renderIndexInput(git, batch, reasons)
	client := r.runner.client(spec.Index)
	lifecycle, err := r.opts.ProjectManager.OpenRoot(ctx, id, client, r.runner.indexOptions(client, payload, approvedPlan)...)
	if err != nil {
		id, err = r.ensureIndexSession(ctx, spec)
		if err == nil {
			r.mu.Lock()
			r.indexSessionID = id
			r.mu.Unlock()
			lifecycle, err = r.opts.ProjectManager.OpenRoot(ctx, id, client, r.runner.indexOptions(client, payload, approvedPlan)...)
		}
	}
	if err != nil {
		return r.finishIndexError(status, terminal, err)
	}
	_, err = r.runner.runIndex(ctx, lifecycle, client, spec)
	closeErr := lifecycle.Close()
	if err == nil {
		err = closeErr
	}
	if err == nil {
		err = ctx.Err()
	}
	if err != nil {
		return r.finishIndexError(status, terminal, err)
	}
	status, err = r.indexCommitStatus(operationID)
	if err != nil {
		terminal(ActivityFailed, err)
		return err
	}
	status.State = StateIdle
	status.OperationID = ""
	status.OperationStartedAt = time.Time{}
	status.TriggerReasons = nil
	status.LastSuccessfulIndex = time.Now()
	status.LastError = ""
	status.LastCancellation = ""
	status.ObservedHEAD = git.HEAD
	status.IndexedHEAD = git.HEAD
	status.Branch = git.Branch
	status.Detached = git.Detached
	status.Dirty = git.Dirty
	status.DirtyHash = git.DirtyHash
	status = advanceCoverage(status, git)
	if err := r.persistStatus(status); err != nil {
		err = fmt.Errorf("persist successful index status: %w", err)
		terminal(ActivityFailed, err)
		return err
	}
	r.mu.Lock()
	if len(batch) > 0 {
		r.submitted = nil
	}
	r.mu.Unlock()
	terminal(ActivitySucceeded, nil)
	r.resetIdle()
	return nil
}

func (r *Runtime) indexCommitStatus(operationID string) (Status, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.specErr != nil {
		return Status{}, r.specErr
	}
	if r.status.OperationID != operationID {
		return Status{}, fmt.Errorf("stale Codebase index operation %s", operationID)
	}
	return cloneStatus(r.status), nil
}

func (r *Runtime) persistStatus(status Status) error {
	r.mu.Lock()
	status.LastRequested = cloneTimes(r.status.LastRequested)
	written, err := r.store.writeStatus(status)
	if err != nil {
		r.mu.Unlock()
		return err
	}
	r.status = cloneStatus(written)
	r.mu.Unlock()
	return nil
}

func (r *Runtime) finishIndexError(status Status, terminal func(ActivityState, error), err error) error {
	current, commitErr := r.indexCommitStatus(status.OperationID)
	if commitErr != nil {
		err = errors.Join(err, commitErr)
		terminal(ActivityFailed, err)
		return err
	}
	status = current
	activityState := ActivityFailed
	status.State = StateError
	status.LastError = boundText(err.Error(), 2000)
	if errors.Is(err, context.Canceled) {
		activityState = ActivityCancelled
		status.State = StateDegraded
		status.LastCancellation = status.LastError
	}
	status.OperationID = ""
	status.OperationStartedAt = time.Time{}
	status.TriggerReasons = nil
	if writeErr := r.persistStatus(status); writeErr != nil {
		err = errors.Join(err, fmt.Errorf("persist failed index status: %w", writeErr))
	}
	terminal(activityState, err)
	return err
}

func renderIndexInput(g gitState, batch []conversationPair, reasons []string) string {
	var b strings.Builder
	fmt.Fprintf(&b, "Codebase maintenance trigger: %s\nProject root: repository working directory\n", strings.Join(reasons, ", "))
	if g.IsGit {
		fmt.Fprintf(&b, "Git HEAD: %s\nBranch: %s detached=%v dirty=%v dirty_hash=%s\nChanged paths:\n%s\nCoverage candidates:\n%s\n", g.HEAD, g.Branch, g.Detached, g.Dirty, g.DirtyHash, strings.Join(g.Changed, "\n"), strings.Join(g.Candidates, "\n"))
	} else {
		b.WriteString("Git state unavailable: this is not a Git project. Inspect current files directly.\n")
	}
	if len(batch) > 0 {
		b.WriteString("Recent conversation batch (fallible context):\n")
		for _, p := range batch {
			fmt.Fprintf(&b, "User: %s\nAssistant: %s\n", p.User, p.Assistant)
		}
	}
	return b.String()
}

func (r *Runtime) startConversation(sessionID string) {
	if sessionID == "" {
		return
	}
	r.mu.Lock()
	if !r.enabled || r.closing || r.closed {
		r.mu.Unlock()
		return
	}
	ctx, cancel := context.WithCancel(r.ctx)
	feed := bus.SubscribeAgentFeed(r.opts.Bus, sessionID)
	done := make(chan struct{})
	r.conversationFeed = feed
	r.conversationCancel = cancel
	r.conversationDone = done
	r.mu.Unlock()
	r.wg.Add(1)
	go func() {
		defer r.wg.Done()
		defer close(done)
		defer feed.Close()
		for {
			select {
			case <-ctx.Done():
				return
			case <-feed.Done():
				return
			case evt := <-feed.Events():
				r.consumeConversationEvent(sessionID, evt)
			}
		}
	}()
}
func (r *Runtime) stopConversation() {
	r.mu.Lock()
	cancel := r.conversationCancel
	feed := r.conversationFeed
	done := r.conversationDone
	r.conversationCancel = nil
	r.conversationFeed = nil
	r.conversationDone = nil
	r.mu.Unlock()
	if cancel != nil {
		cancel()
	}
	if feed != nil {
		feed.Close()
	}
	if done != nil {
		<-done
	}
}

func (r *Runtime) consumeConversationEvent(sessionID string, evt events.Event) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if !r.enabled || sessionID != r.activeSessionID || len(r.submitted) > 0 {
		return
	}
	run := r.runs[evt.RunID]
	switch evt.Type {
	case events.KindCustom:
		if evt.Name != events.CustomInputAccepted {
			return
		}
		var body events.InputAcceptedBody
		if json.Unmarshal(evt.Payload, &body) != nil || body.Role != types.RoleUser || strings.TrimSpace(body.Text) == "" {
			return
		}
		r.activeTurns++
		if r.idleTimer != nil {
			r.idleTimer.Stop()
			r.idleTimer = nil
		}
		r.runs[evt.RunID] = &conversationRun{user: body.Text}
	case events.KindTextMessageContent:
		if run == nil {
			return
		}
		var body events.TextMessageContentData
		if json.Unmarshal(evt.Payload, &body) == nil {
			run.assistant.WriteString(body.Content)
		}
	case events.KindRunError:
		if run != nil {
			run.failed = true
		}
	case events.KindRunFinished:
		if run == nil {
			return
		}
		delete(r.runs, evt.RunID)
		if r.activeTurns > 0 {
			r.activeTurns--
		}
		if r.activeTurns == 0 {
			go r.resetIdle()
		}
		var body events.RunFinishedData
		_ = json.Unmarshal(evt.Payload, &body)
		answer := strings.TrimSpace(run.assistant.String())
		if run.failed || body.StopReason != "end_turn" || answer == "" {
			return
		}
		r.pairs = append(r.pairs, conversationPair{User: run.user, Assistant: answer, SessionID: sessionID})
		if len(r.pairs) >= r.spec.Schedule.ConversationPairs {
			r.submitted = append([]conversationPair(nil), r.pairs...)
			r.pairs = nil
			r.requestTriggerLocked("conversation")
			go r.signalWake()
		}
	}
}

func (r *Runtime) touchUserActivity() {
	r.mu.Lock()
	if r.idleTimer != nil {
		r.idleTimer.Stop()
		r.idleTimer = nil
	}
	r.mu.Unlock()
}
func (r *Runtime) resetIdle() {
	r.mu.Lock()
	defer r.mu.Unlock()
	if !r.enabled || r.closing || r.closed || r.activeTurns > 0 {
		return
	}
	if r.idleTimer != nil {
		r.idleTimer.Stop()
	}
	delay := r.spec.Schedule.IdleDelay.Duration
	if delay <= 0 {
		return
	}
	r.idleTimer = time.AfterFunc(delay, func() { r.trigger("idle") })
}

func (r *Runtime) contextEvidence(ctx context.Context, root *coresession.Session, history []types.Message, turn uint64) (string, error) {
	r.mu.Lock()
	enabled := r.enabled
	r.mu.Unlock()
	if !enabled {
		return unavailableEvidence("Codebase is disabled"), nil
	}
	if err := r.reloadSpec(); err != nil {
		return unavailableEvidence(err.Error()), nil
	}
	r.mu.Lock()
	spec := r.spec
	sessionID := root.ID
	r.mu.Unlock()
	op := types.NewID()
	opCtx, opCancel := context.WithCancel(ctx)
	r.mu.Lock()
	if !r.enabled || r.closing || r.closed {
		r.mu.Unlock()
		opCancel()
		return unavailableEvidence("Codebase is disabled"), nil
	}
	r.contextCancels[op] = opCancel
	r.contextWG.Add(1)
	r.mu.Unlock()
	defer func() {
		opCancel()
		r.mu.Lock()
		delete(r.contextCancels, op)
		r.mu.Unlock()
		r.contextWG.Done()
	}()
	started := time.Now()
	r.activity.publish(Activity{OperationID: op, Mode: ModeContext, State: ActivityRunning, Phase: "inspect", Summary: "Gathering codebase context", SessionID: sessionID, Turn: turn, StartedAt: started})
	mode := r.opts.ProjectManager.CollaborationMode(sessionID)
	loopState := "none"
	if raw, readErr := root.ReadRecord(opCtx, coderloop.StateNamespace); readErr == nil {
		loopState = strings.TrimSpace(string(raw))
	} else if !errors.Is(readErr, coresession.ErrRecordNotFound) {
		loopState = "unknown"
	}
	metadata := fmt.Sprintf("Gather repository evidence relevant to the current projected conversation.\nProject: %s\nSession: %s\nOperation: %s\nCollaboration mode: %s\nAutonomous loop state: %s", r.opts.Project.Root(), sessionID, op, mode, loopState)
	out, err := r.runner.runContext(opCtx, root, history, spec, metadata)
	if err != nil {
		if ctx.Err() != nil {
			r.activity.publish(Activity{OperationID: op, Mode: ModeContext, State: ActivityCancelled, Error: ctx.Err().Error(), SessionID: sessionID, Turn: turn, StartedAt: started, CompletedAt: time.Now()})
			return "", ctx.Err()
		}
		state := ActivityFailed
		if errors.Is(err, context.DeadlineExceeded) {
			state = ActivityTimedOut
		}
		r.activity.publish(Activity{OperationID: op, Mode: ModeContext, State: state, Error: err.Error(), SessionID: sessionID, Turn: turn, StartedAt: started, CompletedAt: time.Now()})
		return unavailableEvidence(err.Error()), nil
	}
	if strings.TrimSpace(out) == "" {
		out = unavailableEvidence("Context returned no evidence")
	}
	r.activity.publish(Activity{OperationID: op, Mode: ModeContext, State: ActivitySucceeded, Summary: "Codebase context ready", SessionID: sessionID, Turn: turn, StartedAt: started, CompletedAt: time.Now()})
	return out, nil
}

func (r *Runtime) queryContext(ctx context.Context, root *coresession.Session, query string, turn uint64, maxChars int64) (string, error) {
	if root == nil {
		return "", fmt.Errorf("Codebase query has no root Session")
	}
	if !r.Enabled() {
		return "", fmt.Errorf("Codebase is disabled")
	}
	if err := r.reloadSpec(); err != nil {
		return "", err
	}
	r.mu.Lock()
	spec := r.spec
	sessionID := root.ID
	if !r.enabled || r.closing || r.closed {
		r.mu.Unlock()
		return "", fmt.Errorf("Codebase is disabled")
	}
	r.mu.Unlock()

	op := types.NewID()
	opCtx, opCancel := context.WithCancel(ctx)
	r.mu.Lock()
	if !r.enabled || r.closing || r.closed {
		r.mu.Unlock()
		opCancel()
		return "", fmt.Errorf("Codebase is disabled")
	}
	r.contextCancels[op] = opCancel
	r.contextWG.Add(1)
	r.mu.Unlock()
	defer func() {
		opCancel()
		r.mu.Lock()
		delete(r.contextCancels, op)
		r.mu.Unlock()
		r.contextWG.Done()
	}()

	started := time.Now()
	r.activity.publish(Activity{OperationID: op, Mode: ModeContext, State: ActivityRunning, Phase: "query", Summary: "Querying codebase context", SessionID: sessionID, Turn: turn, StartedAt: started})
	mode := r.opts.ProjectManager.CollaborationMode(sessionID)
	loopState := "none"
	if raw, readErr := root.ReadRecord(opCtx, coderloop.StateNamespace); readErr == nil {
		loopState = strings.TrimSpace(string(raw))
	} else if !errors.Is(readErr, coresession.ErrRecordNotFound) {
		loopState = "unknown"
	}
	metadata := fmt.Sprintf("Project: %s\nSession: %s\nOperation: %s\nCollaboration mode: %s\nAutonomous loop state: %s", r.opts.Project.Root(), sessionID, op, mode, loopState)
	out, err := r.runner.runQuery(opCtx, root, spec, query, metadata, maxChars)
	if err != nil {
		state := ActivityFailed
		if errors.Is(err, context.DeadlineExceeded) {
			state = ActivityTimedOut
		} else if ctx.Err() != nil {
			state = ActivityCancelled
			err = ctx.Err()
		}
		r.activity.publish(Activity{OperationID: op, Mode: ModeContext, State: state, Error: err.Error(), SessionID: sessionID, Turn: turn, StartedAt: started, CompletedAt: time.Now()})
		return strings.TrimSpace(out), err
	}
	if strings.TrimSpace(out) == "" {
		return "", fmt.Errorf("Context Provider returned no answer")
	}
	r.activity.publish(Activity{OperationID: op, Mode: ModeContext, State: ActivitySucceeded, Summary: "Codebase query answered", SessionID: sessionID, Turn: turn, StartedAt: started, CompletedAt: time.Now()})
	return out, nil
}

func unavailableEvidence(reason string) string {
	return "Codebase evidence is unavailable. Investigate the repository with native tools before relying on assumptions. Reason: " + boundText(reason, 500)
}

func (r *Runtime) BeginShutdown() {
	r.commandMu.Lock()
	defer r.commandMu.Unlock()
	r.mu.Lock()
	if r.closing || r.closed {
		r.mu.Unlock()
		return
	}
	r.closing = true
	r.pending = map[string]bool{}
	r.pairs = nil
	r.submitted = nil
	r.runs = map[string]*conversationRun{}
	timer := r.idleTimer
	r.idleTimer = nil
	cancelIndex := r.indexCancel
	contextCancels := make([]context.CancelFunc, 0, len(r.contextCancels))
	for _, cancel := range r.contextCancels {
		contextCancels = append(contextCancels, cancel)
	}
	r.mu.Unlock()
	if timer != nil {
		timer.Stop()
	}
	r.stopConversation()
	if cancelIndex != nil {
		cancelIndex()
	}
	for _, cancel := range contextCancels {
		cancel()
	}
}

func (r *Runtime) Close() error {
	r.commandMu.Lock()
	r.mu.Lock()
	done := r.closeDone
	wait := r.closeWait
	if done == nil {
		done = make(chan struct{})
		r.closeDone = done
		r.closing = true
		timer := r.idleTimer
		cancelIndex := r.indexCancel
		contextCancels := make([]context.CancelFunc, 0, len(r.contextCancels))
		for _, contextCancel := range r.contextCancels {
			contextCancels = append(contextCancels, contextCancel)
		}
		initialized := r.initialized
		r.mu.Unlock()
		if timer != nil {
			timer.Stop()
		}
		r.stopConversation()
		if cancelIndex != nil {
			cancelIndex()
		}
		for _, contextCancel := range contextCancels {
			contextCancel()
		}
		r.cancel()
		go r.finishClose(done, initialized)
	} else {
		r.mu.Unlock()
	}
	r.commandMu.Unlock()

	if wait <= 0 {
		wait = 10 * time.Second
	}
	select {
	case <-done:
		r.mu.Lock()
		err := r.closeErr
		r.mu.Unlock()
		return err
	case <-time.After(wait):
		return fmt.Errorf("timed out closing Codebase runtime")
	}
}

func (r *Runtime) finishClose(done chan struct{}, initialized bool) {
	r.wg.Wait()
	r.contextWG.Wait()
	var err error
	if initialized {
		status := r.Status()
		if status.State == StateIndexing {
			status.State = StateDegraded
			status.LastCancellation = "runtime closed during indexing"
		}
		_, err = r.store.writeStatus(status)
	}
	r.mu.Lock()
	r.closeErr = err
	r.closed = true
	close(done)
	r.mu.Unlock()
}
