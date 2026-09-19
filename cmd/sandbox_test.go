package main

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/basenana/friday/config"
	"github.com/basenana/friday/sandbox"
)

func TestSandboxAllowCommandPersistsProjectGrant(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	projectDir := t.TempDir()

	origWd, wdErr := os.Getwd()
	if wdErr != nil {
		t.Skipf("getwd: %v", wdErr)
	}
	if err := os.Chdir(projectDir); err != nil {
		t.Skipf("chdir: %v", err)
	}
	defer os.Chdir(origWd)

	oldCfg := cfg
	cfg = config.DefaultConfig()
	cfg.DataDir = filepath.Join(home, ".friday")
	defer func() { cfg = oldCfg }()

	if err := sandboxAllowCmd.RunE(sandboxAllowCmd, []string{"gofmt"}); err != nil {
		t.Fatalf("RunE: %v", err)
	}
	path, err := sandbox.ProjectAllowPath(cfg.DataDirPath(), projectDir)
	if err != nil {
		t.Fatal(err)
	}
	allow, err := sandbox.LoadProjectAllow(path)
	if err != nil {
		t.Fatalf("LoadProjectAllow: %v", err)
	}
	if len(allow) != 1 || allow[0] != "gofmt" {
		t.Fatalf("allow = %#v, want [gofmt]", allow)
	}
}

func TestSandboxAllowCommandRejectsDenyListedCommand(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	projectDir := t.TempDir()

	origWd, wdErr := os.Getwd()
	if wdErr != nil {
		t.Skipf("getwd: %v", wdErr)
	}
	if err := os.Chdir(projectDir); err != nil {
		t.Skipf("chdir: %v", err)
	}
	defer os.Chdir(origWd)

	oldCfg := cfg
	cfg = config.DefaultConfig()
	cfg.DataDir = filepath.Join(home, ".friday")
	defer func() { cfg = oldCfg }()

	for _, denied := range sandbox.DefaultDeniedCommands {
		if err := sandboxAllowCmd.RunE(sandboxAllowCmd, []string{denied}); err == nil {
			t.Fatalf("deny-listed %q must not be grantable", denied)
		}
	}
	// Nothing was persisted by the rejected attempts.
	path, err := sandbox.ProjectAllowPath(cfg.DataDirPath(), projectDir)
	if err != nil {
		t.Fatal(err)
	}
	if allow, err := sandbox.LoadProjectAllow(path); err != nil || allow != nil {
		t.Fatalf("allow = %#v, err = %v; want no file", allow, err)
	}
}
