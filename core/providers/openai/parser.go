package openai

import (
	"encoding/xml"
	"fmt"
	"strings"

	"github.com/basenana/friday/core/providers"
	"github.com/basenana/friday/core/providers/common"
)

var thinkingTags = []struct {
	open  string
	close string
}{
	{open: "<think>", close: "</think>"},
	{open: "<thinking>", close: "</thinking>"},
}

// thinkingStreamParser incrementally strips <think>/<thinking> tags from
// streamed content: text inside the tags is emitted as Reasoning deltas and
// everything else as Content deltas. Partial tag prefixes that straddle chunk
// boundaries are buffered until they can be resolved.
type thinkingStreamParser struct {
	pending  string
	openTag  string
	closeTag string
}

func newThinkingStreamParser() *thinkingStreamParser {
	return &thinkingStreamParser{}
}

func (p *thinkingStreamParser) write(s string) []providers.Delta {
	p.pending += s
	var result []providers.Delta

	for p.pending != "" {
		if p.closeTag != "" {
			end := strings.Index(p.pending, p.closeTag)
			if end >= 0 {
				if end > 0 {
					result = append(result, providers.Delta{Reasoning: p.pending[:end]})
				}
				p.pending = p.pending[end+len(p.closeTag):]
				p.openTag = ""
				p.closeTag = ""
				continue
			}
			// Inside a thinking section with no closing tag yet: emit the
			// reasoning text as it arrives, retaining only a suffix that
			// could be the prefix of a closing tag split across chunks.
			keep := thinkingCloseTagPrefixSuffixLength(p.closeTag, p.pending)
			if emit := len(p.pending) - keep; emit > 0 {
				result = append(result, providers.Delta{Reasoning: p.pending[:emit]})
				p.pending = p.pending[emit:]
			}
			break
		}

		start, tag := findThinkingTag(p.pending)
		if start >= 0 {
			if start > 0 {
				result = append(result, providers.Delta{Content: p.pending[:start]})
			}
			p.pending = p.pending[start+len(tag.open):]
			p.openTag = tag.open
			p.closeTag = tag.close
			continue
		}

		keep := thinkingTagPrefixSuffixLength(p.pending)
		if emit := len(p.pending) - keep; emit > 0 {
			result = append(result, providers.Delta{Content: p.pending[:emit]})
			p.pending = p.pending[emit:]
		}
		break
	}

	return result
}

// flush returns buffered text as deltas. Buffering outside a thinking section
// only holds back a partial opening tag, which is restored verbatim as
// Content; buffering inside an unclosed thinking section is emitted as
// Reasoning. Callers must skip flush entirely when the stream failed, so
// truncated reasoning is not re-emitted as Content.
func (p *thinkingStreamParser) flush() []providers.Delta {
	if p.pending == "" && p.openTag == "" {
		return nil
	}
	content := p.pending
	if p.openTag != "" {
		p.pending = ""
		return []providers.Delta{{Reasoning: content}}
	}
	p.pending = ""
	return []providers.Delta{{Content: content}}
}

func findThinkingTag(s string) (int, struct {
	open  string
	close string
}) {
	index := -1
	var found struct {
		open  string
		close string
	}
	for _, tag := range thinkingTags {
		if candidate := strings.Index(s, tag.open); candidate >= 0 && (index < 0 || candidate < index) {
			index = candidate
			found = tag
		}
	}
	return index, found
}

func thinkingTagPrefixSuffixLength(s string) int {
	for length := min(len(s), len("<thinking>")-1); length > 0; length-- {
		suffix := s[len(s)-length:]
		for _, tag := range thinkingTags {
			if strings.HasPrefix(tag.open, suffix) {
				return length
			}
		}
	}
	return 0
}

// thinkingCloseTagPrefixSuffixLength returns how many trailing bytes of s to
// retain because they could be the beginning of closeTag split across chunks.
func thinkingCloseTagPrefixSuffixLength(closeTag, s string) int {
	for length := min(len(s), len(closeTag)-1); length > 0; length-- {
		if strings.HasPrefix(closeTag, s[len(s)-length:]) {
			return length
		}
	}
	return 0
}

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
		}
		if normalized, errMsg, ok := common.NormalizeToolUseArguments(use.Arguments, use.Name); !ok {
			use.Arguments = normalized
			if use.Error == "" {
				use.Error = errMsg
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

type compatibleResponse struct {
	*providers.CommonResponse
	buf     *xmlParser
	emitted bool
}

func (r *compatibleResponse) nextChoice(chunk interface{}) {}

func (r *compatibleResponse) updateUsage(chunk interface{}) {}

func (r *compatibleResponse) fail(err error) {
	r.Err <- err
}

func (r *compatibleResponse) close() {
	msgList := r.buf.flush()
	for _, msg := range msgList {
		r.emit(msg)
	}
	close(r.Stream)
	close(r.Err)
}

func (r *compatibleResponse) emit(delta providers.Delta) {
	r.emitted = true
	r.Stream <- delta
}

func newCompatibleResponse() *compatibleResponse {
	return &compatibleResponse{CommonResponse: providers.NewCommonResponse(), buf: newXmlParser()}
}

var _ providers.Response = (*compatibleResponse)(nil)
