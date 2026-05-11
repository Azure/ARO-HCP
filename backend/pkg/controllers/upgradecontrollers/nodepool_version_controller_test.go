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

package upgradecontrollers

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/blang/semver/v4"
	"github.com/go-logr/logr"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	kruntime "k8s.io/apimachinery/pkg/runtime"
	"k8s.io/utils/ptr"

	azcorearm "github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"

	arohcpv1alpha1 "github.com/openshift-online/ocm-sdk-go/arohcp/v1alpha1"
	configv1 "github.com/openshift/api/config/v1"
	"github.com/openshift/hypershift/api/hypershift/v1beta1"

	"github.com/Azure/ARO-HCP/backend/pkg/controllers/controllerutils"
	"github.com/Azure/ARO-HCP/backend/pkg/listers"
	"github.com/Azure/ARO-HCP/backend/pkg/listertesting"
	"github.com/Azure/ARO-HCP/internal/api"
	"github.com/Azure/ARO-HCP/internal/api/arm"
	"github.com/Azure/ARO-HCP/internal/cincinnati"
	"github.com/Azure/ARO-HCP/internal/databasetesting"
	"github.com/Azure/ARO-HCP/internal/ocm"
	"github.com/Azure/ARO-HCP/internal/utils"
)

const (
	testSubscriptionID    = "00000000-0000-0000-0000-000000000001"
	testResourceGroupName = "test-rg"
	testClusterName       = "test-cluster"
	testNodePoolName      = "test-nodepool"
	testCSClusterIDStr    = "/api/aro_hcp/v1alpha1/clusters/" + testClusterName
	testCSNodePoolIDStr   = testCSClusterIDStr + "/node_pools/" + testNodePoolName
	testClusterExternalID = "11111111-1111-1111-1111-111111111111"
)

// alwaysSyncCooldownChecker is a test helper that always allows sync.
type alwaysSyncCooldownChecker struct{}

func (a *alwaysSyncCooldownChecker) CanSync(ctx context.Context, key any) bool {
	return true
}

var _ controllerutils.CooldownChecker = &alwaysSyncCooldownChecker{}

// createTestSubscription creates a subscription in the mock database.
func createTestSubscription(t *testing.T, ctx context.Context, mockResourcesDBClient *databasetesting.MockResourcesDBClient) {
	t.Helper()

	subResourceID := api.Must(azcorearm.ParseResourceID("/subscriptions/" + testSubscriptionID))
	subscription := &arm.Subscription{
		CosmosMetadata: arm.CosmosMetadata{
			ResourceID: subResourceID,
		},
		ResourceID: subResourceID,
		State:      arm.SubscriptionStateRegistered,
		Properties: &arm.SubscriptionProperties{
			TenantId: ptr.To("test-tenant-id"),
		},
	}
	_, err := mockResourcesDBClient.Subscriptions().Create(ctx, subscription, nil)
	require.NoError(t, err)
}

// createTestNodePoolWithVersion creates a parent cluster and a node pool in the mock database.
func createTestNodePoolWithVersion(t *testing.T, ctx context.Context, mockResourcesDBClient *databasetesting.MockResourcesDBClient, desiredVersion string) {
	t.Helper()

	// Create subscription first
	createTestSubscription(t, ctx, mockResourcesDBClient)

	// Create parent cluster first (required by mock DB structure).
	clusterResourceID := api.Must(azcorearm.ParseResourceID("/subscriptions/" + testSubscriptionID +
		"/resourceGroups/" + testResourceGroupName +
		"/providers/Microsoft.RedHatOpenShift/hcpOpenShiftClusters/" + testClusterName))
	clusterInternalID, err := api.NewInternalID(testCSClusterIDStr)
	require.NoError(t, err)

	cluster := &api.HCPOpenShiftCluster{
		TrackedResource: arm.TrackedResource{
			Resource: arm.Resource{
				ID:   clusterResourceID,
				Name: testClusterName,
				Type: api.ClusterResourceType.String(),
			},
			Location: "eastus",
		},
		ServiceProviderProperties: api.HCPOpenShiftClusterServiceProviderProperties{
			ProvisioningState: arm.ProvisioningStateSucceeded,
			ClusterServiceID:  &clusterInternalID,
		},
	}
	_, err = mockResourcesDBClient.HCPClusters(testSubscriptionID, testResourceGroupName).Create(ctx, cluster, nil)
	require.NoError(t, err)

	// Create node pool with version
	nodePoolResourceID := api.Must(azcorearm.ParseResourceID("/subscriptions/" + testSubscriptionID +
		"/resourceGroups/" + testResourceGroupName +
		"/providers/Microsoft.RedHatOpenShift/hcpOpenShiftClusters/" + testClusterName +
		"/nodePools/" + testNodePoolName))
	nodePoolInternalID, err := api.NewInternalID(testCSNodePoolIDStr)
	require.NoError(t, err)

	nodePool := &api.HCPOpenShiftClusterNodePool{
		TrackedResource: arm.TrackedResource{
			Resource: arm.Resource{
				ID:   nodePoolResourceID,
				Name: testNodePoolName,
				Type: api.NodePoolResourceType.String(),
			},
			Location: "eastus",
		},
		Properties: api.HCPOpenShiftClusterNodePoolProperties{
			Version: api.NodePoolVersionProfile{
				ID: desiredVersion,
			},
		},
		ServiceProviderProperties: api.HCPOpenShiftClusterNodePoolServiceProviderProperties{
			ClusterServiceID: nodePoolInternalID,
		},
	}
	_, err = mockResourcesDBClient.HCPClusters(testSubscriptionID, testResourceGroupName).
		NodePools(testClusterName).Create(ctx, nodePool, nil)
	require.NoError(t, err)
}

// newCSNodePool creates a Cluster Service node pool for testing.
func newCSNodePool(t *testing.T, version string) *arohcpv1alpha1.NodePool {
	t.Helper()

	builder := arohcpv1alpha1.NewNodePool().
		ID(testNodePoolName)

	if version != "" {
		builder = builder.Version(arohcpv1alpha1.NewVersion().RawID(version))
	}

	csNodePool, err := builder.Build()
	require.NoError(t, err)
	return csNodePool
}

