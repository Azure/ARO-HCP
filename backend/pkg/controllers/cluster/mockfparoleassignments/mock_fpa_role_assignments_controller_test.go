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

package mockfparoleassignments

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/go-logr/logr/testr"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	azcorearm "github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"

	"github.com/Azure/ARO-HCP/backend/pkg/azure/roleassignment"
	"github.com/Azure/ARO-HCP/backend/pkg/utils/controllerutils"
	"github.com/Azure/ARO-HCP/internal/api/azureresourcesapi"
	"github.com/Azure/ARO-HCP/internal/api/coreapi"
	"github.com/Azure/ARO-HCP/internal/api/metadataapi"
	"github.com/Azure/ARO-HCP/internal/apihelpers/azureresourcesapihelpers"
	"github.com/Azure/ARO-HCP/internal/apihelpers/coreapihelpers"
	"github.com/Azure/ARO-HCP/internal/database/cosmosstorage/cosmosstorageutils"
	"github.com/Azure/ARO-HCP/internal/database/cosmosstoragetesting/azureresourcescosmosstoragetesting"
	"github.com/Azure/ARO-HCP/internal/database/listers/azureresourceslisters"
	"github.com/Azure/ARO-HCP/internal/utils"
)

func TestManagedResourceGroupScope(t *testing.T) {
	t.Parallel()

	confirmed := mustReservedID(t, "confirmed")
	pending := mustReservedID(t, "pending")

	t.Run("prefers the confirmed AzureResource", func(t *testing.T) {
		t.Parallel()
		scope := managedResourceGroupScope(coreapi.AzureReference{AzureResource: confirmed, PendingAzureResource: pending})
		assert.Same(t, confirmed, scope, "confirmed AzureResource is preferred over pending")
	})

	t.Run("falls back to PendingAzureResource", func(t *testing.T) {
		t.Parallel()
		scope := managedResourceGroupScope(coreapi.AzureReference{PendingAzureResource: pending})
		assert.Same(t, pending, scope, "pending reference is used when no confirmed one exists")
	})

	t.Run("nil when neither reference is set", func(t *testing.T) {
		t.Parallel()
		scope := managedResourceGroupScope(coreapi.AzureReference{})
		assert.Nil(t, scope, "no scope when the MRG is absent")
	})
}

func TestServiceProviderClusterWithReservedAzureResourceIDs(t *testing.T) {
	t.Parallel()

	scope, err := azcorearm.ParseResourceID(testManagedRGScope)
	require.NoError(t, err)

	// The map key is the lowercased identity; the reserved name must be computed from the
	// ORIGINAL casing carried on the value, so it matches what Cluster Service generates.
	desiredKey := coreapihelpers.NewRoleAssignmentKey(coreapi.RoleAssignmentKeyPartPrincipalID(testMockFPAPrincipalID), coreapi.RoleAssignmentKeyPartRoleDefinitionResourceID(OwnerRoleDefinitionResourceID))
	wantReservedID := roleassignment.ManagedResourceGroupScopedRoleAssignmentResourceID(scope.String(), testMockFPAPrincipalID, OwnerRoleDefinitionResourceID)

	s := &mockFPARoleAssignmentsSyncer{}

	t.Run("assigns a deterministic reserved ID to a non-draining key that lacks one", func(t *testing.T) {
		t.Parallel()
		existing := &coreapi.ServiceProviderCluster{}
		existing.Status.MockFirstPartyApplicationRoleAssignmentsOverManagedResourceGroup = map[coreapi.RoleAssignmentKey]*coreapi.MockFPARoleAssignmentStatus{
			desiredKey: {
				CasePreservedPrincipalID:              testMockFPAPrincipalID,
				CasePreservedRoleDefinitionResourceID: OwnerRoleDefinitionResourceID,
			},
		}

		got, err := s.serviceProviderClusterWithReservedAzureResourceIDs(existing, scope)
		require.NoError(t, err)
		gotStatus := got.Status.MockFirstPartyApplicationRoleAssignmentsOverManagedResourceGroup[desiredKey]
		require.NotNil(t, gotStatus.ReservedAzureResourceID, "non-draining key should get a reserved ID")
		assert.Equal(t, wantReservedID, gotStatus.ReservedAzureResourceID.String(), "reserved ID must be the deterministic name from the case-preserved casing")

		assert.Nil(t, existing.Status.MockFirstPartyApplicationRoleAssignmentsOverManagedResourceGroup[desiredKey].ReservedAzureResourceID, "input must not be mutated")
	})

	t.Run("errors when a non-draining key lacks case-preserved casing", func(t *testing.T) {
		t.Parallel()
		existing := &coreapi.ServiceProviderCluster{}
		existing.Status.MockFirstPartyApplicationRoleAssignmentsOverManagedResourceGroup = map[coreapi.RoleAssignmentKey]*coreapi.MockFPARoleAssignmentStatus{
			desiredKey: {},
		}

		_, err := s.serviceProviderClusterWithReservedAzureResourceIDs(existing, scope)
		require.Error(t, err, "cannot compute the name without the original casing")
	})

	t.Run("does not reserve for a draining key", func(t *testing.T) {
		t.Parallel()
		stamp := metav1.NewTime(time.Now())
		existing := &coreapi.ServiceProviderCluster{}
		existing.Status.MockFirstPartyApplicationRoleAssignmentsOverManagedResourceGroup = map[coreapi.RoleAssignmentKey]*coreapi.MockFPARoleAssignmentStatus{
			desiredKey: {DeconfigureTimestamp: &stamp},
		}

		got, err := s.serviceProviderClusterWithReservedAzureResourceIDs(existing, scope)
		require.NoError(t, err)
		assert.Nil(t, got.Status.MockFirstPartyApplicationRoleAssignmentsOverManagedResourceGroup[desiredKey].ReservedAzureResourceID, "draining key must not get a reserved ID")
	})

	t.Run("preserves an existing reserved ID, never recomputes", func(t *testing.T) {
		t.Parallel()
		preset := mustReservedID(t, "11111111-1111-1111-1111-111111111111")
		existing := &coreapi.ServiceProviderCluster{}
		existing.Status.MockFirstPartyApplicationRoleAssignmentsOverManagedResourceGroup = map[coreapi.RoleAssignmentKey]*coreapi.MockFPARoleAssignmentStatus{
			desiredKey: {ReservedAzureResourceID: preset},
		}

		got, err := s.serviceProviderClusterWithReservedAzureResourceIDs(existing, scope)
		require.NoError(t, err)
		assert.Equal(t, preset.String(), got.Status.MockFirstPartyApplicationRoleAssignmentsOverManagedResourceGroup[desiredKey].ReservedAzureResourceID.String(), "existing reserved ID must be preserved")
	})
}

