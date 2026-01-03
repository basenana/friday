package chunks

import (
	"bytes"
	"fmt"

	"github.com/basenana/friday/types"
)

func MemoryToChunkCard(memory *types.Memory) *types.Chunk {
	return &types.Chunk{
		Type:     types.TypeMemory,
		Metadata: memory.Metadata,
		Content:  memoryChunkContent(memory),
	}
}

func memoryChunkContent(memory *types.Memory) string {
	buf := &bytes.Buffer{}
	buf.WriteString(fmt.Sprintf("CreatedAt: %s\n", memory.CreatedAt))
	buf.WriteString(fmt.Sprintf("Overview: %s\n", memory.Overview))
	buf.WriteString(fmt.Sprintf("Relevant: %s\n\n", memory.Relevant))

	buf.WriteString(memory.Details)
	if memory.Comment != "" {
		buf.WriteString("\n\n")
		buf.WriteString("---\n")
		buf.WriteString(fmt.Sprintf("Comment: %s\n", memory.Comment))
	}
	return buf.String()
}
