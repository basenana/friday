package tui

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"charm.land/bubbles/v2/viewport"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"github.com/basenana/friday/bus"
	"github.com/basenana/friday/config"
	"github.com/basenana/friday/core/collaboration"
	"github.com/basenana/friday/core/planning"
	"github.com/basenana/friday/core/session"
	"github.com/basenana/friday/core/types"
	"github.com/basenana/friday/sandbox"
	"github.com/basenana/friday/sessions"
)

type selectorKind string

const (
	selectorResume selectorKind = "resume"
	selectorModel  selectorKind = "model"
	selectorTasks  selectorKind = "tasks"
)

type selectorItem struct {
	value, label, description string
	data                      any
}

type selectorState struct {
	kind     selectorKind
	title    string
	items    []selectorItem
	selected int
	query    string
}

func (s *selectorState) visibleIndexes() []int {
	query := strings.ToLower(strings.TrimSpace(s.query))
	indexes := make([]int, 0, len(s.items))
	for i, item := range s.items {
		haystack := strings.ToLower(item.label + " " + item.description + " " + item.value)
		if query == "" || strings.Contains(haystack, query) {
			indexes = append(indexes, i)
		}
	}
	return indexes
}

func (s *selectorState) View(width int) string {
	lines := []string{accentStyle.Copy().Bold(true).Render(s.title)}
	if s.query != "" {
		lines = append(lines, mutedStyle.Render("filter: "+terminalSafe(s.query)))
	}
	visible := s.visibleIndexes()
	if len(visible) == 0 {
		lines = append(lines, mutedStyle.Render("no items"))
	}
	start := max(s.selected-7, 0)
	for pos := start; pos < min(start+8, len(visible)); pos++ {
		i := visible[pos]
		prefix := "  "
		if pos == s.selected {
			prefix = "› "
		}
		line := prefix + s.items[i].label
		if s.items[i].description != "" {
			line += "  " + s.items[i].description
		}
		if pos == s.selected {
			line = accentStyle.Copy().Bold(true).Render(line)
		} else {
			line = mutedStyle.Render(line)
		}
		lines = append(lines, truncateWidth(line, max(width-8, 12)))
	}
	hint := "type to filter · ↑/↓ select · Enter open · Esc close"
	if s.kind == selectorTasks {
		hint = "type to filter · Enter details · x stop · Esc close"
	}
	lines = append(lines, mutedStyle.Render(hint))
	return menuStyle.Width(max(width-4, 20)).Render(strings.Join(lines, "\n"))
}

func (m *model) updateSelector(key tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	s := m.selector
	if key.Code == tea.KeyEsc {
		m.selector = nil
		m.layout()
		return m.dispatchIfIdle()
	}
	visible := s.visibleIndexes()
	switch key.Code {
	case tea.KeyUp:
		if len(visible) > 0 {
			s.selected = (s.selected - 1 + len(visible)) % len(visible)
		}
	case tea.KeyDown:
		if len(visible) > 0 {
			s.selected = (s.selected + 1) % len(visible)
		}
	case tea.KeyBackspace:
		runes := []rune(s.query)
		if len(runes) > 0 {
			s.query = string(runes[:len(runes)-1])
			s.selected = 0
		}
	case tea.KeyEnter:
		if len(visible) == 0 {
			return m, nil
		}
		item := s.items[visible[s.selected]]
		m.selector = nil
		switch s.kind {
		case selectorResume:
			cmd, err := m.switchSession(item.value)
			if err != nil {
				m.appendBlock(chatBlock{kind: blockError, content: err.Error()})
				return m, nil
			}
			return m, cmd
		case selectorModel:
			return m.applyModel(item.data.(config.ModelConfig))
		case selectorTasks:
			task := item.data.(*sandbox.Task)
			content := fmt.Sprintf("Status: %s\nPID: %d\nCommand: %s\n\n%s", task.Status, task.PID, task.Command, task.Output)
			m.detail = newDetailState("task · "+shortID(task.ID), content, m.width, m.height)
		}
	default:
		if s.kind == selectorTasks && strings.EqualFold(key.String(), "x") && len(visible) > 0 {
			item := s.items[visible[s.selected]]
			if err := m.registry.KillTask(m.sessionID, item.value); err != nil {
				m.appendBlock(chatBlock{kind: blockError, content: "stop: " + err.Error()})
			} else {
				m.appendBlock(chatBlock{kind: blockDivider, content: "stopped task " + item.value})
			}
			m.openTasksSelector()
		} else if key.Text != "" && !key.Mod.Contains(tea.ModCtrl) && !key.Mod.Contains(tea.ModAlt) {
			s.query += key.Text
			s.selected = 0
		}
	}
	m.layout()
	return m, nil
}

