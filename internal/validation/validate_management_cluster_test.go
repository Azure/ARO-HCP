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
	"time"

	azcorearm "github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"

	"github.com/Azure/ARO-HCP/internal/api"
	"github.com/Azure/ARO-HCP/internal/api/arm"
)

func validManagementCluster(t *testing.T) *api.ManagementCluster {
	t.Helper()
	resourceID := api.Must(azcorearm.ParseResourceID("/providers/Microsoft.RedHatOpenShift/hcpOpenShiftManagementClusters/12345678-1234-1234-1234-123456789abc"))
	return &api.ManagementCluster{
		CosmosMetadata: arm.CosmosMetadata{
			ResourceID: resourceID,
		},
		ResourceID: resourceID,
		Spec: api.ManagementClusterSpec{
			SchedulingPolicy: api.ManagementClusterSchedulingPolicySchedulable,
		},
		Status: api.ManagementClusterStatus{
			AKSResourceID:                            api.Must(azcorearm.ParseResourceID("/subscriptions/00000000-0000-0000-0000-000000000000/resourceGroups/rg/providers/Microsoft.ContainerService/managedClusters/pers-westus3-mgmt-1")),
			PublicDNSZoneResourceID:                  api.Must(azcorearm.ParseResourceID("/subscriptions/00000000-0000-0000-0000-000000000000/resourceGroups/rg/providers/Microsoft.Network/dnszones/example.com")),
			CXSecretsKeyVaultURL:                     "https://kv-cx-secrets.vault.azure.net",
			CXManagedIdentitiesKeyVaultURL:           "https://kv-cx-mi.vault.azure.net",
			CXSecretsKeyVaultManagedIdentityClientID: "12345678-1234-1234-1234-123456789012",
			CSProvisionShardID:                       "/api/clusters_mgmt/v1/provision_shards/shard-1",
			MaestroConfig: api.MaestroConfig{
				ConsumerName: "hcp-underlay-westus3-mgmt-1",
				RESTAPIConfig: api.MaestroRESTAPIConfig{
					URL: "http://maestro.maestro.svc.cluster.local:8000",
				},
				GRPCAPIConfig: api.MaestroGRPCAPIConfig{
					URL: "maestro-grpc.maestro.svc.cluster.local:8090",
				},
			},
		},
	}
}

