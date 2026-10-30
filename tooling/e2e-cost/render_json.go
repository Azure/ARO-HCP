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

import (
	"encoding/json"
	"fmt"
	"io"
	"time"
)

// reportData is a computed report for the site viewer: the same figures the
// single-file report renders, without its page, styles, or chart runtime.
type reportData struct {
	Version          int             `json:"version"`
	Job              Job             `json:"job"`
	QueryStart       string          `json:"queryStart"`
	QueryEnd         string          `json:"queryEnd"`
	CollectedAt      time.Time       `json:"collectedAt"`
	ExcludedGroups   []string        `json:"excludedGroups,omitempty"`
	SubscriptionMode bool            `json:"subscriptionMode"`
	Incomplete       bool            `json:"incomplete"`
	HasCharges       bool            `json:"hasCharges"`
	Positive         float64         `json:"positive"`
	Negative         float64         `json:"negative"`
	Net              float64         `json:"net"`
	InfraRate        string          `json:"infraRate,omitempty"`
	JobDuration      string          `json:"jobDuration,omitempty"`
	PostJobResources int             `json:"postJobResources"`
	PostJobNet       float64         `json:"postJobNet"`
	ResourceCount    int             `json:"resourceCount"`
	Alerts           []Diagnostic    `json:"alerts"`
	InfoDiagnostics  []Diagnostic    `json:"infoDiagnostics"`
	Inventory        json.RawMessage `json:"inventory"`
	Option           json.RawMessage `json:"option"`
	TypeData         json.RawMessage `json:"typeData"`
	Filtered         json.RawMessage `json:"filtered,omitempty"`
	Subscriptions    json.RawMessage `json:"subscriptions,omitempty"`
}

// RenderJSON writes the computed report as JSON for the site viewer, which
// renders it with the single-file report's styles and scripts. Validation and
// figures are identical to Render.
func RenderJSON(w io.Writer, snapshot *Snapshot) error {
	view, err := newCostReport(snapshot)
	if err != nil {
		return err
	}
	raw := func(s string) json.RawMessage {
		if s == "" {
			return nil
		}
		return json.RawMessage(s)
	}
	data := reportData{
		Version: SchemaVersion, Job: snapshot.Job, QueryStart: snapshot.QueryStart, QueryEnd: snapshot.QueryEnd,
		CollectedAt: snapshot.CollectedAt, ExcludedGroups: snapshot.ExcludedGroups, SubscriptionMode: view.SubscriptionMode,
		Incomplete: view.Incomplete, HasCharges: view.HasCharges, Positive: view.Positive, Negative: view.Negative, Net: view.Net,
		InfraRate: view.InfraRate, JobDuration: view.JobDuration, PostJobResources: view.PostJobResources, PostJobNet: view.PostJobNet,
		ResourceCount: view.ResourceCount, Alerts: view.Alerts, InfoDiagnostics: view.InfoDiagnostics,
		Inventory: raw(string(view.InventoryData)), Option: raw(string(view.Option)), TypeData: raw(string(view.TypeData)),
		Filtered: raw(string(view.FilteredData)), Subscriptions: raw(string(view.SubscriptionData)),
	}
	if err := json.NewEncoder(w).Encode(data); err != nil {
		return fmt.Errorf("render cost report: write JSON: %w", err)
	}
	return nil
}
