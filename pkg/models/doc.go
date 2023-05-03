package models

type Doc struct {
	Id       string
	Metadata map[string]interface{}
	Content  string
}
