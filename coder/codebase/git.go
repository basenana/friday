package codebase

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/basenana/friday/sandbox"
)

type gitState struct {
	IsGit         bool
	HEAD          string
	Branch        string
	Detached      bool
	Dirty         bool
	DirtyHash     string
	Changed       []string
	Candidates    []string
	CandidateHash string
	AfterPath     string
	Total         int
}

func collectGitState(ctx context.Context, exec *sandbox.Executor, root string, pageSize int, previous Status) (gitState, error) {
	head, err := runGit(ctx, exec, root, "git rev-parse --verify HEAD")
	if err != nil {
		message := strings.ToLower(err.Error())
		if strings.Contains(message, "not a git repository") {
			return gitState{}, nil
		}
		if !strings.Contains(message, "needed a single revision") && !strings.Contains(message, "unknown revision") {
			return gitState{}, err
		}
	}
	state := gitState{IsGit: true, HEAD: strings.TrimSpace(head)}
	branch, branchErr := runGit(ctx, exec, root, "git symbolic-ref --short -q HEAD")
	state.Branch = strings.TrimSpace(branch)
	state.Detached = branchErr != nil || state.Branch == ""

	rawStatus, err := runGit(ctx, exec, root, "git status --porcelain=v1 -z --untracked-files=all")
	if err != nil {
		return gitState{}, err
	}
	state.Dirty = rawStatus != ""
	diffCommand := "git diff --name-only -z HEAD"
	if state.HEAD == "" {
		diffCommand = "git diff --name-only -z --cached"
	}
	changed, err := runGit(ctx, exec, root, diffCommand)
	if err != nil {
		return gitState{}, err
	}
	untracked, err := runGit(ctx, exec, root, "git ls-files --others --exclude-standard -z")
	if err != nil {
		return gitState{}, err
	}
	state.Changed = uniqueSorted(append(splitNUL(changed), splitNUL(untracked)...))
	if state.Dirty {
		stagedCommand := "git diff --cached --raw -z HEAD"
		if state.HEAD == "" {
			stagedCommand = "git ls-files -s -z"
		}
		staged, stagedErr := runGit(ctx, exec, root, stagedCommand)
		if stagedErr != nil {
			return gitState{}, stagedErr
		}
		fs, ok := sandbox.NewLocalFileSystem(exec, root).(gitFileSystem)
		if !ok {
			return gitState{}, fmt.Errorf("sandbox filesystem does not support safe Git hashing")
		}
		state.DirtyHash, err = hashWorkingTree(ctx, fs, root, rawStatus, staged, state.Changed)
		if err != nil {
			return gitState{}, err
		}
	}

	candidateRaw, err := runGit(ctx, exec, root, "git ls-files -co --exclude-standard -z")
	if err != nil {
		return gitState{}, err
	}
	all := uniqueSorted(splitNUL(candidateRaw))
	state.Total = len(all)
	hash := sha256.Sum256([]byte(strings.Join(all, "\x00")))
	state.CandidateHash = fmt.Sprintf("%x", hash[:])
	if previous.CandidateHash == state.CandidateHash && previous.PresentedCount >= state.Total {
		return state, nil
	}
	after := previous.AfterPath
	if previous.CandidateHash != state.CandidateHash {
		after = ""
	}
	start := sort.SearchStrings(all, after)
	for start < len(all) && all[start] <= after {
		start++
	}
	end := start + pageSize
	if end > len(all) {
		end = len(all)
	}
	if pageSize > 0 {
		state.Candidates = append([]string(nil), all[start:end]...)
	}
	if len(state.Candidates) > 0 {
		state.AfterPath = state.Candidates[len(state.Candidates)-1]
	}
	if end == len(all) {
		state.AfterPath = ""
	}
	return state, nil
}

