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

package validationutils

import (
	"context"
	"fmt"

	checkaccessv2 "github.com/Azure/checkaccess-v2-go-sdk/client"

	azureclient "github.com/Azure/ARO-HCP/backend/pkg/azure/client"
	"github.com/Azure/ARO-HCP/internal/api/coreapi"
	internalazure "github.com/Azure/ARO-HCP/internal/azure"
)

const assignAction = "Microsoft.ManagedIdentity/userAssignedIdentities/assign/action"

// ContainerRegistryPullCredentialsPermissionValidation validates that the CAPZ identity
// has assign/action permission on the customer's container registry pull managed identity.
// This permission is required for CAPZ to attach the MI to worker VMs.
type ContainerRegistryPullCredentialsPermissionValidation struct {
	smiClientBuilder           azureclient.ServiceManagedIdentityClientBuilder
	checkAccessV2ClientBuilder azureclient.CheckAccessV2ClientBuilder
}

func NewContainerRegistryPullCredentialsPermissionValidation(
	smiClientBuilder azureclient.ServiceManagedIdentityClientBuilder,
	checkAccessV2ClientBuilder azureclient.CheckAccessV2ClientBuilder,
) *ContainerRegistryPullCredentialsPermissionValidation {
	return &ContainerRegistryPullCredentialsPermissionValidation{
		smiClientBuilder:           smiClientBuilder,
		checkAccessV2ClientBuilder: checkAccessV2ClientBuilder,
	}
}

func (v *ContainerRegistryPullCredentialsPermissionValidation) Name() string {
	return "ContainerRegistryPullCredentialsPermissionValidation"
}

func (v *ContainerRegistryPullCredentialsPermissionValidation) InputKey(cluster *coreapi.HCPOpenShiftCluster) string {
	mi := cluster.CustomerProperties.Platform.ContainerRegistry.PullManagedIdentity
	if mi == nil {
		return ""
	}
	return mi.String()
}

func (v *ContainerRegistryPullCredentialsPermissionValidation) Validate(ctx context.Context, clusterSubscription *coreapi.Subscription, cluster *coreapi.HCPOpenShiftCluster) ValidationResult {
	containerRegistryPullMI := cluster.CustomerProperties.Platform.ContainerRegistry.PullManagedIdentity
	if containerRegistryPullMI == nil {
		return SkippedValidation(
			"NotApplicable",
			"No container registry pull managed identity configured.",
			"containerRegistryPullManagedIdentity is nil, skipping validation",
		)
	}

	capzIdentifier := string(internalazure.ClusterOperatorIdentifierClusterAPIAzure)
	capzResourceID, ok := cluster.CustomerProperties.Platform.OperatorsAuthentication.UserAssignedIdentities.ControlPlaneOperators[capzIdentifier]
	if !ok || capzResourceID == nil {
		return UnknownValidation(
			"InternalError",
			"Unable to verify container registry pull credentials permissions.",
			fmt.Sprintf("CAPZ identity (%s) not found in cluster operator identities", capzIdentifier),
			ControllerReportingPolicyTypeError,
		)
	}

	smiResourceID := cluster.CustomerProperties.Platform.OperatorsAuthentication.UserAssignedIdentities.ServiceManagedIdentity
	clusterIdentityURL := cluster.ServiceProviderProperties.ManagedIdentitiesDataPlaneIdentityURL

	acrPullMIClient, err := v.smiClientBuilder.UserAssignedIdentitiesClient(ctx, clusterIdentityURL, smiResourceID, containerRegistryPullMI.SubscriptionID)
	if err != nil {
		return UnknownValidation(
			"InternalError",
			"Unable to verify container registry pull credentials permissions.",
			fmt.Sprintf("failed to get user assigned identities client for container registry pull MI subscription: %s", err),
			ControllerReportingPolicyTypeError,
		)
	}

	_, err = acrPullMIClient.Get(ctx, containerRegistryPullMI.ResourceGroupName, containerRegistryPullMI.Name, nil)
	if err != nil {
		return FailedValidation(
			"ManagedIdentityNotFound",
			fmt.Sprintf("Container registry pull managed identity %s not found or not accessible: %s", containerRegistryPullMI, err),
			fmt.Sprintf("container registry pull managed identity %s not found or not accessible: %s", containerRegistryPullMI, err),
		)
	}

	capzClient, err := v.smiClientBuilder.UserAssignedIdentitiesClient(ctx, clusterIdentityURL, smiResourceID, capzResourceID.SubscriptionID)
	if err != nil {
		return UnknownValidation(
			"InternalError",
			"Unable to verify container registry pull credentials permissions.",
			fmt.Sprintf("failed to get user assigned identities client for CAPZ MI subscription: %s", err),
			ControllerReportingPolicyTypeError,
		)
	}

	capzMI, err := capzClient.Get(ctx, capzResourceID.ResourceGroupName, capzResourceID.Name, nil)
	if err != nil {
		return UnknownValidation(
			"InternalError",
			"Unable to verify container registry pull credentials permissions.",
			fmt.Sprintf("failed to get CAPZ managed identity %s: %s", capzResourceID, err),
			ControllerReportingPolicyTypeError,
		)
	}
	if capzMI.Properties == nil || capzMI.Properties.PrincipalID == nil {
		return UnknownValidation(
			"InternalError",
			"Unable to verify container registry pull credentials permissions.",
			fmt.Sprintf("CAPZ managed identity %s has no principal ID", capzResourceID),
			ControllerReportingPolicyTypeError,
		)
	}
	capzPrincipalID := *capzMI.Properties.PrincipalID

	tenantID := *clusterSubscription.Properties.TenantId
	checkAccessClient, err := v.checkAccessV2ClientBuilder.Build(tenantID)
	if err != nil {
		return UnknownValidation(
			"InternalError",
			"Unable to verify container registry pull credentials permissions.",
			fmt.Sprintf("failed to build CheckAccess V2 client: %s", err),
			ControllerReportingPolicyTypeError,
		)
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
			Id: containerRegistryPullMI.String(),
		},
	}

	resp, err := checkAccessClient.CheckAccess(ctx, authzReq)
	if err != nil {
		return UnknownValidation(
			"InternalError",
			"Unable to verify container registry pull credentials permissions.",
			fmt.Sprintf("failed to check CAPZ assign/action permission on container registry pull MI: %s", err),
			ControllerReportingPolicyTypeError,
		)
	}

	for _, decision := range resp.Value {
		if decision.ActionId == assignAction && decision.AccessDecision == checkaccessv2.Allowed {
			return PassedValidation(
				coreapi.ControllerConditionReasonAsExpected,
				v.InputKey(cluster),
				fmt.Sprintf("CAPZ identity has assign/action permission on container registry pull MI %s.", containerRegistryPullMI),
			)
		}
	}

	userMsg := fmt.Sprintf(
		"CAPZ identity %s does not have assign/action permission on the container registry pull managed identity %s. "+
			"Grant 'Managed Identity Operator' to the CAPZ identity scoped to the container registry pull MI: "+
			"az role assignment create --assignee-object-id %s --assignee-principal-type ServicePrincipal "+
			"--role \"Managed Identity Operator\" --scope %s",
		capzResourceID, containerRegistryPullMI,
		capzPrincipalID, containerRegistryPullMI,
	)
	return FailedValidation("PermissionDenied", userMsg, userMsg)
}