func (m *model) openResumeSelector() {
	var metas []sessions.SessionMeta
	var err error
	if m.projectMgr != nil {
		metas, err = m.projectMgr.List(true)
	} else {
		metas, err = m.sessMgr.GetStore().ListActive()
	}
	if err != nil {
		m.appendBlock(chatBlock{kind: blockError, content: "list sessions: " + err.Error()})
		return
	}
	sort.Slice(metas, func(i, j int) bool { return metas[i].UpdatedAt.After(metas[j].UpdatedAt) })
	items := make([]selectorItem, 0, len(metas))
	for _, meta := range metas {
		label := meta.Name
		if label == "" {
			label = shortID(meta.ID)
		}
		marker := ""
		if meta.ID == m.sessionID {
			marker = "current · "
		}
		items = append(items, selectorItem{value: meta.ID, label: label, description: fmt.Sprintf("%s%s · %d messages", marker, meta.UpdatedAt.Format("Jan 02 15:04"), meta.MessageCount)})
	}
	m.selector = &selectorState{kind: selectorResume, title: "Resume session", items: items}
}

func (m *model) openModelSelector() {
	runtimeState, _ := m.runtime.Runtime(m.sessionID)
	items := make([]selectorItem, 0, len(m.cfg.ChatModels()))
	for _, model := range m.cfg.ChatModels() {
		current := ""
		if runtimeState.Model.Model == model.Model && runtimeState.Model.Provider == model.Provider {
			current = "current · "
		}
		items = append(items, selectorItem{value: model.Provider + "/" + model.Model, label: model.Model, description: current + model.Provider, data: model})
	}
	m.selector = &selectorState{kind: selectorModel, title: "Select model", items: items}
}

func (m *model) resolveModel(target string) (config.ModelConfig, error) {
	target = strings.TrimSpace(target)
	var matches []config.ModelConfig
	for _, candidate := range m.cfg.ChatModels() {
		if target == candidate.Provider+"/"+candidate.Model || target == candidate.Model {
			matches = append(matches, candidate)
		}
	}
	if len(matches) == 1 {
		return matches[0], nil
	}
	if len(matches) > 1 {
		return config.ModelConfig{}, fmt.Errorf("model %q is ambiguous; use provider/model", target)
	}
	return config.ModelConfig{}, fmt.Errorf("model not configured: %s", target)
}

func (m *model) applyModel(model config.ModelConfig) (tea.Model, tea.Cmd) {
	if count := runningTaskCount(m.registry.ListTasks(m.sessionID)); count > 0 {
		m.appendBlock(chatBlock{kind: blockError, content: fmt.Sprintf("switch model: %d background task(s) still running; stop or wait for them first", count)})
		return m, nil
	}
	old, err := m.runtime.Runtime(m.sessionID)
	if err != nil {
		m.appendBlock(chatBlock{kind: blockError, content: err.Error()})
		return m, nil
	}
	selection := sessions.ModelSelection{Provider: model.Provider, Model: model.Model}
	if err := m.runtime.SetModel(m.sessionID, selection); err != nil {
		m.appendBlock(chatBlock{kind: blockError, content: err.Error()})
		return m, nil
	}
	if err := m.registry.Reconfigure(m.sessionID); err != nil {
		var rollbackErr error
		if old.Model.Model == "" {
			rollbackErr = m.runtime.ClearModel(m.sessionID)
		} else {
			rollbackErr = m.runtime.SetModel(m.sessionID, old.Model)
		}
		if rollbackErr == nil {
			rollbackErr = m.registry.Reconfigure(m.sessionID)
		}
		message := "switch model: " + err.Error()
		if rollbackErr != nil {
			message += "; rollback failed: " + rollbackErr.Error()
		}
		m.appendBlock(chatBlock{kind: blockError, content: message})
		return m, nil
	}
	m.activeModel = model
	m.appendBlock(chatBlock{kind: blockDivider, content: "model · " + model.Provider + "/" + model.Model})
	return m, nil
}

