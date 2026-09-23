package tui

import (
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/basenana/friday/config"
	"github.com/basenana/friday/shellcmd"
)

func TestResolveVSCodeReviewLaunchFallsBackToMacOSOpen(t *testing.T) {
	plan := vscodeReviewPlan{Direct: true, Command: "code", Args: []string{"-n", "/tmp/review"}}
	lookup := func(name string) (string, error) {
		if name == "code" {
			return "", errors.New("not found")
		}
		if name == "open" {
			return "/usr/bin/open", nil
		}
		return "", errors.New("unexpected command")
	}

	got, err := resolveVSCodeReviewLaunch(plan, "darwin", lookup)
	if err != nil {
		t.Fatal(err)
	}
	wantArgs := []string{"-na", "Visual Studio Code", "--args", "-n", "/tmp/review"}
	if got.Command != "/usr/bin/open" || !reflect.DeepEqual(got.Args, wantArgs) {
		t.Fatalf("launch = %#v, want macOS open %v", got, wantArgs)
	}
}

func TestResolveVSCodeReviewLaunchReportsMissingConfiguredCommand(t *testing.T) {
	plan := vscodeReviewPlan{Direct: true, Command: "my-code", Args: []string{"-n", "/tmp/review"}}
	_, err := resolveVSCodeReviewLaunch(plan, "darwin", func(string) (string, error) {
		return "", errors.New("not found")
	})
	if err == nil || !strings.Contains(err.Error(), "editor.command") {
		t.Fatalf("error = %v, want editor.command guidance", err)
	}
}

func TestPlanVSCodeReviewDirectLaunch(t *testing.T) {
	path := "/srv/project/.friday/worktrees/review target"
	for _, tc := range []struct {
		name string
		env  reviewEnvironment
		args []string
	}{
		{name: "local", args: []string{"-n", path}},
		{name: "vscode remote terminal", env: reviewEnvironment{VSCodeIPC: true, SSH: true}, args: []string{"-r", path}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			plan, err := planVSCodeReview(path, config.EditorConfig{Command: "code"}, tc.env)
			if err != nil {
				t.Fatal(err)
			}
			if !plan.Direct || plan.Command != "code" || !reflect.DeepEqual(plan.Args, tc.args) {
				t.Fatalf("plan = %#v, want direct code %v", plan, tc.args)
			}
		})
	}
}

func TestPlanVSCodeReviewOrdinarySSHReturnsLocalOpenInstructions(t *testing.T) {
	path := "/srv/project/worktrees/review `target`"
	command := "/Applications/Visual Studio Code/bin/code"
	plan, err := planVSCodeReview(path, config.EditorConfig{Command: command, RemoteAuthority: "ssh-remote+dev box"}, reviewEnvironment{SSH: true})
	if err != nil {
		t.Fatal(err)
	}
	if plan.Direct {
		t.Fatal("ordinary SSH attempted to launch a remote GUI")
	}
	if !strings.Contains(plan.Message, "vscode://vscode-remote/ssh-remote+dev%20box/srv/project/worktrees/review%20%60target%60") {
		t.Fatalf("remote URI missing or unescaped: %q", plan.Message)
	}
	wantCommand := shellcmd.Join(command, "--remote", "ssh-remote+dev box", path)
	if !strings.Contains(plan.Message, wantCommand) {
		t.Fatalf("local fallback command missing or unsafe: %q", plan.Message)
	}
	if strings.Contains(plan.Message, "`"+wantCommand+"`") {
		t.Fatalf("fallback command used an unsafe inline code span: %q", plan.Message)
	}
}

func TestPlanVSCodeReviewConfiguredRemoteDoesNotDependOnSSHEnvironment(t *testing.T) {
	plan, err := planVSCodeReview("/srv/project/worktree", config.EditorConfig{
		Command:         "code",
		RemoteAuthority: "ssh-remote+dev",
	}, reviewEnvironment{})
	if err != nil {
		t.Fatal(err)
	}
	if plan.Direct || !strings.Contains(plan.Message, "vscode://vscode-remote/ssh-remote+dev/srv/project/worktree") {
		t.Fatalf("configured remote plan = %#v, want local open instructions", plan)
	}
}

func TestPlanVSCodeReviewOrdinarySSHRequiresAuthority(t *testing.T) {
	_, err := planVSCodeReview("/srv/project", config.EditorConfig{Command: "code"}, reviewEnvironment{SSH: true})
	if err == nil || !strings.Contains(err.Error(), "editor.remote_authority") {
		t.Fatalf("error = %v", err)
	}
}
