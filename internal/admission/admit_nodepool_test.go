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
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/blang/semver/v4"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"k8s.io/apimachinery/pkg/api/operation"
	"k8s.io/utils/ptr"

	azcorearm "github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"

	"github.com/Azure/ARO-HCP/internal/api"
	"github.com/Azure/ARO-HCP/internal/api/arm"
	"github.com/Azure/ARO-HCP/internal/validation"
)

func TestMutateNodePool(t *testing.T) {
	const (
		clusterSubnet  = "/subscriptions/00000000-0000-0000-0000-000000000000/resourceGroups/rg/providers/Microsoft.Network/virtualNetworks/cluster-vnet/subnets/cluster-subnet"
		nodePoolSubnet = "/subscriptions/00000000-0000-0000-0000-000000000000/resourceGroups/rg/providers/Microsoft.Network/virtualNetworks/np-vnet/subnets/np-subnet"
	)

	parseID := func(s string) *azcorearm.ResourceID {
		return api.Must(azcorearm.ParseResourceID(s))
	}

	admissionContextWithClusterSubnet := func(subnetID string) *NodePoolAdmissionContext {
		c := &api.HCPOpenShiftCluster{}
		if subnetID != "" {
			c.CustomerProperties.Platform.SubnetID = parseID(subnetID)
		}
		return &NodePoolAdmissionContext{Cluster: c}
	}

	nodePoolWithSubnet := func(subnetID string) *api.HCPOpenShiftClusterNodePool {
		np := &api.HCPOpenShiftClusterNodePool{}
		if subnetID != "" {
			np.Properties.Platform.SubnetID = parseID(subnetID)
		}
		return np
	}

	tests := []struct {
		name             string
		op               operation.Type
		admissionContext *NodePoolAdmissionContext
		oldObj           *api.HCPOpenShiftClusterNodePool // nil for create
		newObj           *api.HCPOpenShiftClusterNodePool
		expected         *api.HCPOpenShiftClusterNodePool
	}{
		{
			name:             "create: nil nodepool subnet defaults to cluster subnet",
			op:               operation.Create,
			admissionContext: admissionContextWithClusterSubnet(clusterSubnet),
			oldObj:           nil,
			newObj:           nodePoolWithSubnet(""),
			expected:         nodePoolWithSubnet(clusterSubnet),
		},
		{
			name:             "create: nodepool subnet preserved when set",
			op:               operation.Create,
			admissionContext: admissionContextWithClusterSubnet(clusterSubnet),
			oldObj:           nil,
			newObj:           nodePoolWithSubnet(nodePoolSubnet),
			expected:         nodePoolWithSubnet(nodePoolSubnet),
		},
		{
			name:             "update: nil nodepool subnet not defaulted",
			op:               operation.Update,
			admissionContext: admissionContextWithClusterSubnet(clusterSubnet),
			oldObj:           nodePoolWithSubnet(clusterSubnet),
			newObj:           nodePoolWithSubnet(""),
			expected:         nodePoolWithSubnet(""),
		},
		{
			name:             "update: nodepool subnet preserved when set",
			op:               operation.Update,
			admissionContext: admissionContextWithClusterSubnet(clusterSubnet),
			oldObj:           nodePoolWithSubnet(nodePoolSubnet),
			newObj:           nodePoolWithSubnet(nodePoolSubnet),
			expected:         nodePoolWithSubnet(nodePoolSubnet),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			errs := MutateNodePool(
				context.Background(),
				tt.admissionContext,
				operation.Operation{Type: tt.op},
				tt.newObj,
				tt.oldObj,
			)
			require.Empty(t, errs)
			assertNodePoolEqual(t, tt.expected, tt.newObj)
		})
	}
}

