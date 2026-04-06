package session

import (
	"context"
	"encoding/json"
	"fmt"
	"regexp"
	"slices"
	"strings"
	"time"

	"github.com/basenana/friday/core/providers"
	"github.com/basenana/friday/core/types"
)

var (
	filePathPattern = regexp.MustCompile("(?i)(?:^|[\\s(\"'])(?:((?:\\.{0,2}/|/)?[A-Za-z0-9_.~\\-/]+?\\.(?:go|mod|sum|ts|tsx|js|jsx|json|yaml|yml|toml|md|txt|py|rs|java|c|cc|cpp|h|hpp|sh|sql|html|css|xml)))(?:$|[\\s)\"',:;])")
)

func MergeCaseFiles(existing, next CaseFile) CaseFile {
	merged := existing.clone()

	if strings.TrimSpace(next.TaskObjective) != "" {
		merged.TaskObjective = next.TaskObjective
	}
	merged.UserConstraints = mergeUniqueStrings(merged.UserConstraints, next.UserConstraints, 8)
	merged.ArchitectureDecisions = mergeUniqueStrings(merged.ArchitectureDecisions, next.ArchitectureDecisions, 8)
	if strings.TrimSpace(next.CurrentStatus) != "" {
		merged.CurrentStatus = next.CurrentStatus
	}
	merged.PendingWork = mergeUniqueStrings(merged.PendingWork, next.PendingWork, defaultPendingWorkLimit)
	merged.RecentRequests = mergeUniqueStrings(merged.RecentRequests, next.RecentRequests, defaultRecentRequestLimit)
	merged.RecentFiles = mergeUniqueStrings(merged.RecentFiles, next.RecentFiles, defaultRecentFileLimit)
	merged.ImportantCommandsOrTools = mergeUniqueStrings(merged.ImportantCommandsOrTools, next.ImportantCommandsOrTools, 8)
	merged.KnownRisks = mergeUniqueStrings(merged.KnownRisks, next.KnownRisks, 8)
	merged.TimelineHighlights = mergeUniqueStrings(merged.TimelineHighlights, next.TimelineHighlights, defaultTimelineHighlightLimit)
	return merged
}

type CaseFile struct {
	TaskObjective            string   `json:"task_objective,omitempty" jsonschema:"required,description=Primary user objective for this session. Use an empty string when unknown"`
	UserConstraints          []string `json:"user_constraints,omitempty" jsonschema:"required,maxItems=8,description=Concrete user constraints, non-goals, or requirements. Use an empty array when none are known"`
	ArchitectureDecisions    []string `json:"architecture_decisions,omitempty" jsonschema:"required,maxItems=8,description=Important design or implementation decisions that should persist across compaction. Use an empty array when none exist"`
	CurrentStatus            string   `json:"current_status,omitempty" jsonschema:"required,description=Concise current progress summary describing the present state of the work. Use an empty string when unknown"`
	PendingWork              []string `json:"pending_work,omitempty" jsonschema:"required,maxItems=8,description=Concrete remaining tasks, next steps, or unresolved work items. Use an empty array when there is no pending work"`
	RecentRequests           []string `json:"recent_requests,omitempty" jsonschema:"required,maxItems=3,description=Most relevant recent user asks that still matter for the next turn. Use an empty array when none are relevant"`
	RecentFiles              []string `json:"recent_files,omitempty" jsonschema:"required,maxItems=5,description=Most relevant file paths discussed, inspected, or edited recently. Use an empty array when no file references matter"`
	ImportantCommandsOrTools []string `json:"important_commands_or_tools,omitempty" jsonschema:"required,maxItems=8,description=Important commands, tools, or tool categories used during the session. Use an empty array when none are worth keeping"`
	KnownRisks               []string `json:"known_risks,omitempty" jsonschema:"required,maxItems=8,description=Known risks, blockers, failures, or caveats that should not be forgotten. Use an empty array when there are no known risks"`
	TimelineHighlights       []string `json:"timeline_highlights,omitempty" jsonschema:"required,maxItems=12,description=Short timeline bullets covering major events, milestones, or discoveries in order. Use an empty array when there are no important highlights"`
}

func (cf CaseFile) ToMessage() types.Message {
	return types.Message{
		Role:    types.RoleAgent,
		Content: cf.String(),
	}
}

