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
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/utils/ptr"

	azcorearm "github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"
	"github.com/Azure/azure-sdk-for-go/sdk/data/azcosmos"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/resources/armdeploymentstacks"

	"github.com/Azure/ARO-HCP/backend/pkg/azure/roleassignment"
	"github.com/Azure/ARO-HCP/backend/pkg/utils/controllerutils"
	"github.com/Azure/ARO-HCP/internal/api/coreapi"
	"github.com/Azure/ARO-HCP/internal/api/metadataapi"
	"github.com/Azure/ARO-HCP/internal/azure"
	"github.com/Azure/ARO-HCP/internal/database/cosmosstorage/corecosmosstorage"
	"github.com/Azure/ARO-HCP/internal/database/cosmosstorage/cosmosstorageutils"
	"github.com/Azure/ARO-HCP/internal/database/cosmosstoragetesting/corecosmosstoragetesting"
	"github.com/Azure/ARO-HCP/internal/database/listers/corelisters"
	"github.com/Azure/ARO-HCP/internal/database/listertesting/corelistertesting"
)

const testSubscriptionID = "00000000-0000-0000-0000-000000000000"

var testKey = controllerutils.HCPClusterKey{SubscriptionID: testSubscriptionID, ResourceGroupName: "test-rg", HCPClusterName: "test-cluster"}

type dbStackDesireLister struct {
	corelisters.StackDesireLister
	db corecosmosstorage.ResourcesDBClient
}

func (l dbStackDesireLister) Get(ctx context.Context, sub, rg, cluster, name string) (*coreapi.StackDesire, error) {
	return l.db.StackDesires(sub, rg, cluster).Get(ctx, name)
}

func testInputs() (*coreapi.Cluster, *coreapi.ServiceProviderCluster) {
	id := metadataapi.Must(azcorearm.ParseResourceID("/subscriptions/" + testSubscriptionID + "/resourceGroups/test-rg/providers/Microsoft.RedHatOpenShift/hcpOpenShiftClusters/test-cluster"))
	cluster := &coreapi.Cluster{
		CosmosMetadata:  coreapi.CosmosMetadata{ResourceID: id, PartitionKey: testSubscriptionID},
		TrackedResource: coreapi.TrackedResource{Resource: coreapi.Resource{ID: id, Name: id.Name, Type: id.ResourceType.String()}, Location: "eastus"},
	}
	cluster.CustomerProperties.Platform.ManagedResourceGroup = "managed-rg"
	cp := metadataapi.Must(azcorearm.ParseResourceID("/subscriptions/" + testSubscriptionID + "/resourceGroups/test-rg/providers/Microsoft.ManagedIdentity/userAssignedIdentities/cp"))
	dp := metadataapi.Must(azcorearm.ParseResourceID("/subscriptions/" + testSubscriptionID + "/resourceGroups/test-rg/providers/Microsoft.ManagedIdentity/userAssignedIdentities/dp"))
	smi := metadataapi.Must(azcorearm.ParseResourceID("/subscriptions/" + testSubscriptionID + "/resourceGroups/test-rg/providers/Microsoft.ManagedIdentity/userAssignedIdentities/smi"))
	identities := &cluster.CustomerProperties.Platform.OperatorsAuthentication.UserAssignedIdentities
	identities.ControlPlaneOperators = map[string]*azcorearm.ResourceID{string(azure.ClusterOperatorIdentifierControlPlane): cp}
	identities.DataPlaneOperators = map[string]*azcorearm.ResourceID{string(azure.ClusterOperatorIdentifierDiskCSIDriver): dp}
	identities.ServiceManagedIdentity = smi
	spc := &coreapi.ServiceProviderCluster{CosmosMetadata: coreapi.CosmosMetadata{
		ResourceID:   metadataapi.Must(azcorearm.ParseResourceID(id.String() + "/" + coreapi.ServiceProviderClusterResourceTypeName + "/" + coreapi.ServiceProviderClusterResourceName)),
		PartitionKey: testSubscriptionID,
	}}
	spc.Status.AzureResources.ManagedResourceGroup.AzureResource = metadataapi.Must(azcorearm.ParseResourceID("/subscriptions/" + testSubscriptionID + "/resourceGroups/managed-rg"))
	spc.Status.MSIManagedIdentities.ControlPlaneOperatorsIdentities = map[string]*coreapi.ServiceProviderClusterControlPlaneOperatorIdentity{
		strings.ToLower(cp.String()): {ResourceID: cp, PrincipalID: ptr.To("11111111-1111-1111-1111-111111111111")},
	}
	spc.Status.DataPlaneOperatorsManagedIdentities.Identities = map[string]*coreapi.ServiceProviderClusterDataPlaneOperatorManagedIdentity{
		strings.ToLower(dp.String()): {ResourceID: dp, PrincipalID: ptr.To("22222222-2222-2222-2222-222222222222")},
	}
	spc.Status.MSIManagedIdentities.ServiceManagedIdentity = &coreapi.ServiceProviderClusterServiceManagedIdentity{ResourceID: smi, PrincipalID: ptr.To("33333333-3333-3333-3333-333333333333")}
	return cluster, spc
}

