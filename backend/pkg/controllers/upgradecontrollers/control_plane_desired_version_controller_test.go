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

package upgradecontrollers

import (
	"context"
	"testing"

	"github.com/blang/semver/v4"
	"github.com/go-logr/logr"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"

	apimeta "k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/utils/ptr"

	azcorearm "github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"

	configv1 "github.com/openshift/api/config/v1"
	cvocincinnati "github.com/openshift/cluster-version-operator/pkg/cincinnati"

	"github.com/Azure/ARO-HCP/backend/pkg/controllers/controllerutils"
	"github.com/Azure/ARO-HCP/backend/pkg/listertesting"
	"github.com/Azure/ARO-HCP/internal/api"
	"github.com/Azure/ARO-HCP/internal/api/arm"
	"github.com/Azure/ARO-HCP/internal/cincinnati"
	"github.com/Azure/ARO-HCP/internal/databasetesting"
	"github.com/Azure/ARO-HCP/internal/ocm"
	"github.com/Azure/ARO-HCP/internal/utils"
)

func TestDesiredControlPlaneZVersion_ZStreamManagedUpgrade(t *testing.T) {
	tests := []struct {
		name                  string
		activeVersions        []api.HCPClusterActiveVersion
		customerDesiredMinor  string
		channelGroup          string
		mockSetup             func(*cincinnati.MockClient)
		expectedVersion       *semver.Version
		expectedError         bool
		expectedErrorContains string
	}{
		{
			name:                 "Z-stream upgrade - finds latest gateway",
			activeVersions:       []api.HCPClusterActiveVersion{{Version: ptr.To(semver.MustParse("4.19.15")), State: configv1.CompletedUpdate}},
			customerDesiredMinor: "4.19",
			channelGroup:         "stable",
			mockSetup: func(mc *cincinnati.MockClient) {
				// Cincinnati may return versions from other minors which should be filtered out
				mc.EXPECT().GetUpdates(gomock.AssignableToTypeOf(context.Background()), api.Must(cincinnati.GetCincinnatiURI("stable")), "multi", "multi", "stable-4.19", semver.MustParse("4.19.15")).Return(
					configv1.Release{Version: "4.19.15"},
					[]configv1.Release{{Version: "4.19.18"}, {Version: "4.19.22"}, {Version: "4.20.5"}}, // 4.20.5 should be filtered out
					[]configv1.ConditionalUpdate{},
					nil,
				)

				// Check if next minor (4.20) exists using latest candidate (4.19.22) and if 4.19.22 is a gateway to 4.20 (called twice)
				mc.EXPECT().GetUpdates(gomock.AssignableToTypeOf(context.Background()), api.Must(cincinnati.GetCincinnatiURI("stable")), "multi", "multi", "stable-4.20", semver.MustParse("4.19.22")).Times(2).Return(
					configv1.Release{Version: "4.19.22"},
					[]configv1.Release{{Version: "4.20.5"}},
					[]configv1.ConditionalUpdate{},
					nil,
				)
			},
			expectedVersion: ptr.To(semver.MustParse("4.19.22")),
			expectedError:   false,
		},
		{
			name:                 "Z-stream upgrade - already at latest",
			activeVersions:       []api.HCPClusterActiveVersion{{Version: ptr.To(semver.MustParse("4.19.22")), State: configv1.CompletedUpdate}},
			customerDesiredMinor: "4.19",
			channelGroup:         "stable",
			mockSetup: func(mc *cincinnati.MockClient) {
				mc.EXPECT().GetUpdates(gomock.AssignableToTypeOf(context.Background()), api.Must(cincinnati.GetCincinnatiURI("stable")), "multi", "multi", "stable-4.19", semver.MustParse("4.19.22")).Return(
					configv1.Release{Version: "4.19.22"},
					[]configv1.Release{}, // No newer versions
					[]configv1.ConditionalUpdate{},
					nil,
				)
			},
			expectedVersion: nil,
			expectedError:   false,
		},
		{
			name:                 "Z-stream upgrade - actual has edge to next minor, no gateway in candidates",
			activeVersions:       []api.HCPClusterActiveVersion{{Version: ptr.To(semver.MustParse("4.19.15")), State: configv1.CompletedUpdate}},
			customerDesiredMinor: "4.19",
			channelGroup:         "stable",
			mockSetup: func(mc *cincinnati.MockClient) {
				// Query for 4.19 versions from 4.19.15
				mc.EXPECT().GetUpdates(gomock.AssignableToTypeOf(context.Background()), api.Must(cincinnati.GetCincinnatiURI("stable")), "multi", "multi", "stable-4.19", semver.MustParse("4.19.15")).Return(
					configv1.Release{Version: "4.19.15"},
					[]configv1.Release{{Version: "4.19.20"}}, // Latest, but no gateway to 4.20
					[]configv1.ConditionalUpdate{},
					nil,
				)

				// Check if next minor (4.20) exists using latest candidate (4.19.20) and if 4.19.20 is a gateway to 4.20 (called twice)
				mc.EXPECT().GetUpdates(gomock.AssignableToTypeOf(context.Background()), api.Must(cincinnati.GetCincinnatiURI("stable")), "multi", "multi", "stable-4.20", semver.MustParse("4.19.20")).Times(2).Return(
					configv1.Release{Version: "4.19.20"},
					[]configv1.Release{}, // No path to 4.20
					[]configv1.ConditionalUpdate{},
					nil,
				)
			},
			expectedVersion: nil, // No upgrade - would break existing path
			expectedError:   false,
		},
		{
			name:                 "Z-stream upgrade - multiple active versions, only common candidates considered",
			customerDesiredMinor: "4.19",
			channelGroup:         "stable",
			activeVersions: []api.HCPClusterActiveVersion{
				{Version: ptr.To(semver.MustParse("4.19.12")), State: configv1.CompletedUpdate}, // Most recent
				{Version: ptr.To(semver.MustParse("4.19.10")), State: configv1.CompletedUpdate}, // Older active version
			},
			mockSetup: func(mc *cincinnati.MockClient) {
				// Query for 4.19 versions from 4.19.12 (most recent active version)
				mc.EXPECT().GetUpdates(gomock.AssignableToTypeOf(context.Background()), api.Must(cincinnati.GetCincinnatiURI("stable")), "multi", "multi", "stable-4.19", semver.MustParse("4.19.12")).Return(
					configv1.Release{Version: "4.19.12"},
					[]configv1.Release{{Version: "4.19.15"}, {Version: "4.19.18"}, {Version: "4.19.22"}}, // Reachable from 4.19.12
					[]configv1.ConditionalUpdate{},
					nil,
				)

				// Query for 4.19 versions from 4.19.10 (older active version)
				mc.EXPECT().GetUpdates(gomock.AssignableToTypeOf(context.Background()), api.Must(cincinnati.GetCincinnatiURI("stable")), "multi", "multi", "stable-4.19", semver.MustParse("4.19.10")).Return(
					configv1.Release{Version: "4.19.10"},
					[]configv1.Release{{Version: "4.19.15"}, {Version: "4.19.18"}}, // Reachable from 4.19.10. Note: 4.19.22 is NOT reachable
					[]configv1.ConditionalUpdate{},
					nil,
				)

				// Check if next minor (4.20) exists using latest common candidate (4.19.18) and if 4.19.18 is a gateway to 4.20 (called twice)
				mc.EXPECT().GetUpdates(gomock.AssignableToTypeOf(context.Background()), api.Must(cincinnati.GetCincinnatiURI("stable")), "multi", "multi", "stable-4.20", semver.MustParse("4.19.18")).Times(2).Return(
					configv1.Release{Version: "4.19.18"},
					[]configv1.Release{{Version: "4.20.5"}},
					[]configv1.ConditionalUpdate{},
					nil,
				)
			},
			expectedVersion: ptr.To(semver.MustParse("4.19.18")), // Latest common candidate that's a gateway
			expectedError:   false,
		},
		{
			name:                 "Z-stream upgrade - actual has NO edge to next minor, no gateway in candidates",
			activeVersions:       []api.HCPClusterActiveVersion{{Version: ptr.To(semver.MustParse("4.19.10")), State: configv1.CompletedUpdate}},
			customerDesiredMinor: "4.19",
			channelGroup:         "stable",
			mockSetup: func(mc *cincinnati.MockClient) {
				// Query for 4.19 versions from 4.19.10
				mc.EXPECT().GetUpdates(gomock.AssignableToTypeOf(context.Background()), api.Must(cincinnati.GetCincinnatiURI("stable")), "multi", "multi", "stable-4.19", semver.MustParse("4.19.10")).Return(
					configv1.Release{Version: "4.19.10"},
					[]configv1.Release{{Version: "4.19.18"}}, // Latest, but no gateway to 4.20
					[]configv1.ConditionalUpdate{},
					nil,
				)

				// Check if next minor (4.20) exists using latest candidate (4.19.18) - it doesn't
				mc.EXPECT().GetUpdates(gomock.AssignableToTypeOf(context.Background()), api.Must(cincinnati.GetCincinnatiURI("stable")), "multi", "multi", "stable-4.20", semver.MustParse("4.19.18")).Return(
					configv1.Release{},
					nil,
					nil,
					&cvocincinnati.Error{Reason: "VersionNotFound"}, // Next minor doesn't exist
				)

				// Since next minor doesn't exist, we return latest candidate (4.19.18)
			},
			expectedVersion: ptr.To(semver.MustParse("4.19.18")), // Safe to upgrade - no existing path to break
			expectedError:   false,
		},
		{
			name:                 "Z-stream upgrade - Cincinnati query error",
			activeVersions:       []api.HCPClusterActiveVersion{{Version: ptr.To(semver.MustParse("4.19.15")), State: configv1.CompletedUpdate}},
			customerDesiredMinor: "4.19",
			channelGroup:         "stable",
			mockSetup: func(mc *cincinnati.MockClient) {
				// Mock Cincinnati returning an error
				mc.EXPECT().GetUpdates(gomock.AssignableToTypeOf(context.Background()), api.Must(cincinnati.GetCincinnatiURI("stable")), "multi", "multi", "stable-4.19", semver.MustParse("4.19.15")).Return(
					configv1.Release{},
					nil,
					nil,
					&cvocincinnati.Error{Message: "example error message"},
				)
			},
			expectedVersion:       nil,
			expectedError:         true,
			expectedErrorContains: "example error message",
		},
		{
			name:                 "Z-stream upgrade - no desired minor version specified",
			activeVersions:       []api.HCPClusterActiveVersion{{Version: ptr.To(semver.MustParse("4.19.15")), State: configv1.CompletedUpdate}},
			customerDesiredMinor: "",
			channelGroup:         "stable",
			mockSetup:            func(mc *cincinnati.MockClient) {},
			expectedVersion:      nil,
			expectedError:        false,
		},
		{
			name:                 "Z-stream upgrade - no channel group specified",
			activeVersions:       []api.HCPClusterActiveVersion{{Version: ptr.To(semver.MustParse("4.19.15")), State: configv1.CompletedUpdate}},
			customerDesiredMinor: "4.19",
			channelGroup:         "",
			mockSetup:            func(mc *cincinnati.MockClient) {},
			expectedVersion:      nil,
			expectedError:        false,
		},
		{
			name:                 "Z-stream upgrade - candidate channel, customer desired full version (4.20.15) normalized to same minor",
			activeVersions:       []api.HCPClusterActiveVersion{{Version: ptr.To(semver.MustParse("4.20.10")), State: configv1.CompletedUpdate}},
			customerDesiredMinor: "4.20.15",
			channelGroup:         "candidate",
			mockSetup: func(mc *cincinnati.MockClient) {
				mc.EXPECT().GetUpdates(gomock.AssignableToTypeOf(context.Background()), api.Must(cincinnati.GetCincinnatiURI("candidate")), "multi", "multi", "candidate-4.20", semver.MustParse("4.20.10")).Return(
					configv1.Release{Version: "4.20.10"},
					[]configv1.Release{{Version: "4.20.15"}, {Version: "4.20.12"}},
					[]configv1.ConditionalUpdate{},
					nil,
				)
				// Check if next minor (4.21) exists using latest candidate (4.20.15)
				mc.EXPECT().GetUpdates(gomock.AssignableToTypeOf(context.Background()), api.Must(cincinnati.GetCincinnatiURI("candidate")), "multi", "multi", "candidate-4.21", semver.MustParse("4.20.15")).Return(
					configv1.Release{Version: "4.20.15"},
					[]configv1.Release{{Version: "4.21.0"}},
					[]configv1.ConditionalUpdate{},
					nil,
				)
				// isGatewayToNextMinor(4.20.15) - has path to 4.21, so 4.20.15 is selected
				mc.EXPECT().GetUpdates(gomock.AssignableToTypeOf(context.Background()), api.Must(cincinnati.GetCincinnatiURI("candidate")), "multi", "multi", "candidate-4.21", semver.MustParse("4.20.15")).Return(
					configv1.Release{Version: "4.20.15"},
					[]configv1.Release{{Version: "4.21.0"}},
					[]configv1.ConditionalUpdate{},
					nil,
				)
			},
			expectedVersion: ptr.To(semver.MustParse("4.20.15")),
			expectedError:   false,
		},
		{
			name:                 "Z-stream upgrade - nightly channel, customer desired full version (4.19.0-0.nightly-multi-...) normalized to same minor",
			activeVersions:       []api.HCPClusterActiveVersion{{Version: ptr.To(api.Must(semver.ParseTolerant("4.19.0-0.nightly-multi-2026-01-10-204154"))), State: configv1.CompletedUpdate}},
			customerDesiredMinor: "4.19.0-0.nightly-multi-2026-01-12-061259",
			channelGroup:         "nightly",
			mockSetup: func(mc *cincinnati.MockClient) {
				activeVer := api.Must(semver.ParseTolerant("4.19.0-0.nightly-multi-2026-01-10-204154"))
				latestVer := api.Must(semver.ParseTolerant("4.19.0-0.nightly-multi-2026-01-12-061259"))
				mc.EXPECT().GetUpdates(gomock.AssignableToTypeOf(context.Background()), api.Must(cincinnati.GetCincinnatiURI("nightly")), "multi", "multi", "nightly-4.19", activeVer).Return(
					configv1.Release{Version: "4.19.0-0.nightly-multi-2026-01-10-204154"},
					[]configv1.Release{{Version: "4.19.0-0.nightly-multi-2026-01-12-061259"}},
					[]configv1.ConditionalUpdate{},
					nil,
				)
				// Check if next minor (4.20) exists using latest candidate - it doesn't; return latest
				mc.EXPECT().GetUpdates(gomock.AssignableToTypeOf(context.Background()), api.Must(cincinnati.GetCincinnatiURI("nightly")), "multi", "multi", "nightly-4.20", latestVer).Return(
					configv1.Release{},
					nil,
					nil,
					&cvocincinnati.Error{Reason: "VersionNotFound"},
				)
			},
			expectedVersion: ptr.To(api.Must(semver.ParseTolerant("4.19.0-0.nightly-multi-2026-01-12-061259"))),
			expectedError:   false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctrl := gomock.NewController(t)

			mockCincinnatiClient := cincinnati.NewMockClient(ctrl)
			tt.mockSetup(mockCincinnatiClient)

			syncer := &controlPlaneDesiredVersionSyncer{resourcesDBClient: databasetesting.NewMockResourcesDBClient()}

			ctx := context.Background()
			result, err := syncer.desiredControlPlaneZVersion(ctx, mockCincinnatiClient, api.Must(api.ToClusterResourceID("6b690bec-0c16-4ecb-8f67-781caf40bba7", "test-rg", "test-cluster")), tt.customerDesiredMinor, tt.channelGroup, tt.activeVersions, false)

			assertVersionResult(t, result, err, tt.expectedVersion, tt.expectedError, tt.expectedErrorContains)
		})
	}
}

