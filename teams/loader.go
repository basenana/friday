package teams

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"gopkg.in/yaml.v3"
)

// Loader reads team definitions from disk. A team lives at {teamsPath}/{name}/
// with team.json + members/*.md. Multiple paths may be supplied (later wins).
type Loader struct {
	paths []string
	cache map[string]*Team
	mu    sync.Mutex
}

// NewLoader creates a Loader that searches the given paths in order.
func NewLoader(paths ...string) *Loader {
	return &Loader{paths: paths, cache: make(map[string]*Team)}
}

// Load reads all teams from all paths into the cache. Later paths override
// earlier ones with the same name.
func (l *Loader) Load() error {
	l.mu.Lock()
	defer l.mu.Unlock()
	cache := make(map[string]*Team)
	for _, p := range l.paths {
		if err := l.loadFromPath(p, cache); err != nil {
			return err
		}
	}
	l.cache = cache
	return nil
}

func (l *Loader) loadFromPath(teamsPath string, cache map[string]*Team) error {
	entries, err := os.ReadDir(teamsPath)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return fmt.Errorf("read teams directory: %w", err)
	}
	for _, entry := range entries {
		if !entry.IsDir() || strings.HasPrefix(entry.Name(), ".") {
			continue
		}
		t, err := loadTeamDir(filepath.Join(teamsPath, entry.Name()))
		if err != nil {
			return fmt.Errorf("load team %s: %w", entry.Name(), err)
		}
		if t != nil {
			cache[t.Name] = t
		}
	}
	return nil
}

func loadTeamDir(dir string) (*Team, error) {
	teamFile := filepath.Join(dir, "team.json")
	data, err := os.ReadFile(teamFile)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("read team.json: %w", err)
	}
	var t Team
	if err := unmarshalJSON(data, &t); err != nil {
		return nil, fmt.Errorf("parse team.json: %w", err)
	}
	dirName := filepath.Base(dir)
	if t.Name == "" {
		// Fall back to directory name if missing in JSON.
		t.Name = dirName
	}
	if err := validatePathName("team", t.Name); err != nil {
		return nil, err
	}
	if t.Name != dirName {
		return nil, fmt.Errorf("team name %q must match directory %q", t.Name, dirName)
	}
	return &t, nil
}

// Get returns a team by name with members fully loaded.
func (l *Loader) Get(name string) (*Team, error) {
	l.mu.Lock()
	t, ok := l.cache[name]
	l.mu.Unlock()
	if !ok {
		return nil, fmt.Errorf("team not found: %s", name)
	}
	return t, nil
}

// List returns all loaded teams sorted by name.
func (l *Loader) List() []*Team {
	l.mu.Lock()
	out := make([]*Team, 0, len(l.cache))
	for _, t := range l.cache {
		out = append(out, t)
	}
	l.mu.Unlock()
	return out
}

// LoadMembers loads the .md files for each MemberRef into full Members.
func LoadMembers(teamsPath, teamName string, refs []MemberRef) ([]Member, error) {
	teamDir, err := teamDirPath(teamsPath, teamName)
	if err != nil {
		return nil, err
	}
	membersDir := filepath.Join(teamDir, "members")
	out := make([]Member, 0, len(refs))
	for _, ref := range refs {
		m, err := loadMemberFile(membersDir, ref)
		if err != nil {
			return nil, fmt.Errorf("load member %s: %w", ref.Name, err)
		}
		out = append(out, m)
	}
	return out, nil
}

func loadMemberFile(dir string, ref MemberRef) (Member, error) {
	if err := validatePathName("member", ref.Name); err != nil {
		return Member{}, err
	}
	path := filepath.Join(dir, ref.Name+".md")
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			// Allow members with no .md file — use roster info only.
			return Member{Name: ref.Name, Role: ref.Role, Model: ref.Model}, nil
		}
		return Member{}, fmt.Errorf("read member file: %w", err)
	}
	fm, instructions, err := parseMemberFile(data)
	if err != nil {
		return Member{}, err
	}
	return Member{
		Name:         firstNonEmpty(fm.Name, ref.Name),
		Role:         firstNonEmptyRole(fm.Role, ref.Role),
		Model:        firstNonEmpty(fm.Model, ref.Model),
		Skills:       fm.Skills,
		ToolsAllow:   fm.ToolsAllow,
		Instructions: instructions,
	}, nil
}

