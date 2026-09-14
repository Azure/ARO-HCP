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

package msicredentials

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/wait"
	utilsclock "k8s.io/utils/clock"

	azcorearm "github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/cloud"
	"github.com/Azure/azure-sdk-for-go/sdk/security/keyvault/azsecrets"
	"github.com/Azure/msi-dataplane/pkg/dataplane"

	azureclient "github.com/Azure/ARO-HCP/backend/pkg/azure/client"
	"github.com/Azure/ARO-HCP/backend/pkg/utils/controllerutils"
	"github.com/Azure/ARO-HCP/internal/api/coreapi"
	"github.com/Azure/ARO-HCP/internal/azure"
	controllerutil "github.com/Azure/ARO-HCP/internal/controllerutils"
	"github.com/Azure/ARO-HCP/internal/database/cosmosstorage/corecosmosstorage"
	"github.com/Azure/ARO-HCP/internal/database/cosmosstorage/cosmosstorageutils"
	"github.com/Azure/ARO-HCP/internal/database/informers/coreinformers"
	"github.com/Azure/ARO-HCP/internal/database/listers/corelisters"
	"github.com/Azure/ARO-HCP/internal/database/listers/fleetlisters"
	"github.com/Azure/ARO-HCP/internal/utils"
)

// MSIBasedOperatorCredentialsControllerName is the single source of truth for
// this controller's name. It is used for the workqueue name (a Prometheus
// label), context/logger controller name, and log fields.
const MSIBasedOperatorCredentialsControllerName = "MSIBasedOperatorCredentials"

const (
	// msiBasedOperatorCredentialsRecheckInterval is the base interval before
	// re-querying the Managed Identities Data Plane for credentials that are
	// already Configured. Combined with msiBasedOperatorCredentialsRecheckJitter
	// via wait.Jitter.
	msiBasedOperatorCredentialsRecheckInterval = 12 * time.Hour
	msiBasedOperatorCredentialsRecheckJitter   = 0.5
)

// msiBasedOperatorCredentialsSyncer writes the initial MSI dataplane
// credentials for each control-plane operator into the hosted-clusters
// managed identities Key Vault on the cluster's provision shard.
//
// Credentials come from the hardcoded identity when that environment is in
// use, otherwise from the real Managed Identities Data Plane. The fake
// MI dataplane client is not used.
//
// For PendingConfigure and Configured, the secret name is
// dataplane.FormatUserAssignedIdentityCredentialsForStorage("<cs_cluster_id>-<operatorName>", ...).
// The same secret is overwritten on replacement, so there is no delete during
// identity rotation. Configure and refresh wait until ObserveRoleAssignments
// has confirmed the managed-resource-group role assignments for that operator.
// PendingDeconfigure deletes the tracked secret. Cluster deletion
// (DeletionTimestamp set) skips PendingConfigure and Configured so credentials
// are not refreshed while Cluster Service tears down the HostedCluster, and
// deconfigures PendingDeconfigure.
type msiBasedOperatorCredentialsSyncer struct {
	clock                         utilsclock.PassiveClock
	clusterLister                 corelisters.ClusterLister
	serviceProviderClusterLister  corelisters.ServiceProviderClusterLister
	managementClusterLister       fleetlisters.ManagementClusterLister
	resourcesDBClient             corecosmosstorage.ResourcesDBClient
	fpaMIdataplaneClientBuilder   azureclient.FPAMIDataplaneClientBuilder
	hardcodedIdentity             *azureclient.HardcodedIdentity
	cloudConfiguration            *cloud.Configuration
	keyVaultSecretsClientBuilder  azureclient.KeyVaultSecretsClientBuilder
	clusterScopedIdentitiesConfig *azure.ClusterScopedIdentitiesConfig
}

var _ controllerutils.ClusterSyncer = (*msiBasedOperatorCredentialsSyncer)(nil)

