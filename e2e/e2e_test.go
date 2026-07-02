//go:build e2e

package e2e

import (
	"os"
	"testing"
)

// TestMain is the shared entry point for the e2e test suite. It does not
// perform any global setup that would prevent individual tests from running
// (each test loads config lazily and skips if absent).
func TestMain(m *testing.M) {
	os.Exit(m.Run())
}
