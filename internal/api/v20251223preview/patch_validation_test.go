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

package v20251223preview

import (
	"testing"

	"github.com/stretchr/testify/require"

	"k8s.io/utils/ptr"

	"github.com/Azure/ARO-HCP/internal/api/v20251223preview/generated"
)

// TestClusterConvertToInternal_RejectsNullOnRequiredFields verifies that
// ConvertToInternal returns validation errors when required fields are nil.
// This implements Option B from the DDR: PATCH with null on a required field
// returns 400 BadRequest.
func TestClusterConvertToInternal_RejectsNullOnRequiredFields(t *testing.T) {
	tests := []struct {
		name          string
		cluster       *HcpOpenShiftCluster
		expectedField string
	}{
		{
			name: "null HostPrefix is rejected",
			cluster: &HcpOpenShiftCluster{
				generated.HcpOpenShiftCluster{
					Properties: &generated.HcpOpenShiftClusterProperties{
						Network: &generated.NetworkProfile{
							HostPrefix: nil,
						},
					},
				},
			},
			expectedField: "properties.network.hostPrefix",
		},
		{
			name: "null MaxPodGracePeriodSeconds is rejected",
			cluster: &HcpOpenShiftCluster{
				generated.HcpOpenShiftCluster{
					Properties: &generated.HcpOpenShiftClusterProperties{
						Autoscaling: &generated.ClusterAutoscalingProfile{
							MaxPodGracePeriodSeconds:    nil,
							MaxNodeProvisionTimeSeconds: ptr.To(int32(900)),
							PodPriorityThreshold:        ptr.To(int32(-10)),
						},
					},
				},
			},
			expectedField: "properties.autoscaling.maxPodGracePeriodSeconds",
		},
		{
			name: "null MaxNodeProvisionTimeSeconds is rejected",
			cluster: &HcpOpenShiftCluster{
				generated.HcpOpenShiftCluster{
					Properties: &generated.HcpOpenShiftClusterProperties{
						Autoscaling: &generated.ClusterAutoscalingProfile{
							MaxPodGracePeriodSeconds:    ptr.To(int32(600)),
							MaxNodeProvisionTimeSeconds: nil,
							PodPriorityThreshold:        ptr.To(int32(-10)),
						},
					},
				},
			},
			expectedField: "properties.autoscaling.maxNodeProvisionTimeSeconds",
		},
		{
			name: "null PodPriorityThreshold is rejected",
			cluster: &HcpOpenShiftCluster{
				generated.HcpOpenShiftCluster{
					Properties: &generated.HcpOpenShiftClusterProperties{
						Autoscaling: &generated.ClusterAutoscalingProfile{
							MaxPodGracePeriodSeconds:    ptr.To(int32(600)),
							MaxNodeProvisionTimeSeconds: ptr.To(int32(900)),
							PodPriorityThreshold:        nil,
						},
					},
				},
			},
			expectedField: "properties.autoscaling.podPriorityThreshold",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := tt.cluster.ConvertToInternal(nil)
			require.Error(t, err, "expected validation error for null required field")
			require.Contains(t, err.Error(), tt.expectedField)
		})
	}
}

// TestClusterConvertToInternal_AcceptsExplicitZero verifies that explicit
// zero values (not null) are accepted and preserved.
func TestClusterConvertToInternal_AcceptsExplicitZero(t *testing.T) {
	cluster := &HcpOpenShiftCluster{
		generated.HcpOpenShiftCluster{
			Properties: &generated.HcpOpenShiftClusterProperties{
				Network: &generated.NetworkProfile{
					HostPrefix: ptr.To(int32(0)),
				},
				Autoscaling: &generated.ClusterAutoscalingProfile{
					MaxPodGracePeriodSeconds:    ptr.To(int32(0)),
					MaxNodeProvisionTimeSeconds: ptr.To(int32(0)),
					PodPriorityThreshold:        ptr.To(int32(0)),
				},
			},
		},
	}

	result, err := cluster.ConvertToInternal(nil)
	require.NoError(t, err)
	require.Equal(t, int32(0), result.CustomerProperties.Network.HostPrefix)
	require.Equal(t, int32(0), result.CustomerProperties.Autoscaling.MaxPodGracePeriodSeconds)
	require.Equal(t, int32(0), result.CustomerProperties.Autoscaling.MaxNodeProvisionTimeSeconds)
	require.Equal(t, int32(0), result.CustomerProperties.Autoscaling.PodPriorityThreshold)
}

// TestNodePoolConvertToInternal_RejectsNullAutoRepair verifies that
// ConvertToInternal returns a validation error when AutoRepair is nil.
func TestNodePoolConvertToInternal_RejectsNullAutoRepair(t *testing.T) {
	np := &NodePool{
		generated.NodePool{
			Properties: &generated.NodePoolProperties{
				AutoRepair: nil,
			},
		},
	}

	_, err := np.ConvertToInternal(nil)
	require.Error(t, err)
	require.Contains(t, err.Error(), "properties.autoRepair")
}

// TestNodePoolConvertToInternal_AcceptsExplicitFalseAutoRepair verifies that
// AutoRepair=false (explicit, not null) is accepted and preserved.
func TestNodePoolConvertToInternal_AcceptsExplicitFalseAutoRepair(t *testing.T) {
	np := &NodePool{
		generated.NodePool{
			Properties: &generated.NodePoolProperties{
				AutoRepair: ptr.To(false),
			},
		},
	}

	result, err := np.ConvertToInternal(nil)
	require.NoError(t, err)
	require.Equal(t, false, result.Properties.AutoRepair)
}

// TestNewHCPOpenShiftCluster_NilInput verifies that NewHCPOpenShiftCluster(nil)
// returns a non-nil, defaulted struct ready for unmarshaling.
func TestNewHCPOpenShiftCluster_NilInput(t *testing.T) {
	v := version{}
	result := v.NewHCPOpenShiftCluster(nil)
	require.NotNil(t, result, "NewHCPOpenShiftCluster(nil) must not return nil")
}

// TestNewHCPOpenShiftClusterNodePool_NilInput verifies that
// NewHCPOpenShiftClusterNodePool(nil) returns a non-nil, defaulted struct.
func TestNewHCPOpenShiftClusterNodePool_NilInput(t *testing.T) {
	v := version{}
	result := v.NewHCPOpenShiftClusterNodePool(nil)
	require.NotNil(t, result, "NewHCPOpenShiftClusterNodePool(nil) must not return nil")
}
