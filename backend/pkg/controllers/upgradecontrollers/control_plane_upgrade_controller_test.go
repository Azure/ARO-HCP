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
	"strings"
	"testing"

	"go.uber.org/mock/gomock"

	configv1 "github.com/openshift/api/config/v1"

	"github.com/Azure/ARO-HCP/internal/api"
	"github.com/Azure/ARO-HCP/internal/cincinatti"
)

func TestDesiredControlPlaneZVersion_ZStreamManagedUpgrade(t *testing.T) {
	tests := []struct {
		name                  string
		actualLatestVersion   string
		customerDesiredMinor  string
		channelGroup          string
		mockSetup             func(*cincinatti.MockClient)
		expectedVersion       string
		expectedError         bool
		expectedErrorContains string
	}{
		{
			name:                 "Z-stream upgrade - finds latest gateway",
			actualLatestVersion:  "4.19.15",
			customerDesiredMinor: "4.19",
			channelGroup:         "stable",
			mockSetup: func(mc *cincinatti.MockClient) {
				mc.EXPECT().GetUpdates(
					gomock.Any(),
					gomock.Any(),
					"multi",
					"multi",
					"stable-4.19",
					mustParse("4.19.15"),
				).Return(
					configv1.Release{Version: "4.19.15"},
					[]configv1.Release{
						{Version: "4.19.18"},
						{Version: "4.19.22"},
					},
					[]configv1.ConditionalUpdate{},
					nil,
				)

				// Mock next minor check for gateway detection (4.19.22)
				mc.EXPECT().GetUpdates(
					gomock.Any(),
					gomock.Any(),
					"multi",
					"multi",
					"stable-4.20",
					mustParse("4.19.22"),
				).Return(
					configv1.Release{Version: "4.19.22"},
					[]configv1.Release{
						{Version: "4.20.5"},
					},
					[]configv1.ConditionalUpdate{},
					nil,
				)
			},
			expectedVersion: "4.19.22",
			expectedError:   false,
		},
		{
			name:                 "Z-stream upgrade - already at latest",
			actualLatestVersion:  "4.19.22",
			customerDesiredMinor: "4.19",
			channelGroup:         "stable",
			mockSetup: func(mc *cincinatti.MockClient) {
				mc.EXPECT().GetUpdates(
					gomock.Any(),
					gomock.Any(),
					"multi",
					"multi",
					"stable-4.19",
					mustParse("4.19.22"),
				).Return(
					configv1.Release{Version: "4.19.22"},
					[]configv1.Release{}, // No newer versions
					[]configv1.ConditionalUpdate{},
					nil,
				)
			},
			expectedVersion: "",
			expectedError:   false,
		},
		{
			name:                 "Z-stream upgrade - actual has edge to next minor, no gateway in candidates",
			actualLatestVersion:  "4.19.15",
			customerDesiredMinor: "4.19",
			channelGroup:         "stable",
			mockSetup: func(mc *cincinatti.MockClient) {
				// Query for 4.19 versions from 4.19.15
				mc.EXPECT().GetUpdates(
					gomock.Any(),
					gomock.Any(),
					"multi",
					"multi",
					"stable-4.19",
					mustParse("4.19.15"),
				).Return(
					configv1.Release{Version: "4.19.15"},
					[]configv1.Release{
						{Version: "4.19.20"}, // Latest, but no gateway to 4.20
					},
					[]configv1.ConditionalUpdate{},
					nil,
				)

				// Check if 4.19.20 is a gateway to 4.20 (it's not)
				mc.EXPECT().GetUpdates(
					gomock.Any(),
					gomock.Any(),
					"multi",
					"multi",
					"stable-4.20",
					mustParse("4.19.20"),
				).Return(
					configv1.Release{Version: "4.19.20"},
					[]configv1.Release{}, // No path to 4.20
					[]configv1.ConditionalUpdate{},
					nil,
				)

				// Check if actual version 4.19.15 has edge to 4.20 (it does)
				mc.EXPECT().GetUpdates(
					gomock.Any(),
					gomock.Any(),
					"multi",
					"multi",
					"stable-4.20",
					mustParse("4.19.15"),
				).Return(
					configv1.Release{Version: "4.19.15"},
					[]configv1.Release{
						{Version: "4.20.5"}, // Has path to 4.20
					},
					[]configv1.ConditionalUpdate{},
					nil,
				)
			},
			expectedVersion: "", // No upgrade - would break existing path
			expectedError:   false,
		},
		{
			name:                 "Z-stream upgrade - actual has NO edge to next minor, no gateway in candidates",
			actualLatestVersion:  "4.19.10",
			customerDesiredMinor: "4.19",
			channelGroup:         "stable",
			mockSetup: func(mc *cincinatti.MockClient) {
				// Query for 4.19 versions from 4.19.10
				mc.EXPECT().GetUpdates(
					gomock.Any(),
					gomock.Any(),
					"multi",
					"multi",
					"stable-4.19",
					mustParse("4.19.10"),
				).Return(
					configv1.Release{Version: "4.19.10"},
					[]configv1.Release{
						{Version: "4.19.18"}, // Latest, but no gateway to 4.20
					},
					[]configv1.ConditionalUpdate{},
					nil,
				)

				// Check if 4.19.18 is a gateway to 4.20 (it's not)
				mc.EXPECT().GetUpdates(
					gomock.Any(),
					gomock.Any(),
					"multi",
					"multi",
					"stable-4.20",
					mustParse("4.19.18"),
				).Return(
					configv1.Release{Version: "4.19.18"},
					[]configv1.Release{}, // No path to 4.20
					[]configv1.ConditionalUpdate{},
					nil,
				)

				// Check if actual version 4.19.10 has edge to 4.20 (it doesn't)
				mc.EXPECT().GetUpdates(
					gomock.Any(),
					gomock.Any(),
					"multi",
					"multi",
					"stable-4.20",
					mustParse("4.19.10"),
				).Return(
					configv1.Release{Version: "4.19.10"},
					[]configv1.Release{}, // No path to 4.20
					[]configv1.ConditionalUpdate{},
					nil,
				)
			},
			expectedVersion: "4.19.18", // Safe to upgrade - no existing path to break
			expectedError:   false,
		},
		{
			name:                  "Z-stream upgrade - invalid actual version",
			actualLatestVersion:   "invalid-version",
			customerDesiredMinor:  "4.19",
			channelGroup:          "stable",
			mockSetup:             func(mc *cincinatti.MockClient) {},
			expectedVersion:       "",
			expectedError:         true,
			expectedErrorContains: "invalid actual latest version",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctrl := gomock.NewController(t)
			defer ctrl.Finish()

			mockCincinnatiClient := cincinatti.NewMockClient(ctrl)
			tt.mockSetup(mockCincinnatiClient)

			syncer := &controlPlaneUpgradeSyncer{}

			customerDesired := &api.HCPOpenShiftCluster{
				CustomerProperties: api.HCPOpenShiftClusterCustomerProperties{
					Version: api.VersionProfile{
						ID:           tt.customerDesiredMinor,
						ChannelGroup: tt.channelGroup,
					},
				},
			}

			serviceProviderCluster := &api.ServiceProviderCluster{
				Version: &api.HCPClusterVersion{
					ActiveVersions: []api.HCPClusterActiveVersion{
						{Version: tt.actualLatestVersion},
					},
				},
			}

			ctx := context.Background()
			result, err := syncer.desiredControlPlaneZVersion(ctx, mockCincinnatiClient, customerDesired, serviceProviderCluster)

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
				if result != tt.expectedVersion {
					t.Errorf("Expected version %q, got %q", tt.expectedVersion, result)
				}
			}
		})
	}
}

