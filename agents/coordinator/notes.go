package coordinator

type Note struct {
	Planning *Planning
}

func newEmptyNotebook() *Note {
	return &Note{}
}

type Planning struct {
}
