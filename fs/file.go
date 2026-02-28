package fs

import (
	"os"
	"path/filepath"
	"strings"
)

type FileSystem struct {
	root string
}

func NewFileSystem(root string) *FileSystem {
	return &FileSystem{root: root}
}

func (f *FileSystem) Ls(dirPath string) ([]string, error) {
	absPath := f.AbsolutePath(dirPath)

	entries, err := os.ReadDir(absPath)
	if err != nil {
		return nil, err
	}

	var result []string
	for _, entry := range entries {
		result = append(result, entry.Name())
	}
	return result, nil
}

func (f *FileSystem) MkdirAll(dirPath string) error {
	absPath := f.AbsolutePath(dirPath)
	return os.MkdirAll(absPath, 0755)
}

func (f *FileSystem) Read(filePath string) (string, error) {
	absPath := f.AbsolutePath(filePath)
	data, err := os.ReadFile(absPath)
	if err != nil {
		return "", err
	}
	return string(data), nil
}

func (f *FileSystem) Write(filePath string, data string) error {
	absPath := f.AbsolutePath(filePath)

	dir := filepath.Dir(absPath)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return err
	}

	return os.WriteFile(absPath, []byte(data), 0644)
}

func (f *FileSystem) Delete(path string) error {
	absPath := f.AbsolutePath(path)
	return os.RemoveAll(absPath)
}

func (f *FileSystem) AbsolutePath(path string) string {
	if filepath.IsAbs(path) {
		return path
	}
	return filepath.Join(f.root, path)
}

func (f *FileSystem) EnsureDir(dirPath string) error {
	absPath := f.AbsolutePath(dirPath)
	return os.MkdirAll(absPath, 0755)
}

func (f *FileSystem) SetRoot(root string) {
	f.root = root
}

func (f *FileSystem) Root() string {
	return f.root
}

func expandHome(path string) string {
	if strings.HasPrefix(path, "~/") {
		home, err := os.UserHomeDir()
		if err != nil {
			return path
		}
		return filepath.Join(home, path[2:])
	}
	return path
}
