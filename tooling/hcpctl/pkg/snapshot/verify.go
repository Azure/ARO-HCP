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

package snapshot

// VerificationStatus represents the outcome of a verification check on a query.
type VerificationStatus string

const (
	// VerificationPass indicates the query returned results as expected.
	VerificationPass VerificationStatus = "pass"
	// VerificationFail indicates the query was expected to return results but did not.
	VerificationFail VerificationStatus = "fail"
	// VerificationSkipped indicates the query was not executed because prerequisites were not met.
	VerificationSkipped VerificationStatus = "skipped"
)

// VerificationCase records the outcome of a single query's verification check.
type VerificationCase struct {
	// Suite is the grouping key for this case (e.g. "type/name" or "context").
	Suite string
	// Query is the "component/queryName" identifier.
	Query string
	// Category is the query category for display purposes.
	Category string
	// ResourceType is the ARM resource type (e.g. "microsoft.redhatopenshift/hcpopenshiftclusters").
	// Used to produce stable jUnit test identifiers that don't change with resource names.
	ResourceType string
	// Status is the verification outcome.
	Status VerificationStatus
	// Message provides context about the failure or skip reason.
	Message string
	// RenderedKQL is the fully rendered KQL query text, provided so that
	// downstream consumers (e.g. HTML overview) can display it without
	// needing access to the query templates or data.
	RenderedKQL string
}

// VerificationReport collects all verification cases from a gathering run.
type VerificationReport struct {
	Cases []VerificationCase
}

// Failures returns the number of failed verification cases.
func (r *VerificationReport) Failures() int {
	count := 0
	for _, c := range r.Cases {
		if c.Status == VerificationFail {
			count++
		}
	}
	return count
}

// Record adds a verification case to the report.
func (r *VerificationReport) Record(c VerificationCase) {
	r.Cases = append(r.Cases, c)
}
