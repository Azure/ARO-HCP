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

package validation

import (
	"context"
	"testing"

	azcorearm "github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"

	"github.com/Azure/ARO-HCP/internal/api"
	"github.com/Azure/ARO-HCP/internal/api/arm"
)

func validManagementClusterDeployment(t *testing.T) *api.ManagementClusterDeployment {
	t.Helper()
	resourceID := api.Must(api.ToManagementClusterDeploymentResourceID("1"))
	return &api.ManagementClusterDeployment{
		CosmosMetadata: arm.CosmosMetadata{
			ResourceID: resourceID,
		},
		Status: api.ManagementClusterDeploymentStatus{
			AKSResourceID:                                        api.Must(azcorearm.ParseResourceID("/subscriptions/00000000-0000-0000-0000-000000000000/resourceGroups/hcp-underlay-westus3/providers/Microsoft.ContainerService/managedClusters/hcp-underlay-westus3-mgmt-1")),
			PublicDNSZoneResourceID:                              api.Must(azcorearm.ParseResourceID("/subscriptions/00000000-0000-0000-0000-000000000000/resourceGroups/global/providers/Microsoft.Network/dnszones/westus3.aroapp.io")),
			HostedClustersSecretsKeyVaultURL:                     "https://hcp-underlay-westus3-mgmt-1-cx.vault.azure.net",
			HostedClustersManagedIdentitiesKeyVaultURL:           "https://hcp-underlay-westus3-mgmt-1-msi.vault.azure.net",
			HostedClustersSecretsKeyVaultManagedIdentityClientID: "00000000-0000-0000-0000-000000000001",
			MaestroConsumerName:                                  "hcp-underlay-westus3-mgmt-1",
			MaestroRESTAPIURL:                                    "http://maestro.maestro.svc.cluster.local:8000",
			MaestroGRPCTarget:                                    "maestro-grpc.maestro.svc.cluster.local:8090",
		},
	}
}

