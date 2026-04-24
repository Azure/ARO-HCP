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

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/utils/ptr"

	azcorearm "github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"

	"github.com/Azure/ARO-HCP/internal/api"
	"github.com/Azure/ARO-HCP/internal/api/arm"
)

func validManagementCluster(t *testing.T) *api.ManagementCluster {
	t.Helper()
	resourceID := api.Must(api.ToManagementClusterResourceID("00000000-0000-0000-0000-000000000000", "rg", "pers-westus3-mgmt-1"))
	return &api.ManagementCluster{
		CosmosMetadata: arm.CosmosMetadata{
			ResourceID: resourceID,
		},
		ResourceID: resourceID,
		Spec: api.ManagementClusterSpec{
			SchedulingPolicy: api.ManagementClusterSchedulingPolicySchedulable,
		},
		Status: api.ManagementClusterStatus{
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
			name: "invalid hostedClustersSecretsKeyVaultManagedIdentityClientID",
			modify: func(t *testing.T, mc *api.ManagementCluster) {
				mc.Status.HostedClustersSecretsKeyVaultManagedIdentityClientID = "not-a-uuid"
			},
			expectErrors: []expectedError{
				{fieldPath: "status.hostedClustersSecretsKeyVaultManagedIdentityClientID", message: "invalid"},
			},
		},
		// Ready condition cross-field validation
		{
			name: "Ready=True with complete status accepted",
			modify: func(t *testing.T, mc *api.ManagementCluster) {
				mc.Status.Conditions = []metav1.Condition{
					{Type: string(api.ManagementClusterConditionReady), Status: metav1.ConditionTrue, LastTransitionTime: metav1.Now()},
				}
			},
			expectErrors: nil,
		},
		{
			name: "Ready=False with empty status accepted",
			modify: func(t *testing.T, mc *api.ManagementCluster) {
				mc.Status = api.ManagementClusterStatus{
					Conditions: []metav1.Condition{
						{Type: string(api.ManagementClusterConditionReady), Status: metav1.ConditionFalse, LastTransitionTime: metav1.Now()},
					},
				}
			},
			expectErrors: nil,
		},
		{
			name: "Ready=True with empty status rejected",
			modify: func(t *testing.T, mc *api.ManagementCluster) {
				mc.Status = api.ManagementClusterStatus{
					Conditions: []metav1.Condition{
						{Type: string(api.ManagementClusterConditionReady), Status: metav1.ConditionTrue, LastTransitionTime: metav1.Now()},
					},
				}
			},
			expectErrors: []expectedError{
				{fieldPath: "status.conditions[Ready]", message: "status.aksResourceID"},
				{fieldPath: "status.conditions[Ready]", message: "status.publicDNSZoneResourceID"},
				{fieldPath: "status.conditions[Ready]", message: "status.hostedClustersSecretsKeyVaultURL"},
				{fieldPath: "status.conditions[Ready]", message: "status.hostedClustersManagedIdentitiesKeyVaultURL"},
				{fieldPath: "status.conditions[Ready]", message: "status.hostedClustersSecretsKeyVaultManagedIdentityClientID"},
				{fieldPath: "status.conditions[Ready]", message: "status.clusterServiceProvisionShardID"},
				{fieldPath: "status.conditions[Ready]", message: "status.maestroConsumerName"},
				{fieldPath: "status.conditions[Ready]", message: "status.maestroRESTAPIURL"},
				{fieldPath: "status.conditions[Ready]", message: "status.maestroGRPCTarget"},
			},
		},
		{
			name: "Ready=True with missing aksResourceID rejected",
			modify: func(t *testing.T, mc *api.ManagementCluster) {
				mc.Status.AKSResourceID = nil
				mc.Status.Conditions = []metav1.Condition{
					{Type: string(api.ManagementClusterConditionReady), Status: metav1.ConditionTrue, LastTransitionTime: metav1.Now()},
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
			name: "hostedClustersSecretsKeyVaultURL changed",
			modify: func(t *testing.T, mc *api.ManagementCluster) {
				mc.Status.HostedClustersSecretsKeyVaultURL = "https://kv-other.vault.azure.net"
			},
			expectErrors: []expectedError{
				{fieldPath: "status.hostedClustersSecretsKeyVaultURL", message: "immutable"},
			},
		},
		{
			name: "hostedClustersManagedIdentitiesKeyVaultURL changed",
			modify: func(t *testing.T, mc *api.ManagementCluster) {
				mc.Status.HostedClustersManagedIdentitiesKeyVaultURL = "https://kv-other.vault.azure.net"
			},
			expectErrors: []expectedError{
				{fieldPath: "status.hostedClustersManagedIdentitiesKeyVaultURL", message: "immutable"},
			},
		},
		{
			name: "hostedClustersSecretsKeyVaultManagedIdentityClientID changed",
			modify: func(t *testing.T, mc *api.ManagementCluster) {
				mc.Status.HostedClustersSecretsKeyVaultManagedIdentityClientID = "99999999-9999-9999-9999-999999999999"
			},
			expectErrors: []expectedError{
				{fieldPath: "status.hostedClustersSecretsKeyVaultManagedIdentityClientID", message: "immutable"},
			},
		},
		{
			name: "clusterServiceProvisionShardID changed",
			modify: func(t *testing.T, mc *api.ManagementCluster) {
				mc.Status.ClusterServiceProvisionShardID = ptr.To(api.Must(api.NewInternalID("/api/aro_hcp/v1alpha1/provision_shards/11111111-2222-3333-4444-555555555555")))
			},
			expectErrors: []expectedError{
				{fieldPath: "status.clusterServiceProvisionShardID", message: "immutable"},
			},
		},
		{
			name: "maestroConsumerName changed",
			modify: func(t *testing.T, mc *api.ManagementCluster) {
				mc.Status.MaestroConsumerName = "different-consumer"
			},
			expectErrors: []expectedError{
				{fieldPath: "status.maestroConsumerName", message: "immutable"},
			},
		},
		{
			name: "maestroRESTAPIURL changed",
			modify: func(t *testing.T, mc *api.ManagementCluster) {
				mc.Status.MaestroRESTAPIURL = "http://different:8000"
			},
			expectErrors: []expectedError{
				{fieldPath: "status.maestroRESTAPIURL", message: "immutable"},
			},
		},
		{
			name: "maestroGRPCTarget changed",
			modify: func(t *testing.T, mc *api.ManagementCluster) {
				mc.Status.MaestroGRPCTarget = "different:8090"
			},
			expectErrors: []expectedError{
				{fieldPath: "status.maestroGRPCTarget", message: "immutable"},
			},
		},
		{
			name: "resourceId changed",
			modify: func(t *testing.T, mc *api.ManagementCluster) {
				mc.ResourceID = api.Must(api.ToManagementClusterResourceID("11111111-1111-1111-1111-111111111111", "other-rg", "different-mgmt-1"))
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
