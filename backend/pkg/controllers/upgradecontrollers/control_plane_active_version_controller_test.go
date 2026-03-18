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
	"testing"

	"github.com/blang/semver/v4"
	"github.com/go-logr/logr"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/utils/ptr"

	azcorearm "github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"

	configv1 "github.com/openshift/api/config/v1"
	hsv1beta1 "github.com/openshift/hypershift/api/hypershift/v1beta1"

	"github.com/Azure/ARO-HCP/backend/pkg/controllers/controllerutils"
	"github.com/Azure/ARO-HCP/internal/api"
	"github.com/Azure/ARO-HCP/internal/api/arm"
	"github.com/Azure/ARO-HCP/internal/databasetesting"
	"github.com/Azure/ARO-HCP/internal/utils"
)

func TestControlPlaneActiveVersionSyncer_SyncOnce(t *testing.T) {
	testKey := controllerutils.HCPClusterKey{
		SubscriptionID:    testSubscriptionID,
		ResourceGroupName: testResourceGroupName,
		HCPClusterName:    testClusterName,
	}

	tests := []struct {
		name                  string
		seedDB                func(t *testing.T, ctx context.Context, mockDB *databasetesting.MockDBClient)
		expectedError         bool
		expectedErrorContains string
		validateAfter         func(t *testing.T, ctx context.Context, mockDB *databasetesting.MockDBClient)
	}{
		{
			name: "cluster not found in cosmos returns nil",
			seedDB: func(t *testing.T, ctx context.Context, mockDB *databasetesting.MockDBClient) {
				t.Helper()
				// No cluster seeded - Get will return not found.
			},
			expectedError: false,
		},
		{
			name: "no management cluster content returns nil (no error)",
			seedDB: func(t *testing.T, ctx context.Context, mockDB *databasetesting.MockDBClient) {
				t.Helper()
				createTestHCPCluster(t, ctx, mockDB)
				// No ManagementClusterContent - Get returns not found, version is nil, no update.
			},
			expectedError: false,
		},
		{
			name: "active versions unchanged when management cluster version matches current",
			seedDB: func(t *testing.T, ctx context.Context, mockDB *databasetesting.MockDBClient) {
				t.Helper()
				createTestHCPCluster(t, ctx, mockDB)
				createServiceProviderClusterWithVersion(t, ctx, mockDB, "4.19.15")
				createManagementClusterContentWithHostedClusterHistory(t, ctx, mockDB, []configv1.UpdateHistory{
					{Version: "4.19.15", State: configv1.CompletedUpdate},
				})
			},
			expectedError: false,
			validateAfter: func(t *testing.T, ctx context.Context, mockDB *databasetesting.MockDBClient) {
				t.Helper()
				spc, err := mockDB.ServiceProviderClusters(testSubscriptionID, testResourceGroupName, testClusterName).Get(ctx, api.ServiceProviderClusterResourceName)
				require.NoError(t, err)
				assert.Equal(t, []api.HCPClusterActiveVersion{
					{Version: ptr.To(semver.MustParse("4.19.15")), State: configv1.CompletedUpdate},
				}, spc.Status.ControlPlaneVersion.ActiveVersions)
			},
		},
		{
			name: "all versions from newest until last completed when multiple history entries",
			seedDB: func(t *testing.T, ctx context.Context, mockDB *databasetesting.MockDBClient) {
				t.Helper()
				createTestHCPCluster(t, ctx, mockDB)
				createManagementClusterContentWithHostedClusterHistory(t, ctx, mockDB, []configv1.UpdateHistory{
					{Version: "4.19.17", State: configv1.PartialUpdate}, {Version: "4.19.16", State: configv1.PartialUpdate}, {Version: "4.19.15", State: configv1.CompletedUpdate},
					{Version: "4.19.14", State: configv1.PartialUpdate}, {Version: "4.19.13", State: configv1.CompletedUpdate},
				})
			},
			expectedError: false,
			validateAfter: func(t *testing.T, ctx context.Context, mockDB *databasetesting.MockDBClient) {
				t.Helper()
				spc, err := mockDB.ServiceProviderClusters(testSubscriptionID, testResourceGroupName, testClusterName).Get(ctx, api.ServiceProviderClusterResourceName)
				require.NoError(t, err)
				assert.Equal(t, []api.HCPClusterActiveVersion{
					{Version: ptr.To(semver.MustParse("4.19.17")), State: configv1.PartialUpdate}, {Version: ptr.To(semver.MustParse("4.19.16")), State: configv1.PartialUpdate}, {Version: ptr.To(semver.MustParse("4.19.15")), State: configv1.CompletedUpdate},
				}, spc.Status.ControlPlaneVersion.ActiveVersions)
			},
		},
		{
			name: "one active version when version history has one element",
			seedDB: func(t *testing.T, ctx context.Context, mockDB *databasetesting.MockDBClient) {
				t.Helper()
				createTestHCPCluster(t, ctx, mockDB)
				createManagementClusterContentWithHostedClusterHistory(t, ctx, mockDB, []configv1.UpdateHistory{
					{Version: "4.19.16", State: configv1.PartialUpdate},
				})
			},
			expectedError: false,
			validateAfter: func(t *testing.T, ctx context.Context, mockDB *databasetesting.MockDBClient) {
				t.Helper()
				spc, err := mockDB.ServiceProviderClusters(testSubscriptionID, testResourceGroupName, testClusterName).Get(ctx, api.ServiceProviderClusterResourceName)
				require.NoError(t, err)
				assert.Equal(t, []api.HCPClusterActiveVersion{
					{Version: ptr.To(semver.MustParse("4.19.16")), State: configv1.PartialUpdate},
				}, spc.Status.ControlPlaneVersion.ActiveVersions)
			},
		},
		{
			name: "no active versions when version history is empty",
			seedDB: func(t *testing.T, ctx context.Context, mockDB *databasetesting.MockDBClient) {
				t.Helper()
				createTestHCPCluster(t, ctx, mockDB)
				createManagementClusterContentWithHostedClusterHistory(t, ctx, mockDB, []configv1.UpdateHistory{})
			},
			expectedError: false,
			validateAfter: func(t *testing.T, ctx context.Context, mockDB *databasetesting.MockDBClient) {
				t.Helper()
				spc, err := mockDB.ServiceProviderClusters(testSubscriptionID, testResourceGroupName, testClusterName).Get(ctx, api.ServiceProviderClusterResourceName)
				require.NoError(t, err)
				require.Empty(t, spc.Status.ControlPlaneVersion.ActiveVersions)
			},
		},
		{
			name: "history entries with empty or invalid version are skipped",
			seedDB: func(t *testing.T, ctx context.Context, mockDB *databasetesting.MockDBClient) {
				t.Helper()
				createTestHCPCluster(t, ctx, mockDB)
				createManagementClusterContentWithHostedClusterHistory(t, ctx, mockDB, []configv1.UpdateHistory{
					{Version: "", State: configv1.PartialUpdate},
					{Version: "not-a-version", State: configv1.PartialUpdate},
					{Version: "4.19.15", State: configv1.CompletedUpdate},
				})
			},
			expectedError: false,
			validateAfter: func(t *testing.T, ctx context.Context, mockDB *databasetesting.MockDBClient) {
				t.Helper()
				spc, err := mockDB.ServiceProviderClusters(testSubscriptionID, testResourceGroupName, testClusterName).Get(ctx, api.ServiceProviderClusterResourceName)
				require.NoError(t, err)
				assert.Equal(t, []api.HCPClusterActiveVersion{
					{Version: ptr.To(semver.MustParse("4.19.15")), State: configv1.CompletedUpdate},
				}, spc.Status.ControlPlaneVersion.ActiveVersions)
			},
		},
		{
			name: "nightly version in history is parsed and included",
			seedDB: func(t *testing.T, ctx context.Context, mockDB *databasetesting.MockDBClient) {
				t.Helper()
				createTestHCPCluster(t, ctx, mockDB)
				createManagementClusterContentWithHostedClusterHistory(t, ctx, mockDB, []configv1.UpdateHistory{
					{Version: "4.19.0-0.nightly-multi-2026-01-10-204154", State: configv1.CompletedUpdate},
				})
			},
			expectedError: false,
			validateAfter: func(t *testing.T, ctx context.Context, mockDB *databasetesting.MockDBClient) {
				t.Helper()
				spc, err := mockDB.ServiceProviderClusters(testSubscriptionID, testResourceGroupName, testClusterName).Get(ctx, api.ServiceProviderClusterResourceName)
				require.NoError(t, err)
				assert.Equal(t, []api.HCPClusterActiveVersion{
					{Version: ptr.To(api.Must(semver.ParseTolerant("4.19.0-0.nightly-multi-2026-01-10-204154"))), State: configv1.CompletedUpdate},
				}, spc.Status.ControlPlaneVersion.ActiveVersions)
			},
		},
		{
			name: "no HostedCluster in KubeContent returns error",
			seedDB: func(t *testing.T, ctx context.Context, mockDB *databasetesting.MockDBClient) {
				t.Helper()
				createTestHCPCluster(t, ctx, mockDB)
				cm := &unstructured.Unstructured{}
				cm.SetAPIVersion("v1")
				cm.SetKind("ConfigMap")
				cm.SetName("other")
				createManagementClusterContentWithKubeContentItems(t, ctx, mockDB, []runtime.RawExtension{{Object: cm}})
			},
			expectedError:         true,
			expectedErrorContains: "no HostedCluster found in KubeContent",
		},
		{
			name: "invalid management cluster content returns error",
			seedDB: func(t *testing.T, ctx context.Context, mockDB *databasetesting.MockDBClient) {
				t.Helper()
				createTestHCPCluster(t, ctx, mockDB)
				// Empty RawExtension cannot be parsed and triggers an error.
				createManagementClusterContentWithKubeContentItems(t, ctx, mockDB, []runtime.RawExtension{{}})
			},
			expectedError:         true,
			expectedErrorContains: "RawExtension has no Object or Raw",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			runCtx := utils.ContextWithLogger(context.Background(), logr.Discard())
			mockDB := databasetesting.NewMockDBClient()

			tt.seedDB(t, runCtx, mockDB)

			syncer := &controlPlaneActiveVersionSyncer{
				cooldownChecker: &alwaysSyncCooldownChecker{},
				cosmosClient:    mockDB,
			}

			err := syncer.SyncOnce(runCtx, testKey)

			assertSyncResult(t, err, tt.expectedError, tt.expectedErrorContains)
			if tt.expectedError && tt.expectedErrorContains != "" {
				assert.Contains(t, err.Error(), tt.expectedErrorContains)
			}

			if tt.validateAfter != nil && !tt.expectedError {
				tt.validateAfter(t, runCtx, mockDB)
			}
		})
	}
}

