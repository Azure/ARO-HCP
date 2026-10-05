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
	"io"
	"maps"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	azcorearm "github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"
	azfake "github.com/Azure/azure-sdk-for-go/sdk/azcore/fake"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/to"

	"github.com/Azure/ARO-HCP/internal/api/metadataapi"
	hcpsdk20240610preview "github.com/Azure/ARO-HCP/test/sdk/v20240610preview/resourcemanager/redhatopenshifthcp/armredhatopenshifthcp"
	hcpsdk20251223preview "github.com/Azure/ARO-HCP/test/sdk/v20251223preview/resourcemanager/redhatopenshifthcp/armredhatopenshifthcp"
	hcpsdk20260630preview "github.com/Azure/ARO-HCP/test/sdk/v20260630preview/resourcemanager/redhatopenshifthcp/armredhatopenshifthcp"
	hcpsdk20260901preview "github.com/Azure/ARO-HCP/test/sdk/v20260901preview/resourcemanager/redhatopenshifthcp/armredhatopenshifthcp"
	hcpsdk20261001preview "github.com/Azure/ARO-HCP/test/sdk/v20261001preview/resourcemanager/redhatopenshifthcp/armredhatopenshifthcp"
)

func TestUpdateHelpersPreserveOmittedTags(t *testing.T) {
	helpers := []struct {
		name       string
		timeoutTag string
		update     func(*testing.T, *azcorearm.ClientOptions, map[string]*string) error
	}{
		{
			name: "cluster 20240610", timeoutTag: metadataapi.TagClusterMaxUpdateDuration,
			update: func(t *testing.T, options *azcorearm.ClientOptions, tags map[string]*string) error {
				client, err := hcpsdk20240610preview.NewHcpOpenShiftClustersClient(fakeSubscriptionID, &azfake.TokenCredential{}, options)
				require.NoError(t, err)
				_, err = UpdateHCPCluster20240610(t.Context(), client, "rg", "cluster", hcpsdk20240610preview.HcpOpenShiftClusterUpdate{Tags: tags}, 10*time.Minute)
				return err
			},
		},
		{
			name: "cluster 20251223", timeoutTag: metadataapi.TagClusterMaxUpdateDuration,
			update: func(t *testing.T, options *azcorearm.ClientOptions, tags map[string]*string) error {
				client, err := hcpsdk20251223preview.NewHcpOpenShiftClustersClient(fakeSubscriptionID, &azfake.TokenCredential{}, options)
				require.NoError(t, err)
				_, err = UpdateHCPCluster20251223(t.Context(), client, "rg", "cluster", hcpsdk20251223preview.HcpOpenShiftClusterUpdate{Tags: tags}, 10*time.Minute)
				return err
			},
		},
		{
			name: "cluster 20260630", timeoutTag: metadataapi.TagClusterMaxUpdateDuration,
			update: func(t *testing.T, options *azcorearm.ClientOptions, tags map[string]*string) error {
				client, err := hcpsdk20260630preview.NewHcpOpenShiftClustersClient(fakeSubscriptionID, &azfake.TokenCredential{}, options)
				require.NoError(t, err)
				_, err = UpdateHCPCluster20260630(t.Context(), client, "rg", "cluster", hcpsdk20260630preview.HcpOpenShiftClusterUpdate{Tags: tags}, 10*time.Minute)
				return err
			},
		},
		{
			name: "cluster 20260901", timeoutTag: metadataapi.TagClusterMaxUpdateDuration,
			update: func(t *testing.T, options *azcorearm.ClientOptions, tags map[string]*string) error {
				client, err := hcpsdk20260901preview.NewHcpOpenShiftClustersClient(fakeSubscriptionID, &azfake.TokenCredential{}, options)
				require.NoError(t, err)
				_, err = UpdateHCPCluster20260901(t.Context(), client, "rg", "cluster", hcpsdk20260901preview.HcpOpenShiftClusterUpdate{Tags: tags}, 10*time.Minute)
				return err
			},
		},
		{
			name: "cluster 20261001", timeoutTag: metadataapi.TagClusterMaxUpdateDuration,
			update: func(t *testing.T, options *azcorearm.ClientOptions, tags map[string]*string) error {
				client, err := hcpsdk20261001preview.NewHcpOpenShiftClustersClient(fakeSubscriptionID, &azfake.TokenCredential{}, options)
				require.NoError(t, err)
				_, err = UpdateHCPCluster20261001(t.Context(), client, "rg", "cluster", hcpsdk20261001preview.HcpOpenShiftCluster{Tags: tags}, 10*time.Minute)
				return err
			},
		},
		{
			name: "node pool 20240610", timeoutTag: metadataapi.TagNodePoolMaxUpdateDuration,
			update: func(t *testing.T, options *azcorearm.ClientOptions, tags map[string]*string) error {
				client, err := hcpsdk20240610preview.NewNodePoolsClient(fakeSubscriptionID, &azfake.TokenCredential{}, options)
				require.NoError(t, err)
				_, err = UpdateNodePoolAndWait20240610(t.Context(), client, "rg", "cluster", "pool", hcpsdk20240610preview.NodePoolUpdate{Tags: tags}, 10*time.Minute)
				return err
			},
		},
	}
	for _, helper := range helpers {
		t.Run(helper.name, func(t *testing.T) {
			existing := map[string]*string{
				"customer":                                     to.Ptr("keep-me"),
				metadataapi.TagClusterSizeOverride:             to.Ptr("small"),
				metadataapi.TagClusterControlPlaneExactVersion: to.Ptr("4.21.0"),
				metadataapi.TagClusterMaxDeletionDuration:      to.Ptr("30m"),
				strings.ToUpper(helper.timeoutTag):             to.Ptr("1m"),
			}
			for _, tc := range []struct {
				name         string
				existingTags map[string]*string
				updateTags   map[string]*string
				getFails     bool
			}{
				{name: "omitted tags preserve current tags", existingTags: existing},
				{name: "untagged resource", existingTags: nil},
				{name: "explicit empty tags replace current tags", existingTags: existing, updateTags: map[string]*string{}},
				{name: "explicit tags replace current tags", existingTags: existing, updateTags: map[string]*string{"replacement": to.Ptr("new"), strings.ToUpper(helper.timeoutTag): to.Ptr("1m")}},
				{name: "failed tag read prevents update", existingTags: existing, getFails: true},
			} {
				t.Run(tc.name, func(t *testing.T) {
					original := maps.Clone(tc.updateTags)
					storedTags := tc.existingTags
					var sentTags map[string]*string
					getsBeforeUpdate, patches := 0, 0
					transport := &fakeTransport{do: func(req *http.Request) (*http.Response, error) {
						status := http.StatusOK
						var body []byte
						switch req.Method {
						case http.MethodGet:
							if patches == 0 {
								getsBeforeUpdate++
							}
							if tc.getFails {
								status = http.StatusForbidden
								body = []byte(`{"error":{"code":"AuthorizationFailed","message":"cannot read tags"}}`)
							}
						case http.MethodPatch:
							patches++
							var payload struct {
								Tags map[string]*string `json:"tags"`
							}
							require.NoError(t, json.NewDecoder(req.Body).Decode(&payload))
							sentTags = payload.Tags
							storedTags = sentTags
						default:
							t.Fatalf("unexpected HTTP method %s", req.Method)
						}
						if body == nil {
							var err error
							body, err = json.Marshal(map[string]any{
								"tags":       storedTags,
								"properties": map[string]string{"provisioningState": "Succeeded"},
							})
							require.NoError(t, err)
						}
						return &http.Response{
							StatusCode: status, Header: http.Header{"Content-Type": {"application/json"}},
							Body: io.NopCloser(strings.NewReader(string(body))), Request: req,
						}, nil
					}}
					err := helper.update(t, fakeClientOptions(transport), tc.updateTags)
					require.Equal(t, original, tc.updateTags, "helper must not modify caller tags")
					if tc.getFails {
						require.ErrorContains(t, err, "failed reading tags before updating")
						require.Equal(t, 0, patches, "failed tag read must not send a replacement")
						return
					}
					require.NoError(t, err)
					require.Equal(t, 1, patches, "helper must submit the update")
					expected := maps.Clone(tc.updateTags)
					if tc.updateTags == nil {
						require.Equal(t, 1, getsBeforeUpdate, "omitted tags require a current resource read")
						expected = maps.Clone(tc.existingTags)
					} else {
						require.Zero(t, getsBeforeUpdate, "explicit replacement tags need no preliminary read")
					}
					if expected == nil {
						expected = map[string]*string{}
					}
					delete(expected, strings.ToUpper(helper.timeoutTag))
					expected[helper.timeoutTag] = to.Ptr("9m0s")
					require.Equal(t, expected, sentTags, "PATCH must preserve tag semantics while setting the deadline")
				})
			}
		})
	}
}
