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
	"errors"
	"fmt"
	"time"

	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	utilsclock "k8s.io/utils/clock"

	"github.com/Azure/ARO-HCP/backend/pkg/controllers/cluster/credentialrevocation"
	"github.com/Azure/ARO-HCP/backend/pkg/utils/controllerutils"
	"github.com/Azure/ARO-HCP/backend/pkg/utils/operationutils"
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
	revocationLister  corelisters.SystemAdminCredentialRevocationLister
}

var _ controllerutils.SystemAdminCredentialRevocationSyncer = (*revocationMarkRequests)(nil)

const SystemAdminCredentialRevocationMarkRequestsControllerName = "SystemAdminCredentialRevocationMarkRequests"

// NewRevocationMarkRequestsController returns a RevocationWatchingController that
// performs the first step of a revocation: it does a live list of every
// SystemAdminCredentialRequest for the cluster and marks each one with a
// DeletionTimestamp so the per-credential deletion controller tears it down. Once
// every credential request is marked, it sets CredentialsMarkedForDeletion=True
// on the revocation.
func NewRevocationMarkRequestsController(
	clock utilsclock.PassiveClock,
	resourcesDBClient corecosmosstorage.ResourcesDBClient,
	backendInformers coreinformers.BackendInformers,
) controllerutils.Controller {
	_, revocationLister := backendInformers.SystemAdminCredentialRevocations()
	syncer := &revocationMarkRequests{
		clock:             clock,
		resourcesDBClient: resourcesDBClient,
		revocationLister:  revocationLister,
	}

	return controllerutils.NewSystemAdminCredentialRevocationWatchingController(
		SystemAdminCredentialRevocationMarkRequestsControllerName,
		resourcesDBClient,
		backendInformers,
		nil,
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
	if revocation.Status.DeletionTimestamp != nil {
		return nil
	}
	if err := operationutils.LinkCredentialIntent(ctx, c.resourcesDBClient, revocation.ResourceID, revocation.Spec.OperationID, true); err != nil {
		if errors.Is(err, operationutils.ErrCredentialIntentComplete) {
			now := metav1.NewTime(c.clock.Now())
			revocation.Status.DeletionTimestamp = &now
			_, err = revocationCRUD.Replace(ctx, revocation, nil)
		}
		return err
	}
	// A cached active owner avoids unnecessary live claim attempts. Pending
	// documents never block each other; the cluster ETag decides the winner.
	otherActive := false
	if c.revocationLister != nil {
		others, err := c.revocationLister.ListForCluster(ctx, key.SubscriptionID, key.ResourceGroupName, key.HCPClusterName)
		if err != nil {
			return err
		}
		for _, other := range others {
			if other.Spec.OperationID != revocation.Spec.OperationID && meta.IsStatusConditionTrue(other.Status.Conditions, coreapi.SystemAdminCredentialRevocationConditionActive) {
				otherActive = true
			}
		}
	}
	claimed := false
	if !otherActive {
		claimed, err = credentialrevocation.Claim(ctx, c.resourcesDBClient, revocation)
	}
	if err != nil {
		return err
	}
	active := metav1.Condition{Type: coreapi.SystemAdminCredentialRevocationConditionActive, Status: metav1.ConditionTrue, Reason: "Claimed", Message: "Owns cluster credential revocation", LastTransitionTime: metav1.NewTime(c.clock.Now())}
	if !claimed {
		active.Status = metav1.ConditionFalse
		active.Reason = "WaitingForOtherRevocation"
		active.Message = "Waiting for the active revocation owner to release the cluster"
	}
	if meta.SetStatusCondition(&revocation.Status.Conditions, active) {
		revocation, err = revocationCRUD.Replace(ctx, revocation, nil)
		if err != nil {
			return err
		}
	}
	if !claimed {
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
		if owned, err := credentialrevocation.Owns(ctx, c.resourcesDBClient, revocation); err != nil || !owned {
			return err
		}
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
	changed := meta.SetStatusCondition(&replacement.Status.Conditions, metav1.Condition{
		Type:               coreapi.SystemAdminCredentialRevocationConditionCredentialsMarkedForDeletion,
		Status:             metav1.ConditionTrue,
		Reason:             "CredentialsMarked",
		Message:            "All credential requests have been marked for deletion",
		LastTransitionTime: metav1.NewTime(c.clock.Now()),
	})
	if !changed {
		return nil
	}
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
