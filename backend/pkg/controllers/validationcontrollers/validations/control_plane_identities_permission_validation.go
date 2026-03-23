package validations

import (
	"context"

	azureclient "github.com/Azure/ARO-HCP/backend/pkg/azure/client"
	"github.com/Azure/ARO-HCP/internal/api"
	"github.com/Azure/ARO-HCP/internal/api/arm"
	"github.com/Azure/ARO-HCP/internal/utils"
	azcorearm "github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"
	"github.com/go-errors/errors"
)

// ControlPlaneIdentitiesPermissionValidation validates that the control plane identities have the necessary permissions.
type ControlPlaneIdentitiesPermissionValidation struct {
	fpamiDataplaneClientBuilder azureclient.FPAMIDataplaneClientBuilder
}

func NewControlPlaneIdentitiesPermissionValidation(fpamiDataplaneClientBuilder azureclient.FPAMIDataplaneClientBuilder) *ControlPlaneIdentitiesPermissionValidation {
	return &ControlPlaneIdentitiesPermissionValidation{
		fpamiDataplaneClientBuilder: fpamiDataplaneClientBuilder,
	}
}

func (v *ControlPlaneIdentitiesPermissionValidation) Name() string {
	return "ControlPlaneIdentitiesPermissionValidation"
}

func (v *ControlPlaneIdentitiesPermissionValidation) Validate(ctx context.Context, clusterSubscription *arm.Subscription, cluster *api.HCPOpenShiftCluster) error {
	logger := utils.LoggerFromContext(ctx)
	logger.Info("Validating control plane identities permissions")

	controlPlaneMissingActions := make(map[string][]string)
	for operatorName, identity := range cluster.CustomerProperties.Platform.OperatorsAuthentication.UserAssignedIdentities.ControlPlaneOperators {
		missingActions, err := v.findMissingActionsForIdentity(ctx, cluster, identity)
		if err != nil {
			return err
		}
		controlPlaneMissingActions[operatorName] = missingActions
	}

	if len(controlPlaneMissingActions) > 0 {
		return errors.Errorf("control plane operators missing required permissions: %v",
			controlPlaneMissingActions)
	}

	return nil
}

func (v *ControlPlaneIdentitiesPermissionValidation) findMissingActionsForIdentity(ctx context.Context, cluster *api.HCPOpenShiftCluster, identity *azcorearm.ResourceID) ([]string, error) {
	// Get roleDefinitionResourceId from operators managed identities configuration
	// https://github.com/Azure/ARO-HCP/pull/

	// Get the role definitions(actions and data actions) for the identity
	// https://github.com/Azure/ARO-HCP/pull/4403

	// Use the CheckAccessV2 client to check if the identity has the necessary permissions
	// https://github.com/Azure/ARO-HCP/pull/3907
	// If the identity does not have the necessary permissions, return the missing actions
	// If the identity has the necessary permissions, return nil

	return nil, nil
}
