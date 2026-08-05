// Copyright 2026 Microsoft Corporation
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//	http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package version

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/blang/semver/v4"
	"github.com/go-logr/logr"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"

	apimeta "k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	kruntime "k8s.io/apimachinery/pkg/runtime"
	clocktesting "k8s.io/utils/clock/testing"
	"k8s.io/utils/ptr"

	azcorearm "github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"

	configv1 "github.com/openshift/api/config/v1"
	hypershiftv1beta1 "github.com/openshift/hypershift/api/hypershift/v1beta1"

	"github.com/Azure/ARO-HCP/backend/pkg/controllers/controlplaneversion"
	"github.com/Azure/ARO-HCP/backend/pkg/utils/controllerutils"
	"github.com/Azure/ARO-HCP/internal/api"
	"github.com/Azure/ARO-HCP/internal/api/arm"
	"github.com/Azure/ARO-HCP/internal/api/kubeapplier"
	"github.com/Azure/ARO-HCP/internal/database/cosmosstorage/cosmosstorageutils"
	"github.com/Azure/ARO-HCP/internal/database/cosmosstoragetesting/corecosmosstoragetesting"
	"github.com/Azure/ARO-HCP/internal/database/listers/corelisters"
	"github.com/Azure/ARO-HCP/internal/database/listertesting/corelistertesting"
	"github.com/Azure/ARO-HCP/internal/database/listertesting/kubeapplierlistertesting"
	"github.com/Azure/ARO-HCP/internal/ocm"
	"github.com/Azure/ARO-HCP/internal/utils"
)

// testGraphNode is the JSON structure for a node in the Cincinnati graph response.
type testGraphNode struct {
	Version  string            `json:"version"`
	Payload  string            `json:"payload"`
	Metadata map[string]string `json:"metadata,omitempty"`
}

// testGraph is the JSON structure for a Cincinnati graph response.
type testGraph struct {
	Nodes []testGraphNode `json:"nodes"`
}

// testRelease describes a version with its channel membership for test fixtures.
type testRelease struct {
	version  string
	channels string
}

// testRoundTripperFromReleases builds a RoundTrip function that returns the
// given releases as a Cincinnati graph for any request.
func testRoundTripperFromReleases(releases []testRelease) controlplaneversion.RoundTrip {
	nodes := make([]testGraphNode, 0, len(releases))
	for _, r := range releases {
		n := testGraphNode{
			Version: r.version,
			Payload: "quay.io/openshift-release-dev/ocp-release:" + r.version + "-multi",
		}
		if r.channels != "" {
			n.Metadata = map[string]string{
				"io.openshift.upgrades.graph.release.channels": r.channels,
			}
		}
		nodes = append(nodes, n)
	}
	body, _ := json.Marshal(testGraph{Nodes: nodes})
	return func(_ *http.Request) (*http.Response, error) {
		return &http.Response{
			StatusCode: http.StatusOK,
			Body:       io.NopCloser(strings.NewReader(string(body))),
		}, nil
	}
}

// testHostedCluster builds a HostedCluster with the given channel and
// available updates for use in update-path tests.
func testHostedCluster(channel string, updates []configv1.Release) *hypershiftv1beta1.HostedCluster {
	return &hypershiftv1beta1.HostedCluster{
		Spec: hypershiftv1beta1.HostedClusterSpec{
			Channel: channel,
		},
		Status: hypershiftv1beta1.HostedClusterStatus{
			Version: &hypershiftv1beta1.ClusterVersionStatus{
				Desired:          configv1.Release{Version: "0.0.0"},
				AvailableUpdates: updates,
			},
		},
	}
}

func TestDesiredControlPlaneZVersion_ZStreamManagedUpgrade(t *testing.T) {
	tests := []struct {
		name                  string
		activeVersions        []api.HCPClusterActiveVersion
		customerDesiredMinor  string
		channelGroup          string
		roundTripper          controlplaneversion.RoundTrip
		hostedCluster         *hypershiftv1beta1.HostedCluster
		expectedVersion       *semver.Version
		expectedError         bool
		expectedErrorContains string
	}{
		{
			name:                 "Z-stream upgrade - returns best ranked version from HostedCluster updates",
			activeVersions:       []api.HCPClusterActiveVersion{{Version: ptr.To(semver.MustParse("4.19.15")), State: configv1.CompletedUpdate}},
			customerDesiredMinor: "4.19",
			channelGroup:         "stable",
			hostedCluster: testHostedCluster("stable-4.19", []configv1.Release{
				{Version: "4.19.18", Channels: []string{"stable-4.19", "stable-4.20"}},
				{Version: "4.19.22", Channels: []string{"stable-4.19"}},
			}),
			expectedVersion: ptr.To(semver.MustParse("4.19.18")),
			expectedError:   false,
		},
		{
			name:                 "Z-stream upgrade - already at latest returns error (no available updates)",
			activeVersions:       []api.HCPClusterActiveVersion{{Version: ptr.To(semver.MustParse("4.19.22")), State: configv1.CompletedUpdate}},
			customerDesiredMinor: "4.19",
			channelGroup:         "stable",
			hostedCluster: &hypershiftv1beta1.HostedCluster{
				Spec: hypershiftv1beta1.HostedClusterSpec{
					Channel: "stable-4.19",
				},
				Status: hypershiftv1beta1.HostedClusterStatus{
					Version: &hypershiftv1beta1.ClusterVersionStatus{
						Desired: configv1.Release{Version: "4.19.22"},
					},
				},
			},
			expectedVersion:       nil,
			expectedError:         true,
			expectedErrorContains: "no updates are currently recommended",
		},
		{
			name:                 "Z-stream upgrade - no desired minor version specified",
			activeVersions:       []api.HCPClusterActiveVersion{{Version: ptr.To(semver.MustParse("4.19.15")), State: configv1.CompletedUpdate}},
			customerDesiredMinor: "",
			channelGroup:         "stable",
			expectedVersion:      nil,
			expectedError:        false,
		},
		{
			name:                 "Z-stream upgrade - no channel group specified",
			activeVersions:       []api.HCPClusterActiveVersion{{Version: ptr.To(semver.MustParse("4.19.15")), State: configv1.CompletedUpdate}},
			customerDesiredMinor: "4.19",
			channelGroup:         "",
			expectedVersion:      nil,
			expectedError:        false,
		},
		{
			name:                 "Z-stream upgrade - candidate channel, customer desired full version (4.20.15) normalized to same minor",
			activeVersions:       []api.HCPClusterActiveVersion{{Version: ptr.To(semver.MustParse("4.20.10")), State: configv1.CompletedUpdate}},
			customerDesiredMinor: "4.20.15",
			channelGroup:         "candidate",
			hostedCluster: testHostedCluster("candidate-4.20", []configv1.Release{
				{Version: "4.20.15", Channels: []string{"candidate-4.20", "candidate-4.21"}},
				{Version: "4.20.12", Channels: []string{"candidate-4.20"}},
			}),
			expectedVersion: ptr.To(semver.MustParse("4.20.15")),
			expectedError:   false,
		},
		{
			name:                 "Z-stream upgrade - nil hostedCluster falls back to install path via roundTripper",
			activeVersions:       []api.HCPClusterActiveVersion{{Version: ptr.To(semver.MustParse("4.19.15")), State: configv1.CompletedUpdate}},
			customerDesiredMinor: "4.19",
			channelGroup:         "stable",
			hostedCluster:        nil,
			roundTripper: testRoundTripperFromReleases([]testRelease{
				{version: "4.19.15", channels: "stable-4.19,stable-4.20"},
				{version: "4.19.22", channels: "stable-4.19"},
			}),
			expectedVersion: ptr.To(semver.MustParse("4.19.15")),
			expectedError:   false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mockResourcesDBClient := corecosmosstoragetesting.NewMockResourcesDBClient()
			syncer := &controlPlaneDesiredVersionSyncer{
				resourcesDBClient:             mockResourcesDBClient,
				roundTripper:                  tt.roundTripper,
				serviceProviderClusterLister:  &corelistertesting.DBServiceProviderClusterLister{ResourcesDBClient: mockResourcesDBClient},
				serviceProviderNodePoolLister: &corelistertesting.DBServiceProviderNodePoolLister{ResourcesDBClient: mockResourcesDBClient},
			}

			ctx := context.Background()
			result, err := syncer.desiredControlPlaneZVersion(ctx, tt.hostedCluster, api.Must(api.ToClusterResourceID("6b690bec-0c16-4ecb-8f67-781caf40bba7", "test-rg", "test-cluster")), tt.customerDesiredMinor, tt.channelGroup, tt.activeVersions, false)

			assertVersionResult(t, result, err, tt.expectedVersion, tt.expectedError, tt.expectedErrorContains)
		})
	}
}

