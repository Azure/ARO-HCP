// Copyright 2026 Microsoft Corporation
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://www.apache.org/licenses/LICENSE-2.0
// Unless required by applicable law or agreed to in writing, software distributed
// under the License is distributed on an "AS IS" BASIS, WITHOUT WARRANTIES OR
// CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package cifailures

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

func TestSchemaRequiresSourceAndCorrectTypes(t *testing.T) {
	var columns []schemaColumn
	for table, names := range requiredSchema {
		for name, kind := range names {
			columns = append(columns, schemaColumn{table, name, kind})
		}
	}
	if err := validateSchema(columns); err != nil {
		t.Fatal(err)
	}
	for i, column := range columns {
		if column.TableName != "ciTestResults" || column.ColumnName != "source" {
			continue
		}
		columns[i].ColumnType = "dynamic"
		err := validateSchema(columns)
		if err == nil || !strings.Contains(err.Error(), "ciTestResults.source:string") || !strings.Contains(err.Error(), "#7875") {
			t.Fatalf("missing actionable source prerequisite: %v", err)
		}
		columns = append(columns[:i], columns[i+1:]...)
		if err := validateSchema(columns); err == nil {
			t.Fatal("accepted missing source column")
		}
		break
	}
}

func TestAssemblePreservesRepeatedCasesAndMissingNames(t *testing.T) {
	metadata := testBundle().Metadata
	run := runRow{BuildID: "run-1", JobName: "pull-ci-Azure-ARO-HCP-main-e2e-parallel", RunFinishedAt: metadata.Window.From, RunFailed: true, Processed: true}
	observation := observationRow{BuildID: "run-1", TestID: "same-id", Message: "failure"}
	bundle, err := assembleBundle(metadata, []runRow{run}, []observationRow{observation, observation})
	if err != nil {
		t.Fatal(err)
	}
	if len(bundle.Observations) != 2 {
		t.Fatal("deduplicated legitimate test cases")
	}
	if bundle.Observations[0].Fingerprint != bundle.Observations[1].Fingerprint {
		t.Fatal("identical inputs have different fingerprints")
	}
	if bundle.Observations[0].TestName != "" || bundle.Observations[0].Source != "" {
		t.Fatal("guessed missing name or historical source")
	}
	if !bundle.Observations[0].StartedAt.IsZero() {
		t.Fatal("zero test timing was changed")
	}
}

