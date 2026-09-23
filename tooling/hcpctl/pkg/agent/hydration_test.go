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

package agent

import (
	"bytes"
	"compress/gzip"
	"encoding/base64"
	"io"
	"net/url"
	"strings"
	"testing"
)

func TestQueryToDeepLink(t *testing.T) {
	tests := []struct {
		name          string
		kustoEndpoint string
		kustoDatabase string
		query         string
		wantURL       string
	}{
		{
			name:          "standard endpoint produces dataexplorer.azure.com link",
			kustoEndpoint: "https://hcp-stg-uk-2.uksouth.kusto.windows.net",
			kustoDatabase: "ServiceLogs",
			query:         "traces | take 10",
			wantURL:       "https://dataexplorer.azure.com/clusters/hcp-stg-uk-2.uksouth/databases/ServiceLogs",
		},
		{
			name:          "different cluster and database",
			kustoEndpoint: "https://mycluster.eastus.kusto.windows.net",
			kustoDatabase: "MyDB",
			query:         "StormEvents | count",
			wantURL:       "https://dataexplorer.azure.com/clusters/mycluster.eastus/databases/MyDB",
		},
		{
			name:          "endpoint with trailing slash",
			kustoEndpoint: "https://hcp-prod.westeurope.kusto.windows.net/",
			kustoDatabase: "Logs",
			query:         "requests | take 1",
			wantURL:       "https://dataexplorer.azure.com/clusters/hcp-prod.westeurope/databases/Logs",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := queryToDeepLink(tt.kustoEndpoint, tt.kustoDatabase, tt.query)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}

			// Parse the result to validate structure using url.URL rather than string matching.
			parsed, err := url.Parse(got)
			if err != nil {
				t.Fatalf("result is not a valid URL: %v", err)
			}

			// Reconstruct the URL without query to compare scheme+host+path.
			withoutQuery := &url.URL{
				Scheme: parsed.Scheme,
				Host:   parsed.Host,
				Path:   parsed.Path,
			}
			if withoutQuery.String() != tt.wantURL {
				t.Errorf("URL structure mismatch\ngot:  %s\nwant: %s", withoutQuery.String(), tt.wantURL)
			}

			// Verify the query parameter is valid gzip+base64 that decodes back to the original query.
			encodedQuery := parsed.Query().Get("query")
			if encodedQuery == "" {
				t.Fatal("missing 'query' parameter in result URL")
			}

			compressed, err := base64.StdEncoding.DecodeString(encodedQuery)
			if err != nil {
				t.Fatalf("failed to base64 decode query param: %v", err)
			}

			reader, err := gzip.NewReader(bytes.NewReader(compressed))
			if err != nil {
				t.Fatalf("failed to create gzip reader: %v", err)
			}
			defer reader.Close()

			decompressed, err := io.ReadAll(reader)
			if err != nil {
				t.Fatalf("failed to decompress: %v", err)
			}

			if string(decompressed) != tt.query {
				t.Errorf("round-trip query mismatch\ngot:  %q\nwant: %q", string(decompressed), tt.query)
			}
		})
	}
}

func TestQueryToDeepLink_invalidEndpoint(t *testing.T) {
	_, err := queryToDeepLink("://not-a-url", "DB", "query")
	if err == nil {
		t.Fatal("expected error for invalid endpoint, got nil")
	}
	if !strings.Contains(err.Error(), "failed to parse kusto endpoint") {
		t.Errorf("unexpected error message: %v", err)
	}
}