func runningTaskCount(tasks []*sandbox.Task) int {
	count := 0
	for _, task := range tasks {
		if task != nil && task.Status == sandbox.TaskRunning {
			count++
		}
	}
	return count
}

func (m *model) openTasksSelector() {
	tasks := m.registry.ListTasks(m.sessionID)
	sort.Slice(tasks, func(i, j int) bool {
		if tasks[i].Status == sandbox.TaskRunning && tasks[j].Status != sandbox.TaskRunning {
			return true
		}
		if tasks[j].Status == sandbox.TaskRunning && tasks[i].Status != sandbox.TaskRunning {
			return false
		}
		return tasks[i].StartedAt.After(tasks[j].StartedAt)
	})
	items := make([]selectorItem, 0, len(tasks))
	for _, task := range tasks {
		items = append(items, selectorItem{value: task.ID, label: shortID(task.ID), description: fmt.Sprintf("%s · %s", task.Status, firstLine(task.Command)), data: task})
	}
	m.selector = &selectorState{kind: selectorTasks, title: "Background tasks", items: items}
}

type commandConfirmation struct{ action, target, label string }

func (c *commandConfirmation) View(width int) string {
	prompt := fmt.Sprintf("%s session %s?", titleWord(c.action), c.label)
	if c.action == "stop" {
		prompt = "Stop all running background tasks?"
	}
	return menuStyle.Width(max(width-4, 20)).Render(fmt.Sprintf("%s\n%s confirm · %s cancel", prompt, accentStyle.Render("y"), mutedStyle.Render("n/esc")))
}

func titleWord(value string) string {
	runes := []rune(value)
	if len(runes) == 0 {
		return value
	}
	if runes[0] >= 'a' && runes[0] <= 'z' {
		runes[0] -= 'a' - 'A'
	}
	return string(runes)
}

func (m *model) updateCommandConfirmation(key tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	if key.String() == "n" || key.Code == tea.KeyEsc {
		m.commandConfirm = nil
		m.layout()
		return m.dispatchIfIdle()
	}
	if strings.ToLower(key.String()) != "y" {
		return m, nil
	}
	c := m.commandConfirm
	m.commandConfirm = nil
	if c.action == "stop" {
		if err := m.registry.KillTask(m.sessionID, "all"); err != nil {
			m.appendBlock(chatBlock{kind: blockError, content: "stop: " + err.Error()})
		} else {
			m.appendBlock(chatBlock{kind: blockDivider, content: "stopped all background tasks"})
		}
		return m, nil
	}
	current := c.target == m.sessionID
	if current && m.projectMgr != nil {
		newID, err := m.createProjectRoot(true)
		if err != nil {
			m.appendBlock(chatBlock{kind: blockError, content: "prepare replacement: " + err.Error()})
			return m, nil
		}
		cmd, err := m.switchSession(newID)
		if err != nil {
			_ = m.projectMgr.DeleteRoot(newID)
			m.appendBlock(chatBlock{kind: blockError, content: err.Error()})
			return m, nil
		}
		if c.action == "archive" {
			err = m.projectMgr.Archive(c.target)
		} else {
			err = m.projectMgr.DeleteRoot(c.target)
		}
		if err != nil {
			m.appendBlock(chatBlock{kind: blockError, content: c.action + ": " + err.Error()})
		} else {
			m.appendBlock(chatBlock{kind: blockDivider, content: c.action + "d · " + c.label})
		}
		return m, cmd
	}
	m.registry.Shutdown(c.target)
	var err error
	if c.action == "archive" {
		if m.projectMgr != nil {
			err = m.projectMgr.Archive(c.target)
		} else {
			err = m.sessMgr.Archive(c.target)
		}
	} else {
		if m.projectMgr != nil {
			err = m.projectMgr.DeleteRoot(c.target)
		} else {
			err = m.sessMgr.Delete(c.target)
		}
	}
	if err != nil {
		m.appendBlock(chatBlock{kind: blockError, content: c.action + ": " + err.Error()})
		return m, nil
	}
	if !current {
		m.appendBlock(chatBlock{kind: blockDivider, content: c.action + "d · " + c.label})
		return m, nil
	}
	newID := types.NewID()
	cmd, switchErr := m.switchSession(newID)
	if switchErr != nil {
		m.appendBlock(chatBlock{kind: blockError, content: switchErr.Error()})
		return m, nil
	}
	return m, cmd
}