func TestDesiredControlPlaneZVersion_NextYStreamUpgrade(t *testing.T) {
	tests := []struct {
		name                  string
		activeVersions        []api.HCPClusterActiveVersion
		customerDesiredMinor  string
		channelGroup          string
		roundTripper          controlplaneversion.RoundTrip
		hostedCluster         *hypershiftv1beta1.HostedCluster
		cosmosResources       []any
		expectedVersion       *semver.Version
		expectedError         bool
		expectedErrorContains string
	}{
		{
			name:                 "Y-stream upgrade - direct path available via target minor graph",
			activeVersions:       []api.HCPClusterActiveVersion{{Version: ptr.To(semver.MustParse("4.19.22")), State: configv1.CompletedUpdate}},
			customerDesiredMinor: "4.20",
			channelGroup:         "stable",
			roundTripper: testRoundTripperFromReleases([]testRelease{
				{version: "4.20.10", channels: "stable-4.20,stable-4.21"},
				{version: "4.20.15", channels: "stable-4.20"},
			}),
			expectedVersion: ptr.To(semver.MustParse("4.20.10")),
			expectedError:   false,
		},
		{
			name:                 "Y-stream upgrade - succeeds with node pool within skew versus desired minor",
			activeVersions:       []api.HCPClusterActiveVersion{{Version: ptr.To(semver.MustParse("4.19.22")), State: configv1.CompletedUpdate}},
			customerDesiredMinor: "4.20",
			channelGroup:         "stable",
			roundTripper: testRoundTripperFromReleases([]testRelease{
				{version: "4.20.10", channels: "stable-4.20,stable-4.21"},
				{version: "4.20.15", channels: "stable-4.20"},
			}),
			cosmosResources: testCosmosClusterWithWorkersNodePoolAtVersion("4.18.0"),
			expectedVersion: ptr.To(semver.MustParse("4.20.10")),
			expectedError:   false,
		},
		{
			name:                 "Y-stream upgrade - no target minor versions, falls back to current minor via hostedCluster",
			activeVersions:       []api.HCPClusterActiveVersion{{Version: ptr.To(semver.MustParse("4.19.15")), State: configv1.CompletedUpdate}},
			customerDesiredMinor: "4.20",
			channelGroup:         "stable",
			roundTripper:         testRoundTripperFromReleases([]testRelease{}),
			hostedCluster: testHostedCluster("stable-4.19", []configv1.Release{
				{Version: "4.19.22", Channels: []string{"stable-4.19", "stable-4.20"}},
				{Version: "4.19.18", Channels: []string{"stable-4.19"}},
			}),
			expectedVersion: ptr.To(semver.MustParse("4.19.22")),
			expectedError:   false,
		},
		{
			name:                 "Y-stream upgrade - no path in target minor and no updates in current minor",
			activeVersions:       []api.HCPClusterActiveVersion{{Version: ptr.To(semver.MustParse("4.20.22")), State: configv1.CompletedUpdate}},
			customerDesiredMinor: "4.21",
			channelGroup:         "candidate",
			cosmosResources:      testCosmosClusterWithWorkersNodePoolAtVersion("4.20.22"),
			roundTripper:         testRoundTripperFromReleases([]testRelease{}),
			hostedCluster: &hypershiftv1beta1.HostedCluster{
				Spec: hypershiftv1beta1.HostedClusterSpec{
					Channel: "candidate-4.20",
				},
				Status: hypershiftv1beta1.HostedClusterStatus{
					Version: &hypershiftv1beta1.ClusterVersionStatus{
						Desired: configv1.Release{Version: "4.20.22"},
					},
				},
			},
			expectedVersion:       nil,
			expectedError:         true,
			expectedErrorContains: "no updates are currently recommended",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx := context.Background()
			mockResourcesDBClient, err := corecosmosstoragetesting.NewMockResourcesDBClientWithResources(ctx, tt.cosmosResources)
			require.NoError(t, err)
			syncer := &controlPlaneDesiredVersionSyncer{
				resourcesDBClient:             mockResourcesDBClient,
				roundTripper:                  tt.roundTripper,
				serviceProviderClusterLister:  &corelistertesting.DBServiceProviderClusterLister{ResourcesDBClient: mockResourcesDBClient},
				serviceProviderNodePoolLister: &corelistertesting.DBServiceProviderNodePoolLister{ResourcesDBClient: mockResourcesDBClient},
			}

			result, err := syncer.desiredControlPlaneZVersion(ctx, tt.hostedCluster, api.Must(api.ToClusterResourceID("6b690bec-0c16-4ecb-8f67-781caf40bba7", "test-rg", "test-cluster")), tt.customerDesiredMinor, tt.channelGroup, tt.activeVersions, false)

			assertVersionResult(t, result, err, tt.expectedVersion, tt.expectedError, tt.expectedErrorContains)
		})
	}
}

