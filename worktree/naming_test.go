package worktree

import (
	"path/filepath"
	"strings"
	"testing"
)

func TestSlug(t *testing.T) {
	for _, tc := range []struct {
		in, want string
	}{
		{"Add Login", "add-login"},
		{"  fix/api: client  ", "fix-api-client"},
		{"实现登录功能", "task"},
	} {
		if got := slug(tc.in); got != tc.want {
			t.Errorf("slug(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

func TestGeneratedNameUsesRequirementWithoutHashAndFitsLimit(t *testing.T) {
	for _, tc := range []struct{ in, want string }{
		{"add login", "add-login"},
		{"add user authentication with oauth", "add-user-authenticat"},
		{"实现登录功能", "task"},
	} {
		if got := generatedName(tc.in); got != tc.want {
			t.Fatalf("generatedName(%q) = %q, want %q", tc.in, got, tc.want)
		}
		if got := len(generatedName(tc.in)); got > 20 {
			t.Fatalf("generatedName(%q) length = %d, want <= 20", tc.in, got)
		}
	}
}

func TestRepositoryIDUsesRepositoryDirectoryName(t *testing.T) {
	id := repositoryID(filepath.Join(t.TempDir(), "friday", ".git"))
	if !strings.HasPrefix(id, "friday-") {
		t.Fatalf("repositoryID() = %q, want friday prefix", id)
	}
}
