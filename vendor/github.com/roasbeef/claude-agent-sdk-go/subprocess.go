package claudeagent

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
	"sync"
)

// SubprocessRunner abstracts over Claude Code CLI subprocess execution.
//
// This interface allows swapping implementations for testing (mock subprocess),
// containerized execution (Docker/Kubernetes), or remote execution (SSH, gRPC).
type SubprocessRunner interface {
	// Start spawns the subprocess with the given arguments, environment, and
	// working directory. Returns stdin, stdout, stderr pipes.
	Start(ctx context.Context, args []string, env []string, cwd string) (
		stdin io.WriteCloser,
		stdout io.ReadCloser,
		stderr io.ReadCloser,
		err error,
	)

	// Wait blocks until the subprocess exits and returns the exit error.
	Wait() error

	// Kill forcefully terminates the subprocess.
	Kill() error

	// IsAlive returns true if the subprocess is still running.
	IsAlive() bool
}

// LocalSubprocessRunner executes Claude Code CLI as a local subprocess.
//
// This is the standard implementation that spawns the CLI binary using
// os/exec.Cmd.
type LocalSubprocessRunner struct {
	cliPath string
	cmd     *exec.Cmd
}

// NewLocalSubprocessRunner creates a runner for the local Claude CLI.
//
// The cliPath must point to the claude executable.
func NewLocalSubprocessRunner(cliPath string) *LocalSubprocessRunner {
	return &LocalSubprocessRunner{
		cliPath: cliPath,
	}
}

// Start spawns the Claude CLI subprocess with the given arguments, environment,
// and working directory.
//
// Arguments should include CLI flags like "--output-format stream-json".
// Environment should include ANTHROPIC_API_KEY and other necessary variables.
// If cwd is non-empty, the subprocess will be started in that directory.
//
// Note: We use exec.Command instead of exec.CommandContext to avoid issues
// with stdout pipe being closed prematurely. Callers should use Kill() for
// process termination when the context is canceled.
func (r *LocalSubprocessRunner) Start(
	ctx context.Context,
	args []string,
	env []string,
	cwd string,
) (io.WriteCloser, io.ReadCloser, io.ReadCloser, error) {
	// Create command without context - context-based lifecycle causes issues
	// with stdout pipes. Caller is responsible for killing on context cancel.
	r.cmd = exec.Command(r.cliPath, args...)
	r.cmd.Env = env

	// Set working directory if specified. Empty string uses the parent
	// process's cwd.
	if cwd != "" {
		// Validate the directory exists and is accessible.
		fi, err := os.Stat(cwd)
		if err != nil {
			return nil, nil, nil, fmt.Errorf("invalid working directory: %w", err)
		}
		if !fi.IsDir() {
			return nil, nil, nil, fmt.Errorf("cwd is not a directory: %s", cwd)
		}
		r.cmd.Dir = cwd
	}

	// Set up pipes
	stdin, err := r.cmd.StdinPipe()
	if err != nil {
		return nil, nil, nil, fmt.Errorf("failed to create stdin pipe: %w", err)
	}

	stdout, err := r.cmd.StdoutPipe()
	if err != nil {
		stdin.Close()
		return nil, nil, nil, fmt.Errorf("failed to create stdout pipe: %w", err)
	}

	stderr, err := r.cmd.StderrPipe()
	if err != nil {
		stdin.Close()
		stdout.Close()
		return nil, nil, nil, fmt.Errorf("failed to create stderr pipe: %w", err)
	}

	// Start the subprocess
	if err := r.cmd.Start(); err != nil {
		stdin.Close()
		stdout.Close()
		stderr.Close()
		return nil, nil, nil, fmt.Errorf("failed to start subprocess: %w", err)
	}

	return stdin, stdout, stderr, nil
}

// Wait blocks until the subprocess exits.
func (r *LocalSubprocessRunner) Wait() error {
	if r.cmd == nil || r.cmd.Process == nil {
		return fmt.Errorf("subprocess not started")
	}
	return r.cmd.Wait()
}

// Kill forcefully terminates the subprocess.
func (r *LocalSubprocessRunner) Kill() error {
	if r.cmd == nil || r.cmd.Process == nil {
		return nil
	}
	return r.cmd.Process.Kill()
}

// IsAlive returns true if the subprocess is still running.
func (r *LocalSubprocessRunner) IsAlive() bool {
	if r.cmd == nil || r.cmd.Process == nil {
		return false
	}

	// Check process state
	// On Unix: try to send signal 0 (doesn't actually send signal, just checks)
	// For now, assume alive if we have a process handle
	// TODO: Add platform-specific liveness check
	return true
}

// MockSubprocessRunner simulates a Claude CLI subprocess for testing.
//
// It provides in-memory pipes and allows tests to inject responses and
// verify requests without spawning an actual subprocess.
type MockSubprocessRunner struct {
	// Pipes for test control
	StdinPipe  *MockPipe
	StdoutPipe *MockPipe
	StderrPipe *MockPipe

	// Process state
	started bool
	exited  bool
	exitErr error

	// Captured start parameters for test inspection.
	StartArgs []string
	StartEnv  []string
	StartCwd  string
}

