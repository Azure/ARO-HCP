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

package clustersserviceregistration

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"go.uber.org/mock/gomock"

	apimeta "k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	azcorearm "github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"

	arohcpv1alpha1 "github.com/openshift-online/ocm-api-model/clientapi/arohcp/v1alpha1"
	ocmerrors "github.com/openshift-online/ocm-sdk-go/errors"

	fleetcontrollers "github.com/Azure/ARO-HCP/fleet/pkg/controllers/base"
	"github.com/Azure/ARO-HCP/internal/api"
	"github.com/Azure/ARO-HCP/internal/api/fleet"
	"github.com/Azure/ARO-HCP/internal/databasetesting"
	"github.com/Azure/ARO-HCP/internal/ocm"
)

const testAKSResourceID = "/subscriptions/00000000-0000-0000-0000-000000000000/resourceGroups/rg/providers/Microsoft.ContainerService/managedClusters/mc"

func testStamp(identifier string, approved bool) *fleet.Stamp {
	resourceID := api.Must(fleet.ToStampResourceID(identifier))
	stamp := &fleet.Stamp{
		CosmosMetadata: api.CosmosMetadata{ResourceID: resourceID},
		ResourceID:     resourceID,
	}
	if approved {
		apimeta.SetStatusCondition(&stamp.Status.Conditions, metav1.Condition{
			Type:   string(fleet.StampConditionApproved),
			Status: metav1.ConditionTrue,
			Reason: string(fleet.StampConditionReasonAutoApproved),
		})
	}
	return stamp
}

func testManagementCluster(stampIdentifier string) *fleet.ManagementCluster {
	resourceID := api.Must(fleet.ToManagementClusterResourceID(stampIdentifier))
	aksResourceID := api.Must(azcorearm.ParseResourceID(testAKSResourceID))
	dnsZoneResourceID := api.Must(azcorearm.ParseResourceID("/subscriptions/00000000-0000-0000-0000-000000000000/resourceGroups/dns-rg/providers/Microsoft.Network/dnszones/example.com"))
	placeholderShardID, _ := api.NewInternalID("/api/aro_hcp/v1alpha1/provision_shards/placeholder")
	return &fleet.ManagementCluster{
		CosmosMetadata: api.CosmosMetadata{ResourceID: resourceID},
		ResourceID:     resourceID,
		Spec: fleet.ManagementClusterSpec{
			SchedulingPolicy: fleet.ManagementClusterSchedulingPolicySchedulable,
		},
		Status: fleet.ManagementClusterStatus{
			AKSResourceID:                                        aksResourceID,
			PublicDNSZoneResourceID:                              dnsZoneResourceID,
			HostedClustersSecretsKeyVaultURL:                     "https://kv-secrets.vault.azure.net",
			HostedClustersManagedIdentitiesKeyVaultURL:           "https://kv-mi.vault.azure.net",
			HostedClustersSecretsKeyVaultManagedIdentityClientID: "12345678-1234-1234-1234-123456789012",
			ClusterServiceProvisionShardID:                       &placeholderShardID,
			MaestroConsumerName:                                  "consumer-1",
			MaestroRESTAPIURL:                                    "http://maestro:8000",
			MaestroGRPCTarget:                                    "maestro:8090",
			KubeApplierCosmosContainerName:                       "kube-applier-test",
		},
	}
}

func shardWithAKSID(href, aksResourceID string) *arohcpv1alpha1.ProvisionShard {
	shard, _ := arohcpv1alpha1.NewProvisionShard().
		HREF(href).
		AzureShard(arohcpv1alpha1.NewAzureShard().
			AksManagementClusterResourceId(aksResourceID),
		).
		Build()
	return shard
}

