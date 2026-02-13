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

package database

import (
	"context"
	"errors"

	"k8s.io/utils/clock"

	"github.com/Azure/ARO-HCP/internal/api/arm"
	"github.com/Azure/ARO-HCP/internal/utils"
)

var localClock clock.Clock = clock.RealClock{}

// CancelActiveOperations queries for operation documents with a non-terminal
// status using the filters specified in opts. For every document returned in
// the query result, CancelActiveOperations adds patch operations to the given
// DBTransaction to mark the document as canceled.
func CancelActiveOperations(ctx context.Context, dbClient DBClient, transaction DBTransaction, opts *DBClientListActiveOperationDocsOptions) error {
	var now = localClock.Now()

	errs := []error{}
	subscriptionID := transaction.GetPartitionKey()
	iterator := dbClient.Operations(subscriptionID).ListActiveOperations(opts)
	for _, operation := range iterator.Items(ctx) {
		operationToWrite := operation.DeepCopy()
		operationToWrite.LastTransitionTime = now
		operationToWrite.Status = arm.ProvisioningStateCanceled
		operationToWrite.Error = &arm.CloudErrorBody{
			Code:    arm.CloudErrorCodeCanceled,
			Message: "This operation was superseded by another",
		}

		_, err := dbClient.Operations(subscriptionID).AddReplaceToTransaction(ctx, transaction, operationToWrite, nil)
		if err != nil {
			errs = append(errs, utils.TrackError(err))
		}
	}
	if err := iterator.GetError(); err != nil {
		errs = append(errs, utils.TrackError(err))
	}

	return errors.Join(errs...)
}