// testCosmosClusterWithWorkersNodePoolAtVersion returns a cluster and workers node pool for the subscription, resource group,
// and cluster name shared by desiredControlPlaneZVersion tests. nodePoolVersionId is properties.version.id on the pool.
// Also seeds an empty ServiceProviderCluster and an empty ServiceProviderNodePool the
// way the production creator controllers would have populated them.
func testCosmosClusterWithWorkersNodePoolAtVersion(nodePoolVersionId string) []any {
	clusterResourceId, cluster := testCosmosClusterResource()
	workersNodePool := testCosmosNodePool(clusterResourceId, "workers", nodePoolVersionId, false)
	return []any{
		cluster,
		workersNodePool,
		testCosmosServiceProviderNodePool(workersNodePool.ResourceID),
	}
}

func testCosmosClusterResource() (*azcorearm.ResourceID, *api.HCPOpenShiftCluster) {
	clusterResourceId := api.Must(api.ToClusterResourceID("6b690bec-0c16-4ecb-8f67-781caf40bba7", "test-rg", "test-cluster"))
	cluster := &api.HCPOpenShiftCluster{
		CosmosMetadata: arm.CosmosMetadata{
			ResourceID:   clusterResourceId,
			PartitionKey: strings.ToLower(clusterResourceId.SubscriptionID),
		},
		TrackedResource: arm.TrackedResource{
			Resource: arm.Resource{
				ID:   clusterResourceId,
				Name: clusterResourceId.Name,
				Type: clusterResourceId.ResourceType.String(),
			},
		},
		ServiceProviderProperties: api.HCPOpenShiftClusterServiceProviderProperties{
			ClusterServiceID: api.Ptr(api.Must(api.NewInternalID("/api/clusters_mgmt/v1/clusters/test-cluster"))),
		},
	}
	return clusterResourceId, cluster
}

// testCosmosServiceProviderNodePool returns an empty ServiceProviderNodePool nested under
// the given node pool, the way CreateServiceProviderNodePool would have populated it in production.
func testCosmosServiceProviderNodePool(nodePoolResourceId *azcorearm.ResourceID) *api.ServiceProviderNodePool {
	spnpResourceId := api.Must(azcorearm.ParseResourceID(
		nodePoolResourceId.String() + "/serviceProviderNodePools/" + api.ServiceProviderNodePoolResourceName,
	))
	return &api.ServiceProviderNodePool{
		CosmosMetadata: arm.CosmosMetadata{
			ResourceID:   spnpResourceId,
			PartitionKey: strings.ToLower(spnpResourceId.SubscriptionID),
		},
	}
}

func testCosmosNodePool(clusterResourceId *azcorearm.ResourceID, name, nodePoolVersionId string, deleting bool) *api.HCPOpenShiftClusterNodePool {
	nodePoolResourceId := api.Must(azcorearm.ParseResourceID(clusterResourceId.String() + "/nodePools/" + name))
	nodePool := &api.HCPOpenShiftClusterNodePool{
		CosmosMetadata: arm.CosmosMetadata{
			ResourceID:   nodePoolResourceId,
			PartitionKey: strings.ToLower(nodePoolResourceId.SubscriptionID),
		},
		TrackedResource: arm.NewTrackedResource(nodePoolResourceId, "eastus"),
		Properties: api.HCPOpenShiftClusterNodePoolProperties{
			Version: api.NodePoolVersionProfile{ID: nodePoolVersionId},
		},
		ServiceProviderProperties: api.HCPOpenShiftClusterNodePoolServiceProviderProperties{
			ClusterServiceID: api.Ptr(api.Must(api.NewInternalID("/api/clusters_mgmt/v1/clusters/test-cluster/node_pools/" + name))),
		},
	}
	if deleting {
		nodePool.ServiceProviderProperties.DeletionTimestamp = ptr.To(metav1.Now())
	}
	return nodePool
}

