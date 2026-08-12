// Copyright 2026 Microsoft Corporation
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//	http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package version

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/blang/semver/v4"
	"github.com/go-logr/logr"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"

	clocktesting "k8s.io/utils/clock/testing"
	"k8s.io/utils/ptr"

	azcorearm "github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"

	"github.com/Azure/ARO-HCP/internal/api/coreapi"
	"github.com/Azure/ARO-HCP/internal/api/metadataapi"
	"github.com/Azure/ARO-HCP/internal/cincinnati"
	"github.com/Azure/ARO-HCP/internal/database/cosmosstorage/cosmosstorageutils"
	"github.com/Azure/ARO-HCP/internal/database/cosmosstoragetesting/corecosmosstoragetesting"
	"github.com/Azure/ARO-HCP/internal/database/listers/corelisters"
	"github.com/Azure/ARO-HCP/internal/database/listertesting/corelistertesting"
	"github.com/Azure/ARO-HCP/internal/utils"
)

// channelExistence maps channel group -> minor -> whether GraphClient.ChannelExists returns true.
type channelExistence map[string]map[string]bool

func mockGraphClient(ctrl *gomock.Controller, existence channelExistence) *cincinnati.MockGraphClient {
	mc := cincinnati.NewMockGraphClient(ctrl)
	for channelGroup, minors := range existence {
		for minor, exists := range minors {
			mc.EXPECT().ChannelExists(gomock.Any(), channelGroup, minor).Return(exists, nil)
		}
	}
	return mc
}

// assertVersionResult is a helper function that validates a resolved desired control plane version
func assertVersionResult(t *testing.T, result *semver.Version, err error, expectedVersion *semver.Version, expectedError bool, expectedErrorContains string) {
	if expectedError {
		assert.Error(t, err)
		assert.NotEmpty(t, expectedErrorContains)
		assert.ErrorContains(t, err, expectedErrorContains)
	} else {
		assert.NoError(t, err)
		if expectedVersion == nil {
			assert.Nil(t, result)
		} else {
			assert.NotNil(t, result)
			assert.True(t, result.EQ(*expectedVersion), "Expected version %q, got %q", expectedVersion.String(), result.String())
		}
	}
}

// boomActiveOperationLister is a test double that returns the configured
// error from ListActiveOperationsForCluster. It exists so the gating helper
// can exercise its error-propagation branch without a misbehaving mock DB.
type boomActiveOperationLister struct {
	corelisters.ActiveOperationLister
	err error
}

func (b *boomActiveOperationLister) Get(_ context.Context, _, _ string) (*coreapi.Operation, error) {
	return nil, b.err
}

func (b *boomActiveOperationLister) ListActiveOperationsForCluster(_ context.Context, _, _, _ string) ([]*coreapi.Operation, error) {
	return nil, b.err
}

// seedClusterCreateOperation seeds an active Create operation rooted at the
// given ExternalID into the mock DB so the DB-backed active operation lister
// can find it.
func seedClusterCreateOperation(t *testing.T, ctx context.Context, mockDB *corecosmosstoragetesting.MockResourcesDBClient, externalID *azcorearm.ResourceID, opName string) {
	t.Helper()
	opResourceID := metadataapi.Must(azcorearm.ParseResourceID(coreapi.ToOperationResourceIDString(externalID.SubscriptionID, opName)))
	operationID := metadataapi.Must(azcorearm.ParseResourceID(
		"/subscriptions/" + externalID.SubscriptionID +
			"/providers/Microsoft.RedHatOpenShift/locations/eastus/hcpOperationStatuses/" + opName,
	))
	op := &coreapi.Operation{
		CosmosMetadata: coreapi.CosmosMetadata{
			ResourceID:   opResourceID,
			PartitionKey: strings.ToLower(externalID.SubscriptionID),
		},
		Status:      coreapi.ProvisioningStateAccepted,
		Request:     cosmosstorageutils.OperationRequestCreate,
		ExternalID:  externalID,
		OperationID: operationID,
	}
	_, err := mockDB.Operations(externalID.SubscriptionID).Create(ctx, op, nil)
	require.NoError(t, err)
}

