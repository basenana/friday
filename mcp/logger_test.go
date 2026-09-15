package mcp

import (
	"fmt"
	"strings"
	"testing"

	corelogger "github.com/basenana/friday/core/logger"
)

type mcpLogEntry struct {
	level   string
	name    string
	message string
	fields  []interface{}
}

type mcpLogCapture struct {
	corelogger.Logger
	entries *[]mcpLogEntry
	name    string
	fields  []interface{}
}

func (l *mcpLogCapture) Named(name string) corelogger.Logger {
	fullName := name
	if l.name != "" {
		fullName = l.name + "." + name
	}
	return &mcpLogCapture{entries: l.entries, name: fullName, fields: append([]interface{}(nil), l.fields...)}
}

func (l *mcpLogCapture) With(fields ...interface{}) corelogger.Logger {
	combined := append([]interface{}(nil), l.fields...)
	combined = append(combined, fields...)
	return &mcpLogCapture{entries: l.entries, name: l.name, fields: combined}
}

func (l *mcpLogCapture) Infof(format string, args ...interface{}) {
	l.append("info", format, args...)
}

func (l *mcpLogCapture) Errorf(format string, args ...interface{}) {
	l.append("error", format, args...)
}

func (l *mcpLogCapture) append(level, format string, args ...interface{}) {
	*l.entries = append(*l.entries, mcpLogEntry{
		level:   level,
		name:    l.name,
		message: fmt.Sprintf(format, args...),
		fields:  append([]interface{}(nil), l.fields...),
	})
}

func TestTransportLoggerIdentifiesMCPConnection(t *testing.T) {
	original := corelogger.Root()
	var entries []mcpLogEntry
	corelogger.SetRoot(&mcpLogCapture{entries: &entries})
	t.Cleanup(func() { corelogger.SetRoot(original) })

	logger := newTransportLogger("docs", TransportStreamableHTTP)
	logger.Infof("listening to %s", "server")
	logger.Errorf("connection failed: %s", "closed")

	if len(entries) != 2 {
		t.Fatalf("entries = %#v", entries)
	}
	if entries[0].level != "info" || entries[0].message != "listening to server" {
		t.Fatalf("info entry = %#v", entries[0])
	}
	if entries[1].level != "error" || entries[1].message != "connection failed: closed" {
		t.Fatalf("error entry = %#v", entries[1])
	}
	for _, entry := range entries {
		if entry.name != "mcp.transport" {
			t.Fatalf("logger name = %q", entry.name)
		}
		fields := fmt.Sprint(entry.fields)
		if !strings.Contains(fields, "server docs") || !strings.Contains(fields, "transport streamable-http") {
			t.Fatalf("MCP context fields = %#v", entry.fields)
		}
	}
}