// hostedClusterContentResourceID returns the resource ID for the readonly HostedCluster ManagementClusterContent
// associated with the test cluster. The slice lister matches on this ID to satisfy GetForCluster.
func hostedClusterContentResourceID(t *testing.T) *azcorearm.ResourceID {
	t.Helper()
	return api.Must(azcorearm.ParseResourceID(
		"/subscriptions/" + testSubscriptionID +
			"/resourceGroups/" + testResourceGroupName +
			"/providers/Microsoft.RedHatOpenShift/hcpOpenShiftClusters/" + testClusterName +
			"/" + api.ManagementClusterContentResourceTypeName + "/" + string(api.MaestroBundleInternalNameReadonlyHypershiftHostedCluster)))
}

// newHostedClusterContent builds a ManagementClusterContent whose KubeContent contains a single HostedCluster
// with the given Spec.ClusterID. The controller parses spec.clusterID out of this raw payload via
// unstructured.Unstructured, which requires apiVersion/kind to be present.
func newHostedClusterContent(t *testing.T, clusterID string) *api.ManagementClusterContent {
	t.Helper()
	hostedCluster := &v1beta1.HostedCluster{
		TypeMeta: metav1.TypeMeta{
			Kind:       "HostedCluster",
			APIVersion: v1beta1.GroupVersion.String(),
		},
		Spec: v1beta1.HostedClusterSpec{
			ClusterID: clusterID,
		},
	}
	raw, err := json.Marshal(hostedCluster)
	require.NoError(t, err)
	return &api.ManagementClusterContent{
		CosmosMetadata: api.CosmosMetadata{ResourceID: hostedClusterContentResourceID(t)},
		Status: api.ManagementClusterContentStatus{
			KubeContent: &metav1.List{
				Items: []kruntime.RawExtension{{Raw: raw}},
			},
		},
	}
}

// newValidHostedClusterContentLister returns a lister with a HostedCluster carrying the canonical test UUID.
// Tests that don't care about the new error paths get a working lister this way.
func newValidHostedClusterContentLister(t *testing.T) listers.ManagementClusterContentLister {
	t.Helper()
	return &listertesting.SliceManagementClusterContentLister{
		Contents: []*api.ManagementClusterContent{newHostedClusterContent(t, testClusterExternalID)},
	}
}

// errorManagementClusterContentLister always returns the configured error for every method.
type errorManagementClusterContentLister struct {
	err error
}

func (l *errorManagementClusterContentLister) List(_ context.Context) ([]*api.ManagementClusterContent, error) {
	return nil, l.err
}
func (l *errorManagementClusterContentLister) GetForCluster(_ context.Context, _, _, _, _ string) (*api.ManagementClusterContent, error) {
	return nil, l.err
}
func (l *errorManagementClusterContentLister) ListForCluster(_ context.Context, _, _, _ string) ([]*api.ManagementClusterContent, error) {
	return nil, l.err
}
func (l *errorManagementClusterContentLister) ListForNodePool(_ context.Context, _, _, _, _ string) ([]*api.ManagementClusterContent, error) {
	return nil, l.err
}

