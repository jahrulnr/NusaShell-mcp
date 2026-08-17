package main

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
)

// MissingCommandError is returned when an exec command is empty.
type MissingCommandError struct{}

func (e *MissingCommandError) Error() string { return "command is required" }

// Process is a long-lived non-PTY command owned by the MCP server.
// Its lifetime is independent from any individual MCP request.
type Process struct {
	ID        string    `json:"processId"`
	Command   string    `json:"command"`
	Cwd       string    `json:"cwd"`
	Shell     string    `json:"shell"`
	ShellKind ShellKind `json:"shellKind"`
	StartedAt int64     `json:"startedAt"`

	mu        sync.Mutex
	outputWG  sync.WaitGroup
	stdout    bytes.Buffer
	stderr    bytes.Buffer
	truncated bool
	cmd       *exec.Cmd
	done      chan struct{}
	exitCode  *int
	signal    string
}

func (p *Process) appendOutput(dst *bytes.Buffer, data []byte) {
	p.mu.Lock()
	defer p.mu.Unlock()
	dst.Write(data)
	if dst.Len() > maxBufferChars {
		b := dst.Bytes()
		dst.Reset()
		dst.Write(b[len(b)-maxBufferChars:])
		p.truncated = true
	}
}

func (p *Process) snapshot(clear bool) (stdout, stderr string, truncated bool, exited bool, exitCode *int, signal string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	stdout, stderr = p.stdout.String(), p.stderr.String()
	truncated = p.truncated
	exited = p.exitCode != nil
	if p.exitCode != nil {
		v := *p.exitCode
		exitCode = &v
	}
	signal = p.signal
	if clear {
		p.stdout.Reset()
		p.stderr.Reset()
		p.truncated = false
	}
	return
}

func (p *Process) isExited() bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.exitCode != nil
}

func (p *Process) setExit(err error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	code := 0
	if err != nil {
		if ee, ok := err.(*exec.ExitError); ok {
			code = ee.ExitCode()
		} else {
			code = -1
		}
	}
	p.exitCode = &code
	if err != nil {
		p.signal = err.Error()
	}
	close(p.done)
}

func (p *Process) wait(ctx context.Context, timeout time.Duration) bool {
	if timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, timeout)
		defer cancel()
	}
	select {
	case <-p.done:
		return true
	case <-ctx.Done():
		return false
	}
}

func (p *Process) kill() error {
	p.mu.Lock()
	cmd := p.cmd
	exited := p.exitCode != nil
	p.mu.Unlock()
	if exited || cmd == nil || cmd.Process == nil {
		return nil
	}
	return killProcessTree(cmd.Process)
}

type ProcessManager struct {
	mu        sync.RWMutex
	processes map[string]*Process
}

func NewProcessManager() *ProcessManager {
	return &ProcessManager{processes: map[string]*Process{}}
}

func (m *ProcessManager) Add(p *Process) {
	m.mu.Lock()
	m.processes[p.ID] = p
	m.mu.Unlock()
}

func (m *ProcessManager) Get(id string) (*Process, error) {
	m.mu.RLock()
	p := m.processes[id]
	m.mu.RUnlock()
	if p == nil {
		return nil, fmt.Errorf("Process not found: %s", id)
	}
	return p, nil
}

func (m *ProcessManager) List() []*Process {
	m.mu.RLock()
	defer m.mu.RUnlock()
	out := make([]*Process, 0, len(m.processes))
	for _, p := range m.processes {
		out = append(out, p)
	}
	return out
}

func (m *ProcessManager) Delete(id string) {
	m.mu.Lock()
	delete(m.processes, id)
	m.mu.Unlock()
}

func startProcess(command, cwd, shell string, extraEnv []string) (*Process, error) {
	if strings.TrimSpace(command) == "" {
		return nil, &MissingCommandError{}
	}
	if cwd == "" {
		cwd, _ = userHomeDir()
	}
	if !isAbs(cwd) {
		return nil, &InvalidCwdError{Cwd: cwd}
	}
	resolved := ResolveShell(shell)
	if !resolved.Available {
		return nil, &ShellUnavailableError{Shell: shell}
	}

	cmd := exec.Command(resolved.Path, execArgsForShell(resolved.Kind, command)...)
	cmd.Dir = cwd
	cmd.Env = osEnvironForShell(resolved.Path, extraEnv)

	p := &Process{
		ID:        "proc_" + uuid.NewString()[:12],
		Command:   command,
		Cwd:       cwd,
		Shell:     resolved.Path,
		ShellKind: resolved.Kind,
		StartedAt: time.Now().UnixMilli(),
		cmd:       cmd,
		done:      make(chan struct{}),
	}

	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, err
	}
	stderrPipe, err := cmd.StderrPipe()
	if err != nil {
		return nil, err
	}

	if err := setProcessGroup(cmd); err != nil {
		return nil, err
	}
	if err := cmd.Start(); err != nil {
		return nil, err
	}

	p.outputWG.Add(2)
	go func() {
		defer p.outputWG.Done()
		buf := make([]byte, 8192)
		for {
			n, err := stdout.Read(buf)
			if n > 0 {
				p.appendOutput(&p.stdout, buf[:n])
			}
			if err != nil {
				break
			}
		}
	}()
	go func() {
		defer p.outputWG.Done()
		buf := make([]byte, 8192)
		for {
			n, err := stderrPipe.Read(buf)
			if n > 0 {
				p.appendOutput(&p.stderr, buf[:n])
			}
			if err != nil {
				break
			}
		}
	}()
	go func() {
		err := cmd.Wait()
		p.outputWG.Wait()
		p.setExit(err)
	}()

	return p, nil
}

func osEnvironForShell(shell string, extraEnv []string) []string {
	env := append([]string{}, os.Environ()...)
	env = append(env, "TERM=xterm-256color")
	env = append(env, extraEnv...)
	return env
}
