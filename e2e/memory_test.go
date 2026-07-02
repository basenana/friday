//go:build e2e

package e2e

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/basenana/friday/core/types"
	"github.com/basenana/friday/memory"
	"github.com/basenana/friday/setup"
)

// TestMemory_DailyLogWrite verifies that MemorySystem.Write appends to the
// daily log file.
func TestMemory_DailyLogWrite(t *testing.T) {
	dir := t.TempDir()
	ms := memory.NewMemorySystem(dir, 7)
	if err := ms.EnsureDir(); err != nil {
		t.Fatal(err)
	}
	if err := ms.Write("test content line", memory.MemoryTypeDaily); err != nil {
		t.Fatalf("Write: %v", err)
	}

	// The today file should exist and contain the content.
	logs, err := ms.LoadRecentLogs()
	if err != nil {
		t.Fatalf("LoadRecentLogs: %v", err)
	}
	joined := strings.Join(logs, "\n")
	if !strings.Contains(joined, "test content line") {
		t.Errorf("expected daily log to contain 'test content line', got %q", truncate(joined, 300))
	}
}

// TestMemory_CuratedWrite verifies that curated memories go into MEMORY.md.
func TestMemory_CuratedWrite(t *testing.T) {
	dir := t.TempDir()
	ms := memory.NewMemorySystem(dir, 7)
	if err := ms.EnsureDir(); err != nil {
		t.Fatal(err)
	}
	if err := ms.Write("important curated fact", memory.MemoryTypeCurated); err != nil {
		t.Fatalf("Write: %v", err)
	}

	data, err := os.ReadFile(filepath.Join(dir, "MEMORY.md"))
	if err != nil {
		t.Fatalf("read MEMORY.md: %v", err)
	}
	if !strings.Contains(string(data), "important curated fact") {
		t.Errorf("MEMORY.md = %q, want it to contain 'important curated fact'", truncate(string(data), 300))
	}
}

// TestMemory_RecentLogs verifies that LoadRecentLogs returns logs from the
// past N days.
func TestMemory_RecentLogs(t *testing.T) {
	dir := t.TempDir()
	days := 3
	ms := memory.NewMemorySystem(dir, days)
	if err := ms.EnsureDir(); err != nil {
		t.Fatal(err)
	}

	// Write one for today.
	if err := ms.Write("today entry", memory.MemoryTypeDaily); err != nil {
		t.Fatal(err)
	}

	logs, err := ms.LoadRecentLogs()
	if err != nil {
		t.Fatalf("LoadRecentLogs: %v", err)
	}
	if len(logs) == 0 {
		t.Error("expected at least one log entry")
	}
}

// TestMemory_ForgettingEvaluation verifies the forgetting algorithm returns
// Forget=true for an old, low-usage memory.
func TestMemory_ForgettingEvaluation(t *testing.T) {
	fs := memory.ForgettingSystem{
		HalfLifeDays:      30,
		FrequencyWeight:   0.6,
		DeletionThreshold: 0.2,
		MaxUsageCount:     100,
	}
	old := &memory.Memory{
		ID:         "x",
		CreatedAt:  time.Now().AddDate(0, 0, -120),
		LastUsedAt: time.Now().AddDate(0, 0, -120),
		UsageCount: 1,
	}
	res := fs.Evaluate(old)
	if !res.Forget {
		t.Errorf("expected Forget=true for old low-usage memory, got %+v", res)
	}
}

// TestMemory_ForgettingRetention verifies a frequently-used memory is retained
// even when old.
func TestMemory_ForgettingRetention(t *testing.T) {
	fs := memory.ForgettingSystem{
		HalfLifeDays:      30,
		FrequencyWeight:   0.6,
		DeletionThreshold: 0.1,
		MaxUsageCount:     100,
	}
	m := &memory.Memory{
		ID:         "y",
		CreatedAt:  time.Now().AddDate(0, 0, -40),
		LastUsedAt: time.Now().AddDate(0, 0, -40),
		UsageCount: 50,
	}
	res := fs.Evaluate(m)
	if res.Forget {
		t.Errorf("expected Forget=false for high-frequency memory, got %+v", res)
	}
}

// TestMemory_ProcessorFlow exercises the Processor against a real LLM via an
// AgentContext.
func TestMemory_ProcessorFlow(t *testing.T) {
	cfg := loadConfig(t)
	fc := fridayConfig(t, cfg, "chat")
	dir := t.TempDir()
	fc.DataDir = dir
	fc.Workspace = filepath.Join(dir, "workspace")
	fc.Memory.Enabled = true
	mgr := newSessionManager(t, dir)
	mgr.SetLLM(newClient(t, cfg, "chat"))
	ac, err := setup.NewAgent(mgr, fc, setup.WithIsolate(true))
	if err != nil {
		t.Fatalf("setup.NewAgent: %v", err)
	}
	defer ac.Close()

	memoryPath := fc.MemoryPath()
	ms := memory.NewMemorySystem(memoryPath, 7)
	if err := ms.EnsureDir(); err != nil {
		t.Fatal(err)
	}

	proc := memory.NewProcessor(ac, memory.ProcessorConfig{
		MemoryPath:    memoryPath,
		WorkspacePath: fc.WorkspacePath(),
	})

	ctx, cancel := context.WithTimeout(context.Background(), cfg.TestTimeout())
	defer cancel()

	hist := &memory.SessionHistory{
		ID:        types.NewID(),
		CreatedAt: time.Now(),
		Messages: []types.Message{
			{Role: types.RoleUser, Content: "We discussed the deployment pipeline today."},
			{Role: types.RoleAssistant, Content: "Yes, the pipeline has three stages."},
		},
		MessageCount: 2,
	}
	out, err := proc.ProcessSession(ctx, hist)
	if err != nil {
		t.Fatalf("ProcessSession: %v", err)
	}
	if strings.TrimSpace(out) == "" {
		t.Error("expected non-empty processor output")
	}
}
