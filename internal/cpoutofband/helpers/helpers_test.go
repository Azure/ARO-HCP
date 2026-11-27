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

package helpers

import (
	"context"
	"strings"
	"testing"

	"github.com/blang/semver/v4"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	azcorearm "github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"

	"github.com/Azure/ARO-HCP/arohcp-cosmos-client/api/coreapi"
	"github.com/Azure/ARO-HCP/arohcp-cosmos-client/api/fleetapi"
	"github.com/Azure/ARO-HCP/arohcp-cosmos-client/api/metadataapi"
	"github.com/Azure/ARO-HCP/arohcp-cosmos-client/apihelpers/fleetapihelpers"
	"github.com/Azure/ARO-HCP/internal/cpoutofband"
	"github.com/Azure/ARO-HCP/internal/database/cosmosstoragetesting/fleetcosmosstoragetesting"
	"github.com/Azure/ARO-HCP/internal/database/listertesting/fleetlistertesting"
)

func TestRecognizedVersionForOutOfBandPin(t *testing.T) {
	t.Parallel()

	const outOfBandVersion = "4.19.99"
	cpoutofband.ConfigureControlPlaneVersions(map[string]string{outOfBandVersion: "quay.io/openshift-release-dev/ocp-release-priv@sha256:0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"})
	t.Cleanup(func() { cpoutofband.ConfigureControlPlaneVersions(nil) })

	ctx := context.Background()

	t.Run("nightly cluster is told its exact nightly build, not a channel best", func(t *testing.T) {
		t.Parallel()

		const nightly = "4.19.0-0.nightly-2026-11-20-123456"
		cluster := newTestClusterForChannelBest("nightly-cluster", "nightly", "4.19")
		cluster.ServiceProviderProperties.ExperimentalFeatures.ControlPlaneExactVersion = vForChannelBest(nightly)
		// The store has no nightly rollout at all: nightly builds are not
		// published to the Cincinnati graph, so no best version is ever
		// selected for a nightly channel. The exact nightly build must be
		// returned without consulting the rollout.
		_, rolloutLister := newTestRolloutStoreForChannelBest(t)

		got, err := RecognizedVersionForOutOfBandPin(ctx, rolloutLister, cluster, *vForChannelBest(outOfBandVersion))
		require.NoError(t, err)
		assert.Equal(t, nightly, got.String(), "a nightly cluster must be told the exact nightly build set on it")
	})

	t.Run("nightly cluster without an exact nightly build errors", func(t *testing.T) {
		t.Parallel()

		cluster := newTestClusterForChannelBest("nightly-cluster", "nightly", "4.19")
		_, rolloutLister := newTestRolloutStoreForChannelBest(t)

		_, err := RecognizedVersionForOutOfBandPin(ctx, rolloutLister, cluster, *vForChannelBest(outOfBandVersion))
		require.Error(t, err, "a nightly cluster with no exact nightly build has no recognized version to tell Cluster Service")
	})

	t.Run("nightly cluster whose exact nightly build is itself out of band errors", func(t *testing.T) {
		t.Parallel()

		cluster := newTestClusterForChannelBest("nightly-cluster", "nightly", "4.19")
		cluster.ServiceProviderProperties.ExperimentalFeatures.ControlPlaneExactVersion = vForChannelBest(outOfBandVersion)
		_, rolloutLister := newTestRolloutStoreForChannelBest(t)

		_, err := RecognizedVersionForOutOfBandPin(ctx, rolloutLister, cluster, *vForChannelBest(outOfBandVersion))
		require.Error(t, err, "an out-of-band version must never be told to Cluster Service, even as the nightly substitute")
	})

	t.Run("non-nightly cluster is told the channel's best exact version", func(t *testing.T) {
		t.Parallel()

		const best = "4.19.120"
		cluster := newTestClusterForChannelBest("fast-cluster", "fast", "4.19")
		_, rolloutLister := newTestRolloutStoreForChannelBest(t, newTestRolloutForChannelBest("fast-4.19", vForChannelBest(best), fleetapi.ControlPlaneVersionRolloutStatus{}))

		got, err := RecognizedVersionForOutOfBandPin(ctx, rolloutLister, cluster, *vForChannelBest(outOfBandVersion))
		require.NoError(t, err)
		assert.Equal(t, best, got.String(), "a non-nightly cluster must be told its channel's best exact version")
	})

	t.Run("non-nightly cluster without a selected best errors", func(t *testing.T) {
		t.Parallel()

		cluster := newTestClusterForChannelBest("fast-cluster", "fast", "4.19")
		_, rolloutLister := newTestRolloutStoreForChannelBest(t)

		_, err := RecognizedVersionForOutOfBandPin(ctx, rolloutLister, cluster, *vForChannelBest(outOfBandVersion))
		require.Error(t, err)
		assert.Contains(t, err.Error(), "fast-4.19")
	})

	t.Run("non-nightly cluster with rollout but no best exact version errors", func(t *testing.T) {
		t.Parallel()

		cluster := newTestClusterForChannelBest("fast-cluster", "fast", "4.19")
		_, rolloutLister := newTestRolloutStoreForChannelBest(t, newTestRolloutForChannelBest("fast-4.19", nil, fleetapi.ControlPlaneVersionRolloutStatus{}))

		_, err := RecognizedVersionForOutOfBandPin(ctx, rolloutLister, cluster, *vForChannelBest(outOfBandVersion))
		require.Error(t, err)
		assert.Contains(t, err.Error(), "has no best exact version")
	})

	t.Run("non-nightly cluster with active channel pin returns pin, not best", func(t *testing.T) {
		t.Parallel()

		const pin = "4.19.30"
		const best = "4.19.120"
		cluster := newTestClusterForChannelBest("fast-cluster", "fast", "4.19")
		rollout := newTestRolloutForChannelBest("fast-4.19", vForChannelBest(best), fleetapi.ControlPlaneVersionRolloutStatus{})
		rollout.Spec.PinnedVersion = fleetapi.ServiceProviderClusterPinnedVersion{ExactVersion: vForChannelBest(pin)}
		_, rolloutLister := newTestRolloutStoreForChannelBest(t, rollout)

		got, err := RecognizedVersionForOutOfBandPin(ctx, rolloutLister, cluster, *vForChannelBest(outOfBandVersion))
		require.NoError(t, err)
		assert.Equal(t, pin, got.String(), "active channel pin must override best exact version")
	})

	t.Run("non-nightly cluster with active channel pin until best overtakes", func(t *testing.T) {
		t.Parallel()

		const pin = "4.19.30"
		const until = "4.19.50"
		const best = "4.19.40"
		cluster := newTestClusterForChannelBest("fast-cluster", "fast", "4.19")
		rollout := newTestRolloutForChannelBest("fast-4.19", vForChannelBest(best), fleetapi.ControlPlaneVersionRolloutStatus{})
		rollout.Spec.PinnedVersion = fleetapi.ServiceProviderClusterPinnedVersion{ExactVersion: vForChannelBest(pin), UntilExactVersion: vForChannelBest(until)}
		_, rolloutLister := newTestRolloutStoreForChannelBest(t, rollout)

		got, err := RecognizedVersionForOutOfBandPin(ctx, rolloutLister, cluster, *vForChannelBest(outOfBandVersion))
		require.NoError(t, err)
		assert.Equal(t, pin, got.String(), "pin must stay active while best <= until")
	})

	t.Run("non-nightly cluster with expired channel pin falls back to best", func(t *testing.T) {
		t.Parallel()

		const pin = "4.19.30"
		const until = "4.19.50"
		const best = "4.19.60"
		cluster := newTestClusterForChannelBest("fast-cluster", "fast", "4.19")
		rollout := newTestRolloutForChannelBest("fast-4.19", vForChannelBest(best), fleetapi.ControlPlaneVersionRolloutStatus{})
		rollout.Spec.PinnedVersion = fleetapi.ServiceProviderClusterPinnedVersion{ExactVersion: vForChannelBest(pin), UntilExactVersion: vForChannelBest(until)}
		_, rolloutLister := newTestRolloutStoreForChannelBest(t, rollout)

		got, err := RecognizedVersionForOutOfBandPin(ctx, rolloutLister, cluster, *vForChannelBest(outOfBandVersion))
		require.NoError(t, err)
		assert.Equal(t, best, got.String(), "expired pin (best > until) must fall back to best")
	})

	t.Run("non-nightly cluster with active channel pin that is out-of-band errors", func(t *testing.T) {
		t.Parallel()

		cluster := newTestClusterForChannelBest("fast-cluster", "fast", "4.19")
		rollout := newTestRolloutForChannelBest("fast-4.19", vForChannelBest("4.19.120"), fleetapi.ControlPlaneVersionRolloutStatus{})
		rollout.Spec.PinnedVersion = fleetapi.ServiceProviderClusterPinnedVersion{ExactVersion: vForChannelBest(outOfBandVersion)}
		_, rolloutLister := newTestRolloutStoreForChannelBest(t, rollout)

		_, err := RecognizedVersionForOutOfBandPin(ctx, rolloutLister, cluster, *vForChannelBest(outOfBandVersion))
		require.Error(t, err)
		assert.Contains(t, err.Error(), "is itself out of band")
		assert.Contains(t, err.Error(), "fast-4.19")
	})

	t.Run("non-nightly cluster with active channel pin and no best returns pin", func(t *testing.T) {
		t.Parallel()

		const pin = "4.19.30"
		cluster := newTestClusterForChannelBest("fast-cluster", "fast", "4.19")
		rollout := newTestRolloutForChannelBest("fast-4.19", nil, fleetapi.ControlPlaneVersionRolloutStatus{})
		rollout.Spec.PinnedVersion = fleetapi.ServiceProviderClusterPinnedVersion{ExactVersion: vForChannelBest(pin)}
		_, rolloutLister := newTestRolloutStoreForChannelBest(t, rollout)

		got, err := RecognizedVersionForOutOfBandPin(ctx, rolloutLister, cluster, *vForChannelBest(outOfBandVersion))
		require.NoError(t, err)
		assert.Equal(t, pin, got.String(), "active pin with no best must still win")
	})
}

