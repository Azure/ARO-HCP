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

package database

import (
	"testing"

	"k8s.io/utils/ptr"

	azcorearm "github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"

	arohcpv1alpha1 "github.com/openshift-online/ocm-sdk-go/arohcp/v1alpha1"

	"github.com/Azure/ARO-HCP/internal/api"
	"github.com/Azure/ARO-HCP/internal/api/arm"
	v20240610preview "github.com/Azure/ARO-HCP/internal/api/v20240610preview"
	v20251223preview "github.com/Azure/ARO-HCP/internal/api/v20251223preview"
	"github.com/Azure/ARO-HCP/internal/ocm"
)

// TestStorageDefaultsConsistencyNodePool verifies that the defaults applied by
// applyNodePoolStorageDefaults match the corresponding defaults in
// NewDefaultHCPOpenShiftClusterNodePool and SetDefaultValuesNodePool.
// This catches drift between the three defaulting layers described in
// docs/api-version-defaults-and-storage.md.
func TestStorageDefaultsConsistencyNodePool(t *testing.T) {
	// 1. Internal API constructor defaults
	resourceID := api.Must(azcorearm.ParseResourceID(
		"/subscriptions/00000000-0000-0000-0000-000000000000/resourceGroups/rg/providers/Microsoft.RedHatOpenShift/hcpOpenShiftClusters/cluster/nodePools/np",
	))
	internalDefault := api.NewDefaultHCPOpenShiftClusterNodePool(resourceID, "eastus")

	// 3. Storage defaults
	storageDefault := &api.HCPOpenShiftClusterNodePool{}
	applyNodePoolStorageDefaults(storageDefault)

	// Verify DiskStorageAccountType against internal constructor
	if storageDefault.Properties.Platform.OSDisk.DiskStorageAccountType != internalDefault.Properties.Platform.OSDisk.DiskStorageAccountType {
		t.Errorf("storage default DiskStorageAccountType = %q, internal constructor = %q",
			storageDefault.Properties.Platform.OSDisk.DiskStorageAccountType,
			internalDefault.Properties.Platform.OSDisk.DiskStorageAccountType)
	}

	// Verify against each versioned API's SetDefaultValues
	t.Run("v20240610preview", func(t *testing.T) {
		externalDefault := &v20240610preview.NodePool{}
		v20240610preview.SetDefaultValuesNodePool(externalDefault)

		if string(storageDefault.Properties.Platform.OSDisk.DiskStorageAccountType) != string(ptr.Deref(externalDefault.Properties.Platform.OSDisk.DiskStorageAccountType, "")) {
			t.Errorf("storage default DiskStorageAccountType = %q, versioned default = %q",
				storageDefault.Properties.Platform.OSDisk.DiskStorageAccountType,
				ptr.Deref(externalDefault.Properties.Platform.OSDisk.DiskStorageAccountType, ""))
		}
	})
	t.Run("v20251223preview", func(t *testing.T) {
		externalDefault := &v20251223preview.NodePool{}
		v20251223preview.SetDefaultValuesNodePool(externalDefault)

		if string(storageDefault.Properties.Platform.OSDisk.DiskStorageAccountType) != string(ptr.Deref(externalDefault.Properties.Platform.OSDisk.DiskStorageAccountType, "")) {
			t.Errorf("storage default DiskStorageAccountType = %q, versioned default = %q",
				storageDefault.Properties.Platform.OSDisk.DiskStorageAccountType,
				ptr.Deref(externalDefault.Properties.Platform.OSDisk.DiskStorageAccountType, ""))
		}
	})
}