func testSyncer(t *testing.T, resources ...any) (*roleAssignmentsSyncer, *corecosmosstoragetesting.MockResourcesDBClient) {
	t.Helper()
	db, err := corecosmosstoragetesting.NewMockResourcesDBClientWithResources(t.Context(), resources)
	require.NoError(t, err)
	return &roleAssignmentsSyncer{
		resourcesDBClient:            db,
		clusterLister:                &corelistertesting.DBClusterLister{ResourcesDBClient: db},
		serviceProviderClusterLister: &corelistertesting.DBServiceProviderClusterLister{ResourcesDBClient: db},
		stackDesireLister:            dbStackDesireLister{db: db},
		subscriptionLister: &corelistertesting.SliceSubscriptionLister{Subscriptions: []*coreapi.Subscription{{
			CosmosMetadata: coreapi.CosmosMetadata{ResourceID: metadataapi.Must(azcorearm.ParseResourceID("/subscriptions/" + testSubscriptionID))},
			Properties:     &coreapi.SubscriptionProperties{TenantId: ptr.To("customer-tenant")},
		}}},
		clusterScopedIdentitiesConfig: azure.NewClusterScopedIdentitiesConfig(azure.RoleDefinitionConfigSetNameDev),
	}, db
}

func TestStackTemplateAdoptsExistingClusterAndNoOp(t *testing.T) {
	cluster, spc := testInputs()
	legacy := []*azcorearm.ResourceID{metadataapi.Must(azcorearm.ParseResourceID(spc.Status.AzureResources.ManagedResourceGroup.AzureResource.String() + "/providers/Microsoft.Authorization/roleAssignments/historical"))}
	spc.Status.AzureResources.RoleAssignments.AzureResources = legacy
	spc.Spec.EarliestRecheckTimesByController = map[string]*metav1.Time{RoleAssignmentsControllerName: {Time: time.Now().Add(24 * time.Hour)}}
	syncer, db := testSyncer(t, cluster, spc)
	require.NoError(t, syncer.SyncOnce(t.Context(), testKey))
	desires := db.StackDesires(testKey.SubscriptionID, testKey.ResourceGroupName, testKey.HCPClusterName)
	desire, err := desires.Get(t.Context(), RoleAssignmentsDesireName)
	require.NoError(t, err)
	require.Equal(t, "Apply", desire.Spec.Action)
	require.Equal(t, "FPA", desire.Spec.Authentication.Type)
	require.Equal(t, "customer-tenant", desire.Spec.Authentication.TenantID)
	require.Equal(t, 6*time.Hour, desire.Spec.ReconcileInterval.Duration)
	require.Equal(t, spc.Status.AzureResources.ManagedResourceGroup.AzureResource.String()+"/providers/Microsoft.Resources/deploymentStacks/aro-hcp-role-assignments", desire.Spec.TargetResourceID.String())
	require.Equal(t, cluster.ID.String()+"/"+coreapi.StackDesireResourceTypeName+"/"+RoleAssignmentsDesireName, desire.ResourceID.String())
	require.NotNil(t, desire.ParentResourceID)
	require.Equal(t, strings.ToLower(cluster.ID.String()), strings.ToLower(desire.ParentResourceID.String()))
	require.Equal(t, strings.ToLower(cluster.ID.SubscriptionID), desire.PartitionKey)
	require.NotNil(t, desire.Spec.Stack)
	require.Nil(t, desire.Spec.Stack.Location, "RG-scoped stack must not override the resource group's location")
	properties := desire.Spec.Stack.Properties
	require.NotNil(t, properties)
	require.Equal(t, &armdeploymentstacks.ActionOnUnmanage{
		Resources:        ptr.To(armdeploymentstacks.DeploymentStacksDeleteDetachEnumDelete),
		ResourceGroups:   ptr.To(armdeploymentstacks.DeploymentStacksDeleteDetachEnumDetach),
		ManagementGroups: ptr.To(armdeploymentstacks.DeploymentStacksDeleteDetachEnumDetach),
	}, properties.ActionOnUnmanage)
	require.Equal(t, &armdeploymentstacks.DenySettings{Mode: ptr.To(armdeploymentstacks.DenySettingsModeNone)}, properties.DenySettings)
	template := decodeTemplate(t, desire.Spec.Stack)
	require.Equal(t, "https://schema.management.azure.com/schemas/2019-04-01/deploymentTemplate.json#", template.Schema)
	require.Equal(t, "1.0.0.0", template.ContentVersion)
	expected, err := syncer.expectedRoleAssignments(cluster, spc)
	require.NoError(t, err)
	require.Len(t, template.Resources, len(expected))
	principals := map[string]bool{}
	for i, resource := range template.Resources {
		require.Equal(t, "Microsoft.Authorization/roleAssignments", resource.Type)
		require.Equal(t, "2022-04-01", resource.APIVersion)
		require.Equal(t, "ServicePrincipal", resource.Properties.PrincipalType)
		require.Equal(t, expected[i].principalID, resource.Properties.PrincipalID)
		require.Equal(t, expected[i].roleDefinitionID, resource.Properties.RoleDefinitionID)
		require.Equal(t, roleassignment.GenerateManagedResourceGroupScopedRoleAssignmentName(spc.Status.AzureResources.ManagedResourceGroup.AzureResource.String(), resource.Properties.PrincipalID, resource.Properties.RoleDefinitionID), resource.Name)
		principals[resource.Properties.PrincipalID] = true
	}
	require.Len(t, principals, 3, "template includes CP, DP and service identities")
	stored, err := db.ServiceProviderClusters(testKey.SubscriptionID, testKey.ResourceGroupName, testKey.HCPClusterName).Get(t.Context(), coreapi.ServiceProviderClusterResourceName)
	require.NoError(t, err)
	require.False(t, stored.Status.AzureResources.RoleAssignments.StackReady)
	require.Equal(t, legacy, stored.Status.AzureResources.RoleAssignments.AzureResources, "legacy lists remain untouched")
	for range 10 {
		require.NoError(t, syncer.SyncOnce(t.Context(), testKey))
	}
	unchanged, err := desires.Get(t.Context(), RoleAssignmentsDesireName)
	require.NoError(t, err)
	require.Equal(t, desire, unchanged, "identical intent must not change the desire ETag or content")
	unchangedSPC, err := db.ServiceProviderClusters(testKey.SubscriptionID, testKey.ResourceGroupName, testKey.HCPClusterName).Get(t.Context(), coreapi.ServiceProviderClusterResourceName)
	require.NoError(t, err)
	require.Equal(t, stored, unchangedSPC, "unchanged readiness must not write SPC")
}

