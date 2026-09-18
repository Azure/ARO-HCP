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
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/Azure/azure-kusto-go/azkustodata"

	"github.com/Azure/ARO-HCP/tooling/tenant-quota/pkg/config"
)

func TestOutcomeMappingMustIncludeADOBuildID(t *testing.T) {
	for _, tc := range []struct {
		name, mapping, wantErr string
	}{
		{"flat mapping", `[{"column":"adoBuildId","datatype":"string","path":"$['adoBuildId']"}]`, ""},
		{"migrated", `[{"Column":"adoBuildId","DataType":"string","Properties":{"Path":"$['adoBuildId']"}}]`, ""},
		{"missing mapping", `[]`, "deploy the Kusto schema migration first"},
		{"old mapping", `[{"Column":"buildId","DataType":"string","Properties":{"Path":"$['buildId']"}}]`, "deploy the Kusto schema migration first"},
		{"wrong path", `[{"Column":"adoBuildId","DataType":"string","Properties":{"Path":"$['buildId']"}}]`, "deploy the Kusto schema migration first"},
		{"wrong type", `[{"Column":"adoBuildId","DataType":"long","Properties":{"Path":"$['adoBuildId']"}}]`, "deploy the Kusto schema migration first"},
		{"malformed", `{`, "failed to decode"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			encoded, err := json.Marshal(tc.mapping)
			require.NoError(t, err)
			client, err := azkustodata.New(azkustodata.NewConnectionStringBuilder("http://localhost"),
				azkustodata.WithHttpClient(&http.Client{Transport: ingestionTransport(func(r *http.Request) (*http.Response, error) {
					if r.Method == http.MethodGet && r.URL.Path == "/v1/rest/auth/metadata" {
						return &http.Response{StatusCode: http.StatusNotFound, Body: http.NoBody}, nil
					}
					if r.Method != http.MethodPost || r.URL.Path != "/v1/rest/mgmt" {
						return nil, fmt.Errorf("unexpected request: %s %s", r.Method, r.URL)
					}
					var request struct {
						DB, CSL string
					}
					if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
						return nil, err
					}
					if request.DB != "ServiceLogs" || !strings.Contains(request.CSL, "ingestion json mapping") || !strings.Contains(request.CSL, "ciJobOutcomesMapping") {
						return nil, fmt.Errorf("unexpected mapping query: %+v", request)
					}
					body := fmt.Sprintf(`{"Tables":[{"TableName":"Table","Columns":[{"ColumnName":"Mapping","ColumnType":"string"}],"Rows":[[%s]]}]}`, encoded)
					return &http.Response{StatusCode: http.StatusOK, Header: http.Header{"Content-Type": []string{"application/json"}}, Body: io.NopCloser(strings.NewReader(body))}, nil
				})}))
			require.NoError(t, err)
			t.Cleanup(func() { require.NoError(t, client.Close()) })
			w := &Writer{config: &config.Config{CIJobOutcomes: config.CIJobOutcomesConfig{
				Database: "ServiceLogs",
				Outcomes: config.KustoTableConfig{Table: "ciJobOutcomes", IngestionMapping: "ciJobOutcomesMapping"},
			}}}
			err = w.checkOutcomeMapping(t.Context(), client)
			if tc.wantErr == "" {
				require.NoError(t, err)
			} else {
				require.ErrorContains(t, err, tc.wantErr)
			}
		})
	}
}

