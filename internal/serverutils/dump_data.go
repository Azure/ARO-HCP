// Copyright 2025 Microsoft Corporation
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

package serverutils

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"strings"

	"github.com/go-logr/logr"

	azcorearm "github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"

	"github.com/Azure/ARO-HCP/internal/api"
	"github.com/Azure/ARO-HCP/internal/database"
	"github.com/Azure/ARO-HCP/internal/utils"
)

// NodePoolLister exposes the slice of the cached node pool lister that
// DumpDataToLogger needs to diff against live cosmos data. It exists locally
// so internal/serverutils does not have to import the backend-side lister
// package.
type NodePoolLister interface {
	ListForCluster(ctx context.Context, subscriptionID, resourceGroupName, clusterName string) ([]*api.HCPOpenShiftClusterNodePool, error)
}

// ServiceProviderNodePoolLister exposes the slice of the cached service
// provider node pool lister that DumpDataToLogger needs to diff against live
// cosmos data. It exists locally so internal/serverutils does not have to
// import the backend-side lister package.
type ServiceProviderNodePoolLister interface {
	Get(ctx context.Context, subscriptionID, resourceGroupName, clusterName, nodePoolName string) (*api.ServiceProviderNodePool, error)
}

// DumpDataToLogger writes a structured-log entry for every document related
// to resourceID. It covers three storage layers:
//
//  1. The resources container: the resource at resourceID itself plus every
//     descendant under it (cluster + nested children).
//  2. The operations container: every operation in the subscription whose
//     externalID is rooted at resourceID.
//  3. Every per-management-cluster kube-applier container: when both
//     kubeApplierDBClients and managementClusterLister are non-nil, the
//     function iterates the lister, opens an untyped CRUD per MC, and dumps
//     every document under resourceID's prefix. *Desire documents live
//     here, scoped to the cluster or nodepool they target.
//
// Passing nil for kubeApplierDBClients or managementClusterLister skips
// layer (3); callers that don't yet have those wired (e.g. frontend
// request handlers) can leave them nil without losing the cosmos / ops
// dumps.
//
// When nodePoolLister or serviceProviderNodePoolLister is non-nil, the
// function also diffs the cached view against a fresh cosmos list for the
// containing cluster and logs error-level messages whenever the cache and
// the live list disagree (missing, extra, or differing objects). Both the
// cached and live objects are emitted so the discrepancy can be inspected.
func DumpDataToLogger(
	ctx context.Context,
	resourcesDBClient database.ResourcesDBClient,
	kubeApplierDBClients database.KubeApplierDBClients,
	managementClusterLister database.ManagementClusterLister,
	nodePoolLister NodePoolLister,
	serviceProviderNodePoolLister ServiceProviderNodePoolLister,
	resourceID *azcorearm.ResourceID,
) error {
	logger := utils.LoggerFromContext(ctx)

	// load the HCP from the cosmos DB
	cosmosCRUD, err := resourcesDBClient.UntypedCRUD(*resourceID)
	if err != nil {
		return utils.TrackError(err)
	}
	startingCosmosRecord, err := cosmosCRUD.Get(ctx, resourceID)
	if err != nil {
		return utils.TrackError(err)
	}
	logger.Info(fmt.Sprintf("dumping resourceID %v", startingCosmosRecord.ResourceID),
		"currentResourceID", resourceIDToString(startingCosmosRecord.ResourceID),
		"content", startingCosmosRecord,
	)

	allCosmosRecords, err := cosmosCRUD.ListRecursive(ctx, nil)
	if err != nil {
		return utils.TrackError(err)
	}

	errs := []error{}
	for _, typedDocument := range allCosmosRecords.Items(ctx) {
		logger.Info(fmt.Sprintf("dumping resourceID %v", typedDocument.ResourceID),
			"currentResourceID", resourceIDToString(typedDocument.ResourceID),
			"content", typedDocument,
		)
	}
	if err := allCosmosRecords.GetError(); err != nil {
		errs = append(errs, err)
	}

	// dump all related operations, including the completed ones.
	allOperationsForSubscription, err := resourcesDBClient.Operations(resourceID.SubscriptionID).List(ctx, nil)
	if err != nil {
		errs = append(errs, err)
	}
	resourceIDString := strings.ToLower(resourceID.String())
	for _, operation := range allOperationsForSubscription.Items(ctx) {
		currOperationTarget := strings.ToLower(operation.ExternalID.String())
		if strings.HasPrefix(currOperationTarget, resourceIDString) {
			logger.Info(fmt.Sprintf("dumping resourceID %v", operation.ResourceID),
				"currentResourceID", resourceIDToString(operation.ResourceID),
				"content", operation,
			)
		}
	}
	if err := allOperationsForSubscription.GetError(); err != nil {
		errs = append(errs, err)
	}

	if err := dumpKubeApplierData(ctx, kubeApplierDBClients, managementClusterLister, resourceID); err != nil {
		errs = append(errs, err)
	}

	diffNodePoolCacheAgainstLive(ctx, resourcesDBClient, nodePoolLister, serviceProviderNodePoolLister, resourceID)

	return utils.TrackError(errors.Join(errs...))
}

