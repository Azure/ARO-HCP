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

package clusterpropertiescontroller

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"

	azcorearm "github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"

	arohcpv1alpha1 "github.com/openshift-online/ocm-sdk-go/arohcp/v1alpha1"

	"github.com/Azure/ARO-HCP/backend/pkg/controllers/controllerutils"
	"github.com/Azure/ARO-HCP/backend/pkg/listertesting"
	"github.com/Azure/ARO-HCP/internal/api"
	"github.com/Azure/ARO-HCP/internal/api/arm"
	"github.com/Azure/ARO-HCP/internal/databasetesting"
	"github.com/Azure/ARO-HCP/internal/ocm"
)

func TestDesiredControlPlaneSizeSyncer_SyncOnce(t *testing.T) {
	largeStr := string(api.HostedClusterControlPlaneSizeLarge)
	mediumStr := string(api.HostedClusterControlPlaneSizeMedium)
	// CS stores the size override in lowercase (ocm.DesiredHostedClusterSizeOverride
	// normalizes it), so anything we seed into or expect from CS uses the
	// lowercased form even though the API surface uses the capitalized enum.
	largeCS := strings.ToLower(largeStr)
	mediumCS := strings.ToLower(mediumStr)

	type setup struct {
		specSize                   *string
		statusSize                 *string
		cluster                    *api.HCPOpenShiftCluster
		seedServiceProviderCluster bool
		csProperties               map[string]string
		csGetErr                   error
		csUpdateErr                error
		expectCSGet                bool
		expectCSPush               bool
		// expectedProperties is the full properties map on the captured
		// UpdateCluster builder. Only checked when expectCSPush is true.
		expectedProperties map[string]string
		// expectedStatusSize is what
		// ServiceProviderCluster.Status.DesiredHostedClusterControlPlaneSize
		// must equal after a successful reconcile. Nil pointer means absent.
		expectedStatusSize                *string
		expectServiceProviderClusterWrite bool
		expectErrContains                 string
	}

	tests := []struct {
		name string
		setup
	}{
		{
			name: "no ServiceProviderCluster in cache - skip",
			setup: setup{
				seedServiceProviderCluster: false,
			},
		},
		{
			name: "Spec and Status both nil - skip",
			setup: setup{
				seedServiceProviderCluster: true,
				cluster:                    newTestCluster(testClusterName),
			},
		},
		{
			name: "Spec and Status both equal - skip",
			setup: setup{
				seedServiceProviderCluster: true,
				specSize:                   &largeStr,
				statusSize:                 &largeStr,
				cluster:                    newTestCluster(testClusterName),
			},
		},
		{
			name: "cluster missing in cache - skip",
			setup: setup{
				seedServiceProviderCluster: true,
				specSize:                   &largeStr,
				cluster:                    nil,
			},
		},
		{
			name: "cluster has no ClusterServiceID - skip",
			setup: setup{
				seedServiceProviderCluster: true,
				specSize:                   &largeStr,
				cluster: newTestCluster(testClusterName, func(c *api.HCPOpenShiftCluster) {
					c.ServiceProviderProperties.ClusterServiceID = nil
				}),
			},
		},
		{
			name: "set transition - writes CS and records Status",
			setup: setup{
				seedServiceProviderCluster: true,
				specSize:                   &largeStr,
				statusSize:                 nil,
				cluster:                    newTestCluster(testClusterName),
				csProperties:               nil,
				expectCSGet:                true,
				expectCSPush:               true,
				expectedProperties: map[string]string{
					ocm.CSPropertySizeOverride: largeCS,
				},
				expectedStatusSize:                &largeStr,
				expectServiceProviderClusterWrite: true,
			},
		},
		{
			name: "change transition - overwrites stale CS and preserves other properties",
			setup: setup{
				seedServiceProviderCluster: true,
				specSize:                   &mediumStr,
				statusSize:                 &largeStr,
				cluster:                    newTestCluster(testClusterName),
				csProperties: map[string]string{
					ocm.CSPropertySizeOverride:  largeCS,
					ocm.CSPropertySingleReplica: ocm.CSPropertyEnabled,
				},
				expectCSGet:  true,
				expectCSPush: true,
				expectedProperties: map[string]string{
					ocm.CSPropertySizeOverride:  mediumCS,
					ocm.CSPropertySingleReplica: ocm.CSPropertyEnabled,
				},
				expectedStatusSize:                &mediumStr,
				expectServiceProviderClusterWrite: true,
			},
		},
		{
			name: "unset transition - clears CS property and clears Status",
			setup: setup{
				seedServiceProviderCluster: true,
				specSize:                   nil,
				statusSize:                 &largeStr,
				cluster:                    newTestCluster(testClusterName),
				csProperties: map[string]string{
					ocm.CSPropertySizeOverride:  largeCS,
					ocm.CSPropertySingleReplica: ocm.CSPropertyEnabled,
				},
				expectCSGet:  true,
				expectCSPush: true,
				expectedProperties: map[string]string{
					ocm.CSPropertySingleReplica: ocm.CSPropertyEnabled,
				},
				expectedStatusSize:                nil,
				expectServiceProviderClusterWrite: true,
			},
		},
		{
			name: "no CS write needed but Status drifted - just record Status",
			setup: setup{
				seedServiceProviderCluster: true,
				specSize:                   &largeStr,
				statusSize:                 nil,
				cluster:                    newTestCluster(testClusterName),
				csProperties: map[string]string{
					ocm.CSPropertySizeOverride: largeCS,
				},
				expectCSGet:                       true,
				expectCSPush:                      false,
				expectedStatusSize:                &largeStr,
				expectServiceProviderClusterWrite: true,
			},
		},
		{
			name: "CS GetCluster fails - error",
			setup: setup{
				seedServiceProviderCluster: true,
				specSize:                   &largeStr,
				cluster:                    newTestCluster(testClusterName),
				csGetErr:                   fmt.Errorf("boom"),
				expectCSGet:                true,
				expectErrContains:          "failed to get cluster from Cluster Service",
			},
		},
		{
			name: "CS UpdateCluster fails - error",
			setup: setup{
				seedServiceProviderCluster: true,
				specSize:                   &largeStr,
				cluster:                    newTestCluster(testClusterName),
				csUpdateErr:                fmt.Errorf("boom"),
				expectCSGet:                true,
				expectCSPush:               true,
				expectErrContains:          "failed to update cluster in Cluster Service",
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			ctx := context.Background()
			ctrl := gomock.NewController(t)
			defer ctrl.Finish()

			mockResourcesDBClient := databasetesting.NewMockResourcesDBClient()

			var seededServiceProviderCluster *api.ServiceProviderCluster
			if tc.seedServiceProviderCluster {
				clusterResourceID := api.Must(azcorearm.ParseResourceID(
					"/subscriptions/" + testSubscriptionID +
						"/resourceGroups/" + testResourceGroupName +
						"/providers/Microsoft.RedHatOpenShift/hcpOpenShiftClusters/" + testClusterName,
				))
				serviceProviderClusterCRUD := mockResourcesDBClient.ServiceProviderClusters(
					clusterResourceID.SubscriptionID,
					clusterResourceID.ResourceGroupName,
					clusterResourceID.Name,
				)
				// Use the Created return so the lister seed carries the ETag
				// the mock CRUD assigned; the syncer's Replace requires it.
				created, err := serviceProviderClusterCRUD.Create(ctx, newTestServiceProviderCluster(testClusterName, tc.specSize, tc.statusSize), nil)
				require.NoError(t, err)
				seededServiceProviderCluster = created
			}

			serviceProviderClusterListerStub := &listertesting.SliceServiceProviderClusterLister{}
			if seededServiceProviderCluster != nil {
				serviceProviderClusterListerStub.ServiceProviderClusters = []*api.ServiceProviderCluster{seededServiceProviderCluster}
			}
			clusterLister := &listertesting.SliceClusterLister{}
			if tc.cluster != nil {
				clusterLister.Clusters = []*api.HCPOpenShiftCluster{tc.cluster}
			}

			mockCSClient := ocm.NewMockClusterServiceClientSpec(ctrl)
			if tc.expectCSGet {
				mockCSClient.EXPECT().
					GetCluster(gomock.Any(), api.Must(api.NewInternalID(testClusterServiceIDStr))).
					Return(buildBareCSCluster(t, tc.csProperties), tc.csGetErr)
			}
			var capturedBuilder *arohcpv1alpha1.ClusterBuilder
			if tc.expectCSPush {
				mockCSClient.EXPECT().
					UpdateCluster(gomock.Any(), api.Must(api.NewInternalID(testClusterServiceIDStr)), gomock.Any()).
					DoAndReturn(func(_ context.Context, _ api.InternalID, b *arohcpv1alpha1.ClusterBuilder) (*arohcpv1alpha1.Cluster, error) {
						capturedBuilder = b
						return buildBareCSCluster(t, tc.csProperties), tc.csUpdateErr
					})
			}

			syncer := &desiredControlPlaneSizeSyncer{
				cooldownChecker:              &alwaysSyncCooldownChecker{},
				serviceProviderClusterLister: serviceProviderClusterListerStub,
				clusterLister:                clusterLister,
				resourcesDBClient:            mockResourcesDBClient,
				clusterServiceClient:         mockCSClient,
			}

			key := controllerutils.HCPClusterKey{
				SubscriptionID:    testSubscriptionID,
				ResourceGroupName: testResourceGroupName,
				HCPClusterName:    testClusterName,
			}
			err := syncer.SyncOnce(ctx, key)

			if tc.expectErrContains != "" {
				require.Error(t, err)
				assert.Contains(t, err.Error(), tc.expectErrContains)
				return
			}
			require.NoError(t, err)

			if tc.expectCSPush && tc.expectedProperties != nil {
				require.NotNil(t, capturedBuilder, "expected an UpdateCluster call with a builder")
				built, buildErr := capturedBuilder.Build()
				require.NoError(t, buildErr)
				assert.Equal(t, tc.expectedProperties, built.Properties())
			}

			if !tc.expectServiceProviderClusterWrite {
				return
			}
			liveServiceProviderCluster, err := mockResourcesDBClient.
				ServiceProviderClusters(testSubscriptionID, testResourceGroupName, testClusterName).
				Get(ctx, api.ServiceProviderClusterResourceName)
			require.NoError(t, err)
			if tc.expectedStatusSize == nil {
				assert.Nil(t, liveServiceProviderCluster.Status.DesiredHostedClusterControlPlaneSize)
			} else {
				require.NotNil(t, liveServiceProviderCluster.Status.DesiredHostedClusterControlPlaneSize)
				assert.Equal(t, *tc.expectedStatusSize, *liveServiceProviderCluster.Status.DesiredHostedClusterControlPlaneSize)
			}
		})
	}
}

