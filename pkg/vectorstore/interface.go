package vectorstore

import "friday/pkg/models"

type VectorStore interface {
	Store(id, content string, metadata map[string]interface{}, vectors []float32) error
	Search(vectors []float32, k int) ([]models.Doc, error)
	Exist(id string) bool
}
