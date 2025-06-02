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
	parts := strings.Split(input, "<tool_use>")

	var xmlStructures []string
	for _, part := range parts {
		part = "<tool_use>" + part
		matches := xmlPattern.FindAllString(part, -1)
		xmlStructures = append(xmlStructures, matches...)
	}
	return xmlStructures
}
