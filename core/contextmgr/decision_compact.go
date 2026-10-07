package contextmgr

import (
	"context"
	"fmt"
	"math"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/basenana/friday/core/providers"
	"github.com/basenana/friday/core/session"
	"github.com/basenana/friday/core/types"
)

const (
	decisionCompactTimeout   = 30 * time.Second
	decisionCompactThreshold = 0.5
	decisionBatchPairLimit   = 50
	decisionGoalLimit        = 500
	decisionGoalCount        = 3
)

type decisionCompactState struct {
	Context string                   `json:"context"`
	Goal    string                   `json:"goal,omitempty"`
	History []decisionCompactMessage `json:"history"`
}

type decisionCompactMessage struct {
	Index      int                    `json:"index"`
	Role       types.MessageRole      `json:"role"`
	Text       string                 `json:"text,omitempty"`
	ToolCalls  []decisionCompactCall  `json:"tool_calls,omitempty"`
	ToolResult *decisionCompactResult `json:"tool_result,omitempty"`
}

type decisionCompactCall struct {
	ID        string `json:"id"`
	Tool      string `json:"tool"`
	Arguments string `json:"arguments"`
}

type decisionCompactResult struct {
	ID        string `json:"id"`
	Success   bool   `json:"success"`
	Status    string `json:"status,omitempty"`
	ErrorCode string `json:"error_code,omitempty"`
	RuneCount int    `json:"rune_count"`
	Omitted   bool   `json:"omitted"`
}

type decisionToolPair struct {
	ID           string
	TemporaryID  string
	ToolName     string
	ResultChars  int
	AssistantIdx int
	ResultIdx    int
	Call         types.ToolCall
	Result       types.ToolResult
}

type decisionBatch struct {
	Index int
	Pairs []decisionToolPair
}

type decisionBatchResult struct {
	Index   int
	Actions map[string]decisionPairAction
	Cache   map[string]decisionCachedAction
	Err     error
}

type decisionPairAction uint8

const (
	decisionKeep decisionPairAction = iota
	decisionDropResult
	decisionDropCall
)

func (m *Manager) buildDecisionProjection(
	ctx context.Context,
	sess *session.Session,
	history []types.Message,
) (
	projected []types.Message,
	prefix []types.Message,
	sourceMessages int,
	savedTokens int64,
	cacheEpoch string,
	cacheAdditions map[string]decisionCachedAction,
	decided bool,
	err error,
) {
	if err := ctx.Err(); err != nil {
		return nil, nil, 0, 0, "", nil, false, err
	}
	groups := groupHistory(history)
	tailStart := len(groups) - projectionTailGroups
	if tailStart < 0 {
		tailStart = 0
	}
	oldMessages := flattenGroups(groups[:tailStart])
	tailMessages := flattenGroups(groups[tailStart:])
	pairs := collectDecisionPairs(oldMessages)
	if len(pairs) == 0 {
		return nil, nil, 0, 0, "", nil, false, nil
	}

	epoch, cacheEnabled := decisionEpochKey(sess.ID, history)
	cache := decisionEpochCache{Decisions: make(map[string]decisionCachedAction)}
	if cacheEnabled {
		cache = loadDecisionCache(ctx, sess, epoch)
	}
	actions := make(map[string]decisionPairAction, len(pairs))
	unresolved := make([]decisionToolPair, 0, len(pairs))
	for _, pair := range pairs {
		hash := decisionPairHash(pair.Call, pair.Result)
		cached, ok := cache.Decisions[pair.ID]
		if ok && cached.PairHash == hash {
			if action, valid := decisionActionFromCache(cached.Action); valid {
				actions[pair.ID] = action
				continue
			}
		}
		unresolved = append(unresolved, pair)
	}

	additions := make(map[string]decisionCachedAction, len(unresolved))
	if len(unresolved) > 0 {
		batches := make([]decisionBatch, 0, (len(unresolved)+decisionBatchPairLimit-1)/decisionBatchPairLimit)
		for start := 0; start < len(unresolved); start += decisionBatchPairLimit {
			end := start + decisionBatchPairLimit
			if end > len(unresolved) {
				end = len(unresolved)
			}
			batches = append(batches, decisionBatch{Index: len(batches), Pairs: unresolved[start:end]})
		}

		results := make(chan decisionBatchResult, len(batches))
		var wg sync.WaitGroup
		wg.Add(len(batches))
		for _, batch := range batches {
			batch := batch
			go func() {
				defer wg.Done()
				results <- m.evaluateDecisionBatch(ctx, sess.ID, history, oldMessages, batch)
			}()
		}
		wg.Wait()
		close(results)

		ordered := make([]decisionBatchResult, 0, len(batches))
		for result := range results {
			ordered = append(ordered, result)
		}
		if err := ctx.Err(); err != nil {
			return nil, nil, 0, 0, "", nil, false, err
		}
		sort.Slice(ordered, func(i, j int) bool { return ordered[i].Index < ordered[j].Index })
		for _, result := range ordered {
			if result.Err != nil {
				m.logger.Warnw("decision microcompact batch failed; keeping batch", "session", sess.ID, "batch", result.Index, "error", result.Err)
				continue
			}
			for id, action := range result.Actions {
				actions[id] = action
			}
			for id, action := range result.Cache {
				additions[id] = action
			}
		}
	}
	for _, pair := range pairs {
		if _, ok := actions[pair.ID]; !ok {
			actions[pair.ID] = decisionKeep
		}
	}

	prefix, err = m.rebuildDecisionPrefix(oldMessages, tailMessages, pairs, actions)
	if err != nil {
		m.logger.Warnw("decision microcompact reconstruction failed; using deterministic projection", "session", sess.ID, "error", err)
		return nil, nil, 0, 0, "", nil, false, nil
	}

	for _, msg := range oldMessages {
		savedTokens += msg.FuzzyTokens()
	}
	for _, msg := range prefix {
		savedTokens -= msg.FuzzyTokens()
	}
	if savedTokens < 0 {
		savedTokens = 0
	}
	projected = make([]types.Message, 0, len(prefix)+len(tailMessages))
	projected = append(projected, cloneMessages(prefix)...)
	projected = append(projected, cloneMessages(tailMessages)...)
	if !cacheEnabled {
		epoch = ""
		additions = nil
	}
	return projected, prefix, len(oldMessages), savedTokens, epoch, additions, true, nil
}

