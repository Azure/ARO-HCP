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

package rollout

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/blang/semver/v4"
	"github.com/google/go-cmp/cmp"
	"github.com/stretchr/testify/require"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	clocktesting "k8s.io/utils/clock/testing"

	azcorearm "github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"

	"github.com/Azure/ARO-HCP/backend/pkg/utils/controllerutils"
	"github.com/Azure/ARO-HCP/internal/api/coreapi"
	"github.com/Azure/ARO-HCP/internal/api/fleetapi"
	"github.com/Azure/ARO-HCP/internal/database/cosmosstorage/cosmosstorageutils"
	"github.com/Azure/ARO-HCP/internal/database/listertesting/corelistertesting"
	"github.com/Azure/ARO-HCP/internal/database/listertesting/fleetlistertesting"
)

type profileCheckingSelector struct{ t *testing.T }

func TestRolloutWritersWaitForProfile(t *testing.T) {
	rollout := newTestRollout("stable-4.21", v("4.21.6"), fleetapi.ControlPlaneVersionRolloutStatus{})
	rollout.Spec.Version = coreapi.VersionProfile{}
	before := rollout.DeepCopy()
	lister := &fleetlistertesting.SliceControlPlaneVersionRolloutLister{ControlPlaneVersionRollouts: []*fleetapi.ControlPlaneVersionRollout{rollout}}
	// No selector, membership listers or write clients: an absent profile needs no work.
	for name, syncer := range map[string]controllerutils.ControlPlaneVersionRolloutSyncer{
		"best":        &bestVersionSelectionSyncer{rolloutLister: lister},
		"status":      &statusCollectorSyncer{rolloutLister: lister},
		"progressive": &zStreamProgressiveDesiredVersionRolloutSyncer{rolloutLister: lister},
	} {
		t.Run(name, func(t *testing.T) {
			require.NoError(t, syncer.SyncOnce(t.Context(), controllerutils.ControlPlaneVersionRolloutKey{YStreamChannel: "stable-4.21"}))
			require.Empty(t, cmp.Diff(before, rollout, cmp.AllowUnexported(azcorearm.ResourceID{}, azcorearm.ResourceType{})))
		})
	}
}

func (s profileCheckingSelector) BestExactVersionForProfile(_ context.Context, profile coreapi.VersionProfile) (*semver.Version, error) {
	require.Empty(s.t, cmp.Diff(coreapi.VersionProfile{ID: "4.21", ChannelGroup: "stable"}, profile))
	return v("4.21.6"), nil
}

