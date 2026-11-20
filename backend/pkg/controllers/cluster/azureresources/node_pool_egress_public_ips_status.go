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

package azureresources

import (
	"strings"

	"k8s.io/apimachinery/pkg/api/meta"
	"k8s.io/utils/ptr"

	azcorearm "github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/resources/armdeploymentstacks/v2"

	"github.com/Azure/ARO-HCP/backend/pkg/utils/controllerutils"
	"github.com/Azure/ARO-HCP/internal/api/azureresourcesapi"
	"github.com/Azure/ARO-HCP/internal/api/coreapi"
	"github.com/Azure/ARO-HCP/internal/api/metadataapi"
	"github.com/Azure/ARO-HCP/internal/apihelpers/azureresourcesapihelpers"
	"github.com/Azure/ARO-HCP/internal/apihelpers/coreapihelpers"
)

func NodePoolEgressPublicIPsApplicable(cluster *coreapi.Cluster) bool {
	return cluster.ServiceProviderProperties.DeletionTimestamp == nil && (len(cluster.CustomerProperties.Platform.OutboundType) == 0 || cluster.CustomerProperties.Platform.OutboundType == metadataapi.OutboundTypeLoadBalancer)
}

func NodePoolEgressPublicIPsDesiredSpec(cluster *coreapi.Cluster, spc *coreapi.ServiceProviderCluster, subscription *coreapi.Subscription) (*azureresourcesapi.DeploymentStackDesireSpec, error) {
	csID := controllerutils.ClusterServiceIDForCluster(cluster)
	if cluster.ID == nil || len(csID) == 0 || len(strings.TrimSpace(cluster.Location)) == 0 || spc == nil || subscription == nil || subscription.Properties == nil || len(strings.TrimSpace(ptr.Deref(subscription.Properties.TenantId, ""))) == 0 {
		return nil, nil
	}
	scope := spc.Status.AzureResources.ManagedResourceGroup.AzureResource
	if scope == nil {
		return nil, nil
	}
	expected, err := coreapihelpers.ToResourceGroupResourceID(cluster.ID.SubscriptionID, cluster.CustomerProperties.Platform.ManagedResourceGroup)
	if err != nil {
		return nil, err
	}
	if !strings.EqualFold(expected.String(), scope.String()) {
		return nil, nil
	}
	if len(spc.Status.ReservedNodePoolEgressPublicIPResourceIDs) == 0 {
		return nil, nil
	}
	spec, err := nodePoolEgressPublicIPsDeploymentStackSpec(scope, spc.Status.ReservedNodePoolEgressPublicIPResourceIDs, cluster.Location, *subscription.Properties.TenantId)
	return &spec, err
}

func NodePoolEgressPublicIPsReady(spec azureresourcesapi.DeploymentStackDesireSpec, desire *azureresourcesapi.DeploymentStackDesire, reservedIDs []*azcorearm.ResourceID) (bool, error) {
	current, err := NodePoolEgressPublicIPsCurrentIntent(spec, desire)
	if err != nil {
		return false, err
	}
	if !current || NodePoolEgressPublicIPsError(desire) != nil || !meta.IsStatusConditionTrue(desire.Status.Conditions, azureresourcesapi.DeploymentStackDesireConditionTypeSuccessfullyApplied) || meta.IsStatusConditionTrue(desire.Status.Conditions, azureresourcesapi.DeploymentStackDesireConditionTypeDegraded) {
		return false, nil
	}
	if desire.Status.Stack == nil || desire.Status.Stack.Properties == nil || len(reservedIDs) != nodePoolEgressPublicIPCount {
		return false, nil
	}
	properties := desire.Status.Stack.Properties
	if properties.ProvisioningState != nil && *properties.ProvisioningState != armdeploymentstacks.DeploymentStackProvisioningStateSucceeded {
		return false, nil
	}
	for _, resourceID := range reservedIDs {
		if resourceID == nil {
			return false, nil
		}
		managed := false
		for _, resource := range properties.Resources {
			if resource == nil || resource.ID == nil || !strings.EqualFold(resourceID.String(), *resource.ID) {
				continue
			}
			if resource.Status == nil || *resource.Status != armdeploymentstacks.ResourceStatusModeManaged {
				return false, nil
			}
			managed = true
		}
		if !managed {
			return false, nil
		}
	}
	return true, nil
}

