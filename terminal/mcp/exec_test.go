package main

import (
	"context"
	"testing"
	"time"
)

// TestRunExecTimeout verifies that a long-running command without an explicit
// timeout is killed once the bounded timeout elapses and timedOut is reported.
// The default (5m) real wall-clock bound is impractical to test; we exercise
// the same code path with a small explicit timeout.
func TestRunExecTimeout(t *testing.T) {
	ctx := context.Background()
	start := time.Now()
	res, err := RunExecWithContext(ctx, "sleep 30", "", "auto", 800)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !res.TimedOut {
		t.Fatalf("expected timedOut=true for a 30s command with an 800ms timeout (took %v)", time.Since(start))
	}
	if time.Since(start) > 10*time.Second {
		t.Fatalf("timed-out exec took too long: %v", time.Since(start))
	}
}

// TestRunExecFast verifies non-timeout commands still complete normally.
func TestRunExecFast(t *testing.T) {
	res, err := RunExecWithContext(context.Background(), "printf 'ping'", "", "auto", 5000)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if res.TimedOut {
		t.Fatal("fast command must not be reported as timed out")
	}
	if res.Stdout != "ping" {
		t.Fatalf("unexpected stdout: %q", res.Stdout)
	}
}