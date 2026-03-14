package sandbox

import (
	"testing"
)

func TestParseSimpleCommand(t *testing.T) {
	cmd := "git status"
	commands, err := ParseCommands(cmd)
	if err != nil {
		t.Fatalf("ParseCommands error = %v", err)
	}
	if len(commands) != 1 {
		t.Errorf("Expected 1 command, got %d", len(commands))
	}
	if commands[0].Name != "git" {
		t.Errorf("Expected name 'git', got %q", commands[0].Name)
	}
	if commands[0].Subcmd != "status" {
		t.Errorf("Expected subcmd 'status', got %q", commands[0].Subcmd)
	}
}

func TestParseCompoundCommand(t *testing.T) {
	cmd := "git status && npm install"
	commands, err := ParseCommands(cmd)
	if err != nil {
		t.Fatalf("ParseCommands error = %v", err)
	}
	if len(commands) != 2 {
		t.Errorf("Expected 2 commands, got %d", len(commands))
	}
	if commands[0].Name != "git" {
		t.Errorf("Expected first command 'git', got %q", commands[0].Name)
	}
	if commands[1].Name != "npm" {
		t.Errorf("Expected second command 'npm', got %q", commands[1].Name)
	}
}

func TestParsePipeCommand(t *testing.T) {
	cmd := "cat file | grep pattern | wc -l"
	commands, err := ParseCommands(cmd)
	if err != nil {
		t.Fatalf("ParseCommands error = %v", err)
	}
	if len(commands) != 3 {
		t.Errorf("Expected 3 commands, got %d", len(commands))
	}
	wantNames := []string{"cat", "grep", "wc"}
	for i, c := range commands {
		if c.Name != wantNames[i] {
			t.Errorf("Expected command %d to be %q, got %q", i, wantNames[i], c.Name)
		}
	}
}

func TestParseQuotedArgs(t *testing.T) {
	tests := []struct {
		cmd      string
		wantArgs []string
	}{
		{`echo "hello world"`, []string{"echo", "hello world"}},
		{`git commit -m "fix: bug fix"`, []string{"git", "commit", "-m", "fix: bug fix"}},
	}

	for _, tt := range tests {
		commands, err := ParseCommands(tt.cmd)
		if err != nil {
			t.Fatalf("ParseCommands(%q) error = %v", tt.cmd, err)
		}
		if len(commands) != 1 {
			t.Errorf("Expected 1 command, got %d", len(commands))
		}
		for i, arg := range tt.wantArgs {
			if commands[0].Args[i] != arg {
				t.Errorf("Expected arg %d to be %q, got %q", i, arg, commands[0].Args[i])
			}
		}
	}
}

func TestParseCommandWithSubcmd(t *testing.T) {
	tests := []struct {
		cmd          string
		wantName     string
		wantSubcmd   string
		wantArgCount int
	}{
		{"git push origin main", "git", "push", 4},
		{"npm run build", "npm", "run", 3},
		{"docker run -it ubuntu", "docker", "run", 4},
		{"kubectl get pods", "kubectl", "get", 3},
	}

	for _, tt := range tests {
		commands, err := ParseCommands(tt.cmd)
		if err != nil {
			t.Fatalf("ParseCommands(%q) error = %v", tt.cmd, err)
		}
		if len(commands) != 1 {
			t.Errorf("Expected 1 command, got %d", len(commands))
		}
		if commands[0].Name != tt.wantName {
			t.Errorf("Expected name %q, got %q", tt.wantName, commands[0].Name)
		}
		if commands[0].Subcmd != tt.wantSubcmd {
			t.Errorf("Expected subcmd %q, got %q", tt.wantSubcmd, commands[0].Subcmd)
		}
		if len(commands[0].Args) != tt.wantArgCount {
			t.Errorf("Expected %d args, got %d", tt.wantArgCount, len(commands[0].Args))
		}
	}
}

func TestParseEmptyCommand(t *testing.T) {
	commands, err := ParseCommands("")
	if err != nil {
		t.Errorf("ParseCommands('') should not error, got %v", err)
	}
	if len(commands) != 0 {
		t.Errorf("Expected 0 commands, got %d", len(commands))
	}
}

func TestParseInvalidCommand(t *testing.T) {
	// Shell parser may handle some "invalid" commands gracefully
	invalidCmds := []string{
		"echo 'unclosed",
	}

	for _, cmd := range invalidCmds {
		_, err := ParseCommands(cmd)
		// Error is acceptable for invalid commands
		// We just want to make sure it doesn't panic
		_ = err
	}
}

func TestMatchPattern(t *testing.T) {
	tests := []struct {
		cmd     string
		pattern string
		want    bool
	}{
		// Single wildcard matches anything
		{"git status", "*", true},
		{"npm install", "*", true},
		{"echo hello", "*", true},

		// Command name only - matches commands starting with this name
		{"git status", "git", true},
		{"git push", "git", true},
		{"npm install", "npm", true},
		{"npm install package", "npm", true},
		{"docker ps", "docker", true},
		{"docker run ubuntu", "docker", true},

		// Command + subcommand - matches commands with same command and subcommand
		{"git status", "git status", true},
		{"git push", "git status", false},
		{"npm run", "npm run", true},
		{"npm run build", "npm run", true},        // same command and subcommand
		{"npm install package", "npm run", false}, // different subcommand

		// Wildcard patterns
		{"git push origin", "git push *", true},
		{"git push", "git push *", true},
		{"npm run build", "npm run *", true},
		{"npm run test", "npm run *", true},
		{"npm run test --verbose", "npm run *", true},
		{"make build", "make *", true},
		{"make test", "make *", true},
	}

	for _, tt := range tests {
		commands, err := ParseCommands(tt.cmd)
		if err != nil {
			t.Errorf("ParseCommands(%q) error = %v", tt.cmd, err)
			continue
		}
		if len(commands) != 1 {
			t.Errorf("ParseCommands(%q) got %d commands, want 1", tt.cmd, len(commands))
			continue
		}

		got := commands[0].MatchPattern(tt.pattern)
		if got != tt.want {
			t.Errorf("Command(%q).MatchPattern(%q) = %v, want %v", tt.cmd, tt.pattern, got, tt.want)
		}
	}
}
