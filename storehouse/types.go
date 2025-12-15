package storehouse

type Chunk struct {
	ID       string            `json:"id"`
	Type     string            `json:"type"`
	Metadata map[string]string `json:"metadata"`
	Content  string            `json:"content"`

	Vector []float64 `json:"-"`
}

const (
	TypeAll      string = ""
	TypeDocument string = "document" // deadly right things
	TypeReview   string = "review"   // learned from final report/result
	TypeMemory   string = "memory"   // learned from conversation
)
