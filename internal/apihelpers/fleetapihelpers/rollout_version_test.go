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

package fleetapihelpers_test

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/blang/semver/v4"
	"github.com/google/go-cmp/cmp"
	"github.com/stretchr/testify/require"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	clocktesting "k8s.io/utils/clock/testing"

	azcorearm "github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"

	"github.com/Azure/ARO-HCP/internal/api/coreapi"
	"github.com/Azure/ARO-HCP/internal/api/fleetapi"
	"github.com/Azure/ARO-HCP/internal/apihelpers/fleetapihelpers"
	"github.com/Azure/ARO-HCP/internal/database/cosmosstorage/cosmosstorageutils"
	"github.com/Azure/ARO-HCP/internal/database/cosmosstoragetesting/fleetcosmosstoragetesting"
)

func TestNormalizeRolloutVersion(t *testing.T) {
	t.Parallel()
	clock := clocktesting.NewFakeClock(time.Date(2026, 5, 6, 7, 8, 9, 0, time.UTC))
	best := semver.MustParse("4.21.1")
	for _, tc := range []struct {
		name    string
		channel string
		version coreapi.VersionProfile
		want    coreapi.VersionProfile
		wantErr string
	}{
		{name: "legacy stable", channel: "stable-4.21", want: coreapi.VersionProfile{ID: "4.21", ChannelGroup: "stable"}},
		{name: "legacy nightly", channel: "nightly-4.21", want: coreapi.VersionProfile{ID: "4.21", ChannelGroup: "nightly"}},
		{name: "already normalized", channel: "stable-4.21", version: coreapi.VersionProfile{ID: "4.21", ChannelGroup: "stable"}},
		{name: "missing ID", channel: "stable-4.21", version: coreapi.VersionProfile{ChannelGroup: "stable"}, wantErr: "invalid rollout minor version"},
		{name: "missing group", channel: "stable-4.21", version: coreapi.VersionProfile{ID: "4.21"}, wantErr: "unsupported channel group"},
		{name: "mismatched ID", channel: "stable-4.21", version: coreapi.VersionProfile{ID: "4.22", ChannelGroup: "stable"}, wantErr: "does not match spec.version"},
		{name: "mismatched group", channel: "stable-4.21", version: coreapi.VersionProfile{ID: "4.21", ChannelGroup: "fast"}, wantErr: "does not match spec.version"},
		{name: "legacy malformed", channel: "stable-invalid", wantErr: "invalid version channel"},
		{name: "legacy exact", channel: "stable-4.21.0", wantErr: "invalid version channel"},
		{name: "legacy unknown group", channel: "unknown-4.21", wantErr: "unsupported channel group"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			rid, err := fleetapihelpers.ToControlPlaneVersionRolloutResourceID(tc.channel)
			require.NoError(t, err)
			rollout := &fleetapi.ControlPlaneVersionRollout{
				CosmosMetadata: coreapi.CosmosMetadata{ResourceID: rid, PartitionKey: strings.ToLower(coreapi.ProviderNamespace), CosmosETag: "etag", ExistingCosmosUID: "uid", InstanceVersion: 7},
				Spec:           fleetapi.ControlPlaneVersionRolloutSpec{Version: tc.version, BestExactVersion: &best},
				Status: fleetapi.ControlPlaneVersionRolloutStatus{
					LastAssignmentTime:                           &metav1.Time{Time: clock.Now()},
					Conditions:                                   []metav1.Condition{{Type: "Progressing", Status: metav1.ConditionTrue, LastTransitionTime: metav1.Time{Time: clock.Now()}}},
					ClusterCountByDesiredExactVersion:            map[string]int64{"4.21.1": 3},
					MismatchedClusterCountByDesiredExactVersion:  map[string]int64{"4.21.1": 2},
					FailedClusterCountByDesiredExactVersion:      map[string]int64{"4.21.1": 1},
					ClusterCountByAchievedExactVersion:           map[string]int64{"4.21.0": 2},
					SuccessfulClusterCountByAchievedExactVersion: map[string]int64{"4.21.0": 1},
				},
			}
			before := rollout.DeepCopy()
			desired, err := fleetapihelpers.NormalizeRolloutVersion(rollout)
			if tc.wantErr != "" {
				require.ErrorContains(t, err, tc.wantErr)
			} else {
				require.NoError(t, err)
			}
			var want *fleetapi.ControlPlaneVersionRollout
			if tc.want != (coreapi.VersionProfile{}) {
				want = before.DeepCopy()
				want.Spec.Version = tc.want
			}
			require.Empty(t, cmp.Diff(want, desired, cmp.AllowUnexported(azcorearm.ResourceID{}, azcorearm.ResourceType{})))
			if desired != nil {
				second, err := fleetapihelpers.NormalizeRolloutVersion(desired)
				require.NoError(t, err)
				require.Nil(t, second, "backfill must be idempotent")
				desired.Spec.Version.ID = "4.22"
				desired.Spec.BestExactVersion.Patch++
				desired.Status.ClusterCountByDesiredExactVersion["4.21.1"]++
				desired.Status.Conditions[0].Reason = "changed"
			}
			require.Empty(t, cmp.Diff(before, rollout, cmp.AllowUnexported(azcorearm.ResourceID{}, azcorearm.ResourceType{})), "backfill must preserve its input")
		})
	}
	for _, rollout := range []*fleetapi.ControlPlaneVersionRollout{nil, {}} {
		desired, err := fleetapihelpers.NormalizeRolloutVersion(rollout)
		require.ErrorContains(t, err, "without resource ID")
		require.Nil(t, desired)
	}
}

