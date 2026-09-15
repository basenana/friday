package mcp

import (
	corelogger "github.com/basenana/friday/core/logger"
	mcpgo "github.com/mark3labs/mcp-go/util"
)

// transportLogger routes mcp-go transport logs through Friday's logger. This
// keeps transport diagnostics out of stdout/stderr (and therefore the TUI)
// while retaining enough context to identify the MCP connection involved.
type transportLogger struct {
	logger corelogger.Logger
}

var _ mcpgo.Logger = (*transportLogger)(nil)

func newTransportLogger(server string, transport TransportType) *transportLogger {
	if server == "" {
		server = "legacy"
	}
	return &transportLogger{
		logger: corelogger.New("mcp.transport").With(
			"server", server,
			"transport", transport,
		),
	}
}

func (l *transportLogger) Infof(format string, args ...any) {
	l.logger.Infof(format, args...)
}

func (l *transportLogger) Errorf(format string, args ...any) {
	l.logger.Errorf(format, args...)
}
