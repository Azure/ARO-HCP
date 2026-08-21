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

package keys

import (
	"context"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	azcorearm "github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"

	"github.com/Azure/ARO-HCP/internal/api/coreapi"
	"github.com/Azure/ARO-HCP/internal/api/fleetapi"
	"github.com/Azure/ARO-HCP/internal/api/kubeapplierapi"
	"github.com/Azure/ARO-HCP/internal/api/metadataapi"
	"github.com/Azure/ARO-HCP/internal/database/cosmosstoragetesting/kubeappliercosmosstoragetesting"
)

const (
	testSubscription    = "00000000-0000-0000-0000-000000000001"
	testRG              = "test-rg"
	testCluster         = "test-cluster"
	testNodePool        = "test-np"
	testCredReq         = "test-cred-req"
	testRevocation      = "test-revocation"
	testDesireName      = "my-desire"
	testStampIdentifier = "eastus"
)

func TestApplyDesireKeyFromResourceID_ClusterScoped(t *testing.T) {
	idStr := kubeapplierapi.ToClusterScopedApplyDesireResourceIDString(testSubscription, testRG, testCluster, testDesireName)
	id := metadataapi.Must(azcorearm.ParseResourceID(idStr))

	key, err := ApplyDesireKeyFromResourceID(id)
	require.NoError(t, err)
	require.Equal(t, strings.ToLower(coreapi.ToClusterResourceIDString(testSubscription, testRG, testCluster)), key.ParentResourceID)
	require.Equal(t, testDesireName, key.Name)
}

func TestApplyDesireKeyFromResourceID_NodePoolScoped(t *testing.T) {
	idStr := kubeapplierapi.ToNodePoolScopedApplyDesireResourceIDString(testSubscription, testRG, testCluster, testNodePool, testDesireName)
	id := metadataapi.Must(azcorearm.ParseResourceID(idStr))

	key, err := ApplyDesireKeyFromResourceID(id)
	require.NoError(t, err)
	require.Equal(t, strings.ToLower(coreapi.ToNodePoolResourceIDString(testSubscription, testRG, testCluster, testNodePool)), key.ParentResourceID)
	require.Equal(t, testDesireName, key.Name)
}

func TestApplyDesireKeyFromResourceID_SystemAdminCredentialRequestScoped(t *testing.T) {
	idStr := kubeapplierapi.ToSystemAdminCredentialRequestScopedApplyDesireResourceIDString(testSubscription, testRG, testCluster, testCredReq, testDesireName)
	id := metadataapi.Must(azcorearm.ParseResourceID(idStr))

	key, err := ApplyDesireKeyFromResourceID(id)
	require.NoError(t, err)
	require.Equal(t, strings.ToLower(coreapi.ToSystemAdminCredentialRequestResourceIDString(testSubscription, testRG, testCluster, testCredReq)), key.ParentResourceID)
	require.Equal(t, testDesireName, key.Name)
}

func TestApplyDesireKeyFromResourceID_SystemAdminCredentialRevocationScoped(t *testing.T) {
	idStr := kubeapplierapi.ToSystemAdminCredentialRevocationScopedApplyDesireResourceIDString(testSubscription, testRG, testCluster, testRevocation, testDesireName)
	id := metadataapi.Must(azcorearm.ParseResourceID(idStr))

	key, err := ApplyDesireKeyFromResourceID(id)
	require.NoError(t, err)
	require.Equal(t, strings.ToLower(coreapi.ToSystemAdminCredentialRevocationResourceIDString(testSubscription, testRG, testCluster, testRevocation)), key.ParentResourceID)
	require.Equal(t, testDesireName, key.Name)
}

func TestApplyDesireKeyFromResourceID_ManagementClusterScoped(t *testing.T) {
	idStr := kubeapplierapi.ToManagementClusterScopedApplyDesireResourceIDString(testStampIdentifier, testDesireName)
	id := metadataapi.Must(azcorearm.ParseResourceID(idStr))

	key, err := ApplyDesireKeyFromResourceID(id)
	require.NoError(t, err)
	require.Equal(t, strings.ToLower(fleetapi.ToManagementClusterResourceIDString(testStampIdentifier)), key.ParentResourceID)
	require.Equal(t, testDesireName, key.Name)
}

