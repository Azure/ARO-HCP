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

package operationcontrollers

import (
	"context"
	"errors"
	"net/http"
	"strings"

	ocmerrors "github.com/openshift-online/ocm-sdk-go/errors"

	"github.com/Azure/ARO-HCP/internal/api"
	"github.com/Azure/ARO-HCP/internal/database"
	"github.com/Azure/ARO-HCP/internal/utils"
)

func (c *operationExternalAuthDelete) legacyShouldProcess(ctx context.Context, operation *api.Operation) bool {
	if operation.Status.IsTerminal() {
		return false
	}
	if operation.Request != database.OperationRequestDelete {
		return false
	}
	if operation.ExternalID == nil || !strings.EqualFold(operation.ExternalID.ResourceType.String(), api.ExternalAuthResourceType.String()) {
		return false
	}
	return true
}

func (c *operationExternalAuthDelete) legacySynchronizeOperation(ctx context.Context, operation *api.Operation) error {
	logger := utils.LoggerFromContext(ctx)

	if !c.legacyShouldProcess(ctx, operation) {
		return nil // no work to do
	}

	_, err := c.clusterServiceClient.GetExternalAuth(ctx, operation.InternalID)
	var ocmGetExternalAuthError *ocmerrors.Error
	if err != nil && errors.As(err, &ocmGetExternalAuthError) && ocmGetExternalAuthError.Status() == http.StatusNotFound {
		logger.Info("external auth was deleted")

		err = SetDeleteOperationAsCompleted(ctx, c.clock, c.resourcesDBClient, operation, postAsyncNotificationFn(c.notificationClient))
		if err != nil {
			return utils.TrackError(err)
		}
		return nil
	}
	if err != nil {
		return utils.TrackError(err)
	}

	return nil
}
