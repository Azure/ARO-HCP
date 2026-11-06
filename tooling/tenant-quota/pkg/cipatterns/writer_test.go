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

package cipatterns

import (
	"bytes"
	"compress/gzip"
	"context"
	"encoding/base64"
	"encoding/json"
	"encoding/xml"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/Azure/azure-kusto-go/azkustodata"
	"github.com/Azure/azure-kusto-go/azkustoingest"
	"github.com/stretchr/testify/require"

	"github.com/Azure/ARO-HCP/tooling/hcpctl/pkg/kusto"
	tenantkusto "github.com/Azure/ARO-HCP/tooling/tenant-quota/pkg/kusto"
)

type fixtureTransport func(*http.Request) (*http.Response, error)

func (f fixtureTransport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestQueuedSDKWirePreservesEnvelopesWithoutExtentGates(t *testing.T) {
	var uploads, messages [][]byte
	client := &http.Client{Transport: fixtureTransport(func(r *http.Request) (*http.Response, error) {
		var body string
		status := http.StatusOK
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/v1/rest/auth/metadata":
			status = http.StatusNotFound
		case r.URL.Path == "/v1/rest/mgmt":
			var request struct {
				Query string `json:"csl"`
			}
			if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
				return nil, err
			}
			switch request.Query {
			case ".get kusto identity token":
				body = `{"Tables":[{"TableName":"Table","Columns":[{"ColumnName":"AuthorizationContext","ColumnType":"string"}],"Rows":[["test"]]}]}`
			case ".get ingestion resources":
				body = `{"Tables":[{"TableName":"Table","Columns":[{"ColumnName":"ResourceTypeName","ColumnType":"string"},{"ColumnName":"StorageRoot","ColumnType":"string"}],"Rows":[["TempStorage","https://storage.invalid/container"],["SecuredReadyForAggregationQueue","https://storage.invalid/queue"]]}]}`
			default:
				return nil, fmt.Errorf("unexpected management command: %s", request.Query)
			}
		case r.Method == http.MethodPut:
			payload, err := io.ReadAll(r.Body)
			if err != nil {
				return nil, err
			}
			uploads = append(uploads, payload)
			status = http.StatusCreated
		case r.URL.Path == "/queue/messages":
			var message struct {
				Text string `xml:"MessageText"`
			}
			if err := xml.NewDecoder(r.Body).Decode(&message); err != nil {
				return nil, err
			}
			decoded, err := base64.StdEncoding.DecodeString(message.Text)
			if err != nil {
				return nil, err
			}
			messages = append(messages, decoded)
			body, status = `<QueueMessagesList/>`, http.StatusCreated
		default:
			return nil, fmt.Errorf("unexpected request: %s %s", r.Method, r.URL)
		}
		return &http.Response{StatusCode: status, Header: http.Header{"Content-Type": []string{"application/json"}}, Body: io.NopCloser(strings.NewReader(body)), Request: r}, nil
	})}
	ingestor, err := azkustoingest.New(azkustodata.NewConnectionStringBuilder("http://localhost"), azkustoingest.WithHttpClient(client), azkustoingest.WithDefaultDatabase("test"), azkustoingest.WithDefaultTable("ciFailurePatterns"))
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, ingestor.Close()) })
	i := sampleInput()
	i.observation.Text = "🧪\r\ncontext deadline exceeded\x00"
	row := computedRow(i)
	batches, err := encodeBatches([]resultRow{row})
	require.NoError(t, err)
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	// Both submissions really upload and enqueue. No permanent batch tag can
	// suppress a later repair of a missing computation.
	for range 2 {
		require.NoError(t, tenantkusto.IngestJSONLines(ctx, ingestor, "ciFailurePatternsMapping", bytes.NewBuffer(batches[0].Bytes())))
	}
	require.Len(t, uploads, 2)
	require.Len(t, messages, 2)
	for index, message := range messages {
		var decoded struct {
			Database string `json:"DatabaseName"`
			Table    string `json:"TableName"`
			Extra    struct {
				Format      string `json:"format"`
				Mapping     string `json:"ingestionMappingReference"`
				Tags        string `json:"tags"`
				IfNotExists string `json:"ingestIfNotExists"`
			} `json:"AdditionalProperties"`
		}
		require.NoError(t, json.Unmarshal(message, &decoded))
		require.Equal(t, "test", decoded.Database)
		require.Equal(t, "ciFailurePatterns", decoded.Table)
		require.Equal(t, "multijson", decoded.Extra.Format)
		require.Equal(t, "ciFailurePatternsMapping", decoded.Extra.Mapping)
		require.Empty(t, decoded.Extra.Tags)
		require.Empty(t, decoded.Extra.IfNotExists)
		reader, err := gzip.NewReader(bytes.NewReader(uploads[index]))
		require.NoError(t, err)
		payload, err := io.ReadAll(reader)
		require.NoError(t, err)
		require.NoError(t, reader.Close())
		require.Equal(t, batches[0].Bytes(), payload)
	}
}

