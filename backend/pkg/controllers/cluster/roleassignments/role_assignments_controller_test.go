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

package roleassignments

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/go-logr/logr/testr"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/utils/ptr"

	azcorearm "github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/resources/armdeploymentstacks"

	"github.com/Azure/ARO-HCP/backend/pkg/azure/roleassignment"
	"github.com/Azure/ARO-HCP/internal/api/coreapi"
	"github.com/Azure/ARO-HCP/internal/api/metadataapi"
	"github.com/Azure/ARO-HCP/internal/apihelpers/coreapihelpers"
	"github.com/Azure/ARO-HCP/internal/azure"
	"github.com/Azure/ARO-HCP/internal/database/cosmosstorage/cosmosstorageutils"
	"github.com/Azure/ARO-HCP/internal/database/cosmosstoragetesting/corecosmosstoragetesting"
	"github.com/Azure/ARO-HCP/internal/database/listertesting/corelistertesting"
	"github.com/Azure/ARO-HCP/internal/utils"
)

const (
	testSubscriptionID    = "00000000-0000-0000-0000-000000000000"
	testResourceGroupName = "test-rg"
	testClusterName       = "test-cluster"
	testTenantID          = "test-tenant-id"
	testManagedRGName     = "test-managed-rg"

	// testControlPlaneOperatorName / testDataPlaneOperatorName are real operator
	// identifiers so their role definitions can be resolved from the cluster-scoped
	// identities config. "control-plane" is control-plane only; "disk-csi-driver" has a
	// data-plane config, so the test cluster exercises both the CP and DP enumeration.
	testControlPlaneOperatorName = string(azure.ClusterOperatorIdentifierControlPlane)
	testDataPlaneOperatorName    = string(azure.ClusterOperatorIdentifierDiskCSIDriver)

	testControlPlaneIdentityID = "/subscriptions/" + testSubscriptionID +
		"/resourceGroups/" + testResourceGroupName +
		"/providers/Microsoft.ManagedIdentity/userAssignedIdentities/cp-identity"
	testDataPlaneIdentityID = "/subscriptions/" + testSubscriptionID +
		"/resourceGroups/" + testResourceGroupName +
		"/providers/Microsoft.ManagedIdentity/userAssignedIdentities/dp-identity"
	testServiceManagedIdentityID = "/subscriptions/" + testSubscriptionID +
		"/resourceGroups/" + testResourceGroupName +
		"/providers/Microsoft.ManagedIdentity/userAssignedIdentities/smi-identity"

	testControlPlanePrincipalID           = "cp-principal-11111111-1111-1111-1111-111111111111"
	testControlPlaneARMPrincipalID        = "arm-principal-cp-33333333-3333-3333-3333-333333333333"
	testDataPlanePrincipalID              = "dp-principal-22222222-2222-2222-2222-222222222222"
	testServiceManagedIdentityPrincipalID = "smi-principal-44444444-4444-4444-4444-444444444444"
)

// testDeterministicResourceID returns the same deterministic role assignment
// resource ID ClusterRoleAssignments' reservation pass computes for key, so
// tests can assert on the exact value without depending on internals.
func testDeterministicResourceID(t *testing.T, key coreapi.RoleAssignmentKey) *azcorearm.ResourceID {
	t.Helper()
	id, err := azcorearm.ParseResourceID(roleassignment.ManagedResourceGroupScopedRoleAssignmentResourceID(testManagedResourceGroupID(t).String(), key.PrincipalID, key.RoleDefinitionResourceID))
	require.NoError(t, err)
	return id
}

func newTestRoleAssignmentsSyncer(resourcesDB *corecosmosstoragetesting.MockResourcesDBClient) *clusterRoleAssignmentsSyncer {
	return &clusterRoleAssignmentsSyncer{
		clusterLister:                &corelistertesting.DBClusterLister{ResourcesDBClient: resourcesDB},
		serviceProviderClusterLister: &corelistertesting.DBServiceProviderClusterLister{ResourcesDBClient: resourcesDB},
		subscriptionLister:           &corelistertesting.DBSubscriptionLister{ResourcesDBClient: resourcesDB},
		stackDesireLister:            &corelistertesting.DBStackDesireLister{ResourcesDBClient: resourcesDB},
		resourcesDBClient:            resourcesDB,
	}
}

