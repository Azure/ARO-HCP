// Copyright 2025 Microsoft Corporation
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

package admission

import (
	"strings"
	"testing"

	"github.com/blang/semver/v4"
	"github.com/stretchr/testify/assert"

	azcorearm "github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"

	"github.com/Azure/ARO-HCP/internal/api"
)

func TestAdmitNodePoolUpdate_VersionValidation(t *testing.T) {
	tests := []struct {
		name             string
		newVersion       string
		activeVersions   []string // current active versions in ServiceProviderNodePool (first is highest)
		clusterVersions  []string // active versions in ServiceProviderCluster (first is highest)
		desiredVersion   string   // desired version in ServiceProviderNodePool.Spec
		expectError      string
		expectErrorCount int
	}{
		{
			name:            "valid z-stream upgrade",
			activeVersions:  []string{"4.17.0"},
			newVersion:      "4.17.1",
			clusterVersions: []string{"4.18.0"},
			desiredVersion:  "4.17.0",
		},
		{
			name:            "valid y-stream upgrade",
			activeVersions:  []string{"4.17.0"},
			newVersion:      "4.18.0",
			clusterVersions: []string{"4.18.0"},
			desiredVersion:  "4.17.0",
		},
		{
			name:            "same version as desired skips validation",
			activeVersions:  []string{"4.17.0"},
			newVersion:      "4.17.0",
			clusterVersions: []string{"4.18.0"},
			desiredVersion:  "4.17.0",
		},
		{
			name:            "downgrade not allowed",
			activeVersions:  []string{"4.18.0"},
			newVersion:      "4.17.0",
			clusterVersions: []string{"4.18.0"},
			desiredVersion:  "4.18.0",
			expectError:     "cannot downgrade",
		},
		{
			name:            "major version change not allowed",
			activeVersions:  []string{"4.17.0"},
			newVersion:      "5.0.0",
			clusterVersions: []string{"5.0.0"},
			desiredVersion:  "4.17.0",
			expectError:     "major version changes are not supported",
		},
		{
			name:            "skipping minor versions not allowed",
			activeVersions:  []string{"4.16.0"},
			newVersion:      "4.18.0",
			clusterVersions: []string{"4.18.0"},
			desiredVersion:  "4.16.0",
			expectError:     "skipping minor versions is not allowed",
		},
		{
			name:            "cannot exceed cluster version",
			activeVersions:  []string{"4.17.0"},
			newVersion:      "4.18.0",
			clusterVersions: []string{"4.17.5"},
			desiredVersion:  "4.17.0",
			expectError:     "cannot exceed control plane version",
		},
		{
			name:            "empty active versions allows any valid new version",
			activeVersions:  []string{},
			newVersion:      "4.18.0",
			clusterVersions: []string{"4.18.0"},
			desiredVersion:  "",
		},
		{
			name:            "empty active versions still validates against cluster",
			activeVersions:  []string{},
			newVersion:      "4.19.0",
			clusterVersions: []string{"4.18.0"},
			desiredVersion:  "",
			expectError:     "cannot exceed control plane version",
		},
		{
			name:            "empty new version skips validation",
			activeVersions:  []string{"4.17.0"},
			newVersion:      "",
			clusterVersions: []string{"4.18.0"},
			desiredVersion:  "4.17.0",
		},
		{
			name:             "multiple validation failures",
			activeVersions:   []string{"4.18.0"},
			newVersion:       "4.15.0",
			clusterVersions:  []string{"4.18.0"},
			desiredVersion:   "4.18.0",
			expectError:      "cannot downgrade",
			expectErrorCount: 2,
		},
		{
			name:            "version already in active versions skips validation",
			activeVersions:  []string{"4.18.0", "4.17.0"},
			newVersion:      "4.17.0",
			clusterVersions: []string{"4.18.0"},
			desiredVersion:  "4.18.0",
		},
		{
			name:            "X.Y format without patch is rejected",
			activeVersions:  []string{"4.17.0"},
			newVersion:      "4.18",
			clusterVersions: []string{"4.18.0"},
			desiredVersion:  "4.17.0",
			expectError:     "invalid node pool version format",
		},
		{
			name:            "prerelease version upgrade is valid",
			activeVersions:  []string{"4.17.0"},
			newVersion:      "4.18.0-rc.1",
			clusterVersions: []string{"4.18.0"},
			desiredVersion:  "4.17.0",
		},
		{
			name:            "nightly version upgrade is valid",
			activeVersions:  []string{"4.17.0"},
			newVersion:      "4.18.0-0.nightly-2024-01-15-123456",
			clusterVersions: []string{"4.18.0"},
			desiredVersion:  "4.17.0",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			newNodePool := &api.HCPOpenShiftClusterNodePool{
				Properties: api.HCPOpenShiftClusterNodePoolProperties{
					Version: api.NodePoolVersionProfile{
						ID:           tt.newVersion,
						ChannelGroup: "stable",
					},
				},
			}
			oldNodePool := &api.HCPOpenShiftClusterNodePool{
				Properties: api.HCPOpenShiftClusterNodePoolProperties{
					Version: api.NodePoolVersionProfile{
						ID: func() string {
							if len(tt.activeVersions) > 0 {
								return tt.activeVersions[0]
							}
							return ""
						}(),
						ChannelGroup: "stable",
					},
				},
			}
			cluster := &api.HCPOpenShiftCluster{
				CustomerProperties: api.HCPOpenShiftClusterCustomerProperties{
					Version: api.VersionProfile{
						ID:           "4.18",
						ChannelGroup: "stable",
					},
				},
			}

			// Build ServiceProviderNodePool with active versions
			var activeVersions []api.HCPNodePoolActiveVersion
			for _, v := range tt.activeVersions {
				ver := semver.MustParse(v)
				activeVersions = append(activeVersions, api.HCPNodePoolActiveVersion{Version: &ver})
			}
			var desiredVer *semver.Version
			if tt.desiredVersion != "" {
				v := semver.MustParse(tt.desiredVersion)
				desiredVer = &v
			}
			spNodePool := &api.ServiceProviderNodePool{
				Spec: api.ServiceProviderNodePoolSpec{
					NodePoolVersion: api.ServiceProviderNodePoolSpecVersion{
						DesiredVersion: desiredVer,
					},
				},
				Status: api.ServiceProviderNodePoolStatus{
					NodePoolVersion: api.ServiceProviderNodePoolStatusVersion{
						ActiveVersions: activeVersions,
					},
				},
			}

			spCluster := serviceProviderClusterWithVersions(t, tt.clusterVersions)

			errs := AdmitNodePoolUpdate(newNodePool, oldNodePool, cluster, spNodePool, spCluster)

			if tt.expectError != "" {
				if len(errs) == 0 {
					t.Fatalf("expected error containing %q, got none", tt.expectError)
				}
				found := false
				for _, e := range errs {
					if strings.Contains(e.Error(), tt.expectError) {
						found = true
						break
					}
				}
				if !found {
					t.Fatalf("expected error containing %q, got %v", tt.expectError, errs)
				}
				if tt.expectErrorCount > 0 && len(errs) != tt.expectErrorCount {
					t.Errorf("expected %d errors, got %d: %v", tt.expectErrorCount, len(errs), errs)
				}
				return
			}

			if len(errs) != 0 {
				t.Fatalf("unexpected errors: %v", errs)
			}
		})
	}
}

