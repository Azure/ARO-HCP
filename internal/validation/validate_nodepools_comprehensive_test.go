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

package validation

import (
	"context"
	"strings"
	"testing"

	"k8s.io/utils/ptr"

	azcorearm "github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"

	"github.com/Azure/ARO-HCP/internal/api"
	"github.com/Azure/ARO-HCP/internal/api/arm"
)

// Comprehensive tests for ValidateNodePoolCreate
func TestValidateNodePoolCreate(t *testing.T) {
	ctx := context.Background()

	type expectedError struct {
		message   string // Expected error message (partial match)
		fieldPath string // Expected field path for the error
	}

	tests := []struct {
		name         string
		nodePool     *api.HCPOpenShiftClusterNodePool
		expectErrors []expectedError
	}{
		{
			name:         "valid nodepool - create",
			nodePool:     createValidNodePool(),
			expectErrors: []expectedError{},
		},
		{
			name: "valid nodepool with autoscaling - create",
			nodePool: func() *api.HCPOpenShiftClusterNodePool {
				np := createValidNodePool()
				np.Properties.AutoScaling = &api.NodePoolAutoScaling{
					Min: 1,
					Max: 5,
				}
				np.Properties.Replicas = 0 // Should be 0 when autoscaling is enabled
				return np
			}(),
			expectErrors: []expectedError{},
		},
		{
			name: "valid nodepool with autoscaling min=0 - create",
			nodePool: func() *api.HCPOpenShiftClusterNodePool {
				np := createValidNodePool()
				np.Properties.AutoScaling = &api.NodePoolAutoScaling{
					Min: 0,
					Max: 5,
				}
				np.Properties.Replicas = 0
				return np
			}(),
			expectErrors: []expectedError{},
		},
		{
			name: "valid nodepool with labels - create",
			nodePool: func() *api.HCPOpenShiftClusterNodePool {
				np := createValidNodePool()
				np.Properties.Labels = map[string]string{
					"environment":           "test",
					"app.kubernetes.io/env": "production",
					"team":                  "platform",
				}
				return np
			}(),
			expectErrors: []expectedError{},
		},
		{
			name: "valid nodepool with taints - create",
			nodePool: func() *api.HCPOpenShiftClusterNodePool {
				np := createValidNodePool()
				np.Properties.Taints = []api.Taint{
					{
						Key:    "dedicated",
						Value:  "gpu",
						Effect: api.EffectNoSchedule,
					},
					{
						Key:    "environment",
						Value:  "test",
						Effect: api.EffectPreferNoSchedule,
					},
				}
				return np
			}(),
			expectErrors: []expectedError{},
		},
		{
			name: "valid nodepool with encryption set ID - create",
			nodePool: func() *api.HCPOpenShiftClusterNodePool {
				np := createValidNodePool()
				np.Properties.Platform.OSDisk.EncryptionSetID = "/subscriptions/12345678-1234-1234-1234-123456789012/resourceGroups/test-rg/providers/Microsoft.Compute/diskEncryptionSets/test-des"
				return np
			}(),
			expectErrors: []expectedError{},
		},
		{
			name: "valid nodepool with custom OS disk size - create",
			nodePool: func() *api.HCPOpenShiftClusterNodePool {
				np := createValidNodePool()
				np.Properties.Platform.OSDisk.SizeGiB = ptr.To[int32](64)
				return np
			}(),
			expectErrors: []expectedError{},
		},
		{
			name: "valid nodepool with different storage account type - create",
			nodePool: func() *api.HCPOpenShiftClusterNodePool {
				np := createValidNodePool()
				np.Properties.Platform.OSDisk.DiskStorageAccountType = api.DiskStorageAccountTypeStandardSSD_LRS
				return np
			}(),
			expectErrors: []expectedError{},
		},
		{
			name: "valid nodepool with availability zone - create",
			nodePool: func() *api.HCPOpenShiftClusterNodePool {
				np := createValidNodePool()
				np.Properties.Platform.AvailabilityZone = "1"
				return np
			}(),
			expectErrors: []expectedError{},
		},
		{
			name: "valid nodepool with node drain timeout - create",
			nodePool: func() *api.HCPOpenShiftClusterNodePool {
				np := createValidNodePool()
				np.Properties.NodeDrainTimeoutMinutes = ptr.To[int32](60)
				return np
			}(),
			expectErrors: []expectedError{},
		},
		{
			name: "valid nodepool with version ID - create",
			nodePool: func() *api.HCPOpenShiftClusterNodePool {
				np := createValidNodePool()
				np.Properties.Version.ID = "4.15.1"
				np.Properties.Version.ChannelGroup = "fast"
				return np
			}(),
			expectErrors: []expectedError{},
		},
		{
			name: "invalid version ID - create",
			nodePool: func() *api.HCPOpenShiftClusterNodePool {
				np := createValidNodePool()
				np.Properties.Version.ID = "invalid-version"
				return np
			}(),
			expectErrors: []expectedError{
				{message: "Malformed version", fieldPath: "properties.version.id"},
			},
		},
		{
			name: "missing channel group - create",
			nodePool: func() *api.HCPOpenShiftClusterNodePool {
				np := createValidNodePool()
				np.Properties.Version.ChannelGroup = ""
				return np
			}(),
			expectErrors: []expectedError{
				{message: "Required value", fieldPath: "properties.version.channelGroup"},
			},
		},
		{
			name: "missing version ID when channel group is not stable - create",
			nodePool: func() *api.HCPOpenShiftClusterNodePool {
				np := createValidNodePool()
				np.Properties.Version.ID = ""
				np.Properties.Version.ChannelGroup = "fast"
				return np
			}(),
			expectErrors: []expectedError{
				{message: "Required value", fieldPath: "properties.version.id"},
			},
		},
		{
			name: "invalid subnet ID - create",
			nodePool: func() *api.HCPOpenShiftClusterNodePool {
				np := createValidNodePool()
				np.Properties.Platform.SubnetID = "invalid-resource-id"
				return np
			}(),
			expectErrors: []expectedError{
				{message: "invalid resource ID", fieldPath: "properties.platform.subnetId"},
			},
		},
		{
			name: "wrong subnet resource type - create",
			nodePool: func() *api.HCPOpenShiftClusterNodePool {
				np := createValidNodePool()
				np.Properties.Platform.SubnetID = "/subscriptions/12345678-1234-1234-1234-123456789012/resourceGroups/test-rg/providers/Microsoft.Network/virtualNetworks/test-vnet"
				return np
			}(),
			expectErrors: []expectedError{
				{message: "resource ID must reference an instance of type", fieldPath: "properties.platform.subnetId"},
			},
		},
		{
			name: "missing VM size - create",
			nodePool: func() *api.HCPOpenShiftClusterNodePool {
				np := createValidNodePool()
				np.Properties.Platform.VMSize = ""
				return np
			}(),
			expectErrors: []expectedError{
				{message: "Required value", fieldPath: "properties.platform.vmSize"},
			},
		},
		{
			name: "OS disk size too small - create",
			nodePool: func() *api.HCPOpenShiftClusterNodePool {
				np := createValidNodePool()
				np.Properties.Platform.OSDisk.SizeGiB = ptr.To[int32](63)
				return np
			}(),
			expectErrors: []expectedError{
				{message: "must be greater than or equal to 64", fieldPath: "properties.platform.osDisk.sizeGiB"},
			},
		},
		{
			name: "invalid disk storage account type - create",
			nodePool: func() *api.HCPOpenShiftClusterNodePool {
				np := createValidNodePool()
				np.Properties.Platform.OSDisk.DiskStorageAccountType = "InvalidType"
				return np
			}(),
			expectErrors: []expectedError{
				{message: "Unsupported value", fieldPath: "properties.platform.osDisk.diskStorageAccountType"},
			},
		},
		{
			name: "invalid encryption set ID - create",
			nodePool: func() *api.HCPOpenShiftClusterNodePool {
				np := createValidNodePool()
				np.Properties.Platform.OSDisk.EncryptionSetID = "invalid-resource-id"
				return np
			}(),
			expectErrors: []expectedError{
				{message: "invalid resource ID", fieldPath: "properties.platform.osDisk.encryptionSetId"},
			},
		},
		{
			name: "wrong encryption set resource type - create",
			nodePool: func() *api.HCPOpenShiftClusterNodePool {
				np := createValidNodePool()
				np.Properties.Platform.OSDisk.EncryptionSetID = "/subscriptions/12345678-1234-1234-1234-123456789012/resourceGroups/test-rg/providers/Microsoft.Network/virtualNetworks/test-vnet"
				return np
			}(),
			expectErrors: []expectedError{
				{message: "resource ID must reference an instance of type", fieldPath: "properties.platform.osDisk.encryptionSetId"},
			},
		},
		{
			name: "replicas at maximum limit (200) - create",
			nodePool: func() *api.HCPOpenShiftClusterNodePool {
				np := createValidNodePool()
				np.Properties.Replicas = MaxNodePoolNodes
				return np
			}(),
			expectErrors: []expectedError{},
		},
		{
			name: "negative replicas - create",
			nodePool: func() *api.HCPOpenShiftClusterNodePool {
				np := createValidNodePool()
				np.Properties.Replicas = -1
				return np
			}(),
			expectErrors: []expectedError{
				{message: "must be greater than or equal to 0", fieldPath: "properties.replicas"},
			},
		},
		{
			name: "replicas exceeds maximum limit (201) - create",
			nodePool: func() *api.HCPOpenShiftClusterNodePool {
				np := createValidNodePool()
				np.Properties.Replicas = MaxNodePoolNodes + 1
				return np
			}(),
			expectErrors: []expectedError{
				{message: "must be less than or equal to 200", fieldPath: "properties.replicas"},
			},
		},
		{
			name: "non-zero replicas with autoscaling - create",
			nodePool: func() *api.HCPOpenShiftClusterNodePool {
				np := createValidNodePool()
				np.Properties.Replicas = 3
				np.Properties.AutoScaling = &api.NodePoolAutoScaling{
					Min: 1,
					Max: 5,
				}
				return np
			}(),
			expectErrors: []expectedError{
				{message: "must be equal to 0", fieldPath: "properties.replicas"},
			},
		},
		{
			name: "autoscaling max at maximum limit (200) - create",
			nodePool: func() *api.HCPOpenShiftClusterNodePool {
				np := createValidNodePool()
				np.Properties.Replicas = 0
				np.Properties.AutoScaling = &api.NodePoolAutoScaling{
					Min: 1,
					Max: MaxNodePoolNodes,
				}
				return np
			}(),
			expectErrors: []expectedError{},
		},
		{
			name: "autoscaling min too small - create",
			nodePool: func() *api.HCPOpenShiftClusterNodePool {
				np := createValidNodePool()
				np.Properties.Replicas = 0
				np.Properties.AutoScaling = &api.NodePoolAutoScaling{
					Min: -1,
					Max: 5,
				}
				return np
			}(),
			expectErrors: []expectedError{
				{message: "must be greater than or equal to 0", fieldPath: "properties.autoScaling.min"},
			},
		},
		{
			// When Min is invalid (too large), Max is valid, we should only get Min error (not Max >= Min error).
			name: "autoscaling min exceeds limit but max is valid - create",
			nodePool: func() *api.HCPOpenShiftClusterNodePool {
				np := createValidNodePool()
				np.Properties.Replicas = 0
				np.Properties.AutoScaling = &api.NodePoolAutoScaling{
					Min: MaxNodePoolNodes + 1,
					Max: 100,
				}
				return np
			}(),
			expectErrors: []expectedError{
				{message: "must be less than or equal to 200", fieldPath: "properties.autoScaling.min"},
			},
		},
		{
			// When Min is invalid (too small), Max is valid, we should only get Min error (not Max >= Min error).
			name: "autoscaling min negative but max is valid - create",
			nodePool: func() *api.HCPOpenShiftClusterNodePool {
				np := createValidNodePool()
				np.Properties.Replicas = 0
				np.Properties.AutoScaling = &api.NodePoolAutoScaling{
					Min: -1,
					Max: 100,
				}
				return np
			}(),
			expectErrors: []expectedError{
				{message: "must be greater than or equal to 0", fieldPath: "properties.autoScaling.min"},
			},
		},
		{
			name: "autoscaling max less than min - create",
			nodePool: func() *api.HCPOpenShiftClusterNodePool {
				np := createValidNodePool()
				np.Properties.Replicas = 0
				np.Properties.AutoScaling = &api.NodePoolAutoScaling{
					Min: 5,
					Max: 3,
				}
				return np
			}(),
			expectErrors: []expectedError{
				{message: "must be greater than or equal to 5", fieldPath: "properties.autoScaling.max"},
			},
		},
		{
			// Note: Both Min and Max validate max=200 (though logically redundant) for explicit error messages on both fields.
			// When Min is invalid, we skip the Min<=Max check to avoid misleading "Max must be >= invalid_min" errors.
			name: "autoscaling min and max both exceed limit with min > max - create",
			nodePool: func() *api.HCPOpenShiftClusterNodePool {
				np := createValidNodePool()
				np.Properties.Replicas = 0
				np.Properties.AutoScaling = &api.NodePoolAutoScaling{
					Min: MaxNodePoolNodes + 2,
					Max: MaxNodePoolNodes + 1,
				}
				return np
			}(),
			expectErrors: []expectedError{
				{message: "must be less than or equal to 200", fieldPath: "properties.autoScaling.min"},
				{message: "must be less than or equal to 200", fieldPath: "properties.autoScaling.max"},
			},
		},
		{
			name: "invalid label key - create",
			nodePool: func() *api.HCPOpenShiftClusterNodePool {
				np := createValidNodePool()
				np.Properties.Labels = map[string]string{
					"invalid key with spaces": "value",
				}
				return np
			}(),
			expectErrors: []expectedError{
				{message: "name part must consist of alphanumeric characters", fieldPath: "properties.labels"},
			},
		},
		{
			name: "invalid label value - create",
			nodePool: func() *api.HCPOpenShiftClusterNodePool {
				np := createValidNodePool()
				np.Properties.Labels = map[string]string{
					"valid-key": "invalid value with spaces and special chars!@#",
				}
				return np
			}(),
			expectErrors: []expectedError{
				{message: "a valid label must be an empty string or consist of alphanumeric characters", fieldPath: "properties.labels[valid-key]"},
			},
		},
		{
			name: "empty label key - create",
			nodePool: func() *api.HCPOpenShiftClusterNodePool {
				np := createValidNodePool()
				np.Properties.Labels = map[string]string{
					"": "value",
				}
				return np
			}(),
			expectErrors: []expectedError{
				{message: "name part must be non-empty", fieldPath: "properties.labels"},
			},
		},
		{
			name: "taint missing key - create",
			nodePool: func() *api.HCPOpenShiftClusterNodePool {
				np := createValidNodePool()
				np.Properties.Taints = []api.Taint{
					{
						Value:  "test",
						Effect: api.EffectNoSchedule,
					},
				}
				return np
			}(),
			expectErrors: []expectedError{
				{message: "Required value", fieldPath: "properties.taints[0].key"},
			},
		},
		{
			name: "taint invalid key - create",
			nodePool: func() *api.HCPOpenShiftClusterNodePool {
				np := createValidNodePool()
				np.Properties.Taints = []api.Taint{
					{
						Key:    "invalid key with spaces",
						Value:  "test",
						Effect: api.EffectNoSchedule,
					},
				}
				return np
			}(),
			expectErrors: []expectedError{
				{message: "name part must consist of alphanumeric characters", fieldPath: "properties.taints[0].key"},
			},
		},
		{
			name: "taint invalid value - create",
			nodePool: func() *api.HCPOpenShiftClusterNodePool {
				np := createValidNodePool()
				np.Properties.Taints = []api.Taint{
					{
						Key:    "dedicated",
						Value:  "invalid value with spaces and special chars!@#",
						Effect: api.EffectNoSchedule,
					},
				}
				return np
			}(),
			expectErrors: []expectedError{
				{message: "a valid label must be an empty string or consist of alphanumeric characters", fieldPath: "properties.taints[0].value"},
			},
		},
		{
			name: "taint invalid effect - create",
			nodePool: func() *api.HCPOpenShiftClusterNodePool {
				np := createValidNodePool()
				np.Properties.Taints = []api.Taint{
					{
						Key:    "dedicated",
						Value:  "gpu",
						Effect: "InvalidEffect",
					},
				}
				return np
			}(),
			expectErrors: []expectedError{
				{message: "Unsupported value", fieldPath: "properties.taints[0].effect"},
			},
		},
		{
			name: "multiple validation errors - create",
			nodePool: func() *api.HCPOpenShiftClusterNodePool {
				np := createValidNodePool()
				np.Properties.Version.ID = "invalid-version"
				np.Properties.Platform.VMSize = ""
				np.Properties.Platform.OSDisk.SizeGiB = ptr.To[int32](0)
				np.Properties.Replicas = -1
				return np
			}(),
			expectErrors: []expectedError{
				{message: "Malformed version", fieldPath: "properties.version.id"},
				{message: "Required value", fieldPath: "properties.platform.vmSize"},
				{message: "must be greater than or equal to 64", fieldPath: "properties.platform.osDisk.sizeGiB"},
				{message: "must be greater than or equal to 0", fieldPath: "properties.replicas"},
			},
		},
		{
			name: "multiple taint errors - create",
			nodePool: func() *api.HCPOpenShiftClusterNodePool {
				np := createValidNodePool()
				np.Properties.Taints = []api.Taint{
					{
						Value:  "test",
						Effect: api.EffectNoSchedule,
					},
					{
						Key:    "invalid key with spaces",
						Value:  "invalid value with spaces",
						Effect: "InvalidEffect",
					},
				}
				return np
			}(),
			expectErrors: []expectedError{
				{message: "Required value", fieldPath: "properties.taints[0].key"},
				{message: "name part must consist of alphanumeric characters", fieldPath: "properties.taints[1].key"},
				{message: "a valid label must be an empty string or consist of alphanumeric characters", fieldPath: "properties.taints[1].value"},
				{message: "Unsupported value", fieldPath: "properties.taints[1].effect"},
			},
		},
		{
			name: "multiple label errors - create",
			nodePool: func() *api.HCPOpenShiftClusterNodePool {
				np := createValidNodePool()
				np.Properties.Labels = map[string]string{
					"":                        "value1",
					"invalid key with spaces": "value2",
					"valid-key":               "invalid value with spaces!@#",
				}
				return np
			}(),
			expectErrors: []expectedError{
				{message: "name part must be non-empty", fieldPath: "properties.labels"},
				{message: "name part must consist of alphanumeric characters", fieldPath: "properties.labels"},
				{message: "a valid label must be an empty string or consist of alphanumeric characters", fieldPath: "properties.labels[valid-key]"},
			},
		},
		{
			name: "valid empty optional fields - create",
			nodePool: func() *api.HCPOpenShiftClusterNodePool {
				np := createValidNodePool()
				np.Properties.Platform.SubnetID = ""
				np.Properties.Platform.AvailabilityZone = ""
				np.Properties.Platform.OSDisk.EncryptionSetID = ""
				np.Properties.Labels = nil
				np.Properties.Taints = nil
				np.Properties.NodeDrainTimeoutMinutes = nil
				return np
			}(),
			expectErrors: []expectedError{},
		},
		{
			name: "missing location - create",
			nodePool: func() *api.HCPOpenShiftClusterNodePool {
				np := createValidNodePool()
				np.Location = ""
				return np
			}(),
			expectErrors: []expectedError{
				{message: "Required value", fieldPath: "trackedResource.location"},
			},
		},
		{
			name: "replicas exceeds 200 with availability zone set - valid - create",
			nodePool: func() *api.HCPOpenShiftClusterNodePool {
				np := createValidNodePool()
				np.Properties.Platform.AvailabilityZone = "1"
				np.Properties.Replicas = 250
				return np
			}(),
			expectErrors: []expectedError{},
		},
		{
			name: "autoscaling both min and max exceed 200 with availability zone set - valid - create",
			nodePool: func() *api.HCPOpenShiftClusterNodePool {
				np := createValidNodePool()
				np.Properties.Platform.AvailabilityZone = "1"
				np.Properties.Replicas = 0
				np.Properties.AutoScaling = &api.NodePoolAutoScaling{
					Min: 300,
					Max: 1000,
				}
				return np
			}(),
			expectErrors: []expectedError{},
		},
		// Node pool resource naming validation tests (covering middleware_validatestatic_test.go patterns)
		{
			name: "invalid nodepool resource name - special character",
			nodePool: func() *api.HCPOpenShiftClusterNodePool {
				np := createValidNodePool()
				np.ID.Name = "$"
				return np
			}(),
			expectErrors: []expectedError{
				{message: "must be a valid DNS RFC 1035 label", fieldPath: "id"},
			},
		},
		{
			name: "invalid nodepool resource name - starts with hyphen",
			nodePool: func() *api.HCPOpenShiftClusterNodePool {
				np := createValidNodePool()
				np.ID.Name = "-abcde"
				return np
			}(),
			expectErrors: []expectedError{
				{message: "must be a valid DNS RFC 1035 label", fieldPath: "id"},
			},
		},
		{
			name: "invalid nodepool resource name - starts with number",
			nodePool: func() *api.HCPOpenShiftClusterNodePool {
				np := createValidNodePool()
				np.ID.Name = "1nodepool"
				return np
			}(),
			expectErrors: []expectedError{
				{message: "must be a valid DNS RFC 1035 label", fieldPath: "id"},
			},
		},
		{
			name: "invalid nodepool resource name - too long",
			nodePool: func() *api.HCPOpenShiftClusterNodePool {
				np := createValidNodePool()
				np.ID.Name = "07B4gc00vjA2C8KL3Ns4No9fi" // Too long for node pool name
				return np
			}(),
			expectErrors: []expectedError{
				{message: "must be a valid DNS RFC 1035 label", fieldPath: "id"},
			},
		},
		{
			name: "invalid nodepool resource name - too short",
			nodePool: func() *api.HCPOpenShiftClusterNodePool {
				np := createValidNodePool()
				np.ID.Name = "a"
				return np
			}(),
			expectErrors: []expectedError{
				{message: "must be a valid DNS RFC 1035 label", fieldPath: "id"},
			},
		},
		{
			name: "valid nodepool resource name - minimum length",
			nodePool: func() *api.HCPOpenShiftClusterNodePool {
				np := createValidNodePool()
				np.ID.Name = "abc"
				np.Name = "abc"
				return np
			}(),
			expectErrors: []expectedError{},
		},
		{
			name: "valid nodepool resource name - with hyphens",
			nodePool: func() *api.HCPOpenShiftClusterNodePool {
				np := createValidNodePool()
				np.ID.Name = "my-pool-1"
				np.Name = "my-pool-1"
				return np
			}(),
			expectErrors: []expectedError{},
		},
		{
			name: "valid nodepool resource name - maximum length",
			nodePool: func() *api.HCPOpenShiftClusterNodePool {
				np := createValidNodePool()
				np.ID.Name = "myNodePool12345" // 15 chars total - at max length
				np.Name = "myNodePool12345"
				return np
			}(),
			expectErrors: []expectedError{},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			errs := ValidateNodePoolCreate(ctx, tt.nodePool)

			if len(tt.expectErrors) == 0 {
				if len(errs) != 0 {
					t.Errorf("expected no errors, got %d: %v", len(errs), errs)
				}
				return
			}

			// Check that each expected error message and field path is found
			for _, expectedErr := range tt.expectErrors {
				found := false
				for _, err := range errs {
					messageMatch := strings.Contains(err.Detail, expectedErr.message) || strings.Contains(err.Error(), expectedErr.message)
					fieldMatch := strings.Contains(err.Field, expectedErr.fieldPath)
					if messageMatch && fieldMatch {
						found = true
						break
					}
				}
				if !found {
					t.Errorf("expected error containing message '%s' at field '%s' but not found in: %v", expectedErr.message, expectedErr.fieldPath, errs)
				}
			}
		})
	}
}

