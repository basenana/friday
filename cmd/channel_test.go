package main

import (
	"testing"
)

func TestChannelFlagsDefault(t *testing.T) {
	if channelListen != "127.0.0.1:8999" {
		t.Errorf("expected default listen 127.0.0.1:8999, got %q", channelListen)
	}
	if channelPublicURL != "" {
		t.Errorf("expected default public-url empty, got %q", channelPublicURL)
	}
	if channelAuthToken != "" {
		t.Errorf("expected default auth-token empty, got %q", channelAuthToken)
	}
}

func TestChannelCommandRegistered(t *testing.T) {
	found := false
	for _, cmd := range rootCmd.Commands() {
		if cmd.Use == "channel" {
			found = true
			break
		}
	}
	if !found {
		t.Error("channel command not registered with root")
	}
}
