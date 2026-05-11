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

	"github.com/Azure/ARO-HCP/internal/database"
	"github.com/Azure/ARO-HCP/internal/utils"
)

func DumpDataToLogger(ctx context.Context, resourcesDBClient database.ResourcesDBClient, resourceID *azcorearm.ResourceID) error {
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
		"currentResourceID", startingCosmosRecord.ResourceID.String(),
		"content", startingCosmosRecord,
	)

	allCosmosRecords, err := cosmosCRUD.ListRecursive(ctx, nil)
	if err != nil {
		return utils.TrackError(err)
	}

	errs := []error{}
	for _, typedDocument := range allCosmosRecords.Items(ctx) {
		logger.Info(fmt.Sprintf("dumping resourceID %v", typedDocument.ResourceID),
			"currentResourceID", typedDocument.ResourceID.String(),
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
				"currentResourceID", operation.ResourceID.String(),
				"content", operation,
			)
		}
	}
	if err := allOperationsForSubscription.GetError(); err != nil {
		errs = append(errs, err)
	}

	return utils.TrackError(errors.Join(errs...))
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
