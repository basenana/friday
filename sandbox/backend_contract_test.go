package sandbox

import (
	"context"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/basenana/friday/shellcmd"
)

const backendContractHelperEnv = "FRIDAY_BACKEND_CONTRACT_HELPER"

func backendContractRequired() bool {
	return os.Getenv("FRIDAY_REQUIRE_NATIVE_SANDBOX") == "1"
}

func nativeBackendExecutor(t *testing.T, cfg *Config) *Executor {
	t.Helper()
	if runtime.GOOS != "darwin" && runtime.GOOS != "linux" {
		if backendContractRequired() {
			t.Fatalf("native sandbox is unsupported on %s", runtime.GOOS)
		}
		t.Skipf("native sandbox is unsupported on %s", runtime.GOOS)
	}
	cfg.Sandbox.Enabled = true
	cfg.Permissions.Allow = []string{"*"}
	cfg.Permissions.Deny = nil
	executor := NewExecutor(cfg)
	if !executor.IsSandboxAvailable() {
		message := fmt.Sprintf("native %s backend is unavailable on %s", executor.SandboxName(), runtime.GOOS)
		if backendContractRequired() {
			t.Fatal(message)
		}
		t.Skip(message)
	}
	return executor
}

func runBackendContract(t *testing.T, executor *Executor, command, workdir, homeDir string, env ...string) *Result {
	t.Helper()
	result, err := executor.Run(context.Background(), command, ExecOptions{
		Workdir: workdir,
		HomeDir: homeDir,
		Env:     env,
		Timeout: 10 * time.Second,
	})
	if err != nil {
		t.Fatalf("Run(%q): %v (result=%+v)", command, err, result)
	}
	if result == nil {
		t.Fatalf("Run(%q) returned nil result", command)
	}
	return result
}

func requireContractSuccess(t *testing.T, result *Result) {
	t.Helper()
	if result.ExitCode != 0 {
		t.Fatalf("command failed: exit=%d stdout=%q stderr=%q", result.ExitCode, result.Stdout, result.Stderr)
	}
}

func requireContractFailure(t *testing.T, result *Result) {
	t.Helper()
	if result.ExitCode == 0 {
		t.Fatalf("command unexpectedly succeeded: stdout=%q stderr=%q", result.Stdout, result.Stderr)
	}
}

