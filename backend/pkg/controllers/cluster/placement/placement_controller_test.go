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

package placement

import (
	"context"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	azcorearm "github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"

	"github.com/Azure/ARO-HCP/backend/pkg/utils/controllerutils"
	"github.com/Azure/ARO-HCP/internal/api/coreapi"
	"github.com/Azure/ARO-HCP/internal/api/fleetapi"
	"github.com/Azure/ARO-HCP/internal/api/metadataapi"
	"github.com/Azure/ARO-HCP/internal/database/cosmosstoragetesting/corecosmosstoragetesting"
	"github.com/Azure/ARO-HCP/internal/database/listertesting/corelistertesting"
	"github.com/Azure/ARO-HCP/internal/database/listertesting/fleetlistertesting"
)

// mcForStamp builds a ManagementCluster for the given stamp with the requested
// scheduling policy and Ready condition status.
func mcForStamp(stamp string, schedulable, ready bool) *fleetapi.ManagementCluster {
	resourceID := metadataapi.Must(fleetapi.ToManagementClusterResourceID(stamp))

	policy := fleetapi.ManagementClusterSchedulingPolicyUnschedulable
	if schedulable {
		policy = fleetapi.ManagementClusterSchedulingPolicySchedulable
	}
	readyStatus := metav1.ConditionFalse
	if ready {
		readyStatus = metav1.ConditionTrue
	}

	return &fleetapi.ManagementCluster{
		CosmosMetadata: coreapi.CosmosMetadata{
			ResourceID:   resourceID,
			PartitionKey: strings.ToLower(stamp),
		},
		ResourceID: resourceID,
		Spec:       fleetapi.ManagementClusterSpec{SchedulingPolicy: policy},
		Status: fleetapi.ManagementClusterStatus{
			Conditions: []metav1.Condition{{
				Type:   string(fleetapi.ManagementClusterConditionReady),
				Status: readyStatus,
				Reason: "Test",
			}},
		},
	}
}

// ridForStamp returns the management-cluster resource ID for a stamp.
func ridForStamp(stamp string) *azcorearm.ResourceID {
	return metadataapi.Must(fleetapi.ToManagementClusterResourceID(stamp))
}

// spcAssignedToStamp builds a ServiceProviderCluster already placed on the
// management cluster identified by stamp via Spec intent (used to seed
// placement counts).
func spcAssignedToStamp(stamp string) *coreapi.ServiceProviderCluster {
	return &coreapi.ServiceProviderCluster{
		Spec: coreapi.ServiceProviderClusterSpec{
			ManagementClusterResourceID: ridForStamp(stamp),
		},
	}
}

// spcAssignedToStampViaStatus builds a ServiceProviderCluster placed on the
// management cluster identified by stamp only via the observed Status (Spec
// intent still nil), as happens during the transition before backfill.
func spcAssignedToStampViaStatus(stamp string) *coreapi.ServiceProviderCluster {
	return &coreapi.ServiceProviderCluster{
		Status: coreapi.ServiceProviderClusterStatus{
			ManagementClusterResourceID: ridForStamp(stamp),
		},
	}
}

