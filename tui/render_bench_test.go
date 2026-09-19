package tui

import (
	"fmt"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/basenana/friday/core/actor/events"
)

// buildLongTranscript fills the model with a realistic mix of transcript
// blocks. Renderers see worst-case content sizes (long unbroken output).
func buildLongTranscript(m *model, blocks int) {
	for i := 0; i < blocks; i++ {
		switch i % 5 {
		case 0:
			m.appendBlock(chatBlock{kind: blockUser, content: strings.Repeat("user message text ", 40)})
		case 1:
			m.appendBlock(chatBlock{kind: blockAssistant, content: strings.Repeat("assistant **markdown** reply ", 60)})
		case 2:
			m.appendBlock(chatBlock{kind: blockReasoning, content: strings.Repeat("reasoning trace ", 80)})
		case 3:
			m.appendBlock(chatBlock{kind: blockToolCall, toolName: "bash",
				toolArgs: `{"command":"ls -la"}`, toolOutput: strings.Repeat("file output line\n", 30),
				toolArgsComplete: true, success: true})
		case 4:
			m.appendBlock(chatBlock{kind: blockDivider, content: "run finished"})
		}
	}
}

func makeTextDeltaBatch(n int) []events.Event {
	batch := make([]events.Event, 0, n)
	for i := 0; i < n; i++ {
		batch = append(batch, events.NewEvent(events.KindTextMessageContent, "run").
			WithMessageID("message").
			WithPayload(events.TextMessageContentData{Content: fmt.Sprintf("delta %d ", i)}))
	}
	return batch
}

// BenchmarkViewIdleLongTranscript measures the cost of a presentation-only
// frame (cursor blink, spinner tick, mouse scroll) with a long transcript.
func BenchmarkViewIdleLongTranscript(b *testing.B) {
	m, _, _ := newTestModel(b)
	_, _ = m.Update(tea.WindowSizeMsg{Width: 120, Height: 40})
	buildLongTranscript(m, 300)
	_ = m.View().Content // warm caches
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = m.View().Content
	}
}

// BenchmarkViewStreamingAppend measures the cost of a frame right after a
// streamed delta lands, the hottest path while an agent is replying.
func BenchmarkViewStreamingAppend(b *testing.B) {
	m, _, _ := newTestModel(b)
	_, _ = m.Update(tea.WindowSizeMsg{Width: 120, Height: 40})
	buildLongTranscript(m, 300)
	_ = m.View().Content
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		m.appendStreamContent(blockAssistant, "more streamed delta text ")
		_ = m.View().Content
	}
}

func BenchmarkMergeAdjacentStreamEvents(b *testing.B) {
	batch := makeTextDeltaBatch(200)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = mergeAdjacentStreamEvents(batch)
	}
}

func BenchmarkRenderStatus(b *testing.B) {
	m, _, _ := newTestModel(b)
	_, _ = m.Update(tea.WindowSizeMsg{Width: 120, Height: 40})
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = m.renderStatus()
	}
}
