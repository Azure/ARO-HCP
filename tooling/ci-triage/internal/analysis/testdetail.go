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

package analysis

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"time"

	"github.com/go-logr/logr"

	"github.com/Azure/ARO-HCP/tooling/ci-triage/internal/config"
	"github.com/Azure/ARO-HCP/tooling/ci-triage/internal/gcs"
)

// TestDetailResult is the deep-dive on a single test in a single job run.
type TestDetailResult struct {
	Test      string           `json:"test"`
	JobURL    string           `json:"job_url"`
	Env       string           `json:"env"`
	Result    string           `json:"result,omitempty"`
	Duration  int              `json:"duration,omitempty"`
	StartTime string           `json:"start_time,omitempty"`
	EndTime   string           `json:"end_time,omitempty"`
	Error     string           `json:"error,omitempty"`
	Output    string           `json:"output,omitempty"`
	AzureLog  []AzureLogEntry  `json:"azure_log,omitempty"`
}

// AzureLogEntry is a parsed line from azure.log.
type AzureLogEntry struct {
	Time    string `json:"time"`
	Level   string `json:"level"`
	Msg     string `json:"msg"`
	Event   string `json:"event,omitempty"`
}

// TestDetail fetches all available data for one test in one job run.
func TestDetail(ctx context.Context, jobURL, env, testName string) (*TestDetailResult, error) {
	log := logr.FromContextOrDiscard(ctx)
	cfg, ok := config.Envs[env]
	if !ok {
		return nil, &config.UnknownEnvError{Env: env}
	}

	baseURL := config.NormalizeBaseURL(jobURL)
	gcsClient := gcs.NewClient(&http.Client{Timeout: 20 * time.Second})

	result := &TestDetailResult{
		Test:   testName,
		JobURL: jobURL,
		Env:    env,
	}

	// Fetch extension test results for this job
	extResults, err := gcsClient.FetchExtensionResults(ctx, baseURL, cfg.Step, cfg.Container)
	if err != nil {
		log.V(1).Info("extension results unavailable", "error", err)
	}
	for _, r := range extResults {
		if r.Name == testName {
			result.Result = r.Result
			result.Duration = r.Duration
			result.StartTime = r.StartTime
			result.EndTime = r.EndTime
			result.Error = r.Error
			result.Output = r.Output
			break
		}
	}

	// Fetch azure.log for this test
	azureLogRaw, err := gcsClient.FetchAzureLog(ctx, baseURL, cfg.Step, cfg.Container, testName)
	if err != nil {
		log.V(1).Info("azure.log unavailable", "error", err)
	}
	if azureLogRaw != "" {
		result.AzureLog = parseAzureLog(azureLogRaw)
	}

	return result, nil
}

// parseAzureLog parses JSON-lines azure.log, extracting key fields.
// Filters to only interesting entries (errors, retries, slow responses).
func parseAzureLog(raw string) []AzureLogEntry {
	var entries []AzureLogEntry
	for _, line := range strings.Split(raw, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}

		// Quick parse — extract key fields without full JSON unmarshal
		var entry struct {
			Time  string `json:"time"`
			Level string `json:"level"`
			Msg   string `json:"msg"`
			Event string `json:"event"`
		}
		// Simple JSON parse
		if err := parseJSONLine(line, &entry); err != nil {
			continue
		}

		// Filter: keep errors, retries, and response entries (skip verbose request bodies)
		if entry.Event == "Request" && !strings.Contains(entry.Msg, "OUTGOING") {
			continue
		}

		// Truncate verbose messages
		msg := entry.Msg
		if len(msg) > 500 {
			msg = msg[:500] + "..."
		}

		entries = append(entries, AzureLogEntry{
			Time:  entry.Time,
			Level: entry.Level,
			Msg:   msg,
			Event: entry.Event,
		})
	}
	return entries
}

func parseJSONLine(line string, out any) error {
	return json.Unmarshal([]byte(line), out)
}
