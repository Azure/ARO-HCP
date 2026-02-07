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
	"net/url"
	"strings"
	"testing"

	"github.com/blang/semver/v4"
	"go.uber.org/mock/gomock"

	configv1 "github.com/openshift/api/config/v1"
	"github.com/openshift/cluster-version-operator/pkg/cincinnati"

	"github.com/Azure/ARO-HCP/internal/api"
	cincinatti "github.com/Azure/ARO-HCP/internal/cincinatti"
)

func TestDesiredControlPlaneZVersion_ZStreamManagedUpgrade(t *testing.T) {
	tests := []struct {
		name                  string
		activeVersions        []api.HCPClusterActiveVersion
		customerDesiredMinor  string
		channelGroup          string
		mockSetup             func(*cincinatti.MockClient)
		expectedVersion       *semver.Version
		expectedError         bool
		expectedErrorContains string
	}{
		{
			name:                 "Z-stream upgrade - finds latest gateway",
			activeVersions:       []api.HCPClusterActiveVersion{{Version: mustParsePtr("4.19.15")}},
			customerDesiredMinor: "4.19",
			channelGroup:         "stable",
			mockSetup: func(mc *cincinatti.MockClient) {
				mc.EXPECT().GetUpdates(
					gomock.AssignableToTypeOf(context.Background()),
					mustGetCincinnatiURI("stable"),
					"multi",
					"multi",
					"stable-4.19",
					semver.MustParse("4.19.15"),
				).Return(
					configv1.Release{Version: "4.19.15"},
					[]configv1.Release{
						{Version: "4.19.18"},
						{Version: "4.19.22"},
					},
					[]configv1.ConditionalUpdate{},
					nil,
				)

				// Check if next minor (4.20) exists using latest candidate (4.19.22)
				mc.EXPECT().GetUpdates(
					gomock.AssignableToTypeOf(context.Background()),
					mustGetCincinnatiURI("stable"),
					"multi",
					"multi",
					"stable-4.20",
					semver.MustParse("4.19.22"),
				).Return(
					configv1.Release{Version: "4.19.22"},
					[]configv1.Release{
						{Version: "4.20.5"},
					},
					[]configv1.ConditionalUpdate{},
					nil,
				)

				// Check if 4.19.22 (latest candidate) is a gateway to 4.20
				mc.EXPECT().GetUpdates(
					gomock.AssignableToTypeOf(context.Background()),
					mustGetCincinnatiURI("stable"),
					"multi",
					"multi",
					"stable-4.20",
					semver.MustParse("4.19.22"),
				).Return(
					configv1.Release{Version: "4.19.22"},
					[]configv1.Release{
						{Version: "4.20.5"},
					},
					[]configv1.ConditionalUpdate{},
					nil,
				)
			},
			expectedVersion: mustParsePtr("4.19.22"),
			expectedError:   false,
		},
		{
			name:                 "Z-stream upgrade - already at latest",
			activeVersions:       []api.HCPClusterActiveVersion{{Version: mustParsePtr("4.19.22")}},
			customerDesiredMinor: "4.19",
			channelGroup:         "stable",
			mockSetup: func(mc *cincinatti.MockClient) {
				mc.EXPECT().GetUpdates(
					gomock.AssignableToTypeOf(context.Background()),
					mustGetCincinnatiURI("stable"),
					"multi",
					"multi",
					"stable-4.19",
					semver.MustParse("4.19.22"),
				).Return(
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
			activeVersions:       []api.HCPClusterActiveVersion{{Version: mustParsePtr("4.19.15")}},
			customerDesiredMinor: "4.19",
			channelGroup:         "stable",
			mockSetup: func(mc *cincinatti.MockClient) {
				// Query for 4.19 versions from 4.19.15
				mc.EXPECT().GetUpdates(
					gomock.AssignableToTypeOf(context.Background()),
					mustGetCincinnatiURI("stable"),
					"multi",
					"multi",
					"stable-4.19",
					semver.MustParse("4.19.15"),
				).Return(
					configv1.Release{Version: "4.19.15"},
					[]configv1.Release{
						{Version: "4.19.20"}, // Latest, but no gateway to 4.20
					},
					[]configv1.ConditionalUpdate{},
					nil,
				)

				// Check if next minor exists using latest candidate (4.19.20)
				mc.EXPECT().GetUpdates(
					gomock.AssignableToTypeOf(context.Background()),
					mustGetCincinnatiURI("stable"),
					"multi",
					"multi",
					"stable-4.20",
					semver.MustParse("4.19.20"),
				).Return(
					configv1.Release{Version: "4.19.20"},
					[]configv1.Release{
						{Version: "4.20.5"}, // Next minor exists
					},
					[]configv1.ConditionalUpdate{},
					nil,
				)

				// Check if 4.19.20 (latest candidate) is a gateway to 4.20 (it's not)
				mc.EXPECT().GetUpdates(
					gomock.AssignableToTypeOf(context.Background()),
					mustGetCincinnatiURI("stable"),
					"multi",
					"multi",
					"stable-4.20",
					semver.MustParse("4.19.20"),
				).Return(
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
				{Version: mustParsePtr("4.19.12")}, // Most recent
				{Version: mustParsePtr("4.19.10")}, // Older active version
			},
			mockSetup: func(mc *cincinatti.MockClient) {
				// Query for 4.19 versions from 4.19.12 (most recent active version)
				mc.EXPECT().GetUpdates(
					gomock.AssignableToTypeOf(context.Background()),
					mustGetCincinnatiURI("stable"),
					"multi",
					"multi",
					"stable-4.19",
					semver.MustParse("4.19.12"),
				).Return(
					configv1.Release{Version: "4.19.12"},
					[]configv1.Release{
						{Version: "4.19.15"}, // Reachable from 4.19.12
						{Version: "4.19.18"}, // Reachable from 4.19.12
						{Version: "4.19.22"}, // Reachable from 4.19.12
					},
					[]configv1.ConditionalUpdate{},
					nil,
				)

				// Query for 4.19 versions from 4.19.10 (older active version)
				mc.EXPECT().GetUpdates(
					gomock.AssignableToTypeOf(context.Background()),
					mustGetCincinnatiURI("stable"),
					"multi",
					"multi",
					"stable-4.19",
					semver.MustParse("4.19.10"),
				).Return(
					configv1.Release{Version: "4.19.10"},
					[]configv1.Release{
						{Version: "4.19.15"}, // Reachable from 4.19.10
						{Version: "4.19.18"}, // Reachable from 4.19.10
						// Note: 4.19.22 is NOT reachable from 4.19.10
					},
					[]configv1.ConditionalUpdate{},
					nil,
				)

				// Check if next minor (4.20) exists using latest common candidate (4.19.18)
				mc.EXPECT().GetUpdates(
					gomock.AssignableToTypeOf(context.Background()),
					mustGetCincinnatiURI("stable"),
					"multi",
					"multi",
					"stable-4.20",
					semver.MustParse("4.19.18"),
				).Return(
					configv1.Release{Version: "4.19.18"},
					[]configv1.Release{
						{Version: "4.20.5"},
					},
					[]configv1.ConditionalUpdate{},
					nil,
				)

				// Check if 4.19.18 (latest common candidate) is a gateway to 4.20
				mc.EXPECT().GetUpdates(
					gomock.AssignableToTypeOf(context.Background()),
					mustGetCincinnatiURI("stable"),
					"multi",
					"multi",
					"stable-4.20",
					semver.MustParse("4.19.18"),
				).Return(
					configv1.Release{Version: "4.19.18"},
					[]configv1.Release{
						{Version: "4.20.5"},
					},
					[]configv1.ConditionalUpdate{},
					nil,
				)
			},
			expectedVersion: mustParsePtr("4.19.18"), // Latest common candidate that's a gateway
			expectedError:   false,
		},
		{
			name:                 "Z-stream upgrade - actual has NO edge to next minor, no gateway in candidates",
			activeVersions:       []api.HCPClusterActiveVersion{{Version: mustParsePtr("4.19.10")}},
			customerDesiredMinor: "4.19",
			channelGroup:         "stable",
			mockSetup: func(mc *cincinatti.MockClient) {
				// Query for 4.19 versions from 4.19.10
				mc.EXPECT().GetUpdates(
					gomock.AssignableToTypeOf(context.Background()),
					mustGetCincinnatiURI("stable"),
					"multi",
					"multi",
					"stable-4.19",
					semver.MustParse("4.19.10"),
				).Return(
					configv1.Release{Version: "4.19.10"},
					[]configv1.Release{
						{Version: "4.19.18"}, // Latest, but no gateway to 4.20
					},
					[]configv1.ConditionalUpdate{},
					nil,
				)

				// Check if next minor (4.20) exists using latest candidate (4.19.18) - it doesn't
				mc.EXPECT().GetUpdates(
					gomock.AssignableToTypeOf(context.Background()),
					mustGetCincinnatiURI("stable"),
					"multi",
					"multi",
					"stable-4.20",
					semver.MustParse("4.19.18"),
				).Return(
					configv1.Release{},
					nil,
					nil,
					&cincinnati.Error{Reason: "VersionNotFound"}, // Next minor doesn't exist
				)

				// Since next minor doesn't exist, we return latest candidate (4.19.18)
			},
			expectedVersion: mustParsePtr("4.19.18"), // Safe to upgrade - no existing path to break
			expectedError:   false,
		},
		{
			name:                 "Z-stream upgrade - Cincinnati query error",
			activeVersions:       []api.HCPClusterActiveVersion{{Version: mustParsePtr("4.19.15")}},
			customerDesiredMinor: "4.19",
			channelGroup:         "stable",
			mockSetup: func(mc *cincinatti.MockClient) {
				// Mock Cincinnati returning an error
				mc.EXPECT().GetUpdates(
					gomock.AssignableToTypeOf(context.Background()),
					mustGetCincinnatiURI("stable"),
					"multi",
					"multi",
					"stable-4.19",
					semver.MustParse("4.19.15"),
				).Return(
					configv1.Release{},
					nil,
					nil,
					&cincinnati.Error{Message: "example error message"},
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
			defer ctrl.Finish()

			mockCincinnatiClient := cincinatti.NewMockClient(ctrl)
			tt.mockSetup(mockCincinnatiClient)

			syncer := &controlPlaneUpgradeSyncer{}

			ctx := context.Background()
			result, err := syncer.desiredControlPlaneZVersion(ctx, mockCincinnatiClient, tt.customerDesiredMinor, tt.channelGroup, tt.activeVersions)

			if tt.expectedError {
				if err == nil {
					t.Errorf("Expected error but got none")
				} else if tt.expectedErrorContains != "" && !strings.Contains(err.Error(), tt.expectedErrorContains) {
					t.Errorf("Expected error containing %q, got %q", tt.expectedErrorContains, err.Error())
				}
			} else {
				if err != nil {
					t.Errorf("Unexpected error: %v", err)
				}
				if (result == nil) != (tt.expectedVersion == nil) {
					t.Errorf("Expected version %v, got %v", tt.expectedVersion, result)
				} else if result != nil && tt.expectedVersion != nil && !result.EQ(*tt.expectedVersion) {
					t.Errorf("Expected version %q, got %q", tt.expectedVersion.String(), result.String())
				}
			}
		})
	}
}

func TestDesiredControlPlaneZVersion_NextYStreamUpgrade(t *testing.T) {
	tests := []struct {
		name                  string
		activeVersions        []api.HCPClusterActiveVersion
		customerDesiredMinor  string
		channelGroup          string
		mockSetup             func(*cincinatti.MockClient)
		expectedVersion       *semver.Version
		expectedError         bool
		expectedErrorContains string
	}{
		{
			name:                 "Y-stream upgrade - direct path available returns latest version with gateway to next minor",
			activeVersions:       []api.HCPClusterActiveVersion{{Version: mustParsePtr("4.19.22")}},
			customerDesiredMinor: "4.20",
			channelGroup:         "stable",
			mockSetup: func(mc *cincinatti.MockClient) {
				// Query for 4.20 versions from 4.19.22
				mc.EXPECT().GetUpdates(
					gomock.AssignableToTypeOf(context.Background()),
					mustGetCincinnatiURI("stable"),
					"multi",
					"multi",
					"stable-4.20",
					semver.MustParse("4.19.22"),
				).Return(
					configv1.Release{Version: "4.19.22"},
					[]configv1.Release{
						{Version: "4.20.15"},
						{Version: "4.20.10"},
					},
					[]configv1.ConditionalUpdate{},
					nil,
				)

				// Check if 4.20.15 (latest candidate) has gateway to 4.21
				// This is called twice: once to check if next minor exists, once to check gateway
				mc.EXPECT().GetUpdates(
					gomock.AssignableToTypeOf(context.Background()),
					mustGetCincinnatiURI("stable"),
					"multi",
					"multi",
					"stable-4.21",
					semver.MustParse("4.20.15"),
				).Return(
					configv1.Release{Version: "4.20.15"},
					[]configv1.Release{}, // No path to 4.21
					[]configv1.ConditionalUpdate{},
					nil,
				).Times(2)

				// Check if 4.20.10 has gateway to 4.21 - it does
				mc.EXPECT().GetUpdates(
					gomock.AssignableToTypeOf(context.Background()),
					mustGetCincinnatiURI("stable"),
					"multi",
					"multi",
					"stable-4.21",
					semver.MustParse("4.20.10"),
				).Return(
					configv1.Release{Version: "4.20.10"},
					[]configv1.Release{
						{Version: "4.21.0"},
					},
					[]configv1.ConditionalUpdate{},
					nil,
				)
			},
			expectedVersion: mustParsePtr("4.20.10"),
			expectedError:   false,
		},
		{
			name:                 "Y-stream upgrade - no direct path, falls back to Z-stream",
			activeVersions:       []api.HCPClusterActiveVersion{{Version: mustParsePtr("4.19.15")}},
			customerDesiredMinor: "4.20",
			channelGroup:         "stable",
			mockSetup: func(mc *cincinatti.MockClient) {
				// Query for 4.20 versions from 4.19.15 - no path
				mc.EXPECT().GetUpdates(
					gomock.AssignableToTypeOf(context.Background()),
					mustGetCincinnatiURI("stable"),
					"multi",
					"multi",
					"stable-4.20",
					semver.MustParse("4.19.15"),
				).Return(
					configv1.Release{Version: "4.19.15"},
					[]configv1.Release{}, // No direct path to 4.20
					[]configv1.ConditionalUpdate{},
					nil,
				)

				// Fallback to Z-stream in actual minor (4.19)
				mc.EXPECT().GetUpdates(
					gomock.AssignableToTypeOf(context.Background()),
					mustGetCincinnatiURI("stable"),
					"multi",
					"multi",
					"stable-4.19",
					semver.MustParse("4.19.15"),
				).Return(
					configv1.Release{Version: "4.19.15"},
					[]configv1.Release{
						{Version: "4.19.22"},
						{Version: "4.19.18"},
					},
					[]configv1.ConditionalUpdate{},
					nil,
				)

				// Check if next minor (4.20) exists using latest candidate (4.19.22)
				mc.EXPECT().GetUpdates(
					gomock.AssignableToTypeOf(context.Background()),
					mustGetCincinnatiURI("stable"),
					"multi",
					"multi",
					"stable-4.20",
					semver.MustParse("4.19.22"),
				).Return(
					configv1.Release{Version: "4.19.22"},
					[]configv1.Release{
						{Version: "4.20.5"},
					},
					[]configv1.ConditionalUpdate{},
					nil,
				)

				// Check if 4.19.22 (latest candidate) is a gateway to 4.20
				mc.EXPECT().GetUpdates(
					gomock.AssignableToTypeOf(context.Background()),
					mustGetCincinnatiURI("stable"),
					"multi",
					"multi",
					"stable-4.20",
					semver.MustParse("4.19.22"),
				).Return(
					configv1.Release{Version: "4.19.22"},
					[]configv1.Release{
						{Version: "4.20.5"},
					},
					[]configv1.ConditionalUpdate{},
					nil,
				)
			},
			expectedVersion: mustParsePtr("4.19.22"),
			expectedError:   false,
		},
		{
			name:                 "Y-stream upgrade - multiple active versions, only common candidates considered",
			customerDesiredMinor: "4.20",
			channelGroup:         "stable",
			activeVersions: []api.HCPClusterActiveVersion{
				{Version: mustParsePtr("4.19.18")}, // Most recent
				{Version: mustParsePtr("4.19.15")}, // Older active version
			},
			mockSetup: func(mc *cincinatti.MockClient) {
				// Query for 4.20 versions from 4.19.18 (most recent active version)
				mc.EXPECT().GetUpdates(
					gomock.AssignableToTypeOf(context.Background()),
					mustGetCincinnatiURI("stable"),
					"multi",
					"multi",
					"stable-4.20",
					semver.MustParse("4.19.18"),
				).Return(
					configv1.Release{Version: "4.19.18"},
					[]configv1.Release{
						{Version: "4.20.8"},  // Reachable from 4.19.18
						{Version: "4.20.12"}, // Reachable from 4.19.18
						{Version: "4.20.15"}, // Reachable from 4.19.18
					},
					[]configv1.ConditionalUpdate{},
					nil,
				)

				// Query for 4.20 versions from 4.19.15 (older active version)
				mc.EXPECT().GetUpdates(
					gomock.AssignableToTypeOf(context.Background()),
					mustGetCincinnatiURI("stable"),
					"multi",
					"multi",
					"stable-4.20",
					semver.MustParse("4.19.15"),
				).Return(
					configv1.Release{Version: "4.19.15"},
					[]configv1.Release{
						{Version: "4.20.8"},  // Reachable from 4.19.15
						{Version: "4.20.12"}, // Reachable from 4.19.15
						// Note: 4.20.15 is NOT reachable from 4.19.15
					},
					[]configv1.ConditionalUpdate{},
					nil,
				)

				// Check if next minor (4.21) exists using latest candidate (4.20.12)
				// For Y-stream upgrades, actualMinor != targetMinor, so uses latest candidate
				mc.EXPECT().GetUpdates(
					gomock.AssignableToTypeOf(context.Background()),
					mustGetCincinnatiURI("stable"),
					"multi",
					"multi",
					"stable-4.21",
					semver.MustParse("4.20.12"),
				).Return(
					configv1.Release{Version: "4.20.12"},
					[]configv1.Release{
						{Version: "4.21.3"},
					},
					[]configv1.ConditionalUpdate{},
					nil,
				)

				// Check if 4.20.12 (latest common candidate) is a gateway to 4.21
				mc.EXPECT().GetUpdates(
					gomock.AssignableToTypeOf(context.Background()),
					mustGetCincinnatiURI("stable"),
					"multi",
					"multi",
					"stable-4.21",
					semver.MustParse("4.20.12"),
				).Return(
					configv1.Release{Version: "4.20.12"},
					[]configv1.Release{
						{Version: "4.21.3"},
					},
					[]configv1.ConditionalUpdate{},
					nil,
				)
			},
			expectedVersion: mustParsePtr("4.20.12"), // Latest common candidate with gateway to 4.21
			expectedError:   false,
		},
		{
			name:                 "Y-stream upgrade - no gateway found but returns latest anyway",
			activeVersions:       []api.HCPClusterActiveVersion{{Version: mustParsePtr("4.19.15")}},
			customerDesiredMinor: "4.20",
			channelGroup:         "stable",
			mockSetup: func(mc *cincinatti.MockClient) {
				// Query for 4.20 versions from 4.19.15
				mc.EXPECT().GetUpdates(
					gomock.AssignableToTypeOf(context.Background()),
					mustGetCincinnatiURI("stable"),
					"multi",
					"multi",
					"stable-4.20",
					semver.MustParse("4.19.15"),
				).Return(
					configv1.Release{Version: "4.19.15"},
					[]configv1.Release{
						{Version: "4.20.12"}, // Latest in 4.20, but no gateway to 4.21
					},
					[]configv1.ConditionalUpdate{},
					nil,
				)

				// Check if next minor (4.21) exists using latest candidate (4.20.12)
				// For Y-stream upgrades, actualMinor != targetMinor, so uses latest candidate
				mc.EXPECT().GetUpdates(
					gomock.AssignableToTypeOf(context.Background()),
					mustGetCincinnatiURI("stable"),
					"multi",
					"multi",
					"stable-4.21",
					semver.MustParse("4.20.12"),
				).Return(
					configv1.Release{},
					nil,
					nil,
					&cincinnati.Error{Reason: "VersionNotFound"}, // Next minor doesn't exist yet
				)
			},
			expectedVersion: mustParsePtr("4.20.12"), // Returns latest even without gateway - user wants to be on 4.20
			expectedError:   false,
		},
		{
			name:                 "Y-stream upgrade - Cincinnati query error",
			activeVersions:       []api.HCPClusterActiveVersion{{Version: mustParsePtr("4.19.22")}},
			customerDesiredMinor: "4.20",
			channelGroup:         "stable",
			mockSetup: func(mc *cincinatti.MockClient) {
				// Mock Cincinnati returning an error
				mc.EXPECT().GetUpdates(
					gomock.AssignableToTypeOf(context.Background()),
					mustGetCincinnatiURI("stable"),
					"multi",
					"multi",
					"stable-4.20",
					semver.MustParse("4.19.22"),
				).Return(
					configv1.Release{},
					nil,
					nil,
					&cincinnati.Error{Message: "example error message"},
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
			defer ctrl.Finish()

			mockCincinnatiClient := cincinatti.NewMockClient(ctrl)
			tt.mockSetup(mockCincinnatiClient)

			syncer := &controlPlaneUpgradeSyncer{}

			ctx := context.Background()
			result, err := syncer.desiredControlPlaneZVersion(ctx, mockCincinnatiClient, tt.customerDesiredMinor, tt.channelGroup, tt.activeVersions)

			if tt.expectedError {
				if err == nil {
					t.Errorf("Expected error but got none")
				} else if tt.expectedErrorContains != "" && !strings.Contains(err.Error(), tt.expectedErrorContains) {
					t.Errorf("Expected error containing %q, got %q", tt.expectedErrorContains, err.Error())
				}
			} else {
				if err != nil {
					t.Errorf("Unexpected error: %v", err)
				}
				if (result == nil) != (tt.expectedVersion == nil) {
					t.Errorf("Expected version %v, got %v", tt.expectedVersion, result)
				} else if result != nil && tt.expectedVersion != nil && !result.EQ(*tt.expectedVersion) {
					t.Errorf("Expected version %q, got %q", tt.expectedVersion.String(), result.String())
				}
			}
		})
	}
}

func TestDesiredControlPlaneZVersion_Validations(t *testing.T) {
	tests := []struct {
		name                  string
		activeVersions        []api.HCPClusterActiveVersion
		customerDesiredMinor  string
		channelGroup          string
		mockSetup             func(*cincinatti.MockClient)
		expectedVersion       *semver.Version
		expectedError         bool
		expectedErrorContains string
	}{
		{
			name:                  "Validation - downgrade not allowed (4.20 -> 4.19)",
			activeVersions:        []api.HCPClusterActiveVersion{{Version: mustParsePtr("4.20.15")}},
			customerDesiredMinor:  "4.19",
			channelGroup:          "stable",
			mockSetup:             func(mc *cincinatti.MockClient) {},
			expectedVersion:       nil,
			expectedError:         true,
			expectedErrorContains: "invalid next y-stream upgrade path from",
		},
		{
			name:                  "Validation - major version change not supported (4.20 -> 5.0)",
			activeVersions:        []api.HCPClusterActiveVersion{{Version: mustParsePtr("4.20.15")}},
			customerDesiredMinor:  "5.0",
			channelGroup:          "stable",
			mockSetup:             func(mc *cincinatti.MockClient) {},
			expectedVersion:       nil,
			expectedError:         true,
			expectedErrorContains: "invalid next y-stream upgrade path from",
		},
		{
			name:                  "Validation - skip minor version not allowed (4.19 -> 4.21)",
			activeVersions:        []api.HCPClusterActiveVersion{{Version: mustParsePtr("4.19.22")}},
			customerDesiredMinor:  "4.21",
			channelGroup:          "stable",
			mockSetup:             func(mc *cincinatti.MockClient) {},
			expectedVersion:       nil,
			expectedError:         true,
			expectedErrorContains: "invalid next y-stream upgrade path from",
		},
		{
			name:                  "Validation - same major, downgrade minor (4.20 -> 4.18)",
			activeVersions:        []api.HCPClusterActiveVersion{{Version: mustParsePtr("4.20.5")}},
			customerDesiredMinor:  "4.18",
			channelGroup:          "stable",
			mockSetup:             func(mc *cincinatti.MockClient) {},
			expectedVersion:       nil,
			expectedError:         true,
			expectedErrorContains: "invalid next y-stream upgrade path from",
		},
		{
			name:                  "Validation - invalid customerDesiredMinor format",
			activeVersions:        []api.HCPClusterActiveVersion{{Version: mustParsePtr("4.19.15")}},
			customerDesiredMinor:  "invalid.version",
			channelGroup:          "stable",
			mockSetup:             func(mc *cincinatti.MockClient) {},
			expectedVersion:       nil,
			expectedError:         true,
			expectedErrorContains: "invalid desired minor version",
		},
		{
			name:                  "Validation - invalid actual version (0.0.0)",
			activeVersions:        []api.HCPClusterActiveVersion{{Version: mustParsePtr("0.0.0")}},
			customerDesiredMinor:  "4.19",
			channelGroup:          "stable",
			mockSetup:             func(mc *cincinatti.MockClient) {},
			expectedVersion:       nil,
			expectedError:         true,
			expectedErrorContains: "invalid next y-stream upgrade path",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctrl := gomock.NewController(t)
			defer ctrl.Finish()

			mockCincinnatiClient := cincinatti.NewMockClient(ctrl)
			tt.mockSetup(mockCincinnatiClient)

			syncer := &controlPlaneUpgradeSyncer{}

			ctx := context.Background()
			result, err := syncer.desiredControlPlaneZVersion(ctx, mockCincinnatiClient, tt.customerDesiredMinor, tt.channelGroup, tt.activeVersions)

			if tt.expectedError {
				if err == nil {
					t.Errorf("Expected error but got none")
				} else if tt.expectedErrorContains != "" && !strings.Contains(err.Error(), tt.expectedErrorContains) {
					t.Errorf("Expected error containing %q, got %q", tt.expectedErrorContains, err.Error())
				}
			} else {
				if err != nil {
					t.Errorf("Unexpected error: %v", err)
				}
				if (result == nil) != (tt.expectedVersion == nil) {
					t.Errorf("Expected version %v, got %v", tt.expectedVersion, result)
				} else if result != nil && tt.expectedVersion != nil && !result.EQ(*tt.expectedVersion) {
					t.Errorf("Expected version %q, got %q", tt.expectedVersion.String(), result.String())
				}
			}
		})
	}
}

func TestDesiredControlPlaneZVersion_InitialVersionSelection(t *testing.T) {
	tests := []struct {
		name                  string
		customerDesiredMinor  string
		channelGroup          string
		mockSetup             func(*cincinatti.MockClient)
		expectedVersion       *semver.Version
		expectedError         bool
		expectedErrorContains string
	}{
		{
			name:                 "Initial version - prefers gateway over absolute latest",
			customerDesiredMinor: "4.19",
			channelGroup:         "stable",
			mockSetup: func(mc *cincinatti.MockClient) {
				// Query for 4.19 versions from seedVersion (4.19.0)
				mc.EXPECT().GetUpdates(
					gomock.AssignableToTypeOf(context.Background()),
					mustGetCincinnatiURI("stable"),
					"multi",
					"multi",
					"stable-4.19",
					semver.MustParse("4.19.0"),
				).Return(
					configv1.Release{Version: "4.19.0"},
					[]configv1.Release{
						{Version: "4.19.15"},
						{Version: "4.19.22"},
					},
					[]configv1.ConditionalUpdate{},
					nil,
				)

				// Check if next minor (4.20) exists using latest candidate (4.19.22)
				mc.EXPECT().GetUpdates(
					gomock.AssignableToTypeOf(context.Background()),
					mustGetCincinnatiURI("stable"),
					"multi",
					"multi",
					"stable-4.20",
					semver.MustParse("4.19.22"),
				).Return(
					configv1.Release{Version: "4.19.22"},
					[]configv1.Release{
						{Version: "4.20.5"},
					},
					[]configv1.ConditionalUpdate{},
					nil,
				)

				// Check if 4.19.22 (latest) is a gateway to 4.20 - it's not
				mc.EXPECT().GetUpdates(
					gomock.AssignableToTypeOf(context.Background()),
					mustGetCincinnatiURI("stable"),
					"multi",
					"multi",
					"stable-4.20",
					semver.MustParse("4.19.22"),
				).Return(
					configv1.Release{Version: "4.19.22"},
					[]configv1.Release{}, // No path to 4.20
					[]configv1.ConditionalUpdate{},
					nil,
				)

				// Check if 4.19.15 is a gateway to 4.20 - it is
				mc.EXPECT().GetUpdates(
					gomock.AssignableToTypeOf(context.Background()),
					mustGetCincinnatiURI("stable"),
					"multi",
					"multi",
					"stable-4.20",
					semver.MustParse("4.19.15"),
				).Return(
					configv1.Release{Version: "4.19.15"},
					[]configv1.Release{
						{Version: "4.20.5"},
					},
					[]configv1.ConditionalUpdate{},
					nil,
				)
			},
			expectedVersion: mustParsePtr("4.19.15"), // Prefers gateway version over absolute latest
			expectedError:   false,
		},
		{
			name:                 "Initial version - no updates available, falls back to seedVersion",
			customerDesiredMinor: "4.19",
			channelGroup:         "stable",
			mockSetup: func(mc *cincinatti.MockClient) {
				// Query for 4.19 versions from seedVersion (4.19.0)
				// No updates available - Cincinnati returns empty list
				mc.EXPECT().GetUpdates(
					gomock.AssignableToTypeOf(context.Background()),
					mustGetCincinnatiURI("stable"),
					"multi",
					"multi",
					"stable-4.19",
					semver.MustParse("4.19.0"),
				).Return(
					configv1.Release{Version: "4.19.0"},
					[]configv1.Release{}, // No newer versions available
					[]configv1.ConditionalUpdate{},
					nil,
				)
			},
			expectedVersion: mustParsePtr("4.19.0"), // Falls back to seedVersion
			expectedError:   false,
		},
		{
			name:                 "Initial version - next minor doesn't exist yet, returns latest",
			customerDesiredMinor: "4.19",
			channelGroup:         "stable",
			mockSetup: func(mc *cincinatti.MockClient) {
				// Query for 4.19 versions from seedVersion (4.19.0)
				mc.EXPECT().GetUpdates(
					gomock.AssignableToTypeOf(context.Background()),
					mustGetCincinnatiURI("stable"),
					"multi",
					"multi",
					"stable-4.19",
					semver.MustParse("4.19.0"),
				).Return(
					configv1.Release{Version: "4.19.0"},
					[]configv1.Release{
						{Version: "4.19.15"},
						{Version: "4.19.22"},
					},
					[]configv1.ConditionalUpdate{},
					nil,
				)

				// Check if next minor (4.20) exists using latest candidate (4.19.22)
				mc.EXPECT().GetUpdates(
					gomock.AssignableToTypeOf(context.Background()),
					mustGetCincinnatiURI("stable"),
					"multi",
					"multi",
					"stable-4.20",
					semver.MustParse("4.19.22"),
				).Return(
					configv1.Release{},
					nil,
					nil,
					&cincinnati.Error{Reason: "VersionNotFound"}, // Next minor doesn't exist yet
				)

				// Since next minor doesn't exist, return latest candidate
			},
			expectedVersion: mustParsePtr("4.19.22"), // Returns latest - no next minor to preserve path to
			expectedError:   false,
		},
		{
			name:                 "Initial version - Cincinnati query error",
			customerDesiredMinor: "4.19",
			channelGroup:         "stable",
			mockSetup: func(mc *cincinatti.MockClient) {
				// Mock Cincinnati returning an error
				mc.EXPECT().GetUpdates(
					gomock.AssignableToTypeOf(context.Background()),
					mustGetCincinnatiURI("stable"),
					"multi",
					"multi",
					"stable-4.19",
					semver.MustParse("4.19.0"),
				).Return(
					configv1.Release{},
					nil,
					nil,
					&cincinnati.Error{Message: "example error message"},
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
			defer ctrl.Finish()

			mockCincinnatiClient := cincinatti.NewMockClient(ctrl)
			tt.mockSetup(mockCincinnatiClient)

			syncer := &controlPlaneUpgradeSyncer{}

			// Empty active versions - simulating a new cluster
			activeVersions := []api.HCPClusterActiveVersion{}

			ctx := context.Background()
			result, err := syncer.desiredControlPlaneZVersion(ctx, mockCincinnatiClient, tt.customerDesiredMinor, tt.channelGroup, activeVersions)

			if tt.expectedError {
				if err == nil {
					t.Errorf("Expected error but got none")
				} else if tt.expectedErrorContains != "" && !strings.Contains(err.Error(), tt.expectedErrorContains) {
					t.Errorf("Expected error containing %q, got %q", tt.expectedErrorContains, err.Error())
				}
			} else {
				if err != nil {
					t.Errorf("Unexpected error: %v", err)
				}
				if (result == nil) != (tt.expectedVersion == nil) {
					t.Errorf("Expected version %v, got %v", tt.expectedVersion, result)
				} else if result != nil && tt.expectedVersion != nil && !result.EQ(*tt.expectedVersion) {
					t.Errorf("Expected version %q, got %q", tt.expectedVersion.String(), result.String())
				}
			}
		})
	}
}

// mustParsePtr parses a version string and returns a pointer to semver.Version.
// Returns nil for empty string.
func mustParsePtr(version string) *semver.Version {
	v := semver.MustParse(version)
	return &v
}

// mustGetCincinnatiURI gets the Cincinnati URI for a channel group, panicking on error.
func mustGetCincinnatiURI(channelGroup string) *url.URL {
	uri, err := cincinatti.GetCincinnatiURI(channelGroup)
	if err != nil {
		panic(err)
	}
	return uri
}
