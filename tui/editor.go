package tui

import (
	"fmt"
	"os"
	"os/exec"
	"runtime"
	"strings"

	tea "charm.land/bubbletea/v2"

	"github.com/basenana/friday/shellcmd"
)

type editorFinishedMsg struct {
	text string
	err  error
}

func (m *model) openEditor() tea.Cmd {
	editor := strings.TrimSpace(os.Getenv("VISUAL"))
	if editor == "" {
		editor = strings.TrimSpace(os.Getenv("EDITOR"))
	}
	if editor == "" {
		m.appendBlock(chatBlock{kind: blockError, content: "VISUAL and EDITOR are not set"})
		return nil
	}
	f, err := os.CreateTemp("", "friday-prompt-*.md")
	if err != nil {
		m.appendBlock(chatBlock{kind: blockError, content: "editor: " + err.Error()})
		return nil
	}
	path := f.Name()
	if _, err = f.WriteString(m.textarea.Value()); err == nil {
		err = f.Close()
	} else {
		_ = f.Close()
	}
	if err != nil {
		_ = os.Remove(path)
		m.appendBlock(chatBlock{kind: blockError, content: "editor: " + err.Error()})
		return nil
	}
	command := editorProcess(editor, path)
	return tea.ExecProcess(command, func(execErr error) tea.Msg {
		defer os.Remove(path)
		if execErr != nil {
			return editorFinishedMsg{err: execErr}
		}
		data, readErr := os.ReadFile(path)
		if readErr != nil {
			return editorFinishedMsg{err: readErr}
		}
		if len(data) > 4<<20 {
			return editorFinishedMsg{err: fmt.Errorf("edited prompt exceeds 4 MiB")}
		}
		return editorFinishedMsg{text: string(data)}
	})
}

func editorProcess(editor, path string) *exec.Cmd {
	if runtime.GOOS == "windows" {
		shell := strings.TrimSpace(os.Getenv("ComSpec"))
		if shell == "" {
			shell = "cmd.exe"
		}
		// TempDir paths are controlled by the runtime. Quoting still protects
		// spaces and shell metacharacters in a user-configured temp directory.
		quotedPath := `"` + strings.ReplaceAll(path, `"`, `""`) + `"`
		return exec.Command(shell, "/d", "/s", "/c", editor+" "+quotedPath)
	}
	shell := strings.TrimSpace(os.Getenv("SHELL"))
	if shell == "" {
		shell = "/bin/sh"
	}
	return exec.Command(shell, "-c", editor+" "+shellcmd.QuoteArg(path))
}