func vForChannelBest(s string) *semver.Version {
	parsed := semver.MustParse(s)
	return &parsed
}

func newTestClusterForChannelBest(name, channelGroup, versionID string) *coreapi.Cluster {
	id := metadataapi.Must(azcorearm.ParseResourceID(
		"/subscriptions/" + testSubscriptionID +
			"/resourceGroups/" + testResourceGroupName +
			"/providers/Microsoft.RedHatOpenShift/hcpOpenShiftClusters/" + name,
	))
	return &coreapi.Cluster{
		CosmosMetadata: coreapi.CosmosMetadata{
			ResourceID:   id,
			PartitionKey: strings.ToLower(id.SubscriptionID),
		},
		TrackedResource: coreapi.TrackedResource{
			Resource: coreapi.Resource{ID: id, Name: name, Type: id.ResourceType.String()},
		},
		CustomerProperties: coreapi.ClusterCustomerProperties{
			Version: coreapi.VersionProfile{ID: versionID, ChannelGroup: channelGroup},
		},
	}
}

func newTestRolloutForChannelBest(yStreamChannel string, best *semver.Version, status fleetapi.ControlPlaneVersionRolloutStatus) *fleetapi.ControlPlaneVersionRollout {
	id := metadataapi.Must(fleetapihelpers.ToControlPlaneVersionRolloutResourceID(yStreamChannel))
	profile := metadataapi.Must(fleetapihelpers.RolloutVersionFromName(yStreamChannel))
	return &fleetapi.ControlPlaneVersionRollout{
		CosmosMetadata: coreapi.CosmosMetadata{
			ResourceID:   id,
			PartitionKey: strings.ToLower(coreapi.ProviderNamespace),
		},
		Spec:   fleetapi.ControlPlaneVersionRolloutSpec{Version: profile, BestExactVersion: best},
		Status: status,
	}
}

func newTestRolloutStoreForChannelBest(t *testing.T, rollouts ...*fleetapi.ControlPlaneVersionRollout) (*fleetcosmosstoragetesting.MockFleetDBClient, *fleetlistertesting.DBControlPlaneVersionRolloutLister) {
	t.Helper()
	resources := make([]any, 0, len(rollouts))
	for _, r := range rollouts {
		resources = append(resources, r)
	}
	mockFleet, err := fleetcosmosstoragetesting.NewMockFleetDBClientWithResources(context.Background(), resources)
	require.NoError(t, err, "failed to build mock fleet DB client")
	return mockFleet, &fleetlistertesting.DBControlPlaneVersionRolloutLister{FleetDBClient: mockFleet}
}

const (
	testSubscriptionID    = "00000000-0000-0000-0000-000000000000"
	testResourceGroupName = "test-rg"
)