func TestDesiredControlPlaneZVersion_NextYStreamUpgrade(t *testing.T) {
	tests := []struct {
		name                  string
		activeVersions        []api.HCPClusterActiveVersion
		customerDesiredMinor  string
		channelGroup          string
		mockSetup             func(*cincinnati.MockClient)
		cosmosResources       []any
		expectedVersion       *semver.Version
		expectedError         bool
		expectedErrorContains string
	}{
		{
			name:                 "Y-stream upgrade - direct path available returns latest version with gateway to next minor",
			activeVersions:       []api.HCPClusterActiveVersion{{Version: ptr.To(semver.MustParse("4.19.22")), State: configv1.CompletedUpdate}},
			customerDesiredMinor: "4.20",
			channelGroup:         "stable",
			mockSetup: func(mc *cincinnati.MockClient) {
				// Query for 4.20 versions from 4.19.22
				// Cincinnati may return versions from other minors which should be filtered out
				mc.EXPECT().GetUpdates(gomock.AssignableToTypeOf(context.Background()), api.Must(cincinnati.GetCincinnatiURI("stable")), "multi", "multi", "stable-4.20", semver.MustParse("4.19.22")).Return(
					configv1.Release{Version: "4.19.22"},
					[]configv1.Release{{Version: "4.20.15"}, {Version: "4.20.10"}, {Version: "4.19.25"}}, // 4.19.25 should be filtered out
					[]configv1.ConditionalUpdate{},
					nil,
				)

				// Check if 4.20.15 (latest candidate) has gateway to 4.21
				// This is called twice: once to check if next minor exists, once to check gateway
				mc.EXPECT().GetUpdates(gomock.AssignableToTypeOf(context.Background()), api.Must(cincinnati.GetCincinnatiURI("stable")), "multi", "multi", "stable-4.21", semver.MustParse("4.20.15")).Return(
					configv1.Release{Version: "4.20.15"},
					[]configv1.Release{}, // No path to 4.21
					[]configv1.ConditionalUpdate{},
					nil,
				).Times(2)

				// Check if 4.20.10 has gateway to 4.21 - it does
				mc.EXPECT().GetUpdates(gomock.AssignableToTypeOf(context.Background()), api.Must(cincinnati.GetCincinnatiURI("stable")), "multi", "multi", "stable-4.21", semver.MustParse("4.20.10")).Return(
					configv1.Release{Version: "4.20.10"},
					[]configv1.Release{{Version: "4.21.0"}},
					[]configv1.ConditionalUpdate{},
					nil,
				)
			},
			expectedVersion: ptr.To(semver.MustParse("4.20.10")),
			expectedError:   false,
		},
		{
			name:                 "Y-stream upgrade - succeeds with node pool within skew versus desired minor",
			activeVersions:       []api.HCPClusterActiveVersion{{Version: ptr.To(semver.MustParse("4.19.22")), State: configv1.CompletedUpdate}},
			customerDesiredMinor: "4.20",
			channelGroup:         "stable",
			mockSetup: func(mc *cincinnati.MockClient) {
				// Query for 4.20 versions from 4.19.22
				// Cincinnati may return versions from other minors which should be filtered out
				mc.EXPECT().GetUpdates(gomock.AssignableToTypeOf(context.Background()), api.Must(cincinnati.GetCincinnatiURI("stable")), "multi", "multi", "stable-4.20", semver.MustParse("4.19.22")).Return(
					configv1.Release{Version: "4.19.22"},
					[]configv1.Release{{Version: "4.20.15"}, {Version: "4.20.10"}, {Version: "4.19.25"}}, // 4.19.25 should be filtered out
					[]configv1.ConditionalUpdate{},
					nil,
				)

				// Check if 4.20.15 (latest candidate) has gateway to 4.21
				// This is called twice: once to check if next minor exists, once to check gateway
				mc.EXPECT().GetUpdates(gomock.AssignableToTypeOf(context.Background()), api.Must(cincinnati.GetCincinnatiURI("stable")), "multi", "multi", "stable-4.21", semver.MustParse("4.20.15")).Return(
					configv1.Release{Version: "4.20.15"},
					[]configv1.Release{}, // No path to 4.21
					[]configv1.ConditionalUpdate{},
					nil,
				).Times(2)

				// Check if 4.20.10 has gateway to 4.21 - it does
				mc.EXPECT().GetUpdates(gomock.AssignableToTypeOf(context.Background()), api.Must(cincinnati.GetCincinnatiURI("stable")), "multi", "multi", "stable-4.21", semver.MustParse("4.20.10")).Return(
					configv1.Release{Version: "4.20.10"},
					[]configv1.Release{{Version: "4.21.0"}},
					[]configv1.ConditionalUpdate{},
					nil,
				)
			},
			cosmosResources: testCosmosClusterWithWorkersNodePoolAtVersion("4.18.0"),
			expectedVersion: ptr.To(semver.MustParse("4.20.10")),
			expectedError:   false,
		},
		{
			name:                 "Y-stream upgrade - no direct path, falls back to Z-stream",
			activeVersions:       []api.HCPClusterActiveVersion{{Version: ptr.To(semver.MustParse("4.19.15")), State: configv1.CompletedUpdate}},
			customerDesiredMinor: "4.20",
			channelGroup:         "stable",
			mockSetup: func(mc *cincinnati.MockClient) {
				// Query for 4.20 versions from 4.19.15 - no path
				mc.EXPECT().GetUpdates(gomock.AssignableToTypeOf(context.Background()), api.Must(cincinnati.GetCincinnatiURI("stable")), "multi", "multi", "stable-4.20", semver.MustParse("4.19.15")).Return(
					configv1.Release{Version: "4.19.15"},
					[]configv1.Release{}, // No direct path to 4.20
					[]configv1.ConditionalUpdate{},
					nil,
				)

				// Fallback to Z-stream in actual minor (4.19)
				mc.EXPECT().GetUpdates(gomock.AssignableToTypeOf(context.Background()), api.Must(cincinnati.GetCincinnatiURI("stable")), "multi", "multi", "stable-4.19", semver.MustParse("4.19.15")).Return(
					configv1.Release{Version: "4.19.15"},
					[]configv1.Release{{Version: "4.19.22"}, {Version: "4.19.18"}},
					[]configv1.ConditionalUpdate{},
					nil,
				)

				// Check if next minor (4.20) exists using latest candidate (4.19.22) and if 4.19.22 is a gateway to 4.20 (called twice)
				mc.EXPECT().GetUpdates(gomock.AssignableToTypeOf(context.Background()), api.Must(cincinnati.GetCincinnatiURI("stable")), "multi", "multi", "stable-4.20", semver.MustParse("4.19.22")).Times(2).Return(
					configv1.Release{Version: "4.19.22"},
					[]configv1.Release{{Version: "4.20.5"}},
					[]configv1.ConditionalUpdate{},
					nil,
				)
			},
			expectedVersion: ptr.To(semver.MustParse("4.19.22")),
			expectedError:   false,
		},
		{
			name:                 "Y-stream upgrade - multiple active versions, only common candidates considered",
			customerDesiredMinor: "4.20",
			channelGroup:         "stable",
			activeVersions: []api.HCPClusterActiveVersion{
				{Version: ptr.To(semver.MustParse("4.19.18")), State: configv1.CompletedUpdate}, // Most recent
				{Version: ptr.To(semver.MustParse("4.19.15")), State: configv1.CompletedUpdate}, // Older active version
			},
			mockSetup: func(mc *cincinnati.MockClient) {
				// Query for 4.20 versions from 4.19.18 (most recent active version)
				mc.EXPECT().GetUpdates(gomock.AssignableToTypeOf(context.Background()), api.Must(cincinnati.GetCincinnatiURI("stable")), "multi", "multi", "stable-4.20", semver.MustParse("4.19.18")).Return(
					configv1.Release{Version: "4.19.18"},
					[]configv1.Release{{Version: "4.20.8"}, {Version: "4.20.12"}, {Version: "4.20.15"}}, // Reachable from 4.19.18
					[]configv1.ConditionalUpdate{},
					nil,
				)

				// Query for 4.20 versions from 4.19.15 (older active version)
				mc.EXPECT().GetUpdates(gomock.AssignableToTypeOf(context.Background()), api.Must(cincinnati.GetCincinnatiURI("stable")), "multi", "multi", "stable-4.20", semver.MustParse("4.19.15")).Return(
					configv1.Release{Version: "4.19.15"},
					[]configv1.Release{{Version: "4.20.8"}, {Version: "4.20.12"}}, // Reachable from 4.19.15. Note: 4.20.15 is NOT reachable
					[]configv1.ConditionalUpdate{},
					nil,
				)

				// Check if next minor (4.21) exists using latest candidate (4.20.12) and if 4.20.12 is a gateway to 4.21 (called twice)
				// For Y-stream upgrades, actualMinor != targetMinor, so uses latest candidate
				mc.EXPECT().GetUpdates(gomock.AssignableToTypeOf(context.Background()), api.Must(cincinnati.GetCincinnatiURI("stable")), "multi", "multi", "stable-4.21", semver.MustParse("4.20.12")).Times(2).Return(
					configv1.Release{Version: "4.20.12"},
					[]configv1.Release{{Version: "4.21.3"}},
					[]configv1.ConditionalUpdate{},
					nil,
				)
			},
			expectedVersion: ptr.To(semver.MustParse("4.20.12")), // Latest common candidate with gateway to 4.21
			expectedError:   false,
		},
		{
			name:                 "Y-stream upgrade - no gateway found but returns latest anyway",
			activeVersions:       []api.HCPClusterActiveVersion{{Version: ptr.To(semver.MustParse("4.19.15")), State: configv1.CompletedUpdate}},
			customerDesiredMinor: "4.20",
			channelGroup:         "stable",
			mockSetup: func(mc *cincinnati.MockClient) {
				// Query for 4.20 versions from 4.19.15
				mc.EXPECT().GetUpdates(gomock.AssignableToTypeOf(context.Background()), api.Must(cincinnati.GetCincinnatiURI("stable")), "multi", "multi", "stable-4.20", semver.MustParse("4.19.15")).Return(
					configv1.Release{Version: "4.19.15"},
					[]configv1.Release{{Version: "4.20.12"}}, // Latest in 4.20, but no gateway to 4.21
					[]configv1.ConditionalUpdate{},
					nil,
				)

				// Check if next minor (4.21) exists using latest candidate (4.20.12)
				// For Y-stream upgrades, actualMinor != targetMinor, so uses latest candidate
				mc.EXPECT().GetUpdates(gomock.AssignableToTypeOf(context.Background()), api.Must(cincinnati.GetCincinnatiURI("stable")), "multi", "multi", "stable-4.21", semver.MustParse("4.20.12")).Return(
					configv1.Release{},
					nil,
					nil,
					&cvocincinnati.Error{Reason: "VersionNotFound"}, // Next minor doesn't exist yet
				)
			},
			expectedVersion: ptr.To(semver.MustParse("4.20.12")), // Returns latest even without gateway - user wants to be on 4.20
			expectedError:   false,
		},
		{
			name:                 "Y-stream upgrade - Cincinnati query error",
			activeVersions:       []api.HCPClusterActiveVersion{{Version: ptr.To(semver.MustParse("4.19.22")), State: configv1.CompletedUpdate}},
			customerDesiredMinor: "4.20",
			channelGroup:         "stable",
			mockSetup: func(mc *cincinnati.MockClient) {
				// Mock Cincinnati returning an error
				mc.EXPECT().GetUpdates(gomock.AssignableToTypeOf(context.Background()), api.Must(cincinnati.GetCincinnatiURI("stable")), "multi", "multi", "stable-4.20", semver.MustParse("4.19.22")).Return(
					configv1.Release{},
					nil,
					nil,
					&cvocincinnati.Error{Message: "example error message"},
				)
			},
			expectedVersion:       nil,
			expectedError:         true,
			expectedErrorContains: "example error message",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctrl := gomock.NewController(t)

			mockCincinnatiClient := cincinnati.NewMockClient(ctrl)
			tt.mockSetup(mockCincinnatiClient)

			ctx := context.Background()
			mockResourcesDBClient, err := databasetesting.NewMockResourcesDBClientWithResources(ctx, tt.cosmosResources)
			require.NoError(t, err)
			syncer := &controlPlaneDesiredVersionSyncer{resourcesDBClient: mockResourcesDBClient}

			result, err := syncer.desiredControlPlaneZVersion(ctx, mockCincinnatiClient, api.Must(api.ToClusterResourceID("6b690bec-0c16-4ecb-8f67-781caf40bba7", "test-rg", "test-cluster")), tt.customerDesiredMinor, tt.channelGroup, tt.activeVersions, false)

			assertVersionResult(t, result, err, tt.expectedVersion, tt.expectedError, tt.expectedErrorContains)
		})
	}
}

