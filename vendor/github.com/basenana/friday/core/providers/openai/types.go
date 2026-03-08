package openai

import (
	"encoding/xml"
)

type Model struct {
	Name             string
	Temperature      *float64
	FrequencyPenalty *float64
	PresencePenalty  *float64
	StrictMode       bool
	QPM              int64
	Proxy            string
}

type ToolUse struct {
	XMLName   xml.Name `xml:"tool_use" json:"-"`
	ID        string   `xml:"id" json:"id"`
	Name      string   `xml:"name" json:"name"`
	Arguments string   `xml:"arguments" json:"arguments"`
	Error     string   `xml:"error" json:"error"`
	Reasoning string   `xml:"-" json:"-"`
}

type Reasoning struct {
	XMLName xml.Name `xml:"think"`
	Content string   `xml:",chardata"`
}