// NewMSIBasedOperatorCredentialsController creates a cluster-watching
// controller that writes and deletes MSI-based control-plane operator
// credentials in the hosted-clusters managed identities Key Vault using
// ServiceProviderCluster.Status.MSIBasedOperatorCredentials.
func NewMSIBasedOperatorCredentialsController(
	clock utilsclock.PassiveClock,
	resourcesDBClient corecosmosstorage.ResourcesDBClient,
	backendInformers coreinformers.BackendInformers,
	managementClusterLister fleetlisters.ManagementClusterLister,
	hardcodedIdentity *azureclient.HardcodedIdentity,
	fpaMIdataplaneClientBuilder azureclient.FPAMIDataplaneClientBuilder,
	cloudConfiguration *cloud.Configuration,
	keyVaultSecretsClientBuilder azureclient.KeyVaultSecretsClientBuilder,
	clusterScopedIdentitiesConfig *azure.ClusterScopedIdentitiesConfig,
) controllerutils.Controller {
	_, clusterLister := backendInformers.Clusters()
	_, serviceProviderClusterLister := backendInformers.ServiceProviderClusters()

	syncer := &msiBasedOperatorCredentialsSyncer{
		clock:                         clock,
		clusterLister:                 clusterLister,
		serviceProviderClusterLister:  serviceProviderClusterLister,
		managementClusterLister:       managementClusterLister,
		resourcesDBClient:             resourcesDBClient,
		hardcodedIdentity:             hardcodedIdentity,
		fpaMIdataplaneClientBuilder:   fpaMIdataplaneClientBuilder,
		cloudConfiguration:            cloudConfiguration,
		keyVaultSecretsClientBuilder:  keyVaultSecretsClientBuilder,
		clusterScopedIdentitiesConfig: clusterScopedIdentitiesConfig,
	}

	return controllerutils.NewClusterWatchingController(
		MSIBasedOperatorCredentialsControllerName,
		resourcesDBClient,
		backendInformers,
		nil,
		1*time.Minute,
		syncer,
	)
}

func (s *msiBasedOperatorCredentialsSyncer) needsWork(cluster *coreapi.HCPOpenShiftCluster, serviceProviderCluster *coreapi.ServiceProviderCluster) bool {
	now := s.clock.Now()
	for _, status := range serviceProviderCluster.Status.MSIBasedOperatorCredentials {
		if status == nil {
			return true
		}
		switch status.Phase {
		case coreapi.MSIBasedOperatorCredentialsPhasePendingConfigure:
			if cluster.ServiceProviderProperties.DeletionTimestamp != nil {
				continue
			}
			return true
		case coreapi.MSIBasedOperatorCredentialsPhasePendingDeconfigure:
			return true
		case coreapi.MSIBasedOperatorCredentialsPhaseConfigured:
			if cluster.ServiceProviderProperties.DeletionTimestamp != nil {
				continue
			}
			if status.EarliestRecheckTime != nil && now.Before(status.EarliestRecheckTime.Time) {
				continue
			}
			return true
		}
	}
	return false
}

func (s *msiBasedOperatorCredentialsSyncer) skipConfiguredEnsure(cluster *coreapi.HCPOpenShiftCluster, status *coreapi.MSIBasedOperatorCredentialsStatus, now time.Time) bool {
	if cluster.ServiceProviderProperties.DeletionTimestamp != nil {
		return status.Phase == coreapi.MSIBasedOperatorCredentialsPhasePendingConfigure ||
			status.Phase == coreapi.MSIBasedOperatorCredentialsPhaseConfigured
	}
	if status.Phase != coreapi.MSIBasedOperatorCredentialsPhaseConfigured {
		return false
	}
	return status.EarliestRecheckTime != nil && now.Before(status.EarliestRecheckTime.Time)
}