func TestNodePoolVersionSyncer_SyncOnce(t *testing.T) {
	testKey := controllerutils.HCPNodePoolKey{
		SubscriptionID:    testSubscriptionID,
		ResourceGroupName: testResourceGroupName,
		HCPClusterName:    testClusterName,
		HCPNodePoolName:   testNodePoolName,
	}

	tests := []struct {
		name                  string
		seedDB                func(t *testing.T, ctx context.Context, mockResourcesDBClient *databasetesting.MockResourcesDBClient)
		mockCS                func(t *testing.T, mockCS *ocm.MockClusterServiceClientSpec)
		contentLister         func(t *testing.T) listers.ManagementClusterContentLister
		expectedError         bool
		expectedErrorContains string
	}{
		{
			name: "nodepool not found in cosmos returns nil",
			seedDB: func(t *testing.T, ctx context.Context, mockResourcesDBClient *databasetesting.MockResourcesDBClient) {
				t.Helper()
				// Don't seed any node pool - Get will fail with not found.
			},
			mockCS: func(t *testing.T, mockCS *ocm.MockClusterServiceClientSpec) {
				t.Helper()
				// No CS mock setup needed - we won't reach CS call.
			},
			expectedError: false,
		},
		{
			name: "cluster service get nodepool call returns error",
			seedDB: func(t *testing.T, ctx context.Context, mockResourcesDBClient *databasetesting.MockResourcesDBClient) {
				t.Helper()
				createTestNodePoolWithVersion(t, ctx, mockResourcesDBClient, "4.19.15")
			},
			mockCS: func(t *testing.T, mockCS *ocm.MockClusterServiceClientSpec) {
				t.Helper()
				mockCS.EXPECT().
					GetNodePool(gomock.Any(), gomock.Any()).
					Return(nil, errors.New("cluster service unavailable")).
					Times(1)
			},
			expectedError:         true,
			expectedErrorContains: "cluster service unavailable",
		},
		{
			name: "missing version returns error",
			seedDB: func(t *testing.T, ctx context.Context, mockResourcesDBClient *databasetesting.MockResourcesDBClient) {
				t.Helper()
				createTestNodePoolWithVersion(t, ctx, mockResourcesDBClient, "4.19.15")
			},
			mockCS: func(t *testing.T, mockCS *ocm.MockClusterServiceClientSpec) {
				t.Helper()
				csNodePool := newCSNodePool(t, "") // No version
				mockCS.EXPECT().
					GetNodePool(gomock.Any(), gomock.Any()).
					Return(csNodePool, nil).
					Times(1)
			},
			expectedError:         true,
			expectedErrorContains: "node pool version not found in Cluster Service respons",
		},
		{
			name: "management cluster content not found is silently skipped",
			seedDB: func(t *testing.T, ctx context.Context, mockResourcesDBClient *databasetesting.MockResourcesDBClient) {
				t.Helper()
				createTestNodePoolWithVersion(t, ctx, mockResourcesDBClient, "4.19.15")
			},
			mockCS: func(t *testing.T, mockCS *ocm.MockClusterServiceClientSpec) {
				t.Helper()
				// No CS mock setup - we return before reaching GetNodePool.
			},
			contentLister: func(t *testing.T) listers.ManagementClusterContentLister {
				t.Helper()
				return &listertesting.SliceManagementClusterContentLister{}
			},
			expectedError: false,
		},
		{
			name: "management cluster content lister error is propagated",
			seedDB: func(t *testing.T, ctx context.Context, mockResourcesDBClient *databasetesting.MockResourcesDBClient) {
				t.Helper()
				createTestNodePoolWithVersion(t, ctx, mockResourcesDBClient, "4.19.15")
			},
			mockCS: func(t *testing.T, mockCS *ocm.MockClusterServiceClientSpec) {
				t.Helper()
			},
			contentLister: func(t *testing.T) listers.ManagementClusterContentLister {
				t.Helper()
				return &errorManagementClusterContentLister{err: errors.New("indexer is on fire")}
			},
			expectedError:         true,
			expectedErrorContains: "failed to get cluster management cluster content",
		},
		{
			name: "management cluster content with nil KubeContent is silently skipped",
			seedDB: func(t *testing.T, ctx context.Context, mockResourcesDBClient *databasetesting.MockResourcesDBClient) {
				t.Helper()
				createTestNodePoolWithVersion(t, ctx, mockResourcesDBClient, "4.19.15")
			},
			mockCS: func(t *testing.T, mockCS *ocm.MockClusterServiceClientSpec) {
				t.Helper()
			},
			contentLister: func(t *testing.T) listers.ManagementClusterContentLister {
				t.Helper()
				return &listertesting.SliceManagementClusterContentLister{
					Contents: []*api.ManagementClusterContent{{
						CosmosMetadata: api.CosmosMetadata{ResourceID: hostedClusterContentResourceID(t)},
					}},
				}
			},
			expectedError: false,
		},
		{
			name: "management cluster content with multiple kubecontent items is silently skipped",
			seedDB: func(t *testing.T, ctx context.Context, mockResourcesDBClient *databasetesting.MockResourcesDBClient) {
				t.Helper()
				createTestNodePoolWithVersion(t, ctx, mockResourcesDBClient, "4.19.15")
			},
			mockCS: func(t *testing.T, mockCS *ocm.MockClusterServiceClientSpec) {
				t.Helper()
			},
			contentLister: func(t *testing.T) listers.ManagementClusterContentLister {
				t.Helper()
				content := newHostedClusterContent(t, testClusterExternalID)
				// Duplicate the single item so the count check (!= 1) fails.
				content.Status.KubeContent.Items = append(content.Status.KubeContent.Items, content.Status.KubeContent.Items[0])
				return &listertesting.SliceManagementClusterContentLister{
					Contents: []*api.ManagementClusterContent{content},
				}
			},
			expectedError: false,
		},
		{
			name: "kubecontent unmarshal failure is returned as error",
			seedDB: func(t *testing.T, ctx context.Context, mockResourcesDBClient *databasetesting.MockResourcesDBClient) {
				t.Helper()
				createTestNodePoolWithVersion(t, ctx, mockResourcesDBClient, "4.19.15")
			},
			mockCS: func(t *testing.T, mockCS *ocm.MockClusterServiceClientSpec) {
				t.Helper()
			},
			contentLister: func(t *testing.T) listers.ManagementClusterContentLister {
				t.Helper()
				return &listertesting.SliceManagementClusterContentLister{
					Contents: []*api.ManagementClusterContent{{
						CosmosMetadata: api.CosmosMetadata{ResourceID: hostedClusterContentResourceID(t)},
						Status: api.ManagementClusterContentStatus{
							KubeContent: &metav1.List{
								Items: []kruntime.RawExtension{{Raw: []byte("not-json")}},
							},
						},
					}},
				}
			},
			expectedError:         true,
			expectedErrorContains: "failed to unmarshal kubecontent",
		},
		{
			name: "kubecontent without HostedCluster.Spec.ClusterID is silently skipped",
			seedDB: func(t *testing.T, ctx context.Context, mockResourcesDBClient *databasetesting.MockResourcesDBClient) {
				t.Helper()
				createTestNodePoolWithVersion(t, ctx, mockResourcesDBClient, "4.19.15")
			},
			mockCS: func(t *testing.T, mockCS *ocm.MockClusterServiceClientSpec) {
				t.Helper()
			},
			contentLister: func(t *testing.T) listers.ManagementClusterContentLister {
				t.Helper()
				return &listertesting.SliceManagementClusterContentLister{
					Contents: []*api.ManagementClusterContent{newHostedClusterContent(t, "")},
				}
			},
			expectedError: false,
		},
		{
			name: "invalid HostedCluster.Spec.ClusterID is returned as error",
			seedDB: func(t *testing.T, ctx context.Context, mockResourcesDBClient *databasetesting.MockResourcesDBClient) {
				t.Helper()
				createTestNodePoolWithVersion(t, ctx, mockResourcesDBClient, "4.19.15")
			},
			mockCS: func(t *testing.T, mockCS *ocm.MockClusterServiceClientSpec) {
				t.Helper()
			},
			contentLister: func(t *testing.T) listers.ManagementClusterContentLister {
				t.Helper()
				return &listertesting.SliceManagementClusterContentLister{
					Contents: []*api.ManagementClusterContent{newHostedClusterContent(t, "not-a-uuid")},
				}
			},
			expectedError:         true,
			expectedErrorContains: "failed to parse cluster UUID",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx := context.Background()
			ctrl := gomock.NewController(t)

			mockResourcesDBClient := databasetesting.NewMockResourcesDBClient()
			mockCS := ocm.NewMockClusterServiceClientSpec(ctrl)
			mockClientCache := cincinnati.NewMockClientCache(ctrl)
			mockClientCache.EXPECT().GetOrCreateClient(gomock.Any()).Return(cincinnati.NewMockClient(ctrl)).AnyTimes()

			tt.seedDB(t, ctx, mockResourcesDBClient)
			tt.mockCS(t, mockCS)

			contentLister := newValidHostedClusterContentLister(t)
			if tt.contentLister != nil {
				contentLister = tt.contentLister(t)
			}

			syncer := &nodePoolVersionSyncer{
				cooldownChecker:                       &alwaysSyncCooldownChecker{},
				clusterManagementClusterContentLister: contentLister,
				resourcesDBClient:                     mockResourcesDBClient,
				clusterServiceClient:                  mockCS,
				cincinnatiClientCache:                 mockClientCache,
			}

			ctx = utils.ContextWithLogger(ctx, logr.Discard())

			err := syncer.SyncOnce(ctx, testKey)

			assertSyncResult(t, err, tt.expectedError, tt.expectedErrorContains)
		})
	}
}

