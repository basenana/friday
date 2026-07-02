package actor

import "strings"

// Message is a single unit delivered to an Actor's inbox.
type Message struct {
	ID        string            // caller-side identifier (A2A taskID, ACP requestID, ...)
	Content   string            // user text
	ImageURLs []string          // image references
	Metadata  map[string]string // protocol-specific metadata; actor does not interpret
}

// MessageFromText is a convenience constructor for a text-only Message.
func MessageFromText(content string) Message {
	return Message{Content: content}
}

// MergeMessages combines one or more inbox messages into a single prompt and
// deduplicated image list.
//
//   - One message  → returned as-is (slice header copied for safety).
//   - Many messages → contents joined with "\n---\n"; empty contents skipped.
//
// Metadata is not merged; the first message's Metadata is the authoritative
// source for the run.
func MergeMessages(msgs []Message) (prompt string, imageURLs []string) {
	if len(msgs) == 0 {
		return "", nil
	}
	if len(msgs) == 1 {
		return msgs[0].Content, append([]string(nil), msgs[0].ImageURLs...)
	}

	parts := make([]string, 0, len(msgs))
	seen := make(map[string]struct{}, len(msgs))
	for _, m := range msgs {
		if m.Content != "" {
			parts = append(parts, m.Content)
		}
		for _, url := range m.ImageURLs {
			if url == "" {
				continue
			}
			if _, ok := seen[url]; ok {
				continue
			}
			seen[url] = struct{}{}
			imageURLs = append(imageURLs, url)
		}
	}
	prompt = strings.Join(parts, "\n---\n")
	return
}