func getRoleAssignmentsStackDesire(t *testing.T, ctx context.Context, resourcesDB *corecosmosstoragetesting.MockResourcesDBClient) *coreapi.StackDesire {
	t.Helper()
	desire, err := resourcesDB.StackDesires(testSubscriptionID, testResourceGroupName, testClusterName).Get(ctx, RoleAssignmentsDesireName)
	if cosmosstorageutils.IsNotFoundError(err) {
		return nil
	}
	require.NoError(t, err)
	return desire
}

// templateResources extracts the "resources" array from a StackDesire's ARM
// template as []map[string]any. A document round-tripped through Cosmos JSON
// storage decodes Template's nested array as []any of map[string]any, rather
// than the []map[string]any a producer builds it as in memory, so this
// normalizes either representation for assertions.
func templateResources(t *testing.T, template any) []map[string]any {
	t.Helper()
	asMap, ok := template.(map[string]any)
	require.True(t, ok, "template must decode to map[string]any, got %T", template)
	raw := asMap["resources"]
	switch typed := raw.(type) {
	case []map[string]any:
		return typed
	case []any:
		result := make([]map[string]any, 0, len(typed))
		for _, item := range typed {
			resource, ok := item.(map[string]any)
			require.True(t, ok, "template resource must decode to map[string]any, got %T", item)
			result = append(result, resource)
		}
		return result
	default:
		require.Failf(t, "unexpected resources type", "%T", raw)
		return nil
	}
}

func getUpdatedServiceProviderCluster(t *testing.T, ctx context.Context, resourcesDB *corecosmosstoragetesting.MockResourcesDBClient) *coreapi.ServiceProviderCluster {
	t.Helper()
	updated, err := resourcesDB.ServiceProviderClusters(testSubscriptionID, testResourceGroupName, testClusterName).Get(ctx, coreapi.ServiceProviderClusterResourceName)
	require.NoError(t, err)
	return updated
}

func TestClusterRoleAssignmentsSyncOnceSkipsClusterDeletion(t *testing.T) {
	t.Parallel()

	ctx := utils.ContextWithLogger(context.Background(), testr.New(t))
	cluster := newTestCluster(true)
	keys := testDesiredRoleAssignmentKeys(t)
	serviceProviderCluster := newTestServiceProviderCluster(t, true, true, true, map[coreapi.RoleAssignmentKey]*coreapi.RoleAssignmentStatus{
		keys[0]: {ReservedAzureResourceID: testDeterministicResourceID(t, keys[0])},
	})
	existingDesire := newTestStackDesireForCluster(t, coreapi.StackDesireActionApply)

	mockResourcesDB, err := corecosmosstoragetesting.NewMockResourcesDBClientWithResources(ctx, []any{cluster, serviceProviderCluster, existingDesire, newTestSubscription(ptr.To(testTenantID))})
	require.NoError(t, err)

	syncer := newTestRoleAssignmentsSyncer(mockResourcesDB)
	require.NoError(t, syncer.SyncOnce(ctx, testHCPClusterKey))

	assert.Nil(t, getRoleAssignmentsStackDesire(t, ctx, mockResourcesDB), "StackDesire document must be removed on cluster deletion")
}

func TestClusterRoleAssignmentsSyncOnceClusterNotFound(t *testing.T) {
	t.Parallel()

	ctx := utils.ContextWithLogger(context.Background(), testr.New(t))
	existingDesire := newTestStackDesireForCluster(t, coreapi.StackDesireActionApply)

	mockResourcesDB, err := corecosmosstoragetesting.NewMockResourcesDBClientWithResources(ctx, []any{existingDesire})
	require.NoError(t, err)

	syncer := newTestRoleAssignmentsSyncer(mockResourcesDB)
	require.NoError(t, syncer.SyncOnce(ctx, testHCPClusterKey))

	assert.Nil(t, getRoleAssignmentsStackDesire(t, ctx, mockResourcesDB), "StackDesire document must be removed when the cluster no longer exists")
}