// assertSyncResult is a helper function that validates the result of SyncOnce
func assertSyncResult(t *testing.T, err error, expectedError bool, expectedErrorContains string) {
	t.Helper()
	if expectedError {
		assert.Error(t, err)
		assert.NotEmpty(t, err, expectedErrorContains)
	} else {
		assert.NoError(t, err)
	}
}

func TestNodePoolVersionSyncer_ValidateDesiredNodePoolVersion(t *testing.T) {
	tests := []struct {
		name                 string
		desiredVersion       string
		activeVersions       []string
		controlPlaneVersions []string
		allowMajorUpgrades   bool
		expectError          bool
		errorContains        string
	}{
		// Control plane constraint tests
		{
			name:                 "desired equals control plane - pass",
			desiredVersion:       "4.19.10",
			activeVersions:       []string{"4.19.5"},
			controlPlaneVersions: []string{"4.19.10"},
			expectError:          false,
		},
		{
			name:                 "desired less than control plane - pass",
			desiredVersion:       "4.19.5",
			activeVersions:       []string{"4.19.3"},
			controlPlaneVersions: []string{"4.19.10"},
			expectError:          false,
		},
		{
			name:                 "desired greater than control plane - fail",
			desiredVersion:       "4.20.5",
			activeVersions:       []string{"4.19.10"},
			controlPlaneVersions: []string{"4.19.10"},
			expectError:          true,
			errorContains:        "cannot exceed control plane version",
		},
		{
			name:                 "desired same minor higher patch than control plane - fail",
			desiredVersion:       "4.19.15",
			activeVersions:       []string{"4.19.5"},
			controlPlaneVersions: []string{"4.19.10"},
			expectError:          true,
			errorContains:        "cannot exceed control plane version",
		},
		// Minor version skipping tests
		{
			name:                 "z-stream upgrade - pass",
			desiredVersion:       "4.19.10",
			activeVersions:       []string{"4.19.5"},
			controlPlaneVersions: []string{"4.19.10"},
			expectError:          false,
		},
		{
			name:                 "y-stream upgrade (+1) - pass",
			desiredVersion:       "4.20.5",
			activeVersions:       []string{"4.19.10"},
			controlPlaneVersions: []string{"4.20.5"},
			expectError:          false,
		},
		{
			name:                 "skip minor version (+2) - fail",
			desiredVersion:       "4.20.5",
			activeVersions:       []string{"4.18.10"},
			controlPlaneVersions: []string{"4.20.5"},
			expectError:          true,
			errorContains:        "skipping minor versions is not allowed",
		},
		{
			name:                 "major version change - fail by default",
			desiredVersion:       "5.0.0",
			activeVersions:       []string{"4.22.0"},
			controlPlaneVersions: []string{"5.0.0"},
			expectError:          true,
			errorContains:        "major version changes are not supported",
		},
		{
			name:                 "valid major upgrade 4.22 to 5.0",
			desiredVersion:       "5.0.0",
			activeVersions:       []string{"4.22.0"},
			controlPlaneVersions: []string{"5.0.0"},
			allowMajorUpgrades:   true,
			expectError:          false,
		},
		{
			name:                 "valid major upgrade 4.23 to 5.1",
			desiredVersion:       "5.1.0",
			activeVersions:       []string{"4.23.0"},
			controlPlaneVersions: []string{"5.1.0"},
			allowMajorUpgrades:   true,
			expectError:          false,
		},
		{
			name:                 "invalid major upgrade 4.22 to 5.1",
			desiredVersion:       "5.1.0",
			activeVersions:       []string{"4.22.0"},
			controlPlaneVersions: []string{"5.1.0"},
			allowMajorUpgrades:   true,
			expectError:          true,
			errorContains:        "4.22 can only upgrade to 5.0",
		},
		{
			name:                 "invalid major upgrade 4.23 to 5.0",
			desiredVersion:       "5.0.0",
			activeVersions:       []string{"4.23.0"},
			controlPlaneVersions: []string{"5.0.0"},
			allowMajorUpgrades:   true,
			expectError:          true,
			errorContains:        "4.23 can only upgrade to 5.1",
		},
		{
			name:                 "invalid major upgrade 4.20 not supported",
			desiredVersion:       "5.0.0",
			activeVersions:       []string{"4.20.0"},
			controlPlaneVersions: []string{"5.0.0"},
			allowMajorUpgrades:   true,
			expectError:          true,
			errorContains:        "major version upgrades are not supported",
		},
		// Downgrade tests
		{
			name:                 "desired equals highest active - pass",
			desiredVersion:       "4.19.10",
			activeVersions:       []string{"4.19.10"},
			controlPlaneVersions: []string{"4.19.10"},
			expectError:          false, // Short-circuits as version is already active
		},
		{
			name:                 "desired greater than highest active - pass",
			desiredVersion:       "4.19.15",
			activeVersions:       []string{"4.19.10"},
			controlPlaneVersions: []string{"4.19.15"},
			expectError:          false,
		},
		{
			name:                 "desired less than highest active (partial downgrade) - fail",
			desiredVersion:       "4.19.8",
			activeVersions:       []string{"4.19.10"},
			controlPlaneVersions: []string{"4.19.10"},
			expectError:          true,
			errorContains:        "cannot downgrade",
		},
		{
			name:                 "desired lower minor than highest active - fail",
			desiredVersion:       "4.18.15",
			activeVersions:       []string{"4.19.10"},
			controlPlaneVersions: []string{"4.19.10"},
			expectError:          true,
			errorContains:        "cannot downgrade",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx := context.Background()
			ctx = utils.ContextWithLogger(ctx, logr.Discard())

			desiredVersion := semver.MustParse(tt.desiredVersion)

			// Build ServiceProviderNodePool with active versions
			var nodePoolActiveVersions []api.HCPNodePoolActiveVersion
			for _, v := range tt.activeVersions {
				version := semver.MustParse(v)
				nodePoolActiveVersions = append(nodePoolActiveVersions, api.HCPNodePoolActiveVersion{Version: &version})
			}
			spNodePool := &api.ServiceProviderNodePool{
				Status: api.ServiceProviderNodePoolStatus{
					NodePoolVersion: api.ServiceProviderNodePoolStatusVersion{
						ActiveVersions: nodePoolActiveVersions,
					},
				},
			}

			// Build ServiceProviderCluster with control plane versions
			var cpActiveVersions []api.HCPClusterActiveVersion
			for _, v := range tt.controlPlaneVersions {
				version := semver.MustParse(v)
				cpActiveVersions = append(cpActiveVersions, api.HCPClusterActiveVersion{Version: &version, State: configv1.CompletedUpdate})
			}
			spCluster := &api.ServiceProviderCluster{
				Status: api.ServiceProviderClusterStatus{
					ControlPlaneVersion: api.ServiceProviderClusterStatusVersion{
						ActiveVersions: cpActiveVersions,
					},
				},
			}

			ctrl := gomock.NewController(t)

			// Create a mock Cincinnati client that returns the desired version as available
			mockCincinnatiClient := cincinnati.NewMockClient(ctrl)
			mockCincinnatiClient.EXPECT().
				GetUpdates(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).
				Return(configv1.Release{}, []configv1.Release{{Version: tt.desiredVersion}}, nil, nil).
				AnyTimes()

			mockClientCache := cincinnati.NewMockClientCache(ctrl)
			mockClientCache.EXPECT().GetOrCreateClient(gomock.Any()).Return(mockCincinnatiClient).AnyTimes()

			syncer := &nodePoolVersionSyncer{
				cooldownChecker:       &alwaysSyncCooldownChecker{},
				cincinnatiClientCache: mockClientCache,
			}

			// Create subscription based on allowMajorUpgrades flag
			subscription := &arm.Subscription{
				Properties: &arm.SubscriptionProperties{},
			}

			if tt.allowMajorUpgrades {
				subscription.Properties.RegisteredFeatures = &[]arm.Feature{{
					Name:  ptr.To(api.FeatureExperimentalReleaseFeatures),
					State: ptr.To("Registered"),
				}}
			}

			err := syncer.validateDesiredNodePoolVersion(
				ctx,
				&desiredVersion,
				spNodePool,
				spCluster,
				subscription,
				"stable",
				[16]byte{}, // dummy UUID
			)

			if tt.expectError {
				require.Error(t, err)
				assert.Contains(t, err.Error(), tt.errorContains)
			} else {
				require.NoError(t, err)
			}
		})
	}
}