func (m *Manager) evaluateDecisionBatch(ctx context.Context, sessionID string, history, oldMessages []types.Message, batch decisionBatch) decisionBatchResult {
	request := providers.DecisionRequest{
		SessionID: sessionID,
		State: decisionCompactState{
			Context: "Coding-assistant conversation micro-compaction. Decide whether old paired tool calls and their full results still matter. Dropped information can usually be recovered by running the tool again.",
			Goal:    decisionGoal(history),
			History: decisionHistory(oldMessages, batch.Pairs),
		},
		Questions: decisionQuestions(batch.Pairs),
	}
	decisionCtx, cancel := context.WithTimeout(ctx, decisionCompactTimeout)
	response, err := m.cfg.DecisionProvider.Evaluate(decisionCtx, request)
	cancel()
	result := decisionBatchResult{Index: batch.Index, Err: err}
	if err != nil {
		return result
	}
	result.Actions = make(map[string]decisionPairAction, len(batch.Pairs))
	result.Cache = make(map[string]decisionCachedAction, len(batch.Pairs))
	for _, pair := range batch.Pairs {
		callValue, callOK := decisionNoul(response.Answers["call_"+pair.TemporaryID])
		resultValue, resultOK := decisionNoul(response.Answers["result_"+pair.TemporaryID])
		if !callOK || !resultOK {
			result.Actions[pair.ID] = decisionKeep
			continue
		}
		action := decisionKeep
		switch {
		case resultValue >= decisionCompactThreshold:
			action = decisionKeep
		case callValue >= decisionCompactThreshold:
			action = decisionDropResult
		default:
			action = decisionDropCall
		}
		result.Actions[pair.ID] = action
		result.Cache[pair.ID] = decisionCachedAction{PairHash: decisionPairHash(pair.Call, pair.Result), Action: decisionActionName(action)}
	}
	return result
}

func decisionActionName(action decisionPairAction) string {
	switch action {
	case decisionKeep:
		return "keep"
	case decisionDropResult:
		return "drop_result"
	case decisionDropCall:
		return "drop_call"
	default:
		return ""
	}
}

func decisionActionFromCache(action string) (decisionPairAction, bool) {
	switch action {
	case "keep":
		return decisionKeep, true
	case "drop_result":
		return decisionDropResult, true
	case "drop_call":
		return decisionDropCall, true
	default:
		return decisionKeep, false
	}
}

