package sandbox

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/basenana/friday/coder/project"
)

// ProjectAllowDoc is the on-disk document for per-project sandbox command
// grants. It lives at <DataDir>/projects/<ProjectID>/sandbox.json — on the
// HOME side, outside the agent's default filesystem write roots — so a
// sandboxed agent cannot edit it to escalate its own permissions.
type ProjectAllowDoc struct {
	Version int      `json:"version"`
	Allow   []string `json:"allow"`
}

// projectAllowVersion is the only supported document version.
const projectAllowVersion = 1

// ProjectAllowPath returns the per-project sandbox allow file path for the
// project containing workdir. The path is derived from the canonical
// (symlink-resolved) project root, so opening the same repository through
// different paths or symlinks yields the same file.
func ProjectAllowPath(dataDir, workdir string) (string, error) {
	root, err := project.CanonicalRoot(workdir)
	if err != nil {
		return "", fmt.Errorf("resolve project root: %w", err)
	}
	return filepath.Join(dataDir, "projects", project.ProjectID(root), "sandbox.json"), nil
}

// LoadProjectAllow reads the project allow list from path. A missing file is
// not an error and yields a nil slice. An existing file must be a regular,
// non-group/world-writable file of at most 1 MiB with version 1; violations
// fail loudly instead of being ignored (the failure direction never widens
// permissions). Entries are trimmed and de-duplicated.
func LoadProjectAllow(path string) ([]string, error) {
	f, err := os.Open(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	defer f.Close()

	info, err := f.Stat()
	if err != nil {
		return nil, err
	}
	if err := validateProjectAllowFile(path, info); err != nil {
		return nil, err
	}

	data, err := io.ReadAll(io.LimitReader(f, maxConfigFileSize+1))
	if err != nil {
		return nil, err
	}
	if int64(len(data)) > maxConfigFileSize {
		return nil, fmt.Errorf("project allow file too large: %s exceeds %d bytes", path, maxConfigFileSize)
	}

	var doc ProjectAllowDoc
	if err := json.Unmarshal(data, &doc); err != nil {
		return nil, fmt.Errorf("decode project allow file %s: %w", path, err)
	}
	if doc.Version != projectAllowVersion {
		return nil, fmt.Errorf("unsupported project allow file version %d in %s", doc.Version, path)
	}

	seen := make(map[string]bool, len(doc.Allow))
	allow := make([]string, 0, len(doc.Allow))
	for _, entry := range doc.Allow {
		entry = strings.TrimSpace(entry)
		if entry == "" || seen[entry] {
			continue
		}
		seen[entry] = true
		allow = append(allow, entry)
	}
	return allow, nil
}

// AppendProjectAllow adds command to the project allow document at path,
// creating the file (and its parent directories) when needed. The write is
// atomic (temp file + fsync + rename) and the file is created with 0600.
// Appending an existing command is a no-op. An existing invalid file is an
// error; permissions problems are never silently repaired.
func AppendProjectAllow(path, command string) error {
	command = strings.TrimSpace(command)
	if command == "" {
		return fmt.Errorf("command is required")
	}

	allow, err := LoadProjectAllow(path)
	if err != nil {
		return err
	}
	for _, existing := range allow {
		if existing == command {
			return nil
		}
	}

	doc := ProjectAllowDoc{Version: projectAllowVersion, Allow: append(allow, command)}
	data, err := json.MarshalIndent(doc, "", "  ")
	if err != nil {
		return err
	}

	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(dir, ".sandbox-allow-*")
	if err != nil {
		return err
	}
	name := tmp.Name()
	defer os.Remove(name)

	var writeErr error
	if writeErr = tmp.Chmod(0o600); writeErr == nil {
		_, writeErr = tmp.Write(data)
	}
	if writeErr == nil {
		writeErr = tmp.Sync()
	}
	if closeErr := tmp.Close(); writeErr == nil {
		writeErr = closeErr
	}
	if writeErr != nil {
		return writeErr
	}
	return os.Rename(name, path)
}

// validateProjectAllowFile enforces the same ownership/permission/size
// contract as the main sandbox configuration (see validateConfigFilePerm).
func validateProjectAllowFile(path string, info os.FileInfo) error {
	if os.Geteuid() == 0 {
		return nil
	}
	if !info.Mode().IsRegular() {
		return fmt.Errorf("project allow path is not a regular file: %s", path)
	}
	if info.Mode().Perm()&0o022 != 0 {
		return fmt.Errorf("project allow file permissions too open: %s (%s)", path, info.Mode().Perm())
	}
	if info.Size() > maxConfigFileSize {
		return fmt.Errorf("project allow file too large: %s exceeds %d bytes", path, maxConfigFileSize)
	}
	dirInfo, err := os.Stat(filepath.Dir(path))
	if err != nil {
		return err
	}
	if dirInfo.Mode().Perm()&0o002 != 0 {
		return fmt.Errorf("project allow directory is world-writable: %s", filepath.Dir(path))
	}
	return nil
}

// ValidateGrantableCommand checks that command may be granted to a project
// allow list: it must be a single plain command name (not a shell command
// line and not a wildcard pattern) and must not match any active deny rule.
func ValidateGrantableCommand(cfg *Config, command string) error {
	command = strings.TrimSpace(command)
	if command == "" {
		return fmt.Errorf("command is required")
	}
	if strings.ContainsAny(command, " \t\n\r|;&<>()`$'\"*?[]{}") {
		return fmt.Errorf("grant a single plain command name, got %q", command)
	}
	if cfg == nil {
		return nil
	}
	commands, err := ParseCommands(command)
	if err != nil {
		return fmt.Errorf("invalid command %q: %w", command, err)
	}
	if len(commands) != 1 || commands[0].Name != command {
		return fmt.Errorf("grant a single plain command name, got %q", command)
	}
	for _, pattern := range cfg.Permissions.Deny {
		if commands[0].MatchPattern(pattern) {
			return fmt.Errorf("command %q matches deny rule %q and cannot be granted", command, pattern)
		}
	}
	return nil
}
