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

package command

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/go-logr/logr"
	"github.com/go-logr/logr/funcr"
	"github.com/google/go-cmp/cmp"
)

func TestCensor(t *testing.T) {
	for _, tc := range []struct {
		name    string
		secrets []string
		input   string
		want    string
	}{
		{
			name:  "nothing registered leaves input untouched",
			input: "https://github.com/Azure/ARO-HCP.git",
			want:  "https://github.com/Azure/ARO-HCP.git",
		},
		{
			name:    "empty secrets are ignored",
			secrets: []string{""},
			input:   "plain text",
			want:    "plain text",
		},
		{
			name:    "token in a remote URL is redacted",
			secrets: []string{"ghs_supersecret"},
			input:   "remote: https://x-access-token:ghs_supersecret@github.com/Azure/ARO-HCP.git",
			want:    "remote: https://x-access-token:[REDACTED]@github.com/Azure/ARO-HCP.git",
		},
		{
			name:    "every occurrence is redacted",
			secrets: []string{"hook"},
			input:   "hook and hook",
			want:    "[REDACTED] and [REDACTED]",
		},
		{
			name:    "multiple secrets",
			secrets: []string{"tok", "web"},
			input:   "tok then web",
			want:    "[REDACTED] then [REDACTED]",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := NewCensor(tc.secrets...).Censor(tc.input)
			if diff := cmp.Diff(tc.want, got); diff != "" {
				t.Errorf("unexpected censored output (-want +got):\n%s", diff)
			}
		})
	}
}

func TestCensorNil(t *testing.T) {
	var c *Censor
	// A nil censor has to stay usable: Runner.Censor is optional.
	c.Add("secret")
	if got := c.Censor("secret"); got != "secret" {
		t.Errorf("nil censor changed input: got %q", got)
	}
}

func TestCensorAddAfterConstruction(t *testing.T) {
	// The GitHub installation token is only known partway through a run, so
	// it has to be registerable against an already-built censor.
	c := NewCensor("first")
	c.Add("second")
	got := c.Censor("first second")
	if diff := cmp.Diff("[REDACTED] [REDACTED]", got); diff != "" {
		t.Errorf("unexpected censored output (-want +got):\n%s", diff)
	}
}

func TestRunnerOutput(t *testing.T) {
	r := NewRunner("", nil)
	got, err := r.Output(context.Background(), "echo", "  hello  ")
	if err != nil {
		t.Fatalf("Output returned an unexpected error: %v", err)
	}
	if diff := cmp.Diff("hello", got); diff != "" {
		t.Errorf("unexpected stdout (-want +got):\n%s", diff)
	}
}

func TestRunnerRunsInDir(t *testing.T) {
	dir := t.TempDir()
	// macOS resolves TMPDIR through a /private symlink, so compare the
	// resolved paths rather than the raw ones.
	want, err := filepath.EvalSymlinks(dir)
	if err != nil {
		t.Fatalf("failed to resolve temp dir: %v", err)
	}

	got, err := NewRunner(dir, nil).Output(context.Background(), "pwd")
	if err != nil {
		t.Fatalf("Output returned an unexpected error: %v", err)
	}
	resolved, err := filepath.EvalSymlinks(got)
	if err != nil {
		t.Fatalf("failed to resolve reported dir: %v", err)
	}
	if diff := cmp.Diff(want, resolved); diff != "" {
		t.Errorf("command ran in the wrong directory (-want +got):\n%s", diff)
	}
}

func TestRunnerErrorIncludesOutput(t *testing.T) {
	// The bash `run()` helper discarded output even on failure. Keeping it
	// on the error is the point of the Runner, so assert it.
	err := NewRunner("", nil).Run(context.Background(), "sh", "-c", "echo boom >&2; exit 3")
	if err == nil {
		t.Fatal("expected an error from a failing command, got nil")
	}
	if !strings.Contains(err.Error(), "boom") {
		t.Errorf("error did not include command output: %v", err)
	}
	if !strings.Contains(err.Error(), "exit status 3") {
		t.Errorf("error did not include the exit status: %v", err)
	}
}

func TestRunnerErrorIsCensored(t *testing.T) {
	r := NewRunner("", NewCensor("hunter2"))
	err := r.Run(context.Background(), "sh", "-c", "echo using hunter2 >&2; exit 1")
	if err == nil {
		t.Fatal("expected an error from a failing command, got nil")
	}
	if strings.Contains(err.Error(), "hunter2") {
		t.Errorf("secret leaked into the error: %v", err)
	}
	if !strings.Contains(err.Error(), redacted) {
		t.Errorf("error was not censored: %v", err)
	}
}

func TestRunnerCommandLineIsCensored(t *testing.T) {
	// The failing command line is quoted back in the error; a credential
	// passed as an argument must not survive that.
	r := NewRunner("", NewCensor("tok123"))
	err := r.Run(context.Background(), "false", "https://x-access-token:tok123@github.com/Azure/ARO-HCP.git")
	if err == nil {
		t.Fatal("expected an error from a failing command, got nil")
	}
	if strings.Contains(err.Error(), "tok123") {
		t.Errorf("secret leaked into the error: %v", err)
	}
}

