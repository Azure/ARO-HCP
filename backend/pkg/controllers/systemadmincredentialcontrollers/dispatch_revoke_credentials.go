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
	"encoding/json"
	"fmt"
	"strings"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/tools/cache"
	utilsclock "k8s.io/utils/clock"

	azcorearm "github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"

	"github.com/Azure/ARO-HCP/backend/pkg/controllers/controllerutils"
	"github.com/Azure/ARO-HCP/backend/pkg/controllers/operationcontrollers"
	"github.com/Azure/ARO-HCP/backend/pkg/maestrohelpers"
	"github.com/Azure/ARO-HCP/internal/api"
	"github.com/Azure/ARO-HCP/internal/api/arm"
	"github.com/Azure/ARO-HCP/internal/api/kubeapplier"
	"github.com/Azure/ARO-HCP/internal/database"
	"github.com/Azure/ARO-HCP/internal/systemadmincredential"
	"github.com/Azure/ARO-HCP/internal/utils"
)

type dispatchRevokeCredentials struct {
	clock                utilsclock.PassiveClock
	resourcesDBClient    database.ResourcesDBClient
	kubeApplierDBClients database.KubeApplierDBClients

	hostedClusterNamespaceEnvIdentifier string
}

// NewDispatchRevokeCredentialsController returns a Controller that handles
// RevokeCredentials operations. It lists every active credential under the
// cluster, flips each to AwaitingRevocation, and creates the cluster-scoped
// CRR ApplyDesire + ReadDesire.
//
// Operation documents relevant to this controller will have the following values:
//
//	ResourceType: Microsoft.RedHatOpenShift/hcpOpenShiftClusters
//	     Request: RevokeCredentials
//	      Status: Accepted
func NewDispatchRevokeCredentialsController(
	clock utilsclock.PassiveClock,
	resourcesDBClient database.ResourcesDBClient,
	kubeApplierDBClients database.KubeApplierDBClients,
	activeOperationInformer cache.SharedIndexInformer,
	hostedClusterNamespaceEnvIdentifier string,
) controllerutils.Controller {
	syncer := &dispatchRevokeCredentials{
		clock:                               clock,
		resourcesDBClient:                   resourcesDBClient,
		kubeApplierDBClients:                kubeApplierDBClients,
		hostedClusterNamespaceEnvIdentifier: hostedClusterNamespaceEnvIdentifier,
	}

	controller := operationcontrollers.NewGenericOperationController(
		"SystemAdminCredentialDispatchRevokeCredentials",
		syncer,
		10*time.Second,
		activeOperationInformer,
		resourcesDBClient,
	)

	return controller
}

func (c *dispatchRevokeCredentials) ShouldProcess(ctx context.Context, operation *api.Operation) bool {
	if operation.Status.IsTerminal() {
		return false
	}
	if operation.Request != api.OperationRequestRevokeCredentials {
		return false
	}
	if operation.Status != arm.ProvisioningStateAccepted {
		return false
	}
	return true
}

