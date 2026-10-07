package contextmgr

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"

	"github.com/basenana/friday/core/logger"
	"github.com/basenana/friday/core/session"
	"github.com/basenana/friday/core/types"
)

const (
	decisionCacheNamespace = "decisioncache"
	decisionCacheVersion   = 1
)

type decisionEpochCache struct {
	Version   int                             `json:"version"`
	Epoch     string                          `json:"epoch"`
	Decisions map[string]decisionCachedAction `json:"decisions"`
}

type decisionCachedAction struct {
	PairHash string `json:"pair_hash"`
	Action   string `json:"action"`
}

type decisionEpochPayload struct {
	SessionID string                    `json:"session_id"`
	Version   int                       `json:"version"`
	History   []decisionSemanticMessage `json:"history"`
}

type decisionSemanticMessage struct {
	Role               types.MessageRole    `json:"role"`
	Content            string               `json:"content,omitempty"`
	Reasoning          string               `json:"reasoning,omitempty"`
	ReasoningSignature string               `json:"reasoning_signature,omitempty"`
	RedactedThinking   string               `json:"redacted_thinking,omitempty"`
	Image              *types.ImageContent  `json:"image,omitempty"`
	Images             []types.ImageContent `json:"images,omitempty"`
	ToolCalls          []types.ToolCall     `json:"tool_calls,omitempty"`
	ToolResult         *types.ToolResult    `json:"tool_result,omitempty"`
}

var decisionRecordLogger = logger.New("contextmgr.decisioncache")

func decisionEpochKey(sessionID string, history []types.Message) (string, bool) {
	lastUser := -1
	for i := len(history) - 1; i >= 0; i-- {
		if history[i].Role == types.RoleUser {
			lastUser = i
			break
		}
	}
	if lastUser < 0 {
		return "", false
	}

	semantic := make([]decisionSemanticMessage, lastUser+1)
	for i, msg := range history[:lastUser+1] {
		semantic[i] = decisionSemanticMessage{
			Role:               msg.Role,
			Content:            msg.Content,
			Reasoning:          msg.Reasoning,
			ReasoningSignature: msg.ReasoningSignature,
			RedactedThinking:   msg.RedactedThinking,
			Image:              msg.Image,
			Images:             msg.Images,
			ToolCalls:          msg.ToolCalls,
			ToolResult:         msg.ToolResult,
		}
	}
	encoded, err := json.Marshal(decisionEpochPayload{SessionID: sessionID, Version: decisionCacheVersion, History: semantic})
	if err != nil {
		return "", false
	}
	hash := sha256.Sum256(encoded)
	return hex.EncodeToString(hash[:]), true
}

func decisionPairHash(call types.ToolCall, result types.ToolResult) string {
	encoded, _ := json.Marshal(struct {
		Call   types.ToolCall   `json:"call"`
		Result types.ToolResult `json:"result"`
	}{Call: call, Result: result})
	hash := sha256.Sum256(encoded)
	return hex.EncodeToString(hash[:])
}

func loadDecisionCache(ctx context.Context, sess *session.Session, epoch string) decisionEpochCache {
	empty := decisionEpochCache{Version: decisionCacheVersion, Epoch: epoch, Decisions: make(map[string]decisionCachedAction)}
	if epoch == "" {
		return empty
	}
	data, err := sess.ReadRecord(ctx, decisionCacheNamespace)
	if err != nil {
		if !errors.Is(err, session.ErrRecordNotFound) {
			decisionRecordLogger.Warnw("failed to read decision cache", "session", sess.ID, "error", err)
		}
		return empty
	}
	var cache decisionEpochCache
	if err := json.Unmarshal(data, &cache); err != nil {
		decisionRecordLogger.Warnw("failed to decode decision cache", "session", sess.ID, "error", err)
		return empty
	}
	if cache.Version != decisionCacheVersion || cache.Epoch != epoch {
		return empty
	}
	for id, action := range cache.Decisions {
		if !validDecisionCachedAction(action) {
			delete(cache.Decisions, id)
		}
	}
	if cache.Decisions == nil {
		cache.Decisions = make(map[string]decisionCachedAction)
	}
	return cache
}

func mergeDecisionCache(ctx context.Context, sess *session.Session, epoch string, additions map[string]decisionCachedAction) error {
	if epoch == "" || len(additions) == 0 {
		return nil
	}
	return sess.UpdateRecord(ctx, decisionCacheNamespace, func(current []byte) ([]byte, error) {
		cache := decisionEpochCache{Version: decisionCacheVersion, Epoch: epoch, Decisions: make(map[string]decisionCachedAction)}
		var stored decisionEpochCache
		if json.Unmarshal(current, &stored) == nil && stored.Version == decisionCacheVersion && stored.Epoch == epoch {
			cache = stored
			if cache.Decisions == nil {
				cache.Decisions = make(map[string]decisionCachedAction)
			}
		}
		for id, action := range additions {
			if !validDecisionCachedAction(action) {
				continue
			}
			if existing, ok := cache.Decisions[id]; ok && validDecisionCachedAction(existing) {
				continue
			}
			cache.Decisions[id] = action
		}
		return json.Marshal(cache)
	})
}

func validDecisionCachedAction(action decisionCachedAction) bool {
	if action.PairHash == "" {
		return false
	}
	switch action.Action {
	case "keep", "drop_result", "drop_call":
		return true
	default:
		return false
	}
}