func TestRunnerConcurrentStreams(t *testing.T) {
	// os/exec drains stdout and stderr in separate goroutines, and only
	// serialises them when Stdout and Stderr are the same writer compared
	// with ==. Two distinct MultiWriters never are, so both copiers reach
	// the shared combined buffer and censoring writer at once. Run under
	// -race to catch a regression; the assertions below also catch the
	// corruption that race produces.
	const lines = 200
	r := NewRunner("", NewCensor("hunter2"))
	script := fmt.Sprintf(
		`for i in $(seq 1 %d); do echo "out hunter2 $i"; echo "err hunter2 $i" >&2; done`, lines)

	stdout, err := r.Output(context.Background(), "sh", "-c", script)
	if err != nil {
		t.Fatalf("Output returned an unexpected error: %v", err)
	}

	got := strings.Split(strings.TrimSpace(stdout), "\n")
	if len(got) != lines {
		t.Errorf("captured %d stdout lines, want %d", len(got), lines)
	}
	// Interleaved stderr must not have bled into the stdout capture.
	for _, line := range got {
		if !strings.HasPrefix(line, "out ") {
			t.Fatalf("stdout capture was corrupted by stderr: %q", line)
		}
	}
}

func TestRunnerConcurrentStreamsCensored(t *testing.T) {
	// The censoring writer buffers partial lines, so a concurrent write
	// corrupting that buffer is a secret-disclosure bug, not just garbled
	// output. Exercise it at the verbosity that enables live streaming.
	const lines = 200
	var live bytes.Buffer
	restore := liveOutput
	liveOutput = &live
	t.Cleanup(func() { liveOutput = restore })

	logger := funcr.New(func(string, string) {}, funcr.Options{Verbosity: 2})
	ctx := logr.NewContext(context.Background(), logger)

	r := NewRunner("", NewCensor("hunter2"))
	script := fmt.Sprintf(
		`for i in $(seq 1 %d); do echo "out hunter2 $i"; echo "err hunter2 $i" >&2; done; exit 1`, lines)

	err := r.Run(ctx, "sh", "-c", script)
	if err == nil {
		t.Fatal("expected an error from a failing command, got nil")
	}

	// Both the captured error and the live stream must be fully redacted.
	for name, text := range map[string]string{"error": err.Error(), "live stream": live.String()} {
		if strings.Contains(text, "hunter2") {
			t.Errorf("secret survived in the %s", name)
		}
	}

	// Count only the emitted lines, not the command line, which also
	// contains the secret twice and is censored separately.
	var censored int
	for _, line := range strings.Split(err.Error(), "\n") {
		if strings.HasPrefix(line, "out ") || strings.HasPrefix(line, "err ") {
			if !strings.Contains(line, redacted) {
				t.Fatalf("line escaped redaction: %q", line)
			}
			censored++
		}
	}
	if want := lines * 2; censored != want {
		t.Errorf("captured %d censored lines, want %d", censored, want)
	}
}

func TestRunnerContextCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := NewRunner("", nil).Run(ctx, "sleep", "30"); err == nil {
		t.Error("expected an error when the context is already cancelled, got nil")
	}
}

func TestCensoringWriter(t *testing.T) {
	for _, tc := range []struct {
		name   string
		writes []string
		want   string
	}{
		{
			name:   "redacts within a single write",
			writes: []string{"before secret after\n"},
			want:   "before [REDACTED] after\n",
		},
		{
			name: "redacts a secret split across writes",
			// The whole reason for buffering: a chunk boundary must not
			// let a secret slip through unredacted.
			writes: []string{"before sec", "ret after\n"},
			want:   "before [REDACTED] after\n",
		},
		{
			name:   "handles multiple lines in one write",
			writes: []string{"secret\nsecret\n"},
			want:   "[REDACTED]\n[REDACTED]\n",
		},
		{
			name:   "flushes an unterminated trailing line on close",
			writes: []string{"trailing secret"},
			want:   "trailing [REDACTED]",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var sink bytes.Buffer
			w := newCensoringWriter(&sink, NewCensor("secret"))
			for _, write := range tc.writes {
				if _, err := w.Write([]byte(write)); err != nil {
					t.Fatalf("Write returned an unexpected error: %v", err)
				}
			}
			if err := w.Close(); err != nil {
				t.Fatalf("Close returned an unexpected error: %v", err)
			}
			if diff := cmp.Diff(tc.want, sink.String()); diff != "" {
				t.Errorf("unexpected censored stream (-want +got):\n%s", diff)
			}
		})
	}
}

func TestCensoringWriterReportsFullWriteLength(t *testing.T) {
	// io.MultiWriter treats a short write as an error, so Write must report
	// the length it was handed even while buffering a partial line.
	w := newCensoringWriter(&bytes.Buffer{}, NewCensor("secret"))
	input := []byte("no newline here")
	n, err := w.Write(input)
	if err != nil {
		t.Fatalf("Write returned an unexpected error: %v", err)
	}
	if n != len(input) {
		t.Errorf("Write reported %d bytes, want %d", n, len(input))
	}
}

func TestMissingTools(t *testing.T) {
	missing := MissingTools("sh", "definitely-not-a-real-binary-xyz")
	if diff := cmp.Diff([]string{"definitely-not-a-real-binary-xyz"}, missing); diff != "" {
		t.Errorf("unexpected missing tools (-want +got):\n%s", diff)
	}

	if missing := MissingTools("sh"); missing != nil {
		t.Errorf("expected no missing tools, got %v", missing)
	}
}

func TestMain(m *testing.M) {
	// The runner tests shell out to POSIX utilities; skip the package
	// wholesale somewhere they are unavailable rather than failing oddly.
	if len(MissingTools("sh", "echo", "pwd", "false", "sleep")) > 0 {
		os.Exit(0)
	}
	os.Exit(m.Run())
}