func TestClusterRoleAssignmentsSyncOnceSkipsUntilManagedResourceGroupConfirmed(t *testing.T) {
	t.Parallel()

	ctx := utils.ContextWithLogger(context.Background(), testr.New(t))
	cluster := newTestCluster(false)
	keys := testDesiredRoleAssignmentKeys(t)
	serviceProviderCluster := newTestServiceProviderCluster(t, false, true, true, map[coreapi.RoleAssignmentKey]*coreapi.RoleAssignmentStatus{
		keys[0]: {},
	})

	mockResourcesDB, err := corecosmosstoragetesting.NewMockResourcesDBClientWithResources(ctx, []any{cluster, serviceProviderCluster, newTestSubscription(ptr.To(testTenantID))})
	require.NoError(t, err)

	syncer := newTestRoleAssignmentsSyncer(mockResourcesDB)
	require.NoError(t, syncer.SyncOnce(ctx, testHCPClusterKey))

	assert.Nil(t, getRoleAssignmentsStackDesire(t, ctx, mockResourcesDB))
	updated := getUpdatedServiceProviderCluster(t, ctx, mockResourcesDB)
	assert.Nil(t, updated.Status.RoleAssignmentsOverManagedResourceGroup[keys[0]].ReservedAzureResourceID)
}

func TestClusterRoleAssignmentsSyncOnceReservesIDsAndCreatesStackDesire(t *testing.T) {
	t.Parallel()

	ctx := utils.ContextWithLogger(context.Background(), testr.New(t))
	cluster := newTestCluster(false)
	keys := testDesiredRoleAssignmentKeys(t)
	serviceProviderCluster := newTestServiceProviderCluster(t, true, true, true, map[coreapi.RoleAssignmentKey]*coreapi.RoleAssignmentStatus{
		keys[0]: {},
		keys[1]: {},
	})

	mockResourcesDB, err := corecosmosstoragetesting.NewMockResourcesDBClientWithResources(ctx, []any{cluster, serviceProviderCluster, newTestSubscription(ptr.To(testTenantID))})
	require.NoError(t, err)

	syncer := newTestRoleAssignmentsSyncer(mockResourcesDB)
	require.NoError(t, syncer.SyncOnce(ctx, testHCPClusterKey))

	updated := getUpdatedServiceProviderCluster(t, ctx, mockResourcesDB)
	id0 := updated.Status.RoleAssignmentsOverManagedResourceGroup[keys[0]].ReservedAzureResourceID
	id1 := updated.Status.RoleAssignmentsOverManagedResourceGroup[keys[1]].ReservedAzureResourceID
	require.NotNil(t, id0)
	require.NotNil(t, id1)
	assert.Equal(t, testDeterministicResourceID(t, keys[0]).String(), id0.String())
	assert.Equal(t, testDeterministicResourceID(t, keys[1]).String(), id1.String())

	desire := getRoleAssignmentsStackDesire(t, ctx, mockResourcesDB)
	require.NotNil(t, desire)
	assert.Equal(t, coreapi.StackDesireActionApply, desire.Spec.Action)
	assert.Equal(t, coreapi.StackDesireAuthenticationTypeFPA, desire.Spec.Authentication.Type)
	assert.Equal(t, testTenantID, desire.Spec.Authentication.TenantID)
	require.NotNil(t, desire.Spec.TargetResourceID)
	assert.Equal(t, testManagedResourceGroupID(t).String()+"/providers/Microsoft.Resources/deploymentStacks/"+RoleAssignmentsStackName, desire.Spec.TargetResourceID.String())

	require.NotNil(t, desire.Spec.Stack)
	require.NotNil(t, desire.Spec.Stack.Properties)
	require.NotNil(t, desire.Spec.Stack.Properties.ActionOnUnmanage)
	assert.Equal(t, armdeploymentstacks.DeploymentStacksDeleteDetachEnumDelete, ptr.Deref(desire.Spec.Stack.Properties.ActionOnUnmanage.Resources, ""))
	assert.Equal(t, armdeploymentstacks.DeploymentStacksDeleteDetachEnumDetach, ptr.Deref(desire.Spec.Stack.Properties.ActionOnUnmanage.ResourceGroups, ""))
	assert.Equal(t, armdeploymentstacks.DeploymentStacksDeleteDetachEnumDetach, ptr.Deref(desire.Spec.Stack.Properties.ActionOnUnmanage.ManagementGroups, ""))
	require.NotNil(t, desire.Spec.Stack.Properties.DenySettings)
	assert.Equal(t, armdeploymentstacks.DenySettingsModeNone, ptr.Deref(desire.Spec.Stack.Properties.DenySettings.Mode, ""))

	resources := templateResources(t, desire.Spec.Stack.Properties.Template)
	require.Len(t, resources, 2)

	// Keys are sorted by (PrincipalID, RoleDefinitionResourceID), independent
	// of map iteration order, so the template (and its spec hash) is stable
	// across syncs when nothing changed.
	sortedKeys := []coreapi.RoleAssignmentKey{keys[0], keys[1]}
	if sortedKeys[1].PrincipalID < sortedKeys[0].PrincipalID {
		sortedKeys[0], sortedKeys[1] = sortedKeys[1], sortedKeys[0]
	}
	for i, key := range sortedKeys {
		resource := resources[i]
		assert.Equal(t, "Microsoft.Authorization/roleAssignments", resource["type"])
		assert.Equal(t, testDeterministicResourceID(t, key).Name, resource["name"])
		properties, ok := resource["properties"].(map[string]any)
		require.True(t, ok)
		assert.Equal(t, key.PrincipalID, properties["principalId"])
		assert.Equal(t, key.RoleDefinitionResourceID, properties["roleDefinitionId"])
		assert.Equal(t, "ServicePrincipal", properties["principalType"])
	}
}