func runGit(ctx context.Context, exec *sandbox.Executor, root, command string) (string, error) {
	result, err := exec.Run(ctx, command, sandbox.ExecOptions{Workdir: root, Env: []string{"LC_ALL=C"}, Timeout: time.Minute})
	if err != nil {
		return "", err
	}
	if result.ExitCode != 0 {
		return "", errors.New(strings.TrimSpace(result.Stderr))
	}
	if result.StdoutTruncated || result.StderrTruncated {
		return "", fmt.Errorf("Git control command output was truncated")
	}
	return result.Stdout, nil
}

type gitFileSystem interface {
	Resolve(context.Context, string, sandbox.FileAccessMode) (string, error)
	Lstat(context.Context, string) (os.FileInfo, error)
	Readlink(context.Context, string) (string, error)
	Open(context.Context, string) (io.ReadCloser, error)
}

func hashWorkingTree(ctx context.Context, fs gitFileSystem, root, rawStatus, staged string, paths []string) (string, error) {
	h := sha256.New()
	_, _ = io.WriteString(h, rawStatus)
	_, _ = io.WriteString(h, "\x00staged\x00"+staged)
	buf := make([]byte, 32*1024)
	for _, rel := range paths {
		if err := validateGitPath(rel); err != nil {
			return "", err
		}
		if _, err := fs.Resolve(ctx, filepath.FromSlash(rel), sandbox.FileAccessRead); err != nil {
			return "", fmt.Errorf("resolve Git path %q: %w", rel, err)
		}
		_, _ = io.WriteString(h, "\x00"+rel+"\x00")
		path := filepath.Join(root, filepath.FromSlash(rel))
		info, err := fs.Lstat(ctx, path)
		if os.IsNotExist(err) {
			_, _ = io.WriteString(h, "deleted")
			continue
		}
		if err != nil {
			return "", err
		}
		if info.Mode()&os.ModeSymlink != 0 {
			target, err := fs.Readlink(ctx, path)
			if err != nil {
				return "", err
			}
			_, _ = io.WriteString(h, "symlink:"+target)
			continue
		}
		if !info.Mode().IsRegular() {
			_, _ = io.WriteString(h, info.Mode().String())
			continue
		}
		f, err := fs.Open(ctx, path)
		if err != nil {
			return "", err
		}
		for {
			if err := ctx.Err(); err != nil {
				_ = f.Close()
				return "", err
			}
			n, readErr := f.Read(buf)
			if n > 0 {
				_, _ = h.Write(buf[:n])
			}
			if readErr == io.EOF {
				break
			}
			if readErr != nil {
				_ = f.Close()
				return "", readErr
			}
		}
		if err := f.Close(); err != nil {
			return "", err
		}
	}
	return fmt.Sprintf("%x", h.Sum(nil)), nil
}

func validateGitPath(path string) error {
	if path == "" || !utf8.ValidString(path) || strings.IndexByte(path, 0) >= 0 || filepath.IsAbs(path) || !filepath.IsLocal(filepath.FromSlash(path)) {
		return fmt.Errorf("unsafe or unsupported Git path %q", path)
	}
	return nil
}

func splitNUL(v string) []string {
	parts := strings.Split(v, "\x00")
	out := parts[:0]
	for _, part := range parts {
		if part != "" {
			out = append(out, part)
		}
	}
	return out
}

func uniqueSorted(in []string) []string {
	sort.Strings(in)
	out := in[:0]
	for _, item := range in {
		if len(out) == 0 || out[len(out)-1] != item {
			out = append(out, item)
		}
	}
	return out
}

func observeGitState(status Status, state gitState) Status {
	status.ObservedHEAD = state.HEAD
	status.Branch = state.Branch
	status.Detached = state.Detached
	status.Dirty = state.Dirty
	status.DirtyHash = state.DirtyHash
	return status
}

func advanceCoverage(status Status, state gitState) Status {
	if status.CandidateHash != state.CandidateHash {
		status.PresentedCount = 0
	}
	status.CandidateHash = state.CandidateHash
	status.AfterPath = state.AfterPath
	status.PresentedCount += len(state.Candidates)
	status.CandidateCount = state.Total
	if status.PresentedCount > status.CandidateCount {
		status.PresentedCount = status.CandidateCount
	}
	status.IndexedHEAD = state.HEAD
	return status
}