// clusterScopeFromResourceID walks resourceID's parent chain to find a
// hcpOpenShiftClusters resource and returns its (subscription, resource
// group, cluster name). An empty clusterName means resourceID isn't rooted
// at a cluster.
func clusterScopeFromResourceID(resourceID *azcorearm.ResourceID) (subscriptionID, resourceGroupName, clusterName string) {
	for cur := resourceID; cur != nil; cur = cur.Parent {
		if strings.EqualFold(cur.ResourceType.String(), api.ClusterResourceType.String()) {
			return cur.SubscriptionID, cur.ResourceGroupName, cur.Name
		}
	}
	return "", "", ""
}

// diffNodePoolCacheAgainstLive compares the cached node pool and service
// provider node pool listers against a fresh cosmos list for the cluster
// containing resourceID. Discrepancies are logged at error level with both
// the cached and live objects so a human can compare them. The function is a
// best-effort debugging aid and never returns an error; failures while
// gathering data are logged and skipped.
func diffNodePoolCacheAgainstLive(
	ctx context.Context,
	resourcesDBClient database.ResourcesDBClient,
	nodePoolLister NodePoolLister,
	serviceProviderNodePoolLister ServiceProviderNodePoolLister,
	resourceID *azcorearm.ResourceID,
) {
	if nodePoolLister == nil && serviceProviderNodePoolLister == nil {
		return
	}
	logger := utils.LoggerFromContext(ctx)

	subscriptionID, resourceGroupName, clusterName := clusterScopeFromResourceID(resourceID)
	if clusterName == "" {
		return
	}

	liveNodePoolsByID, ok := loadLiveNodePools(ctx, resourcesDBClient, subscriptionID, resourceGroupName, clusterName)
	if !ok {
		return
	}

	if nodePoolLister != nil {
		diffNodePoolsAgainstCache(ctx, logger, nodePoolLister, subscriptionID, resourceGroupName, clusterName, liveNodePoolsByID)
	}

	if serviceProviderNodePoolLister != nil {
		diffServiceProviderNodePoolsAgainstCache(ctx, logger, resourcesDBClient, serviceProviderNodePoolLister, subscriptionID, resourceGroupName, clusterName, liveNodePoolsByID)
	}
}

// loadLiveNodePools fetches all node pools for the cluster from cosmos and
// returns them keyed by lowercased ResourceID. The bool is false when the
// list failed; in that case the error has already been logged.
func loadLiveNodePools(
	ctx context.Context,
	resourcesDBClient database.ResourcesDBClient,
	subscriptionID, resourceGroupName, clusterName string,
) (map[string]*api.HCPOpenShiftClusterNodePool, bool) {
	logger := utils.LoggerFromContext(ctx)

	iter, err := resourcesDBClient.HCPClusters(subscriptionID, resourceGroupName).NodePools(clusterName).List(ctx, nil)
	if err != nil {
		logger.Error(err, "failed to list live node pools for cache comparison",
			"subscriptionID", subscriptionID, "resourceGroupName", resourceGroupName, "clusterName", clusterName)
		return nil, false
	}
	byID := map[string]*api.HCPOpenShiftClusterNodePool{}
	for _, np := range iter.Items(ctx) {
		byID[strings.ToLower(np.ResourceID.String())] = np
	}
	if err := iter.GetError(); err != nil {
		logger.Error(err, "error iterating live node pools for cache comparison",
			"subscriptionID", subscriptionID, "resourceGroupName", resourceGroupName, "clusterName", clusterName)
	}
	return byID, true
}