// TestStorageDefaultsConsistencyCluster verifies that the defaults applied by
// applyClusterStorageDefaults match the corresponding defaults in
// NewDefaultHCPOpenShiftCluster and SetDefaultValuesCluster.
func TestStorageDefaultsConsistencyCluster(t *testing.T) {
	// 1. Internal API constructor defaults
	resourceID := api.Must(azcorearm.ParseResourceID(
		"/subscriptions/00000000-0000-0000-0000-000000000000/resourceGroups/rg/providers/Microsoft.RedHatOpenShift/hcpOpenShiftClusters/cluster",
	))
	internalDefault := api.NewDefaultHCPOpenShiftCluster(resourceID, "eastus")

	// 3. Storage defaults
	storageDefault := &api.HCPOpenShiftCluster{}
	applyClusterStorageDefaults(storageDefault)

	// Each storage-defaulted field must match the internal constructor default.
	internalChecks := []struct {
		name        string
		storageVal  string
		internalVal string
	}{
		{
			name:        "NetworkType",
			storageVal:  string(storageDefault.CustomerProperties.Network.NetworkType),
			internalVal: string(internalDefault.CustomerProperties.Network.NetworkType),
		},
		{
			name:        "Visibility",
			storageVal:  string(storageDefault.CustomerProperties.API.Visibility),
			internalVal: string(internalDefault.CustomerProperties.API.Visibility),
		},
		{
			name:        "OutboundType",
			storageVal:  string(storageDefault.CustomerProperties.Platform.OutboundType),
			internalVal: string(internalDefault.CustomerProperties.Platform.OutboundType),
		},
		{
			name:        "ClusterImageRegistry.State",
			storageVal:  string(storageDefault.CustomerProperties.ClusterImageRegistry.State),
			internalVal: string(internalDefault.CustomerProperties.ClusterImageRegistry.State),
		},
		{
			name:        "Etcd.DataEncryption.KeyManagementMode",
			storageVal:  string(storageDefault.CustomerProperties.Etcd.DataEncryption.KeyManagementMode),
			internalVal: string(internalDefault.CustomerProperties.Etcd.DataEncryption.KeyManagementMode),
		},
	}

	for _, c := range internalChecks {
		t.Run(c.name, func(t *testing.T) {
			if c.storageVal != c.internalVal {
				t.Errorf("storage default = %q, internal constructor = %q", c.storageVal, c.internalVal)
			}
		})
	}

	// Verify against each versioned API's SetDefaultValues
	t.Run("v20240610preview", func(t *testing.T) {
		externalDefault := &v20240610preview.HcpOpenShiftCluster{}
		v20240610preview.SetDefaultValuesCluster(externalDefault)

		checks := []struct {
			name           string
			storageVal     string
			externalPtrVal *string
		}{
			{"NetworkType", string(storageDefault.CustomerProperties.Network.NetworkType), stringPtrFromGenerated(externalDefault.Properties.Network.NetworkType)},
			{"Visibility", string(storageDefault.CustomerProperties.API.Visibility), stringPtrFromGenerated(externalDefault.Properties.API.Visibility)},
			{"OutboundType", string(storageDefault.CustomerProperties.Platform.OutboundType), stringPtrFromGenerated(externalDefault.Properties.Platform.OutboundType)},
			{"ClusterImageRegistry.State", string(storageDefault.CustomerProperties.ClusterImageRegistry.State), stringPtrFromGenerated(externalDefault.Properties.ClusterImageRegistry.State)},
			{"Etcd.DataEncryption.KeyManagementMode", string(storageDefault.CustomerProperties.Etcd.DataEncryption.KeyManagementMode), stringPtrFromGenerated(externalDefault.Properties.Etcd.DataEncryption.KeyManagementMode)},
		}
		for _, c := range checks {
			t.Run(c.name, func(t *testing.T) {
				if c.externalPtrVal == nil {
					t.Errorf("versioned default is nil, expected %q", c.storageVal)
				} else if c.storageVal != *c.externalPtrVal {
					t.Errorf("storage default = %q, versioned default = %q", c.storageVal, *c.externalPtrVal)
				}
			})
		}
	})
	t.Run("v20251223preview", func(t *testing.T) {
		externalDefault := &v20251223preview.HcpOpenShiftCluster{}
		v20251223preview.SetDefaultValuesCluster(externalDefault)

		checks := []struct {
			name           string
			storageVal     string
			externalPtrVal *string
		}{
			{"NetworkType", string(storageDefault.CustomerProperties.Network.NetworkType), stringPtrFromGenerated(externalDefault.Properties.Network.NetworkType)},
			{"Visibility", string(storageDefault.CustomerProperties.API.Visibility), stringPtrFromGenerated(externalDefault.Properties.API.Visibility)},
			{"OutboundType", string(storageDefault.CustomerProperties.Platform.OutboundType), stringPtrFromGenerated(externalDefault.Properties.Platform.OutboundType)},
			{"ClusterImageRegistry.State", string(storageDefault.CustomerProperties.ClusterImageRegistry.State), stringPtrFromGenerated(externalDefault.Properties.ClusterImageRegistry.State)},
			{"Etcd.DataEncryption.KeyManagementMode", string(storageDefault.CustomerProperties.Etcd.DataEncryption.KeyManagementMode), stringPtrFromGenerated(externalDefault.Properties.Etcd.DataEncryption.KeyManagementMode)},
		}
		for _, c := range checks {
			t.Run(c.name, func(t *testing.T) {
				if c.externalPtrVal == nil {
					t.Errorf("versioned default is nil, expected %q", c.storageVal)
				} else if c.storageVal != *c.externalPtrVal {
					t.Errorf("storage default = %q, versioned default = %q", c.storageVal, *c.externalPtrVal)
				}
			})
		}
	})
}

