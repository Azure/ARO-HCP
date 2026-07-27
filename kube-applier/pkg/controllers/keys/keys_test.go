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

	"github.com/Azure/ARO-HCP/internal/api"
	"github.com/Azure/ARO-HCP/internal/api/kubeapplier"
	"github.com/Azure/ARO-HCP/internal/databasetesting"
)

const (
	testSubscription = "00000000-0000-0000-0000-000000000001"
	testRG           = "test-rg"
	testCluster      = "test-cluster"
	testNodePool     = "test-np"
	testCredReq      = "test-cred-req"
	testRevocation   = "test-revocation"
	testDesireName   = "my-desire"
)

func TestParseDesireParts_ClusterScoped(t *testing.T) {
	idStr := kubeapplier.ToClusterScopedApplyDesireResourceIDString(testSubscription, testRG, testCluster, testDesireName)
	id := api.Must(azcorearm.ParseResourceID(idStr))

	key, err := ApplyDesireKeyFromResourceID(id)
	require.NoError(t, err)
	require.Equal(t, testSubscription, key.SubscriptionID)
	require.Equal(t, testRG, key.ResourceGroupName)
	require.Equal(t, testCluster, key.ClusterName)
	require.Empty(t, key.SubResourceType)
	require.Empty(t, key.SubResourceName)
	require.Equal(t, testDesireName, key.Name)
	require.True(t, key.IsClusterScoped())
	require.False(t, key.IsNodePoolScoped())
}

func TestParseDesireParts_NodePoolScoped(t *testing.T) {
	idStr := kubeapplier.ToNodePoolScopedApplyDesireResourceIDString(testSubscription, testRG, testCluster, testNodePool, testDesireName)
	id := api.Must(azcorearm.ParseResourceID(idStr))

	key, err := ApplyDesireKeyFromResourceID(id)
	require.NoError(t, err)
	require.Equal(t, testCluster, key.ClusterName)
	require.Equal(t, "nodepools", key.SubResourceType)
	require.Equal(t, testNodePool, key.SubResourceName)
	require.Equal(t, testDesireName, key.Name)
	require.False(t, key.IsClusterScoped())
	require.True(t, key.IsNodePoolScoped())
}

func TestParseDesireParts_CredentialRequestScoped(t *testing.T) {
	idStr := kubeapplier.ToCredentialRequestScopedApplyDesireResourceIDString(testSubscription, testRG, testCluster, testCredReq, testDesireName)
	id := api.Must(azcorearm.ParseResourceID(idStr))

	key, err := ApplyDesireKeyFromResourceID(id)
	require.NoError(t, err)
	require.Equal(t, testCluster, key.ClusterName)
	require.Equal(t, "systemadmincredentialrequests", key.SubResourceType)
	require.Equal(t, testCredReq, key.SubResourceName)
	require.Equal(t, testDesireName, key.Name)
	require.False(t, key.IsClusterScoped())
	require.False(t, key.IsNodePoolScoped())
}

func TestParseDesireParts_RevocationScoped(t *testing.T) {
	idStr := kubeapplier.ToRevocationScopedApplyDesireResourceIDString(testSubscription, testRG, testCluster, testRevocation, testDesireName)
	id := api.Must(azcorearm.ParseResourceID(idStr))

	key, err := ApplyDesireKeyFromResourceID(id)
	require.NoError(t, err)
	require.Equal(t, testCluster, key.ClusterName)
	require.Equal(t, "systemadmincredentialrevocations", key.SubResourceType)
	require.Equal(t, testRevocation, key.SubResourceName)
	require.Equal(t, testDesireName, key.Name)
	require.False(t, key.IsClusterScoped())
	require.False(t, key.IsNodePoolScoped())
}

func TestParseDesireParts_ReadDesire(t *testing.T) {
	idStr := kubeapplier.ToCredentialRequestScopedReadDesireResourceIDString(testSubscription, testRG, testCluster, testCredReq, testDesireName)
	id := api.Must(azcorearm.ParseResourceID(idStr))

	key, err := ReadDesireKeyFromResourceID(id)
	require.NoError(t, err)
	require.Equal(t, testCluster, key.ClusterName)
	require.Equal(t, "systemadmincredentialrequests", key.SubResourceType)
	require.Equal(t, testCredReq, key.SubResourceName)
	require.Equal(t, testDesireName, key.Name)
}

