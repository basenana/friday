package codebase

import (
	"fmt"
	"strings"
)

type CommandKind uint8

const (
	CommandStatus CommandKind = iota + 1
	CommandIndex
	CommandOff
)

type Command struct{ Kind CommandKind }

func ParseCommand(input string) (Command, bool, error) {
	fields := strings.Fields(input)
	if len(fields) == 0 || fields[0] != "/codebase" {
		return Command{}, false, nil
	}
	if len(fields) == 1 {
		return Command{Kind: CommandStatus}, true, nil
	}
	if len(fields) != 2 {
		return Command{}, true, fmt.Errorf("usage: /codebase [index|off]")
	}
	switch fields[1] {
	case "index":
		return Command{Kind: CommandIndex}, true, nil
	case "off":
		return Command{Kind: CommandOff}, true, nil
	default:
		return Command{}, true, fmt.Errorf("usage: /codebase [index|off]")
	}
}
