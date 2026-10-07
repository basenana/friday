package contextmgr

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/basenana/friday/core/session"
	"github.com/basenana/friday/core/types"
)

func TestDecisionCacheEpochStableWithinUserTurn(t *testing.T) {
	history := []types.Message{
		{Role: types.RoleSystem, Content: "system"},
		{Role: types.RoleUser, Content: "goal", Tokens: 10, Time: time.Unix(1, 0), Metadata: map[string]string{"trace": "one"}},
	}
	want, ok := decisionEpochKey("session-a", history)
	if !ok {
		t.Fatal("decisionEpochKey returned ok=false")
	}

	changedAccounting := append([]types.Message(nil), history...)
	changedAccounting[1].Tokens = 99
	changedAccounting[1].Time = time.Unix(99, 0)
	changedAccounting[1].Metadata = map[string]string{"trace": "two"}
	got, ok := decisionEpochKey("session-a", changedAccounting)
	if !ok || got != want {
		t.Fatalf("accounting-only changes changed epoch: got %q want %q", got, want)
	}

	appended := append(changedAccounting,
		types.Message{Role: types.RoleAssistant, ToolCalls: []types.ToolCall{{ID: "call-1", Name: "fs_read", Arguments: `{"path":"x"}`}}},
		types.Message{Role: types.RoleTool, ToolResult: &types.ToolResult{CallID: "call-1", Content: "result", Success: true}},
	)
	got, ok = decisionEpochKey("session-a", appended)
	if !ok || got != want {
		t.Fatalf("assistant/tool append changed epoch: got %q want %q", got, want)
	}
}

func TestDecisionCacheEpochChangesWithSemanticBoundary(t *testing.T) {
	base := []types.Message{
		{Role: types.RoleSystem, Content: "system"},
		{Role: types.RoleUser, Content: "goal", Reasoning: "why", Images: []types.ImageContent{{Type: types.ImageTypeURL, URL: "https://example.test/image"}}},
	}
	baseKey, ok := decisionEpochKey("session-a", base)
	if !ok {
		t.Fatal("decisionEpochKey returned ok=false")
	}

	tests := []struct {
		name      string
		sessionID string
		history   []types.Message
	}{
		{name: "session", sessionID: "session-b", history: base},
		{name: "new user", sessionID: "session-a", history: append(append([]types.Message(nil), base...), types.Message{Role: types.RoleUser, Content: "next"})},
		{name: "old semantic content", sessionID: "session-a", history: []types.Message{{Role: types.RoleSystem, Content: "changed"}, base[1]}},
		{name: "reasoning", sessionID: "session-a", history: []types.Message{base[0], {Role: types.RoleUser, Content: "goal", Reasoning: "different", Images: base[1].Images}}},
		{name: "images", sessionID: "session-a", history: []types.Message{base[0], {Role: types.RoleUser, Content: "goal", Reasoning: "why", Images: []types.ImageContent{{Type: types.ImageTypeURL, URL: "https://example.test/other"}}}}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, ok := decisionEpochKey(tt.sessionID, tt.history)
			if !ok || got == baseKey {
				t.Fatalf("semantic change did not change epoch: got %q base %q", got, baseKey)
			}
		})
	}
}

func TestDecisionCacheEpochRequiresUserMessage(t *testing.T) {
	if key, ok := decisionEpochKey("session-a", []types.Message{{Role: types.RoleAssistant, Content: "hello"}}); ok || key != "" {
		t.Fatalf("decisionEpochKey = %q, %v; want empty, false", key, ok)
	}
}

