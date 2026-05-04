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
	"github.com/Azure/ARO-HCP/internal/api/fleet"
)

func validManagementClusterDeployment(t *testing.T) *fleet.ManagementClusterDeployment {
	t.Helper()
	return &fleet.ManagementClusterDeployment{
		CosmosMetadata: api.CosmosMetadata{
			ResourceID: api.Must(fleet.ToManagementClusterDeploymentResourceID("test-stamp-1")),
		},
		Spec: fleet.ManagementClusterDeploymentSpec{
			StampIdentifier: "test-stamp-1",
		},
		Status: fleet.ManagementClusterDeploymentStatus{
			AKSResourceID:                                        api.Must(azcorearm.ParseResourceID("/subscriptions/00000000-0000-0000-0000-000000000000/resourceGroups/rg/providers/Microsoft.ContainerService/managedClusters/test-stamp-1")),
			PublicDNSZoneResourceID:                              api.Must(azcorearm.ParseResourceID("/subscriptions/00000000-0000-0000-0000-000000000000/resourceGroups/rg/providers/Microsoft.Network/dnszones/example.com")),
			HostedClustersSecretsKeyVaultURL:                     "https://kv-cx-secrets.vault.azure.net",
			HostedClustersManagedIdentitiesKeyVaultURL:           "https://kv-cx-mi.vault.azure.net",
			HostedClustersSecretsKeyVaultManagedIdentityClientID: "12345678-1234-1234-1234-123456789012",
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
		modify       func(t *testing.T, mcd *fleet.ManagementClusterDeployment)
		expectErrors []expectedError
	}{
		{
			name:         "valid create",
			modify:       func(t *testing.T, mcd *fleet.ManagementClusterDeployment) {},
			expectErrors: nil,
		},
		// Spec — StampIdentifier
		{
			name: "missing stampIdentifier rejected",
			modify: func(t *testing.T, mcd *fleet.ManagementClusterDeployment) {
				mcd.Spec.StampIdentifier = ""
			},
			expectErrors: []expectedError{
				{fieldPath: "spec.stampIdentifier", message: "Required"},
			},
		},
		// Status — all required fields missing
		{
			name: "empty status rejected",
			modify: func(t *testing.T, mcd *fleet.ManagementClusterDeployment) {
				mcd.Status = fleet.ManagementClusterDeploymentStatus{}
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
		// Status — individual required fields
		{
			name: "missing aksResourceID rejected",
			modify: func(t *testing.T, mcd *fleet.ManagementClusterDeployment) {
				mcd.Status.AKSResourceID = nil
			},
			expectErrors: []expectedError{
				{fieldPath: "status.aksResourceID", message: "Required"},
			},
		},
		{
			name: "missing publicDNSZoneResourceID rejected",
			modify: func(t *testing.T, mcd *fleet.ManagementClusterDeployment) {
				mcd.Status.PublicDNSZoneResourceID = nil
			},
			expectErrors: []expectedError{
				{fieldPath: "status.publicDNSZoneResourceID", message: "Required"},
			},
		},
		{
			name: "missing hostedClustersSecretsKeyVaultURL rejected",
			modify: func(t *testing.T, mcd *fleet.ManagementClusterDeployment) {
				mcd.Status.HostedClustersSecretsKeyVaultURL = ""
			},
			expectErrors: []expectedError{
				{fieldPath: "status.hostedClustersSecretsKeyVaultURL", message: "Required"},
			},
		},
		{
			name: "missing hostedClustersManagedIdentitiesKeyVaultURL rejected",
			modify: func(t *testing.T, mcd *fleet.ManagementClusterDeployment) {
				mcd.Status.HostedClustersManagedIdentitiesKeyVaultURL = ""
			},
			expectErrors: []expectedError{
				{fieldPath: "status.hostedClustersManagedIdentitiesKeyVaultURL", message: "Required"},
			},
		},
		{
			name: "missing hostedClustersSecretsKeyVaultManagedIdentityClientID rejected",
			modify: func(t *testing.T, mcd *fleet.ManagementClusterDeployment) {
				mcd.Status.HostedClustersSecretsKeyVaultManagedIdentityClientID = ""
			},
			expectErrors: []expectedError{
				{fieldPath: "status.hostedClustersSecretsKeyVaultManagedIdentityClientID", message: "Required"},
			},
		},
		{
			name: "missing maestroConsumerName rejected",
			modify: func(t *testing.T, mcd *fleet.ManagementClusterDeployment) {
				mcd.Status.MaestroConsumerName = ""
			},
			expectErrors: []expectedError{
				{fieldPath: "status.maestroConsumerName", message: "Required"},
			},
		},
		{
			name: "missing maestroRESTAPIURL rejected",
			modify: func(t *testing.T, mcd *fleet.ManagementClusterDeployment) {
				mcd.Status.MaestroRESTAPIURL = ""
			},
			expectErrors: []expectedError{
				{fieldPath: "status.maestroRESTAPIURL", message: "Required"},
			},
		},
		{
			name: "missing maestroGRPCTarget rejected",
			modify: func(t *testing.T, mcd *fleet.ManagementClusterDeployment) {
				mcd.Status.MaestroGRPCTarget = ""
			},
			expectErrors: []expectedError{
				{fieldPath: "status.maestroGRPCTarget", message: "Required"},
			},
		},
		// Status — format validation
		{
			name: "invalid hostedClustersSecretsKeyVaultManagedIdentityClientID",
			modify: func(t *testing.T, mcd *fleet.ManagementClusterDeployment) {
				mcd.Status.HostedClustersSecretsKeyVaultManagedIdentityClientID = "not-a-uuid"
			},
			expectErrors: []expectedError{
				{fieldPath: "status.hostedClustersSecretsKeyVaultManagedIdentityClientID", message: "invalid"},
			},
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
		modify       func(t *testing.T, mcd *fleet.ManagementClusterDeployment)
		expectErrors []expectedError
	}{
		{
			name:         "valid update - no changes",
			modify:       func(t *testing.T, mcd *fleet.ManagementClusterDeployment) {},
			expectErrors: nil,
		},
		// Immutability checks
		{
			name: "stampIdentifier changed",
			modify: func(t *testing.T, mcd *fleet.ManagementClusterDeployment) {
				mcd.Spec.StampIdentifier = "different-stamp"
			},
			expectErrors: []expectedError{
				{fieldPath: "spec.stampIdentifier", message: "immutable"},
			},
		},
		{
			name: "aksResourceID changed",
			modify: func(t *testing.T, mcd *fleet.ManagementClusterDeployment) {
				mcd.Status.AKSResourceID = api.Must(azcorearm.ParseResourceID("/subscriptions/00000000-0000-0000-0000-000000000000/resourceGroups/rg/providers/Microsoft.ContainerService/managedClusters/different-name"))
			},
			expectErrors: []expectedError{
				{fieldPath: "status.aksResourceID", message: "immutable"},
			},
		},
		{
			name: "publicDNSZoneResourceID changed",
			modify: func(t *testing.T, mcd *fleet.ManagementClusterDeployment) {
				mcd.Status.PublicDNSZoneResourceID = api.Must(azcorearm.ParseResourceID("/subscriptions/00000000-0000-0000-0000-000000000000/resourceGroups/rg/providers/Microsoft.Network/dnszones/other.com"))
			},
			expectErrors: []expectedError{
				{fieldPath: "status.publicDNSZoneResourceID", message: "immutable"},
			},
		},
		{
			name: "hostedClustersSecretsKeyVaultURL changed",
			modify: func(t *testing.T, mcd *fleet.ManagementClusterDeployment) {
				mcd.Status.HostedClustersSecretsKeyVaultURL = "https://kv-other.vault.azure.net"
			},
			expectErrors: []expectedError{
				{fieldPath: "status.hostedClustersSecretsKeyVaultURL", message: "immutable"},
			},
		},
		{
			name: "hostedClustersManagedIdentitiesKeyVaultURL changed",
			modify: func(t *testing.T, mcd *fleet.ManagementClusterDeployment) {
				mcd.Status.HostedClustersManagedIdentitiesKeyVaultURL = "https://kv-other.vault.azure.net"
			},
			expectErrors: []expectedError{
				{fieldPath: "status.hostedClustersManagedIdentitiesKeyVaultURL", message: "immutable"},
			},
		},
		{
			name: "hostedClustersSecretsKeyVaultManagedIdentityClientID changed",
			modify: func(t *testing.T, mcd *fleet.ManagementClusterDeployment) {
				mcd.Status.HostedClustersSecretsKeyVaultManagedIdentityClientID = "99999999-9999-9999-9999-999999999999"
			},
			expectErrors: []expectedError{
				{fieldPath: "status.hostedClustersSecretsKeyVaultManagedIdentityClientID", message: "immutable"},
			},
		},
		{
			name: "maestroConsumerName changed",
			modify: func(t *testing.T, mcd *fleet.ManagementClusterDeployment) {
				mcd.Status.MaestroConsumerName = "different-consumer"
			},
			expectErrors: []expectedError{
				{fieldPath: "status.maestroConsumerName", message: "immutable"},
			},
		},
		{
			name: "maestroRESTAPIURL changed",
			modify: func(t *testing.T, mcd *fleet.ManagementClusterDeployment) {
				mcd.Status.MaestroRESTAPIURL = "http://different:8000"
			},
			expectErrors: []expectedError{
				{fieldPath: "status.maestroRESTAPIURL", message: "immutable"},
			},
		},
		{
			name: "maestroGRPCTarget changed",
			modify: func(t *testing.T, mcd *fleet.ManagementClusterDeployment) {
				mcd.Status.MaestroGRPCTarget = "different:8090"
			},
			expectErrors: []expectedError{
				{fieldPath: "status.maestroGRPCTarget", message: "immutable"},
			},
		},
		{
			name: "managementClusterID changed",
			modify: func(t *testing.T, mcd *fleet.ManagementClusterDeployment) {
				mcd.Status.ManagementClusterID = api.Must(azcorearm.ParseResourceID("/subscriptions/11111111-1111-1111-1111-111111111111/resourceGroups/rg/providers/Microsoft.RedHatOpenShiftManagement/hcpManagementClusters/different"))
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
			newObj := validManagementClusterDeployment(t)
			tt.modify(t, newObj)
			errs := ValidateManagementClusterDeploymentUpdate(context.Background(), newObj, oldObj)
			verifyErrorsMatch(t, tt.expectErrors, errs)
		})
	}
}
