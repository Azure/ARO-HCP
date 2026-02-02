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

package controllers

import (
	"testing"

	azcorearm "github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"

	"github.com/Azure/ARO-HCP/internal/api"
	"github.com/Azure/ARO-HCP/internal/api/arm"
)

func TestGenerateDenyAssignmentID(t *testing.T) {
	tests := []struct {
		name      string
		clusterID string
		suffix    string
	}{
		{
			name:      "resources deny assignment",
			clusterID: "/subscriptions/00000000-0000-0000-0000-000000000001/resourceGroups/test-rg/providers/Microsoft.RedHatOpenShift/hcpOpenShiftClusters/test-cluster",
			suffix:    "resources-deny-assignment",
		},
		{
			name:      "compute deny assignment",
			clusterID: "/subscriptions/00000000-0000-0000-0000-000000000001/resourceGroups/test-rg/providers/Microsoft.RedHatOpenShift/hcpOpenShiftClusters/test-cluster",
			suffix:    "compute-deny-assignment",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			id1 := generateDenyAssignmentID(tt.clusterID, tt.suffix)
			id2 := generateDenyAssignmentID(tt.clusterID, tt.suffix)

			// IDs should be deterministic
			if id1 != id2 {
				t.Errorf("generateDenyAssignmentID() not deterministic: got %s and %s", id1, id2)
			}

			// ID should be a valid UUID
			if len(id1) != 36 {
				t.Errorf("generateDenyAssignmentID() returned invalid UUID length: got %d, want 36", len(id1))
			}
		})
	}
}

func TestGenerateDenyAssignmentID_DifferentInputs(t *testing.T) {
	clusterID := "/subscriptions/00000000-0000-0000-0000-000000000001/resourceGroups/test-rg/providers/Microsoft.RedHatOpenShift/hcpOpenShiftClusters/test-cluster"

	id1 := generateDenyAssignmentID(clusterID, "suffix1")
	id2 := generateDenyAssignmentID(clusterID, "suffix2")

	// Different suffixes should produce different IDs
	if id1 == id2 {
		t.Errorf("generateDenyAssignmentID() should produce different IDs for different suffixes")
	}
}

func TestIsKMSEncryptionEnabled(t *testing.T) {
	tests := []struct {
		name     string
		cluster  *api.HCPOpenShiftCluster
		expected bool
	}{
		{
			name: "KMS encryption enabled",
			cluster: &api.HCPOpenShiftCluster{
				CustomerProperties: api.HCPOpenShiftClusterCustomerProperties{
					Etcd: api.EtcdProfile{
						DataEncryption: api.EtcdDataEncryptionProfile{
							CustomerManaged: &api.CustomerManagedEncryptionProfile{
								EncryptionType: api.CustomerManagedEncryptionTypeKMS,
							},
						},
					},
				},
			},
			expected: true,
		},
		{
			name: "CustomerManaged is nil",
			cluster: &api.HCPOpenShiftCluster{
				CustomerProperties: api.HCPOpenShiftClusterCustomerProperties{
					Etcd: api.EtcdProfile{
						DataEncryption: api.EtcdDataEncryptionProfile{
							CustomerManaged: nil,
						},
					},
				},
			},
			expected: false,
		},
		{
			name: "EncryptionType is empty",
			cluster: &api.HCPOpenShiftCluster{
				CustomerProperties: api.HCPOpenShiftClusterCustomerProperties{
					Etcd: api.EtcdProfile{
						DataEncryption: api.EtcdDataEncryptionProfile{
							CustomerManaged: &api.CustomerManagedEncryptionProfile{
								EncryptionType: "",
							},
						},
					},
				},
			},
			expected: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := isKMSEncryptionEnabled(tt.cluster)
			if result != tt.expected {
				t.Errorf("isKMSEncryptionEnabled() = %v, want %v", result, tt.expected)
			}
		})
	}
}

func TestCollectPrincipalIDs(t *testing.T) {
	c := &denyAssignmentReconciler{}
	cache := map[string]string{
		"cp-cluster-api-azure": "cp-principal-1",
		"cp-control-plane":     "cp-principal-2",
		"dp-image-registry":    "dp-principal-1",
		"dp-disk-csi-driver":   "dp-principal-2",
		"service":              "service-principal",
	}

	tests := []struct {
		name            string
		controlPlaneOps []string
		dataPlaneOps    []string
		addServiceMI    bool
		wantCount       int
		wantErr         bool
	}{
		{
			name:            "control plane operators only",
			controlPlaneOps: []string{"cluster-api-azure", "control-plane"},
			dataPlaneOps:    []string{},
			addServiceMI:    false,
			wantCount:       2,
			wantErr:         false,
		},
		{
			name:            "control plane and data plane operators",
			controlPlaneOps: []string{"cluster-api-azure"},
			dataPlaneOps:    []string{"image-registry", "disk-csi-driver"},
			addServiceMI:    false,
			wantCount:       3,
			wantErr:         false,
		},
		{
			name:            "with service managed identity",
			controlPlaneOps: []string{"cluster-api-azure"},
			dataPlaneOps:    []string{"image-registry"},
			addServiceMI:    true,
			wantCount:       3,
			wantErr:         false,
		},
		{
			name:            "missing control plane operator",
			controlPlaneOps: []string{"nonexistent-operator"},
			dataPlaneOps:    []string{},
			addServiceMI:    false,
			wantCount:       0,
			wantErr:         true,
		},
		{
			name:            "missing data plane operator",
			controlPlaneOps: []string{},
			dataPlaneOps:    []string{"nonexistent-operator"},
			addServiceMI:    false,
			wantCount:       0,
			wantErr:         true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, err := c.collectPrincipalIDs(tt.controlPlaneOps, tt.dataPlaneOps, tt.addServiceMI, cache)

			if tt.wantErr {
				if err == nil {
					t.Errorf("collectPrincipalIDs() expected error, got nil")
				}
				return
			}

			if err != nil {
				t.Errorf("collectPrincipalIDs() unexpected error: %v", err)
				return
			}

			if len(result) != tt.wantCount {
				t.Errorf("collectPrincipalIDs() returned %d principals, want %d", len(result), tt.wantCount)
			}
		})
	}
}