// testCosmosClusterWithWorkersNodePoolAtVersion returns a cluster and workers node pool for the subscription, resource group,
// and cluster name shared by desiredControlPlaneZVersion tests. nodePoolVersionId is properties.version.id on the pool.
func testCosmosClusterWithWorkersNodePoolAtVersion(nodePoolVersionId string) []any {
	clusterResourceId := api.Must(api.ToClusterResourceID("6b690bec-0c16-4ecb-8f67-781caf40bba7", "test-rg", "test-cluster"))
	cluster := &api.HCPOpenShiftCluster{
		TrackedResource: arm.TrackedResource{
			Resource: arm.Resource{
				ID:   clusterResourceId,
				Name: clusterResourceId.Name,
				Type: clusterResourceId.ResourceType.String(),
			},
		},
		ServiceProviderProperties: api.HCPOpenShiftClusterServiceProviderProperties{
			ClusterServiceID: api.Ptr(api.Must(api.NewInternalID("/api/clusters_mgmt/v1/clusters/test-cluster"))),
		},
	}
	nodePoolResourceId := api.Must(azcorearm.ParseResourceID(clusterResourceId.String() + "/nodePools/workers"))
	return []any{
		cluster,
		&api.HCPOpenShiftClusterNodePool{
			TrackedResource: arm.NewTrackedResource(nodePoolResourceId, "eastus"),
			Properties: api.HCPOpenShiftClusterNodePoolProperties{
				Version: api.NodePoolVersionProfile{ID: nodePoolVersionId},
			},
			ServiceProviderProperties: api.HCPOpenShiftClusterNodePoolServiceProviderProperties{
				ClusterServiceID: api.Must(api.NewInternalID("/api/clusters_mgmt/v1/clusters/test-cluster/node_pools/workers")),
			},
		},
	}
}

