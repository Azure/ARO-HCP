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

package kusto

import (
	"bytes"
	"compress/flate"
	"compress/gzip"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/Azure/azure-kusto-go/azkustodata"
	"github.com/Azure/azure-kusto-go/azkustodata/kql"
	"github.com/Azure/azure-kusto-go/azkustodata/query"
)

const completeHeader = `{"FrameType":"DataSetHeader","IsProgressive":false,"Version":"v2.0","IsFragmented":true,"ErrorReportingPlacement":"EndOfTable"}`
const completeProperties = `{"FrameType":"DataTable","TableId":0,"TableKind":"QueryProperties","TableName":"@ExtendedProperties","Columns":[{"ColumnName":"TableId","ColumnType":"int"},{"ColumnName":"Key","ColumnType":"string"},{"ColumnName":"Value","ColumnType":"dynamic"}],"Rows":[]}`
const completeTerminal = `{"FrameType":"DataSetCompletion","HasErrors":false,"Cancelled":false}`

func completePrimary(columns, rows string, id int) []string {
	var decodedRows []json.RawMessage
	if err := json.Unmarshal([]byte(rows), &decodedRows); err != nil {
		panic(fmt.Sprintf("invalid primary-table fixture rows: %v", err))
	}
	return []string{
		fmt.Sprintf(`{"FrameType":"TableHeader","TableId":%d,"TableKind":"PrimaryResult","TableName":"PrimaryResult","Columns":%s}`, id, columns),
		fmt.Sprintf(`{"FrameType":"TableFragment","TableFragmentType":"DataAppend","TableId":%d,"Rows":%s}`, id, rows),
		fmt.Sprintf(`{"FrameType":"TableCompletion","TableId":%d,"RowCount":%d}`, id, len(decodedRows)),
	}
}

func completeWire(primary []string, terminal string) string {
	frames := append([]string{completeHeader, completeProperties}, primary...)
	if terminal != "" {
		frames = append(frames, terminal)
	}
	return "[" + strings.Join(frames, "\n,") + "\n]\n"
}

func newCompleteSDK(t *testing.T, handler http.HandlerFunc, transport bool, limit int64) *azkustodata.Client {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.URL.Path == "/v1/rest/auth/metadata" {
			writer.WriteHeader(http.StatusNotFound)
			return
		}
		handler(writer, request)
	}))
	t.Cleanup(server.Close)
	client := server.Client()
	client.Timeout = 5 * time.Second
	if transport {
		client.Transport = CompleteTransport{Base: client.Transport, MaxResponseBytes: limit}
	}
	sdk, err := azkustodata.New(azkustodata.NewConnectionStringBuilder(server.URL), azkustodata.WithHttpClient(client))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = sdk.Close() })
	return sdk
}

func TestValidateV2KeepsServerErrorDetails(t *testing.T) {
	body := `[{"FrameType":"DataSetCompletion","HasErrors":true,"Cancelled":false,"OneApiErrors":[{"error":{"code":"LimitsExceeded","message":"Query result set has exceeded the internal limit"}}]}]`
	err := validateV2([]byte(body))
	if err == nil || !strings.Contains(err.Error(), "LimitsExceeded: Query result set has exceeded the internal limit") {
		t.Fatalf("server error code and message were not preserved: %v", err)
	}
}