func TestAdmitNodePool_SubnetVNet(t *testing.T) {
	const (
		clusterSubnet       = "/subscriptions/00000000-0000-0000-0000-000000000000/resourceGroups/rg/providers/Microsoft.Network/virtualNetworks/cluster-vnet/subnets/cluster-subnet"
		sameVNetSubnet      = "/subscriptions/00000000-0000-0000-0000-000000000000/resourceGroups/rg/providers/Microsoft.Network/virtualNetworks/cluster-vnet/subnets/np-subnet"
		differentVNetSubnet = "/subscriptions/00000000-0000-0000-0000-000000000000/resourceGroups/rg/providers/Microsoft.Network/virtualNetworks/other-vnet/subnets/np-subnet"
	)

	parseID := func(s string) *azcorearm.ResourceID {
		return api.Must(azcorearm.ParseResourceID(s))
	}

	cluster := &api.HCPOpenShiftCluster{
		CustomerProperties: api.HCPOpenShiftClusterCustomerProperties{
			Platform: api.CustomerPlatformProfile{SubnetID: parseID(clusterSubnet)},
			Version:  api.VersionProfile{ChannelGroup: "stable"},
		},
	}

	nodePoolWithSubnet := func(subnetID string) *api.HCPOpenShiftClusterNodePool {
		np := &api.HCPOpenShiftClusterNodePool{
			Properties: api.HCPOpenShiftClusterNodePoolProperties{
				Version: api.NodePoolVersionProfile{ChannelGroup: "stable"},
			},
		}
		if subnetID != "" {
			np.Properties.Platform.SubnetID = parseID(subnetID)
		}
		return np
	}

	createOperation := operation.Operation{Type: operation.Create}
	updateOperation := operation.Operation{Type: operation.Update}

	tests := []struct {
		name      string
		newObj    *api.HCPOpenShiftClusterNodePool
		oldObj    *api.HCPOpenShiftClusterNodePool
		op        operation.Operation
		expectErr string
	}{
		{
			name:   "create: subnet matches cluster subnet (same cluster reuse allowed)",
			newObj: nodePoolWithSubnet(clusterSubnet),
			op:     createOperation,
		},
		{
			name:   "create: subnet in same VNet allowed",
			newObj: nodePoolWithSubnet(sameVNetSubnet),
			op:     createOperation,
		},
		{
			name:      "create: subnet in different VNet rejected",
			newObj:    nodePoolWithSubnet(differentVNetSubnet),
			op:        createOperation,
			expectErr: "must belong to the same VNet as the parent cluster VNet",
		},
		{
			name:   "update: unchanged subnet in different VNet not re-validated",
			oldObj: nodePoolWithSubnet(differentVNetSubnet),
			newObj: nodePoolWithSubnet(differentVNetSubnet),
			op:     updateOperation,
		},
		{
			name:      "update: subnet changed to different VNet rejected",
			oldObj:    nodePoolWithSubnet(sameVNetSubnet),
			newObj:    nodePoolWithSubnet(differentVNetSubnet),
			expectErr: "must belong to the same VNet as the parent cluster VNet",
			op:        updateOperation,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			errs := admitNodePoolCommon(tt.newObj, tt.oldObj, cluster, tt.op)
			if tt.expectErr == "" {
				assert.Empty(t, errs)
				return
			}
			require.NotEmpty(t, errs)
			found := false
			for _, e := range errs {
				if strings.Contains(e.Error(), tt.expectErr) {
					found = true
					break
				}
			}
			assert.True(t, found, "expected error containing %q, got %v", tt.expectErr, errs)
		})
	}
}

// assertNodePoolEqual compares node pools via their JSON representations so
// that pointers to types with unexported fields (e.g. *azcorearm.ResourceID)
// are compared by their externally-visible state.
func assertNodePoolEqual(t *testing.T, expected, actual *api.HCPOpenShiftClusterNodePool) {
	t.Helper()
	expectedJSON, err := json.MarshalIndent(expected, "", "  ")
	require.NoError(t, err)
	actualJSON, err := json.MarshalIndent(actual, "", "  ")
	require.NoError(t, err)
	assert.Equal(t, string(expectedJSON), string(actualJSON))
}