func TestDesiredControlPlaneZVersion_NextYStreamUpgrade(t *testing.T) {
	tests := []struct {
		name                  string
		actualLatestVersion   string
		customerDesiredMinor  string
		channelGroup          string
		mockSetup             func(*cincinatti.MockClient)
		expectedVersion       string
		expectedError         bool
		expectedErrorContains string
	}{
		{
			name:                 "Y-stream upgrade - direct path available returns latest version with gateway to next minor",
			actualLatestVersion:  "4.19.22",
			customerDesiredMinor: "4.20",
			channelGroup:         "stable",
			mockSetup: func(mc *cincinatti.MockClient) {
				// Query for 4.20 versions from 4.19.22
				mc.EXPECT().GetUpdates(
					gomock.Any(),
					gomock.Any(),
					"multi",
					"multi",
					"stable-4.20",
					mustParse("4.19.22"),
				).Return(
					configv1.Release{Version: "4.19.22"},
					[]configv1.Release{
						{Version: "4.20.15"},
						{Version: "4.20.10"},
					},
					[]configv1.ConditionalUpdate{},
					nil,
				)

				// Check if 4.20.15 (latest) has gateway to 4.21 - it doesn't
				mc.EXPECT().GetUpdates(
					gomock.Any(),
					gomock.Any(),
					"multi",
					"multi",
					"stable-4.21",
					mustParse("4.20.15"),
				).Return(
					configv1.Release{Version: "4.20.15"},
					[]configv1.Release{}, // No path to 4.21
					[]configv1.ConditionalUpdate{},
					nil,
				)

				// Check if 4.20.10 has gateway to 4.21 - it does
				mc.EXPECT().GetUpdates(
					gomock.Any(),
					gomock.Any(),
					"multi",
					"multi",
					"stable-4.21",
					mustParse("4.20.10"),
				).Return(
					configv1.Release{Version: "4.20.10"},
					[]configv1.Release{
						{Version: "4.21.0"},
					},
					[]configv1.ConditionalUpdate{},
					nil,
				)
			},
			expectedVersion: "4.20.10",
			expectedError:   false,
		},
		{
			name:                 "Y-stream upgrade - no direct path, falls back to Z-stream",
			actualLatestVersion:  "4.19.15",
			customerDesiredMinor: "4.20",
			channelGroup:         "stable",
			mockSetup: func(mc *cincinatti.MockClient) {
				// Query for 4.20 versions from 4.19.15 - no path
				mc.EXPECT().GetUpdates(
					gomock.Any(),
					gomock.Any(),
					"multi",
					"multi",
					"stable-4.20",
					mustParse("4.19.15"),
				).Return(
					configv1.Release{Version: "4.19.15"},
					[]configv1.Release{}, // No direct path to 4.20
					[]configv1.ConditionalUpdate{},
					nil,
				)

				// Fallback to Z-stream in actual minor (4.19)
				mc.EXPECT().GetUpdates(
					gomock.Any(),
					gomock.Any(),
					"multi",
					"multi",
					"stable-4.19",
					mustParse("4.19.15"),
				).Return(
					configv1.Release{Version: "4.19.15"},
					[]configv1.Release{
						{Version: "4.19.22"},
						{Version: "4.19.18"},
					},
					[]configv1.ConditionalUpdate{},
					nil,
				)

				// Mock next minor check for gateway detection (4.19.22)
				mc.EXPECT().GetUpdates(
					gomock.Any(),
					gomock.Any(),
					"multi",
					"multi",
					"stable-4.20",
					mustParse("4.19.22"),
				).Return(
					configv1.Release{Version: "4.19.22"},
					[]configv1.Release{
						{Version: "4.20.5"},
					},
					[]configv1.ConditionalUpdate{},
					nil,
				)
			},
			expectedVersion: "4.19.22",
			expectedError:   false,
		},
		{
			name:                  "Y-stream upgrade - invalid path (skip minor)",
			actualLatestVersion:   "4.19.22",
			customerDesiredMinor:  "4.21",
			channelGroup:          "stable",
			mockSetup:             func(mc *cincinatti.MockClient) {},
			expectedVersion:       "",
			expectedError:         true,
			expectedErrorContains: "invalid next y-stream upgrade path",
		},
		{
			name:                  "Y-stream upgrade - invalid path (downgrade)",
			actualLatestVersion:   "4.20.15",
			customerDesiredMinor:  "4.19",
			channelGroup:          "stable",
			mockSetup:             func(mc *cincinatti.MockClient) {},
			expectedVersion:       "",
			expectedError:         true,
			expectedErrorContains: "invalid next y-stream upgrade path",
		},
		{
			name:                 "Y-stream upgrade - no gateway found but returns latest anyway",
			actualLatestVersion:  "4.19.15",
			customerDesiredMinor: "4.20",
			channelGroup:         "stable",
			mockSetup: func(mc *cincinatti.MockClient) {
				// Query for 4.20 versions from 4.19.15
				mc.EXPECT().GetUpdates(
					gomock.Any(),
					gomock.Any(),
					"multi",
					"multi",
					"stable-4.20",
					mustParse("4.19.15"),
				).Return(
					configv1.Release{Version: "4.19.15"},
					[]configv1.Release{
						{Version: "4.20.12"}, // Latest in 4.20, but no gateway to 4.21
					},
					[]configv1.ConditionalUpdate{},
					nil,
				)

				// Check if 4.20.12 is a gateway to 4.21 (it's not)
				mc.EXPECT().GetUpdates(
					gomock.Any(),
					gomock.Any(),
					"multi",
					"multi",
					"stable-4.21",
					mustParse("4.20.12"),
				).Return(
					configv1.Release{Version: "4.20.12"},
					[]configv1.Release{}, // No path to 4.21
					[]configv1.ConditionalUpdate{},
					nil,
				)
			},
			expectedVersion: "4.20.12", // Returns latest even without gateway - user wants to be on 4.20
			expectedError:   false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctrl := gomock.NewController(t)
			defer ctrl.Finish()

			mockCincinnatiClient := cincinatti.NewMockClient(ctrl)
			tt.mockSetup(mockCincinnatiClient)

			syncer := &controlPlaneUpgradeSyncer{}

			customerDesired := &api.HCPOpenShiftCluster{
				CustomerProperties: api.HCPOpenShiftClusterCustomerProperties{
					Version: api.VersionProfile{
						ID:           tt.customerDesiredMinor,
						ChannelGroup: tt.channelGroup,
					},
				},
			}

			serviceProviderCluster := &api.ServiceProviderCluster{
				Version: &api.HCPClusterVersion{
					ActiveVersions: []api.HCPClusterActiveVersion{
						{Version: tt.actualLatestVersion},
					},
				},
			}

			ctx := context.Background()
			result, err := syncer.desiredControlPlaneZVersion(ctx, mockCincinnatiClient, customerDesired, serviceProviderCluster)

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
				if result != tt.expectedVersion {
					t.Errorf("Expected version %q, got %q", tt.expectedVersion, result)
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
		expectedVersion       string
		expectedError         bool
		expectedErrorContains string
	}{
		{
			name:                 "Initial version - finds latest with gateway to next minor",
			customerDesiredMinor: "4.19",
			channelGroup:         "stable",
			mockSetup: func(mc *cincinatti.MockClient) {
				// Query for 4.19 versions from seedVersion (4.19.0)
				mc.EXPECT().GetUpdates(
					gomock.Any(),
					gomock.Any(),
					"multi",
					"multi",
					"stable-4.19",
					mustParse("4.19.0"),
				).Return(
					configv1.Release{Version: "4.19.0"},
					[]configv1.Release{
						{Version: "4.19.15"},
						{Version: "4.19.22"},
					},
					[]configv1.ConditionalUpdate{},
					nil,
				)

				// Check if 4.19.22 has gateway to 4.20
				mc.EXPECT().GetUpdates(
					gomock.Any(),
					gomock.Any(),
					"multi",
					"multi",
					"stable-4.20",
					mustParse("4.19.22"),
				).Return(
					configv1.Release{Version: "4.19.22"},
					[]configv1.Release{
						{Version: "4.20.5"},
					},
					[]configv1.ConditionalUpdate{},
					nil,
				)
			},
			expectedVersion: "4.19.22",
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
					gomock.Any(),
					gomock.Any(),
					"multi",
					"multi",
					"stable-4.19",
					mustParse("4.19.0"),
				).Return(
					configv1.Release{Version: "4.19.0"},
					[]configv1.Release{}, // No newer versions available
					[]configv1.ConditionalUpdate{},
					nil,
				)
			},
			expectedVersion: "4.19.0", // Falls back to seedVersion
			expectedError:   false,
		},
		{
			name:                 "Initial version - newer versions exist, returns latest even without gateway",
			customerDesiredMinor: "4.19",
			channelGroup:         "stable",
			mockSetup: func(mc *cincinatti.MockClient) {
				// Query for 4.19 versions from seedVersion (4.19.0)
				mc.EXPECT().GetUpdates(
					gomock.Any(),
					gomock.Any(),
					"multi",
					"multi",
					"stable-4.19",
					mustParse("4.19.0"),
				).Return(
					configv1.Release{Version: "4.19.0"},
					[]configv1.Release{
						{Version: "4.19.15"},
						{Version: "4.19.22"},
					},
					[]configv1.ConditionalUpdate{},
					nil,
				)

				// First, check if seedVersion (4.19.0) has edge to 4.20 (since actualMinor == targetMinor)
				mc.EXPECT().GetUpdates(
					gomock.Any(),
					gomock.Any(),
					"multi",
					"multi",
					"stable-4.20",
					mustParse("4.19.0"),
				).Return(
					configv1.Release{Version: "4.19.0"},
					[]configv1.Release{}, // No path to 4.20
					[]configv1.ConditionalUpdate{},
					nil,
				)

				// Since 4.19.0 doesn't have edge to 4.20, we can safely upgrade to latest
				// Check if 4.19.22 (latest) has gateway to 4.20 - it doesn't, but that's okay
				mc.EXPECT().GetUpdates(
					gomock.Any(),
					gomock.Any(),
					"multi",
					"multi",
					"stable-4.20",
					mustParse("4.19.22"),
				).Return(
					configv1.Release{Version: "4.19.22"},
					[]configv1.Release{}, // No path to 4.20
					[]configv1.ConditionalUpdate{},
					nil,
				)

				// Check if 4.19.15 has gateway to 4.20 - it doesn't either
				mc.EXPECT().GetUpdates(
					gomock.Any(),
					gomock.Any(),
					"multi",
					"multi",
					"stable-4.20",
					mustParse("4.19.15"),
				).Return(
					configv1.Release{Version: "4.19.15"},
					[]configv1.Release{}, // No path to 4.20
					[]configv1.ConditionalUpdate{},
					nil,
				)
			},
			expectedVersion: "4.19.22", // Returns latest version (no existing gateway to break)
			expectedError:   false,
		},
		{
			name:                  "Initial version - invalid customerDesiredMinor",
			customerDesiredMinor:  "invalid",
			channelGroup:          "stable",
			mockSetup:             func(mc *cincinatti.MockClient) {},
			expectedVersion:       "",
			expectedError:         true,
			expectedErrorContains: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctrl := gomock.NewController(t)
			defer ctrl.Finish()

			mockCincinnatiClient := cincinatti.NewMockClient(ctrl)
			tt.mockSetup(mockCincinnatiClient)

			syncer := &controlPlaneUpgradeSyncer{}

			customerDesired := &api.HCPOpenShiftCluster{
				CustomerProperties: api.HCPOpenShiftClusterCustomerProperties{
					Version: api.VersionProfile{
						ID:           tt.customerDesiredMinor,
						ChannelGroup: tt.channelGroup,
					},
				},
			}

			// Empty active versions - simulating a new cluster
			serviceProviderCluster := &api.ServiceProviderCluster{
				Version: &api.HCPClusterVersion{
					ActiveVersions: []api.HCPClusterActiveVersion{},
				},
			}

			ctx := context.Background()
			result, err := syncer.desiredControlPlaneZVersion(ctx, mockCincinnatiClient, customerDesired, serviceProviderCluster)

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
				if result != tt.expectedVersion {
					t.Errorf("Expected version %q, got %q", tt.expectedVersion, result)
				}
			}
		})
	}
}
