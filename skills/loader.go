package skills

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"gopkg.in/yaml.v3"
)

// Loader loads and manages skills from a directory
type Loader struct {
	skillsPath string
	skills     map[string]*Skill
}

// NewLoader creates a new skill loader
func NewLoader(skillsPath string) *Loader {
	return &Loader{
		skillsPath: skillsPath,
		skills:     make(map[string]*Skill),
	}
}

// Load traverses the skills directory and loads all skills
func (l *Loader) Load() error {
	if _, err := os.Stat(l.skillsPath); os.IsNotExist(err) {
		return nil
	}

	entries, err := os.ReadDir(l.skillsPath)
	if err != nil {
		return fmt.Errorf("read skills directory: %w", err)
	}

	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}

		skillPath := filepath.Join(l.skillsPath, entry.Name())
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

	frontmatter, instructions, err := parseSkillFile(content)
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

// parseSkillFile parses SKILL.md content into frontmatter and instructions
func parseSkillFile(content []byte) (*Frontmatter, string, error) {
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
	decoder.KnownFields(true)

	if err := decoder.Decode(&frontmatter); err != nil {
		return nil, "", fmt.Errorf("parse frontmatter: %w", err)
	}

	return &frontmatter, instructions, nil
}

// Get returns a skill by name
func (l *Loader) Get(name string) *Skill {
	return l.skills[name]
}

// LoadSkillFromDir loads and returns a skill from a subdirectory name
func (l *Loader) LoadSkillFromDir(dirName string) (*Skill, error) {
	skillPath := filepath.Join(l.skillsPath, dirName)
	return l.loadSkill(skillPath)
}

// List returns all loaded skills
func (l *Loader) List() []*Skill {
	skills := make([]*Skill, 0, len(l.skills))
	for _, skill := range l.skills {
		skills = append(skills, skill)
	}
	return skills
}

// LoadResource loads a resource file from a skill
func (l *Loader) LoadResource(skillName, resourcePath string) ([]byte, error) {
	skill := l.skills[skillName]
	if skill == nil {
		return nil, fmt.Errorf("skill not found: %s", skillName)
	}

	cleanBasePath := filepath.Clean(skill.BasePath)
	fullPath := filepath.Join(cleanBasePath, resourcePath)

	cleanFullPath := filepath.Clean(fullPath)
	if !strings.HasPrefix(cleanFullPath, cleanBasePath+string(filepath.Separator)) {
		return nil, fmt.Errorf("invalid resource path (path traversal): %s", resourcePath)
	}

	content, err := os.ReadFile(cleanFullPath)
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

// SkillsPath returns the skills directory path
func (l *Loader) SkillsPath() string {
	return l.skillsPath
}