func NodePoolEgressPublicIPsCurrentIntent(spec azureresourcesapi.DeploymentStackDesireSpec, desire *azureresourcesapi.DeploymentStackDesire) (bool, error) {
	matches, err := nodePoolEgressPublicIPsIntentMatches(spec, desire)
	if err != nil || !matches {
		return false, err
	}
	hash, err := azureresourcesapihelpers.DeploymentStackDesireSpecHash(desire.Spec)
	return err == nil && desire.Status.ObservedSpecHash == hash, err
}

func nodePoolEgressPublicIPsIntentMatches(spec azureresourcesapi.DeploymentStackDesireSpec, desire *azureresourcesapi.DeploymentStackDesire) (bool, error) {
	if desire == nil {
		return false, nil
	}
	if spec.TargetResourceID != nil && desire.Spec.TargetResourceID != nil && strings.EqualFold(spec.TargetResourceID.String(), desire.Spec.TargetResourceID.String()) {
		spec.TargetResourceID = desire.Spec.TargetResourceID
	}
	expectedHash, err := azureresourcesapihelpers.DeploymentStackDesireSpecHash(spec)
	if err != nil {
		return false, err
	}
	actualHash, err := azureresourcesapihelpers.DeploymentStackDesireSpecHash(desire.Spec)
	if err != nil {
		return false, err
	}
	return expectedHash == actualHash, nil
}

func NodePoolEgressPublicIPsError(desire *azureresourcesapi.DeploymentStackDesire) *coreapi.CloudErrorBody {
	if failure := NodePoolEgressPublicIPsFailedResourcesError(desire); failure != nil {
		return failure
	}
	if desire.Status.Stack != nil && desire.Status.Stack.Properties != nil {
		if detail := stackCloudError(desire.Status.Stack.Properties.Error); detail != nil {
			return detail
		}
	}
	if desire.Status.Error != nil {
		return &coreapi.CloudErrorBody{Code: ptr.Deref(desire.Status.Error.Code, ""), Message: "Azure rejected the node-pool egress public IP deployment stack request"}
	}
	return nil
}

func NodePoolEgressPublicIPsFailedResourcesError(desire *azureresourcesapi.DeploymentStackDesire) *coreapi.CloudErrorBody {
	if desire == nil || desire.Status.Stack == nil || desire.Status.Stack.Properties == nil {
		return nil
	}
	properties := desire.Status.Stack.Properties
	var failedResources []coreapi.CloudErrorBody
	for _, resource := range properties.FailedResources {
		if resource == nil {
			continue
		}
		failure := coreapi.CloudErrorBody{
			Code: "DeploymentStackDeploymentFailed", Message: "Node-pool egress public IP stack resource failed",
			Target: ptr.Deref(resource.ID, ""),
		}
		if detail := stackCloudError(resource.Error); detail != nil {
			failure.Details = append(failure.Details, *detail)
		}
		failedResources = append(failedResources, failure)
	}
	if len(failedResources) == 0 {
		return nil
	}
	failure := stackCloudError(properties.Error)
	if failure == nil {
		failure = &coreapi.CloudErrorBody{}
	}
	if len(failure.Code) == 0 {
		failure.Code = "DeploymentStackDeploymentFailed"
	}
	if len(failure.Message) == 0 {
		failure.Message = "Node-pool egress public IP stack contains failed resources"
	}
	failure.Details = append(failure.Details, failedResources...)
	return failure
}

func stackCloudError(detail *armdeploymentstacks.ErrorDetail) *coreapi.CloudErrorBody {
	if detail == nil {
		return nil
	}
	result := &coreapi.CloudErrorBody{Code: ptr.Deref(detail.Code, ""), Message: ptr.Deref(detail.Message, ""), Target: ptr.Deref(detail.Target, "")}
	for _, child := range detail.Details {
		if converted := stackCloudError(child); converted != nil {
			result.Details = append(result.Details, *converted)
		}
	}
	return result
}