func TestParseDesireParts_ErrorCases(t *testing.T) {
	t.Run("nil resource ID", func(t *testing.T) {
		_, err := ApplyDesireKeyFromResourceID(nil)
		require.Error(t, err)
	})

	t.Run("no parent", func(t *testing.T) {
		id := api.Must(azcorearm.ParseResourceID("/subscriptions/" + testSubscription))
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
			idStr: kubeapplier.ToClusterScopedApplyDesireResourceIDString(testSubscription, testRG, testCluster, testDesireName),
		},
		{
			name:  "nodepool-scoped apply desire",
			idStr: kubeapplier.ToNodePoolScopedApplyDesireResourceIDString(testSubscription, testRG, testCluster, testNodePool, testDesireName),
		},
		{
			name:  "credential-request-scoped apply desire",
			idStr: kubeapplier.ToCredentialRequestScopedApplyDesireResourceIDString(testSubscription, testRG, testCluster, testCredReq, testDesireName),
		},
		{
			name:  "revocation-scoped apply desire",
			idStr: kubeapplier.ToRevocationScopedApplyDesireResourceIDString(testSubscription, testRG, testCluster, testRevocation, testDesireName),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			id := api.Must(azcorearm.ParseResourceID(tt.idStr))
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
			idStr: kubeapplier.ToClusterScopedReadDesireResourceIDString(testSubscription, testRG, testCluster, testDesireName),
		},
		{
			name:  "nodepool-scoped read desire",
			idStr: kubeapplier.ToNodePoolScopedReadDesireResourceIDString(testSubscription, testRG, testCluster, testNodePool, testDesireName),
		},
		{
			name:  "credential-request-scoped read desire",
			idStr: kubeapplier.ToCredentialRequestScopedReadDesireResourceIDString(testSubscription, testRG, testCluster, testCredReq, testDesireName),
		},
		{
			name:  "revocation-scoped read desire",
			idStr: kubeapplier.ToRevocationScopedReadDesireResourceIDString(testSubscription, testRG, testCluster, testRevocation, testDesireName),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			id := api.Must(azcorearm.ParseResourceID(tt.idStr))
			key, err := ReadDesireKeyFromResourceID(id)
			require.NoError(t, err)

			roundTripped := key.GetResourceID()
			require.Equal(t, strings.ToLower(tt.idStr), strings.ToLower(roundTripped.String()), "GetResourceID should round-trip to the original ID string")
		})
	}
}

func TestApplyDesireKey_CRUD_DispatchesCorrectly(t *testing.T) {
	ctx := context.Background()
	mockDB := databasetesting.NewMockKubeApplierDBClient()

	tests := []struct {
		name  string
		idStr string
	}{
		{
			name:  "cluster-scoped",
			idStr: kubeapplier.ToClusterScopedApplyDesireResourceIDString(testSubscription, testRG, testCluster, testDesireName),
		},
		{
			name:  "nodepool-scoped",
			idStr: kubeapplier.ToNodePoolScopedApplyDesireResourceIDString(testSubscription, testRG, testCluster, testNodePool, testDesireName),
		},
		{
			name:  "credential-request-scoped",
			idStr: kubeapplier.ToCredentialRequestScopedApplyDesireResourceIDString(testSubscription, testRG, testCluster, testCredReq, testDesireName),
		},
		{
			name:  "revocation-scoped",
			idStr: kubeapplier.ToRevocationScopedApplyDesireResourceIDString(testSubscription, testRG, testCluster, testRevocation, testDesireName),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			id := api.Must(azcorearm.ParseResourceID(tt.idStr))
			key, err := ApplyDesireKeyFromResourceID(id)
			require.NoError(t, err)

			crud, err := key.CRUD(mockDB)
			require.NoError(t, err, "CRUD dispatch should succeed")
			require.NotNil(t, crud, "CRUD should return a non-nil value")

			desire := &kubeapplier.ApplyDesire{}
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
	mockDB := databasetesting.NewMockKubeApplierDBClient()

	tests := []struct {
		name  string
		idStr string
	}{
		{
			name:  "cluster-scoped",
			idStr: kubeapplier.ToClusterScopedReadDesireResourceIDString(testSubscription, testRG, testCluster, testDesireName),
		},
		{
			name:  "nodepool-scoped",
			idStr: kubeapplier.ToNodePoolScopedReadDesireResourceIDString(testSubscription, testRG, testCluster, testNodePool, testDesireName),
		},
		{
			name:  "credential-request-scoped",
			idStr: kubeapplier.ToCredentialRequestScopedReadDesireResourceIDString(testSubscription, testRG, testCluster, testCredReq, testDesireName),
		},
		{
			name:  "revocation-scoped",
			idStr: kubeapplier.ToRevocationScopedReadDesireResourceIDString(testSubscription, testRG, testCluster, testRevocation, testDesireName),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			id := api.Must(azcorearm.ParseResourceID(tt.idStr))
			key, err := ReadDesireKeyFromResourceID(id)
			require.NoError(t, err)

			crud, err := key.CRUD(mockDB)
			require.NoError(t, err, "CRUD dispatch should succeed")
			require.NotNil(t, crud, "CRUD should return a non-nil value")

			desire := &kubeapplier.ReadDesire{}
			desire.ResourceID = id
			desire.PartitionKey = testSubscription

			created, err := crud.Create(ctx, desire, nil)
			require.NoError(t, err, "should be able to create via the returned CRUD")
			require.NotNil(t, created)
		})
	}
}
