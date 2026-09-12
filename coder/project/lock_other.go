//go:build !unix && !windows

package project

func acquireProjectFileLock(string) (func(), error) { return func() {}, nil }
