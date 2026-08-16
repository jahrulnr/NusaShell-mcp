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

// RunExec runs a one-shot shell command.
func RunExec(command, cwd, shell string, timeoutMs int) (*ExecResult, error) {
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

	args := execArgsForShell(resolved.Kind, command)
	cmd := exec.Command(resolved.Path, args...)
	cmd.Dir = cwd

	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	startedAt := time.Now()

	var timeoutCh <-chan time.Time
	if timeoutMs > 0 {
		timeoutCh = time.After(time.Duration(timeoutMs) * time.Millisecond)
	}

	if err := cmd.Start(); err != nil {
		return nil, err
	}

	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()

	timedOut := false
	var waitErr error

	select {
	case waitErr = <-done:
	case <-timeoutCh:
		timedOut = true
		_ = cmd.Process.Kill()
		<-done
	}

	exitCode := 0
	signal := ""
	if waitErr != nil {
		if exitErr, ok := waitErr.(*exec.ExitError); ok {
			code := exitErr.ExitCode()
			exitCode = code
			if exitErr.ProcessState != nil && exitErr.ProcessState.Sys() != nil {
				// Signal info is platform-specific; keep a best-effort string.
				signal = exitErr.ProcessState.String()
			}
		} else {
			return nil, waitErr
		}
	}

	out := stdout.String()
	errOut := stderr.String()
	truncated := false
	if len(out) > maxBufferChars {
		out = out[len(out)-maxBufferChars:]
		truncated = true
	}
	if len(errOut) > maxBufferChars {
		errOut = errOut[len(errOut)-maxBufferChars:]
		truncated = true
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

// MissingCommandError is returned when command is empty.
type MissingCommandError struct{}

func (e *MissingCommandError) Error() string { return "command is required" }

// RunExecWithContext runs a command honoring context cancellation.
func RunExecWithContext(ctx context.Context, command, cwd, shell string, timeoutMs int) (*ExecResult, error) {
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
	var timeoutCh <-chan time.Time
	if timeoutMs > 0 {
		timeoutCh = time.After(time.Duration(timeoutMs) * time.Millisecond)
	}
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
