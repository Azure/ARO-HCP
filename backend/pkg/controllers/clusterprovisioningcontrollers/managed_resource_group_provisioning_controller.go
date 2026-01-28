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

package clusterprovisioningcontrollers

import (
	"context"
	"fmt"
	"net/http"
	"time"

	azureclient "github.com/Azure/ARO-HCP/backend/pkg/azure/client"
	"github.com/Azure/ARO-HCP/backend/pkg/controllers/controllerutils"
	"github.com/Azure/ARO-HCP/backend/pkg/listers"
	"github.com/Azure/ARO-HCP/internal/api"
	"github.com/Azure/ARO-HCP/internal/database"
	"github.com/Azure/ARO-HCP/internal/utils"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/to"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/resources/armresources"
)

// managedResourceGroupProvisioningSyncer is a Cluster syncer that ensures that the
// Cluster's Managed Resource Group is provisioned.
type managedResourceGroupProvisioningSyncer struct {
	cosmosClient database.DBClient

	azureFPAClientBuilder azureclient.FirstPartyApplicationClientBuilder
}

var _ controllerutils.ClusterSyncer = (*managedResourceGroupProvisioningSyncer)(nil)

func NewManagedResourceGroupProvisioningController(
	cosmosClient database.DBClient,
	subscriptionLister listers.SubscriptionLister,
) controllerutils.Controller {
	syncer := &managedResourceGroupProvisioningSyncer{
		cosmosClient: cosmosClient,
	}

	return controllerutils.NewClusterWatchingController(
		"ManagedResourceGroupCreation",
		cosmosClient,
		subscriptionLister,
		1*time.Minute,
		syncer,
	)
}

func (c *managedResourceGroupProvisioningSyncer) SyncOnce(ctx context.Context, key controllerutils.HCPClusterKey) error {
	existingCluster, err := c.cosmosClient.HCPClusters(key.SubscriptionID, key.ResourceGroupName).Get(ctx, key.HCPClusterName)
	if database.IsResponseError(err, http.StatusNotFound) {
		return nil // cluster doesn't exist, no work to do
	}
	if err != nil {
		return utils.TrackError(fmt.Errorf("failed to get Cluster: %w", err))
	}

	// TODO do we want to store that the managed resource group was created in
	// a ServiceProviderCluster attribute, do we rely on the controller state until
	// it succeeds or we retry indefinitely somehow?
	// TODO if we store in in ServiceProviderCluster, what should we indicate/store?
	// "managedResourceGroupProvisioned: true" or something else/more?
	// TODO How do we ensure that we don't create the Managed Resource Group if the
	// cluster is being deleted, making sure that there's no race condition
	// with CS (and in the future with another backend controller) where one
	// deletes it and another creates it afterwards?
	// TODO how are we going to coordinate with CS?
	// TODO how are we going to coordinate with other controllers in the RP,
	// including validation ones?
	// TODO do we need to check the state of some other aspect, operation or
	// cluster or other resource when deciding if we should process?

	// TODO what should be the criteria of shouldProcess? the answer to this is
	// dependent on the answers to the questions above.
	shouldProcess := c.shouldProcess()
	if !shouldProcess {
		return nil // no work to do
	}

	subscription, err := c.cosmosClient.Subscriptions().Get(ctx, existingCluster.ID.SubscriptionID)
	if err != nil {
		return utils.TrackError(fmt.Errorf("failed to get Subscription: %w", err))
	}

	resourceGroupsClient, err := c.azureFPAClientBuilder.ResourceGroupsClient(*subscription.Properties.TenantId, existingCluster.ID.SubscriptionID)
	if err != nil {
		return utils.TrackError(fmt.Errorf("failed to get resource groups client: %w", err))
	}

	err = c.ensureManagedResourceGroupCreated(existingCluster, ctx, resourceGroupsClient, key)
	if err != nil {
		return utils.TrackError(fmt.Errorf("failed to ensure managed resource group created: %w", err))
	}

	return nil
}

func (c *managedResourceGroupProvisioningSyncer) shouldProcess() bool {
	return true
}