func TestCreateDenyAssignmentResource(t *testing.T) {
	scope := "/subscriptions/00000000-0000-0000-0000-000000000001/resourceGroups/test-rg"
	denyAssignmentID := "test-deny-assignment-id"
	excludedPrincipalIDs := []string{"principal-1", "principal-2"}
	actions := []string{"Microsoft.Compute/*/delete", "Microsoft.Compute/*/write"}
	notActions := []string{"Microsoft.Compute/disks/write"}
	dataActions := []string{"Microsoft.Storage/storageAccounts/blobServices/containers/blobs/read"}

	resource := createDenyAssignmentResource(scope, denyAssignmentID, excludedPrincipalIDs, actions, notActions, dataActions)

	// Verify location is global
	if *resource.Location != "global" {
		t.Errorf("createDenyAssignmentResource() Location = %s, want 'global'", *resource.Location)
	}

	// Verify properties exist
	props, ok := resource.Properties.(map[string]any)
	if !ok {
		t.Fatal("createDenyAssignmentResource() Properties is not a map")
	}

	// Verify DenyAssignmentName
	if props["DenyAssignmentName"] != denyAssignmentID {
		t.Errorf("createDenyAssignmentResource() DenyAssignmentName = %s, want %s", props["DenyAssignmentName"], denyAssignmentID)
	}

	// Verify Scope
	if props["Scope"] != scope {
		t.Errorf("createDenyAssignmentResource() Scope = %s, want %s", props["Scope"], scope)
	}

	// Verify IsSystemProtected
	if props["IsSystemProtected"] != true {
		t.Errorf("createDenyAssignmentResource() IsSystemProtected = %v, want true", props["IsSystemProtected"])
	}

	// Verify Principals contains all principals GUID
	principals, ok := props["Principals"].([]any)
	if !ok || len(principals) != 1 {
		t.Fatal("createDenyAssignmentResource() Principals is invalid")
	}
	principal := principals[0].(map[string]any)
	if principal["id"] != allPrincipalsGUID {
		t.Errorf("createDenyAssignmentResource() Principal id = %s, want %s", principal["id"], allPrincipalsGUID)
	}

	// Verify ExcludePrincipals
	excludedPrincipals, ok := props["ExcludePrincipals"].([]any)
	if !ok || len(excludedPrincipals) != len(excludedPrincipalIDs) {
		t.Fatalf("createDenyAssignmentResource() ExcludePrincipals has %d entries, want %d", len(excludedPrincipals), len(excludedPrincipalIDs))
	}
}

func TestIsClusterReadyForDenyAssignments(t *testing.T) {
	tests := []struct {
		name              string
		provisioningState arm.ProvisioningState
		expected          bool
	}{
		{
			name:              "Succeeded state",
			provisioningState: arm.ProvisioningStateSucceeded,
			expected:          true,
		},
		{
			name:              "Updating state",
			provisioningState: arm.ProvisioningStateUpdating,
			expected:          true,
		},
		{
			name:              "Creating state",
			provisioningState: arm.ProvisioningStateProvisioning,
			expected:          false,
		},
		{
			name:              "Deleting state",
			provisioningState: arm.ProvisioningStateDeleting,
			expected:          false,
		},
		{
			name:              "Failed state",
			provisioningState: arm.ProvisioningStateFailed,
			expected:          false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			resourceID, _ := azcorearm.ParseResourceID("/subscriptions/00000000-0000-0000-0000-000000000001/resourceGroups/test-rg/providers/Microsoft.RedHatOpenShift/hcpOpenShiftClusters/test-cluster")
			cluster := &api.HCPOpenShiftCluster{
				ServiceProviderProperties: api.HCPOpenShiftClusterServiceProviderProperties{
					ProvisioningState: tt.provisioningState,
				},
			}
			cluster.ID = resourceID

			result := isClusterReadyForDenyAssignments(cluster)
			if result != tt.expected {
				t.Errorf("isClusterReadyForDenyAssignments() = %v, want %v", result, tt.expected)
			}
		})
	}
}
