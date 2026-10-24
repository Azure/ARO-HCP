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
	"fmt"
	"strings"
	"time"

	"k8s.io/client-go/tools/cache"
	"k8s.io/utils/ptr"

	azcorearm "github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"

	"github.com/Azure/ARO-HCP/backend/pkg/utils/controllerutils"
	"github.com/Azure/ARO-HCP/internal/api/coreapi"
	"github.com/Azure/ARO-HCP/internal/apihelpers/coreapihelpers"
	controllerutil "github.com/Azure/ARO-HCP/internal/controllerutils"
	"github.com/Azure/ARO-HCP/internal/database/cosmosstorage/billingcosmosstorage"
	"github.com/Azure/ARO-HCP/internal/database/cosmosstorage/cosmosstorageutils"
	"github.com/Azure/ARO-HCP/internal/database/listers/corelisters"
	"github.com/Azure/ARO-HCP/internal/utils"
)

const ReconcileBillingManagedResourceGroupControllerName = "ReconcileBillingManagedResourceGroup"

// billingMetadataInconsistency is diagnostic data, not a reconciliation failure:
// billing metadata must not affect the cluster's aggregated Degraded condition.
type billingMetadataInconsistency struct {
	reason  string
	message string
}

func (e *billingMetadataInconsistency) Error() string { return e.message }

type reconcileBillingManagedResourceGroup struct {
	clusterLister   corelisters.ClusterLister
	billingLister   corelisters.BillingLister
	billingDBClient billingcosmosstorage.BillingDBClient
}

// newReconcileBillingManagedResourceGroupSyncer repairs the managed RG on
// existing billing documents. In particular, older writers used the customer's
// cluster RG instead. Linking and creating billing documents remain separate work.
func newReconcileBillingManagedResourceGroupSyncer(billingDBClient billingcosmosstorage.BillingDBClient, clusterLister corelisters.ClusterLister, billingLister corelisters.BillingLister) controllerutil.GenericSyncer[controllerutils.HCPClusterKey] {
	return &reconcileBillingManagedResourceGroup{
		clusterLister:   clusterLister,
		billingLister:   billingLister,
		billingDBClient: billingDBClient,
	}
}

func newManagedRGWatchingController(syncer controllerutil.GenericSyncer[controllerutils.HCPClusterKey], clusterInformer, billingInformer controllerutil.Notifier) (*controllerutil.GenericWatchingController[controllerutils.HCPClusterKey], error) {
	// Use NewGenericWatchingController instead of NewClusterWatchingController
	// so storage failures are retried without writing per-cluster health conditions.
	controller := controllerutil.NewGenericWatchingController(ReconcileBillingManagedResourceGroupControllerName, coreapi.ClusterResourceType, syncer, controllerutils.ReconcileTotal)
	const resync = time.Hour
	if err := controller.QueueForInformersWithMaxDepth(resync, 0, clusterInformer); err != nil {
		return nil, fmt.Errorf("register cluster watch: %w", err)
	}
	// Billing documents carry the owning cluster's ARM ID rather than implementing
	// CosmosPersistable, which the generic Cosmos event handler requires.
	enqueueBilling := func(obj any) {
		doc := obj.(*billingcosmosstorage.BillingDocument)
		controller.EnqueueResourceIDAddWithMaxDepth(doc.ResourceID, true, 0)
	}
	logger := utils.DefaultLogger().WithValues(utils.LogValues{}.AddControllerName(ReconcileBillingManagedResourceGroupControllerName)...)
	_, err := billingInformer.AddEventHandlerWithOptions(cache.ResourceEventHandlerFuncs{
		AddFunc: enqueueBilling,
		UpdateFunc: func(_, newObj any) {
			enqueueBilling(newObj)
		},
	}, cache.HandlerOptions{Logger: &logger, ResyncPeriod: ptr.To(resync)})
	if err != nil {
		return nil, fmt.Errorf("register billing watch: %w", err)
	}
	return controller, nil
}

func (c *reconcileBillingManagedResourceGroup) CooldownChecker() controllerutil.CooldownChecker {
	return nil
}

func (c *reconcileBillingManagedResourceGroup) MakeKey(resourceID *azcorearm.ResourceID) controllerutils.HCPClusterKey {
	return controllerutils.HCPClusterKey{
		SubscriptionID:    resourceID.SubscriptionID,
		ResourceGroupName: resourceID.ResourceGroupName,
		HCPClusterName:    resourceID.Name,
	}
}