// TestCSToRPDefaultsConsistencyCluster verifies that when Cluster Service
// returns the default values for storage-defaulted fields, the CS→RP
// conversion produces the same values as storage defaults. This is "layer 4"
// of the DDR's recommended consistency check (docs/api-version-defaults-and-storage.md).
func TestCSToRPDefaultsConsistencyCluster(t *testing.T) {
	resourceID := api.Must(azcorearm.ParseResourceID(
		"/subscriptions/00000000-0000-0000-0000-000000000000/resourceGroups/rg/providers/Microsoft.RedHatOpenShift/hcpOpenShiftClusters/cluster",
	))

	// Build a CS cluster with the default values for each storage-defaulted field.
	// These are the CS-side representations of the default values.
	csCluster, err := arohcpv1alpha1.NewCluster().
		API(arohcpv1alpha1.NewClusterAPI().
			Listening(arohcpv1alpha1.ListeningMethodExternal)).
		Network(arohcpv1alpha1.NewNetwork().
			Type("OVNKubernetes")).
		Azure(arohcpv1alpha1.NewAzure().
			NodesOutboundConnectivity(arohcpv1alpha1.NewAzureNodesOutboundConnectivity().
				OutboundType("load_balancer")).
			EtcdEncryption(arohcpv1alpha1.NewAzureEtcdEncryption().
				DataEncryption(arohcpv1alpha1.NewAzureEtcdDataEncryption().
					KeyManagementMode("platform_managed")))).
		ImageRegistry(arohcpv1alpha1.NewClusterImageRegistry().
			State("enabled")).
		Build()
	if err != nil {
		t.Fatalf("failed to build CS cluster: %v", err)
	}

	rpCluster, err := ocm.ConvertCStoHCPOpenShiftCluster(resourceID, "eastus", csCluster)
	if err != nil {
		t.Fatalf("ConvertCStoHCPOpenShiftCluster failed: %v", err)
	}

	storageDefault := &api.HCPOpenShiftCluster{}
	applyClusterStorageDefaults(storageDefault)

	checks := []struct {
		name       string
		csToRPVal  string
		storageVal string
	}{
		{"NetworkType", string(rpCluster.CustomerProperties.Network.NetworkType), string(storageDefault.CustomerProperties.Network.NetworkType)},
		{"Visibility", string(rpCluster.CustomerProperties.API.Visibility), string(storageDefault.CustomerProperties.API.Visibility)},
		{"OutboundType", string(rpCluster.CustomerProperties.Platform.OutboundType), string(storageDefault.CustomerProperties.Platform.OutboundType)},
		{"ClusterImageRegistry.State", string(rpCluster.CustomerProperties.ClusterImageRegistry.State), string(storageDefault.CustomerProperties.ClusterImageRegistry.State)},
		{"Etcd.DataEncryption.KeyManagementMode", string(rpCluster.CustomerProperties.Etcd.DataEncryption.KeyManagementMode), string(storageDefault.CustomerProperties.Etcd.DataEncryption.KeyManagementMode)},
	}
	for _, c := range checks {
		t.Run(c.name, func(t *testing.T) {
			if c.csToRPVal != c.storageVal {
				t.Errorf("CS→RP default = %q, storage default = %q", c.csToRPVal, c.storageVal)
			}
		})
	}
}