func TestRolloutVersionBackfillPersistence(t *testing.T) {
	t.Parallel()
	clock := clocktesting.NewFakeClock(time.Date(2026, 5, 6, 7, 8, 9, 0, time.UTC))
	rid, err := fleetapihelpers.ToControlPlaneVersionRolloutResourceID("stable-4.21")
	require.NoError(t, err)
	best := semver.MustParse("4.21.1")
	legacy := &fleetapi.ControlPlaneVersionRollout{
		CosmosMetadata: coreapi.CosmosMetadata{ResourceID: rid, PartitionKey: strings.ToLower(coreapi.ProviderNamespace), InstanceVersion: 3},
		Spec:           fleetapi.ControlPlaneVersionRolloutSpec{BestExactVersion: &best},
		Status:         fleetapi.ControlPlaneVersionRolloutStatus{ClusterCountByDesiredExactVersion: map[string]int64{"4.21.1": 2}},
	}
	doc, err := cosmosstorageutils.InternalToCosmosGeneric(legacy)
	require.NoError(t, err)
	doc.CosmosETag = "legacy-etag"
	data, err := json.Marshal(doc)
	require.NoError(t, err)
	// Persist the historical representation with the field absent.
	var raw map[string]any
	require.NoError(t, json.Unmarshal(data, &raw))
	delete(raw["properties"].(map[string]any)["spec"].(map[string]any), "version")
	data, err = json.Marshal(raw)
	require.NoError(t, err)
	db := fleetcosmosstoragetesting.NewMockFleetDBClient()
	db.StoreDocument(doc.ID, data)
	crud := db.ControlPlaneVersionRollouts()
	existing, err := crud.Get(t.Context(), rid.Name)
	require.NoError(t, err)
	require.Empty(t, cmp.Diff(coreapi.VersionProfile{}, existing.Spec.Version), "decoding must leave backfill detectable")
	// A legacy writer can update selection and status before the seeder runs.
	writer := existing.DeepCopy()
	writer.Spec.BestExactVersion.Patch = 2
	writer.Status.ClusterCountByDesiredExactVersion["4.21.1"] = 3
	writer.Status.LastAssignmentTime = &metav1.Time{Time: clock.Now()}
	writer.Status.Conditions = []metav1.Condition{{Type: "Progressing", Status: metav1.ConditionTrue, LastTransitionTime: metav1.Time{Time: clock.Now()}, Reason: "Canary"}}
	updated, err := crud.Replace(t.Context(), writer, existing, nil)
	require.NoError(t, err)
	require.Empty(t, cmp.Diff(writer.Spec, updated.Spec))
	require.Empty(t, cmp.Diff(writer.Status, updated.Status))
	desired, err := fleetapihelpers.NormalizeRolloutVersion(existing)
	require.NoError(t, err)
	_, err = crud.Replace(t.Context(), desired, existing, nil)
	require.True(t, cosmosstorageutils.IsPreconditionFailedError(err), "stale migration must not overwrite selection/status: %v", err)
	desired, err = fleetapihelpers.NormalizeRolloutVersion(updated)
	require.NoError(t, err)
	backfilled, err := crud.Replace(t.Context(), desired, updated, nil)
	require.NoError(t, err)
	// A writer whose informer still has the legacy shape reaches the ETag check.
	stale := updated.DeepCopy()
	stale.Spec.BestExactVersion.Patch = 3
	stale.Status.ClusterCountByDesiredExactVersion["4.21.1"] = 4
	_, err = crud.Replace(t.Context(), stale, updated, nil)
	require.True(t, cosmosstorageutils.IsPreconditionFailedError(err), "stale legacy writer must conflict after backfill: %v", err)
	cleared := backfilled.DeepCopy()
	cleared.Spec.Version = coreapi.VersionProfile{}
	_, err = crud.Replace(t.Context(), cleared, backfilled, nil)
	require.ErrorContains(t, err, "replace validation failed", "a current writer cannot remove the profile")
	read, err := crud.Get(t.Context(), rid.Name)
	require.NoError(t, err)
	require.Empty(t, cmp.Diff(backfilled, read, cmp.AllowUnexported(azcorearm.ResourceID{}, azcorearm.ResourceType{})))
	require.Empty(t, cmp.Diff(writer.Spec.BestExactVersion, read.Spec.BestExactVersion))
	require.Empty(t, cmp.Diff(writer.Status, read.Status))
	desired, err = fleetapihelpers.NormalizeRolloutVersion(read)
	require.NoError(t, err)
	require.Nil(t, desired)
}