func TestStackReadiness(t *testing.T) {
	applied := []metav1.Condition{{Type: coreapi.StackDesireConditionSuccessfullyApplied, Status: metav1.ConditionTrue}}
	succeeded := &armdeploymentstacks.DeploymentStack{Properties: &armdeploymentstacks.DeploymentStackProperties{ProvisioningState: ptr.To(armdeploymentstacks.DeploymentStackProvisioningStateSucceeded)}}
	for _, tc := range []struct {
		name            string
		observed        bool
		changeSpec      bool
		changePersisted bool
		status          coreapi.StackDesireStatus
		want            bool
	}{
		{name: "pending without success"},
		{name: "current hash succeeded", observed: true, status: coreapi.StackDesireStatus{Stack: succeeded, Conditions: applied}, want: true},
		{name: "Azure provisioning state casing", observed: true, status: coreapi.StackDesireStatus{Stack: &armdeploymentstacks.DeploymentStack{Properties: &armdeploymentstacks.DeploymentStackProperties{ProvisioningState: ptr.To(armdeploymentstacks.DeploymentStackProvisioningState("Succeeded"))}}, Conditions: applied}, want: true},
		{name: "older hash cannot satisfy new intent", observed: true, changeSpec: true, status: coreapi.StackDesireStatus{Stack: succeeded, Conditions: applied}},
		{name: "persisted spec must also match intent", observed: true, changePersisted: true, status: coreapi.StackDesireStatus{Stack: succeeded, Conditions: applied}},
		{name: "unobserved spec cannot satisfy intent", status: coreapi.StackDesireStatus{Stack: succeeded, Conditions: applied}},
		{name: "different observed hash cannot satisfy intent", status: coreapi.StackDesireStatus{ObservedSpecHash: "other", Stack: succeeded, Conditions: applied}},
		{name: "same hash repair pending revokes success", observed: true, status: coreapi.StackDesireStatus{Stack: &armdeploymentstacks.DeploymentStack{Properties: &armdeploymentstacks.DeploymentStackProperties{ProvisioningState: ptr.To(armdeploymentstacks.DeploymentStackProvisioningStateDeploying)}}, Conditions: applied}},
		{name: "same hash repair failed revokes success", observed: true, status: coreapi.StackDesireStatus{Stack: &armdeploymentstacks.DeploymentStack{Properties: &armdeploymentstacks.DeploymentStackProperties{ProvisioningState: ptr.To(armdeploymentstacks.DeploymentStackProvisioningStateFailed)}}, Conditions: applied}},
		{name: "error outcome revokes success", observed: true, status: coreapi.StackDesireStatus{Error: &armdeploymentstacks.ErrorDetail{Code: ptr.To("AuthorizationFailed")}, Conditions: []metav1.Condition{{Type: coreapi.StackDesireConditionDegraded, Status: metav1.ConditionTrue}}}},
		{name: "missing success condition", observed: true, status: coreapi.StackDesireStatus{Stack: succeeded}},
		{name: "false success condition", observed: true, status: coreapi.StackDesireStatus{Stack: succeeded, Conditions: []metav1.Condition{{Type: coreapi.StackDesireConditionSuccessfullyApplied, Status: metav1.ConditionFalse}}}},
		{name: "unknown success condition", observed: true, status: coreapi.StackDesireStatus{Stack: succeeded, Conditions: []metav1.Condition{{Type: coreapi.StackDesireConditionSuccessfullyApplied, Status: metav1.ConditionUnknown}}}},
		{name: "deleted is not applied", observed: true, status: coreapi.StackDesireStatus{Conditions: []metav1.Condition{{Type: coreapi.StackDesireConditionSuccessfullyDeleted, Status: metav1.ConditionTrue}}}},
		{name: "absent stack", observed: true, status: coreapi.StackDesireStatus{Conditions: applied}},
		{name: "absent stack properties", observed: true, status: coreapi.StackDesireStatus{Stack: &armdeploymentstacks.DeploymentStack{}, Conditions: applied}},
		{name: "absent provisioning state", observed: true, status: coreapi.StackDesireStatus{Stack: &armdeploymentstacks.DeploymentStack{Properties: &armdeploymentstacks.DeploymentStackProperties{}}, Conditions: applied}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cluster, spc := testInputs()
			syncer, db := testSyncer(t, cluster, spc)
			require.NoError(t, syncer.SyncOnce(t.Context(), testKey))
			desires := db.StackDesires(testKey.SubscriptionID, testKey.ResourceGroupName, testKey.HCPClusterName)
			desire, err := desires.Get(t.Context(), RoleAssignmentsDesireName)
			require.NoError(t, err)
			desire.Status = *tc.status.DeepCopy()
			if tc.observed {
				desire.Status.ObservedSpecHash, err = coreapi.StackDesireSpecHash(desire.Spec)
				require.NoError(t, err)
				desire.Status.ObservedAt = ptr.To(metav1.Now())
			}
			if tc.changePersisted {
				// Simulate A -> B while status still describes A and the producer wants A again.
				desire.Spec.ReconcileInterval.Duration = time.Hour
			}
			desire, err = desires.Replace(t.Context(), desire, nil)
			require.NoError(t, err)
			if tc.changeSpec {
				spc.Status.MSIManagedIdentities.ServiceManagedIdentity.PrincipalID = ptr.To("44444444-4444-4444-4444-444444444444")
			}
			// Exercise both promotion and revocation, not just an unchanged bool.
			spc.Status.AzureResources.RoleAssignments.StackReady = !tc.want
			stored, err := db.ServiceProviderClusters(testKey.SubscriptionID, testKey.ResourceGroupName, testKey.HCPClusterName).Get(t.Context(), coreapi.ServiceProviderClusterResourceName)
			require.NoError(t, err)
			spc.CosmosMetadata = stored.CosmosMetadata
			_, err = db.ServiceProviderClusters(testKey.SubscriptionID, testKey.ResourceGroupName, testKey.HCPClusterName).Replace(t.Context(), spc, nil)
			require.NoError(t, err)
			require.NoError(t, syncer.SyncOnce(t.Context(), testKey))
			stored, err = db.ServiceProviderClusters(testKey.SubscriptionID, testKey.ResourceGroupName, testKey.HCPClusterName).Get(t.Context(), coreapi.ServiceProviderClusterResourceName)
			require.NoError(t, err)
			require.Equal(t, tc.want, stored.Status.AzureResources.RoleAssignments.StackReady)
			updated, err := desires.Get(t.Context(), RoleAssignmentsDesireName)
			require.NoError(t, err)
			require.Equal(t, desire.Status, updated.Status, "producer must preserve applier status")
			if tc.changeSpec {
				require.NotEqual(t, decodeTemplate(t, desire.Spec.Stack), decodeTemplate(t, updated.Spec.Stack))
			}
		})
	}
}

