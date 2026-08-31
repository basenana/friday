package cards

// DiffCard shows a unified diff. Files lists the affected file paths;
// UnifiedDiff carries the raw `diff -u` style text.
type DiffCard struct {
	SchemaVersion int        `json:"schema_version,omitempty"`
	Title         string     `json:"title,omitempty"`
	Files         []string   `json:"files,omitempty"`
	UnifiedDiff   string     `json:"unified_diff,omitempty"`
	Source        *SourceRef `json:"source,omitempty"`
}

// CardKind implements Component.
func (DiffCard) CardKind() Kind { return KindDiff }

// PlanStep is one entry in a PlanCard.
type PlanStep struct {
	Title  string `json:"title"`
	Status string `json:"status,omitempty"` // e.g. "pending" | "running" | "done" | "failed"
	Detail string `json:"detail,omitempty"`
}

// PlanCard shows a multi-step plan (todo list).
type PlanCard struct {
	Title string     `json:"title,omitempty"`
	Steps []PlanStep `json:"steps"`
}

// CardKind implements Component.
func (PlanCard) CardKind() Kind { return KindPlan }
