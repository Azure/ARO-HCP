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

package citests

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Azure/azure-kusto-go/azkustodata"
	"github.com/Azure/azure-kusto-go/azkustodata/query"

	"github.com/Azure/ARO-HCP/tooling/hcpctl/pkg/kusto"
)

// collectionSelection is the scope used by the wire-level collection tests.
func collectionSelection() Selection {
	return Selection{
		Origin:    "https://hcp-dev-us-2.eastus2.kusto.windows.net/ServiceLogs",
		From:      time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC),
		To:        time.Date(2026, 2, 1, 0, 0, 0, 0, time.UTC),
		JobFilter: "job𐐷\"; take 1",
	}
}

func TestCollectBatchesObservationsAndKeepsCapturedRuns(t *testing.T) {
	selection := collectionSelection()
	buildIDs := make([]string, 0, observationBatchSize+2)
	for i := range observationBatchSize + 2 {
		buildIDs = append(buildIDs, fmt.Sprintf("run-🙂\"%d; take 1", i))
	}
	harness := newWireHarness(t, selection, buildIDs, "success")
	bundle, err := Collect(context.Background(), harness.client, "ServiceLogs", selection, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	if harness.queries.Load() != 1+1+2 {
		t.Fatalf("expected schema, run and two observation queries, got %d", harness.queries.Load())
	}
	if len(bundle.Runs) != len(buildIDs) {
		t.Fatalf("runs = %d, want %d", len(bundle.Runs), len(buildIDs))
	}
	if got := harness.batchSizes(); len(got) != 2 || got[0] != observationBatchSize || got[1] != 2 {
		t.Fatalf("observation batches = %v", got)
	}
	// Every run contributes byte-equivalent observations twice on the wire.
	if len(bundle.Observations) != len(buildIDs) {
		t.Fatalf("observations = %d, want %d after collapsing duplicates", len(bundle.Observations), len(buildIDs))
	}
	if bundle.Selection != selection {
		t.Fatalf("selection changed: %+v", bundle.Selection)
	}
	if bundle.Runs[0].Environment != EnvironmentDev || bundle.Runs[0].OverallResult != "SUCCESS" || !bundle.Runs[0].Processed {
		t.Fatalf("run mapping changed: %+v", bundle.Runs[0])
	}
	if bundle.Observations[0].Name != "" || bundle.Observations[0].NameVariants != 0 {
		t.Fatalf("missing name was guessed: %+v", bundle.Observations[0])
	}
	if err := bundle.Validate(); err != nil {
		t.Fatal(err)
	}
}

func TestCollectWithoutRunsIssuesNoObservationQuery(t *testing.T) {
	selection := collectionSelection()
	harness := newWireHarness(t, selection, nil, "success")
	bundle, err := Collect(context.Background(), harness.client, "ServiceLogs", selection, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	if harness.queries.Load() != 2 || len(harness.batchSizes()) != 0 {
		t.Fatalf("empty population issued %d queries with batches %v", harness.queries.Load(), harness.batchSizes())
	}
	if len(bundle.Runs) != 0 || len(bundle.Observations) != 0 {
		t.Fatalf("empty population produced facts: %+v", bundle)
	}
}

func TestCollectRejectsPartialLaterBatch(t *testing.T) {
	selection := collectionSelection()
	buildIDs := make([]string, 0, observationBatchSize+1)
	for i := range observationBatchSize + 1 {
		buildIDs = append(buildIDs, fmt.Sprintf("run-%d", i))
	}
	harness := newWireHarness(t, selection, buildIDs, "second-batch-partial")
	bundle, err := Collect(context.Background(), harness.client, "ServiceLogs", selection, time.Minute)
	if err == nil {
		t.Fatalf("accepted a partial second observation batch: %+v", bundle)
	}
	if bundle.SchemaVersion != 0 || len(bundle.Runs) != 0 || len(bundle.Observations) != 0 {
		t.Fatalf("partial collection returned a usable bundle: %+v", bundle)
	}
}

func TestCollectRejectsRunQueryFailureBeforeObservations(t *testing.T) {
	selection := collectionSelection()
	harness := newWireHarness(t, selection, []string{"run-1"}, "runs-partial")
	bundle, err := Collect(context.Background(), harness.client, "ServiceLogs", selection, time.Minute)
	if err == nil {
		t.Fatalf("accepted a partial run response: %+v", bundle)
	}
	if len(harness.batchSizes()) != 0 {
		t.Fatal("observation query issued after a failed run query")
	}
}

func TestValidateSchemaRequiresEveryColumnAndType(t *testing.T) {
	var columns []schemaColumn
	for table, required := range requiredSchema {
		for name, kind := range required {
			columns = append(columns, schemaColumn{table, name, kind})
		}
	}
	if err := validateSchema(columns); err != nil {
		t.Fatal(err)
	}
	broken := append([]schemaColumn(nil), columns...)
	for i := range broken {
		if broken[i].TableName == "ciTestResults" && broken[i].ColumnName == "result" {
			broken[i].ColumnType = "string-typed"
		}
	}
	err := validateSchema(broken)
	if err == nil || !strings.Contains(err.Error(), "ciTestResults.result:string") || !strings.Contains(err.Error(), "deploy") {
		t.Fatalf("missing actionable schema error: %v", err)
	}
	if err := validateSchema(columns[:len(columns)-1]); err == nil {
		t.Fatal("accepted a missing schema column")
	}
}

func TestCollectRejectsIncompleteSelection(t *testing.T) {
	selection := collectionSelection()
	for _, mutate := range []func(*Selection){
		func(s *Selection) { s.Origin = "" },
		func(s *Selection) { s.To = s.From },
		func(s *Selection) { s.Environment = "saas" },
	} {
		broken := selection
		mutate(&broken)
		if _, err := Collect(context.Background(), nil, "ServiceLogs", broken, time.Minute); err == nil {
			t.Fatalf("accepted invalid selection %+v", broken)
		}
	}
	for _, timeout := range []time.Duration{0, -time.Second, 31 * time.Minute} {
		if _, err := Collect(context.Background(), nil, "ServiceLogs", selection, timeout); err == nil {
			t.Fatalf("accepted unbounded timeout %s", timeout)
		}
	}
	if _, err := Collect(context.Background(), nil, "", selection, time.Minute); err == nil {
		t.Fatal("accepted a missing database")
	}
}

func TestDeadlineAndContextCancellationReturnNoBundle(t *testing.T) {
	selection := collectionSelection()
	bundle, err := Collect(context.Background(), deadlineQueryClient{t}, "ServiceLogs", selection, time.Millisecond)
	if !errors.Is(err, context.DeadlineExceeded) || bundle.SchemaVersion != 0 {
		t.Fatalf("timeout produced usable partial results: %+v, %v", bundle, err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := Collect(ctx, deadlineQueryClient{t}, "ServiceLogs", selection, time.Minute); err == nil {
		t.Fatal("accepted a canceled collection")
	}
}

func TestCollapseObservationsKeepsConflictingExecutions(t *testing.T) {
	start := time.Date(2026, 1, 2, 0, 0, 0, 0, time.UTC)
	base := Observation{BuildID: "run-1", TestID: "test", Name: "name", NameVariants: 1, Source: "e2e", Result: "passed",
		StartedAt: start, FinishedAt: start.Add(10 * time.Minute)}
	retry := base
	retry.FinishedAt = retry.FinishedAt.Add(time.Minute)
	collapsed := collapseObservations([]Observation{base, base, retry})
	if len(collapsed) != 2 {
		t.Fatalf("collapsed conflicting executions: %+v", collapsed)
	}
	if !collapsed[0].FinishedAt.Before(collapsed[1].FinishedAt) {
		t.Fatalf("observations are not time-ordered: %+v", collapsed)
	}
}

// wireHarness emulates the Kusto v2 query wire protocol for the collection tests.
type wireHarness struct {
	t          *testing.T
	selection  Selection
	buildIDs   []string
	mode       string
	client     *azkustodata.Client
	queries    atomic.Int32
	batches    []int
	runQueries atomic.Int32
}

func (h *wireHarness) batchSizes() []int {
	return append([]int(nil), h.batches...)
}

func newWireHarness(t *testing.T, selection Selection, buildIDs []string, mode string) *wireHarness {
	t.Helper()
	harness := &wireHarness{t: t, selection: selection, buildIDs: buildIDs, mode: mode}
	server := httptest.NewServer(http.HandlerFunc(harness.serve))
	t.Cleanup(server.Close)
	httpClient := server.Client()
	httpClient.Timeout = 5 * time.Second
	httpClient.Transport = kusto.CompleteTransport{Base: httpClient.Transport}
	client, err := azkustodata.New(azkustodata.NewConnectionStringBuilder(server.URL), azkustodata.WithHttpClient(httpClient))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = client.Close() })
	harness.client = client
	return harness
}

func (h *wireHarness) serve(writer http.ResponseWriter, request *http.Request) {
	harness := h.t
	if request.Method == http.MethodGet && request.URL.Path == "/v1/rest/auth/metadata" {
		// The unauthenticated SDK probes cloud metadata before querying.
		writer.WriteHeader(http.StatusNotFound)
		return
	}
	if request.Method != http.MethodPost || request.URL.Path != "/v2/rest/query" {
		harness.Errorf("unexpected SDK request %s %s", request.Method, request.URL.Path)
		writer.WriteHeader(http.StatusBadRequest)
		return
	}
	var body struct {
		CSL        string `json:"csl"`
		Properties struct {
			Options    map[string]any
			Parameters map[string]string
		} `json:"properties"`
	}
	if err := json.NewDecoder(request.Body).Decode(&body); err != nil {
		harness.Error(err)
		writer.WriteHeader(http.StatusBadRequest)
		return
	}
	requestNumber := h.queries.Add(1)
	writer.Header().Set("Content-Type", "application/json")
	if body.Properties.Options["notruncation"] != true || body.Properties.Options["servertimeout"] == nil {
		harness.Error("missing completeness/timeout request properties")
	}
	switch {
	case strings.Contains(body.CSL, "getschema"):
		if requestNumber != 1 {
			harness.Error("schema was not queried first")
		}
		var rows [][]any
		for table, columns := range requiredSchema {
			for name, kind := range columns {
				rows = append(rows, []any{table, name, kind})
			}
		}
		writeWireFrames(harness, writer, []wireColumn{{"TableName", "string"}, {"ColumnName", "string"}, {"ColumnType", "string"}}, rows, "success")
	case strings.Contains(body.CSL, "ciJobOutcomes"):
		h.runQueries.Add(1)
		parameters := body.Properties.Parameters
		if len(parameters) != 3 ||
			parameters["windowFrom"] != fmt.Sprintf("datetime(%s)", h.selection.From.Format(time.RFC3339Nano)) ||
			parameters["windowTo"] != fmt.Sprintf("datetime(%s)", h.selection.To.Format(time.RFC3339Nano)) {
			harness.Errorf("incorrect run parameters: %+v", parameters)
		}
		encodedFilters, err := json.Marshal(struct {
			JobFilter string `json:"job_filter"`
		}{JobFilter: h.selection.JobFilter})
		if err != nil {
			harness.Error(err)
		}
		if parameters["filters"] != "dynamic("+string(encodedFilters)+")" {
			harness.Errorf("filter parameter bytes changed: %q", parameters["filters"])
		}
		columns := []wireColumn{
			{"buildId", "string"}, {"jobName", "string"}, {"prowUrl", "string"}, {"overallResult", "string"},
			{"runtimeRegion", "string"}, {"runFailed", "bool"}, {"runStartedAt", "datetime"},
			{"runFinishedAt", "datetime"}, {"processed", "bool"},
		}
		var rows [][]any
		for _, buildID := range h.buildIDs {
			rows = append(rows, []any{buildID, "pull-ci-Azure-ARO-HCP-main-e2e-parallel-" + h.selection.JobFilter, "", "SUCCESS", "eastus2",
				false, h.selection.From.Add(-3 * time.Hour).Format(time.RFC3339Nano),
				h.selection.From.Add(24 * time.Hour).Format(time.RFC3339Nano), true})
		}
		mode := "success"
		if h.mode == "runs-partial" {
			mode = "table-partial"
		}
		writeWireFrames(harness, writer, columns, rows, mode)
	default:
		if h.runQueries.Load() == 0 {
			harness.Error("observation query preceded run query")
		}
		if len(body.Properties.Parameters) != 1 {
			harness.Errorf("incorrect observation parameters: %+v", body.Properties.Parameters)
		}
		var capturedIDs []string
		encoded := strings.TrimSuffix(strings.TrimPrefix(body.Properties.Parameters["buildIDs"], "dynamic("), ")")
		if err := json.Unmarshal([]byte(encoded), &capturedIDs); err != nil {
			harness.Errorf("buildIDs is not a JSON array: %v", err)
		}
		h.batches = append(h.batches, len(capturedIDs))
		columns := []wireColumn{
			{"buildId", "string"}, {"source", "string"}, {"testId", "string"}, {"testName", "string"},
			{"nameVariants", "long"}, {"result", "string"}, {"failed", "bool"}, {"startedAt", "datetime"},
			{"finishedAt", "datetime"}, {"setupFinishTime", "datetime"}, {"testStartTime", "datetime"},
			{"cleanupStartTime", "datetime"},
		}
		start := h.selection.From.Add(48 * time.Hour)
		var rows [][]any
		for _, buildID := range capturedIDs {
			observation := []any{buildID, "e2e", "test-" + buildID, "", 0, "passed", false,
				start.Format(time.RFC3339Nano), start.Add(10 * time.Minute).Format(time.RFC3339Nano),
				nil, nil, nil}
			if h.mode == "late-names" {
				// The name dimension becomes visible only after the first batch.
				observation[2] = slowTestID
				if len(h.batches) == 2 {
					observation[3], observation[4] = slowTestName, 1
				}
			}
			rows = append(rows, observation, observation)
		}
		mode := "success"
		if h.mode == "second-batch-partial" && len(h.batches) == 2 {
			mode = "dataset-partial"
		}
		writeWireFrames(harness, writer, columns, rows, mode)
	}
}

type wireColumn struct {
	Name string `json:"ColumnName"`
	Kind string `json:"ColumnType"`
}

// writeWireFrames emits newline-delimited fragmented v2 frames, including the
// terminal completion frame a complete client requires.
func writeWireFrames(t *testing.T, writer http.ResponseWriter, columns []wireColumn, rows [][]any, mode string) {
	t.Helper()
	if rows == nil {
		rows = [][]any{}
	}
	encodedColumns, err := json.Marshal(columns)
	if err != nil {
		t.Fatal(err)
	}
	encodedRows, err := json.Marshal(rows)
	if err != nil {
		t.Fatal(err)
	}
	header := `{"FrameType":"DataSetHeader","IsProgressive":false,"Version":"v2.0","IsFragmented":true,"ErrorReportingPlacement":"EndOfTable"}`
	const apiErrors = `[{"error":{"code":"LimitsExceeded","message":"E_QUERY_RESULT_SET_TOO_LARGE","@type":"KustoServicePartialQueryFailureLimitsExceededException","@message":"results truncated"}}]`
	tableErrors, datasetErrors := "", ""
	hasErrors := false
	if mode == "table-partial" {
		tableErrors = `,"OneApiErrors":` + apiErrors
	}
	if mode == "dataset-partial" {
		hasErrors = true
		datasetErrors = `,"OneApiErrors":` + apiErrors
	}
	frames := []string{
		header,
		`{"FrameType":"DataTable","TableId":0,"TableKind":"QueryProperties","TableName":"@ExtendedProperties","Columns":[{"ColumnName":"TableId","ColumnType":"int"},{"ColumnName":"Key","ColumnType":"string"},{"ColumnName":"Value","ColumnType":"dynamic"}],"Rows":[]}`,
		fmt.Sprintf(`{"FrameType":"TableHeader","TableId":1,"TableKind":"PrimaryResult","TableName":"PrimaryResult","Columns":%s}`, encodedColumns),
		fmt.Sprintf(`{"FrameType":"TableFragment","TableFragmentType":"DataAppend","TableId":1,"Rows":%s}`, encodedRows),
		fmt.Sprintf(`{"FrameType":"TableCompletion","TableId":1,"RowCount":%d%s}`, len(rows), tableErrors),
		fmt.Sprintf(`{"FrameType":"DataSetCompletion","HasErrors":%t,"Cancelled":false%s}`, hasErrors, datasetErrors),
	}
	if _, err := fmt.Fprint(writer, "["+strings.Join(frames, "\n,")+"\n]\n"); err != nil {
		t.Error(err)
	}
}

type deadlineQueryClient struct{ t *testing.T }

func (client deadlineQueryClient) Query(ctx context.Context, _ string, _ azkustodata.Statement, _ ...azkustodata.QueryOption) (query.Dataset, error) {
	client.t.Helper()
	if _, ok := ctx.Deadline(); !ok {
		client.t.Fatal("collection query has no explicit deadline")
	}
	<-ctx.Done()
	return nil, ctx.Err()
}