func TestLegacyRolloutWriters(t *testing.T) {
	t.Parallel()
	for _, writer := range []string{"best", "status", "progressive conditions"} {
		t.Run(writer, func(t *testing.T) {
			ctx := t.Context()
			clock := clocktesting.NewFakeClock(statusTestNow)
			status := fleetapi.ControlPlaneVersionRolloutStatus{}
			if writer == "progressive conditions" {
				status.Conditions = []metav1.Condition{
					{Type: ConditionProgressing, Status: metav1.ConditionTrue, LastTransitionTime: metav1.Time{Time: clock.Now()}, Reason: "Canary"},
					{Type: ConditionDegraded, Status: metav1.ConditionFalse, LastTransitionTime: metav1.Time{Time: clock.Now()}, Reason: "Canary"},
				}
			}
			db, lister := newTestRolloutStore(t, newTestRollout("stable-4.21", v("4.21.1"), status))
			crud := db.ControlPlaneVersionRollouts()
			stored, err := lister.Get(ctx, "stable-4.21")
			require.NoError(t, err)
			data, ok := db.GetDocument(stored.GetCosmosUID())
			require.True(t, ok)
			var doc map[string]any
			require.NoError(t, json.Unmarshal(data, &doc))
			delete(doc["properties"].(map[string]any)["spec"].(map[string]any), "version")
			data, err = json.Marshal(doc)
			require.NoError(t, err)
			db.StoreDocument(stored.GetCosmosUID(), data)
			legacy, err := lister.Get(ctx, "stable-4.21")
			require.NoError(t, err)
			require.Equal(t, stored.Spec.Version, legacy.Spec.Version, "storage reads must normalize legacy profiles before reconciliation")
			before := legacy.DeepCopy()
			staleLister := &fleetlistertesting.SliceControlPlaneVersionRolloutLister{ControlPlaneVersionRollouts: []*fleetapi.ControlPlaneVersionRollout{legacy}}
			var sync func(context.Context, controllerutils.ControlPlaneVersionRolloutKey) error
			switch writer {
			case "best":
				sync = (&bestVersionSelectionSyncer{rolloutLister: staleLister, fleetDBClient: db, selector: profileCheckingSelector{t}, config: NewDefaultRolloutConfig()}).SyncOnce
			case "status":
				sync = (&statusCollectorSyncer{
					clock: clock, rolloutLister: staleLister, fleetDBClient: db, config: NewDefaultRolloutConfig(),
					clusterLister:                &corelistertesting.SliceClusterLister{Clusters: []*coreapi.Cluster{newTestCluster("c1", "stable", "4.22")}},
					serviceProviderClusterLister: &corelistertesting.SliceServiceProviderClusterLister{ServiceProviderClusters: []*coreapi.ServiceProviderCluster{newTestServiceProviderCluster("c1", v("4.21.1"), nil, nil)}},
				}).SyncOnce
			default:
				c := &zStreamProgressiveDesiredVersionRolloutSyncer{clock: clock, fleetDBClient: db}
				sync = func(ctx context.Context, _ controllerutils.ControlPlaneVersionRolloutKey) error {
					return c.recordCondition(ctx, legacy, rolloutDecisionResult{Outcome: outcomeProgressing, Message: "waiting for canaries"}, nil)
				}
			}
			key := controllerutils.ControlPlaneVersionRolloutKey{YStreamChannel: "stable-4.21"}
			require.NoError(t, sync(ctx, key))
			written, err := crud.Get(ctx, key.YStreamChannel)
			require.NoError(t, err)
			want := before.DeepCopy()
			switch writer {
			case "best":
				want.Spec.BestExactVersion = v("4.21.6")
			case "status":
				want.Status.ClusterCountByDesiredExactVersion = map[string]int64{"4.21.1": 1}
				want.Status.MismatchedClusterCountByDesiredExactVersion = map[string]int64{"4.21.1": 1}
			default:
				for i := range want.Status.Conditions {
					want.Status.Conditions[i].Reason = string(outcomeProgressing)
					want.Status.Conditions[i].Message = "waiting for canaries"
				}
			}
			require.Empty(t, cmp.Diff(want.Spec, written.Spec))
			require.Empty(t, cmp.Diff(want.Status, written.Status))
			data, ok = db.GetDocument(written.GetCosmosUID())
			require.True(t, ok)
			var persisted cosmosstorageutils.GenericDocument[fleetapi.ControlPlaneVersionRollout]
			require.NoError(t, json.Unmarshal(data, &persisted))
			require.Equal(t, stored.Spec.Version, persisted.Content.Spec.Version, "writers must preserve the normalized profile in storage")
			rewritten, err := crud.Replace(ctx, written.DeepCopy(), written, nil)
			require.NoError(t, err)
			require.NoError(t, sync(ctx, key), "stale writes after reserialization should be handled as ETag conflicts")
			after, err := crud.Get(ctx, key.YStreamChannel)
			require.NoError(t, err)
			require.Empty(t, cmp.Diff(rewritten, after, cmp.AllowUnexported(azcorearm.ResourceID{}, azcorearm.ResourceType{})))
			require.Empty(t, cmp.Diff(before, legacy, cmp.AllowUnexported(azcorearm.ResourceID{}, azcorearm.ResourceType{})))
		})
	}
}