func TestValidateManagementClusterCreate(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name         string
		modify       func(t *testing.T, mc *api.ManagementCluster)
		expectErrors []expectedError
	}{
		{
			name:         "valid create",
			modify:       func(t *testing.T, mc *api.ManagementCluster) {},
			expectErrors: nil,
		},
		// ResourceID
		{
			name: "missing resourceId",
			modify: func(t *testing.T, mc *api.ManagementCluster) {
				mc.ResourceID = nil
			},
			expectErrors: []expectedError{
				{fieldPath: "resourceId", message: "Required"},
			},
		},
		{
			name: "non-UUID resourceId name",
			modify: func(t *testing.T, mc *api.ManagementCluster) {
				mc.ResourceID = api.Must(azcorearm.ParseResourceID("/providers/Microsoft.RedHatOpenShift/hcpOpenShiftManagementClusters/not-a-uuid"))
			},
			expectErrors: []expectedError{
				{fieldPath: "resourceId.name", message: "invalid"},
			},
		},
		// SchedulingPolicy
		{
			name: "empty schedulingPolicy rejected",
			modify: func(t *testing.T, mc *api.ManagementCluster) {
				mc.Spec.SchedulingPolicy = ""
			},
			expectErrors: []expectedError{
				{fieldPath: "spec.schedulingPolicy", message: "Required"},
				{fieldPath: "spec.schedulingPolicy", message: "Unsupported value"},
			},
		},
		{
			name: "invalid schedulingPolicy rejected",
			modify: func(t *testing.T, mc *api.ManagementCluster) {
				mc.Spec.SchedulingPolicy = "InvalidValue"
			},
			expectErrors: []expectedError{
				{fieldPath: "spec.schedulingPolicy", message: "Unsupported value"},
			},
		},
		{
			name: "Unschedulable schedulingPolicy accepted",
			modify: func(t *testing.T, mc *api.ManagementCluster) {
				mc.Spec.SchedulingPolicy = api.ManagementClusterSchedulingPolicyUnschedulable
			},
			expectErrors: nil,
		},
		// Status — entirely optional on create
		{
			name: "empty status accepted",
			modify: func(t *testing.T, mc *api.ManagementCluster) {
				mc.Status = api.ManagementClusterStatus{}
			},
			expectErrors: nil,
		},
		{
			name: "invalid cxSecretsKeyVaultManagedIdentityClientID",
			modify: func(t *testing.T, mc *api.ManagementCluster) {
				mc.Status.CXSecretsKeyVaultManagedIdentityClientID = "not-a-uuid"
			},
			expectErrors: []expectedError{
				{fieldPath: "status.cxSecretsKeyVaultManagedIdentityClientID", message: "invalid"},
			},
		},
		// Ready condition cross-field validation
		{
			name: "Ready=True with complete status accepted",
			modify: func(t *testing.T, mc *api.ManagementCluster) {
				mc.Status.Conditions = []api.Condition{
					{Type: string(api.ManagementClusterConditionReady), Status: api.ConditionTrue, LastTransitionTime: time.Now()},
				}
			},
			expectErrors: nil,
		},
		{
			name: "Ready=False with empty status accepted",
			modify: func(t *testing.T, mc *api.ManagementCluster) {
				mc.Status = api.ManagementClusterStatus{
					Conditions: []api.Condition{
						{Type: string(api.ManagementClusterConditionReady), Status: api.ConditionFalse, LastTransitionTime: time.Now()},
					},
				}
			},
			expectErrors: nil,
		},
		{
			name: "Ready=True with empty status rejected",
			modify: func(t *testing.T, mc *api.ManagementCluster) {
				mc.Status = api.ManagementClusterStatus{
					Conditions: []api.Condition{
						{Type: string(api.ManagementClusterConditionReady), Status: api.ConditionTrue, LastTransitionTime: time.Now()},
					},
				}
			},
			expectErrors: []expectedError{
				{fieldPath: "status.conditions[Ready]", message: "status.aksResourceID"},
				{fieldPath: "status.conditions[Ready]", message: "status.publicDNSZoneResourceID"},
				{fieldPath: "status.conditions[Ready]", message: "status.cxSecretsKeyVaultURL"},
				{fieldPath: "status.conditions[Ready]", message: "status.cxManagedIdentitiesKeyVaultURL"},
				{fieldPath: "status.conditions[Ready]", message: "status.cxSecretsKeyVaultManagedIdentityClientID"},
				{fieldPath: "status.conditions[Ready]", message: "status.csProvisionShardID"},
				{fieldPath: "status.conditions[Ready]", message: "status.maestroConfig.consumerName"},
				{fieldPath: "status.conditions[Ready]", message: "status.maestroConfig.restAPIConfig.url"},
				{fieldPath: "status.conditions[Ready]", message: "status.maestroConfig.grpcAPIConfig.url"},
			},
		},
		{
			name: "Ready=True with missing aksResourceID rejected",
			modify: func(t *testing.T, mc *api.ManagementCluster) {
				mc.Status.AKSResourceID = nil
				mc.Status.Conditions = []api.Condition{
					{Type: string(api.ManagementClusterConditionReady), Status: api.ConditionTrue, LastTransitionTime: time.Now()},
				}
			},
			expectErrors: []expectedError{
				{fieldPath: "status.conditions[Ready]", message: "status.aksResourceID"},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			mc := validManagementCluster(t)
			tt.modify(t, mc)
			errs := ValidateManagementClusterCreate(context.Background(), mc)
			verifyErrorsMatch(t, tt.expectErrors, errs)
		})
	}
}

