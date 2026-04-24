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
	"time"

	"github.com/blang/semver/v4"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"k8s.io/apimachinery/pkg/api/operation"
	utilsclock "k8s.io/utils/clock"
	clocktesting "k8s.io/utils/clock/testing"
	"k8s.io/utils/ptr"

	azcorearm "github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"

	"github.com/Azure/ARO-HCP/internal/api"
	"github.com/Azure/ARO-HCP/internal/api/arm"
	"github.com/Azure/ARO-HCP/internal/utils"
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
		return &NodePoolAdmissionContext{Clock: utilsclock.RealClock{}, Cluster: c}
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
			// Clear the deadline before comparison — it's time-dependent and tested separately.
			tt.newObj.ServiceProviderProperties.CreateOperationCompletionDeadline = nil
			assertNodePoolEqual(t, tt.expected, tt.newObj)
		})
	}
}

func TestMutateNodePoolCreateOperationCompletionDeadline(t *testing.T) {
	afecRegistered := &arm.Subscription{
		Properties: &arm.SubscriptionProperties{
			RegisteredFeatures: &[]arm.Feature{
				{
					Name:  ptr.To(api.FeatureExperimentalReleaseFeatures),
					State: ptr.To("Registered"),
				},
			},
		},
	}
	noAFEC := &arm.Subscription{
		Properties: &arm.SubscriptionProperties{},
	}

	fixedNow, _ := time.Parse(time.RFC3339, "2025-01-15T10:00:00Z")
	fakeClock := clocktesting.NewFakePassiveClock(fixedNow)

	tests := []struct {
		name             string
		subscription     *arm.Subscription
		tags             map[string]string
		op               operation.Operation
		expectErrors     []utils.ExpectedError
		expectDeadline   bool
		expectedDuration time.Duration
	}{
		{
			name:             "CREATE defaults to 60 minutes",
			subscription:     noAFEC,
			op:               operation.Operation{Type: operation.Create},
			expectDeadline:   true,
			expectedDuration: 60 * time.Minute,
		},
		{
			name:         "UPDATE does not set deadline",
			subscription: noAFEC,
			op:           operation.Operation{Type: operation.Update},
		},
		{
			name:             "AFEC registered with max-creation-duration tag overrides default",
			subscription:     afecRegistered,
			tags:             map[string]string{api.TagNodePoolMaxCreationDuration: "19m"},
			op:               operation.Operation{Type: operation.Create},
			expectDeadline:   true,
			expectedDuration: 19 * time.Minute,
		},
		{
			name:             "AFEC registered without tag uses default",
			subscription:     afecRegistered,
			op:               operation.Operation{Type: operation.Create},
			expectDeadline:   true,
			expectedDuration: 60 * time.Minute,
		},
		{
			name:             "no AFEC ignores max-creation-duration tag, uses default",
			subscription:     noAFEC,
			tags:             map[string]string{api.TagNodePoolMaxCreationDuration: "19m"},
			op:               operation.Operation{Type: operation.Create},
			expectDeadline:   true,
			expectedDuration: 60 * time.Minute,
		},
		{
			name:         "AFEC registered with invalid duration value",
			subscription: afecRegistered,
			tags:         map[string]string{api.TagNodePoolMaxCreationDuration: "not-a-duration"},
			op:           operation.Operation{Type: operation.Create},
			expectErrors: []utils.ExpectedError{
				{FieldPath: "tags", Message: "must be a valid Go duration string"},
			},
		},
		{
			name:             "AFEC registered with unrecognized experimental nodepool tag",
			subscription:     afecRegistered,
			tags:             map[string]string{"aro-hcp.experimental.nodepool.unknown": "value"},
			op:               operation.Operation{Type: operation.Create},
			expectDeadline:   true,
			expectedDuration: 60 * time.Minute,
			expectErrors: []utils.ExpectedError{
				{FieldPath: "tags", Message: "unrecognized experimental tag"},
			},
		},
		{
			name:             "nil subscription still sets default deadline",
			subscription:     nil,
			op:               operation.Operation{Type: operation.Create},
			expectDeadline:   true,
			expectedDuration: 60 * time.Minute,
		},
		{
			name:             "AFEC registered with empty string tag uses default",
			subscription:     afecRegistered,
			tags:             map[string]string{api.TagNodePoolMaxCreationDuration: ""},
			op:               operation.Operation{Type: operation.Create},
			expectDeadline:   true,
			expectedDuration: 60 * time.Minute,
		},
		{
			name:             "AFEC registered with case insensitive tag key",
			subscription:     afecRegistered,
			tags:             map[string]string{"ARO-HCP.Experimental.Nodepool.Max-Creation-Duration": "25m"},
			op:               operation.Operation{Type: operation.Create},
			expectDeadline:   true,
			expectedDuration: 25 * time.Minute,
		},
		{
			name:             "AFEC registered with compound duration",
			subscription:     afecRegistered,
			tags:             map[string]string{api.TagNodePoolMaxCreationDuration: "1h30m"},
			op:               operation.Operation{Type: operation.Create},
			expectDeadline:   true,
			expectedDuration: 90 * time.Minute,
		},
		{
			name:             "AFEC registered with unrecognized experimental tag in mixed case",
			subscription:     afecRegistered,
			tags:             map[string]string{"ARO-HCP.Experimental.Nodepool.Unknown-Feature": "value"},
			op:               operation.Operation{Type: operation.Create},
			expectDeadline:   true,
			expectedDuration: 60 * time.Minute,
			expectErrors: []utils.ExpectedError{
				{FieldPath: "tags", Message: "unrecognized experimental tag"},
			},
		},
		{
			name:             "non-experimental tags are ignored",
			subscription:     afecRegistered,
			tags:             map[string]string{"environment": "dev", "team": "platform"},
			op:               operation.Operation{Type: operation.Create},
			expectDeadline:   true,
			expectedDuration: 60 * time.Minute,
		},
		{
			name:             "valid tag alongside unrecognized experimental tag fails",
			subscription:     afecRegistered,
			tags:             map[string]string{api.TagNodePoolMaxCreationDuration: "19m", "aro-hcp.experimental.nodepool.unknown": "value"},
			op:               operation.Operation{Type: operation.Create},
			expectDeadline:   true,
			expectedDuration: 19 * time.Minute,
			expectErrors: []utils.ExpectedError{
				{FieldPath: "tags", Message: "unrecognized experimental tag"},
			},
		},
		{
			name:             "no AFEC ignores unrecognized experimental nodepool tags",
			subscription:     noAFEC,
			tags:             map[string]string{"aro-hcp.experimental.nodepool.unknown": "value"},
			op:               operation.Operation{Type: operation.Create},
			expectDeadline:   true,
			expectedDuration: 60 * time.Minute,
		},
		{
			name:         "AFEC registered with duration less than one minute is rejected",
			subscription: afecRegistered,
			tags:         map[string]string{api.TagNodePoolMaxCreationDuration: "30s"},
			op:           operation.Operation{Type: operation.Create},
			expectErrors: []utils.ExpectedError{
				{FieldPath: "tags", Message: "must be at least 1m0s"},
			},
		},
		{
			name:         "AFEC registered with zero duration is rejected",
			subscription: afecRegistered,
			tags:         map[string]string{api.TagNodePoolMaxCreationDuration: "0s"},
			op:           operation.Operation{Type: operation.Create},
			expectErrors: []utils.ExpectedError{
				{FieldPath: "tags", Message: "must be at least 1m0s"},
			},
		},
		{
			name:         "AFEC registered with negative duration is rejected",
			subscription: afecRegistered,
			tags:         map[string]string{api.TagNodePoolMaxCreationDuration: "-5m"},
			op:           operation.Operation{Type: operation.Create},
			expectErrors: []utils.ExpectedError{
				{FieldPath: "tags", Message: "must be at least 1m0s"},
			},
		},
		{
			name:             "AFEC registered with exactly one minute is accepted",
			subscription:     afecRegistered,
			tags:             map[string]string{api.TagNodePoolMaxCreationDuration: "1m"},
			op:               operation.Operation{Type: operation.Create},
			expectDeadline:   true,
			expectedDuration: time.Minute,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			nodePool := &api.HCPOpenShiftClusterNodePool{
				TrackedResource: arm.TrackedResource{
					Tags: tt.tags,
				},
			}
			admissionContext := &NodePoolAdmissionContext{
				Clock:            fakeClock,
				Subscription:     tt.subscription,
				OriginalNodePool: nodePool.DeepCopy(),
				Cluster:          &api.HCPOpenShiftCluster{},
			}
			errs := MutateNodePool(context.Background(), admissionContext, tt.op, nodePool, nil)

			utils.VerifyErrorsMatch(t, tt.expectErrors, errs)

			if !tt.expectDeadline {
				if nodePool.ServiceProviderProperties.CreateOperationCompletionDeadline != nil {
					t.Errorf("expected no deadline, got %v", nodePool.ServiceProviderProperties.CreateOperationCompletionDeadline)
				}
				return
			}

			deadline := nodePool.ServiceProviderProperties.CreateOperationCompletionDeadline
			if deadline == nil {
				t.Fatal("expected deadline to be set, got nil")
			}

			expected := fixedNow.Add(tt.expectedDuration)
			if !deadline.Time.Equal(expected) {
				t.Errorf("expected deadline %v, got %v", expected, deadline.Time)
			}
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

	newAdmissionContext := func(withServiceProvider bool) *NodePoolAdmissionContext {
		admissionContext := &NodePoolAdmissionContext{
			Cluster: cluster,
		}
		if withServiceProvider {
			version := semver.MustParse("4.14.0")
			admissionContext.ServiceProviderNodePool = &api.ServiceProviderNodePool{
				Spec: api.ServiceProviderNodePoolSpec{
					NodePoolVersion: api.ServiceProviderNodePoolSpecVersion{
						DesiredVersion: &version,
					},
				},
				Status: api.ServiceProviderNodePoolStatus{
					NodePoolVersion: api.ServiceProviderNodePoolStatusVersion{
						ActiveVersions: []api.HCPNodePoolActiveVersion{
							{Version: &version},
						},
					},
				},
			}
			admissionContext.ServiceProviderCluster = &api.ServiceProviderCluster{
				Status: api.ServiceProviderClusterStatus{
					ControlPlaneVersion: api.ServiceProviderClusterStatusVersion{
						ActiveVersions: []api.HCPClusterActiveVersion{
							{Version: &version},
						},
					},
				},
			}
		}
		return admissionContext
	}

	tests := []struct {
		name             string
		op               operation.Type
		newObj           *api.HCPOpenShiftClusterNodePool
		oldObj           *api.HCPOpenShiftClusterNodePool
		admissionContext *NodePoolAdmissionContext
		expectErrors     []utils.ExpectedError
	}{
		{
			name:             "create: subnet matches cluster subnet (same cluster reuse allowed)",
			op:               operation.Create,
			newObj:           nodePoolWithSubnet(clusterSubnet),
			admissionContext: newAdmissionContext(false),
			expectErrors:     []utils.ExpectedError{},
		},
		{
			name:             "create: subnet in same VNet allowed",
			op:               operation.Create,
			newObj:           nodePoolWithSubnet(sameVNetSubnet),
			admissionContext: newAdmissionContext(false),
			expectErrors:     []utils.ExpectedError{},
		},
		{
			name:             "create: subnet in different VNet rejected",
			op:               operation.Create,
			newObj:           nodePoolWithSubnet(differentVNetSubnet),
			admissionContext: newAdmissionContext(false),
			expectErrors: []utils.ExpectedError{
				{FieldPath: "properties.platform.subnetId", Message: "must belong to the same VNet as the parent cluster VNet"},
			},
		},
		{
			name:             "update: unchanged subnet in different VNet not re-validated",
			op:               operation.Update,
			oldObj:           nodePoolWithSubnet(differentVNetSubnet),
			newObj:           nodePoolWithSubnet(differentVNetSubnet),
			admissionContext: newAdmissionContext(true),
			expectErrors:     []utils.ExpectedError{},
		},
		{
			name:             "update: subnet changed to different VNet rejected",
			op:               operation.Update,
			oldObj:           nodePoolWithSubnet(sameVNetSubnet),
			newObj:           nodePoolWithSubnet(differentVNetSubnet),
			admissionContext: newAdmissionContext(true),
			expectErrors: []utils.ExpectedError{
				{FieldPath: "properties.platform.subnetId", Message: "must belong to the same VNet as the parent cluster VNet"},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			errs := AdmitNodePool(context.Background(), tt.admissionContext, operation.Operation{Type: tt.op}, tt.newObj, tt.oldObj)
			utils.VerifyErrorsMatch(t, tt.expectErrors, errs)
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

func TestAdmitNodePool_VersionValidation(t *testing.T) {
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
			name:            "z-stream downgrade within skew succeeds",
			activeVersions:  []string{"4.18.5"},
			newVersion:      "4.18.2",
			clusterVersions: []string{"4.18.5"},
			desiredVersion:  "4.18.5",
		},
		{
			name:            "y-stream downgrade succeeds",
			activeVersions:  []string{"4.18.0"},
			newVersion:      "4.17.0",
			clusterVersions: []string{"4.18.0"},
			desiredVersion:  "4.18.0",
		},
		{
			name:            "cross-major downgrade fails without flag",
			activeVersions:  []string{"5.0.1"},
			newVersion:      "4.22.0",
			clusterVersions: []string{"5.0.1"},
			desiredVersion:  "5.0.1",
			expectErrors: []utils.ExpectedError{
				{FieldPath: "properties.version.id", Message: "cross-major version operations are not supported"},
			},
		},
		{
			name:               "cross-major downgrade succeeds with flag",
			activeVersions:     []string{"5.0.1"},
			newVersion:         "4.22.0",
			clusterVersions:    []string{"5.0.1"},
			desiredVersion:     "5.0.1",
			allowMajorUpgrades: true,
			expectErrors:       []utils.ExpectedError{},
		},
		{
			name:               "cross-major downgrade to unsupported minor fails",
			activeVersions:     []string{"5.0.1"},
			newVersion:         "4.20.0",
			clusterVersions:    []string{"5.0.1"},
			desiredVersion:     "5.0.1",
			allowMajorUpgrades: true,
			expectErrors: []utils.ExpectedError{
				{FieldPath: "properties.version.id", Message: "not allowed to coexist with a different-major control plane"},
			},
		},
		{
			name:               "cross-major downgrade to incompatible CP minor fails",
			activeVersions:     []string{"5.0.1"},
			newVersion:         "4.23.0",
			clusterVersions:    []string{"5.0.1"},
			desiredVersion:     "5.0.1",
			allowMajorUpgrades: true,
			expectErrors: []utils.ExpectedError{
				{FieldPath: "properties.version.id", Message: "cannot coexist with control plane version"},
			},
		},
		{
			name:            "downgrade at N-2 boundary succeeds (4.21 to 4.19)",
			activeVersions:  []string{"4.21.5"},
			newVersion:      "4.19.0",
			clusterVersions: []string{"4.21.5"},
			desiredVersion:  "4.21.5",
			expectErrors:    []utils.ExpectedError{},
		},
		{
			name:            "downgrade beyond N-2 fails",
			activeVersions:  []string{"4.21.5"},
			newVersion:      "4.18.0",
			clusterVersions: []string{"4.21.5"},
			desiredVersion:  "4.21.5",
			expectErrors: []utils.ExpectedError{
				{FieldPath: "properties.version.id", Message: "must be within 2 minor versions"},
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
			name:            "y-stream upgrade skipping two minor versions is allowed",
			activeVersions:  []string{"4.16.0"},
			newVersion:      "4.18.0",
			clusterVersions: []string{"4.18.0"},
			desiredVersion:  "4.16.0",
			expectErrors:    []utils.ExpectedError{},
		},
		{
			name:            "y-stream upgrade skipping three minor versions is rejected",
			activeVersions:  []string{"4.16.0"},
			newVersion:      "4.19.0",
			clusterVersions: []string{"4.19.0"},
			desiredVersion:  "4.16.0",
			expectErrors: []utils.ExpectedError{
				{FieldPath: "properties.version.id", Message: "skipping more than 2 minor versions"},
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
		// Multi-element activeVersions tests
		{
			name:            "multi-active: upgrade skip uses lowest - fail",
			activeVersions:  []string{"4.18.0", "4.20.0"},
			newVersion:      "4.21.0",
			clusterVersions: []string{"4.21.0"},
			desiredVersion:  "4.18.0",
			expectErrors: []utils.ExpectedError{
				{FieldPath: "properties.version.id", Message: "skipping more than 2 minor versions"},
			},
		},
		{
			name:            "multi-active: upgrade within +2 of lowest - pass",
			activeVersions:  []string{"4.18.0", "4.20.0"},
			newVersion:      "4.20.5",
			clusterVersions: []string{"4.20.5"},
			desiredVersion:  "4.18.0",
			expectErrors:    []utils.ExpectedError{},
		},
		{
			name:            "multi-active: mid-upgrade downgrade beyond N-2 - fail",
			activeVersions:  []string{"4.18.0", "4.20.0"},
			newVersion:      "4.17.0",
			clusterVersions: []string{"4.20.0"},
			desiredVersion:  "4.18.0",
			expectErrors: []utils.ExpectedError{
				{FieldPath: "properties.version.id", Message: "must be within 2 minor versions of control plane"},
			},
		},
		{
			name:            "multi-active: mid-upgrade downgrade within N-2 - pass",
			activeVersions:  []string{"4.18.0", "4.20.0"},
			newVersion:      "4.18.5",
			clusterVersions: []string{"4.20.0"},
			desiredVersion:  "4.18.0",
			expectErrors:    []utils.ExpectedError{},
		},
		// Cross-major downgrade: additional skew map entries
		{
			name:               "valid major downgrade 5.0 to 4.21",
			activeVersions:     []string{"5.0.1"},
			newVersion:         "4.21.0",
			clusterVersions:    []string{"5.0.1"},
			desiredVersion:     "5.0.1",
			allowMajorUpgrades: true,
			expectErrors:       []utils.ExpectedError{},
		},
		// Cross-major skew: same-major NP change when CP is different major
		{
			name:               "same-major NP change with cross-major CP - valid skew",
			activeVersions:     []string{"4.22.0"},
			newVersion:         "4.21.0",
			clusterVersions:    []string{"5.0.1"},
			desiredVersion:     "4.22.0",
			allowMajorUpgrades: true,
			expectErrors:       []utils.ExpectedError{},
		},
		{
			name:               "same-major NP change with cross-major CP - invalid skew",
			activeVersions:     []string{"4.22.0"},
			newVersion:         "4.15.0",
			clusterVersions:    []string{"5.0.1"},
			desiredVersion:     "4.22.0",
			allowMajorUpgrades: true,
			expectErrors: []utils.ExpectedError{
				{FieldPath: "properties.version.id", Message: "not allowed to coexist with a different-major control plane"},
			},
		},
		{
			name:            "same-major NP change with cross-major CP - rejected without AFEC",
			activeVersions:  []string{"4.22.0"},
			newVersion:      "4.21.0",
			clusterVersions: []string{"5.0.1"},
			desiredVersion:  "4.22.0",
			expectErrors: []utils.ExpectedError{
				{FieldPath: "properties.version.id", Message: "cross-major version operations are not supported"},
			},
		},
		// Multi-version CP: N-2 skew uses highest CP version
		{
			name:            "multi-CP: N-2 skew checked against highest CP version",
			activeVersions:  []string{"4.21.0"},
			newVersion:      "4.18.0",
			clusterVersions: []string{"4.20.0", "4.21.0"},
			desiredVersion:  "4.21.0",
			expectErrors: []utils.ExpectedError{
				{FieldPath: "properties.version.id", Message: "must be within 2 minor versions"},
			},
		},
		{
			name:            "multi-CP: N-2 boundary passes against highest CP version",
			activeVersions:  []string{"4.21.0"},
			newVersion:      "4.19.0",
			clusterVersions: []string{"4.20.0", "4.21.0"},
			desiredVersion:  "4.21.0",
			expectErrors:    []utils.ExpectedError{},
		},
		// Empty activeVersions with desired below CP
		{
			name:            "empty active versions with desired below CP - pass",
			activeVersions:  []string{},
			newVersion:      "4.17.0",
			clusterVersions: []string{"4.18.0"},
			desiredVersion:  "",
			expectErrors:    []utils.ExpectedError{},
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

			errs := AdmitNodePool(context.Background(), &NodePoolAdmissionContext{
				Cluster:                 cluster,
				ServiceProviderNodePool: spNodePool,
				ServiceProviderCluster:  spCluster,
			}, op, newNodePool, oldNodePool)
			utils.VerifyErrorsMatch(t, tt.expectErrors, errs)
		})
	}
}

func TestAdmitNodePool_AllowsDifferentChannelGroupClusterAndNodePool(t *testing.T) {
	newNodePool := &api.HCPOpenShiftClusterNodePool{
		Properties: api.HCPOpenShiftClusterNodePoolProperties{
			Version: api.NodePoolVersionProfile{
				ID:           "4.17.0",
				ChannelGroup: "fast",
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

	op := operation.Operation{Type: operation.Create}

	errs := AdmitNodePool(context.Background(), &NodePoolAdmissionContext{
		Cluster:                 cluster,
		ServiceProviderNodePool: spNodePool,
		ServiceProviderCluster:  spCluster,
	}, op, newNodePool, nil)

	require.Empty(t, errs)
}

func TestAdmitNodePoolOnDelete(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	clusterResourceID := api.Must(azcorearm.ParseResourceID(
		"/subscriptions/00000000-0000-0000-0000-000000000000/resourceGroups/rg/providers/Microsoft.RedHatOpenShift/hcpOpenShiftClusters/cluster"))

	makeTestNodePool := func(name string) *api.HCPOpenShiftClusterNodePool {
		nodePoolResourceID := api.Must(azcorearm.ParseResourceID(clusterResourceID.String() + "/nodePools/" + name))
		return &api.HCPOpenShiftClusterNodePool{
			CosmosMetadata: arm.CosmosMetadata{
				ResourceID: nodePoolResourceID,
			},
			TrackedResource: arm.NewTrackedResource(nodePoolResourceID, "eastus"),
		}
	}

	makeDeletingNodePool := func(name string) *api.HCPOpenShiftClusterNodePool {
		nodePool := makeTestNodePool(name)
		nodePool.Properties.ProvisioningState = arm.ProvisioningStateDeleting
		return nodePool
	}

	tests := []struct {
		name                 string
		existingNodePools    []*api.HCPOpenShiftClusterNodePool
		nodePoolBeingDeleted *api.HCPOpenShiftClusterNodePool
		expectErrors         []utils.ExpectedError
	}{
		{
			name:                 "allows delete when another node pool exists",
			existingNodePools:    []*api.HCPOpenShiftClusterNodePool{makeTestNodePool("workers"), makeTestNodePool("infra")},
			nodePoolBeingDeleted: makeTestNodePool("workers"),
			expectErrors:         []utils.ExpectedError{},
		},
		{
			name: "allows delete when the only other remaining node pool is being deleted",
			existingNodePools: []*api.HCPOpenShiftClusterNodePool{
				makeDeletingNodePool("workers"),
				makeTestNodePool("infra"),
			},
			nodePoolBeingDeleted: makeTestNodePool("infra"),
			expectErrors:         []utils.ExpectedError{},
		},
		{
			name:                 "rejects delete of last node pool",
			existingNodePools:    []*api.HCPOpenShiftClusterNodePool{makeTestNodePool("workers")},
			nodePoolBeingDeleted: makeTestNodePool("workers"),
			expectErrors: []utils.ExpectedError{
				{FieldPath: "name", Message: "The last node pool can not be deleted from a cluster."},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			admissionContext := &NodePoolDeleteAdmissionContext{
				ClusterNodePools: tt.existingNodePools,
			}

			errs := AdmitNodePoolOnDelete(ctx, admissionContext, tt.nodePoolBeingDeleted)
			utils.VerifyErrorsMatch(t, tt.expectErrors, errs)
		})
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
