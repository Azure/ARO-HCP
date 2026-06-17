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

	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/util/json"
	"k8s.io/client-go/tools/cache"
	utilsclock "k8s.io/utils/clock"

	"github.com/Azure/ARO-HCP/backend/pkg/controllers/controllerutils"
	"github.com/Azure/ARO-HCP/backend/pkg/controllers/operationcontrollers"
	"github.com/Azure/ARO-HCP/backend/pkg/listers"
	"github.com/Azure/ARO-HCP/internal/api"
	"github.com/Azure/ARO-HCP/internal/api/arm"
	"github.com/Azure/ARO-HCP/internal/api/kubeapplier"
	"github.com/Azure/ARO-HCP/internal/database"
	"github.com/Azure/ARO-HCP/internal/systemadmincredential"
	"github.com/Azure/ARO-HCP/internal/utils"
)

// operationRevokeCredentialsDispatch is controller #4. Replaces
// dispatch_revoke_credentials.go. Flips eligible credentials to
// AwaitingRevocation, applies a per-revoke
// CertificateRevocationRequest via ApplyDesire, mirrors the CRR via
// ReadDesire, then moves the operation to Deleting.
type operationRevokeCredentialsDispatch struct {
	clock                utilsclock.PassiveClock
	clusterLister        listers.ClusterLister
	resourcesDBClient    database.ResourcesDBClient
	kubeApplierDBClients database.KubeApplierDBClients
	notificationClient   *http.Client
	hostedClusterNSEnvID string
}

func NewOperationRevokeCredentialsDispatchController(
	clock utilsclock.PassiveClock,
	clusterLister listers.ClusterLister,
	resourcesDBClient database.ResourcesDBClient,
	kubeApplierDBClients database.KubeApplierDBClients,
	notificationClient *http.Client,
	hostedClusterNamespaceEnvIdentifier string,
	activeOperationInformer cache.SharedIndexInformer,
) controllerutils.Controller {
	syncer := &operationRevokeCredentialsDispatch{
		clock:                clock,
		clusterLister:        clusterLister,
		resourcesDBClient:    resourcesDBClient,
		kubeApplierDBClients: kubeApplierDBClients,
		notificationClient:   notificationClient,
		hostedClusterNSEnvID: hostedClusterNamespaceEnvIdentifier,
	}
	return operationcontrollers.NewGenericOperationController(
		"SystemAdminCredentialRevokeDispatch",
		syncer,
		10*time.Second,
		activeOperationInformer,
		resourcesDBClient,
	)
}

func (c *operationRevokeCredentialsDispatch) ShouldProcess(ctx context.Context, op *api.Operation) bool {
	if op.Status.IsTerminal() {
		return false
	}
	if op.Request != database.OperationRequestRevokeCredentials {
		return false
	}
	if op.Status != arm.ProvisioningStateAccepted {
		return false
	}
	if op.ExternalID == nil {
		return false
	}
	return true
}