func TestDesiredControlPlaneZVersion_Validations(t *testing.T) {
	tests := []struct {
		name                        string
		activeVersions              []api.HCPClusterActiveVersion
		customerDesiredMinor        string
		channelGroup                string
		mockSetup                   func(*cincinnati.MockClient)
		cosmosResources             []any
		experimentalReleaseFeatures bool
		expectedVersion             *semver.Version
		expectedError               bool
		expectedErrorContains       string
	}{
		{
			name:                  "Validation - downgrade not allowed (4.20 -> 4.19)",
			activeVersions:        []api.HCPClusterActiveVersion{{Version: ptr.To(semver.MustParse("4.20.15")), State: configv1.CompletedUpdate}},
			customerDesiredMinor:  "4.19",
			channelGroup:          "stable",
			mockSetup:             func(mc *cincinnati.MockClient) {},
			expectedVersion:       nil,
			expectedError:         true,
			expectedErrorContains: "only upgrades to the next minor version are allowed, no downgrades",
		},
		{
			name:                  "Validation - OpenShift 5.x requires AFEC (4.20 -> 5.0)",
			activeVersions:        []api.HCPClusterActiveVersion{{Version: ptr.To(semver.MustParse("4.20.15")), State: configv1.CompletedUpdate}},
			customerDesiredMinor:  "5.0",
			channelGroup:          "stable",
			mockSetup:             func(mc *cincinnati.MockClient) {},
			expectedVersion:       nil,
			expectedError:         true,
			expectedErrorContains: "OpenShift v5 and above is not supported",
		},
		{
			name:                        "Validation - unsupported cross-major (4.20 -> 5.0, not a supported 4→5 landing) when AFEC registered",
			activeVersions:              []api.HCPClusterActiveVersion{{Version: ptr.To(semver.MustParse("4.20.15")), State: configv1.CompletedUpdate}},
			customerDesiredMinor:        "5.0",
			channelGroup:                "stable",
			mockSetup:                   func(mc *cincinnati.MockClient) {},
			experimentalReleaseFeatures: true,
			expectedVersion:             nil,
			expectedError:               true,
			expectedErrorContains:       "cross-major upgrade from 4.20 is only allowed to",
		},
		{
			name:                  "Validation - skip minor version not allowed (4.19 -> 4.21)",
			activeVersions:        []api.HCPClusterActiveVersion{{Version: ptr.To(semver.MustParse("4.19.22")), State: configv1.CompletedUpdate}},
			customerDesiredMinor:  "4.21",
			channelGroup:          "stable",
			mockSetup:             func(mc *cincinnati.MockClient) {},
			expectedVersion:       nil,
			expectedError:         true,
			expectedErrorContains: "only upgrade to the next minor is allowed",
		},
		{
			name:                  "Validation - major version downgrade not allowed (5.1 -> 4.20)",
			activeVersions:        []api.HCPClusterActiveVersion{{Version: ptr.To(semver.MustParse("5.1.5")), State: configv1.CompletedUpdate}},
			customerDesiredMinor:  "4.20",
			channelGroup:          "stable",
			mockSetup:             func(mc *cincinnati.MockClient) {},
			expectedVersion:       nil,
			expectedError:         true,
			expectedErrorContains: "only upgrades to the next minor version are allowed, no downgrades",
		},
		{
			name:                        "Validation - node pool minor skew blocks supported cross-major desired minor",
			activeVersions:              []api.HCPClusterActiveVersion{{Version: ptr.To(semver.MustParse("4.22.0")), State: configv1.CompletedUpdate}},
			customerDesiredMinor:        "5.0",
			channelGroup:                "stable",
			mockSetup:                   func(mc *cincinnati.MockClient) {},
			cosmosResources:             testCosmosClusterWithWorkersNodePoolAtVersion("4.20.0"),
			experimentalReleaseFeatures: true,
			expectedVersion:             nil,
			expectedError:               true,
			expectedErrorContains:       "incompatible with node pool",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctrl := gomock.NewController(t)

			mockCincinnatiClient := cincinnati.NewMockClient(ctrl)
			tt.mockSetup(mockCincinnatiClient)

			ctx := context.Background()
			mockResourcesDBClient, err := databasetesting.NewMockResourcesDBClientWithResources(ctx, tt.cosmosResources)
			require.NoError(t, err)
			syncer := &controlPlaneDesiredVersionSyncer{resourcesDBClient: mockResourcesDBClient}

			result, err := syncer.desiredControlPlaneZVersion(ctx, mockCincinnatiClient, api.Must(api.ToClusterResourceID("6b690bec-0c16-4ecb-8f67-781caf40bba7", "test-rg", "test-cluster")), tt.customerDesiredMinor, tt.channelGroup, tt.activeVersions, tt.experimentalReleaseFeatures)

			assertVersionResult(t, result, err, tt.expectedVersion, tt.expectedError, tt.expectedErrorContains)
		})
	}
}