func TestSelectManagementCluster(t *testing.T) {
	tests := []struct {
		name                    string
		managementClusters      []*fleetapi.ManagementCluster
		serviceProviderClusters []*coreapi.ServiceProviderCluster
		expectedStamp           string // "" means an error is expected
		expectError             bool
	}{
		{
			name:               "no management clusters - error",
			managementClusters: nil,
			expectError:        true,
		},
		{
			name:               "no eligible - all unschedulable",
			managementClusters: []*fleetapi.ManagementCluster{mcForStamp("1", false, true)},
			expectError:        true,
		},
		{
			name:               "no eligible - schedulable but not ready",
			managementClusters: []*fleetapi.ManagementCluster{mcForStamp("1", true, false)},
			expectError:        true,
		},
		{
			name:               "single eligible - chosen",
			managementClusters: []*fleetapi.ManagementCluster{mcForStamp("1", true, true)},
			expectedStamp:      "1",
		},
		{
			name: "bin-pack - highest assigned count wins",
			managementClusters: []*fleetapi.ManagementCluster{
				mcForStamp("1", true, true),
				mcForStamp("2", true, true),
				mcForStamp("3", true, true),
			},
			serviceProviderClusters: []*coreapi.ServiceProviderCluster{
				spcAssignedToStamp("2"),
				spcAssignedToStamp("2"),
				spcAssignedToStamp("2"),
				spcAssignedToStamp("3"),
			},
			expectedStamp: "2",
		},
		{
			name: "tie-break - equal counts pick lowest resource ID (order independent)",
			managementClusters: []*fleetapi.ManagementCluster{
				mcForStamp("3", true, true),
				mcForStamp("2", true, true),
				mcForStamp("1", true, true),
			},
			serviceProviderClusters: nil, // all zero
			expectedStamp:           "1",
		},
		{
			name: "ineligible MC with highest count is skipped",
			managementClusters: []*fleetapi.ManagementCluster{
				mcForStamp("1", true, true),
				mcForStamp("2", false, true), // unschedulable but has more assignments
			},
			serviceProviderClusters: []*coreapi.ServiceProviderCluster{
				spcAssignedToStamp("2"),
				spcAssignedToStamp("2"),
				spcAssignedToStamp("1"),
			},
			expectedStamp: "1",
		},
		{
			name:               "ignores SPC with nil placement",
			managementClusters: []*fleetapi.ManagementCluster{mcForStamp("1", true, true)},
			serviceProviderClusters: []*coreapi.ServiceProviderCluster{
				{Spec: coreapi.ServiceProviderClusterSpec{}}, // nil ManagementClusterResourceID
			},
			expectedStamp: "1",
		},
		{
			name: "count fallback - Status placement counts when Spec unset",
			managementClusters: []*fleetapi.ManagementCluster{
				mcForStamp("1", true, true),
				mcForStamp("2", true, true),
			},
			serviceProviderClusters: []*coreapi.ServiceProviderCluster{
				spcAssignedToStampViaStatus("2"),
				spcAssignedToStampViaStatus("2"),
			},
			expectedStamp: "2",
		},
		{
			name: "count fallback - Spec preferred, Status used as fallback (mixed)",
			managementClusters: []*fleetapi.ManagementCluster{
				mcForStamp("1", true, true),
				mcForStamp("2", true, true),
			},
			serviceProviderClusters: []*coreapi.ServiceProviderCluster{
				spcAssignedToStamp("1"),          // Spec -> 1  (count 1)
				spcAssignedToStampViaStatus("2"), // Status -> 2
				spcAssignedToStampViaStatus("2"), // Status -> 2 (count 2)
			},
			expectedStamp: "2",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			chosen, err := selectManagementCluster(tc.managementClusters, tc.serviceProviderClusters)

			if tc.expectError {
				require.Error(t, err)
				assert.Nil(t, chosen)
				return
			}
			require.NoError(t, err)
			require.NotNil(t, chosen)
			assert.Equal(t, ridForStamp(tc.expectedStamp).String(), chosen.String())
		})
	}
}

