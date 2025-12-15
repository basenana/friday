package knowledge

import "github.com/basenana/friday/tools"

type Option struct {
	SystemPrompt string
	Tools        []*tools.Tool
}
