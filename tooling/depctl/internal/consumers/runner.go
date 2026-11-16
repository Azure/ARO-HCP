// Copyright 2026 Microsoft Corporation
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

package consumers

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"sync/atomic"
	"time"

	"github.com/Azure/ARO-HCP/tooling/depctl/internal/model"
)

// MaxLogBytes bounds captured consumer output. Truncation is reported, never
// silently hidden.
const MaxLogBytes = 2 << 20

// Runner executes registered consumers in a candidate or baseline tree.
type Runner struct {
	Registry *Registry
	// MaxLogBytes overrides the default output bound when non-zero.
	MaxLogBytes int64
	sequence    atomic.Uint64
}

// Returns a process-unique invocation number.
func (r *Runner) nextSequence() uint64 { return r.sequence.Add(1) }

// NewRunner returns a runner over a registry.
func NewRunner(registry *Registry) *Runner { return &Runner{Registry: registry} }

// Run executes one consumer and returns its bounded result.
//
// A consumer with an unmet prerequisite is reported as not run rather than
// failed: a missing prerequisite is incomplete evidence, not a pass or a
// regression.
func (r *Runner) Run(ctx context.Context, root string, ref model.ConsumerRef, logDirectory string) model.ConsumerResult {
	result := model.ConsumerResult{ID: ref.ID, Phase: "candidate", Status: model.ConsumerNotRun}
	spec, err := r.Registry.Get(ref.ID)
	if err != nil {
		result.Status = model.ConsumerError
		result.Reason = err.Error()
		return result
	}
	if missing := spec.Missing(root, ref.Params); len(missing) > 0 {
		result.Reason = "missing prerequisite: " + strings.Join(missing, ", ")
		return result
	}
	arguments, err := spec.Expand(root, ref.Params)
	if err != nil {
		result.Status = model.ConsumerError
		result.Reason = err.Error()
		return result
	}
	if spec.Preflight != nil {
		if err := spec.Preflight(root, ref.Params); err != nil {
			result.Reason = err.Error()
			return result
		}
	}
	limit := r.MaxLogBytes
	if limit == 0 {
		limit = MaxLogBytes
	}
	if logDirectory != "" {
		if err := os.MkdirAll(logDirectory, 0o755); err != nil {
			result.Status = model.ConsumerError
			result.Reason = "consumer log directory could not be created: " + err.Error()
			return result
		}
	}
	directory := spec.Directory
	workDirectory := filepath.Join(root, filepath.FromSlash(directory))
	if info, err := os.Stat(workDirectory); err != nil || !info.IsDir() {
		result.Status = model.ConsumerError
		result.Reason = fmt.Sprintf("consumer %s directory %q is unavailable", ref.ID, directory)
		return result
	}

	runContext, cancel := context.WithTimeout(ctx, spec.Timeout)
	defer cancel()

	capture := &boundedBuffer{limit: limit}
	// The launcher always stays the reviewed program; a phase-specific binary is
	// passed through the consumer's own selection mechanism. A consumer that
	// invokes the tool by name gets a temporary alias so the name resolves to
	// exactly the binary the preflight verified.
	environment := append([]string{}, spec.Environment...)
	executable := ref.Params["executablePath"]
	if executable != "" && spec.AliasOnPath && spec.SelectedBinary != "" {
		aliasDirectory, err := os.MkdirTemp("", "depctl-tool-")
		if err != nil {
			result.Status = model.ConsumerError
			result.Reason = "tool alias could not be created: " + err.Error()
			return result
		}
		defer os.RemoveAll(aliasDirectory)
		alias := filepath.Join(aliasDirectory, spec.SelectedBinary)
		if err := os.Symlink(executable, alias); err != nil {
			result.Status = model.ConsumerError
			result.Reason = "tool alias could not be created: " + err.Error()
			return result
		}
		environment = append(environment, "PATH="+aliasDirectory+":"+os.Getenv("PATH"))
	}
	if executable != "" && spec.SelectionEnvironment != nil {
		environment = append(environment, spec.SelectionEnvironment(executable)...)
	}
	command := exec.CommandContext(runContext, spec.Program, arguments...)
	configureProcessGroup(command)
	command.Cancel = func() error { return terminateProcessTree(command.Process) }
	command.WaitDelay = 5 * time.Second
	command.Dir = workDirectory
	command.Env = consumerEnvironment(environment)
	command.Stdout = capture
	command.Stderr = capture
	started := time.Now().UTC()
	result.StartedAt = started.Format(time.RFC3339)
	err = command.Run()
	result.DurationMS = time.Since(started).Milliseconds()

	// Every invocation gets its own file: two runs of the same consumer in one
	// second must not overwrite each other's captured output.
	logName := fmt.Sprintf("%s-%s-%d-%d.log", sanitize(ref.ID),
		started.Format("20060102T150405.000000000Z"), os.Getpid(), r.nextSequence())
	if logDirectory != "" {
		logPath := filepath.Join(logDirectory, logName)
		if writeErr := os.WriteFile(logPath, capture.Bytes(), 0o600); writeErr != nil {
			result.Status = model.ConsumerError
			result.Reason = joinReason(result.Reason, "consumer log could not be written: "+writeErr.Error())
			return result
		}
		result.LogPath = logPath
		result.LogDigest = digestBytes(capture.Bytes())
	}
	if capture.truncated > 0 {
		result.Reason = fmt.Sprintf("captured output truncated: %d bytes were dropped", capture.truncated)
	}
	switch {
	case runContext.Err() == context.DeadlineExceeded:
		result.Status = model.ConsumerFailed
		result.Reason = joinReason(result.Reason, fmt.Sprintf("timed out after %s", spec.Timeout))
		result.ExitCode = -1
	case ctx.Err() == context.Canceled:
		result.Status = model.ConsumerError
		result.Reason = joinReason(result.Reason, "cancelled")
		result.ExitCode = -1
	case err != nil:
		var exit *exec.ExitError
		if errors.As(err, &exit) {
			result.Status = model.ConsumerFailed
			result.ExitCode = exit.ExitCode()
			result.Reason = joinReason(result.Reason, summarize(capture.Bytes()))
		} else {
			result.Status = model.ConsumerError
			result.Reason = joinReason(result.Reason, err.Error())
		}
	default:
		result.Status = model.ConsumerPassed
	}
	return result
}

