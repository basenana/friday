package knowledge

import (
	"github.com/basenana/friday/core/tools"
)

type Option struct {
	SystemPrompt string
	Tools        []*tools.Tool
}