// createServiceProviderClusterWithVersion creates a ServiceProviderCluster with the given control plane version.
func createServiceProviderClusterWithVersion(t *testing.T, ctx context.Context, mockResourcesDBClient *databasetesting.MockResourcesDBClient, controlPlaneVersion string) {
	t.Helper()

	clusterResourceID := "/subscriptions/" + testSubscriptionID +
		"/resourceGroups/" + testResourceGroupName +
		"/providers/Microsoft.RedHatOpenShift/hcpOpenShiftClusters/" + testClusterName
	// ServiceProviderCluster resource ID format: {clusterResourceID}/{resourceTypeName}/{resourceName}
	spClusterResourceID := clusterResourceID + "/" + api.ServiceProviderClusterResourceTypeName + "/" + api.ServiceProviderClusterResourceName

	cpVersion := semver.MustParse(controlPlaneVersion)
	spCluster := &api.ServiceProviderCluster{
		CosmosMetadata: api.CosmosMetadata{
			ResourceID: api.Must(azcorearm.ParseResourceID(spClusterResourceID)),
		},
		Status: api.ServiceProviderClusterStatus{
			ControlPlaneVersion: api.ServiceProviderClusterStatusVersion{
				ActiveVersions: []api.HCPClusterActiveVersion{
					{Version: &cpVersion, State: configv1.CompletedUpdate},
				},
			},
		},
	}
	_, err := mockResourcesDBClient.ServiceProviderClusters(testSubscriptionID, testResourceGroupName, testClusterName).Create(ctx, spCluster, nil)
	require.NoError(t, err)
}

// createServiceProviderNodePoolWithVersion creates a ServiceProviderNodePool with the given active version.
func createServiceProviderNodePoolWithVersion(t *testing.T, ctx context.Context, mockResourcesDBClient *databasetesting.MockResourcesDBClient, activeVersion string) {
	t.Helper()

	nodePoolResourceID := "/subscriptions/" + testSubscriptionID +
		"/resourceGroups/" + testResourceGroupName +
		"/providers/Microsoft.RedHatOpenShift/hcpOpenShiftClusters/" + testClusterName +
		"/nodePools/" + testNodePoolName
	// ServiceProviderNodePool resource ID format: {nodePoolResourceID}/{resourceTypeName}/{resourceName}
	spNodePoolResourceID := nodePoolResourceID + "/" + api.ServiceProviderNodePoolResourceTypeName + "/" + api.ServiceProviderNodePoolResourceName

	version := semver.MustParse(activeVersion)
	spNodePool := &api.ServiceProviderNodePool{
		CosmosMetadata: api.CosmosMetadata{
			ResourceID: api.Must(azcorearm.ParseResourceID(spNodePoolResourceID)),
		},
		Status: api.ServiceProviderNodePoolStatus{
			NodePoolVersion: api.ServiceProviderNodePoolStatusVersion{
				ActiveVersions: []api.HCPNodePoolActiveVersion{
					{Version: &version},
				},
			},
		},
	}
	_, err := mockResourcesDBClient.ServiceProviderNodePools(testSubscriptionID, testResourceGroupName, testClusterName, testNodePoolName).Create(ctx, spNodePool, nil)
	require.NoError(t, err)
}

func newCSNodePoolWithVersion(t *testing.T, version string) *arohcpv1alpha1.NodePool {
	t.Helper()

	csNodePool, err := arohcpv1alpha1.NewNodePool().
		ID(testNodePoolName).
		Version(arohcpv1alpha1.NewVersion().RawID(version)).
		Build()
	require.NoError(t, err)
	return csNodePool
}

