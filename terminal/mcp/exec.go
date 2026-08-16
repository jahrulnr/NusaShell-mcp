package main

import (
	"bytes"
	"context"
	"os/exec"
	"strings"
	"time"
)

// ExecResult is the result of a one-shot command execution.
type ExecResult struct {
	Stdout     string    `json:"stdout"`
	Stderr     string    `json:"stderr"`
	ExitCode   *int      `json:"exitCode"`
	Signal     string    `json:"signal,omitempty"`
	TimedOut   bool      `json:"timedOut"`
	Truncated  bool      `json:"truncated"`
	Cwd        string    `json:"cwd"`
	Shell      string    `json:"shell"`
	ShellKind  ShellKind `json:"shellKind"`
	DurationMs int64     `json:"durationMs"`
}

// RunExec runs a one-shot shell command. If timeoutMs is <= 0 a safe default
// (5 minutes) is applied so a runaway command cannot hold the MCP request
// forever. Use RunExecWithContext for full context control.
func RunExec(command, cwd, shell string, timeoutMs int) (*ExecResult, error) {
	effective := timeoutMs
	if effective <= 0 {
		effective = defaultExecTimeoutMs
	}
	return RunExecWithTimeout(command, cwd, shell, effective)
}

// MissingCommandError is returned when command is empty.
type MissingCommandError struct{}

func (e *MissingCommandError) Error() string { return "command is required" }

// defaultExecTimeoutMs bounds a command that did not specify a timeout.
const defaultExecTimeoutMs = 5 * 60 * 1000

// RunExecWithTimeout runs a command honoring an explicit timeout (applied
// even when the caller passed no timeout). It mirrors RunExecWithContext but
// derives the timeout internally when timeoutMs <= 0.
func RunExecWithTimeout(command, cwd, shell string, timeoutMs int) (*ExecResult, error) {
	ctx := context.Background()
	return runExec(ctx, command, cwd, shell, timeoutMs)
}

// RunExecWithContext runs a command honoring context cancellation.
func RunExecWithContext(ctx context.Context, command, cwd, shell string, timeoutMs int) (*ExecResult, error) {
	return runExec(ctx, command, cwd, shell, timeoutMs)
}

func runExec(ctx context.Context, command, cwd, shell string, timeoutMs int) (*ExecResult, error) {
	if strings.TrimSpace(command) == "" {
		return nil, &MissingCommandError{}
	}
	if cwd == "" {
		home, _ := userHomeDir()
		cwd = home
	}
	if !isAbs(cwd) {
		return nil, &InvalidCwdError{Cwd: cwd}
	}
	resolved := ResolveShell(shell)
	if !resolved.Available {
		return nil, &ShellUnavailableError{Shell: shell}
	}
	cmd := exec.CommandContext(ctx, resolved.Path, execArgsForShell(resolved.Kind, command)...)
	cmd.Dir = cwd
	var stdout, stderrB bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderrB

	startedAt := time.Now()
	effectiveTimeout := timeoutMs
	if effectiveTimeout <= 0 {
		effectiveTimeout = defaultExecTimeoutMs
	}
	var timeoutCh <-chan time.Time
	timeoutCh = time.After(time.Duration(effectiveTimeout) * time.Millisecond)

	if err := cmd.Start(); err != nil {
		return nil, err
	}
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()

	timedOut := false
	cancelled := false
	var waitErr error
	select {
	case waitErr = <-done:
	case <-timeoutCh:
		timedOut = true
		_ = cmd.Process.Kill()
		<-done
	case <-ctx.Done():
		cancelled = true
		_ = cmd.Process.Kill()
		<-done
	}
	exitCode := 0
	signal := ""
	if waitErr != nil {
		if exitErr, ok := waitErr.(*exec.ExitError); ok {
			exitCode = exitErr.ExitCode()
			if exitErr.ProcessState != nil && exitErr.ProcessState.Sys() != nil {
				signal = exitErr.ProcessState.String()
			}
		} else {
			return nil, waitErr
		}
	}
	out := stdout.String()
	errOut := stderrB.String()
	truncated := false
	if len(out) > maxBufferChars {
		out = out[len(out)-maxBufferChars:]
		truncated = true
	}
	if len(errOut) > maxBufferChars {
		errOut = errOut[len(errOut)-maxBufferChars:]
		truncated = true
	}
	if cancelled {
		signal = "cancelled"
	}
	return &ExecResult{
		Stdout:     stripANSI(out),
		Stderr:     stripANSI(errOut),
		ExitCode:   &exitCode,
		Signal:     signal,
		TimedOut:   timedOut,
		Truncated:  truncated,
		Cwd:        cwd,
		Shell:      resolved.Path,
		ShellKind:  resolved.Kind,
		DurationMs: time.Since(startedAt).Milliseconds(),
	}, nil
}