func TestAdmitNodePoolUpdate_VersionValidation(t *testing.T) {
	tests := []struct {
		name               string
		newVersion         string
		activeVersions     []string // current active versions in ServiceProviderNodePool (first is highest)
		clusterVersions    []string // active versions in ServiceProviderCluster (first is highest)
		desiredVersion     string   // desired version in ServiceProviderNodePool.Spec
		allowMajorUpgrades bool     // experimental feature flag
		expectError        string
		expectErrorCount   int
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
			name:            "major version change not allowed by default",
			activeVersions:  []string{"4.22.0"},
			newVersion:      "5.0.0",
			clusterVersions: []string{"5.0.0"},
			desiredVersion:  "4.22.0",
			expectError:     "major version changes are not supported",
		},
		{
			name:               "valid major upgrade 4.22 to 5.0",
			activeVersions:     []string{"4.22.0"},
			newVersion:         "5.0.0",
			clusterVersions:    []string{"5.0.0"},
			desiredVersion:     "4.22.0",
			allowMajorUpgrades: true,
		},
		{
			name:               "valid major upgrade 4.23 to 5.1",
			activeVersions:     []string{"4.23.0"},
			newVersion:         "5.1.0",
			clusterVersions:    []string{"5.1.0"},
			desiredVersion:     "4.23.0",
			allowMajorUpgrades: true,
		},
		{
			name:               "invalid major upgrade 4.22 to 5.1",
			activeVersions:     []string{"4.22.0"},
			newVersion:         "5.1.0",
			clusterVersions:    []string{"5.1.0"},
			desiredVersion:     "4.22.0",
			allowMajorUpgrades: true,
			expectError:        "4.22 can only upgrade to 5.0",
		},
		{
			name:               "invalid major upgrade 4.23 to 5.0",
			activeVersions:     []string{"4.23.0"},
			newVersion:         "5.0.0",
			clusterVersions:    []string{"5.0.0"},
			desiredVersion:     "4.23.0",
			allowMajorUpgrades: true,
			expectError:        "4.23 can only upgrade to 5.1",
		},
		{
			name:               "invalid major upgrade 4.20 not supported",
			activeVersions:     []string{"4.20.0"},
			newVersion:         "5.0.0",
			clusterVersions:    []string{"5.0.0"},
			desiredVersion:     "4.20.0",
			allowMajorUpgrades: true,
			expectError:        "major version upgrades are not supported",
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
			name:            "empty active versions allows any valid new version",
			activeVersions:  []string{},
			newVersion:      "4.18.0",
			clusterVersions: []string{"4.18.0"},
			desiredVersion:  "",
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
			expectErrorCount: 3,
		},
		{
			name:            "version already in active versions skips validation",
			activeVersions:  []string{"4.18.0", "4.17.0"},
			newVersion:      "4.18.0",
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

			// Use cluster version from test case's clusterVersions if cross-major upgrade
			clusterVersion := "4.18"
			if tt.allowMajorUpgrades && len(tt.clusterVersions) > 0 {
				clusterVersion = tt.clusterVersions[0]
			}

			cluster := &api.HCPOpenShiftCluster{
				CustomerProperties: api.HCPOpenShiftClusterCustomerProperties{
					Version: api.VersionProfile{
						ID:           clusterVersion,
						ChannelGroup: "stable",
					},
				},
			}

			// Create operation based on allowMajorUpgrades flag
			var op operation.Operation
			if tt.allowMajorUpgrades {
				op = operation.Operation{
					Type: operation.Update,
					Options: validation.AFECsToValidationOptions([]arm.Feature{{
						Name:  ptr.To(api.FeatureExperimentalReleaseFeatures),
						State: ptr.To("Registered"),
					}}),
				}
			} else {
				op = operation.Operation{Type: operation.Update}
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

			errs := AdmitNodePoolUpdate(newNodePool, oldNodePool, cluster, spNodePool, spCluster, op)

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

	// Empty update operation (no experimental features)
	op := operation.Operation{Type: operation.Update}

	errs := AdmitNodePoolUpdate(newNodePool, oldNodePool, cluster, spNodePool, spCluster, op)

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
	)
	clusterSubnetID := api.Must(azcorearm.ParseResourceID(clusterVNetSubnetARM))

	generateCluster := func(versionID string) *api.HCPOpenShiftCluster {
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
		errSubstring     string
	}{
		{
			name:             "node pool version within n-2 skew is valid",
			clusterVersionID: "4.18",
			versionID:        "4.17.0",
			channelGroup:     "stable",
			nodePoolSubnetID: clusterSubnetID,
			wantErr:          false,
		},
		{
			name:             "node pool version exceeding n-2 skew is invalid",
			clusterVersionID: "4.18",
			versionID:        "4.15.0",
			channelGroup:     "stable",
			nodePoolSubnetID: clusterSubnetID,
			wantErr:          true,
			errSubstring:     "cannot be more than 2 minors below",
		},
		{
			name:             "node pool version cannot exceed cluster version",
			clusterVersionID: "4.18",
			versionID:        "4.19.0",
			channelGroup:     "stable",
			nodePoolSubnetID: clusterSubnetID,
			wantErr:          true,
			errSubstring:     "must not exceed cluster minor version",
		},
		{
			name:             "cross-major 5.0 allows 4.22",
			clusterVersionID: "5.0",
			versionID:        "4.22.0",
			channelGroup:     "stable",
			nodePoolSubnetID: clusterSubnetID,
			wantErr:          false,
		},
		{
			name:             "cross-major 5.0 rejects 4.20",
			clusterVersionID: "5.0",
			versionID:        "4.20.0",
			channelGroup:     "stable",
			nodePoolSubnetID: clusterSubnetID,
			wantErr:          true,
			errSubstring:     "is not compatible with cluster minor version",
		},
		{
			name:             "cross-major 5.2 allows 4.23",
			clusterVersionID: "5.2",
			versionID:        "4.23.0",
			channelGroup:     "stable",
			nodePoolSubnetID: clusterSubnetID,
			wantErr:          false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			c := generateCluster(tt.clusterVersionID)
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
			errs := AdmitNodePoolCreate(nodePool, c, spCluster, operation.Operation{Type: operation.Create})
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

func TestAdmitNodePoolCreate_UpgradingControlPlane(t *testing.T) {
	t.Parallel()
	const clusterVNetSubnetARM = "/subscriptions/12345678-1234-1234-1234-123456789012/resourceGroups/rg/providers/Microsoft.Network/virtualNetworks/vnet/subnets/snet"
	clusterSubnetID := api.Must(azcorearm.ParseResourceID(clusterVNetSubnetARM))

	generateCluster := func(versionID string) *api.HCPOpenShiftCluster {
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
		nodePoolVersion  string
		cpVersions       []string
		wantErr          bool
		errSubstring     string
	}{
		{
			name:             "upgrading CP: node pool at lowest active is valid",
			clusterVersionID: "4.21",
			nodePoolVersion:  "4.20.9",
			cpVersions:       []string{"4.20.9", "4.21.0"},
			wantErr:          false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			cluster := generateCluster(tt.clusterVersionID)
			nodePool := &api.HCPOpenShiftClusterNodePool{
				Properties: api.HCPOpenShiftClusterNodePoolProperties{
					Version: api.NodePoolVersionProfile{
						ID:           tt.nodePoolVersion,
						ChannelGroup: "stable",
					},
					Platform: api.NodePoolPlatformProfile{
						SubnetID: clusterSubnetID,
					},
				},
			}
			spCluster := serviceProviderClusterWithVersions(t, tt.cpVersions)
			errs := AdmitNodePoolCreate(nodePool, cluster, spCluster, operation.Operation{Type: operation.Create})
			if !tt.wantErr {
				assert.Empty(t, errs, "unexpected errors: %v", errs)
				return
			}
			assert.NotEmpty(t, errs)
			assert.NotEmpty(t, tt.errSubstring, "errSubstring is required when wantErr is true")
			assert.Contains(t, errs.ToAggregate().Error(), tt.errSubstring)
		})
	}
}