func collectDecisionPairs(messages []types.Message) []decisionToolPair {
	type callLocation struct {
		message int
		call    types.ToolCall
	}
	calls := make(map[string]callLocation)
	for messageIdx, msg := range messages {
		for _, call := range msg.ToolCalls {
			if call.ID != "" {
				calls[call.ID] = callLocation{message: messageIdx, call: call}
			}
		}
	}
	pairs := make([]decisionToolPair, 0)
	for resultIdx, msg := range messages {
		if msg.ToolResult == nil || msg.ToolResult.CallID == "" {
			continue
		}
		location, ok := calls[msg.ToolResult.CallID]
		if !ok {
			// Orphan tool result with no matching assistant call inside the
			// old-message slice. Conservative keep — the caller will not see
			// this pair in the question set, so it remains untouched.
			continue
		}
		pairs = append(pairs, decisionToolPair{
			ID:           msg.ToolResult.CallID,
			TemporaryID:  fmt.Sprintf("t%d", len(pairs)+1),
			ToolName:     location.call.Name,
			ResultChars:  len([]rune(msg.ToolResult.Content)),
			AssistantIdx: location.message,
			ResultIdx:    resultIdx,
			Call:         location.call,
			Result:       *msg.ToolResult,
		})
	}
	return pairs
}

func decisionGoal(history []types.Message) string {
	goals := make([]string, 0, decisionGoalCount)
	for i := len(history) - 1; i >= 0 && len(goals) < decisionGoalCount; i-- {
		msg := history[i]
		if msg.Role != types.RoleUser || messageHasImage(msg) || strings.TrimSpace(msg.Content) == "" {
			continue
		}
		runes := []rune(msg.Content)
		if len(runes) > decisionGoalLimit {
			runes = runes[:decisionGoalLimit]
		}
		goals = append(goals, string(runes))
	}
	for left, right := 0, len(goals)-1; left < right; left, right = left+1, right-1 {
		goals[left], goals[right] = goals[right], goals[left]
	}
	return strings.Join(goals, "\n")
}

func decisionHistory(messages []types.Message, pairs []decisionToolPair) []decisionCompactMessage {
	temporaryIDs := make(map[string]string, len(pairs))
	for _, pair := range pairs {
		temporaryIDs[pair.ID] = pair.TemporaryID
	}
	history := make([]decisionCompactMessage, 0, len(messages))
	for i, msg := range messages {
		record := decisionCompactMessage{Index: i, Role: msg.Role, Text: msg.Content}
		if msg.Role == types.RoleTool || msg.ToolResult != nil {
			record.Text = ""
		}
		for _, call := range msg.ToolCalls {
			temporaryID, ok := temporaryIDs[call.ID]
			if !ok {
				continue
			}
			record.ToolCalls = append(record.ToolCalls, decisionCompactCall{ID: temporaryID, Tool: call.Name, Arguments: call.Arguments})
		}
		if msg.ToolResult != nil {
			if temporaryID, ok := temporaryIDs[msg.ToolResult.CallID]; ok {
				record.ToolResult = &decisionCompactResult{
					ID: temporaryID, Success: msg.ToolResult.Success, Status: msg.ToolResult.Status,
					ErrorCode: msg.ToolResult.ErrorCode, RuneCount: len([]rune(msg.ToolResult.Content)), Omitted: true,
				}
			}
		}
		history = append(history, record)
	}
	return history
}

func decisionQuestions(pairs []decisionToolPair) map[string]providers.DecisionQuestion {
	questions := make(map[string]providers.DecisionQuestion, len(pairs)*2)
	for _, pair := range pairs {
		questions["call_"+pair.TemporaryID] = providers.NoulQuestion{
			Instructions: fmt.Sprintf("Tool call %s (%s) should stay in the history: knowing this call was made, with its input, still matters for what the assistant does next", pair.TemporaryID, pair.ToolName),
			Criteria:     &providers.NoulCriteria{True: "The call and its input remain relevant to the current goal.", False: "The call is obsolete or can be safely rediscovered."},
		}
		questions["result_"+pair.TemporaryID] = providers.NoulQuestion{
			Instructions: fmt.Sprintf("The full output of tool call %s (%s, %d chars) should stay in the history verbatim: the assistant still needs its contents and re-running the tool would not do", pair.TemporaryID, pair.ToolName, pair.ResultChars),
			Criteria:     &providers.NoulCriteria{True: "The exact result contents are still needed and are not safely recoverable.", False: "A shortened result is enough or the tool can be run again."},
		}
	}
	return questions
}

