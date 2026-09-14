package tui

import (
	"time"
	"unicode/utf8"

	"github.com/basenana/friday/core/actor/events"
	corelogger "github.com/basenana/friday/core/logger"
)

const maxTUILogTextRunes = 2048

func tuiLogger() corelogger.Logger {
	return corelogger.New("tui")
}

func (m *model) logFields(fields ...interface{}) []interface{} {
	base := make([]interface{}, 0, len(fields)+4)
	if m != nil && m.sessionID != "" {
		base = append(base, "session_id", m.sessionID)
	}
	if m != nil && m.currentRunID != "" {
		base = append(base, "current_run_id", m.currentRunID)
	}
	return append(base, fields...)
}

func (m *model) logInfo(message string, fields ...interface{}) {
	tuiLogger().Infow(message, m.logFields(fields...)...)
}

func (m *model) logWarn(message string, fields ...interface{}) {
	tuiLogger().Warnw(message, m.logFields(fields...)...)
}

func (m *model) logError(message string, err error, fields ...interface{}) {
	if err == nil {
		return
	}
	fields = append(fields, "error", boundedTUILogText(err.Error()))
	tuiLogger().Errorw(message, m.logFields(fields...)...)
}

func (m *model) decodeEventPayload(evt events.Event, target any) bool {
	if err := events.DecodePayload(evt, target); err != nil {
		m.logWarn("failed to decode actor event payload",
			"event_type", evt.Type,
			"event_name", evt.Name,
			"run_id", evt.RunID,
			"replaying", m.replaying,
			"error", boundedTUILogText(err.Error()),
		)
		return false
	}
	return true
}

func boundedTUILogText(value string) string {
	if utf8.RuneCountInString(value) <= maxTUILogTextRunes {
		return value
	}
	runes := []rune(value)
	return string(runes[:maxTUILogTextRunes]) + "…"
}

func elapsedMilliseconds(started time.Time) int64 {
	return time.Since(started).Milliseconds()
}