func TestReadDesireKeyFromResourceID_ManagementClusterScoped(t *testing.T) {
	idStr := kubeapplierapi.ToManagementClusterScopedReadDesireResourceIDString(testStampIdentifier, testDesireName)
	id := metadataapi.Must(azcorearm.ParseResourceID(idStr))

	key, err := ReadDesireKeyFromResourceID(id)
	require.NoError(t, err)
	require.Equal(t, strings.ToLower(fleetapi.ToManagementClusterResourceIDString(testStampIdentifier)), key.ParentResourceID)
	require.Equal(t, testDesireName, key.Name)
}

func TestReadDesireKeyFromResourceID_SystemAdminCredentialRequestScoped(t *testing.T) {
	idStr := kubeapplierapi.ToSystemAdminCredentialRequestScopedReadDesireResourceIDString(testSubscription, testRG, testCluster, testCredReq, testDesireName)
	id := metadataapi.Must(azcorearm.ParseResourceID(idStr))

	key, err := ReadDesireKeyFromResourceID(id)
	require.NoError(t, err)
	require.Equal(t, strings.ToLower(coreapi.ToSystemAdminCredentialRequestResourceIDString(testSubscription, testRG, testCluster, testCredReq)), key.ParentResourceID)
	require.Equal(t, testDesireName, key.Name)
}

func TestDesireKeyFromResourceID_ErrorCases(t *testing.T) {
	t.Run("nil resource ID", func(t *testing.T) {
		_, err := ApplyDesireKeyFromResourceID(nil)
		require.Error(t, err)
	})
	t.Run("resource ID with no parent", func(t *testing.T) {
		id := &azcorearm.ResourceID{Name: "orphan"}
		_, err := ApplyDesireKeyFromResourceID(id)
		require.Error(t, err)
	})
}

func TestGetResourceID_RoundTrips(t *testing.T) {
	tests := []struct {
		name  string
		idStr string
	}{
		{
			name:  "cluster-scoped apply desire",
			idStr: kubeapplierapi.ToClusterScopedApplyDesireResourceIDString(testSubscription, testRG, testCluster, testDesireName),
		},
		{
			name:  "nodepool-scoped apply desire",
			idStr: kubeapplierapi.ToNodePoolScopedApplyDesireResourceIDString(testSubscription, testRG, testCluster, testNodePool, testDesireName),
		},
		{
			name:  "credential-request-scoped apply desire",
			idStr: kubeapplierapi.ToSystemAdminCredentialRequestScopedApplyDesireResourceIDString(testSubscription, testRG, testCluster, testCredReq, testDesireName),
		},
		{
			name:  "revocation-scoped apply desire",
			idStr: kubeapplierapi.ToSystemAdminCredentialRevocationScopedApplyDesireResourceIDString(testSubscription, testRG, testCluster, testRevocation, testDesireName),
		},
		{
			name:  "management-cluster-scoped apply desire",
			idStr: kubeapplierapi.ToManagementClusterScopedApplyDesireResourceIDString(testStampIdentifier, testDesireName),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			id := metadataapi.Must(azcorearm.ParseResourceID(tt.idStr))
			key, err := ApplyDesireKeyFromResourceID(id)
			require.NoError(t, err)

			roundTripped := key.GetResourceID()
			require.Equal(t, strings.ToLower(tt.idStr), strings.ToLower(roundTripped.String()), "GetResourceID should round-trip to the original ID string")
		})
	}
}

func TestGetResourceID_ReadDesireRoundTrips(t *testing.T) {
	tests := []struct {
		name  string
		idStr string
	}{
		{
			name:  "cluster-scoped read desire",
			idStr: kubeapplierapi.ToClusterScopedReadDesireResourceIDString(testSubscription, testRG, testCluster, testDesireName),
		},
		{
			name:  "nodepool-scoped read desire",
			idStr: kubeapplierapi.ToNodePoolScopedReadDesireResourceIDString(testSubscription, testRG, testCluster, testNodePool, testDesireName),
		},
		{
			name:  "credential-request-scoped read desire",
			idStr: kubeapplierapi.ToSystemAdminCredentialRequestScopedReadDesireResourceIDString(testSubscription, testRG, testCluster, testCredReq, testDesireName),
		},
		{
			name:  "revocation-scoped read desire",
			idStr: kubeapplierapi.ToSystemAdminCredentialRevocationScopedReadDesireResourceIDString(testSubscription, testRG, testCluster, testRevocation, testDesireName),
		},
		{
			name:  "management-cluster-scoped read desire",
			idStr: kubeapplierapi.ToManagementClusterScopedReadDesireResourceIDString(testStampIdentifier, testDesireName),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			id := metadataapi.Must(azcorearm.ParseResourceID(tt.idStr))
			key, err := ReadDesireKeyFromResourceID(id)
			require.NoError(t, err)

			roundTripped := key.GetResourceID()
			require.Equal(t, strings.ToLower(tt.idStr), strings.ToLower(roundTripped.String()), "GetResourceID should round-trip to the original ID string")
		})
	}
}

