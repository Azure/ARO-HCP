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

package operations

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	utilsclock "k8s.io/utils/clock"
	"k8s.io/utils/ptr"

	azcorearm "github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/resources/armdeploymentstacks/v2"

	"github.com/Azure/ARO-HCP/backend/pkg/controllers/cluster/azureresources"
	"github.com/Azure/ARO-HCP/backend/pkg/utils/controllerutils"
	operationbase "github.com/Azure/ARO-HCP/backend/pkg/utils/operationutils"
	"github.com/Azure/ARO-HCP/internal/api/azureresourcesapi"
	"github.com/Azure/ARO-HCP/internal/api/coreapi"
	"github.com/Azure/ARO-HCP/internal/api/metadataapi"
	"github.com/Azure/ARO-HCP/internal/apihelpers/azureresourcesapihelpers"
	"github.com/Azure/ARO-HCP/internal/database/cosmosstorage/cosmosstorageutils"
	"github.com/Azure/ARO-HCP/internal/database/cosmosstoragetesting/corecosmosstoragetesting"
	"github.com/Azure/ARO-HCP/internal/database/listers/azureresourceslisters"
	"github.com/Azure/ARO-HCP/internal/database/listertesting/corelistertesting"
)

func skipNodePoolEgressForUnrelatedChecks(controller *operationClusterCreate) {
	if lister, ok := controller.clusterLister.(*corelistertesting.SliceClusterLister); ok {
		for _, cluster := range lister.Clusters {
			if cluster != nil {
				cluster.CustomerProperties.Platform.OutboundType = metadataapi.OutboundType("UserDefinedRouting")
			}
		}
	}
}

type nodePoolEgressStackLister struct {
	azureresourceslisters.DeploymentStackDesireLister
	desire *azureresourcesapi.DeploymentStackDesire
}

func (lister *nodePoolEgressStackLister) GetForCluster(context.Context, string, string, string, string) (*azureresourcesapi.DeploymentStackDesire, error) {
	if lister.desire == nil {
		return nil, cosmosstorageutils.NewNotFoundError()
	}
	return lister.desire, nil
}

