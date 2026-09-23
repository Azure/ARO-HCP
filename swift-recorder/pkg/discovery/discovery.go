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

// Package discovery extracts pod sandbox ADD attempts from the local CNI log.
package discovery

import (
	"bytes"
	"context"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strings"
	"syscall"
	"time"
)

const (
	pollInterval  = 100 * time.Millisecond
	rotationDrain = 500 * time.Millisecond
	maxLineBytes  = 256 * 1024
	maxPollBytes  = 1024 * 1024
)

// Attempt contains only the allowlisted identity and timing of an ADD command.
// NetNS is untrusted input: the caller must validate it before use.
type Attempt struct {
	PodUID     string
	PodName    string
	Namespace  string
	SandboxID  string
	NetNS      string
	ObservedAt time.Time
	// SourceTime is zero when ts is absent or not a recognized timestamp.
	SourceTime time.Time
}

// Tail polls the active log at path until cancellation or a filesystem error.
// Existing input is skipped at startup. A missing file is normal, and its first
// creation (like a rotation replacement) is read from the beginning. Readiness
// is signaled once after initialization, even if the path does not yet exist.
// Lines over 256 KiB and malformed records are silently discarded, never logged.
// No cursor survives Tail returning. Copytruncate detection is best effort:
// truncation followed by regrowth past the cursor between polls can be missed.
//
// Callbacks may be nil. They run serially on the calling goroutine, must return
// promptly, and must not panic; Tail does not spawn goroutines or recover panics.
// Cancellation closes the descriptor and returns ctx.Err().
func Tail(ctx context.Context, path string, onAttempt func(Attempt), onReady func()) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	input, err := openLog(path, true)
	if err != nil {
		return err
	}
	defer func() {
		if input != nil {
			_ = input.file.Close()
		}
	}()
	ticker := time.NewTicker(pollInterval)
	defer ticker.Stop()
	if onReady != nil {
		onReady()
	}
	var rotatedAt time.Time
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		if input != nil {
			active, err := os.Stat(path)
			if err != nil && !os.IsNotExist(err) {
				return fmt.Errorf("stat CNI log: %w", err)
			}
			if rotatedAt.IsZero() && (os.IsNotExist(err) || !os.SameFile(input.info, active)) {
				rotatedAt = time.Now()
			}
			if err := input.readPoll(ctx, onAttempt); err != nil {
				return err
			}
			// Allow writers holding the renamed inode a short window to finish.
			if !rotatedAt.IsZero() && time.Since(rotatedAt) >= rotationDrain {
				if err := input.file.Close(); err != nil {
					return fmt.Errorf("close rotated CNI log: %w", err)
				}
				input = nil
				rotatedAt = time.Time{}
			}
		}
		if input == nil {
			input, err = openLog(path, false)
			if err != nil {
				return err
			}
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
		}
	}
}

type logInput struct {
	file    *os.File
	info    os.FileInfo
	offset  int64
	line    []byte
	discard bool
}

func openLog(path string, fromEnd bool) (*logInput, error) {
	// Nonblocking open prevents an unexpected FIFO from hanging initialization.
	file, err := os.OpenFile(path, os.O_RDONLY|syscall.O_NONBLOCK, 0)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("open CNI log: %w", err)
	}
	info, err := file.Stat()
	if err != nil {
		_ = file.Close()
		return nil, fmt.Errorf("stat open CNI log: %w", err)
	}
	if !info.Mode().IsRegular() {
		_ = file.Close()
		return nil, fmt.Errorf("CNI log must be a regular file")
	}
	input := &logInput{file: file, info: info, line: make([]byte, 0, maxLineBytes)}
	if fromEnd {
		input.offset, err = file.Seek(0, io.SeekEnd)
		if err == nil && input.offset > 0 {
			// Do not interpret the appended suffix of a pre-start partial line.
			var last [1]byte
			_, err = file.ReadAt(last[:], input.offset-1)
			input.discard = last[0] != '\n'
			if err == io.EOF {
				// A concurrent copytruncate is handled by the next size check.
				err = nil
				input.discard = false
			}
		}
		if err != nil {
			_ = file.Close()
			return nil, fmt.Errorf("seek initial CNI log: %w", err)
		}
	}
	return input, nil
}