type typedTemplate struct {
	Schema         string `json:"$schema"`
	ContentVersion string
	Resources      []struct {
		Type       string
		APIVersion string
		Name       string
		Properties struct{ PrincipalID, RoleDefinitionID, PrincipalType string }
	}
}

func decodeTemplate(t *testing.T, stack *armdeploymentstacks.DeploymentStack) typedTemplate {
	t.Helper()
	require.NotNil(t, stack)
	require.NotNil(t, stack.Properties)
	data, err := json.Marshal(stack.Properties.Template)
	require.NoError(t, err)
	var template typedTemplate
	require.NoError(t, json.Unmarshal(data, &template))
	return template
}

func TestUnresolvedInputsNeverPublishPartialTemplate(t *testing.T) {
	for _, tc := range []struct {
		name      string
		mutate    func(*roleAssignmentsSyncer, *coreapi.Cluster, *coreapi.ServiceProviderCluster)
		wantError bool
	}{
		{name: "MRG absent", mutate: func(_ *roleAssignmentsSyncer, _ *coreapi.Cluster, spc *coreapi.ServiceProviderCluster) {
			spc.Status.AzureResources.ManagedResourceGroup.AzureResource = nil
		}},
		{name: "CP unresolved", mutate: func(_ *roleAssignmentsSyncer, _ *coreapi.Cluster, spc *coreapi.ServiceProviderCluster) {
			spc.Status.MSIManagedIdentities.ControlPlaneOperatorsIdentities = nil
		}},
		{name: "DP unresolved", mutate: func(_ *roleAssignmentsSyncer, _ *coreapi.Cluster, spc *coreapi.ServiceProviderCluster) {
			spc.Status.DataPlaneOperatorsManagedIdentities.Identities = nil
		}},
		{name: "SMI unresolved", mutate: func(_ *roleAssignmentsSyncer, _ *coreapi.Cluster, spc *coreapi.ServiceProviderCluster) {
			spc.Status.MSIManagedIdentities.ServiceManagedIdentity.PrincipalID = ptr.To("")
		}},
		{name: "SMI replaced", mutate: func(_ *roleAssignmentsSyncer, cluster *coreapi.Cluster, _ *coreapi.ServiceProviderCluster) {
			cluster.CustomerProperties.Platform.OperatorsAuthentication.UserAssignedIdentities.ServiceManagedIdentity = metadataapi.Must(azcorearm.ParseResourceID(cluster.CustomerProperties.Platform.OperatorsAuthentication.UserAssignedIdentities.ServiceManagedIdentity.String() + "-new"))
		}},
		{name: "missing config", mutate: func(syncer *roleAssignmentsSyncer, _ *coreapi.Cluster, _ *coreapi.ServiceProviderCluster) {
			syncer.clusterScopedIdentitiesConfig.ServiceManagedIdentity = nil
		}, wantError: true},
		{name: "tenant unresolved", mutate: func(syncer *roleAssignmentsSyncer, _ *coreapi.Cluster, _ *coreapi.ServiceProviderCluster) {
			syncer.subscriptionLister = &corelistertesting.SliceSubscriptionLister{}
		}, wantError: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cluster, spc := testInputs()
			syncer, db := testSyncer(t, cluster, spc)
			require.NoError(t, syncer.SyncOnce(t.Context(), testKey))
			desires := db.StackDesires(testKey.SubscriptionID, testKey.ResourceGroupName, testKey.HCPClusterName)
			before, err := desires.Get(t.Context(), RoleAssignmentsDesireName)
			require.NoError(t, err)
			spc.Status.AzureResources.RoleAssignments.StackReady = true
			tc.mutate(syncer, cluster, spc)
			syncer.clusterLister = &corelistertesting.SliceClusterLister{Clusters: []*coreapi.Cluster{cluster}}
			stored, err := db.ServiceProviderClusters(testKey.SubscriptionID, testKey.ResourceGroupName, testKey.HCPClusterName).Get(t.Context(), coreapi.ServiceProviderClusterResourceName)
			require.NoError(t, err)
			spc.CosmosMetadata = stored.CosmosMetadata
			_, err = db.ServiceProviderClusters(testKey.SubscriptionID, testKey.ResourceGroupName, testKey.HCPClusterName).Replace(t.Context(), spc, nil)
			require.NoError(t, err)
			err = syncer.SyncOnce(t.Context(), testKey)
			if tc.wantError {
				require.Error(t, err)
			} else {
				require.NoError(t, err)
			}
			after, err := desires.Get(t.Context(), RoleAssignmentsDesireName)
			require.NoError(t, err)
			require.Equal(t, before, after, "unresolved inputs must not replace the last complete template")
			stored, err = db.ServiceProviderClusters(testKey.SubscriptionID, testKey.ResourceGroupName, testKey.HCPClusterName).Get(t.Context(), coreapi.ServiceProviderClusterResourceName)
			require.NoError(t, err)
			require.False(t, stored.Status.AzureResources.RoleAssignments.StackReady)
			require.NoError(t, desires.Delete(t.Context(), RoleAssignmentsDesireName))
			_ = syncer.SyncOnce(t.Context(), testKey)
			_, err = desires.Get(t.Context(), RoleAssignmentsDesireName)
			require.True(t, cosmosstorageutils.IsNotFoundError(err), "unresolved inputs must not create a partial desire either")
		})
	}
}

