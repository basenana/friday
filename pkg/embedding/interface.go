package embedding

type Embedding interface {
	Vector(doc string) ([]float32, error)
}
