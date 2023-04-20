package embedding

type Embedding interface {
	VectorQuery(doc string) ([]float32, error)
	VectorDocs(docs []string) ([][]float32, error)
}
