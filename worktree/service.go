package worktree

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/basenana/friday/coder/project"
	"github.com/basenana/friday/sessions"
)

type Worktree struct {
	ID         string
	Name       string
	Path       string
	HEAD       string
	Branch     string
	Detached   bool
	Main       bool
	GitTracked bool
	Registered bool
	Current    bool
	Stale      bool
	ProjectID  string
	SessionID  string
	CreatedAt  time.Time
	LastUsedAt time.Time
}

type Service struct {
	runner        runner
	cwd           string
	controlDir    string
	commonDir     string
	projectRoot   string
	repositoryID  string
	identity      project.Identity
	root          string
	prefix        string
	store         Store
	nameGenerator func(context.Context, string) (string, error)
}

type Created struct {
	Worktree Worktree
	service  *Service
	branch   bool
	path     bool
}

type RemoveOptions struct {
	DeleteBranch       bool
	Sessions           *sessions.Manager
	AcquireProjectLock func() (release func(), err error)
}

func Open(ctx context.Context, cwd, root, prefix, dataDir string) (*Service, error) {
	return openWithRunner(ctx, cwd, root, prefix, dataDir, gitRunner{})
}

// DiscoverProjectIdentity returns the stable logical project identity shared
// by every linked checkout of the Git repository containing cwd.
func DiscoverProjectIdentity(ctx context.Context, cwd string) (project.Identity, error) {
	_, common, err := discoverRepository(ctx, cwd, gitRunner{})
	if err != nil {
		return project.Identity{}, err
	}
	return projectIdentity(common), nil
}

func discoverRepository(ctx context.Context, cwd string, r runner) (string, string, error) {
	absCWD, err := filepath.Abs(cwd)
	if err != nil {
		return "", "", err
	}
	if resolved, resolveErr := filepath.EvalSymlinks(absCWD); resolveErr == nil {
		absCWD = resolved
	}
	top, err := r.Run(ctx, absCWD, "rev-parse", "--show-toplevel")
	if err != nil {
		return "", "", fmt.Errorf("discover Git worktree root: %w", err)
	}
	absCWD = trimGitLine(top)
	if resolved, resolveErr := filepath.EvalSymlinks(absCWD); resolveErr == nil {
		absCWD = resolved
	}
	common, err := r.Run(ctx, absCWD, "rev-parse", "--git-common-dir")
	if err != nil {
		return "", "", fmt.Errorf("discover Git repository: %w", err)
	}
	common = trimGitLine(common)
	if !filepath.IsAbs(common) {
		common = filepath.Join(absCWD, common)
	}
	common, err = filepath.Abs(common)
	if err != nil {
		return "", "", err
	}
	if resolved, resolveErr := filepath.EvalSymlinks(common); resolveErr == nil {
		common = resolved
	}
	return filepath.Clean(absCWD), filepath.Clean(common), nil
}

func openWithRunner(ctx context.Context, cwd, root, prefix, dataDir string, r runner) (*Service, error) {
	absCWD, common, err := discoverRepository(ctx, cwd, r)
	if err != nil {
		return nil, err
	}
	root, err = filepath.Abs(root)
	if err != nil {
		return nil, err
	}
	if err := os.MkdirAll(root, 0o755); err != nil {
		return nil, fmt.Errorf("create worktree root: %w", err)
	}
	if resolved, resolveErr := filepath.EvalSymlinks(root); resolveErr == nil {
		root = resolved
	}
	identity := projectIdentity(common)
	id := identity.ID
	store, err := NewStore(filepath.Join(dataDir, "projects"), identity.ID)
	if err != nil {
		return nil, fmt.Errorf("open project worktree store: %w", err)
	}
	projectRoot := filepath.Dir(common)
	if filepath.Base(common) != ".git" {
		projectRoot = absCWD
	}
	return &Service{runner: r, cwd: absCWD, controlDir: absCWD, commonDir: common, projectRoot: filepath.Clean(projectRoot), repositoryID: id, identity: identity, root: root, prefix: prefix, store: store}, nil
}

func projectIdentity(common string) project.Identity {
	common = filepath.Clean(common)
	return project.Identity{ID: repositoryID(common), Name: repositoryName(common), Repository: common}
}

func repositoryID(path string) string {
	clean := filepath.Clean(path)
	sum := sha256.Sum256([]byte(clean))
	return fmt.Sprintf("%s-%x", project.ProjectIDPrefix(repositoryName(clean)), sum[:6])
}

func legacyRepositoryID(path string) string {
	clean := filepath.Clean(path)
	sum := sha256.Sum256([]byte(clean))
	return fmt.Sprintf("%s-%x", repositoryName(clean), sum[:6])
}