func (c *reconcileBillingManagedResourceGroup) isEligibleForRepair(cluster *coreapi.Cluster) bool {
	return cluster != nil && cluster.ID != nil &&
		cluster.ServiceProviderProperties.DeletionTimestamp == nil &&
		cluster.ServiceProviderProperties.ProvisioningState != coreapi.ProvisioningStateDeleting &&
		len(cluster.ServiceProviderProperties.BillingDocumentCosmosID) > 0
}

func (c *reconcileBillingManagedResourceGroup) SyncOnce(ctx context.Context, key controllerutils.HCPClusterKey) error {
	cluster, err := c.clusterLister.Get(ctx, key.SubscriptionID, key.ResourceGroupName, key.HCPClusterName)
	if cosmosstorageutils.IsNotFoundError(err) {
		return nil
	}
	if err != nil {
		return utils.TrackError(err)
	}
	if !c.isEligibleForRepair(cluster) {
		return nil
	}
	clusterUID := cluster.ServiceProviderProperties.ClusterUID
	billingDocumentID := cluster.ServiceProviderProperties.BillingDocumentCosmosID
	logger := key.AddLoggerValues(utils.LoggerFromContext(ctx)).WithValues(
		"billingDocID", billingDocumentID, "clusterUID", clusterUID,
		"billingDocumentCosmosID", billingDocumentID)
	// Billing document IDs are assigned from the cluster UID.
	// Validate the cluster's link before following it.
	billingLinkMatchesClusterUID := clusterUID != "" && billingDocumentID == clusterUID
	if !billingLinkMatchesClusterUID {
		logger.Info("cluster billing link does not match its UID",
			"event", "billing_metadata_inconsistency", "reason", "ClusterIdentityMismatch")
		return nil
	}
	doc, err := c.billingLister.GetByID(ctx, billingDocumentID)
	if cosmosstorageutils.IsNotFoundError(err) {
		return nil
	}
	if err != nil {
		return utils.TrackError(err)
	}
	if doc.DeletionTime != nil {
		return nil
	}
	desired, inconsistency := validateAndResolveBillingManagedResourceGroup(cluster, doc)
	if inconsistency != nil {
		logger.Error(inconsistency, "billing metadata requires reconciliation",
			"event", "billing_metadata_inconsistency",
			"reason", inconsistency.reason)
		// Input events and resync recheck persistent data inconsistencies without
		// treating them as transient storage failures in the retry loop.
		return nil
	}
	if doc.ManagedResourceGroup == desired {
		return nil
	}
	patch := billingcosmosstorage.NewActiveBillingManagedResourceGroupPatch(desired)
	previous := doc.ManagedResourceGroup
	// The configured managed RG is immutable for this cluster UID. Updating only
	// that field preserves billing watermarks; the condition rejects closed records.
	if err := c.billingDBClient.BillingDocs(cluster.ID.SubscriptionID).PatchByID(ctx, doc.ID, patch); err != nil {
		if cosmosstorageutils.IsNotFoundError(err) {
			return nil
		}
		// Retry from the listers; conflicts can persist until the cache refreshes.
		return utils.TrackError(err)
	}
	logger.Info("reconciled billing managed resource group",
		"previousManagedResourceGroup", previous, "managedResourceGroup", desired)
	return nil
}

// validateAndResolveBillingManagedResourceGroup validates the billing record's
// cluster identity and resolves its desired managed RG from cluster configuration.
func validateAndResolveBillingManagedResourceGroup(cluster *coreapi.Cluster, doc *billingcosmosstorage.BillingDocument) (string, *billingMetadataInconsistency) {
	clusterResourceIDMatches := doc.ResourceID != nil &&
		strings.EqualFold(doc.ResourceID.String(), cluster.ID.String())
	if !clusterResourceIDMatches {
		return "", &billingMetadataInconsistency{
			reason:  "ClusterIdentityMismatch",
			message: fmt.Sprintf("billing document %q does not match cluster resource %q", doc.ID, cluster.ID),
		}
	}
	name := cluster.CustomerProperties.Platform.ManagedResourceGroup
	if name == "" {
		// Mirror an unset name rather than retaining stale billing metadata.
		return "", nil
	}
	// Do not use the customer's RG as a fallback: that is the historical defect
	// this controller repairs. Only a configured RG name is authoritative here.
	return coreapihelpers.ToResourceGroupResourceIDString(cluster.ID.SubscriptionID, name), nil
}
