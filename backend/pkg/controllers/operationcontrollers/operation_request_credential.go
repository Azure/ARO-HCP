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
	"crypto/sha256"
	"fmt"
	"net/http"
	"strings"
	"time"

	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/tools/cache"
	utilsclock "k8s.io/utils/clock"

	azcorearm "github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"
	"github.com/Azure/azure-sdk-for-go/sdk/security/keyvault/azsecrets"

	cmv1 "github.com/openshift-online/ocm-sdk-go/clustersmgmt/v1"

	azureclient "github.com/Azure/ARO-HCP/backend/pkg/azure/client"
	"github.com/Azure/ARO-HCP/backend/pkg/controllers/controllerutils"
	"github.com/Azure/ARO-HCP/internal/api"
	"github.com/Azure/ARO-HCP/internal/api/arm"
	"github.com/Azure/ARO-HCP/internal/database"
	dblisters "github.com/Azure/ARO-HCP/internal/database/listers"
	"github.com/Azure/ARO-HCP/internal/ocm"
	"github.com/Azure/ARO-HCP/internal/utils"
)

type operationRequestCredential struct {
	clock                       utilsclock.PassiveClock
	resourcesDBClient           database.ResourcesDBClient
	clustersServiceClient       ocm.ClusterServiceClientSpec
	notificationClient          *http.Client
	managementClusterLister     dblisters.ManagementClusterLister
	keyVaultSecretClientFactory azureclient.KeyVaultSecretClientFactory
}

// NewOperationRequestCredentialController returns a new Controller instance that
// follows an asynchronous admin credential request operation to completion and
// updates the corresponding operation document in Cosmos DB.
//
// Operation documents relevant to this controller will have the following values:
//
//	ResourceType: Microsoft.RedHatOpenShift/hcpOpenShiftClusters
//	     Request: RequestCredential
//	      Status: any non-terminal value
//	  InternalID: a Clusters Service HREF value
//
// Note that "to completion" does not imply success. An operation is considered
// complete when its status field reaches what Azure defines as a terminal value;
// any of "Succeeded", "Failed", or "Canceled". Once the operation status reaches
// a terminal value, there will be no further updates to the operation document.
func NewOperationRequestCredentialController(
	clock utilsclock.PassiveClock,
	resourcesDBClient database.ResourcesDBClient,
	clustersServiceClient ocm.ClusterServiceClientSpec,
	notificationClient *http.Client,
	activeOperationInformer cache.SharedIndexInformer,
	managementClusterLister dblisters.ManagementClusterLister,
	keyVaultSecretClientFactory azureclient.KeyVaultSecretClientFactory,
) controllerutils.Controller {
	syncer := &operationRequestCredential{
		clock:                       clock,
		resourcesDBClient:           resourcesDBClient,
		clustersServiceClient:       clustersServiceClient,
		notificationClient:          notificationClient,
		managementClusterLister:     managementClusterLister,
		keyVaultSecretClientFactory: keyVaultSecretClientFactory,
	}

	controller := NewGenericOperationController(
		"OperationRequestCredential",
		syncer,
		10*time.Second,
		activeOperationInformer,
		resourcesDBClient,
	)

	return controller
}

func (opsync *operationRequestCredential) ShouldProcess(ctx context.Context, operation *api.Operation) bool {
	if operation.Status.IsTerminal() {
		return false
	}
	if operation.Request != database.OperationRequestRequestCredential {
		return false
	}
	if len(operation.InternalID.String()) == 0 {
		return false
	}
	return true
}

func (opsync *operationRequestCredential) SynchronizeOperation(ctx context.Context, key controllerutils.OperationKey) error {
	logger := utils.LoggerFromContext(ctx)
	logger.Info("checking operation")

	oldOperation, err := opsync.resourcesDBClient.Operations(key.SubscriptionID).Get(ctx, key.OperationName)
	if database.IsNotFoundError(err) {
		return nil // no work to do
	}
	if err != nil {
		return fmt.Errorf("failed to get active operation: %w", err)
	}
	if !opsync.ShouldProcess(ctx, oldOperation) {
		return nil // no work to do
	}

	breakGlassCredential, err := opsync.clustersServiceClient.GetBreakGlassCredential(ctx, oldOperation.InternalID)
	if err != nil {
		return utils.TrackError(err)
	}

	var newOperationStatus arm.ProvisioningState
	var newOperationError *arm.CloudErrorBody

	switch status := breakGlassCredential.Status(); status {
	case cmv1.BreakGlassCredentialStatusCreated:
		newOperationStatus = arm.ProvisioningStateProvisioning
	case cmv1.BreakGlassCredentialStatusFailed:
		// XXX Cluster Service does not provide a reason for the failure,
		//     so we have no choice but to use a generic error message.
		newOperationStatus = arm.ProvisioningStateFailed
		newOperationError = &arm.CloudErrorBody{
			Code:    arm.CloudErrorCodeInternalServerError,
			Message: "Failed to provision cluster credential",
		}
	case cmv1.BreakGlassCredentialStatusIssued:
		credResourceID, err := opsync.createSystemAdminCredentialRequest(ctx, key, oldOperation, breakGlassCredential)
		if err != nil {
			return utils.TrackError(err)
		}
		credInternalID, err := api.NewInternalID(credResourceID.String())
		if err != nil {
			return utils.TrackError(fmt.Errorf("failed to create InternalID for credential: %w", err))
		}
		oldOperation.InternalID = credInternalID
		newOperationStatus = arm.ProvisioningStateSucceeded
	default:
		return fmt.Errorf("unhandled BreakGlassCredentialStatus '%s'", status)
	}

	if !needToPatchOperation(oldOperation, newOperationStatus, newOperationError) {
		return nil
	}

	err = patchOperation(ctx, opsync.clock, opsync.resourcesDBClient, oldOperation, newOperationStatus, newOperationError, postAsyncNotificationFn(opsync.notificationClient))
	if err != nil {
		return utils.TrackError(err)
	}

	return nil
}