func (m *model) requestSessionConfirmation(action, target string) {
	id := m.sessionID
	label := "current"
	if target != "" {
		var meta *sessions.SessionMeta
		var err error
		if m.projectMgr != nil {
			meta, err = m.projectMgr.Resolve(target)
		} else {
			meta, err = m.sessMgr.ResolveActiveSession(target)
		}
		if err != nil {
			m.appendBlock(chatBlock{kind: blockError, content: err.Error()})
			return
		}
		id = meta.ID
		label = meta.Name
		if label == "" {
			label = shortID(meta.ID)
		}
	}
	m.commandConfirm = &commandConfirmation{action: action, target: id, label: label}
}

func (m *model) showStatus() {
	meta, _ := m.runtime.GetStore().GetMeta(m.sessionID)
	model := m.activeModel
	name := "(unnamed)"
	if meta != nil && meta.Name != "" {
		name = meta.Name
	}
	contextLine := fmt.Sprintf("%d tokens", m.tokenCount)
	if model.ContextWindow > 0 {
		contextLine = fmt.Sprintf("%d / %d tokens (%.1f%%)", m.tokenCount, model.ContextWindow, float64(m.tokenCount)/float64(model.ContextWindow)*100)
	}
	sandboxName := "default"
	if m.cfg.Sandbox != nil && !m.cfg.Sandbox.Sandbox.Enabled {
		sandboxName = "disabled"
	}
	msg := fmt.Sprintf("## Friday status\n\n- Session: %s (`%s`)\n- Mode: `%s`\n- Model: `%s/%s`\n- Reasoning: `%s`\n- Workdir: `%s`\n- Context: %s\n- Sandbox: `%s`\n- Background tasks: %d", name, m.sessionID, m.mode, model.Provider, model.Model, effectiveEffort(m), m.workdir, contextLine, sandboxName, len(m.registry.ListTasks(m.sessionID)))
	m.appendBlock(chatBlock{kind: blockAssistant, content: msg})
}

func effectiveEffort(m *model) string {
	if m.mode == collaboration.ModePlan {
		return m.cfg.Collaboration.Plan.ReasoningEffort
	}
	e := m.activeModel.ReasoningEffort
	if e == "" {
		return "default"
	}
	return e
}

type diffLoadedMsg struct {
	content string
	err     error
}

func (m *model) showDiff() tea.Cmd {
	workdir := m.workdir
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		content, err := collectWorkingTreeDiff(ctx, workdir)
		return diffLoadedMsg{content: content, err: err}
	}
}

