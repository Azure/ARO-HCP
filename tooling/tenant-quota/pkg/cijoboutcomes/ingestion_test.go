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
	"bytes"
	"compress/gzip"
	"context"
	"encoding/base64"
	"encoding/json"
	"encoding/xml"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/Azure/azure-kusto-go/azkustodata"
	"github.com/Azure/azure-kusto-go/azkustoingest"
)

type ingestionTransport func(*http.Request) (*http.Response, error)

func (f ingestionTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	return f(r)
}

func TestIngestRowsWireFormat(t *testing.T) {
	for _, tc := range []struct {
		table, tag string
	}{
		{"ciJobOutcomes", runTag("123")},
		{"ciTestResults", testsTag("123")},
		{"ciTestNames", ""},
		{"ciJobOutcomes", "run-\"quoted\\tag"},
	} {
		t.Run(tc.table+"/"+tc.tag, func(t *testing.T) {
			messages, uploads := make(chan []byte, 1), make(chan []byte, 1)
			client := &http.Client{Transport: ingestionTransport(func(r *http.Request) (*http.Response, error) {
				var body string
				status := http.StatusOK
				switch {
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
					uploads <- payload
					status = http.StatusCreated
				case r.URL.Path == "/queue/messages":
					var message struct {
						Text string `xml:"MessageText"`
					}
					if err := xml.NewDecoder(r.Body).Decode(&message); err != nil {
						return nil, err
					}
					messages <- []byte(message.Text)
					body, status = `<QueueMessagesList/>`, http.StatusCreated
				default:
					return nil, fmt.Errorf("unexpected request: %s %s", r.Method, r.URL)
				}
				return &http.Response{StatusCode: status, Header: http.Header{"Content-Type": []string{"application/json"}}, Body: io.NopCloser(strings.NewReader(body)), Request: r}, nil
			})}
			ingestor, err := azkustoingest.New(azkustodata.NewConnectionStringBuilder("http://localhost"),
				azkustoingest.WithHttpClient(client), azkustoingest.WithDefaultDatabase("ServiceLogs"),
				azkustoingest.WithDefaultTable(tc.table))
			require.NoError(t, err)
			t.Cleanup(func() { require.NoError(t, ingestor.Close()) })
			target := &tableIngestor{name: tc.table, mapping: tc.table + "Mapping", ingestor: ingestor}
			rows := []map[string]string{{"buildId": "123"}, {"buildId": "456"}}
			ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
			defer cancel()
			require.NoError(t, ingestRows(ctx, target, rows, tc.tag))
			var message struct {
				Database string `json:"DatabaseName"`
				Table    string `json:"TableName"`
				Extra    struct {
					Format      string `json:"format"`
					Mapping     string `json:"ingestionMappingReference"`
					MappingType string `json:"ingestionMappingType"`
					Tags        string `json:"tags"`
					IfNotExists string `json:"ingestIfNotExists"`
				} `json:"AdditionalProperties"`
			}
			decoded, err := base64.StdEncoding.DecodeString(string(<-messages))
			require.NoError(t, err)
			require.NoError(t, json.Unmarshal(decoded, &message))
			require.Equal(t, "ServiceLogs", message.Database)
			require.Equal(t, tc.table, message.Table)
			require.Equal(t, "multijson", message.Extra.Format)
			require.Equal(t, target.mapping, message.Extra.Mapping)
			require.Equal(t, "Json", message.Extra.MappingType)
			if tc.tag == "" {
				require.Empty(t, message.Extra.Tags)
				require.Empty(t, message.Extra.IfNotExists)
			} else {
				var tags, ifNotExists []string
				require.NoError(t, json.Unmarshal([]byte(message.Extra.Tags), &tags))
				require.NoError(t, json.Unmarshal([]byte(message.Extra.IfNotExists), &ifNotExists))
				require.Equal(t, []string{"ingest-by:" + tc.tag}, tags)
				require.Equal(t, []string{tc.tag}, ifNotExists)
			}
			reader, err := gzip.NewReader(bytes.NewReader(<-uploads))
			require.NoError(t, err)
			payload, err := io.ReadAll(reader)
			require.NoError(t, err)
			require.NoError(t, reader.Close())
			require.Equal(t, "{\"buildId\":\"123\"}\n{\"buildId\":\"456\"}\n", string(payload))
		})
	}
}
