package usage_test

import (
	"context"
	"errors"
	"path/filepath"
	"sync"
	"testing"

	"github.com/basenana/friday/core/providers"
	coresession "github.com/basenana/friday/core/session"
	sessionfile "github.com/basenana/friday/sessions/file"
	"github.com/basenana/friday/sessions/usage"
)

func TestHookAggregatesRootAndForkCallsByModelAndEndpoint(t *testing.T) {
	store := sessionfile.NewFileSessionStore(filepath.Join(t.TempDir(), "sessions"))
	root, err := store.Create("root", nil)
	if err != nil {
		t.Fatal(err)
	}
	children := []*coresession.Session{root, root.Fork(), root.Fork()}
	hook := usage.Hook{}

	var wg sync.WaitGroup
	errs := make(chan error, len(children)*10)
	for index, sess := range children {
		for call := 0; call < 10; call++ {
			wg.Add(1)
			go func(index int, sess *coresession.Session) {
				defer wg.Done()
				stats := &coresession.ModelCallStats{
					Model:       "shared",
					EndpointKey: []string{"endpoint-a", "endpoint-b", "endpoint-a"}[index],
					Tokens: providers.Tokens{
						PromptTokens: 100, CachedPromptTokens: 60,
						CacheCreationTokens: 10, CompletionTokens: 20,
					},
				}
				if err := hook.AfterModelCall(context.Background(), sess, nil, stats); err != nil {
					errs <- err
				}
			}(index, sess)
		}
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Fatal(err)
	}

	reloaded, err := store.Load("root", nil)
	if err != nil {
		t.Fatal(err)
	}
	snapshot, err := usage.Read(context.Background(), reloaded)
	if err != nil {
		t.Fatal(err)
	}
	if snapshot.Version != 1 || snapshot.TrackingSince.IsZero() || len(snapshot.Models) != 2 {
		t.Fatalf("snapshot metadata = %#v", snapshot)
	}
	a := findModel(t, snapshot, "shared", "endpoint-a")
	b := findModel(t, snapshot, "shared", "endpoint-b")
	if a.Calls != 20 || a.PromptTokens != 2_000 || a.CachedPromptTokens != 1_200 || a.CacheCreationTokens != 200 || a.CompletionTokens != 400 {
		t.Fatalf("endpoint-a usage = %#v", a)
	}
	if b.Calls != 10 || b.PromptTokens != 1_000 || b.CachedPromptTokens != 600 || b.CacheCreationTokens != 100 || b.CompletionTokens != 200 {
		t.Fatalf("endpoint-b usage = %#v", b)
	}
}

func TestHookTracksFailuresAndUnknownModels(t *testing.T) {
	root := coresession.New("root", nil)
	stats := &coresession.ModelCallStats{Err: "failed", Tokens: providers.Tokens{PromptTokens: 5}}
	if err := (usage.Hook{}).AfterModelCall(context.Background(), root, nil, stats); err != nil {
		t.Fatal(err)
	}
	snapshot, err := usage.Read(context.Background(), root)
	if err != nil {
		t.Fatal(err)
	}
	entry := findModel(t, snapshot, "(unknown)", "")
	if entry.Calls != 1 || entry.FailedCalls != 1 || entry.PromptTokens != 5 {
		t.Fatalf("unknown model usage = %#v", entry)
	}
}

func TestRecordTurnCountsEveryFinishedOutcome(t *testing.T) {
	root := coresession.New("root", nil)
	for _, turn := range []struct {
		reason   string
		duration int64
	}{{"end_turn", 100}, {"plan_completed", 200}, {"error", 300}, {"cancelled", 400}} {
		if err := usage.RecordTurn(context.Background(), root, turn.reason, turn.duration); err != nil {
			t.Fatal(err)
		}
	}
	snapshot, err := usage.Read(context.Background(), root)
	if err != nil {
		t.Fatal(err)
	}
	turns := snapshot.Turns
	if turns.Count != 4 || turns.EndTurn != 1 || turns.PlanCompleted != 1 || turns.Failed != 1 || turns.Cancelled != 1 || turns.DurationMs != 1_000 {
		t.Fatalf("turn usage = %#v", turns)
	}
}

func TestReadMissingAndInvalidSnapshots(t *testing.T) {
	root := coresession.New("root", nil)
	snapshot, err := usage.Read(context.Background(), root)
	if err != nil || snapshot.Version != 1 || len(snapshot.Models) != 0 {
		t.Fatalf("missing snapshot = %#v, err=%v", snapshot, err)
	}
	if err := root.UpdateRecord(context.Background(), usage.RecordNamespace, func([]byte) ([]byte, error) {
		return []byte(`{"version":2}`), nil
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := usage.Read(context.Background(), root); err == nil {
		t.Fatal("unsupported snapshot version was accepted")
	}
	if err := (usage.Hook{}).AfterModelCall(context.Background(), root, nil, &coresession.ModelCallStats{Model: "m"}); err == nil {
		t.Fatal("invalid existing snapshot was silently overwritten")
	}
	if _, err := usage.Read(context.Background(), root); err == nil || errors.Is(err, coresession.ErrRecordNotFound) {
		t.Fatalf("invalid snapshot read error = %v", err)
	}
}

func findModel(t *testing.T, snapshot usage.Snapshot, model, endpoint string) usage.ModelUsage {
	t.Helper()
	for _, entry := range snapshot.Models {
		if entry.Model == model && entry.Endpoint == endpoint {
			return entry
		}
	}
	t.Fatalf("model %q via %q not found in %#v", model, endpoint, snapshot.Models)
	return usage.ModelUsage{}
}
