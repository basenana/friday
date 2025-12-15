package documents

import (
	"context"

	"github.com/basenana/friday/providers"
	"github.com/basenana/friday/storehouse"
)

func Vectorization(ctx context.Context, chunks []*storehouse.Chunk, store storehouse.Sotrehouse, embedding providers.Embedding) error {
	for _, chunk := range chunks {
		result, err := embedding.Vectorization(ctx, chunk.Content)
		if err != nil {
			return err
		}
		chunk.Vector = result
	}

	_, err := store.Save(ctx, chunks...)
	if err != nil {
		return err
	}
	return nil
}