func TestClusterRoleAssignmentsSyncOnceNoOpWhenSpecUnchanged(t *testing.T) {
	t.Parallel()

	ctx := utils.ContextWithLogger(context.Background(), testr.New(t))
	cluster := newTestCluster(false)
	keys := testDesiredRoleAssignmentKeys(t)
	serviceProviderCluster := newTestServiceProviderCluster(t, true, true, true, map[coreapi.RoleAssignmentKey]*coreapi.RoleAssignmentStatus{
		keys[0]: {ReservedAzureResourceID: testDeterministicResourceID(t, keys[0])},
	})

	mockResourcesDB, err := corecosmosstoragetesting.NewMockResourcesDBClientWithResources(ctx, []any{cluster, serviceProviderCluster, newTestSubscription(ptr.To(testTenantID))})
	require.NoError(t, err)

	syncer := newTestRoleAssignmentsSyncer(mockResourcesDB)
	require.NoError(t, syncer.SyncOnce(ctx, testHCPClusterKey))

	spcBefore := getUpdatedServiceProviderCluster(t, ctx, mockResourcesDB)
	desireBefore := getRoleAssignmentsStackDesire(t, ctx, mockResourcesDB)
	require.NotNil(t, desireBefore)

	require.NoError(t, syncer.SyncOnce(ctx, testHCPClusterKey))

	spcAfter := getUpdatedServiceProviderCluster(t, ctx, mockResourcesDB)
	desireAfter := getRoleAssignmentsStackDesire(t, ctx, mockResourcesDB)
	require.NotNil(t, desireAfter)
	assert.Equal(t, spcBefore.InstanceVersion, spcAfter.InstanceVersion, "no new reservation should be made once a key has a ReservedAzureResourceID")
	assert.Equal(t, desireBefore.InstanceVersion, desireAfter.InstanceVersion, "an unchanged spec must not be replaced")
}

