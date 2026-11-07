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

package model

// Normalize replaces nil slices with empty ones so that a record's JSON shape
// does not depend on whether a collection was populated.
func nonNil[T any](values []T) []T {
	if values == nil {
		return []T{}
	}
	return values
}

// Normalize empties nil collections on the inventory record.
func (r *Inventory) Normalize() {
	r.Errors = nonNil(r.Errors)
	r.Families = nonNil(r.Families)
	for index := range r.Families {
		r.Families[index].ToolNames = nonNil(r.Families[index].ToolNames)
		r.Families[index].Pins = nonNil(r.Families[index].Pins)
	}
}

// Normalize empties nil collections on the plan record.
func (r *Plan) Normalize() {
	r.Families = nonNil(r.Families)
	r.Consumers = nonNil(r.Consumers)
	r.Blockers = nonNil(r.Blockers)
	for index := range r.Families {
		r.Families[index].Targets = nonNil(r.Families[index].Targets)
	}
}

// Normalize empties nil collections on the candidate record.
func (r *Candidate) Normalize() {
	r.ChangedPaths = nonNil(r.ChangedPaths)
	r.Generated = nonNil(r.Generated)
	r.Tools = nonNil(r.Tools)
	if r.FileDigests == nil {
		r.FileDigests = map[string]string{}
	}
}

// Normalize empties nil collections on the validation record.
func (r *Validation) Normalize() {
	r.Results = nonNil(r.Results)
	r.NotRun = nonNil(r.NotRun)
}

// Normalize empties nil collections on the proposal record.
func (r *Proposal) Normalize() {
	r.Labels = nonNil(r.Labels)
	r.Markers = nonNil(r.Markers)
	r.SourceRefs = nonNil(r.SourceRefs)
	r.PermittedOperations = nonNil(r.PermittedOperations)
	r.RiskNotes = nonNil(r.RiskNotes)
}

// Normalize empties nil collections on the publication receipt.
func (r *PublicationReceipt) Normalize() {
	r.Unfinished = nonNil(r.Unfinished)
}

// Normalize empties nil collections on the maintenance decision.
func (r *MaintenanceDecision) Normalize() {
	r.ActiveHolds = nonNil(r.ActiveHolds)
	r.CI.Positive = nonNil(r.CI.Positive)
	r.CI.Blocked = nonNil(r.CI.Blocked)
	r.ReleaseBlockers = nonNil(r.ReleaseBlockers)
	r.RetryBlockers = nonNil(r.RetryBlockers)
	r.RefreshBlockers = nonNil(r.RefreshBlockers)
	r.AllowedActions = nonNil(r.AllowedActions)
	if r.Investigation != nil {
		r.Investigation.Normalize()
	}
}

// Normalize empties nil collections on the investigation record.
func (r *Investigation) Normalize() {
	r.ChangedPaths = nonNil(r.ChangedPaths)
	r.Evidence = nonNil(r.Evidence)
	r.Comparisons = nonNil(r.Comparisons)
	r.Rationale = nonNil(r.Rationale)
	r.Missing = nonNil(r.Missing)
}

// Normalize empties nil collections on the run result.
func (r *RunResult) Normalize() {
	r.Families = nonNil(r.Families)
	r.Blockers = nonNil(r.Blockers)
	r.Errors = nonNil(r.Errors)
	r.Artifacts = nonNil(r.Artifacts)
	r.Actions = nonNil(r.Actions)
}

// Normalize empties nil collections on the evidence manifest.
func (r *EvidenceManifest) Normalize() {
	r.Files = nonNil(r.Files)
	r.Missing = nonNil(r.Missing)
}
