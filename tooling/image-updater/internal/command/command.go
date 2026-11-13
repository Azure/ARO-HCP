// Copyright 2025 Microsoft Corporation
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

// Package command runs external processes on behalf of the image-updater.
// Everything the tool shells out to (git, make) goes through a Runner so that
// logging, output capture and secret redaction are handled in one place
// rather than at each call site.
package command

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
	"sync"

	"github.com/go-logr/logr"
)

// redacted replaces a secret value wherever it would otherwise be logged.
const redacted = "[REDACTED]"

// liveOutput is where command output is streamed at verbosity 2. A variable
// so tests can capture it rather than flood the test log.
var liveOutput io.Writer = os.Stderr

// Censor removes secret values from text before it reaches a log line or an
// error message. This matters because some commands must receive a credential
// in their arguments -- `git push https://x-access-token:<token>@github.com/...`
// being the obvious one -- and git echoes the remote URL back on failure.
//
// A nil *Censor is usable and redacts nothing.
type Censor struct {
	secrets []string
}

// NewCensor returns a Censor that redacts the given secrets. Empty values are
// ignored so callers can pass optional credentials unconditionally.
func NewCensor(secrets ...string) *Censor {
	c := &Censor{}
	c.Add(secrets...)
	return c
}

// Add registers additional secrets to redact. Credentials such as a GitHub
// installation token are only known partway through a run, so the censor has
// to be extensible after construction.
func (c *Censor) Add(secrets ...string) {
	if c == nil {
		return
	}
	for _, secret := range secrets {
		if secret != "" {
			c.secrets = append(c.secrets, secret)
		}
	}
}

// Censor returns s with every registered secret replaced.
func (c *Censor) Censor(s string) string {
	if c == nil {
		return s
	}
	for _, secret := range c.secrets {
		s = strings.ReplaceAll(s, secret, redacted)
	}
	return s
}

// Runner executes commands in a fixed working directory.
type Runner struct {
	// Dir is the working directory for every command. Empty means the
	// current process working directory.
	Dir string
	// Censor redacts secrets from logs and errors. May be nil.
	Censor *Censor
}

// NewRunner returns a Runner rooted at dir.
func NewRunner(dir string, censor *Censor) *Runner {
	return &Runner{Dir: dir, Censor: censor}
}

// Run executes a command and discards its stdout. Output is streamed to
// stderr at verbosity 2 and above, and is always attached to the returned
// error, so a failure stays diagnosable at the default verbosity. This
// replaces the bash `run()` helper, which sent output to /dev/null and lost
// it on failure too.
func (r *Runner) Run(ctx context.Context, name string, args ...string) error {
	_, err := r.run(ctx, name, args, false)
	return err
}

// Output executes a command and returns its stdout with surrounding
// whitespace trimmed.
func (r *Runner) Output(ctx context.Context, name string, args ...string) (string, error) {
	stdout, err := r.run(ctx, name, args, true)
	return strings.TrimSpace(stdout), err
}

func (r *Runner) run(ctx context.Context, name string, args []string, captureStdout bool) (string, error) {
	logger := logr.FromContextOrDiscard(ctx)
	commandLine := r.Censor.Censor(strings.Join(append([]string{name}, args...), " "))
	logger.V(2).Info("running command", "command", commandLine, "dir", r.Dir)

	cmd := exec.CommandContext(ctx, name, args...)
	cmd.Dir = r.Dir

	var stdout, combined bytes.Buffer
	stdoutWriters := []io.Writer{&combined}
	stderrWriters := []io.Writer{&combined}
	if captureStdout {
		stdoutWriters = append(stdoutWriters, &stdout)
	}
	if logger.V(2).Enabled() {
		// Stream live so long-running targets such as `make materialize`
		// are observable, but route through the censor so a credential can
		// never reach the job log.
		live := newCensoringWriter(liveOutput, r.Censor)
		defer func() {
			_ = live.Close()
		}()
		stdoutWriters = append(stdoutWriters, live)
		stderrWriters = append(stderrWriters, live)
	}
	// Both streams feed the same combined buffer, and at verbosity 2 the
	// same censoring writer. os/exec drains stdout and stderr in separate
	// goroutines and only promises to serialise them when Stdout and
	// Stderr are the same writer compared with ==, which two distinct
	// MultiWriters never are. Neither bytes.Buffer nor censoringWriter is
	// concurrency-safe, so a command writing to both streams would race:
	// corrupting captured output, and -- worse -- corrupting the censor's
	// partial-line buffer, which could let a secret through unredacted.
	var mu sync.Mutex
	cmd.Stdout = &serialWriter{mu: &mu, delegate: io.MultiWriter(stdoutWriters...)}
	cmd.Stderr = &serialWriter{mu: &mu, delegate: io.MultiWriter(stderrWriters...)}

	if err := cmd.Run(); err != nil {
		output := strings.TrimSpace(r.Censor.Censor(combined.String()))
		if output == "" {
			return "", fmt.Errorf("%s failed: %w", commandLine, err)
		}
		return "", fmt.Errorf("%s failed: %w\n%s", commandLine, err, output)
	}

	return stdout.String(), nil
}

// serialWriter serialises writes to its delegate. Several serialWriters
// sharing one mutex serialise against each other, which is what lets the
// stdout and stderr copier goroutines share a destination safely.
type serialWriter struct {
	mu       *sync.Mutex
	delegate io.Writer
}

func (w *serialWriter) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.delegate.Write(p)
}

// censoringWriter redacts secrets from a stream. It buffers an incomplete
// trailing line so a secret split across two writes is still caught: every
// secret handled here is a single-line token, so holding back a partial line
// is sufficient.
type censoringWriter struct {
	delegate io.Writer
	censor   *Censor
	pending  []byte
}

func newCensoringWriter(delegate io.Writer, censor *Censor) *censoringWriter {
	return &censoringWriter{delegate: delegate, censor: censor}
}

func (w *censoringWriter) Write(p []byte) (int, error) {
	w.pending = append(w.pending, p...)
	for {
		i := bytes.IndexByte(w.pending, '\n')
		if i < 0 {
			return len(p), nil
		}
		line := string(w.pending[:i+1])
		w.pending = w.pending[i+1:]
		if _, err := io.WriteString(w.delegate, w.censor.Censor(line)); err != nil {
			return len(p), err
		}
	}
}

// Close flushes any buffered partial line.
func (w *censoringWriter) Close() error {
	if len(w.pending) == 0 {
		return nil
	}
	line := string(w.pending)
	w.pending = nil
	_, err := io.WriteString(w.delegate, w.censor.Censor(line))
	return err
}

// MissingTools returns the subset of names that are not executable on PATH,
// in the order given. It replaces the script's required-tools preflight loop.
func MissingTools(names ...string) []string {
	var missing []string
	for _, name := range names {
		if _, err := exec.LookPath(name); err != nil {
			missing = append(missing, name)
		}
	}
	return missing
}