func TestCompleteQueryRejectsPartialDatasets(t *testing.T) {
	primary := completePrimary(`[{"ColumnName":"value","ColumnType":"string"}]`, `[["usable-looking-row"]]`, 1)
	good := completeWire(primary, completeTerminal)
	lateErrors := `[{"error":{"code":"LimitsExceeded","message":"late failure"}}]`
	tests := []struct {
		name      string
		body      string
		wantError bool
	}{
		{name: "complete", body: good},
		{name: "completed-primary-missing-terminal-closed-array", body: completeWire(primary, ""), wantError: true},
		{name: "completed-primary-truncated-next-frame", body: strings.TrimSuffix(completeWire(primary, ""), "\n]\n") + "\n,{\"FrameType\":", wantError: true},
		{name: "missing-array-close", body: strings.TrimSuffix(good, "\n]\n"), wantError: true},
		{name: "malformed-after-terminal", body: good + "{", wantError: true},
		{name: "another-json-after-terminal", body: good + "{}", wantError: true},
		{name: "frame-after-terminal", body: completeWire(primary, completeTerminal+"\n,"+completeProperties), wantError: true},
		{name: "late-dataset-errors", body: completeWire(primary, `{"FrameType":"DataSetCompletion","HasErrors":true,"Cancelled":false,"OneApiErrors":`+lateErrors+`}`), wantError: true},
		{name: "error-with-false-success-flag", body: completeWire(primary, `{"FrameType":"DataSetCompletion","HasErrors":false,"Cancelled":false,"OneApiErrors":`+lateErrors+`}`), wantError: true},
		{name: "late-table-errors", body: strings.Replace(good, `"RowCount":1}`, `"RowCount":1,"OneApiErrors":`+lateErrors+`}`, 1), wantError: true},
		{name: "dataset-error-flag-without-details", body: completeWire(primary, `{"FrameType":"DataSetCompletion","HasErrors":true,"Cancelled":false}`), wantError: true},
		{name: "cancelled-terminal", body: completeWire(primary, `{"FrameType":"DataSetCompletion","HasErrors":false,"Cancelled":true}`), wantError: true},
		{name: "missing-primary", body: completeWire(nil, completeTerminal), wantError: true},
		{name: "multiple-primary", body: completeWire(append(append([]string{}, primary...), completePrimary(`[{"ColumnName":"value","ColumnType":"string"}]`, `[["second"]]`, 2)...), completeTerminal), wantError: true},
		{name: "bad-long", body: completeWire(completePrimary(`[{"ColumnName":"value","ColumnType":"long"}]`, `[["not-a-long"]]`, 1), completeTerminal), wantError: true},
		{name: "bad-datetime", body: completeWire(completePrimary(`[{"ColumnName":"value","ColumnType":"datetime"}]`, `[["not-a-date"]]`, 1), completeTerminal), wantError: true},
		{name: "bad-bool", body: completeWire(completePrimary(`[{"ColumnName":"value","ColumnType":"bool"}]`, `[["not-a-bool"]]`, 1), completeTerminal), wantError: true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			for _, transport := range []bool{false, true} {
				t.Run(fmt.Sprintf("transport=%t", transport), func(t *testing.T) {
					sdk := newCompleteSDK(t, func(writer http.ResponseWriter, request *http.Request) {
						if request.URL.Path != "/v2/rest/query" {
							t.Errorf("unexpected path %s", request.URL.Path)
						}
						_, _ = io.WriteString(writer, tc.body)
					}, transport, 0)
					ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
					defer cancel()
					// Exercise the ordinary, source-compatible hcpctl adapter too.
					client := &Client{kustoClient: sdk}
					dataset, err := client.Query(ctx, "fixture", kql.New("print value='fixture'"))
					if tc.wantError {
						if err == nil || dataset != nil {
							t.Fatalf("partial dataset escaped: %v, %v", dataset, err)
						}
						return
					}
					if err != nil {
						t.Fatal(err)
					}
					rows, err := DecodePrimary[struct {
						Value string `kusto:"value"`
					}](dataset)
					if err != nil || len(rows) != 1 || rows[0].Value != "usable-looking-row" {
						t.Fatalf("bad complete result: %+v, %v", rows, err)
					}
				})
			}
		})
	}
}