func (cf CaseFile) String() string {
	if cf.IsZero() {
		return ""
	}

	raw, err := json.Marshal(cf)
	if err != nil {
		return ""
	}
	return "<case_file>\n" + string(raw) + "\n</case_file>"
}

func (cf CaseFile) clone() CaseFile {
	return CaseFile{
		TaskObjective:            cf.TaskObjective,
		UserConstraints:          append([]string(nil), cf.UserConstraints...),
		ArchitectureDecisions:    append([]string(nil), cf.ArchitectureDecisions...),
		CurrentStatus:            cf.CurrentStatus,
		PendingWork:              append([]string(nil), cf.PendingWork...),
		RecentRequests:           append([]string(nil), cf.RecentRequests...),
		RecentFiles:              append([]string(nil), cf.RecentFiles...),
		ImportantCommandsOrTools: append([]string(nil), cf.ImportantCommandsOrTools...),
		KnownRisks:               append([]string(nil), cf.KnownRisks...),
		TimelineHighlights:       append([]string(nil), cf.TimelineHighlights...),
	}
}

func (cf CaseFile) IsZero() bool {
	return strings.TrimSpace(cf.TaskObjective) == "" &&
		len(cf.UserConstraints) == 0 &&
		len(cf.ArchitectureDecisions) == 0 &&
		strings.TrimSpace(cf.CurrentStatus) == "" &&
		len(cf.PendingWork) == 0 &&
		len(cf.RecentRequests) == 0 &&
		len(cf.RecentFiles) == 0 &&
		len(cf.ImportantCommandsOrTools) == 0 &&
		len(cf.KnownRisks) == 0 &&
		len(cf.TimelineHighlights) == 0
}

func ParseCaseFileMessage(content string) (CaseFile, bool) {
	content = strings.TrimSpace(content)
	if !strings.HasPrefix(content, "<case_file>") || !strings.HasSuffix(content, "</case_file>") {
		return CaseFile{}, false
	}

	body := strings.TrimSpace(strings.TrimSuffix(strings.TrimPrefix(content, "<case_file>"), "</case_file>"))
	if body == "" {
		return CaseFile{}, false
	}

	var cf CaseFile
	if err := json.Unmarshal([]byte(body), &cf); err != nil {
		return CaseFile{}, false
	}
	return cf, !cf.IsZero()
}

func SummarizeToCaseFile(ctx context.Context, llm providers.Client, existing CaseFile, history []types.Message) (CaseFile, error) {
	if len(history) == 0 {
		return existing, nil
	}

	if llm == nil {
		if !existing.IsZero() {
			return existing, nil
		}
		return HeuristicCaseFile(history), nil
	}

	var summarized CaseFile
	req := providers.NewRequest(compactPrompt(history, existing))

	if err := llm.StructuredPredict(ctx, req, &summarized); err == nil && !summarized.IsZero() {
		return MergeCaseFiles(existing, summarized), nil
	}

	summarized, err := summarizeCaseFileFromCompletion(ctx, llm, req)
	if err == nil {
		return MergeCaseFiles(existing, summarized), nil
	}

	if !existing.IsZero() {
		return existing, nil
	}
	return HeuristicCaseFile(history), nil
}

func summarizeCaseFileFromCompletion(ctx context.Context, llm providers.Client, req providers.Request) (CaseFile, error) {
	resp := llm.Completion(ctx, req)
	msgCh := resp.Message()

	var raw strings.Builder
Wait:
	for {
		select {
		case <-ctx.Done():
			return CaseFile{}, ctx.Err()
		case err := <-resp.Error():
			if err != nil {
				return CaseFile{}, err
			}
		case delta, ok := <-msgCh:
			if !ok {
				break Wait
			}
			if delta.Content != "" {
				raw.WriteString(delta.Content)
			}
		}
	}

	var summarized CaseFile
	if err := decodeJSONObject(raw.String(), &summarized); err != nil {
		return CaseFile{}, err
	}
	if summarized.IsZero() {
		return CaseFile{}, fmt.Errorf("completion summary is empty")
	}
	return summarized, nil
}

