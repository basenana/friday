package session

import (
	"encoding/json"
	"reflect"
	"strings"
	"time"

	"github.com/basenana/friday/core/providers"
	"github.com/basenana/friday/core/types"
)

// MaxCalibrationSamples is the maximum number of calibration samples to keep
// for the sliding average calibration factor.
const MaxCalibrationSamples = 20

// TokenCalibration tracks runtime token calibration state.
//
// Deprecated: Superseded by TokenCheckpoint. Retained for backward compatibility.
type TokenCalibration struct {
	LastActualPromptTokens   int64   // Last API-returned prompt_tokens
	LastFuzzyPromptTokens    int64   // Last estimated history tokens for the same request
	LastPromptOverheadTokens int64   // Estimated non-history prompt overhead for the same request
	CalibrationFactor        float64 // Running average of actual/fuzzy ratio for unknown messages
	CalibrationSamples       int     // Number of calibration samples collected
}

// EstimateRequestOverhead returns the approximate prompt-token cost of data
// carried outside session history, such as the system prompt and tool schemas.
func EstimateRequestOverhead(req providers.Request) int64 {
	if req == nil {
		return 0
	}

	var total int64
	if prompt := strings.TrimSpace(req.SystemPrompt()); prompt != "" {
		total += types.Message{Role: types.RoleSystem, Content: prompt}.EstimatedTokens()
	}

	for _, tool := range req.ToolDefines() {
		total += estimateToolTokens(tool)
	}
	return total
}

func estimateToolTokens(tool providers.ToolDefine) int64 {
	if tool == nil {
		return 0
	}

	params, _ := json.Marshal(tool.GetParameters())
	body := tool.GetName() + "\n" + tool.GetDescription() + "\n" + string(params)
	return types.Message{Role: types.RoleSystem, Content: body}.EstimatedTokens()
}

func normalizedCalibrationFactor(factor float64) float64 {
	if factor <= 0 {
		return 1
	}
	return factor
}

// CalibrateAndBackfill updates runtime token calibration using the exact
// request payload that was sent to the provider. Only request messages that can
// be mapped back to unchanged session history receive exact per-message tokens.
//
// Deprecated: This function is superseded by the TokenCheckpoint mechanism in
// ContextState. It is retained for backward compatibility but is no longer called
// in the main agent loop. It may be removed in a future version along with
// TokenCalibration, CalibratedTokenCount, and related helpers.
func CalibrateAndBackfill(sess *Session, req providers.Request, actualPromptTokens int64) {
	if sess == nil || req == nil || actualPromptTokens <= 0 {
		return
	}

	requestHistory := req.History()
	if len(requestHistory) == 0 {
		return
	}

	promptOverhead := EstimateRequestOverhead(req)
	historyTarget := actualPromptTokens - promptOverhead
	if historyTarget < 0 {
		historyTarget = 0
	}

	var (
		knownTokens     int64
		unknownFuzzySum int64
		estimatedTotal  int64
	)
	for _, msg := range requestHistory {
		if msg.Tokens != 0 {
			knownTokens += msg.Tokens
			estimatedTotal += msg.Tokens
			continue
		}

		fuzzy := msg.EstimatedTokens()
		unknownFuzzySum += fuzzy
		estimatedTotal += fuzzy
	}

	remaining := historyTarget - knownTokens
	if remaining < 0 {
		remaining = 0
	}

	var (
		updates   map[int]int64
		writer    MessageWriter
		sessionID string
		temporary bool
		fullCopy  []types.Message
		needSync  bool
	)

	sess.mu.Lock()
	if len(sess.History) == 0 {
		sess.mu.Unlock()
		return
	}

	cal := &sess.tokenCalibration
	cal.LastActualPromptTokens = actualPromptTokens
	cal.LastFuzzyPromptTokens = estimatedTotal
	cal.LastPromptOverheadTokens = promptOverhead

	if unknownFuzzySum > 0 && remaining > 0 {
		updateCalibrationFactor(cal, float64(remaining)/float64(unknownFuzzySum))
		updates = applyRequestBackfillLocked(sess.History, requestHistory, remaining)
		if len(updates) > 0 {
			needSync = true
		}
	}

	writer = sess.writer
	sessionID = sess.ID
	temporary = sess.Temporary
	if needSync && writer != nil && !temporary {
		if _, ok := writer.(MessageTokenUpdater); !ok {
			fullCopy = make([]types.Message, len(sess.History))
			copy(fullCopy, sess.History)
		}
	}
	sess.mu.Unlock()

	if !needSync || writer == nil || temporary {
		return
	}

	if updater, ok := writer.(MessageTokenUpdater); ok {
		_ = updater.UpdateMessageTokens(sessionID, updates)
		return
	}
	_ = writer.ReplaceMessages(sessionID, fullCopy...)
}

