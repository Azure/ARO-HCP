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
	"strings"

	azcorearm "github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"

	"github.com/Azure/ARO-HCP/internal/database"
	"github.com/Azure/ARO-HCP/internal/utils"
)

func DumpDataToLogger(ctx context.Context, cosmosClient database.DBClient, resourceID *azcorearm.ResourceID) error {
	logger := utils.LoggerFromContext(ctx)

	// load the HCP from the cosmos DB
	cosmosCRUD, err := cosmosClient.UntypedCRUD(*resourceID)
	if err != nil {
		return utils.TrackError(err)
	}
	startingCosmosRecord, err := cosmosCRUD.Get(ctx, resourceID)
	if err != nil {
		return utils.TrackError(err)
	}
	allCosmosRecords, err := cosmosCRUD.ListRecursive(ctx, nil)
	if err != nil {
		return utils.TrackError(err)
	}

	errs := []error{}
	content, err := json.Marshal(startingCosmosRecord)
	if err != nil {
		errs = append(errs, err)
	}
	logger.Info(string(content))

	for _, typedDocument := range allCosmosRecords.Items(ctx) {
		content, err := json.Marshal(typedDocument)
		if err != nil {
			errs = append(errs, err)
		}
		logger.Info(string(content))
	}
	if err := allCosmosRecords.GetError(); err != nil {
		errs = append(errs, err)
	}

	// dump all related operations, including the completed ones.
	allOperationsForSubscription, err := cosmosClient.Operations(resourceID.SubscriptionID).List(ctx, nil)
	if err != nil {
		errs = append(errs, err)
	}
	resourceIDString := strings.ToLower(resourceID.String())
	for _, operation := range allOperationsForSubscription.Items(ctx) {
		currOperationTarget := strings.ToLower(operation.ExternalID.String())
		if strings.HasPrefix(currOperationTarget, resourceIDString) {
			currBytes, err := json.Marshal(operation)
			if err != nil {
				errs = append(errs, err)
				continue
			}
			logger.Info(string(currBytes))
		}
	}
	if err := allOperationsForSubscription.GetError(); err != nil {
		errs = append(errs, err)
	}

	return utils.TrackError(errors.Join(errs...))
}
