package openai

import (
	"regexp"
	"strings"
)

var (
	xmlPattern = regexp.MustCompile(`<tool_use>.*</tool_use>`)
)

func extractXMLStructures(input string) []string {
	input = strings.ReplaceAll(input, "\n", "")
	matches := xmlPattern.FindAllString(input, -1)

	var xmlStructures []string
	for _, match := range matches {
		xmlStructures = append(xmlStructures, match)
	}

	return xmlStructures
}
