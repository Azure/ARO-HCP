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

package framework

import (
	"bytes"
	"context"
	"errors"
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/go-logr/logr"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// shellRunner returns a MakeRunner whose command factory runs the target string
// as a POSIX shell one-liner ("sh -c <target>"). This lets tests drive all
// MakeRunner paths without requiring a real Makefile.
func shellRunner(t *testing.T, opts ...func(*MakeRunner)) *MakeRunner {
	t.Helper()
	r := NewMakeRunner(logr.Discard())
	r.commandFor = func(target string) *exec.Cmd {
		return exec.Command("sh", "-c", target)
	}
	for _, opt := range opts {
		opt(r)
	}
	return r
}

func TestMakeRunner_SuccessfulExit(t *testing.T) {
	t.Parallel()

	r := shellRunner(t)
	err := r.Run(context.Background(), "exit 0")
	assert.NoError(t, err, "expected nil error for zero exit code")
}

func TestMakeRunner_NonZeroExit(t *testing.T) {
	t.Parallel()

	r := shellRunner(t)
	err := r.Run(context.Background(), "exit 42")
	require.Error(t, err, "expected error for non-zero exit code")

	var exitErr *exec.ExitError
	require.True(t, errors.As(err, &exitErr), "error should be an *exec.ExitError")
	assert.Equal(t, 42, exitErr.ExitCode(), "exit code should be 42")
}

func TestMakeRunner_ContextCancelledDuringRun(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithCancel(context.Background())

	r := shellRunner(t, func(r *MakeRunner) {
		// Short grace period so the test does not block for 30 s.
		r.ShutdownGracePeriod = 200 * time.Millisecond
	})

	// Cancel the context a short time after Run starts so that the
	// "sleep 60" subprocess is definitely running.
	go func() {
		time.Sleep(50 * time.Millisecond)
		cancel()
	}()

	start := time.Now()
	err := r.Run(ctx, "sleep 60")
	elapsed := time.Since(start)

	require.Error(t, err, "expected error when context is cancelled")
	assert.ErrorIs(t, err, context.Canceled, "error should wrap context.Canceled")
	// The test should finish well within the grace period + a generous margin,
	// confirming the subprocess was actually terminated.
	assert.Less(t, elapsed, 5*time.Second,
		"Run should return quickly after context cancellation, got %s", elapsed)
}

func TestMakeRunner_ContextCancelledGracefulSIGTERM(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithCancel(context.Background())

	r := shellRunner(t, func(r *MakeRunner) {
		// Give the process 2 s to handle SIGTERM — well below the 30 s default.
		r.ShutdownGracePeriod = 2 * time.Second
	})

	// The subprocess traps SIGTERM and exits cleanly within the grace period.
	go func() {
		time.Sleep(50 * time.Millisecond)
		cancel()
	}()

	start := time.Now()
	err := r.Run(ctx, "trap 'exit 0' TERM; sleep 60")
	elapsed := time.Since(start)

	require.Error(t, err, "expected error when context is cancelled")
	assert.ErrorIs(t, err, context.Canceled, "error should wrap context.Canceled")
	assert.Less(t, elapsed, 2*time.Second,
		"Run should return quickly after SIGTERM is handled, got %s", elapsed)
}

func TestMakeRunner_ContextCancelledForcesKillAfterGrace(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithCancel(context.Background())

	r := shellRunner(t, func(r *MakeRunner) {
		// Very short grace period: the process ignores SIGTERM so SIGKILL must fire.
		r.ShutdownGracePeriod = 150 * time.Millisecond
	})

	// The subprocess ignores SIGTERM; it should be killed after the grace period.
	go func() {
		time.Sleep(50 * time.Millisecond)
		cancel()
	}()

	start := time.Now()
	err := r.Run(ctx, "trap '' TERM; sleep 60")
	elapsed := time.Since(start)

	require.Error(t, err, "expected error when context is cancelled")
	assert.ErrorIs(t, err, context.Canceled, "error should wrap context.Canceled")
	// grace period + reasonable margin
	assert.Less(t, elapsed, 2*time.Second,
		"Run should return after SIGKILL, got %s", elapsed)
}

func TestMakeRunner_DefaultGracePeriodWhenZero(t *testing.T) {
	t.Parallel()

	r := shellRunner(t)
	r.ShutdownGracePeriod = 0

	// Verify zero is replaced with the default inside run().
	// We cannot easily observe the exact duration, but we can confirm the
	// runner still works correctly for a successful invocation.
	err := r.Run(context.Background(), "exit 0")
	assert.NoError(t, err, "runner with zero ShutdownGracePeriod should succeed")
}

func TestMakeRunner_ExtraEnvIsPassedToSubprocess(t *testing.T) {
	t.Parallel()

	var stdout bytes.Buffer
	r := shellRunner(t, func(r *MakeRunner) {
		r.ExtraEnv = []string{"MAKE_RUNNER_TEST_VAR=hello_from_test"}
	})

	err := r.RunWithOutput(context.Background(),
		"echo $MAKE_RUNNER_TEST_VAR", &stdout, &stdout)
	require.NoError(t, err, "expected no error")
	assert.Equal(t, "hello_from_test", strings.TrimSpace(stdout.String()),
		"ExtraEnv variable should be visible in the subprocess")
}

func TestMakeRunner_WorkDirIsUsed(t *testing.T) {
	t.Parallel()

	tmpDir := t.TempDir()
	var stdout bytes.Buffer

	r := shellRunner(t, func(r *MakeRunner) {
		r.WorkDir = tmpDir
	})

	err := r.RunWithOutput(context.Background(), "pwd", &stdout, &stdout)
	require.NoError(t, err, "expected no error")
	// t.TempDir() may return a symlink-resolved path; use HasSuffix comparison
	// against the base name which is always unique.
	assert.Contains(t, strings.TrimSpace(stdout.String()), t.Name(),
		"working directory should be set to WorkDir")
}

func TestMakeRunner_OutputRoutedToWriters(t *testing.T) {
	t.Parallel()

	var stdout, stderr bytes.Buffer
	r := shellRunner(t)

	err := r.RunWithOutput(context.Background(),
		"echo out; echo err >&2", &stdout, &stderr)
	require.NoError(t, err, "expected no error")
	assert.Equal(t, "out", strings.TrimSpace(stdout.String()),
		"stdout should contain the echoed line")
	assert.Equal(t, "err", strings.TrimSpace(stderr.String()),
		"stderr should contain the echoed line")
}

func TestMakeRunner_NewMakeRunnerDefaults(t *testing.T) {
	t.Parallel()

	r := NewMakeRunner(logr.Discard())
	assert.Equal(t, DefaultMakeRunnerShutdownGracePeriod, r.ShutdownGracePeriod,
		"ShutdownGracePeriod should default to DefaultMakeRunnerShutdownGracePeriod")
	assert.NotNil(t, r.commandFor,
		"commandFor should be set to the default factory by NewMakeRunner")
}
