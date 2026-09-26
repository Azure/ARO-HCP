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

package cosmosstorageutils

import (
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"k8s.io/component-base/metrics/legacyregistry"
	"k8s.io/utils/ptr"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	azcorearm "github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"
	"github.com/Azure/azure-sdk-for-go/sdk/data/azcosmos"

	"github.com/Azure/ARO-HCP/internal/api/coreapi"
	"github.com/Azure/ARO-HCP/internal/database/cosmosstorage/cosmosmetrics"
	"github.com/Azure/ARO-HCP/internal/utils"
)

type queryTelemetryTransport func(*http.Request) (*http.Response, error)

func (f queryTelemetryTransport) Do(req *http.Request) (*http.Response, error) {
	return f(req)
}

func TestListQueryTelemetry(t *testing.T) {
	prefix, err := azcorearm.ParseResourceID("/subscriptions/test-subscription")
	require.NoError(t, err)
	for _, tc := range []struct {
		shape        string
		prefix       *azcorearm.ResourceID
		resourceType *azcorearm.ResourceType
		nonRecursive bool
		global       bool
	}{
		{shape: "type_live", resourceType: &coreapi.ClusterResourceType},
		{shape: "recursive_prefix_live", prefix: prefix},
		{shape: "typed_prefix_live", prefix: prefix, resourceType: &coreapi.ClusterResourceType},
		{shape: "children_prefix_live", prefix: prefix, nonRecursive: true},
		{shape: "global_type_live", global: true},
	} {
		for _, partition := range []string{"", "test-partition"} {
			for _, singlePage := range []bool{false, true} {
				scope := "cross_partition"
				if partition != "" {
					scope = "single_partition"
				}
				mode := "all_pages"
				if singlePage {
					mode = "single_page"
				}
				t.Run(tc.shape+"/"+scope+"/"+mode, func(t *testing.T) {
					requests := 0
					key, err := azcosmos.NewKeyCredential("a2V5")
					require.NoError(t, err)
					client, err := azcosmos.NewClientWithKey("https://cosmos.test", key, &azcosmos.ClientOptions{
						ClientOptions: azcore.ClientOptions{Transport: queryTelemetryTransport(func(req *http.Request) (*http.Response, error) {
							body := `{}`
							if req.Method == http.MethodPost {
								requests++
								body = `{"Documents":[],"_count":0}`
								require.Equal(t, "list_query_telemetry", cosmosmetrics.CallSiteFromContext(req.Context()))
								if partition == "" {
									require.Empty(t, req.Header.Get("x-ms-documentdb-partitionkey"))
									require.Equal(t, "true", req.Header.Get("x-ms-documentdb-query-enablecrosspartition"))
								} else {
									require.JSONEq(t, `["test-partition"]`, req.Header.Get("x-ms-documentdb-partitionkey"))
								}
							}
							return &http.Response{StatusCode: http.StatusOK, Header: http.Header{"Content-Type": {"application/json"}}, Body: io.NopCloser(strings.NewReader(body)), Request: req}, nil
						})},
					})
					require.NoError(t, err)
					container, err := client.NewContainer("db", "Resources")
					require.NoError(t, err)
					ctx := cosmosmetrics.ContextWithCallSite(utils.ContextWithControllerName(t.Context(), t.Name()), "list_query_telemetry")
					var options *DBClientListResourceDocsOptions
					if singlePage {
						options = &DBClientListResourceDocsOptions{PageSizeHint: ptr.To(int32(1))}
					}
					// Use deltas so repeated test runs do not need to reset shared collectors.
					executions := func() float64 {
						families, err := legacyregistry.DefaultGatherer.Gather()
						require.NoError(t, err)
						for _, family := range families {
							if family.GetName() != "cosmos_query_executions_total" {
								continue
							}
							for _, metric := range family.Metric {
								labels := map[string]string{}
								for _, label := range metric.Label {
									labels[label.GetName()] = label.GetValue()
								}
								if labels["source"] == t.Name() {
									require.Equal(t, map[string]string{
										"source_kind": "controller", "source": t.Name(), "cosmosdb_container": "Resources",
										"call_site": "list_query_telemetry", "query_shape": tc.shape, "query_scope": scope,
									}, labels)
									return metric.Counter.GetValue()
								}
							}
						}
						return 0
					}
					before := executions()
					var iterator DBClientIterator[coreapi.HCPOpenShiftCluster]
					if tc.global {
						lister := &CosmosGlobalLister[coreapi.HCPOpenShiftCluster, GenericDocument[coreapi.HCPOpenShiftCluster]]{
							ContainerClient: container, PartitionKey: partition, ResourceTypes: []azcorearm.ResourceType{coreapi.ClusterResourceType},
						}
						iterator, err = lister.List(ctx, options)
					} else {
						iterator, err = list[coreapi.HCPOpenShiftCluster, GenericDocument[coreapi.HCPOpenShiftCluster]](ctx, container, partition, tc.resourceType, tc.prefix, options, tc.nonRecursive)
					}
					require.NoError(t, err)
					require.Equal(t, before, executions(), "constructing an iterator must not execute a query")
					for range iterator.Items(ctx) {
						t.Fatal("expected an empty result")
					}
					require.NoError(t, iterator.GetError())
					require.Equal(t, 1, requests)
					require.Equal(t, before+1, executions())
				})
			}
		}
	}
}
