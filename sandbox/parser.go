package sandbox

import (
	"strings"

	"mvdan.cc/sh/v3/syntax"
)

// Command represents a parsed shell command
type Command struct {
	Text   string   // Full command text "git push origin main"
	Name   string   // Command name "git"
	Subcmd string   // Subcommand "push" (if exists)
	Args   []string // All arguments ["git", "push", "origin", "main"]
}

// ParseCommands parses a shell command string and extracts all individual commands
// It handles compound commands like "A && B", "A || B", "A; B", "A | B"
func ParseCommands(cmd string) ([]Command, error) {
	if strings.TrimSpace(cmd) == "" {
		return nil, nil
	}

	parser := syntax.NewParser()

	// Parse as a complete program
	prog, err := parser.Parse(strings.NewReader(cmd), "")
	if err != nil {
		return nil, err
	}

	var commands []Command

	// Walk the AST to extract all call expressions
	syntax.Walk(prog, func(node syntax.Node) bool {
		switch n := node.(type) {
		case *syntax.CallExpr:
			cmd := extractCallCommand(n)
			if cmd.Name != "" {
				commands = append(commands, cmd)
			}
		}
		return true
	})

	return commands, nil
}

// extractCallCommand extracts a Command from a CallExpr node
func extractCallCommand(node *syntax.CallExpr) Command {
	var args []string

	for _, word := range node.Args {
		arg := extractWord(word)
		args = append(args, arg)
	}

	cmd := Command{
		Text: strings.Join(args, " "),
		Args: args,
	}

	if len(args) > 0 {
		cmd.Name = args[0]
	}
	if len(args) > 1 {
		cmd.Subcmd = args[1]
	}

	return cmd
}

// extractWord extracts the string value from a Word node
func extractWord(word *syntax.Word) string {
	var sb strings.Builder

	for _, part := range word.Parts {
		switch p := part.(type) {
		case *syntax.Lit:
			sb.WriteString(p.Value)
		case *syntax.SglQuoted:
			sb.WriteString(p.Value)
		case *syntax.DblQuoted:
			// Recursively extract quoted content
			for _, qpart := range p.Parts {
				if lit, ok := qpart.(*syntax.Lit); ok {
					sb.WriteString(lit.Value)
				}
			}
		case *syntax.ParamExp:
			// Handle variable expansion like $VAR, ${VAR}
			// Param is a *Lit in newer versions
			sb.WriteString("$")
			sb.WriteString(p.Param.Value)
		}
	}

	return sb.String()
}

// MatchPattern checks if a command matches a pattern
// Pattern formats:
//   - "git"          -> matches all git commands
//   - "git push"     -> matches git push (exact subcommand)
//   - "git push *"   -> matches git push with any arguments
//   - "npm run *"    -> matches npm run with any arguments
//   - "docker run * --privileged"  -> matches with wildcards in middle
func (c Command) MatchPattern(pattern string) bool {
	parts := strings.Fields(pattern)
	if len(parts) == 0 {
		return false
	}

	// Case 1: Only command name "git", "npm"
	// Special case: single "*" matches anything
	if len(parts) == 1 && parts[0] == "*" {
		return true
	}
	if len(parts) == 1 {
		return c.Name == parts[0]
	}

	// Case 2: Command + subcommand "git push", "npm run"
	if len(parts) == 2 {
		// If second part is wildcard, just check command name
		if parts[1] == "*" {
			return c.Name == parts[0]
		}
		return c.Name == parts[0] && c.Subcmd == parts[1]
	}

	// Case 3: Pattern with wildcards "git push *", "docker run * ubuntu *"
	if len(c.Args) < len(parts) {
		// Command has fewer parts than pattern, but wildcard can match empty
		// Check each part
		for i, p := range parts {
			if p == "*" {
				continue // Wildcard matches anything (including nothing)
			}
			if i >= len(c.Args) {
				return false
			}
			if c.Args[i] != p {
				return false
			}
		}
		return true
	}

	// Command has equal or more parts than pattern
	for i, p := range parts {
		if p == "*" {
			continue // Wildcard matches any single arg
		}
		if i >= len(c.Args) || c.Args[i] != p {
			return false
		}
	}

	return true
}

// String returns the full command text
func (c Command) String() string {
	return c.Text
}
