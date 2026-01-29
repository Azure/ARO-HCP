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

package databasemutationhelpers

import (
	"context"

	azcorearm "github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"

	"github.com/Azure/ARO-HCP/internal/database"
	"github.com/Azure/ARO-HCP/internal/utils"
	"github.com/Azure/ARO-HCP/test-integration/utils/integrationutils"
)

type OperationAccessor interface {
	CompleteOperation(ctx context.Context, resourceIDString string) error
}

type operationAccessor struct {
	dbClient database.DBClient
}

func newOperationAccessor(dbClient database.DBClient) *operationAccessor {
	return &operationAccessor{dbClient: dbClient}
}

var _ OperationAccessor = &operationAccessor{}

func (c operationAccessor) CompleteOperation(ctx context.Context, resourceIDString string) error {
	resourceID, err := azcorearm.ParseResourceID(resourceIDString)
	if err != nil {
		return utils.TrackError(err)
	}

	if err := integrationutils.MarkOperationsCompleteForName(ctx, c.dbClient, resourceID.SubscriptionID, resourceID.Name); err != nil {
		return utils.TrackError(err)
	}
	return nil
}
