//go:build linux

package sandbox

func addLinuxRuntimeCompatMounts(args []string) []string {
	return append(args,
		"--dir", "/var",
		"--symlink", "../run", "/var/run",
	)
}
