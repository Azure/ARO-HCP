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

// Package state persists run intent and observations.
//
// The journal is written before an external effect and read after one, so an
// at-least-once invocation can reconcile against remote truth. It provides no
// distributed mutual exclusion: the lock below only coordinates processes on
// this host.
package state

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"time"
)

// Event kinds.
const (
	EventIntent   = "intent"
	EventObserved = "observed"
	EventDecision = "decision"
)

// Event is one journal entry.
type Event struct {
	Time   string         `json:"time"`
	RunID  string         `json:"runID"`
	Phase  string         `json:"phase"`
	Kind   string         `json:"kind"`
	Action string         `json:"action"`
	Detail map[string]any `json:"detail,omitempty"`
}

// Journal is a directory holding an append-only journal and named state files.
type Journal struct {
	directory string
}

// MaxJournalBytes bounds how much journal a reader will load.
const MaxJournalBytes = 32 << 20

// Open creates the journal directory when needed.
func Open(directory string) (*Journal, error) {
	absolute, err := filepath.Abs(directory)
	if err != nil {
		return nil, fmt.Errorf("resolve state directory: %w", err)
	}
	if err := os.MkdirAll(absolute, 0o755); err != nil {
		return nil, fmt.Errorf("create state directory: %w", err)
	}
	return &Journal{directory: absolute}, nil
}

// Directory returns the journal directory.
func (j *Journal) Directory() string { return j.directory }

// Append writes one event with fsync before returning.
func (j *Journal) Append(event Event, clock func() time.Time) error {
	if clock == nil {
		clock = time.Now
	}
	if event.Time == "" {
		event.Time = clock().UTC().Format(time.RFC3339Nano)
	}
	encoded, err := json.Marshal(event)
	if err != nil {
		return fmt.Errorf("encode journal event: %w", err)
	}
	path := filepath.Join(j.directory, "journal.jsonl")
	file, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return fmt.Errorf("open journal: %w", err)
	}
	defer file.Close()
	if _, err := file.Write(append(encoded, '\n')); err != nil {
		return fmt.Errorf("append journal: %w", err)
	}
	return file.Sync()
}

// Read returns all journal events in order.
func (j *Journal) Read() ([]Event, error) {
	path := filepath.Join(j.directory, "journal.jsonl")
	info, err := os.Stat(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("stat journal: %w", err)
	}
	if info.Size() > MaxJournalBytes {
		return nil, fmt.Errorf("journal exceeds %d bytes", MaxJournalBytes)
	}
	content, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read journal: %w", err)
	}
	var events []Event
	for _, line := range splitLines(content) {
		if len(line) == 0 {
			continue
		}
		var event Event
		if err := json.Unmarshal(line, &event); err != nil {
			return nil, fmt.Errorf("decode journal event: %w", err)
		}
		events = append(events, event)
	}
	return events, nil
}

// Splits journal content into lines.
func splitLines(content []byte) [][]byte {
	var lines [][]byte
	start := 0
	for index, character := range content {
		if character == '\n' {
			lines = append(lines, content[start:index])
			start = index + 1
		}
	}
	if start < len(content) {
		lines = append(lines, content[start:])
	}
	return lines
}

// SaveState atomically stores one named state document.
func (j *Journal) SaveState(name string, value any) error {
	encoded, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return fmt.Errorf("encode state %s: %w", name, err)
	}
	path := filepath.Join(j.directory, name+".json")
	temporary, err := os.CreateTemp(j.directory, ".state-")
	if err != nil {
		return fmt.Errorf("create state file: %w", err)
	}
	defer func() { _ = os.Remove(temporary.Name()) }()
	if _, err := temporary.Write(append(encoded, '\n')); err != nil {
		temporary.Close()
		return fmt.Errorf("write state %s: %w", name, err)
	}
	if err := temporary.Sync(); err != nil {
		temporary.Close()
		return fmt.Errorf("sync state %s: %w", name, err)
	}
	if err := temporary.Close(); err != nil {
		return fmt.Errorf("close state %s: %w", name, err)
	}
	return os.Rename(temporary.Name(), path)
}

// LoadState reads one named state document; found reports existence.
func (j *Journal) LoadState(name string, target any) (bool, error) {
	content, err := os.ReadFile(filepath.Join(j.directory, name+".json"))
	if errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("read state %s: %w", name, err)
	}
	if err := json.Unmarshal(content, target); err != nil {
		return false, fmt.Errorf("decode state %s: %w", name, err)
	}
	return true, nil
}

// Lock takes the single-host journal lock. The returned function releases it.
//
// The lock is deliberately not presented as a shared lease: it cannot coordinate
// independent hosts, so a live publisher must additionally be restricted to one
// holder by the execution integration.
func (j *Journal) Lock() (func(), error) {
	path := filepath.Join(j.directory, "lock")
	file, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o644)
	if err != nil {
		if errors.Is(err, os.ErrExist) {
			owner, _ := os.ReadFile(path)
			return nil, fmt.Errorf("state directory is locked by pid %s", string(owner))
		}
		return nil, fmt.Errorf("take journal lock: %w", err)
	}
	if _, err := file.WriteString(strconv.Itoa(os.Getpid())); err != nil {
		file.Close()
		_ = os.Remove(path)
		return nil, fmt.Errorf("write journal lock: %w", err)
	}
	if err := file.Close(); err != nil {
		_ = os.Remove(path)
		return nil, fmt.Errorf("close journal lock: %w", err)
	}
	return func() { _ = os.Remove(path) }, nil
}

// NewRunID returns a sortable attempt identifier.
func NewRunID(clock func() time.Time) string {
	if clock == nil {
		clock = time.Now
	}
	return clock().UTC().Format("20060102T150405.000000000Z")
}

// SortStrings returns a sorted copy, used for stable journal details.
func SortStrings(values []string) []string {
	copied := append([]string{}, values...)
	sort.Strings(copied)
	return copied
}