func TestDesiredControlPlaneZVersion_Validations(t *testing.T) {
	tests := []struct {
		name                        string
		activeVersions              []api.HCPClusterActiveVersion
		customerDesiredMinor        string
		channelGroup                string
		cosmosResources             []any
		experimentalReleaseFeatures bool
		expectedVersion             *semver.Version
		expectedError               bool
		expectedErrorContains       string
	}{
		{
			name:                  "Validation - downgrade not allowed (4.20 -> 4.19)",
			activeVersions:        []api.HCPClusterActiveVersion{{Version: ptr.To(semver.MustParse("4.20.15")), State: configv1.CompletedUpdate}},
			customerDesiredMinor:  "4.19",
			channelGroup:          "stable",
			expectedVersion:       nil,
			expectedError:         true,
			expectedErrorContains: "only upgrades to the next minor version are allowed, no downgrades",
		},
		{
			name:                  "Validation - OpenShift 5.x requires AFEC (4.20 -> 5.0)",
			activeVersions:        []api.HCPClusterActiveVersion{{Version: ptr.To(semver.MustParse("4.20.15")), State: configv1.CompletedUpdate}},
			customerDesiredMinor:  "5.0",
			channelGroup:          "stable",
			expectedVersion:       nil,
			expectedError:         true,
			expectedErrorContains: "OpenShift v5 and above is not supported",
		},
		{
			name:                        "Validation - unsupported cross-major (4.20 -> 5.0, not a supported 4→5 landing) when AFEC registered",
			activeVersions:              []api.HCPClusterActiveVersion{{Version: ptr.To(semver.MustParse("4.20.15")), State: configv1.CompletedUpdate}},
			customerDesiredMinor:        "5.0",
			channelGroup:                "stable",
			experimentalReleaseFeatures: true,
			expectedVersion:             nil,
			expectedError:               true,
			expectedErrorContains:       "cross-major upgrade from 4.20 is only allowed to",
		},
		{
			name:                  "Validation - skip minor version not allowed (4.19 -> 4.21)",
			activeVersions:        []api.HCPClusterActiveVersion{{Version: ptr.To(semver.MustParse("4.19.22")), State: configv1.CompletedUpdate}},
			customerDesiredMinor:  "4.21",
			channelGroup:          "stable",
			expectedVersion:       nil,
			expectedError:         true,
			expectedErrorContains: "only upgrade to the next minor is allowed",
		},
		{
			name:                  "Validation - major version downgrade not allowed (5.1 -> 4.20)",
			activeVersions:        []api.HCPClusterActiveVersion{{Version: ptr.To(semver.MustParse("5.1.5")), State: configv1.CompletedUpdate}},
			customerDesiredMinor:  "4.20",
			channelGroup:          "stable",
			expectedVersion:       nil,
			expectedError:         true,
			expectedErrorContains: "only upgrades to the next minor version are allowed, no downgrades",
		},
		{
			name:                        "Validation - node pool minor skew blocks supported cross-major desired minor",
			activeVersions:              []api.HCPClusterActiveVersion{{Version: ptr.To(semver.MustParse("4.22.0")), State: configv1.CompletedUpdate}},
			customerDesiredMinor:        "5.0",
			channelGroup:                "stable",
			cosmosResources:             testCosmosClusterWithWorkersNodePoolAtVersion("4.20.0"),
			experimentalReleaseFeatures: true,
			expectedVersion:             nil,
			expectedError:               true,
			expectedErrorContains:       "incompatible with node pool",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx := context.Background()
			mockResourcesDBClient, err := corecosmosstoragetesting.NewMockResourcesDBClientWithResources(ctx, tt.cosmosResources)
			require.NoError(t, err)
			syncer := &controlPlaneDesiredVersionSyncer{
				resourcesDBClient:             mockResourcesDBClient,
				serviceProviderClusterLister:  &corelistertesting.DBServiceProviderClusterLister{ResourcesDBClient: mockResourcesDBClient},
				serviceProviderNodePoolLister: &corelistertesting.DBServiceProviderNodePoolLister{ResourcesDBClient: mockResourcesDBClient},
			}

			result, err := syncer.desiredControlPlaneZVersion(ctx, nil, api.Must(api.ToClusterResourceID("6b690bec-0c16-4ecb-8f67-781caf40bba7", "test-rg", "test-cluster")), tt.customerDesiredMinor, tt.channelGroup, tt.activeVersions, tt.experimentalReleaseFeatures)

			assertVersionResult(t, result, err, tt.expectedVersion, tt.expectedError, tt.expectedErrorContains)
		})
	}
}

func TestDesiredControlPlaneZVersion_CrossMajorUpgrade(t *testing.T) {
	tests := []struct {
		name                        string
		activeVersions              []api.HCPClusterActiveVersion
		customerDesiredMinor        string
		channelGroup                string
		roundTripper                controlplaneversion.RoundTrip
		cosmosResources             []any
		experimentalReleaseFeatures bool
		expectedVersion             *semver.Version
		expectedError               bool
		expectedErrorContains       string
	}{
		{
			name:                        "Cross-major allowed — 4.22 to 5.0 with experimental release features and compatible node pools",
			activeVersions:              []api.HCPClusterActiveVersion{{Version: ptr.To(semver.MustParse("4.22.0")), State: configv1.CompletedUpdate}},
			customerDesiredMinor:        "5.0",
			channelGroup:                "stable",
			cosmosResources:             testCosmosClusterWithWorkersNodePoolAtVersion("4.22.0"),
			experimentalReleaseFeatures: true,
			roundTripper: testRoundTripperFromReleases([]testRelease{
				{version: "5.0.10", channels: "stable-5.0,stable-5.1"},
				{version: "5.0.15", channels: "stable-5.0"},
			}),
			expectedVersion: ptr.To(semver.MustParse("5.0.10")),
			expectedError:   false,
		},
		{
			name:                        "Cross-major not allowed — 4.22 to 5.0 without experimental release features",
			activeVersions:              []api.HCPClusterActiveVersion{{Version: ptr.To(semver.MustParse("4.22.0")), State: configv1.CompletedUpdate}},
			customerDesiredMinor:        "5.0",
			channelGroup:                "stable",
			experimentalReleaseFeatures: false,
			expectedVersion:             nil,
			expectedError:               true,
			expectedErrorContains:       "OpenShift v5 and above is not supported",
		},
		{
			name:                        "Cross-major not allowed — 4.21 to 5.0 is not a supported landing even with experimental release features",
			activeVersions:              []api.HCPClusterActiveVersion{{Version: ptr.To(semver.MustParse("4.21.10")), State: configv1.CompletedUpdate}},
			customerDesiredMinor:        "5.0",
			channelGroup:                "stable",
			cosmosResources:             testCosmosClusterWithWorkersNodePoolAtVersion("4.21.0"),
			experimentalReleaseFeatures: true,
			expectedVersion:             nil,
			expectedError:               true,
			expectedErrorContains:       "cross-major upgrade from 4.21 is only allowed to",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx := context.Background()
			mockResourcesDBClient, err := corecosmosstoragetesting.NewMockResourcesDBClientWithResources(ctx, tt.cosmosResources)
			require.NoError(t, err)
			syncer := &controlPlaneDesiredVersionSyncer{
				resourcesDBClient:             mockResourcesDBClient,
				roundTripper:                  tt.roundTripper,
				serviceProviderClusterLister:  &corelistertesting.DBServiceProviderClusterLister{ResourcesDBClient: mockResourcesDBClient},
				serviceProviderNodePoolLister: &corelistertesting.DBServiceProviderNodePoolLister{ResourcesDBClient: mockResourcesDBClient},
			}

			result, err := syncer.desiredControlPlaneZVersion(ctx, nil, api.Must(api.ToClusterResourceID("6b690bec-0c16-4ecb-8f67-781caf40bba7", "test-rg", "test-cluster")), tt.customerDesiredMinor, tt.channelGroup, tt.activeVersions, tt.experimentalReleaseFeatures)

			assertVersionResult(t, result, err, tt.expectedVersion, tt.expectedError, tt.expectedErrorContains)
		})
	}
}

