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
	"testing"

	"github.com/blang/semver/v4"
	"go.uber.org/mock/gomock"

	"k8s.io/utils/ptr"

	configv1 "github.com/openshift/api/config/v1"
	cvocincinnati "github.com/openshift/cluster-version-operator/pkg/cincinnati"

	"github.com/Azure/ARO-HCP/internal/api/metadataapi"
	"github.com/Azure/ARO-HCP/internal/cincinnati"
)

func TestControlPlaneInitialVersion_Selection(t *testing.T) {
	tests := []struct {
		name                  string
		customerDesiredMinor  string
		channelGroup          string
		channelExistence      channelExistence
		mockSetup             func(*cincinnati.MockClient)
		expectedVersion       *semver.Version
		expectedError         bool
		expectedErrorContains string
	}{
		{
			name:                 "Initial version - prefers gateway over absolute latest",
			customerDesiredMinor: "4.19",
			channelGroup:         "stable",
			channelExistence:     channelExistence{"stable": {"4.20": true}},
			mockSetup: func(mc *cincinnati.MockClient) {
				// Query for 4.19 versions from seedVersion (4.19.0)
				// Cincinnati may return versions from other minors which should be filtered out
				mc.EXPECT().GetUpdates(gomock.AssignableToTypeOf(context.Background()), metadataapi.Must(cincinnati.GetCincinnatiURI("stable")), "multi", "multi", "stable-4.19", semver.MustParse("4.19.0")).Return(
					configv1.Release{Version: "4.19.0"},
					[]configv1.Release{{Version: "4.19.15"}, {Version: "4.19.22"}, {Version: "4.20.5"}}, // 4.20.5 should be filtered out
					[]configv1.ConditionalUpdate{},
					nil,
				)

				// Returns empty list - 4.19.22 is NOT a gateway to 4.20
				mc.EXPECT().GetUpdates(gomock.AssignableToTypeOf(context.Background()), metadataapi.Must(cincinnati.GetCincinnatiURI("stable")), "multi", "multi", "stable-4.20", semver.MustParse("4.19.22")).Return(
					configv1.Release{Version: "4.19.22"},
					[]configv1.Release{}, // No path to 4.20
					[]configv1.ConditionalUpdate{},
					nil,
				)

				// Check if 4.19.15 is a gateway to 4.20 - it is
				mc.EXPECT().GetUpdates(gomock.AssignableToTypeOf(context.Background()), metadataapi.Must(cincinnati.GetCincinnatiURI("stable")), "multi", "multi", "stable-4.20", semver.MustParse("4.19.15")).Return(
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
				mc.EXPECT().GetUpdates(gomock.AssignableToTypeOf(context.Background()), metadataapi.Must(cincinnati.GetCincinnatiURI("stable")), "multi", "multi", "stable-4.19", semver.MustParse("4.19.0")).Return(
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
			channelExistence:     channelExistence{"stable": {"4.20": false}},
			mockSetup: func(mc *cincinnati.MockClient) {
				// Query for 4.19 versions from seedVersion (4.19.0)
				// Cincinnati may return versions from other minors which should be filtered out
				mc.EXPECT().GetUpdates(gomock.AssignableToTypeOf(context.Background()), metadataapi.Must(cincinnati.GetCincinnatiURI("stable")), "multi", "multi", "stable-4.19", semver.MustParse("4.19.0")).Return(
					configv1.Release{Version: "4.19.0"},
					[]configv1.Release{{Version: "4.19.15"}, {Version: "4.19.22"}, {Version: "4.20.0"}}, // 4.20.0 should be filtered out
					[]configv1.ConditionalUpdate{},
					nil,
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
				mc.EXPECT().GetUpdates(gomock.AssignableToTypeOf(context.Background()), metadataapi.Must(cincinnati.GetCincinnatiURI("stable")), "multi", "multi", "stable-4.19", semver.MustParse("4.19.0")).Return(
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
				mc.EXPECT().GetUpdates(gomock.AssignableToTypeOf(context.Background()), metadataapi.Must(cincinnati.GetCincinnatiURI("stable")), "multi", "multi", "stable-4.19", semver.MustParse("4.19.0")).Return(
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

			syncer := &controlPlaneInitialVersionSyncer{
				desiredVersionSyncerCommon: desiredVersionSyncerCommon{
					graphClient: mockGraphClient(ctrl, tt.channelExistence),
				},
			}

			ctx := context.Background()
			result, err := syncer.initialDesiredControlPlaneVersion(ctx, mockCincinnatiClient, tt.customerDesiredMinor, tt.channelGroup)

			assertVersionResult(t, result, err, tt.expectedVersion, tt.expectedError, tt.expectedErrorContains)
		})
	}
}
