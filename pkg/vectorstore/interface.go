package vectorstore

type VectorStore interface {
	Store(id, content string, metadata map[string]interface{}, vectors []float32) error
	Search(vectors []float32, k int) ([]Doc, error)
}
