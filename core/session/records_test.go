package session

import (
	"context"
	"testing"
)

func TestSessionRecordForkCopiesThenDiverges(t *testing.T) {
	ctx := context.Background()
	root := New("root", nil)
	if err := root.UpdateRecord(ctx, "instructions", func([]byte) ([]byte, error) {
		return []byte("root"), nil
	}); err != nil {
		t.Fatalf("UpdateRecord(root): %v", err)
	}

	fork := root.Fork()
	got, err := fork.ReadRecord(ctx, "instructions")
	if err != nil || string(got) != "root" {
		t.Fatalf("fork record = %q, %v", got, err)
	}
	if err := fork.UpdateRecord(ctx, "instructions", func([]byte) ([]byte, error) {
		return []byte("fork"), nil
	}); err != nil {
		t.Fatalf("UpdateRecord(fork): %v", err)
	}

	rootValue, _ := root.ReadRecord(ctx, "instructions")
	forkValue, _ := fork.ReadRecord(ctx, "instructions")
	if string(rootValue) != "root" || string(forkValue) != "fork" {
		t.Fatalf("records did not diverge: root=%q fork=%q", rootValue, forkValue)
	}
}

func TestTemporaryChildStartsWithEmptyRecords(t *testing.T) {
	ctx := context.Background()
	root := New("root", nil)
	if err := root.UpdateRecord(ctx, "instructions", func([]byte) ([]byte, error) {
		return []byte("root"), nil
	}); err != nil {
		t.Fatal(err)
	}

	child := root.NewTemporaryChild()
	if _, err := child.ReadRecord(ctx, "instructions"); err != ErrRecordNotFound {
		t.Fatalf("temporary child ReadRecord error = %v, want ErrRecordNotFound", err)
	}
}

func TestReplaceHistoryKeepsSessionRecords(t *testing.T) {
	ctx := context.Background()
	sess := New("root", nil)
	if err := sess.UpdateRecord(ctx, "instructions", func([]byte) ([]byte, error) {
		return []byte("loaded"), nil
	}); err != nil {
		t.Fatal(err)
	}
	if err := sess.ReplaceHistory(); err != nil {
		t.Fatal(err)
	}
	got, err := sess.ReadRecord(ctx, "instructions")
	if err != nil || string(got) != "loaded" {
		t.Fatalf("record after ReplaceHistory = %q, %v", got, err)
	}
}