func TestCompleteQueryDynamicUnicodeParameters(t *testing.T) {
	const text = "中文🙂𐐷e\u0301\"\\\r\n'; .drop table ciTestResults; //"
	const stamp = "2026-11-04T01:02:03.1234567Z"
	payload, err := json.Marshal([]struct {
		Text string `json:"text"`
		At   string `json:"at"`
	}{{text, stamp}})
	if err != nil {
		t.Fatal(err)
	}
	sdk := newCompleteSDK(t, func(writer http.ResponseWriter, request *http.Request) {
		var body struct {
			CSL        string `json:"csl"`
			Properties struct {
				Parameters map[string]string
				Options    map[string]any
			} `json:"properties"`
		}
		if err := json.NewDecoder(request.Body).Decode(&body); err != nil {
			t.Error(err)
			return
		}
		if strings.Contains(body.CSL, text) || !strings.Contains(body.CSL, "payload:dynamic") {
			t.Errorf("payload interpolated into query: %q", body.CSL)
		}
		if body.Properties.Options["queryconsistency"] != "strongconsistency" || body.Properties.Options["query_results_cache_max_age"] != "00:00:00" || body.Properties.Options["notruncation"] != true || body.Properties.Options["servertimeout"] == nil {
			t.Errorf("missing required read options: %v", body.Properties.Options)
		}
		parameter := body.Properties.Parameters["payload"]
		if parameter != "dynamic("+string(payload)+")" {
			t.Errorf("dynamic payload changed bytes: %q", parameter)
		}
		rows, err := json.Marshal([][]any{{text, stamp, nil}})
		if err != nil {
			t.Error(err)
			return
		}
		_, _ = io.WriteString(writer, completeWire(completePrimary(`[{"ColumnName":"text","ColumnType":"string"},{"ColumnName":"at","ColumnType":"datetime"},{"ColumnName":"empty","ColumnType":"datetime"}]`, string(rows), 1), completeTerminal))
	}, false, 0)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	parameters := kql.NewParameters().AddSerializedDynamic("payload", payload)
	dataset, err := CompleteQuery(ctx, sdk, "fixture", kql.New("print input=payload | mv-expand input | project text=tostring(input.text), at=todatetime(input.at), empty=datetime(null)"), azkustodata.QueryParameters(parameters), azkustodata.CustomQueryOption("queryconsistency", "weakconsistency"), azkustodata.CustomQueryOption("query_results_cache_max_age", "01:00:00"))
	if err != nil {
		t.Fatal(err)
	}
	rows, err := DecodePrimary[struct {
		Text  string    `kusto:"text"`
		At    time.Time `kusto:"at"`
		Empty time.Time `kusto:"empty"`
	}](dataset)
	if err != nil || len(rows) != 1 {
		t.Fatalf("decode payload: %+v, %v", rows, err)
	}
	if rows[0].Text != text || rows[0].At.Format(time.RFC3339Nano) != stamp || !rows[0].Empty.IsZero() {
		t.Fatalf("payload fields changed: %+v", rows[0])
	}
}

func TestCompleteQueryCancellationAndDeadline(t *testing.T) {
	if dataset, err := CompleteQuery(context.Background(), nil, "fixture", kql.New("print 1")); err == nil || dataset != nil {
		t.Fatal("accepted a query without a deadline")
	}
	for _, transport := range []bool{false, true} {
		t.Run(fmt.Sprintf("transport=%t", transport), func(t *testing.T) {
			sdk := newCompleteSDK(t, func(writer http.ResponseWriter, request *http.Request) {
				primary := completePrimary(`[{"ColumnName":"value","ColumnType":"string"}]`, `[["partial"]]`, 1)
				_, _ = io.WriteString(writer, strings.TrimSuffix(completeWire(primary, ""), "\n]\n"))
				writer.(http.Flusher).Flush()
				<-request.Context().Done()
			}, transport, 0)
			ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
			defer cancel()
			dataset, err := CompleteQuery(ctx, sdk, "fixture", kql.New("print 1"))
			if err == nil || dataset != nil || !errors.Is(err, context.DeadlineExceeded) {
				t.Fatalf("cancelled query exposed data or lost cancellation: %v, %v", dataset, err)
			}
		})
	}
}

