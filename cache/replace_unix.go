//go:build !windows

package cache

import "os"

func replaceFile(source, target string) error { return os.Rename(source, target) }
