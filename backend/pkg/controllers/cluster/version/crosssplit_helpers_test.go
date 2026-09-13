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
	"strings"
	"testing"

	"github.com/blang/semver/v4"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	azcorearm "github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"

	configv1 "github.com/openshift/api/config/v1"

	"github.com/Azure/ARO-HCP/backend/pkg/kubeapplierhelpers"
	"github.com/Azure/ARO-HCP/internal/api/coreapi"
	"github.com/Azure/ARO-HCP/internal/api/kubeapplierapi"
	"github.com/Azure/ARO-HCP/internal/api/metadataapi"
	"github.com/Azure/ARO-HCP/internal/database/cosmosstorage/cosmosstorageutils"
	"github.com/Azure/ARO-HCP/internal/database/cosmosstoragetesting/corecosmosstoragetesting"
	"github.com/Azure/ARO-HCP/internal/database/listers/corelisters"
)

const (
	testSubscriptionID    = "00000000-0000-0000-0000-000000000001"
	testResourceGroupName = "test-rg"
	testClusterName       = "test-cluster"
	testCSClusterIDStr    = "/api/aro_hcp/v1alpha1/clusters/" + testClusterName
)

// hostedClusterReadDesireResourceID returns the resource ID for the readonly
// HostedCluster ReadDesire associated with the test cluster.
func hostedClusterReadDesireResourceID(t *testing.T) *azcorearm.ResourceID {
	t.Helper()
	return metadataapi.Must(azcorearm.ParseResourceID(
		kubeapplierapi.ToClusterScopedReadDesireResourceIDString(
			testSubscriptionID, testResourceGroupName, testClusterName, kubeapplierhelpers.ReadDesireNameReadonlyHostedCluster)))
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
	spClusterResourceID := clusterResourceID + "/" + coreapi.ServiceProviderClusterResourceTypeName + "/" + coreapi.ServiceProviderClusterResourceName

	cpVersion := semver.MustParse(controlPlaneVersion)
	spcCRUD := mockResourcesDBClient.ServiceProviderClusters(testSubscriptionID, testResourceGroupName, testClusterName)

	existing, getErr := spcCRUD.Get(ctx, coreapi.ServiceProviderClusterResourceName)
	if getErr == nil {
		replacement := existing.DeepCopy()
		replacement.Status.ControlPlaneVersion.ActiveVersions = []coreapi.HCPClusterActiveVersion{
			{Version: &cpVersion, State: configv1.CompletedUpdate},
		}
		_, err := spcCRUD.Replace(ctx, replacement, nil)
		require.NoError(t, err)
		return
	}
	require.True(t, cosmosstorageutils.IsNotFoundError(getErr), "unexpected error reading SPC before seeding: %v", getErr)

	spCluster := &coreapi.ServiceProviderCluster{
		CosmosMetadata: coreapi.CosmosMetadata{
			ResourceID:   metadataapi.Must(azcorearm.ParseResourceID(spClusterResourceID)),
			PartitionKey: strings.ToLower(testSubscriptionID),
		},
		Status: coreapi.ServiceProviderClusterStatus{
			ControlPlaneVersion: coreapi.ServiceProviderClusterStatusVersion{
				ActiveVersions: []coreapi.HCPClusterActiveVersion{
					{Version: &cpVersion, State: configv1.CompletedUpdate},
				},
			},
		},
	}
	_, err := spcCRUD.Create(ctx, spCluster, nil)
	require.NoError(t, err)
}

// boomActiveOperationLister is a test double that returns the configured
// error from ListActiveOperationsForCluster. It exists so gating helpers can
// exercise their error-propagation branch without a misbehaving mock DB.
type boomActiveOperationLister struct {
	corelisters.ActiveOperationLister
	err error
}

func (b *boomActiveOperationLister) Get(_ context.Context, _, _ string) (*coreapi.Operation, error) {
	return nil, b.err
}

func (b *boomActiveOperationLister) ListActiveOperationsForCluster(_ context.Context, _, _, _ string) ([]*coreapi.Operation, error) {
	return nil, b.err
}

// seedClusterCreateOperation seeds an active Create operation rooted at the
// given ExternalID into the mock DB so the DB-backed active operation lister
// can find it.
func seedClusterCreateOperation(t *testing.T, ctx context.Context, mockDB *corecosmosstoragetesting.MockResourcesDBClient, externalID *azcorearm.ResourceID, opName string) {
	t.Helper()
	opResourceID := metadataapi.Must(azcorearm.ParseResourceID(coreapi.ToOperationResourceIDString(externalID.SubscriptionID, opName)))
	operationID := metadataapi.Must(azcorearm.ParseResourceID(
		"/subscriptions/" + externalID.SubscriptionID +
			"/providers/Microsoft.RedHatOpenShift/locations/eastus/hcpOperationStatuses/" + opName,
	))
	op := &coreapi.Operation{
		CosmosMetadata: coreapi.CosmosMetadata{
			ResourceID:   opResourceID,
			PartitionKey: strings.ToLower(externalID.SubscriptionID),
		},
		Status:      coreapi.ProvisioningStateAccepted,
		Request:     cosmosstorageutils.OperationRequestCreate,
		ExternalID:  externalID,
		OperationID: operationID,
	}
	_, err := mockDB.Operations(externalID.SubscriptionID).Create(ctx, op, nil)
	require.NoError(t, err)
}
