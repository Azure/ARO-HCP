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
	"fmt"
	"strings"

	azcorearm "github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"

	"github.com/Azure/ARO-HCP/backend/pkg/azure/roleassignment"
	"github.com/Azure/ARO-HCP/internal/api/coreapi"
	"github.com/Azure/ARO-HCP/internal/azure"
	controllerutil "github.com/Azure/ARO-HCP/internal/controllerutils"
	"github.com/Azure/ARO-HCP/internal/utils"
)

// secretNameIdentifier returns the identifier passed to
// dataplane.FormatUserAssignedIdentityCredentialsForStorage. The library
// prefixes it with "uamsi-" to produce the Key Vault secret name.
func secretNameIdentifier(csClusterID, operatorName string) string {
	return fmt.Sprintf("%s-%s", csClusterID, operatorName)
}

// resolvedMSIBasedOperatorIdentity returns the ClientID, PrincipalID, and
// TenantID for a control-plane operator identity from ManagedIdentityDetails.
// Hardcoded-identity environments store that metadata on
// MetadataFromHardcodedIdentity; environments with a real Managed Identities
// Data Plane store it on MetadataFromManagedIdentitiesDataplaneService.
// ARM User Assigned Identities metadata is not used: in hardcoded-identity
// environments it describes the customer UAMI, not the backing credentials.
func resolvedMSIBasedOperatorIdentity(serviceProviderCluster *coreapi.ServiceProviderCluster, identityResourceID *azcorearm.ResourceID) (coreapi.MSIBasedOperatorCredentialsObservedIdentity, bool) {
	key := strings.ToLower(identityResourceID.String())
	metadata, ok := serviceProviderCluster.Status.ManagedIdentityDetails[key]
	if !ok || metadata == nil {
		return coreapi.MSIBasedOperatorCredentialsObservedIdentity{}, false
	}

	value := metadata.MetadataFromHardcodedIdentity
	if value == nil {
		value = metadata.MetadataFromManagedIdentitiesDataplaneService
	}
	if value == nil || !value.HasResolvedIdentityInformation() {
		return coreapi.MSIBasedOperatorCredentialsObservedIdentity{}, false
	}

	return coreapi.MSIBasedOperatorCredentialsObservedIdentity{
		ResourceID:  identityResourceID,
		ClientID:    *value.ClientID,
		PrincipalID: *value.PrincipalID,
		TenantID:    *value.TenantID,
	}, true
}

func observedIdentityEqual(a, b coreapi.MSIBasedOperatorCredentialsObservedIdentity) bool {
	if a.ClientID != b.ClientID || a.PrincipalID != b.PrincipalID || a.TenantID != b.TenantID {
		return false
	}
	return controllerutil.ResourceIDsEqual(a.ResourceID, b.ResourceID)
}

func stampIdentifierFromManagementClusterResourceID(mcResourceID *azcorearm.ResourceID) (string, error) {
	if mcResourceID == nil || mcResourceID.Parent == nil || len(mcResourceID.Parent.Name) == 0 {
		return "", utils.TrackError(fmt.Errorf("management cluster resource ID is missing parent stamp identifier"))
	}
	return mcResourceID.Parent.Name, nil
}

func expectedControlPlaneOperatorRoleAssignmentIDs(
	cluster *coreapi.HCPOpenShiftCluster,
	clusterScopedIdentitiesConfig *azure.ClusterScopedIdentitiesConfig,
	operatorName string,
	principalID string,
) ([]*azcorearm.ResourceID, error) {
	managedResourceGroupName := cluster.CustomerProperties.Platform.ManagedResourceGroup
	if len(managedResourceGroupName) == 0 {
		return nil, utils.TrackError(fmt.Errorf("managed resource group name is empty for cluster %q", cluster.ID.String()))
	}
	if len(principalID) == 0 {
		return nil, nil
	}
	scopeID, err := coreapi.ToResourceGroupResourceID(cluster.ID.SubscriptionID, managedResourceGroupName)
	if err != nil {
		return nil, utils.TrackError(fmt.Errorf("failed to build managed resource group scope for cluster %q: %w", cluster.ID.String(), err))
	}

	operatorIdentity, ok := clusterScopedIdentitiesConfig.ControlPlaneOperatorsIdentities[azure.ClusterOperatorIdentifier(operatorName)]
	if !ok || operatorIdentity == nil {
		return nil, utils.TrackError(fmt.Errorf("no control plane operator identity configuration for operator %q", operatorName))
	}
	roleDefinitionIDs := operatorIdentity.RoleDefinitionsResourceIDs()
	if len(roleDefinitionIDs) == 0 {
		return nil, utils.TrackError(fmt.Errorf("no role definitions configured for control plane operator %q", operatorName))
	}

	var expected []*azcorearm.ResourceID
	for _, roleDefinitionID := range roleDefinitionIDs {
		if roleDefinitionID == nil {
			continue
		}
		fullID := roleassignment.ManagedResourceGroupScopedRoleAssignmentResourceID(scopeID.String(), principalID, roleDefinitionID.String())
		parsed, err := azcorearm.ParseResourceID(fullID)
		if err != nil {
			return nil, utils.TrackError(fmt.Errorf("failed to parse role assignment resource ID %q: %w", fullID, err))
		}
		expected = append(expected, parsed)
	}
	return expected, nil
}

func operatorRoleAssignmentsComplete(
	cluster *coreapi.HCPOpenShiftCluster,
	serviceProviderCluster *coreapi.ServiceProviderCluster,
	clusterScopedIdentitiesConfig *azure.ClusterScopedIdentitiesConfig,
	operatorName string,
	principalID string,
) (bool, error) {
	expected, err := expectedControlPlaneOperatorRoleAssignmentIDs(cluster, clusterScopedIdentitiesConfig, operatorName, principalID)
	if err != nil {
		return false, err
	}
	if len(expected) == 0 {
		return false, nil
	}

	confirmed := serviceProviderCluster.Status.AzureResources.RoleAssignments.AzureResources
	for _, expectedID := range expected {
		found := false
		for _, confirmedID := range confirmed {
			if controllerutil.ResourceIDsEqual(confirmedID, expectedID) {
				found = true
				break
			}
		}
		if !found {
			return false, nil
		}
	}
	return true, nil
}