func TestDesiredControlPlaneZVersion_CrossMajorUpgrade(t *testing.T) {
	tests := []struct {
		name                        string
		activeVersions              []api.HCPClusterActiveVersion
		customerDesiredMinor        string
		channelGroup                string
		mockSetup                   func(*cincinnati.MockClient)
		cosmosResources             []any
		experimentalReleaseFeatures bool
		expectedVersion             *semver.Version
		expectedError               bool
		expectedErrorContains       string
	}{
		{
			name:                        "Cross-major allowed — 4.22 to 5.0 with experimental release features and compatible node pools",
			activeVersions:              []api.HCPClusterActiveVersion{{Version: ptr.To(semver.MustParse("4.22.0")), State: configv1.CompletedUpdate}},
			customerDesiredMinor:        "5.0",
			channelGroup:                "stable",
			cosmosResources:             testCosmosClusterWithWorkersNodePoolAtVersion("4.22.0"),
			experimentalReleaseFeatures: true,
			mockSetup: func(mc *cincinnati.MockClient) {
				stableURI := api.Must(cincinnati.GetCincinnatiURI("stable"))
				mc.EXPECT().GetUpdates(gomock.AssignableToTypeOf(context.Background()), stableURI, "multi", "multi", "stable-5.0", semver.MustParse("4.22.0")).Return(
					configv1.Release{Version: "4.22.0"},
					[]configv1.Release{{Version: "5.0.15"}, {Version: "5.0.10"}, {Version: "4.22.5"}},
					[]configv1.ConditionalUpdate{},
					nil,
				)
				mc.EXPECT().GetUpdates(gomock.AssignableToTypeOf(context.Background()), stableURI, "multi", "multi", "stable-5.1", semver.MustParse("5.0.15")).Times(2).Return(
					configv1.Release{Version: "5.0.15"},
					[]configv1.Release{},
					[]configv1.ConditionalUpdate{},
					nil,
				)
				mc.EXPECT().GetUpdates(gomock.AssignableToTypeOf(context.Background()), stableURI, "multi", "multi", "stable-5.1", semver.MustParse("5.0.10")).Return(
					configv1.Release{Version: "5.0.10"},
					[]configv1.Release{{Version: "5.1.0"}},
					[]configv1.ConditionalUpdate{},
					nil,
				)
			},
			expectedVersion: ptr.To(semver.MustParse("5.0.10")),
			expectedError:   false,
		},
		{
			name:                        "Cross-major not allowed — 4.22 to 5.0 without experimental release features",
			activeVersions:              []api.HCPClusterActiveVersion{{Version: ptr.To(semver.MustParse("4.22.0")), State: configv1.CompletedUpdate}},
			customerDesiredMinor:        "5.0",
			channelGroup:                "stable",
			mockSetup:                   func(mc *cincinnati.MockClient) {},
			experimentalReleaseFeatures: false,
			expectedVersion:             nil,
			expectedError:               true,
			expectedErrorContains:       "OpenShift v5 and above is not supported",
		},
		{
			name:                        "Cross-major not allowed — 4.21 to 5.0 is not a supported landing even with experimental release features",
			activeVersions:              []api.HCPClusterActiveVersion{{Version: ptr.To(semver.MustParse("4.21.10")), State: configv1.CompletedUpdate}},
			customerDesiredMinor:        "5.0",
			channelGroup:                "stable",
			cosmosResources:             testCosmosClusterWithWorkersNodePoolAtVersion("4.21.0"),
			mockSetup:                   func(mc *cincinnati.MockClient) {},
			experimentalReleaseFeatures: true,
			expectedVersion:             nil,
			expectedError:               true,
			expectedErrorContains:       "cross-major upgrade from 4.21 is only allowed to",
		},
		{
			name:                        "Cross-major not allowed — 4.22 to 5.1 skips the supported 4.22 to 5.0 path",
			activeVersions:              []api.HCPClusterActiveVersion{{Version: ptr.To(semver.MustParse("4.22.0")), State: configv1.CompletedUpdate}},
			customerDesiredMinor:        "5.1",
			channelGroup:                "stable",
			cosmosResources:             testCosmosClusterWithWorkersNodePoolAtVersion("4.22.0"),
			mockSetup:                   func(mc *cincinnati.MockClient) {},
			experimentalReleaseFeatures: true,
			expectedVersion:             nil,
			expectedError:               true,
			expectedErrorContains:       "cross-major upgrade from 4.22 is only allowed to",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctrl := gomock.NewController(t)

			mockCincinnatiClient := cincinnati.NewMockClient(ctrl)
			tt.mockSetup(mockCincinnatiClient)

			ctx := context.Background()
			mockResourcesDBClient, err := databasetesting.NewMockResourcesDBClientWithResources(ctx, tt.cosmosResources)
			require.NoError(t, err)
			syncer := &controlPlaneDesiredVersionSyncer{resourcesDBClient: mockResourcesDBClient}

			result, err := syncer.desiredControlPlaneZVersion(ctx, mockCincinnatiClient, api.Must(api.ToClusterResourceID("6b690bec-0c16-4ecb-8f67-781caf40bba7", "test-rg", "test-cluster")), tt.customerDesiredMinor, tt.channelGroup, tt.activeVersions, tt.experimentalReleaseFeatures)

			assertVersionResult(t, result, err, tt.expectedVersion, tt.expectedError, tt.expectedErrorContains)
		})
	}
}

