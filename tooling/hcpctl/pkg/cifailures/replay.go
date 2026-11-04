// Copyright 2026 Microsoft Corporation
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://www.apache.org/licenses/LICENSE-2.0
// Unless required by applicable law or agreed to in writing, software distributed
// under the License is distributed on an "AS IS" BASIS, WITHOUT WARRANTIES OR
// CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package cifailures

import (
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"
	"unicode/utf8"
)

// Fingerprint uses a versioned ordered sequence of UTF-8 byte strings, each
// prefixed with its unsigned 64-bit big-endian byte length. Empty and absent
// timestamps use Go's zero timestamp; nonzero timestamps are canonical UTC.
// No joined test name, extractor output, environment, window, or sample
// participates in identity: test ID remains stable when name enrichment arrives.
func Fingerprint(o Observation) string {
	h := sha256.New()
	var size [8]byte
	fields := [...]string{
		"cifailures-observation-v1", o.Origin, o.BuildID, o.Source, o.TestID,
		o.StartedAt.UTC().Format(time.RFC3339Nano), o.FinishedAt.UTC().Format(time.RFC3339Nano),
		o.SetupFinishTime.UTC().Format(time.RFC3339Nano), o.TestStartTime.UTC().Format(time.RFC3339Nano),
		o.CleanupStartTime.UTC().Format(time.RFC3339Nano), o.Text,
	}
	for _, field := range fields {
		binary.BigEndian.PutUint64(size[:], uint64(len(field)))
		_, _ = h.Write(size[:])
		_, _ = io.WriteString(h, field)
	}
	return "sha256:v1:" + hex.EncodeToString(h.Sum(nil))
}

func (m Metadata) Validate() error {
	if m.Origin == "" {
		return fmt.Errorf("origin is required")
	}
	if m.Window.From.IsZero() || m.Window.To.IsZero() || !m.Window.From.Before(m.Window.To) {
		return fmt.Errorf("window must have nonzero from < to")
	}
	return nil
}

func (b Bundle) Validate() error {
	if b.SchemaVersion != 1 && b.SchemaVersion != BundleSchemaVersion {
		return fmt.Errorf("unsupported replay schema_version %d (supported: 1, %d)", b.SchemaVersion, BundleSchemaVersion)
	}
	if err := b.Metadata.Validate(); err != nil {
		return fmt.Errorf("invalid replay metadata: %w", err)
	}
	if b.SchemaVersion == 1 && b.Metadata.Environment == "" {
		return fmt.Errorf("schema 1 replay requires its recorded environment")
	}
	runs := make(map[string]string, len(b.Runs))
	for _, run := range b.Runs {
		if run.BuildID == "" {
			return fmt.Errorf("replay run has empty build_id")
		}
		if _, exists := runs[run.BuildID]; exists {
			return fmt.Errorf("replay contains duplicate run %q", run.BuildID)
		}
		if run.FinishedAt.Before(b.Metadata.Window.From) || !run.FinishedAt.Before(b.Metadata.Window.To) {
			return fmt.Errorf("run %q finished_at is outside [from,to)", run.BuildID)
		}
		if !strings.Contains(run.JobName, b.Metadata.Filters.JobFilter) {
			return fmt.Errorf("run %q does not match saved job_filter", run.BuildID)
		}
		environment := run.Environment
		if b.SchemaVersion == 1 {
			environment = b.Metadata.Environment
		}
		if environment == "" || (b.Metadata.Environment != "" && environment != b.Metadata.Environment) {
			return fmt.Errorf("run %q environment differs from saved selection", run.BuildID)
		}
		runs[run.BuildID] = environment
	}
	for i, observation := range b.Observations {
		if _, exists := runs[observation.BuildID]; !exists {
			return fmt.Errorf("observation %d references unknown run %q", i, observation.BuildID)
		}
		if observation.Origin != b.Metadata.Origin || observation.Environment != runs[observation.BuildID] {
			return fmt.Errorf("observation %d origin/environment differs from its saved run", i)
		}
		if b.Metadata.Filters.Source != "" && observation.Source != b.Metadata.Filters.Source {
			return fmt.Errorf("observation %d does not match saved source filter", i)
		}
		if !utf8.ValidString(observation.Text) {
			return fmt.Errorf("observation %d contains invalid UTF-8 raw text", i)
		}
		if observation.Fingerprint != Fingerprint(observation) {
			return fmt.Errorf("observation %d fingerprint does not match exact saved input", i)
		}
	}
	return nil
}

func ReadBundle(reader io.Reader) (Bundle, error) {
	var bundle Bundle
	decoder := json.NewDecoder(reader)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&bundle); err != nil {
		return Bundle{}, fmt.Errorf("decode replay bundle: %w", err)
	}
	var extra any
	if err := decoder.Decode(&extra); err != io.EOF {
		return Bundle{}, fmt.Errorf("replay bundle must contain exactly one JSON object")
	}
	if err := bundle.Validate(); err != nil {
		return Bundle{}, err
	}
	return bundle, nil
}

// SaveBundle publishes a complete private file atomically, refusing to replace
// an existing file (including a symlink). Raw messages may contain secrets.
func SaveBundle(path string, bundle Bundle) error {
	if path == "" || path == "-" {
		return fmt.Errorf("save-input requires a file path, not stdout")
	}
	if err := bundle.Validate(); err != nil {
		return err
	}
	file, err := os.CreateTemp(filepath.Dir(path), ".failure-patterns-*.json")
	if err != nil {
		return fmt.Errorf("create private replay temporary file: %w", err)
	}
	defer func() { _ = os.Remove(file.Name()) }()
	defer func() { _ = file.Close() }()
	if err := json.NewEncoder(file).Encode(bundle); err != nil {
		return fmt.Errorf("encode replay bundle: %w", err)
	}
	if err := file.Sync(); err != nil {
		return fmt.Errorf("sync replay bundle: %w", err)
	}
	if err := file.Close(); err != nil {
		return fmt.Errorf("close replay bundle: %w", err)
	}
	if err := os.Link(file.Name(), path); err != nil {
		return fmt.Errorf("publish replay bundle without overwriting %q: %w", path, err)
	}
	return nil
}
