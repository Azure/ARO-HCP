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

package database

import (
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	azcorearm "github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"

	"github.com/Azure/ARO-HCP/internal/api"
	"github.com/Azure/ARO-HCP/internal/api/arm"
)

// These tests complement the randomized fuzz-based round-trip tests by
// pinning Status.Conditions preservation with a deterministic fixture.
// They exist so a regression in Cosmos conversion that happens to miss
// the Conditions slice fails visibly with a small, readable diff rather
// than waiting for a specific fuzz seed to reproduce.
//
// Contract under test: the storage shape documented in
// internal/api/STATUS_OWNERSHIP.md — Status is preserved verbatim
// through internal -> Cosmos -> internal conversion.

func statusConditionFixture() []api.Condition {
	return []api.Condition{
		{
			Type:               "Progressing",
			Status:             api.ConditionTrue,
			LastTransitionTime: time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC),
			Reason:             "Reconciling",
			Message:            "control plane install in progress",
		},
		{
			Type:               "Degraded",
			Status:             api.ConditionFalse,
			LastTransitionTime: time.Date(2026, 1, 1, 13, 0, 0, 0, time.UTC),
			Reason:             "AsExpected",
			Message:            "no degradation observed",
		},
	}
}

func clusterFixtureWithConditions(t *testing.T) *api.HCPOpenShiftCluster {
	t.Helper()
	id := api.Must(azcorearm.ParseResourceID("/subscriptions/0465bc32-c654-41b8-8d87-9815d7abe8f6/resourceGroups/rg/providers/Microsoft.RedHatOpenShift/hcpOpenShiftClusters/c1"))
	return &api.HCPOpenShiftCluster{
		TrackedResource: arm.TrackedResource{
			Resource: arm.Resource{
				ID:   id,
				Name: "c1",
				Type: "Microsoft.RedHatOpenShift/hcpOpenShiftClusters",
			},
			Location: "eastus",
		},
		ServiceProviderProperties: api.HCPOpenShiftClusterServiceProviderProperties{
			ClusterServiceID: api.Must(api.NewInternalID("/api/clusters_mgmt/v1/clusters/abcdef123456")),
		},
		Status: api.HCPOpenShiftClusterStatus{
			Conditions: statusConditionFixture(),
		},
	}
}

func TestRoundTripCluster_StatusConditionsPreserved(t *testing.T) {
	original := clusterFixtureWithConditions(t)

	cosmosObj, err := InternalToCosmosCluster(original)
	require.NoError(t, err)
	require.NotNil(t, cosmosObj)

	final, err := CosmosToInternalCluster(cosmosObj)
	require.NoError(t, err)
	require.NotNil(t, final)

	// ExistingCosmosUID is populated on read from the Cosmos document ID; clear for comparison.
	final.ServiceProviderProperties.ExistingCosmosUID = ""

	assert.Equal(t, "", cmp.Diff(original.Status, final.Status, api.CmpDiffOptions...),
		"Status.Conditions must round-trip verbatim through Cosmos conversion")
}

func TestRoundTripNodePool_StatusConditionsPreserved(t *testing.T) {
	id := api.Must(azcorearm.ParseResourceID("/subscriptions/0465bc32-c654-41b8-8d87-9815d7abe8f6/resourceGroups/rg/providers/Microsoft.RedHatOpenShift/hcpOpenShiftClusters/c1/nodePools/np1"))
	original := &api.HCPOpenShiftClusterNodePool{
		TrackedResource: arm.TrackedResource{
			Resource: arm.Resource{
				ID:   id,
				Name: "np1",
				Type: "Microsoft.RedHatOpenShift/hcpOpenShiftClusters/nodePools",
			},
			Location: "eastus",
		},
		ServiceProviderProperties: api.HCPOpenShiftClusterNodePoolServiceProviderProperties{
			ClusterServiceID: api.Must(api.NewInternalID("/api/clusters_mgmt/v1/clusters/abcdef123456/node_pools/np1")),
		},
		Status: api.HCPOpenShiftClusterNodePoolStatus{
			Conditions: statusConditionFixture(),
		},
	}

	cosmosObj, err := InternalToCosmosNodePool(original)
	require.NoError(t, err)
	require.NotNil(t, cosmosObj)

	final, err := CosmosToInternalNodePool(cosmosObj)
	require.NoError(t, err)
	require.NotNil(t, final)

	final.ServiceProviderProperties.ExistingCosmosUID = ""

	assert.Equal(t, "", cmp.Diff(original.Status, final.Status, api.CmpDiffOptions...),
		"Status.Conditions must round-trip verbatim through Cosmos conversion")
}

func TestRoundTripExternalAuth_StatusConditionsPreserved(t *testing.T) {
	id := api.Must(azcorearm.ParseResourceID("/subscriptions/0465bc32-c654-41b8-8d87-9815d7abe8f6/resourceGroups/rg/providers/Microsoft.RedHatOpenShift/hcpOpenShiftClusters/c1/externalAuths/ea1"))
	original := &api.HCPOpenShiftClusterExternalAuth{
		ProxyResource: arm.ProxyResource{
			Resource: arm.Resource{
				ID:   id,
				Name: "ea1",
				Type: "Microsoft.RedHatOpenShift/hcpOpenShiftClusters/externalAuths",
			},
		},
		ServiceProviderProperties: api.HCPOpenShiftClusterExternalAuthServiceProviderProperties{
			ClusterServiceID: api.Must(api.NewInternalID("/api/clusters_mgmt/v1/clusters/abcdef123456/external_auth_config/external_auths/ea1")),
		},
		Status: api.HCPOpenShiftClusterExternalAuthStatus{
			Conditions: statusConditionFixture(),
		},
	}

	cosmosObj, err := InternalToCosmosExternalAuth(original)
	require.NoError(t, err)
	require.NotNil(t, cosmosObj)

	final, err := CosmosToInternalExternalAuth(cosmosObj)
	require.NoError(t, err)
	require.NotNil(t, final)

	final.ServiceProviderProperties.ExistingCosmosUID = ""

	assert.Equal(t, "", cmp.Diff(original.Status, final.Status, api.CmpDiffOptions...),
		"Status.Conditions must round-trip verbatim through Cosmos conversion")
}

// TestRoundTripCluster_EmptyStatusOmitted locks the zero-migration
// guarantee: a cluster with no conditions round-trips with an empty
// Status, not a populated-but-zero one, so existing Cosmos documents
// written before this schema landed do not change shape when read and
// rewritten.
func TestRoundTripCluster_EmptyStatusOmitted(t *testing.T) {
	original := clusterFixtureWithConditions(t)
	original.Status = api.HCPOpenShiftClusterStatus{}

	cosmosObj, err := InternalToCosmosCluster(original)
	require.NoError(t, err)

	final, err := CosmosToInternalCluster(cosmosObj)
	require.NoError(t, err)

	assert.Empty(t, final.Status.Conditions, "empty Status must not gain spurious conditions through round-trip")
}
