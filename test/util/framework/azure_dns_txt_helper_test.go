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

package framework

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	azcorearm "github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"
	azfake "github.com/Azure/azure-sdk-for-go/sdk/azcore/fake"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/policy"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/to"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/dns/armdns"
)

func TestPresentAzureDNSTXTPreservesOtherWriters(t *testing.T) {
	for _, scenario := range []string{"new record", "existing record", "preexisting token", "ETag conflict", "lost write response"} {
		t.Run(scenario, func(t *testing.T) {
			const token = "digicert-validation-token"
			var record *armdns.RecordSet
			if scenario != "new record" {
				record = &armdns.RecordSet{Etag: to.Ptr("1"), Properties: &armdns.RecordSetProperties{
					TTL: to.Ptr[int64](600), Metadata: map[string]*string{"owner": to.Ptr("other-writer")},
					TxtRecords: []*armdns.TxtRecord{{Value: []*string{to.Ptr("original")}}},
				}}
				if scenario == "preexisting token" {
					record.Properties.TxtRecords = append(record.Properties.TxtRecords, &armdns.TxtRecord{Value: []*string{to.Ptr(token)}})
				}
			}
			puts, deletes := 0, 0
			respond := func(status int, value any) (*http.Response, error) {
				data, err := json.Marshal(value)
				require.NoError(t, err)
				return &http.Response{StatusCode: status, Header: http.Header{"Content-Type": {"application/json"}}, Body: io.NopCloser(strings.NewReader(string(data)))}, nil
			}
			transport := &fakeTransport{do: func(req *http.Request) (*http.Response, error) {
				assert.True(t, strings.HasSuffix(req.URL.Path, "/dnsZones/example.com/TXT/apps"), req.URL.Path)
				switch req.Method {
				case http.MethodGet:
					if record == nil {
						return respond(http.StatusNotFound, map[string]any{"error": map[string]string{"code": "NotFound"}})
					}
					return respond(http.StatusOK, record)
				case http.MethodPut:
					puts++
					if record == nil {
						assert.Equal(t, "*", req.Header.Get("If-None-Match"))
					} else {
						assert.Equal(t, *record.Etag, req.Header.Get("If-Match"))
					}
					if scenario == "ETag conflict" && puts == 1 {
						record.Properties.TxtRecords = append(record.Properties.TxtRecords, &armdns.TxtRecord{Value: []*string{to.Ptr("concurrent-value")}})
						record.Etag = to.Ptr("concurrent-etag")
						return respond(http.StatusPreconditionFailed, map[string]any{"error": map[string]string{"code": "PreconditionFailed"}})
					}
					var updated armdns.RecordSet
					require.NoError(t, json.NewDecoder(req.Body).Decode(&updated))
					updated.Etag = to.Ptr(fmt.Sprint(puts + 1))
					record = &updated
					if scenario == "lost write response" && puts == 1 {
						return respond(http.StatusServiceUnavailable, map[string]any{"error": map[string]string{"code": "Unavailable"}})
					}
					return respond(http.StatusOK, record)
				case http.MethodDelete:
					deletes++
					assert.Equal(t, *record.Etag, req.Header.Get("If-Match"))
					record = nil
					return respond(http.StatusNoContent, nil)
				default:
					t.Fatalf("unexpected Azure DNS method %s", req.Method)
					return nil, nil
				}
			}}
			client, err := armdns.NewRecordSetsClient("subscription", &azfake.TokenCredential{}, &azcorearm.ClientOptions{
				ClientOptions: azcore.ClientOptions{Transport: transport, Retry: policy.RetryOptions{MaxRetries: -1}},
			})
			require.NoError(t, err)
			cleanup, err := PresentAzureDNSTXT(t.Context(), client, "rg", "example.com", "apps", token)
			if scenario == "lost write response" {
				require.Error(t, err)
			} else {
				require.NoError(t, err)
			}
			require.NotNil(t, cleanup)
			require.NotNil(t, record)
			if scenario != "new record" {
				assert.Equal(t, int64(600), *record.Properties.TTL)
				assert.Equal(t, "other-writer", *record.Properties.Metadata["owner"])
				// Changes made after presentation must survive cleanup as well.
				record.Properties.TxtRecords = append(record.Properties.TxtRecords, &armdns.TxtRecord{Value: []*string{to.Ptr("later-value")}})
				record.Etag = to.Ptr("later-etag")
			}
			require.NoError(t, cleanup(t.Context()))
			if scenario == "new record" {
				assert.Nil(t, record)
				assert.Equal(t, 1, deletes)
				return
			}
			assert.Zero(t, deletes)
			var values []string
			for _, txt := range record.Properties.TxtRecords {
				values = append(values, *txt.Value[0])
			}
			assert.Contains(t, values, "original")
			assert.Contains(t, values, "later-value")
			if scenario == "preexisting token" {
				assert.Contains(t, values, token, "cleanup must preserve a token it did not add")
				assert.Zero(t, puts)
			} else {
				assert.NotContains(t, values, token)
			}
			if scenario == "ETag conflict" {
				assert.Contains(t, values, "concurrent-value")
			}
		})
	}
}