func (opsync *operationRequestCredential) createSystemAdminCredentialRequest(
	ctx context.Context,
	key controllerutils.OperationKey,
	operation *api.Operation,
	breakGlassCredential *cmv1.BreakGlassCredential,
) (*azcorearm.ResourceID, error) {
	now := metav1.Now()
	credName := strings.ToLower(key.OperationName)

	credResourceID, err := api.ToSystemAdminCredentialRequestResourceID(
		operation.ExternalID.SubscriptionID,
		operation.ExternalID.ResourceGroupName,
		operation.ExternalID.Name,
		credName,
	)
	if err != nil {
		return nil, utils.TrackError(fmt.Errorf("failed to build credential request resource ID: %w", err))
	}

	serviceProviderCluster, err := opsync.resourcesDBClient.ServiceProviderClusters(
		operation.ExternalID.SubscriptionID,
		operation.ExternalID.ResourceGroupName,
		operation.ExternalID.Name,
	).Get(ctx, api.ServiceProviderClusterResourceName)
	if err != nil {
		return nil, utils.TrackError(fmt.Errorf("failed to get ServiceProviderCluster: %w", err))
	}

	managementClusterResourceID := serviceProviderCluster.Status.ManagementClusterResourceID
	if managementClusterResourceID == nil || managementClusterResourceID.Parent == nil {
		return nil, utils.TrackError(fmt.Errorf("ServiceProviderCluster has no ManagementClusterResourceID"))
	}

	stampIdentifier := managementClusterResourceID.Parent.Name
	managementCluster, err := opsync.managementClusterLister.Get(ctx, stampIdentifier)
	if err != nil {
		return nil, utils.TrackError(fmt.Errorf("failed to get ManagementCluster for stamp %q: %w", stampIdentifier, err))
	}

	keyVaultSecretName := keyVaultSecretNameForCredential(credResourceID)

	cred := &api.SystemAdminCredentialRequest{
		CosmosMetadata: api.CosmosMetadata{
			ResourceID:   credResourceID,
			PartitionKey: strings.ToLower(operation.ExternalID.SubscriptionID),
		},
		Spec: api.SystemAdminCredentialRequestSpec{
			OperationID:         key.OperationName,
			CreationTimestamp:   now,
			ExpirationTimestamp: metav1.NewTime(breakGlassCredential.ExpirationTimestamp()),
		},
		Status: api.SystemAdminCredentialRequestStatus{
			Kubeconfig:         breakGlassCredential.Kubeconfig(),
			KeyVaultSecretName: keyVaultSecretName,
		},
	}
	meta.SetStatusCondition(&cred.Status.Conditions, metav1.Condition{
		Type:               api.SystemAdminCredentialRequestConditionIssued,
		Status:             metav1.ConditionTrue,
		LastTransitionTime: now,
		Reason:             "BreakGlassCredentialIssued",
		Message:            "Credential issued by cluster service.",
	})

	credCRUD := opsync.resourcesDBClient.SystemAdminCredentialRequests(
		operation.ExternalID.SubscriptionID,
		operation.ExternalID.ResourceGroupName,
		operation.ExternalID.Name,
	)
	_, err = credCRUD.Create(ctx, cred, nil)
	if err != nil {
		if database.IsConflictError(err) {
			return credResourceID, nil
		}
		return nil, utils.TrackError(fmt.Errorf("failed to create SystemAdminCredentialRequest: %w", err))
	}

	keyVaultSecretClient, err := opsync.keyVaultSecretClientFactory.KeyVaultSecretClient(
		managementCluster.Status.HostedClustersSecretsKeyVaultManagedIdentityClientID,
		managementCluster.Status.HostedClustersSecretsKeyVaultURL,
	)
	if err != nil {
		return nil, utils.TrackError(fmt.Errorf("failed to create Key Vault secret client: %w", err))
	}

	_, err = keyVaultSecretClient.SetSecret(ctx, keyVaultSecretName, azsecrets.SetSecretParameters{
		Value: &cred.Status.Kubeconfig,
		Tags: azureclient.KeyVaultSecretTags(
			azureclient.KeyVaultBinarySourceBackend,
			operation.ExternalID.SubscriptionID,
			operation.ExternalID.ResourceGroupName,
			operation.ExternalID.Name,
		),
	}, nil)
	if err != nil {
		return nil, utils.TrackError(fmt.Errorf("failed to store kubeconfig in Key Vault: %w", err))
	}

	return credResourceID, nil
}

func keyVaultSecretNameForCredential(credResourceID *azcorearm.ResourceID) string {
	hash := sha256.Sum256([]byte(strings.ToLower(credResourceID.String())))
	return fmt.Sprintf("%x", hash[:32])
}