func TestBackendContractFilesystem(t *testing.T) {
	root := t.TempDir()
	workdir := filepath.Join(root, "work")
	homeDir := filepath.Join(root, "home")
	writeRoot := filepath.Join(root, "write")
	readonlyDir := filepath.Join(workdir, "readonly")
	protectedFile := filepath.Join(workdir, "existing.pem")
	denyDir := filepath.Join(workdir, "denied")
	denyFile := filepath.Join(workdir, "denied.txt")
	symlinkTarget := filepath.Join(root, "physical-secret.txt")
	symlinkAlias := filepath.Join(workdir, "secret-link")
	hostFile := filepath.Join(root, "host.txt")
	for _, dir := range []string{workdir, homeDir, writeRoot, readonlyDir, denyDir} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	for path, data := range map[string]string{
		filepath.Join(readonlyDir, "value.txt"): "readonly",
		protectedFile:                           "protected",
		filepath.Join(denyDir, "secret.txt"):    "directory-secret",
		denyFile:                                "file-secret",
		symlinkTarget:                           "symlink-secret",
		hostFile:                                "host",
	} {
		if err := os.WriteFile(path, []byte(data), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.Symlink(symlinkTarget, symlinkAlias); err != nil {
		t.Fatal(err)
	}

	cfg := DefaultConfig()
	cfg.Sandbox.Network.Isolation = true
	cfg.Sandbox.Filesystem = FilesystemConfig{
		Write:     []string{writeRoot},
		ReadOnly:  []string{readonlyDir},
		Protected: []string{"*.pem", "missing-*.key"},
		Deny:      []string{denyDir, denyFile, symlinkAlias, "missing-denied"},
	}
	executor := nativeBackendExecutor(t, cfg)

	t.Run("write roots and default readonly", func(t *testing.T) {
		result := runBackendContract(t, executor,
			"printf work > "+shellcmd.QuoteArg(filepath.Join(workdir, "created.txt"))+" && printf allowed > "+shellcmd.QuoteArg(filepath.Join(writeRoot, "created.txt")),
			workdir, homeDir)
		requireContractSuccess(t, result)
		result = runBackendContract(t, executor, "printf changed >> "+shellcmd.QuoteArg(hostFile), workdir, homeDir)
		requireContractFailure(t, result)
	})

	t.Run("readonly nested in writable workdir", func(t *testing.T) {
		readonlyFile := filepath.Join(readonlyDir, "value.txt")
		result := runBackendContract(t, executor, "cat "+shellcmd.QuoteArg(readonlyFile), workdir, homeDir)
		requireContractSuccess(t, result)
		if !strings.Contains(result.Stdout, "readonly") {
			t.Fatalf("readonly content missing: %q", result.Stdout)
		}
		for name, command := range map[string]string{
			"append":      "printf x >> " + shellcmd.QuoteArg(readonlyFile),
			"unlink":      "rm " + shellcmd.QuoteArg(readonlyFile),
			"rename-over": "printf x > replacement && mv replacement " + shellcmd.QuoteArg(readonlyFile),
			"chmod":       "chmod 600 " + shellcmd.QuoteArg(readonlyFile),
			"create":      "printf x > " + shellcmd.QuoteArg(filepath.Join(readonlyDir, "new.txt")),
		} {
			t.Run(name, func(t *testing.T) {
				requireContractFailure(t, runBackendContract(t, executor, command, workdir, homeDir))
			})
		}
	})

	t.Run("protected glob is a startup snapshot", func(t *testing.T) {
		for name, command := range map[string]string{
			"append":      "printf x >> " + shellcmd.QuoteArg(protectedFile),
			"unlink":      "rm " + shellcmd.QuoteArg(protectedFile),
			"rename-over": "printf x > replacement.pem.tmp && mv replacement.pem.tmp " + shellcmd.QuoteArg(protectedFile),
			"truncate":    ": > " + shellcmd.QuoteArg(protectedFile),
		} {
			t.Run(name, func(t *testing.T) {
				requireContractFailure(t, runBackendContract(t, executor, command, workdir, homeDir))
			})
		}
		future := filepath.Join(workdir, "future.pem")
		requireContractSuccess(t, runBackendContract(t, executor, "printf future > "+shellcmd.QuoteArg(future), workdir, homeDir))
	})

	t.Run("deny masks original objects", func(t *testing.T) {
		result := runBackendContract(t, executor, "cat "+shellcmd.QuoteArg(denyFile), workdir, homeDir)
		if strings.Contains(result.Stdout, "file-secret") {
			t.Fatalf("denied file content was exposed: %q", result.Stdout)
		}
		requireContractFailure(t, runBackendContract(t, executor, "printf replacement > "+shellcmd.QuoteArg(denyFile), workdir, homeDir))
		requireContractFailure(t, runBackendContract(t, executor, "cat "+shellcmd.QuoteArg(filepath.Join(denyDir, "secret.txt")), workdir, homeDir))
		requireContractFailure(t, runBackendContract(t, executor, "printf replacement > "+shellcmd.QuoteArg(filepath.Join(denyDir, "new.txt")), workdir, homeDir))
		result = runBackendContract(t, executor, "cat "+shellcmd.QuoteArg(symlinkTarget), workdir, homeDir)
		if strings.Contains(result.Stdout, "symlink-secret") {
			t.Fatalf("canonical symlink target was exposed: %q", result.Stdout)
		}
	})
}

func TestBackendContractStandardDevices(t *testing.T) {
	workdir := t.TempDir()
	cfg := DefaultConfig()
	cfg.Sandbox.Filesystem = FilesystemConfig{}
	executor := nativeBackendExecutor(t, cfg)
	result := runBackendContract(t, executor,
		"printf x > /dev/null && cat /dev/null && "+
			"test \"$(head -c 1 /dev/zero | wc -c)\" = 1 && "+
			"test \"$(head -c 1 /dev/random | wc -c)\" = 1 && "+
			"test \"$(head -c 1 /dev/urandom | wc -c)\" = 1",
		workdir, workdir)
	requireContractSuccess(t, result)
	for _, device := range []string{"/dev/zero", "/dev/random", "/dev/urandom"} {
		result = runBackendContract(t, executor, "! sh -c "+shellcmd.QuoteArg("printf x > "+device), workdir, workdir)
		requireContractSuccess(t, result)
	}
}

func TestBackendContractNetworkSwitch(t *testing.T) {
	workdir := t.TempDir()
	homeDir := filepath.Join(workdir, "home")
	if err := os.Mkdir(homeDir, 0o755); err != nil {
		t.Fatal(err)
	}
	helper := shellcmd.Join(os.Args[0], "-test.run=^TestBackendContractHelperProcess$")

	t.Run("isolation disabled allows connect", func(t *testing.T) {
		listener := contractListener(t)
		defer listener.Close()
		accepted := make(chan error, 1)
		go func() {
			conn, err := listener.Accept()
			if err == nil {
				_ = conn.Close()
			}
			accepted <- err
		}()
		cfg := DefaultConfig()
		cfg.Sandbox.Filesystem = FilesystemConfig{}
		cfg.Sandbox.Network.Isolation = false
		executor := nativeBackendExecutor(t, cfg)
		result := runBackendContract(t, executor, helper, workdir, homeDir,
			backendContractHelperEnv+"=connect", "FRIDAY_BACKEND_CONTRACT_ADDR="+listener.Addr().String())
		requireContractSuccess(t, result)
		select {
		case err := <-accepted:
			if err != nil {
				t.Fatalf("accept sandbox connection: %v", err)
			}
		case <-time.After(3 * time.Second):
			t.Fatal("sandbox connection was not accepted")
		}
	})

	t.Run("isolation disabled allows listen and accept", func(t *testing.T) {
		cfg := DefaultConfig()
		cfg.Sandbox.Filesystem = FilesystemConfig{}
		cfg.Sandbox.Network.Isolation = false
		executor := nativeBackendExecutor(t, cfg)
		marker := filepath.Join(workdir, "listener.addr")
		type runOutcome struct {
			result *Result
			err    error
		}
		done := make(chan runOutcome, 1)
		go func() {
			result, err := executor.Run(context.Background(), helper, ExecOptions{
				Workdir: workdir,
				HomeDir: homeDir,
				Env: []string{
					backendContractHelperEnv + "=listen",
					"FRIDAY_BACKEND_CONTRACT_MARKER=" + marker,
				},
				Timeout: 10 * time.Second,
			})
			done <- runOutcome{result: result, err: err}
		}()
		addr := waitForContractMarker(t, marker)
		conn, err := net.DialTimeout("tcp", addr, 2*time.Second)
		if err != nil {
			t.Fatalf("connect to sandbox listener %s: %v", addr, err)
		}
		_ = conn.Close()
		outcome := <-done
		if outcome.err != nil {
			t.Fatalf("sandbox listener failed: %v (result=%+v)", outcome.err, outcome.result)
		}
		requireContractSuccess(t, outcome.result)
	})

	t.Run("isolation blocks host listener", func(t *testing.T) {
		listener := contractListener(t)
		defer listener.Close()
		cfg := DefaultConfig()
		cfg.Sandbox.Filesystem = FilesystemConfig{}
		cfg.Sandbox.Network.Isolation = true
		executor := nativeBackendExecutor(t, cfg)
		result := runBackendContract(t, executor, helper, workdir, homeDir,
			backendContractHelperEnv+"=connect", "FRIDAY_BACKEND_CONTRACT_ADDR="+listener.Addr().String())
		requireContractFailure(t, result)
	})
}

func TestBackendContractTimeoutReapsDescendants(t *testing.T) {
	workdir := t.TempDir()
	ready := filepath.Join(workdir, "descendant-ready")
	marker := filepath.Join(workdir, "orphaned")
	cfg := DefaultConfig()
	cfg.Sandbox.Filesystem = FilesystemConfig{}
	executor := nativeBackendExecutor(t, cfg)
	type outcome struct {
		result *Result
		err    error
	}
	done := make(chan outcome, 1)
	go func() {
		result, err := executor.Run(context.Background(),
			"(printf ready > "+shellcmd.QuoteArg(ready)+"; sleep 2; printf orphaned > "+shellcmd.QuoteArg(marker)+") & wait",
			ExecOptions{Workdir: workdir, HomeDir: workdir, Timeout: time.Second})
		done <- outcome{result: result, err: err}
	}()
	waitForContractMarker(t, ready)
	observed := <-done
	if observed.err != nil {
		t.Fatalf("timeout Run: %v", observed.err)
	}
	if observed.result == nil || !observed.result.TimedOut {
		t.Fatalf("expected timeout, got %+v", observed.result)
	}
	time.Sleep(1300 * time.Millisecond)
	if _, err := os.Stat(marker); !os.IsNotExist(err) {
		t.Fatalf("sandbox descendant survived timeout; marker stat = %v", err)
	}
}

func TestBackendContractCancellationReapsDescendants(t *testing.T) {
	workdir := t.TempDir()
	ready := filepath.Join(workdir, "cancel-descendant-ready")
	marker := filepath.Join(workdir, "orphaned-after-cancel")
	cfg := DefaultConfig()
	cfg.Sandbox.Filesystem = FilesystemConfig{}
	executor := nativeBackendExecutor(t, cfg)
	ctx, cancel := context.WithCancel(context.Background())
	type outcome struct {
		result *Result
		err    error
	}
	done := make(chan outcome, 1)
	go func() {
		result, err := executor.Run(ctx,
			"(printf ready > "+shellcmd.QuoteArg(ready)+"; sleep 2; printf orphaned > "+shellcmd.QuoteArg(marker)+") & wait",
			ExecOptions{Workdir: workdir, HomeDir: workdir, Timeout: 5 * time.Second})
		done <- outcome{result: result, err: err}
	}()
	waitForContractMarker(t, ready)
	cancel()
	select {
	case outcome := <-done:
		if outcome.err != nil || outcome.result == nil {
			t.Fatalf("cancelled sandbox command failed unexpectedly: err=%v result=%+v", outcome.err, outcome.result)
		}
		if outcome.result.TimedOut {
			t.Fatalf("explicit cancellation was reported as a timeout: %+v", outcome.result)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("cancelled sandbox command did not exit")
	}
	time.Sleep(2300 * time.Millisecond)
	if _, err := os.Stat(marker); !os.IsNotExist(err) {
		t.Fatalf("sandbox descendant survived cancellation; marker stat = %v", err)
	}
}

func contractListener(t *testing.T) net.Listener {
	t.Helper()
	listener, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		if backendContractRequired() {
			t.Fatalf("native contract requires host loopback listener: %v", err)
		}
		t.Skipf("host loopback listener unavailable: %v", err)
	}
	return listener
}

func waitForContractMarker(t *testing.T, path string) string {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		data, err := os.ReadFile(path)
		if err == nil && strings.TrimSpace(string(data)) != "" {
			return strings.TrimSpace(string(data))
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for helper marker %s", path)
	return ""
}

func TestBackendContractHelperProcess(t *testing.T) {
	switch os.Getenv(backendContractHelperEnv) {
	case "":
		return
	case "connect":
		conn, err := net.DialTimeout("tcp", os.Getenv("FRIDAY_BACKEND_CONTRACT_ADDR"), 2*time.Second)
		if err != nil {
			t.Fatal(err)
		}
		_ = conn.Close()
	case "listen":
		listener, err := net.Listen("tcp4", "127.0.0.1:0")
		if err != nil {
			t.Fatal(err)
		}
		defer listener.Close()
		if err := os.WriteFile(os.Getenv("FRIDAY_BACKEND_CONTRACT_MARKER"), []byte(listener.Addr().String()), 0o600); err != nil {
			t.Fatal(err)
		}
		_ = listener.(*net.TCPListener).SetDeadline(time.Now().Add(4 * time.Second))
		conn, err := listener.Accept()
		if err != nil {
			t.Fatal(err)
		}
		_ = conn.Close()
	default:
		t.Fatalf("unknown helper operation %q", os.Getenv(backendContractHelperEnv))
	}
}

func contractHostPIDCommand(pid int) string {
	return "kill -0 " + strconv.Itoa(pid)
}
