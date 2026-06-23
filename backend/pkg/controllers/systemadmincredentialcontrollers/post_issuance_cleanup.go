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
	"time"

	"github.com/Azure/ARO-HCP/backend/pkg/controllers/controllerutils"
	"github.com/Azure/ARO-HCP/backend/pkg/informers"
	"github.com/Azure/ARO-HCP/internal/api"
	"github.com/Azure/ARO-HCP/internal/api/kubeapplier"
	controllerutil "github.com/Azure/ARO-HCP/internal/controllerutils"
	"github.com/Azure/ARO-HCP/internal/database"
	unionkubeapplierinformers "github.com/Azure/ARO-HCP/internal/database/unioninformers/kubeapplier"
	"github.com/Azure/ARO-HCP/internal/utils"
)

// postIssuanceCleanupSyncer tears down per-credential ApplyDesires
// and ReadDesires once a credential reaches Phase=Issued or Failed.
// It creates DeleteDesires for ApplyDesires, waits for their
// Successful=True condition, then removes all related Cosmos docs.
type postIssuanceCleanupSyncer struct {
	cooldownChecker controllerutil.CooldownChecker

	resourcesDBClient    database.ResourcesDBClient
	kubeApplierDBClients database.KubeApplierDBClients
}

var _ controllerutils.ClusterSyncer = (*postIssuanceCleanupSyncer)(nil)

// NewPostIssuanceCleanupController wires the post-issuance cleanup
// controller.
func NewPostIssuanceCleanupController(
	resourcesDBClient database.ResourcesDBClient,
	kubeApplierDBClients database.KubeApplierDBClients,
	backendInformers informers.BackendInformers,
	kubeApplierInformers *unionkubeapplierinformers.UnionKubeApplierInformers,
) controllerutils.Controller {
	syncer := &postIssuanceCleanupSyncer{
		cooldownChecker:      controllerutil.NewTimeBasedCooldownChecker(30 * time.Second),
		resourcesDBClient:    resourcesDBClient,
		kubeApplierDBClients: kubeApplierDBClients,
	}

	return controllerutils.NewClusterWatchingController(
		"SystemAdminCredentialPostIssuanceCleanup",
		resourcesDBClient,
		backendInformers,
		kubeApplierInformers,
		1*time.Minute,
		syncer,
	)
}

func (c *postIssuanceCleanupSyncer) CooldownChecker() controllerutil.CooldownChecker {
	return c.cooldownChecker
}

func (c *postIssuanceCleanupSyncer) SyncOnce(ctx context.Context, key controllerutils.HCPClusterKey) error {
	logger := utils.LoggerFromContext(ctx)

	spc, err := database.GetOrCreateServiceProviderCluster(ctx, c.resourcesDBClient, key.GetResourceID())
	if err != nil {
		return utils.TrackError(fmt.Errorf("failed to get ServiceProviderCluster: %w", err))
	}
	mcResourceID := spc.Status.ManagementClusterResourceID
	if mcResourceID == nil {
		return nil
	}

	kaClient := c.kubeApplierDBClients.For(ctx, mcResourceID)
	if kaClient == nil {
		return nil
	}

	credCRUD := c.resourcesDBClient.SystemAdminCredentials(key.SubscriptionID, key.ResourceGroupName, key.HCPClusterName)
	iter, err := credCRUD.List(ctx, nil)
	if err != nil {
		return utils.TrackError(fmt.Errorf("failed to list SystemAdminCredentials: %w", err))
	}

	for _, cred := range iter.Items(ctx) {
		phase := cred.Status.Phase
		if phase != api.SystemAdminCredentialPhaseIssued && phase != api.SystemAdminCredentialPhaseFailed {
			continue
		}
		if len(cred.Status.OutstandingDesires) == 0 {
			continue
		}

		credName := cred.GetResourceID().Name
		logger.Info("cleaning up post-issuance desires", "credentialName", credName)

		replacement := cred.DeepCopy()
		err := driveDesireTeardown(ctx, kaClient, key, replacement)
		if err != nil {
			return utils.TrackError(err)
		}

		if _, err := credCRUD.Replace(ctx, replacement, nil); err != nil {
			return utils.TrackError(fmt.Errorf("failed to replace SystemAdminCredential: %w", err))
		}
	}

	if err := iter.GetError(); err != nil {
		return utils.TrackError(fmt.Errorf("error iterating SystemAdminCredentials: %w", err))
	}

	return nil
}