func TestDeletionPurgesDesireWithoutPrerequisites(t *testing.T) {
	cluster, spc := testInputs()
	syncer, db := testSyncer(t, cluster, spc)
	require.NoError(t, syncer.SyncOnce(t.Context(), testKey))
	cluster.ServiceProviderProperties.DeletionTimestamp = ptr.To(metav1.Now())
	syncer.clusterLister = &corelistertesting.SliceClusterLister{Clusters: []*coreapi.Cluster{cluster}}
	syncer.serviceProviderClusterLister = nil
	syncer.subscriptionLister = nil
	for range 2 {
		require.NoError(t, syncer.SyncOnce(t.Context(), testKey))
		_, err := db.StackDesires(testKey.SubscriptionID, testKey.ResourceGroupName, testKey.HCPClusterName).Get(t.Context(), RoleAssignmentsDesireName)
		require.True(t, cosmosstorageutils.IsNotFoundError(err))
	}
}

func TestLiveParentRecheckBeforePublishing(t *testing.T) {
	for _, existing := range []bool{false, true} {
		for _, missing := range []bool{false, true} {
			t.Run(fmt.Sprintf("existing=%t/missing=%t", existing, missing), func(t *testing.T) {
				cluster, spc := testInputs()
				syncer, db := testSyncer(t, cluster, spc)
				if existing {
					require.NoError(t, syncer.SyncOnce(t.Context(), testKey))
					spc.Status.MSIManagedIdentities.ServiceManagedIdentity.PrincipalID = ptr.To("44444444-4444-4444-4444-444444444444")
					syncer.serviceProviderClusterLister = &corelistertesting.SliceServiceProviderClusterLister{ServiceProviderClusters: []*coreapi.ServiceProviderCluster{spc}}
				}
				// The informer still sees a live cluster while Cosmos has moved on.
				syncer.clusterLister = &corelistertesting.SliceClusterLister{Clusters: []*coreapi.Cluster{cluster}}
				clusters := db.HCPClusters(testKey.SubscriptionID, testKey.ResourceGroupName)
				if missing {
					require.NoError(t, clusters.Delete(t.Context(), testKey.HCPClusterName))
				} else {
					live, err := clusters.Get(t.Context(), testKey.HCPClusterName)
					require.NoError(t, err)
					live.ServiceProviderProperties.DeletionTimestamp = ptr.To(metav1.Now())
					_, err = clusters.Replace(t.Context(), live, nil)
					require.NoError(t, err)
				}
				require.NoError(t, syncer.SyncOnce(t.Context(), testKey))
				_, err := db.StackDesires(testKey.SubscriptionID, testKey.ResourceGroupName, testKey.HCPClusterName).Get(t.Context(), RoleAssignmentsDesireName)
				require.True(t, cosmosstorageutils.IsNotFoundError(err), "live deletion must prevent Create/Replace and purge any existing desire")
			})
		}
	}
}

