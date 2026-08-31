package skills

import "io/fs"

// Provider defines the interface for skill content sources.
// Implementations can load skills from filesystem, remote APIs, or any other source.
type Provider interface {
	// List returns all available skills
	List() []*Skill
	// Get returns a skill by name, or ErrSkillNotFound if not found
	Get(name string) (*Skill, error)
	// LoadResource loads a resource file from a skill
	LoadResource(skillName, resourcePath string) ([]byte, error)
	// ListResources lists all resources in a skill
	ListResources(skillName string) ([]*Resource, error)
	// ListFiles lists directory entries within a skill's directory at the given sub-path.
	// subPath is relative to the skill root; empty string lists the root.
	ListFiles(skillName, subPath string) ([]fs.DirEntry, error)
	// ReadFile reads an arbitrary file within a skill's directory.
	// filePath is relative to the skill root.
	ReadFile(skillName, filePath string) ([]byte, error)
}
