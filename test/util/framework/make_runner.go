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
	"context"
	"fmt"
	"os"
	"os/exec"
	"syscall"
	"time"

	"github.com/go-logr/logr"
)

// commandFor is the default command factory used by MakeRunner.
func defaultCommandFor(target string) *exec.Cmd {
	return exec.Command("make", target)
}

const (
	// DefaultMakeRunnerShutdownGracePeriod is how long MakeRunner waits after sending
	// SIGTERM to the process group before escalating to SIGKILL.
	DefaultMakeRunnerShutdownGracePeriod = 30 * time.Second
)

// MakeRunner runs make targets as subprocesses with graceful shutdown.
//
// Each invocation of Run places make and all of its children in a dedicated
// process group. When the supplied context is cancelled the runner sends SIGTERM
// to the whole group — giving shell scripts a chance to clean up — and only
// escalates to SIGKILL if the process has not exited within ShutdownGracePeriod.
type MakeRunner struct {
	// WorkDir is the directory from which make is invoked.
	// Defaults to the current process's working directory when empty.
	WorkDir string

	// ExtraEnv holds additional KEY=VALUE pairs appended to os.Environ() for
	// every Run call. Variables already present in os.Environ() are not removed;
	// entries in ExtraEnv take precedence because they appear last.
	ExtraEnv []string

	// ShutdownGracePeriod is how long to wait for the process group to exit
	// after SIGTERM before SIGKILL is sent. Defaults to DefaultMakeRunnerShutdownGracePeriod.
	ShutdownGracePeriod time.Duration

	// Logger receives informational messages emitted during shutdown sequences.
	Logger logr.Logger

	// commandFor builds the exec.Cmd for a given target. Defaults to
	// exec.Command("make", target). Unexported so it can be overridden from
	// within the framework package in tests without affecting the public API.
	commandFor func(target string) *exec.Cmd
}

// NewMakeRunner returns a MakeRunner with defaults suitable for use in E2E tests.
func NewMakeRunner(logger logr.Logger) *MakeRunner {
	return &MakeRunner{
		Logger:              logger,
		ShutdownGracePeriod: DefaultMakeRunnerShutdownGracePeriod,
		commandFor:          defaultCommandFor,
	}
}

// Run executes "make <target>", forwarding stdout and stderr to os.Stdout and os.Stderr.
// Use RunWithOutput to route output to custom writers (e.g. ginkgo.GinkgoWriter).
//
// The subprocess is placed in its own process group (Setpgid) so that context
// cancellation terminates the whole tree, not just the top-level make PID.
func (r *MakeRunner) Run(ctx context.Context, target string) error {
	return r.run(ctx, target, os.Stdout, os.Stderr)
}

// RunWithOutput executes "make <target>", writing stdout and stderr to the
// provided writers. Passing ginkgo.GinkgoWriter for both captures output in
// the Ginkgo test log.
func (r *MakeRunner) RunWithOutput(ctx context.Context, target string, stdout, stderr interface{ Write([]byte) (int, error) }) error {
	return r.run(ctx, target, stdout, stderr)
}

func (r *MakeRunner) run(ctx context.Context, target string, stdout, stderr interface{ Write([]byte) (int, error) }) error {
	gracePeriod := r.ShutdownGracePeriod
	if gracePeriod == 0 {
		gracePeriod = DefaultMakeRunnerShutdownGracePeriod
	}

	cmdFor := r.commandFor
	if cmdFor == nil {
		cmdFor = defaultCommandFor
	}
	cmd := cmdFor(target)
	cmd.Env = append(os.Environ(), r.ExtraEnv...)
	cmd.Stdout = stdout
	cmd.Stderr = stderr
	if r.WorkDir != "" {
		cmd.Dir = r.WorkDir
	}
	// New process group so that -pgid signals reach every child spawned by make
	// (templatize, az CLI, shell scripts) and not just the top-level make PID.
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}

	if err := cmd.Start(); err != nil {
		return fmt.Errorf("start make %s: %w", target, err)
	}

	// waitCh carries the result of cmd.Wait() from the reaping goroutine.
	waitCh := make(chan error, 1)
	go func() {
		waitCh <- cmd.Wait()
	}()

	select {
	case err := <-waitCh:
		return err

	case <-ctx.Done():
		// SIGTERM first, SIGKILL after grace period.
		pid := cmd.Process.Pid
		pgid, pgidErr := syscall.Getpgid(pid)
		if pgidErr == nil {
			r.Logger.Info("context cancelled — sending SIGTERM to make process group",
				"target", target, "pgid", pgid)
			_ = syscall.Kill(-pgid, syscall.SIGTERM)
		} else {
			r.Logger.Info("context cancelled — sending SIGTERM to make pid (pgid lookup failed)",
				"target", target, "pid", pid, "error", pgidErr)
			_ = syscall.Kill(pid, syscall.SIGTERM)
		}

		select {
		case <-waitCh:
			// Process exited cleanly after SIGTERM; nothing more to do.
		case <-time.After(gracePeriod):
			if pgidErr == nil {
				r.Logger.Info("grace period elapsed — sending SIGKILL to make process group",
					"target", target, "pgid", pgid)
				_ = syscall.Kill(-pgid, syscall.SIGKILL)
			} else {
				r.Logger.Info("grace period elapsed — sending SIGKILL to make pid (pgid lookup failed)",
					"target", target, "pid", pid, "error", pgidErr)
				_ = syscall.Kill(pid, syscall.SIGKILL)
			}
			<-waitCh
		}

		return fmt.Errorf("make %s: interrupted: %w", target, ctx.Err())
	}
}
