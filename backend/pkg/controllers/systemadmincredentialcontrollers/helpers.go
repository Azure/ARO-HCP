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

package systemadmincredentialcontrollers

import (
	"context"
	"fmt"
	"strings"

	azcorearm "github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"

	"github.com/Azure/ARO-HCP/backend/pkg/controllers/controllerutils"
	"github.com/Azure/ARO-HCP/internal/api"
	"github.com/Azure/ARO-HCP/internal/api/kubeapplier"
	"github.com/Azure/ARO-HCP/internal/database"
	"github.com/Azure/ARO-HCP/internal/utils"
)

// ensureDeleteDesire creates a DeleteDesire with the same TargetItem
// as the given ApplyDesire if one does not already exist. Conflict
// errors are treated as success (idempotent).
func ensureDeleteDesire(
	ctx context.Context,
	deleteDesireCRUD database.ResourceCRUD[kubeapplier.DeleteDesire, *kubeapplier.DeleteDesire],
	key controllerutils.HCPClusterKey,
	applyDesire *kubeapplier.ApplyDesire,
) error {
	desireName := applyDesire.GetResourceID().Name
	resourceIDStr := kubeapplier.ToClusterScopedDeleteDesireResourceIDString(key.SubscriptionID, key.ResourceGroupName, key.HCPClusterName, desireName)
	resourceID, _ := azcorearm.ParseResourceID(resourceIDStr)

	desire := &kubeapplier.DeleteDesire{
		CosmosMetadata: api.CosmosMetadata{
			ResourceID:   resourceID,
			PartitionKey: strings.ToLower(applyDesire.Spec.ManagementCluster.String()),
		},
		Spec: kubeapplier.DeleteDesireSpec{
			ManagementCluster: applyDesire.Spec.ManagementCluster,
			TargetItem:        applyDesire.Spec.TargetItem,
		},
	}

	_, err := deleteDesireCRUD.Create(ctx, desire, nil)
	if database.IsConflictError(err) {
		return nil
	}
	if err != nil {
		return utils.TrackError(fmt.Errorf("create DeleteDesire %q: %w", desireName, err))
	}
	return nil
}

// isDeleteDesireSuccessful returns true when the DeleteDesire's
// Conditions include Successful=True.
func isDeleteDesireSuccessful(dd *kubeapplier.DeleteDesire) bool {
	for _, c := range dd.Status.Conditions {
		if c.Type == "Successful" && c.Status == "True" {
			return true
		}
	}
	return false
}

// deleteApplyDesireIfExists deletes an ApplyDesire by name, ignoring NotFound errors.
func deleteApplyDesireIfExists(
	ctx context.Context,
	crud database.ResourceCRUD[kubeapplier.ApplyDesire, *kubeapplier.ApplyDesire],
	name string,
) error {
	if err := crud.Delete(ctx, name); err != nil && !database.IsNotFoundError(err) {
		return utils.TrackError(fmt.Errorf("delete ApplyDesire %q: %w", name, err))
	}
	return nil
}

// deleteDeleteDesireIfExists deletes a DeleteDesire by name, ignoring NotFound errors.
func deleteDeleteDesireIfExists(
	ctx context.Context,
	crud database.ResourceCRUD[kubeapplier.DeleteDesire, *kubeapplier.DeleteDesire],
	name string,
) error {
	if err := crud.Delete(ctx, name); err != nil && !database.IsNotFoundError(err) {
		return utils.TrackError(fmt.Errorf("delete DeleteDesire %q: %w", name, err))
	}
	return nil
}

// deleteReadDesireIfExists deletes a ReadDesire by name, ignoring NotFound errors.
func deleteReadDesireIfExists(
	ctx context.Context,
	crud database.ResourceCRUD[kubeapplier.ReadDesire, *kubeapplier.ReadDesire],
	name string,
) error {
	if err := crud.Delete(ctx, name); err != nil && !database.IsNotFoundError(err) {
		return utils.TrackError(fmt.Errorf("delete ReadDesire %q: %w", name, err))
	}
	return nil
}
