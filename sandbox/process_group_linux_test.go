//go:build linux

package sandbox

import (
	"os/exec"
	"testing"
)

func TestConfigureProcessGroupStartsLinuxProcessGroup(t *testing.T) {
	cmd := exec.Command("/bin/true")
	configureProcessGroup(cmd)
	if cmd.SysProcAttr == nil || !cmd.SysProcAttr.Setpgid {
		t.Fatalf("configureProcessGroup() = %#v, want Setpgid", cmd.SysProcAttr)
	}
	if cmd.SysProcAttr.Setsid {
		t.Fatalf("configureProcessGroup() = %#v, Setsid conflicts with bwrap --new-session", cmd.SysProcAttr)
	}
}
