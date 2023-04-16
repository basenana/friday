package vectorstore

type VectorStore interface {
	EmbeddingDoc(id, content string, vectors []float32)
	Search(vectors []float32, k int) ([]string, error)
}
