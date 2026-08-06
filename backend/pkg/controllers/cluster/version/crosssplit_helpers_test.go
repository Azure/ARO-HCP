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

// Test fixtures shared with the nodepool/version package before the
// upgradecontrollers split. Kept in sync with the definitions in
// nodepool/version/nodepool_version_controller_test.go.

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/blang/semver/v4"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	kruntime "k8s.io/apimachinery/pkg/runtime"
	"k8s.io/utils/ptr"

	azcorearm "github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"

	configv1 "github.com/openshift/api/config/v1"
	"github.com/openshift/hypershift/api/hypershift/v1beta1"

	"github.com/Azure/ARO-HCP/backend/pkg/kubeapplierhelpers"
	"github.com/Azure/ARO-HCP/internal/api"
	"github.com/Azure/ARO-HCP/internal/api/arm"
	"github.com/Azure/ARO-HCP/internal/api/kubeapplier"
	"github.com/Azure/ARO-HCP/internal/database/cosmosstorage/cosmosstorageutils"
	"github.com/Azure/ARO-HCP/internal/database/cosmosstoragetesting/corecosmosstoragetesting"
	"github.com/Azure/ARO-HCP/internal/database/listers/kubeapplierlisters"
	"github.com/Azure/ARO-HCP/internal/database/listertesting/kubeapplierlistertesting"
)

const (
	testSubscriptionID    = "00000000-0000-0000-0000-000000000001"
	testResourceGroupName = "test-rg"
	testClusterName       = "test-cluster"
	testCSClusterIDStr    = "/api/aro_hcp/v1alpha1/clusters/" + testClusterName
	testClusterExternalID = "11111111-1111-1111-1111-111111111111"
)

// createTestSubscription creates a subscription in the mock database.
func createTestSubscription(t *testing.T, ctx context.Context, mockResourcesDBClient *corecosmosstoragetesting.MockResourcesDBClient) {
	t.Helper()

	subResourceID := api.Must(azcorearm.ParseResourceID("/subscriptions/" + testSubscriptionID))
	subscription := &arm.Subscription{
		CosmosMetadata: arm.CosmosMetadata{
			ResourceID:   subResourceID,
			PartitionKey: strings.ToLower(subResourceID.SubscriptionID),
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

// hostedClusterReadDesireResourceID returns the resource ID for the readonly
// HostedCluster ReadDesire associated with the test cluster.
func hostedClusterReadDesireResourceID(t *testing.T) *azcorearm.ResourceID {
	t.Helper()
	return api.Must(azcorearm.ParseResourceID(
		kubeapplier.ToClusterScopedReadDesireResourceIDString(
			testSubscriptionID, testResourceGroupName, testClusterName, kubeapplierhelpers.ReadDesireNameReadonlyHostedCluster)))
}

// newHostedClusterReadDesire builds a ReadDesire whose Status.KubeContent.Raw is
// the serialized HostedCluster carrying the given Spec.ClusterID.
func newHostedClusterReadDesire(t *testing.T, clusterID string) *kubeapplier.ReadDesire {
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
	return &kubeapplier.ReadDesire{
		CosmosMetadata: api.CosmosMetadata{ResourceID: hostedClusterReadDesireResourceID(t)},
		Status: kubeapplier.ReadDesireStatus{
			KubeContent: &kruntime.RawExtension{Raw: raw},
		},
	}
}

// newValidHostedClusterReadDesireLister returns a lister with a HostedCluster
// ReadDesire carrying the canonical test UUID.
func newValidHostedClusterReadDesireLister(t *testing.T) kubeapplierlisters.ReadDesireLister {
	t.Helper()
	return &kubeapplierlistertesting.SliceReadDesireLister{
		Desires: []*kubeapplier.ReadDesire{newHostedClusterReadDesire(t, testClusterExternalID)},
	}
}

// assertSyncResult asserts on the error returned by a SyncOnce call.
func assertSyncResult(t *testing.T, err error, expectedError bool, expectedErrorContains string) {
	t.Helper()
	if expectedError {
		assert.Error(t, err)
		assert.ErrorContains(t, err, expectedErrorContains)
	} else {
		assert.NoError(t, err)
	}
}

// createServiceProviderClusterWithVersion ensures a ServiceProviderCluster
// exists with the given control plane active version, creating or replacing as
// needed.
func createServiceProviderClusterWithVersion(t *testing.T, ctx context.Context, mockResourcesDBClient *corecosmosstoragetesting.MockResourcesDBClient, controlPlaneVersion string) {
	t.Helper()

	clusterResourceID := "/subscriptions/" + testSubscriptionID +
		"/resourceGroups/" + testResourceGroupName +
		"/providers/Microsoft.RedHatOpenShift/hcpOpenShiftClusters/" + testClusterName
	spClusterResourceID := clusterResourceID + "/" + api.ServiceProviderClusterResourceTypeName + "/" + api.ServiceProviderClusterResourceName

	cpVersion := semver.MustParse(controlPlaneVersion)
	spcCRUD := mockResourcesDBClient.ServiceProviderClusters(testSubscriptionID, testResourceGroupName, testClusterName)

	existing, getErr := spcCRUD.Get(ctx, api.ServiceProviderClusterResourceName)
	if getErr == nil {
		replacement := existing.DeepCopy()
		replacement.Status.ControlPlaneVersion.ActiveVersions = []api.HCPClusterActiveVersion{
			{Version: &cpVersion, State: configv1.CompletedUpdate},
		}
		_, err := spcCRUD.Replace(ctx, replacement, nil)
		require.NoError(t, err)
		return
	}
	require.True(t, cosmosstorageutils.IsNotFoundError(getErr), "unexpected error reading SPC before seeding: %v", getErr)

	spCluster := &api.ServiceProviderCluster{
		CosmosMetadata: api.CosmosMetadata{
			ResourceID:   api.Must(azcorearm.ParseResourceID(spClusterResourceID)),
			PartitionKey: strings.ToLower(testSubscriptionID),
		},
		Status: api.ServiceProviderClusterStatus{
			ControlPlaneVersion: api.ServiceProviderClusterStatusVersion{
				ActiveVersions: []api.HCPClusterActiveVersion{
					{Version: &cpVersion, State: configv1.CompletedUpdate},
				},
			},
		},
	}
	_, err := spcCRUD.Create(ctx, spCluster, nil)
	require.NoError(t, err)
}
