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

package lifecycle

import (
	"context"
	"testing"

	apimeta "k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	azcorearm "github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"

	fleetcontrollers "github.com/Azure/ARO-HCP/fleet/pkg/controllers/base"
	"github.com/Azure/ARO-HCP/internal/api"
	"github.com/Azure/ARO-HCP/internal/api/fleet"
	"github.com/Azure/ARO-HCP/internal/databasetesting"
)

func testManagementCluster(stampIdentifier string, conditions ...metav1.Condition) *fleet.ManagementCluster {
	resourceID := api.Must(fleet.ToManagementClusterResourceID(stampIdentifier))
	aksResourceID := api.Must(azcorearm.ParseResourceID("/subscriptions/00000000-0000-0000-0000-000000000000/resourceGroups/rg/providers/Microsoft.ContainerService/managedClusters/mc"))
	dnsResourceID := api.Must(azcorearm.ParseResourceID("/subscriptions/00000000-0000-0000-0000-000000000000/resourceGroups/dns-rg/providers/Microsoft.Network/dnszones/example.com"))
	placeholderShardID := api.Must(api.NewInternalID("/api/aro_hcp/v1alpha1/provision_shards/placeholder"))
	managementCluster := &fleet.ManagementCluster{
		CosmosMetadata: api.CosmosMetadata{ResourceID: resourceID},
		ResourceID:     resourceID,
		Spec: fleet.ManagementClusterSpec{
			SchedulingPolicy: fleet.ManagementClusterSchedulingPolicySchedulable,
		},
		Status: fleet.ManagementClusterStatus{
			AKSResourceID:                                        aksResourceID,
			PublicDNSZoneResourceID:                              dnsResourceID,
			HostedClustersSecretsKeyVaultURL:                     "https://kv-secrets.vault.azure.net",
			HostedClustersManagedIdentitiesKeyVaultURL:           "https://kv-mi.vault.azure.net",
			HostedClustersSecretsKeyVaultManagedIdentityClientID: "12345678-1234-1234-1234-123456789012",
			ClusterServiceProvisionShardID:                       &placeholderShardID,
			MaestroConsumerName:                                  "consumer-1",
			MaestroRESTAPIURL:                                    "http://maestro:8000",
			MaestroGRPCTarget:                                    "maestro:8090",
			KubeApplierCosmosContainerName:                       "kube-applier-test",
			Conditions:                                           conditions,
		},
	}
	return managementCluster
}

func testStamp(identifier string) *fleet.Stamp {
	resourceID := api.Must(fleet.ToStampResourceID(identifier))
	return &fleet.Stamp{
		CosmosMetadata: api.CosmosMetadata{ResourceID: resourceID},
		ResourceID:     resourceID,
	}
}

func conditionTrue(condType string) metav1.Condition {
	return metav1.Condition{
		Type:   condType,
		Status: metav1.ConditionTrue,
		Reason: string(fleet.ManagementClusterConditionReasonRegistered),
	}
}

func conditionFalse(condType string) metav1.Condition {
	return metav1.Condition{
		Type:   condType,
		Status: metav1.ConditionFalse,
		Reason: string(fleet.ManagementClusterConditionReasonRegistrationFailed),
	}
}