func TestClusterRoleAssignmentsSyncOnceReplacesWhenSpecChanged(t *testing.T) {
	t.Parallel()

	ctx := utils.ContextWithLogger(context.Background(), testr.New(t))
	cluster := newTestCluster(false)
	keys := testDesiredRoleAssignmentKeys(t)
	serviceProviderCluster := newTestServiceProviderCluster(t, true, true, true, map[coreapi.RoleAssignmentKey]*coreapi.RoleAssignmentStatus{
		keys[0]: {ReservedAzureResourceID: testDeterministicResourceID(t, keys[0])},
	})
	existingDesire := newTestStackDesireForCluster(t, coreapi.StackDesireActionApply)
	existingDesire.Spec.Stack.Properties.Template = map[string]any{
		"$schema":        "https://schema.management.azure.com/schemas/2019-04-01/deploymentTemplate.json#",
		"contentVersion": "1.0.0.0",
		"resources":      []map[string]any{},
	}

	mockResourcesDB, err := corecosmosstoragetesting.NewMockResourcesDBClientWithResources(ctx, []any{cluster, serviceProviderCluster, existingDesire, newTestSubscription(ptr.To(testTenantID))})
	require.NoError(t, err)

	before := getRoleAssignmentsStackDesire(t, ctx, mockResourcesDB)
	require.NotNil(t, before)

	syncer := newTestRoleAssignmentsSyncer(mockResourcesDB)
	require.NoError(t, syncer.SyncOnce(ctx, testHCPClusterKey))

	after := getRoleAssignmentsStackDesire(t, ctx, mockResourcesDB)
	require.NotNil(t, after)
	assert.NotEqual(t, before.InstanceVersion, after.InstanceVersion)
	resources := templateResources(t, after.Spec.Stack.Properties.Template)
	require.Len(t, resources, 1)
}

func TestClusterRoleAssignmentsSyncOnceIncludesDrainingKeyWithinWindow(t *testing.T) {
	t.Parallel()

	ctx := utils.ContextWithLogger(context.Background(), testr.New(t))
	cluster := newTestCluster(false)
	keys := testDesiredRoleAssignmentKeys(t)
	recentlyStamped := metav1.NewTime(time.Now().Add(-time.Hour))
	serviceProviderCluster := newTestServiceProviderCluster(t, true, true, true, map[coreapi.RoleAssignmentKey]*coreapi.RoleAssignmentStatus{
		keys[0]: {
			ReservedAzureResourceID: testDeterministicResourceID(t, keys[0]),
			DeconfigureTimestamp:    &recentlyStamped,
		},
	})

	mockResourcesDB, err := corecosmosstoragetesting.NewMockResourcesDBClientWithResources(ctx, []any{cluster, serviceProviderCluster, newTestSubscription(ptr.To(testTenantID))})
	require.NoError(t, err)

	syncer := newTestRoleAssignmentsSyncer(mockResourcesDB)
	require.NoError(t, syncer.SyncOnce(ctx, testHCPClusterKey))

	desire := getRoleAssignmentsStackDesire(t, ctx, mockResourcesDB)
	require.NotNil(t, desire)
	resources := templateResources(t, desire.Spec.Stack.Properties.Template)
	require.Len(t, resources, 1, "a draining key still inside its deconfigure wait must keep being granted")
	assert.Equal(t, testDeterministicResourceID(t, keys[0]).Name, resources[0]["name"])
}

func TestClusterRoleAssignmentsSyncOnceEmptyDesiredSet(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name           string
		existingAction string
		wantDesire     bool
		wantAction     string
	}{
		{
			name:       "no existing desire stays absent",
			wantDesire: false,
		},
		{
			name:           "existing applied desire is asked to delete",
			existingAction: coreapi.StackDesireActionApply,
			wantDesire:     true,
			wantAction:     coreapi.StackDesireActionDelete,
		},
		{
			name:           "existing delete request is left alone",
			existingAction: coreapi.StackDesireActionDelete,
			wantDesire:     true,
			wantAction:     coreapi.StackDesireActionDelete,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			ctx := utils.ContextWithLogger(context.Background(), testr.New(t))
			cluster := newTestCluster(false)
			serviceProviderCluster := newTestServiceProviderCluster(t, true, true, true, nil)

			resources := []any{cluster, serviceProviderCluster, newTestSubscription(ptr.To(testTenantID))}
			var before *coreapi.StackDesire
			if tt.existingAction != "" {
				before = newTestStackDesireForCluster(t, tt.existingAction)
				resources = append(resources, before)
			}

			mockResourcesDB, err := corecosmosstoragetesting.NewMockResourcesDBClientWithResources(ctx, resources)
			require.NoError(t, err)

			syncer := newTestRoleAssignmentsSyncer(mockResourcesDB)
			require.NoError(t, syncer.SyncOnce(ctx, testHCPClusterKey))

			after := getRoleAssignmentsStackDesire(t, ctx, mockResourcesDB)
			if !tt.wantDesire {
				assert.Nil(t, after)
				return
			}
			require.NotNil(t, after)
			assert.Equal(t, tt.wantAction, after.Spec.Action)
			if before != nil && tt.existingAction == tt.wantAction {
				assert.Equal(t, before.InstanceVersion, after.InstanceVersion, "an already-requested deletion must not be replaced again")
			}
		})
	}
}