// createTestHCPCluster creates an HCP cluster in the mock database (no node pools).
// Used as the parent resource for control plane active version sync.
func createTestHCPCluster(t *testing.T, ctx context.Context, mockDB *databasetesting.MockDBClient) {
	t.Helper()

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
			ClusterServiceID:  clusterInternalID,
		},
	}
	_, err = mockDB.HCPClusters(testSubscriptionID, testResourceGroupName).Create(ctx, cluster, nil)
	require.NoError(t, err)
}

// createManagementClusterContentWithHostedClusterHistory creates a ManagementClusterContent with
// KubeContent containing a HostedCluster whose status.version.history is built from entries (newest first).
func createManagementClusterContentWithHostedClusterHistory(t *testing.T, ctx context.Context, mockDB *databasetesting.MockDBClient, entries []configv1.UpdateHistory) {
	t.Helper()

	hc := &hsv1beta1.HostedCluster{}
	hc.APIVersion = "hypershift.openshift.io/v1beta1"
	hc.Kind = "HostedCluster"
	hc.SetName(testClusterName)
	hc.Status.Version = &hsv1beta1.ClusterVersionStatus{History: entries}
	uObj, err := runtime.DefaultUnstructuredConverter.ToUnstructured(hc)
	require.NoError(t, err)
	u := &unstructured.Unstructured{Object: uObj}

	// First item is not a HostedCluster so the controller must skip it and find the HostedCluster by APIVersion/Kind.
	other := &unstructured.Unstructured{}
	other.SetAPIVersion("v1")
	other.SetKind("ConfigMap")
	other.SetName("other")

	createManagementClusterContentWithKubeContentItems(t, ctx, mockDB, []runtime.RawExtension{{Object: other}, {Object: u}})
}