func collectWorkingTreeDiff(ctx context.Context, workdir string) (string, error) {
	const maxOutput = 1 << 20
	commands := [][]string{{"status", "--short"}, {"diff", "--no-ext-diff"}, {"diff", "--cached", "--no-ext-diff"}}
	labels := []string{"STATUS", "UNSTAGED", "STAGED"}
	out := &cappedBuffer{limit: maxOutput}
	for i, args := range commands {
		remaining := maxOutput - out.length()
		if remaining <= 0 {
			out.markTruncated()
			break
		}
		result, truncated, err := runGitLimited(ctx, workdir, remaining, args...)
		if err != nil {
			if ctxErr := ctx.Err(); ctxErr != nil {
				return "", fmt.Errorf("diff timed out: %w", ctxErr)
			}
			return "", gitOutputError("git "+strings.Join(args, " "), result, err)
		}
		_, _ = fmt.Fprintf(out, "## %s\n", labels[i])
		if len(result) == 0 {
			_, _ = out.Write([]byte("(none)\n"))
		} else {
			_, _ = out.Write(result)
			if result[len(result)-1] != '\n' {
				_, _ = out.Write([]byte{'\n'})
			}
		}
		if truncated {
			out.markTruncated()
			break
		}
	}
	if err := ctx.Err(); err != nil {
		return "", fmt.Errorf("diff timed out: %w", err)
	}
	if out.isTruncated() {
		return out.render(), nil
	}
	untracked, namesTruncated, err := runGitLimited(ctx, workdir, maxOutput, "ls-files", "--others", "--exclude-standard", "-z")
	if err != nil {
		if ctxErr := ctx.Err(); ctxErr != nil {
			return "", fmt.Errorf("diff timed out: %w", ctxErr)
		}
		return "", gitOutputError("git ls-files", untracked, err)
	}
	if len(untracked) > 0 {
		_, _ = out.Write([]byte("## UNTRACKED CONTENT\n"))
		for _, raw := range bytes.Split(untracked, []byte{0}) {
			if out.isTruncated() {
				break
			}
			name := string(raw)
			if name == "" {
				continue
			}
			path := filepath.Join(workdir, filepath.FromSlash(name))
			info, statErr := os.Lstat(path)
			if statErr != nil {
				_, _ = fmt.Fprintf(out, "### %s\n(unreadable: %v)\n", name, statErr)
				continue
			}
			if info.Mode().IsRegular() && info.Size() > 256<<10 {
				_, _ = fmt.Fprintf(out, "### %s\n(skipped: %d bytes)\n", name, info.Size())
				continue
			}
			remaining := maxOutput - out.length()
			if remaining <= 0 {
				out.markTruncated()
				break
			}
			result, truncated, diffErr := runGitLimited(ctx, workdir, remaining, "diff", "--no-index", "--", os.DevNull, name)
			if diffErr != nil {
				if exitErr, ok := diffErr.(*exec.ExitError); !ok || exitErr.ExitCode() != 1 {
					_, _ = fmt.Fprintf(out, "### %s\n(diff failed: %v)\n", name, diffErr)
					continue
				}
			}
			_, _ = out.Write(result)
			if len(result) > 0 && result[len(result)-1] != '\n' {
				_, _ = out.Write([]byte{'\n'})
			}
			if truncated {
				out.markTruncated()
				break
			}
		}
	}
	if namesTruncated {
		out.markTruncated()
	}
	if err := ctx.Err(); err != nil {
		return "", fmt.Errorf("diff timed out: %w", err)
	}
	return out.render(), nil
}

func gitOutputError(operation string, output []byte, err error) error {
	detail := strings.TrimSpace(string(output))
	if detail == "" {
		detail = err.Error()
	}
	return fmt.Errorf("%s: %s", operation, detail)
}

type cappedBuffer struct {
	mu        sync.Mutex
	buf       bytes.Buffer
	limit     int
	truncated bool
}

func (b *cappedBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	remaining := b.limit - b.buf.Len()
	if remaining > 0 {
		_, _ = b.buf.Write(p[:min(remaining, len(p))])
	}
	if len(p) > max(remaining, 0) {
		b.truncated = true
	}
	return len(p), nil
}

func (b *cappedBuffer) length() int {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Len()
}

func (b *cappedBuffer) markTruncated() {
	b.mu.Lock()
	b.truncated = true
	b.mu.Unlock()
}

func (b *cappedBuffer) isTruncated() bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.truncated
}