func TestDesiredControlPlaneZVersion_InitialVersionSelection(t *testing.T) {
	tests := []struct {
		name                  string
		customerDesiredMinor  string
		channelGroup          string
		mockSetup             func(*cincinnati.MockClient)
		expectedVersion       *semver.Version
		expectedError         bool
		expectedErrorContains string
	}{
		{
			name:                 "Initial version - prefers gateway over absolute latest",
			customerDesiredMinor: "4.19",
			channelGroup:         "stable",
			mockSetup: func(mc *cincinnati.MockClient) {
				// Query for 4.19 versions from seedVersion (4.19.0)
				// Cincinnati may return versions from other minors which should be filtered out
				mc.EXPECT().GetUpdates(gomock.AssignableToTypeOf(context.Background()), api.Must(cincinnati.GetCincinnatiURI("stable")), "multi", "multi", "stable-4.19", semver.MustParse("4.19.0")).Return(
					configv1.Release{Version: "4.19.0"},
					[]configv1.Release{{Version: "4.19.15"}, {Version: "4.19.22"}, {Version: "4.20.5"}}, // 4.20.5 should be filtered out
					[]configv1.ConditionalUpdate{},
					nil,
				)

				// Check if next minor (4.20) exists using latest candidate (4.19.22) and if 4.19.22 is a gateway to 4.20 (called twice)
				// Returns empty list - 4.19.22 is NOT a gateway to 4.20
				mc.EXPECT().GetUpdates(gomock.AssignableToTypeOf(context.Background()), api.Must(cincinnati.GetCincinnatiURI("stable")), "multi", "multi", "stable-4.20", semver.MustParse("4.19.22")).Times(2).Return(
					configv1.Release{Version: "4.19.22"},
					[]configv1.Release{}, // No path to 4.20
					[]configv1.ConditionalUpdate{},
					nil,
				)

				// Check if 4.19.15 is a gateway to 4.20 - it is
				mc.EXPECT().GetUpdates(gomock.AssignableToTypeOf(context.Background()), api.Must(cincinnati.GetCincinnatiURI("stable")), "multi", "multi", "stable-4.20", semver.MustParse("4.19.15")).Return(
					configv1.Release{Version: "4.19.15"},
					[]configv1.Release{{Version: "4.20.5"}},
					[]configv1.ConditionalUpdate{},
					nil,
				)
			},
			expectedVersion: ptr.To(semver.MustParse("4.19.15")), // Prefers gateway version over absolute latest
			expectedError:   false,
		},
		{
			name:                 "Initial version - no updates available, falls back to seedVersion",
			customerDesiredMinor: "4.19",
			channelGroup:         "stable",
			mockSetup: func(mc *cincinnati.MockClient) {
				// Query for 4.19 versions from seedVersion (4.19.0)
				// No updates available - Cincinnati returns empty list
				mc.EXPECT().GetUpdates(gomock.AssignableToTypeOf(context.Background()), api.Must(cincinnati.GetCincinnatiURI("stable")), "multi", "multi", "stable-4.19", semver.MustParse("4.19.0")).Return(
					configv1.Release{Version: "4.19.0"},
					[]configv1.Release{}, // No newer versions available
					[]configv1.ConditionalUpdate{},
					nil,
				)
			},
			expectedVersion: ptr.To(semver.MustParse("4.19.0")), // Falls back to seedVersion
			expectedError:   false,
		},
		{
			name:                 "Initial version - next minor doesn't exist yet, returns latest",
			customerDesiredMinor: "4.19",
			channelGroup:         "stable",
			mockSetup: func(mc *cincinnati.MockClient) {
				// Query for 4.19 versions from seedVersion (4.19.0)
				// Cincinnati may return versions from other minors which should be filtered out
				mc.EXPECT().GetUpdates(gomock.AssignableToTypeOf(context.Background()), api.Must(cincinnati.GetCincinnatiURI("stable")), "multi", "multi", "stable-4.19", semver.MustParse("4.19.0")).Return(
					configv1.Release{Version: "4.19.0"},
					[]configv1.Release{{Version: "4.19.15"}, {Version: "4.19.22"}, {Version: "4.20.0"}}, // 4.20.0 should be filtered out
					[]configv1.ConditionalUpdate{},
					nil,
				)

				// Check if next minor (4.20) exists using latest candidate (4.19.22)
				mc.EXPECT().GetUpdates(gomock.AssignableToTypeOf(context.Background()), api.Must(cincinnati.GetCincinnatiURI("stable")), "multi", "multi", "stable-4.20", semver.MustParse("4.19.22")).Return(
					configv1.Release{},
					nil,
					nil,
					&cvocincinnati.Error{Reason: "VersionNotFound"}, // Next minor doesn't exist yet
				)

				// Since next minor doesn't exist, return latest candidate
			},
			expectedVersion: ptr.To(semver.MustParse("4.19.22")), // Returns latest - no next minor to preserve path to
			expectedError:   false,
		},
		{
			name:                 "Initial version - Cincinnati query error",
			customerDesiredMinor: "4.19",
			channelGroup:         "stable",
			mockSetup: func(mc *cincinnati.MockClient) {
				// Mock Cincinnati returning an error
				mc.EXPECT().GetUpdates(gomock.AssignableToTypeOf(context.Background()), api.Must(cincinnati.GetCincinnatiURI("stable")), "multi", "multi", "stable-4.19", semver.MustParse("4.19.0")).Return(
					configv1.Release{},
					nil,
					nil,
					&cvocincinnati.Error{Message: "example error message"},
				)
			},
			expectedVersion:       nil,
			expectedError:         true,
			expectedErrorContains: "example error message",
		},
		{
			name:                 "Initial version - Cincinnati version not found",
			customerDesiredMinor: "4.19",
			channelGroup:         "stable",
			mockSetup: func(mc *cincinnati.MockClient) {
				// Mock Cincinnati returning a VersionNotFound error
				mc.EXPECT().GetUpdates(gomock.AssignableToTypeOf(context.Background()), api.Must(cincinnati.GetCincinnatiURI("stable")), "multi", "multi", "stable-4.19", semver.MustParse("4.19.0")).Return(
					configv1.Release{},
					nil,
					nil,
					&cvocincinnati.Error{Reason: "VersionNotFound"},
				)
			},
			expectedVersion:       nil,
			expectedError:         true,
			expectedErrorContains: "VersionNotFound",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctrl := gomock.NewController(t)

			mockCincinnatiClient := cincinnati.NewMockClient(ctrl)
			tt.mockSetup(mockCincinnatiClient)

			syncer := &controlPlaneDesiredVersionSyncer{resourcesDBClient: databasetesting.NewMockResourcesDBClient()}

			// Empty active versions - simulating a new cluster
			activeVersions := []api.HCPClusterActiveVersion{}

			ctx := context.Background()
			result, err := syncer.desiredControlPlaneZVersion(ctx, mockCincinnatiClient, api.Must(api.ToClusterResourceID("6b690bec-0c16-4ecb-8f67-781caf40bba7", "test-rg", "test-cluster")), tt.customerDesiredMinor, tt.channelGroup, activeVersions, false)

			assertVersionResult(t, result, err, tt.expectedVersion, tt.expectedError, tt.expectedErrorContains)
		})
	}
}