func TestApplyDesireKey_CRUD_DispatchesCorrectly(t *testing.T) {
	ctx := context.Background()
	mockDB := kubeappliercosmosstoragetesting.NewMockKubeApplierDBClient()

	tests := []struct {
		name  string
		idStr string
	}{
		{
			name:  "cluster-scoped",
			idStr: kubeapplierapi.ToClusterScopedApplyDesireResourceIDString(testSubscription, testRG, testCluster, testDesireName),
		},
		{
			name:  "nodepool-scoped",
			idStr: kubeapplierapi.ToNodePoolScopedApplyDesireResourceIDString(testSubscription, testRG, testCluster, testNodePool, testDesireName),
		},
		{
			name:  "credential-request-scoped",
			idStr: kubeapplierapi.ToSystemAdminCredentialRequestScopedApplyDesireResourceIDString(testSubscription, testRG, testCluster, testCredReq, testDesireName),
		},
		{
			name:  "revocation-scoped",
			idStr: kubeapplierapi.ToSystemAdminCredentialRevocationScopedApplyDesireResourceIDString(testSubscription, testRG, testCluster, testRevocation, testDesireName),
		},
		{
			name:  "management-cluster-scoped",
			idStr: kubeapplierapi.ToManagementClusterScopedApplyDesireResourceIDString(testStampIdentifier, testDesireName),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			id := metadataapi.Must(azcorearm.ParseResourceID(tt.idStr))
			key, err := ApplyDesireKeyFromResourceID(id)
			require.NoError(t, err)

			crud, err := key.CRUD(mockDB)
			require.NoError(t, err, "CRUD dispatch should succeed")
			require.NotNil(t, crud, "CRUD should return a non-nil value")

			desire := &kubeapplierapi.ApplyDesire{}
			desire.ResourceID = id
			desire.PartitionKey = testSubscription

			created, err := crud.Create(ctx, desire, nil)
			require.NoError(t, err, "should be able to create via the returned CRUD")
			require.NotNil(t, created)
		})
	}
}

func TestReadDesireKey_CRUD_DispatchesCorrectly(t *testing.T) {
	ctx := context.Background()
	mockDB := kubeappliercosmosstoragetesting.NewMockKubeApplierDBClient()

	tests := []struct {
		name  string
		idStr string
	}{
		{
			name:  "cluster-scoped",
			idStr: kubeapplierapi.ToClusterScopedReadDesireResourceIDString(testSubscription, testRG, testCluster, testDesireName),
		},
		{
			name:  "nodepool-scoped",
			idStr: kubeapplierapi.ToNodePoolScopedReadDesireResourceIDString(testSubscription, testRG, testCluster, testNodePool, testDesireName),
		},
		{
			name:  "credential-request-scoped",
			idStr: kubeapplierapi.ToSystemAdminCredentialRequestScopedReadDesireResourceIDString(testSubscription, testRG, testCluster, testCredReq, testDesireName),
		},
		{
			name:  "revocation-scoped",
			idStr: kubeapplierapi.ToSystemAdminCredentialRevocationScopedReadDesireResourceIDString(testSubscription, testRG, testCluster, testRevocation, testDesireName),
		},
		{
			name:  "management-cluster-scoped",
			idStr: kubeapplierapi.ToManagementClusterScopedReadDesireResourceIDString(testStampIdentifier, testDesireName),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			id := metadataapi.Must(azcorearm.ParseResourceID(tt.idStr))
			key, err := ReadDesireKeyFromResourceID(id)
			require.NoError(t, err)

			crud, err := key.CRUD(mockDB)
			require.NoError(t, err, "CRUD dispatch should succeed")
			require.NotNil(t, crud, "CRUD should return a non-nil value")

			desire := &kubeapplierapi.ReadDesire{}
			desire.ResourceID = id
			desire.PartitionKey = testSubscription

			created, err := crud.Create(ctx, desire, nil)
			require.NoError(t, err, "should be able to create via the returned CRUD")
			require.NotNil(t, created)
		})
	}
}
