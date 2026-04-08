package a2a

import (
	"runtime/debug"
	"strings"

	"github.com/a2aproject/a2a-go/a2a"
)

// Config holds A2A server configuration.
type Config struct {
	BaseURL string // Public URL for the agent card (e.g. "http://127.0.0.1:8999/")
	Listen  string // Listen address (e.g. "127.0.0.1:8999")
}

// moduleVersion returns the module version from build info, or "dev" if unavailable.
func moduleVersion() string {
	bi, ok := debug.ReadBuildInfo()
	if !ok {
		return "dev"
	}
	for _, dep := range bi.Deps {
		if strings.HasPrefix(dep.Path, "github.com/basenana/friday") {
			v := strings.TrimPrefix(dep.Version, "v")
			if v != "" {
				return v
			}
		}
	}
	// Fallback to main module version
	if bi.Main.Version != "" && bi.Main.Version != "(devel)" {
		return strings.TrimPrefix(bi.Main.Version, "v")
	}
	return "dev"
}

// NewAgentCard builds the Friday Agent Card from config.
func NewAgentCard(cfg Config) *a2a.AgentCard {
	return &a2a.AgentCard{
		Name:               "Friday",
		Description:        "Unix-style terminal AI agent exposed through A2A",
		URL:                cfg.BaseURL,
		Version:            moduleVersion(),
		ProtocolVersion:    string(a2a.Version),
		PreferredTransport: a2a.TransportProtocolJSONRPC,
		DefaultInputModes:  []string{"text/plain"},
		DefaultOutputModes: []string{"text/plain"},
		Capabilities: a2a.AgentCapabilities{
			Streaming: true,
		},
		Skills: []a2a.AgentSkill{
			{
				ID:          "chat",
				Name:        "Friday Chat",
				Description: "Interactive text chat backed by Friday sessions",
				Tags:        []string{"chat", "text"},
				Examples:    []string{"Hello!", "Explain this error message", "Help me write a function"},
			},
		},
	}
}