// assertVersionResult is a helper function that validates the result of desiredControlPlaneZVersion
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

func createTestHCPClusterWithCustomerVersion(t *testing.T, ctx context.Context, mockResourcesDBClient *databasetesting.MockResourcesDBClient, customerVersionID, channelGroup string) {
	t.Helper()
	createTestSubscription(t, ctx, mockResourcesDBClient)
	clusterResourceID := api.Must(azcorearm.ParseResourceID("/subscriptions/" + testSubscriptionID +
		"/resourceGroups/" + testResourceGroupName +
		"/providers/Microsoft.RedHatOpenShift/hcpOpenShiftClusters/" + testClusterName))
	clusterInternalID, err := api.NewInternalID(testCSClusterIDStr)
	require.NoError(t, err)
	cluster := &api.HCPOpenShiftCluster{
		TrackedResource: arm.TrackedResource{
			Resource: arm.Resource{
				ID:   clusterResourceID,
				Name: testClusterName,
				Type: api.ClusterResourceType.String(),
			},
			Location: "eastus",
		},
		CustomerProperties: api.HCPOpenShiftClusterCustomerProperties{
			Version: api.VersionProfile{
				ID:           customerVersionID,
				ChannelGroup: channelGroup,
			},
		},
		ServiceProviderProperties: api.HCPOpenShiftClusterServiceProviderProperties{
			ProvisioningState: arm.ProvisioningStateSucceeded,
			ClusterServiceID:  &clusterInternalID,
		},
	}
	_, err = mockResourcesDBClient.HCPClusters(testSubscriptionID, testResourceGroupName).Create(ctx, cluster, nil)
	require.NoError(t, err)
}

