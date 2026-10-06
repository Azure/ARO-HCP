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

package cosmosstorageutils_test

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

func TestCosmosGenericToInternalRolloutVersion(t *testing.T) {
	t.Parallel()
	clock := clocktesting.NewFakeClock(time.Date(2026, 5, 6, 7, 8, 9, 0, time.UTC))
	best := semver.MustParse("4.21.1")
	for _, tc := range []struct {
		name           string
		channel        string
		version        coreapi.VersionProfile
		want           coreapi.VersionProfile
		envelopeOnlyID bool
	}{
		{name: "legacy stable", channel: "stable-4.21", want: coreapi.VersionProfile{ID: "4.21", ChannelGroup: "stable"}},
		{name: "legacy nightly", channel: "nightly-4.21", want: coreapi.VersionProfile{ID: "4.21", ChannelGroup: "nightly"}},
		{name: "already normalized", channel: "stable-4.21", version: coreapi.VersionProfile{ID: "4.21", ChannelGroup: "stable"}, want: coreapi.VersionProfile{ID: "4.21", ChannelGroup: "stable"}},
		{name: "envelope-only resource ID", channel: "stable-4.21", envelopeOnlyID: true, want: coreapi.VersionProfile{ID: "4.21", ChannelGroup: "stable"}},
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
			want := rollout.DeepCopy()
			want.Spec.Version = tc.want
			doc, err := cosmosstorageutils.InternalToCosmosGeneric(rollout)
			require.NoError(t, err)
			doc.ID = rollout.ExistingCosmosUID
			doc.CosmosETag = rollout.CosmosETag
			// Read metadata comes from the envelope, not the embedded content.
			doc.Content.CosmosETag = ""
			doc.Content.ExistingCosmosUID = ""
			doc.Content.PartitionKey = ""
			if tc.envelopeOnlyID {
				doc.Content.ResourceID = nil
			}
			converted, err := cosmosstorageutils.CosmosGenericToInternal(doc)
			require.NoError(t, err)
			require.Empty(t, cmp.Diff(want, converted, cmp.AllowUnexported(azcorearm.ResourceID{}, azcorearm.ResourceType{})), "conversion must preserve selection, status, and metadata")
			err = fleetapihelpers.ValidateRolloutVersion(converted)
			require.NoError(t, err)
			second, err := cosmosstorageutils.CosmosGenericToInternal(doc)
			require.NoError(t, err)
			require.Empty(t, cmp.Diff(want, second, cmp.AllowUnexported(azcorearm.ResourceID{}, azcorearm.ResourceType{})), "normalization must be idempotent")
		})
	}
	t.Run("nil document", func(t *testing.T) {
		converted, err := cosmosstorageutils.CosmosGenericToInternal[fleetapi.ControlPlaneVersionRollout](nil)
		require.NoError(t, err)
		require.Nil(t, converted)
	})
	t.Run("missing resource ID", func(t *testing.T) {
		converted, err := cosmosstorageutils.CosmosGenericToInternal(&cosmosstorageutils.GenericDocument[fleetapi.ControlPlaneVersionRollout]{})
		require.ErrorContains(t, err, "missing a resourceID")
		require.Nil(t, converted)
	})
}

func TestRolloutVersionNormalizationPersistence(t *testing.T) {
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
	require.Empty(t, cmp.Diff(coreapi.VersionProfile{ID: "4.21", ChannelGroup: "stable"}, existing.Spec.Version), "shared Get must normalize legacy identity")
	stored, ok := db.GetDocument(doc.ID)
	require.True(t, ok)
	require.Equal(t, json.RawMessage(data), stored, "Get must not rewrite the raw document")
	// An ordinary writer persists the normalized profile with its selection/status update.
	writer := existing.DeepCopy()
	writer.Spec.BestExactVersion.Patch = 2
	writer.Status.ClusterCountByDesiredExactVersion["4.21.1"] = 3
	writer.Status.LastAssignmentTime = &metav1.Time{Time: clock.Now()}
	writer.Status.Conditions = []metav1.Condition{{Type: "Progressing", Status: metav1.ConditionTrue, LastTransitionTime: metav1.Time{Time: clock.Now()}, Reason: "Canary"}}
	updated, err := crud.Replace(t.Context(), writer, existing, nil)
	require.NoError(t, err)
	require.Empty(t, cmp.Diff(writer.Spec, updated.Spec))
	require.Empty(t, cmp.Diff(writer.Status, updated.Status))
	stored, ok = db.GetDocument(doc.ID)
	require.True(t, ok)
	var persisted cosmosstorageutils.GenericDocument[fleetapi.ControlPlaneVersionRollout]
	require.NoError(t, json.Unmarshal(stored, &persisted))
	require.Empty(t, cmp.Diff(existing.Spec.Version, persisted.Content.Spec.Version), "Replace must persist the normalized profile")
	// A writer holding the earlier normalized read still reaches the ETag check.
	stale := existing.DeepCopy()
	stale.Spec.BestExactVersion.Patch = 3
	stale.Status.ClusterCountByDesiredExactVersion["4.21.1"] = 4
	_, err = crud.Replace(t.Context(), stale, existing, nil)
	require.True(t, cosmosstorageutils.IsPreconditionFailedError(err), "stale writer must not overwrite selection/status: %v", err)
	cleared := updated.DeepCopy()
	cleared.Spec.Version = coreapi.VersionProfile{}
	_, err = crud.Replace(t.Context(), cleared, updated, nil)
	require.ErrorContains(t, err, "replace validation failed", "a current writer cannot remove the profile")
	read, err := crud.Get(t.Context(), rid.Name)
	require.NoError(t, err)
	require.Empty(t, cmp.Diff(updated, read, cmp.AllowUnexported(azcorearm.ResourceID{}, azcorearm.ResourceType{})))
	require.Empty(t, cmp.Diff(writer.Spec.BestExactVersion, read.Spec.BestExactVersion))
	require.Empty(t, cmp.Diff(writer.Status, read.Status))
}
