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
	"strings"
	"testing"

	"k8s.io/utils/ptr"

	azcorearm "github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"

	"github.com/Azure/ARO-HCP/internal/api"
	"github.com/Azure/ARO-HCP/internal/api/arm"
	"github.com/Azure/ARO-HCP/internal/api/fleet"
)

func validManagementCluster(t *testing.T) *fleet.ManagementCluster {
	t.Helper()
	resourceID := api.Must(fleet.ToManagementClusterResourceID("1"))
	return &fleet.ManagementCluster{
		CosmosMetadata: arm.CosmosMetadata{
			ResourceID: resourceID,
		},
		ResourceID: resourceID,
		Spec: fleet.ManagementClusterSpec{
			SchedulingPolicy: fleet.ManagementClusterSchedulingPolicySchedulable,
		},
		Status: fleet.ManagementClusterStatus{
			AKSResourceID:                                        api.Must(azcorearm.ParseResourceID("/subscriptions/00000000-0000-0000-0000-000000000000/resourceGroups/rg/providers/Microsoft.ContainerService/managedClusters/pers-westus3-mgmt-1")),
			PublicDNSZoneResourceID:                              api.Must(azcorearm.ParseResourceID("/subscriptions/00000000-0000-0000-0000-000000000000/resourceGroups/rg/providers/Microsoft.Network/dnszones/example.com")),
			HostedClustersSecretsKeyVaultURL:                     "https://kv-cx-secrets.vault.azure.net",
			HostedClustersManagedIdentitiesKeyVaultURL:           "https://kv-cx-mi.vault.azure.net",
			HostedClustersSecretsKeyVaultManagedIdentityClientID: "12345678-1234-1234-1234-123456789012",
			ClusterServiceProvisionShardID:                       ptr.To(api.Must(api.NewInternalID("/api/aro_hcp/v1alpha1/provision_shards/aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee"))),
			MaestroConsumerName:                                  "hcp-underlay-westus3-mgmt-1",
			MaestroRESTAPIURL:                                    "http://maestro.maestro.svc.cluster.local:8000",
			MaestroGRPCTarget:                                    "maestro-grpc.maestro.svc.cluster.local:8090",
		},
	}
}

