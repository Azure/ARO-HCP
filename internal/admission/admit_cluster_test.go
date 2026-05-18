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

package admission

import (
	"context"
	"fmt"
	"testing"

	"github.com/blang/semver/v4"
	"github.com/stretchr/testify/assert"

	"k8s.io/apimachinery/pkg/api/operation"
	"k8s.io/utils/ptr"

	azcorearm "github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"

	"github.com/Azure/ARO-HCP/internal/api"
	"github.com/Azure/ARO-HCP/internal/api/arm"
	"github.com/Azure/ARO-HCP/internal/databasetesting"
	"github.com/Azure/ARO-HCP/internal/utils"
)

func TestMutateCluster(t *testing.T) {
	afecRegistered := &arm.Subscription{
		Properties: &arm.SubscriptionProperties{
			RegisteredFeatures: &[]arm.Feature{
				{
					Name:  ptr.To(api.FeatureExperimentalReleaseFeatures),
					State: ptr.To("Registered"),
				},
			},
		},
	}
	noAFEC := &arm.Subscription{
		Properties: &arm.SubscriptionProperties{},
	}

	tests := []struct {
		name                             string
		subscription                     *arm.Subscription
		tags                             map[string]string
		expectErrors                     []utils.ExpectedError
		expectZeroFeatures               bool
		expectedControlPlaneAvailability api.ControlPlaneAvailability
		expectedControlPlanePodSizing    api.ControlPlanePodSizing
	}{
		{
			name:               "nil subscription ignores all tags",
			subscription:       nil,
			tags:               map[string]string{api.TagClusterSingleReplica: string(api.SingleReplicaControlPlane), api.TagClusterSizeOverride: string(api.MinimalControlPlanePodSizing)},
			expectErrors:       []utils.ExpectedError{},
			expectZeroFeatures: true,
		},
		{
			name:               "no AFEC registered ignores all tags",
			subscription:       noAFEC,
			tags:               map[string]string{api.TagClusterSingleReplica: string(api.SingleReplicaControlPlane), api.TagClusterSizeOverride: string(api.MinimalControlPlanePodSizing)},
			expectErrors:       []utils.ExpectedError{},
			expectZeroFeatures: true,
		},
		{
			name:                             "AFEC registered with single-replica tag only",
			subscription:                     afecRegistered,
			tags:                             map[string]string{api.TagClusterSingleReplica: string(api.SingleReplicaControlPlane)},
			expectErrors:                     []utils.ExpectedError{},
			expectedControlPlaneAvailability: api.SingleReplicaControlPlane,
		},
		{
			name:                          "AFEC registered with size-override tag only",
			subscription:                  afecRegistered,
			tags:                          map[string]string{api.TagClusterSizeOverride: string(api.MinimalControlPlanePodSizing)},
			expectErrors:                  []utils.ExpectedError{},
			expectedControlPlanePodSizing: api.MinimalControlPlanePodSizing,
		},
		{
			name:                             "AFEC registered with both tags",
			subscription:                     afecRegistered,
			tags:                             map[string]string{api.TagClusterSingleReplica: string(api.SingleReplicaControlPlane), api.TagClusterSizeOverride: string(api.MinimalControlPlanePodSizing)},
			expectErrors:                     []utils.ExpectedError{},
			expectedControlPlaneAvailability: api.SingleReplicaControlPlane,
			expectedControlPlanePodSizing:    api.MinimalControlPlanePodSizing,
		},
		{
			name:               "AFEC registered but no tags",
			subscription:       afecRegistered,
			tags:               map[string]string{},
			expectErrors:       []utils.ExpectedError{},
			expectZeroFeatures: true,
		},
		{
			name:                          "AFEC registered with case insensitive tag keys - size-override",
			subscription:                  afecRegistered,
			tags:                          map[string]string{"ARO-HCP.Experimental.Cluster.Size-Override": string(api.MinimalControlPlanePodSizing)},
			expectErrors:                  []utils.ExpectedError{},
			expectedControlPlanePodSizing: api.MinimalControlPlanePodSizing,
		},
		{
			name:                             "AFEC registered with case insensitive tag keys - single-replica",
			subscription:                     afecRegistered,
			tags:                             map[string]string{"ARO-HCP.Experimental.Cluster.Single-Replica": string(api.SingleReplicaControlPlane)},
			expectErrors:                     []utils.ExpectedError{},
			expectedControlPlaneAvailability: api.SingleReplicaControlPlane,
		},
		{
			name:               "AFEC registered but tag values are empty strings",
			subscription:       afecRegistered,
			tags:               map[string]string{api.TagClusterSingleReplica: "", api.TagClusterSizeOverride: ""},
			expectErrors:       []utils.ExpectedError{},
			expectZeroFeatures: true,
		},
		{
			name:         "AFEC registered but single-replica tag has invalid value",
			subscription: afecRegistered,
			tags:         map[string]string{api.TagClusterSingleReplica: "yes"},
			expectErrors: []utils.ExpectedError{
				{FieldPath: "tags", Message: "Invalid value"},
			},
		},
		{
			name:         "AFEC registered but single-replica tag rejects true",
			subscription: afecRegistered,
			tags:         map[string]string{api.TagClusterSingleReplica: "true"},
			expectErrors: []utils.ExpectedError{
				{FieldPath: "tags", Message: "Invalid value"},
			},
		},
		{
			name:         "AFEC registered but size-override tag has invalid value",
			subscription: afecRegistered,
			tags:         map[string]string{api.TagClusterSizeOverride: "1"},
			expectErrors: []utils.ExpectedError{
				{FieldPath: "tags", Message: "Invalid value"},
			},
		},
		{
			name:         "AFEC registered with unrecognized experimental tag",
			subscription: afecRegistered,
			tags:         map[string]string{"aro-hcp.experimental.cluster.unknown-feature": string(api.SingleReplicaControlPlane)},
			expectErrors: []utils.ExpectedError{
				{FieldPath: "tags", Message: "unrecognized experimental tag"},
			},
		},
		{
			name:                          "AFEC registered with only size-override after removing single-replica",
			subscription:                  afecRegistered,
			tags:                          map[string]string{api.TagClusterSizeOverride: string(api.MinimalControlPlanePodSizing)},
			expectErrors:                  []utils.ExpectedError{},
			expectedControlPlanePodSizing: api.MinimalControlPlanePodSizing,
		},
		{
			name:         "AFEC registered with unrecognized experimental tag in mixed case",
			subscription: afecRegistered,
			tags:         map[string]string{"ARO-HCP.Experimental.Cluster.Unknown-Feature": string(api.SingleReplicaControlPlane)},
			expectErrors: []utils.ExpectedError{
				{FieldPath: "tags", Message: "unrecognized experimental tag"},
			},
		},
		{
			name:               "non-experimental tags are ignored",
			subscription:       afecRegistered,
			tags:               map[string]string{"environment": "dev", "team": "platform"},
			expectErrors:       []utils.ExpectedError{},
			expectZeroFeatures: true,
		},
		{
			name:         "valid tag alongside unrecognized experimental tag fails",
			subscription: afecRegistered,
			tags:         map[string]string{api.TagClusterSingleReplica: string(api.SingleReplicaControlPlane), "aro-hcp.experimental.cluster.unknown": "value"},
			expectErrors: []utils.ExpectedError{
				{FieldPath: "tags", Message: "unrecognized experimental tag"},
			},
		},
		{
			name:               "nil tags",
			subscription:       afecRegistered,
			tags:               nil,
			expectErrors:       []utils.ExpectedError{},
			expectZeroFeatures: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cluster := &api.HCPOpenShiftCluster{
				TrackedResource: arm.TrackedResource{
					Tags: tt.tags,
				},
			}
			errs := MutateCluster(cluster, tt.subscription)

			utils.VerifyErrorsMatch(t, tt.expectErrors, errs)

			if tt.expectZeroFeatures {
				if cluster.ServiceProviderProperties.ExperimentalFeatures != (api.ExperimentalFeatures{}) {
					t.Errorf("expected zero ExperimentalFeatures, got %+v", cluster.ServiceProviderProperties.ExperimentalFeatures)
				}
				return
			}
			if cluster.ServiceProviderProperties.ExperimentalFeatures.ControlPlaneAvailability != tt.expectedControlPlaneAvailability {
				t.Errorf("expected ControlPlaneAvailability %q, got %q",
					tt.expectedControlPlaneAvailability, cluster.ServiceProviderProperties.ExperimentalFeatures.ControlPlaneAvailability)
			}
			if cluster.ServiceProviderProperties.ExperimentalFeatures.ControlPlanePodSizing != tt.expectedControlPlanePodSizing {
				t.Errorf("expected ControlPlanePodSizing %q, got %q",
					tt.expectedControlPlanePodSizing, cluster.ServiceProviderProperties.ExperimentalFeatures.ControlPlanePodSizing)
			}
		})
	}
}