// NewMockSubprocessRunner creates a mock runner for testing.
func NewMockSubprocessRunner() *MockSubprocessRunner {
	return &MockSubprocessRunner{
		StdinPipe:  NewMockPipe(),
		StdoutPipe: NewMockPipe(),
		StderrPipe: NewMockPipe(),
	}
}

// Start simulates subprocess startup and captures parameters for test inspection.
func (m *MockSubprocessRunner) Start(
	ctx context.Context,
	args []string,
	env []string,
	cwd string,
) (io.WriteCloser, io.ReadCloser, io.ReadCloser, error) {
	m.started = true
	m.StartArgs = args
	m.StartEnv = env
	m.StartCwd = cwd
	return m.StdinPipe, m.StdoutPipe, m.StderrPipe, nil
}

// Wait simulates waiting for subprocess exit.
func (m *MockSubprocessRunner) Wait() error {
	// Block until exit is triggered
	for !m.exited {
		// In real tests, exited would be set by test code
		return m.exitErr
	}
	return m.exitErr
}

// Kill simulates killing the subprocess.
func (m *MockSubprocessRunner) Kill() error {
	m.exited = true
	return nil
}

// IsAlive returns subprocess status.
func (m *MockSubprocessRunner) IsAlive() bool {
	return m.started && !m.exited
}

// Exit signals subprocess termination (for test control).
func (m *MockSubprocessRunner) Exit(err error) {
	m.exited = true
	m.exitErr = err
	// Close pipes to signal EOF
	m.StdinPipe.Close()
	m.StdoutPipe.Close()
	m.StderrPipe.Close()
}

// MockPipe simulates an in-memory pipe for testing.
//
// Unlike io.Pipe which is synchronous (writes block until reads), MockPipe
// uses a buffered approach where writes append to an internal buffer and
// reads block waiting for data. This eliminates race conditions in tests
// where strict goroutine ordering would otherwise be required.
type MockPipe struct {
	mu     sync.Mutex
	cond   *sync.Cond
	buf    bytes.Buffer
	closed bool
}

// NewMockPipe creates a mock pipe with buffered writes.
func NewMockPipe() *MockPipe {
	p := &MockPipe{}
	p.cond = sync.NewCond(&p.mu)
	return p
}

// Read implements io.Reader. Blocks until data is available or pipe is closed.
func (p *MockPipe) Read(data []byte) (int, error) {
	p.mu.Lock()
	defer p.mu.Unlock()

	// Wait for data or close.
	for p.buf.Len() == 0 && !p.closed {
		p.cond.Wait()
	}

	// If closed and no data, return EOF.
	if p.closed && p.buf.Len() == 0 {
		return 0, io.EOF
	}

	return p.buf.Read(data)
}

// Write implements io.Writer. Writes are buffered and non-blocking.
func (p *MockPipe) Write(data []byte) (int, error) {
	p.mu.Lock()
	defer p.mu.Unlock()

	if p.closed {
		return 0, io.ErrClosedPipe
	}

	n, err := p.buf.Write(data)
	p.cond.Signal()
	return n, err
}

// Close closes the pipe, signaling EOF to readers.
func (p *MockPipe) Close() error {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.closed = true
	p.cond.Broadcast()
	return nil
}

// CloseWrite closes only the write side (useful for signaling EOF).
func (p *MockPipe) CloseWrite() error {
	return p.Close()
}

// CloseRead closes only the read side.
func (p *MockPipe) CloseRead() error {
	return p.Close()
}

// WriteString is a helper for writing strings to the pipe.
func (p *MockPipe) WriteString(s string) error {
	_, err := p.Write([]byte(s))
	return err
}

// DiscoverCLIPath discovers the Claude CLI executable path.
//
// Search order:
// 1. Explicit path in options
// 2. "claude" in system PATH
// 3. Common installation locations
func DiscoverCLIPath(options *Options) (string, error) {
	if options.CLIPath != "" {
		return options.CLIPath, nil
	}

	// Try PATH
	path, err := exec.LookPath("claude")
	if err == nil {
		return path, nil
	}

	// Try common locations
	commonPaths := []string{
		"/usr/local/bin/claude",
		"/usr/bin/claude",
		// Add more platform-specific paths as needed
	}

	for _, p := range commonPaths {
		if _, err := exec.LookPath(p); err == nil {
			return p, nil
		}
	}

	return "", &ErrCLINotFound{}
}

// ValidateCLIVersion checks that the installed CLI meets minimum requirements.
//
// Minimum required version: 2.0.0
func ValidateCLIVersion(cliPath string) error {
	cmd := exec.Command(cliPath, "--version")
	out, err := cmd.Output()
	if err != nil {
		return fmt.Errorf("failed to check version: %w", err)
	}

	version := strings.TrimSpace(string(out))

	// Parse version (simple check for now)
	// Expected format: "claude version 2.0.0" or "2.0.0"
	if !strings.Contains(version, "2.") {
		return &ErrCLIVersionIncompatible{
			Found:    version,
			Required: "2.0.0+",
		}
	}

	return nil
}