func TestNodePoolVersionSyncer_SyncOnce_SkipMinorVersionFails(t *testing.T) {
	ctx := context.Background()
	ctrl := gomock.NewController(t)

	mockResourcesDBClient := databasetesting.NewMockResourcesDBClient()
	mockCS := ocm.NewMockClusterServiceClientSpec(ctrl)
	mockClientCache := cincinnati.NewMockClientCache(ctrl)
	mockClientCache.EXPECT().GetOrCreateClient(gomock.Any()).Return(cincinnati.NewMockClient(ctrl)).AnyTimes()

	// Create node pool with desired version 4.20.0 (skips from 4.18.x)
	createTestNodePoolWithVersion(t, ctx, mockResourcesDBClient, "4.20.0")

	// Create ServiceProviderCluster with control plane at 4.20.0 (allowing the desired version)
	createServiceProviderClusterWithVersion(t, ctx, mockResourcesDBClient, "4.20.0")

	// Create ServiceProviderNodePool with active version 4.18.10 (to create skew)
	createServiceProviderNodePoolWithVersion(t, ctx, mockResourcesDBClient, "4.18.10")

	// CS returns node pool with current version 4.18.10
	csNodePool := newCSNodePoolWithVersion(t, "4.18.10")
	mockCS.EXPECT().
		GetNodePool(gomock.Any(), gomock.Any()).
		Return(csNodePool, nil).
		Times(1)

	syncer := &nodePoolVersionSyncer{
		cooldownChecker:                       &alwaysSyncCooldownChecker{},
		clusterManagementClusterContentLister: newValidHostedClusterContentLister(t),
		resourcesDBClient:                     mockResourcesDBClient,
		clusterServiceClient:                  mockCS,
		cincinnatiClientCache:                 mockClientCache,
	}

	testKey := controllerutils.HCPNodePoolKey{
		SubscriptionID:    testSubscriptionID,
		ResourceGroupName: testResourceGroupName,
		HCPClusterName:    testClusterName,
		HCPNodePoolName:   testNodePoolName,
	}

	ctx = utils.ContextWithLogger(ctx, logr.Discard())
	err := syncer.SyncOnce(ctx, testKey)

	require.Error(t, err)
	assert.Contains(t, err.Error(), "skipping minor versions is not allowed")
}

func TestNodePoolVersionSyncer_SyncOnce_DesiredExceedsControlPlaneFails(t *testing.T) {
	ctx := context.Background()
	ctrl := gomock.NewController(t)

	mockResourcesDBClient := databasetesting.NewMockResourcesDBClient()
	mockCS := ocm.NewMockClusterServiceClientSpec(ctrl)
	mockClientCache := cincinnati.NewMockClientCache(ctrl)
	mockClientCache.EXPECT().GetOrCreateClient(gomock.Any()).Return(cincinnati.NewMockClient(ctrl)).AnyTimes()

	// Create node pool with desired version 4.19.15 (exceeds control plane 4.19.10)
	createTestNodePoolWithVersion(t, ctx, mockResourcesDBClient, "4.19.15")

	// Create ServiceProviderCluster with control plane at 4.19.10 (lower than desired)
	createServiceProviderClusterWithVersion(t, ctx, mockResourcesDBClient, "4.19.10")

	// Create ServiceProviderNodePool with active version 4.19.5 (so desired is not already active)
	createServiceProviderNodePoolWithVersion(t, ctx, mockResourcesDBClient, "4.19.5")

	// CS returns node pool with current version 4.19.5
	csNodePool := newCSNodePoolWithVersion(t, "4.19.5")
	mockCS.EXPECT().
		GetNodePool(gomock.Any(), gomock.Any()).
		Return(csNodePool, nil).
		Times(1)

	syncer := &nodePoolVersionSyncer{
		cooldownChecker:                       &alwaysSyncCooldownChecker{},
		clusterManagementClusterContentLister: newValidHostedClusterContentLister(t),
		resourcesDBClient:                     mockResourcesDBClient,
		clusterServiceClient:                  mockCS,
		cincinnatiClientCache:                 mockClientCache,
	}

	testKey := controllerutils.HCPNodePoolKey{
		SubscriptionID:    testSubscriptionID,
		ResourceGroupName: testResourceGroupName,
		HCPClusterName:    testClusterName,
		HCPNodePoolName:   testNodePoolName,
	}

	ctx = utils.ContextWithLogger(ctx, logr.Discard())
	err := syncer.SyncOnce(ctx, testKey)

	require.Error(t, err)
	assert.Contains(t, err.Error(), "cannot exceed control plane version")
}

func TestNodePoolVersionSyncer_SyncOnce_NoUpgradePathInCincinnatiFails(t *testing.T) {
	ctx := context.Background()
	ctrl := gomock.NewController(t)

	mockResourcesDBClient := databasetesting.NewMockResourcesDBClient()
	mockCS := ocm.NewMockClusterServiceClientSpec(ctrl)
	mockCincinnati := cincinnati.NewMockClient(ctrl)

	// Create node pool with desired version 4.19.10
	createTestNodePoolWithVersion(t, ctx, mockResourcesDBClient, "4.19.10")

	// Create ServiceProviderCluster with control plane at 4.20.0 (allows the desired version)
	createServiceProviderClusterWithVersion(t, ctx, mockResourcesDBClient, "4.20.0")

	// CS returns node pool with current version 4.19.7
	csNodePool := newCSNodePoolWithVersion(t, "4.19.7")
	mockCS.EXPECT().
		GetNodePool(gomock.Any(), gomock.Any()).
		Return(csNodePool, nil).
		Times(1)

	// Setup Cincinnati mock to return NO upgrade path (empty candidates)
	mockCincinnati.EXPECT().
		GetUpdates(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).
		Return(
			configv1.Release{Version: "4.19.7"},
			[]configv1.Release{}, // Empty - no upgrade path available
			nil,
			nil,
		).
		Times(1)

	mockClientCache := cincinnati.NewMockClientCache(ctrl)
	mockClientCache.EXPECT().GetOrCreateClient(gomock.Any()).Return(mockCincinnati).AnyTimes()

	syncer := &nodePoolVersionSyncer{
		cooldownChecker:                       &alwaysSyncCooldownChecker{},
		clusterManagementClusterContentLister: newValidHostedClusterContentLister(t),
		resourcesDBClient:                     mockResourcesDBClient,
		clusterServiceClient:                  mockCS,
		cincinnatiClientCache:                 mockClientCache,
	}

	testKey := controllerutils.HCPNodePoolKey{
		SubscriptionID:    testSubscriptionID,
		ResourceGroupName: testResourceGroupName,
		HCPClusterName:    testClusterName,
		HCPNodePoolName:   testNodePoolName,
	}

	ctx = utils.ContextWithLogger(ctx, logr.Discard())
	err := syncer.SyncOnce(ctx, testKey)

	require.Error(t, err)
	assert.Contains(t, err.Error(), "no upgrade path available")
}

