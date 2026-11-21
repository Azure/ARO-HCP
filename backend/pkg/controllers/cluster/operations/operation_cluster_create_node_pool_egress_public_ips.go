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

	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/Azure/ARO-HCP/backend/pkg/controllers/cluster/azureresources"
	operationbase "github.com/Azure/ARO-HCP/backend/pkg/utils/operationutils"
	"github.com/Azure/ARO-HCP/internal/api/azureresourcesapi"
	"github.com/Azure/ARO-HCP/internal/api/coreapi"
	"github.com/Azure/ARO-HCP/internal/database/cosmosstorage/cosmosstorageutils"
	"github.com/Azure/ARO-HCP/internal/database/listers/azureresourceslisters"
	"github.com/Azure/ARO-HCP/internal/database/listers/corelisters"
)

type clusterCreateNodePoolEgressPublicIPsCheck struct {
	clusterLister                corelisters.ClusterLister
	serviceProviderClusterLister corelisters.ServiceProviderClusterLister
	subscriptionLister           corelisters.SubscriptionLister
	desireLister                 azureresourceslisters.DeploymentStackDesireLister
}

func (c *clusterCreateNodePoolEgressPublicIPsCheck) GetSourceName() string {
	return "nodePoolEgressPublicIPs"
}

func (c *clusterCreateNodePoolEgressPublicIPsCheck) CalculateOperationStatus(ctx context.Context, operation *coreapi.Operation, _ struct{}) (*operationbase.OperationState, error) {
	pending := operationbase.NewOperationState(coreapi.ProvisioningStateProvisioning, "node pool public IPs not confirmed")
	id := operation.ExternalID
	cluster, err := c.clusterLister.Get(ctx, id.SubscriptionID, id.ResourceGroupName, id.Name)
	if cosmosstorageutils.IsNotFoundError(err) {
		return pending, nil
	}
	if err != nil {
		return nil, err
	}
	if !azureresources.NodePoolEgressPublicIPsApplicable(cluster) {
		return operationbase.NewOperationState(coreapi.ProvisioningStateSucceeded, ""), nil
	}
	spc, err := c.serviceProviderClusterLister.Get(ctx, id.SubscriptionID, id.ResourceGroupName, id.Name)
	if cosmosstorageutils.IsNotFoundError(err) {
		return pending, nil
	}
	if err != nil {
		return nil, err
	}
	subscription, err := c.subscriptionLister.Get(ctx, id.SubscriptionID)
	if cosmosstorageutils.IsNotFoundError(err) {
		return pending, nil
	}
	if err != nil {
		return nil, err
	}
	spec, err := azureresources.NodePoolEgressPublicIPsDesiredSpec(cluster, spc, subscription)
	if err != nil {
		return nil, err
	}
	if spec == nil {
		return pending, nil
	}
	desire, err := c.desireLister.GetForCluster(ctx, id.SubscriptionID, id.ResourceGroupName, id.Name, azureresourcesapi.NodePoolEgressPublicIPsDeploymentStackDesireName)
	if cosmosstorageutils.IsNotFoundError(err) {
		return pending, nil
	}
	if err != nil {
		return nil, err
	}
	current, err := azureresources.NodePoolEgressPublicIPsCurrentIntent(*spec, desire)
	if err != nil {
		return nil, err
	}
	if !current {
		return pending, nil
	}
	condition := meta.FindStatusCondition(desire.Status.Conditions, azureresourcesapi.DeploymentStackDesireConditionTypeSuccessfullyApplied)
	stackError := azureresources.NodePoolEgressPublicIPsError(desire)
	if stackError != nil {
		pending.Message += ": " + stackError.String()
		pending.WithCloudErrorCode(stackError.Code)
	}
	if condition != nil && condition.Status == metav1.ConditionFalse && condition.Reason == "InvalidIntent" {
		return operationbase.NewFailedOperationState(pending.CloudErrorCode, pending.Message, stackError), nil
	}
	if condition == nil {
		return pending, nil
	}
	if meta.IsStatusConditionTrue(desire.Status.Conditions, azureresourcesapi.DeploymentStackDesireConditionTypeSuccessfullyApplied) {
		ready, err := azureresources.NodePoolEgressPublicIPsReady(*spec, desire, spc.Status.ReservedNodePoolEgressPublicIPResourceIDs)
		if err != nil {
			return nil, err
		}
		if !ready {
			return pending, nil
		}
		return operationbase.NewOperationState(coreapi.ProvisioningStateSucceeded, ""), nil
	}
	return pending, nil
}
