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

package gcs

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/Azure/ARO-HCP/tooling/ci-triage/internal/config"
)

// StepExecution represents one step in the ci-operator step graph with timing.
type StepExecution struct {
	Name         string   `json:"name"`
	StartedAt    string   `json:"started_at,omitempty"`
	FinishedAt   string   `json:"finished_at,omitempty"`
	DurationSecs float64  `json:"duration_seconds"`
	Failed       bool     `json:"failed"`
	Dependencies []string `json:"dependencies,omitempty"`
}

// rawStepGraphEntry is the raw JSON structure from ci-operator-step-graph.json.
type rawStepGraphEntry struct {
	Name         string   `json:"name"`
	StartedAt    string   `json:"started_at"`
	FinishedAt   string   `json:"finished_at"`
	Duration     int64    `json:"duration"` // nanoseconds
	Failed       bool     `json:"failed"`
	Dependencies []string `json:"dependencies"`
}

// FetchStepGraph fetches and parses ci-operator-step-graph.json from a job's artifacts.
// Returns the step execution DAG with timing information per step.
func (c *Client) FetchStepGraph(ctx context.Context, baseURL string) ([]StepExecution, error) {
	stepGraphURL := fmt.Sprintf("%s/artifacts/ci-operator-step-graph.json", baseURL)
	data, err := c.fetchBytes(ctx, stepGraphURL, 15*time.Second)
	if err != nil {
		return nil, err
	}
	if data == nil {
		return nil, nil
	}

	var raw []rawStepGraphEntry
	if err := json.Unmarshal(data, &raw); err != nil {
		return nil, fmt.Errorf("parsing step graph: %w", err)
	}

	steps := make([]StepExecution, 0, len(raw))
	for _, r := range raw {
		steps = append(steps, StepExecution{
			Name:         r.Name,
			StartedAt:    r.StartedAt,
			FinishedAt:   r.FinishedAt,
			DurationSecs: float64(r.Duration) / 1e9,
			Failed:       r.Failed,
			Dependencies: r.Dependencies,
		})
	}
	return steps, nil
}

// TimingMetadata represents ARM deployment timing data from the test visualization artifacts.
type TimingMetadata struct {
	StartedAt   string                                  `json:"startedAt"`
	FinishedAt  string                                  `json:"finishedAt"`
	Identifier  []string                                `json:"identifier,omitempty"`
	Steps       []TimingStep                            `json:"steps,omitempty"`
	Deployments map[string]map[string][]ARMOperation    `json:"deployments,omitempty"`
}

// TimingStep is a named step with start/finish timestamps from timing metadata.
type TimingStep struct {
	Name       string `json:"name"`
	StartedAt  string `json:"startedAt"`
	FinishedAt string `json:"finishedAt"`
}

// ARMOperation represents a single ARM deployment operation with timing and child operations.
type ARMOperation struct {
	OperationType  string         `json:"operationType"`
	Resource       ARMResource    `json:"resource"`
	Duration       string         `json:"duration"` // ISO 8601 duration (e.g., "PT3.72S")
	StartTimestamp string         `json:"startTimestamp"`
	Children       []ARMOperation `json:"children,omitempty"`
}

// ARMResource identifies an Azure resource in a deployment operation.
type ARMResource struct {
	Name          string `json:"name"`
	ResourceGroup string `json:"resourceGroup"`
	ResourceType  string `json:"resourceType"`
}

// FetchTimingMetadata fetches and parses the timing-metadata YAML file from
// the test visualization artifacts. These contain ARM deployment timing trees
// with per-resource operation traces.
func (c *Client) FetchTimingMetadata(ctx context.Context, baseURL, step string) ([]TimingMetadata, error) {
	// List timing metadata files
	prefix := extractGCSPath(baseURL) + "/artifacts/" + step + "/aro-hcp-gather-test-visualization/artifacts/test-timing/"
	listURL := fmt.Sprintf("%s?prefix=%s&fields=items(name)&maxResults=50", config.GCSAPI, prefix)

	var listResp gcsListResponse
	if err := c.fetchJSON(ctx, listURL, 10*time.Second, &listResp); err != nil {
		return nil, nil // non-fatal
	}

	var results []TimingMetadata
	for _, item := range listResp.Items {
		if len(item.Name) == 0 {
			continue
		}
		fileURL := config.GCSDirect + "/" + item.Name
		data, err := c.fetchBytes(ctx, fileURL, 15*time.Second)
		if err != nil || data == nil {
			continue
		}
		var tm TimingMetadata
		if err := json.Unmarshal(data, &tm); err != nil {
			continue // might be YAML, skip for now
		}
		results = append(results, tm)
	}
	return results, nil
}

