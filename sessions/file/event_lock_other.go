//go:build !unix && !windows

package file

import "fmt"

func tryAcquireEventWriterLock(string) (func(), error) {
	return nil, fmt.Errorf("event writer locking is unsupported on this platform")
}