func (b *cappedBuffer) render() string {
	content, truncated := b.snapshot()
	if !truncated {
		return string(content)
	}
	marker := []byte("\n… output truncated at 1 MiB")
	cut := min(len(content), max(b.limit-len(marker), 0))
	for cut > 0 && !utf8.Valid(content[:cut]) {
		cut--
	}
	return string(content[:cut]) + string(marker)
}

func (b *cappedBuffer) snapshot() ([]byte, bool) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return append([]byte(nil), b.buf.Bytes()...), b.truncated
}

func runGitLimited(ctx context.Context, workdir string, limit int, args ...string) ([]byte, bool, error) {
	buffer := &cappedBuffer{limit: max(limit, 0)}
	cmd := exec.CommandContext(ctx, "git", append([]string{"-C", workdir}, args...)...)
	cmd.Stdout, cmd.Stderr = buffer, buffer
	err := cmd.Run()
	output, truncated := buffer.snapshot()
	return output, truncated, err
}

// configuredSessionModel resolves the complete configured model record for a
// persisted selection. The bool reports whether a valid override was found.
func configuredSessionModel(sessMgr sessionRuntime, cfg *config.Config, sessionID string) (config.ModelConfig, bool) {
	fallback := cfg.PrimaryModel()
	runtimeState, err := sessMgr.Runtime(sessionID)
	if err != nil || runtimeState.Model.Model == "" {
		return fallback, false
	}
	for _, candidate := range cfg.ChatModels() {
		if candidate.Provider == runtimeState.Model.Provider && candidate.Model == runtimeState.Model.Model {
			return candidate, true
		}
	}
	return fallback, false
}

func normalizeSessionModel(sessMgr sessionRuntime, cfg *config.Config, sessionID string) error {
	runtimeState, err := sessMgr.Runtime(sessionID)
	if err != nil || runtimeState.Model.Model == "" {
		return err
	}
	if _, valid := configuredSessionModel(sessMgr, cfg, sessionID); valid {
		return nil
	}
	return sessMgr.ClearModel(sessionID)
}

func (m *model) copyResponse(index int) {
	var values []string
	if m.latestPlan != nil && m.latestPlan.Status == planning.ArtifactProposed {
		values = append(values, m.latestPlan.Markdown)
	}
	for i := len(m.messages) - 1; i >= 0; i-- {
		if m.messages[i].kind == blockAssistant {
			values = append(values, m.messages[i].content)
		}
	}
	if index < 1 || index > len(values) {
		m.appendBlock(chatBlock{kind: blockError, content: fmt.Sprintf("copy: response %d not found", index)})
		return
	}
	if err := writeClipboard(values[index-1]); err != nil {
		m.appendBlock(chatBlock{kind: blockError, content: "copy: " + err.Error()})
		return
	}
	m.appendBlock(chatBlock{kind: blockDivider, content: fmt.Sprintf("copied response %d", index)})
}

func writeClipboard(value string) error {
	var name string
	var args []string
	switch runtime.GOOS {
	case "darwin":
		name = "pbcopy"
	case "windows":
		name = "clip"
	default:
		if _, err := exec.LookPath("wl-copy"); err == nil {
			name = "wl-copy"
		} else {
			name, args = "xclip", []string{"-selection", "clipboard"}
		}
	}
	cmd := exec.Command(name, args...)
	cmd.Stdin = bytes.NewBufferString(value)
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("clipboard backend %s unavailable: %w", name, err)
	}
	return nil
}

type planHandoffState struct {
	selected int
	view     viewport.Model
}

func (m *model) syncPlanHandoff() {
	if m.planHandoff == nil || m.latestPlan == nil {
		return
	}
	content := m.markdown(m.latestPlan.Markdown)
	width := max(m.width-8, 20)
	panelHeight := max(m.height/2, 8)
	bodyHeight := min(max(lipgloss.Height(content), 1), max(panelHeight-6, 1))
	p := m.planHandoff
	p.view.SetWidth(width)
	p.view.SetHeight(bodyHeight)
	p.view.SetContent(content)
}

