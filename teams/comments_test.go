package teams

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestAppendAndQueryComments(t *testing.T) {
	dir := t.TempDir()
	team := "alpha"

	comments := []Comment{
		{TS: time.Now().UTC(), From: "leader", Text: "kicking off T01", Kind: CommentKindProgress, Anchor: Anchor{ProposalID: "p1", TaskID: "T01"}},
		{TS: time.Now().UTC(), From: "dev", Text: "T01 done", Kind: CommentKindReview, Anchor: Anchor{ProposalID: "p1", TaskID: "T01"}},
		{TS: time.Now().UTC(), From: "leader", Text: "unrelated note", Kind: CommentKindNote, Anchor: Anchor{ProposalID: "p2"}},
	}
	for _, c := range comments {
		if err := AppendComment(dir, team, c); err != nil {
			t.Fatalf("AppendComment: %v", err)
		}
	}

	// File exists at expected path.
	if _, err := os.Stat(filepath.Join(dir, team, "comments.jsonl")); err != nil {
		t.Fatalf("expected file: %v", err)
	}

	// Filter by proposal + task.
	got, err := QueryComments(dir, team, CommentFilter{ProposalID: "p1", TaskID: "T01"})
	if err != nil {
		t.Fatalf("QueryComments: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("expected 2 matching comments, got %d", len(got))
	}

	// Filter by author.
	got, err = QueryComments(dir, team, CommentFilter{From: "leader"})
	if err != nil {
		t.Fatalf("QueryComments: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("expected 2 from leader, got %d", len(got))
	}

	// Limit.
	got, err = QueryComments(dir, team, CommentFilter{Limit: 1})
	if err != nil {
		t.Fatalf("QueryComments: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("expected limit respected, got %d", len(got))
	}

	// Missing file → nil, nil.
	got, err = QueryComments(dir, "nonexistent", CommentFilter{})
	if err != nil {
		t.Fatalf("QueryComments missing team: %v", err)
	}
	if got != nil {
		t.Fatalf("expected nil for missing team, got %v", got)
	}
}

func TestQueryComments_SkipsCorruptLines(t *testing.T) {
	dir := t.TempDir()
	team := "beta"
	if err := AppendComment(dir, team, Comment{From: "a", Text: "good"}); err != nil {
		t.Fatal(err)
	}
	// Append a malformed line manually.
	path := filepath.Join(dir, team, "comments.jsonl")
	f, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0644)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.WriteString("this is not json\n"); err != nil {
		t.Fatal(err)
	}
	f.Close()
	if err := AppendComment(dir, team, Comment{From: "b", Text: "good2"}); err != nil {
		t.Fatal(err)
	}

	got, err := QueryComments(dir, team, CommentFilter{})
	if err != nil {
		t.Fatalf("QueryComments: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("expected corrupt line skipped, got %d valid", len(got))
	}
}