func TestDecisionPairHashCoversCallAndFullResult(t *testing.T) {
	call := types.ToolCall{ID: "call-1", Name: "fs_read", Arguments: `{"path":"a"}`}
	result := types.ToolResult{CallID: "call-1", Content: "SECRET-BODY", Success: true, Status: "ok", ElapsedMs: 5}
	base := decisionPairHash(call, result)

	tests := []struct {
		name   string
		call   types.ToolCall
		result types.ToolResult
	}{
		{name: "id", call: types.ToolCall{ID: "call-2", Name: call.Name, Arguments: call.Arguments}, result: result},
		{name: "name", call: types.ToolCall{ID: call.ID, Name: "fs_search", Arguments: call.Arguments}, result: result},
		{name: "arguments", call: types.ToolCall{ID: call.ID, Name: call.Name, Arguments: `{"path":"b"}`}, result: result},
		{name: "content", call: call, result: types.ToolResult{CallID: result.CallID, Content: "OTHER", Success: true, Status: result.Status, ElapsedMs: result.ElapsedMs}},
		{name: "status", call: call, result: types.ToolResult{CallID: result.CallID, Content: result.Content, Success: false, Status: "error", ErrorCode: "E", ElapsedMs: result.ElapsedMs}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := decisionPairHash(tt.call, tt.result); got == base {
				t.Fatalf("pair hash did not change for %s", tt.name)
			}
		})
	}

	encoded, err := json.Marshal(decisionEpochCache{
		Version: decisionCacheVersion,
		Epoch:   "epoch",
		Decisions: map[string]decisionCachedAction{
			call.ID: {PairHash: base, Action: "keep"},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(encoded), result.Content) {
		t.Fatalf("cache record leaked result body: %s", encoded)
	}
}

func TestDecisionCacheRecordMergeReplacesEpochAndPreservesExistingAction(t *testing.T) {
	sess := session.New("session-a", nil)
	ctx := context.Background()
	first := map[string]decisionCachedAction{
		"call-1": {PairHash: "hash-1", Action: "keep"},
	}
	if err := mergeDecisionCache(ctx, sess, "epoch-1", first); err != nil {
		t.Fatal(err)
	}
	if err := mergeDecisionCache(ctx, sess, "epoch-1", map[string]decisionCachedAction{
		"call-1": {PairHash: "other", Action: "drop_call"},
		"call-2": {PairHash: "hash-2", Action: "drop_result"},
	}); err != nil {
		t.Fatal(err)
	}
	cache := loadDecisionCache(ctx, sess, "epoch-1")
	if got := cache.Decisions["call-1"]; got != first["call-1"] {
		t.Fatalf("existing action overwritten: got %#v want %#v", got, first["call-1"])
	}
	if got := cache.Decisions["call-2"].Action; got != "drop_result" {
		t.Fatalf("new action = %q, want drop_result", got)
	}

	if err := mergeDecisionCache(ctx, sess, "epoch-2", map[string]decisionCachedAction{
		"call-3": {PairHash: "hash-3", Action: "drop_call"},
	}); err != nil {
		t.Fatal(err)
	}
	cache = loadDecisionCache(ctx, sess, "epoch-2")
	if len(cache.Decisions) != 1 || cache.Decisions["call-3"].Action != "drop_call" {
		t.Fatalf("new epoch cache = %#v", cache.Decisions)
	}
}

func TestDecisionCacheRecordRejectsInvalidDataAndActions(t *testing.T) {
	ctx := context.Background()
	tests := []struct {
		name string
		data []byte
	}{
		{name: "corrupt", data: []byte("{")},
		{name: "version", data: mustDecisionCacheJSON(t, decisionEpochCache{Version: decisionCacheVersion + 1, Epoch: "epoch", Decisions: map[string]decisionCachedAction{"call": {PairHash: "hash", Action: "keep"}}})},
		{name: "epoch", data: mustDecisionCacheJSON(t, decisionEpochCache{Version: decisionCacheVersion, Epoch: "other", Decisions: map[string]decisionCachedAction{"call": {PairHash: "hash", Action: "keep"}}})},
		{name: "unknown action", data: mustDecisionCacheJSON(t, decisionEpochCache{Version: decisionCacheVersion, Epoch: "epoch", Decisions: map[string]decisionCachedAction{"call": {PairHash: "hash", Action: "future"}}})},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			sess := session.New("session-a", nil)
			if err := sess.UpdateRecord(ctx, decisionCacheNamespace, func([]byte) ([]byte, error) { return tt.data, nil }); err != nil {
				t.Fatal(err)
			}
			cache := loadDecisionCache(ctx, sess, "epoch")
			if len(cache.Decisions) != 0 {
				t.Fatalf("invalid cache loaded decisions: %#v", cache.Decisions)
			}
		})
	}
}

func TestDecisionCacheRecordUsesFixedNamespaceAndSurvivesReload(t *testing.T) {
	store := &memoryDecisionRecordStore{records: make(map[string][]byte)}
	ctx := context.Background()
	first := session.New("session-a", nil, session.WithRecordStore(store))
	if err := mergeDecisionCache(ctx, first, "epoch", map[string]decisionCachedAction{
		"call": {PairHash: "hash", Action: "keep"},
	}); err != nil {
		t.Fatal(err)
	}
	if got := store.namespaces(); len(got) != 1 || got[0] != "decisioncache" {
		t.Fatalf("record namespaces = %v, want [decisioncache]", got)
	}

	reloaded := session.New("session-a", nil, session.WithRecordStore(store))
	cache := loadDecisionCache(ctx, reloaded, "epoch")
	if got := cache.Decisions["call"]; got.PairHash != "hash" || got.Action != "keep" {
		t.Fatalf("reloaded action = %#v", got)
	}
}

func mustDecisionCacheJSON(t *testing.T, cache decisionEpochCache) []byte {
	t.Helper()
	data, err := json.Marshal(cache)
	if err != nil {
		t.Fatal(err)
	}
	return data
}

type memoryDecisionRecordStore struct {
	mu      sync.Mutex
	records map[string][]byte
	err     error
	seen    []string
}

func (s *memoryDecisionRecordStore) ReadSessionRecord(_ context.Context, sessionID, namespace string) ([]byte, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.seen = append(s.seen, namespace)
	if s.err != nil {
		return nil, s.err
	}
	data, ok := s.records[sessionID+"/"+namespace]
	if !ok {
		return nil, session.ErrRecordNotFound
	}
	return append([]byte(nil), data...), nil
}

func (s *memoryDecisionRecordStore) UpdateSessionRecord(_ context.Context, sessionID, namespace string, update func([]byte) ([]byte, error)) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.seen = append(s.seen, namespace)
	if s.err != nil {
		return s.err
	}
	key := sessionID + "/" + namespace
	next, err := update(append([]byte(nil), s.records[key]...))
	if err != nil {
		return err
	}
	s.records[key] = append([]byte(nil), next...)
	return nil
}

func (s *memoryDecisionRecordStore) namespaces() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	seen := make(map[string]bool)
	var namespaces []string
	for _, namespace := range s.seen {
		if !seen[namespace] {
			seen[namespace] = true
			namespaces = append(namespaces, namespace)
		}
	}
	return namespaces
}

var _ session.RecordStore = (*memoryDecisionRecordStore)(nil)
var _ = errors.New
