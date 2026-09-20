//go:build darwin

package sandbox

import (
	"os/exec"
	"testing"
)

func TestConfigureProcessGroupStartsDarwinSession(t *testing.T) {
	cmd := exec.Command("/usr/bin/true")
	configureProcessGroup(cmd)
	if cmd.SysProcAttr == nil || !cmd.SysProcAttr.Setsid {
		t.Fatalf("configureProcessGroup() = %#v, want Setsid", cmd.SysProcAttr)
	}
	if cmd.SysProcAttr.Setpgid {
		t.Fatalf("configureProcessGroup() = %#v, Setsid already creates a process group", cmd.SysProcAttr)
	}
}
