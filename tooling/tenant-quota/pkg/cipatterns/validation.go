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

package cipatterns

import (
	"fmt"
	"reflect"
	"time"

	"github.com/Azure/ARO-HCP/tooling/cifailurepatterns"
)

func validateInput(i input, origin, build string) error {
	o := i.observation
	if o.Origin != origin || (build != "" && o.BuildID != build) {
		return fmt.Errorf("outside requested scope")
	}
	variants := o.NameVariants
	if variants < 0 {
		return fmt.Errorf("invalid name variants")
	}
	if (variants == 0 && i.nameStatus != "missing") || (variants == 1 && i.nameStatus != "unique") || (variants > 1 && i.nameStatus != "conflicting") || (variants != 1 && o.TestName != "") {
		return fmt.Errorf("inconsistent name enrichment")
	}
	return nil
}

func validateInputPopulation(inputs []input) error {
	if len(inputs) > maxReadRows {
		return fmt.Errorf("input population exceeds %d rows", maxReadRows)
	}
	var size int
	for _, i := range inputs {
		if i.multiplicity <= 0 {
			return fmt.Errorf("input multiplicity must be positive")
		}
		if i.observation.Origin == "" || i.observation.BuildID == "" {
			return fmt.Errorf("input origin/build ID is missing")
		}
		size += len(i.observation.Text) + len(i.observation.TestName) + len(i.observation.Source) + len(i.observation.TestID) + len(i.observation.BuildID) + len(i.observation.Origin) + 256
		if size > maxReadBytes {
			return fmt.Errorf("input population exceeds %d bytes", maxReadBytes)
		}
	}
	return nil
}

// validatedVisibleResults rejects partial envelopes and contradictory retry copies before
// any missing work is submitted. Diagnostic timestamps/commits do not define identity.
func validatedVisibleResults(inputs []input, version failurePatternVersion, rows []resultRow) (map[string]resultRow, error) {
	if len(rows) > maxReadRows {
		return nil, fmt.Errorf("result population exceeds %d rows", maxReadRows)
	}
	expected := make(map[string]input, len(inputs))
	for _, i := range inputs {
		i = prepareInput(i, version)
		expected[i.key] = i
	}
	visible := make(map[string]resultRow, len(rows))
	for _, row := range rows {
		want, ok := expected[row.ResultKey]
		if !ok {
			return nil, fmt.Errorf("unexpected result key %q", row.ResultKey)
		}
		if !matchesEnvelope(row, want, version) {
			return nil, fmt.Errorf("result %s envelope does not match raw input/target", row.ResultKey)
		}
		if row.ComputedAt.IsZero() {
			return nil, fmt.Errorf("result %s missing computedAt", row.ResultKey)
		}
		if err := validateFindings(row.Findings, row.Message); err != nil {
			return nil, fmt.Errorf("result %s: %w", row.ResultKey, err)
		}
		if previous, exists := visible[row.ResultKey]; exists && !reflect.DeepEqual(previous.Findings, row.Findings) {
			return nil, fmt.Errorf("conflicting findings for result %s", row.ResultKey)
		}
		visible[row.ResultKey] = row
	}
	return visible, nil
}

func matchesEnvelope(row resultRow, i input, version failurePatternVersion) bool {
	o := i.observation
	return row.ResultKey == i.key && row.RawFingerprint == o.Fingerprint &&
		row.ExtractorVersion == version.extractorVersion && row.ResultSchemaVersion == version.resultSchemaVersion &&
		row.Origin == o.Origin && row.BuildID == o.BuildID && row.Source == o.Source &&
		row.TestID == o.TestID && row.Message == o.Text && row.TestName == o.TestName &&
		matchesTime(row.StartedAt, o.StartedAt) && matchesTime(row.FinishedAt, o.FinishedAt) &&
		matchesTime(row.SetupFinishTime, o.SetupFinishTime) && matchesTime(row.TestStartTime, o.TestStartTime) &&
		matchesTime(row.CleanupStartTime, o.CleanupStartTime)
}

func matchesTime(stored *time.Time, raw time.Time) bool {
	if stored == nil {
		return raw.IsZero()
	}
	return stored.Equal(raw)
}

func validateFindings(findings []cifailurepatterns.Finding, text string) error {
	if len(findings) == 0 {
		return fmt.Errorf("findings must be a nonempty array")
	}
	for _, finding := range findings {
		if finding.Canonical == "" || finding.Key == "" || finding.Outcome == "" || finding.RuleID == "" {
			return fmt.Errorf("incomplete finding")
		}
		switch finding.Outcome {
		case "matched", "fallback", "missing_evidence":
		default:
			return fmt.Errorf("unknown finding outcome %q", finding.Outcome)
		}
		for _, location := range finding.Locations {
			if location.Start < 0 || location.End <= location.Start || location.End > len(text) {
				return fmt.Errorf("finding location is outside raw bytes")
			}
		}
		if finding.Subject != nil && (finding.Subject.Kind == "" || finding.Subject.Name == "") {
			return fmt.Errorf("incomplete finding subject")
		}
	}
	return nil
}