func (s *msiBasedOperatorCredentialsSyncer) SyncOnce(ctx context.Context, key controllerutils.HCPClusterKey) error {
	logger := utils.LoggerFromContext(ctx)
	existingCluster, err := s.clusterLister.Get(ctx, key.SubscriptionID, key.ResourceGroupName, key.HCPClusterName)
	if cosmosstorageutils.IsNotFoundError(err) {
		return nil
	}
	if err != nil {
		return utils.TrackError(fmt.Errorf("failed to get Cluster from cache: %w", err))
	}

	existingServiceProviderCluster, err := s.serviceProviderClusterLister.Get(ctx, key.SubscriptionID, key.ResourceGroupName, key.HCPClusterName)
	if cosmosstorageutils.IsNotFoundError(err) {
		return nil
	}
	if err != nil {
		return utils.TrackError(fmt.Errorf("failed to get ServiceProviderCluster: %w", err))
	}

	if !s.needsWork(existingCluster, existingServiceProviderCluster) {
		return nil
	}

	csClusterID := controllerutils.ClusterServiceIDForCluster(existingCluster)
	replacement := existingServiceProviderCluster.DeepCopy()
	timeNow := s.clock.Now()
	var errs []error

	// Loop 1: persist Key Vault secret names that are not yet on the document
	// before any SetSecret. PendingConfigure and Configured record the name
	// when it is not already SecretName. PendingDeconfigure is skipped: its
	// names are already on SecretName or leftover PendingSecretName.
	// Cluster deletion skips PendingConfigure and Configured.
	for operatorName := range replacement.Status.MSIBasedOperatorCredentials {
		status := replacement.Status.MSIBasedOperatorCredentials[operatorName]
		if status == nil {
			errs = append(errs, utils.TrackError(fmt.Errorf("MSIBasedOperatorCredentials has a nil status for operator %s", operatorName)))
			continue
		}
		if s.skipConfiguredEnsure(existingCluster, status, timeNow) {
			continue
		}

		switch status.Phase {
		case coreapi.MSIBasedOperatorCredentialsPhasePendingConfigure,
			coreapi.MSIBasedOperatorCredentialsPhaseConfigured:
			if len(csClusterID) == 0 {
				continue
			}
			desiredSecretName := dataplane.IdentifierForUserAssignedIdentityCredentials(secretNameIdentifier(csClusterID, operatorName))
			if status.SecretName == desiredSecretName {
				status.PendingSecretName = ""
				continue
			}
			status.PendingSecretName = desiredSecretName
		}
	}

	if controllerutil.NeedsUpdate(existingServiceProviderCluster, replacement) {
		logger.Info("persisting pending MSI-based operator credential secret names onto ServiceProviderCluster")

		persistedServiceProviderCluster, err := s.resourcesDBClient.ServiceProviderClusters(existingCluster.ID.SubscriptionID, existingCluster.ID.ResourceGroupName, existingCluster.ID.Name).Replace(ctx, replacement, nil)
		if err != nil {
			return utils.TrackError(fmt.Errorf("failed to replace ServiceProviderCluster: %w", err))
		}
		existingServiceProviderCluster = persistedServiceProviderCluster
		replacement = existingServiceProviderCluster.DeepCopy()
	}

	getKeyVaultClient := s.newKeyVaultSecretsClientGetter(ctx, existingServiceProviderCluster)

	for operatorName := range replacement.Status.MSIBasedOperatorCredentials {
		status := replacement.Status.MSIBasedOperatorCredentials[operatorName]
		if status == nil {
			errs = append(errs, utils.TrackError(fmt.Errorf("MSIBasedOperatorCredentials has a nil status for operator %s", operatorName)))
			continue
		}
		if s.skipConfiguredEnsure(existingCluster, status, timeNow) {
			continue
		}

		switch status.Phase {
		case coreapi.MSIBasedOperatorCredentialsPhasePendingConfigure,
			coreapi.MSIBasedOperatorCredentialsPhaseConfigured:
			if len(csClusterID) == 0 {
				continue
			}
			err := s.ensureOperatorCredentials(ctx, existingCluster, existingServiceProviderCluster, operatorName, status, csClusterID, getKeyVaultClient)
			if err != nil {
				errs = append(errs, err)
			}
		case coreapi.MSIBasedOperatorCredentialsPhasePendingDeconfigure:
			if len(status.PendingSecretName) == 0 && len(status.SecretName) == 0 {
				status.Phase = coreapi.MSIBasedOperatorCredentialsPhaseDeconfigured
				status.KeyVaultURL = ""
				recheckAt := metav1.NewTime(s.clock.Now().Add(wait.Jitter(msiBasedOperatorCredentialsRecheckInterval, msiBasedOperatorCredentialsRecheckJitter)))
				status.EarliestRecheckTime = &recheckAt
				continue
			}
			keyVaultClient, _, err := getKeyVaultClient(status)
			if err != nil {
				errs = append(errs, err)
				continue
			}
			err = s.deconfigureOperatorCredentials(ctx, status, keyVaultClient)
			if err != nil {
				errs = append(errs, err)
			}
		}
	}

	if controllerutil.NeedsUpdate(existingServiceProviderCluster, replacement) {
		logger.Info("persisting MSI-based operator credentials configure/deconfigure result onto ServiceProviderCluster")

		_, err := s.resourcesDBClient.ServiceProviderClusters(existingCluster.ID.SubscriptionID, existingCluster.ID.ResourceGroupName, existingCluster.ID.Name).Replace(ctx, replacement, nil)
		if err != nil {
			return utils.TrackError(fmt.Errorf("failed to replace ServiceProviderCluster: %w", err))
		}
	}

	return errors.Join(errs...)
}

