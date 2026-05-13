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
	"testing"

	"github.com/blang/semver/v4"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"k8s.io/apimachinery/pkg/api/operation"
	"k8s.io/utils/ptr"

	azcorearm "github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"

	resourcesapi "github.com/Azure/ARO-HCP/internal/apis/resources"
	armresourcesapi "github.com/Azure/ARO-HCP/internal/apis/resources/arm"
	"github.com/Azure/ARO-HCP/internal/utils"
	"github.com/Azure/ARO-HCP/internal/validation"
)

func TestMutateNodePool(t *testing.T) {
	const (
		clusterSubnet  = "/subscriptions/00000000-0000-0000-0000-000000000000/resourceGroups/rg/providers/Microsoft.Network/virtualNetworks/cluster-vnet/subnets/cluster-subnet"
		nodePoolSubnet = "/subscriptions/00000000-0000-0000-0000-000000000000/resourceGroups/rg/providers/Microsoft.Network/virtualNetworks/np-vnet/subnets/np-subnet"
	)

	parseID := func(s string) *azcorearm.ResourceID {
		return resourcesapi.Must(azcorearm.ParseResourceID(s))
	}

	admissionContextWithClusterSubnet := func(subnetID string) *NodePoolAdmissionContext {
		c := &resourcesapi.HCPOpenShiftCluster{}
		if subnetID != "" {
			c.CustomerProperties.Platform.SubnetID = parseID(subnetID)
		}
		return &NodePoolAdmissionContext{Cluster: c}
	}

	nodePoolWithSubnet := func(subnetID string) *resourcesapi.HCPOpenShiftClusterNodePool {
		np := &resourcesapi.HCPOpenShiftClusterNodePool{}
		if subnetID != "" {
			np.Properties.Platform.SubnetID = parseID(subnetID)
		}
		return np
	}

	tests := []struct {
		name             string
		op               operation.Type
		admissionContext *NodePoolAdmissionContext
		oldObj           *resourcesapi.HCPOpenShiftClusterNodePool // nil for create
		newObj           *resourcesapi.HCPOpenShiftClusterNodePool
		expected         *resourcesapi.HCPOpenShiftClusterNodePool
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
		return resourcesapi.Must(azcorearm.ParseResourceID(s))
	}

	cluster := &resourcesapi.HCPOpenShiftCluster{
		CustomerProperties: resourcesapi.HCPOpenShiftClusterCustomerProperties{
			Platform: resourcesapi.CustomerPlatformProfile{SubnetID: parseID(clusterSubnet)},
			Version:  resourcesapi.VersionProfile{ChannelGroup: "stable"},
		},
	}

	nodePoolWithSubnet := func(subnetID string) *resourcesapi.HCPOpenShiftClusterNodePool {
		np := &resourcesapi.HCPOpenShiftClusterNodePool{
			Properties: resourcesapi.HCPOpenShiftClusterNodePoolProperties{
				Version: resourcesapi.NodePoolVersionProfile{ChannelGroup: "stable"},
			},
		}
		if subnetID != "" {
			np.Properties.Platform.SubnetID = parseID(subnetID)
		}
		return np
	}

	tests := []struct {
		name         string
		newObj       *resourcesapi.HCPOpenShiftClusterNodePool
		oldObj       *resourcesapi.HCPOpenShiftClusterNodePool
		expectErrors []utils.ExpectedError
	}{
		{
			name:         "create: subnet matches cluster subnet (same cluster reuse allowed)",
			newObj:       nodePoolWithSubnet(clusterSubnet),
			expectErrors: []utils.ExpectedError{},
		},
		{
			name:         "create: subnet in same VNet allowed",
			newObj:       nodePoolWithSubnet(sameVNetSubnet),
			expectErrors: []utils.ExpectedError{},
		},
		{
			name:   "create: subnet in different VNet rejected",
			newObj: nodePoolWithSubnet(differentVNetSubnet),
			expectErrors: []utils.ExpectedError{
				{FieldPath: "properties.platform.subnetId", Message: "must belong to the same VNet as the parent cluster VNet"},
			},
		},
		{
			name:         "update: unchanged subnet in different VNet not re-validated",
			oldObj:       nodePoolWithSubnet(differentVNetSubnet),
			newObj:       nodePoolWithSubnet(differentVNetSubnet),
			expectErrors: []utils.ExpectedError{},
		},
		{
			name:   "update: subnet changed to different VNet rejected",
			oldObj: nodePoolWithSubnet(sameVNetSubnet),
			newObj: nodePoolWithSubnet(differentVNetSubnet),
			expectErrors: []utils.ExpectedError{
				{FieldPath: "properties.platform.subnetId", Message: "must belong to the same VNet as the parent cluster VNet"},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			errs := AdmitNodePool(tt.newObj, tt.oldObj, cluster)
			utils.VerifyErrorsMatch(t, tt.expectErrors, errs)
		})
	}
}