func diffNodePoolsAgainstCache(
	ctx context.Context,
	logger logr.Logger,
	nodePoolLister NodePoolLister,
	subscriptionID, resourceGroupName, clusterName string,
	liveByID map[string]*api.HCPOpenShiftClusterNodePool,
) {
	cached, err := nodePoolLister.ListForCluster(ctx, subscriptionID, resourceGroupName, clusterName)
	if err != nil {
		logger.Error(err, "failed to list cached node pools for cache comparison",
			"subscriptionID", subscriptionID, "resourceGroupName", resourceGroupName, "clusterName", clusterName)
		return
	}
	cachedByID := map[string]*api.HCPOpenShiftClusterNodePool{}
	for _, np := range cached {
		cachedByID[strings.ToLower(np.ResourceID.String())] = np
	}

	for id, live := range liveByID {
		c, ok := cachedByID[id]
		if !ok {
			logger.Error(nil, "node pool present in live cosmos but missing from informer cache",
				"currentResourceID", id, "live", live)
			continue
		}
		if !reflect.DeepEqual(live, c) {
			logger.Error(nil, "node pool informer cache differs from live cosmos",
				"currentResourceID", id, "live", live, "cached", c)
		}
	}
	for id, c := range cachedByID {
		if _, ok := liveByID[id]; !ok {
			logger.Error(nil, "node pool present in informer cache but missing from live cosmos",
				"currentResourceID", id, "cached", c)
		}
	}
}

func diffServiceProviderNodePoolsAgainstCache(
	ctx context.Context,
	logger logr.Logger,
	resourcesDBClient database.ResourcesDBClient,
	serviceProviderNodePoolLister ServiceProviderNodePoolLister,
	subscriptionID, resourceGroupName, clusterName string,
	liveNodePoolsByID map[string]*api.HCPOpenShiftClusterNodePool,
) {
	for _, np := range liveNodePoolsByID {
		nodePoolName := np.ResourceID.Name
		liveSPNP, err := resourcesDBClient.ServiceProviderNodePools(subscriptionID, resourceGroupName, clusterName, nodePoolName).
			Get(ctx, api.ServiceProviderNodePoolResourceName)
		liveMissing := database.IsNotFoundError(err)
		if err != nil && !liveMissing {
			logger.Error(err, "failed to get live service provider node pool for cache comparison",
				"subscriptionID", subscriptionID, "resourceGroupName", resourceGroupName, "clusterName", clusterName, "nodePoolName", nodePoolName)
			continue
		}

		cachedSPNP, err := serviceProviderNodePoolLister.Get(ctx, subscriptionID, resourceGroupName, clusterName, nodePoolName)
		cachedMissing := database.IsNotFoundError(err)
		if err != nil && !cachedMissing {
			logger.Error(err, "failed to get cached service provider node pool for cache comparison",
				"subscriptionID", subscriptionID, "resourceGroupName", resourceGroupName, "clusterName", clusterName, "nodePoolName", nodePoolName)
			continue
		}

		switch {
		case liveMissing && cachedMissing:
			// no-op
		case liveMissing && !cachedMissing:
			logger.Error(nil, "service provider node pool present in informer cache but missing from live cosmos",
				"currentResourceID", strings.ToLower(cachedSPNP.ResourceID.String()), "cached", cachedSPNP)
		case !liveMissing && cachedMissing:
			logger.Error(nil, "service provider node pool present in live cosmos but missing from informer cache",
				"currentResourceID", strings.ToLower(liveSPNP.ResourceID.String()), "live", liveSPNP)
		case !reflect.DeepEqual(liveSPNP, cachedSPNP):
			logger.Error(nil, "service provider node pool informer cache differs from live cosmos",
				"currentResourceID", strings.ToLower(liveSPNP.ResourceID.String()), "live", liveSPNP, "cached", cachedSPNP)
		}
	}
}