func TestValidateManagementClusterUpdate(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name         string
		modify       func(t *testing.T, mc *api.ManagementCluster)
		expectErrors []expectedError
	}{
		{
			name:         "valid update - no changes",
			modify:       func(t *testing.T, mc *api.ManagementCluster) {},
			expectErrors: nil,
		},
		{
			name: "valid update - change schedulingPolicy",
			modify: func(t *testing.T, mc *api.ManagementCluster) {
				mc.Spec.SchedulingPolicy = api.ManagementClusterSchedulingPolicyUnschedulable
			},
			expectErrors: nil,
		},
		// Immutability checks
		{
			name: "aksResourceID changed",
			modify: func(t *testing.T, mc *api.ManagementCluster) {
				mc.Status.AKSResourceID = api.Must(azcorearm.ParseResourceID("/subscriptions/00000000-0000-0000-0000-000000000000/resourceGroups/rg/providers/Microsoft.ContainerService/managedClusters/different-name"))
			},
			expectErrors: []expectedError{
				{fieldPath: "status.aksResourceID", message: "immutable"},
			},
		},
		{
			name: "publicDNSZoneResourceID changed",
			modify: func(t *testing.T, mc *api.ManagementCluster) {
				mc.Status.PublicDNSZoneResourceID = api.Must(azcorearm.ParseResourceID("/subscriptions/00000000-0000-0000-0000-000000000000/resourceGroups/rg/providers/Microsoft.Network/dnszones/other.com"))
			},
			expectErrors: []expectedError{
				{fieldPath: "status.publicDNSZoneResourceID", message: "immutable"},
			},
		},
		{
			name: "cxSecretsKeyVaultURL changed",
			modify: func(t *testing.T, mc *api.ManagementCluster) {
				mc.Status.CXSecretsKeyVaultURL = "https://kv-other.vault.azure.net"
			},
			expectErrors: []expectedError{
				{fieldPath: "status.cxSecretsKeyVaultURL", message: "immutable"},
			},
		},
		{
			name: "cxManagedIdentitiesKeyVaultURL changed",
			modify: func(t *testing.T, mc *api.ManagementCluster) {
				mc.Status.CXManagedIdentitiesKeyVaultURL = "https://kv-other.vault.azure.net"
			},
			expectErrors: []expectedError{
				{fieldPath: "status.cxManagedIdentitiesKeyVaultURL", message: "immutable"},
			},
		},
		{
			name: "cxSecretsKeyVaultManagedIdentityClientID changed",
			modify: func(t *testing.T, mc *api.ManagementCluster) {
				mc.Status.CXSecretsKeyVaultManagedIdentityClientID = "99999999-9999-9999-9999-999999999999"
			},
			expectErrors: []expectedError{
				{fieldPath: "status.cxSecretsKeyVaultManagedIdentityClientID", message: "immutable"},
			},
		},
		{
			name: "csProvisionShardID changed",
			modify: func(t *testing.T, mc *api.ManagementCluster) {
				mc.Status.CSProvisionShardID = "/api/clusters_mgmt/v1/provision_shards/shard-2"
			},
			expectErrors: []expectedError{
				{fieldPath: "status.csProvisionShardID", message: "immutable"},
			},
		},
		{
			name: "maestroConfig consumerName changed",
			modify: func(t *testing.T, mc *api.ManagementCluster) {
				mc.Status.MaestroConfig.ConsumerName = "different-consumer"
			},
			expectErrors: []expectedError{
				{fieldPath: "status.maestroConfig.consumerName", message: "immutable"},
			},
		},
		{
			name: "maestroConfig REST API URL changed",
			modify: func(t *testing.T, mc *api.ManagementCluster) {
				mc.Status.MaestroConfig.RESTAPIConfig.URL = "http://different:8000"
			},
			expectErrors: []expectedError{
				{fieldPath: "status.maestroConfig.restAPIConfig.url", message: "immutable"},
			},
		},
		{
			name: "maestroConfig GRPC API URL changed",
			modify: func(t *testing.T, mc *api.ManagementCluster) {
				mc.Status.MaestroConfig.GRPCAPIConfig.URL = "different:8090"
			},
			expectErrors: []expectedError{
				{fieldPath: "status.maestroConfig.grpcAPIConfig.url", message: "immutable"},
			},
		},
		{
			name: "resourceId changed",
			modify: func(t *testing.T, mc *api.ManagementCluster) {
				mc.ResourceID = api.Must(azcorearm.ParseResourceID("/providers/Microsoft.RedHatOpenShift/hcpOpenShiftManagementClusters/aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee"))
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
			verifyErrorsMatch(t, tt.expectErrors, errs)
		})
	}
}