// LegacyRegistryPaths returns the current stable-ID registry location and the
// exact raw-basename location used by the standalone worktree implementation.
// Every returned path is lexically confined below dataDir/worktrees.
func LegacyRegistryPaths(dataDir string, identity project.Identity) ([]string, error) {
	root := filepath.Clean(filepath.Join(dataDir, "worktrees"))
	ids := []string{identity.ID, legacyRepositoryID(identity.Repository)}
	paths := make([]string, 0, len(ids))
	seen := make(map[string]bool, len(ids))
	for _, id := range ids {
		candidate := filepath.Clean(filepath.Join(root, id, "registry.json"))
		rel, err := filepath.Rel(root, candidate)
		if err != nil {
			return nil, fmt.Errorf("resolve legacy worktree registry: %w", err)
		}
		if rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) || filepath.IsAbs(rel) {
			return nil, fmt.Errorf("legacy worktree registry escapes data directory")
		}
		if !seen[candidate] {
			seen[candidate] = true
			paths = append(paths, candidate)
		}
	}
	return paths, nil
}

func trimGitLine(output string) string { return strings.TrimRight(output, "\r\n") }

func repositoryName(path string) string {
	base := filepath.Base(filepath.Clean(path))
	if base == ".git" {
		base = filepath.Base(filepath.Dir(path))
	} else {
		base = strings.TrimSuffix(base, ".git")
	}
	if base == "" || base == "." {
		return "repository"
	}
	return base
}

func (s *Service) RepositoryID() string { return s.repositoryID }

func (s *Service) SetNameGenerator(generator func(context.Context, string) (string, error)) {
	s.nameGenerator = generator
}

func (s *Service) ProjectIdentity() project.Identity { return s.identity }

func (s *Service) ProjectCodeRoot() string { return s.projectRoot }

func (s *Service) SetCurrentPath(path string) error {
	abs, err := filepath.Abs(path)
	if err != nil {
		return err
	}
	if resolved, resolveErr := filepath.EvalSymlinks(abs); resolveErr == nil {
		abs = resolved
	}
	s.cwd = filepath.Clean(abs)
	return nil
}

func (s *Service) SetCurrentCanonicalPath(path string) {
	s.cwd = filepath.Clean(path)
}

func (s *Service) List(ctx context.Context) ([]Worktree, error) {
	out, err := s.runner.Run(ctx, s.cwd, "worktree", "list", "--porcelain")
	if err != nil {
		return nil, err
	}
	items, err := parsePorcelain(out)
	if err != nil {
		return nil, err
	}
	if len(items) > 0 {
		items[0].Main = true
	}
	current, _ := filepath.Abs(s.cwd)
	for i := range items {
		items[i].GitTracked = true
		if resolved, resolveErr := filepath.EvalSymlinks(items[i].Path); resolveErr == nil {
			items[i].Path = resolved
		}
		items[i].ID = worktreeID(items[i].Path)
		items[i].Name = filepath.Base(filepath.Clean(items[i].Path))
		items[i].Current = filepath.Clean(items[i].Path) == filepath.Clean(current)
	}
	entries, err := s.store.List()
	if err != nil {
		return nil, fmt.Errorf("load worktree metadata: %w", err)
	}
	matched := make([]bool, len(entries))
	for i := range items {
		items[i].ProjectID = s.identity.ID
		for j, entry := range entries {
			if sameCanonicalPath(entry.Path, items[i].Path) {
				matched[j] = true
				items[i].Registered = true
				items[i].ID, items[i].Name = entry.ID, entry.Name
				items[i].ProjectID, items[i].SessionID = s.identity.ID, entry.SessionID
				items[i].CreatedAt, items[i].LastUsedAt = entry.CreatedAt, entry.LastUsedAt
				break
			}
		}
	}
	for i, entry := range entries {
		if !matched[i] {
			items = append(items, Worktree{ID: entry.ID, Name: entry.Name, Path: entry.Path, Branch: entry.Branch, ProjectID: s.identity.ID, SessionID: entry.SessionID, CreatedAt: entry.CreatedAt, LastUsedAt: entry.LastUsedAt, Stale: true, Registered: true})
		}
	}
	return items, nil
}