func TestNodePoolVersionSyncer_SyncOnce_DowngradeFails(t *testing.T) {
	ctx := context.Background()
	ctrl := gomock.NewController(t)

	mockResourcesDBClient := databasetesting.NewMockResourcesDBClient()
	mockCS := ocm.NewMockClusterServiceClientSpec(ctrl)
	mockClientCache := cincinnati.NewMockClientCache(ctrl)
	mockClientCache.EXPECT().GetOrCreateClient(gomock.Any()).Return(cincinnati.NewMockClient(ctrl)).AnyTimes()

	// Create node pool with desired version 4.19.5 (downgrade from 4.19.10)
	createTestNodePoolWithVersion(t, ctx, mockResourcesDBClient, "4.19.5")

	// Create ServiceProviderCluster with control plane at 4.20.0
	createServiceProviderClusterWithVersion(t, ctx, mockResourcesDBClient, "4.20.0")

	// Create ServiceProviderNodePool with active version 4.19.10 (higher than desired)
	createServiceProviderNodePoolWithVersion(t, ctx, mockResourcesDBClient, "4.19.10")

	// CS returns node pool with current version 4.19.10
	csNodePool := newCSNodePoolWithVersion(t, "4.19.10")
	mockCS.EXPECT().
		GetNodePool(gomock.Any(), gomock.Any()).
		Return(csNodePool, nil).
		Times(1)

	syncer := &nodePoolVersionSyncer{
		cooldownChecker:                       &alwaysSyncCooldownChecker{},
		clusterManagementClusterContentLister: newValidHostedClusterContentLister(t),
		resourcesDBClient:                     mockResourcesDBClient,
		clusterServiceClient:                  mockCS,
		cincinnatiClientCache:                 mockClientCache,
	}

	testKey := controllerutils.HCPNodePoolKey{
		SubscriptionID:    testSubscriptionID,
		ResourceGroupName: testResourceGroupName,
		HCPClusterName:    testClusterName,
		HCPNodePoolName:   testNodePoolName,
	}

	ctx = utils.ContextWithLogger(ctx, logr.Discard())
	err := syncer.SyncOnce(ctx, testKey)

	require.Error(t, err)
	assert.Contains(t, err.Error(), "cannot downgrade")
}

func TestNodePoolVersionSyncer_SyncOnce_UpgradePathExistsSucceeds(t *testing.T) {
	ctx := context.Background()
	ctrl := gomock.NewController(t)

	mockResourcesDBClient := databasetesting.NewMockResourcesDBClient()
	mockCS := ocm.NewMockClusterServiceClientSpec(ctrl)
	mockCincinnati := cincinnati.NewMockClient(ctrl)

	// Create node pool with desired version 4.19.15 (valid upgrade from 4.19.10)
	createTestNodePoolWithVersion(t, ctx, mockResourcesDBClient, "4.19.15")

	// Create ServiceProviderCluster with control plane at 4.20.0
	createServiceProviderClusterWithVersion(t, ctx, mockResourcesDBClient, "4.20.0")

	// CS returns node pool with current version 4.19.10
	csNodePool := newCSNodePoolWithVersion(t, "4.19.10")
	mockCS.EXPECT().
		GetNodePool(gomock.Any(), gomock.Any()).
		Return(csNodePool, nil).
		Times(1)

	// Setup Cincinnati mock to return valid upgrade path including desired version
	mockCincinnati.EXPECT().
		GetUpdates(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).
		Return(
			configv1.Release{Version: "4.19.10"},
			[]configv1.Release{
				{Version: "4.19.12"},
				{Version: "4.19.15"}, // Desired version is in candidates
				{Version: "4.19.18"},
			},
			nil,
			nil,
		).
		Times(1)

	mockClientCache := cincinnati.NewMockClientCache(ctrl)
	mockClientCache.EXPECT().GetOrCreateClient(gomock.Any()).Return(mockCincinnati).AnyTimes()

	syncer := &nodePoolVersionSyncer{
		cooldownChecker:                       &alwaysSyncCooldownChecker{},
		clusterManagementClusterContentLister: newValidHostedClusterContentLister(t),
		resourcesDBClient:                     mockResourcesDBClient,
		clusterServiceClient:                  mockCS,
		cincinnatiClientCache:                 mockClientCache,
	}

	testKey := controllerutils.HCPNodePoolKey{
		SubscriptionID:    testSubscriptionID,
		ResourceGroupName: testResourceGroupName,
		HCPClusterName:    testClusterName,
		HCPNodePoolName:   testNodePoolName,
	}

	ctx = utils.ContextWithLogger(ctx, logr.Discard())
	err := syncer.SyncOnce(ctx, testKey)
	require.NoError(t, err)

	// Verify the ServiceProviderNodePool was updated correctly
	spnp, err := mockResourcesDBClient.ServiceProviderNodePools(
		testSubscriptionID, testResourceGroupName, testClusterName, testNodePoolName,
	).Get(ctx, api.ServiceProviderNodePoolResourceName)
	require.NoError(t, err)

	// Verify DesiredVersion was persisted
	require.NotNil(t, spnp.Spec.NodePoolVersion.DesiredVersion)
	expectedDesiredVersion := semver.MustParse("4.19.15")
	require.True(t, expectedDesiredVersion.EQ(*spnp.Spec.NodePoolVersion.DesiredVersion))
}