// TODO in CS we have the business logic of the provision task defined within an interface "ProvisionStep" which is
// has a function that returns (bool, error):
// It returns whether the work has been fully completed or not, or an error. For example, if it attempts
// to create a resource that takes some time to be provisioned on the Azure side, it would trigger the creation of it,
// check if it's been fully provisioned and if not it would return false with no error. It would then be retried later.
// Do we want to follow something similar here? or do we want to just signal with an error on both cases?
// In this specific provisioning task this does not fully apply because the call to Azure seems to be synchronous but
// for other provisioning tasks this becomes relevant.
// TODO in CS before the provisioning step itself the synchronous create cluster request in CS is only accepted if:
//   - static syntax validation on it passes
//   - MRG name cannot be empty (in CS is required but in RP it is optional and the RP generates a default one (in ocm/convert.go) if the user has not provided it)
//   - Specified MRG name cannot be the same name as the ARO-HCP Cluster resource group
//   - The user-provided Subnet and NSG resource IDs cannot have their resource group name be the same as the MRG name
//   - The user-provided MIs cannot have their resource IDs be the same as the MRG name
//   - At DB level the MRG does not exist within the Subscription of the Cluster being created (this does not include potential cross-region existence)
//
// Additionally, it also doesn't run until a CS inflight check (Asynchronous) verifies that the MRG must not preexist
// within the Subscription of the Cluster being created.
//
// The MRG name must be unique cross clusters within the subscription between regions. Although CS only checks
// within the same region as it does checks in the database which is regional.
//
// All of them apply to the context of a Subscription
func (c *managedResourceGroupProvisioningSyncer) ensureManagedResourceGroupCreated(
	cluster *api.HCPOpenShiftCluster,
	ctx context.Context, resourceGroupsClient azureclient.ResourceGroupsClient, key controllerutils.HCPClusterKey,
) error {
	logger := utils.LoggerFromContext(ctx)
	logger.With("managed_resource_group_name", cluster.CustomerProperties.Platform.ManagedResourceGroup)
	utils.ContextWithLogger(ctx, logger)

	// TODO if it exists, do we consider the work done or do we want to somehow
	// check if existing differs from desired and apply changes? The implications
	// of attempting changes are:
	// * Some attributes in Azure are READ-ONLY an cannot be changed after
	//   creation
	// * If there are changes that we do not have in desired we would "undo" them
	// As a relevant note: The managed resource group is created by the ARO-HCP service
	// and the user shouldn't have permissions to directly delete it because a
	// deny assignment would be applied a bit after its creation (this is in AME
	// environments, in lower ones there's actually no way to prevent that as
	// Microsoft's DenyAssignment mechanism is not available)
	getResp, err := resourceGroupsClient.Get(ctx, key.ResourceGroupName, nil)
	if !azureclient.IsResourceGroupNotFoundErr(err) {
		return utils.TrackError(fmt.Errorf("failed to get managed resource group: %w", err))
	}
	var existingManagedResourceGroup *armresources.ResourceGroup
	if err == nil {
		existingManagedResourceGroup = &getResp.ResourceGroup
	}

	desiredManagedResourceGroup := c.buildNewManagedResourceGroup(cluster)
	// TODO here we only peform CreateOrUpdate if we didn't get it in the GET call.
	// Is this what we want? As a relevant aspect I think that for calls that trigger
	// an asynchronous request that takes a while to complete even when there wouldn't
	// be changes that would change the state on the Azure side so that's why
	// we don't unconditionally perform BeginCreateOrUpdate calls.
	if existingManagedResourceGroup == nil {
		createOrUpdateResp, err := resourceGroupsClient.CreateOrUpdate(ctx, cluster.CustomerProperties.Platform.ManagedResourceGroup, desiredManagedResourceGroup, nil)
		if err != nil {
			return utils.TrackError(fmt.Errorf("failed to create or update the managed resource group: %w", err))
		}
		existingManagedResourceGroup = &createOrUpdateResp.ResourceGroup
	}

	// TODO Could it be that there's a pre-existing managed resource group in the
	// subscription in Azure Location? For example, another ARO-HCP regional RP
	// where a cluster is created specifying the same MRG within the same
	// Subscription.
	if existingManagedResourceGroup.Location != desiredManagedResourceGroup.Location {
		return utils.TrackError(
			fmt.Errorf("existing managed resource group Location attribute differs from desired. Desired: %s, Existing: %s",
				*desiredManagedResourceGroup.Location, *existingManagedResourceGroup.Location,
			),
		)
	}

	// TODO could it be that there's a pre-existing managed resource group in the
	// subscription with a different ManagedBy attribute? The ManagedBy value set
	// by the ARO-HCP service is the cluster's Resource ID. I guess it might depend
	// on when do we do the check of "the MRG name must be unique cross clusters within the subscription between regions" on
	// when do we do the check of  usage check?
	if existingManagedResourceGroup.ManagedBy != desiredManagedResourceGroup.ManagedBy {
		return utils.TrackError(
			fmt.Errorf("unexpected managed resource group ManagedBy attribute. Desired: %s, Existing: %s",
				*desiredManagedResourceGroup.ManagedBy, *existingManagedResourceGroup.ManagedBy,
			),
		)
	}

	return nil
}

func (c *managedResourceGroupProvisioningSyncer) buildNewManagedResourceGroup(cluster *api.HCPOpenShiftCluster) armresources.ResourceGroup {
	managedBy := cluster.ID.String()
	return armresources.ResourceGroup{
		// TODO do we set the Name for clarity too? According to the Go data type it is
		// a READ-ONLY field. The name is passed on the CreateOrUpdate call as another argument.
		Location:  to.Ptr(cluster.Location),
		ManagedBy: &managedBy,
	}
}