func (c *operationRevokeCredentialsDispatch) SynchronizeOperation(ctx context.Context, key controllerutils.OperationKey) error {
	logger := utils.LoggerFromContext(ctx)

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
		// Cluster gone; close the operation as Succeeded — nothing to
		// revoke.
		return patchOperationStatus(ctx, c.clock, c.resourcesDBClient, op, arm.ProvisioningStateSucceeded, nil, c.notificationClient)
	}
	if err != nil {
		return fmt.Errorf("get cluster: %w", err)
	}

	// Validate that the sentinel on the cluster still points at this
	// operation. The frontend sets it; if the customer cancelled (or a
	// stale operation hits this controller), bail.
	if cluster.ServiceProviderProperties.RevokeCredentialsOperationID != op.OperationID.Name {
		logger.Info("RevokeCredentialsOperationID does not match this operation; bailing",
			"clusterSentinel", cluster.ServiceProviderProperties.RevokeCredentialsOperationID,
			"operationID", op.OperationID.Name)
		return patchOperationStatus(ctx, c.clock, c.resourcesDBClient, op, arm.ProvisioningStateCanceled, &arm.CloudErrorBody{
			Code:    arm.CloudErrorCodeConflict,
			Message: "Revoke operation superseded",
		}, c.notificationClient)
	}
	if cluster.ServiceProviderProperties.ClusterServiceID == nil {
		// No CS reference yet; wait. The cluster_service_id_sync will retrigger.
		return nil
	}

	spc, err := database.GetOrCreateServiceProviderCluster(ctx, c.resourcesDBClient, clusterRID)
	if err != nil {
		return fmt.Errorf("get/create SPC: %w", err)
	}
	if spc.Status.ManagementClusterResourceID == nil {
		// Nothing to revoke on the MC side if we never placed.
		return patchOperationStatus(ctx, c.clock, c.resourcesDBClient, op, arm.ProvisioningStateSucceeded, nil, c.notificationClient)
	}
	mcRID := spc.Status.ManagementClusterResourceID
	kaClient := c.kubeApplierDBClients.For(ctx, mcRID)
	if kaClient == nil {
		logger.Info("no kube-applier client yet; will retry")
		return nil
	}

	// Flip every Requested/Issued credential under the cluster to
	// AwaitingRevocation. Tolerant of zero credentials (the cutover
	// edge — see PLAN.md "Migration").
	credentialsCRUD := c.resourcesDBClient.HCPClusters(clusterRID.SubscriptionID, clusterRID.ResourceGroupName).
		SystemAdminCredentials(clusterRID.Name)
	iter, err := credentialsCRUD.List(ctx, nil)
	if err != nil {
		return fmt.Errorf("list credentials: %w", err)
	}
	for _, cred := range iter.Items(ctx) {
		if cred == nil {
			continue
		}
		if cred.Status.Phase != api.SystemAdminCredentialPhaseRequested && cred.Status.Phase != api.SystemAdminCredentialPhaseIssued {
			continue
		}
		replacement := cred.DeepCopy()
		replacement.Status.Phase = api.SystemAdminCredentialPhaseAwaitingRevocation
		if _, err := credentialsCRUD.Replace(ctx, replacement, nil); err != nil {
			return fmt.Errorf("flip credential %q to AwaitingRevocation: %w", cred.GetResourceID().Name, err)
		}
	}
	if err := iter.GetError(); err != nil {
		return fmt.Errorf("iterate credentials: %w", err)
	}

	// Build and apply the CertificateRevocationRequest. One CRR per
	// revoke operation; the name carries the per-revoke 16-char suffix.
	revokeSuffix := systemadmincredential.RevokeOpSuffix(op.OperationID.Name)
	hcpNamespace := hostedClusterNamespace(c.hostedClusterNSEnvID, cluster.ServiceProviderProperties.ClusterServiceID.ID())
	crrObj, err := systemadmincredential.BuildRevocationRequest(clusterRID, revokeSuffix, hcpNamespace)
	if err != nil {
		return fmt.Errorf("build CRR: %w", err)
	}
	crrName := systemadmincredential.CRRNamePrefix + "-" + revokeSuffix
	applyCRUD, err := kaClient.ApplyDesiresForCluster(clusterRID.SubscriptionID, clusterRID.ResourceGroupName, clusterRID.Name)
	if err != nil {
		return fmt.Errorf("get ApplyDesires CRUD: %w", err)
	}
	readCRUD, err := kaClient.ReadDesiresForCluster(clusterRID.SubscriptionID, clusterRID.ResourceGroupName, clusterRID.Name)
	if err != nil {
		return fmt.Errorf("get ReadDesires CRUD: %w", err)
	}
	raw, err := json.Marshal(crrObj)
	if err != nil {
		return fmt.Errorf("marshal CRR: %w", err)
	}
	ad := &kubeapplier.ApplyDesire{
		CosmosMetadata: buildScopedDesireMetadata(clusterRID, crrName, kubeapplier.ApplyDesireResourceTypeName),
		Spec: kubeapplier.ApplyDesireSpec{
			ManagementCluster: mcRID,
			TargetItem:        targetItemFor(crrObj),
			KubeContent:       &runtime.RawExtension{Raw: raw},
		},
	}
	if _, err := applyCRUD.Create(ctx, ad, nil); err != nil && !database.IsConflictError(err) {
		return fmt.Errorf("create CRR ApplyDesire: %w", err)
	}
	rd := &kubeapplier.ReadDesire{
		CosmosMetadata: buildScopedDesireMetadata(clusterRID, crrName, kubeapplier.ReadDesireResourceTypeName),
		Spec: kubeapplier.ReadDesireSpec{
			ManagementCluster: mcRID,
			TargetItem:        targetItemFor(crrObj),
		},
	}
	if _, err := readCRUD.Create(ctx, rd, nil); err != nil && !database.IsConflictError(err) {
		return fmt.Errorf("create CRR ReadDesire: %w", err)
	}

	// Move the operation to Deleting; the poller (controller #5) takes
	// it from here.
	return patchOperationStatus(ctx, c.clock, c.resourcesDBClient, op, arm.ProvisioningStateDeleting, nil, c.notificationClient)
}