// Remove removes one project-owned checkout without forcing Git, archives its
// valid referenced session, and optionally deletes its branch safely.
func (s *Service) Remove(ctx context.Context, target string, options RemoveOptions) error {
	meta, err := s.store.Get(target)
	if err != nil {
		return err
	}
	release := func() {}
	if options.AcquireProjectLock != nil {
		release, err = options.AcquireProjectLock()
		if err != nil {
			return fmt.Errorf("cannot remove worktree while a running project TUI owns %s; close it and retry: %w", s.identity.Name, err)
		}
		if release == nil {
			release = func() {}
		}
	}
	defer release()

	// Re-resolve the exact record after taking the project lock. This prevents
	// a target selected before the lock from acting on replaced metadata.
	meta, err = s.store.Get(meta.ID)
	if err != nil {
		return fmt.Errorf("reload worktree %q: %w", target, err)
	}
	items, err := s.List(ctx)
	if err != nil {
		return fmt.Errorf("inspect worktree %s: %w", meta.ID, err)
	}
	var live *Worktree
	for i := range items {
		if sameCanonicalPath(items[i].Path, meta.Path) {
			copy := items[i]
			live = &copy
			break
		}
	}
	if live != nil && live.Main {
		return fmt.Errorf("refusing to remove the main checkout %s", meta.Path)
	}
	if live != nil && live.Current {
		return fmt.Errorf("refusing to remove the current checkout %s; run the command from another checkout", meta.Path)
	}
	if options.DeleteBranch && live != nil && live.GitTracked {
		if live.Detached || strings.TrimSpace(live.Branch) == "" {
			return fmt.Errorf("cannot delete a branch for detached worktree %s; attach it to a branch or remove it without --delete-branch", meta.Path)
		}
		// Branch is dynamic Git state. Snapshot it while the checkout still
		// exists and persist it before removal so a later safe-deletion retry
		// cannot fall back to the stale branch recorded at association time.
		if live.Branch != meta.Branch {
			if err := s.store.UpdateMetadata(meta.ID, func(current *Metadata) error {
				current.Branch = live.Branch
				return nil
			}); err != nil {
				return fmt.Errorf("persist current worktree branch %s: %w", live.Branch, err)
			}
			meta.Branch = live.Branch
		}
	}
	if live != nil && live.GitTracked {
		if _, err := s.runner.Run(ctx, s.controlDir, "worktree", "remove", meta.Path); err != nil {
			return fmt.Errorf("remove checkout %s: %w; commit or stash local changes, then retry", meta.Path, err)
		}
	}
	if options.Sessions != nil && meta.SessionID != "" {
		if _, err := options.Sessions.ArchiveIfExists(meta.SessionID); err != nil {
			return fmt.Errorf("archive worktree session %s: %w", meta.SessionID, err)
		}
	}
	if err := s.store.Remove(meta.ID); err != nil {
		return err
	}
	if !options.DeleteBranch || meta.Branch == "" {
		return nil
	}
	if _, err := s.runner.Run(ctx, s.controlDir, "branch", "-d", meta.Branch); err != nil {
		_, restoreErr := s.store.Ensure(meta)
		failure := fmt.Errorf("delete branch %s: %w; the branch and retry metadata were retained, resolve the Git error and retry with --delete-branch", meta.Branch, err)
		if restoreErr != nil {
			return errors.Join(failure, fmt.Errorf("restore retry metadata: %w", restoreErr))
		}
		return failure
	}
	return nil
}

func sameCanonicalPath(a, b string) bool { return filepath.Clean(a) == filepath.Clean(b) }

func (s *Service) Associate(path, branch, _ string, sessionID string) error {
	meta, err := s.store.Ensure(Metadata{
		ID: worktreeID(path), Name: filepath.Base(filepath.Clean(path)), Path: path, Branch: branch,
	})
	if err != nil {
		return err
	}
	return s.store.UpdateSession(meta.ID, sessionID)
}

func (s *Service) forget(path string) error {
	entries, err := s.store.List()
	if err != nil {
		return err
	}
	for _, entry := range entries {
		if sameCanonicalPath(entry.Path, path) {
			return s.store.Remove(entry.ID)
		}
	}
	return nil
}

func parsePorcelain(raw string) ([]Worktree, error) {
	var result []Worktree
	var current *Worktree
	flush := func() {
		if current != nil {
			result = append(result, *current)
			current = nil
		}
	}
	for _, line := range strings.Split(raw, "\n") {
		if line == "" {
			flush()
			continue
		}
		key, value, _ := strings.Cut(line, " ")
		switch key {
		case "worktree":
			flush()
			current = &Worktree{Path: value}
		case "HEAD":
			if current == nil {
				return nil, fmt.Errorf("HEAD before worktree")
			}
			current.HEAD = value
		case "branch":
			if current == nil {
				return nil, fmt.Errorf("branch before worktree")
			}
			current.Branch = strings.TrimPrefix(value, "refs/heads/")
		case "detached":
			if current == nil {
				return nil, fmt.Errorf("detached before worktree")
			}
			current.Detached = true
		case "prunable":
			if current == nil {
				return nil, fmt.Errorf("prunable before worktree")
			}
			current.Stale = true
		}
	}
	flush()
	return result, nil
}

