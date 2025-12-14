package memory

import (
	"context"
	"fmt"
)

func remindMessage(key string) string {
	return fmt.Sprintf("Retrieve from %s if needed", key)
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