// createManagementClusterContentWithKubeContentItems creates a ManagementClusterContent with
// the given KubeContent items. Used to test error paths (e.g. invalid JSON or empty RawExtension).
func createManagementClusterContentWithKubeContentItems(t *testing.T, ctx context.Context, mockDB *databasetesting.MockDBClient, items []runtime.RawExtension) {
	t.Helper()

	clusterRID := api.Must(azcorearm.ParseResourceID("/subscriptions/" + testSubscriptionID +
		"/resourceGroups/" + testResourceGroupName +
		"/providers/Microsoft.RedHatOpenShift/hcpOpenShiftClusters/" + testClusterName))
	managementClusterContentResourceID := api.Must(azcorearm.ParseResourceID(clusterRID.String() + "/" + api.ManagementClusterContentResourceTypeName + "/" + string(api.MaestroBundleInternalNameReadonlyHypershiftHostedCluster)))

	managementClusterContent := &api.ManagementClusterContent{
		CosmosMetadata: api.CosmosMetadata{ResourceID: managementClusterContentResourceID},
		ResourceID:     *managementClusterContentResourceID,
		Status: api.ManagementClusterContentStatus{
			KubeContent: &metav1.List{
				Items: items,
			},
		},
	}
	_, err := mockDB.ManagementClusterContents(testSubscriptionID, testResourceGroupName, testClusterName).Create(ctx, managementClusterContent, nil)
	require.NoError(t, err)
}