func TestAdmitNodePoolUpdate_IncludesAdmitNodePoolChecks(t *testing.T) {
	// Test that AdmitNodePoolUpdate also performs AdmitNodePool checks
	newNodePool := &api.HCPOpenShiftClusterNodePool{
		Properties: api.HCPOpenShiftClusterNodePoolProperties{
			Version: api.NodePoolVersionProfile{
				ID:           "4.17.0",
				ChannelGroup: "fast", // Different from cluster
			},
		},
	}
	oldNodePool := &api.HCPOpenShiftClusterNodePool{
		Properties: api.HCPOpenShiftClusterNodePoolProperties{
			Version: api.NodePoolVersionProfile{
				ID:           "4.16.0",
				ChannelGroup: "stable", // Different from cluster
			},
		},
	}
	cluster := &api.HCPOpenShiftCluster{
		CustomerProperties: api.HCPOpenShiftClusterCustomerProperties{
			Version: api.VersionProfile{
				ID:           "4.18",
				ChannelGroup: "stable", // Different from node pool
			},
		},
	}

	// Create ServiceProviderNodePool with same version as new (so version validation is skipped)
	ver := semver.MustParse("4.17.0")
	spNodePool := &api.ServiceProviderNodePool{
		Spec: api.ServiceProviderNodePoolSpec{
			NodePoolVersion: api.ServiceProviderNodePoolSpecVersion{
				DesiredVersion: &ver,
			},
		},
		Status: api.ServiceProviderNodePoolStatus{
			NodePoolVersion: api.ServiceProviderNodePoolStatusVersion{
				ActiveVersions: []api.HCPNodePoolActiveVersion{{Version: &ver}},
			},
		},
	}

	spCluster := serviceProviderClusterWithVersions(t, []string{"4.18.0"})

	errs := AdmitNodePoolUpdate(newNodePool, oldNodePool, cluster, spNodePool, spCluster)

	if len(errs) == 0 {
		t.Fatal("expected error for channel group mismatch, got none")
	}

	found := false
	for _, e := range errs {
		if strings.Contains(e.Error(), "must be the same as control plane channel group") {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("expected channel group mismatch error, got %v", errs)
	}
}

func serviceProviderClusterWithVersions(t *testing.T, versions []string) *api.ServiceProviderCluster {
	t.Helper()
	var active []api.HCPClusterActiveVersion
	for _, s := range versions {
		v := semver.MustParse(s)
		active = append(active, api.HCPClusterActiveVersion{Version: &v})
	}
	return &api.ServiceProviderCluster{
		Status: api.ServiceProviderClusterStatus{
			ControlPlaneVersion: api.ServiceProviderClusterStatusVersion{
				ActiveVersions: active,
			},
		},
	}
}

func TestAdmitNodePoolCreate(t *testing.T) {
	t.Parallel()
	const (
		clusterVNetSubnetARM = "/subscriptions/12345678-1234-1234-1234-123456789012/resourceGroups/rg/providers/Microsoft.Network/virtualNetworks/vnet/subnets/snet"
		otherVNetSubnetARM   = "/subscriptions/12345678-1234-1234-1234-123456789012/resourceGroups/rg/providers/Microsoft.Network/virtualNetworks/other-vnet/subnets/snet"

		nodePoolVersionNightly417 = "4.17.0-0.nightly-2024-06-01-abcdef"
		nodePoolVersionRc417      = "4.17.0-rc.2"
		nodePoolVersionRc417Low   = "4.17.0-rc.1"
	)
	clusterSubnetID := api.Must(azcorearm.ParseResourceID(clusterVNetSubnetARM))
	otherSubnetID := api.Must(azcorearm.ParseResourceID(otherVNetSubnetARM))

	cluster := func(versionID string) *api.HCPOpenShiftCluster {
		return &api.HCPOpenShiftCluster{
			CustomerProperties: api.HCPOpenShiftClusterCustomerProperties{
				Version: api.VersionProfile{
					ID:           versionID,
					ChannelGroup: "stable",
				},
				Platform: api.CustomerPlatformProfile{
					SubnetID: clusterSubnetID,
				},
			},
		}
	}

	tests := []struct {
		name             string
		clusterVersionID string
		versionID        string
		channelGroup     string
		nodePoolSubnetID *azcorearm.ResourceID
		spCluster        *api.ServiceProviderCluster
		wantErr          bool
		errSubstring     string // required when wantErr is true
	}{
		{
			name:             "channel group must match cluster",
			clusterVersionID: "4.18",
			versionID:        "4.17.0",
			channelGroup:     "fast",
			nodePoolSubnetID: clusterSubnetID,
			wantErr:          true,
			errSubstring:     "must be the same as control plane channel group",
		},
		{
			name:             "subnet must belong to cluster VNet",
			clusterVersionID: "4.18",
			versionID:        "4.17.0",
			channelGroup:     "stable",
			nodePoolSubnetID: otherSubnetID,
			wantErr:          true,
			errSubstring:     "same VNet",
		},
		{
			name:             "valid within skew",
			clusterVersionID: "4.18",
			versionID:        "4.17.0",
			channelGroup:     "stable",
			nodePoolSubnetID: clusterSubnetID,
			wantErr:          false,
		},
		{
			name:             "minor below skew",
			clusterVersionID: "4.18",
			versionID:        "4.15.0",
			channelGroup:     "stable",
			nodePoolSubnetID: clusterSubnetID,
			wantErr:          true,
			errSubstring:     "node pool minor version 4.15 must be within [4.16, 4.18] for cluster minor version 4.18",
		},
		{
			name:             "node pool greater than cluster version",
			clusterVersionID: "4.18",
			versionID:        "4.19.0",
			channelGroup:     "stable",
			nodePoolSubnetID: clusterSubnetID,
			wantErr:          true,
			errSubstring:     "node pool minor version 4.19 must not exceed cluster minor version 4.18",
		},
		{
			name:             "at cluster desired minor passes skew",
			clusterVersionID: "4.18",
			versionID:        "4.18.0",
			channelGroup:     "stable",
			nodePoolSubnetID: clusterSubnetID,
			wantErr:          false,
		},
		{
			name:             "nightly node pool version within skew",
			clusterVersionID: "4.18",
			versionID:        nodePoolVersionNightly417,
			channelGroup:     "stable",
			nodePoolSubnetID: clusterSubnetID,
			wantErr:          false,
		},
		{
			name:             "release candidate node pool version within skew",
			clusterVersionID: "4.18",
			versionID:        nodePoolVersionRc417,
			channelGroup:     "stable",
			nodePoolSubnetID: clusterSubnetID,
			wantErr:          false,
		},
		{
			name:             "lowest active nightly with matching node pool nightly passes",
			clusterVersionID: "4.18",
			versionID:        nodePoolVersionNightly417,
			channelGroup:     "stable",
			nodePoolSubnetID: clusterSubnetID,
			spCluster:        serviceProviderClusterWithVersions(t, []string{"4.18.0", nodePoolVersionNightly417}),
			wantErr:          false,
		},
		{
			name:             "lowest active release candidate with matching node pool passes",
			clusterVersionID: "4.18",
			versionID:        nodePoolVersionRc417Low,
			channelGroup:     "stable",
			nodePoolSubnetID: clusterSubnetID,
			spCluster:        serviceProviderClusterWithVersions(t, []string{"4.18.0", nodePoolVersionRc417Low}),
			wantErr:          false,
		},
		{
			name:             "empty node pool version id skips skew validation",
			clusterVersionID: "4.18",
			versionID:        "",
			channelGroup:     "stable",
			nodePoolSubnetID: clusterSubnetID,
			wantErr:          false,
		},
		{
			name:             "cross-major cluster 5.0 allows node pool 4.21",
			clusterVersionID: "5.0",
			versionID:        "4.21.0",
			channelGroup:     "stable",
			nodePoolSubnetID: clusterSubnetID,
			wantErr:          false,
		},
		{
			name:             "cross-major cluster 5.0 allows node pool 4.22",
			clusterVersionID: "5.0",
			versionID:        "4.22.0",
			channelGroup:     "stable",
			nodePoolSubnetID: clusterSubnetID,
			wantErr:          false,
		},
		{
			name:             "cross-major cluster 5.0 rejects node pool 4.20",
			clusterVersionID: "5.0",
			versionID:        "4.20.0",
			channelGroup:     "stable",
			nodePoolSubnetID: clusterSubnetID,
			wantErr:          true,
			errSubstring:     "must be one of [4.21 4.22]",
		},
		{
			name:             "cross-major cluster 5.1 allows node pool 4.23",
			clusterVersionID: "5.1",
			versionID:        "4.23.0",
			channelGroup:     "stable",
			nodePoolSubnetID: clusterSubnetID,
			wantErr:          false,
		},
		{
			name:             "cross-major cluster 5.1 rejects node pool 4.24",
			clusterVersionID: "5.1",
			versionID:        "4.24.0",
			channelGroup:     "stable",
			nodePoolSubnetID: clusterSubnetID,
			wantErr:          true,
			errSubstring:     "must be one of [4.21 4.22 4.23]",
		},
		{
			name:             "cross-major cluster 5.2 allows node pool 4.23",
			clusterVersionID: "5.2",
			versionID:        "4.23.0",
			channelGroup:     "stable",
			nodePoolSubnetID: clusterSubnetID,
			wantErr:          false,
		},
		{
			name:             "cross-major cluster 5.2 rejects node pool 4.22",
			clusterVersionID: "5.2",
			versionID:        "4.22.0",
			channelGroup:     "stable",
			nodePoolSubnetID: clusterSubnetID,
			wantErr:          true,
			errSubstring:     "must be one of [4.23]",
		},
		{
			name:             "lowest active rejects node pool newer than lowest patch",
			clusterVersionID: "4.18",
			versionID:        "4.18.5",
			channelGroup:     "stable",
			nodePoolSubnetID: clusterSubnetID,
			spCluster:        serviceProviderClusterWithVersions(t, []string{"4.18.0", "4.19.0"}),
			wantErr:          true,
			errSubstring:     "invalid node pool version 4.18.5: cannot exceed control plane version 4.18.0",
		},
		{
			name:             "lowest active allows node pool at lowest patch",
			clusterVersionID: "4.18",
			versionID:        "4.18.0",
			channelGroup:     "stable",
			nodePoolSubnetID: clusterSubnetID,
			spCluster:        serviceProviderClusterWithVersions(t, []string{"4.18.0", "4.19.0"}),
			wantErr:          false,
		},
		{
			name:             "lowest active rejects node pool newer than lowest minor when desired is higher",
			clusterVersionID: "4.18",
			versionID:        "4.18.0",
			channelGroup:     "stable",
			nodePoolSubnetID: clusterSubnetID,
			spCluster:        serviceProviderClusterWithVersions(t, []string{"4.17.0"}),
			wantErr:          true,
			errSubstring:     "invalid node pool version 4.18.0: cannot exceed control plane version 4.17.0",
		},
		{
			name:             "no active control plane versions skips lowest-active validation",
			clusterVersionID: "4.18",
			versionID:        "4.17.0",
			channelGroup:     "stable",
			nodePoolSubnetID: clusterSubnetID,
			spCluster:        serviceProviderClusterWithVersions(t, []string{}),
			wantErr:          false,
		},
		{
			// Cluster desired 5.2 allows 4.23; lowest active 5.0 still applies the 5.0 cross-major allowlist, which excludes 4.23.
			name:             "lowest active cross-major skew stricter than cluster desired rejects node pool",
			clusterVersionID: "5.2",
			versionID:        "4.23.0",
			channelGroup:     "stable",
			nodePoolSubnetID: clusterSubnetID,
			spCluster:        serviceProviderClusterWithVersions(t, []string{"5.0.0", "5.2.0"}),
			wantErr:          true,
			errSubstring:     "must be one of [4.21 4.22]",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			c := cluster(tt.clusterVersionID)
			nodePool := &api.HCPOpenShiftClusterNodePool{
				Properties: api.HCPOpenShiftClusterNodePoolProperties{
					Version: api.NodePoolVersionProfile{ID: tt.versionID, ChannelGroup: tt.channelGroup},
					Platform: api.NodePoolPlatformProfile{
						SubnetID: tt.nodePoolSubnetID,
					},
				},
			}
			spCluster := tt.spCluster
			if spCluster == nil {
				spCluster = &api.ServiceProviderCluster{}
			}
			errs := AdmitNodePoolCreate(nodePool, c, spCluster)
			if !tt.wantErr {
				assert.Empty(t, errs)
				return
			}
			assert.NotEmpty(t, errs)
			assert.NotEmpty(t, tt.errSubstring, "errSubstring is required when wantErr is true")
			assert.Contains(t, errs.ToAggregate().Error(), tt.errSubstring)
		})
	}
}
