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

package billing

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"k8s.io/client-go/tools/cache"
	"k8s.io/utils/ptr"

	azcorearm "github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"

	"github.com/Azure/ARO-HCP/arohcp-cosmos-client/api/coreapi"
	"github.com/Azure/ARO-HCP/arohcp-cosmos-client/apihelpers/coreapihelpers"
	"github.com/Azure/ARO-HCP/arohcp-cosmos-client/storage/billingcosmosstorage"
	"github.com/Azure/ARO-HCP/arohcp-cosmos-client/storage/cosmosstorageutils"
	"github.com/Azure/ARO-HCP/backend/pkg/utils/controllerutils"
	"github.com/Azure/ARO-HCP/internal/database/listers/corelisters"
	"github.com/Azure/ARO-HCP/internal/utils"
)

const ReconcileBillingManagedResourceGroupControllerName = "ReconcileBillingManagedResourceGroup"

type reconcileBillingManagedResourceGroup struct {
	clusterLister   corelisters.ClusterLister
	billingLister   corelisters.BillingLister
	billingDBClient billingcosmosstorage.BillingDBClient
}

// newReconcileBillingManagedResourceGroupSyncer repairs the managed RG on
// existing billing documents to mirror the cluster's configured managed RG.
func newReconcileBillingManagedResourceGroupSyncer(billingDBClient billingcosmosstorage.BillingDBClient, clusterLister corelisters.ClusterLister, billingLister corelisters.BillingLister) controllerutils.ClusterSyncer {
	return &reconcileBillingManagedResourceGroup{
		clusterLister:   clusterLister,
		billingLister:   billingLister,
		billingDBClient: billingDBClient,
	}
}

type billingResourceEnqueuer interface {
	EnqueueResourceIDAddWithMaxDepth(resourceID *azcorearm.ResourceID, changed bool, maxDepth int)
}

func watchBillingManagedResourceGroup(queue billingResourceEnqueuer, billingInformer controllerutils.Notifier) error {
	// Billing documents carry the owning cluster's ARM ID rather than implementing
	// CosmosPersistable, which the generic Cosmos event handler requires.
	logger := utils.DefaultLogger().WithValues(utils.LogValues{}.AddControllerName(ReconcileBillingManagedResourceGroupControllerName)...)
	_, err := billingInformer.AddEventHandlerWithOptions(cache.ResourceEventHandlerFuncs{
		AddFunc: func(obj any) {
			enqueueBillingManagedResourceGroup(queue, obj)
		},
		UpdateFunc: func(_, newObj any) {
			enqueueBillingManagedResourceGroup(queue, newObj)
		},
	}, cache.HandlerOptions{Logger: &logger, ResyncPeriod: ptr.To(time.Hour)})
	if err != nil {
		return fmt.Errorf("register billing watch: %w", err)
	}
	return nil
}

func enqueueBillingManagedResourceGroup(queue billingResourceEnqueuer, obj any) {
	doc := obj.(*billingcosmosstorage.BillingDocument)
	queue.EnqueueResourceIDAddWithMaxDepth(doc.ResourceID, true, 0)
}

func (c *reconcileBillingManagedResourceGroup) needsWork(cluster *coreapi.Cluster) bool {
	return cluster != nil && cluster.ID != nil &&
		cluster.ServiceProviderProperties.DeletionTimestamp == nil &&
		cluster.ServiceProviderProperties.ProvisioningState != coreapi.ProvisioningStateDeleting &&
		len(cluster.ServiceProviderProperties.BillingDocumentCosmosID) != 0
}

func (c *reconcileBillingManagedResourceGroup) SyncOnce(ctx context.Context, key controllerutils.HCPClusterKey) error {
	cluster, err := c.clusterLister.Get(ctx, key.SubscriptionID, key.ResourceGroupName, key.HCPClusterName)
	if cosmosstorageutils.IsNotFoundError(err) {
		return nil
	}
	if err != nil {
		return utils.TrackError(err)
	}
	if !c.needsWork(cluster) {
		return nil
	}
	clusterUID := cluster.ServiceProviderProperties.ClusterUID
	billingDocumentID := cluster.ServiceProviderProperties.BillingDocumentCosmosID
	logger := key.AddLoggerValues(utils.LoggerFromContext(ctx)).WithValues(
		"billingDocID", billingDocumentID, "clusterUID", clusterUID,
		"billingDocumentCosmosID", billingDocumentID)
	// Billing document IDs are assigned from the cluster UID.
	// Validate the cluster's link before following it.
	billingLinkMatchesClusterUID := len(clusterUID) != 0 && billingDocumentID == clusterUID
	if !billingLinkMatchesClusterUID {
		logger.Info("cluster billing link does not match its UID",
			"event", "billing_metadata_inconsistency", "reason", "ClusterIdentityMismatch")
		return nil
	}
	doc, err := c.billingLister.GetByID(ctx, billingDocumentID)
	if cosmosstorageutils.IsNotFoundError(err) {
		// The billing informer will enqueue the cluster when the linked record appears.
		return nil
	}
	if err != nil {
		return utils.TrackError(err)
	}
	if doc.DeletionTime != nil {
		return nil
	}
	clusterResourceIDMatches := doc.ResourceID != nil &&
		strings.EqualFold(doc.ResourceID.String(), cluster.ID.String())
	if !clusterResourceIDMatches {
		logger.Info("billing document does not match cluster resource",
			"event", "billing_metadata_inconsistency",
			"reason", "ClusterIdentityMismatch")
		return nil
	}
	// An unset cluster value must clear stale billing metadata, not fall back to the customer RG.
	var desired string
	var configurationErr error
	if name := cluster.CustomerProperties.Platform.ManagedResourceGroup; len(name) != 0 {
		desired = coreapihelpers.ToResourceGroupResourceIDString(cluster.ID.SubscriptionID, name)
	} else {
		configurationErr = errors.New("cluster has no configured managed resource group; correct cluster configuration to enable billing")
		logger.Error(configurationErr,
			"Billing service will not be able to bill this cluster with an empty managed resource group until the cluster configuration is corrected",
			"event", "billing_metadata_inconsistency", "reason", "MissingManagedResourceGroup",
			"managedResourceGroup", doc.ManagedResourceGroup)
	}
	// Compare the stored representation so noncanonical values are repaired too.
	if doc.ManagedResourceGroup == desired {
		return configurationErr
	}
	patch := billingcosmosstorage.NewActiveBillingManagedResourceGroupPatch(desired)
	previous := doc.ManagedResourceGroup
	// The configured managed RG is immutable for this cluster UID. Updating only
	// that field preserves billing watermarks; the condition rejects closed records.
	patchErr := c.billingDBClient.BillingDocs(cluster.ID.SubscriptionID).PatchByID(ctx, doc.ID, patch)
	if cosmosstorageutils.IsNotFoundError(patchErr) {
		return configurationErr
	}
	if patchErr != nil {
		// Retry from the listers; conflicts can persist until the cache refreshes.
		return utils.TrackError(errors.Join(configurationErr, patchErr))
	}
	logger.Info("reconciled billing managed resource group",
		"previousManagedResourceGroup", previous, "managedResourceGroup", desired)
	return configurationErr
}
