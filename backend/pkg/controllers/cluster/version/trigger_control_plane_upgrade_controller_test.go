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

package version

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/blang/semver/v4"
	"github.com/go-logr/logr"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"

	clocktesting "k8s.io/utils/clock/testing"
	"k8s.io/utils/ptr"

	configv1 "github.com/openshift/api/config/v1"
	hsv1beta1 "github.com/openshift/hypershift/api/hypershift/v1beta1"

	"github.com/Azure/ARO-HCP/internal/api/coreapi"
	"github.com/Azure/ARO-HCP/internal/api/metadataapi"
	"github.com/Azure/ARO-HCP/internal/apihelpers/coreapihelpers"
	"github.com/Azure/ARO-HCP/internal/cincinnati"
	"github.com/Azure/ARO-HCP/internal/database/cosmosstoragetesting/corecosmosstoragetesting"
	"github.com/Azure/ARO-HCP/internal/database/listers/corelisters"
	"github.com/Azure/ARO-HCP/internal/database/listertesting/corelistertesting"
	"github.com/Azure/ARO-HCP/internal/database/listertesting/kubeapplierlistertesting"
	"github.com/Azure/ARO-HCP/internal/utils"
)

func TestTriggerControlPlaneUpgradeSyncer_ShouldTriggerUpgrade(t *testing.T) {
	clusterResourceID := metadataapi.Must(coreapihelpers.ToClusterResourceID(testSubscriptionID, testResourceGroupName, testClusterName))
	now := time.Date(2026, 6, 26, 12, 0, 0, 0, time.UTC)
	listerBoom := errors.New("active operation lister exploded")

	newCluster := func(createdAt *time.Time, activeOperationID string) *coreapi.Cluster {
		c := &coreapi.Cluster{
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
			ServiceProviderProperties: coreapi.ClusterServiceProviderProperties{
				ActiveOperationID: activeOperationID,
			},
		}
		if createdAt != nil {
			c.SystemData = &coreapi.SystemData{CreatedAt: createdAt}
		}
		return c
	}

	tests := []struct {
		name           string
		cluster        *coreapi.Cluster
		seedOperation  bool
		opLister       func(mockDB *corecosmosstoragetesting.MockResourcesDBClient) corelisters.ActiveOperationLister
		wantShouldRun  bool
		wantErrContain string
	}{
		{
			name:          "cluster older than grace period runs even with active create (gate 1)",
			cluster:       newCluster(ptr.To(now.Add(-3*time.Hour)), "op-create-1"),
			seedOperation: true,
			wantShouldRun: true,
		},
		{
			name:          "cluster with no SystemData.CreatedAt runs (treated as old enough)",
			cluster:       newCluster(nil, "op-create-1"),
			seedOperation: true,
			wantShouldRun: true,
		},
		{
			name:          "cluster younger than grace period with no active create runs (gate 2)",
			cluster:       newCluster(ptr.To(now.Add(-5*time.Minute)), ""),
			seedOperation: false,
			wantShouldRun: true,
		},
		{
			name:          "young cluster + active create skips",
			cluster:       newCluster(ptr.To(now.Add(-5*time.Minute)), "op-create-1"),
			seedOperation: true,
			wantShouldRun: false,
		},
		{
			name:          "cluster exactly at grace period boundary still skips (boundary is strict >)",
			cluster:       newCluster(ptr.To(now.Add(-clusterCreateGracePeriod)), "op-create-1"),
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
			syncer := &triggerControlPlaneUpgradeSyncer{
				clock:                 clocktesting.NewFakePassiveClock(now),
				activeOperationLister: opLister,
			}

			gotShouldRun, err := syncer.shouldTriggerUpgrade(ctx, tt.cluster)
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

func TestTriggerControlPlaneUpgradeSyncerAcceptsRelease(t *testing.T) {
	for _, tc := range []struct {
		name       string
		active     string
		additional string
		desired    string
		blocked    bool
		accepted   string
		image      string
		noImage    bool
		wantError  string
		wantImage  string
	}{
		{name: "minor accepted", active: "4.19.1", desired: "4.20.2", wantImage: "quay.io/example/release:4.20.2"},
		{name: "blocked minor", active: "4.19.1", desired: "4.20.2", blocked: true, wantError: "OperatorBlocked"},
		{name: "z-stream ignores minor gate", active: "4.20.1", desired: "4.20.2", blocked: true, wantImage: "quay.io/example/release:4.20.2"},
		{name: "oldest partial version keeps minor gate", active: "4.20.1", additional: "4.19.1", desired: "4.20.2", blocked: true, wantError: "OperatorBlocked"},
		{name: "already accepted minor permits next z-stream", active: "4.19.1", desired: "4.20.3", blocked: true, accepted: "4.20", wantImage: "quay.io/example/release:4.20.3"},
		{name: "missing release image fails closed", active: "4.19.1", desired: "4.20.2", noImage: true, wantError: "release image is missing"},
		{name: "same active version requires no write", active: "4.20.2", desired: "4.20.2"},
		{name: "existing accepted image requires no write", active: "4.19.1", desired: "4.20.2", accepted: "4.20", image: "quay.io/example/release:4.20.2"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			client := cincinnati.NewMockClient(gomock.NewController(t))
			c, _, spc := newUpgradeTestSyncer(t, client)
			spc.Status.ControlPlaneVersion.ActiveVersions[0].Version = ptr.To(semver.MustParse(tc.active))
			spc.Spec.ControlPlaneVersion.DesiredVersion = ptr.To(semver.MustParse(tc.desired))
			if tc.additional != "" {
				spc.Status.ControlPlaneVersion.ActiveVersions = append(spc.Status.ControlPlaneVersion.ActiveVersions,
					coreapi.ServiceProviderClusterActiveVersion{Version: ptr.To(semver.MustParse(tc.additional)), State: configv1.PartialUpdate})
			}
			if tc.accepted != "" {
				spc.Spec.ControlPlaneVersion.AcceptedMinorVersion = ptr.To(tc.accepted)
			}
			if tc.image != "" {
				spc.Spec.DesiredHostedCluster = &hsv1beta1.HostedCluster{}
				spc.Spec.DesiredHostedCluster.Spec.Release.Image = tc.image
			}
			c.readDesireLister = &kubeapplierlistertesting.SliceReadDesireLister{Desires: upgradeReadDesires(t, !tc.blocked)}
			db := corecosmosstoragetesting.NewMockResourcesDBClient()
			crud := db.ServiceProviderClusters(testSubscriptionID, testResourceGroupName, testClusterName)
			stored, err := crud.Create(t.Context(), spc, nil)
			require.NoError(t, err)
			c.resourcesDBClient = db
			c.serviceProviderClusterLister = &corelistertesting.SliceServiceProviderClusterLister{ServiceProviderClusters: []*coreapi.ServiceProviderCluster{stored}}
			lookupContext := gomock.Cond[context.Context](func(ctx context.Context) bool {
				_, hasDeadline := ctx.Deadline()
				return hasDeadline
			})
			if tc.wantImage != "" || tc.noImage {
				image := tc.wantImage
				client.EXPECT().GetUpdates(lookupContext, gomock.Any(), "multi", "multi", "stable-4.20", semver.MustParse(tc.desired)).
					Return(configv1.Release{Version: tc.desired, Image: image}, nil, nil, nil)
			}
			err = c.SyncOnce(t.Context(), upgradeTestKey())
			if tc.wantError != "" {
				require.ErrorContains(t, err, tc.wantError)
			} else {
				require.NoError(t, err)
			}
			updated, err := crud.Get(t.Context(), coreapi.ServiceProviderClusterResourceName)
			require.NoError(t, err)
			if tc.wantImage != "" {
				require.Equal(t, tc.wantImage, updated.Spec.DesiredHostedCluster.Spec.Release.Image)
				require.Equal(t, "stable-4.20", updated.Spec.DesiredHostedCluster.Spec.Channel)
				require.Equal(t, "4.20", *updated.Spec.ControlPlaneVersion.AcceptedMinorVersion)
			} else {
				require.Equal(t, tc.image, releaseImage(updated))
			}
		})
	}
}

func TestTriggerControlPlaneUpgradeMissingActiveVersion(t *testing.T) {
	client := cincinnati.NewMockClient(gomock.NewController(t))
	c, _, spc := newUpgradeTestSyncer(t, client)
	spc.Status.ControlPlaneVersion.ActiveVersions[0].Version = nil
	require.ErrorContains(t, c.SyncOnce(t.Context(), upgradeTestKey()), "control plane active version is missing")
}

func releaseImage(spc *coreapi.ServiceProviderCluster) string {
	if spc.Spec.DesiredHostedCluster == nil {
		return ""
	}
	return spc.Spec.DesiredHostedCluster.Spec.Release.Image
}

func TestResolveNightlyControlPlaneRelease(t *testing.T) {
	client := cincinnati.NewMockClient(gomock.NewController(t))
	syncer, cluster, _ := newUpgradeTestSyncer(t, client)
	cluster.CustomerProperties.Version.ChannelGroup = "nightly"
	nightlyURI := metadataapi.Must(cincinnati.GetCincinnatiURI("nightly"))
	desired := semver.MustParse("4.22.0-0.nightly-2026-10-20")
	client.EXPECT().GetUpdates(gomock.Any(), nightlyURI, "multi", "multi", "nightly-4.22", desired).
		Return(configv1.Release{Version: desired.String(), Image: "quay.io/openshift-release-dev/ocp-release-nightly@sha256:abc"}, nil, nil, nil)
	image, channel, err := syncer.resolveRelease(t.Context(), upgradeTestKey(), cluster.CustomerProperties.Version.ChannelGroup, &desired)
	require.NoError(t, err)
	require.Equal(t, "ocp.example.azurecr.io/openshift-release-dev/ocp-release-nightly@sha256:abc", image)
	require.Empty(t, channel)
}

func TestTriggerControlPlaneUpgradeAcceptanceWriteConflict(t *testing.T) {
	client := cincinnati.NewMockClient(gomock.NewController(t))
	c, _, spc := newUpgradeTestSyncer(t, client)
	db := corecosmosstoragetesting.NewMockResourcesDBClient()
	crud := db.ServiceProviderClusters(testSubscriptionID, testResourceGroupName, testClusterName)
	stale, err := crud.Create(t.Context(), spc, nil)
	require.NoError(t, err)
	c.resourcesDBClient = db
	c.serviceProviderClusterLister = &corelistertesting.SliceServiceProviderClusterLister{ServiceProviderClusters: []*coreapi.ServiceProviderCluster{stale}}
	newer := stale.DeepCopy()
	newer.Spec.ControlPlaneVersion.DesiredVersion = ptr.To(semver.MustParse("4.20.3"))
	_, err = crud.Replace(t.Context(), newer, nil)
	require.NoError(t, err)
	client.EXPECT().GetUpdates(gomock.Any(), gomock.Any(), "multi", "multi", "stable-4.20", semver.MustParse("4.20.2")).
		Return(configv1.Release{Version: "4.20.2", Image: "quay.io/example/release:4.20.2"}, nil, nil, nil)
	require.NoError(t, c.SyncOnce(t.Context(), upgradeTestKey()))
	current, err := crud.Get(t.Context(), coreapi.ServiceProviderClusterResourceName)
	require.NoError(t, err)
	require.Equal(t, "4.20.3", current.Spec.ControlPlaneVersion.DesiredVersion.String())
	require.Nil(t, current.Spec.ControlPlaneVersion.AcceptedMinorVersion)
}
