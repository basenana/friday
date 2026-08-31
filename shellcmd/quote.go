// Package shellcmd provides shell quoting and command building utilities.
// Extracted from sandbox so other packages (env) can build safe shell
// command strings without creating an import cycle.
package shellcmd

import (
	"strings"

	"mvdan.cc/sh/v3/syntax"
)

// QuoteArg quotes a single shell argument using bash quoting rules.
func QuoteArg(arg string) string {
	quoted, err := syntax.Quote(arg, syntax.LangBash)
	if err == nil {
		return quoted
	}
	return "'" + strings.ReplaceAll(arg, "'", "'\\''") + "'"
}

// Join builds a single shell command string from a binary path and a list
// of arguments, with each part bash-quoted.
func Join(command string, args ...string) string {
	quoted := make([]string, 0, len(args)+1)
	quoted = append(quoted, QuoteArg(command))
	for _, arg := range args {
		quoted = append(quoted, QuoteArg(arg))
	}
	return strings.Join(quoted, " ")
}
