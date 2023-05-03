package embedding

type Embedding interface {
	VectorQuery(doc string) ([]float32, map[string]interface{}, error)
	VectorDocs(docs []string) ([][]float32, []map[string]interface{}, error)
}
