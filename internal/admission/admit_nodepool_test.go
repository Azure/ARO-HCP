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

			// Build ServiceProviderCluster with active versions
			var clusterActiveVersions []api.HCPClusterActiveVersion
			for _, v := range tt.clusterVersions {
				ver := semver.MustParse(v)
				clusterActiveVersions = append(clusterActiveVersions, api.HCPClusterActiveVersion{Version: &ver})
			}
			spCluster := &api.ServiceProviderCluster{
				Status: api.ServiceProviderClusterStatus{
					ControlPlaneVersion: api.ServiceProviderClusterStatusVersion{
						ActiveVersions: clusterActiveVersions,
					},
				},
			}

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

	clusterVer := semver.MustParse("4.18.0")
	spCluster := &api.ServiceProviderCluster{
		Status: api.ServiceProviderClusterStatus{
			ControlPlaneVersion: api.ServiceProviderClusterStatusVersion{
				ActiveVersions: []api.HCPClusterActiveVersion{{Version: &clusterVer}},
			},
		},
	}

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
