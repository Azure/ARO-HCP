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
	"bytes"
	"encoding/json"
	"fmt"
	"sort"

	"github.com/Azure/ARO-HCP/tooling/cifailurepatterns"
)

// BuildReport preserves every observation occurrence and its complete findings.
// Results are ordered by the exact observation JSON; finding order is unchanged.
// ExtractorVersion always identifies the shared package's extraction behavior.
func BuildReport(bundle Bundle) (Report, error) {
	if err := bundle.Validate(); err != nil {
		return Report{}, fmt.Errorf("build failure report: %w", err)
	}

	// Cache the complete serialized context once rather than encoding in comparisons.
	observations := make([]struct {
		observation *Observation
		key         []byte
	}, len(bundle.Observations))
	for i := range bundle.Observations {
		observation := &bundle.Observations[i]
		key, err := json.Marshal(observation)
		if err != nil {
			return Report{}, fmt.Errorf("serialize report observation %d: %w", i, err)
		}
		observations[i].observation = observation
		observations[i].key = key
	}
	sort.Slice(observations, func(i, j int) bool {
		return bytes.Compare(observations[i].key, observations[j].key) < 0
	})

	report := Report{
		SchemaVersion:    ReportSchemaVersion,
		ExtractorVersion: cifailurepatterns.Version,
		Metadata:         bundle.Metadata,
		Results:          make([]Result, len(observations)),
	}
	for i, entry := range observations {
		observation := entry.observation
		report.Results[i] = Result{
			Observation: *observation,
			Findings: cifailurepatterns.Extract(cifailurepatterns.Input{
				Text:     observation.Text,
				TestName: observation.TestName,
				Source:   observation.Source,
			}),
		}
	}
	report.Summary, report.Patterns = groupResults(report.Results)
	return report, nil
}
