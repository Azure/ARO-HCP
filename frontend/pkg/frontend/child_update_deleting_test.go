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

package frontend

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/go-logr/logr/testr"
	"github.com/stretchr/testify/require"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	azcorearm "github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"

	"github.com/Azure/ARO-HCP/internal/api/coreapi"
	"github.com/Azure/ARO-HCP/internal/api/metadataapi"
	"github.com/Azure/ARO-HCP/internal/apitesting/coreapitesting"
	"github.com/Azure/ARO-HCP/internal/errorutils"
	"github.com/Azure/ARO-HCP/internal/utils"
)

func TestFailedChildDeletionAdmission(t *testing.T) {
	for _, resource := range []string{"nodepool", "externalauth"} {
		for _, method := range []string{http.MethodPut, http.MethodPatch, http.MethodDelete} {
			for _, deleting := range []bool{false, true} {
				t.Run(fmt.Sprintf("%s/%s/deleting=%t", resource, method, deleting), func(t *testing.T) {
					f := NewTestFrontend(t)
					db := f.resourcesDBClient
					ctx := utils.ContextWithLogger(t.Context(), testr.New(t))
					version, ok := f.apiRegistry.Lookup(string(metadataapi.APIVersionV20251223Preview))
					require.True(t, ok)
					ctx = ContextWithVersion(ctx, version)
					ctx = ContextWithCorrelationData(ctx, &coreapi.CorrelationData{})
					ctx = ContextWithSystemData(ctx, &coreapi.SystemData{})
					cluster := coreapitesting.MinimumValidClusterTestCase()
					cluster.SetResourceID(cluster.ID)
					cluster.SetPartitionKey(cluster.ID.SubscriptionID)
					cluster.ServiceProviderProperties.ProvisioningState = coreapi.ProvisioningStateSucceeded
					_, err := db.HCPClusters(cluster.ID.SubscriptionID, cluster.ID.ResourceGroupName).Create(ctx, cluster, nil)
					require.NoError(t, err)
					_, err = db.Subscriptions().Create(ctx, newTestSubscription(cluster.ID.SubscriptionID, coreapi.SubscriptionStateRegistered, nil), nil)
					require.NoError(t, err)

					var deletionTime *metav1.Time
					if deleting {
						deletionTime = &metav1.Time{Time: time.Now().UTC().Add(-2 * time.Hour)}
					}
					rejected := deleting && method != http.MethodDelete
					var resourceID *azcorearm.ResourceID
					var body []byte
					var handler func(http.ResponseWriter, *http.Request) error
					var verifyStored func()
					if resource == "nodepool" {
						resourceID = metadataapi.Must(azcorearm.ParseResourceID(cluster.ID.String() + "/nodePools/test-nodepool"))
						obj := coreapi.NewDefaultNodePool(resourceID, coreapitesting.TestLocation)
						obj.SetResourceID(obj.ID)
						obj.SetPartitionKey(obj.ID.SubscriptionID)
						obj.Properties.Version = coreapi.NodePoolVersionProfile{ID: "4.20.8", ChannelGroup: "stable"}
						obj.Properties.Platform.SubnetID = cluster.CustomerProperties.Platform.SubnetID
						obj.Properties.Platform.VMSize = "Standard_D8s_v3"
						obj.Properties.ProvisioningState = coreapi.ProvisioningStateFailed
						obj.ServiceProviderProperties.DeletionTimestamp = deletionTime
						client := db.HCPClusters(resourceID.SubscriptionID, resourceID.ResourceGroupName).NodePools(resourceID.Parent.Name)
						obj, err = client.Create(ctx, obj, nil)
						require.NoError(t, err)
						informer, _ := f.informers.ServiceProviderClusters()
						require.NoError(t, informer.GetStore().Add(newTestServiceProviderCluster(cluster.ID)))
						informer, _ = f.informers.ServiceProviderNodePools()
						require.NoError(t, informer.GetStore().Add(newTestServiceProviderNodePool(resourceID)))
						informer, _ = f.informers.NodePools()
						require.NoError(t, informer.GetStore().Add(obj.DeepCopy()))
						other := coreapi.NewDefaultNodePool(metadataapi.Must(azcorearm.ParseResourceID(cluster.ID.String()+"/nodePools/other")), coreapitesting.TestLocation)
						other.SetResourceID(other.ID)
						require.NoError(t, informer.GetStore().Add(other))
						body, err = coreapi.MarshalJSON(version.NewNodePool(obj))
						handler = f.CreateOrUpdateNodePool
						if method == http.MethodDelete {
							handler = f.DeleteNodePool
						}
						verifyStored = func() {
							stored, err := client.Get(ctx, resourceID.Name)
							require.NoError(t, err)
							if rejected {
								require.Equal(t, obj, stored, "rejection must not change the resource or its deadlines")
							} else {
								require.NotEmpty(t, stored.ServiceProviderProperties.ActiveOperationID)
							}
						}
					} else {
						obj := coreapitesting.MinimumValidExternalAuthTestCase()
						resourceID = obj.ID
						obj.SetResourceID(obj.ID)
						obj.SetPartitionKey(obj.ID.SubscriptionID)
						obj.Properties.ProvisioningState = coreapi.ProvisioningStateFailed
						obj.ServiceProviderProperties.DeletionTimestamp = deletionTime
						client := db.HCPClusters(resourceID.SubscriptionID, resourceID.ResourceGroupName).ExternalAuth(resourceID.Parent.Name)
						obj, err = client.Create(ctx, obj, nil)
						require.NoError(t, err)
						body, err = coreapi.MarshalJSON(version.NewExternalAuth(obj))
						handler = f.CreateOrUpdateExternalAuth
						if method == http.MethodDelete {
							handler = f.DeleteExternalAuth
						}
						verifyStored = func() {
							stored, err := client.Get(ctx, resourceID.Name)
							require.NoError(t, err)
							if rejected {
								require.Equal(t, obj, stored, "rejection must not change the resource or its deadlines")
							} else {
								require.NotEmpty(t, stored.ServiceProviderProperties.ActiveOperationID)
							}
						}
					}
					require.NoError(t, err)
					ctx = utils.ContextWithResourceID(ctx, resourceID)
					ctx = ContextWithBody(ctx, body)
					request := httptest.NewRequestWithContext(ctx, method, resourceID.String(), nil)
					request.Header.Set("Referer", "https://management.azure.com"+resourceID.String()+"?api-version="+version.String())
					response := httptest.NewRecorder()
					errorutils.ReportError(handler)(response, request)
					if rejected {
						require.Equal(t, http.StatusBadRequest, response.Code, response.Body.String())
						require.Contains(t, response.Body.String(), "is being deleted and cannot be updated")
					} else if method == http.MethodPut {
						require.Equal(t, http.StatusOK, response.Code, response.Body.String())
					} else {
						require.Equal(t, http.StatusAccepted, response.Code, response.Body.String())
					}
					verifyStored()
					operations, err := db.Operations(resourceID.SubscriptionID).List(ctx, nil)
					require.NoError(t, err)
					count := 0
					for range operations.Items(ctx) {
						count++
					}
					require.NoError(t, operations.GetError())
					if rejected {
						require.Zero(t, count, "rejection must not create an operation")
					} else {
						require.Equal(t, 1, count)
					}
				})
			}
		}
	}
}