func TestExpectedAssignmentsStableAndDeduplicated(t *testing.T) {
	cluster, spc := testInputs()
	syncer, _ := testSyncer(t)
	identities := cluster.CustomerProperties.Platform.OperatorsAuthentication.UserAssignedIdentities.ControlPlaneOperators
	var sharedID *azcorearm.ResourceID
	for _, id := range identities {
		sharedID = id
	}
	for name, config := range syncer.clusterScopedIdentitiesConfig.ControlPlaneOperatorsIdentities {
		if config != nil && len(config.RoleDefinitionsResourceIDs()) > 0 {
			identities[string(name)] = sharedID
		}
	}
	first, err := syncer.desiredSpec(t.Context(), cluster, spc)
	require.NoError(t, err)
	for range 64 {
		next, err := syncer.desiredSpec(t.Context(), cluster, spc)
		require.NoError(t, err)
		require.Equal(t, first, next)
	}
	assignments, err := syncer.expectedRoleAssignments(cluster, spc)
	require.NoError(t, err)
	seen := map[string]bool{}
	previous := ""
	for _, assignment := range assignments {
		id := strings.ToLower(assignment.resourceID.String())
		require.False(t, seen[id], "shared principals and roles must be deduplicated")
		require.Greater(t, id, previous, "stable order uses canonical resource IDs")
		seen[id] = true
		previous = id
	}
}

