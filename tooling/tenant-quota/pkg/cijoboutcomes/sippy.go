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

package cijoboutcomes

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// sippyRun is one CI job run as Sippy reports it. Only the fields that reach
// Kusto are decoded.
type sippyRun struct {
	ProwID          string         `json:"prow_id"`
	Job             string         `json:"job"`
	URL             string         `json:"url"`
	TestFailures    int            `json:"test_failures"`
	FailedTestNames []string       `json:"failed_test_names"`
	Failed          bool           `json:"failed"`
	Succeeded       bool           `json:"succeeded"`
	OverallResult   string         `json:"overall_result"`
	Timestamp       sippyTimestamp `json:"timestamp"`
}

// sippyTimestamp decodes the time a run started.
//
// Sippy reports it as an RFC 3339 string, but has historically reported Unix
// milliseconds, and still does on some endpoints. Accept either so an upstream
// change cannot silently stop ingestion.
type sippyTimestamp struct {
	time.Time
}

func (t *sippyTimestamp) UnmarshalJSON(data []byte) error {
	trimmed := strings.TrimSpace(string(data))
	if trimmed == "null" || trimmed == `""` {
		return nil
	}

	if strings.HasPrefix(trimmed, `"`) {
		var text string
		if err := json.Unmarshal(data, &text); err != nil {
			return fmt.Errorf("failed to decode timestamp %s: %w", trimmed, err)
		}
		parsed, err := time.Parse(time.RFC3339, text)
		if err != nil {
			return fmt.Errorf("failed to parse timestamp %q: %w", text, err)
		}
		t.Time = parsed.UTC()
		return nil
	}

	var millis int64
	if err := json.Unmarshal(data, &millis); err != nil {
		return fmt.Errorf("failed to decode timestamp %s: %w", trimmed, err)
	}
	if millis > 0 {
		t.Time = time.UnixMilli(millis).UTC()
	}
	return nil
}

type sippyResponse struct {
	Rows []sippyRun `json:"rows"`
}

// fetchRuns lists a release's e2e runs that started after the given time.
func fetchRuns(ctx context.Context, client *http.Client, endpoint, release, jobFilter string, since time.Time) ([]sippyRun, error) {
	filter, err := json.Marshal(map[string]any{
		"items": []map[string]string{
			{"columnField": "name", "operatorValue": "contains", "value": jobFilter},
			{"columnField": "timestamp", "operatorValue": ">", "value": since.UTC().Format(time.RFC3339)},
		},
		"linkOperator": "and",
	})
	if err != nil {
		return nil, fmt.Errorf("failed to build filter: %w", err)
	}

	query := url.Values{}
	query.Set("release", release)
	query.Set("filter", string(filter))
	requestURL := strings.TrimSuffix(endpoint, "/") + "/api/jobs/runs?" + query.Encode()

	request, err := http.NewRequestWithContext(ctx, http.MethodGet, requestURL, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to build request: %w", err)
	}

	response, err := client.Do(request)
	if err != nil {
		return nil, fmt.Errorf("failed to query Sippy: %w", err)
	}
	defer func() { _ = response.Body.Close() }()

	if response.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("sippy returned %s for release %q", response.Status, release)
	}

	var decoded sippyResponse
	if err := json.NewDecoder(response.Body).Decode(&decoded); err != nil {
		return nil, fmt.Errorf("failed to decode Sippy response: %w", err)
	}
	return decoded.Rows, nil
}
