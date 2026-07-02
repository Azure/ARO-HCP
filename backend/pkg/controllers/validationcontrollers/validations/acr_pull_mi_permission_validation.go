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

package validations

import (
	"context"
	"fmt"

	checkaccessv2 "github.com/Azure/checkaccess-v2-go-sdk/client"

	azureclient "github.com/Azure/ARO-HCP/backend/pkg/azure/client"
	"github.com/Azure/ARO-HCP/internal/api"
	"github.com/Azure/ARO-HCP/internal/api/arm"
	internalazure "github.com/Azure/ARO-HCP/internal/azure"
	"github.com/Azure/ARO-HCP/internal/utils"
)

const assignAction = "Microsoft.ManagedIdentity/userAssignedIdentities/*/assign/action"

// AcrPullManagedIdentityPermissionValidation validates that the CAPZ identity
// has assign/action permission on the customer's ACR pull managed identity.
// This permission is required for CAPZ to attach the MI to worker VMs.
type AcrPullManagedIdentityPermissionValidation struct {
	smiClientBuilder           azureclient.ServiceManagedIdentityClientBuilder
	checkAccessV2ClientBuilder azureclient.CheckAccessV2ClientBuilder
}

func NewAcrPullManagedIdentityPermissionValidation(
	smiClientBuilder azureclient.ServiceManagedIdentityClientBuilder,
	checkAccessV2ClientBuilder azureclient.CheckAccessV2ClientBuilder,
) *AcrPullManagedIdentityPermissionValidation {
	return &AcrPullManagedIdentityPermissionValidation{
		smiClientBuilder:           smiClientBuilder,
		checkAccessV2ClientBuilder: checkAccessV2ClientBuilder,
	}
}

func (v *AcrPullManagedIdentityPermissionValidation) Name() string {
	return "AcrPullManagedIdentityPermissionValidation"
}

func (v *AcrPullManagedIdentityPermissionValidation) Validate(ctx context.Context, clusterSubscription *arm.Subscription, cluster *api.HCPOpenShiftCluster) error {
	if cluster.CustomerProperties.Platform.ContainerRegistry == nil {
		return nil
	}
	if cluster.CustomerProperties.Platform.ContainerRegistry.Credentials.ManagedIdentity == nil ||
		cluster.CustomerProperties.Platform.ContainerRegistry.Credentials.ManagedIdentity.ResourceID == nil {
		return nil
	}

	acrPullMIResourceID := cluster.CustomerProperties.Platform.ContainerRegistry.Credentials.ManagedIdentity.ResourceID

	capzIdentifier := string(internalazure.ClusterOperatorIdentifierClusterAPIAzure)
	capzResourceID, ok := cluster.CustomerProperties.Platform.OperatorsAuthentication.UserAssignedIdentities.ControlPlaneOperators[capzIdentifier]
	if !ok || capzResourceID == nil {
		return utils.TrackError(fmt.Errorf("CAPZ identity (%s) not found in cluster operator identities", capzIdentifier))
	}

	smiResourceID := cluster.CustomerProperties.Platform.OperatorsAuthentication.UserAssignedIdentities.ServiceManagedIdentity
	clusterIdentityURL := cluster.ServiceProviderProperties.ManagedIdentitiesDataPlaneIdentityURL

	uaisClient, err := v.smiClientBuilder.UserAssignedIdentitiesClient(ctx, clusterIdentityURL, smiResourceID, cluster.ID.SubscriptionID)
	if err != nil {
		return utils.TrackError(fmt.Errorf("failed to get user assigned identities client: %w", err))
	}

	capzMI, err := uaisClient.Get(ctx, capzResourceID.ResourceGroupName, capzResourceID.Name, nil)
	if err != nil {
		return utils.TrackError(fmt.Errorf("failed to get CAPZ managed identity %s: %w", capzResourceID, err))
	}
	if capzMI.Properties == nil || capzMI.Properties.PrincipalID == nil {
		return utils.TrackError(fmt.Errorf("CAPZ managed identity %s has no principal ID", capzResourceID))
	}
	capzPrincipalID := *capzMI.Properties.PrincipalID

	tenantID := *clusterSubscription.Properties.TenantId
	checkAccessClient, err := v.checkAccessV2ClientBuilder.Build(tenantID)
	if err != nil {
		return utils.TrackError(fmt.Errorf("failed to build CheckAccess V2 client: %w", err))
	}

	authzReq := checkaccessv2.AuthorizationRequest{
		Subject: checkaccessv2.SubjectInfo{
			Attributes: checkaccessv2.SubjectAttributes{
				ObjectId: capzPrincipalID,
			},
		},
		Actions: []checkaccessv2.ActionInfo{
			{Id: assignAction},
		},
		Resource: checkaccessv2.ResourceInfo{
			Id: acrPullMIResourceID.String(),
		},
	}

	resp, err := checkAccessClient.CheckAccess(ctx, authzReq)
	if err != nil {
		return utils.TrackError(fmt.Errorf("failed to check CAPZ assign/action permission on ACR pull MI: %w", err))
	}

	for _, decision := range resp.Value {
		if decision.ActionId == assignAction && decision.AccessDecision == checkaccessv2.Allowed {
			return nil
		}
	}

	return utils.TrackError(fmt.Errorf(
		"CAPZ identity %s does not have assign/action permission on the ACR pull managed identity %s. "+
			"Grant 'Managed Identity Operator' to the CAPZ identity scoped to the ACR pull MI: "+
			"az role assignment create --assignee-object-id %s --assignee-principal-type ServicePrincipal "+
			"--role \"Managed Identity Operator\" --scope %s",
		capzResourceID, acrPullMIResourceID,
		capzPrincipalID, acrPullMIResourceID,
	))
}
