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
	"context"
	"fmt"
	"strings"
	"time"

	"k8s.io/utils/ptr"

	azcorearm "github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/resources/armdeploymentstacks/v2"

	"github.com/Azure/ARO-HCP/backend/pkg/utils/controllerutils"
	"github.com/Azure/ARO-HCP/internal/api/azureresourcesapi"
	"github.com/Azure/ARO-HCP/internal/api/coreapi"
	"github.com/Azure/ARO-HCP/internal/api/metadataapi"
	"github.com/Azure/ARO-HCP/internal/apihelpers/azureresourcesapihelpers"
	"github.com/Azure/ARO-HCP/internal/apihelpers/coreapihelpers"
	"github.com/Azure/ARO-HCP/internal/database/cosmosstorage/azureresourcescosmosstorage"
	"github.com/Azure/ARO-HCP/internal/database/cosmosstorage/corecosmosstorage"
	"github.com/Azure/ARO-HCP/internal/database/cosmosstorage/cosmosstorageutils"
	"github.com/Azure/ARO-HCP/internal/database/informers/azureresourcesinformers"
	"github.com/Azure/ARO-HCP/internal/database/informers/coreinformers"
	"github.com/Azure/ARO-HCP/internal/database/listers/azureresourceslisters"
	"github.com/Azure/ARO-HCP/internal/database/listers/corelisters"
	"github.com/Azure/ARO-HCP/internal/ocm"
	"github.com/Azure/ARO-HCP/internal/utils"
)

const NodePoolEgressPublicIPsControllerName = "NodePoolEgressPublicIPs"

const (
	nodePoolEgressPublicIPCount      = 9
	nodePoolEgressPublicIPAPIVersion = "2024-05-01"
	nodePoolEgressPublicIPsStackName = "egress-public-ips"
)

type nodePoolEgressPublicIPsSyncer struct {
	resourcesDBClient            corecosmosstorage.ResourcesDBClient
	clusterLister                corelisters.ClusterLister
	serviceProviderClusterLister corelisters.ServiceProviderClusterLister
	subscriptionLister           corelisters.SubscriptionLister
	deploymentStackDesireLister  azureresourceslisters.DeploymentStackDesireLister
	azureResourcesDBClient       azureresourcescosmosstorage.AzureResourcesDBClient
	clustersServiceClient        ocm.ClusterServiceClientSpec
}

var _ controllerutils.ClusterSyncer = (*nodePoolEgressPublicIPsSyncer)(nil)

func NewNodePoolEgressPublicIPsController(resourcesDBClient corecosmosstorage.ResourcesDBClient, azureResourcesDBClient azureresourcescosmosstorage.AzureResourcesDBClient, backendInformers coreinformers.BackendInformers, azureResourcesInformers azureresourcesinformers.AzureResourcesInformers, clustersServiceClient ocm.ClusterServiceClientSpec) controllerutils.Controller {
	_, clusterLister := backendInformers.Clusters()
	_, serviceProviderClusterLister := backendInformers.ServiceProviderClusters()
	_, subscriptionLister := backendInformers.Subscriptions()
	desireInformer, desireLister := azureResourcesInformers.DeploymentStackDesires()
	syncer := &nodePoolEgressPublicIPsSyncer{
		resourcesDBClient:            resourcesDBClient,
		clusterLister:                clusterLister,
		serviceProviderClusterLister: serviceProviderClusterLister,
		subscriptionLister:           subscriptionLister,
		deploymentStackDesireLister:  desireLister,
		azureResourcesDBClient:       azureResourcesDBClient,
		clustersServiceClient:        clustersServiceClient,
	}
	controller := controllerutils.NewClusterWatchingController(NodePoolEgressPublicIPsControllerName, resourcesDBClient, backendInformers, nil, time.Minute, syncer)
	controller.AddInterestingResource(coreapi.ClusterResourceType, "*")
	controller.AddInterestingResource(coreapi.ServiceProviderClusterResourceType, coreapi.ServiceProviderClusterResourceName)
	if err := controller.QueueForChildInformers(0, desireInformer); err != nil {
		panic(err)
	}
	controller.AddInterestingResource(azureresourcesapi.ClusterScopedDeploymentStackDesireResourceType, azureresourcesapi.NodePoolEgressPublicIPsDeploymentStackDesireName)
	return controller
}