// TestCSToRPDefaultsConsistencyNodePool verifies that when Cluster Service
// returns the default value for DiskStorageAccountType, the CS→RP conversion
// produces the same value as the storage default.
func TestCSToRPDefaultsConsistencyNodePool(t *testing.T) {
	resourceID := api.Must(azcorearm.ParseResourceID(
		"/subscriptions/00000000-0000-0000-0000-000000000000/resourceGroups/rg/providers/Microsoft.RedHatOpenShift/hcpOpenShiftClusters/cluster/nodePools/np",
	))

	csNodePool, err := arohcpv1alpha1.NewNodePool().
		AzureNodePool(arohcpv1alpha1.NewAzureNodePool().
			OsDisk(arohcpv1alpha1.NewAzureNodePoolOsDisk().
				StorageAccountType("Premium_LRS"))).
		Build()
	if err != nil {
		t.Fatalf("failed to build CS nodepool: %v", err)
	}

	rpNodePool, err := ocm.ConvertCStoNodePool(resourceID, "eastus", csNodePool)
	if err != nil {
		t.Fatalf("ConvertCStoNodePool failed: %v", err)
	}

	storageDefault := &api.HCPOpenShiftClusterNodePool{}
	applyNodePoolStorageDefaults(storageDefault)

	if string(rpNodePool.Properties.Platform.OSDisk.DiskStorageAccountType) != string(storageDefault.Properties.Platform.OSDisk.DiskStorageAccountType) {
		t.Errorf("CS→RP default DiskStorageAccountType = %q, storage default = %q",
			rpNodePool.Properties.Platform.OSDisk.DiskStorageAccountType,
			storageDefault.Properties.Platform.OSDisk.DiskStorageAccountType)
	}
}