func (c *dispatchRevokeCredentials) SynchronizeOperation(ctx context.Context, key controllerutils.OperationKey) error {
	logger := utils.LoggerFromContext(ctx)
	logger.Info("checking revoke operation")

	operation, err := c.resourcesDBClient.Operations(key.SubscriptionID).Get(ctx, key.OperationName)
	if database.IsNotFoundError(err) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("failed to get active operation: %w", err)
	}
	if !c.ShouldProcess(ctx, operation) {
		return nil
	}

	cluster, err := c.resourcesDBClient.HCPClusters(operation.ExternalID.SubscriptionID, operation.ExternalID.ResourceGroupName).Get(ctx, operation.ExternalID.Name)
	if err != nil {
		return utils.TrackError(err)
	}

	// Verify the operation matches the cluster's revoke sentinel.
	if cluster.ServiceProviderProperties.RevokeCredentialsOperationID != operation.OperationID.Name {
		logger.Info("operation does not match cluster's RevokeCredentialsOperationID, skipping")
		return nil
	}

	// List all active credentials and flip them to AwaitingRevocation.
	credCRUD := c.resourcesDBClient.SystemAdminCredentialRequests(
		operation.ExternalID.SubscriptionID,
		operation.ExternalID.ResourceGroupName,
		operation.ExternalID.Name,
	)
	iter, err := credCRUD.List(ctx, nil)
	if err != nil {
		return utils.TrackError(fmt.Errorf("failed to list credentials: %w", err))
	}

	for _, cred := range iter.Items(ctx) {
		if !cred.Status.IsPending() && !cred.Status.IsIssued() {
			continue
		}
		replacement := cred.DeepCopy()
		replacement.Status.SetCondition(api.SystemAdminCredentialRequestConditionAwaitingRevocation, metav1.ConditionTrue, "RevocationRequested", "Revocation has been requested")
		if _, err := credCRUD.Replace(ctx, replacement, nil); err != nil {
			return utils.TrackError(fmt.Errorf("failed to flip credential to AwaitingRevocation: %w", err))
		}
	}
	if err := iter.GetError(); err != nil {
		return utils.TrackError(fmt.Errorf("failed to iterate credentials: %w", err))
	}

	// Write the cluster-scoped CRR ApplyDesire + ReadDesire.
	revokeOpSuffix := strings.ReplaceAll(operation.OperationID.Name, "-", "")
	if len(revokeOpSuffix) > 16 {
		revokeOpSuffix = revokeOpSuffix[:16]
	}

	spc, err := database.GetOrCreateServiceProviderCluster(ctx, c.resourcesDBClient, operation.ExternalID)
	if err != nil {
		return utils.TrackError(err)
	}
	mcResourceID := spc.Status.ManagementClusterResourceID
	if mcResourceID == nil {
		return fmt.Errorf("management cluster resource ID not set for cluster")
	}

	csClusterID := cluster.ServiceProviderProperties.ClusterServiceID.ID()
	hcpNamespace := fmt.Sprintf("ocm-%s-%s", c.hostedClusterNamespaceEnvIdentifier, csClusterID)

	clusterResourceID := operation.ExternalID

	// Create CRR ApplyDesire
	kaClient := c.kubeApplierDBClients.For(ctx, mcResourceID)
	if kaClient == nil {
		return fmt.Errorf("no kube-applier client for management cluster")
	}

	crrObj := systemadmincredential.BuildRevocationRequest(clusterResourceID, revokeOpSuffix, hcpNamespace)
	crrDesireName := fmt.Sprintf("systemAdminCredentialRevocation-%s", revokeOpSuffix)

	applyCRUD, err := kaClient.ApplyDesiresForCluster(operation.ExternalID.SubscriptionID, operation.ExternalID.ResourceGroupName, operation.ExternalID.Name)
	if err != nil {
		return utils.TrackError(err)
	}

	if err := c.createCRRApplyDesire(ctx, applyCRUD, operation.ExternalID, crrDesireName, mcResourceID, hcpNamespace, crrObj); err != nil {
		return err
	}

	// Create CRR ReadDesire
	readCRUD, err := kaClient.ReadDesiresForCluster(operation.ExternalID.SubscriptionID, operation.ExternalID.ResourceGroupName, operation.ExternalID.Name)
	if err != nil {
		return utils.TrackError(err)
	}

	crrReadDesireName := maestrohelpers.ReadDesireNameForSystemAdminCredentialRequestRevocation(revokeOpSuffix)
	readResourceIDStr := kubeapplier.ToClusterScopedReadDesireResourceIDString(
		operation.ExternalID.SubscriptionID, operation.ExternalID.ResourceGroupName, operation.ExternalID.Name, crrReadDesireName)
	readResourceID, _ := azcorearm.ParseResourceID(readResourceIDStr)

	readDesire := &kubeapplier.ReadDesire{
		CosmosMetadata: api.CosmosMetadata{
			ResourceID:   readResourceID,
			PartitionKey: strings.ToLower(mcResourceID.String()),
		},
		Spec: kubeapplier.ReadDesireSpec{
			ManagementCluster: mcResourceID,
			TargetItem: kubeapplier.ResourceReference{
				Group:     "certificates.hypershift.openshift.io",
				Version:   "v1alpha1",
				Resource:  "certificaterevocationrequests",
				Namespace: hcpNamespace,
				Name:      crrObj.Name,
			},
		},
	}
	if _, err := readCRUD.Create(ctx, readDesire, nil); err != nil && !database.IsConflictError(err) {
		return utils.TrackError(fmt.Errorf("create CRR ReadDesire: %w", err))
	}

	// Move the operation to Deleting.
	replacement := operation.DeepCopy()
	replacement.Status = arm.ProvisioningStateDeleting
	replacement.LastTransitionTime = c.clock.Now()
	if _, err := c.resourcesDBClient.Operations(key.SubscriptionID).Replace(ctx, replacement, nil); err != nil {
		return utils.TrackError(err)
	}

	logger.Info("dispatched revocation", "revokeOpSuffix", revokeOpSuffix)
	return nil
}

func (c *dispatchRevokeCredentials) createCRRApplyDesire(
	ctx context.Context,
	crud database.ResourceCRUD[kubeapplier.ApplyDesire, *kubeapplier.ApplyDesire],
	clusterID *azcorearm.ResourceID,
	desireName string,
	mcResourceID *azcorearm.ResourceID,
	hcpNamespace string,
	obj interface{},
) error {
	resourceIDStr := kubeapplier.ToClusterScopedApplyDesireResourceIDString(
		clusterID.SubscriptionID, clusterID.ResourceGroupName, clusterID.Name, desireName)
	resourceID, _ := azcorearm.ParseResourceID(resourceIDStr)

	rawJSON, err := json.Marshal(obj)
	if err != nil {
		return utils.TrackError(fmt.Errorf("failed to marshal CRR: %w", err))
	}

	desire := &kubeapplier.ApplyDesire{
		CosmosMetadata: api.CosmosMetadata{
			ResourceID:   resourceID,
			PartitionKey: strings.ToLower(mcResourceID.String()),
		},
		Spec: kubeapplier.ApplyDesireSpec{
			ManagementCluster: mcResourceID,
			TargetItem: kubeapplier.ResourceReference{
				Group:     "certificates.hypershift.openshift.io",
				Version:   "v1alpha1",
				Resource:  "certificaterevocationrequests",
				Namespace: hcpNamespace,
				Name:      fmt.Sprintf("system-admin-credential-revocation-%s", strings.ReplaceAll(desireName, "systemAdminCredentialRevocation-", "")),
			},
			KubeContent: &runtime.RawExtension{Raw: rawJSON},
		},
	}

	if _, err := crud.Create(ctx, desire, nil); err != nil && !database.IsConflictError(err) {
		return utils.TrackError(fmt.Errorf("create CRR ApplyDesire: %w", err))
	}
	return nil
}
