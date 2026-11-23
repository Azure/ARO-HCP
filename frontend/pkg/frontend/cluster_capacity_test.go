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
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/require"

	"k8s.io/utils/ptr"

	azcorearm "github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"

	"github.com/Azure/ARO-HCP/arohcp-cosmos-client/api/coreapi"
	"github.com/Azure/ARO-HCP/arohcp-cosmos-client/api/metadataapi"
	"github.com/Azure/ARO-HCP/arohcp-cosmos-client/storage/cosmosstorageutils"
	"github.com/Azure/ARO-HCP/internal/apitesting/coreapitesting"
	"github.com/Azure/ARO-HCP/internal/database/listers/corelisters"
	"github.com/Azure/ARO-HCP/internal/errorutils"
	"github.com/Azure/ARO-HCP/internal/utils"
)

func TestClusterCreateMinimalCapacity(t *testing.T) {
	limit := 20
	belowLimit := limit - 1
	atLimit := limit
	aboveLimit := limit + 1

	for _, tt := range []struct {
		name                string
		existingClusters    int
		clusterTags         map[string]string
		clusterExists       bool
		wantPreflightStatus coreapi.DeploymentPreflightStatus
		wantStatusCode      int
		wantErrorCode       string
		wantPersisted       bool
	}{
		{
			name:                "below the limit is created",
			existingClusters:    belowLimit,
			clusterTags:         map[string]string{metadataapi.TagClusterSizeOverride: string(coreapi.MinimalControlPlanePodSizing)},
			wantPreflightStatus: coreapi.DeploymentPreflightStatusSucceeded,
			wantStatusCode:      http.StatusCreated,
			wantErrorCode:       "",
			wantPersisted:       true,
		},
		{
			name:                "at the limit is rejected",
			existingClusters:    atLimit,
			clusterTags:         map[string]string{metadataapi.TagClusterSizeOverride: string(coreapi.MinimalControlPlanePodSizing)},
			wantPreflightStatus: coreapi.DeploymentPreflightStatusSucceeded,
			wantStatusCode:      http.StatusBadRequest,
			wantErrorCode:       coreapi.CloudErrorCodeCapacityExceeded,
			wantPersisted:       false,
		},
		{
			name:                "above the limit is rejected",
			existingClusters:    aboveLimit,
			clusterTags:         map[string]string{metadataapi.TagClusterSizeOverride: string(coreapi.MinimalControlPlanePodSizing)},
			wantPreflightStatus: coreapi.DeploymentPreflightStatusSucceeded,
			wantStatusCode:      http.StatusBadRequest,
			wantErrorCode:       coreapi.CloudErrorCodeCapacityExceeded,
			wantPersisted:       false,
		},
		{
			name:             "invalid request at the limit reports the validation error",
			existingClusters: atLimit,
			clusterTags: map[string]string{
				metadataapi.TagClusterSizeOverride:  string(coreapi.MinimalControlPlanePodSizing),
				metadataapi.TagClusterSingleReplica: "invalid",
			},
			wantPreflightStatus: coreapi.DeploymentPreflightStatusFailed,
			wantStatusCode:      http.StatusBadRequest,
			wantErrorCode:       coreapi.CloudErrorCodeInvalidRequestContent,
			wantPersisted:       false,
		},
		{
			name:                "existing cluster at the limit is updated",
			existingClusters:    atLimit,
			clusterTags:         map[string]string{metadataapi.TagClusterSizeOverride: string(coreapi.MinimalControlPlanePodSizing)},
			clusterExists:       true,
			wantPreflightStatus: coreapi.DeploymentPreflightStatusSucceeded,
			wantStatusCode:      http.StatusOK,
			wantErrorCode:       "",
			wantPersisted:       true,
		},
	} {
		t.Run(tt.name, func(t *testing.T) {
			f := NewTestFrontend(t)
			db := f.resourcesDBClient
			f.resourcesDBClient = noInventoryListsDB{db}
			cluster := coreapitesting.MinimumValidClusterTestCase()
			cluster.Tags = tt.clusterTags
			// Only the cache contains these documents, all in a different
			// subscription. The regional limit must not use live DB inventory.
			var inventory []*coreapi.Cluster
			for i := range tt.existingClusters {
				stored := coreapitesting.MinimumValidClusterTestCase()
				stored.ID = metadataapi.Must(azcorearm.ParseResourceID(fmt.Sprintf(
					"/subscriptions/11111111-2222-4333-8444-555555555555/resourceGroups/other/providers/Microsoft.RedHatOpenShift/hcpOpenShiftClusters/cluster-%d", i)))
				stored.SetResourceID(stored.ID)
				stored.SetPartitionKey(stored.ID.SubscriptionID)
				stored.ServiceProviderProperties.ExperimentalFeatures.ControlPlanePodSizing = coreapi.MinimalControlPlanePodSizing
				inventory = append(inventory, stored)
			}
			f.clusterLister = corelisters.NewClusterLister(newTestClusterInformer(t, inventory...).GetIndexer())
			f.maxE2EMinimalSwiftClusters = limit
			subscription := newTestSubscription(cluster.ID.SubscriptionID, coreapi.SubscriptionStateRegistered, &coreapi.SubscriptionProperties{
				RegisteredFeatures: &[]coreapi.Feature{{Name: ptr.To(metadataapi.FeatureExperimentalReleaseFeatures), State: ptr.To("Registered")}},
			})
			if tt.clusterExists {
				stored := cluster.DeepCopy()
				stored.SetResourceID(stored.ID)
				stored.SetPartitionKey(stored.ID.SubscriptionID)
				stored.ServiceProviderProperties.ExperimentalFeatures.ControlPlanePodSizing = coreapi.MinimalControlPlanePodSizing
				stored.ServiceProviderProperties.ClusterServiceID = stored.ServiceProviderProperties.PendingClusterServiceID
				stored.ServiceProviderProperties.ProvisioningState = coreapi.ProvisioningStateSucceeded
				_, err := db.HCPClusters(stored.ID.SubscriptionID, stored.ID.ResourceGroupName).Create(t.Context(), stored, nil)
				require.NoError(t, err)
				serviceProviderClusters, _ := f.informers.ServiceProviderClusters()
				require.NoError(t, serviceProviderClusters.GetStore().Add(newTestServiceProviderCluster(stored.ID)))
				_, err = db.Subscriptions().Create(t.Context(), subscription, nil)
				require.NoError(t, err)
			}
			version, ok := f.apiRegistry.Lookup(string(metadataapi.APIVersionV20251223Preview))
			require.True(t, ok)
			body, err := coreapi.MarshalJSON(version.NewCluster(cluster))
			require.NoError(t, err)
			ctx := ContextWithVersion(t.Context(), version)
			ctx = ContextWithSubscription(ctx, subscription)
			ctx = ContextWithCorrelationData(ctx, &coreapi.CorrelationData{})
			ctx = ContextWithSystemData(ctx, cluster.SystemData)
			ctx = ContextWithBody(ctx, body)
			ctx = utils.ContextWithResourceID(ctx, cluster.ID)

			// Preflight validates the request only; capacity is checked on create.
			var resource map[string]any
			require.NoError(t, json.Unmarshal(body, &resource))
			resource["apiVersion"] = version.String()
			resource["name"] = cluster.ID.Name
			resource["type"] = coreapi.ClusterResourceType.String()
			preflightBody, err := json.Marshal(map[string]any{"resources": []any{resource}})
			require.NoError(t, err)
			preflightRequest := httptest.NewRequest(http.MethodPost, "/preflight", nil).WithContext(ContextWithBody(ctx, preflightBody))
			preflightRequest.SetPathValue(PathSegmentSubscriptionID, cluster.ID.SubscriptionID)
			preflightRequest.SetPathValue(PathSegmentResourceGroupName, cluster.ID.ResourceGroupName)
			preflightRecorder := httptest.NewRecorder()
			errorutils.ReportError(f.ArmDeploymentPreflight)(preflightRecorder, preflightRequest)
			require.Equal(t, http.StatusOK, preflightRecorder.Code, preflightRecorder.Body.String())
			var preflightResponse coreapi.DeploymentPreflightResponse
			require.NoError(t, json.Unmarshal(preflightRecorder.Body.Bytes(), &preflightResponse))
			require.Equal(t, tt.wantPreflightStatus, preflightResponse.Status)

			request := httptest.NewRequest(http.MethodPut, cluster.ID.String(), nil).WithContext(ctx)
			request.Header.Set(coreapi.HeaderNameIdentityURL, coreapitesting.TestManagedIdentitiesDataPlaneIdentityURL)
			recorder := httptest.NewRecorder()
			errorutils.ReportError(f.CreateOrUpdateHCPCluster)(recorder, request)
			require.Equal(t, tt.wantStatusCode, recorder.Code, recorder.Body.String())
			if len(tt.wantErrorCode) > 0 {
				var response coreapi.CloudError
				require.NoError(t, json.Unmarshal(recorder.Body.Bytes(), &response))
				require.NotNil(t, response.CloudErrorBody)
				require.Equal(t, tt.wantErrorCode, response.Code)
			}

			created, err := db.HCPClusters(cluster.ID.SubscriptionID, cluster.ID.ResourceGroupName).Get(t.Context(), cluster.ID.Name)
			if !tt.wantPersisted {
				require.True(t, cosmosstorageutils.IsNotFoundError(err), "rejected create must not persist a cluster: %v", err)
				return
			}
			require.NoError(t, err, "admitted create must persist the cluster")
			require.Equal(t, coreapi.MinimalControlPlanePodSizing, created.ServiceProviderProperties.ExperimentalFeatures.ControlPlanePodSizing)
		})
	}
}