func updateCalibrationFactor(cal *TokenCalibration, ratio float64) {
	if ratio <= 0 {
		return
	}

	n := cal.CalibrationSamples
	if n >= MaxCalibrationSamples {
		cal.CalibrationFactor = (cal.CalibrationFactor*float64(n-1) + ratio) / float64(n)
		return
	}

	cal.CalibrationSamples++
	cal.CalibrationFactor = (cal.CalibrationFactor*float64(n) + ratio) / float64(cal.CalibrationSamples)
}

func applyRequestBackfillLocked(history, requestHistory []types.Message, total int64) map[int]int64 {
	allocations := allocateRequestTokens(requestHistory, total)
	if len(allocations) == 0 {
		return nil
	}

	matches := matchRequestMessages(history, requestHistory)
	updates := make(map[int]int64)
	for reqIdx, tokenCount := range allocations {
		historyIdx := matches[reqIdx]
		if historyIdx < 0 || historyIdx >= len(history) || tokenCount <= 0 {
			continue
		}
		if history[historyIdx].Tokens != 0 {
			continue
		}

		history[historyIdx].Tokens = tokenCount
		updates[historyIdx] = tokenCount
	}

	if len(updates) == 0 {
		return nil
	}
	return updates
}

func allocateRequestTokens(requestHistory []types.Message, total int64) map[int]int64 {
	if total <= 0 {
		return nil
	}

	var (
		fuzzySum  int64
		lastIndex = -1
	)
	for i, msg := range requestHistory {
		if msg.Tokens != 0 {
			continue
		}
		fuzzy := msg.EstimatedTokens()
		if fuzzy <= 0 {
			continue
		}
		fuzzySum += fuzzy
		lastIndex = i
	}
	if fuzzySum <= 0 || lastIndex < 0 {
		return nil
	}

	allocations := make(map[int]int64)
	var allocated int64
	for i, msg := range requestHistory {
		if msg.Tokens != 0 {
			continue
		}
		fuzzy := msg.EstimatedTokens()
		if fuzzy <= 0 {
			continue
		}
		share := int64(float64(fuzzy) * float64(total) / float64(fuzzySum))
		allocations[i] = share
		allocated += share
	}
	allocations[lastIndex] += total - allocated
	return allocations
}

func matchRequestMessages(history, requestHistory []types.Message) []int {
	matches := make([]int, len(requestHistory))
	for i := range matches {
		matches[i] = -1
	}

	j := len(history) - 1
	for i := len(requestHistory) - 1; i >= 0 && j >= 0; i-- {
		for ; j >= 0; j-- {
			if messagesEquivalentForBackfill(history[j], requestHistory[i]) {
				matches[i] = j
				j--
				break
			}
		}
	}
	return matches
}

func messagesEquivalentForBackfill(historyMsg, requestMsg types.Message) bool {
	if historyMsg.Role != requestMsg.Role {
		return false
	}
	if historyMsg.Content != requestMsg.Content || historyMsg.Reasoning != requestMsg.Reasoning {
		return false
	}
	if !sameTime(historyMsg.Time, requestMsg.Time) {
		return false
	}
	if !reflect.DeepEqual(historyMsg.Image, requestMsg.Image) {
		return false
	}
	if !reflect.DeepEqual(historyMsg.ToolCalls, requestMsg.ToolCalls) {
		return false
	}
	if !reflect.DeepEqual(historyMsg.ToolResult, requestMsg.ToolResult) {
		return false
	}
	return true
}

func sameTime(a, b time.Time) bool {
	if a.IsZero() || b.IsZero() {
		return a.IsZero() && b.IsZero()
	}
	return a.Equal(b)
}

// CalibratedTokenCount returns token count using the session calibration factor
// for messages without exact stored token values.
func CalibratedTokenCount(history []types.Message, factor float64) int64 {
	factor = normalizedCalibrationFactor(factor)

	var total int64
	for _, msg := range history {
		if msg.Tokens != 0 {
			total += msg.Tokens
			continue
		}
		total += int64(float64(msg.EstimatedTokens()) * factor)
	}
	return total
}