// assertNodePoolEqual compares node pools via their JSON representations so
// that pointers to types with unexported fields (e.g. *azcorearm.ResourceID)
// are compared by their externally-visible state.
func assertNodePoolEqual(t *testing.T, expected, actual *resourcesapi.HCPOpenShiftClusterNodePool) {
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
		expectErrors       []utils.ExpectedError
	}{
		{
			name:            "valid z-stream upgrade",
			activeVersions:  []string{"4.17.0"},
			newVersion:      "4.17.1",
			clusterVersions: []string{"4.18.0"},
			desiredVersion:  "4.17.0",
			expectErrors:    []utils.ExpectedError{},
		},
		{
			name:            "valid y-stream upgrade",
			activeVersions:  []string{"4.17.0"},
			newVersion:      "4.18.0",
			clusterVersions: []string{"4.18.0"},
			desiredVersion:  "4.17.0",
			expectErrors:    []utils.ExpectedError{},
		},
		{
			name:            "same version as desired skips validation",
			activeVersions:  []string{"4.17.0"},
			newVersion:      "4.17.0",
			clusterVersions: []string{"4.18.0"},
			desiredVersion:  "4.17.0",
			expectErrors:    []utils.ExpectedError{},
		},
		{
			name:            "downgrade not allowed",
			activeVersions:  []string{"4.18.0"},
			newVersion:      "4.17.0",
			clusterVersions: []string{"4.18.0"},
			desiredVersion:  "4.18.0",
			expectErrors: []utils.ExpectedError{
				{FieldPath: "properties.version.id", Message: "cannot downgrade from current version"},
				{FieldPath: "properties.version.id", Message: "cannot downgrade from version"},
			},
		},
		{
			name:            "major version change not allowed by default",
			activeVersions:  []string{"4.22.0"},
			newVersion:      "5.0.0",
			clusterVersions: []string{"5.0.0"},
			desiredVersion:  "4.22.0",
			expectErrors: []utils.ExpectedError{
				{FieldPath: "properties.version.id", Message: "major version changes are not supported"},
			},
		},
		{
			name:               "valid major upgrade 4.22 to 5.0",
			activeVersions:     []string{"4.22.0"},
			newVersion:         "5.0.0",
			clusterVersions:    []string{"5.0.0"},
			desiredVersion:     "4.22.0",
			allowMajorUpgrades: true,
			expectErrors:       []utils.ExpectedError{},
		},
		{
			name:               "valid major upgrade 4.23 to 5.1",
			activeVersions:     []string{"4.23.0"},
			newVersion:         "5.1.0",
			clusterVersions:    []string{"5.1.0"},
			desiredVersion:     "4.23.0",
			allowMajorUpgrades: true,
			expectErrors:       []utils.ExpectedError{},
		},
		{
			name:               "invalid major upgrade 4.22 to 5.1",
			activeVersions:     []string{"4.22.0"},
			newVersion:         "5.1.0",
			clusterVersions:    []string{"5.1.0"},
			desiredVersion:     "4.22.0",
			allowMajorUpgrades: true,
			expectErrors: []utils.ExpectedError{
				{FieldPath: "properties.version.id", Message: "4.22 can only upgrade to 5.0"},
			},
		},
		{
			name:               "invalid major upgrade 4.23 to 5.0",
			activeVersions:     []string{"4.23.0"},
			newVersion:         "5.0.0",
			clusterVersions:    []string{"5.0.0"},
			desiredVersion:     "4.23.0",
			allowMajorUpgrades: true,
			expectErrors: []utils.ExpectedError{
				{FieldPath: "properties.version.id", Message: "4.23 can only upgrade to 5.1"},
			},
		},
		{
			name:               "invalid major upgrade 4.20 not supported",
			activeVersions:     []string{"4.20.0"},
			newVersion:         "5.0.0",
			clusterVersions:    []string{"5.0.0"},
			desiredVersion:     "4.20.0",
			allowMajorUpgrades: true,
			expectErrors: []utils.ExpectedError{
				{FieldPath: "properties.version.id", Message: "major version upgrades are not supported"},
			},
		},
		{
			name:            "skipping minor versions not allowed",
			activeVersions:  []string{"4.16.0"},
			newVersion:      "4.18.0",
			clusterVersions: []string{"4.18.0"},
			desiredVersion:  "4.16.0",
			expectErrors: []utils.ExpectedError{
				{FieldPath: "properties.version.id", Message: "skipping minor versions is not allowed"},
			},
		},
		{
			name:            "cannot exceed cluster version",
			activeVersions:  []string{"4.17.0"},
			newVersion:      "4.18.0",
			clusterVersions: []string{"4.17.5"},
			desiredVersion:  "4.17.0",
			expectErrors: []utils.ExpectedError{
				{FieldPath: "properties.version.id", Message: "cannot exceed control plane version"},
			},
		},
		{
			name:            "empty active versions allows any valid new version",
			activeVersions:  []string{},
			newVersion:      "4.18.0",
			clusterVersions: []string{"4.18.0"},
			desiredVersion:  "",
			expectErrors:    []utils.ExpectedError{},
		},
		{
			name:            "empty active versions still validates against cluster",
			activeVersions:  []string{},
			newVersion:      "4.19.0",
			clusterVersions: []string{"4.18.0"},
			desiredVersion:  "",
			expectErrors: []utils.ExpectedError{
				{FieldPath: "properties.version.id", Message: "cannot exceed control plane version"},
			},
		},
		{
			name:            "empty new version skips validation",
			activeVersions:  []string{"4.17.0"},
			newVersion:      "",
			clusterVersions: []string{"4.18.0"},
			desiredVersion:  "4.17.0",
			expectErrors:    []utils.ExpectedError{},
		},
		{
			name:            "multiple validation failures",
			activeVersions:  []string{"4.18.0"},
			newVersion:      "4.15.0",
			clusterVersions: []string{"4.18.0"},
			desiredVersion:  "4.18.0",
			expectErrors: []utils.ExpectedError{
				{FieldPath: "properties.version.id", Message: "cannot downgrade from current version"},
				{FieldPath: "properties.version.id", Message: "cannot downgrade from version"},
			},
		},
		{
			name:            "version already in active versions skips validation",
			activeVersions:  []string{"4.18.0", "4.17.0"},
			newVersion:      "4.18.0",
			clusterVersions: []string{"4.18.0"},
			desiredVersion:  "4.18.0",
			expectErrors:    []utils.ExpectedError{},
		},
		{
			name:            "X.Y format without patch is rejected",
			activeVersions:  []string{"4.17.0"},
			newVersion:      "4.18",
			clusterVersions: []string{"4.18.0"},
			desiredVersion:  "4.17.0",
			expectErrors: []utils.ExpectedError{
				{FieldPath: "properties.version.id", Message: "invalid node pool version format"},
			},
		},
		{
			name:            "prerelease version upgrade is valid",
			activeVersions:  []string{"4.17.0"},
			newVersion:      "4.18.0-rc.1",
			clusterVersions: []string{"4.18.0"},
			desiredVersion:  "4.17.0",
			expectErrors:    []utils.ExpectedError{},
		},
		{
			name:            "nightly version upgrade is valid",
			activeVersions:  []string{"4.17.0"},
			newVersion:      "4.18.0-0.nightly-2024-01-15-123456",
			clusterVersions: []string{"4.18.0"},
			desiredVersion:  "4.17.0",
			expectErrors:    []utils.ExpectedError{},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			newNodePool := &resourcesapi.HCPOpenShiftClusterNodePool{
				Properties: resourcesapi.HCPOpenShiftClusterNodePoolProperties{
					Version: resourcesapi.NodePoolVersionProfile{
						ID:           tt.newVersion,
						ChannelGroup: "stable",
					},
				},
			}
			oldNodePool := &resourcesapi.HCPOpenShiftClusterNodePool{
				Properties: resourcesapi.HCPOpenShiftClusterNodePoolProperties{
					Version: resourcesapi.NodePoolVersionProfile{
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
			cluster := &resourcesapi.HCPOpenShiftCluster{
				CustomerProperties: resourcesapi.HCPOpenShiftClusterCustomerProperties{
					Version: resourcesapi.VersionProfile{
						ID:           "4.18",
						ChannelGroup: "stable",
					},
				},
			}

			// Create operation based on allowMajorUpgrades flag
			var op operation.Operation
			if tt.allowMajorUpgrades {
				op = operation.Operation{
					Type: operation.Update,
					Options: validation.AFECsToValidationOptions([]armresourcesapi.Feature{{
						Name:  ptr.To(resourcesapi.FeatureExperimentalReleaseFeatures),
						State: ptr.To("Registered"),
					}}),
				}
			} else {
				op = operation.Operation{Type: operation.Update}
			}

			// Build ServiceProviderNodePool with active versions
			var activeVersions []resourcesapi.HCPNodePoolActiveVersion
			for _, v := range tt.activeVersions {
				ver := semver.MustParse(v)
				activeVersions = append(activeVersions, resourcesapi.HCPNodePoolActiveVersion{Version: &ver})
			}
			var desiredVer *semver.Version
			if tt.desiredVersion != "" {
				v := semver.MustParse(tt.desiredVersion)
				desiredVer = &v
			}
			spNodePool := &resourcesapi.ServiceProviderNodePool{
				Spec: resourcesapi.ServiceProviderNodePoolSpec{
					NodePoolVersion: resourcesapi.ServiceProviderNodePoolSpecVersion{
						DesiredVersion: desiredVer,
					},
				},
				Status: resourcesapi.ServiceProviderNodePoolStatus{
					NodePoolVersion: resourcesapi.ServiceProviderNodePoolStatusVersion{
						ActiveVersions: activeVersions,
					},
				},
			}

			// Build ServiceProviderCluster with active versions
			var clusterActiveVersions []resourcesapi.HCPClusterActiveVersion
			for _, v := range tt.clusterVersions {
				ver := semver.MustParse(v)
				clusterActiveVersions = append(clusterActiveVersions, resourcesapi.HCPClusterActiveVersion{Version: &ver})
			}
			spCluster := &resourcesapi.ServiceProviderCluster{
				Status: resourcesapi.ServiceProviderClusterStatus{
					ControlPlaneVersion: resourcesapi.ServiceProviderClusterStatusVersion{
						ActiveVersions: clusterActiveVersions,
					},
				},
			}

			errs := AdmitNodePoolUpdate(newNodePool, oldNodePool, cluster, spNodePool, spCluster, op)
			utils.VerifyErrorsMatch(t, tt.expectErrors, errs)
		})
	}
}

func TestAdmitNodePoolUpdate_IncludesAdmitNodePoolChecks(t *testing.T) {
	// Test that AdmitNodePoolUpdate also performs AdmitNodePool checks
	newNodePool := &resourcesapi.HCPOpenShiftClusterNodePool{
		Properties: resourcesapi.HCPOpenShiftClusterNodePoolProperties{
			Version: resourcesapi.NodePoolVersionProfile{
				ID:           "4.17.0",
				ChannelGroup: "fast", // Different from cluster
			},
		},
	}
	oldNodePool := &resourcesapi.HCPOpenShiftClusterNodePool{
		Properties: resourcesapi.HCPOpenShiftClusterNodePoolProperties{
			Version: resourcesapi.NodePoolVersionProfile{
				ID:           "4.16.0",
				ChannelGroup: "stable", // Different from cluster
			},
		},
	}
	cluster := &resourcesapi.HCPOpenShiftCluster{
		CustomerProperties: resourcesapi.HCPOpenShiftClusterCustomerProperties{
			Version: resourcesapi.VersionProfile{
				ID:           "4.18",
				ChannelGroup: "stable", // Different from node pool
			},
		},
	}

	// Create ServiceProviderNodePool with same version as new (so version validation is skipped)
	ver := semver.MustParse("4.17.0")
	spNodePool := &resourcesapi.ServiceProviderNodePool{
		Spec: resourcesapi.ServiceProviderNodePoolSpec{
			NodePoolVersion: resourcesapi.ServiceProviderNodePoolSpecVersion{
				DesiredVersion: &ver,
			},
		},
		Status: resourcesapi.ServiceProviderNodePoolStatus{
			NodePoolVersion: resourcesapi.ServiceProviderNodePoolStatusVersion{
				ActiveVersions: []resourcesapi.HCPNodePoolActiveVersion{{Version: &ver}},
			},
		},
	}

	clusterVer := semver.MustParse("4.18.0")
	spCluster := &resourcesapi.ServiceProviderCluster{
		Status: resourcesapi.ServiceProviderClusterStatus{
			ControlPlaneVersion: resourcesapi.ServiceProviderClusterStatusVersion{
				ActiveVersions: []resourcesapi.HCPClusterActiveVersion{{Version: &clusterVer}},
			},
		},
	}

	// Empty update operation (no experimental features)
	op := operation.Operation{Type: operation.Update}

	errs := AdmitNodePoolUpdate(newNodePool, oldNodePool, cluster, spNodePool, spCluster, op)

	expectedErrors := []utils.ExpectedError{
		{FieldPath: "properties.version.channelGroup", Message: "must be the same as control plane channel group"},
	}

	utils.VerifyErrorsMatch(t, expectedErrors, errs)
}