// newTestStackDesireForCluster builds the role-assignments StackDesire for
// the test cluster/managed resource group, with the given spec action.
func newTestStackDesireForCluster(t *testing.T, action string) *coreapi.StackDesire {
	t.Helper()
	resourceID := metadataapi.Must(azcorearm.ParseResourceID(
		"/subscriptions/" + testSubscriptionID +
			"/resourceGroups/" + testResourceGroupName +
			"/providers/Microsoft.RedHatOpenShift/hcpOpenShiftClusters/" + testClusterName +
			"/stackDesires/" + RoleAssignmentsDesireName,
	))
	target := metadataapi.Must(azcorearm.ParseResourceID(testManagedResourceGroupID(t).String() + "/providers/Microsoft.Resources/deploymentStacks/" + RoleAssignmentsStackName))
	return &coreapi.StackDesire{
		CosmosMetadata: coreapi.CosmosMetadata{
			ResourceID:   resourceID,
			PartitionKey: strings.ToLower(resourceID.SubscriptionID),
		},
		Spec: coreapi.StackDesireSpec{
			TargetResourceID: target,
			Authentication: coreapi.StackDesireAuthentication{
				Type:     coreapi.StackDesireAuthenticationTypeFPA,
				TenantID: testTenantID,
			},
			Action: action,
			Stack: &armdeploymentstacks.DeploymentStack{
				Properties: &armdeploymentstacks.DeploymentStackProperties{
					Template: map[string]any{
						"$schema":        "https://schema.management.azure.com/schemas/2019-04-01/deploymentTemplate.json#",
						"contentVersion": "1.0.0.0",
						"resources":      []map[string]any{},
					},
					ActionOnUnmanage: roleAssignmentsActionOnUnmanage(),
					DenySettings: &armdeploymentstacks.DenySettings{
						Mode: ptr.To(armdeploymentstacks.DenySettingsModeNone),
					},
				},
			},
		},
	}
}

// newTestCluster builds an HCPOpenShiftCluster addressable by the mock
// ResourcesDBClient with one control-plane and one data-plane operator identity and
// the given deletion state.
func newTestCluster(deleting bool) *coreapi.Cluster {
	resourceID := metadataapi.Must(azcorearm.ParseResourceID(
		"/subscriptions/" + testSubscriptionID +
			"/resourceGroups/" + testResourceGroupName +
			"/providers/Microsoft.RedHatOpenShift/hcpOpenShiftClusters/" + testClusterName,
	))

	cluster := &coreapi.Cluster{
		CosmosMetadata: coreapi.CosmosMetadata{
			ResourceID:   resourceID,
			PartitionKey: strings.ToLower(resourceID.SubscriptionID),
		},
		TrackedResource: coreapi.TrackedResource{
			Resource: coreapi.Resource{
				ID:   resourceID,
				Name: testClusterName,
				Type: resourceID.ResourceType.String(),
			},
		},
	}
	cluster.CustomerProperties.Platform.ManagedResourceGroup = testManagedRGName
	cluster.CustomerProperties.Platform.OperatorsAuthentication.UserAssignedIdentities.ControlPlaneOperators = map[string]*azcorearm.ResourceID{
		testControlPlaneOperatorName: metadataapi.Must(azcorearm.ParseResourceID(testControlPlaneIdentityID)),
	}
	cluster.CustomerProperties.Platform.OperatorsAuthentication.UserAssignedIdentities.DataPlaneOperators = map[string]*azcorearm.ResourceID{
		testDataPlaneOperatorName: metadataapi.Must(azcorearm.ParseResourceID(testDataPlaneIdentityID)),
	}
	cluster.CustomerProperties.Platform.OperatorsAuthentication.UserAssignedIdentities.ServiceManagedIdentity = metadataapi.Must(azcorearm.ParseResourceID(testServiceManagedIdentityID))
	if deleting {
		cluster.ServiceProviderProperties.DeletionTimestamp = &metav1.Time{Time: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)}
	}
	return cluster
}