func TestNodePoolVersionSyncer_SyncOnce_DesiredVersionUnchangedOnFailure_ChangedOnSuccess(t *testing.T) {
	ctx := context.Background()
	ctrl := gomock.NewController(t)

	mockResourcesDBClient := databasetesting.NewMockResourcesDBClient()
	mockCS := ocm.NewMockClusterServiceClientSpec(ctrl)

	// Seed the database with a node pool
	createTestNodePoolWithVersion(t, ctx, mockResourcesDBClient, "4.19.15")

	// Setup CS mock to return a node pool with version
	csNodePool := newCSNodePool(t, "4.19.10")
	mockCS.EXPECT().
		GetNodePool(gomock.Any(), gomock.Any()).
		Return(csNodePool, nil).
		Times(1)

	// Phase 1: Cincinnati mock returns valid upgrade path
	mockCincinnati := cincinnati.NewMockClient(ctrl)
	mockCincinnati.EXPECT().
		GetUpdates(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).
		Return(
			configv1.Release{Version: "4.19.10"},
			[]configv1.Release{{Version: "4.19.15"}},
			nil,
			nil,
		).
		AnyTimes()

	mockClientCache := cincinnati.NewMockClientCache(ctrl)
	mockClientCache.EXPECT().GetOrCreateClient(gomock.Any()).Return(mockCincinnati).Times(1)

	syncer := &nodePoolVersionSyncer{
		cooldownChecker:                       &alwaysSyncCooldownChecker{},
		clusterManagementClusterContentLister: newValidHostedClusterContentLister(t),
		resourcesDBClient:                     mockResourcesDBClient,
		clusterServiceClient:                  mockCS,
		cincinnatiClientCache:                 mockClientCache,
	}

	testKey := controllerutils.HCPNodePoolKey{
		SubscriptionID:    testSubscriptionID,
		ResourceGroupName: testResourceGroupName,
		HCPClusterName:    testClusterName,
		HCPNodePoolName:   testNodePoolName,
	}

	ctx = utils.ContextWithLogger(ctx, logr.Discard())
	err := syncer.SyncOnce(ctx, testKey)
	require.NoError(t, err)

	// Verify the ServiceProviderNodePool was created with correct versions
	spnp, err := mockResourcesDBClient.ServiceProviderNodePools(
		testSubscriptionID, testResourceGroupName, testClusterName, testNodePoolName,
	).Get(ctx, api.ServiceProviderNodePoolResourceName)
	require.NoError(t, err, "ServiceProviderNodePool should exist after sync")

	expectedActiveVersion := semver.MustParse("4.19.10")
	expectedDesiredVersion := semver.MustParse("4.19.15")

	// Verify ActiveVersion was persisted (from Cluster Service)
	require.NotNil(t, spnp.Status.NodePoolVersion.ActiveVersions,
		"ActiveVersion should be set")
	require.True(t, expectedActiveVersion.EQ(*spnp.Status.NodePoolVersion.ActiveVersions[0].Version),
		"ActiveVersion should match CS version %s, got %s", "4.19.10", spnp.Status.NodePoolVersion.ActiveVersions[0].Version)

	// Verify DesiredVersion was persisted (from customer's HCPNodePool)
	require.NotNil(t, spnp.Spec.NodePoolVersion.DesiredVersion,
		"DesiredVersion should be set")
	require.True(t, expectedDesiredVersion.EQ(*spnp.Spec.NodePoolVersion.DesiredVersion),
		"DesiredVersion should match customer version %s, got %s", "4.19.15", spnp.Spec.NodePoolVersion.DesiredVersion)

	// --- Phase 2: Change version, Cincinnati fails, desired should NOT change ---

	// Update the HCPNodePool with a new desired version
	nodePool, err := mockResourcesDBClient.HCPClusters(testSubscriptionID, testResourceGroupName).
		NodePools(testClusterName).Get(ctx, testNodePoolName)
	require.NoError(t, err)
	nodePool.Properties.Version.ID = "4.19.20"
	_, err = mockResourcesDBClient.HCPClusters(testSubscriptionID, testResourceGroupName).
		NodePools(testClusterName).Replace(ctx, nodePool, nil)
	require.NoError(t, err)

	// Setup CS mocks for second sync
	mockCS.EXPECT().
		GetNodePool(gomock.Any(), gomock.Any()).
		Return(csNodePool, nil).
		Times(1)

	mockCincinnatiFailing := cincinnati.NewMockClient(ctrl)
	mockCincinnatiFailing.EXPECT().
		GetUpdates(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).
		Return(
			configv1.Release{Version: "4.19.10"},
			[]configv1.Release{}, // Empty - no upgrade path available
			nil,
			nil,
		).
		AnyTimes()
	mockClientCache.EXPECT().GetOrCreateClient(gomock.Any()).Return(mockCincinnatiFailing).Times(1)

	// SyncOnce should fail because Cincinnati doesn't have upgrade path
	err = syncer.SyncOnce(ctx, testKey)
	require.Error(t, err, "SyncOnce should fail when Cincinnati has no upgrade path")
	assert.Contains(t, err.Error(), "no upgrade path available")

	// Verify that DesiredVersion was NOT changed (still 4.19.15)
	spnp, err = mockResourcesDBClient.ServiceProviderNodePools(
		testSubscriptionID, testResourceGroupName, testClusterName, testNodePoolName,
	).Get(ctx, api.ServiceProviderNodePoolResourceName)
	require.NoError(t, err)
	require.NotNil(t, spnp.Spec.NodePoolVersion.DesiredVersion,
		"DesiredVersion should still be set")
	require.True(t, expectedDesiredVersion.EQ(*spnp.Spec.NodePoolVersion.DesiredVersion),
		"DesiredVersion should NOT have changed after failed validation, expected %s, got %s",
		"4.19.15", spnp.Spec.NodePoolVersion.DesiredVersion)

	// --- Phase 3: Cincinnati succeeds, desired should change ---

	// Setup CS mocks for third sync
	mockCS.EXPECT().
		GetNodePool(gomock.Any(), gomock.Any()).
		Return(csNodePool, nil).
		Times(1)

	mockCincinnatiSucceeding := cincinnati.NewMockClient(ctrl)
	mockCincinnatiSucceeding.EXPECT().
		GetUpdates(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).
		Return(
			configv1.Release{Version: "4.19.10"},
			[]configv1.Release{{Version: "4.19.20"}}, // Valid upgrade path
			nil,
			nil,
		).
		AnyTimes()
	mockClientCache.EXPECT().GetOrCreateClient(gomock.Any()).Return(mockCincinnatiSucceeding).Times(1)

	// SyncOnce should succeed now
	err = syncer.SyncOnce(ctx, testKey)
	require.NoError(t, err, "SyncOnce should succeed when Cincinnati has valid upgrade path")

	// Verify that DesiredVersion HAS changed to 4.19.20
	spnp, err = mockResourcesDBClient.ServiceProviderNodePools(
		testSubscriptionID, testResourceGroupName, testClusterName, testNodePoolName,
	).Get(ctx, api.ServiceProviderNodePoolResourceName)
	require.NoError(t, err)
	expectedNewDesiredVersion := semver.MustParse("4.19.20")
	require.NotNil(t, spnp.Spec.NodePoolVersion.DesiredVersion,
		"DesiredVersion should be set")
	require.True(t, expectedNewDesiredVersion.EQ(*spnp.Spec.NodePoolVersion.DesiredVersion),
		"DesiredVersion should have changed after successful validation, expected %s, got %s",
		"4.19.20", spnp.Spec.NodePoolVersion.DesiredVersion)
}
