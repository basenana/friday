package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestValidateTimeouts(t *testing.T) {
	tests := []struct {
		name    string
		mutate  func(*Config)
		wantErr string
	}{
		{
			name: "model timeout valid",
			mutate: func(c *Config) {
				c.Model.Timeout = "2h"
			},
		},
		{
			name: "models entry timeout valid",
			mutate: func(c *Config) {
				c.Models = []ModelConfig{{Model: "m1", Timeout: "30m"}}
			},
		},
		{
			name: "image model timeout valid",
			mutate: func(c *Config) {
				c.ImageModel.Timeout = "90s"
			},
		},
		{
			name: "decision model timeout valid",
			mutate: func(c *Config) {
				c.DecisionModel.Timeout = "45m"
			},
		},
		{
			name: "agent turn timeout valid",
			mutate: func(c *Config) {
				c.Agent.TurnTimeout = "4h"
			},
		},
		{
			name: "model timeout unparseable",
			mutate: func(c *Config) {
				c.Model.Timeout = "not-a-duration"
			},
			wantErr: `invalid model.timeout "not-a-duration"`,
		},
		{
			name: "models entry timeout unparseable",
			mutate: func(c *Config) {
				c.Models = []ModelConfig{{Model: "slow-model", Timeout: "abc"}}
			},
			wantErr: `invalid models[slow-model].timeout "abc"`,
		},
		{
			name: "image model timeout unparseable",
			mutate: func(c *Config) {
				c.ImageModel.Timeout = "soon"
			},
			wantErr: `invalid image_model.timeout "soon"`,
		},
		{
			name: "decision model timeout unparseable",
			mutate: func(c *Config) {
				c.DecisionModel.Timeout = "later"
			},
			wantErr: `invalid decision_model.timeout "later"`,
		},
		{
			name: "agent turn timeout unparseable",
			mutate: func(c *Config) {
				c.Agent.TurnTimeout = "forever"
			},
			wantErr: `invalid agent.turn_timeout "forever"`,
		},
		{
			name: "model timeout zero",
			mutate: func(c *Config) {
				c.Model.Timeout = "0s"
			},
			wantErr: `must be positive`,
		},
		{
			name: "agent turn timeout negative",
			mutate: func(c *Config) {
				c.Agent.TurnTimeout = "-5m"
			},
			wantErr: `must be positive`,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := DefaultConfig()
			tt.mutate(cfg)
			err := cfg.validate()
			if tt.wantErr == "" {
				if err != nil {
					t.Fatalf("validate() error = %v", err)
				}
				return
			}
			if err == nil {
				t.Fatal("validate() accepted invalid timeout")
			}
			if !strings.Contains(err.Error(), tt.wantErr) {
				t.Fatalf("validate() error = %v, want containing %q", err, tt.wantErr)
			}
		})
	}
}

func TestTimeoutDuration(t *testing.T) {
	if got := (ModelConfig{Timeout: "2h"}).TimeoutDuration(); got != 2*time.Hour {
		t.Fatalf("TimeoutDuration() = %v, want 2h", got)
	}
	if got := (ModelConfig{Timeout: ""}).TimeoutDuration(); got != 0 {
		t.Fatalf("empty TimeoutDuration() = %v, want 0", got)
	}
	if got := (ModelConfig{Timeout: "bogus"}).TimeoutDuration(); got != 0 {
		t.Fatalf("unparseable TimeoutDuration() = %v, want 0", got)
	}
	if got := (DecisionModelConfig{Timeout: "30m"}).TimeoutDuration(); got != 30*time.Minute {
		t.Fatalf("decision TimeoutDuration() = %v, want 30m", got)
	}
	if got := (DecisionModelConfig{Timeout: ""}).TimeoutDuration(); got != 0 {
		t.Fatalf("empty decision TimeoutDuration() = %v, want 0", got)
	}
}

func TestAgentTurnTimeoutDuration(t *testing.T) {
	cfg := DefaultConfig()
	if got := cfg.Agent.TurnTimeoutDuration(); got != 0 {
		t.Fatalf("default TurnTimeoutDuration() = %v, want 0", got)
	}
	cfg.Agent.TurnTimeout = "1h"
	if got := cfg.Agent.TurnTimeoutDuration(); got != time.Hour {
		t.Fatalf("TurnTimeoutDuration() = %v, want 1h", got)
	}
}

func TestLoadTimeoutsFromJSON(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.json")
	writeTestConfig(t, path, `{
  "model": {"model": "slow-local", "timeout": "45m"},
  "agent": {"turn_timeout": "4h"}
}`)
	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if got := cfg.Model.TimeoutDuration(); got != 45*time.Minute {
		t.Fatalf("model timeout = %v, want 45m", got)
	}
	if got := cfg.Agent.TurnTimeoutDuration(); got != 4*time.Hour {
		t.Fatalf("agent turn timeout = %v, want 4h", got)
	}

	writeTestConfig(t, path, `{"model": {"model": "slow-local"}, "agent": {"turn_timeout": "abc"}}`)
	if _, err := Load(path); err == nil || !strings.Contains(err.Error(), "agent.turn_timeout") {
		t.Fatalf("Load() err = %v, want agent.turn_timeout failure", err)
	}

	writeTestConfig(t, path, `{"model": {"model": "m"}}`)
	cfg, err = Load(path)
	if err != nil {
		t.Fatalf("Load() without timeout blocks error = %v", err)
	}
	if cfg.Agent.TurnTimeout != "" || cfg.Model.Timeout != "" {
		t.Fatal("absent timeout blocks should stay empty")
	}
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
}