func TestValidateManagementClusterDeploymentCreate(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name         string
		modify       func(t *testing.T, mcd *api.ManagementClusterDeployment)
		expectErrors []expectedError
	}{
		{
			name:         "valid create",
			modify:       func(t *testing.T, mcd *api.ManagementClusterDeployment) {},
			expectErrors: nil,
		},
		{
			name: "empty status rejected",
			modify: func(t *testing.T, mcd *api.ManagementClusterDeployment) {
				mcd.Status = api.ManagementClusterDeploymentStatus{}
			},
			expectErrors: []expectedError{
				{fieldPath: "status.aksResourceID", message: "Required"},
				{fieldPath: "status.publicDNSZoneResourceID", message: "Required"},
				{fieldPath: "status.hostedClustersSecretsKeyVaultURL", message: "Required"},
				{fieldPath: "status.hostedClustersManagedIdentitiesKeyVaultURL", message: "Required"},
				{fieldPath: "status.hostedClustersSecretsKeyVaultManagedIdentityClientID", message: "Required"},
				{fieldPath: "status.maestroConsumerName", message: "Required"},
				{fieldPath: "status.maestroRESTAPIURL", message: "Required"},
				{fieldPath: "status.maestroGRPCTarget", message: "Required"},
			},
		},
		{
			name: "missing aksResourceID rejected",
			modify: func(t *testing.T, mcd *api.ManagementClusterDeployment) {
				mcd.Status.AKSResourceID = nil
			},
			expectErrors: []expectedError{
				{fieldPath: "status.aksResourceID", message: "Required"},
			},
		},
		{
			name: "missing publicDNSZoneResourceID rejected",
			modify: func(t *testing.T, mcd *api.ManagementClusterDeployment) {
				mcd.Status.PublicDNSZoneResourceID = nil
			},
			expectErrors: []expectedError{
				{fieldPath: "status.publicDNSZoneResourceID", message: "Required"},
			},
		},
		{
			name: "invalid hostedClustersSecretsKeyVaultManagedIdentityClientID",
			modify: func(t *testing.T, mcd *api.ManagementClusterDeployment) {
				mcd.Status.HostedClustersSecretsKeyVaultManagedIdentityClientID = "not-a-uuid"
			},
			expectErrors: []expectedError{
				{fieldPath: "status.hostedClustersSecretsKeyVaultManagedIdentityClientID", message: "invalid"},
			},
		},
		{
			name: "aksResourceID wrong resource type",
			modify: func(t *testing.T, mcd *api.ManagementClusterDeployment) {
				mcd.Status.AKSResourceID = api.Must(azcorearm.ParseResourceID("/subscriptions/00000000-0000-0000-0000-000000000000/resourceGroups/rg/providers/Microsoft.Compute/virtualMachines/vm"))
			},
			expectErrors: []expectedError{
				{fieldPath: "status.aksResourceID", message: "must reference an instance of type"},
			},
		},
		{
			name: "publicDNSZoneResourceID wrong resource type",
			modify: func(t *testing.T, mcd *api.ManagementClusterDeployment) {
				mcd.Status.PublicDNSZoneResourceID = api.Must(azcorearm.ParseResourceID("/subscriptions/00000000-0000-0000-0000-000000000000/resourceGroups/rg/providers/Microsoft.Storage/storageAccounts/sa"))
			},
			expectErrors: []expectedError{
				{fieldPath: "status.publicDNSZoneResourceID", message: "must reference an instance of type"},
			},
		},
		{
			name: "aksResourceID missing resource group",
			modify: func(t *testing.T, mcd *api.ManagementClusterDeployment) {
				mcd.Status.AKSResourceID = api.Must(azcorearm.ParseResourceID("/subscriptions/00000000-0000-0000-0000-000000000000/providers/Microsoft.ContainerService/managedClusters/cluster"))
			},
			expectErrors: []expectedError{
				{fieldPath: "status.aksResourceID", message: "resource group is required"},
			},
		},
		{
			name: "invalid maestroGRPCTarget - missing port",
			modify: func(t *testing.T, mcd *api.ManagementClusterDeployment) {
				mcd.Status.MaestroGRPCTarget = "maestro-grpc.maestro.svc.cluster.local"
			},
			expectErrors: []expectedError{
				{fieldPath: "status.maestroGRPCTarget", message: "must be host:port"},
			},
		},
		{
			name: "managementClusterID accepted when set",
			modify: func(t *testing.T, mcd *api.ManagementClusterDeployment) {
				mcd.Status.ManagementClusterID = api.Must(api.ToManagementClusterResourceID("00000000-0000-0000-0000-000000000000", "hcp-underlay-westus3", "hcp-underlay-westus3-mgmt-1"))
			},
			expectErrors: nil,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			mcd := validManagementClusterDeployment(t)
			tt.modify(t, mcd)
			errs := ValidateManagementClusterDeploymentCreate(context.Background(), mcd)
			verifyErrorsMatch(t, tt.expectErrors, errs)
		})
	}
}

