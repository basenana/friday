package skills

import (
	"bytes"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"
)

// Loader loads and manages skills from multiple directories (PATH-like)
type Loader struct {
	skillsPaths []string
	skills      map[string]*Skill
}

// NewLoader creates a new skill loader with multiple paths
func NewLoader(skillsPaths ...string) *Loader {
	return &Loader{
		skillsPaths: skillsPaths,
		skills:      make(map[string]*Skill),
	}
}

// Load traverses all skills directories and loads skills
// Directories are processed in order; later directories override earlier ones
func (l *Loader) Load() error {
	for _, skillsPath := range l.skillsPaths {
		if err := l.loadFromDir(skillsPath); err != nil {
			return err
		}
	}
	return nil
}

// loadFromDir loads skills from a single directory
func (l *Loader) loadFromDir(skillsPath string) error {
	if _, err := os.Stat(skillsPath); os.IsNotExist(err) {
		return nil
	}

	entries, err := os.ReadDir(skillsPath)
	if err != nil {
		return fmt.Errorf("read skills directory: %w", err)
	}

	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}

		if strings.HasPrefix(entry.Name(), ".") {
			continue
		}

		skillPath := filepath.Join(skillsPath, entry.Name())
		skill, err := l.loadSkill(skillPath)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Warning: failed to load skill %s: %v\n", entry.Name(), err)
			continue
		}

		if skill != nil {
			l.skills[skill.Name] = skill
		}
	}

	return nil
}

// loadSkill loads a single skill from its directory
func (l *Loader) loadSkill(skillPath string) (*Skill, error) {
	skillFile := filepath.Join(skillPath, "SKILL.md")

	content, err := os.ReadFile(skillFile)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("read SKILL.md: %w", err)
	}

	frontmatter, instructions, err := ParseSkillFile(content)
	if err != nil {
		return nil, err
	}

	if frontmatter.Name == "" {
		return nil, fmt.Errorf("skill name is required in frontmatter")
	}

	return &Skill{
		Name:         frontmatter.Name,
		Description:  frontmatter.Description,
		Frontmatter:  frontmatter,
		Instructions: instructions,
		BasePath:     skillPath,
	}, nil
}

// ParseSkillFile parses SKILL.md content into frontmatter and instructions
func ParseSkillFile(content []byte) (*Frontmatter, string, error) {
	contentStr := string(content)

	// Normalize line endings for consistent parsing
	contentStr = strings.ReplaceAll(contentStr, "\r\n", "\n")

	if !strings.HasPrefix(contentStr, "---\n") {
		return &Frontmatter{}, contentStr, nil
	}

	contentStr = strings.TrimPrefix(contentStr, "---\n")

	endIdx := strings.Index(contentStr, "\n---")
	if endIdx == -1 {
		return &Frontmatter{}, "", fmt.Errorf("frontmatter not properly closed")
	}

	frontmatterStr := contentStr[:endIdx]
	instructions := strings.TrimSpace(contentStr[endIdx+4:])

	var frontmatter Frontmatter
	decoder := yaml.NewDecoder(bytes.NewReader([]byte(frontmatterStr)))

	if err := decoder.Decode(&frontmatter); err != nil {
		return nil, "", fmt.Errorf("parse frontmatter: %w", err)
	}

	// Unknown keys are captured by the inline Metadata map. Surface them so
	// typo'd frontmatter fields (e.g. `descripton`) are not silently ignored.
	if len(frontmatter.Metadata) > 0 {
		keys := make([]string, 0, len(frontmatter.Metadata))
		for key := range frontmatter.Metadata {
			keys = append(keys, key)
		}
		sort.Strings(keys)
		label := frontmatter.Name
		if label == "" {
			label = "(unnamed)"
		}
		fmt.Fprintf(os.Stderr, "Warning: skill %s: unknown frontmatter fields: %s\n", label, strings.Join(keys, ", "))
	}

	return &frontmatter, instructions, nil
}

// Get returns a skill by name, or an error if not found
func (l *Loader) Get(name string) (*Skill, error) {
	skill := l.skills[name]
	if skill == nil {
		return nil, ErrSkillNotFound{name}
	}
	return skill, nil
}

// LoadSkillFromDir loads and returns a skill from a subdirectory name
// Searches through all paths in order, returns first match
func (l *Loader) LoadSkillFromDir(dirName string) (*Skill, error) {
	for _, skillsPath := range l.skillsPaths {
		skillPath := filepath.Join(skillsPath, dirName)
		skill, err := l.loadSkill(skillPath)
		if err != nil {
			return nil, err
		}
		if skill != nil {
			return skill, nil
		}
	}
	return nil, nil
}

// List returns all loaded skills
func (l *Loader) List() []*Skill {
	skills := make([]*Skill, 0, len(l.skills))
	for _, skill := range l.skills {
		skills = append(skills, skill)
	}
	return skills
}

