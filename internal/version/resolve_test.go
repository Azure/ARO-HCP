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
	"testing"

	"github.com/blang/semver/v4"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"

	configv1 "github.com/openshift/api/config/v1"
	"github.com/openshift/cluster-version-operator/pkg/cincinnati"

	"github.com/Azure/ARO-HCP/internal/api"
	cincinatti "github.com/Azure/ARO-HCP/internal/cincinatti"
)

func TestResolveInitialVersion(t *testing.T) {
	tests := []struct {
		name                  string
		customerDesiredMinor  string
		channelGroup          string
		mockSetup             func(*cincinatti.MockClient)
		expectedVersion       semver.Version
		expectedError         bool
		expectedErrorContains string
	}{
		{
			name:                 "prefers gateway over absolute latest",
			customerDesiredMinor: "4.19",
			channelGroup:         "stable",
			mockSetup: func(mc *cincinatti.MockClient) {
				mc.EXPECT().GetUpdates(gomock.AssignableToTypeOf(context.Background()), api.Must(cincinatti.GetCincinnatiURI("stable")), "multi", "multi", "stable-4.19", semver.MustParse("4.19.0")).Return(
					configv1.Release{Version: "4.19.0"},
					[]configv1.Release{{Version: "4.19.15"}, {Version: "4.19.22"}, {Version: "4.20.5"}},
					[]configv1.ConditionalUpdate{},
					nil,
				)

				// 4.19.22 is NOT a gateway to 4.20
				mc.EXPECT().GetUpdates(gomock.AssignableToTypeOf(context.Background()), api.Must(cincinatti.GetCincinnatiURI("stable")), "multi", "multi", "stable-4.20", semver.MustParse("4.19.22")).Times(2).Return(
					configv1.Release{Version: "4.19.22"},
					[]configv1.Release{},
					[]configv1.ConditionalUpdate{},
					nil,
				)

				// 4.19.15 IS a gateway to 4.20
				mc.EXPECT().GetUpdates(gomock.AssignableToTypeOf(context.Background()), api.Must(cincinatti.GetCincinnatiURI("stable")), "multi", "multi", "stable-4.20", semver.MustParse("4.19.15")).Return(
					configv1.Release{Version: "4.19.15"},
					[]configv1.Release{{Version: "4.20.5"}},
					[]configv1.ConditionalUpdate{},
					nil,
				)
			},
			expectedVersion: semver.MustParse("4.19.15"),
		},
		{
			name:                 "no updates available - falls back to X.Y.0",
			customerDesiredMinor: "4.19",
			channelGroup:         "stable",
			mockSetup: func(mc *cincinatti.MockClient) {
				mc.EXPECT().GetUpdates(gomock.AssignableToTypeOf(context.Background()), api.Must(cincinatti.GetCincinnatiURI("stable")), "multi", "multi", "stable-4.19", semver.MustParse("4.19.0")).Return(
					configv1.Release{Version: "4.19.0"},
					[]configv1.Release{},
					[]configv1.ConditionalUpdate{},
					nil,
				)
			},
			expectedVersion: semver.MustParse("4.19.0"),
		},
		{
			name:                 "next minor does not exist - returns latest",
			customerDesiredMinor: "4.19",
			channelGroup:         "stable",
			mockSetup: func(mc *cincinatti.MockClient) {
				mc.EXPECT().GetUpdates(gomock.AssignableToTypeOf(context.Background()), api.Must(cincinatti.GetCincinnatiURI("stable")), "multi", "multi", "stable-4.19", semver.MustParse("4.19.0")).Return(
					configv1.Release{Version: "4.19.0"},
					[]configv1.Release{{Version: "4.19.15"}, {Version: "4.19.22"}},
					[]configv1.ConditionalUpdate{},
					nil,
				)

				mc.EXPECT().GetUpdates(gomock.AssignableToTypeOf(context.Background()), api.Must(cincinatti.GetCincinnatiURI("stable")), "multi", "multi", "stable-4.20", semver.MustParse("4.19.22")).Return(
					configv1.Release{},
					nil,
					nil,
					&cincinnati.Error{Reason: "VersionNotFound"},
				)
			},
			expectedVersion: semver.MustParse("4.19.22"),
		},
		{
			name:                 "no gateway found - falls back to X.Y.0",
			customerDesiredMinor: "4.19",
			channelGroup:         "stable",
			mockSetup: func(mc *cincinatti.MockClient) {
				mc.EXPECT().GetUpdates(gomock.AssignableToTypeOf(context.Background()), api.Must(cincinatti.GetCincinnatiURI("stable")), "multi", "multi", "stable-4.19", semver.MustParse("4.19.0")).Return(
					configv1.Release{Version: "4.19.0"},
					[]configv1.Release{{Version: "4.19.15"}, {Version: "4.19.22"}},
					[]configv1.ConditionalUpdate{},
					nil,
				)

				// Next minor exists, but no candidates are gateways
				mc.EXPECT().GetUpdates(gomock.AssignableToTypeOf(context.Background()), api.Must(cincinatti.GetCincinnatiURI("stable")), "multi", "multi", "stable-4.20", semver.MustParse("4.19.22")).Times(2).Return(
					configv1.Release{Version: "4.19.22"},
					[]configv1.Release{},
					[]configv1.ConditionalUpdate{},
					nil,
				)

				mc.EXPECT().GetUpdates(gomock.AssignableToTypeOf(context.Background()), api.Must(cincinatti.GetCincinnatiURI("stable")), "multi", "multi", "stable-4.20", semver.MustParse("4.19.15")).Return(
					configv1.Release{Version: "4.19.15"},
					[]configv1.Release{},
					[]configv1.ConditionalUpdate{},
					nil,
				)
			},
			expectedVersion: semver.MustParse("4.19.0"),
		},
		{
			name:                 "Cincinnati query error",
			customerDesiredMinor: "4.19",
			channelGroup:         "stable",
			mockSetup: func(mc *cincinatti.MockClient) {
				mc.EXPECT().GetUpdates(gomock.AssignableToTypeOf(context.Background()), api.Must(cincinatti.GetCincinnatiURI("stable")), "multi", "multi", "stable-4.19", semver.MustParse("4.19.0")).Return(
					configv1.Release{},
					nil,
					nil,
					&cincinnati.Error{Message: "service unavailable"},
				)
			},
			expectedError:         true,
			expectedErrorContains: "service unavailable",
		},
		{
			name:                 "nightly channel group",
			customerDesiredMinor: "4.19",
			channelGroup:         "nightly",
			mockSetup: func(mc *cincinatti.MockClient) {
				mc.EXPECT().GetUpdates(gomock.AssignableToTypeOf(context.Background()), api.Must(cincinatti.GetCincinnatiURI("nightly")), "multi", "multi", "nightly-4.19", semver.MustParse("4.19.0")).Return(
					configv1.Release{Version: "4.19.0"},
					[]configv1.Release{{Version: "4.19.5"}},
					[]configv1.ConditionalUpdate{},
					nil,
				)

				mc.EXPECT().GetUpdates(gomock.AssignableToTypeOf(context.Background()), api.Must(cincinatti.GetCincinnatiURI("nightly")), "multi", "multi", "nightly-4.20", semver.MustParse("4.19.5")).Return(
					configv1.Release{},
					nil,
					nil,
					&cincinnati.Error{Reason: "VersionNotFound"},
				)
			},
			expectedVersion: semver.MustParse("4.19.5"),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctrl := gomock.NewController(t)

			mockCincinnatiClient := cincinatti.NewMockClient(ctrl)
			tt.mockSetup(mockCincinnatiClient)

			ctx := context.Background()
			result, err := ResolveInitialVersion(ctx, mockCincinnatiClient, tt.channelGroup, tt.customerDesiredMinor)

			if tt.expectedError {
				require.Error(t, err)
				assert.Contains(t, err.Error(), tt.expectedErrorContains)
			} else {
				require.NoError(t, err)
				assert.True(t, result.EQ(tt.expectedVersion), "Expected version %q, got %q", tt.expectedVersion.String(), result.String())
			}
		})
	}
}