func completeFixture(t *testing.T, schema map[string]string, values []map[string]any, terminal bool) string {
	t.Helper()
	names := make([]string, 0, len(schema))
	for name := range schema {
		names = append(names, name)
	}
	sort.Strings(names)
	columns := make([]map[string]string, 0, len(schema))
	for _, name := range names {
		columns = append(columns, map[string]string{"ColumnName": name, "ColumnType": schema[name]})
	}
	rows := make([][]any, 0, len(values))
	for _, value := range values {
		row := make([]any, len(names))
		for index, name := range names {
			row[index] = value[name]
		}
		rows = append(rows, row)
	}
	encodedColumns, err := json.Marshal(columns)
	require.NoError(t, err)
	encodedRows, err := json.Marshal(rows)
	require.NoError(t, err)
	frames := []string{`{"FrameType":"DataSetHeader","IsProgressive":false,"Version":"v2.0","IsFragmented":true,"ErrorReportingPlacement":"EndOfTable"}`,
		`{"FrameType":"DataTable","TableId":0,"TableKind":"QueryProperties","TableName":"@ExtendedProperties","Columns":[{"ColumnName":"TableId","ColumnType":"int"},{"ColumnName":"Key","ColumnType":"string"},{"ColumnName":"Value","ColumnType":"dynamic"}],"Rows":[]}`,
		fmt.Sprintf(`{"FrameType":"TableHeader","TableId":1,"TableKind":"PrimaryResult","TableName":"PrimaryResult","Columns":%s}`, encodedColumns),
		fmt.Sprintf(`{"FrameType":"TableFragment","TableFragmentType":"DataAppend","TableId":1,"Rows":%s}`, encodedRows),
		fmt.Sprintf(`{"FrameType":"TableCompletion","TableId":1,"RowCount":%d}`, len(rows))}
	if terminal {
		frames = append(frames, `{"FrameType":"DataSetCompletion","HasErrors":false,"Cancelled":false}`)
	}
	return "[" + strings.Join(frames, "\n,") + "\n]\n"
}

func newFixtureSDK(t *testing.T, handler http.HandlerFunc) *azkustodata.Client {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/v1/rest/auth/metadata" {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		handler(w, r)
	}))
	t.Cleanup(server.Close)
	client := server.Client()
	client.Transport = kusto.CompleteTransport{Base: client.Transport}
	sdk, err := azkustodata.New(azkustodata.NewConnectionStringBuilder(server.URL), azkustodata.WithHttpClient(client))
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, sdk.Close()) })
	return sdk
}