func TestControlPlaneDesiredVersionSyncer_SyncOnce(t *testing.T) {
	clusterKey := controllerutils.HCPClusterKey{
		SubscriptionID:    testSubscriptionID,
		ResourceGroupName: testResourceGroupName,
		HCPClusterName:    testClusterName,
	}
	subResourceID := api.Must(azcorearm.ParseResourceID("/subscriptions/" + testSubscriptionID))
	subscriptionLister := &listertesting.SliceSubscriptionLister{
		Subscriptions: []*arm.Subscription{{
			CosmosMetadata: arm.CosmosMetadata{ResourceID: subResourceID},
			ResourceID:     subResourceID,
			Properties:     &arm.SubscriptionProperties{},
		}},
	}

	stableURI := api.Must(cincinnati.GetCincinnatiURI("stable"))
	const testChannelGroup = "stable"

	tests := []struct {
		name                string
		customerVersion     string
		controlPlaneVersion string
		setupCincinnati     func(mc *cincinnati.MockClient)
		wantSyncErr         bool
		wantErrContains     string
		wantDesiredVersion  *semver.Version
		wantIntentFailed    *metav1.Condition
	}{
		{
			name:                "successful resolution persists desired version and sets IntentFailed False",
			customerVersion:     "4.19",
			controlPlaneVersion: "4.19.15",
			setupCincinnati: func(mc *cincinnati.MockClient) {
				mc.EXPECT().GetUpdates(gomock.Any(), stableURI, "multi", "multi", "stable-4.19", semver.MustParse("4.19.15")).Return(
					configv1.Release{},
					[]configv1.Release{{Version: "4.19.22"}},
					nil,
					nil,
				).Times(1)
				mc.EXPECT().GetUpdates(gomock.Any(), stableURI, "multi", "multi", "stable-4.20", semver.MustParse("4.19.22")).Return(
					configv1.Release{}, nil, nil, &cvocincinnati.Error{Reason: "VersionNotFound"},
				).Times(1)
			},
			wantDesiredVersion: ptr.To(semver.MustParse("4.19.22")),
			wantIntentFailed: &metav1.Condition{
				Type:   api.ControllerConditionTypeIntentFailed,
				Status: metav1.ConditionFalse,
				Reason: api.ControllerConditionReasonAsExpected,
			},
		},
		{
			name:                "validation error persists IntentFailed and does not set desired version",
			customerVersion:     "4.19",
			controlPlaneVersion: "4.20.15",
			setupCincinnati:     func(mc *cincinnati.MockClient) {},
			wantDesiredVersion:  nil,
			wantIntentFailed: &metav1.Condition{
				Type:    api.ControllerConditionTypeIntentFailed,
				Status:  metav1.ConditionTrue,
				Reason:  api.VersionUpgradeNotAcceptedReason,
				Message: "invalid next y-stream upgrade path from 4.20.0 to 4.19.0: only upgrades to the next minor version are allowed, no downgrades",
			},
		},
		{
			name:                "Cincinnati upstream error does not persist IntentFailed or desired version",
			customerVersion:     "4.19",
			controlPlaneVersion: "4.19.15",
			setupCincinnati: func(mc *cincinnati.MockClient) {
				mc.EXPECT().GetUpdates(gomock.Any(), stableURI, "multi", "multi", "stable-4.19", semver.MustParse("4.19.15")).Return(
					configv1.Release{}, nil, nil, &cvocincinnati.Error{Reason: "ServiceUnavailable", Message: "503 Service Unavailable"},
				).Times(1)
			},
			wantSyncErr:        true,
			wantErrContains:    "503 Service Unavailable",
			wantDesiredVersion: nil,
			wantIntentFailed:   nil,
		},
		{
			name:                "Cincinnati VersionNotFound persists IntentFailed and does not set desired version",
			customerVersion:     "4.19",
			controlPlaneVersion: "4.19.15",
			setupCincinnati: func(mc *cincinnati.MockClient) {
				mc.EXPECT().GetUpdates(gomock.Any(), stableURI, "multi", "multi", "stable-4.19", semver.MustParse("4.19.15")).Return(
					configv1.Release{}, nil, nil, &cvocincinnati.Error{Reason: "VersionNotFound"},
				).Times(1)
			},
			wantDesiredVersion: nil,
			wantIntentFailed: &metav1.Condition{
				Type:    api.ControllerConditionTypeIntentFailed,
				Status:  metav1.ConditionTrue,
				Reason:  api.VersionUpgradeNotAcceptedReason,
				Message: "VersionNotFound: ",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			ctx := utils.ContextWithLogger(context.Background(), logr.Discard())
			ctrl := gomock.NewController(t)
			mockResourcesDBClient := databasetesting.NewMockResourcesDBClient()
			mockCS := ocm.NewMockClusterServiceClientSpec(ctrl)

			createTestHCPClusterWithCustomerVersion(t, ctx, mockResourcesDBClient, tt.customerVersion, testChannelGroup)
			createServiceProviderClusterWithVersion(t, ctx, mockResourcesDBClient, tt.controlPlaneVersion)

			mockCincinnati := cincinnati.NewMockClient(ctrl)
			tt.setupCincinnati(mockCincinnati)

			mockClientCache := cincinnati.NewMockClientCache(ctrl)
			mockClientCache.EXPECT().GetOrCreateClient(gomock.Any()).Return(mockCincinnati).AnyTimes()

			syncer := &controlPlaneDesiredVersionSyncer{
				cooldownChecker:                       &alwaysSyncCooldownChecker{},
				clusterManagementClusterContentLister: newValidHostedClusterContentLister(t),
				resourcesDBClient:                     mockResourcesDBClient,
				clusterServiceClient:                  mockCS,
				subscriptionLister:                    subscriptionLister,
				cincinnatiClientCache:                 mockClientCache,
			}

			err := syncer.SyncOnce(ctx, clusterKey)
			if tt.wantSyncErr {
				require.Error(t, err)
				require.NotEmpty(t, tt.wantErrContains, "when wantSyncErr is true, wantErrContains must be set to a substring of the expected error")
				assert.ErrorContains(t, err, tt.wantErrContains)
			} else {
				require.NoError(t, err)
				assert.Empty(t, tt.wantErrContains, "when wantSyncErr is false, wantErrContains must be empty")
			}

			serviceProviderCluster, getServiceProviderClusterErr := mockResourcesDBClient.ServiceProviderClusters(testSubscriptionID, testResourceGroupName, testClusterName).Get(ctx, api.ServiceProviderClusterResourceName)
			require.NoError(t, getServiceProviderClusterErr)
			gotDesired := serviceProviderCluster.Spec.ControlPlaneVersion.DesiredVersion
			if tt.wantDesiredVersion != nil {
				require.NotNil(t, gotDesired)
				assert.True(t, gotDesired.EQ(*tt.wantDesiredVersion), "wanted desired version %s, got %s", tt.wantDesiredVersion.String(), gotDesired.String())
			} else {
				assert.Nil(t, gotDesired)
			}

			controlPlaneDesiredVersionControllerDoc, getControllerDocErr := mockResourcesDBClient.HCPClusters(testSubscriptionID, testResourceGroupName).
				Controllers(testClusterName).Get(ctx, controlPlaneDesiredVersionControllerName)
			if tt.wantIntentFailed != nil {
				require.NoError(t, getControllerDocErr)
				require.NotNil(t, controlPlaneDesiredVersionControllerDoc)
				intentFailedCondition := apimeta.FindStatusCondition(controlPlaneDesiredVersionControllerDoc.Status.Conditions,
					api.ControllerConditionTypeIntentFailed)
				require.NotNil(t, intentFailedCondition)
				assert.Equal(t, tt.wantIntentFailed.Type, intentFailedCondition.Type)
				assert.Equal(t, tt.wantIntentFailed.Status, intentFailedCondition.Status)
				assert.Equal(t, tt.wantIntentFailed.Reason, intentFailedCondition.Reason)
				if tt.wantIntentFailed.Status == metav1.ConditionTrue {
					require.NotEmpty(t, tt.wantIntentFailed.Message, "set wantIntentFailed.Message to the exact persisted IntentFailed message")
					assert.Equal(t, tt.wantIntentFailed.Message, intentFailedCondition.Message)
				} else {
					assert.Empty(t, intentFailedCondition.Message, "when wantIntentFailed.Status is false, intentFailedCondition.Message must be empty")
				}
			}
		})
	}
}
