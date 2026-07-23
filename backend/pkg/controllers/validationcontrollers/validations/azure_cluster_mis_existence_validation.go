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
	"strings"

	azcorearm "github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"

	azureclient "github.com/Azure/ARO-HCP/backend/pkg/azure/client"
	"github.com/Azure/ARO-HCP/internal/api"
	"github.com/Azure/ARO-HCP/internal/api/arm"
)

// AzureClusterManagedIdentitiesExistenceValidation validates the existence of all managed identities defined in the cluster.
// It assumes all identities present are for recognized operators.
type AzureClusterManagedIdentitiesExistenceValidation struct {
	smiClientBuilder azureclient.ServiceManagedIdentityClientBuilder
}

func NewAzureClusterManagedIdentitiesExistenceValidation(
	smiClientBuilder azureclient.ServiceManagedIdentityClientBuilder,
) *AzureClusterManagedIdentitiesExistenceValidation {
	return &AzureClusterManagedIdentitiesExistenceValidation{
		smiClientBuilder: smiClientBuilder,
	}
}

func (v *AzureClusterManagedIdentitiesExistenceValidation) Name() string {
	return "AzureClusterManagedIdentitiesExistenceValidation"
}

func (v *AzureClusterManagedIdentitiesExistenceValidation) Validate(ctx context.Context, clusterSubscription *arm.Subscription, cluster *api.HCPOpenShiftCluster) *ValidationResult {
	smiResourceID := cluster.CustomerProperties.Platform.OperatorsAuthentication.UserAssignedIdentities.ServiceManagedIdentity
	clusterIdentityURL := cluster.ServiceProviderProperties.ManagedIdentitiesDataPlaneIdentityURL
	// We check the existence of the Cluster's Service Managed Identity by
	// attempting to retrieve the user assigned identities client using the
	// service managed identity's identity credentials, which we obtain by
	// requesting them via the Managed Identities Data Plane Service. If the
	// service managed identity does not exist the request will fail.
	uaisClient, err := v.smiClientBuilder.UserAssignedIdentitiesClient(ctx, clusterIdentityURL, smiResourceID, cluster.ID.SubscriptionID)
	if err != nil {
		msg := fmt.Sprintf("failed to get user assigned identities client: %s", err)
		return &ValidationResult{
			Outcome: OutcomeTypeUnknown,
			Unknown: &UnknownResult{
				Reason:                 "InfrastructureError",
				ServiceProviderMessage: msg,
				UserMessage:            "Unable to verify managed identities existence.",
				ReportingPolicy:        ReportingPolicyTypeError,
			},
		}
	}

	clusterUAIsProfile := &cluster.CustomerProperties.Platform.OperatorsAuthentication.UserAssignedIdentities
	clusterOperatorsMIsResourceIDs := v.clusterOperatorsManagedIdentities(clusterUAIsProfile)

	var notFoundMIsStrs []string
	for _, resourceID := range clusterOperatorsMIsResourceIDs {
		_, err := uaisClient.Get(ctx, resourceID.ResourceGroupName, resourceID.Name, nil)
		if azureclient.IsResourceNotFoundErr(err) {
			notFoundMIsStrs = append(notFoundMIsStrs, resourceID.String())
			continue
		}
		if err != nil {
			msg := fmt.Sprintf("failed to get managed identity '%s': %s", resourceID, err)
			return &ValidationResult{
				Outcome: OutcomeTypeUnknown,
				Unknown: &UnknownResult{
					Reason:                 "InfrastructureError",
					ServiceProviderMessage: msg,
					UserMessage:            "Unable to verify managed identities existence.",
					ReportingPolicy:        ReportingPolicyTypeError,
				},
			}
		}
	}

	if len(notFoundMIsStrs) > 0 {
		userMsg := fmt.Sprintf("Managed identities not found: %s", strings.Join(notFoundMIsStrs, ", "))
		return &ValidationResult{
			Outcome: OutcomeTypeFailed,
			Failed: &FailedResult{
				Reason:                 "ManagedIdentitiesNotFound",
				ServiceProviderMessage: userMsg,
				UserMessage:            userMsg,
			},
		}
	}

	return &ValidationResult{Outcome: OutcomeTypePassed}
}

// clusterOperatorsManagedIdentities returns a list of the control and data plane identities defined in the cluster.
func (v *AzureClusterManagedIdentitiesExistenceValidation) clusterOperatorsManagedIdentities(
	clusterUAIsProfile *api.UserAssignedIdentitiesProfile) []*azcorearm.ResourceID {
	var resourceIDs []*azcorearm.ResourceID

	for _, miResourceID := range clusterUAIsProfile.ControlPlaneOperators {
		resourceIDs = append(resourceIDs, miResourceID)
	}
	for _, miResourceID := range clusterUAIsProfile.DataPlaneOperators {
		resourceIDs = append(resourceIDs, miResourceID)
	}

	return resourceIDs
}