func decisionNoul(answer providers.DecisionAnswer) (float64, bool) {
	noul, ok := answer.(providers.NoulAnswer)
	if !ok || math.IsNaN(noul.Noul) || math.IsInf(noul.Noul, 0) || noul.Noul < 0 || noul.Noul > 1 {
		return 0, false
	}
	return noul.Noul, true
}

func (m *Manager) rebuildDecisionPrefix(oldMessages, tailMessages []types.Message, pairs []decisionToolPair, actions map[string]decisionPairAction) ([]types.Message, error) {
	messages := make([]types.Message, len(oldMessages))
	for i, msg := range oldMessages {
		messages[i] = cloneDecisionMessage(msg)
	}
	keepImageIdx := latestImageMessageIndex(append(cloneMessages(oldMessages), tailMessages...))
	for i := range messages {
		if i != keepImageIdx {
			messages[i] = stripMessageImages(messages[i])
		}
	}

	removeResults := make(map[int]bool)
	for _, pair := range pairs {
		switch actions[pair.ID] {
		case decisionDropResult:
			messages[pair.ResultIdx] = pruneMessage(messages[pair.ResultIdx], m.cfg)
		case decisionDropCall:
			calls := messages[pair.AssistantIdx].ToolCalls
			callIdx := -1
			for i := range calls {
				if calls[i].ID == pair.ID {
					callIdx = i
					break
				}
			}
			if callIdx < 0 {
				return nil, fmt.Errorf("tool call %s moved during reconstruction", pair.ID)
			}
			messages[pair.AssistantIdx].ToolCalls = append(calls[:callIdx:callIdx], calls[callIdx+1:]...)
			messages[pair.AssistantIdx].Tokens = 0
			removeResults[pair.ResultIdx] = true
		}
	}

	prefix := make([]types.Message, 0, len(messages))
	for i, msg := range messages {
		if removeResults[i] || messageEmpty(msg) {
			continue
		}
		prefix = append(prefix, msg)
	}
	if err := validateDecisionProjection(prefix, pairs, actions); err != nil {
		return nil, err
	}
	return prefix, nil
}

func cloneDecisionMessage(msg types.Message) types.Message {
	msg.ToolCalls = append([]types.ToolCall(nil), msg.ToolCalls...)
	if msg.ToolResult != nil {
		result := *msg.ToolResult
		msg.ToolResult = &result
	}
	if msg.Image != nil {
		image := *msg.Image
		msg.Image = &image
	}
	msg.Images = append([]types.ImageContent(nil), msg.Images...)
	if msg.Metadata != nil {
		msg.Metadata = make(map[string]string, len(msg.Metadata))
		for key, value := range msg.Metadata {
			msg.Metadata[key] = value
		}
	}
	return msg
}

func messageEmpty(msg types.Message) bool {
	return msg.Content == "" && msg.Reasoning == "" && msg.ReasoningSignature == "" && msg.RedactedThinking == "" && msg.Image == nil && len(msg.Images) == 0 && len(msg.ToolCalls) == 0 && msg.ToolResult == nil
}

func validateDecisionProjection(messages []types.Message, pairs []decisionToolPair, actions map[string]decisionPairAction) error {
	calls := make(map[string]bool)
	results := make(map[string]bool)
	for _, msg := range messages {
		for _, call := range msg.ToolCalls {
			calls[call.ID] = true
		}
		if msg.ToolResult != nil {
			results[msg.ToolResult.CallID] = true
		}
	}
	for _, pair := range pairs {
		if actions[pair.ID] == decisionDropCall {
			if calls[pair.ID] || results[pair.ID] {
				return fmt.Errorf("dropped pair %s remains", pair.ID)
			}
			continue
		}
		if !calls[pair.ID] || !results[pair.ID] {
			return fmt.Errorf("kept pair %s became orphaned", pair.ID)
		}
	}
	for id := range results {
		if !calls[id] {
			return fmt.Errorf("tool result %s is orphaned", id)
		}
	}
	return nil
}