func TestConsumerCompleteInputReadsPreserveUnicodeAndRejectPartialShape(t *testing.T) {
	for _, test := range []struct {
		name                 string
		terminal, omitColumn bool
	}{{"complete", true, false}, {"missing terminal", false, false}, {"missing column", true, true}} {
		t.Run(test.name, func(t *testing.T) {
			c := newTestController(t)
			const build = "🧪'); .drop table ciTestResults; //"
			const text = "中文🙂𐐷e\u0301\r\ncontext deadline exceeded\x00"
			values := map[string]any{"origin": c.origin(), "buildId": build, "source": "e2e", "testId": "id", "message": text, "testName": "", "nameStatus": "conflicting", "nameVariantCount": int64(2), "multiplicity": int64(3), "startedAt": "0001-01-01T00:00:00Z", "finishedAt": "9999-12-31T23:59:59.9999999Z"}
			schema := map[string]string{}
			for name, kind := range inputColumns {
				schema[name] = kind
			}
			if test.omitColumn {
				delete(schema, "testName")
			}
			sdk := newFixtureSDK(t, func(w http.ResponseWriter, r *http.Request) {
				var body struct {
					CSL        string `json:"csl"`
					Properties struct {
						Parameters map[string]string
						Options    map[string]any
					} `json:"properties"`
				}
				require.NoError(t, json.NewDecoder(r.Body).Decode(&body))
				require.NotContains(t, body.CSL, build)
				require.Contains(t, body.CSL, "request:dynamic")
				parameter := body.Properties.Parameters["request"]
				require.True(t, strings.HasPrefix(parameter, "dynamic("))
				var payload map[string]any
				require.NoError(t, json.Unmarshal([]byte(strings.TrimSuffix(strings.TrimPrefix(parameter, "dynamic("), ")")), &payload))
				require.Equal(t, build, payload["buildId"])
				require.Equal(t, "strongconsistency", body.Properties.Options["queryconsistency"])
				require.Equal(t, "00:00:00", body.Properties.Options["query_results_cache_max_age"])
				_, _ = io.WriteString(w, completeFixture(t, schema, []map[string]any{values}, test.terminal))
			})
			inputs, err := c.loadInputs(t.Context(), sdk, build)
			if !test.terminal || test.omitColumn {
				require.Error(t, err)
				require.Nil(t, inputs)
				return
			}
			require.NoError(t, err)
			require.Len(t, inputs, 1)
			require.Equal(t, text, inputs[0].observation.Text)
			require.EqualValues(t, 3, inputs[0].multiplicity)
			require.Empty(t, inputs[0].observation.TestName)
			require.True(t, inputs[0].observation.StartedAt.IsZero())
		})
	}
	c := newTestController(t)
	sdk := newFixtureSDK(t, func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, completeFixture(t, map[string]string{"origin": "string"}, nil, true))
	})
	inputs, err := c.loadInputs(t.Context(), sdk, "")
	require.Error(t, err)
	require.Nil(t, inputs, "empty table without required columns is not an empty population")
}

func TestCompleteResultReadRejectsMalformedFindings(t *testing.T) {
	for _, malformed := range []bool{false, true} {
		c := newTestController(t)
		i := sampleInput()
		row := computedRow(i)
		encoded, err := json.Marshal(row)
		require.NoError(t, err)
		var values map[string]any
		require.NoError(t, json.Unmarshal(encoded, &values))
		if malformed {
			values["findings"] = []any{map[string]any{"canonical": "x", "key": "k", "rule_id": "r", "outcome": "matched", "locations": []any{map[string]any{"end": 1}}}}
		}
		sdk := newFixtureSDK(t, func(w http.ResponseWriter, r *http.Request) {
			_, _ = io.WriteString(w, completeFixture(t, resultColumns, []map[string]any{values}, true))
		})
		rows, err := c.loadResults(t.Context(), sdk, []input{i}, compiledFailurePatternVersion())
		if malformed {
			require.Error(t, err)
			require.Nil(t, rows)
			continue
		}
		require.NoError(t, err)
		_, err = validatedVisibleResults([]input{i}, compiledFailurePatternVersion(), rows)
		require.NoError(t, err)
	}
}

