package sandbox

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/basenana/friday/core/tools"
)

// benchSearchWorkerCounts mirrors the plan's worker sweep: 1 is the closest
// proxy for the pre-concurrency serial baseline, higher values show scaling.
var benchSearchWorkerCounts = []int{1, 2, 4, 8, 16}

func benchSearchHandler(root string) tools.ToolHandlerFunc {
	return fsSearchFileSystemHandler(NewLocalFileSystem(NewExecutor(DefaultConfig()), root))
}

func runSearchBenchmark(b *testing.B, root string) {
	for _, workers := range benchSearchWorkerCounts {
		b.Run(fmt.Sprintf("workers=%d", workers), func(b *testing.B) {
			searchWorkerOverride = workers
			defer func() { searchWorkerOverride = 0 }()

			handler := benchSearchHandler(root)
			req := &tools.Request{Arguments: map[string]any{"directory": ".", "regex": `needle[0-9]+`}}
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				result, err := handler(context.Background(), req)
				if err != nil {
					b.Fatalf("fs_search error: %v", err)
				}
				if result.IsError {
					b.Fatalf("fs_search failed: %s", result.Content)
				}
			}
		})
	}
}

// BenchmarkFsSearchSyntheticLargeTree measures a flat-ish synthetic tree:
// 2000 regular files of ~4 KiB across 40 directories, ~100 files with
// matches (kept below the match limit so the whole tree is always scanned),
// plus .git directories and binary files that must be skipped.
func BenchmarkFsSearchSyntheticLargeTree(b *testing.B) {
	root := b.TempDir()
	filler := strings.Repeat("filler line padding 0123456789 abcdefghijklmnopqrstuvwxyz\n", 1) // 56 bytes
	for dir := 0; dir < 40; dir++ {
		dirPath := filepath.Join(root, fmt.Sprintf("dir%02d", dir))
		if err := os.MkdirAll(dirPath, 0o755); err != nil {
			b.Fatal(err)
		}
		for file := 0; file < 50; file++ {
			var content strings.Builder
			hasNeedle := (dir*50+file)%20 == 0
			for line := 0; line < 74; line++ { // ~4 KiB per file
				if hasNeedle && line%18 == 5 {
					content.WriteString(fmt.Sprintf("needle%d interesting searchable line\n", line))
					continue
				}
				content.WriteString(filler)
			}
			name := filepath.Join(dirPath, fmt.Sprintf("file%03d.txt", file))
			if err := os.WriteFile(name, []byte(content.String()), 0o644); err != nil {
				b.Fatal(err)
			}
		}
	}
	// Directories that must be skipped entirely, with bait content.
	for i := 0; i < 5; i++ {
		gitDir := filepath.Join(root, fmt.Sprintf("dir%02d", i*7), ".git")
		if err := os.MkdirAll(gitDir, 0o755); err != nil {
			b.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(gitDir, "bait.txt"), []byte("needle9 must be skipped\n"), 0o644); err != nil {
			b.Fatal(err)
		}
	}
	// Binary files that must be detected and skipped by the probe.
	for i := 0; i < 20; i++ {
		binary := append([]byte("needle9\x00\x01\x02binary"), make([]byte, 256)...)
		name := filepath.Join(root, fmt.Sprintf("dir%02d", i%40), fmt.Sprintf("blob%02d.dat", i))
		if err := os.WriteFile(name, binary, 0o644); err != nil {
			b.Fatal(err)
		}
	}
	runSearchBenchmark(b, root)
}

// BenchmarkFsSearchRealisticMixed measures a deep, many-small-files tree:
// depth-6 nesting with 3 subdirectories per level and files at every level,
// roughly 1100 directories and 3600 small files, 1 in 20 files matching.
func BenchmarkFsSearchRealisticMixed(b *testing.B) {
	root := b.TempDir()
	counter := 0
	var build func(dir string, depth int)
	build = func(dir string, depth int) {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			b.Fatal(err)
		}
		files := 3
		if depth == 0 {
			files = 6
		}
		for file := 0; file < files; file++ {
			counter++
			var content strings.Builder
			for line := 0; line < 10; line++ {
				fmt.Fprintf(&content, "package line %d routine payload text\n", line)
			}
			if counter%20 == 0 {
				fmt.Fprintf(&content, "needle%d buried in a small module file\n", counter)
			}
			name := filepath.Join(dir, fmt.Sprintf("mod%05d.go", counter))
			if err := os.WriteFile(name, []byte(content.String()), 0o644); err != nil {
				b.Fatal(err)
			}
		}
		if depth >= 6 {
			return
		}
		for sub := 0; sub < 3; sub++ {
			build(filepath.Join(dir, fmt.Sprintf("sub%d", sub)), depth+1)
		}
	}
	build(root, 0)
	runSearchBenchmark(b, root)
}