type conflictingSPCDB struct {
	corecosmosstorage.ResourcesDBClient
}
type conflictingSPCCRUD struct {
	cosmosstorageutils.ResourceCRUD[coreapi.ServiceProviderCluster, *coreapi.ServiceProviderCluster]
}

func (d conflictingSPCDB) ServiceProviderClusters(sub, rg, cluster string) cosmosstorageutils.ResourceCRUD[coreapi.ServiceProviderCluster, *coreapi.ServiceProviderCluster] {
	return conflictingSPCCRUD{d.ResourcesDBClient.ServiceProviderClusters(sub, rg, cluster)}
}
func (conflictingSPCCRUD) Replace(context.Context, *coreapi.ServiceProviderCluster, *azcosmos.ItemOptions) (*coreapi.ServiceProviderCluster, error) {
	return nil, corecosmosstoragetesting.NewPreconditionFailedError()
}

func TestReadinessConflictDoesNotPublishNewIntent(t *testing.T) {
	cluster, spc := testInputs()
	spc.Status.AzureResources.RoleAssignments.StackReady = true
	syncer, db := testSyncer(t, cluster, spc)
	syncer.resourcesDBClient = conflictingSPCDB{db}
	require.Error(t, syncer.SyncOnce(t.Context(), testKey))
	_, err := db.StackDesires(testKey.SubscriptionID, testKey.ResourceGroupName, testKey.HCPClusterName).Get(t.Context(), RoleAssignmentsDesireName)
	require.True(t, cosmosstorageutils.IsNotFoundError(err), "must revoke stale readiness before publishing")
}