// driveDesireTeardown walks OutstandingDesires and drives cleanup.
// For ApplyDesires: ensure DeleteDesire exists, wait for Successful=True,
// then delete both. For ReadDesires: delete directly.
// It mutates cred.Status.OutstandingDesires in place.
func driveDesireTeardown(
	ctx context.Context,
	kaClient database.KubeApplierDBClient,
	key controllerutils.HCPClusterKey,
	cred *api.SystemAdminCredential,
) error {
	applyDesireCRUD, err := kaClient.ApplyDesiresForCluster(key.SubscriptionID, key.ResourceGroupName, key.HCPClusterName)
	if err != nil {
		return fmt.Errorf("get ApplyDesire CRUD: %w", err)
	}
	deleteDesireCRUD, err := kaClient.DeleteDesiresForCluster(key.SubscriptionID, key.ResourceGroupName, key.HCPClusterName)
	if err != nil {
		return fmt.Errorf("get DeleteDesire CRUD: %w", err)
	}
	readDesireCRUD, err := kaClient.ReadDesiresForCluster(key.SubscriptionID, key.ResourceGroupName, key.HCPClusterName)
	if err != nil {
		return fmt.Errorf("get ReadDesire CRUD: %w", err)
	}

	remaining := make([]api.SystemAdminCredentialDesireRef, 0, len(cred.Status.OutstandingDesires))

	for _, ref := range cred.Status.OutstandingDesires {
		switch ref.Kind {
		case api.SystemAdminCredentialDesireKindApply:
			done, err := driveApplyDesireDeletion(ctx, applyDesireCRUD, deleteDesireCRUD, key, ref.Name)
			if err != nil {
				return err
			}
			if !done {
				remaining = append(remaining, ref)
			}

		case api.SystemAdminCredentialDesireKindRead:
			err := deleteReadDesire(ctx, readDesireCRUD, ref.Name)
			if err != nil {
				return err
			}
			// ReadDesire deleted directly, don't keep in remaining

		case api.SystemAdminCredentialDesireKindDelete:
			done, err := checkDeleteDesireComplete(ctx, deleteDesireCRUD, applyDesireCRUD, key, ref.Name)
			if err != nil {
				return err
			}
			if !done {
				remaining = append(remaining, ref)
			}

		default:
			remaining = append(remaining, ref)
		}
	}

	cred.Status.OutstandingDesires = remaining
	return nil
}

// driveApplyDesireDeletion ensures a DeleteDesire exists for the named
// ApplyDesire and checks completion. Returns true when both the
// ApplyDesire and DeleteDesire have been removed from Cosmos.
func driveApplyDesireDeletion(
	ctx context.Context,
	applyDesireCRUD database.ResourceCRUD[kubeapplier.ApplyDesire, *kubeapplier.ApplyDesire],
	deleteDesireCRUD database.ResourceCRUD[kubeapplier.DeleteDesire, *kubeapplier.DeleteDesire],
	key controllerutils.HCPClusterKey,
	desireName string,
) (bool, error) {
	applyDesire, err := applyDesireCRUD.Get(ctx, desireName)
	if database.IsNotFoundError(err) {
		// ApplyDesire already gone; clean up DeleteDesire if it exists
		_ = deleteDeleteDesireIfExists(ctx, deleteDesireCRUD, desireName)
		return true, nil
	}
	if err != nil {
		return false, utils.TrackError(fmt.Errorf("get ApplyDesire %q: %w", desireName, err))
	}

	// Ensure DeleteDesire exists
	err = ensureDeleteDesire(ctx, deleteDesireCRUD, key, applyDesire)
	if err != nil {
		return false, err
	}

	// Check if DeleteDesire reports Successful=True
	deleteDesire, err := deleteDesireCRUD.Get(ctx, desireName)
	if database.IsNotFoundError(err) {
		return false, nil // just created; wait
	}
	if err != nil {
		return false, utils.TrackError(fmt.Errorf("get DeleteDesire %q: %w", desireName, err))
	}

	if !isDeleteDesireSuccessful(deleteDesire) {
		return false, nil
	}

	// Delete both Cosmos documents
	if err := applyDesireCRUD.Delete(ctx, desireName); err != nil && !database.IsNotFoundError(err) {
		return false, utils.TrackError(fmt.Errorf("delete ApplyDesire %q: %w", desireName, err))
	}
	if err := deleteDesireCRUD.Delete(ctx, desireName); err != nil && !database.IsNotFoundError(err) {
		return false, utils.TrackError(fmt.Errorf("delete DeleteDesire %q: %w", desireName, err))
	}

	return true, nil
}

func checkDeleteDesireComplete(
	ctx context.Context,
	deleteDesireCRUD database.ResourceCRUD[kubeapplier.DeleteDesire, *kubeapplier.DeleteDesire],
	applyDesireCRUD database.ResourceCRUD[kubeapplier.ApplyDesire, *kubeapplier.ApplyDesire],
	key controllerutils.HCPClusterKey,
	desireName string,
) (bool, error) {
	deleteDesire, err := deleteDesireCRUD.Get(ctx, desireName)
	if database.IsNotFoundError(err) {
		return true, nil
	}
	if err != nil {
		return false, utils.TrackError(fmt.Errorf("get DeleteDesire %q: %w", desireName, err))
	}

	if !isDeleteDesireSuccessful(deleteDesire) {
		return false, nil
	}

	// Clean up
	_ = deleteApplyDesireIfExists(ctx, applyDesireCRUD, desireName)
	if err := deleteDesireCRUD.Delete(ctx, desireName); err != nil && !database.IsNotFoundError(err) {
		return false, utils.TrackError(fmt.Errorf("delete DeleteDesire %q: %w", desireName, err))
	}
	return true, nil
}

func deleteReadDesire(
	ctx context.Context,
	crud database.ResourceCRUD[kubeapplier.ReadDesire, *kubeapplier.ReadDesire],
	name string,
) error {
	if err := crud.Delete(ctx, name); err != nil && !database.IsNotFoundError(err) {
		return utils.TrackError(fmt.Errorf("delete ReadDesire %q: %w", name, err))
	}
	return nil
}
