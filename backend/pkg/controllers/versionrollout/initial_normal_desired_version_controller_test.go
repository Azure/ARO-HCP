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

package versionrollout

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	clocktesting "k8s.io/utils/clock/testing"

	"github.com/Azure/ARO-HCP/backend/pkg/utils/controllerutils"
	"github.com/Azure/ARO-HCP/internal/api/coreapi"
	"github.com/Azure/ARO-HCP/internal/api/fleetapi"
	"github.com/Azure/ARO-HCP/internal/database/cosmosstoragetesting/corecosmosstoragetesting"
	"github.com/Azure/ARO-HCP/internal/database/listertesting/corelistertesting"
)

type initialVersionRetryQueue struct {
	keys   []any
	delays []time.Duration
}

func (q *initialVersionRetryQueue) EnqueueAfter(key any, delay time.Duration) {
	q.keys = append(q.keys, key)
	q.delays = append(q.delays, delay)
}

func TestInitialNormalDesiredVersion(t *testing.T) {
	for _, tc := range []struct {
		name, requested, desired, want          string
		missingRollout, missingBest, concurrent bool
	}{
		{name: "initialize despite failed rollout", requested: "4.21", want: "4.21.6"},
		{name: "requested exact version selects channel best", requested: "4.21.1", want: "4.21.6"},
		{name: "preserve existing desired", requested: "4.21", desired: "4.21.1", want: "4.21.1"},
		{name: "preserve concurrent assignment", requested: "4.21", concurrent: true, want: "4.21.8"},
		{name: "requested channel has no rollout", requested: "4.22"},
		{name: "invalid requested version", requested: "invalid"},
		{name: "missing rollout", requested: "4.21", missingRollout: true},
		{name: "missing best", requested: "4.21", missingBest: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx := context.Background()
			now := time.Date(2026, 9, 11, 12, 0, 0, 0, time.UTC)
			// Active version is deliberately in another minor: initial assignment uses
			// the customer's requested channel, not normal rollout membership.
			serviceProviderCluster := newTestSPC("c1", nil, []coreapi.HCPClusterActiveVersion{completed("4.20.8")}, nil)
			if tc.desired != "" {
				serviceProviderCluster.Spec.ControlPlaneVersion.DesiredVersion = v(tc.desired)
			}
			db, err := corecosmosstoragetesting.NewMockResourcesDBClientWithResources(ctx, []any{
				newTestCluster("c1", "stable", tc.requested), serviceProviderCluster,
			})
			require.NoError(t, err)
			rollout := newTestRollout("stable-4.21", v("4.21.6"), fleetapi.ControlPlaneVersionRolloutStatus{
				FailedClusterCountByDesiredExactVersion: map[string]int64{"4.21.6": 100},
				Conditions:                              []metav1.Condition{{Type: ConditionDegraded, Status: metav1.ConditionTrue, Reason: "Failure", Message: "existing condition"}},
			})
			if tc.missingBest {
				rollout.Spec.BestExactVersion = nil
			}
			rollouts := []*fleetapi.ControlPlaneVersionRollout{rollout}
			if tc.missingRollout {
				rollouts = nil
			}
			fleet, rolloutLister := newTestRolloutStore(t, rollouts...)
			beforeRollout, _ := fleet.ControlPlaneVersionRollouts().Get(ctx, "stable-4.21")
			retryQueue := &initialVersionRetryQueue{}
			syncer := &initialNormalClusterDesiredVersionSyncer{
				enqueueAfter: retryQueue,
				clock:        clocktesting.NewFakeClock(now), resourcesDBClient: db, rolloutLister: rolloutLister,
				clusterLister:                &corelistertesting.DBClusterLister{ResourcesDBClient: db},
				serviceProviderClusterLister: &corelistertesting.DBServiceProviderClusterLister{ResourcesDBClient: db},
			}
			crud := db.ServiceProviderClusters(testSubscriptionID, testResourceGroupName, "c1")
			before, err := crud.Get(ctx, coreapi.ServiceProviderClusterResourceName)
			require.NoError(t, err)
			if tc.concurrent {
				syncer.serviceProviderClusterLister = &corelistertesting.SliceServiceProviderClusterLister{ServiceProviderClusters: []*coreapi.ServiceProviderCluster{before}}
				updated := before.DeepCopy()
				setDesiredVersion(updated, v("4.21.8"), metav1.Time{Time: now})
				before, err = crud.Replace(ctx, updated, nil)
				require.NoError(t, err)
			}
			key := controllerutils.HCPClusterKey{SubscriptionID: testSubscriptionID, ResourceGroupName: testResourceGroupName, HCPClusterName: "c1"}
			if tc.requested == "invalid" {
				require.ErrorContains(t, syncer.SyncOnce(ctx, key), "cannot determine requested channel")
			} else {
				require.NoError(t, syncer.SyncOnce(ctx, key))
			}
			if tc.missingRollout || tc.missingBest || tc.requested == "4.22" {
				require.Equal(t, []any{key}, retryQueue.keys)
				require.Equal(t, []time.Duration{10 * time.Second}, retryQueue.delays)
			} else {
				require.Empty(t, retryQueue.keys)
			}
			current, err := crud.Get(ctx, coreapi.ServiceProviderClusterResourceName)
			require.NoError(t, err)
			if tc.want == "" {
				require.Nil(t, current.Spec.ControlPlaneVersion.DesiredVersion)
			} else {
				require.NotNil(t, current.Spec.ControlPlaneVersion.DesiredVersion)
				require.Equal(t, tc.want, current.Spec.ControlPlaneVersion.DesiredVersion.String())
			}
			if tc.want == "" || tc.desired != "" || tc.concurrent {
				require.Equal(t, before, current)
			} else {
				require.NotNil(t, current.Spec.ControlPlaneVersion.DesiredVersionLastTransitionTime)
				require.True(t, now.Equal(current.Spec.ControlPlaneVersion.DesiredVersionLastTransitionTime.Time))
			}
			if tc.requested == "invalid" {
				require.ErrorContains(t, syncer.SyncOnce(ctx, key), "cannot determine requested channel")
			} else {
				require.NoError(t, syncer.SyncOnce(ctx, key))
			}
			afterSecondSync, err := crud.Get(ctx, coreapi.ServiceProviderClusterResourceName)
			require.NoError(t, err)
			require.Equal(t, current, afterSecondSync, "repeated sync must not rewrite desired version")
			if !tc.missingRollout {
				afterRollout, err := fleet.ControlPlaneVersionRollouts().Get(ctx, "stable-4.21")
				require.NoError(t, err)
				require.Equal(t, beforeRollout, afterRollout, "initial assignment must not write rollout conditions or other rollout fields")
			}
		})
	}
}