func (m *model) renderPlanHandoff() string {
	m.syncPlanHandoff()
	p := m.planHandoff
	if p == nil || m.latestPlan == nil {
		return ""
	}
	options := []string{"Approve · implement", "Request changes"}
	title := strings.TrimSpace(m.latestPlan.Title)
	header := "Plan ready"
	if title != "" {
		header += " · " + terminalSafe(title)
	}
	if m.latestPlan.Version > 0 {
		header += fmt.Sprintf(" · v%d", m.latestPlan.Version)
	}
	lines := []string{accentStyle.Copy().Bold(true).Render(header), p.view.View()}
	for i, option := range options {
		prefix := "  "
		if i == p.selected {
			prefix = "› "
			option = accentStyle.Render(option)
		}
		lines = append(lines, prefix+option)
	}
	lines = append(lines, mutedStyle.Render("PgUp/PgDn scroll plan · ↑/↓ select · Enter confirm"))
	return menuStyle.Width(max(m.width-4, 20)).Render(strings.Join(lines, "\n"))
}

func (m *model) planHandoffHeight() int {
	m.syncPlanHandoff()
	if m.planHandoff == nil {
		return 0
	}
	// Header, two actions, help, and the card's vertical border.
	return m.planHandoff.view.Height() + 6
}

func (m *model) updatePlanHandoff(key tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	if key.Code == tea.KeyEsc {
		m.planHandoff = nil
		return m.dispatchIfIdle()
	}
	switch key.Code {
	case tea.KeyPgUp, tea.KeyPgDown:
		var cmd tea.Cmd
		m.planHandoff.view, cmd = m.planHandoff.view.Update(key)
		return m, cmd
	case tea.KeyUp:
		m.planHandoff.selected = (m.planHandoff.selected + 1) % 2
	case tea.KeyDown:
		m.planHandoff.selected = (m.planHandoff.selected + 1) % 2
	case tea.KeyEnter:
		choice := m.planHandoff.selected
		m.planHandoff = nil
		if choice == 1 {
			m.appendBlock(chatBlock{kind: blockDivider, content: "plan changes requested · send feedback below"})
			return m.dispatchIfIdle()
		}
		if m.latestPlan == nil {
			return m, nil
		}
		lifecycle, ok := m.registry.Lifecycle(m.sessionID)
		if !ok || lifecycle.Current() == nil {
			m.appendBlock(chatBlock{kind: blockError, content: "approve plan: active session unavailable"})
			m.planHandoff = &planHandoffState{}
			return m, nil
		}
		m.planCompacting = true
		m.layout()
		return m, compactForPlanApproval(m.sessionID, m.latestPlan.ID, lifecycle.Current())
	}
	if key.String() == "ctrl+u" || key.String() == "ctrl+d" {
		var cmd tea.Cmd
		m.planHandoff.view, cmd = m.planHandoff.view.Update(key)
		return m, cmd
	}
	return m, nil
}

type planCompactFinishedMsg struct {
	sessionID string
	planID    string
	tokens    int
	err       error
}

type manualCompactFinishedMsg struct {
	sessionID      string
	beforeTokens   int64
	afterTokens    int64
	beforeMessages int
	afterMessages  int
	duration       time.Duration
	err            error
}

type manualCompactor interface {
	CompactHistoryWithTrigger(context.Context, session.CompactTrigger) error
	HistoryLen() int
	Tokens() int64
}

func compactManually(sessionID string, sess manualCompactor) tea.Cmd {
	return func() tea.Msg {
		startedAt := time.Now()
		beforeTokens := sess.Tokens()
		beforeMessages := sess.HistoryLen()
		err := sess.CompactHistoryWithTrigger(context.Background(), session.CompactTriggerManual)
		return manualCompactFinishedMsg{
			sessionID:      sessionID,
			beforeTokens:   beforeTokens,
			afterTokens:    sess.Tokens(),
			beforeMessages: beforeMessages,
			afterMessages:  sess.HistoryLen(),
			duration:       time.Since(startedAt),
			err:            err,
		}
	}
}

