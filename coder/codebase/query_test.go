package codebase

import (
	"context"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/basenana/friday/core/api"
	coresession "github.com/basenana/friday/core/session"
	"github.com/basenana/friday/core/tools"
)

func TestQueryValidationUsesTrimmedUnicodeRunes(t *testing.T) {
	root := coresession.New("root", nil)
	h := &Hook{root: root, enabled: func() bool { return true }, query: func(context.Context, *coresession.Session, string, uint64, int64) (string, error) { return "ok", nil }}
	req := &api.Request{}
	if err := h.BeforeAgent(context.Background(), root, req); err != nil {
		t.Fatal(err)
	}
	for _, query := range []string{" \n\t ", strings.Repeat("界", maxCodebaseQueryRunes+1)} {
		result, err := req.Tools[0].Handler(context.Background(), &tools.Request{Arguments: map[string]interface{}{"query": query}})
		if err != nil || !result.IsError {
			t.Fatalf("query %q result=%+v err=%v", query[:min(len(query), 8)], result, err)
		}
	}
}

func TestQueryToolDeduplicatesAndSharesTurnLimitAcrossForks(t *testing.T) {
	root := coresession.New("root", nil)
	h := &Hook{root: root, enabled: func() bool { return true }, query: func(context.Context, *coresession.Session, string, uint64, int64) (string, error) { return "ok", nil }}
	existing := tools.NewTool(codebaseContextQueryToolName)
	rootReq := &api.Request{Tools: []*tools.Tool{existing}}
	if err := h.BeforeAgent(context.Background(), root, rootReq); err != nil {
		t.Fatal(err)
	}
	if len(rootReq.Tools) != 1 || rootReq.Tools[0] != existing {
		t.Fatalf("duplicate tool injected: %+v", rootReq.Tools)
	}

	forkReq := &api.Request{}
	if err := h.BeforeAgent(context.Background(), root.Fork(), forkReq); err != nil {
		t.Fatal(err)
	}
	tool := forkReq.Tools[0]
	for i := 0; i < maxCodebaseQueriesPerTurn; i++ {
		result, err := tool.Handler(context.Background(), &tools.Request{Arguments: map[string]interface{}{"query": "q"}})
		if err != nil || result.IsError {
			t.Fatalf("query %d result=%+v err=%v", i, result, err)
		}
	}
	result, _ := tool.Handler(context.Background(), &tools.Request{Arguments: map[string]interface{}{"query": "q"}})
	if !result.IsError {
		t.Fatal("fork did not share root turn query limit")
	}

	next := &api.Request{}
	if err := h.BeforeAgent(context.Background(), root, next); err != nil {
		t.Fatal(err)
	}
	result, err := next.Tools[0].Handler(context.Background(), &tools.Request{Arguments: map[string]interface{}{"query": "q"}})
	if err != nil || result.IsError {
		t.Fatalf("next root turn did not reset query limit: result=%+v err=%v", result, err)
	}
}

func TestQueriesAreSerializedPerHook(t *testing.T) {
	root := coresession.New("root", nil)
	entered := make(chan struct{}, 2)
	release := make(chan struct{})
	var active atomic.Int32
	var peak atomic.Int32
	h := &Hook{root: root, enabled: func() bool { return true }, query: func(context.Context, *coresession.Session, string, uint64, int64) (string, error) {
		current := active.Add(1)
		if current > peak.Load() {
			peak.Store(current)
		}
		entered <- struct{}{}
		<-release
		active.Add(-1)
		return "ok", nil
	}}
	req := &api.Request{}
	if err := h.BeforeAgent(context.Background(), root, req); err != nil {
		t.Fatal(err)
	}
	call := func() {
		_, _ = req.Tools[0].Handler(context.Background(), &tools.Request{Arguments: map[string]interface{}{"query": "q"}})
	}
	go call()
	<-entered
	go call()
	select {
	case <-entered:
		t.Fatal("second query entered before first completed")
	case <-time.After(30 * time.Millisecond):
	}
	close(release)
	select {
	case <-entered:
	case <-time.After(time.Second):
		t.Fatal("second query never ran")
	}
	if peak.Load() != 1 {
		t.Fatalf("peak concurrent queries=%d", peak.Load())
	}
}