func TestPlacementSyncer_SyncOnce(t *testing.T) {
	tests := []struct {
		name               string
		cachedSPC          *coreapi.ServiceProviderCluster   // SPC in cache (nil defaults to existingSPC)
		existingSPC        *coreapi.ServiceProviderCluster   // SPC in cosmos
		otherSPCs          []*coreapi.ServiceProviderCluster // extra SPCs in cache for placement counting
		managementClusters []*fleetapi.ManagementCluster
		expectError        bool
		expectedStamp      string // "" means Spec.ManagementClusterResourceID should remain nil
	}{
		{
			name:               "success - single eligible MC is written to Spec",
			existingSPC:        newTestSPC(),
			managementClusters: []*fleetapi.ManagementCluster{mcForStamp("1", true, true)},
			expectedStamp:      "1",
		},
		{
			name: "already placed in cache - skip, unchanged",
			existingSPC: newTestSPC(func(spc *coreapi.ServiceProviderCluster) {
				spc.Spec.ManagementClusterResourceID = ridForStamp("1")
			}),
			managementClusters: []*fleetapi.ManagementCluster{mcForStamp("1", true, true)},
			expectedStamp:      "1",
		},
		{
			name:      "cache stale but live already placed - skip, keep live value",
			cachedSPC: newTestSPC(), // cache says work needed
			existingSPC: newTestSPC(func(spc *coreapi.ServiceProviderCluster) {
				spc.Spec.ManagementClusterResourceID = ridForStamp("2")
			}),
			managementClusters: []*fleetapi.ManagementCluster{
				mcForStamp("1", true, true),
				mcForStamp("2", true, true),
			},
			expectedStamp: "2",
		},
		{
			name:               "no eligible MC - error, Spec stays nil",
			existingSPC:        newTestSPC(),
			managementClusters: []*fleetapi.ManagementCluster{mcForStamp("1", false, true)},
			expectError:        true,
			expectedStamp:      "",
		},
		{
			name:        "bin-pack chooses fullest eligible MC",
			existingSPC: newTestSPC(),
			otherSPCs: []*coreapi.ServiceProviderCluster{
				spcAssignedToStamp("2"),
				spcAssignedToStamp("2"),
			},
			managementClusters: []*fleetapi.ManagementCluster{
				mcForStamp("1", true, true),
				mcForStamp("2", true, true),
			},
			expectedStamp: "2",
		},
		{
			// Spec nil + Status set: backfill Spec from the observed Status
			// placement. No fresh selection runs — proven by there being NO
			// eligible MC, which selectManagementCluster would reject with an error.
			name: "backfill - Spec nil but Status set, backfills from Status without selection",
			existingSPC: newTestSPC(func(spc *coreapi.ServiceProviderCluster) {
				spc.Status.ManagementClusterResourceID = ridForStamp("2")
			}),
			managementClusters: []*fleetapi.ManagementCluster{mcForStamp("1", false, true)}, // ineligible
			expectError:        false,
			expectedStamp:      "2",
		},
		{
			// Both Spec and Status nil → genuinely unscheduled → fresh selection.
			name:               "fresh selection - both Spec and Status nil",
			existingSPC:        newTestSPC(),
			managementClusters: []*fleetapi.ManagementCluster{mcForStamp("1", true, true)},
			expectedStamp:      "1",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			ctx := context.Background()

			mockDB := corecosmosstoragetesting.NewMockResourcesDBClient()
			spcCRUD := mockDB.ServiceProviderClusters(testClusterSubscriptionID, testClusterResourceGroup, testClusterName)
			_, err := spcCRUD.Create(ctx, tc.existingSPC, nil)
			require.NoError(t, err)

			cachedSPC := tc.cachedSPC
			if cachedSPC == nil {
				cachedSPC = tc.existingSPC
			}
			cacheSPCs := append([]*coreapi.ServiceProviderCluster{cachedSPC}, tc.otherSPCs...)

			syncer := &placementSyncer{
				serviceProviderClusterLister: &corelistertesting.SliceServiceProviderClusterLister{ServiceProviderClusters: cacheSPCs},
				managementClusterLister:      &fleetlistertesting.SliceManagementClusterLister{ManagementClusters: tc.managementClusters},
				cosmosClient:                 mockDB,
			}

			key := controllerutils.HCPClusterKey{
				SubscriptionID:    testClusterSubscriptionID,
				ResourceGroupName: testClusterResourceGroup,
				HCPClusterName:    testClusterName,
			}
			err = syncer.SyncOnce(ctx, key)

			if tc.expectError {
				require.Error(t, err)
			} else {
				require.NoError(t, err)
			}

			updatedSPC, err := spcCRUD.Get(ctx, coreapi.ServiceProviderClusterResourceName)
			require.NoError(t, err)
			if tc.expectedStamp != "" {
				require.NotNil(t, updatedSPC.Spec.ManagementClusterResourceID)
				assert.Equal(t, ridForStamp(tc.expectedStamp).String(), updatedSPC.Spec.ManagementClusterResourceID.String())
			} else {
				assert.Nil(t, updatedSPC.Spec.ManagementClusterResourceID)
			}
		})
	}
}