func (s *Service) Current(ctx context.Context) (*Worktree, error) {
	items, err := s.List(ctx)
	if err != nil {
		return nil, err
	}
	for i := range items {
		if items[i].Current {
			return &items[i], nil
		}
	}
	return nil, fmt.Errorf("current directory is not a registered Git worktree")
}

func (s *Service) Resolve(ctx context.Context, target string) (*Worktree, error) {
	target = strings.TrimSpace(target)
	items, err := s.List(ctx)
	if err != nil {
		return nil, err
	}
	var matches []Worktree
	for _, item := range items {
		base := strings.TrimPrefix(item.Branch, s.prefix)
		if item.Path == target || item.Branch == target || base == target {
			copy := item
			return &copy, nil
		}
		if strings.HasPrefix(item.Branch, target) || strings.HasPrefix(base, target) || strings.HasPrefix(filepath.Base(item.Path), target) {
			matches = append(matches, item)
		}
	}
	if len(matches) == 1 {
		return &matches[0], nil
	}
	if len(matches) > 1 {
		return nil, fmt.Errorf("ambiguous worktree %q", target)
	}
	return nil, fmt.Errorf("worktree not found: %s", target)
}

func (s *Service) Create(ctx context.Context, requirement string) (*Created, error) {
	base := generatedName(requirement)
	if s.nameGenerator != nil {
		if generated, generateErr := s.nameGenerator(ctx, requirement); generateErr == nil {
			base = generatedName(generated)
		}
	}
	head, err := s.runner.Run(ctx, s.cwd, "rev-parse", "HEAD")
	if err != nil {
		return nil, fmt.Errorf("resolve creation base: %w", err)
	}
	head = strings.TrimSpace(head)
	parent := filepath.Join(s.root, s.repositoryID)
	if err := os.MkdirAll(parent, 0o755); err != nil {
		return nil, err
	}
	for attempt := 0; attempt < 100; attempt++ {
		name := base
		if attempt > 0 {
			suffix := fmt.Sprintf("-%d", attempt+1)
			name = truncateName(base, 20-len(suffix)) + suffix
		}
		branch := s.prefix + name
		path := filepath.Join(parent, name)
		if _, statErr := os.Stat(path); statErr == nil || !os.IsNotExist(statErr) {
			continue
		}
		if _, branchErr := s.runner.Run(ctx, s.cwd, "show-ref", "--verify", "--quiet", "refs/heads/"+branch); branchErr == nil {
			continue
		}
		if _, err := s.runner.Run(ctx, s.cwd, "check-ref-format", "--branch", branch); err != nil {
			return nil, err
		}
		if _, err := s.runner.Run(ctx, s.cwd, "worktree", "add", "-b", branch, path, head); err != nil {
			return nil, fmt.Errorf("create worktree: %w", err)
		}
		created := &Created{Worktree: Worktree{Path: path, HEAD: head, Branch: branch}, service: s, branch: true, path: true}
		if err := s.Associate(path, branch, "", ""); err != nil {
			rollbackErr := created.Rollback(ctx)
			if rollbackErr != nil {
				return nil, fmt.Errorf("persist worktree registry: %w; cleanup: %v", err, rollbackErr)
			}
			return nil, fmt.Errorf("persist worktree registry: %w", err)
		}
		return created, nil
	}
	return nil, fmt.Errorf("could not allocate a unique worktree name for %q", requirement)
}

func (c *Created) Rollback(ctx context.Context) error {
	if c == nil || c.service == nil {
		return nil
	}
	var failures []string
	if c.path {
		if _, err := c.service.runner.Run(ctx, c.service.controlDir, "worktree", "remove", c.Worktree.Path); err != nil {
			failures = append(failures, err.Error())
		} else {
			c.path = false
		}
	}
	if c.branch && !c.path {
		if _, err := c.service.runner.Run(ctx, c.service.controlDir, "branch", "-d", c.Worktree.Branch); err != nil {
			failures = append(failures, err.Error())
		} else {
			c.branch = false
		}
	}
	if !c.path && !c.branch {
		if sameCanonicalPath(c.service.cwd, c.Worktree.Path) {
			c.service.cwd = c.service.controlDir
		}
		if err := c.service.forget(c.Worktree.Path); err != nil {
			failures = append(failures, err.Error())
		}
	}
	if len(failures) > 0 {
		return fmt.Errorf("rollback worktree: %s", strings.Join(failures, "; "))
	}
	return nil
}
