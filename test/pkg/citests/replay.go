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

package citests

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"unicode/utf8"
)

// Validate requires a complete, usable collection scope.
func (s Selection) Validate() error {
	if s.Origin == "" {
		return fmt.Errorf("selection origin is required")
	}
	if s.From.IsZero() || s.To.IsZero() || !s.From.Before(s.To) {
		return fmt.Errorf("selection window must have nonzero from < to")
	}
	switch s.Environment {
	case "", EnvironmentDev, EnvironmentInt, EnvironmentStg, EnvironmentProd, EnvironmentUnknown:
	default:
		return fmt.Errorf("selection environment must be dev, int, stg, prod, unknown or empty")
	}
	return nil
}

// Validate rejects a bundle that cannot be scored deterministically: unknown
// versions, duplicate run identities, unknown builds and out-of-scope facts.
func (b Bundle) Validate() error {
	if b.SchemaVersion != BundleSchemaVersion {
		return fmt.Errorf("unsupported observations schema_version %d (supported: %d)", b.SchemaVersion, BundleSchemaVersion)
	}
	if err := b.Selection.Validate(); err != nil {
		return fmt.Errorf("invalid observations selection: %w", err)
	}
	environments := make(map[string]string, len(b.Runs))
	for i, run := range b.Runs {
		if run.BuildID == "" {
			return fmt.Errorf("run %d has an empty build_id", i)
		}
		if _, ok := environments[run.BuildID]; ok {
			return fmt.Errorf("duplicate run %q", run.BuildID)
		}
		if run.FinishedAt.Before(b.Selection.From) || !run.FinishedAt.Before(b.Selection.To) {
			return fmt.Errorf("run %q finished_at is outside the recorded selection window", run.BuildID)
		}
		if !strings.Contains(run.JobName, b.Selection.JobFilter) {
			return fmt.Errorf("run %q does not match the recorded job_filter", run.BuildID)
		}
		switch run.Environment {
		case EnvironmentDev, EnvironmentInt, EnvironmentStg, EnvironmentProd, EnvironmentUnknown:
		default:
			return fmt.Errorf("run %q has unknown environment %q", run.BuildID, run.Environment)
		}
		if b.Selection.Environment != "" && run.Environment != b.Selection.Environment {
			return fmt.Errorf("run %q environment differs from the recorded selection", run.BuildID)
		}
		environments[run.BuildID] = run.Environment
	}
	for i, observation := range b.Observations {
		if _, ok := environments[observation.BuildID]; !ok {
			return fmt.Errorf("observation %d references unknown run %q", i, observation.BuildID)
		}
		if observation.TestID == "" {
			return fmt.Errorf("observation %d has an empty test_id", i)
		}
		if observation.Source == "" {
			return fmt.Errorf("observation %d has an empty source", i)
		}
		if observation.NameVariants < 0 {
			return fmt.Errorf("observation %d has a negative name variant count", i)
		}
		if observation.NameVariants == 1 && observation.Name == "" {
			return fmt.Errorf("observation %d claims one name variant but has no name", i)
		}
		if !utf8.ValidString(observation.Name) {
			return fmt.Errorf("observation %d contains an invalid UTF-8 name", i)
		}
	}
	return nil
}

// ReadBundle decodes exactly one observations object and rejects unknown fields,
// trailing data and any bundle that fails validation.
func ReadBundle(reader io.Reader) (Bundle, error) {
	var bundle Bundle
	decoder := json.NewDecoder(reader)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&bundle); err != nil {
		return Bundle{}, fmt.Errorf("decode observations bundle: %w", err)
	}
	var extra any
	if err := decoder.Decode(&extra); err != io.EOF {
		return Bundle{}, fmt.Errorf("observations bundle must contain exactly one JSON object")
	}
	if err := bundle.Validate(); err != nil {
		return Bundle{}, err
	}
	return bundle, nil
}

// SaveBundle publishes a complete observations file atomically, refusing to
// replace an existing file (including a symlink).
func SaveBundle(path string, bundle Bundle) error {
	if path == "" || path == "-" {
		return fmt.Errorf("save-input requires a file path, not stdout")
	}
	if err := bundle.Validate(); err != nil {
		return err
	}
	file, err := os.CreateTemp(filepath.Dir(path), ".test-priorities-*.json")
	if err != nil {
		return fmt.Errorf("create private observations temporary file: %w", err)
	}
	defer func() { _ = os.Remove(file.Name()) }()
	defer func() { _ = file.Close() }()
	if err := json.NewEncoder(file).Encode(bundle); err != nil {
		return fmt.Errorf("encode observations bundle: %w", err)
	}
	if err := file.Sync(); err != nil {
		return fmt.Errorf("sync observations bundle: %w", err)
	}
	if err := file.Close(); err != nil {
		return fmt.Errorf("close observations bundle: %w", err)
	}
	if err := os.Link(file.Name(), path); err != nil {
		return fmt.Errorf("publish observations bundle without overwriting %q: %w", path, err)
	}
	return nil
}
