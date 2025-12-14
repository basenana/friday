package memory

import (
	"context"
	"fmt"
)

func remindMessage(nid string) string {
	return fmt.Sprintf("Retrieve from note with id: %s if needed", nid)
}

func memoryFromContext(ctx context.Context) *Memory {
	raw := ctx.Value("friday.ctx.memory")
	if raw == nil {
		return nil
	}
	m, ok := raw.(*Memory)
	if !ok {
		return nil
	}
	return m
}
