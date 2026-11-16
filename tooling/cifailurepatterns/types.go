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

package cifailurepatterns

// Input is the semantic evidence for one failure observation.
type Input struct {
	Text     string `json:"text"`
	TestName string `json:"test_name"`
	Source   string `json:"source"`
}

// Finding describes selected evidence, not a verified root cause.
type Finding struct {
	Canonical      string     `json:"canonical"`
	Key            string     `json:"key"`
	Outcome        string     `json:"outcome"`
	RuleID         string     `json:"rule_id"`
	QualityReasons []string   `json:"quality_reasons,omitempty"`
	Provider       string     `json:"provider,omitempty"`
	OuterCode      string     `json:"outer_code,omitempty"`
	DetailCode     string     `json:"detail_code,omitempty"`
	Subject        *Subject   `json:"subject,omitempty"`
	Locations      []Location `json:"locations,omitempty"`
}

// Subject distinguishes a stable alert subject from the names actually observed.
type Subject struct {
	Kind          string   `json:"kind"`
	Name          string   `json:"name"`
	ObservedNames []string `json:"observed_names,omitempty"`
}

// Location is a half-open byte range in the exact, unmodified Input.Text.
type Location struct {
	Start int `json:"start"`
	End   int `json:"end"`
}