func (s *msiBasedOperatorCredentialsSyncer) ensureOperatorCredentials(
	ctx context.Context,
	cluster *coreapi.HCPOpenShiftCluster,
	serviceProviderCluster *coreapi.ServiceProviderCluster,
	operatorName string,
	status *coreapi.MSIBasedOperatorCredentialsStatus,
	csClusterID string,
	getKeyVaultClient func(status *coreapi.MSIBasedOperatorCredentialsStatus) (azureclient.KeyVaultSecretsClient, string, error),
) error {
	identityResourceID := status.ObservedIdentity.ResourceID
	if identityResourceID == nil {
		return utils.TrackError(fmt.Errorf("observed identity ResourceID is nil for operator %s", operatorName))
	}

	ready, err := operatorRoleAssignmentsComplete(cluster, serviceProviderCluster, s.clusterScopedIdentitiesConfig, operatorName, status.ObservedIdentity.PrincipalID)
	if err != nil {
		return err
	}
	if !ready {
		utils.LoggerFromContext(ctx).Info("waiting for managed resource group role assignments before writing MSI-based operator credentials",
			"operatorName", operatorName,
			"identityResourceID", identityResourceID.String())
		return nil
	}

	credentials, err := s.credentialsForOperator(ctx, cluster, operatorName, identityResourceID)
	if err != nil {
		return err
	}

	secretName, parameters, err := dataplane.FormatUserAssignedIdentityCredentialsForStorage(secretNameIdentifier(csClusterID, operatorName), credentials)
	if err != nil {
		return utils.TrackError(fmt.Errorf("failed to format user assigned identity credentials for storage for operator %s: %w", operatorName, err))
	}

	keyVaultClient, vaultURL, err := getKeyVaultClient(status)
	if err != nil {
		return err
	}

	if status.SecretName != "" && status.SecretName != secretName {
		if err := s.deleteSecret(ctx, keyVaultClient, status.SecretName); err != nil {
			return err
		}
		status.SecretName = ""
	}

	err = s.setSecret(ctx, keyVaultClient, secretName, parameters)
	if err != nil {
		return utils.TrackError(fmt.Errorf("failed to set Key Vault secret %s for operator %s: %w", secretName, operatorName, err))
	}

	status.SecretName = secretName
	status.PendingSecretName = ""
	status.KeyVaultURL = vaultURL
	status.Phase = coreapi.MSIBasedOperatorCredentialsPhaseConfigured
	recheckAt := metav1.NewTime(s.clock.Now().Add(wait.Jitter(msiBasedOperatorCredentialsRecheckInterval, msiBasedOperatorCredentialsRecheckJitter)))
	status.EarliestRecheckTime = &recheckAt
	utils.LoggerFromContext(ctx).Info("Ensured MSI-based operator credentials secret",
		"operatorName", operatorName,
		"secretName", secretName)
	return nil
}