func TestClusterCreateNodePoolEgressPublicIPs(t *testing.T) {
	id := metadataapi.Must(azcorearm.ParseResourceID("/subscriptions/sub/resourceGroups/rg/providers/Microsoft.RedHatOpenShift/hcpOpenShiftClusters/cluster"))
	cluster := &coreapi.Cluster{CosmosMetadata: coreapi.CosmosMetadata{ResourceID: id}, TrackedResource: coreapi.TrackedResource{Resource: coreapi.Resource{ID: id}, Location: "eastus"}}
	cluster.ServiceProviderProperties.ClusterServiceID = ptr.To(metadataapi.Must(metadataapi.NewInternalID("/api/aro_hcp/v1alpha1/clusters/cs-cluster")))
	cluster.CustomerProperties.Platform.ManagedResourceGroup = "mrg"
	spc := &coreapi.ServiceProviderCluster{CosmosMetadata: coreapi.CosmosMetadata{ResourceID: metadataapi.Must(azcorearm.ParseResourceID(id.String() + "/serviceProviderClusters/default"))}}
	spc.Status.AzureResources.ManagedResourceGroup.AzureResource = metadataapi.Must(azcorearm.ParseResourceID("/subscriptions/sub/resourceGroups/mrg"))
	for index := 0; index < 9; index++ {
		spc.Status.ReservedNodePoolEgressPublicIPResourceIDs = append(spc.Status.ReservedNodePoolEgressPublicIPResourceIDs, metadataapi.Must(azcorearm.ParseResourceID(fmt.Sprintf("/subscriptions/sub/resourceGroups/mrg/providers/Microsoft.Network/publicIPAddresses/cs-cluster-%d", index))))
	}
	subscription := &coreapi.Subscription{CosmosMetadata: coreapi.CosmosMetadata{ResourceID: metadataapi.Must(azcorearm.ParseResourceID("/subscriptions/sub"))}}
	subscription.Properties = &coreapi.SubscriptionProperties{TenantId: ptr.To("tenant")}
	spec, err := azureresources.NodePoolEgressPublicIPsDesiredSpec(cluster, spc, subscription)
	require.NoError(t, err)
	require.NotNil(t, spec)
	hash, err := azureresourcesapihelpers.DeploymentStackDesireSpecHash(*spec)
	require.NoError(t, err)
	lister := &nodePoolEgressStackLister{}
	check := &clusterCreateNodePoolEgressPublicIPsCheck{clusterLister: &corelistertesting.SliceClusterLister{Clusters: []*coreapi.Cluster{cluster}}, serviceProviderClusterLister: &corelistertesting.SliceServiceProviderClusterLister{ServiceProviderClusters: []*coreapi.ServiceProviderCluster{spc}}, subscriptionLister: &corelistertesting.SliceSubscriptionLister{Subscriptions: []*coreapi.Subscription{subscription}}, desireLister: lister}
	operation := &coreapi.Operation{ExternalID: id}
	var recoverDesire *azureresourcesapi.DeploymentStackDesire
	assertPersisted := func(expected *operationbase.OperationState, expired bool) {
		t.Helper()
		clusterCopy := cluster.DeepCopy()
		clusterCopy.PartitionKey = "sub"
		clusterCopy.ServiceProviderProperties.ActiveOperationID = "op"
		if expired {
			clusterCopy.ServiceProviderProperties.CreateOperationCompletionDeadline = ptr.To(metav1.NewTime(time.Now().Add(-time.Hour)))
		}
		operationCopy := &coreapi.Operation{
			CosmosMetadata: coreapi.CosmosMetadata{ResourceID: metadataapi.Must(azcorearm.ParseResourceID("/subscriptions/sub/providers/Microsoft.RedHatOpenShift/hcpOperationStatuses/op")), PartitionKey: "sub"},
			ExternalID:     id, OperationID: metadataapi.Must(azcorearm.ParseResourceID("/subscriptions/sub/providers/Microsoft.RedHatOpenShift/locations/eastus/operationstatuses/op")),
			Request: cosmosstorageutils.OperationRequestCreate, Status: coreapi.ProvisioningStateAccepted,
		}
		db, err := corecosmosstoragetesting.NewMockResourcesDBClientWithResources(t.Context(), []any{clusterCopy, operationCopy})
		require.NoError(t, err)
		stored, err := db.Operations("sub").Get(t.Context(), "op")
		require.NoError(t, err)
		calculators, err := operationbase.NewOperationStatusCalculators[struct{}](check)
		require.NoError(t, err)
		controller := &operationClusterCreate{clock: utilsclock.RealClock{}, resourcesDBClient: db, statusCalculators: calculators,
			clusterLister:         &corelistertesting.SliceClusterLister{Clusters: []*coreapi.Cluster{clusterCopy}},
			activeOperationLister: &corelistertesting.SliceActiveOperationLister{Operations: []*coreapi.Operation{stored}},
		}
		require.NoError(t, controller.SynchronizeOperation(t.Context(), controllerutils.OperationKey{SubscriptionID: "sub", OperationName: "op", ParentResourceID: id.String()}))
		updated, err := db.Operations("sub").Get(t.Context(), "op")
		require.NoError(t, err)
		require.Equal(t, expected.ProvisioningState, updated.Status)
		if expected.ProvisioningState == coreapi.ProvisioningStateFailed {
			require.NotNil(t, updated.Error)
			require.Equal(t, expected.CloudErrorCode, updated.Error.Code)
			if expected.Error != nil {
				require.Equal(t, expected.Error, updated.Error)
			} else {
				require.Contains(t, updated.Error.Message, "node pool public IPs not confirmed")
				for _, diagnostic := range []string{"AuthorizationFailed", "resource permission missing", "publicIPAddresses/cs-cluster-0", "role missing"} {
					if strings.Contains(expected.Message, diagnostic) {
						require.Contains(t, updated.Error.Message, diagnostic)
					}
				}
			}
		} else {
			require.Nil(t, updated.Error)
		}
		if recoverDesire != nil && !expired {
			require.Equal(t, coreapi.ProvisioningStateProvisioning, updated.Status)
			require.True(t, controller.ShouldProcess(t.Context(), updated))
			previous := lister.desire
			lister.desire = recoverDesire.DeepCopy()
			controller.activeOperationLister = &corelistertesting.SliceActiveOperationLister{Operations: []*coreapi.Operation{updated}}
			require.NoError(t, controller.SynchronizeOperation(t.Context(), controllerutils.OperationKey{SubscriptionID: "sub", OperationName: "op", ParentResourceID: id.String()}))
			recovered, err := db.Operations("sub").Get(t.Context(), "op")
			require.NoError(t, err)
			require.Equal(t, coreapi.ProvisioningStateSucceeded, recovered.Status)
			require.Nil(t, recovered.Error)
			lister.desire = previous
		}
	}
	assertState := func(expected coreapi.ProvisioningState) *operationbase.OperationState {
		t.Helper()
		state, err := check.CalculateOperationStatus(t.Context(), operation, struct{}{})
		require.NoError(t, err)
		require.Equal(t, expected, state.ProvisioningState)
		assertPersisted(state, false)
		return state
	}
	require.Equal(t, "node pool public IPs not confirmed", assertState(coreapi.ProvisioningStateProvisioning).Message)
	lister.desire = &azureresourcesapi.DeploymentStackDesire{Spec: *spec.DeepCopy(), Status: azureresourcesapi.DeploymentStackDesireStatus{ObservedSpecHash: hash, Conditions: []metav1.Condition{{Type: azureresourcesapi.DeploymentStackDesireConditionTypeSuccessfullyApplied, Status: metav1.ConditionFalse, Reason: "WaitingForCompletion"}}}}
	assertState(coreapi.ProvisioningStateProvisioning)
	lister.desire.Status.Conditions[0].Status = metav1.ConditionTrue
	assertState(coreapi.ProvisioningStateProvisioning)
	lister.desire.Status.Stack = &armdeploymentstacks.DeploymentStack{Properties: &armdeploymentstacks.DeploymentStackProperties{ProvisioningState: ptr.To(armdeploymentstacks.DeploymentStackProvisioningStateSucceeded)}}
	for _, resourceID := range spc.Status.ReservedNodePoolEgressPublicIPResourceIDs {
		lister.desire.Status.Stack.Properties.Resources = append(lister.desire.Status.Stack.Properties.Resources, &armdeploymentstacks.ManagedResourceReference{ID: ptr.To(resourceID.String()), Status: ptr.To(armdeploymentstacks.ResourceStatusModeManaged)})
	}
	assertState(coreapi.ProvisioningStateSucceeded)
	success := lister.desire.DeepCopy()
	for _, coverage := range []string{"missing stack", "missing properties", "missing resources", "subset", "wrong ID", "duplicates", "nil entry", "nil ID", "nil status", "delete failed", "remove deny failed", "unknown status", "conflicting duplicate", "deleted only", "missing observed hash", "extra records", "case insensitive"} {
		t.Run("managed coverage/"+coverage, func(t *testing.T) {
			lister.desire = success.DeepCopy()
			properties := lister.desire.Status.Stack.Properties
			expected := coreapi.ProvisioningStateProvisioning
			switch coverage {
			case "missing stack":
				lister.desire.Status.Stack = nil
			case "missing properties":
				lister.desire.Status.Stack.Properties = nil
			case "missing resources":
				properties.Resources = nil
			case "subset":
				properties.Resources = properties.Resources[:8]
			case "wrong ID":
				properties.Resources[0].ID = ptr.To("/unrelated")
			case "duplicates":
				properties.Resources[0] = properties.Resources[1]
			case "nil entry":
				properties.Resources[0] = nil
			case "nil ID":
				properties.Resources[0].ID = nil
			case "nil status":
				properties.Resources[0].Status = nil
			case "delete failed":
				properties.Resources[0].Status = ptr.To(armdeploymentstacks.ResourceStatusModeDeleteFailed)
			case "remove deny failed":
				properties.Resources[0].Status = ptr.To(armdeploymentstacks.ResourceStatusModeRemoveDenyFailed)
			case "unknown status":
				properties.Resources[0].Status = ptr.To(armdeploymentstacks.ResourceStatusMode("unknown"))
			case "conflicting duplicate":
				properties.Resources = append(properties.Resources, &armdeploymentstacks.ManagedResourceReference{ID: properties.Resources[0].ID, Status: ptr.To(armdeploymentstacks.ResourceStatusModeDeleteFailed)})
			case "deleted only":
				for _, resource := range properties.Resources {
					properties.DeletedResources = append(properties.DeletedResources, &armdeploymentstacks.ResourceReference{ID: resource.ID})
				}
				properties.Resources = nil
			case "missing observed hash":
				lister.desire.Status.ObservedSpecHash = ""
			case "extra records":
				properties.Resources = append(properties.Resources, nil, &armdeploymentstacks.ManagedResourceReference{ID: ptr.To("/unrelated"), Status: ptr.To(armdeploymentstacks.ResourceStatusModeManaged)})
				expected = coreapi.ProvisioningStateSucceeded
			case "case insensitive":
				for _, resource := range properties.Resources {
					resource.ID = ptr.To(strings.ToUpper(*resource.ID))
				}
				expected = coreapi.ProvisioningStateSucceeded
			}
			if expected == coreapi.ProvisioningStateProvisioning {
				recoverDesire = success
			}
			before := lister.desire.DeepCopy()
			assertState(expected)
			require.Equal(t, before, lister.desire)
			recoverDesire = nil
		})
	}
	for _, topLevelError := range []bool{false, true} {
		for _, condition := range []string{"pending", "success", "absent", "ID only", "invalid", "contradictory invalid"} {
			t.Run(fmt.Sprintf("failed resources/top-level=%t/condition=%s", topLevelError, condition), func(t *testing.T) {
				recoverDesire = success
				lister.desire = success.DeepCopy()
				resourceID := "/subscriptions/sub/resourceGroups/mrg/providers/Microsoft.Network/publicIPAddresses/cs-cluster-0"
				additionalInfo := []*armdeploymentstacks.ErrorAdditionalInfo{{Info: "diagnostic-to-omit"}}
				resourceError := &armdeploymentstacks.ErrorDetail{
					Code: ptr.To("AuthorizationFailed"), Message: ptr.To("resource permission missing"), Target: ptr.To("nested-target"),
					AdditionalInfo: additionalInfo,
					Details:        []*armdeploymentstacks.ErrorDetail{{Code: ptr.To("Denied"), Message: ptr.To("role missing"), AdditionalInfo: additionalInfo}},
				}
				properties := &armdeploymentstacks.DeploymentStackProperties{
					ProvisioningState: ptr.To(armdeploymentstacks.DeploymentStackProvisioningStateFailed),
					FailedResources:   []*armdeploymentstacks.ResourceReferenceExtended{nil, {ID: &resourceID, Error: resourceError}, {ID: ptr.To(resourceID + "-other")}},
				}
				if topLevelError {
					properties.Error = &armdeploymentstacks.ErrorDetail{Code: ptr.To("DeploymentStackDeploymentFailed"), Message: ptr.To("stack deployment failed"), AdditionalInfo: additionalInfo}
				}
				lister.desire.Status.Stack = &armdeploymentstacks.DeploymentStack{Properties: properties}
				lister.desire.Status.Error = &armdeploymentstacks.ErrorDetail{Code: ptr.To("Forbidden"), Message: ptr.To("transport-to-omit")}
				switch condition {
				case "pending":
					lister.desire.Status.Conditions[0].Status = metav1.ConditionFalse
				case "success":
					properties.ProvisioningState = ptr.To(armdeploymentstacks.DeploymentStackProvisioningStateSucceeded)
				case "absent":
					lister.desire.Status.Conditions = nil
				case "ID only":
					properties.FailedResources = []*armdeploymentstacks.ResourceReferenceExtended{{ID: &resourceID}}
				case "invalid":
					lister.desire.Status.Conditions[0].Status = metav1.ConditionFalse
					lister.desire.Status.Conditions[0].Reason = "InvalidIntent"
				case "contradictory invalid":
					lister.desire.Status.Conditions[0].Reason = "InvalidIntent"
				}
				before := lister.desire.DeepCopy()
				expectedState := coreapi.ProvisioningStateProvisioning
				if condition == "invalid" {
					expectedState = coreapi.ProvisioningStateFailed
					recoverDesire = nil
				}
				failed := assertState(expectedState)
				failed.Error = azureresources.NodePoolEgressPublicIPsError(lister.desire)
				require.Equal(t, "DeploymentStackDeploymentFailed", failed.CloudErrorCode)
				require.Equal(t, resourceID, failed.Error.Details[0].Target)
				if condition == "ID only" {
					require.Len(t, failed.Error.Details, 1)
					require.Contains(t, failed.Error.Details[0].Message, "resource failed")
				} else {
					require.Len(t, failed.Error.Details, 2)
					require.Equal(t, "AuthorizationFailed", failed.Error.Details[0].Details[0].Code)
					require.Equal(t, "nested-target", failed.Error.Details[0].Details[0].Target)
					require.Equal(t, "Denied", failed.Error.Details[0].Details[0].Details[0].Code)
				}
				encoded, err := json.Marshal(failed.Error)
				require.NoError(t, err)
				require.NotContains(t, string(encoded), "diagnostic-to-omit")
				require.NotContains(t, string(encoded), "transport-to-omit")
				require.Contains(t, failed.Message, resourceID)
				require.NotContains(t, failed.Message, "transport-to-omit")
				require.NotContains(t, failed.Message, "diagnostic-to-omit")
				deadline, err := operationbase.ApplyOperationDeadline(failed, nil, ptr.To(metav1.NewTime(time.Now().Add(-time.Hour))), time.Now(), "expired")
				require.NoError(t, err)
				require.Contains(t, deadline.Message, resourceID)
				assertPersisted(deadline, true)
				require.Equal(t, before, lister.desire, "conversion must not mutate the executor observation")
				recoverDesire = nil
				lister.desire.Status.ObservedSpecHash = "previous-intent"
				assertState(coreapi.ProvisioningStateProvisioning)
				lister.desire.Status.ObservedSpecHash = ""
				assertState(coreapi.ProvisioningStateProvisioning)
				lister.desire.Status.ObservedSpecHash = hash
				cluster.Location = "changed-location"
				assertState(coreapi.ProvisioningStateProvisioning)
				cluster.Location = "eastus"
			})
		}
	}
	recoverDesire = nil
	lister.desire = success.DeepCopy()
	lister.desire.Status.Stack.Properties.FailedResources = []*armdeploymentstacks.ResourceReferenceExtended{nil}
	assertState(coreapi.ProvisioningStateSucceeded)
	lister.desire = success.DeepCopy()
	assertState(coreapi.ProvisioningStateSucceeded)
	reservedIDs := spc.Status.ReservedNodePoolEgressPublicIPResourceIDs
	spc.Status.ReservedNodePoolEgressPublicIPResourceIDs = nil
	assertState(coreapi.ProvisioningStateProvisioning)
	spc.Status.ReservedNodePoolEgressPublicIPResourceIDs = reservedIDs
	assertState(coreapi.ProvisioningStateSucceeded)
	spc.Status.ReservedNodePoolEgressPublicIPResourceIDs = append([]*azcorearm.ResourceID(nil), reservedIDs...)
	spc.Status.ReservedNodePoolEgressPublicIPResourceIDs[0] = metadataapi.Must(azcorearm.ParseResourceID("/subscriptions/sub/resourceGroups/mrg/providers/Microsoft.Network/publicIPAddresses/another-reserved-ip"))
	assertState(coreapi.ProvisioningStateProvisioning)
	spc.Status.ReservedNodePoolEgressPublicIPResourceIDs = reservedIDs
	lister.desire.Status.Conditions = append(lister.desire.Status.Conditions, metav1.Condition{Type: azureresourcesapi.DeploymentStackDesireConditionTypeDegraded, Status: metav1.ConditionTrue})
	assertState(coreapi.ProvisioningStateProvisioning)
	lister.desire.Status.Conditions = lister.desire.Status.Conditions[:1]
	lister.desire.Status.Error = &armdeploymentstacks.ErrorDetail{Code: ptr.To("Conflict"), Message: ptr.To("previous failure")}
	assertState(coreapi.ProvisioningStateProvisioning)
	lister.desire.Status.Error = nil
	lister.desire.Status.Stack = &armdeploymentstacks.DeploymentStack{Properties: &armdeploymentstacks.DeploymentStackProperties{ProvisioningState: ptr.To(armdeploymentstacks.DeploymentStackProvisioningStateRunning)}}
	assertState(coreapi.ProvisioningStateProvisioning)
	lister.desire.Status.Stack = nil
	assertState(coreapi.ProvisioningStateProvisioning)
	lister.desire.Status.Stack = success.DeepCopy().Status.Stack
	assertState(coreapi.ProvisioningStateSucceeded)
	cluster.Location = "westus"
	assertState(coreapi.ProvisioningStateProvisioning)
	cluster.Location = "eastus"
	lister.desire.Spec.Stack.Properties.Template = map[string]any{"changed": true}
	assertState(coreapi.ProvisioningStateProvisioning)
	lister.desire.Spec = *spec.DeepCopy()
	lister.desire.Status.ObservedSpecHash = "stale"
	assertState(coreapi.ProvisioningStateProvisioning)
	lister.desire.Status.ObservedSpecHash = hash
	lister.desire.Status.Conditions[0].Status = metav1.ConditionFalse
	lister.desire.Status.Conditions[0].Reason = "ExecutionFailed"
	lister.desire.Status.Error = &armdeploymentstacks.ErrorDetail{Code: ptr.To("DeploymentStackDeploymentFailed"), Message: ptr.To("deployment failed"), Details: []*armdeploymentstacks.ErrorDetail{{Code: ptr.To("AuthorizationFailed"), Message: ptr.To("permission missing"), Target: ptr.To("publicIPAddresses/cs-cluster-0")}}}
	lister.desire.Status.Stack = &armdeploymentstacks.DeploymentStack{Properties: &armdeploymentstacks.DeploymentStackProperties{Error: lister.desire.Status.Error, ProvisioningState: ptr.To(armdeploymentstacks.DeploymentStackProvisioningStateFailed)}}
	require.Contains(t, assertState(coreapi.ProvisioningStateProvisioning).Message, "permission missing", "authoritative desire provides current failure diagnostics")
	state := assertState(coreapi.ProvisioningStateProvisioning)
	require.Contains(t, state.Message, "AuthorizationFailed")
	require.Contains(t, state.Message, "permission missing")
	deadline, err := operationbase.ApplyOperationDeadline(state, nil, ptr.To(metav1.NewTime(time.Now().Add(-time.Hour))), time.Now(), "expired")
	require.NoError(t, err)
	require.Equal(t, coreapi.ProvisioningStateFailed, deadline.ProvisioningState)
	require.Contains(t, deadline.Message, "permission missing")
	assertPersisted(deadline, true)
	lister.desire.Status.Conditions[0].Reason = "InvalidIntent"
	lister.desire.Status.Error.Details[0].Code = ptr.To("InvalidTemplate")
	state = assertState(coreapi.ProvisioningStateFailed)
	require.Equal(t, "InvalidTemplate", state.Error.Details[0].Code)
	lister.desire.Status.Error = nil
	lister.desire.Status.Conditions[0].Status = metav1.ConditionTrue
	lister.desire.Status.Stack = success.DeepCopy().Status.Stack
	assertState(coreapi.ProvisioningStateSucceeded)
	lister.desire.Status.Conditions[0].Status = metav1.ConditionFalse
	lister.desire.Status.Conditions[0].Reason = "RequestFailed"
	lister.desire.Status.Conditions[0].Message = "raw HTTP transport diagnostic"
	lister.desire.Status.Error = &armdeploymentstacks.ErrorDetail{Code: ptr.To("Forbidden"), Message: ptr.To("raw HTTP transport diagnostic")}
	require.Equal(t, "raw HTTP transport diagnostic", lister.desire.Status.Conditions[0].Message, "error conversion must not mutate the executor observation")
	rejected := assertState(coreapi.ProvisioningStateProvisioning)
	require.Equal(t, "Forbidden", rejected.CloudErrorCode)
	require.NotContains(t, rejected.Message, "raw HTTP")
	lister.desire.Status.Conditions[0].Reason = "InvalidIntent"
	require.NotContains(t, assertState(coreapi.ProvisioningStateFailed).Error.Message, "raw HTTP")
	lister.desire.Status.Conditions[0].Reason = "RequestFailed"
	lister.desire.Status.Error = nil
	assertState(coreapi.ProvisioningStateProvisioning)
	lister.desire = nil
	assertState(coreapi.ProvisioningStateProvisioning)
	cluster.CustomerProperties.Platform.OutboundType = metadataapi.OutboundType("UserDefinedRouting")
	assertState(coreapi.ProvisioningStateSucceeded)
}
