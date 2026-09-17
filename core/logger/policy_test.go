package logger

import (
	"go/parser"
	"go/token"
	"io"
	"io/fs"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"
)

func TestUnconfiguredLoggerIsSilent(t *testing.T) {
	logger, ok := newDefault("test").(*defaultLogger)
	if !ok {
		t.Fatal("newDefault returned an unexpected logger implementation")
	}
	if logger.w != io.Discard {
		t.Fatal("unconfigured project logger must discard output")
	}
}

func TestProductionCodeUsesProjectLogger(t *testing.T) {
	_, filename, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("resolve test source path")
	}
	repoRoot := filepath.Clean(filepath.Join(filepath.Dir(filename), "..", ".."))
	forbidden := map[string]bool{"log": true, "log/slog": true}

	err := filepath.WalkDir(repoRoot, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() {
			if path != repoRoot && (entry.Name() == ".git" || entry.Name() == "vendor") {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}

		file, parseErr := parser.ParseFile(token.NewFileSet(), path, nil, parser.ImportsOnly)
		if parseErr != nil {
			return parseErr
		}
		for _, spec := range file.Imports {
			importPath, unquoteErr := strconv.Unquote(spec.Path.Value)
			if unquoteErr != nil {
				return unquoteErr
			}
			if forbidden[importPath] {
				rel, _ := filepath.Rel(repoRoot, path)
				t.Errorf("%s imports %q; use core/logger so TUI output stays isolated", rel, importPath)
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}