// TestPreExistingDataCluster verifies that CosmosToInternalCluster applies
// storage defaults when reading a Cosmos document that predates the
// introduction of storage-defaulted fields.
func TestPreExistingDataCluster(t *testing.T) {
	resourceID := api.Must(azcorearm.ParseResourceID(
		"/subscriptions/00000000-0000-0000-0000-000000000000/resourceGroups/rg/providers/Microsoft.RedHatOpenShift/hcpOpenShiftClusters/cluster",
	))

	// Simulate a pre-existing Cosmos document: all storage-defaulted fields
	// are zero-valued (empty strings), as if the document was created before
	// these fields were added to the API.
	preExistingDoc := &HCPCluster{
		TypedDocument: TypedDocument{
			BaseDocument: BaseDocument{ID: "test-doc-id"},
			ResourceID:   resourceID,
		},
		HCPClusterProperties: HCPClusterProperties{
			ResourceDocument: &ResourceDocument{
				ResourceID:        resourceID,
				InternalID:        api.Must(api.NewInternalID("/api/aro_hcp/v1alpha1/clusters/test-cluster")),
				ProvisioningState: arm.ProvisioningStateSucceeded,
			},
			InternalState: ClusterInternalState{
				InternalAPI: api.HCPOpenShiftCluster{
					// All storage-defaulted fields are intentionally zero-valued:
					// NetworkType, Visibility, OutboundType,
					// ClusterImageRegistry.State, Etcd.DataEncryption.KeyManagementMode
				},
			},
		},
	}

	internalCluster, err := CosmosToInternalCluster(preExistingDoc)
	if err != nil {
		t.Fatalf("CosmosToInternalCluster failed: %v", err)
	}

	// Verify every storage-defaulted field was filled in.
	checks := []struct {
		name string
		got  string
		want string
	}{
		{"NetworkType", string(internalCluster.CustomerProperties.Network.NetworkType), string(api.NetworkTypeOVNKubernetes)},
		{"Visibility", string(internalCluster.CustomerProperties.API.Visibility), string(api.VisibilityPublic)},
		{"OutboundType", string(internalCluster.CustomerProperties.Platform.OutboundType), string(api.OutboundTypeLoadBalancer)},
		{"ClusterImageRegistry.State", string(internalCluster.CustomerProperties.ClusterImageRegistry.State), string(api.ClusterImageRegistryProfileStateEnabled)},
		{"Etcd.DataEncryption.KeyManagementMode", string(internalCluster.CustomerProperties.Etcd.DataEncryption.KeyManagementMode), string(api.EtcdDataEncryptionKeyManagementModeTypePlatformManaged)},
	}
	for _, c := range checks {
		t.Run(c.name, func(t *testing.T) {
			if c.got != c.want {
				t.Errorf("got %q, want %q", c.got, c.want)
			}
		})
	}
}

// TestPreExistingDataNodePool verifies that CosmosToInternalNodePool applies
// storage defaults when reading a Cosmos document that predates the
// introduction of DiskStorageAccountType.
func TestPreExistingDataNodePool(t *testing.T) {
	resourceID := api.Must(azcorearm.ParseResourceID(
		"/subscriptions/00000000-0000-0000-0000-000000000000/resourceGroups/rg/providers/Microsoft.RedHatOpenShift/hcpOpenShiftClusters/cluster/nodePools/np",
	))

	// Simulate a pre-existing Cosmos document missing DiskStorageAccountType.
	preExistingDoc := &NodePool{
		TypedDocument: TypedDocument{
			BaseDocument: BaseDocument{ID: "test-doc-id"},
			ResourceID:   resourceID,
		},
		NodePoolProperties: NodePoolProperties{
			ResourceDocument: &ResourceDocument{
				ResourceID:        resourceID,
				InternalID:        api.Must(api.NewInternalID("/api/aro_hcp/v1alpha1/clusters/test-cluster/node_pools/test-np")),
				ProvisioningState: arm.ProvisioningStateSucceeded,
			},
			InternalState: NodePoolInternalState{
				InternalAPI: api.HCPOpenShiftClusterNodePool{
					// DiskStorageAccountType is intentionally zero-valued
					Properties: api.HCPOpenShiftClusterNodePoolProperties{
						Platform: api.NodePoolPlatformProfile{
							OSDisk: api.OSDiskProfile{
								// DiskStorageAccountType: "" — simulates pre-existing document
							},
						},
					},
				},
			},
		},
	}

	internalNodePool, err := CosmosToInternalNodePool(preExistingDoc)
	if err != nil {
		t.Fatalf("CosmosToInternalNodePool failed: %v", err)
	}

	if internalNodePool.Properties.Platform.OSDisk.DiskStorageAccountType != api.DiskStorageAccountTypePremium_LRS {
		t.Errorf("got DiskStorageAccountType = %q, want %q",
			internalNodePool.Properties.Platform.OSDisk.DiskStorageAccountType,
			api.DiskStorageAccountTypePremium_LRS)
	}
}

// stringPtrFromGenerated converts any ~string typed pointer to a *string.
func stringPtrFromGenerated[T ~string](p *T) *string {
	if p == nil {
		return nil
	}
	s := string(*p)
	return &s
}
