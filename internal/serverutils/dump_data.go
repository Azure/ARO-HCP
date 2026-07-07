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
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	azcorearm "github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"

	"github.com/Azure/ARO-HCP/internal/api"
	"github.com/Azure/ARO-HCP/internal/database"
	"github.com/Azure/ARO-HCP/internal/redact"
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
	resourcesDBClient database.ResourcesDBClient,
	kubeApplierDBClients database.KubeApplierDBClients,
	managementClusterLister database.ManagementClusterLister,
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
	redactedContent, err := redactTypedDocument(startingCosmosRecord)
	if err != nil {
		return utils.TrackError(fmt.Errorf("failed to redact starting cosmos record: %w", err))
	}
	logger.Info(fmt.Sprintf("dumping resourceID %v", startingCosmosRecord.ResourceID),
		"currentResourceID", resourceIDToString(startingCosmosRecord.ResourceID),
		"content", redactedContent,
	)

	allCosmosRecords, err := cosmosCRUD.ListRecursive(ctx, nil)
	if err != nil {
		return utils.TrackError(err)
	}

	errs := []error{}
	for _, typedDocument := range allCosmosRecords.Items(ctx) {
		redactedContent, err := redactTypedDocument(typedDocument)
		if err != nil {
			errs = append(errs, fmt.Errorf("failed to redact typed document for %v: %w", resourceIDToString(typedDocument.ResourceID), err))
		} else {
			logger.Info(fmt.Sprintf("dumping resourceID %v", typedDocument.ResourceID),
				"currentResourceID", resourceIDToString(typedDocument.ResourceID),
				"content", redactedContent,
			)
		}
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
			redactedContent, err := redactTypedDocument(doc)
			if err != nil {
				errs = append(errs, fmt.Errorf("failed to redact kube-applier document for %v: %w", resourceIDToString(doc.ResourceID), err))
			} else {
				mcLogger.Info(fmt.Sprintf("dumping kube-applier resourceID %v", doc.ResourceID),
					"currentResourceID", resourceIDToString(doc.ResourceID),
					"content", redactedContent,
				)
			}
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

// redactTypedDocument returns a redacted TypedDocument.
// For documents needing redacting, it unmarshals Properties, redacts sensitive fields,
// and returns a copied TypedDocument with redacted Properties.
// For other document types, it returns the original document unchanged.
// Returns an error if unmarshalling, redaction, or marshalling fails.
func redactTypedDocument(d *database.TypedDocument) (*database.TypedDocument, error) {
	resourceType := strings.ToLower(d.ResourceType)

	switch resourceType {
	case strings.ToLower(api.ClusterResourceType.String()):
		var cluster api.HCPOpenShiftCluster
		return redactAndMarshal(d, &cluster)
	case strings.ToLower(api.NodePoolResourceType.String()):
		var nodePool api.HCPOpenShiftClusterNodePool
		return redactAndMarshal(d, &nodePool)
	case strings.ToLower(api.ExternalAuthResourceType.String()):
		var externalAuth api.HCPOpenShiftClusterExternalAuth
		return redactAndMarshal(d, &externalAuth)
	case strings.ToLower(api.VersionResourceType.String()):
		var version api.HCPOpenShiftVersion
		return redactAndMarshal(d, &version)
	default:
		return d, nil
	}
}

func redactAndMarshal[T any](d *database.TypedDocument, unmarshalInto *T) (*database.TypedDocument, error) {
	if err := json.Unmarshal(d.Properties, unmarshalInto); err != nil {
		return nil, err
	}

	if err := redact.Redact(unmarshalInto); err != nil {
		return nil, err
	}

	redactedProps, err := json.Marshal(unmarshalInto)
	if err != nil {
		return nil, err
	}

	output := d.Clone()
	output.Properties = redactedProps

	return output, nil
}