func (s *msiBasedOperatorCredentialsSyncer) credentialsForOperator(
	ctx context.Context,
	cluster *coreapi.HCPOpenShiftCluster,
	operatorName string,
	identityResourceID *azcorearm.ResourceID,
) (dataplane.UserAssignedIdentityCredentials, error) {
	if s.hardcodedIdentity != nil {
		if s.cloudConfiguration == nil {
			return dataplane.UserAssignedIdentityCredentials{}, utils.TrackError(fmt.Errorf("cloud configuration is required when using the hardcoded identity"))
		}
		return s.hardcodedIdentity.UserAssignedIdentityCredentials(identityResourceID.String(), s.cloudConfiguration, s.clock.Now()), nil
	}

	if len(cluster.ServiceProviderProperties.ManagedIdentitiesDataPlaneIdentityURL) == 0 {
		return dataplane.UserAssignedIdentityCredentials{}, utils.TrackError(fmt.Errorf("cluster ManagedIdentitiesDataPlaneIdentityURL is empty"))
	}
	if s.fpaMIdataplaneClientBuilder == nil {
		return dataplane.UserAssignedIdentityCredentials{}, utils.TrackError(fmt.Errorf("Managed Identities Data Plane client builder is nil"))
	}

	miDataplaneClient, err := s.fpaMIdataplaneClientBuilder.ManagedIdentitiesDataplane(cluster.ServiceProviderProperties.ManagedIdentitiesDataPlaneIdentityURL)
	if err != nil {
		return dataplane.UserAssignedIdentityCredentials{}, utils.TrackError(fmt.Errorf("failed to get Managed Identities Data Plane Client: %w", err))
	}

	resp, err := miDataplaneClient.GetUserAssignedIdentitiesCredentials(ctx, dataplane.UserAssignedIdentitiesRequest{
		IdentityIDs: []string{identityResourceID.String()},
	})
	if err != nil {
		return dataplane.UserAssignedIdentityCredentials{}, utils.TrackError(fmt.Errorf("failed to get user assigned identities credentials for operator %s: %w", operatorName, err))
	}

	credentials, err := credentialsForIdentity(resp, identityResourceID)
	if err != nil {
		return dataplane.UserAssignedIdentityCredentials{}, utils.TrackError(fmt.Errorf("failed to match Managed Identities Data Plane credentials for operator %s: %w", operatorName, err))
	}
	return credentials, nil
}

func (s *msiBasedOperatorCredentialsSyncer) deconfigureOperatorCredentials(
	ctx context.Context,
	status *coreapi.MSIBasedOperatorCredentialsStatus,
	keyVaultClient azureclient.KeyVaultSecretsClient,
) error {
	var errs []error
	remainingPending := status.PendingSecretName
	remainingSecret := status.SecretName

	if len(status.PendingSecretName) > 0 {
		if err := s.deleteSecret(ctx, keyVaultClient, status.PendingSecretName); err != nil {
			errs = append(errs, err)
		} else {
			remainingPending = ""
		}
	}
	if len(status.SecretName) > 0 && status.SecretName != status.PendingSecretName {
		if err := s.deleteSecret(ctx, keyVaultClient, status.SecretName); err != nil {
			errs = append(errs, err)
		} else {
			remainingSecret = ""
		}
	}

	status.PendingSecretName = remainingPending
	status.SecretName = remainingSecret
	if len(errs) > 0 {
		return errors.Join(errs...)
	}

	status.PendingSecretName = ""
	status.SecretName = ""
	status.KeyVaultURL = ""
	status.Phase = coreapi.MSIBasedOperatorCredentialsPhaseDeconfigured
	recheckAt := metav1.NewTime(s.clock.Now().Add(wait.Jitter(msiBasedOperatorCredentialsRecheckInterval, msiBasedOperatorCredentialsRecheckJitter)))
	status.EarliestRecheckTime = &recheckAt
	return nil
}

func (s *msiBasedOperatorCredentialsSyncer) setSecret(
	ctx context.Context,
	keyVaultClient azureclient.KeyVaultSecretsClient,
	secretName string,
	parameters azsecrets.SetSecretParameters,
) error {
	_, err := keyVaultClient.SetSecret(ctx, secretName, parameters, nil)
	if err == nil {
		return nil
	}
	if !azureclient.IsKeyVaultSecretDeletedButRecoverableErr(err) {
		return err
	}

	_, recoverErr := keyVaultClient.RecoverDeletedSecret(ctx, secretName, nil)
	if recoverErr != nil && !azureclient.IsKeyVaultSecretNotFoundErr(recoverErr) {
		return utils.TrackError(fmt.Errorf("failed to recover deleted Key Vault secret %s: %w", secretName, recoverErr))
	}

	_, err = keyVaultClient.SetSecret(ctx, secretName, parameters, nil)
	return err
}

