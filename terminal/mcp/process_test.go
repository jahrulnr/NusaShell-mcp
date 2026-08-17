package main

import (
	"context"
	"runtime"
	"strings"
	"testing"
	"time"
)

func TestLongProcessSurvivesWaitTimeout(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("uses POSIX shell")
	}
	p, err := startProcess("sleep 2", "", "bash", nil)
	if err != nil {
		t.Fatal(err)
	}
	pm := NewProcessManager()
	pm.Add(p)

	if p.wait(context.Background(), 50*time.Millisecond) {
		t.Fatal("process should still be running")
	}
	if p.isExited() {
		t.Fatal("wait timeout must not terminate the process")
	}
	if err := p.kill(); err != nil {
		t.Fatal(err)
	}
	if !p.wait(context.Background(), time.Second) {
		t.Fatal("killed process did not exit")
	}
}

func TestLongProcessOutputCanBeReadAfterRequestTimeout(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("uses POSIX shell")
	}
	p, err := startProcess("printf 'ready'; sleep 1; printf ' done'", "", "bash", nil)
	if err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	if p.wait(ctx, 0) {
		t.Fatal("request timeout should happen before process completion")
	}
	if p.isExited() {
		t.Fatal("process must survive request timeout")
	}
	time.Sleep(1200 * time.Millisecond)
	stdout, _, _, exited, _, _ := p.snapshot(false)
	if !exited || !strings.Contains(stdout, "ready") || !strings.Contains(stdout, "done") {
		t.Fatalf("unexpected process state/output: exited=%v stdout=%q", exited, stdout)
	}
}

func TestProcessManagerConcurrentAccess(t *testing.T) {
	pm := NewProcessManager()
	p, err := startProcess("printf 'ok'", "", "bash", nil)
	if err != nil {
		t.Fatal(err)
	}
	pm.Add(p)

	done := make(chan struct{})
	go func() {
		defer close(done)
		for i := 0; i < 100; i++ {
			_, _ = pm.Get(p.ID)
			_ = pm.List()
		}
	}()
	_ = p.wait(context.Background(), time.Second)
	<-done
}

func TestFormatProcessTextIncludesOutputForForegroundWait(t *testing.T) {
	text := formatProcessText("proc-1", true, intPtr(0), false, "hello", "oops", true)
	if !strings.Contains(text, "stdout:\nhello") || !strings.Contains(text, "stderr:\noops") {
		t.Fatalf("foreground result omitted command output: %q", text)
	}
}

func TestFormatProcessTextOmitsOutputForBackgroundLaunch(t *testing.T) {
	text := formatProcessText("proc-1", false, nil, false, "background", "", false)
	if strings.Contains(text, "background") {
		t.Fatalf("background receipt unexpectedly included process output: %q", text)
	}
}

func intPtr(v int) *int {
	return &v
}