// Runs written but not yet visible to a query must not be written again, and the
// set that remembers them must not grow without bound.
func TestQueuedRunsAreSuppressedThenForgotten(t *testing.T) {
	w := newTestWriter()
	older := time.Date(2026, 8, 25, 6, 0, 0, 0, time.UTC)
	newer := time.Date(2026, 8, 25, 12, 0, 0, 0, time.UTC)

	w.rememberQueued(ciJobOutcome{BuildID: "old-run", StartedAt: older})
	w.rememberQueued(ciJobOutcome{BuildID: "new-run", StartedAt: newer})

	if !w.queuedRecently("old-run") || !w.queuedRecently("new-run") {
		t.Fatal("a run just queued must be suppressed on the next pass")
	}

	// Once a pass reads from 09:00 onward, the query itself reports old-run, so
	// remembering it separately is no longer needed.
	w.forgetQueuedBefore(time.Date(2026, 8, 25, 9, 0, 0, 0, time.UTC))

	if w.queuedRecently("old-run") {
		t.Error("a run the query now covers must be forgotten")
	}
	if !w.queuedRecently("new-run") {
		t.Error("a run the query does not cover yet must still be suppressed")
	}
}

func newTestWriter() *Writer {
	return &Writer{
		queued:           make(map[string]time.Time),
		artifactAttempts: make(map[string]artifactAttempt),
	}
}

// A run whose artifacts cannot be read must be retried rather than stored
// straight away as a run that ran no tests: the read crosses a network, so an
// error is as likely to be transient as it is to mean the artifacts are absent.
// Retrying forever is no better, because a run really can finish without
// uploading anything, so the retries are bounded.
func TestArtifactReadsAreRetriedThenGivenUpOn(t *testing.T) {
	w := newTestWriter()
	run := ciJobOutcome{BuildID: "run", StartedAt: time.Date(2026, 8, 25, 12, 0, 0, 0, time.UTC)}

	for attempt := 1; attempt < maxArtifactAttempts; attempt++ {
		if !w.shouldRetryArtifacts(run) {
			t.Fatalf("attempt %d of %d must be retried", attempt, maxArtifactAttempts)
		}
	}
	if w.shouldRetryArtifacts(run) {
		t.Error("the run must be stored once the attempts are exhausted, not retried forever")
	}
}

// Storing a run ends its retries: a later pass finds it in the outcome table and
// never reaches the artifact read again, so the count is dead weight.
func TestStoringARunClearsItsAttempts(t *testing.T) {
	w := newTestWriter()
	run := ciJobOutcome{BuildID: "run", StartedAt: time.Date(2026, 8, 25, 12, 0, 0, 0, time.UTC)}

	w.shouldRetryArtifacts(run)
	w.rememberQueued(run)

	if len(w.artifactAttempts) != 0 {
		t.Errorf("a stored run must not keep an attempt count, got %v", w.artifactAttempts)
	}
}

// A run that keeps failing and is never stored still has to be forgotten, or its
// count outlives the window and accumulates for as long as the process runs.
func TestAttemptsForRunsOutsideTheWindowAreForgotten(t *testing.T) {
	w := newTestWriter()
	stale := ciJobOutcome{BuildID: "stale", StartedAt: time.Date(2026, 8, 25, 6, 0, 0, 0, time.UTC)}
	fresh := ciJobOutcome{BuildID: "fresh", StartedAt: time.Date(2026, 8, 25, 12, 0, 0, 0, time.UTC)}

	w.shouldRetryArtifacts(stale)
	w.shouldRetryArtifacts(fresh)

	w.forgetQueuedBefore(time.Date(2026, 8, 25, 9, 0, 0, 0, time.UTC))

	if _, kept := w.artifactAttempts["stale"]; kept {
		t.Error("a run that left the window must not keep its attempt count")
	}
	if _, kept := w.artifactAttempts["fresh"]; !kept {
		t.Error("a run still in the window must keep its attempt count")
	}
}

// The tags are what make a repeated write harmless, so each table's rows for a
// run must carry their own: one tag for both would let a retry that needs to
// write only the outcome row be rejected because the tests already landed.
func TestRunTagsAreDistinctPerTable(t *testing.T) {
	if runTag("2092349199822622720") == testsTag("2092349199822622720") {
		t.Error("the outcome and test rows for a run must be tagged separately")
	}
	if runTag("a") == runTag("b") {
		t.Error("different runs must be tagged differently")
	}
}