// dumpKubeApplierData walks every configured management cluster's kube-applier
// container for documents nested under resourceID's prefix and emits a log
// line per record. *Desire documents live here, scoped to the cluster or
// nodepool they target.
//
// Either input may be nil — both are required to do any work, so a nil on
// either side means "kube-applier data isn't wired here" and the function
// silently no-ops.
func dumpKubeApplierData(
	ctx context.Context,
	kubeApplierDBClients database.KubeApplierDBClients,
	managementClusterLister database.ManagementClusterLister,
	resourceID *azcorearm.ResourceID,
) error {
	if kubeApplierDBClients == nil || managementClusterLister == nil {
		return nil
	}
	logger := utils.LoggerFromContext(ctx)

	managementClusters, err := managementClusterLister.List(ctx)
	if err != nil {
		return utils.TrackError(fmt.Errorf("listing management clusters for kube-applier dump: %w", err))
	}

	errs := []error{}
	for _, mc := range managementClusters {
		mcResourceID := mc.ResourceID
		if mcResourceID == nil {
			mcResourceID = mc.CosmosMetadata.ResourceID
		}
		if mcResourceID == nil {
			continue
		}
		mcLogger := logger.WithValues("managementCluster", strings.ToLower(mcResourceID.String()))

		client := kubeApplierDBClients.For(ctx, mcResourceID)
		if client == nil {
			mcLogger.Error(nil, "no kube-applier client configured for management cluster; skipping")
			continue
		}

		desireCRUD, err := client.UntypedCRUD(*resourceID)
		if err != nil {
			errs = append(errs, utils.TrackError(err))
			continue
		}
		desireIterator, err := desireCRUD.ListRecursive(ctx, nil)
		if err != nil {
			errs = append(errs, utils.TrackError(err))
			continue
		}
		for _, doc := range desireIterator.Items(ctx) {
			mcLogger.Info(fmt.Sprintf("dumping kube-applier resourceID %v", doc.ResourceID),
				"currentResourceID", resourceIDToString(doc.ResourceID),
				"content", doc,
			)
		}
		if err := desireIterator.GetError(); err != nil {
			errs = append(errs, utils.TrackError(err))
		}
	}

	return errors.Join(errs...)
}

func resourceIDToString(id *azcorearm.ResourceID) string {
	if id == nil {
		return "<missing>"
	}
	return id.String()
}

// DumpBillingToLogger dumps active billing documents for the given cluster resource ID to the logger.
func DumpBillingToLogger(ctx context.Context, resourcesDBClient database.ResourcesDBClient, billingDBClient database.BillingDBClient, resourceID *azcorearm.ResourceID) error {
	logger := utils.LoggerFromContext(ctx)

	clusterCRUD := resourcesDBClient.HCPClusters(resourceID.SubscriptionID, resourceID.ResourceGroupName)
	existingCluster, err := clusterCRUD.Get(ctx, resourceID.Name)
	if database.IsNotFoundError(err) {
		return nil
	}
	if err != nil {
		return utils.TrackError(err)
	}

	clusterUID := existingCluster.ServiceProviderProperties.ClusterUID
	if clusterUID == "" {
		return nil
	}

	billingDoc, err := billingDBClient.BillingDocs(resourceID.SubscriptionID).GetByID(ctx, clusterUID)
	if database.IsNotFoundError(err) {
		return nil
	}
	if err != nil {
		return utils.TrackError(err)
	}

	logger.Info(fmt.Sprintf("dumping billing document for resourceID %v", billingDoc.ResourceID),
		"currentResourceID", billingDoc.ResourceID.String(),
		"content", billingDoc,
	)

	return nil
}
