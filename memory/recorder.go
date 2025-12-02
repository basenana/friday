package memory

import (
	"github.com/basenana/friday/types"
)

type Recorder interface {
	Record(message types.Message)
}