func TestCompleteTransportCompressionAndBounds(t *testing.T) {
	wire := completeWire(completePrimary(`[{"ColumnName":"value","ColumnType":"string"}]`, `[["complete"]]`, 1), completeTerminal)
	for _, encoding := range []string{"", "gzip", "deflate"} {
		for _, limit := range []int64{int64(len(wire)), int64(len(wire) - 1)} {
			t.Run(fmt.Sprintf("%s/limit=%d", encoding, limit), func(t *testing.T) {
				sdk := newCompleteSDK(t, func(writer http.ResponseWriter, request *http.Request) {
					writer.Header().Set("Content-Encoding", encoding)
					var compressed bytes.Buffer
					var compressor io.WriteCloser
					switch encoding {
					case "gzip":
						compressor = gzip.NewWriter(&compressed)
					case "deflate":
						compressor, _ = flate.NewWriter(&compressed, flate.DefaultCompression)
					default:
						_, _ = io.WriteString(writer, wire)
						return
					}
					_, _ = io.WriteString(compressor, wire)
					_ = compressor.Close()
					_, _ = writer.Write(compressed.Bytes())
				}, true, limit)
				ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
				defer cancel()
				// Direct SDK Query exercises the installed integrity boundary.
				dataset, err := sdk.Query(ctx, "fixture", kql.New("print value='complete'"))
				if limit < int64(len(wire)) {
					if err == nil || dataset != nil {
						t.Fatalf("oversized response escaped: %v, %v", dataset, err)
					}
					return
				}
				if err != nil {
					t.Fatal(err)
				}
			})
		}
	}
}

func TestCompleteTransportManagementResponses(t *testing.T) {
	const good = `{"Tables":[{"TableName":"PrimaryResult","Columns":[{"ColumnName":"Mapping","DataType":"String","ColumnType":"string"}],"Rows":[["[]"]]}]}`
	tests := []struct {
		name, body string
		wantError  bool
	}{
		{"complete", good, false},
		{"truncated", strings.TrimSuffix(good, "}"), true},
		{"malformed-trailing", good + "{", true},
		{"extra-document", good + "{}", true},
		{"exceptions", strings.TrimSuffix(good, "}") + `,"Exceptions":["failed"]}`, true},
		{"error", `{"error":{"code":"failed"}}`, true},
		{"no-tables", `{"Tables":[]}`, true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			sdk := newCompleteSDK(t, func(writer http.ResponseWriter, request *http.Request) { _, _ = io.WriteString(writer, tc.body) }, true, 0)
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			dataset, err := sdk.Mgmt(ctx, "fixture", kql.New(".show table fixture ingestion json mappings"), azkustodata.ServerTimeout(time.Second))
			if tc.wantError {
				if err == nil || dataset != nil {
					t.Fatalf("management response exposed partial data: %v, %v", dataset, err)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if len(dataset.Tables()) != 1 {
				t.Fatalf("missing management table: %v", dataset)
			}
		})
	}
}

func TestDecodePrimaryRejectsTypedMismatchWithoutRows(t *testing.T) {
	sdk := newCompleteSDK(t, func(writer http.ResponseWriter, request *http.Request) {
		_, _ = io.WriteString(writer, completeWire(completePrimary(`[{"ColumnName":"value","ColumnType":"dynamic"}]`, `[[{"count":1}],[{"count":"invalid"}]]`, 1), completeTerminal))
	}, false, 0)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	var dataset query.Dataset
	dataset, err := CompleteQuery(ctx, sdk, "fixture", kql.New("print value=dynamic({})"))
	if err != nil {
		t.Fatal(err)
	}
	rows, err := DecodePrimary[struct {
		Value struct {
			Count int `json:"count"`
		} `kusto:"value"`
	}](dataset)
	if err == nil || rows != nil {
		t.Fatalf("typed mismatch returned a successfully decoded prefix: %+v, %v", rows, err)
	}
}

func TestDecodePrimaryRejectsMissingFields(t *testing.T) {
	for _, rows := range []string{`[]`, `[["present"]]`} {
		t.Run(rows, func(t *testing.T) {
			sdk := newCompleteSDK(t, func(writer http.ResponseWriter, request *http.Request) {
				_, _ = io.WriteString(writer, completeWire(completePrimary(`[{"ColumnName":"value","ColumnType":"string"}]`, rows, 1), completeTerminal))
			}, false, 0)
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			dataset, err := CompleteQuery(ctx, sdk, "fixture", kql.New("print value='present'"))
			if err != nil {
				t.Fatal(err)
			}
			decoded, err := DecodePrimary[struct {
				Value    string `kusto:"value"`
				Required string `kusto:"required"`
			}](dataset)
			if err == nil || decoded != nil {
				t.Fatalf("missing field silently became a zero value: %+v, %v", decoded, err)
			}
		})
	}
}
