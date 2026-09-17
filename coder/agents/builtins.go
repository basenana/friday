package agents

// RegisterBuiltins registers the built-in read-only explorer. Runtime model
// policy is inherited from the Session unless an Agent spec overrides it.
func RegisterBuiltins(reg *Registry) {
	if reg == nil {
		return
	}
	reg.Register(ExplorerSpec())
}
