package controllers

import (
	"context"
	"fmt"
	"strings"

	"github.com/Azure/ARO-HCP/internal/api"
	"github.com/Azure/ARO-HCP/internal/api/arm"
	"github.com/Azure/ARO-HCP/internal/utils"
	azcorearm "github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"

	azureclient "github.com/Azure/ARO-HCP/backend/pkg/azure/client"
)

// AzureHCPClusterMIsExistenceValidation validates the existence of all managed identities defined in the cluster.
// It assumes all identities present are for recognized operators.
type AzureHCPClusterMIsExistenceValidation struct {
	smiClientBuilder azureclient.SMIClientBuilder
}

func NewAzureHCPClusterMIsExistenceValidation(
	smiClientBuilder azureclient.SMIClientBuilder,
) *AzureHCPClusterMIsExistenceValidation {
	return &AzureHCPClusterMIsExistenceValidation{
		smiClientBuilder: smiClientBuilder,
	}
}

func (v *AzureHCPClusterMIsExistenceValidation) Name() string {
	return "azure-hcp-cluster-mis-existence-validation"
}

func (v *AzureHCPClusterMIsExistenceValidation) Validate(ctx context.Context, clusterSubscription *arm.Subscription, cluster *api.HCPOpenShiftCluster) error {
	smiResourceIDStr := cluster.CustomerProperties.Platform.OperatorsAuthentication.UserAssignedIdentities.ServiceManagedIdentity
	smiResourceID, err := azcorearm.ParseResourceID(smiResourceIDStr)
	if err != nil {
		return err
	}
	// TODO get the cluster identity URL from the cluster. It originally comes
	// from the x-ms-identity-url header provided from ARM when the initial
	// cluster creation request is made. Right now we do not have access to it
	clusterIdentityURL := "TODO"

	// We check the existence of the Cluster's Service Managed Identity by
	// attempting to retrieve the user assigned identities client using the
	// service managed identity's identity credentials, which we obtain by
	// requesting them via the Managed Identities Data Plane Service. If the
	// service managed identity does not exist that request will fail.
	uaisClient, err := v.smiClientBuilder.UserAssignedIdentitiesClient(
		ctx,
		*clusterSubscription.Properties.TenantId,
		cluster.ID.SubscriptionID,
		clusterIdentityURL,
		smiResourceID,
	)
	if err != nil {
		return utils.TrackError(fmt.Errorf("failed to get user assigned identities client: %w", err))
	}

	clusterUAIsProfile := &cluster.CustomerProperties.Platform.OperatorsAuthentication.UserAssignedIdentities
	clusterUAIsResourceIDs, err := v.clusterManagedIdentities(clusterUAIsProfile)
	if err != nil {
		return utils.TrackError(fmt.Errorf("failed to get cluster managed identities resource IDs: %w", err))
	}

	var notFoundMIsStrs []string
	for _, resourceID := range clusterUAIsResourceIDs {
		_, err := uaisClient.Get(ctx, resourceID.ResourceGroupName, resourceID.Name, nil)
		if azureclient.IsResourceNotFoundErr(err) {
			notFoundMIsStrs = append(notFoundMIsStrs, resourceID.String())
		}
		if err != nil {
			// TODO is it ok to error when one of them fails to get when the error is not a resource not found error?
			return utils.TrackError(fmt.Errorf("failed to get managed identity '%s': %w", resourceID, err))
		}
	}

	if len(notFoundMIsStrs) > 0 {
		return utils.TrackError(fmt.Errorf("managed identities not found: %s", strings.Join(notFoundMIsStrs, ", ")))
	}

	return nil
}

// clusterManagedIdentities returns a list of the control and data plane identities defined in the cluster.
func (v *AzureHCPClusterMIsExistenceValidation) clusterManagedIdentities(
	clusterUAIsProfile *api.UserAssignedIdentitiesProfile) ([]*azcorearm.ResourceID, error) {
	var resourceIDs []*azcorearm.ResourceID

	for _, mi := range clusterUAIsProfile.ControlPlaneOperators {
		resourceID, err := azcorearm.ParseResourceID(mi)
		if err != nil {
			return nil, err
		}
		resourceIDs = append(resourceIDs, resourceID)
	}
	for _, mi := range clusterUAIsProfile.DataPlaneOperators {
		resourceID, err := azcorearm.ParseResourceID(mi)
		if err != nil {
			return nil, err
		}
		resourceIDs = append(resourceIDs, resourceID)
	}
	return resourceIDs, nil
}
