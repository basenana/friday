//go:build linux

package sandbox

import "testing"

func TestAddLinuxRuntimeCompatMountsProvidesVarRunSymlink(t *testing.T) {
	args := addLinuxRuntimeCompatMounts(nil)

	want := []string{"--dir", "/var", "--symlink", "../run", "/var/run"}
	if len(args) != len(want) {
		t.Fatalf("args length = %d, want %d: %#v", len(args), len(want), args)
	}
	for i := range want {
		if args[i] != want[i] {
			t.Fatalf("args[%d] = %q, want %q; args=%#v", i, args[i], want[i], args)
		}
	}
}