func HeuristicCaseFile(history []types.Message) CaseFile {
	var (
		result        CaseFile
		recentUsers   []string
		pending       []string
		timeline      []string
		constraints   []string
		commandsTools []string
		risks         []string
	)

	files := extractFileRefsFromHistory(history)
	for _, f := range files {
		result.RecentFiles = append(result.RecentFiles, f.Path)
	}
	result.RecentFiles = limitStrings(result.RecentFiles, defaultRecentFileLimit)

	for _, msg := range history {
		text := strings.TrimSpace(msg.GetContent())
		switch msg.Role {
		case types.RoleUser:
			if text != "" {
				recentUsers = append(recentUsers, text)
				if result.TaskObjective == "" {
					result.TaskObjective = text
				}
				if maybeConstraint(text) {
					constraints = append(constraints, text)
				}
			}

		case types.RoleAssistant:
			if len(msg.ToolCalls) > 0 {
				for _, call := range msg.ToolCalls {
					commandsTools = append(commandsTools, call.Name)
				}
			} else if text != "" {
				result.CurrentStatus = text
			}

		case types.RoleTool:
			if text != "" {
				if strings.Contains(strings.ToLower(text), "error") || strings.Contains(strings.ToLower(text), "failed") {
					risks = append(risks, squeezeWhitespace(text, 160))
				}
			}

		case types.RoleAgent:
			if strings.Contains(text, "<current_todo_list>") {
				pending = append(pending, extractPendingWork(text)...)
			}
		}

		line := summarizeTimelineEntry(msg)
		if line != "" {
			timeline = append(timeline, line)
		}
	}

	result.RecentRequests = tailStrings(recentUsers, defaultRecentRequestLimit)
	result.PendingWork = limitStrings(pending, defaultPendingWorkLimit)
	result.UserConstraints = limitStrings(constraints, 8)
	result.ImportantCommandsOrTools = limitStrings(commandsTools, 8)
	result.KnownRisks = limitStrings(risks, 8)
	result.TimelineHighlights = tailStrings(timeline, defaultTimelineHighlightLimit)

	if result.TaskObjective == "" && len(result.RecentRequests) > 0 {
		result.TaskObjective = result.RecentRequests[len(result.RecentRequests)-1]
	}
	if strings.TrimSpace(result.CurrentStatus) == "" {
		result.CurrentStatus = inferCurrentStatus(history)
	}
	return result
}

func summarizeTimelineEntry(msg types.Message) string {
	text := strings.TrimSpace(msg.GetContent())
	switch {
	case msg.Role == types.RoleUser && text != "":
		return "user: " + squeezeWhitespace(text, 140)
	case msg.Role == types.RoleAssistant && len(msg.ToolCalls) > 0:
		var names []string
		for _, call := range msg.ToolCalls {
			names = append(names, call.Name)
		}
		return "assistant used tools: " + strings.Join(limitStrings(names, 4), ", ")
	case msg.Role == types.RoleAssistant && text != "":
		return "assistant: " + squeezeWhitespace(text, 140)
	case msg.Role == types.RoleTool && text != "":
		return "tool: " + squeezeWhitespace(text, 140)
	default:
		return ""
	}
}

func inferCurrentStatus(history []types.Message) string {
	for i := len(history) - 1; i >= 0; i-- {
		msg := history[i]
		text := strings.TrimSpace(msg.GetContent())
		if msg.Role == types.RoleAssistant && text != "" {
			return squeezeWhitespace(text, 240)
		}
	}
	return ""
}

func extractPendingWork(content string) []string {
	var pending []string
	for _, line := range strings.Split(content, "\n") {
		line = strings.TrimSpace(line)
		if !strings.Contains(line, "status=") || !strings.Contains(line, "describe=") {
			continue
		}
		describe := betweenTokens(line, "describe=", " status=")
		status := afterToken(line, "status=")
		if strings.Contains(status, "completed") {
			continue
		}
		pending = append(pending, strings.TrimSpace(describe))
	}
	return pending
}

func afterToken(line, token string) string {
	idx := strings.Index(line, token)
	if idx < 0 {
		return ""
	}
	return strings.TrimSpace(line[idx+len(token):])
}

func betweenTokens(line, startToken, endToken string) string {
	start := strings.Index(line, startToken)
	if start < 0 {
		return ""
	}
	value := line[start+len(startToken):]
	if end := strings.Index(value, endToken); end >= 0 {
		value = value[:end]
	}
	return strings.TrimSpace(value)
}