func (syncer *nodePoolEgressPublicIPsSyncer) needsWork(cluster *coreapi.Cluster) bool {
	if !NodePoolEgressPublicIPsApplicable(cluster) {
		return false
	}
	return cluster.ID != nil && len(controllerutils.ClusterServiceIDForCluster(cluster)) > 0 && len(strings.TrimSpace(cluster.Location)) > 0
}

func (s *nodePoolEgressPublicIPsSyncer) SyncOnce(ctx context.Context, key controllerutils.HCPClusterKey) error {
	cluster, err := s.clusterLister.Get(ctx, key.SubscriptionID, key.ResourceGroupName, key.HCPClusterName)
	if cosmosstorageutils.IsNotFoundError(err) {
		return nil
	}
	if err != nil {
		return utils.TrackError(fmt.Errorf("get cluster: %w", err))
	}
	if cluster.ServiceProviderProperties.DeletionTimestamp != nil {
		crud, err := s.azureResourcesDBClient.DeploymentStackDesiresForCluster(key.SubscriptionID, key.ResourceGroupName, key.HCPClusterName)
		if err != nil {
			return utils.TrackError(err)
		}
		err = crud.Delete(ctx, azureresourcesapi.NodePoolEgressPublicIPsDeploymentStackDesireName)
		if cosmosstorageutils.IsNotFoundError(err) {
			return nil
		}
		return utils.TrackError(err)
	}
	if !s.needsWork(cluster) {
		return nil
	}
	csID := controllerutils.ClusterServiceIDForCluster(cluster)
	spc, err := s.serviceProviderClusterLister.Get(ctx, key.SubscriptionID, key.ResourceGroupName, key.HCPClusterName)
	if cosmosstorageutils.IsNotFoundError(err) {
		return nil
	}
	if err != nil {
		return utils.TrackError(fmt.Errorf("get service provider cluster: %w", err))
	}
	scope := spc.Status.AzureResources.ManagedResourceGroup.AzureResource
	if scope == nil {
		return nil
	}
	expectedScope, err := coreapihelpers.ToResourceGroupResourceID(cluster.ID.SubscriptionID, cluster.CustomerProperties.Platform.ManagedResourceGroup)
	if err != nil {
		return utils.TrackError(err)
	}
	if !strings.EqualFold(scope.String(), expectedScope.String()) {
		return fmt.Errorf("confirmed managed resource group does not match cluster intent")
	}
	subscription, err := s.subscriptionLister.Get(ctx, key.SubscriptionID)
	if cosmosstorageutils.IsNotFoundError(err) {
		return nil
	}
	if err != nil {
		return utils.TrackError(fmt.Errorf("get subscription: %w", err))
	}
	if subscription.Properties == nil || len(strings.TrimSpace(ptr.Deref(subscription.Properties.TenantId, ""))) == 0 {
		return utils.TrackError(fmt.Errorf("subscription %q has no tenant ID", key.SubscriptionID))
	}
	if cluster.ServiceProviderProperties.ClusterServiceID == nil || len(cluster.ServiceProviderProperties.ClusterServiceID.String()) == 0 {
		csCtx, cancel := context.WithTimeout(ctx, controllerutils.CSReadTimeout)
		defer cancel()
		existing, err := ocm.FindClusterByAzureInfo(csCtx, s.clustersServiceClient, key.SubscriptionID, key.ResourceGroupName, key.HCPClusterName, *subscription.Properties.TenantId, cluster.CustomerProperties.Platform.ManagedResourceGroup)
		if err != nil {
			return utils.TrackError(err)
		}
		if existing != nil {
			adoptedID, err := metadataapi.NewInternalID(existing.HREF())
			if err != nil {
				return utils.TrackError(err)
			}
			return ValidateNodePoolEgressPublicIPReservationIdentity(spc, adoptedID.ClusterID())
		}
	}
	if len(spc.Status.ReservedNodePoolEgressPublicIPResourceIDs) == 0 {
		replacement := spc.DeepCopy()
		for index := 0; index < nodePoolEgressPublicIPCount; index++ {
			resourceID, err := azcorearm.ParseResourceID(fmt.Sprintf("%s/providers/Microsoft.Network/publicIPAddresses/%s-%d", scope.String(), csID, index))
			if err != nil {
				return utils.TrackError(err)
			}
			replacement.Status.ReservedNodePoolEgressPublicIPResourceIDs = append(replacement.Status.ReservedNodePoolEgressPublicIPResourceIDs, resourceID)
		}
		spec, err := NodePoolEgressPublicIPsDesiredSpec(cluster, replacement, subscription)
		if err != nil {
			return utils.TrackError(err)
		}
		existing, err := s.deploymentStackDesireLister.GetForCluster(ctx, key.SubscriptionID, key.ResourceGroupName, key.HCPClusterName, azureresourcesapi.NodePoolEgressPublicIPsDeploymentStackDesireName)
		if err != nil && !cosmosstorageutils.IsNotFoundError(err) {
			return utils.TrackError(err)
		}
		if err == nil {
			matches, err := nodePoolEgressPublicIPsIntentMatches(*spec, existing)
			if err != nil {
				return utils.TrackError(err)
			}
			if !matches {
				return fmt.Errorf("existing node-pool egress public IP stack intent does not match unreserved resource IDs")
			}
		}
		spc, err = s.resourcesDBClient.ServiceProviderClusters(key.SubscriptionID, key.ResourceGroupName, key.HCPClusterName).Replace(ctx, replacement, nil)
		if err != nil {
			return utils.TrackError(err)
		}
	}
	spec, err := NodePoolEgressPublicIPsDesiredSpec(cluster, spc, subscription)
	if err != nil {
		return utils.TrackError(err)
	}
	if spec == nil {
		return nil
	}
	return s.publish(ctx, key, *spec)
}

