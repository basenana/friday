package cards

// FileCard points at a sandbox-resident file. The frontend loads the
// content lazily based on Path (e.g. by calling a sandbox read API).
type FileCard struct {
	Title string `json:"title,omitempty"`
	Path  string `json:"path"`
	Mime  string `json:"mime,omitempty"`
	Size  int64  `json:"size,omitempty"`
}

// CardKind implements Component.
func (FileCard) CardKind() Kind { return KindFile }