func TestRoleAssignmentTemplateResources(t *testing.T) {
	t.Parallel()

	desiredKey := coreapihelpers.NewRoleAssignmentKey(coreapi.RoleAssignmentKeyPartPrincipalID(testMockFPAPrincipalID), coreapi.RoleAssignmentKeyPartRoleDefinitionResourceID(OwnerRoleDefinitionResourceID))
	drainingKey := coreapihelpers.NewRoleAssignmentKey(coreapi.RoleAssignmentKeyPartPrincipalID("old-principal"), coreapi.RoleAssignmentKeyPartRoleDefinitionResourceID(OwnerRoleDefinitionResourceID))
	stamp := metav1.NewTime(time.Now())

	t.Run("renders the non-draining key from the lowercased map key and excludes the draining one", func(t *testing.T) {
		t.Parallel()
		assignments := map[coreapi.RoleAssignmentKey]*coreapi.MockFPARoleAssignmentStatus{
			desiredKey:  {ReservedAzureResourceID: mustReservedID(t, "11111111-1111-1111-1111-111111111111")},
			drainingKey: {DeconfigureTimestamp: &stamp, ReservedAzureResourceID: mustReservedID(t, "22222222-2222-2222-2222-222222222222")},
		}
		resources, err := roleAssignmentTemplateResources(assignments)
		require.NoError(t, err)
		require.Len(t, resources, 1)
		assert.Equal(t, "Microsoft.Authorization/roleAssignments", resources[0]["type"])
		assert.Equal(t, "11111111-1111-1111-1111-111111111111", resources[0]["name"])
		props := resources[0]["properties"].(map[string]any)
		// Properties come from the lowercased map key (Azure treats these IDs
		// case-insensitively); only the name needs the original casing.
		assert.Equal(t, string(desiredKey.PrincipalID), props["principalId"])
		assert.Equal(t, string(desiredKey.RoleDefinitionResourceID), props["roleDefinitionId"])
		assert.EqualValues(t, "ServicePrincipal", props["principalType"])
	})

	t.Run("errors when a non-draining key has no reserved ID", func(t *testing.T) {
		t.Parallel()
		assignments := map[coreapi.RoleAssignmentKey]*coreapi.MockFPARoleAssignmentStatus{
			desiredKey: {},
		}
		_, err := roleAssignmentTemplateResources(assignments)
		require.Error(t, err)
	})

	t.Run("empty when all keys are draining", func(t *testing.T) {
		t.Parallel()
		assignments := map[coreapi.RoleAssignmentKey]*coreapi.MockFPARoleAssignmentStatus{
			drainingKey: {DeconfigureTimestamp: &stamp, ReservedAzureResourceID: mustReservedID(t, "22222222-2222-2222-2222-222222222222")},
		}
		resources, err := roleAssignmentTemplateResources(assignments)
		require.NoError(t, err)
		assert.Empty(t, resources)
	})
}