func TestMappingRequiresEveryTypedColumnAndExactPath(t *testing.T) {
	var entries []map[string]any
	for column, kind := range resultColumns {
		entries = append(entries, map[string]any{"Column": column, "DataType": kind, "Properties": map[string]string{"Path": "$['" + column + "']"}})
	}
	encode := func() string { encoded, err := json.Marshal(entries); require.NoError(t, err); return string(encoded) }
	require.NoError(t, validateMapping(encode()))
	entries = append(entries, entries[0])
	require.ErrorContains(t, validateMapping(encode()), "duplicate mapping")
	entries = entries[:len(entries)-1]
	entries[0]["Properties"] = map[string]string{"Path": "$.wrong"}
	require.ErrorContains(t, validateMapping(encode()), "invalid mapping")
	entries[0]["Properties"] = map[string]string{"Path": "$['" + entries[0]["Column"].(string) + "']"}
	entries[0]["DataType"] = "bool"
	require.Error(t, validateMapping(encode()))
	entries = entries[1:]
	require.Error(t, validateMapping(encode()))
}

func TestCompleteBuildIDReadsPreserveUnicodeAndRejectInvalidPages(t *testing.T) {
	const cursor = "🧪'); .drop table ciTestResults; //"
	// Newest first: strictly descending and strictly before the cursor.
	validIDs := make([]string, discoveryPageSize)
	for index := range validIDs {
		validIDs[index] = fmt.Sprintf("build-%04d", discoveryPageSize-index)
	}
	for _, scenario := range []struct {
		name     string
		schema   map[string]string
		ids      []string
		newest   bool
		terminal bool
		wantErr  bool
	}{
		{name: "full page", ids: validIDs, terminal: true},
		{name: "short page", ids: validIDs[:2], terminal: true},
		{name: "newest page without cursor", ids: []string{"b", "a"}, newest: true, terminal: true},
		{name: "empty page", terminal: true},
		{name: "missing terminal", ids: validIDs[:2], wantErr: true},
		{name: "missing column", schema: map[string]string{"other": "string"}, terminal: true, wantErr: true},
		{name: "wrong column type", schema: map[string]string{"buildId": "long"}, terminal: true, wantErr: true},
		{name: "oversized page", ids: append(append([]string(nil), validIDs...), "a-overflow"), terminal: true, wantErr: true},
		{name: "empty ID", ids: []string{""}, terminal: true, wantErr: true},
		{name: "after cursor", ids: []string{cursor + "-newer"}, terminal: true, wantErr: true},
		{name: "equal cursor", ids: []string{cursor}, terminal: true, wantErr: true},
		{name: "duplicate ID", ids: []string{validIDs[0], validIDs[0]}, terminal: true, wantErr: true},
		{name: "ascending page", ids: []string{validIDs[1], validIDs[0]}, terminal: true, wantErr: true},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			c := newTestController(t)
			schema := scenario.schema
			if schema == nil {
				schema = map[string]string{"buildId": "string"}
			}
			values := make([]map[string]any, len(scenario.ids))
			for index, id := range scenario.ids {
				values[index] = map[string]any{"buildId": id}
			}
			sdk := newFixtureSDK(t, func(w http.ResponseWriter, r *http.Request) {
				var body struct {
					CSL        string `json:"csl"`
					Properties struct {
						Parameters map[string]string
					} `json:"properties"`
				}
				require.NoError(t, json.NewDecoder(r.Body).Decode(&body))
				before := cursor
				if scenario.newest {
					before = ""
				}
				require.NotContains(t, body.CSL, cursor, "arbitrary cursor remains data, not KQL")
				parameter := body.Properties.Parameters["request"]
				require.True(t, strings.HasPrefix(parameter, "dynamic("))
				var payload map[string]any
				require.NoError(t, json.Unmarshal([]byte(strings.TrimSuffix(strings.TrimPrefix(parameter, "dynamic("), ")")), &payload))
				require.Equal(t, before, payload["beforeBuildID"])
				_, _ = io.WriteString(w, completeFixture(t, schema, values, scenario.terminal))
			})
			before := cursor
			if scenario.newest {
				before = ""
			}
			ids, err := c.loadBuildIDs(t.Context(), sdk, before, discoveryPageSize)
			if scenario.wantErr {
				require.Error(t, err)
				require.Nil(t, ids, "an incomplete or invalid page never advances discovery")
				return
			}
			require.NoError(t, err)
			require.Len(t, ids, len(scenario.ids))
			if len(scenario.ids) > 0 {
				require.Equal(t, scenario.ids, ids)
			}
		})
	}
}