// Replaces path separators for use in a log file name.
func sanitize(value string) string {
	replacer := strings.NewReplacer("/", "-", ":", "-", " ", "-")
	return replacer.Replace(value)
}

// Combines two non-empty reason fragments.
func joinReason(existing, addition string) string {
	if existing == "" {
		return addition
	}
	if addition == "" {
		return existing
	}
	return existing + "; " + addition
}

// Returns the last non-empty log lines, which carry the failure.
func summarize(content []byte) string {
	const maxLines = 12
	lines := strings.Split(strings.TrimSpace(string(content)), "\n")
	var selected []string
	for index := len(lines) - 1; index >= 0 && len(selected) < maxLines; index-- {
		if line := strings.TrimRight(lines[index], "\r"); line != "" {
			selected = append(selected, line)
		}
	}
	for left, right := 0, len(selected)-1; left < right; left, right = left+1, right-1 {
		selected[left], selected[right] = selected[right], selected[left]
	}
	joined := strings.Join(selected, "\n")
	if len(joined) > 2000 {
		joined = joined[len(joined)-2000:]
	}
	return joined
}

// Returns a constrained process environment.
//
// Make and Git control variables are dropped so an exported MAKEFLAGS or GIT_DIR
// cannot change what a consumer executes, and only reviewed overrides return.
func consumerEnvironment(overrides []string) []string {
	denied := map[string]bool{
		"MAKE": true, "MAKEFLAGS": true, "MFLAGS": true, "GNUMAKEFLAGS": true,
		"MAKEOVERRIDES": true, "MAKEFILES": true, "MAKELEVEL": true, "BICEP": true,
		"GIT_DIR": true, "GIT_WORK_TREE": true, "GIT_INDEX_FILE": true,
		"GIT_OBJECT_DIRECTORY": true, "GIT_ALTERNATE_OBJECT_DIRECTORIES": true,
		"GIT_NAMESPACE": true, "GIT_COMMON_DIR": true, "GIT_CONFIG": true,
		"GIT_CONFIG_PARAMETERS": true, "GIT_CONFIG_COUNT": true,
	}
	var inherited []string
	for _, item := range os.Environ() {
		name, _, _ := strings.Cut(item, "=")
		if denied[name] {
			continue
		}
		inherited = append(inherited, item)
	}
	return mergeEnvironment(inherited, overrides)
}

// Applies explicit overrides after the inherited environment,
// replacing any inherited value for the same name.
func mergeEnvironment(inherited, overrides []string) []string {
	names := make([]string, 0, len(overrides))
	replacement := make(map[string]string, len(overrides))
	for _, item := range overrides {
		name, value, _ := strings.Cut(item, "=")
		names = append(names, name)
		replacement[name] = value
	}
	drop := map[string]bool{}
	for _, name := range names {
		drop[name] = true
	}
	sort.Strings(names)
	environment := make([]string, 0, len(inherited)+len(overrides))
	for _, item := range inherited {
		name, _, _ := strings.Cut(item, "=")
		if drop[name] {
			continue
		}
		environment = append(environment, item)
	}
	for _, name := range names {
		environment = append(environment, name+"="+replacement[name])
	}
	return environment
}

type boundedBuffer struct {
	limit     int64
	buffer    []byte
	truncated int64
}

// Write appends output up to the limit and counts what was dropped.
func (b *boundedBuffer) Write(p []byte) (int, error) {
	remaining := b.limit - int64(len(b.buffer))
	if remaining <= 0 {
		b.truncated += int64(len(p))
		return len(p), nil
	}
	if int64(len(p)) <= remaining {
		b.buffer = append(b.buffer, p...)
		return len(p), nil
	}
	b.buffer = append(b.buffer, p[:remaining]...)
	b.truncated += int64(len(p)) - remaining
	return len(p), nil
}

// Bytes returns the captured output.
func (b *boundedBuffer) Bytes() []byte { return b.buffer }

// Returns the sha256 digest of the captured bytes.
func digestBytes(content []byte) string {
	sum := sha256.Sum256(content)
	return "sha256:" + hex.EncodeToString(sum[:])
}
