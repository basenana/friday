package skills

import (
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLoaderListFilesFilesystemSkill(t *testing.T) {
	root := t.TempDir()
	skillDir := filepath.Join(root, "disk-skill")
	scriptsDir := filepath.Join(skillDir, "scripts")
	if err := os.MkdirAll(scriptsDir, 0o755); err != nil {
		t.Fatalf("MkdirAll() error: %v", err)
	}
	if err := os.WriteFile(filepath.Join(skillDir, "SKILL.md"), []byte("---\nname: disk-skill\ndescription: test\n---\nTest."), 0o644); err != nil {
		t.Fatalf("write SKILL.md: %v", err)
	}
	if err := os.WriteFile(filepath.Join(scriptsDir, "run.py"), []byte("print('hello')"), 0o644); err != nil {
		t.Fatalf("write run.py: %v", err)
	}

	loader := NewLoader(root)
	if err := loader.Load(); err != nil {
		t.Fatalf("Load() error: %v", err)
	}

	rootEntries, err := loader.ListFiles("disk-skill", "")
	if err != nil {
		t.Fatalf("ListFiles(root) error: %v", err)
	}
	rootNames := dirEntryNames(rootEntries)
	if !containsStr(rootNames, "SKILL.md") || !containsStr(rootNames, "scripts") {
		t.Fatalf("expected filesystem skill root contents, got %v", rootNames)
	}

	scriptEntries, err := loader.ListFiles("disk-skill", "scripts")
	if err != nil {
		t.Fatalf("ListFiles(scripts) error: %v", err)
	}
	scriptNames := dirEntryNames(scriptEntries)
	if !containsStr(scriptNames, "run.py") {
		t.Fatalf("expected run.py in scripts directory, got %v", scriptNames)
	}
}

func TestLoaderReadFileFilesystemSkill(t *testing.T) {
	root := t.TempDir()
	skillDir := filepath.Join(root, "disk-skill")
	scriptsDir := filepath.Join(skillDir, "scripts")
	if err := os.MkdirAll(scriptsDir, 0o755); err != nil {
		t.Fatalf("MkdirAll() error: %v", err)
	}
	if err := os.WriteFile(filepath.Join(skillDir, "SKILL.md"), []byte("---\nname: disk-skill\ndescription: test\n---\nTest."), 0o644); err != nil {
		t.Fatalf("write SKILL.md: %v", err)
	}
	if err := os.WriteFile(filepath.Join(scriptsDir, "run.py"), []byte("print('hello')"), 0o644); err != nil {
		t.Fatalf("write run.py: %v", err)
	}

	loader := NewLoader(root)
	if err := loader.Load(); err != nil {
		t.Fatalf("Load() error: %v", err)
	}

	data, err := loader.ReadFile("disk-skill", "scripts/run.py")
	if err != nil {
		t.Fatalf("ReadFile() error: %v", err)
	}
	if string(data) != "print('hello')" {
		t.Fatalf("unexpected file content: %q", string(data))
	}
}

func TestLoaderListFilesRejectsPathTraversal(t *testing.T) {
	root := t.TempDir()
	skillDir := filepath.Join(root, "disk-skill")
	if err := os.MkdirAll(skillDir, 0o755); err != nil {
		t.Fatalf("MkdirAll() error: %v", err)
	}
	if err := os.WriteFile(filepath.Join(skillDir, "SKILL.md"), []byte("---\nname: disk-skill\ndescription: test\n---\nTest."), 0o644); err != nil {
		t.Fatalf("write SKILL.md: %v", err)
	}

	loader := NewLoader(root)
	if err := loader.Load(); err != nil {
		t.Fatalf("Load() error: %v", err)
	}

	if _, err := loader.ListFiles("disk-skill", "../../etc"); err == nil {
		t.Fatal("expected path traversal error")
	}
}

func TestLoaderReadFileRejectsPathTraversal(t *testing.T) {
	root := t.TempDir()
	skillDir := filepath.Join(root, "disk-skill")
	if err := os.MkdirAll(skillDir, 0o755); err != nil {
		t.Fatalf("MkdirAll() error: %v", err)
	}
	if err := os.WriteFile(filepath.Join(skillDir, "SKILL.md"), []byte("---\nname: disk-skill\ndescription: test\n---\nTest."), 0o644); err != nil {
		t.Fatalf("write SKILL.md: %v", err)
	}

	loader := NewLoader(root)
	if err := loader.Load(); err != nil {
		t.Fatalf("Load() error: %v", err)
	}

	if _, err := loader.ReadFile("disk-skill", "../../etc/passwd"); err == nil {
		t.Fatal("expected path traversal error")
	}
}

func TestLoaderReadFileRejectsSymlinkEscape(t *testing.T) {
	root := t.TempDir()
	skillDir := filepath.Join(root, "disk-skill")
	if err := os.MkdirAll(skillDir, 0o755); err != nil {
		t.Fatalf("MkdirAll() error = %v", err)
	}
	if err := os.WriteFile(filepath.Join(skillDir, "SKILL.md"), []byte("---\nname: disk-skill\ndescription: test\n---\nTest."), 0o644); err != nil {
		t.Fatalf("write SKILL.md: %v", err)
	}

	secretPath := filepath.Join(root, "secret.txt")
	if err := os.WriteFile(secretPath, []byte("super secret"), 0o644); err != nil {
		t.Fatalf("write secret: %v", err)
	}
	// Symlink created inside the skill dir after installation, pointing outside.
	if err := os.Symlink(secretPath, filepath.Join(skillDir, "leak.txt")); err != nil {
		t.Skipf("symlink not supported: %v", err)
	}

	loader := NewLoader(root)
	if err := loader.Load(); err != nil {
		t.Fatalf("Load() error = %v", err)
	}

	if data, err := loader.ReadFile("disk-skill", "leak.txt"); err == nil {
		t.Fatalf("expected symlink escape error, got content %q", string(data))
	}
	if _, err := loader.LoadResource("disk-skill", "leak.txt"); err == nil {
		t.Fatal("expected LoadResource symlink escape error, got nil")
	}
}

func TestLoaderReadFileRejectsDanglingSymlink(t *testing.T) {
	root := t.TempDir()
	skillDir := filepath.Join(root, "disk-skill")
	if err := os.MkdirAll(skillDir, 0o755); err != nil {
		t.Fatalf("MkdirAll() error = %v", err)
	}
	if err := os.WriteFile(filepath.Join(skillDir, "SKILL.md"), []byte("---\nname: disk-skill\ndescription: test\n---\nTest."), 0o644); err != nil {
		t.Fatalf("write SKILL.md: %v", err)
	}
	if err := os.Symlink(filepath.Join(root, "missing.txt"), filepath.Join(skillDir, "dangling.txt")); err != nil {
		t.Skipf("symlink not supported: %v", err)
	}

	loader := NewLoader(root)
	if err := loader.Load(); err != nil {
		t.Fatalf("Load() error = %v", err)
	}

	if _, err := loader.ReadFile("disk-skill", "dangling.txt"); err == nil {
		t.Fatal("expected dangling symlink error, got nil")
	}
}

func TestLoaderListFilesSkipsSymlinkEntries(t *testing.T) {
	root := t.TempDir()
	skillDir := filepath.Join(root, "disk-skill")
	if err := os.MkdirAll(skillDir, 0o755); err != nil {
		t.Fatalf("MkdirAll() error = %v", err)
	}
	if err := os.WriteFile(filepath.Join(skillDir, "SKILL.md"), []byte("---\nname: disk-skill\ndescription: test\n---\nTest."), 0o644); err != nil {
		t.Fatalf("write SKILL.md: %v", err)
	}
	if err := os.WriteFile(filepath.Join(skillDir, "notes.txt"), []byte("keep me"), 0o644); err != nil {
		t.Fatalf("write notes.txt: %v", err)
	}
	if err := os.Symlink(filepath.Join(root, "elsewhere"), filepath.Join(skillDir, "outside-link")); err != nil {
		t.Skipf("symlink not supported: %v", err)
	}

	loader := NewLoader(root)
	if err := loader.Load(); err != nil {
		t.Fatalf("Load() error = %v", err)
	}

	entries, err := loader.ListFiles("disk-skill", "")
	if err != nil {
		t.Fatalf("ListFiles() error = %v", err)
	}
	names := dirEntryNames(entries)
	if containsStr(names, "outside-link") {
		t.Fatalf("expected symlink entry to be skipped, got %v", names)
	}
	if !containsStr(names, "SKILL.md") || !containsStr(names, "notes.txt") {
		t.Fatalf("expected regular entries to be listed, got %v", names)
	}
}

func TestLoaderReadFileNestedFileWithinSkill(t *testing.T) {
	root := t.TempDir()
	skillDir := filepath.Join(root, "disk-skill")
	deepDir := filepath.Join(skillDir, "a", "b")
	if err := os.MkdirAll(deepDir, 0o755); err != nil {
		t.Fatalf("MkdirAll() error = %v", err)
	}
	if err := os.WriteFile(filepath.Join(skillDir, "SKILL.md"), []byte("---\nname: disk-skill\ndescription: test\n---\nTest."), 0o644); err != nil {
		t.Fatalf("write SKILL.md: %v", err)
	}
	if err := os.WriteFile(filepath.Join(deepDir, "nested.txt"), []byte("nested content"), 0o644); err != nil {
		t.Fatalf("write nested.txt: %v", err)
	}

	loader := NewLoader(root)
	if err := loader.Load(); err != nil {
		t.Fatalf("Load() error = %v", err)
	}

	data, err := loader.ReadFile("disk-skill", filepath.Join("a", "b", "nested.txt"))
	if err != nil {
		t.Fatalf("ReadFile() error = %v", err)
	}
	if string(data) != "nested content" {
		t.Fatalf("unexpected content: %q", string(data))
	}
}

func TestParseSkillFileWarnsUnknownFrontmatterFields(t *testing.T) {
	origStderr := os.Stderr
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("pipe() error = %v", err)
	}
	os.Stderr = w
	_, _, parseErr := ParseSkillFile([]byte("---\nname: typo-test\ndescripton: oops\n---\nBody."))
	closeErr := w.Close()
	os.Stderr = origStderr
	if parseErr != nil {
		t.Fatalf("ParseSkillFile() error = %v", parseErr)
	}
	if closeErr != nil {
		t.Fatalf("close() error = %v", closeErr)
	}

	buf := make([]byte, 512)
	n, _ := r.Read(buf)
	warning := string(buf[:n])
	if !strings.Contains(warning, "unknown frontmatter fields") || !strings.Contains(warning, "descripton") {
		t.Fatalf("expected unknown-field warning, got %q", warning)
	}
}

func dirEntryNames(entries []fs.DirEntry) []string {
	names := make([]string, len(entries))
	for i, e := range entries {
		names[i] = e.Name()
	}
	return names
}

func containsStr(slice []string, s string) bool {
	for _, v := range slice {
		if v == s {
			return true
		}
	}
	return false
}
