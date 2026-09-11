//go:build !unix && !windows

package file

// Platforms without a supported advisory file-lock primitive still receive
// process-local serialization and atomic metadata replacement.
func acquireMetadataFileLock(string) (func(), error) {
	return func() {}, nil
}
