package models

type Element struct {
	Content  string   `json:"content"`
	Metadata Metadata `json:"metadata"`
}

type Metadata struct {
	Source   string `json:"source"`
	Title    string `json:"title"`
	Group    string `json:"group"`
	Category string `json:"category"`
}