// Comprehensive tests for ValidateNodePoolUpdate
func TestValidateNodePoolUpdate(t *testing.T) {
	ctx := context.Background()

	type expectedError struct {
		message   string // Expected error message (partial match)
		fieldPath string // Expected field path for the error
	}

	tests := []struct {
		name         string
		newNodePool  *api.HCPOpenShiftClusterNodePool
		oldNodePool  *api.HCPOpenShiftClusterNodePool
		expectErrors []expectedError
	}{
		{
			name:         "valid nodepool update - no changes",
			newNodePool:  createValidNodePool(),
			oldNodePool:  createValidNodePool(),
			expectErrors: []expectedError{},
		},
		{
			name: "valid nodepool update - replicas change",
			newNodePool: func() *api.HCPOpenShiftClusterNodePool {
				np := createValidNodePool()
				np.Properties.Replicas = 5
				return np
			}(),
			oldNodePool: func() *api.HCPOpenShiftClusterNodePool {
				np := createValidNodePool()
				np.Properties.Replicas = 3
				return np
			}(),
			expectErrors: []expectedError{},
		},
		{
			name: "valid nodepool update - autoscaling change",
			newNodePool: func() *api.HCPOpenShiftClusterNodePool {
				np := createValidNodePool()
				np.Properties.Replicas = 0
				np.Properties.AutoScaling = &api.NodePoolAutoScaling{
					Min: 2,
					Max: 10,
				}
				return np
			}(),
			oldNodePool: func() *api.HCPOpenShiftClusterNodePool {
				np := createValidNodePool()
				np.Properties.Replicas = 0
				np.Properties.AutoScaling = &api.NodePoolAutoScaling{
					Min: 1,
					Max: 5,
				}
				return np
			}(),
			expectErrors: []expectedError{},
		},
		{
			name: "valid nodepool update - labels change",
			newNodePool: func() *api.HCPOpenShiftClusterNodePool {
				np := createValidNodePool()
				np.Properties.Labels = map[string]string{
					"environment": "production",
					"team":        "platform",
				}
				return np
			}(),
			oldNodePool: func() *api.HCPOpenShiftClusterNodePool {
				np := createValidNodePool()
				np.Properties.Labels = map[string]string{
					"environment": "test",
				}
				return np
			}(),
			expectErrors: []expectedError{},
		},
		{
			name: "valid nodepool update - taints change",
			newNodePool: func() *api.HCPOpenShiftClusterNodePool {
				np := createValidNodePool()
				np.Properties.Taints = []api.Taint{
					{
						Key:    "dedicated",
						Value:  "gpu",
						Effect: api.EffectNoSchedule,
					},
					{
						Key:    "environment",
						Value:  "test",
						Effect: api.EffectPreferNoSchedule,
					},
				}
				return np
			}(),
			oldNodePool: func() *api.HCPOpenShiftClusterNodePool {
				np := createValidNodePool()
				np.Properties.Taints = []api.Taint{
					{
						Key:    "dedicated",
						Value:  "cpu",
						Effect: api.EffectNoSchedule,
					},
				}
				return np
			}(),
			expectErrors: []expectedError{},
		},
		{
			name: "valid nodepool update - node drain timeout change",
			newNodePool: func() *api.HCPOpenShiftClusterNodePool {
				np := createValidNodePool()
				np.Properties.NodeDrainTimeoutMinutes = ptr.To[int32](120)
				return np
			}(),
			oldNodePool: func() *api.HCPOpenShiftClusterNodePool {
				np := createValidNodePool()
				np.Properties.NodeDrainTimeoutMinutes = ptr.To[int32](60)
				return np
			}(),
			expectErrors: []expectedError{},
		},
		{
			name: "valid nodepool update - version change",
			newNodePool: func() *api.HCPOpenShiftClusterNodePool {
				np := createValidNodePool()
				np.Properties.Version.ID = "4.15.2"
				return np
			}(),
			oldNodePool: func() *api.HCPOpenShiftClusterNodePool {
				np := createValidNodePool()
				np.Properties.Version.ID = "4.15.1"
				return np
			}(),
			expectErrors: []expectedError{},
		},
		{
			name: "immutable provisioning state - update",
			newNodePool: func() *api.HCPOpenShiftClusterNodePool {
				np := createValidNodePool()
				np.Properties.ProvisioningState = arm.ProvisioningStateProvisioning
				return np
			}(),
			oldNodePool: func() *api.HCPOpenShiftClusterNodePool {
				np := createValidNodePool()
				np.Properties.ProvisioningState = arm.ProvisioningStateSucceeded
				return np
			}(),
			expectErrors: []expectedError{
				{message: "field is immutable", fieldPath: "properties.provisioningState"},
			},
		},
		{
			name: "immutable platform profile - update",
			newNodePool: func() *api.HCPOpenShiftClusterNodePool {
				np := createValidNodePool()
				np.Properties.Platform.VMSize = "Standard_D4s_v3"
				return np
			}(),
			oldNodePool: func() *api.HCPOpenShiftClusterNodePool {
				np := createValidNodePool()
				np.Properties.Platform.VMSize = "Standard_D2s_v3"
				return np
			}(),
			expectErrors: []expectedError{
				{message: "field is immutable", fieldPath: "properties.platform"},
			},
		},
		{
			name: "immutable OS disk size - update",
			newNodePool: func() *api.HCPOpenShiftClusterNodePool {
				np := createValidNodePool()
				np.Properties.Platform.OSDisk.SizeGiB = ptr.To[int32](64)
				return np
			}(),
			oldNodePool: func() *api.HCPOpenShiftClusterNodePool {
				np := createValidNodePool()
				np.Properties.Platform.OSDisk.SizeGiB = ptr.To[int32](128)
				return np
			}(),
			expectErrors: []expectedError{
				{message: "field is immutable", fieldPath: "properties.platform"},
			},
		},
		{
			name: "immutable auto repair - update",
			newNodePool: func() *api.HCPOpenShiftClusterNodePool {
				np := createValidNodePool()
				np.Properties.AutoRepair = false
				return np
			}(),
			oldNodePool: func() *api.HCPOpenShiftClusterNodePool {
				np := createValidNodePool()
				np.Properties.AutoRepair = true
				return np
			}(),
			expectErrors: []expectedError{
				{message: "field is immutable", fieldPath: "properties.autoRepair"},
			},
		},
		{
			name: "immutable location - update",
			newNodePool: func() *api.HCPOpenShiftClusterNodePool {
				np := createValidNodePool()
				np.Location = "westus2"
				return np
			}(),
			oldNodePool: func() *api.HCPOpenShiftClusterNodePool {
				np := createValidNodePool()
				np.Location = "eastus"
				return np
			}(),
			expectErrors: []expectedError{
				{message: "field is immutable", fieldPath: "trackedResource.location"},
			},
		},
		{
			name: "invalid new field value on update - update",
			newNodePool: func() *api.HCPOpenShiftClusterNodePool {
				np := createValidNodePool()
				np.Properties.Replicas = -1
				return np
			}(),
			oldNodePool: createValidNodePool(),
			expectErrors: []expectedError{
				{message: "must be greater than or equal to 0", fieldPath: "properties.replicas"},
			},
		},
		{
			name: "scale up to maximum limit - update",
			newNodePool: func() *api.HCPOpenShiftClusterNodePool {
				np := createValidNodePool()
				np.Properties.Replicas = MaxNodePoolNodes
				return np
			}(),
			oldNodePool: func() *api.HCPOpenShiftClusterNodePool {
				np := createValidNodePool()
				return np
			}(),
			expectErrors: []expectedError{},
		},
		{
			name: "replicas exceeds maximum limit - update",
			newNodePool: func() *api.HCPOpenShiftClusterNodePool {
				np := createValidNodePool()
				np.Properties.Replicas = MaxNodePoolNodes + 1
				return np
			}(),
			oldNodePool: createValidNodePool(),
			expectErrors: []expectedError{
				{message: "must be less than or equal to 200", fieldPath: "properties.replicas"},
			},
		},
		{
			name: "autoscaling min and max to maximum limit - update",
			newNodePool: func() *api.HCPOpenShiftClusterNodePool {
				np := createValidNodePool()
				np.Properties.Replicas = 0
				np.Properties.AutoScaling = &api.NodePoolAutoScaling{
					Min: MaxNodePoolNodes,
					Max: MaxNodePoolNodes,
				}
				return np
			}(),
			oldNodePool: func() *api.HCPOpenShiftClusterNodePool {
				np := createValidNodePool()
				return np
			}(),
			expectErrors: []expectedError{},
		},
		{
			name: "invalid autoscaling on update - update",
			newNodePool: func() *api.HCPOpenShiftClusterNodePool {
				np := createValidNodePool()
				np.Properties.Replicas = 0
				np.Properties.AutoScaling = &api.NodePoolAutoScaling{
					Min: 5,
					Max: 3,
				}
				return np
			}(),
			oldNodePool: createValidNodePool(),
			expectErrors: []expectedError{
				{message: "must be greater than or equal to 5", fieldPath: "properties.autoScaling.max"},
			},
		},
		{
			name: "autoscaling min and max exceeds maximum limit with min > max - update",
			newNodePool: func() *api.HCPOpenShiftClusterNodePool {
				np := createValidNodePool()
				np.Properties.Replicas = 0
				np.Properties.AutoScaling = &api.NodePoolAutoScaling{
					Min: MaxNodePoolNodes + 2,
					Max: MaxNodePoolNodes + 1,
				}
				return np
			}(),
			oldNodePool: createValidNodePool(),
			expectErrors: []expectedError{
				{message: "must be less than or equal to 200", fieldPath: "properties.autoScaling.min"},
				{message: "must be less than or equal to 200", fieldPath: "properties.autoScaling.max"},
			},
		},
		{
			name: "invalid labels on update - update",
			newNodePool: func() *api.HCPOpenShiftClusterNodePool {
				np := createValidNodePool()
				np.Properties.Labels = map[string]string{
					"invalid key with spaces": "value",
				}
				return np
			}(),
			oldNodePool: createValidNodePool(),
			expectErrors: []expectedError{
				{message: "name part must consist of alphanumeric characters", fieldPath: "properties.labels"},
			},
		},
		{
			name: "invalid taints on update - update",
			newNodePool: func() *api.HCPOpenShiftClusterNodePool {
				np := createValidNodePool()
				np.Properties.Taints = []api.Taint{
					{
						Key:    "invalid key with spaces",
						Value:  "test",
						Effect: api.EffectNoSchedule,
					},
				}
				return np
			}(),
			oldNodePool: createValidNodePool(),
			expectErrors: []expectedError{
				{message: "name part must consist of alphanumeric characters", fieldPath: "properties.taints[0].key"},
			},
		},
		{
			name: "invalid version on update - update",
			newNodePool: func() *api.HCPOpenShiftClusterNodePool {
				np := createValidNodePool()
				np.Properties.Version.ID = "invalid-version"
				return np
			}(),
			oldNodePool: createValidNodePool(),
			expectErrors: []expectedError{
				{message: "Malformed version", fieldPath: "properties.version.id"},
			},
		},
		{
			name: "multiple immutable field changes - update",
			newNodePool: func() *api.HCPOpenShiftClusterNodePool {
				np := createValidNodePool()
				np.Properties.ProvisioningState = arm.ProvisioningStateProvisioning
				np.Properties.Platform.VMSize = "Standard_D4s_v3"
				np.Properties.AutoRepair = false
				np.Location = "westus2"
				return np
			}(),
			oldNodePool: func() *api.HCPOpenShiftClusterNodePool {
				np := createValidNodePool()
				np.Properties.ProvisioningState = arm.ProvisioningStateSucceeded
				np.Properties.Platform.VMSize = "Standard_D2s_v3"
				np.Properties.AutoRepair = true
				np.Location = "eastus"
				return np
			}(),
			expectErrors: []expectedError{
				{message: "field is immutable", fieldPath: "properties.provisioningState"},
				{message: "field is immutable", fieldPath: "properties.platform"},
				{message: "field is immutable", fieldPath: "properties.autoRepair"},
				{message: "field is immutable", fieldPath: "trackedResource.location"},
			},
		},
		{
			name: "enable autoscaling - update",
			newNodePool: func() *api.HCPOpenShiftClusterNodePool {
				np := createValidNodePool()
				np.Properties.Replicas = 0
				np.Properties.AutoScaling = &api.NodePoolAutoScaling{
					Min: 1,
					Max: 5,
				}
				return np
			}(),
			oldNodePool: func() *api.HCPOpenShiftClusterNodePool {
				np := createValidNodePool()
				np.Properties.Replicas = 3
				np.Properties.AutoScaling = nil
				return np
			}(),
			expectErrors: []expectedError{},
		},
		{
			name: "disable autoscaling - update",
			newNodePool: func() *api.HCPOpenShiftClusterNodePool {
				np := createValidNodePool()
				np.Properties.Replicas = 3
				np.Properties.AutoScaling = nil
				return np
			}(),
			oldNodePool: func() *api.HCPOpenShiftClusterNodePool {
				np := createValidNodePool()
				np.Properties.Replicas = 0
				np.Properties.AutoScaling = &api.NodePoolAutoScaling{
					Min: 1,
					Max: 5,
				}
				return np
			}(),
			expectErrors: []expectedError{},
		},
		{
			name: "clear labels - update",
			newNodePool: func() *api.HCPOpenShiftClusterNodePool {
				np := createValidNodePool()
				np.Properties.Labels = nil
				return np
			}(),
			oldNodePool: func() *api.HCPOpenShiftClusterNodePool {
				np := createValidNodePool()
				np.Properties.Labels = map[string]string{
					"environment": "test",
					"team":        "platform",
				}
				return np
			}(),
			expectErrors: []expectedError{},
		},
		{
			name: "clear taints - update",
			newNodePool: func() *api.HCPOpenShiftClusterNodePool {
				np := createValidNodePool()
				np.Properties.Taints = nil
				return np
			}(),
			oldNodePool: func() *api.HCPOpenShiftClusterNodePool {
				np := createValidNodePool()
				np.Properties.Taints = []api.Taint{
					{
						Key:    "dedicated",
						Value:  "gpu",
						Effect: api.EffectNoSchedule,
					},
				}
				return np
			}(),
			expectErrors: []expectedError{},
		},
		{
			name: "replicas exceeds 200 with availability zone set - valid - update",
			newNodePool: func() *api.HCPOpenShiftClusterNodePool {
				np := createValidNodePool()
				np.Properties.Platform.AvailabilityZone = "1"
				np.Properties.Replicas = 250
				return np
			}(),
			oldNodePool: func() *api.HCPOpenShiftClusterNodePool {
				np := createValidNodePool()
				np.Properties.Platform.AvailabilityZone = "1"
				np.Properties.Replicas = 3
				return np
			}(),
			expectErrors: []expectedError{},
		},
		{
			name: "autoscaling min exceeds 200 with availability zone set - valid - update",
			newNodePool: func() *api.HCPOpenShiftClusterNodePool {
				np := createValidNodePool()
				np.Properties.Platform.AvailabilityZone = "2"
				np.Properties.Replicas = 0
				np.Properties.AutoScaling = &api.NodePoolAutoScaling{
					Min: 250,
					Max: 300,
				}
				return np
			}(),
			oldNodePool: func() *api.HCPOpenShiftClusterNodePool {
				np := createValidNodePool()
				np.Properties.Platform.AvailabilityZone = "2"
				return np
			}(),
			expectErrors: []expectedError{},
		},
		{
			name: "autoscaling both min and max exceed 200 with availability zone set - valid - update",
			newNodePool: func() *api.HCPOpenShiftClusterNodePool {
				np := createValidNodePool()
				np.Properties.Platform.AvailabilityZone = "1"
				np.Properties.Replicas = 0
				np.Properties.AutoScaling = &api.NodePoolAutoScaling{
					Min: 300,
					Max: 1000,
				}
				return np
			}(),
			oldNodePool: func() *api.HCPOpenShiftClusterNodePool {
				np := createValidNodePool()
				np.Properties.Platform.AvailabilityZone = "1"
				return np
			}(),
			expectErrors: []expectedError{},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			errs := ValidateNodePoolUpdate(ctx, tt.newNodePool, tt.oldNodePool)

			if len(tt.expectErrors) == 0 {
				if len(errs) != 0 {
					t.Errorf("expected no errors, got %d: %v", len(errs), errs)
				}
				return
			}

			// Check that each expected error message and field path is found
			for _, expectedErr := range tt.expectErrors {
				found := false
				for _, err := range errs {
					messageMatch := strings.Contains(err.Detail, expectedErr.message) || strings.Contains(err.Error(), expectedErr.message)
					fieldMatch := strings.Contains(err.Field, expectedErr.fieldPath)
					if messageMatch && fieldMatch {
						found = true
						break
					}
				}
				if !found {
					t.Errorf("expected error containing message '%s' at field '%s' but not found in: %v", expectedErr.message, expectedErr.fieldPath, errs)
				}
			}
		})
	}
}

// Helper function to create a valid nodepool for testing
func createValidNodePool() *api.HCPOpenShiftClusterNodePool {
	nodePool := api.NewDefaultHCPOpenShiftClusterNodePool(
		api.Must(azcorearm.ParseResourceID("/subscriptions/12345678-1234-1234-1234-123456789012/resourceGroups/test-rg/providers/Microsoft.RedHatOpenShift/hcpOpenShiftClusters/test-cluster/nodePools/test-nodepool")),
		api.TestLocation,
	)

	// Set required fields that are not in the default
	nodePool.Location = "eastus" // Required for TrackedResource validation
	nodePool.Properties.Version.ID = "4.15"
	nodePool.Properties.Version.ChannelGroup = "stable"
	nodePool.Properties.Platform.SubnetID = "/subscriptions/12345678-1234-1234-1234-123456789012/resourceGroups/test-rg/providers/Microsoft.Network/virtualNetworks/test-vnet/subnets/test-subnet"
	nodePool.Properties.Platform.VMSize = "Standard_D2s_v3"
	nodePool.Properties.Replicas = 3

	return nodePool
}
