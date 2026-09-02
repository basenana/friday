package actor

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	coreactor "github.com/basenana/friday/core/actor"
)

// newFilePathValidator is the default card path policy for friday:
// paths must resolve inside the given workdir (the process working
// directory, which is also where the agent's file tools operate) and
// must exist on disk. It replaces core/actor's /sandbox-relative
// default, which belongs to the original embedding platform.
func newFilePathValidator(workdir string) coreactor.FilePathValidator {
	root := filepath.Clean(workdir)
	return func(path string) error {
		p := strings.TrimSpace(path)
		if p == "" {
			return fmt.Errorf("card path is required")
		}
		if !filepath.IsAbs(p) {
			p = filepath.Join(root, p)
		}
		p = filepath.Clean(p)
		if p != root && !strings.HasPrefix(p, root+string(filepath.Separator)) {
			return fmt.Errorf("card path %q escapes workdir %q", p, root)
		}
		if _, err := os.Stat(p); err != nil {
			return fmt.Errorf("card path not accessible: %w", err)
		}
		return nil
	}
}
