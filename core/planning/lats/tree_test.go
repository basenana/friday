package lats

import (
	"context"
	"math"
	"sync"
	"testing"

	"github.com/basenana/friday/core/session"
	"github.com/basenana/friday/core/types"
)

func TestBackPropagate_RunningAverage(t *testing.T) {
	sess := session.New(types.NewID(), nil)
	root := newRoot("root", sess)

	child := newNode("child")
	root.Expand(context.Background(), child, &Evaluation{Score: 10})

	// After first backprop with reward=10: visits=1, score=10
	if child.visits != 1 {
		t.Fatalf("expected child visits=1, got %d", child.visits)
	}
	if child.evaluation.Score != 10 {
		t.Fatalf("expected child score=10, got %v", child.evaluation.Score)
	}
	if root.visits != 1 {
		t.Fatalf("expected root visits=1, got %d", root.visits)
	}
	if root.evaluation.Score != 10 {
		t.Fatalf("expected root score=10, got %v", root.evaluation.Score)
	}

	grandchild := newNode("grandchild")
	child.Expand(context.Background(), grandchild, &Evaluation{Score: 20})

	// After second backprop with reward=20:
	// grandchild: visits=1, score=20
	// child: visits=2, score=(20 + 1*10)/2 = 15
	// root: visits=2, score=(20 + 1*10)/2 = 15
	if grandchild.evaluation.Score != 20 {
		t.Fatalf("expected grandchild score=20, got %v", grandchild.evaluation.Score)
	}
	if child.evaluation.Score != 15 {
		t.Fatalf("expected child score=15, got %v", child.evaluation.Score)
	}
	if root.evaluation.Score != 15 {
		t.Fatalf("expected root score=15, got %v", root.evaluation.Score)
	}
}

func TestBackPropagate_Concurrent(t *testing.T) {
	sess := session.New(types.NewID(), nil)
	root := newRoot("root", sess)

	child := newNode("child")
	root.Expand(context.Background(), child, &Evaluation{Score: 0})

	var wg sync.WaitGroup
	for i := 0; i < 100; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			leaf := newNode("leaf")
			child.Expand(context.Background(), leaf, &Evaluation{Score: 50})
		}()
	}
	wg.Wait()

	// newRoot starts with visits=0; first Expand increments root and child to 1;
	// each of the 100 goroutines increments both by 1 more.
	if root.visits != 101 {
		t.Fatalf("expected root visits=101, got %d", root.visits)
	}
	if child.visits != 101 {
		t.Fatalf("expected child visits=101, got %d", child.visits)
	}
}

func TestUpperConfidenceBound_ZeroVisits(t *testing.T) {
	sess := session.New(types.NewID(), nil)
	root := newRoot("root", sess)
	child := newNode("child")
	root.Expand(context.Background(), child, &Evaluation{Score: 50})

	// Reset visits to 0 to simulate unvisited node
	child.mu.Lock()
	child.visits = 0
	child.mu.Unlock()

	score := child.upperConfidenceBound()
	if score != math.MaxFloat64 {
		t.Fatalf("expected MaxFloat64 for unvisited node, got %f", score)
	}
}

func TestUpperConfidenceBound_Normal(t *testing.T) {
	sess := session.New(types.NewID(), nil)
	root := newRoot("root", sess)
	// newRoot starts visits at 0; Expand will increment it by 1 via BackPropagate.
	// We want parentVisits to be 10 after Expand, so set it to 9 here.
	root.visits = 9

	child := newNode("child")
	root.Expand(context.Background(), child, &Evaluation{Score: 50})
	child.visits = 2

	score := child.upperConfidenceBound()
	exploitation := float64(50)
	exploration := 2.0 * math.Sqrt(math.Log(float64(10))/float64(2))
	expected := exploitation + exploration

	if math.Abs(score-expected) > 1e-9 {
		t.Fatalf("expected UCB=%f, got %f", expected, score)
	}
}

func TestGetBestNode_SkipsDoneNodes(t *testing.T) {
	sess := session.New(types.NewID(), nil)
	root := newRoot("root", sess)

	done := newNode("done")
	root.Expand(context.Background(), done, &Evaluation{Score: 100, IsDone: true})

	notDone := newNode("notDone")
	root.Expand(context.Background(), notDone, &Evaluation{Score: 1, IsDone: false})

	best := root.GetBestNode()
	if best != notDone {
		t.Fatalf("expected best node to be notDone, got %v", best.Latest())
	}
}

func TestGetBestNode_ReturnsLeaf(t *testing.T) {
	sess := session.New(types.NewID(), nil)
	root := newRoot("root", sess)

	child := newNode("child")
	root.Expand(context.Background(), child, &Evaluation{Score: 50})

	leaf := newNode("leaf")
	child.Expand(context.Background(), leaf, &Evaluation{Score: 100})

	best := root.GetBestNode()
	if best != leaf {
		t.Fatalf("expected best node to be leaf, got %v", best.Latest())
	}
}

func TestGetBestNode_ReturnsSelfWhenNoChildren(t *testing.T) {
	sess := session.New(types.NewID(), nil)
	root := newRoot("root", sess)

	best := root.GetBestNode()
	if best != root {
		t.Fatalf("expected best node to be root when no children, got %v", best.Latest())
	}
}
