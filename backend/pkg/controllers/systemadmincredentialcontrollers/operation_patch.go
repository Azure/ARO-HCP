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

package systemadmincredentialcontrollers

import (
	"context"
	"net/http"

	utilsclock "k8s.io/utils/clock"

	"github.com/Azure/ARO-HCP/internal/api"
	"github.com/Azure/ARO-HCP/internal/api/arm"
	"github.com/Azure/ARO-HCP/internal/database"
	"github.com/Azure/ARO-HCP/internal/utils"
)

// patchOperationStatus updates an Operation's Status / Error fields if
// they differ from the desired values. Local copy of operationcontrollers'
// unexported patchOperation; once that helper is exported we can call
// it directly. We deliberately skip the customer-notification step —
// the existing operationcontrollers writes still fire it via their
// generic loop, but for the SystemAdminCredential controllers the
// frontend re-reads the operation document on every customer poll, so
// missing the asynchronous webhook is not observable. The notification
// client is taken as a parameter for forward compatibility.
func patchOperationStatus(
	ctx context.Context,
	clock utilsclock.PassiveClock,
	resourcesDBClient database.ResourcesDBClient,
	oldOp *api.Operation,
	newStatus arm.ProvisioningState,
	newError *arm.CloudErrorBody,
	_ *http.Client,
) error {
	if oldOp.Status == newStatus && oldOp.Error == newError {
		return nil
	}
	updated := oldOp.DeepCopy()
	updated.LastTransitionTime = clock.Now()
	updated.Status = newStatus
	if newError != nil {
		updated.Error = newError
	}
	if _, err := resourcesDBClient.Operations(updated.OperationID.SubscriptionID).Replace(ctx, updated, nil); err != nil {
		return utils.TrackError(err)
	}
	return nil
}
