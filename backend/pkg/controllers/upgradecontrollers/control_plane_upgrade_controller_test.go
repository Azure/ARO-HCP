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

	configv1 "github.com/openshift/api/config/v1"
	"go.uber.org/mock/gomock"

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
			name:                 "Z-stream upgrade - multiple candidates, selects gateway",
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
						{Version: "4.20.5"},
					},
					[]configv1.ConditionalUpdate{},
					nil,
				)

				// Mock next minor checks - algorithm checks versions in descending order
				// 4.19.22 is checked first (latest), has gateway to 4.20
				mc.EXPECT().GetUpdates(
					gomock.Any(),
					gomock.Any(),
					"multi",
					"multi",
					"stable-4.20",
					mustParse("4.19.22"),
				).Return(
					configv1.Release{Version: "4.19.22"},
					[]configv1.Release{{Version: "4.20.5"}},
					[]configv1.ConditionalUpdate{},
					nil,
				)
			},
			expectedVersion: "4.19.22",
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
			name:                 "Y-stream upgrade - direct path available",
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

				// Mock next minor check for gateway detection (4.20.15)
				mc.EXPECT().GetUpdates(
					gomock.Any(),
					gomock.Any(),
					"multi",
					"multi",
					"stable-4.21",
					mustParse("4.20.15"),
				).Return(
					configv1.Release{Version: "4.20.15"},
					[]configv1.Release{
						{Version: "4.21.0"},
					},
					[]configv1.ConditionalUpdate{},
					nil,
				)
			},
			expectedVersion: "4.20.15",
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
			name:                 "Y-stream upgrade - finds gateway in target minor",
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
						{Version: "4.21.0"},
						{Version: "4.20.10"},
					},
					[]configv1.ConditionalUpdate{},
					nil,
				)

				// Mock next minor check for 4.20.15 - has gateway to 4.21
				mc.EXPECT().GetUpdates(
					gomock.Any(),
					gomock.Any(),
					"multi",
					"multi",
					"stable-4.21",
					mustParse("4.20.15"),
				).Return(
					configv1.Release{Version: "4.20.15"},
					[]configv1.Release{
						{Version: "4.21.0"},
					},
					[]configv1.ConditionalUpdate{},
					nil,
				)
			},
			expectedVersion: "4.20.15",
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