func TestAssembleRetainsRunOnlyPopulation(t *testing.T) {
	metadata := testBundle().Metadata
	runs := []runRow{
		{BuildID: "failed", JobName: "pull-ci-Azure-ARO-HCP-main-e2e-parallel", RunFinishedAt: metadata.Window.From, RunFailed: true},
		{BuildID: "passed", JobName: "pull-ci-Azure-ARO-HCP-main-e2e-parallel", RunFinishedAt: metadata.Window.From, Processed: true},
	}
	bundle, err := assembleBundle(metadata, runs, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(bundle.Runs) != 2 || len(bundle.Observations) != 0 {
		t.Fatalf("lost runs without observations: %+v", bundle)
	}
}

func TestAssembleRejectsUnknownObservationRun(t *testing.T) {
	metadata := testBundle().Metadata
	_, err := assembleBundle(metadata, nil, []observationRow{{BuildID: "not-selected", TestID: "test", Message: "failure"}})
	if err == nil {
		t.Fatal("accepted observation outside selected runs")
	}
}

func TestTypedSDKParametersAndLateFailures(t *testing.T) {
	type testCase struct {
		name           string
		failureQuery   string
		mode           string
		noObservations bool
		noRuns         bool
	}
	tests := []testCase{
		{name: "success"},
		{name: "run-only", noObservations: true},
		{name: "empty-population", noRuns: true, noObservations: true},
	}
	for _, queryName := range []string{"runs", "observations"} {
		for _, mode := range []string{"table-partial", "dataset-partial", "truncated-wire", "completed-primary-missing-terminal", "malformed-trailing"} {
			tests = append(tests, testCase{name: queryName + "/" + mode, failureQuery: queryName, mode: mode})
		}
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			metadata := testBundle().Metadata
			metadata.Environment = ""
			metadata.Filters = Filters{Source: "rp-upgrade🙂\"; take 1", JobFilter: "job𐐷\"; take 1"}
			buildIDs := []string{"run-1", "run-🙂\"2; take 1"}
			if tc.noRuns {
				buildIDs = []string{}
			}
			var requests atomic.Int32
			var runRequests, observationRequests atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
				if request.Method == http.MethodGet && request.URL.Path == "/v1/rest/auth/metadata" {
					// The unauthenticated SDK probes cloud metadata before querying.
					// An empty 404 uses its documented default cloud configuration.
					writer.WriteHeader(http.StatusNotFound)
					return
				}
				if request.Method != http.MethodPost || request.URL.Path != "/v2/rest/query" {
					t.Errorf("unexpected SDK request %s %s", request.Method, request.URL.Path)
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
					t.Error(err)
					writer.WriteHeader(http.StatusBadRequest)
					return
				}
				requestNumber := requests.Add(1)
				writer.Header().Set("Content-Type", "application/json")
				if body.Properties.Options["notruncation"] != true || body.Properties.Options["servertimeout"] == nil {
					t.Error("missing completeness/timeout request properties")
				}
				if strings.Contains(body.CSL, "getschema") {
					if requestNumber != 1 {
						t.Error("schema was not queried first")
					}
					var rows [][]any
					for table, columns := range requiredSchema {
						for name, kind := range columns {
							rows = append(rows, []any{table, name, kind})
						}
					}
					writeFrames(t, writer, []wireColumn{{"TableName", "string"}, {"ColumnName", "string"}, {"ColumnType", "string"}}, rows, "success")
					return
				}
				queryName := "runs"
				parameters := body.Properties.Parameters
				encodedFilters, encodeErr := json.Marshal(metadata.Filters)
				if encodeErr != nil {
					t.Error(encodeErr)
				}
				if parameters["filters"] != "dynamic("+string(encodedFilters)+")" {
					t.Errorf("filter parameter bytes changed: %q", parameters["filters"])
				}
				var columns []wireColumn
				var rows [][]any
				if strings.Contains(body.CSL, "ciJobOutcomes") {
					runRequests.Add(1)
					if observationRequests.Load() != 0 {
						t.Error("run query followed observation query")
					}
					if len(parameters) != 3 || parameters["windowFrom"] != fmt.Sprintf("datetime(%s)", metadata.Window.From.Format(time.RFC3339Nano)) || parameters["windowTo"] != fmt.Sprintf("datetime(%s)", metadata.Window.To.Format(time.RFC3339Nano)) {
						t.Errorf("incorrect run parameters: %+v", parameters)
					}
					columns = []wireColumn{
						{"buildId", "string"}, {"jobName", "string"}, {"prowUrl", "string"},
						{"runFinishedAt", "datetime"}, {"runFailed", "bool"}, {"processed", "bool"},
					}
					for _, buildID := range buildIDs {
						rows = append(rows, []any{buildID, metadata.Filters.JobFilter, "", metadata.Window.From.Format(time.RFC3339Nano), true, true})
					}
				} else {
					queryName = "observations"
					observationRequests.Add(1)
					if runRequests.Load() == 0 {
						t.Error("observation query preceded run query")
					}
					if len(parameters) != 2 {
						t.Errorf("incorrect observation parameters: %+v", parameters)
					}
					var capturedIDs []string
					encodedIDs := strings.TrimSuffix(strings.TrimPrefix(parameters["buildIDs"], "dynamic("), ")")
					if err := json.Unmarshal([]byte(encodedIDs), &capturedIDs); err != nil || capturedIDs == nil {
						t.Errorf("buildIDs is not a JSON array: %v", err)
					}
					if len(capturedIDs) != len(buildIDs) {
						t.Errorf("observation IDs differ from captured runs: %v, want %v", capturedIDs, buildIDs)
					}
					for _, buildID := range buildIDs {
						count := 0
						for _, capturedID := range capturedIDs {
							if capturedID == buildID {
								count++
							}
						}
						if count != 1 {
							t.Errorf("captured run %q occurs %d times in observation restriction", buildID, count)
						}
					}
					columns = []wireColumn{
						{"buildId", "string"}, {"source", "string"}, {"testId", "string"}, {"testName", "string"}, {"nameVariants", "long"},
						{"startedAt", "datetime"}, {"finishedAt", "datetime"}, {"setupFinishTime", "datetime"},
						{"testStartTime", "datetime"}, {"cleanupStartTime", "datetime"}, {"message", "string"},
					}
					if !tc.noObservations {
						observation := []any{buildIDs[0], metadata.Filters.Source, "test-1", "", 0, nil, nil, nil, nil, nil, "raw failure\n"}
						rows = [][]any{observation, observation}
					}
				}
				mode := "success"
				if tc.failureQuery == queryName {
					mode = tc.mode
				}
				// Late failures follow actual rows, not just an empty primary table.
				writeFrames(t, writer, columns, rows, mode)
			}))
			defer server.Close()
			httpClient := server.Client()
			httpClient.Timeout = 5 * time.Second
			httpClient.Transport = kusto.CompleteTransport{Base: httpClient.Transport}
			client, err := azkustodata.New(azkustodata.NewConnectionStringBuilder(server.URL), azkustodata.WithHttpClient(httpClient))
			if err != nil {
				t.Fatal(err)
			}
			defer func() { _ = client.Close() }()
			bundle, err := Collect(context.Background(), client, "ServiceLogs", metadata, time.Minute)
			if runRequests.Load() == 0 {
				t.Fatal("run query was not issued")
			}
			if tc.failureQuery == "runs" && observationRequests.Load() != 0 {
				t.Fatal("observation query issued after run failure")
			}
			if tc.failureQuery != "runs" && observationRequests.Load() == 0 {
				t.Fatal("observation query was not issued")
			}
			if tc.failureQuery == "" && requests.Load() != 3 {
				t.Fatalf("expected schema, run, and observation queries, got %d", requests.Load())
			}
			if tc.failureQuery == "" {
				if err != nil {
					t.Fatal(err)
				}
				wantObservations := 2
				if tc.noObservations {
					wantObservations = 0
				}
				if len(bundle.Observations) != wantObservations || len(bundle.Runs) != len(buildIDs) {
					t.Fatalf("typed mapping lost population: %+v", bundle)
				}
				if wantObservations != 0 && (bundle.Observations[0].Text != "raw failure\n" || bundle.Observations[0].Source != metadata.Filters.Source || bundle.Observations[0].TestName != "" || !bundle.Observations[0].StartedAt.IsZero()) {
					t.Fatal("typed mapping changed raw or missing fields")
				}
				return
			}
			if err == nil || bundle.SchemaVersion != 0 {
				t.Fatalf("accepted %s response: %+v, %v", tc.name, bundle, err)
			}
		})
	}
}

