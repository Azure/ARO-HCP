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

// Package capture collects read-only network namespace diagnostics in a child process.
package capture

import (
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"time"
)

const snapshotTimeout = 500 * time.Millisecond

// Snapshot runs the hidden capture command with a deadline no later than 500 ms.
// Stdout and stderr are independently bounded by maxBytes. Oversized or invalid
// JSON output is rejected, never returned as a partial snapshot.
func Snapshot(ctx context.Context, executable, path string, maxBytes int) (json.RawMessage, error) {
	if maxBytes <= 0 {
		return nil, fmt.Errorf("capture output limit must be positive")
	}
	ctx, cancel := context.WithTimeout(ctx, snapshotTimeout)
	defer cancel()
	stdout := boundedBuffer{limit: maxBytes, cancel: cancel}
	stderr := boundedBuffer{limit: maxBytes, cancel: cancel}
	cmd := exec.CommandContext(ctx, executable, "capture", "--path", path)
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	// Do not wait indefinitely for inherited pipe descriptors after child exit.
	cmd.WaitDelay = 10 * time.Millisecond
	err := cmd.Run()
	if stdout.exceeded || stderr.exceeded {
		return nil, fmt.Errorf("capture output exceeded %d bytes", maxBytes)
	}
	if ctx.Err() != nil {
		return nil, fmt.Errorf("capture canceled: %w", ctx.Err())
	}
	if err != nil {
		return nil, fmt.Errorf("capture failed: %w (stderr: %q)", err, stderr.data)
	}
	if !json.Valid(stdout.data) {
		return nil, fmt.Errorf("capture returned invalid JSON")
	}
	return json.RawMessage(stdout.data), nil
}

type boundedBuffer struct {
	data     []byte
	limit    int
	exceeded bool
	cancel   context.CancelFunc
}

func (b *boundedBuffer) Write(p []byte) (int, error) {
	n := min(len(p), b.limit-len(b.data))
	b.data = append(b.data, p[:n]...)
	if n < len(p) {
		b.exceeded = true
		b.cancel()
	}
	return len(p), nil
}
