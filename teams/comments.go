package teams

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// CommentsPath returns the comments.jsonl path for a team.
func CommentsPath(teamsPath, teamName string) (string, error) {
	dir, err := teamDirPath(teamsPath, teamName)
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "comments.jsonl"), nil
}

// AppendComment writes one Comment as a single JSON line.
func AppendComment(teamsPath, teamName string, c Comment) error {
	if c.TS.IsZero() {
		c.TS = time.Now().UTC()
	}
	commentPath, err := CommentsPath(teamsPath, teamName)
	if err != nil {
		return err
	}
	dir := filepath.Dir(commentPath)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return err
	}
	data, err := json.Marshal(c)
	if err != nil {
		return err
	}
	f, err := os.OpenFile(commentPath, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
	if err != nil {
		return err
	}
	defer f.Close()
	_, err = fmt.Fprintf(f, "%s\n", data)
	return err
}

// CommentFilter narrows a comment query.
type CommentFilter struct {
	ProposalID string
	TaskID     string
	From       string
	Kind       CommentKind
	Limit      int
}

// Matches reports whether a comment passes the filter.
func (f CommentFilter) Matches(c Comment) bool {
	if f.ProposalID != "" && c.Anchor.ProposalID != f.ProposalID {
		return false
	}
	if f.TaskID != "" && c.Anchor.TaskID != f.TaskID {
		return false
	}
	if f.From != "" && !fuzzyMatch(c.From, f.From) {
		return false
	}
	if f.Kind != "" && c.Kind != f.Kind {
		return false
	}
	return true
}

// QueryComments reads comments.jsonl and returns those matching the filter,
// most-recent-last (file order). Skips corrupt lines.
func QueryComments(teamsPath, teamName string, filter CommentFilter) ([]Comment, error) {
	commentPath, err := CommentsPath(teamsPath, teamName)
	if err != nil {
		return nil, err
	}
	f, err := os.Open(commentPath)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	defer f.Close()

	var out []Comment
	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 64*1024), 4*1024*1024)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		var c Comment
		if err := json.Unmarshal([]byte(line), &c); err != nil {
			continue // skip corrupt lines
		}
		if filter.Matches(c) {
			out = append(out, c)
		}
		if filter.Limit > 0 && len(out) >= filter.Limit {
			break
		}
	}
	return out, scanner.Err()
}
