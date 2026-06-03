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

package maestroregistration

import (
	"context"
	"fmt"
	"strings"
	"testing"

	apimeta "k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	azcorearm "github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"

	maestroopenapi "github.com/openshift-online/maestro/pkg/api/openapi"

	fleetcontrollers "github.com/Azure/ARO-HCP/fleet/pkg/controllers/base"
	"github.com/Azure/ARO-HCP/internal/api"
	"github.com/Azure/ARO-HCP/internal/api/fleet"
	"github.com/Azure/ARO-HCP/internal/databasetesting"
)

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
	aksResourceID := api.Must(azcorearm.ParseResourceID("/subscriptions/00000000-0000-0000-0000-000000000000/resourceGroups/rg/providers/Microsoft.ContainerService/managedClusters/mc"))
	dnsResourceID := api.Must(azcorearm.ParseResourceID("/subscriptions/00000000-0000-0000-0000-000000000000/resourceGroups/dns-rg/providers/Microsoft.Network/dnszones/example.com"))
	placeholderShardID := api.Must(api.NewInternalID("/api/aro_hcp/v1alpha1/provision_shards/placeholder"))
	return &fleet.ManagementCluster{
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
		},
	}
}

type fakeMaestroConsumerClient struct {
	getConsumerFunc    func(ctx context.Context, consumerName string) (*maestroopenapi.Consumer, error)
	createConsumerFunc func(ctx context.Context, consumer maestroopenapi.Consumer) (*maestroopenapi.Consumer, error)
}

type fakeMaestroConsumerClientFactory struct {
	client MaestroConsumerClient
}

func (f *fakeMaestroConsumerClientFactory) NewMaestroConsumerClient(maestroURL string) MaestroConsumerClient {
	return f.client
}

func (f *fakeMaestroConsumerClient) GetConsumer(ctx context.Context, consumerName string) (*maestroopenapi.Consumer, error) {
	return f.getConsumerFunc(ctx, consumerName)
}

func (f *fakeMaestroConsumerClient) CreateConsumer(ctx context.Context, consumer maestroopenapi.Consumer) (*maestroopenapi.Consumer, error) {
	return f.createConsumerFunc(ctx, consumer)
}

type fakeStampLister struct {
	stamps map[string]*fleet.Stamp
}

func (f *fakeStampLister) List(ctx context.Context) ([]*fleet.Stamp, error) {
	var result []*fleet.Stamp
	for _, stamp := range f.stamps {
		result = append(result, stamp)
	}
	return result, nil
}

func (f *fakeStampLister) Get(ctx context.Context, stampIdentifier string) (*fleet.Stamp, error) {
	stamp, ok := f.stamps[stampIdentifier]
	if !ok {
		return nil, fmt.Errorf("stamp %q not found", stampIdentifier)
	}
	return stamp, nil
}

func TestEnsureConsumer(t *testing.T) {
	tests := []struct {
		name            string
		consumerName    string
		setupClient     func() MaestroConsumerClient
		wantErrContains string
	}{
		{
			name:         "consumer already exists",
			consumerName: "consumer-1",
			setupClient: func() MaestroConsumerClient {
				consumer := maestroopenapi.NewConsumer()
				consumer.SetName("consumer-1")
				return &fakeMaestroConsumerClient{
					getConsumerFunc: func(ctx context.Context, consumerName string) (*maestroopenapi.Consumer, error) {
						return consumer, nil
					},
				}
			},
		},
		{
			name:         "consumer does not exist, create succeeds",
			consumerName: "consumer-1",
			setupClient: func() MaestroConsumerClient {
				created := maestroopenapi.NewConsumer()
				created.SetName("consumer-1")
				return &fakeMaestroConsumerClient{
					getConsumerFunc: func(ctx context.Context, consumerName string) (*maestroopenapi.Consumer, error) {
						return nil, nil
					},
					createConsumerFunc: func(ctx context.Context, consumer maestroopenapi.Consumer) (*maestroopenapi.Consumer, error) {
						return created, nil
					},
				}
			},
		},
		{
			name:         "get consumer fails",
			consumerName: "consumer-1",
			setupClient: func() MaestroConsumerClient {
				return &fakeMaestroConsumerClient{
					getConsumerFunc: func(ctx context.Context, consumerName string) (*maestroopenapi.Consumer, error) {
						return nil, fmt.Errorf("maestro unavailable")
					},
				}
			},
			wantErrContains: "maestro unavailable",
		},
		{
			name:         "create consumer fails",
			consumerName: "consumer-1",
			setupClient: func() MaestroConsumerClient {
				return &fakeMaestroConsumerClient{
					getConsumerFunc: func(ctx context.Context, consumerName string) (*maestroopenapi.Consumer, error) {
						return nil, nil
					},
					createConsumerFunc: func(ctx context.Context, consumer maestroopenapi.Consumer) (*maestroopenapi.Consumer, error) {
						return nil, fmt.Errorf("create failed")
					},
				}
			},
			wantErrContains: "create failed",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx := context.Background()

			syncer := &maestroRegistrationSyncer{}

			err := syncer.ensureConsumer(ctx, tt.setupClient(), tt.consumerName)

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
		})
	}
}

