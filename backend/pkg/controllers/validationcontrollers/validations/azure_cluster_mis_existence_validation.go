package validations

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

// AzureClusterManagedIdentitiesExistenceValidation validates the existence of all managed identities defined in the cluster.
// It assumes all identities present are for recognized operators.
type AzureClusterManagedIdentitiesExistenceValidation struct {
	smiClientBuilderFactory azureclient.ServiceManagedIdentityClientBuilderFactory
}

func NewAzureClusterManagedIdetitiesExistenceValidation(
	smiClientBuilderFactory azureclient.ServiceManagedIdentityClientBuilderFactory,
) *AzureClusterManagedIdentitiesExistenceValidation {
	return &AzureClusterManagedIdentitiesExistenceValidation{
		smiClientBuilderFactory: smiClientBuilderFactory,
	}
}

func (v *AzureClusterManagedIdentitiesExistenceValidation) Name() string {
	return "AzureClusterManagedIdentitiesExistenceValidation"
}

func (v *AzureClusterManagedIdentitiesExistenceValidation) Validate(ctx context.Context, clusterSubscription *arm.Subscription, cluster *api.HCPOpenShiftCluster) error {
	smiResourceID := cluster.CustomerProperties.Platform.OperatorsAuthentication.UserAssignedIdentities.ServiceManagedIdentity
	// TODO get the cluster identity URL from the cluster. It originally comes
	// from the x-ms-identity-url header provided from ARM when the initial
	// cluster creation request is made. Right now we do not have access to it.
	// This should be available once https://github.com/Azure/ARO-HCP/pull/3838 is merged.
	clusterIdentityURL := "TODO"

	smiClientBuilder := v.smiClientBuilderFactory.NewServiceManagedIdentityClientBuilder(clusterIdentityURL, smiResourceID)
	// We check the existence of the Cluster's Service Managed Identity by
	// attempting to retrieve the user assigned identities client using the
	// service managed identity's identity credentials, which we obtain by
	// requesting them via the Managed Identities Data Plane Service. If the
	// service managed identity does not exist the request will fail.
	uaisClient, err := smiClientBuilder.UserAssignedIdentitiesClient(ctx, cluster.ID.SubscriptionID)
	if err != nil {
		return utils.TrackError(fmt.Errorf("failed to get user assigned identities client: %w", err))
	}

	clusterUAIsProfile := &cluster.CustomerProperties.Platform.OperatorsAuthentication.UserAssignedIdentities
	clusterOperatorsMIsResourceIDs, err := v.clusterOperatorsManagedIdentities(clusterUAIsProfile)
	if err != nil {
		return utils.TrackError(fmt.Errorf("failed to get cluster managed identities resource IDs: %w", err))
	}

	var notFoundMIsStrs []string
	for _, resourceID := range clusterOperatorsMIsResourceIDs {
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

// clusterOperatorsManagedIdentities returns a list of the control and data plane identities defined in the cluster.
func (v *AzureClusterManagedIdentitiesExistenceValidation) clusterOperatorsManagedIdentities(
	clusterUAIsProfile *api.UserAssignedIdentitiesProfile) ([]*azcorearm.ResourceID, error) {
	var resourceIDs []*azcorearm.ResourceID

	for _, miResourceID := range clusterUAIsProfile.ControlPlaneOperators {
		resourceIDs = append(resourceIDs, miResourceID)
	}
	for _, miResourceID := range clusterUAIsProfile.DataPlaneOperators {
		resourceIDs = append(resourceIDs, miResourceID)
	}

	return resourceIDs, nil
}
