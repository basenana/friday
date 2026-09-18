// Package usage persists root-session model and turn usage aggregates.
package usage

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/basenana/friday/core/providers"
	coresession "github.com/basenana/friday/core/session"
)

const (
	// RecordNamespace is the Session record stored as state/usage.json.
	RecordNamespace = "usage"
	currentVersion  = 1
)

// Snapshot is the durable aggregate for one root Session.
type Snapshot struct {
	Version       int          `json:"version"`
	TrackingSince time.Time    `json:"tracking_since"`
	UpdatedAt     time.Time    `json:"updated_at"`
	Models        []ModelUsage `json:"models"`
	Turns         TurnUsage    `json:"turns"`
}

// ModelUsage groups calls by the actual model and endpoint that served them.
type ModelUsage struct {
	Model               string `json:"model"`
	Endpoint            string `json:"endpoint,omitempty"`
	Calls               int64  `json:"calls"`
	FailedCalls         int64  `json:"failed_calls"`
	PromptTokens        int64  `json:"prompt_tokens"`
	CachedPromptTokens  int64  `json:"cached_prompt_tokens"`
	CacheCreationTokens int64  `json:"cache_creation_tokens"`
	CompletionTokens    int64  `json:"completion_tokens"`
}

// TurnUsage contains root Actor turn counts and cumulative wall-clock time.
type TurnUsage struct {
	Count         int64 `json:"count"`
	EndTurn       int64 `json:"end_turn"`
	PlanCompleted int64 `json:"plan_completed"`
	Failed        int64 `json:"failed"`
	Cancelled     int64 `json:"cancelled"`
	DurationMs    int64 `json:"duration_ms"`
}

// Hook accumulates every React model call on the root Session. Forked Sessions
// inherit the hook, so their calls contribute to the same durable snapshot.
type Hook struct{}

var _ coresession.AfterModelCallHook = Hook{}

// AfterModelCall records one completed or failed React model call.
func (Hook) AfterModelCall(ctx context.Context, sess *coresession.Session, _ providers.Request, stats *coresession.ModelCallStats) error {
	if sess == nil || stats == nil {
		return nil
	}
	return update(ctx, sess, func(snapshot *Snapshot) {
		model := strings.TrimSpace(stats.Model)
		if model == "" {
			model = "(unknown)"
		}
		endpoint := strings.TrimSpace(stats.EndpointKey)
		index := modelIndex(snapshot.Models, model, endpoint)
		if index < 0 {
			snapshot.Models = append(snapshot.Models, ModelUsage{Model: model, Endpoint: endpoint})
			index = len(snapshot.Models) - 1
		}
		entry := &snapshot.Models[index]
		entry.Calls++
		if stats.Err != "" {
			entry.FailedCalls++
		}
		entry.PromptTokens += stats.Tokens.PromptTokens
		entry.CachedPromptTokens += stats.Tokens.CachedPromptTokens
		entry.CacheCreationTokens += stats.Tokens.CacheCreationTokens
		entry.CompletionTokens += stats.Tokens.CompletionTokens
	})
}

// RecordTurn adds one finished root Actor turn to the durable aggregate.
func RecordTurn(ctx context.Context, sess *coresession.Session, stopReason string, durationMs int64) error {
	if sess == nil {
		return errors.New("usage: session is required")
	}
	if durationMs < 0 {
		durationMs = 0
	}
	return update(ctx, sess, func(snapshot *Snapshot) {
		snapshot.Turns.Count++
		snapshot.Turns.DurationMs += durationMs
		switch stopReason {
		case "end_turn":
			snapshot.Turns.EndTurn++
		case "plan_completed":
			snapshot.Turns.PlanCompleted++
		case "error":
			snapshot.Turns.Failed++
		case "cancelled":
			snapshot.Turns.Cancelled++
		}
	})
}

// Read loads a root Session's usage. A missing record is an empty snapshot.
func Read(ctx context.Context, sess *coresession.Session) (Snapshot, error) {
	if sess == nil {
		return Snapshot{}, errors.New("usage: session is required")
	}
	raw, err := rootSession(sess).ReadRecord(ctx, RecordNamespace)
	if errors.Is(err, coresession.ErrRecordNotFound) {
		return newSnapshot(), nil
	}
	if err != nil {
		return Snapshot{}, err
	}
	return decodeSnapshot(raw)
}

func update(ctx context.Context, sess *coresession.Session, apply func(*Snapshot)) error {
	root := rootSession(sess)
	return root.UpdateRecord(ctx, RecordNamespace, func(raw []byte) ([]byte, error) {
		snapshot, err := decodeSnapshot(raw)
		if err != nil {
			return nil, err
		}
		now := time.Now().UTC()
		if snapshot.TrackingSince.IsZero() {
			snapshot.TrackingSince = now
		}
		apply(&snapshot)
		snapshot.UpdatedAt = now
		return json.Marshal(snapshot)
	})
}

func decodeSnapshot(raw []byte) (Snapshot, error) {
	if len(raw) == 0 {
		return newSnapshot(), nil
	}
	var snapshot Snapshot
	if err := json.Unmarshal(raw, &snapshot); err != nil {
		return Snapshot{}, fmt.Errorf("usage: decode snapshot: %w", err)
	}
	if snapshot.Version != currentVersion {
		return Snapshot{}, fmt.Errorf("usage: unsupported snapshot version %d", snapshot.Version)
	}
	if snapshot.Models == nil {
		snapshot.Models = []ModelUsage{}
	}
	return snapshot, nil
}

func newSnapshot() Snapshot {
	return Snapshot{Version: currentVersion, Models: []ModelUsage{}}
}

func rootSession(sess *coresession.Session) *coresession.Session {
	if sess.Root != nil {
		return sess.Root
	}
	return sess
}

func modelIndex(models []ModelUsage, model, endpoint string) int {
	for i := range models {
		if models[i].Model == model && models[i].Endpoint == endpoint {
			return i
		}
	}
	return -1
}