func TestDesiredControlPlaneZVersion_InitialVersionSelection(t *testing.T) {
	tests := []struct {
		name                  string
		customerDesiredMinor  string
		channelGroup          string
		roundTripper          controlplaneversion.RoundTrip
		expectedVersion       *semver.Version
		expectedError         bool
		expectedErrorContains string
	}{
		{
			name:                 "Initial version - prefers channel connectivity over latest",
			customerDesiredMinor: "4.19",
			channelGroup:         "stable",
			roundTripper: testRoundTripperFromReleases([]testRelease{
				{version: "4.19.15", channels: "stable-4.19,stable-4.20"},
				{version: "4.19.22", channels: "stable-4.19"},
			}),
			expectedVersion: ptr.To(semver.MustParse("4.19.15")),
			expectedError:   false,
		},
		{
			name:                  "Initial version - empty graph returns error",
			customerDesiredMinor:  "4.19",
			channelGroup:          "stable",
			roundTripper:          testRoundTripperFromReleases([]testRelease{}),
			expectedVersion:       nil,
			expectedError:         true,
			expectedErrorContains: "no install targets found",
		},
		{
			name:                 "Initial version - returns latest when no channel connectivity difference",
			customerDesiredMinor: "4.19",
			channelGroup:         "stable",
			roundTripper: testRoundTripperFromReleases([]testRelease{
				{version: "4.19.15", channels: "stable-4.19"},
				{version: "4.19.22", channels: "stable-4.19"},
			}),
			expectedVersion: ptr.To(semver.MustParse("4.19.22")),
			expectedError:   false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mockResourcesDBClient := corecosmosstoragetesting.NewMockResourcesDBClient()
			syncer := &controlPlaneDesiredVersionSyncer{
				resourcesDBClient:             mockResourcesDBClient,
				roundTripper:                  tt.roundTripper,
				serviceProviderClusterLister:  &corelistertesting.DBServiceProviderClusterLister{ResourcesDBClient: mockResourcesDBClient},
				serviceProviderNodePoolLister: &corelistertesting.DBServiceProviderNodePoolLister{ResourcesDBClient: mockResourcesDBClient},
			}

			activeVersions := []api.HCPClusterActiveVersion{}

			ctx := context.Background()
			result, err := syncer.desiredControlPlaneZVersion(ctx, nil, api.Must(api.ToClusterResourceID("6b690bec-0c16-4ecb-8f67-781caf40bba7", "test-rg", "test-cluster")), tt.customerDesiredMinor, tt.channelGroup, activeVersions, false)

			assertVersionResult(t, result, err, tt.expectedVersion, tt.expectedError, tt.expectedErrorContains)
		})
	}
}

// assertVersionResult is a helper function that validates the result of desiredControlPlaneZVersion
func assertVersionResult(t *testing.T, result *semver.Version, err error, expectedVersion *semver.Version, expectedError bool, expectedErrorContains string) {
	if expectedError {
		assert.Error(t, err)
		assert.NotEmpty(t, expectedErrorContains)
		assert.ErrorContains(t, err, expectedErrorContains)
	} else {
		assert.NoError(t, err)
		if expectedVersion == nil {
			assert.Nil(t, result)
		} else {
			assert.NotNil(t, result)
			assert.True(t, result.EQ(*expectedVersion), "Expected version %q, got %q", expectedVersion.String(), result.String())
		}
	}
}

func createTestHCPClusterWithCustomerVersion(t *testing.T, ctx context.Context, mockResourcesDBClient *corecosmosstoragetesting.MockResourcesDBClient, customerVersionID, channelGroup string) {
	t.Helper()
	createTestSubscription(t, ctx, mockResourcesDBClient)
	clusterResourceID := api.Must(azcorearm.ParseResourceID("/subscriptions/" + testSubscriptionID +
		"/resourceGroups/" + testResourceGroupName +
		"/providers/Microsoft.RedHatOpenShift/hcpOpenShiftClusters/" + testClusterName))
	clusterInternalID, err := api.NewInternalID(testCSClusterIDStr)
	require.NoError(t, err)
	cluster := &api.HCPOpenShiftCluster{
		CosmosMetadata: arm.CosmosMetadata{
			ResourceID:   clusterResourceID,
			PartitionKey: strings.ToLower(clusterResourceID.SubscriptionID),
		},
		TrackedResource: arm.TrackedResource{
			Resource: arm.Resource{
				ID:   clusterResourceID,
				Name: testClusterName,
				Type: api.ClusterResourceType.String(),
			},
			Location: "eastus",
		},
		CustomerProperties: api.HCPOpenShiftClusterCustomerProperties{
			Version: api.VersionProfile{
				ID:           customerVersionID,
				ChannelGroup: channelGroup,
			},
		},
		ServiceProviderProperties: api.HCPOpenShiftClusterServiceProviderProperties{
			ProvisioningState: arm.ProvisioningStateSucceeded,
			ClusterServiceID:  &clusterInternalID,
		},
	}
	_, err = mockResourcesDBClient.HCPClusters(testSubscriptionID, testResourceGroupName).Create(ctx, cluster, nil)
	require.NoError(t, err)
}

