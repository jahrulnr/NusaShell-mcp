package main

import (
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/creack/pty"
	"github.com/google/uuid"
)

const maxBufferChars = 200 * 1024

// Bootstrap rc files: keep shell startup scripts with host runtime data
// (like the Node server did) so interactive sessions get colors + rc.
func bootstrapRoot() string {
	if ud := os.Getenv("NUSASHELL_USER_DATA"); ud != "" {
		return filepath.Join(ud, "runtime")
	}
	return os.TempDir()
}

func ensureBootstrapFiles() (string, string, error) {
	dir := filepath.Join(bootstrapRootDir(), "terminal-bootstrap")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", "", err
	}
	color, err := os.ReadFile(filepath.Join(moduleDir(), "color-bootstrap.sh"))
	if err != nil {
		color = []byte("")
	}
	bashRC := "# NusaShell bash bootstrap\n[ -f \"$HOME/.bashrc\" ] && . \"$HOME/.bashrc\"\n" + string(color)
	zshRC := "# NusaShell zsh bootstrap\n[ -f \"$HOME/.zshrc\" ] && . \"$HOME/.zshrc\"\n" + string(color)
	if err := os.WriteFile(filepath.Join(dir, "bashrc"), []byte(bashRC), 0o644); err != nil {
		return "", "", err
	}
	if err := os.WriteFile(filepath.Join(dir, ".zshrc"), []byte(zshRC), 0o644); err != nil {
		return "", "", err
	}
	return filepath.Join(dir, "bashrc"), filepath.Join(dir, ".zshrc"), nil
}

func moduleDir() string {
	exe, err := os.Executable()
	if err != nil {
		return "."
	}
	return filepath.Dir(exe)
}

func bootstrapRootDir() string {
	if ud := os.Getenv("NUSASHELL_USER_DATA"); ud != "" {
		return filepath.Join(ud, "runtime")
	}
	return os.TempDir()
}

func shellSpawnArgs(shellPath string) []string {
	base := exeBase(shellPath)
	if base == "bash" {
		return []string{"--rcfile", filepath.Join(bootstrapRootDir(), "terminal-bootstrap", "bashrc")}
	}
	return nil
}

func shellSpawnEnv(shellPath string, baseEnv []string) []string {
	base := exeBase(shellPath)
	if base == "zsh" {
		return append(baseEnv, "ZDOTDIR="+filepath.Join(bootstrapRootDir(), "terminal-bootstrap"))
	}
	return baseEnv
}

// Session is an interactive PTY session.
type Session struct {
	ID        string    `json:"sessionId"`
	Shell     string    `json:"shell"`
	ShellKind ShellKind `json:"shellKind"`
	Cwd       string    `json:"cwd"`
	Cols      int       `json:"cols"`
	Rows      int       `json:"rows"`
	CreatedAt int64     `json:"createdAt"`
	Exited    bool      `json:"exited"`
	ExitCode  *int      `json:"exitCode"`

	mu      sync.Mutex
	buffer  string
	trunc   bool
	ptyFile *os.File
	cmd     *exec.Cmd
	doneCh  chan struct{}
}

func (s *Session) appendData(data string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	next := s.buffer + data
	if len(next) > maxBufferChars {
		next = next[len(next)-maxBufferChars:]
		s.trunc = true
	}
	s.buffer = next
}

func (s *Session) drain(clear bool) (string, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := s.buffer
	trunc := s.trunc
	if clear {
		s.buffer = ""
		s.trunc = false
	}
	return out, trunc
}

// SessionManager tracks open PTY sessions.
type SessionManager struct {
	mu       sync.Mutex
	sessions map[string]*Session
}

func NewSessionManager() *SessionManager {
	return &SessionManager{sessions: map[string]*Session{}}
}

func (m *SessionManager) Get(id string) (*Session, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	s, ok := m.sessions[id]
	if !ok {
		return nil, &MissingSessionError{ID: id}
	}
	return s, nil
}

func (m *SessionManager) List() []*Session {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]*Session, 0, len(m.sessions))
	for _, s := range m.sessions {
		out = append(out, s)
	}
	return out
}

func (m *SessionManager) Delete(id string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.sessions, id)
}

func (m *SessionManager) Add(s *Session) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.sessions[s.ID] = s
}

// MissingSessionError is returned when a session ID is unknown.
type MissingSessionError struct{ ID string }

func (e *MissingSessionError) Error() string { return "Session not found: " + e.ID }