func maybeConstraint(text string) bool {
	lower := strings.ToLower(text)
	return strings.Contains(lower, "must") ||
		strings.Contains(lower, "should") ||
		strings.Contains(lower, "don't") ||
		strings.Contains(lower, "do not") ||
		strings.Contains(lower, "need to") ||
		strings.Contains(lower, "require")
}

func extractFileRefsFromHistory(history []types.Message) []FileRef {
	var refs []FileRef
	for _, msg := range history {
		refs = append(refs, ExtractFileRefs(msg.GetContent(), string(msg.Role), msg.Time)...)
		if msg.IsToolCall() {
			for _, call := range msg.ToolCalls {
				refs = append(refs, ExtractFileRefs(call.Arguments, call.Name, msg.Time)...)
			}
		}
	}
	return dedupeFileRefs(refs, defaultRecentFileLimit)
}

func ExtractFileRefs(text, source string, seenAt time.Time) []FileRef {
	matches := filePathPattern.FindAllStringSubmatch(text, -1)
	if len(matches) == 0 {
		return nil
	}

	result := make([]FileRef, 0, len(matches))
	for _, match := range matches {
		if len(match) < 2 {
			continue
		}
		path := strings.TrimSpace(match[1])
		path = strings.Trim(path, `"'`)
		if path == "" {
			continue
		}
		result = append(result, FileRef{Path: path, Source: source, SeenAt: seenAt})
	}
	return dedupeFileRefs(result, defaultRecentFileLimit)
}

func dedupeFileRefs(refs []FileRef, limit int) []FileRef {
	if len(refs) == 0 {
		return nil
	}
	seen := make(map[string]FileRef)
	order := make([]string, 0, len(refs))
	for _, ref := range refs {
		if ref.Path == "" {
			continue
		}
		seen[ref.Path] = ref
		order = append(order, ref.Path)
	}

	slices.Reverse(order)
	var result []FileRef
	added := make(map[string]struct{})
	for _, path := range order {
		if _, ok := added[path]; ok {
			continue
		}
		result = append(result, seen[path])
		added[path] = struct{}{}
		if len(result) >= limit {
			break
		}
	}
	slices.Reverse(result)
	return result
}

func mergeUniqueStrings(base, extra []string, limit int) []string {
	seen := make(map[string]struct{})
	var result []string
	for _, item := range append(base, extra...) {
		item = strings.TrimSpace(item)
		if item == "" {
			continue
		}
		if _, ok := seen[item]; ok {
			continue
		}
		seen[item] = struct{}{}
		result = append(result, item)
	}
	if len(result) > limit {
		result = result[len(result)-limit:]
	}
	return result
}

func tailStrings(items []string, limit int) []string {
	items = limitStrings(items, limit)
	if len(items) <= limit {
		return items
	}
	return items[len(items)-limit:]
}

func limitStrings(items []string, limit int) []string {
	var result []string
	for _, item := range items {
		item = strings.TrimSpace(item)
		if item == "" {
			continue
		}
		result = append(result, squeezeWhitespace(item, 240))
	}
	if len(result) > limit {
		result = result[len(result)-limit:]
	}
	return result
}

func squeezeWhitespace(text string, max int) string {
	text = strings.Join(strings.Fields(strings.TrimSpace(text)), " ")
	runes := []rune(text)
	if len(runes) > max {
		return string(runes[:max]) + "..."
	}
	return text
}

func decodeJSONObject(raw string, target any) error {
	start := strings.Index(raw, "{")
	end := strings.LastIndex(raw, "}")
	if start < 0 || end < 0 || end <= start {
		return fmt.Errorf("json object not found")
	}
	return json.Unmarshal([]byte(raw[start:end+1]), target)
}

func IsSyntheticContextMessage(msg types.Message) bool {
	if msg.Role != types.RoleAgent {
		return false
	}
	content := strings.TrimSpace(msg.Content)
	return strings.HasPrefix(content, "<case_file>") ||
		strings.HasPrefix(content, "[Memory Context]") ||
		strings.HasPrefix(content, "[Long-Term Memory]") ||
		strings.HasPrefix(content, summaryPrefix)
}