func newTestServiceProviderCluster(hcpClusterName string, specSize, statusSize *string) *api.ServiceProviderCluster {
	resourceID := api.Must(azcorearm.ParseResourceID(
		"/subscriptions/" + testSubscriptionID +
			"/resourceGroups/" + testResourceGroupName +
			"/providers/Microsoft.RedHatOpenShift/hcpOpenShiftClusters/" + hcpClusterName +
			"/" + api.ServiceProviderClusterResourceTypeName +
			"/" + api.ServiceProviderClusterResourceName,
	))
	return &api.ServiceProviderCluster{
		CosmosMetadata: arm.CosmosMetadata{
			ResourceID:   resourceID,
			PartitionKey: strings.ToLower(resourceID.SubscriptionID),
		},
		Spec: api.ServiceProviderClusterSpec{
			DesiredHostedClusterControlPlaneSize: specSize,
		},
		Status: api.ServiceProviderClusterStatus{
			DesiredHostedClusterControlPlaneSize: statusSize,
		},
	}
}

// buildBareCSCluster constructs a minimal CS cluster with the supplied
// properties bag for use as the "existing" cluster in update-path tests.
func buildBareCSCluster(t *testing.T, properties map[string]string) *arohcpv1alpha1.Cluster {
	t.Helper()
	builder := arohcpv1alpha1.NewCluster()
	if len(properties) > 0 {
		builder = builder.Properties(properties)
	}
	cluster, err := builder.Build()
	require.NoError(t, err)
	return cluster
}
