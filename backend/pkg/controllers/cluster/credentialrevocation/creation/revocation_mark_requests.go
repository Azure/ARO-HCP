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

package creation

import (
	"context"
	"fmt"
	"time"

	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	utilsclock "k8s.io/utils/clock"

	"github.com/Azure/ARO-HCP/backend/pkg/utils/controllerutils"
	"github.com/Azure/ARO-HCP/internal/api/coreapi"
	"github.com/Azure/ARO-HCP/internal/database/cosmosstorage/corecosmosstorage"
	"github.com/Azure/ARO-HCP/internal/database/cosmosstorage/cosmosstorageutils"
	"github.com/Azure/ARO-HCP/internal/database/informers/coreinformers"
	"github.com/Azure/ARO-HCP/internal/database/listers/corelisters"
	"github.com/Azure/ARO-HCP/internal/utils"
)

type revocationMarkRequests struct {
	clock             utilsclock.PassiveClock
	resourcesDBClient corecosmosstorage.ResourcesDBClient
}

var _ controllerutils.SystemAdminCredentialRevocationSyncer = (*revocationMarkRequests)(nil)

// NewRevocationMarkRequestsController returns a RevocationWatchingController that
// performs the first step of a revocation: it does a live list of every
// SystemAdminCredentialRequest for the cluster and marks each one with a
// DeletionTimestamp so the per-credential deletion controller tears it down. Once
// every credential request is marked, it sets CredentialsMarkedForDeletion=True
// on the revocation.
func NewRevocationMarkRequestsController(
	clock utilsclock.PassiveClock,
	activeOperationLister corelisters.ActiveOperationLister,
	resourcesDBClient corecosmosstorage.ResourcesDBClient,
	backendInformers coreinformers.BackendInformers,
) controllerutils.Controller {
	syncer := &revocationMarkRequests{
		clock:             clock,
		resourcesDBClient: resourcesDBClient,
	}

	return controllerutils.NewSystemAdminCredentialRevocationWatchingController(
		"SystemAdminCredentialRevocationMarkRequests",
		resourcesDBClient,
		backendInformers,
		1*time.Minute,
		syncer,
	)
}

func (c *revocationMarkRequests) SyncOnce(ctx context.Context, key controllerutils.SystemAdminCredentialRevocationKey) error {
	logger := utils.LoggerFromContext(ctx)

	revocationCRUD := c.resourcesDBClient.HCPClusters(key.SubscriptionID, key.ResourceGroupName).SystemAdminCredentialRevocations(key.HCPClusterName)
	revocation, err := revocationCRUD.Get(ctx, key.RevocationName)
	if cosmosstorageutils.IsNotFoundError(err) {
		return nil
	}
	if err != nil {
		return utils.TrackError(fmt.Errorf("failed to get SystemAdminCredentialRevocation: %w", err))
	}

	// Once the revocation is complete/being deleted there is nothing to mark.
	if revocation.Status.DeletionTimestamp != nil || meta.IsStatusConditionTrue(revocation.Status.Conditions, coreapi.SystemAdminCredentialRevocationConditionCredentialsMarkedForDeletion) {
		return nil
	}

	// Live list every credential request for the cluster and mark those that are
	// not yet marked with a DeletionTimestamp.
	credCRUD := c.resourcesDBClient.HCPClusters(key.SubscriptionID, key.ResourceGroupName).SystemAdminCredentialRequests(key.HCPClusterName)
	iter, err := credCRUD.List(ctx, nil)
	if err != nil {
		return utils.TrackError(fmt.Errorf("failed to list SystemAdminCredentialRequests: %w", err))
	}
	for _, cred := range iter.Items(ctx) {
		if cred.Status.DeletionTimestamp != nil {
			continue
		}
		replacement := cred.DeepCopy()
		now := metav1.NewTime(c.clock.Now())
		replacement.Status.DeletionTimestamp = &now
		// This controller is keyed on the revocation, not the credential request,
		// so a failed credential write is NOT retriggered by our informer. Return
		// an error (including on precondition failure) so the workqueue retries.
		if _, err := credCRUD.Replace(ctx, replacement, nil); err != nil {
			return utils.TrackError(fmt.Errorf("failed to mark credential %s for deletion: %w", cred.ResourceID.Name, err))
		}
		logger.Info("marked credential request for deletion", "credential", cred.ResourceID.Name)
	}
	if err := iter.GetError(); err != nil {
		return utils.TrackError(fmt.Errorf("failed to iterate SystemAdminCredentialRequests: %w", err))
	}

	// Every credential request now carries a DeletionTimestamp; record that on the
	// revocation so the desires controller can proceed to completion.
	replacement := revocation.DeepCopy()
	meta.SetStatusCondition(&replacement.Status.Conditions, metav1.Condition{
		Type:    coreapi.SystemAdminCredentialRevocationConditionCredentialsMarkedForDeletion,
		Status:  metav1.ConditionTrue,
		Reason:  "CredentialsMarked",
		Message: "All credential requests have been marked for deletion",
	})
	if _, err := revocationCRUD.Replace(ctx, replacement, nil); err != nil {
		if cosmosstorageutils.IsPreconditionFailedError(err) {
			// Will be retriggered by the informer.
			return nil
		}
		return utils.TrackError(fmt.Errorf("failed to set CredentialsMarkedForDeletion condition: %w", err))
	}

	logger.Info("all credential requests marked for deletion")
	return nil
}