func (m *model) finishManualCompact(msg manualCompactFinishedMsg) (tea.Model, tea.Cmd) {
	m.manualCompacting = false
	if msg.sessionID != m.sessionID {
		m.appendBlock(chatBlock{kind: blockError, content: "compact: session changed while compacting"})
		m.layout()
		return m.dispatchIfIdle()
	}
	if msg.err != nil {
		m.appendBlock(chatBlock{kind: blockError, content: "compact: " + msg.err.Error()})
		m.layout()
		return m.dispatchIfIdle()
	}

	m.tokenCount = int(msg.afterTokens)
	saved := msg.beforeTokens - msg.afterTokens
	stats := fmt.Sprintf("context compacted · %d → %d tokens · saved %d", msg.beforeTokens, msg.afterTokens, saved)
	if msg.beforeTokens > 0 {
		stats += fmt.Sprintf(" (%.1f%%)", float64(saved)/float64(msg.beforeTokens)*100)
	}
	stats += fmt.Sprintf(" · %d → %d messages · %s", msg.beforeMessages, msg.afterMessages, formatElapsed(msg.duration))
	m.appendBlock(chatBlock{kind: blockDivider, content: stats})
	m.layout()
	return m.dispatchIfIdle()
}

type planCompactor interface {
	CompactHistoryWithTrigger(context.Context, session.CompactTrigger) error
	Tokens() int64
}

func compactForPlanApproval(sessionID, planID string, sess planCompactor) tea.Cmd {
	return func() tea.Msg {
		err := sess.CompactHistoryWithTrigger(context.Background(), session.CompactTriggerPlanHandoff)
		return planCompactFinishedMsg{sessionID: sessionID, planID: planID, tokens: int(sess.Tokens()), err: err}
	}
}

func (m *model) finishPlanApproval(msg planCompactFinishedMsg) (tea.Model, tea.Cmd) {
	m.planCompacting = false
	if msg.sessionID != m.sessionID || m.latestPlan == nil || msg.planID != m.latestPlan.ID {
		m.appendBlock(chatBlock{kind: blockError, content: "approve plan: session or plan changed while compacting"})
		m.layout()
		return m.dispatchIfIdle()
	}
	if msg.err != nil {
		m.appendBlock(chatBlock{kind: blockError, content: "compact before implementation: " + msg.err.Error()})
		m.planHandoff = &planHandoffState{}
		m.layout()
		return m, nil
	}

	m.tokenCount = msg.tokens
	if err := m.persistPlanStatus(planning.ArtifactAccepted); err != nil {
		m.appendBlock(chatBlock{kind: blockError, content: "accept plan: " + err.Error()})
		m.planHandoff = &planHandoffState{}
		m.layout()
		return m, nil
	}
	if err := m.runtime.SetMode(m.sessionID, collaboration.ModeDefault); err != nil {
		restoreErr := m.persistPlanStatus(planning.ArtifactProposed)
		message := "exit Plan Mode: " + err.Error()
		if restoreErr != nil {
			message += "; restore plan: " + restoreErr.Error()
		}
		m.appendBlock(chatBlock{kind: blockError, content: message})
		m.planHandoff = &planHandoffState{}
		m.layout()
		return m, nil
	}

	m.mode = collaboration.ModeDefault
	m.appendBlock(chatBlock{kind: blockDivider, content: "context compacted · implementing approved plan"})
	return m.startUserTurn("Implement the approved plan. Re-read relevant files as needed and verify the result.", bus.DeliveryNormal)
}

func (m *model) persistPlanStatus(status planning.ArtifactStatus) error {
	if m.latestPlan == nil {
		return fmt.Errorf("no plan is available")
	}
	updated := *m.latestPlan
	updated.Status = status
	if status == planning.ArtifactAccepted {
		now := time.Now()
		updated.AcceptedAt = &now
	} else {
		updated.AcceptedAt = nil
	}
	if err := m.runtime.SavePlan(updated.SessionID, updated); err != nil {
		return err
	}
	*m.latestPlan = updated
	return nil
}
