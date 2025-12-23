package vfs

import (
	"context"
)

type VirtualFileSystem interface {
	ListFiles(ctx context.Context) ([]*VFile, error)
	ReadFile(ctx context.Context, fname string) (*VFile, error)
	WriteFile(ctx context.Context, file *VFile) (*VFile, error)
}

type VFile struct {
	Filename string `json:"filename"`
	Abstract string `json:"abstract"`
	Content  string `json:"content,omitempty"`
	Filtered string `json:"filtered,omitempty"`
}