func TestValidateManagementClusterCreate(t *testing.T) {
	t.Parallel()

	type expectedError struct {
		message   string
		fieldPath string
	}

	tests := []struct {
		name         string
		modify       func(t *testing.T, mc *fleet.ManagementCluster)
		expectErrors []expectedError
	}{
		{
			name:         "valid create",
			modify:       func(t *testing.T, mc *fleet.ManagementCluster) {},
			expectErrors: nil,
		},
		// ResourceID
		{
			name: "missing resourceId",
			modify: func(t *testing.T, mc *fleet.ManagementCluster) {
				mc.ResourceID = nil
			},
			expectErrors: []expectedError{
				{fieldPath: "resourceId", message: "Required"},
			},
		},
		// Stamp identifier (resourceId.parent.name) — must be 1-3 lowercase alphanumeric
		{
			name: "stamp identifier with uppercase rejected",
			modify: func(t *testing.T, mc *fleet.ManagementCluster) {
				mc.ResourceID = api.Must(azcorearm.ParseResourceID("/providers/Microsoft.RedHatOpenShift/stamps/ABC/managementClusters/default"))
				mc.CosmosMetadata.ResourceID = mc.ResourceID
			},
			expectErrors: []expectedError{
				{fieldPath: "resourceId.parent.name", message: "must be 1-3 lowercase alphanumeric characters"},
			},
		},
		{
			name: "stamp identifier too long rejected",
			modify: func(t *testing.T, mc *fleet.ManagementCluster) {
				mc.ResourceID = api.Must(azcorearm.ParseResourceID("/providers/Microsoft.RedHatOpenShift/stamps/abcd/managementClusters/default"))
				mc.CosmosMetadata.ResourceID = mc.ResourceID
			},
			expectErrors: []expectedError{
				{fieldPath: "resourceId.parent.name", message: "must be 1-3 lowercase alphanumeric characters"},
			},
		},
		{
			name: "stamp identifier with special chars rejected",
			modify: func(t *testing.T, mc *fleet.ManagementCluster) {
				mc.ResourceID = api.Must(azcorearm.ParseResourceID("/providers/Microsoft.RedHatOpenShift/stamps/a-b/managementClusters/default"))
				mc.CosmosMetadata.ResourceID = mc.ResourceID
			},
			expectErrors: []expectedError{
				{fieldPath: "resourceId.parent.name", message: "must be 1-3 lowercase alphanumeric characters"},
			},
		},
		{
			name: "stamp identifier single char accepted",
			modify: func(t *testing.T, mc *fleet.ManagementCluster) {
				mc.ResourceID = api.Must(fleet.ToManagementClusterResourceID("a"))
				mc.CosmosMetadata.ResourceID = mc.ResourceID
			},
			expectErrors: nil,
		},
		{
			name: "stamp identifier three chars accepted",
			modify: func(t *testing.T, mc *fleet.ManagementCluster) {
				mc.ResourceID = api.Must(fleet.ToManagementClusterResourceID("ab3"))
				mc.CosmosMetadata.ResourceID = mc.ResourceID
			},
			expectErrors: nil,
		},
		// SchedulingPolicy
		{
			name: "empty schedulingPolicy rejected",
			modify: func(t *testing.T, mc *fleet.ManagementCluster) {
				mc.Spec.SchedulingPolicy = ""
			},
			expectErrors: []expectedError{
				{fieldPath: "spec.schedulingPolicy", message: "Required"},
				{fieldPath: "spec.schedulingPolicy", message: "Unsupported value"},
			},
		},
		{
			name: "invalid schedulingPolicy rejected",
			modify: func(t *testing.T, mc *fleet.ManagementCluster) {
				mc.Spec.SchedulingPolicy = "InvalidValue"
			},
			expectErrors: []expectedError{
				{fieldPath: "spec.schedulingPolicy", message: "Unsupported value"},
			},
		},
		{
			name: "Unschedulable schedulingPolicy accepted",
			modify: func(t *testing.T, mc *fleet.ManagementCluster) {
				mc.Spec.SchedulingPolicy = fleet.ManagementClusterSchedulingPolicyUnschedulable
			},
			expectErrors: nil,
		},
		// Status — all fields required
		{
			name: "empty status rejected",
			modify: func(t *testing.T, mc *fleet.ManagementCluster) {
				mc.Status = fleet.ManagementClusterStatus{}
			},
			expectErrors: []expectedError{
				{fieldPath: "status.aksResourceID", message: "Required"},
				{fieldPath: "status.publicDNSZoneResourceID", message: "Required"},
				{fieldPath: "status.hostedClustersSecretsKeyVaultURL", message: "Required"},
				{fieldPath: "status.hostedClustersManagedIdentitiesKeyVaultURL", message: "Required"},
				{fieldPath: "status.hostedClustersSecretsKeyVaultManagedIdentityClientID", message: "Required"},
				{fieldPath: "status.clusterServiceProvisionShardID", message: "Required"},
				{fieldPath: "status.maestroConsumerName", message: "Required"},
				{fieldPath: "status.maestroRESTAPIURL", message: "Required"},
				{fieldPath: "status.maestroGRPCTarget", message: "Required"},
			},
		},
		{
			name: "missing aksResourceID rejected",
			modify: func(t *testing.T, mc *fleet.ManagementCluster) {
				mc.Status.AKSResourceID = nil
			},
			expectErrors: []expectedError{
				{fieldPath: "status.aksResourceID", message: "Required"},
			},
		},
		{
			name: "invalid hostedClustersSecretsKeyVaultManagedIdentityClientID",
			modify: func(t *testing.T, mc *fleet.ManagementCluster) {
				mc.Status.HostedClustersSecretsKeyVaultManagedIdentityClientID = "not-a-uuid"
			},
			expectErrors: []expectedError{
				{fieldPath: "status.hostedClustersSecretsKeyVaultManagedIdentityClientID", message: "invalid"},
			},
		},
		{
			name: "invalid maestroGRPCTarget format rejected",
			modify: func(t *testing.T, mc *fleet.ManagementCluster) {
				mc.Status.MaestroGRPCTarget = "missing-port"
			},
			expectErrors: []expectedError{
				{fieldPath: "status.maestroGRPCTarget", message: "must be host:port"},
			},
		},
		{
			name: "invalid maestroGRPCTarget host rejected",
			modify: func(t *testing.T, mc *fleet.ManagementCluster) {
				mc.Status.MaestroGRPCTarget = "not_a_valid_host:8090"
			},
			expectErrors: []expectedError{
				{fieldPath: "status.maestroGRPCTarget", message: "invalid host"},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			mc := validManagementCluster(t)
			tt.modify(t, mc)
			errs := ValidateManagementClusterCreate(context.Background(), mc)

			if len(tt.expectErrors) == 0 {
				if len(errs) != 0 {
					t.Errorf("expected no errors, got %d: %v", len(errs), errs)
				}
				return
			}
			for _, expectedErr := range tt.expectErrors {
				found := false
				for _, err := range errs {
					if strings.Contains(err.Error(), expectedErr.message) && strings.Contains(err.Field, expectedErr.fieldPath) {
						found = true
						break
					}
				}
				if !found {
					t.Errorf("expected error containing message %q at field %q but not found in: %v", expectedErr.message, expectedErr.fieldPath, errs)
				}
			}
		})
	}
}

