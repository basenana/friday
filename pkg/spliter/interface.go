package spliter

import "friday/pkg/models"

type Spliter interface {
	Split(text string) []string
	Merge(elements []models.Element) []models.Element
}
