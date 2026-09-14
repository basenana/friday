package main

import "testing"

func TestDaemonFlagsDefault(t *testing.T) {
	if daemonPort != 8999 {
		t.Fatalf("daemon port = %d, want 8999", daemonPort)
	}
}

func TestDaemonCommandReplacesChannel(t *testing.T) {
	var daemonFound, channelFound bool
	for _, command := range rootCmd.Commands() {
		switch command.Name() {
		case "daemon":
			daemonFound = true
		case "channel":
			channelFound = true
		}
	}
	if !daemonFound {
		t.Fatal("daemon command is not registered")
	}
	if channelFound {
		t.Fatal("obsolete channel command is still registered")
	}
}
