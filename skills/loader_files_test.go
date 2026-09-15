package skills

import (
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	corelogger "github.com/basenana/friday/core/logger"
)

type capturedWarning struct {
	message string
	fields  []interface{}
}

type warningCaptureLogger struct {
	warnings []capturedWarning
}

func (l *warningCaptureLogger) Named(string) corelogger.Logger { return l }
func (l *warningCaptureLogger) With(...interface{}) corelogger.Logger {
	return l
}
func (l *warningCaptureLogger) Info(...interface{})           {}
func (l *warningCaptureLogger) Warn(...interface{})           {}
func (l *warningCaptureLogger) Error(...interface{})          {}
func (l *warningCaptureLogger) Infof(string, ...interface{})  {}
func (l *warningCaptureLogger) Warnf(string, ...interface{})  {}
func (l *warningCaptureLogger) Errorf(string, ...interface{}) {}
func (l *warningCaptureLogger) Infow(string, ...interface{})  {}
func (l *warningCaptureLogger) Errorw(string, ...interface{}) {}
func (l *warningCaptureLogger) Warnw(message string, fields ...interface{}) {
	l.warnings = append(l.warnings, capturedWarning{message: message, fields: fields})
}

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

func TestLoaderStoresAbsoluteSkillPathFromRelativeRoot(t *testing.T) {
	root := t.TempDir()
	skillDir := filepath.Join(root, "disk-skill")
	referencesDir := filepath.Join(skillDir, "references")
	if err := os.MkdirAll(referencesDir, 0o755); err != nil {
		t.Fatalf("MkdirAll() error: %v", err)
	}
	if err := os.WriteFile(filepath.Join(skillDir, "SKILL.md"), []byte("---\nname: disk-skill\ndescription: test\n---\nRead references/guide.md."), 0o644); err != nil {
		t.Fatalf("write SKILL.md: %v", err)
	}
	if err := os.WriteFile(filepath.Join(referencesDir, "guide.md"), []byte("guide"), 0o644); err != nil {
		t.Fatalf("write guide: %v", err)
	}

	workdir, err := os.Getwd()
	if err != nil {
		t.Fatalf("Getwd() error: %v", err)
	}
	relativeRoot, err := filepath.Rel(workdir, root)
	if err != nil {
		t.Fatalf("Rel() error: %v", err)
	}
	loader := NewLoader(relativeRoot)
	if err := loader.Load(); err != nil {
		t.Fatalf("Load() error: %v", err)
	}

	skill, err := loader.Get("disk-skill")
	if err != nil {
		t.Fatalf("Get() error: %v", err)
	}
	wantBasePath, err := filepath.Abs(skillDir)
	if err != nil {
		t.Fatalf("Abs() error: %v", err)
	}
	if skill.BasePath != filepath.Clean(wantBasePath) || !filepath.IsAbs(skill.BasePath) {
		t.Fatalf("BasePath = %q, want absolute %q", skill.BasePath, wantBasePath)
	}
	content, err := loader.ReadFile("disk-skill", "references/guide.md")
	if err != nil || string(content) != "guide" {
		t.Fatalf("ReadFile() = %q, %v; want guide", content, err)
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

func TestLoaderLaterDirectoryOverridesEarlierEverywhere(t *testing.T) {
	global := t.TempDir()
	project := t.TempDir()
	writeSkill := func(root, dir, name, body string) {
		t.Helper()
		path := filepath.Join(root, dir)
		if err := os.MkdirAll(path, 0o755); err != nil {
			t.Fatal(err)
		}
		content := "---\nname: " + name + "\ndescription: test\n---\n" + body
		if err := os.WriteFile(filepath.Join(path, "SKILL.md"), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	writeSkill(global, "shared", "shared", "global")
	writeSkill(project, "shared", "shared", "project")

	loader := NewLoader(global, project)
	if err := loader.Load(); err != nil {
		t.Fatal(err)
	}
	got, err := loader.Get("shared")
	if err != nil || got.Instructions != "project" {
		t.Fatalf("Get() = %#v, %v; want project skill", got, err)
	}
	got, err = loader.LoadSkillFromDir("shared")
	if err != nil || got.Instructions != "project" {
		t.Fatalf("LoadSkillFromDir() = %#v, %v; want project skill", got, err)
	}
}

func TestParseSkillFileLogsUnknownFrontmatterFieldsWithoutWritingStderr(t *testing.T) {
	origLogger := corelogger.Root()
	capture := &warningCaptureLogger{}
	corelogger.SetRoot(capture)
	defer corelogger.SetRoot(origLogger)

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

	stderr, readErr := io.ReadAll(r)
	if readErr != nil {
		t.Fatalf("read stderr pipe: %v", readErr)
	}
	if len(stderr) != 0 {
		t.Fatalf("expected no stderr output, got %q", string(stderr))
	}
	if len(capture.warnings) != 1 {
		t.Fatalf("expected one warning log, got %d", len(capture.warnings))
	}
	warning := capture.warnings[0]
	if warning.message != "skill has unknown frontmatter fields" {
		t.Fatalf("unexpected warning message: %q", warning.message)
	}
	wantFields := []interface{}{"skill", "typo-test", "fields", []string{"descripton"}}
	if !reflect.DeepEqual(warning.fields, wantFields) {
		t.Fatalf("unexpected warning fields: %#v", warning.fields)
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