func TestReconcileProvisionShard(t *testing.T) {
	const (
		storedHREF = "/api/aro_hcp/v1alpha1/provision_shards/placeholder"
		foundHREF  = "/api/aro_hcp/v1alpha1/provision_shards/found-by-aks"
		newHREF    = "/api/aro_hcp/v1alpha1/provision_shards/new"
	)

	storedID, _ := api.NewInternalID(storedHREF)
	foundID, _ := api.NewInternalID(foundHREF)
	notFound, _ := ocmerrors.NewError().Status(404).Build()
	serverError, _ := ocmerrors.NewError().Status(500).Build()

	tests := []struct {
		name              string
		managementCluster *fleet.ManagementCluster
		setupCS           func(ctrl *gomock.Controller) ProvisionShardClient
		wantHREF          string
		wantErrContains   string
	}{
		{
			name:              "stored ID exists, Get OK, Update OK",
			managementCluster: testManagementCluster("s1"),
			setupCS: func(ctrl *gomock.Controller) ProvisionShardClient {
				mock := ocm.NewMockClusterServiceClientSpec(ctrl)
				mock.EXPECT().GetProvisionShard(gomock.Any(), storedID).Return(api.Must(arohcpv1alpha1.NewProvisionShard().HREF(storedHREF).Build()), nil)
				mock.EXPECT().UpdateProvisionShard(gomock.Any(), storedID, gomock.Any()).Return(api.Must(arohcpv1alpha1.NewProvisionShard().HREF(storedHREF).Build()), nil)
				return mock
			},
			wantHREF: storedHREF,
		},
		{
			name:              "stored ID exists, Get OK, Update fails",
			managementCluster: testManagementCluster("s1"),
			setupCS: func(ctrl *gomock.Controller) ProvisionShardClient {
				mock := ocm.NewMockClusterServiceClientSpec(ctrl)
				mock.EXPECT().GetProvisionShard(gomock.Any(), storedID).Return(api.Must(arohcpv1alpha1.NewProvisionShard().HREF(storedHREF).Build()), nil)
				mock.EXPECT().UpdateProvisionShard(gomock.Any(), storedID, gomock.Any()).Return(nil, fmt.Errorf("cs unavailable"))
				return mock
			},
			wantErrContains: "updating provision shard: cs unavailable",
		},
		{
			name:              "stored ID exists, Get returns non-OCM error",
			managementCluster: testManagementCluster("s1"),
			setupCS: func(ctrl *gomock.Controller) ProvisionShardClient {
				mock := ocm.NewMockClusterServiceClientSpec(ctrl)
				mock.EXPECT().GetProvisionShard(gomock.Any(), storedID).Return(nil, fmt.Errorf("network error"))
				return mock
			},
			wantErrContains: "getting provision shard: network error",
		},
		{
			name:              "stored ID exists, Get returns OCM 500",
			managementCluster: testManagementCluster("s1"),
			setupCS: func(ctrl *gomock.Controller) ProvisionShardClient {
				mock := ocm.NewMockClusterServiceClientSpec(ctrl)
				mock.EXPECT().GetProvisionShard(gomock.Any(), storedID).Return(nil, serverError)
				return mock
			},
			wantErrContains: "getting provision shard: status is 500",
		},
		{
			name:              "stored ID exists, Get 404, list finds match, Update OK",
			managementCluster: testManagementCluster("s1"),
			setupCS: func(ctrl *gomock.Controller) ProvisionShardClient {
				mock := ocm.NewMockClusterServiceClientSpec(ctrl)
				mock.EXPECT().GetProvisionShard(gomock.Any(), storedID).Return(nil, notFound)
				mock.EXPECT().ListProvisionShards().Return(
					ocm.NewSimpleProvisionShardListIterator([]*arohcpv1alpha1.ProvisionShard{
						shardWithAKSID(foundHREF, testAKSResourceID),
					}, nil),
				)
				mock.EXPECT().UpdateProvisionShard(gomock.Any(), foundID, gomock.Any()).Return(api.Must(arohcpv1alpha1.NewProvisionShard().HREF(foundHREF).Build()), nil)
				return mock
			},
			wantHREF: foundHREF,
		},
		{
			name:              "stored ID exists, Get 404, list finds match, Update fails",
			managementCluster: testManagementCluster("s1"),
			setupCS: func(ctrl *gomock.Controller) ProvisionShardClient {
				mock := ocm.NewMockClusterServiceClientSpec(ctrl)
				mock.EXPECT().GetProvisionShard(gomock.Any(), storedID).Return(nil, notFound)
				mock.EXPECT().ListProvisionShards().Return(
					ocm.NewSimpleProvisionShardListIterator([]*arohcpv1alpha1.ProvisionShard{
						shardWithAKSID(foundHREF, testAKSResourceID),
					}, nil),
				)
				mock.EXPECT().UpdateProvisionShard(gomock.Any(), foundID, gomock.Any()).Return(nil, fmt.Errorf("update failed"))
				return mock
			},
			wantErrContains: "updating provision shard: update failed",
		},
		{
			name:              "stored ID exists, Get 404, list error",
			managementCluster: testManagementCluster("s1"),
			setupCS: func(ctrl *gomock.Controller) ProvisionShardClient {
				mock := ocm.NewMockClusterServiceClientSpec(ctrl)
				mock.EXPECT().GetProvisionShard(gomock.Any(), storedID).Return(nil, notFound)
				mock.EXPECT().ListProvisionShards().Return(
					ocm.NewSimpleProvisionShardListIterator(nil, fmt.Errorf("list failed")),
				)
				return mock
			},
			wantErrContains: "searching for provision shard by AKS resource ID: list failed",
		},
		{
			name:              "stored ID exists, Get 404, no match, Post OK followed by status update",
			managementCluster: testManagementCluster("s1"),
			setupCS: func(ctrl *gomock.Controller) ProvisionShardClient {
				newID, _ := api.NewInternalID(newHREF)
				mock := ocm.NewMockClusterServiceClientSpec(ctrl)
				mock.EXPECT().GetProvisionShard(gomock.Any(), storedID).Return(nil, notFound)
				mock.EXPECT().ListProvisionShards().Return(
					ocm.NewSimpleProvisionShardListIterator(nil, nil),
				)
				mock.EXPECT().PostProvisionShard(gomock.Any(), gomock.Any()).Return(api.Must(arohcpv1alpha1.NewProvisionShard().HREF(newHREF).Build()), nil)
				mock.EXPECT().UpdateProvisionShard(gomock.Any(), newID, gomock.Any()).Return(api.Must(arohcpv1alpha1.NewProvisionShard().HREF(newHREF).Build()), nil)
				return mock
			},
			wantHREF: newHREF,
		},
		{
			name:              "stored ID exists, Get 404, no match, Post fails",
			managementCluster: testManagementCluster("s1"),
			setupCS: func(ctrl *gomock.Controller) ProvisionShardClient {
				mock := ocm.NewMockClusterServiceClientSpec(ctrl)
				mock.EXPECT().GetProvisionShard(gomock.Any(), storedID).Return(nil, notFound)
				mock.EXPECT().ListProvisionShards().Return(
					ocm.NewSimpleProvisionShardListIterator(nil, nil),
				)
				mock.EXPECT().PostProvisionShard(gomock.Any(), gomock.Any()).Return(nil, fmt.Errorf("post failed"))
				return mock
			},
			wantErrContains: "creating provision shard: post failed",
		},
		{
			name: "no stored ID, list finds match, Update OK",
			managementCluster: func() *fleet.ManagementCluster {
				managementCluster := testManagementCluster("s1")
				managementCluster.Status.ClusterServiceProvisionShardID = nil
				return managementCluster
			}(),
			setupCS: func(ctrl *gomock.Controller) ProvisionShardClient {
				mock := ocm.NewMockClusterServiceClientSpec(ctrl)
				mock.EXPECT().ListProvisionShards().Return(
					ocm.NewSimpleProvisionShardListIterator([]*arohcpv1alpha1.ProvisionShard{
						shardWithAKSID(foundHREF, testAKSResourceID),
					}, nil),
				)
				mock.EXPECT().UpdateProvisionShard(gomock.Any(), foundID, gomock.Any()).Return(api.Must(arohcpv1alpha1.NewProvisionShard().HREF(foundHREF).Build()), nil)
				return mock
			},
			wantHREF: foundHREF,
		},
		{
			name:              "stored ID exists, Get 404, no match, Post OK, status Update fails",
			managementCluster: testManagementCluster("s1"),
			setupCS: func(ctrl *gomock.Controller) ProvisionShardClient {
				newID, _ := api.NewInternalID(newHREF)
				mock := ocm.NewMockClusterServiceClientSpec(ctrl)
				mock.EXPECT().GetProvisionShard(gomock.Any(), storedID).Return(nil, notFound)
				mock.EXPECT().ListProvisionShards().Return(
					ocm.NewSimpleProvisionShardListIterator(nil, nil),
				)
				mock.EXPECT().PostProvisionShard(gomock.Any(), gomock.Any()).Return(api.Must(arohcpv1alpha1.NewProvisionShard().HREF(newHREF).Build()), nil)
				mock.EXPECT().UpdateProvisionShard(gomock.Any(), newID, gomock.Any()).Return(nil, fmt.Errorf("status update failed"))
				return mock
			},
			wantErrContains: "setting provision shard status after create: status update failed",
		},
		{
			name: "no stored ID, no match, Post OK followed by status update",
			managementCluster: func() *fleet.ManagementCluster {
				managementCluster := testManagementCluster("s1")
				managementCluster.Status.ClusterServiceProvisionShardID = nil
				return managementCluster
			}(),
			setupCS: func(ctrl *gomock.Controller) ProvisionShardClient {
				newID, _ := api.NewInternalID(newHREF)
				mock := ocm.NewMockClusterServiceClientSpec(ctrl)
				mock.EXPECT().ListProvisionShards().Return(
					ocm.NewSimpleProvisionShardListIterator(nil, nil),
				)
				mock.EXPECT().PostProvisionShard(gomock.Any(), gomock.Any()).Return(api.Must(arohcpv1alpha1.NewProvisionShard().HREF(newHREF).Build()), nil)
				mock.EXPECT().UpdateProvisionShard(gomock.Any(), newID, gomock.Any()).Return(api.Must(arohcpv1alpha1.NewProvisionShard().HREF(newHREF).Build()), nil)
				return mock
			},
			wantHREF: newHREF,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx := context.Background()
			ctrl := gomock.NewController(t)

			syncer := &clustersServiceRegistrationSyncer{
				clustersServiceClient: tt.setupCS(ctrl),
				region:                "westus3",
			}

			shardID, err := syncer.reconcileProvisionShard(ctx, tt.managementCluster)

			if len(tt.wantErrContains) > 0 {
				if err == nil {
					t.Fatalf("expected error containing %q, got nil", tt.wantErrContains)
				}
				if !strings.Contains(err.Error(), tt.wantErrContains) {
					t.Fatalf("error %q does not contain %q", err.Error(), tt.wantErrContains)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if shardID == nil {
				t.Fatal("expected non-nil shard ID")
			}
			if shardID.Path() != tt.wantHREF {
				t.Errorf("shard HREF: got %q, want %q", shardID.Path(), tt.wantHREF)
			}
		})
	}
}

func TestSyncOnce(t *testing.T) {
	const stampID = "s1"

	storedHREF := "/api/aro_hcp/v1alpha1/provision_shards/placeholder"
	storedID, _ := api.NewInternalID(storedHREF)

	tests := []struct {
		name                   string
		stamp                  *fleet.Stamp
		managementCluster      *fleet.ManagementCluster
		excludeStampFromLister bool
		setupCS                func(ctrl *gomock.Controller) ProvisionShardClient
		wantErr                bool
		wantCondition          string
		wantCondStatus         metav1.ConditionStatus
		wantCondReason         string
	}{
		{
			name:              "MC not found in DB: no-op",
			stamp:             testStamp(stampID, true),
			managementCluster: nil,
			setupCS: func(ctrl *gomock.Controller) ProvisionShardClient {
				return ocm.NewMockClusterServiceClientSpec(ctrl)
			},
		},
		{
			name:                   "stamp lister error: returns error",
			stamp:                  testStamp(stampID, true),
			managementCluster:      testManagementCluster(stampID),
			excludeStampFromLister: true,
			setupCS: func(ctrl *gomock.Controller) ProvisionShardClient {
				return ocm.NewMockClusterServiceClientSpec(ctrl)
			},
			wantErr: true,
		},
		{
			name:              "stamp not approved: sets condition false",
			stamp:             testStamp(stampID, false),
			managementCluster: testManagementCluster(stampID),
			setupCS: func(ctrl *gomock.Controller) ProvisionShardClient {
				return ocm.NewMockClusterServiceClientSpec(ctrl)
			},
			wantCondition:  string(fleet.ManagementClusterConditionClustersServiceRegistered),
			wantCondStatus: metav1.ConditionFalse,
			wantCondReason: string(fleet.ManagementClusterConditionReasonRegistrationFailed),
		},
		{
			name:              "first reconcile error: sets failure condition",
			stamp:             testStamp(stampID, true),
			managementCluster: testManagementCluster(stampID),
			setupCS: func(ctrl *gomock.Controller) ProvisionShardClient {
				mock := ocm.NewMockClusterServiceClientSpec(ctrl)
				mock.EXPECT().GetProvisionShard(gomock.Any(), storedID).Return(nil, fmt.Errorf("cs unavailable"))
				return mock
			},
			wantErr:        true,
			wantCondition:  string(fleet.ManagementClusterConditionClustersServiceRegistered),
			wantCondStatus: metav1.ConditionFalse,
			wantCondReason: string(fleet.ManagementClusterConditionReasonRegistrationFailed),
		},
		{
			name:  "transient error with existing True condition: stays True with CheckFailed",
			stamp: testStamp(stampID, true),
			managementCluster: func() *fleet.ManagementCluster {
				managementCluster := testManagementCluster(stampID)
				apimeta.SetStatusCondition(&managementCluster.Status.Conditions, metav1.Condition{
					Type:   string(fleet.ManagementClusterConditionClustersServiceRegistered),
					Status: metav1.ConditionTrue,
					Reason: string(fleet.ManagementClusterConditionReasonRegistered),
				})
				return managementCluster
			}(),
			setupCS: func(ctrl *gomock.Controller) ProvisionShardClient {
				mock := ocm.NewMockClusterServiceClientSpec(ctrl)
				mock.EXPECT().GetProvisionShard(gomock.Any(), storedID).Return(nil, fmt.Errorf("cs unavailable"))
				return mock
			},
			wantErr:        true,
			wantCondition:  string(fleet.ManagementClusterConditionClustersServiceRegistered),
			wantCondStatus: metav1.ConditionTrue,
			wantCondReason: string(fleet.ManagementClusterConditionReasonRegistrationCheckFailed),
		},
		{
			name:              "happy path: sets success condition with active reason",
			stamp:             testStamp(stampID, true),
			managementCluster: testManagementCluster(stampID),
			setupCS: func(ctrl *gomock.Controller) ProvisionShardClient {
				mock := ocm.NewMockClusterServiceClientSpec(ctrl)
				mock.EXPECT().GetProvisionShard(gomock.Any(), storedID).Return(api.Must(arohcpv1alpha1.NewProvisionShard().HREF(storedHREF).Build()), nil)
				mock.EXPECT().UpdateProvisionShard(gomock.Any(), storedID, gomock.Any()).Return(api.Must(arohcpv1alpha1.NewProvisionShard().HREF(storedHREF).Build()), nil)
				return mock
			},
			wantCondition:  string(fleet.ManagementClusterConditionClustersServiceRegistered),
			wantCondStatus: metav1.ConditionTrue,
			wantCondReason: string(fleet.ManagementClusterConditionReasonRegistered),
		},
		{
			name:              "stored ID 404, recreate: sets condition, keeps original shard ID",
			stamp:             testStamp(stampID, true),
			managementCluster: testManagementCluster(stampID),
			setupCS: func(ctrl *gomock.Controller) ProvisionShardClient {
				notFound, _ := ocmerrors.NewError().Status(404).Build()
				newHREF := "/api/aro_hcp/v1alpha1/provision_shards/new"
				newID, _ := api.NewInternalID(newHREF)
				mock := ocm.NewMockClusterServiceClientSpec(ctrl)
				mock.EXPECT().GetProvisionShard(gomock.Any(), storedID).Return(nil, notFound)
				mock.EXPECT().ListProvisionShards().Return(
					ocm.NewSimpleProvisionShardListIterator(nil, nil),
				)
				mock.EXPECT().PostProvisionShard(gomock.Any(), gomock.Any()).Return(api.Must(arohcpv1alpha1.NewProvisionShard().HREF(newHREF).Build()), nil)
				mock.EXPECT().UpdateProvisionShard(gomock.Any(), newID, gomock.Any()).Return(api.Must(arohcpv1alpha1.NewProvisionShard().HREF(newHREF).Build()), nil)
				return mock
			},
			wantCondition:  string(fleet.ManagementClusterConditionClustersServiceRegistered),
			wantCondStatus: metav1.ConditionTrue,
			wantCondReason: string(fleet.ManagementClusterConditionReasonRegistered),
		},
		{
			name:              "create OK but status update fails: condition false",
			stamp:             testStamp(stampID, true),
			managementCluster: testManagementCluster(stampID),
			setupCS: func(ctrl *gomock.Controller) ProvisionShardClient {
				notFound, _ := ocmerrors.NewError().Status(404).Build()
				newHREF := "/api/aro_hcp/v1alpha1/provision_shards/new"
				newID, _ := api.NewInternalID(newHREF)
				mock := ocm.NewMockClusterServiceClientSpec(ctrl)
				mock.EXPECT().GetProvisionShard(gomock.Any(), storedID).Return(nil, notFound)
				mock.EXPECT().ListProvisionShards().Return(
					ocm.NewSimpleProvisionShardListIterator(nil, nil),
				)
				mock.EXPECT().PostProvisionShard(gomock.Any(), gomock.Any()).Return(api.Must(arohcpv1alpha1.NewProvisionShard().HREF(newHREF).Build()), nil)
				mock.EXPECT().UpdateProvisionShard(gomock.Any(), newID, gomock.Any()).Return(nil, fmt.Errorf("status update failed"))
				return mock
			},
			wantErr:        true,
			wantCondition:  string(fleet.ManagementClusterConditionClustersServiceRegistered),
			wantCondStatus: metav1.ConditionFalse,
			wantCondReason: string(fleet.ManagementClusterConditionReasonRegistrationFailed),
		},
		{
			name:  "unschedulable MC: still sets Registered condition",
			stamp: testStamp(stampID, true),
			managementCluster: func() *fleet.ManagementCluster {
				managementCluster := testManagementCluster(stampID)
				managementCluster.Spec.SchedulingPolicy = fleet.ManagementClusterSchedulingPolicyUnschedulable
				return managementCluster
			}(),
			setupCS: func(ctrl *gomock.Controller) ProvisionShardClient {
				mock := ocm.NewMockClusterServiceClientSpec(ctrl)
				mock.EXPECT().GetProvisionShard(gomock.Any(), storedID).Return(api.Must(arohcpv1alpha1.NewProvisionShard().HREF(storedHREF).Build()), nil)
				mock.EXPECT().UpdateProvisionShard(gomock.Any(), storedID, gomock.Any()).Return(api.Must(arohcpv1alpha1.NewProvisionShard().HREF(storedHREF).Build()), nil)
				return mock
			},
			wantCondition:  string(fleet.ManagementClusterConditionClustersServiceRegistered),
			wantCondStatus: metav1.ConditionTrue,
			wantCondReason: string(fleet.ManagementClusterConditionReasonRegistered),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx := context.Background()
			ctrl := gomock.NewController(t)

			resources := []any{tt.stamp}
			if tt.managementCluster != nil {
				resources = append(resources, tt.managementCluster)
			}
			mockDB, err := databasetesting.NewMockFleetDBClientWithResources(ctx, resources)
			if err != nil {
				t.Fatalf("failed to create mock DB: %v", err)
			}

			stamps := map[string]*fleet.Stamp{}
			if !tt.excludeStampFromLister {
				stamps[tt.stamp.GetStampIdentifier()] = tt.stamp
			}
			stampLister := &fakeStampLister{stamps: stamps}

			syncer := &clustersServiceRegistrationSyncer{
				fleetDBClient:         mockDB,
				clustersServiceClient: tt.setupCS(ctrl),
				stampLister:           stampLister,
				region:                "westus3",
			}

			key := fleetcontrollers.StampKey{StampIdentifier: stampID}
			syncErr := syncer.SyncOnce(ctx, key)

			if tt.wantErr {
				if syncErr == nil {
					t.Fatal("expected error, got nil")
				}
			} else {
				if syncErr != nil {
					t.Fatalf("unexpected error: %v", syncErr)
				}
			}

			if len(tt.wantCondition) > 0 {
				managementCluster, err := mockDB.Stamps().ManagementClusters(stampID).Get(ctx, fleet.ManagementClusterResourceName)
				if err != nil {
					t.Fatalf("failed to re-read MC: %v", err)
				}

				cond := apimeta.FindStatusCondition(managementCluster.Status.Conditions, tt.wantCondition)
				if cond == nil {
					t.Fatalf("expected condition %q to be set", tt.wantCondition)
				}
				if cond.Status != tt.wantCondStatus {
					t.Errorf("condition status: got %v, want %v", cond.Status, tt.wantCondStatus)
				}
				if cond.Reason != tt.wantCondReason {
					t.Errorf("condition reason: got %q, want %q", cond.Reason, tt.wantCondReason)
				}
			}
		})
	}
}

type fakeStampLister struct {
	stamps map[string]*fleet.Stamp
}

func (f *fakeStampLister) List(ctx context.Context) ([]*fleet.Stamp, error) {
	var result []*fleet.Stamp
	for _, s := range f.stamps {
		result = append(result, s)
	}
	return result, nil
}

func (f *fakeStampLister) Get(ctx context.Context, stampIdentifier string) (*fleet.Stamp, error) {
	s, ok := f.stamps[stampIdentifier]
	if !ok {
		return nil, fmt.Errorf("stamp %q not found", stampIdentifier)
	}
	return s, nil
}