// fakeDeploymentStackDesireLister is a minimal DeploymentStackDesireLister whose
// GetForCluster reports a single, optionally-present desire. It is enough to drive
// deleteDeploymentStackDesireDatabaseEntry past its existence precheck without wiring an
// informer indexer.
type fakeDeploymentStackDesireLister struct {
	desire *azureresourcesapi.DeploymentStackDesire
}

var _ azureresourceslisters.DeploymentStackDesireLister = (*fakeDeploymentStackDesireLister)(nil)

func (f *fakeDeploymentStackDesireLister) List(ctx context.Context) ([]*azureresourcesapi.DeploymentStackDesire, error) {
	if f.desire == nil {
		return nil, nil
	}
	return []*azureresourcesapi.DeploymentStackDesire{f.desire}, nil
}

func (f *fakeDeploymentStackDesireLister) GetByResourceID(ctx context.Context, resourceID string) (*azureresourcesapi.DeploymentStackDesire, error) {
	return f.desire, nil
}

func (f *fakeDeploymentStackDesireLister) GetForCluster(ctx context.Context, subscriptionID, resourceGroupName, clusterName, name string) (*azureresourcesapi.DeploymentStackDesire, error) {
	return f.desire, nil
}

func (f *fakeDeploymentStackDesireLister) ListForCluster(ctx context.Context, subscriptionID, resourceGroupName, clusterName string) ([]*azureresourcesapi.DeploymentStackDesire, error) {
	return f.List(ctx)
}

// TestDeleteDeploymentStackDesireDatabaseEntry is a regression test for a delete that was
// keyed by the full resource ID string instead of the resource name. The nested CRUD
// treats its argument as a name, so the wrong key soft-deleted a non-existent document
// (a NotFound that is swallowed as success) and left the real desire behind while still
// logging success. The test asserts the document is actually removed.
func TestDeleteDeploymentStackDesireDatabaseEntry(t *testing.T) {
	ctx := utils.ContextWithLogger(context.Background(), testr.New(t))

	const (
		subscriptionID    = "00000000-0000-0000-0000-000000000000"
		resourceGroupName = "test-rg"
		clusterName       = "test-cluster"
	)
	key := controllerutils.HCPClusterKey{
		SubscriptionID:    subscriptionID,
		ResourceGroupName: resourceGroupName,
		HCPClusterName:    clusterName,
	}

	desireID := metadataapi.Must(azureresourcesapihelpers.ToClusterScopedDeploymentStackDesireResourceID(
		subscriptionID, resourceGroupName, clusterName, azureresourcesapi.MockFPARoleAssignmentsDeploymentStackDesireName))
	desire := &azureresourcesapi.DeploymentStackDesire{
		CosmosMetadata: coreapi.CosmosMetadata{
			ResourceID:   desireID,
			PartitionKey: strings.ToLower(subscriptionID),
		},
	}

	azureDB, err := azureresourcescosmosstoragetesting.NewMockAzureResourcesDBClientWithResources(ctx, []any{desire})
	require.NoError(t, err)

	s := &mockFPARoleAssignmentsSyncer{
		azureResourcesDBClient:      azureDB,
		deploymentStackDesireLister: &fakeDeploymentStackDesireLister{desire: desire},
	}

	require.NoError(t, s.deleteDeploymentStackDesireDatabaseEntry(ctx, key))

	// The document must actually be gone. Keyed by the wrong value the delete is a silent
	// no-op, so this Get would still return the desire.
	crud, err := azureDB.DeploymentStackDesiresForCluster(subscriptionID, resourceGroupName, clusterName)
	require.NoError(t, err)
	_, err = crud.Get(ctx, azureresourcesapi.MockFPARoleAssignmentsDeploymentStackDesireName)
	assert.True(t, cosmosstorageutils.IsNotFoundError(err),
		"expected the mock FPA DeploymentStackDesire to be deleted (Get should return NotFound), got err=%v", err)
}
