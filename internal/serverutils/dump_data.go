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
	"strings"

	azcorearm "github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"

	"github.com/Azure/ARO-HCP/internal/api/metadataapi"
	"github.com/Azure/ARO-HCP/internal/database/cosmosstorage/billingcosmosstorage"
	"github.com/Azure/ARO-HCP/internal/database/cosmosstorage/corecosmosstorage"
	"github.com/Azure/ARO-HCP/internal/database/cosmosstorage/cosmosstorageutils"
	"github.com/Azure/ARO-HCP/internal/database/cosmosstorage/kubeappliercosmosstorage"
	"github.com/Azure/ARO-HCP/internal/utils"
)

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
func DumpDataToLogger(
	ctx context.Context,
	resourcesDBClient corecosmosstorage.ResourcesDBClient,
	kubeApplierDBClients kubeappliercosmosstorage.KubeApplierDBClients,
	managementClusterLister kubeappliercosmosstorage.ManagementClusterLister,
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
	err = cosmosstorageutils.RedactTypedDocument(startingCosmosRecord)
	if err != nil {
		return utils.TrackError(err)
	}
	logger.Info(fmt.Sprintf("dumping resourceID %v", startingCosmosRecord.ResourceID),
		"snapshotType", "cosmos",
		"currentResourceID", resourceIDToString(startingCosmosRecord.ResourceID),
		"objectMetadata", cosmosstorageutils.ObjectMetadataForTypedDocument("resources", startingCosmosRecord),
		"content", startingCosmosRecord,
	)

	allCosmosRecords, err := cosmosCRUD.ListRecursive(ctx, nil)
	if err != nil {
		return utils.TrackError(err)
	}

	errs := []error{}
	for _, typedDocument := range allCosmosRecords.Items(ctx) {
		if err := cosmosstorageutils.RedactTypedDocument(typedDocument); err != nil {
			errs = append(errs, utils.TrackError(err))
			continue
		}
		logger.Info(fmt.Sprintf("dumping resourceID %v", typedDocument.ResourceID),
			"snapshotType", "cosmos",
			"currentResourceID", resourceIDToString(typedDocument.ResourceID),
			"objectMetadata", cosmosstorageutils.ObjectMetadataForTypedDocument("resources", typedDocument),
			"content", typedDocument,
		)
	}
	if err := allCosmosRecords.GetError(); err != nil {
		errs = append(errs, err)
	}

	// dump all related operations, including the completed ones.
	operationIter := resourcesDBClient.Operations(resourceID.SubscriptionID).ListActiveOperations(
		&corecosmosstorage.ResourcesDBClientListActiveOperationDocsOptions{
			ExternalID:             resourceID,
			IncludeNestedResources: true,
			IncludeTerminal:        true,
		})
	for _, operation := range operationIter.Items(ctx) {
		// An operation's own ResourceID is subscription/location-scoped, so derive the HCP
		// cluster name from its ExternalID (the targeted cluster/node pool) when possible.
		opLogger := logger
		if hcpClusterName := metadataapi.ClusterNameFromResourceID(operation.ExternalID); hcpClusterName != "" {
			opLogger = logger.WithValues(utils.LogValues{}.AddHCPClusterName(hcpClusterName)...)
		}
		opLogger.Info(fmt.Sprintf("dumping resourceID %v", operation.ResourceID),
			"snapshotType", "cosmos",
			"currentResourceID", resourceIDToString(operation.ResourceID),
			"objectMetadata", cosmosstorageutils.ObjectMetadataForOperation(operation),
			"content", operation,
		)
	}
	if err := operationIter.GetError(); err != nil {
		errs = append(errs, err)
	}

	if err := dumpKubeApplierData(ctx, kubeApplierDBClients, managementClusterLister, resourceID); err != nil {
		errs = append(errs, err)
	}

	return utils.TrackError(errors.Join(errs...))
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
	kubeApplierDBClients kubeappliercosmosstorage.KubeApplierDBClients,
	managementClusterLister kubeappliercosmosstorage.ManagementClusterLister,
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
				"snapshotType", "cosmos",
				"currentResourceID", resourceIDToString(doc.ResourceID),
				"objectMetadata", cosmosstorageutils.ObjectMetadataForTypedDocument("kubeApplier", doc),
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
func DumpBillingToLogger(ctx context.Context, resourcesDBClient corecosmosstorage.ResourcesDBClient, billingDBClient billingcosmosstorage.BillingDBClient, resourceID *azcorearm.ResourceID) error {
	logger := utils.LoggerFromContext(ctx)

	clusterCRUD := resourcesDBClient.HCPClusters(resourceID.SubscriptionID, resourceID.ResourceGroupName)
	existingCluster, err := clusterCRUD.Get(ctx, resourceID.Name)
	if cosmosstorageutils.IsNotFoundError(err) {
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
	if cosmosstorageutils.IsNotFoundError(err) {
		return nil
	}
	if err != nil {
		return utils.TrackError(err)
	}

	logger.Info(fmt.Sprintf("dumping billing document for resourceID %v", billingDoc.ResourceID),
		"snapshotType", "cosmos",
		"currentResourceID", billingDoc.ResourceID.String(),
		"objectMetadata", metadataapi.ObjectMetadataForResourceID("billing", billingDoc.ResourceID),
		"content", billingDoc,
	)

	return nil
}