func TestSyncOnce(t *testing.T) {
	const stampID = "s1"

	tests := []struct {
		name              string
		stamp             *fleet.Stamp
		managementCluster *fleet.ManagementCluster
		setupClient       func() MaestroConsumerClient
		wantErr           bool
		wantCondition     string
		wantCondStatus    metav1.ConditionStatus
		wantCondReason    string
	}{
		{
			name:              "MC not found in DB: no-op",
			stamp:             testStamp(stampID, true),
			managementCluster: nil,
			setupClient: func() MaestroConsumerClient {
				return &fakeMaestroConsumerClient{}
			},
		},
		{
			name:              "stamp not approved: sets condition false",
			stamp:             testStamp(stampID, false),
			managementCluster: testManagementCluster(stampID),
			setupClient: func() MaestroConsumerClient {
				return &fakeMaestroConsumerClient{}
			},
			wantCondition:  string(fleet.ManagementClusterConditionMaestroRegistered),
			wantCondStatus: metav1.ConditionFalse,
			wantCondReason: string(fleet.ManagementClusterConditionReasonRegistrationFailed),
		},
		{
			name:              "first ensure consumer error: sets failure condition",
			stamp:             testStamp(stampID, true),
			managementCluster: testManagementCluster(stampID),
			setupClient: func() MaestroConsumerClient {
				return &fakeMaestroConsumerClient{
					getConsumerFunc: func(ctx context.Context, consumerName string) (*maestroopenapi.Consumer, error) {
						return nil, fmt.Errorf("maestro unavailable")
					},
				}
			},
			wantErr:        true,
			wantCondition:  string(fleet.ManagementClusterConditionMaestroRegistered),
			wantCondStatus: metav1.ConditionFalse,
			wantCondReason: string(fleet.ManagementClusterConditionReasonRegistrationFailed),
		},
		{
			name:  "transient error with existing True condition: stays True with CheckFailed",
			stamp: testStamp(stampID, true),
			managementCluster: func() *fleet.ManagementCluster {
				managementCluster := testManagementCluster(stampID)
				apimeta.SetStatusCondition(&managementCluster.Status.Conditions, metav1.Condition{
					Type:   string(fleet.ManagementClusterConditionMaestroRegistered),
					Status: metav1.ConditionTrue,
					Reason: string(fleet.ManagementClusterConditionReasonRegistered),
				})
				return managementCluster
			}(),
			setupClient: func() MaestroConsumerClient {
				return &fakeMaestroConsumerClient{
					getConsumerFunc: func(ctx context.Context, consumerName string) (*maestroopenapi.Consumer, error) {
						return nil, fmt.Errorf("maestro unavailable")
					},
				}
			},
			wantErr:        true,
			wantCondition:  string(fleet.ManagementClusterConditionMaestroRegistered),
			wantCondStatus: metav1.ConditionTrue,
			wantCondReason: string(fleet.ManagementClusterConditionReasonRegistrationCheckFailed),
		},
		{
			name:              "happy path: consumer exists, sets success condition",
			stamp:             testStamp(stampID, true),
			managementCluster: testManagementCluster(stampID),
			setupClient: func() MaestroConsumerClient {
				consumer := maestroopenapi.NewConsumer()
				consumer.SetName("consumer-1")
				return &fakeMaestroConsumerClient{
					getConsumerFunc: func(ctx context.Context, consumerName string) (*maestroopenapi.Consumer, error) {
						return consumer, nil
					},
				}
			},
			wantCondition:  string(fleet.ManagementClusterConditionMaestroRegistered),
			wantCondStatus: metav1.ConditionTrue,
			wantCondReason: string(fleet.ManagementClusterConditionReasonRegistered),
		},
		{
			name:              "happy path: consumer created, sets success condition",
			stamp:             testStamp(stampID, true),
			managementCluster: testManagementCluster(stampID),
			setupClient: func() MaestroConsumerClient {
				created := maestroopenapi.NewConsumer()
				created.SetName("consumer-1")
				return &fakeMaestroConsumerClient{
					getConsumerFunc: func(ctx context.Context, consumerName string) (*maestroopenapi.Consumer, error) {
						return nil, nil
					},
					createConsumerFunc: func(ctx context.Context, consumer maestroopenapi.Consumer) (*maestroopenapi.Consumer, error) {
						return created, nil
					},
				}
			},
			wantCondition:  string(fleet.ManagementClusterConditionMaestroRegistered),
			wantCondStatus: metav1.ConditionTrue,
			wantCondReason: string(fleet.ManagementClusterConditionReasonRegistered),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx := context.Background()

			resources := []any{tt.stamp}
			if tt.managementCluster != nil {
				resources = append(resources, tt.managementCluster)
			}
			mockDB, err := databasetesting.NewMockFleetDBClientWithResources(ctx, resources)
			if err != nil {
				t.Fatalf("failed to create mock DB: %v", err)
			}

			stampLister := &fakeStampLister{stamps: map[string]*fleet.Stamp{
				tt.stamp.GetStampIdentifier(): tt.stamp,
			}}

			syncer := &maestroRegistrationSyncer{
				fleetDBClient:                mockDB,
				maestroConsumerClientFactory: &fakeMaestroConsumerClientFactory{client: tt.setupClient()},
				stampLister:                  stampLister,
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
