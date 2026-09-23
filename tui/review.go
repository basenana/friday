package tui

import (
	"errors"
	"fmt"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"

	tea "charm.land/bubbletea/v2"

	"github.com/basenana/friday/config"
	"github.com/basenana/friday/shellcmd"
)

type reviewEnvironment struct {
	SSH       bool
	VSCodeIPC bool
}

type vscodeReviewPlan struct {
	Direct  bool
	Command string
	Args    []string
	Message string
}

type reviewFinishedMsg struct{ err error }

func (m *model) openWorktreeReview() tea.Cmd {
	env := reviewEnvironment{
		SSH:       strings.TrimSpace(os.Getenv("SSH_CONNECTION")) != "" || strings.TrimSpace(os.Getenv("SSH_TTY")) != "",
		VSCodeIPC: strings.TrimSpace(os.Getenv("VSCODE_IPC_HOOK_CLI")) != "",
	}
	plan, err := planVSCodeReview(m.workdir, m.cfg.Editor, env)
	if err != nil {
		m.appendBlock(chatBlock{kind: blockError, content: "review: " + err.Error()})
		return nil
	}
	if !plan.Direct {
		m.appendBlock(chatBlock{kind: blockAssistant, content: plan.Message})
		return nil
	}
	plan, err = resolveVSCodeReviewLaunch(plan, runtime.GOOS, exec.LookPath)
	if err != nil {
		m.appendBlock(chatBlock{kind: blockError, content: "review: " + err.Error()})
		return nil
	}
	return func() tea.Msg {
		output, err := exec.Command(plan.Command, plan.Args...).CombinedOutput()
		if err != nil {
			detail := strings.TrimSpace(string(output))
			if len(detail) > 4096 {
				detail = detail[:4096]
			}
			if detail != "" {
				err = fmt.Errorf("%w: %s", err, detail)
			}
		}
		return reviewFinishedMsg{err: err}
	}
}

func resolveVSCodeReviewLaunch(plan vscodeReviewPlan, goos string, lookPath func(string) (string, error)) (vscodeReviewPlan, error) {
	command, err := lookPath(plan.Command)
	if err == nil {
		plan.Command = command
		return plan, nil
	}
	if goos == "darwin" && plan.Command == "code" {
		openCommand, openErr := lookPath("open")
		if openErr == nil {
			plan.Command = openCommand
			plan.Args = append([]string{"-na", "Visual Studio Code", "--args"}, plan.Args...)
			return plan, nil
		}
	}
	return vscodeReviewPlan{}, fmt.Errorf("VS Code command %q was not found; install the code shell command or configure editor.command", plan.Command)
}

func planVSCodeReview(path string, cfg config.EditorConfig, env reviewEnvironment) (vscodeReviewPlan, error) {
	path = strings.TrimSpace(path)
	if path == "" {
		return vscodeReviewPlan{}, errors.New("worktree path is required")
	}
	path = filepath.Clean(path)
	command := strings.TrimSpace(cfg.Command)
	if command == "" {
		command = "code"
	}
	if env.VSCodeIPC {
		return vscodeReviewPlan{Direct: true, Command: command, Args: []string{"-r", path}}, nil
	}
	authority := strings.TrimSpace(cfg.RemoteAuthority)
	if authority != "" {
		remoteURI := (&url.URL{Scheme: "vscode", Host: "vscode-remote", Path: "/" + authority + filepath.ToSlash(path)}).String()
		localCommand := shellcmd.Join(command, "--remote", authority, path)
		message := fmt.Sprintf("Open this worktree from your local machine:\n\n[Open in VS Code](%s)\n\n%s", remoteURI, markdownCodeBlock(localCommand))
		return vscodeReviewPlan{Message: message}, nil
	}
	if !env.SSH {
		return vscodeReviewPlan{Direct: true, Command: command, Args: []string{"-n", path}}, nil
	}
	return vscodeReviewPlan{}, errors.New("ordinary SSH cannot open the local VS Code GUI; configure editor.remote_authority (for example ssh-remote+dev)")
}

func markdownCodeBlock(content string) string {
	longest := 0
	current := 0
	for _, r := range content {
		if r == '`' {
			current++
			if current > longest {
				longest = current
			}
		} else {
			current = 0
		}
	}
	fence := strings.Repeat("`", max(3, longest+1))
	return fence + "\n" + content + "\n" + fence
}