func TestControlPlaneDesiredVersionSyncer_SyncOnce(t *testing.T) {
	clusterKey := controllerutils.HCPClusterKey{
		SubscriptionID:    testSubscriptionID,
		ResourceGroupName: testResourceGroupName,
		HCPClusterName:    testClusterName,
	}
	subResourceID := api.Must(azcorearm.ParseResourceID("/subscriptions/" + testSubscriptionID))
	subscriptionLister := &corelistertesting.SliceSubscriptionLister{
		Subscriptions: []*arm.Subscription{{
			CosmosMetadata: arm.CosmosMetadata{ResourceID: subResourceID, PartitionKey: strings.ToLower(subResourceID.SubscriptionID)},
			ResourceID:     subResourceID,
			Properties:     &arm.SubscriptionProperties{},
		}},
	}

	const testChannelGroup = "stable"

	tests := []struct {
		name                   string
		customerVersion        string
		controlPlaneVersion    string
		previousDesiredVersion *semver.Version
		roundTripper           controlplaneversion.RoundTrip
		hostedCluster          *hypershiftv1beta1.HostedCluster
		wantSyncErr            bool
		wantErrContains        string
		wantDesiredVersion     *semver.Version
		wantIntentFailed       *metav1.Condition
	}{
		{
			name:                "successful resolution persists desired version and sets IntentFailed False",
			customerVersion:     "4.19",
			controlPlaneVersion: "4.19.15",
			hostedCluster: testHostedCluster("stable-4.19", []configv1.Release{
				{Version: "4.19.22", Channels: []string{"stable-4.19"}},
			}),
			wantDesiredVersion: ptr.To(semver.MustParse("4.19.22")),
			wantIntentFailed: &metav1.Condition{
				Type:   api.ControllerConditionTypeIntentFailed,
				Status: metav1.ConditionFalse,
				Reason: api.ControllerConditionReasonAsExpected,
			},
		},
		{
			name:                   "lower resolved desired does not replace higher previously selected desired",
			customerVersion:        "4.19",
			controlPlaneVersion:    "4.19.15",
			previousDesiredVersion: ptr.To(semver.MustParse("4.19.22")),
			hostedCluster: testHostedCluster("stable-4.19", []configv1.Release{
				{Version: "4.19.18", Channels: []string{"stable-4.19"}},
			}),
			wantDesiredVersion: ptr.To(semver.MustParse("4.19.22")),
			wantIntentFailed: &metav1.Condition{
				Type:   api.ControllerConditionTypeIntentFailed,
				Status: metav1.ConditionFalse,
				Reason: api.ControllerConditionReasonAsExpected,
			},
		},
		{
			name:                "validation error persists IntentFailed and does not set desired version",
			customerVersion:     "4.19",
			controlPlaneVersion: "4.20.15",
			wantDesiredVersion:  nil,
			wantIntentFailed: &metav1.Condition{
				Type:    api.ControllerConditionTypeIntentFailed,
				Status:  metav1.ConditionTrue,
				Reason:  api.VersionUpgradeNotAcceptedReason,
				Message: "invalid next y-stream upgrade path from 4.20.0 to 4.19.0: only upgrades to the next minor version are allowed, no downgrades",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			ctx := utils.ContextWithLogger(context.Background(), logr.Discard())
			ctrl := gomock.NewController(t)
			mockResourcesDBClient := corecosmosstoragetesting.NewMockResourcesDBClient()
			mockCS := ocm.NewMockClusterServiceClientSpec(ctrl)

			createTestHCPClusterWithCustomerVersion(t, ctx, mockResourcesDBClient, tt.customerVersion, testChannelGroup)
			createServiceProviderClusterWithActiveAndDesiredVersion(t, ctx, mockResourcesDBClient, semver.MustParse(tt.controlPlaneVersion), tt.previousDesiredVersion)

			syncer := &controlPlaneDesiredVersionSyncer{
				readDesireLister:              newHostedClusterReadDesireListerFromObject(t, tt.hostedCluster),
				resourcesDBClient:             mockResourcesDBClient,
				clusterServiceClient:          mockCS,
				subscriptionLister:            subscriptionLister,
				roundTripper:                  tt.roundTripper,
				serviceProviderClusterLister:  &corelistertesting.DBServiceProviderClusterLister{ResourcesDBClient: mockResourcesDBClient},
				serviceProviderNodePoolLister: &corelistertesting.DBServiceProviderNodePoolLister{ResourcesDBClient: mockResourcesDBClient},
			}

			err := syncer.SyncOnce(ctx, clusterKey)
			if tt.wantSyncErr {
				require.Error(t, err)
				require.NotEmpty(t, tt.wantErrContains, "when wantSyncErr is true, wantErrContains must be set to a substring of the expected error")
				assert.ErrorContains(t, err, tt.wantErrContains)
			} else {
				require.NoError(t, err)
				assert.Empty(t, tt.wantErrContains, "when wantSyncErr is false, wantErrContains must be empty")
			}

			serviceProviderCluster, getServiceProviderClusterErr := mockResourcesDBClient.ServiceProviderClusters(testSubscriptionID, testResourceGroupName, testClusterName).Get(ctx, api.ServiceProviderClusterResourceName)
			require.NoError(t, getServiceProviderClusterErr)
			gotDesired := serviceProviderCluster.Spec.ControlPlaneVersion.DesiredVersion
			if tt.wantDesiredVersion != nil {
				require.NotNil(t, gotDesired)
				assert.True(t, gotDesired.EQ(*tt.wantDesiredVersion), "wanted desired version %s, got %s", tt.wantDesiredVersion.String(), gotDesired.String())
			} else {
				assert.Nil(t, gotDesired)
			}

			controlPlaneDesiredVersionControllerDoc, getControllerDocErr := mockResourcesDBClient.HCPClusters(testSubscriptionID, testResourceGroupName).
				Controllers(testClusterName).Get(ctx, controlPlaneDesiredVersionControllerName)
			if tt.wantIntentFailed != nil {
				require.NoError(t, getControllerDocErr)
				require.NotNil(t, controlPlaneDesiredVersionControllerDoc)
				intentFailedCondition := apimeta.FindStatusCondition(controlPlaneDesiredVersionControllerDoc.Status.Conditions,
					api.ControllerConditionTypeIntentFailed)
				require.NotNil(t, intentFailedCondition)
				assert.Equal(t, tt.wantIntentFailed.Type, intentFailedCondition.Type)
				assert.Equal(t, tt.wantIntentFailed.Status, intentFailedCondition.Status)
				assert.Equal(t, tt.wantIntentFailed.Reason, intentFailedCondition.Reason)
				if tt.wantIntentFailed.Status == metav1.ConditionTrue {
					require.NotEmpty(t, tt.wantIntentFailed.Message, "set wantIntentFailed.Message to the exact persisted IntentFailed message")
					assert.Equal(t, tt.wantIntentFailed.Message, intentFailedCondition.Message)
				} else {
					assert.Empty(t, intentFailedCondition.Message, "when wantIntentFailed.Status is false, intentFailedCondition.Message must be empty")
				}
			}
		})
	}
}

// newHostedClusterReadDesireListerFromObject builds a ReadDesireLister that
// returns the given HostedCluster. If hc is nil, it returns the default valid
// HostedCluster (matching newValidHostedClusterReadDesireLister).
func newHostedClusterReadDesireListerFromObject(t *testing.T, hc *hypershiftv1beta1.HostedCluster) *kubeapplierlistertesting.SliceReadDesireLister {
	t.Helper()
	if hc == nil {
		return &kubeapplierlistertesting.SliceReadDesireLister{
			Desires: []*kubeapplier.ReadDesire{newHostedClusterReadDesire(t, testClusterExternalID)},
		}
	}
	raw, err := json.Marshal(hc)
	require.NoError(t, err)
	return &kubeapplierlistertesting.SliceReadDesireLister{
		Desires: []*kubeapplier.ReadDesire{{
			CosmosMetadata: api.CosmosMetadata{ResourceID: hostedClusterReadDesireResourceID(t)},
			Status: kubeapplier.ReadDesireStatus{
				KubeContent: &kruntime.RawExtension{Raw: raw},
			},
		}},
	}
}