// newTestServiceProviderCluster builds a ServiceProviderCluster addressable by the
// mock ResourcesDBClient. When mrgConfirmed is true the managed resource group is
// reflected as confirmed (opening the observation gate); cpResolved / dpResolved
// control whether the control-plane / data-plane operator principal IDs are resolved
// on the status. roleAssignments is the initial Status.RoleAssignments map.
func newTestServiceProviderCluster(t *testing.T, mrgConfirmed, cpResolved, dpResolved bool, roleAssignments map[coreapi.RoleAssignmentKey]*coreapi.RoleAssignmentStatus) *coreapi.ServiceProviderCluster {
	t.Helper()
	resourceID := metadataapi.Must(azcorearm.ParseResourceID(
		"/subscriptions/" + testSubscriptionID +
			"/resourceGroups/" + testResourceGroupName +
			"/providers/Microsoft.RedHatOpenShift/hcpOpenShiftClusters/" + testClusterName +
			"/" + coreapi.ServiceProviderClusterResourceTypeName +
			"/" + coreapi.ServiceProviderClusterResourceName,
	))

	serviceProviderCluster := &coreapi.ServiceProviderCluster{
		CosmosMetadata: coreapi.CosmosMetadata{
			ResourceID:   resourceID,
			PartitionKey: strings.ToLower(resourceID.SubscriptionID),
		},
	}
	if mrgConfirmed {
		serviceProviderCluster.Status.AzureResources.ManagedResourceGroup.AzureResource = testManagedResourceGroupID(t)
	}
	if cpResolved {
		serviceProviderCluster.Status.MSIManagedIdentities.ControlPlaneOperatorsIdentities = map[string]*coreapi.ServiceProviderClusterControlPlaneOperatorIdentity{
			strings.ToLower(testControlPlaneIdentityID): {
				ResourceID:  metadataapi.Must(azcorearm.ParseResourceID(testControlPlaneIdentityID)),
				PrincipalID: ptr.To(testControlPlanePrincipalID),
			},
		}
	}
	if dpResolved {
		serviceProviderCluster.Status.DataPlaneOperatorsManagedIdentities.Identities = map[string]*coreapi.ServiceProviderClusterDataPlaneOperatorManagedIdentity{
			strings.ToLower(testDataPlaneIdentityID): {
				ResourceID:  metadataapi.Must(azcorearm.ParseResourceID(testDataPlaneIdentityID)),
				PrincipalID: ptr.To(testDataPlanePrincipalID),
			},
		}
	}
	serviceProviderCluster.Status.RoleAssignmentsOverManagedResourceGroup = roleAssignments
	return serviceProviderCluster
}

// newTestSubscription builds a Subscription for the SliceSubscriptionLister.
func newTestSubscription(tenantID *string) *coreapi.Subscription {
	subscriptionResourceID := metadataapi.Must(azcorearm.ParseResourceID("/subscriptions/" + testSubscriptionID))
	return &coreapi.Subscription{
		CosmosMetadata: coreapi.CosmosMetadata{
			ResourceID:   subscriptionResourceID,
			PartitionKey: strings.ToLower(subscriptionResourceID.SubscriptionID),
		},
		Properties: &coreapi.SubscriptionProperties{
			TenantId: tenantID,
		},
	}
}

// testManagedResourceGroupID returns the confirmed managed resource group reference
// resource ID used to open the observation gate.
func testManagedResourceGroupID(t *testing.T) *azcorearm.ResourceID {
	t.Helper()
	return metadataapi.Must(coreapihelpers.ToResourceGroupResourceID(testSubscriptionID, testManagedRGName))
}
