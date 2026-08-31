package skills

import (
	"errors"
	"io/fs"
	"testing"
)

type fakeProvider struct {
	skills []*Skill
}

func (f *fakeProvider) List() []*Skill                  { return f.skills }
func (f *fakeProvider) Get(name string) (*Skill, error) { return nil, ErrSkillNotFound{name} }
func (f *fakeProvider) LoadResource(skillName, resourcePath string) ([]byte, error) {
	return nil, ErrSkillNotFound{skillName}
}
func (f *fakeProvider) ListResources(skillName string) ([]*Resource, error) {
	return nil, ErrSkillNotFound{skillName}
}
func (f *fakeProvider) ListFiles(skillName, subPath string) ([]fs.DirEntry, error) {
	return nil, ErrSkillNotFound{skillName}
}
func (f *fakeProvider) ReadFile(skillName, filePath string) ([]byte, error) {
	return nil, ErrSkillNotFound{skillName}
}

func TestRegistryDeleteNonLoaderProvider(t *testing.T) {
	registry := NewRegistry(&fakeProvider{})

	err := registry.Delete("some-skill")
	if err == nil {
		t.Fatal("expected error for non-Loader provider")
	}
	if !errors.Is(err, ErrDeleteUnsupported) {
		t.Fatalf("expected ErrDeleteUnsupported, got %v", err)
	}
	var notFound ErrSkillNotFound
	if errors.As(err, &notFound) {
		t.Fatalf("error must not be ErrSkillNotFound, got %v", err)
	}
}

func TestRegistryListReturnsDefensiveCopy(t *testing.T) {
	provider := &fakeProvider{skills: []*Skill{{Name: "a"}, {Name: "b"}}}
	registry := NewRegistry(provider)

	first := registry.List()
	first[0] = &Skill{Name: "mutated"}

	second := registry.List()
	if second[0] == nil || second[0].Name != "a" {
		t.Fatalf("expected List to return a defensive copy, got %#v", second)
	}
}