func ValidateNodePoolEgressPublicIPReservationIdentity(spc *coreapi.ServiceProviderCluster, csID string) error {
	for index, resourceID := range spc.Status.ReservedNodePoolEgressPublicIPResourceIDs {
		if resourceID == nil || resourceID.Name != fmt.Sprintf("%s-%d", csID, index) {
			return fmt.Errorf("reserved node-pool public IP identities conflict with discovered CS cluster; explicit reconciliation is required")
		}
	}
	return nil
}

func nodePoolEgressPublicIPsDeploymentStackSpec(scope *azcorearm.ResourceID, reservedIDs []*azcorearm.ResourceID, location, tenantID string) (azureresourcesapi.DeploymentStackDesireSpec, error) {
	if len(reservedIDs) != nodePoolEgressPublicIPCount {
		return azureresourcesapi.DeploymentStackDesireSpec{}, fmt.Errorf("expected %d reserved node-pool egress public IP resource IDs", nodePoolEgressPublicIPCount)
	}
	target, err := azcorearm.ParseResourceID(scope.String() + "/providers/Microsoft.Resources/deploymentStacks/" + nodePoolEgressPublicIPsStackName)
	if err != nil {
		return azureresourcesapi.DeploymentStackDesireSpec{}, err
	}
	resources := make([]map[string]any, 0, nodePoolEgressPublicIPCount)
	seen := make(map[string]bool, len(reservedIDs))
	for _, resourceID := range reservedIDs {
		if resourceID == nil || resourceID.Parent == nil || !strings.EqualFold(resourceID.Parent.String(), scope.String()) || !strings.EqualFold(resourceID.ResourceType.String(), "Microsoft.Network/publicIPAddresses") || len(resourceID.Name) == 0 || seen[strings.ToLower(resourceID.String())] {
			return azureresourcesapi.DeploymentStackDesireSpec{}, fmt.Errorf("invalid or duplicate reserved node-pool egress public IP resource ID")
		}
		seen[strings.ToLower(resourceID.String())] = true
		resources = append(resources, map[string]any{
			"type":       "Microsoft.Network/publicIPAddresses",
			"apiVersion": nodePoolEgressPublicIPAPIVersion,
			"name":       resourceID.Name,
			"location":   location,
			"sku":        map[string]any{"name": "Standard"},
			"properties": map[string]any{
				"publicIPAddressVersion":   "IPv4",
				"publicIPAllocationMethod": "Static",
				"idleTimeoutInMinutes":     4,
			},
		})
	}
	return azureresourcesapi.DeploymentStackDesireSpec{
		TargetResourceID: target,
		Authentication: azureresourcesapi.DeploymentStackDesireAuthentication{
			Type:                  azureresourcesapi.DeploymentStackDesireAuthenticationTypeFPA,
			FirstPartyApplication: &azureresourcesapi.DeploymentStackDesireAuthenticationFirstPartyApplication{TenantID: tenantID},
		},
		Action: azureresourcesapi.DeploymentStackDesireActionTypeApply,
		Stack: &armdeploymentstacks.DeploymentStack{
			Properties: &armdeploymentstacks.DeploymentStackProperties{
				Template: map[string]any{
					"$schema":        "https://schema.management.azure.com/schemas/2019-04-01/deploymentTemplate.json#",
					"contentVersion": "1.0.0.0",
					"resources":      resources,
				},
				ActionOnUnmanage: &armdeploymentstacks.ActionOnUnmanage{
					Resources:        ptr.To(armdeploymentstacks.UnmanageActionResourceModeDetach),
					ResourceGroups:   ptr.To(armdeploymentstacks.UnmanageActionResourceGroupModeDetach),
					ManagementGroups: ptr.To(armdeploymentstacks.UnmanageActionManagementGroupModeDetach),
				},
				DenySettings: &armdeploymentstacks.DenySettings{Mode: ptr.To(armdeploymentstacks.DenySettingsModeNone)},
			},
		},
	}, nil
}

