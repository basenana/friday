package common

import (
	"context"
	"testing"
	"time"
)

func TestWaitBackoff_Normal(t *testing.T) {
	start := time.Now()
	if err := WaitBackoff(context.Background(), 20*time.Millisecond); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if elapsed := time.Since(start); elapsed < 15*time.Millisecond {
		t.Fatalf("did not appear to wait: %v", elapsed)
	}
}

func TestWaitBackoff_Cancelled(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := WaitBackoff(ctx, 5*time.Second); err == nil {
		t.Fatalf("expected ctx.Err() to be returned")
	}
}

func TestWaitBackoff_ZeroDelay(t *testing.T) {
	if err := WaitBackoff(context.Background(), 0); err != nil {
		t.Fatalf("zero delay should return immediately, got %v", err)
	}
}

func TestWaitBackoff_NegativeDelay(t *testing.T) {
	if err := WaitBackoff(context.Background(), -5*time.Second); err != nil {
		t.Fatalf("negative delay should return immediately, got %v", err)
	}
}