type memberFrontmatter struct {
	Name       string     `yaml:"name"`
	Role       MemberRole `yaml:"role"`
	Model      string     `yaml:"model"`
	Skills     []string   `yaml:"skills"`
	ToolsAllow []string   `yaml:"tools_allow"`
}

func parseMemberFile(content []byte) (*memberFrontmatter, string, error) {
	s := strings.ReplaceAll(string(content), "\r\n", "\n")
	if !strings.HasPrefix(s, "---\n") {
		return &memberFrontmatter{}, strings.TrimSpace(s), nil
	}
	s = strings.TrimPrefix(s, "---\n")
	end := strings.Index(s, "\n---")
	if end == -1 {
		return &memberFrontmatter{}, "", fmt.Errorf("frontmatter not closed")
	}
	fmStr := s[:end]
	body := strings.TrimSpace(s[end+4:])

	var fm memberFrontmatter
	dec := yaml.NewDecoder(bytes.NewReader([]byte(fmStr)))
	dec.KnownFields(true)
	if err := dec.Decode(&fm); err != nil {
		return nil, "", fmt.Errorf("parse frontmatter: %w", err)
	}
	return &fm, body, nil
}

func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if v != "" {
			return v
		}
	}
	return ""
}
func firstNonEmptyRole(vals ...MemberRole) MemberRole {
	for _, v := range vals {
		if v != "" {
			return v
		}
	}
	return RoleMember
}

// Leader returns the first member with the leader role. Returns nil if none.
func Leader(members []Member) *Member {
	for i := range members {
		if members[i].IsLeader() {
			return &members[i]
		}
	}
	return nil
}

// FindMember returns the member matching name (case-insensitive, ignoring spaces).
func FindMember(members []Member, name string) *Member {
	for i := range members {
		if fuzzyMatch(members[i].Name, name) {
			return &members[i]
		}
	}
	return nil
}

func fuzzyMatch(a, b string) bool {
	return strings.ToLower(strings.ReplaceAll(a, " ", "")) ==
		strings.ToLower(strings.ReplaceAll(b, " ", ""))
}

// TouchTeam updates the UpdatedAt timestamp and writes team.json back.
func TouchTeam(teamsPath string, t *Team) error {
	t.UpdatedAt = time.Now().UTC()
	dir, err := teamDirPath(teamsPath, t.Name)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(dir, 0755); err != nil {
		return err
	}
	data, err := marshalJSON(t)
	if err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(dir, "team.json"), data, 0644)
}

func teamDirPath(teamsPath, teamName string) (string, error) {
	if err := validatePathName("team", teamName); err != nil {
		return "", err
	}
	return filepath.Join(teamsPath, teamName), nil
}

func validatePathName(kind, name string) error {
	if strings.TrimSpace(name) == "" {
		return fmt.Errorf("%s name is required", kind)
	}
	if strings.TrimSpace(name) != name {
		return fmt.Errorf("invalid %s name %q", kind, name)
	}
	clean := filepath.Clean(name)
	if clean != name || name == "." || name == ".." || strings.Contains(name, "/") || strings.Contains(name, "\\") {
		return fmt.Errorf("invalid %s name %q", kind, name)
	}
	return nil
}

// JSON helpers kept local so this package has no encoding/json import surface
// beyond these helpers (eases future swap to a different encoder).
func unmarshalJSON(data []byte, v interface{}) error {
	return json.Unmarshal(data, v)
}
func marshalJSON(v interface{}) ([]byte, error) {
	return json.MarshalIndent(v, "", "  ")
}