func TestValidateManagementClusterDeploymentUpdate(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name         string
		modify       func(t *testing.T, mcd *api.ManagementClusterDeployment)
		expectErrors []expectedError
	}{
		{
			name:         "valid update - no changes",
			modify:       func(t *testing.T, mcd *api.ManagementClusterDeployment) {},
			expectErrors: nil,
		},
		{
			name: "aksResourceID changed",
			modify: func(t *testing.T, mcd *api.ManagementClusterDeployment) {
				mcd.Status.AKSResourceID = api.Must(azcorearm.ParseResourceID("/subscriptions/00000000-0000-0000-0000-000000000000/resourceGroups/rg/providers/Microsoft.ContainerService/managedClusters/different-name"))
			},
			expectErrors: []expectedError{
				{fieldPath: "status.aksResourceID", message: "immutable"},
			},
		},
		{
			name: "publicDNSZoneResourceID changed",
			modify: func(t *testing.T, mcd *api.ManagementClusterDeployment) {
				mcd.Status.PublicDNSZoneResourceID = api.Must(azcorearm.ParseResourceID("/subscriptions/00000000-0000-0000-0000-000000000000/resourceGroups/rg/providers/Microsoft.Network/dnszones/other.com"))
			},
			expectErrors: []expectedError{
				{fieldPath: "status.publicDNSZoneResourceID", message: "immutable"},
			},
		},
		{
			name: "hostedClustersSecretsKeyVaultURL changed",
			modify: func(t *testing.T, mcd *api.ManagementClusterDeployment) {
				mcd.Status.HostedClustersSecretsKeyVaultURL = "https://kv-other.vault.azure.net"
			},
			expectErrors: []expectedError{
				{fieldPath: "status.hostedClustersSecretsKeyVaultURL", message: "immutable"},
			},
		},
		{
			name: "hostedClustersManagedIdentitiesKeyVaultURL changed",
			modify: func(t *testing.T, mcd *api.ManagementClusterDeployment) {
				mcd.Status.HostedClustersManagedIdentitiesKeyVaultURL = "https://kv-other.vault.azure.net"
			},
			expectErrors: []expectedError{
				{fieldPath: "status.hostedClustersManagedIdentitiesKeyVaultURL", message: "immutable"},
			},
		},
		{
			name: "hostedClustersSecretsKeyVaultManagedIdentityClientID changed",
			modify: func(t *testing.T, mcd *api.ManagementClusterDeployment) {
				mcd.Status.HostedClustersSecretsKeyVaultManagedIdentityClientID = "99999999-9999-9999-9999-999999999999"
			},
			expectErrors: []expectedError{
				{fieldPath: "status.hostedClustersSecretsKeyVaultManagedIdentityClientID", message: "immutable"},
			},
		},
		{
			name: "maestroConsumerName changed",
			modify: func(t *testing.T, mcd *api.ManagementClusterDeployment) {
				mcd.Status.MaestroConsumerName = "different-consumer"
			},
			expectErrors: []expectedError{
				{fieldPath: "status.maestroConsumerName", message: "immutable"},
			},
		},
		{
			name: "maestroRESTAPIURL changed",
			modify: func(t *testing.T, mcd *api.ManagementClusterDeployment) {
				mcd.Status.MaestroRESTAPIURL = "http://different:8000"
			},
			expectErrors: []expectedError{
				{fieldPath: "status.maestroRESTAPIURL", message: "immutable"},
			},
		},
		{
			name: "maestroGRPCTarget changed",
			modify: func(t *testing.T, mcd *api.ManagementClusterDeployment) {
				mcd.Status.MaestroGRPCTarget = "different:8090"
			},
			expectErrors: []expectedError{
				{fieldPath: "status.maestroGRPCTarget", message: "immutable"},
			},
		},
		{
			name: "managementClusterID changed after set",
			modify: func(t *testing.T, mcd *api.ManagementClusterDeployment) {
				mcd.Status.ManagementClusterID = api.Must(api.ToManagementClusterResourceID("11111111-1111-1111-1111-111111111111", "other-rg", "different-mgmt-1"))
			},
			expectErrors: []expectedError{
				{fieldPath: "status.managementClusterID", message: "immutable"},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			oldObj := validManagementClusterDeployment(t)
			oldObj.Status.ManagementClusterID = api.Must(api.ToManagementClusterResourceID("00000000-0000-0000-0000-000000000000", "hcp-underlay-westus3", "hcp-underlay-westus3-mgmt-1"))
			newObj := validManagementClusterDeployment(t)
			newObj.Status.ManagementClusterID = api.Must(api.ToManagementClusterResourceID("00000000-0000-0000-0000-000000000000", "hcp-underlay-westus3", "hcp-underlay-westus3-mgmt-1"))
			tt.modify(t, newObj)
			errs := ValidateManagementClusterDeploymentUpdate(context.Background(), newObj, oldObj)
			verifyErrorsMatch(t, tt.expectErrors, errs)
		})
	}
}