func createServiceProviderClusterWithActiveAndDesiredVersion(t *testing.T, ctx context.Context, mockResourcesDBClient *corecosmosstoragetesting.MockResourcesDBClient, activeVersion semver.Version, desiredVersion *semver.Version) {
	t.Helper()

	serviceProviderCluster := &api.ServiceProviderCluster{
		CosmosMetadata: api.CosmosMetadata{
			ResourceID: api.Must(azcorearm.ParseResourceID(
				api.ToServiceProviderClusterResourceIDString(testSubscriptionID, testResourceGroupName, testClusterName),
			)),
		},
		Spec: api.ServiceProviderClusterSpec{
			ControlPlaneVersion: api.ServiceProviderClusterSpecVersion{
				DesiredVersion: desiredVersion,
			},
		},
		Status: api.ServiceProviderClusterStatus{
			ControlPlaneVersion: api.ServiceProviderClusterStatusVersion{
				ActiveVersions: []api.HCPClusterActiveVersion{
					{Version: ptr.To(activeVersion), State: configv1.CompletedUpdate},
				},
			},
		},
	}
	serviceProviderCluster.SetPartitionKey(testSubscriptionID)
	_, err := mockResourcesDBClient.ServiceProviderClusters(testSubscriptionID, testResourceGroupName, testClusterName).Create(ctx, serviceProviderCluster, nil)
	require.NoError(t, err)
}

// boomActiveOperationLister is a test double that returns the configured
// error from ListActiveOperationsForCluster. It exists so the gating helper
// can exercise its error-propagation branch without a misbehaving mock DB.
type boomActiveOperationLister struct {
	corelisters.ActiveOperationLister
	err error
}

func (b *boomActiveOperationLister) Get(_ context.Context, _, _ string) (*api.Operation, error) {
	return nil, b.err
}

func (b *boomActiveOperationLister) ListActiveOperationsForCluster(_ context.Context, _, _, _ string) ([]*api.Operation, error) {
	return nil, b.err
}

// seedClusterCreateOperation seeds an active Create operation rooted at the
// given ExternalID into the mock DB so the DB-backed active operation lister
// can find it.
func seedClusterCreateOperation(t *testing.T, ctx context.Context, mockDB *corecosmosstoragetesting.MockResourcesDBClient, externalID *azcorearm.ResourceID, opName string) {
	t.Helper()
	opResourceID := api.Must(azcorearm.ParseResourceID(api.ToOperationResourceIDString(externalID.SubscriptionID, opName)))
	operationID := api.Must(azcorearm.ParseResourceID(
		"/subscriptions/" + externalID.SubscriptionID +
			"/providers/Microsoft.RedHatOpenShift/locations/eastus/hcpOperationStatuses/" + opName,
	))
	op := &api.Operation{
		CosmosMetadata: api.CosmosMetadata{
			ResourceID:   opResourceID,
			PartitionKey: strings.ToLower(externalID.SubscriptionID),
		},
		Status:      arm.ProvisioningStateAccepted,
		Request:     cosmosstorageutils.OperationRequestCreate,
		ExternalID:  externalID,
		OperationID: operationID,
	}
	_, err := mockDB.Operations(externalID.SubscriptionID).Create(ctx, op, nil)
	require.NoError(t, err)
}

func TestControlPlaneDesiredVersionSyncer_ShouldDetermineDesiredVersion(t *testing.T) {
	clusterResourceID := api.Must(api.ToClusterResourceID(testSubscriptionID, testResourceGroupName, testClusterName))
	now := time.Date(2026, 6, 25, 12, 0, 0, 0, time.UTC)
	listerBoom := errors.New("active operation lister exploded")

	newCluster := func(createdAt *time.Time, activeOperationID string) *api.HCPOpenShiftCluster {
		c := &api.HCPOpenShiftCluster{
			CosmosMetadata: api.CosmosMetadata{
				ResourceID: clusterResourceID,
			},
			TrackedResource: arm.TrackedResource{
				Resource: arm.Resource{
					ID:   clusterResourceID,
					Name: testClusterName,
					Type: api.ClusterResourceType.String(),
				},
			},
			ServiceProviderProperties: api.HCPOpenShiftClusterServiceProviderProperties{
				ActiveOperationID: activeOperationID,
			},
		}
		if createdAt != nil {
			c.SystemData = &arm.SystemData{CreatedAt: createdAt}
		}
		return c
	}
	newSPC := func(desired *semver.Version) *api.ServiceProviderCluster {
		return &api.ServiceProviderCluster{
			Spec: api.ServiceProviderClusterSpec{
				ControlPlaneVersion: api.ServiceProviderClusterSpecVersion{DesiredVersion: desired},
			},
		}
	}

	tests := []struct {
		name           string
		cluster        *api.HCPOpenShiftCluster
		spc            *api.ServiceProviderCluster
		seedOperation  bool
		opLister       func(mockDB *corecosmosstoragetesting.MockResourcesDBClient) corelisters.ActiveOperationLister
		wantShouldRun  bool
		wantErrContain string
	}{
		{
			name:          "empty DesiredVersion runs even when create is in flight (gate 1)",
			cluster:       newCluster(ptr.To(now.Add(-5*time.Minute)), "op-create-1"),
			spc:           newSPC(nil),
			seedOperation: true,
			wantShouldRun: true,
		},
		{
			name:          "cluster older than grace period runs even with active create (gate 2)",
			cluster:       newCluster(ptr.To(now.Add(-3*time.Hour)), "op-create-1"),
			spc:           newSPC(ptr.To(semver.MustParse("4.19.15"))),
			seedOperation: true,
			wantShouldRun: true,
		},
		{
			name:          "cluster with no SystemData.CreatedAt runs (treated as old enough)",
			cluster:       newCluster(nil, "op-create-1"),
			spc:           newSPC(ptr.To(semver.MustParse("4.19.15"))),
			seedOperation: true,
			wantShouldRun: true,
		},
		{
			name:          "cluster younger than grace period with no active create runs (gate 3)",
			cluster:       newCluster(ptr.To(now.Add(-5*time.Minute)), ""),
			spc:           newSPC(ptr.To(semver.MustParse("4.19.15"))),
			seedOperation: false,
			wantShouldRun: true,
		},
		{
			name:          "young cluster + DesiredVersion set + active create skips",
			cluster:       newCluster(ptr.To(now.Add(-5*time.Minute)), "op-create-1"),
			spc:           newSPC(ptr.To(semver.MustParse("4.19.15"))),
			seedOperation: true,
			wantShouldRun: false,
		},
		{
			name:    "cluster exactly at grace period boundary still skips (boundary is strict >)",
			cluster: newCluster(ptr.To(now.Add(-clusterCreateGracePeriod)), "op-create-1"),
			spc:     newSPC(ptr.To(semver.MustParse("4.19.15"))),
			// active create present so without the boundary-is-strict gate, the
			// cluster's age would have to push us through.
			seedOperation: true,
			wantShouldRun: false,
		},
		{
			// Fail open: if we can't tell whether a Create is in flight we
			// surface the error to the caller but still report shouldRun=true
			// so a flaky lister doesn't pin the controller in skip-forever
			// mode for the rest of the grace window.
			name:          "active operation lister error is propagated and fails open to shouldRun=true",
			cluster:       newCluster(ptr.To(now.Add(-5*time.Minute)), "op-broken"),
			spc:           newSPC(ptr.To(semver.MustParse("4.19.15"))),
			seedOperation: false,
			opLister: func(_ *corecosmosstoragetesting.MockResourcesDBClient) corelisters.ActiveOperationLister {
				return &boomActiveOperationLister{err: listerBoom}
			},
			wantShouldRun:  true,
			wantErrContain: "failed to get operations",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			ctx := utils.ContextWithLogger(context.Background(), logr.Discard())
			mockDB := corecosmosstoragetesting.NewMockResourcesDBClient()
			if tt.seedOperation {
				seedClusterCreateOperation(t, ctx, mockDB, clusterResourceID, "op-create-1")
			}
			var opLister corelisters.ActiveOperationLister
			if tt.opLister != nil {
				opLister = tt.opLister(mockDB)
			} else {
				opLister = &corelistertesting.DBActiveOperationLister{ResourcesDBClient: mockDB}
			}
			syncer := &controlPlaneDesiredVersionSyncer{
				clock:                 clocktesting.NewFakePassiveClock(now),
				resourcesDBClient:     mockDB,
				activeOperationLister: opLister,
			}

			gotShouldRun, err := syncer.shouldDetermineDesiredVersion(ctx, tt.cluster, tt.spc)
			if tt.wantErrContain != "" {
				require.Error(t, err)
				assert.ErrorContains(t, err, tt.wantErrContain)
			} else {
				require.NoError(t, err)
			}
			assert.Equal(t, tt.wantShouldRun, gotShouldRun)
		})
	}
}

