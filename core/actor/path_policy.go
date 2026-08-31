package actor

import (
	"fmt"
	"path/filepath"
	"strings"
)

// FilePathValidator validates file-card paths before they are emitted.
type FilePathValidator func(path string) error

// defaultFilePathValidator keeps file cards inside the conventional
// /sandbox namespace used by the actor UI contract.
func defaultFilePathValidator(path string) error {
	path = strings.TrimSpace(path)
	if path == "" {
		return fmt.Errorf("file card path is required")
	}
	if !filepath.IsAbs(path) {
		return fmt.Errorf("file card path must be absolute")
	}
	clean := filepath.Clean(path)
	if clean != path {
		return fmt.Errorf("file card path must be clean")
	}
	if clean != "/sandbox" && !strings.HasPrefix(clean, "/sandbox/") {
		return fmt.Errorf("file card path must stay within /sandbox")
	}
	return nil
}

// defaultRichPathValidator adapts the rich-card workdir-relative contract to
// the legacy standalone Actor's /sandbox file namespace. Platform runtimes
// override both validators with their workdir policy via WithFilePathValidator.
func defaultRichPathValidator(path string) error {
	return defaultFilePathValidator("/sandbox/" + path)
}