type wireColumn struct {
	Name string `json:"ColumnName"`
	Kind string `json:"ColumnType"`
}

func writeFrames(t *testing.T, writer http.ResponseWriter, columns []wireColumn, rows [][]any, mode string) {
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
	// Fragmented v2 is newline-delimited, with ordered JSON properties in
	// table headers. Maps or one Encode(frames) call do not model this protocol.
	header := `{"FrameType":"DataSetHeader","IsProgressive":false,"Version":"v2.0","IsFragmented":true,"ErrorReportingPlacement":"EndOfTable"}`
	if mode == "truncated-wire" {
		if _, err := fmt.Fprint(writer, "["+header+"\n,"+`{"FrameType":`); err != nil {
			t.Error(err)
		}
		return
	}
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
	if mode == "completed-primary-missing-terminal" {
		frames = frames[:len(frames)-1]
	}
	if mode == "malformed-trailing" {
		frames = append(frames, `{"FrameType":`)
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

func TestCollectionTimeoutIsBoundedAndReturnsNoBundle(t *testing.T) {
	bundle, err := Collect(context.Background(), deadlineQueryClient{t}, "ServiceLogs", testBundle().Metadata, time.Millisecond)
	if !errors.Is(err, context.DeadlineExceeded) || bundle.SchemaVersion != 0 {
		t.Fatalf("timeout produced usable partial results: %+v, %v", bundle, err)
	}
	for _, timeout := range []time.Duration{0, -time.Second, 31 * time.Minute} {
		if _, err := Collect(context.Background(), nil, "ServiceLogs", testBundle().Metadata, timeout); err == nil {
			t.Fatalf("accepted unbounded timeout %s", timeout)
		}
	}
}

type schemaFailureQueryClient struct{ err error }

func (client schemaFailureQueryClient) Query(context.Context, string, azkustodata.Statement, ...azkustodata.QueryOption) (query.Dataset, error) {
	return nil, client.err
}

func TestSchemaTransportFailureDoesNotSuggestDeployment(t *testing.T) {
	for _, message := range []string{"authentication failed", "connection refused"} {
		cause := errors.New(message)
		_, err := Collect(context.Background(), schemaFailureQueryClient{cause}, "ServiceLogs", testBundle().Metadata, time.Minute)
		if !errors.Is(err, cause) || !strings.Contains(err.Error(), "inspect CI schema") || strings.Contains(err.Error(), "deploy") {
			t.Fatalf("misclassified schema query transport failure: %v", err)
		}
	}
}