// OpenSession creates a new interactive PTY session.
func OpenSession(mgr *SessionManager, shell string, cwd string, cols, rows int) (*Session, error) {
	resolved := ResolveShell(shell)
	if !resolved.Available {
		return nil, &ShellUnavailableError{Shell: shell}
	}

	if cwd == "" {
		home, _ := os.UserHomeDir()
		cwd = home
	}
	if !filepath.IsAbs(cwd) {
		return nil, &InvalidCwdError{Cwd: cwd}
	}
	info, err := os.Stat(cwd)
	if err != nil || !info.IsDir() {
		return nil, &InvalidCwdError{Cwd: cwd}
	}

	if cols < 1 {
		cols = 120
	}
	if rows < 1 {
		rows = 30
	}

	cmd := exec.Command(resolved.Path)
	cmd.Dir = cwd
	cmd.Env = append(os.Environ(),
		"TERM=xterm-256color",
		"COLORTERM=truecolor",
	)

	ptyFile, err := pty.StartWithSize(cmd, &pty.Winsize{Cols: uint16(cols), Rows: uint16(rows)})
	if err != nil {
		return nil, err
	}

	s := &Session{
		ID:        uuid.NewString(),
		Shell:     resolved.Path,
		ShellKind: resolved.Kind,
		Cwd:       cwd,
		Cols:      cols,
		Rows:      rows,
		CreatedAt: time.Now().UnixMilli(),
		ptyFile:   ptyFile,
		cmd:       cmd,
		doneCh:    make(chan struct{}),
	}

	go func() {
		buf := make([]byte, 4096)
		for {
			n, err := ptyFile.Read(buf)
			if n > 0 {
				s.appendData(string(buf[:n]))
			}
			if err != nil {
				if err != io.EOF {
					// ignore read errors; PTY closed
				}
				break
			}
		}
		exitCode := 0
		if err := cmd.Wait(); err != nil {
			if exitErr, ok := err.(*exec.ExitError); ok {
				code := exitErr.ExitCode()
				s.ExitCode = &code
				exitCode = code
			}
		} else {
			zero := 0
			s.ExitCode = &zero
		}
		_ = exitCode
		s.Exited = true
		close(s.doneCh)
	}()

	mgr.Add(s)
	return s, nil
}

// WriteInput sends data to the session's PTY.
func (s *Session) WriteInput(data string) error {
	if s.Exited {
		return &SessionExitedError{ID: s.ID}
	}
	_, err := s.ptyFile.Write([]byte(data))
	return err
}

// Resize updates the PTY dimensions.
func (s *Session) Resize(cols, rows int) error {
	if cols < 1 {
		cols = 1
	}
	if rows < 1 {
		rows = 1
	}
	s.Cols = cols
	s.Rows = rows
	if s.Exited {
		return nil
	}
	return pty.Setsize(s.ptyFile, &pty.Winsize{Cols: uint16(cols), Rows: uint16(rows)})
}

// Close kills the underlying process and releases the PTY.
func (s *Session) Close() {
	if !s.Exited && s.cmd.Process != nil {
		_ = s.cmd.Process.Kill()
	}
	_ = s.ptyFile.Close()
}

// ShellUnavailableError is returned when a requested shell is missing.
type ShellUnavailableError struct{ Shell string }

func (e *ShellUnavailableError) Error() string {
	return "Shell \"" + e.Shell + "\" is not available on this host. Call the shells tool to list installed kinds."
}

// InvalidCwdError is returned when cwd is not an absolute directory.
type InvalidCwdError struct{ Cwd string }

func (e *InvalidCwdError) Error() string {
	return "cwd must be an absolute path to an existing directory (got: " + e.Cwd + ")."
}

// SessionExitedError is returned when writing to an exited session.
type SessionExitedError struct{ ID string }

func (e *SessionExitedError) Error() string { return "Session has exited: " + e.ID }

// stripANSI removes ANSI/OSC escape sequences from text.
func stripANSI(s string) string {
	var sb strings.Builder
	sb.Grow(len(s))
	i := 0
	for i < len(s) {
		c := s[i]
		if c == 0x1b && i+1 < len(s) {
			next := s[i+1]
			if next == '[' {
				j := i + 2
				for j < len(s) {
					if s[j] >= 0x40 && s[j] <= 0x7e {
						break
					}
					j++
				}
				if j < len(s) {
					j++
				}
				i = j
				continue
			}
			if next == ']' {
				j := i + 2
				for j+1 < len(s) {
					if s[j] == 0x1b && s[j+1] == '\\' {
						j += 2
						break
					}
					j++
				}
				i = j
				continue
			}
		}
		sb.WriteByte(c)
		i++
	}
	return sb.String()
}