func TestSyncOnce(t *testing.T) {
	const stampID = "s1"

	csRegistered := string(fleet.ManagementClusterConditionClustersServiceRegistered)
	maestroRegistered := string(fleet.ManagementClusterConditionMaestroRegistered)
	ready := string(fleet.ManagementClusterConditionReady)

	tests := []struct {
		name           string
		resources      []any
		wantCondStatus *metav1.ConditionStatus
		wantCondReason string
		wantNoWrite    bool
	}{
		{
			name:      "MC not found: no-op",
			resources: []any{testStamp(stampID)},
		},
		{
			name: "both True: Ready=True/AllRegistered",
			resources: []any{
				testStamp(stampID),
				testManagementCluster(stampID,
					conditionTrue(csRegistered),
					conditionTrue(maestroRegistered),
				),
			},
			wantCondStatus: conditionStatusPtr(metav1.ConditionTrue),
			wantCondReason: string(fleet.ManagementClusterConditionReasonAllRegistered),
		},
		{
			name: "CS False, Maestro True: Ready=False/RegistrationIncomplete",
			resources: []any{
				testStamp(stampID),
				testManagementCluster(stampID,
					conditionFalse(csRegistered),
					conditionTrue(maestroRegistered),
				),
			},
			wantCondStatus: conditionStatusPtr(metav1.ConditionFalse),
			wantCondReason: string(fleet.ManagementClusterConditionReasonRegistrationIncomplete),
		},
		{
			name: "CS True, Maestro False: Ready=False/RegistrationIncomplete",
			resources: []any{
				testStamp(stampID),
				testManagementCluster(stampID,
					conditionTrue(csRegistered),
					conditionFalse(maestroRegistered),
				),
			},
			wantCondStatus: conditionStatusPtr(metav1.ConditionFalse),
			wantCondReason: string(fleet.ManagementClusterConditionReasonRegistrationIncomplete),
		},
		{
			name: "both False: Ready=False/RegistrationIncomplete",
			resources: []any{
				testStamp(stampID),
				testManagementCluster(stampID,
					conditionFalse(csRegistered),
					conditionFalse(maestroRegistered),
				),
			},
			wantCondStatus: conditionStatusPtr(metav1.ConditionFalse),
			wantCondReason: string(fleet.ManagementClusterConditionReasonRegistrationIncomplete),
		},
		{
			name: "CS absent, Maestro present: preserve existing Ready=True (migration safety)",
			resources: []any{
				testStamp(stampID),
				testManagementCluster(stampID,
					conditionTrue(maestroRegistered),
					metav1.Condition{
						Type:   ready,
						Status: metav1.ConditionTrue,
						Reason: string(fleet.ManagementClusterConditionReasonProvisionShardActive),
					},
				),
			},
			wantCondStatus: conditionStatusPtr(metav1.ConditionTrue),
			wantCondReason: string(fleet.ManagementClusterConditionReasonProvisionShardActive),
			wantNoWrite:    true,
		},
		{
			name: "both absent: preserve existing Ready=True (migration safety)",
			resources: []any{
				testStamp(stampID),
				testManagementCluster(stampID,
					metav1.Condition{
						Type:   ready,
						Status: metav1.ConditionTrue,
						Reason: string(fleet.ManagementClusterConditionReasonProvisionShardActive),
					},
				),
			},
			wantCondStatus: conditionStatusPtr(metav1.ConditionTrue),
			wantCondReason: string(fleet.ManagementClusterConditionReasonProvisionShardActive),
			wantNoWrite:    true,
		},
		{
			name: "both absent, no existing Ready: no Ready condition set",
			resources: []any{
				testStamp(stampID),
				testManagementCluster(stampID),
			},
		},
		{
			name: "both True, Ready already True/AllRegistered: skip write",
			resources: []any{
				testStamp(stampID),
				testManagementCluster(stampID,
					conditionTrue(csRegistered),
					conditionTrue(maestroRegistered),
					metav1.Condition{
						Type:   ready,
						Status: metav1.ConditionTrue,
						Reason: string(fleet.ManagementClusterConditionReasonAllRegistered),
					},
				),
			},
			wantCondStatus: conditionStatusPtr(metav1.ConditionTrue),
			wantCondReason: string(fleet.ManagementClusterConditionReasonAllRegistered),
			wantNoWrite:    true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx := context.Background()

			mockDB, err := databasetesting.NewMockFleetDBClientWithResources(ctx, tt.resources)
			if err != nil {
				t.Fatalf("failed to create mock DB: %v", err)
			}

			syncer := &lifecycleSyncer{
				fleetDBClient: mockDB,
			}

			key := fleetcontrollers.StampKey{StampIdentifier: stampID}
			syncErr := syncer.SyncOnce(ctx, key)
			if syncErr != nil {
				t.Fatalf("unexpected error: %v", syncErr)
			}

			managementCluster, err := mockDB.Stamps().ManagementClusters(stampID).Get(ctx, fleet.ManagementClusterResourceName)
			if err != nil {
				if tt.wantCondStatus == nil {
					return
				}
				t.Fatalf("failed to re-read MC: %v", err)
			}

			cond := apimeta.FindStatusCondition(managementCluster.Status.Conditions, ready)

			if tt.wantCondStatus == nil {
				if cond != nil {
					t.Fatalf("expected no Ready condition, got status=%v reason=%v", cond.Status, cond.Reason)
				}
				return
			}

			if cond == nil {
				t.Fatalf("expected Ready condition with status=%v, got nil", *tt.wantCondStatus)
			}
			if cond.Status != *tt.wantCondStatus {
				t.Errorf("condition status: got %v, want %v", cond.Status, *tt.wantCondStatus)
			}
			if cond.Reason != tt.wantCondReason {
				t.Errorf("condition reason: got %q, want %q", cond.Reason, tt.wantCondReason)
			}
		})
	}
}

func conditionStatusPtr(s metav1.ConditionStatus) *metav1.ConditionStatus {
	return &s
}
