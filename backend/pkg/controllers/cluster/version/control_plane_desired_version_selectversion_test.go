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
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/blang/semver/v4"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"

	"k8s.io/utils/ptr"

	configv1 "github.com/openshift/api/config/v1"

	"github.com/Azure/ARO-HCP/internal/api/coreapi"
	"github.com/Azure/ARO-HCP/internal/api/metadataapi"
	"github.com/Azure/ARO-HCP/internal/cincinnati"
)

// graphResponseRoundTripper returns a RoundTrip that always responds with the given Cincinnati
// graph JSON body and HTTP 200. It records whether it was invoked so a test can assert that the
// graph path was (or was not) taken.
func graphResponseRoundTripper(body string, called *bool) func(*http.Request) (*http.Response, error) {
	return func(*http.Request) (*http.Response, error) {
		if called != nil {
			*called = true
		}
		return &http.Response{
			StatusCode: http.StatusOK,
			Body:       io.NopCloser(strings.NewReader(body)),
		}, nil
	}
}

// TestDesiredControlPlaneZVersion_ZStreamUsesSelectControlPlaneVersion verifies that, for a
// non-nightly channel with a configured roundTripper, the z-stream case resolves the desired version
// via controlplaneversion.SelectControlPlaneVersion (tip of the channel), independent of the
// internal/cincinnati gateway logic.
func TestDesiredControlPlaneZVersion_ZStreamUsesSelectControlPlaneVersion(t *testing.T) {
	ctrl := gomock.NewController(t)

	// Nodes intentionally out of order to prove SelectControlPlaneVersion sorts descending and
	// returns the tip (offset 0) -> 4.20.5.
	graphJSON := `{"nodes":[` +
		`{"version":"4.20.1","payload":"quay.io/openshift-release-dev/ocp-release:4.20.1-multi"},` +
		`{"version":"4.20.5","payload":"quay.io/openshift-release-dev/ocp-release:4.20.5-multi"},` +
		`{"version":"4.20.3","payload":"quay.io/openshift-release-dev/ocp-release:4.20.3-multi"}` +
		`]}`
	var graphCalled bool

	syncer := &controlPlaneUpgradeVersionSyncer{
		desiredVersionSyncerCommon: desiredVersionSyncerCommon{
			graphClient: mockGraphClient(ctrl, nil),
		},
		roundTripper: graphResponseRoundTripper(graphJSON, &graphCalled),
	}

	// cincinnatiClient must NOT be used on the SelectControlPlaneVersion path; give it no expectations.
	result, err := syncer.upgradeDesiredControlPlaneZVersion(
		context.Background(),
		cincinnati.NewMockClient(ctrl),
		metadataapi.Must(coreapi.ToClusterResourceID("6b690bec-0c16-4ecb-8f67-781caf40bba7", "test-rg", "test-cluster")),
		"4.20",
		"candidate",
		[]coreapi.HCPClusterActiveVersion{{Version: ptr.To(semver.MustParse("4.20.1")), State: configv1.CompletedUpdate}},
		false,
	)
	require.NoError(t, err, "expected SelectControlPlaneVersion path to succeed")
	require.NotNil(t, result, "expected a resolved desired version")
	assert.Equal(t, "4.20.5", result.String(), "expected the tip of candidate-4.20")
	assert.True(t, graphCalled, "expected the graph API round tripper to be invoked")
}

// TestDesiredControlPlaneZVersion_NightlyGuardSkipsSelectControlPlaneVersion verifies the nightly
// channel guard: nightly must not use the graph-based SelectControlPlaneVersion path (the graph API
// does not serve nightly) and instead falls back to the internal/cincinnati gateway logic.
func TestDesiredControlPlaneZVersion_NightlyGuardSkipsSelectControlPlaneVersion(t *testing.T) {
	ctrl := gomock.NewController(t)

	// If the guard failed and SelectControlPlaneVersion were used, this round tripper would be hit
	// and error; the gateway logic (mocked below) does not use it.
	var graphCalled bool
	roundTripper := func(*http.Request) (*http.Response, error) {
		graphCalled = true
		return nil, fmt.Errorf("SelectControlPlaneVersion must not be called for the nightly channel")
	}

	activeVer := metadataapi.Must(semver.ParseTolerant("4.19.0-0.nightly-multi-2026-01-10-204154"))
	mockClient := cincinnati.NewMockClient(ctrl)
	mockClient.EXPECT().GetUpdates(gomock.AssignableToTypeOf(context.Background()), metadataapi.Must(cincinnati.GetCincinnatiURI("nightly")), "multi", "multi", "nightly-4.19", activeVer).Return(
		configv1.Release{Version: "4.19.0-0.nightly-multi-2026-01-10-204154"},
		[]configv1.Release{{Version: "4.19.0-0.nightly-multi-2026-01-12-061259"}},
		[]configv1.ConditionalUpdate{},
		nil,
	)

	syncer := &controlPlaneUpgradeVersionSyncer{
		desiredVersionSyncerCommon: desiredVersionSyncerCommon{
			graphClient: mockGraphClient(ctrl, channelExistence{"nightly": {"4.20": false}}),
		},
		roundTripper: roundTripper,
	}

	result, err := syncer.upgradeDesiredControlPlaneZVersion(
		context.Background(),
		mockClient,
		metadataapi.Must(coreapi.ToClusterResourceID("6b690bec-0c16-4ecb-8f67-781caf40bba7", "test-rg", "test-cluster")),
		"4.19.0-0.nightly-multi-2026-01-12-061259",
		nightlyChannelGroup,
		[]coreapi.HCPClusterActiveVersion{{Version: ptr.To(activeVer), State: configv1.CompletedUpdate}},
		false,
	)
	require.NoError(t, err, "expected the nightly gateway fallback to succeed")
	require.NotNil(t, result, "expected a resolved desired version")
	assert.Equal(t, "4.19.0-0.nightly-multi-2026-01-12-061259", result.String(), "expected the gateway-selected nightly version")
	assert.False(t, graphCalled, "expected the nightly guard to skip the graph API round tripper")
}
