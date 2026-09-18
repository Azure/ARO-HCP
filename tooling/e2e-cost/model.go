// Copyright 2026 Microsoft Corporation
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://www.apache.org/licenses/LICENSE-2.0
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package cost

import "time"

const SchemaVersion = 1

// Snapshot serializes only the information required by the offline report.
type Snapshot struct {
	infraLookup    *infraLookup
	Version        int          `json:"version"`
	CollectedAt    time.Time    `json:"collectedAt"`
	Currency       string       `json:"currency"`
	CostBasis      string       `json:"costBasis"`
	QueryStart     string       `json:"queryStart"`
	QueryEnd       string       `json:"queryEnd"`
	Job            Job          `json:"job"`
	Groups         []Group      `json:"groups"`
	ExcludedGroups []string     `json:"excludedGroups,omitempty"`
	Diagnostics    []Diagnostic `json:"diagnostics"`
}

type Job struct {
	URL            string    `json:"url"`
	Name           string    `json:"name"`
	BuildID        string    `json:"buildID"`
	PR             string    `json:"pr,omitempty"`
	Commit         string    `json:"commit,omitempty"`
	Result         string    `json:"result,omitempty"`
	StartedAt      time.Time `json:"startedAt"`
	FinishedAt     time.Time `json:"finishedAt"`
	InfraStartedAt time.Time `json:"infraStartedAt"`
	InfraEndedAt   time.Time `json:"infraEndedAt"`
}

type Group struct {
	SubscriptionID string     `json:"subscriptionID,omitempty"`
	Name           string     `json:"name"`
	Category       string     `json:"category"` // Infra or Tests
	Owner          string     `json:"owner"`    // cluster, Regional, or full test name
	Kind           string     `json:"kind"`     // primary, aks-managed, customer, hcp-managed
	Attribution    string     `json:"attribution,omitempty"`
	Attempts       int        `json:"attempts,omitempty"`
	Outcomes       []string   `json:"outcomes,omitempty"`
	BillingStatus  string     `json:"billingStatus"` // pending, complete, partial, unavailable
	Resources      []Resource `json:"resources"`
}

type Resource struct {
	ID      string   `json:"id,omitempty"` // empty only for Azure charges without a resource ID
	Name    string   `json:"name"`
	Type    string   `json:"type"`
	Source  string   `json:"source"` // artifact, billing, or both
	Charges []Charge `json:"charges"`
}

// Charge is an aggregated resource/meter/day amount, not an archived billing row.
type Charge struct {
	Day           string  `json:"day"`
	MeterID       string  `json:"meterID,omitempty"`
	MeterName     string  `json:"meterName"`
	MeterCategory string  `json:"meterCategory,omitempty"`
	CostUSD       float64 `json:"costUSD"`
}

type Diagnostic struct {
	Severity            string `json:"severity"` // error, warning, info
	Code                string `json:"code"`
	Scope               string `json:"scope"`
	Message             string `json:"message"`
	SubscriptionID      string `json:"subscriptionID,omitempty"`
	ResourceGroup       string `json:"resourceGroup,omitempty"` // Customer RG for managed-mapping diagnostics.
	ClusterCount        int    `json:"clusterCount,omitempty"`
	MissingClusterCount int    `json:"missingClusterCount,omitempty"`
}

func (s *Snapshot) Diagnose(severity, code, scope, message string) {
	s.Diagnostics = append(s.Diagnostics, Diagnostic{Severity: severity, Code: code, Scope: scope, Message: message})
}

func (s *Snapshot) HasErrors() bool {
	for _, d := range s.Diagnostics {
		if d.Severity == "error" {
			return true
		}
	}
	return false
}