func (s *nodePoolEgressPublicIPsSyncer) publish(ctx context.Context, key controllerutils.HCPClusterKey, spec azureresourcesapi.DeploymentStackDesireSpec) error {
	existing, err := s.deploymentStackDesireLister.GetForCluster(ctx, key.SubscriptionID, key.ResourceGroupName, key.HCPClusterName, azureresourcesapi.NodePoolEgressPublicIPsDeploymentStackDesireName)
	absent := cosmosstorageutils.IsNotFoundError(err)
	if err != nil && !absent {
		return utils.TrackError(err)
	}
	crud, err := s.azureResourcesDBClient.DeploymentStackDesiresForCluster(key.SubscriptionID, key.ResourceGroupName, key.HCPClusterName)
	if err != nil {
		return utils.TrackError(err)
	}
	if absent {
		resourceID, err := azureresourcesapihelpers.ToClusterScopedDeploymentStackDesireResourceID(key.SubscriptionID, key.ResourceGroupName, key.HCPClusterName, azureresourcesapi.NodePoolEgressPublicIPsDeploymentStackDesireName)
		if err != nil {
			return utils.TrackError(err)
		}
		_, err = crud.Create(ctx, &azureresourcesapi.DeploymentStackDesire{
			CosmosMetadata: coreapi.CosmosMetadata{ResourceID: resourceID, PartitionKey: strings.ToLower(key.SubscriptionID)},
			Spec:           spec,
		}, nil)
		return utils.TrackError(err)
	}
	if existing.Spec.TargetResourceID == nil || !strings.EqualFold(existing.Spec.TargetResourceID.String(), spec.TargetResourceID.String()) {
		return fmt.Errorf("node-pool egress public IP deployment stack target is immutable")
	}
	spec.TargetResourceID = existing.Spec.TargetResourceID
	existingHash, err := azureresourcesapihelpers.DeploymentStackDesireSpecHash(existing.Spec)
	if err != nil {
		return utils.TrackError(err)
	}
	desiredHash, err := azureresourcesapihelpers.DeploymentStackDesireSpecHash(spec)
	if err != nil {
		return utils.TrackError(err)
	}
	if existingHash == desiredHash {
		return nil
	}
	replacement := existing.DeepCopy()
	replacement.Spec = spec
	_, err = crud.Replace(ctx, replacement, nil)
	return utils.TrackError(err)
}