func TestAdmitClusterOnUpdate(t *testing.T) {
	t.Parallel()

	ctx := context.Background()

	const (
		subscriptionID    = "6b690bec-0c16-4ecb-8f67-781caf40bba7"
		resourceGroupName = "test-rg"
		clusterName       = "test-cluster"
	)

	clusterResourceID := api.Must(azcorearm.ParseResourceID(
		"/subscriptions/" + subscriptionID +
			"/resourceGroups/" + resourceGroupName +
			"/providers/Microsoft.RedHatOpenShift/hcpOpenShiftClusters/" + clusterName))

	serviceProviderResourceID := api.Must(azcorearm.ParseResourceID(
		clusterResourceID.String() + "/serviceProviderClusters/default"))

	serviceProviderClusterStatusWithActiveControlPlaneVersion := func(fullVersion string) api.ServiceProviderClusterStatus {
		return api.ServiceProviderClusterStatus{
			ControlPlaneVersion: api.ServiceProviderClusterStatusVersion{
				ActiveVersions: []api.HCPClusterActiveVersion{{Version: ptr.To(api.Must(semver.ParseTolerant(fullVersion)))}},
			},
		}
	}

	serviceProviderClusterStatusWithActiveControlPlaneVersions := func(fullVersions ...string) api.ServiceProviderClusterStatus {
		active := make([]api.HCPClusterActiveVersion, 0, len(fullVersions))
		for _, v := range fullVersions {
			active = append(active, api.HCPClusterActiveVersion{Version: ptr.To(api.Must(semver.ParseTolerant(v)))})
		}
		return api.ServiceProviderClusterStatus{
			ControlPlaneVersion: api.ServiceProviderClusterStatusVersion{ActiveVersions: active},
		}
	}

	makeTestNodePool := func(name, versionID string) *api.HCPOpenShiftClusterNodePool {
		nodePoolResourceID := api.Must(azcorearm.ParseResourceID(
			clusterResourceID.String() + "/nodePools/" + name))
		return &api.HCPOpenShiftClusterNodePool{
			TrackedResource: arm.NewTrackedResource(nodePoolResourceID, "eastus"),
			Properties: api.HCPOpenShiftClusterNodePoolProperties{
				Version: api.NodePoolVersionProfile{ID: versionID},
			},
		}
	}

	makeServiceProviderNodePool := func(nodePoolName string, activeFullVersions ...string) *api.ServiceProviderNodePool {
		npResourceID := api.Must(azcorearm.ParseResourceID(clusterResourceID.String() + "/nodePools/" + nodePoolName))
		spResourceID := api.Must(azcorearm.ParseResourceID(fmt.Sprintf("%s/%s/%s",
			npResourceID.String(), api.ServiceProviderNodePoolResourceTypeName, api.ServiceProviderNodePoolResourceName)))
		active := make([]api.HCPNodePoolActiveVersion, 0, len(activeFullVersions))
		for _, v := range activeFullVersions {
			active = append(active, api.HCPNodePoolActiveVersion{Version: ptr.To(api.Must(semver.ParseTolerant(v)))})
		}
		return &api.ServiceProviderNodePool{
			CosmosMetadata: api.CosmosMetadata{ResourceID: spResourceID},
			Status: api.ServiceProviderNodePoolStatus{
				NodePoolVersion: api.ServiceProviderNodePoolStatusVersion{ActiveVersions: active},
			},
		}
	}

	tests := []struct {
		name                         string
		oldClusterVersionID          string
		clusterVersionID             string
		serviceProviderClusterStatus api.ServiceProviderClusterStatus
		nodePools                    []*api.HCPOpenShiftClusterNodePool
		serviceProviderNodePools     []*api.ServiceProviderNodePool
		expectErrors                 []utils.ExpectedError
	}{
		{
			name:                         "empty desired version skips admission",
			oldClusterVersionID:          "4.10",
			clusterVersionID:             "",
			serviceProviderClusterStatus: serviceProviderClusterStatusWithActiveControlPlaneVersion("4.22"),
			nodePools:                    []*api.HCPOpenShiftClusterNodePool{makeTestNodePool("np1", "4.10.0")},
			expectErrors:                 []utils.ExpectedError{},
		},
		{
			name:                         "unchanged version skips admission",
			oldClusterVersionID:          "5.0",
			clusterVersionID:             "5.0",
			serviceProviderClusterStatus: serviceProviderClusterStatusWithActiveControlPlaneVersion("4.22"),
			nodePools:                    []*api.HCPOpenShiftClusterNodePool{makeTestNodePool("workers", "4.20.0")},
			expectErrors:                 []utils.ExpectedError{},
		},
		{
			name:                         "unparsable old version id",
			oldClusterVersionID:          "4.x",
			clusterVersionID:             "4.22",
			serviceProviderClusterStatus: serviceProviderClusterStatusWithActiveControlPlaneVersion("4.22"),
			nodePools:                    nil,
			expectErrors: []utils.ExpectedError{
				{FieldPath: "properties.version.id", Message: "Invalid character(s) found in minor number"},
			},
		},
		{
			name:                         "skips skew vs lowest when old minor matches lowest active cluster version",
			oldClusterVersionID:          "4.21",
			clusterVersionID:             "4.23",
			serviceProviderClusterStatus: serviceProviderClusterStatusWithActiveControlPlaneVersion("4.21"),
			nodePools:                    nil,
			expectErrors:                 []utils.ExpectedError{},
		},
		{
			name:                         "allows 4.22 to 5.0 with active cluster version 4.22",
			oldClusterVersionID:          "4.22",
			clusterVersionID:             "5.0",
			serviceProviderClusterStatus: serviceProviderClusterStatusWithActiveControlPlaneVersion("4.22"),
			nodePools:                    nil,
			expectErrors:                 []utils.ExpectedError{},
		},
		{
			name:                         "rejects 5.1 when old minor below lowest active cluster version",
			oldClusterVersionID:          "4.21",
			clusterVersionID:             "5.1",
			serviceProviderClusterStatus: serviceProviderClusterStatusWithActiveControlPlaneVersion("4.22"),
			nodePools:                    nil,
			expectErrors: []utils.ExpectedError{
				{FieldPath: "properties.version.id", Message: "invalid upgrade path"},
			},
		},
		{
			name:                         "rejects 4.24 when old minor below lowest active cluster version",
			oldClusterVersionID:          "4.21",
			clusterVersionID:             "4.24",
			serviceProviderClusterStatus: serviceProviderClusterStatusWithActiveControlPlaneVersion("4.22"),
			nodePools:                    nil,
			expectErrors: []utils.ExpectedError{
				{FieldPath: "properties.version.id", Message: "only upgrade to the next minor is allowed"},
			},
		},
		{
			name:                         "rejects version below highest active cluster version",
			oldClusterVersionID:          "4.22",
			clusterVersionID:             "4.21",
			serviceProviderClusterStatus: serviceProviderClusterStatusWithActiveControlPlaneVersion("4.22"),
			nodePools:                    nil,
			expectErrors: []utils.ExpectedError{
				{FieldPath: "properties.version.id", Message: "must be at least"},
			},
		},
		{
			name:                         "allows upgrade across adjacent active cluster minors",
			oldClusterVersionID:          "4.21",
			clusterVersionID:             "4.22",
			serviceProviderClusterStatus: serviceProviderClusterStatusWithActiveControlPlaneVersions("4.22", "4.21"),
			nodePools:                    nil,
			expectErrors:                 []utils.ExpectedError{},
		},
		{
			name:                         "rejects skip minor vs lowest when fleet spans minors",
			oldClusterVersionID:          "4.21",
			clusterVersionID:             "4.22",
			serviceProviderClusterStatus: serviceProviderClusterStatusWithActiveControlPlaneVersions("4.20", "4.22"),
			nodePools:                    nil,
			expectErrors: []utils.ExpectedError{
				{FieldPath: "properties.version.id", Message: "only upgrade to the next minor is allowed"},
			},
		},
		{
			name:                         "rejects when node pool over two minors behind",
			oldClusterVersionID:          "4.20",
			clusterVersionID:             "4.21",
			serviceProviderClusterStatus: serviceProviderClusterStatusWithActiveControlPlaneVersion("4.20"),
			nodePools:                    []*api.HCPOpenShiftClusterNodePool{makeTestNodePool("workers", "4.17.0")},
			expectErrors: []utils.ExpectedError{
				{FieldPath: "properties.version.id", Message: "must not be more than two minor versions ahead"},
			},
		},
		{
			name:                         "allows no-op version with node pools in skew",
			oldClusterVersionID:          "4.20",
			clusterVersionID:             "4.20",
			serviceProviderClusterStatus: serviceProviderClusterStatusWithActiveControlPlaneVersion("4.20"),
			nodePools: []*api.HCPOpenShiftClusterNodePool{
				makeTestNodePool("workers", "4.18.0"),
				makeTestNodePool("infra", "4.20.3"),
				makeTestNodePool("spot", "4.20.1"),
			},
			expectErrors: []utils.ExpectedError{},
		},
		{
			name:                         "allows 4.22 to 5.0 node pool 4.22",
			oldClusterVersionID:          "4.22",
			clusterVersionID:             "5.0",
			serviceProviderClusterStatus: serviceProviderClusterStatusWithActiveControlPlaneVersion("4.22"),
			nodePools:                    []*api.HCPOpenShiftClusterNodePool{makeTestNodePool("workers", "4.22.0")},
			expectErrors:                 []utils.ExpectedError{},
		},
		{
			name:                         "allows 4.22 to 5.0 node pool 4.21",
			oldClusterVersionID:          "4.22",
			clusterVersionID:             "5.0",
			serviceProviderClusterStatus: serviceProviderClusterStatusWithActiveControlPlaneVersion("4.22"),
			nodePools:                    []*api.HCPOpenShiftClusterNodePool{makeTestNodePool("workers", "4.21.0")},
			expectErrors:                 []utils.ExpectedError{},
		},
		{
			name:                         "allows 4.23 to 5.1 node pool 4.22",
			oldClusterVersionID:          "4.23",
			clusterVersionID:             "5.1",
			serviceProviderClusterStatus: serviceProviderClusterStatusWithActiveControlPlaneVersion("4.23"),
			nodePools:                    []*api.HCPOpenShiftClusterNodePool{makeTestNodePool("workers", "4.22.0")},
			expectErrors:                 []utils.ExpectedError{},
		},
		{
			name:                         "allows 4.23 to 5.1 node pool 4.23",
			oldClusterVersionID:          "4.23",
			clusterVersionID:             "5.1",
			serviceProviderClusterStatus: serviceProviderClusterStatusWithActiveControlPlaneVersion("4.23"),
			nodePools:                    []*api.HCPOpenShiftClusterNodePool{makeTestNodePool("workers", "4.23.0")},
			expectErrors:                 []utils.ExpectedError{},
		},
		{
			name:                         "allows 5.1 to 5.2 node pool 4.23",
			oldClusterVersionID:          "5.1",
			clusterVersionID:             "5.2",
			serviceProviderClusterStatus: serviceProviderClusterStatusWithActiveControlPlaneVersion("5.1"),
			nodePools:                    []*api.HCPOpenShiftClusterNodePool{makeTestNodePool("workers", "4.23.0")},
			expectErrors:                 []utils.ExpectedError{},
		},
		{
			name:                         "rejects 4.22 to 5.0 node pool 4.20",
			oldClusterVersionID:          "4.22",
			clusterVersionID:             "5.0",
			serviceProviderClusterStatus: serviceProviderClusterStatusWithActiveControlPlaneVersion("4.22"),
			nodePools:                    []*api.HCPOpenShiftClusterNodePool{makeTestNodePool("workers", "4.20.0")},
			expectErrors: []utils.ExpectedError{
				{FieldPath: "properties.version.id", Message: "incompatible with node pool"},
			},
		},
		{
			name:                         "rejects 4.23 to 5.1 node pool 4.21",
			oldClusterVersionID:          "4.23",
			clusterVersionID:             "5.1",
			serviceProviderClusterStatus: serviceProviderClusterStatusWithActiveControlPlaneVersion("4.23"),
			nodePools:                    []*api.HCPOpenShiftClusterNodePool{makeTestNodePool("workers", "4.21.0")},
			expectErrors: []utils.ExpectedError{
				{FieldPath: "properties.version.id", Message: "incompatible with node pool"},
			},
		},
		{
			name:                         "rejects 4.22 to 5.0 node pool 4.23",
			oldClusterVersionID:          "4.22",
			clusterVersionID:             "5.0",
			serviceProviderClusterStatus: serviceProviderClusterStatusWithActiveControlPlaneVersion("4.22"),
			nodePools:                    []*api.HCPOpenShiftClusterNodePool{makeTestNodePool("workers", "4.23.0")},
			expectErrors: []utils.ExpectedError{
				{FieldPath: "properties.version.id", Message: "incompatible with node pool"},
			},
		},
		{
			name:                         "rejects 4.22 to 5.0 mixed node pool minors",
			oldClusterVersionID:          "4.22",
			clusterVersionID:             "5.0",
			serviceProviderClusterStatus: serviceProviderClusterStatusWithActiveControlPlaneVersion("4.22"),
			nodePools: []*api.HCPOpenShiftClusterNodePool{
				makeTestNodePool("workers", "4.22.0"),
				makeTestNodePool("legacy", "4.20.0"),
			},
			expectErrors: []utils.ExpectedError{
				{FieldPath: "properties.version.id", Message: "incompatible with node pool"},
			},
		},
		{
			name:                         "rejects 4.22 to 5.0 sp node pool behind customer minor",
			oldClusterVersionID:          "4.22",
			clusterVersionID:             "5.0",
			serviceProviderClusterStatus: serviceProviderClusterStatusWithActiveControlPlaneVersion("4.22"),
			nodePools:                    []*api.HCPOpenShiftClusterNodePool{makeTestNodePool("workers", "4.22.0")},
			serviceProviderNodePools:     []*api.ServiceProviderNodePool{makeServiceProviderNodePool("workers", "4.17.0")},
			expectErrors: []utils.ExpectedError{
				{FieldPath: "properties.version.id", Message: "incompatible with node pool"},
			},
		},
		{
			name:                         "rejects minor upgrade sp node pool two minors behind",
			oldClusterVersionID:          "4.20",
			clusterVersionID:             "4.21",
			serviceProviderClusterStatus: serviceProviderClusterStatusWithActiveControlPlaneVersion("4.20"),
			nodePools:                    []*api.HCPOpenShiftClusterNodePool{makeTestNodePool("workers", "4.20.0")},
			serviceProviderNodePools:     []*api.ServiceProviderNodePool{makeServiceProviderNodePool("workers", "4.17.0")},
			expectErrors: []utils.ExpectedError{
				{FieldPath: "properties.version.id", Message: "must not be more than two minor versions ahead"},
			},
		},
		{
			name:                         "rejects 4.22 to 5.0 incompatible lowest active cluster version",
			oldClusterVersionID:          "4.22",
			clusterVersionID:             "5.0",
			serviceProviderClusterStatus: serviceProviderClusterStatusWithActiveControlPlaneVersion("4.22"),
			nodePools:                    []*api.HCPOpenShiftClusterNodePool{makeTestNodePool("workers", "4.22.0")},
			serviceProviderNodePools:     []*api.ServiceProviderNodePool{makeServiceProviderNodePool("workers", "4.22.0", "4.17.0")},
			expectErrors: []utils.ExpectedError{
				{FieldPath: "properties.version.id", Message: "incompatible with node pool"},
			},
		},
		{
			name:                         "allows 4.22 to 5.0 compatible active cluster versions",
			oldClusterVersionID:          "4.22",
			clusterVersionID:             "5.0",
			serviceProviderClusterStatus: serviceProviderClusterStatusWithActiveControlPlaneVersion("4.22"),
			nodePools:                    []*api.HCPOpenShiftClusterNodePool{makeTestNodePool("workers", "4.22.0")},
			serviceProviderNodePools:     []*api.ServiceProviderNodePool{makeServiceProviderNodePool("workers", "4.22.1", "4.22.0")},
			expectErrors:                 []utils.ExpectedError{},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			serviceProviderCluster := &api.ServiceProviderCluster{
				CosmosMetadata: api.CosmosMetadata{ResourceID: serviceProviderResourceID},
				Status:         tt.serviceProviderClusterStatus,
			}

			resources := []any{serviceProviderCluster}
			for _, nodePool := range tt.nodePools {
				resources = append(resources, nodePool)
			}
			for _, serviceProviderNodePool := range tt.serviceProviderNodePools {
				resources = append(resources, serviceProviderNodePool)
			}
			mockResourcesDBClient, err := databasetesting.NewMockResourcesDBClientWithResources(ctx, resources)
			assert.NoError(t, err)

			oldCluster := &api.HCPOpenShiftCluster{
				TrackedResource: arm.NewTrackedResource(clusterResourceID, "eastus"),
				CustomerProperties: api.HCPOpenShiftClusterCustomerProperties{
					Version: api.VersionProfile{ID: tt.oldClusterVersionID},
				},
			}
			newCluster := &api.HCPOpenShiftCluster{
				TrackedResource: oldCluster.TrackedResource,
				CustomerProperties: api.HCPOpenShiftClusterCustomerProperties{
					Version: api.VersionProfile{ID: tt.clusterVersionID},
				},
			}

			errs := AdmitClusterOnUpdate(ctx, operation.Operation{Type: operation.Update}, mockResourcesDBClient, oldCluster, newCluster)

			utils.VerifyErrorsMatch(t, tt.expectErrors, errs)
		})
	}
}