func TestControlPlaneDesiredVersionSyncer_ShouldDetermineDesiredVersion(t *testing.T) {
	clusterResourceID := metadataapi.Must(coreapi.ToClusterResourceID(testSubscriptionID, testResourceGroupName, testClusterName))
	now := time.Date(2026, 6, 25, 12, 0, 0, 0, time.UTC)
	listerBoom := errors.New("active operation lister exploded")

	newCluster := func(createdAt *time.Time, activeOperationID string) *coreapi.HCPOpenShiftCluster {
		c := &coreapi.HCPOpenShiftCluster{
			CosmosMetadata: coreapi.CosmosMetadata{
				ResourceID: clusterResourceID,
			},
			TrackedResource: coreapi.TrackedResource{
				Resource: coreapi.Resource{
					ID:   clusterResourceID,
					Name: testClusterName,
					Type: coreapi.ClusterResourceType.String(),
				},
			},
			ServiceProviderProperties: coreapi.HCPOpenShiftClusterServiceProviderProperties{
				ActiveOperationID: activeOperationID,
			},
		}
		if createdAt != nil {
			c.SystemData = &coreapi.SystemData{CreatedAt: createdAt}
		}
		return c
	}
	newSPC := func(desired *semver.Version) *coreapi.ServiceProviderCluster {
		return &coreapi.ServiceProviderCluster{
			Spec: coreapi.ServiceProviderClusterSpec{
				ControlPlaneVersion: coreapi.ServiceProviderClusterSpecVersion{DesiredVersion: desired},
			},
		}
	}

	tests := []struct {
		name           string
		cluster        *coreapi.HCPOpenShiftCluster
		spc            *coreapi.ServiceProviderCluster
		seedOperation  bool
		opLister       func(mockDB *corecosmosstoragetesting.MockResourcesDBClient) corelisters.ActiveOperationLister
		wantShouldRun  bool
		wantErrContain string
	}{
		{
			name:          "empty DesiredVersion runs even when create is in flight (gate 1)",
			cluster:       newCluster(ptr.To(now.Add(-5*time.Minute)), "op-create-1"),
			spc:           newSPC(nil),
			seedOperation: true,
			wantShouldRun: true,
		},
		{
			name:          "cluster older than grace period runs even with active create (gate 2)",
			cluster:       newCluster(ptr.To(now.Add(-3*time.Hour)), "op-create-1"),
			spc:           newSPC(ptr.To(semver.MustParse("4.19.15"))),
			seedOperation: true,
			wantShouldRun: true,
		},
		{
			name:          "cluster with no SystemData.CreatedAt runs (treated as old enough)",
			cluster:       newCluster(nil, "op-create-1"),
			spc:           newSPC(ptr.To(semver.MustParse("4.19.15"))),
			seedOperation: true,
			wantShouldRun: true,
		},
		{
			name:          "cluster younger than grace period with no active create runs (gate 3)",
			cluster:       newCluster(ptr.To(now.Add(-5*time.Minute)), ""),
			spc:           newSPC(ptr.To(semver.MustParse("4.19.15"))),
			seedOperation: false,
			wantShouldRun: true,
		},
		{
			name:          "young cluster + DesiredVersion set + active create skips",
			cluster:       newCluster(ptr.To(now.Add(-5*time.Minute)), "op-create-1"),
			spc:           newSPC(ptr.To(semver.MustParse("4.19.15"))),
			seedOperation: true,
			wantShouldRun: false,
		},
		{
			name:    "cluster exactly at grace period boundary still skips (boundary is strict >)",
			cluster: newCluster(ptr.To(now.Add(-clusterCreateGracePeriod)), "op-create-1"),
			spc:     newSPC(ptr.To(semver.MustParse("4.19.15"))),
			// active create present so without the boundary-is-strict gate, the
			// cluster's age would have to push us through.
			seedOperation: true,
			wantShouldRun: false,
		},
		{
			// Fail open: if we can't tell whether a Create is in flight we
			// surface the error to the caller but still report shouldRun=true
			// so a flaky lister doesn't pin the controller in skip-forever
			// mode for the rest of the grace window.
			name:          "active operation lister error is propagated and fails open to shouldRun=true",
			cluster:       newCluster(ptr.To(now.Add(-5*time.Minute)), "op-broken"),
			spc:           newSPC(ptr.To(semver.MustParse("4.19.15"))),
			seedOperation: false,
			opLister: func(_ *corecosmosstoragetesting.MockResourcesDBClient) corelisters.ActiveOperationLister {
				return &boomActiveOperationLister{err: listerBoom}
			},
			wantShouldRun:  true,
			wantErrContain: "failed to get operations",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			ctx := utils.ContextWithLogger(context.Background(), logr.Discard())
			mockDB := corecosmosstoragetesting.NewMockResourcesDBClient()
			if tt.seedOperation {
				seedClusterCreateOperation(t, ctx, mockDB, clusterResourceID, "op-create-1")
			}
			var opLister corelisters.ActiveOperationLister
			if tt.opLister != nil {
				opLister = tt.opLister(mockDB)
			} else {
				opLister = &corelistertesting.DBActiveOperationLister{ResourcesDBClient: mockDB}
			}
			syncer := &desiredVersionSyncerCommon{
				clock:                 clocktesting.NewFakePassiveClock(now),
				resourcesDBClient:     mockDB,
				activeOperationLister: opLister,
			}

			gotShouldRun, err := syncer.shouldDetermineDesiredVersion(ctx, tt.cluster, tt.spc)
			if tt.wantErrContain != "" {
				require.Error(t, err)
				assert.ErrorContains(t, err, tt.wantErrContain)
			} else {
				require.NoError(t, err)
			}
			assert.Equal(t, tt.wantShouldRun, gotShouldRun)
		})
	}
}