// resolveWithinSkill resolves both the skill root and the requested path with
// filepath.EvalSymlinks and verifies the target still lives inside the skill
// root. A lexical prefix check alone is insufficient: a symlink created inside
// an installed skill directory after installation can point anywhere on disk.
// A dangling symlink fails EvalSymlinks and is rejected as well.
func resolveWithinSkill(basePath, relPath string) (string, error) {
	if containsPathTraversal(relPath) {
		return "", fmt.Errorf("invalid path (path traversal): %s", relPath)
	}

	resolvedBase, err := filepath.EvalSymlinks(filepath.Clean(basePath))
	if err != nil {
		return "", fmt.Errorf("resolve skill directory: %w", err)
	}

	candidate := filepath.Join(resolvedBase, relPath)
	resolved, err := filepath.EvalSymlinks(candidate)
	if err != nil {
		return "", fmt.Errorf("resolve path %q: %w", relPath, err)
	}

	if resolved != resolvedBase && !strings.HasPrefix(resolved, resolvedBase+string(filepath.Separator)) {
		return "", fmt.Errorf("invalid path (escapes skill directory): %s", relPath)
	}
	return resolved, nil
}

// LoadResource loads a resource file from a skill
func (l *Loader) LoadResource(skillName, resourcePath string) ([]byte, error) {
	skill := l.skills[skillName]
	if skill == nil {
		return nil, fmt.Errorf("skill not found: %s", skillName)
	}

	resolved, err := resolveWithinSkill(skill.BasePath, resourcePath)
	if err != nil {
		return nil, err
	}

	content, err := os.ReadFile(resolved)
	if err != nil {
		return nil, fmt.Errorf("read resource: %w", err)
	}

	return content, nil
}

// ListResources lists all resources in a skill
func (l *Loader) ListResources(skillName string) ([]*Resource, error) {
	skill := l.skills[skillName]
	if skill == nil {
		return nil, fmt.Errorf("skill not found: %s", skillName)
	}

	var resources []*Resource

	refsPath := filepath.Join(skill.BasePath, "references")
	if entries, err := os.ReadDir(refsPath); err == nil {
		for _, entry := range entries {
			if !entry.IsDir() {
				resources = append(resources, &Resource{
					Name: entry.Name(),
					Path: filepath.Join("references", entry.Name()),
				})
			}
		}
	}

	return resources, nil
}

// Delete removes a skill directory
// Disk removal first, then memory - ensures consistency
func (l *Loader) Delete(skillName string) error {
	skill := l.skills[skillName]
	if skill == nil {
		return fmt.Errorf("skill not found: %s", skillName)
	}

	basePath := skill.BasePath

	// Remove from disk first
	if err := os.RemoveAll(basePath); err != nil {
		return fmt.Errorf("remove skill directory: %w", err)
	}

	// Only remove from memory after successful disk removal
	delete(l.skills, skillName)

	return nil
}

// SkillsPaths returns all skills directory paths
func (l *Loader) SkillsPaths() []string {
	return l.skillsPaths
}

// SkillsPath returns the first skills directory path for backward compatibility
// Deprecated: Use SkillsPaths() instead
func (l *Loader) SkillsPath() string {
	if len(l.skillsPaths) > 0 {
		return l.skillsPaths[0]
	}
	return ""
}

// ListFiles lists directory entries within a skill at the given sub-path.
// Symlink entries are skipped: a symlink created inside an installed skill
// directory after installation can point outside the skill root.
func (l *Loader) ListFiles(skillName, subPath string) ([]fs.DirEntry, error) {
	skill := l.skills[skillName]
	if skill == nil {
		return nil, ErrSkillNotFound{skillName}
	}

	dir, err := resolveWithinSkill(skill.BasePath, subPath)
	if err != nil {
		return nil, err
	}

	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}

	filtered := make([]fs.DirEntry, 0, len(entries))
	for _, entry := range entries {
		if entry.Type()&fs.ModeSymlink != 0 {
			continue
		}
		filtered = append(filtered, entry)
	}
	return filtered, nil
}

// ReadFile reads an arbitrary file within a skill's directory.
func (l *Loader) ReadFile(skillName, filePath string) ([]byte, error) {
	skill := l.skills[skillName]
	if skill == nil {
		return nil, ErrSkillNotFound{skillName}
	}

	resolved, err := resolveWithinSkill(skill.BasePath, filePath)
	if err != nil {
		return nil, err
	}
	return os.ReadFile(resolved)
}

func containsPathTraversal(path string) bool {
	for _, part := range strings.Split(filepath.ToSlash(path), "/") {
		if part == ".." {
			return true
		}
	}
	return false
}

// Verify Loader implements Provider
var _ Provider = (*Loader)(nil)