func (input *logInput) readPoll(ctx context.Context, onAttempt func(Attempt)) error {
	info, err := input.file.Stat()
	if err != nil {
		return fmt.Errorf("stat open CNI log: %w", err)
	}
	if info.Size() < input.offset {
		if _, err := input.file.Seek(0, io.SeekStart); err != nil {
			return fmt.Errorf("rewind truncated CNI log: %w", err)
		}
		input.offset = 0
		input.line = input.line[:0]
		input.discard = false
	}
	var buf [32 * 1024]byte
	for total := 0; total < maxPollBytes; {
		if err := ctx.Err(); err != nil {
			return err
		}
		n, readErr := input.file.Read(buf[:])
		input.offset += int64(n)
		total += n
		for remaining := buf[:n]; len(remaining) > 0; {
			if err := ctx.Err(); err != nil {
				return err
			}
			end := bytes.IndexByte(remaining, '\n')
			part := remaining
			if end >= 0 {
				part = remaining[:end]
			}
			if !input.discard {
				if len(input.line)+len(part) > maxLineBytes {
					input.line = input.line[:0]
					input.discard = true
				} else {
					input.line = append(input.line, part...)
				}
			}
			if end < 0 {
				break
			}
			if !input.discard && onAttempt != nil {
				if attempt, ok := parseAttempt(input.line); ok {
					onAttempt(attempt)
				}
			}
			input.line = input.line[:0]
			input.discard = false
			remaining = remaining[end+1:]
		}
		if readErr != nil && readErr != io.EOF {
			return fmt.Errorf("read CNI log: %w", readErr)
		}
		if readErr == io.EOF || n == 0 {
			break
		}
	}
	return nil
}

func parseAttempt(line []byte) (Attempt, bool) {
	var record struct {
		Message     string          `json:"msg"`
		ContainerID string          `json:"containerId"`
		NetNS       string          `json:"netNS"`
		Args        string          `json:"args"`
		Timestamp   json.RawMessage `json:"ts"`
	}
	if len(line) > maxLineBytes || json.Unmarshal(line, &record) != nil || record.Message != "Processing ADD command" {
		return Attempt{}, false
	}
	if len(record.ContainerID) != 64 || strings.TrimSpace(record.NetNS) == "" {
		return Attempt{}, false
	}
	if _, err := hex.DecodeString(record.ContainerID); err != nil {
		return Attempt{}, false
	}
	attempt := Attempt{SandboxID: record.ContainerID, NetNS: record.NetNS}
	for arg := range strings.SplitSeq(record.Args, ";") {
		if arg == "" {
			continue
		}
		key, value, ok := strings.Cut(arg, "=")
		if !ok || key == "" {
			return Attempt{}, false
		}
		var target *string
		switch key {
		case "K8S_POD_UID":
			target = &attempt.PodUID
		case "K8S_POD_NAME":
			target = &attempt.PodName
		case "K8S_POD_NAMESPACE":
			target = &attempt.Namespace
		default:
			continue
		}
		if *target != "" || strings.TrimSpace(value) == "" {
			return Attempt{}, false
		}
		// A substring would retain the full CNI_ARGS backing string.
		*target = strings.Clone(value)
	}
	if attempt.PodUID == "" || attempt.PodName == "" || attempt.Namespace == "" {
		return Attempt{}, false
	}
	var timestamp string
	if json.Unmarshal(record.Timestamp, &timestamp) == nil {
		for _, layout := range []string{time.RFC3339Nano, "2006-01-02T15:04:05.999999999Z0700"} {
			if parsed, err := time.Parse(layout, timestamp); err == nil {
				attempt.SourceTime = parsed.UTC()
				break
			}
		}
	}
	attempt.ObservedAt = time.Now()
	return attempt, true
}