func (s *msiBasedOperatorCredentialsSyncer) deleteSecret(
	ctx context.Context,
	keyVaultClient azureclient.KeyVaultSecretsClient,
	secretName string,
) error {
	_, err := keyVaultClient.DeleteSecret(ctx, secretName, nil)
	if err != nil && !azureclient.IsKeyVaultSecretNotFoundErr(err) {
		return utils.TrackError(fmt.Errorf("failed to delete Key Vault secret %s: %w", secretName, err))
	}
	return nil
}

func (s *msiBasedOperatorCredentialsSyncer) newKeyVaultSecretsClientGetter(ctx context.Context, serviceProviderCluster *coreapi.ServiceProviderCluster) func(status *coreapi.MSIBasedOperatorCredentialsStatus) (azureclient.KeyVaultSecretsClient, string, error) {
	var client azureclient.KeyVaultSecretsClient
	var resolvedURL string
	return func(status *coreapi.MSIBasedOperatorCredentialsStatus) (azureclient.KeyVaultSecretsClient, string, error) {
		vaultURL, err := s.hostedClustersManagedIdentitiesKeyVaultURL(ctx, serviceProviderCluster, status)
		if err != nil {
			return nil, "", err
		}
		if client != nil && resolvedURL == vaultURL {
			return client, vaultURL, nil
		}
		built, err := s.keyVaultSecretsClientBuilder.SecretsClient(vaultURL)
		if err != nil {
			return nil, "", err
		}
		client = built
		resolvedURL = vaultURL
		return client, vaultURL, nil
	}
}

func (s *msiBasedOperatorCredentialsSyncer) hostedClustersManagedIdentitiesKeyVaultURL(
	ctx context.Context,
	serviceProviderCluster *coreapi.ServiceProviderCluster,
	status *coreapi.MSIBasedOperatorCredentialsStatus,
) (string, error) {
	if serviceProviderCluster.Status.ManagementClusterResourceID != nil {
		stampIdentifier, err := stampIdentifierFromManagementClusterResourceID(serviceProviderCluster.Status.ManagementClusterResourceID)
		if err != nil {
			return "", err
		}
		managementCluster, err := s.managementClusterLister.Get(ctx, stampIdentifier)
		if err != nil {
			return "", utils.TrackError(fmt.Errorf("failed to get ManagementCluster %s: %w", stampIdentifier, err))
		}
		if len(managementCluster.Status.HostedClustersManagedIdentitiesKeyVaultURL) == 0 {
			return "", utils.TrackError(fmt.Errorf("ManagementCluster %s has empty HostedClustersManagedIdentitiesKeyVaultURL", stampIdentifier))
		}
		return managementCluster.Status.HostedClustersManagedIdentitiesKeyVaultURL, nil
	}
	if status != nil && len(status.KeyVaultURL) > 0 {
		return status.KeyVaultURL, nil
	}
	return "", utils.TrackError(fmt.Errorf("ServiceProviderCluster has no ManagementClusterResourceID and no stored KeyVaultURL"))
}

func credentialsForIdentity(resp *dataplane.ManagedIdentityCredentials, identityResourceID *azcorearm.ResourceID) (dataplane.UserAssignedIdentityCredentials, error) {
	if resp == nil {
		return dataplane.UserAssignedIdentityCredentials{}, fmt.Errorf("Managed Identities Data Plane returned a nil credentials response")
	}
	if len(resp.ExplicitIdentities) == 0 {
		return dataplane.UserAssignedIdentityCredentials{}, fmt.Errorf("Managed Identities Data Plane returned no credentials for identity %s", identityResourceID.String())
	}

	for _, identity := range resp.ExplicitIdentities {
		if identity.ResourceID == nil {
			continue
		}
		if strings.EqualFold(*identity.ResourceID, identityResourceID.String()) {
			return identity, nil
		}
	}
	return dataplane.UserAssignedIdentityCredentials{}, fmt.Errorf("Managed Identities Data Plane response did not include credentials for identity %s", identityResourceID.String())
}