func TestValidateManagementClusterUpdate(t *testing.T) {
	t.Parallel()

	type expectedError struct {
		message   string
		fieldPath string
	}

	tests := []struct {
		name         string
		modify       func(t *testing.T, mc *fleet.ManagementCluster)
		expectErrors []expectedError
	}{
		{
			name:         "valid update - no changes",
			modify:       func(t *testing.T, mc *fleet.ManagementCluster) {},
			expectErrors: nil,
		},
		{
			name: "valid update - change schedulingPolicy",
			modify: func(t *testing.T, mc *fleet.ManagementCluster) {
				mc.Spec.SchedulingPolicy = fleet.ManagementClusterSchedulingPolicyUnschedulable
			},
			expectErrors: nil,
		},
		// Immutability checks
		{
			name: "aksResourceID changed",
			modify: func(t *testing.T, mc *fleet.ManagementCluster) {
				mc.Status.AKSResourceID = api.Must(azcorearm.ParseResourceID("/subscriptions/00000000-0000-0000-0000-000000000000/resourceGroups/rg/providers/Microsoft.ContainerService/managedClusters/different-name"))
			},
			expectErrors: []expectedError{
				{fieldPath: "status.aksResourceID", message: "immutable"},
			},
		},
		{
			name: "publicDNSZoneResourceID changed",
			modify: func(t *testing.T, mc *fleet.ManagementCluster) {
				mc.Status.PublicDNSZoneResourceID = api.Must(azcorearm.ParseResourceID("/subscriptions/00000000-0000-0000-0000-000000000000/resourceGroups/rg/providers/Microsoft.Network/dnszones/other.com"))
			},
			expectErrors: []expectedError{
				{fieldPath: "status.publicDNSZoneResourceID", message: "immutable"},
			},
		},
		{
			name: "hostedClustersSecretsKeyVaultURL changed",
			modify: func(t *testing.T, mc *fleet.ManagementCluster) {
				mc.Status.HostedClustersSecretsKeyVaultURL = "https://kv-other.vault.azure.net"
			},
			expectErrors: []expectedError{
				{fieldPath: "status.hostedClustersSecretsKeyVaultURL", message: "immutable"},
			},
		},
		{
			name: "hostedClustersManagedIdentitiesKeyVaultURL changed",
			modify: func(t *testing.T, mc *fleet.ManagementCluster) {
				mc.Status.HostedClustersManagedIdentitiesKeyVaultURL = "https://kv-other.vault.azure.net"
			},
			expectErrors: []expectedError{
				{fieldPath: "status.hostedClustersManagedIdentitiesKeyVaultURL", message: "immutable"},
			},
		},
		{
			name: "hostedClustersSecretsKeyVaultManagedIdentityClientID changed",
			modify: func(t *testing.T, mc *fleet.ManagementCluster) {
				mc.Status.HostedClustersSecretsKeyVaultManagedIdentityClientID = "99999999-9999-9999-9999-999999999999"
			},
			expectErrors: []expectedError{
				{fieldPath: "status.hostedClustersSecretsKeyVaultManagedIdentityClientID", message: "immutable"},
			},
		},
		{
			name: "clusterServiceProvisionShardID changed",
			modify: func(t *testing.T, mc *fleet.ManagementCluster) {
				mc.Status.ClusterServiceProvisionShardID = ptr.To(api.Must(api.NewInternalID("/api/aro_hcp/v1alpha1/provision_shards/11111111-2222-3333-4444-555555555555")))
			},
			expectErrors: []expectedError{
				{fieldPath: "status.clusterServiceProvisionShardID", message: "immutable"},
			},
		},
		{
			name: "maestroConsumerName changed",
			modify: func(t *testing.T, mc *fleet.ManagementCluster) {
				mc.Status.MaestroConsumerName = "different-consumer"
			},
			expectErrors: []expectedError{
				{fieldPath: "status.maestroConsumerName", message: "immutable"},
			},
		},
		{
			name: "maestroRESTAPIURL changed",
			modify: func(t *testing.T, mc *fleet.ManagementCluster) {
				mc.Status.MaestroRESTAPIURL = "http://different:8000"
			},
			expectErrors: []expectedError{
				{fieldPath: "status.maestroRESTAPIURL", message: "immutable"},
			},
		},
		{
			name: "maestroGRPCTarget changed",
			modify: func(t *testing.T, mc *fleet.ManagementCluster) {
				mc.Status.MaestroGRPCTarget = "different:8090"
			},
			expectErrors: []expectedError{
				{fieldPath: "status.maestroGRPCTarget", message: "immutable"},
			},
		},
		{
			name: "resourceId changed",
			modify: func(t *testing.T, mc *fleet.ManagementCluster) {
				mc.ResourceID = api.Must(fleet.ToManagementClusterResourceID("x2"))
			},
			expectErrors: []expectedError{
				{fieldPath: "resourceId", message: "immutable"},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			oldObj := validManagementCluster(t)
			newObj := validManagementCluster(t)
			tt.modify(t, newObj)
			errs := ValidateManagementClusterUpdate(context.Background(), newObj, oldObj)

			if len(tt.expectErrors) == 0 {
				if len(errs) != 0 {
					t.Errorf("expected no errors, got %d: %v", len(errs), errs)
				}
				return
			}
			for _, expectedErr := range tt.expectErrors {
				found := false
				for _, err := range errs {
					if strings.Contains(err.Error(), expectedErr.message) && strings.Contains(err.Field, expectedErr.fieldPath) {
						found = true
						break
					}
				}
				if !found {
					t.Errorf("expected error containing message %q at field %q but not found in: %v", expectedErr.message, expectedErr.fieldPath, errs)
				}
			}
		})
	}
}
