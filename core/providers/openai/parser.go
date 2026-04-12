package openai

import (
	"encoding/json"
	"encoding/xml"
	"fmt"
	"strings"

	"github.com/basenana/friday/core/providers"
)

type xmlParser struct {
	buf *xmlBuffer
}

func newXmlParser() *xmlParser {
	return &xmlParser{}
}

func (p *xmlParser) write(s string) []providers.Delta {
	var (
		result  []providers.Delta
		content string
	)
	for _, r := range s {
		if p.buf != nil {
			if msg, closed := p.buf.write(r); msg != nil || closed {
				if closed {
					p.buf = nil
				}
				if msg != nil {
					result = append(result, *msg)
				}
			}
			continue
		}

		if r == '<' {
			// before xml block content
			if content != "" {
				result = append(result, providers.Delta{Content: content})
				content = ""
			}

			p.buf = &xmlBuffer{}
			p.buf.write('<')
			continue
		}

		content += string(r)
	}

	if content != "" {
		result = append(result, providers.Delta{Content: content})
	}

	if len(result) > 5 {
		return compactMessages(result)
	}

	return result
}

func (p *xmlParser) flush() []providers.Delta {
	if p.buf != nil {
		d := p.buf.flush()
		if d != nil {
			return []providers.Delta{*d}
		}
	}
	return nil
}

type xmlBuffer struct {
	start   bool
	end     bool
	bracket int
	deep    int

	content  []rune
	thinking bool
}

func (l *xmlBuffer) write(r rune) (*providers.Delta, bool) {

	l.content = append(l.content, r)

	// <k>v</k>
	switch r {
	case '<':
		l.bracket += 1
		l.start = true
	case '/':
		if l.start {
			l.end = true
		}
		l.start = false
	case '>':
		l.bracket -= 1
		l.start = false
		if l.end { // closing tag
			l.end = false
			l.deep -= 1
		} else {
			if l.bracket == 0 { // new tag
				l.deep += 1

				if l.deep == 1 && (string(l.content) == "<think>" || string(l.content) == "<thinking>") {
					l.thinking = true
					l.content = l.content[:0]
				}
			}
		}

	default:
		l.start = false
		if l.thinking && l.bracket == 0 {
			return &providers.Delta{Reasoning: string(r)}, false
		}
	}

	// normal close
	if l.deep == 0 && l.bracket == 0 {
		return l.flush(), true
	}

	if l.deep < 0 {
		return l.flush(), true
	}
	return nil, false
}

func (l *xmlBuffer) flush() *providers.Delta {
	if l.thinking {
		return nil
	}

	content := strings.TrimSpace(string(l.content))
	if content == "" {
		return nil
	}
	return xmlBodyToMessage(content)
}

func xmlBodyToMessage(body string) *providers.Delta {
	switch {
	case strings.HasPrefix(body, "<ToolUse>"):
		body = strings.ReplaceAll(body, "<ToolUse>", "<tool_use>")
		body = strings.ReplaceAll(body, "</ToolUse>", "</tool_use>")
		fallthrough
	case strings.Contains(body, "<tool_use>"):
		use := ToolUse{}
		err := xml.Unmarshal([]byte(body), &use)
		if err != nil && (use.Name == "" || use.Arguments == "") {
			use.Error = fmt.Sprintf("The tool %s is used in an incorrect format; please try using the tool again", use.Name)
		} else {
			argBody := make(map[string]interface{})
			if err = json.Unmarshal([]byte(use.Arguments), &argBody); err != nil {
				use.Error = fmt.Sprintf("The arguments passed to the tool %s is not a valid JSON.", use.Name)
			}
		}

		return &providers.Delta{ToolUse: []providers.ToolCall{{
			ID:        use.ID,
			Name:      use.Name,
			Arguments: use.Arguments,
			Error:     use.Error,
		}}}
	case strings.Contains(body, "<thinking>"):
		body = strings.ReplaceAll(body, "<thinking>", "<think>")
		body = strings.ReplaceAll(body, "</thinking>", "</think>")
		fallthrough
	case strings.Contains(body, "<think>"):
		r := Reasoning{}
		err := xml.Unmarshal([]byte(body), &r)
		if err == nil && r.Content != "" {
			return &providers.Delta{Reasoning: r.Content}
		}
		return &providers.Delta{Reasoning: body}
	}
	return &providers.Delta{Content: body}
}

func compactMessages(messages []providers.Delta) []providers.Delta {
	var (
		reasoning string
		content   string
		result    []providers.Delta
	)

	for i, d := range messages {

		switch {
		case d.Content != "":

			if reasoning != "" {
				result = append(result, providers.Delta{Reasoning: reasoning})
				reasoning = ""
			}

			content += d.Content

		case d.Reasoning != "":

			if content != "" {
				result = append(result, providers.Delta{Content: content})
				content = ""
			}

			reasoning += d.Reasoning

		default:

			if reasoning != "" {
				result = append(result, providers.Delta{Reasoning: reasoning})
				reasoning = ""
			}

			if content != "" {
				result = append(result, providers.Delta{Content: content})
				content = ""
			}

			result = append(result, messages[i])

		}
	}

	if reasoning != "" {
		result = append(result, providers.Delta{Reasoning: reasoning})
	}
	if content != "" {
		result = append(result, providers.Delta{Content: content})
	}

	return result
}

func isTooManyError(err error) bool {
	return strings.Contains(err.Error(), "429 Too Many Requests")
}

type compatibleResponse struct {
	*providers.CommonResponse
	buf *xmlParser
}

func (r *compatibleResponse) nextChoice(chunk interface{}) {}

func (r *compatibleResponse) updateUsage(chunk interface{}) {}

func (r *compatibleResponse) fail(err error) {
	r.Err <- err
}

func (r *compatibleResponse) close() {
	msgList := r.buf.flush()
	for _, msg := range msgList {
		r.Stream <- msg
	}
	close(r.Stream)
	close(r.Err)
}

func newCompatibleResponse() *compatibleResponse {
	return &compatibleResponse{CommonResponse: providers.NewCommonResponse(), buf: newXmlParser()}
}

var _ providers.Response = (*compatibleResponse)(nil)
