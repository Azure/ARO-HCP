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
	"fmt"
	"time"

	apimeta "k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/Azure/ARO-HCP/backend/pkg/controllers/controllerutils"
	"github.com/Azure/ARO-HCP/backend/pkg/informers"
	"github.com/Azure/ARO-HCP/backend/pkg/listers"
	"github.com/Azure/ARO-HCP/internal/api"
	controllerutil "github.com/Azure/ARO-HCP/internal/controllerutils"
	"github.com/Azure/ARO-HCP/internal/database"
	"github.com/Azure/ARO-HCP/internal/utils"
)

// credentialDeletionFinalizer is the controller that physically deletes
// a SystemAdminCredential Cosmos document once two preconditions hold:
//   - Spec.DeletionTimestamp is set (someone asked for the credential to
//     go away — typically the SystemAdminRevocation desires-initiator or
//     the cluster-deletion path).
//   - Status.Conditions[DesiresCleanedUp] = True (the credentialDesiresCreator
//     finished tearing down every kube-applier desire the credential owned).
//
// Without the second precondition, deleting the credential doc would
// strand its kube-applier ApplyDesires on the management cluster.
type credentialDeletionFinalizer struct {
	cooldownChecker   controllerutil.CooldownChecker
	credentialLister  listers.SystemAdminCredentialLister
	resourcesDBClient database.ResourcesDBClient
}

func NewCredentialDeletionFinalizerController(
	credentialLister listers.SystemAdminCredentialLister,
	resourcesDBClient database.ResourcesDBClient,
	activeOperationLister listers.ActiveOperationLister,
	backendInformers informers.BackendInformers,
) controllerutils.Controller {
	syncer := &credentialDeletionFinalizer{
		cooldownChecker:   controllerutils.DefaultActiveOperationPrioritizingCooldown(activeOperationLister),
		credentialLister:  credentialLister,
		resourcesDBClient: resourcesDBClient,
	}
	return controllerutils.NewSystemAdminCredentialWatchingController(
		"SystemAdminCredentialDeletionFinalizer",
		resourcesDBClient,
		backendInformers,
		1*time.Minute,
		syncer,
	)
}

func (c *credentialDeletionFinalizer) CooldownChecker() controllerutil.CooldownChecker {
	return c.cooldownChecker
}

func (c *credentialDeletionFinalizer) SyncOnce(ctx context.Context, key controllerutils.HCPSystemAdminCredentialKey) error {
	logger := utils.LoggerFromContext(ctx)

	credential, err := c.credentialLister.Get(ctx, key.SubscriptionID, key.ResourceGroupName, key.HCPClusterName, key.HCPSystemAdminCredentialName)
	if database.IsNotFoundError(err) {
		return nil
	}
	if err != nil {
		return utils.TrackError(fmt.Errorf("get credential: %w", err))
	}
	if credential.Spec.DeletionTimestamp == nil {
		return nil
	}
	cond := apimeta.FindStatusCondition(credential.Status.Conditions, api.SystemAdminCredentialDesiresCleanedUpConditionType)
	if cond == nil || cond.Status != metav1.ConditionTrue {
		// credentialDesiresCreator has not finished tearing down kube-applier
		// content yet. Wait — we'll be re-enqueued when the credential is
		// updated again.
		return nil
	}

	credentialsCRUD := c.resourcesDBClient.HCPClusters(key.SubscriptionID, key.ResourceGroupName).
		SystemAdminCredentials(key.HCPClusterName)
	if err := credentialsCRUD.Delete(ctx, key.HCPSystemAdminCredentialName); err != nil && !database.IsNotFoundError(err) {
		return utils.TrackError(fmt.Errorf("delete credential: %w", err))
	}
	logger.Info("credential deleted by finalizer", "credential", key.HCPSystemAdminCredentialName)
	return nil
}
