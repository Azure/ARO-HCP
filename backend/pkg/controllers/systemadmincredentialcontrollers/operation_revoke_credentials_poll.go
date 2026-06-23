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
	"net/http"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/tools/cache"
	utilsclock "k8s.io/utils/clock"

	certificatesv1alpha1 "github.com/openshift/hypershift/api/certificates/v1alpha1"

	"github.com/Azure/ARO-HCP/backend/pkg/controllers/controllerutils"
	"github.com/Azure/ARO-HCP/backend/pkg/controllers/operationcontrollers"
	"github.com/Azure/ARO-HCP/backend/pkg/listers"
	"github.com/Azure/ARO-HCP/internal/api"
	"github.com/Azure/ARO-HCP/internal/api/arm"
	"github.com/Azure/ARO-HCP/internal/database"
	"github.com/Azure/ARO-HCP/internal/systemadmincredential"
)

// operationRevokeCredentialsPoll is controller #5 (re-sourced). It
// watches the SystemAdminRevocation document written by the dispatcher
// and waits for the revocationDesiresCreator to flip
// SystemAdminRevocationCompleteConditionType to True. Once True, it
// clears the cluster's RevokeCredentialsOperationID sentinel and
// promotes the ARM operation to Succeeded.
//
// All teardown work (credential desires, CRR desires) is owned by
// other controllers; this controller never touches kube-applier
// directly.
type operationRevokeCredentialsPoll struct {
	clock              utilsclock.PassiveClock
	clusterLister      listers.ClusterLister
	resourcesDBClient  database.ResourcesDBClient
	notificationClient *http.Client
}

func NewOperationRevokeCredentialsPollController(
	clock utilsclock.PassiveClock,
	clusterLister listers.ClusterLister,
	resourcesDBClient database.ResourcesDBClient,
	notificationClient *http.Client,
	activeOperationInformer cache.SharedIndexInformer,
) controllerutils.Controller {
	syncer := &operationRevokeCredentialsPoll{
		clock:              clock,
		clusterLister:      clusterLister,
		resourcesDBClient:  resourcesDBClient,
		notificationClient: notificationClient,
	}
	return operationcontrollers.NewGenericOperationController(
		"SystemAdminCredentialRevokePoll",
		syncer,
		10*time.Second,
		activeOperationInformer,
		resourcesDBClient,
	)
}

func (c *operationRevokeCredentialsPoll) ShouldProcess(ctx context.Context, op *api.Operation) bool {
	if op.Status.IsTerminal() {
		return false
	}
	if op.Request != database.OperationRequestRevokeCredentials {
		return false
	}
	if op.Status != arm.ProvisioningStateDeleting {
		return false
	}
	return true
}

func (c *operationRevokeCredentialsPoll) SynchronizeOperation(ctx context.Context, key controllerutils.OperationKey) error {
	op, err := c.resourcesDBClient.Operations(key.SubscriptionID).Get(ctx, key.OperationName)
	if database.IsNotFoundError(err) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("get operation: %w", err)
	}
	if !c.ShouldProcess(ctx, op) {
		return nil
	}

	clusterRID := op.ExternalID
	cluster, err := c.clusterLister.Get(ctx, clusterRID.SubscriptionID, clusterRID.ResourceGroupName, clusterRID.Name)
	if database.IsNotFoundError(err) {
		return patchOperationStatus(ctx, c.clock, c.resourcesDBClient, op, arm.ProvisioningStateSucceeded, nil, c.notificationClient)
	}
	if err != nil {
		return fmt.Errorf("get cluster: %w", err)
	}

	revokeSuffix := systemadmincredential.RevokeOpSuffix(op.OperationID.Name)
	revocationCRUD := c.resourcesDBClient.HCPClusters(clusterRID.SubscriptionID, clusterRID.ResourceGroupName).
		SystemAdminRevocations(clusterRID.Name)
	revocation, err := revocationCRUD.Get(ctx, revokeSuffix)
	if database.IsNotFoundError(err) {
		// Dispatcher hasn't created the SystemAdminRevocation doc yet;
		// wait for the next poll.
		return nil
	}
	if err != nil {
		return fmt.Errorf("get SystemAdminRevocation: %w", err)
	}
	if !isRevocationConditionTrue(revocation, api.SystemAdminRevocationCompleteConditionType) {
		// Still in progress; revocationDesiresCreator will flip the
		// condition once the CRR drains.
		return nil
	}

	// Revocation complete: clear the cluster sentinel and promote the op.
	if cluster.ServiceProviderProperties.RevokeCredentialsOperationID == op.OperationID.Name {
		updated := cluster.DeepCopy()
		updated.ServiceProviderProperties.RevokeCredentialsOperationID = ""
		if _, err := c.resourcesDBClient.HCPClusters(clusterRID.SubscriptionID, clusterRID.ResourceGroupName).Replace(ctx, updated, nil); database.IsPreconditionFailedError(err) {
			// Cluster changed under us; re-poll will retry.
			return nil
		} else if err != nil {
			return fmt.Errorf("clear RevokeCredentialsOperationID: %w", err)
		}
	}
	return patchOperationStatus(ctx, c.clock, c.resourcesDBClient, op, arm.ProvisioningStateSucceeded, nil, c.notificationClient)
}

func crrPreviousCertificatesRevoked(crr *certificatesv1alpha1.CertificateRevocationRequest) bool {
	for _, c := range crr.Status.Conditions {
		if c.Type == certificatesv1alpha1.PreviousCertificatesRevokedType && c.Status == metav1.ConditionTrue {
			return true
		}
	}
	return false
}
