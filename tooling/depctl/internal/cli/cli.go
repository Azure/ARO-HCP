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

// Package cli holds the helpers shared by the depctl commands.
package cli

import (
	"errors"
	"fmt"
	"io"
	"os"
	"sort"
	"strings"

	"github.com/spf13/cobra"

	"github.com/Azure/ARO-HCP/tooling/depctl/internal/orchestration"
	"github.com/Azure/ARO-HCP/tooling/depctl/internal/schema"
	"github.com/Azure/ARO-HCP/tooling/depctl/internal/workspace"
	"github.com/Azure/ARO-HCP/tooling/depctl/pkg/depctl"
)

// StringMapFlag collects repeated key=value flags.
type StringMapFlag map[string]string

// String renders the flag values for help output.
func (m StringMapFlag) String() string {
	keys := make([]string, 0, len(m))
	for key := range m {
		keys = append(keys, key+"="+m[key])
	}
	sort.Strings(keys)
	return fmt.Sprint(keys)
}

// Set parses one key=value pair.
func (m StringMapFlag) Set(value string) error {
	key, item, found := strings.Cut(value, "=")
	if !found || key == "" || item == "" {
		return fmt.Errorf("expected KEY=VALUE, got %q", value)
	}
	m[key] = item
	return nil
}

// Type reports the flag type.
func (m StringMapFlag) Type() string { return "key=value" }

// ReadRecord decodes one bounded versioned record file.
func ReadRecord(path, kind string, target any) error {
	if path == "" {
		return errors.New("a record path is required")
	}
	file, err := os.Open(path)
	if err != nil {
		return err
	}
	defer file.Close()
	content, err := io.ReadAll(io.LimitReader(file, schema.MaxRecordBytes+1))
	if err != nil {
		return err
	}
	if len(content) > schema.MaxRecordBytes {
		return fmt.Errorf("record exceeds %d bytes", schema.MaxRecordBytes)
	}
	return schema.DecodeEnvelope(content, kind, target)
}

// Invalid wraps an input error with the invalid-input exit code.
func Invalid(err error) error {
	if err == nil {
		return nil
	}
	var exit *depctl.ExitError
	if errors.As(err, &exit) {
		return err
	}
	return depctl.Errorf(depctl.ExitInvalid, "%v", err)
}

// Incomplete wraps an error with the incomplete-evidence exit code.
func Incomplete(format string, args ...any) error {
	return depctl.Errorf(depctl.ExitIncomplete, format, args...)
}

// Validation wraps an error with the failed-validation exit code.
func Validation(err error) error {
	return ExitWith(depctl.ExitValidation, err)
}

// Stale wraps an error with the stale-input exit code.
func Stale(err error) error {
	return ExitWith(depctl.ExitStale, err)
}

// Internal wraps an error with the internal-failure exit code.
func Internal(err error) error {
	return ExitWith(depctl.ExitInternal, err)
}

// ExitWith wraps an error with an explicit exit code.
func ExitWith(code int, err error) error {
	return &depctl.ExitError{Code: code, Message: err.Error()}
}

// MapEngineError maps an orchestration error onto the documented exit codes.
func MapEngineError(err error) error {
	switch {
	case errors.Is(err, orchestration.ErrStale), errors.Is(err, orchestration.ErrCandidateDrift),
		errors.Is(err, workspace.ErrCandidateMismatch):
		return Stale(err)
	case errors.Is(err, orchestration.ErrPlanRejected):
		return Incomplete("%v", err)
	case errors.Is(err, orchestration.ErrGeneration):
		return Validation(err)
	default:
		// An unclassified failure is an internal fault, not bad input.
		return Internal(err)
	}
}

// NoArgs reports unexpected positional arguments as invalid input.
func NoArgs(_ *cobra.Command, args []string) error {
	if len(args) > 0 {
		return Invalid(fmt.Errorf("unexpected arguments: %s", strings.Join(args, " ")))
	}
	return nil
}

// ToolPaths builds the per-phase executable selection for a validation run.
func ToolPaths(baseline, candidate map[string]string) orchestration.ToolPaths {
	return orchestration.ToolPaths{Baseline: baseline, Candidate: candidate}
}

// WriteJSON prints one versioned record on stdout.
func WriteJSON(writer io.Writer, record any) error {
	encoded, err := schema.Canonical(orchestration.NormalizeRecord(record))
	if err != nil {
		return err
	}
	_, err = writer.Write(append(encoded, '\n'))
	return err
}

// EmitRecord writes a record to a file, or prints it when no file is requested.
func EmitRecord(writer io.Writer, path string, record any) error {
	if path != "" {
		return orchestration.WriteRecord(path, record)
	}
	return WriteJSON(writer, record)
}
