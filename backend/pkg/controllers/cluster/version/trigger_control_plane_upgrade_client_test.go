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

package version

import (
	"encoding/base64"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	sdk "github.com/openshift-online/ocm-sdk-go"

	"github.com/Azure/ARO-HCP/internal/ocm"
)

type upgradeRoundTripper func(*http.Request) (*http.Response, error)

func (f upgradeRoundTripper) RoundTrip(req *http.Request) (*http.Response, error) { return f(req) }

// Exercise the real SDK/client rather than mocking away RawID or GetCluster's
// linked-autoscaler request. All traffic is handled by the in-memory transport.
func TestTriggerControlPlaneUpgradeCSClientContract(t *testing.T) {
	const policyPath = testCSClusterIDStr + "/control_plane_upgrade_policies"
	const autoscalerPath = testCSClusterIDStr + "/autoscaler"
	for _, tc := range []struct {
		name, versionJSON, wantErr string
		autoscalerFailure          bool
	}{
		{name: "raw source version and linked autoscaler", versionJSON: `"version":{"id":"openshift-v4.19.1","raw_id":"4.19.1"},`},
		{name: "missing version", wantErr: "missing CS source version"},
		// The SDK represents an explicit null version as an empty Version object.
		{name: "null version", versionJSON: `"version":null,`, wantErr: "source version is empty"},
		{name: "missing raw ID", versionJSON: `"version":{"id":"openshift-v4.19.1"},`, wantErr: "source version is empty"},
		{name: "linked autoscaler failure prevents POST", versionJSON: `"version":{"raw_id":"4.19.1"},`, autoscalerFailure: true, wantErr: "linked autoscaler unavailable"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var requests []string
			posts := 0
			transport := upgradeRoundTripper(func(req *http.Request) (*http.Response, error) {
				requests = append(requests, req.Method+" "+req.URL.Path)
				status, body := http.StatusOK, ""
				switch {
				case req.Method == http.MethodGet && req.URL.Path == policyPath:
					require.Equal(t, "creation_timestamp desc", req.URL.Query().Get("order"))
					body = `{"kind":"ControlPlaneUpgradePolicyList","page":1,"size":0,"total":0,"items":[]}`
				case req.Method == http.MethodGet && req.URL.Path == testCSClusterIDStr:
					body = `{` + tc.versionJSON + `"autoscaler":{"kind":"ClusterAutoscalerLink","href":"` + autoscalerPath + `"}}`
				case req.Method == http.MethodGet && req.URL.Path == autoscalerPath:
					if tc.autoscalerFailure {
						status = http.StatusBadRequest
						body = `{"kind":"Error","id":"400","reason":"linked autoscaler unavailable"}`
					} else {
						body = `{"kind":"ClusterAutoscaler","balance_similar_node_groups":true}`
					}
				case req.Method == http.MethodPost && req.URL.Path == policyPath:
					posts++
					data, err := io.ReadAll(req.Body)
					require.NoError(t, err)
					require.JSONEq(t, `{"kind":"ControlPlaneUpgradePolicy","version":"4.20.2"}`, string(data))
					status = http.StatusCreated
					body = `{"kind":"ControlPlaneUpgradePolicy","version":"4.20.2"}`
				default:
					return nil, fmt.Errorf("unexpected request: %s %s", req.Method, req.URL)
				}
				return &http.Response{StatusCode: status, Header: http.Header{"Content-Type": []string{"application/json"}}, Body: io.NopCloser(strings.NewReader(body)), Request: req}, nil
			})
			// The SDK reads claims without signature verification; this synthetic
			// token is only used against the transport above.
			header := base64.RawURLEncoding.EncodeToString([]byte(`{"alg":"RS256","typ":"JWT"}`))
			claims := base64.RawURLEncoding.EncodeToString([]byte(fmt.Sprintf(`{"typ":"Bearer","exp":%d}`, time.Now().Add(time.Hour).Unix())))
			conn, err := sdk.NewConnectionBuilder().URL("https://cs.example.invalid").Tokens(header + "." + claims + ".signature").RetryLimit(0).TransportWrapper(func(http.RoundTripper) http.RoundTripper { return transport }).Build()
			require.NoError(t, err)
			t.Cleanup(func() { require.NoError(t, conn.Close()) })
			syncer, _, _ := newUpgradeTestSyncer(t, ocm.NewClusterServiceClient(conn))
			err = syncer.SyncOnce(t.Context(), upgradeTestKey())
			expectedRequests := []string{"GET " + policyPath, "GET " + testCSClusterIDStr, "GET " + autoscalerPath}
			if tc.wantErr != "" {
				require.ErrorContains(t, err, tc.wantErr)
				require.Zero(t, posts)
			} else {
				require.NoError(t, err)
				require.Equal(t, 1, posts)
				expectedRequests = append(expectedRequests, "POST "+policyPath)
			}
			require.Equal(t, expectedRequests, requests)
		})
	}
}