// TestControlPlaneDesiredVersionSyncer_SyncOnceSkipsWhenGated verifies the
// end-to-end skip behaviour: when shouldDetermineDesiredVersion returns false
// SyncOnce returns nil without touching the SPC DesiredVersion or writing a
// controller doc, so the cluster create can finish without an upgrade
// recomputation racing it.
func TestControlPlaneDesiredVersionSyncer_SyncOnceSkipsWhenGated(t *testing.T) {
	clusterKey := controllerutils.HCPClusterKey{
		SubscriptionID:    testSubscriptionID,
		ResourceGroupName: testResourceGroupName,
		HCPClusterName:    testClusterName,
	}
	ctx := utils.ContextWithLogger(context.Background(), logr.Discard())
	mockDB := corecosmosstoragetesting.NewMockResourcesDBClient()
	now := time.Date(2026, 6, 25, 12, 0, 0, 0, time.UTC)

	// Cluster is 5 minutes old.
	createTestHCPClusterWithCustomerVersion(t, ctx, mockDB, "4.19", "stable")
	clusterCRUD := mockDB.HCPClusters(testSubscriptionID, testResourceGroupName)
	existing, err := clusterCRUD.Get(ctx, testClusterName)
	require.NoError(t, err)
	updated := existing.DeepCopy()
	createdAt := now.Add(-5 * time.Minute)
	updated.SystemData = &arm.SystemData{CreatedAt: &createdAt}
	updated.ServiceProviderProperties.ActiveOperationID = "op-create-1"
	_, err = clusterCRUD.Replace(ctx, updated, nil)
	require.NoError(t, err)

	// SPC already has a desired version — gate 1 will not fire.
	createServiceProviderClusterWithActiveAndDesiredVersion(t, ctx, mockDB, semver.MustParse("4.19.15"), ptr.To(semver.MustParse("4.19.22")))

	// Active Create operation pinned to the cluster itself.
	clusterResourceID := api.Must(api.ToClusterResourceID(testSubscriptionID, testResourceGroupName, testClusterName))
	seedClusterCreateOperation(t, ctx, mockDB, clusterResourceID, "op-create-1")

	ctrl := gomock.NewController(t)

	syncer := &controlPlaneDesiredVersionSyncer{
		clock:                        clocktesting.NewFakePassiveClock(now),
		readDesireLister:             newValidHostedClusterReadDesireLister(t),
		resourcesDBClient:            mockDB,
		serviceProviderClusterLister: &corelistertesting.DBServiceProviderClusterLister{ResourcesDBClient: mockDB},
		clusterServiceClient:         ocm.NewMockClusterServiceClientSpec(ctrl),
		subscriptionLister: &corelistertesting.SliceSubscriptionLister{Subscriptions: []*arm.Subscription{{
			ResourceID: api.Must(azcorearm.ParseResourceID("/subscriptions/" + testSubscriptionID)),
			Properties: &arm.SubscriptionProperties{},
		}}},
		activeOperationLister: &corelistertesting.DBActiveOperationLister{ResourcesDBClient: mockDB},
	}

	require.NoError(t, syncer.SyncOnce(ctx, clusterKey))

	// DesiredVersion is untouched.
	spc, err := mockDB.ServiceProviderClusters(testSubscriptionID, testResourceGroupName, testClusterName).Get(ctx, api.ServiceProviderClusterResourceName)
	require.NoError(t, err)
	require.NotNil(t, spc.Spec.ControlPlaneVersion.DesiredVersion)
	assert.True(t, spc.Spec.ControlPlaneVersion.DesiredVersion.EQ(semver.MustParse("4.19.22")), "DesiredVersion must not change on the skip path")

	// Controller doc was never written, since we returned before WriteController.
	_, getControllerDocErr := mockDB.HCPClusters(testSubscriptionID, testResourceGroupName).
		Controllers(testClusterName).Get(ctx, controlPlaneDesiredVersionControllerName)
	assert.True(t, cosmosstorageutils.IsNotFoundError(getControllerDocErr), "controller doc must not be written on the skip path, got err=%v", getControllerDocErr)
}
