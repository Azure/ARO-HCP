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

// TestEnsureDefaultsConsistencyNodePool verifies that the defaults applied by
// EnsureDefaults match the corresponding defaults in
// NewDefaultHCPOpenShiftClusterNodePool and the versioned constructors.
// This catches drift between the defaulting layers described in
// docs/api-version-defaults-and-storage.md.
func TestEnsureDefaultsConsistencyNodePool(t *testing.T) {
	// 1. Internal API constructor defaults
	resourceID := api.Must(azcorearm.ParseResourceID(
		"/subscriptions/00000000-0000-0000-0000-000000000000/resourceGroups/rg/providers/Microsoft.RedHatOpenShift/hcpOpenShiftClusters/cluster/nodePools/np",
	))
	internalDefault := api.NewDefaultHCPOpenShiftClusterNodePool(resourceID, "eastus")

	// 3. EnsureDefaults
	ensuredDefault := &api.HCPOpenShiftClusterNodePool{}
	ensuredDefault.EnsureDefaults()

	// Verify DiskStorageAccountType against internal constructor
	if ensuredDefault.Properties.Platform.OSDisk.DiskStorageAccountType != internalDefault.Properties.Platform.OSDisk.DiskStorageAccountType {
		t.Errorf("ensured default DiskStorageAccountType = %q, internal constructor = %q",
			ensuredDefault.Properties.Platform.OSDisk.DiskStorageAccountType,
			internalDefault.Properties.Platform.OSDisk.DiskStorageAccountType)
	}

	// Verify against each versioned API's SetDefaultValues
	t.Run("v20240610preview", func(t *testing.T) {
		externalDefault := &v20240610preview.NodePool{}
		v20240610preview.SetDefaultValuesNodePool(externalDefault)

		if string(ensuredDefault.Properties.Platform.OSDisk.DiskStorageAccountType) != string(ptr.Deref(externalDefault.Properties.Platform.OSDisk.DiskStorageAccountType, "")) {
			t.Errorf("ensured default DiskStorageAccountType = %q, versioned default = %q",
				ensuredDefault.Properties.Platform.OSDisk.DiskStorageAccountType,
				ptr.Deref(externalDefault.Properties.Platform.OSDisk.DiskStorageAccountType, ""))
		}
	})
	t.Run("v20251223preview", func(t *testing.T) {
		externalDefault := &v20251223preview.NodePool{}
		v20251223preview.SetDefaultValuesNodePool(externalDefault)

		if string(ensuredDefault.Properties.Platform.OSDisk.DiskStorageAccountType) != string(ptr.Deref(externalDefault.Properties.Platform.OSDisk.DiskStorageAccountType, "")) {
			t.Errorf("ensured default DiskStorageAccountType = %q, versioned default = %q",
				ensuredDefault.Properties.Platform.OSDisk.DiskStorageAccountType,
				ptr.Deref(externalDefault.Properties.Platform.OSDisk.DiskStorageAccountType, ""))
		}
	})
}

// TestEnsureDefaultsConsistencyCluster verifies that the defaults applied by
// EnsureDefaults match the corresponding defaults in
// NewDefaultHCPOpenShiftCluster and the versioned constructors.
func TestEnsureDefaultsConsistencyCluster(t *testing.T) {
	// 1. Internal API constructor defaults
	resourceID := api.Must(azcorearm.ParseResourceID(
		"/subscriptions/00000000-0000-0000-0000-000000000000/resourceGroups/rg/providers/Microsoft.RedHatOpenShift/hcpOpenShiftClusters/cluster",
	))
	internalDefault := api.NewDefaultHCPOpenShiftCluster(resourceID, "eastus")

	// 3. EnsureDefaults
	ensuredDefault := &api.HCPOpenShiftCluster{}
	ensuredDefault.EnsureDefaults()

	// Each canonically-defaulted field must match the internal constructor default.
	internalChecks := []struct {
		name         string
		canonicalVal string
		internalVal  string
	}{
		{
			name:         "NetworkType",
			canonicalVal: string(ensuredDefault.CustomerProperties.Network.NetworkType),
			internalVal:  string(internalDefault.CustomerProperties.Network.NetworkType),
		},
		{
			name:         "Visibility",
			canonicalVal: string(ensuredDefault.CustomerProperties.API.Visibility),
			internalVal:  string(internalDefault.CustomerProperties.API.Visibility),
		},
		{
			name:         "OutboundType",
			canonicalVal: string(ensuredDefault.CustomerProperties.Platform.OutboundType),
			internalVal:  string(internalDefault.CustomerProperties.Platform.OutboundType),
		},
		{
			name:         "ClusterImageRegistry.State",
			canonicalVal: string(ensuredDefault.CustomerProperties.ClusterImageRegistry.State),
			internalVal:  string(internalDefault.CustomerProperties.ClusterImageRegistry.State),
		},
		{
			name:         "Etcd.DataEncryption.KeyManagementMode",
			canonicalVal: string(ensuredDefault.CustomerProperties.Etcd.DataEncryption.KeyManagementMode),
			internalVal:  string(internalDefault.CustomerProperties.Etcd.DataEncryption.KeyManagementMode),
		},
	}

	for _, c := range internalChecks {
		t.Run(c.name, func(t *testing.T) {
			if c.canonicalVal != c.internalVal {
				t.Errorf("ensured default = %q, internal constructor = %q", c.canonicalVal, c.internalVal)
			}
		})
	}

	// Verify against each versioned API's SetDefaultValues
	t.Run("v20240610preview", func(t *testing.T) {
		externalDefault := &v20240610preview.HcpOpenShiftCluster{}
		v20240610preview.SetDefaultValuesCluster(externalDefault)

		checks := []struct {
			name           string
			canonicalVal   string
			externalPtrVal *string
		}{
			{"NetworkType", string(ensuredDefault.CustomerProperties.Network.NetworkType), stringPtrFromGenerated(externalDefault.Properties.Network.NetworkType)},
			{"Visibility", string(ensuredDefault.CustomerProperties.API.Visibility), stringPtrFromGenerated(externalDefault.Properties.API.Visibility)},
			{"OutboundType", string(ensuredDefault.CustomerProperties.Platform.OutboundType), stringPtrFromGenerated(externalDefault.Properties.Platform.OutboundType)},
			{"ClusterImageRegistry.State", string(ensuredDefault.CustomerProperties.ClusterImageRegistry.State), stringPtrFromGenerated(externalDefault.Properties.ClusterImageRegistry.State)},
			{"Etcd.DataEncryption.KeyManagementMode", string(ensuredDefault.CustomerProperties.Etcd.DataEncryption.KeyManagementMode), stringPtrFromGenerated(externalDefault.Properties.Etcd.DataEncryption.KeyManagementMode)},
		}
		for _, c := range checks {
			t.Run(c.name, func(t *testing.T) {
				if c.externalPtrVal == nil {
					t.Errorf("versioned default is nil, expected %q", c.canonicalVal)
				} else if c.canonicalVal != *c.externalPtrVal {
					t.Errorf("ensured default = %q, versioned default = %q", c.canonicalVal, *c.externalPtrVal)
				}
			})
		}
	})
	t.Run("v20251223preview", func(t *testing.T) {
		externalDefault := &v20251223preview.HcpOpenShiftCluster{}
		v20251223preview.SetDefaultValuesCluster(externalDefault)

		checks := []struct {
			name           string
			canonicalVal   string
			externalPtrVal *string
		}{
			{"NetworkType", string(ensuredDefault.CustomerProperties.Network.NetworkType), stringPtrFromGenerated(externalDefault.Properties.Network.NetworkType)},
			{"Visibility", string(ensuredDefault.CustomerProperties.API.Visibility), stringPtrFromGenerated(externalDefault.Properties.API.Visibility)},
			{"OutboundType", string(ensuredDefault.CustomerProperties.Platform.OutboundType), stringPtrFromGenerated(externalDefault.Properties.Platform.OutboundType)},
			{"ClusterImageRegistry.State", string(ensuredDefault.CustomerProperties.ClusterImageRegistry.State), stringPtrFromGenerated(externalDefault.Properties.ClusterImageRegistry.State)},
			{"Etcd.DataEncryption.KeyManagementMode", string(ensuredDefault.CustomerProperties.Etcd.DataEncryption.KeyManagementMode), stringPtrFromGenerated(externalDefault.Properties.Etcd.DataEncryption.KeyManagementMode)},
		}
		for _, c := range checks {
			t.Run(c.name, func(t *testing.T) {
				if c.externalPtrVal == nil {
					t.Errorf("versioned default is nil, expected %q", c.canonicalVal)
				} else if c.canonicalVal != *c.externalPtrVal {
					t.Errorf("ensured default = %q, versioned default = %q", c.canonicalVal, *c.externalPtrVal)
				}
			})
		}
	})
}

// TestCSToRPDefaultsConsistencyCluster verifies that when Cluster Service
// returns the default values for canonically-defaulted fields, the CS→RP
// conversion produces the same values as canonical defaults.
// See docs/api-version-defaults-and-storage.md.
func TestCSToRPDefaultsConsistencyCluster(t *testing.T) {
	resourceID := api.Must(azcorearm.ParseResourceID(
		"/subscriptions/00000000-0000-0000-0000-000000000000/resourceGroups/rg/providers/Microsoft.RedHatOpenShift/hcpOpenShiftClusters/cluster",
	))

	// Build a CS cluster with the default values for each canonically-defaulted field.
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

	rpCluster, err := ocm.LegacyCreateInternalClusterFromClusterService(resourceID, "eastus", csCluster)
	if err != nil {
		t.Fatalf("LegacyCreateInternalClusterFromClusterService failed: %v", err)
	}

	ensuredDefault := &api.HCPOpenShiftCluster{}
	ensuredDefault.EnsureDefaults()

	checks := []struct {
		name         string
		csToRPVal    string
		canonicalVal string
	}{
		{"NetworkType", string(rpCluster.CustomerProperties.Network.NetworkType), string(ensuredDefault.CustomerProperties.Network.NetworkType)},
		{"Visibility", string(rpCluster.CustomerProperties.API.Visibility), string(ensuredDefault.CustomerProperties.API.Visibility)},
		{"OutboundType", string(rpCluster.CustomerProperties.Platform.OutboundType), string(ensuredDefault.CustomerProperties.Platform.OutboundType)},
		{"ClusterImageRegistry.State", string(rpCluster.CustomerProperties.ClusterImageRegistry.State), string(ensuredDefault.CustomerProperties.ClusterImageRegistry.State)},
		{"Etcd.DataEncryption.KeyManagementMode", string(rpCluster.CustomerProperties.Etcd.DataEncryption.KeyManagementMode), string(ensuredDefault.CustomerProperties.Etcd.DataEncryption.KeyManagementMode)},
		{"Version.ID", rpCluster.CustomerProperties.Version.ID, ensuredDefault.CustomerProperties.Version.ID},
	}
	for _, c := range checks {
		t.Run(c.name, func(t *testing.T) {
			if c.csToRPVal != c.canonicalVal {
				t.Errorf("CS→RP default = %q, canonical default = %q", c.csToRPVal, c.canonicalVal)
			}
		})
	}
}

// TestCSToRPDefaultsConsistencyNodePool verifies that when Cluster Service
// returns the default value for DiskStorageAccountType, the CS→RP conversion
// produces the same value as the canonical default.
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

	ensuredDefault := &api.HCPOpenShiftClusterNodePool{}
	ensuredDefault.EnsureDefaults()

	if string(rpNodePool.Properties.Platform.OSDisk.DiskStorageAccountType) != string(ensuredDefault.Properties.Platform.OSDisk.DiskStorageAccountType) {
		t.Errorf("CS→RP default DiskStorageAccountType = %q, ensured default = %q",
			rpNodePool.Properties.Platform.OSDisk.DiskStorageAccountType,
			ensuredDefault.Properties.Platform.OSDisk.DiskStorageAccountType)
	}
}

func TestCSToRPDefaultsEmptyDiskStorageAccountType(t *testing.T) {
	resourceID := api.Must(azcorearm.ParseResourceID(
		"/subscriptions/00000000-0000-0000-0000-000000000000/resourceGroups/rg/providers/Microsoft.RedHatOpenShift/hcpOpenShiftClusters/cluster/nodePools/np",
	))

	// Simulate a pre-existing CS node pool that has no DiskStorageAccountType set.
	csNodePool, err := arohcpv1alpha1.NewNodePool().
		AzureNodePool(arohcpv1alpha1.NewAzureNodePool().
			OsDisk(arohcpv1alpha1.NewAzureNodePoolOsDisk().
				StorageAccountType(""))).
		Build()
	if err != nil {
		t.Fatalf("failed to build CS nodepool: %v", err)
	}

	rpNodePool, err := ocm.ConvertCStoNodePool(resourceID, "eastus", csNodePool)
	if err != nil {
		t.Fatalf("ConvertCStoNodePool failed: %v", err)
	}

	if rpNodePool.Properties.Platform.OSDisk.DiskStorageAccountType != api.DiskStorageAccountTypePremium_LRS {
		t.Errorf("CS→RP conversion must default empty StorageAccountType to Premium_LRS, got %q",
			rpNodePool.Properties.Platform.OSDisk.DiskStorageAccountType)
	}
}

// TestPreExistingDataCluster verifies that CosmosToInternalCluster applies
// canonical defaults when reading a Cosmos document that predates the
// introduction of canonically-defaulted fields.
func TestPreExistingDataCluster(t *testing.T) {
	resourceID := api.Must(azcorearm.ParseResourceID(
		"/subscriptions/00000000-0000-0000-0000-000000000000/resourceGroups/rg/providers/Microsoft.RedHatOpenShift/hcpOpenShiftClusters/cluster",
	))

	// Simulate a pre-existing Cosmos document: all canonically-defaulted fields
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
					// All canonically-defaulted fields are intentionally zero-valued:
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

	// Verify every canonically-defaulted field was filled in.
	checks := []struct {
		name string
		got  string
		want string
	}{
		{"NetworkType", string(internalCluster.CustomerProperties.Network.NetworkType), string(api.NetworkTypeOVNKubernetes)},
		{"Visibility", string(internalCluster.CustomerProperties.API.Visibility), string(api.VisibilityPublic)},
		{"OutboundType", string(internalCluster.CustomerProperties.Platform.OutboundType), string(api.OutboundTypeLoadBalancer)},
		{"ClusterImageRegistry.State", string(internalCluster.CustomerProperties.ClusterImageRegistry.State), string(api.ClusterImageRegistryStateEnabled)},
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
// canonical defaults when reading a Cosmos document that predates the
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

// TestCanonicalDefaultsConsistencyCluster verifies that the internal constructor
// defaults match the canonical api.Default* constants. This provides compile-time
// linkage between the constants and the actual defaulting behavior.
func TestCanonicalDefaultsConsistencyCluster(t *testing.T) {
	resourceID := api.Must(azcorearm.ParseResourceID(
		"/subscriptions/00000000-0000-0000-0000-000000000000/resourceGroups/rg/providers/Microsoft.RedHatOpenShift/hcpOpenShiftClusters/cluster",
	))
	internalDefault := api.NewDefaultHCPOpenShiftCluster(resourceID, "eastus")

	// Non-enum defaults (from defaults.go)
	if internalDefault.CustomerProperties.Version.ChannelGroup != api.DefaultClusterVersionChannelGroup {
		t.Errorf("ChannelGroup = %q, want %q", internalDefault.CustomerProperties.Version.ChannelGroup, api.DefaultClusterVersionChannelGroup)
	}
	if internalDefault.CustomerProperties.Network.PodCIDR != api.DefaultClusterNetworkPodCIDR {
		t.Errorf("PodCIDR = %q, want %q", internalDefault.CustomerProperties.Network.PodCIDR, api.DefaultClusterNetworkPodCIDR)
	}
	if internalDefault.CustomerProperties.Network.ServiceCIDR != api.DefaultClusterNetworkServiceCIDR {
		t.Errorf("ServiceCIDR = %q, want %q", internalDefault.CustomerProperties.Network.ServiceCIDR, api.DefaultClusterNetworkServiceCIDR)
	}
	if internalDefault.CustomerProperties.Network.MachineCIDR != api.DefaultClusterNetworkMachineCIDR {
		t.Errorf("MachineCIDR = %q, want %q", internalDefault.CustomerProperties.Network.MachineCIDR, api.DefaultClusterNetworkMachineCIDR)
	}
	if internalDefault.CustomerProperties.Network.HostPrefix != api.DefaultClusterNetworkHostPrefix {
		t.Errorf("HostPrefix = %d, want %d", internalDefault.CustomerProperties.Network.HostPrefix, api.DefaultClusterNetworkHostPrefix)
	}
	if internalDefault.CustomerProperties.Autoscaling.MaxPodGracePeriodSeconds != api.DefaultClusterMaxPodGracePeriodSeconds {
		t.Errorf("MaxPodGracePeriodSeconds = %d, want %d", internalDefault.CustomerProperties.Autoscaling.MaxPodGracePeriodSeconds, api.DefaultClusterMaxPodGracePeriodSeconds)
	}
	if internalDefault.CustomerProperties.Autoscaling.MaxNodeProvisionTimeSeconds != api.DefaultClusterMaxNodeProvisionTimeSeconds {
		t.Errorf("MaxNodeProvisionTimeSeconds = %d, want %d", internalDefault.CustomerProperties.Autoscaling.MaxNodeProvisionTimeSeconds, api.DefaultClusterMaxNodeProvisionTimeSeconds)
	}
	if internalDefault.CustomerProperties.Autoscaling.PodPriorityThreshold != api.DefaultClusterPodPriorityThreshold {
		t.Errorf("PodPriorityThreshold = %d, want %d", internalDefault.CustomerProperties.Autoscaling.PodPriorityThreshold, api.DefaultClusterPodPriorityThreshold)
	}

	// Enum defaults (from enums.go — verify compile-time linkage)
	if internalDefault.CustomerProperties.Network.NetworkType != api.NetworkTypeOVNKubernetes {
		t.Errorf("NetworkType = %q, want %q", internalDefault.CustomerProperties.Network.NetworkType, api.NetworkTypeOVNKubernetes)
	}
	if internalDefault.CustomerProperties.API.Visibility != api.VisibilityPublic {
		t.Errorf("Visibility = %q, want %q", internalDefault.CustomerProperties.API.Visibility, api.VisibilityPublic)
	}
	if internalDefault.CustomerProperties.Platform.OutboundType != api.OutboundTypeLoadBalancer {
		t.Errorf("OutboundType = %q, want %q", internalDefault.CustomerProperties.Platform.OutboundType, api.OutboundTypeLoadBalancer)
	}
	if internalDefault.CustomerProperties.Etcd.DataEncryption.KeyManagementMode != api.EtcdDataEncryptionKeyManagementModeTypePlatformManaged {
		t.Errorf("KeyManagementMode = %q, want %q", internalDefault.CustomerProperties.Etcd.DataEncryption.KeyManagementMode, api.EtcdDataEncryptionKeyManagementModeTypePlatformManaged)
	}
	if internalDefault.CustomerProperties.ClusterImageRegistry.State != api.ClusterImageRegistryStateEnabled {
		t.Errorf("ClusterImageRegistryState = %q, want %q", internalDefault.CustomerProperties.ClusterImageRegistry.State, api.ClusterImageRegistryStateEnabled)
	}
}

// TestCanonicalDefaultsConsistencyNodePool verifies that the internal constructor
// defaults match the canonical api.Default* constants for node pools.
func TestCanonicalDefaultsConsistencyNodePool(t *testing.T) {
	resourceID := api.Must(azcorearm.ParseResourceID(
		"/subscriptions/00000000-0000-0000-0000-000000000000/resourceGroups/rg/providers/Microsoft.RedHatOpenShift/hcpOpenShiftClusters/cluster/nodePools/np",
	))
	internalDefault := api.NewDefaultHCPOpenShiftClusterNodePool(resourceID, "eastus")

	if internalDefault.Properties.Version.ChannelGroup != api.DefaultNodePoolVersionChannelGroup {
		t.Errorf("ChannelGroup = %q, want %q", internalDefault.Properties.Version.ChannelGroup, api.DefaultNodePoolVersionChannelGroup)
	}
	if ptr.Deref(internalDefault.Properties.Platform.OSDisk.SizeGiB, 0) != api.DefaultNodePoolOSDiskSizeGiB {
		t.Errorf("OSDiskSizeGiB = %d, want %d", ptr.Deref(internalDefault.Properties.Platform.OSDisk.SizeGiB, 0), api.DefaultNodePoolOSDiskSizeGiB)
	}
	if internalDefault.Properties.AutoRepair != true {
		t.Errorf("AutoRepair = %v, want %v", internalDefault.Properties.AutoRepair, true)
	}
	if internalDefault.Properties.Platform.OSDisk.DiskStorageAccountType != api.DiskStorageAccountTypePremium_LRS {
		t.Errorf("DiskStorageAccountType = %q, want %q", internalDefault.Properties.Platform.OSDisk.DiskStorageAccountType, api.DiskStorageAccountTypePremium_LRS)
	}
}

// TestEnsureDefaultsConsistencyExternalAuth verifies that the defaults applied
// by EnsureDefaults match the corresponding defaults in the
// versioned constructors. This catches drift between the defaulting layers
// described in docs/api-version-defaults-and-storage.md.
func TestEnsureDefaultsConsistencyExternalAuth(t *testing.T) {
	ensuredDefault := &api.HCPOpenShiftClusterExternalAuth{}
	ensuredDefault.EnsureDefaults()

	// Verify against each versioned API's SetDefaultValues
	t.Run("v20240610preview", func(t *testing.T) {
		externalDefault := &v20240610preview.ExternalAuth{}
		v20240610preview.SetDefaultValuesExternalAuth(externalDefault)

		if stringPtrFromGenerated(externalDefault.Properties.Claim.Mappings.Username.PrefixPolicy) == nil {
			t.Errorf("versioned default PrefixPolicy is nil, expected %q", ensuredDefault.Properties.Claim.Mappings.Username.PrefixPolicy)
		} else if string(ensuredDefault.Properties.Claim.Mappings.Username.PrefixPolicy) != *stringPtrFromGenerated(externalDefault.Properties.Claim.Mappings.Username.PrefixPolicy) {
			t.Errorf("ensured default PrefixPolicy = %q, versioned default = %q",
				ensuredDefault.Properties.Claim.Mappings.Username.PrefixPolicy,
				*stringPtrFromGenerated(externalDefault.Properties.Claim.Mappings.Username.PrefixPolicy))
		}
	})
	t.Run("v20251223preview", func(t *testing.T) {
		externalDefault := &v20251223preview.ExternalAuth{}
		v20251223preview.SetDefaultValuesExternalAuth(externalDefault)

		if stringPtrFromGenerated(externalDefault.Properties.Claim.Mappings.Username.PrefixPolicy) == nil {
			t.Errorf("versioned default PrefixPolicy is nil, expected %q", ensuredDefault.Properties.Claim.Mappings.Username.PrefixPolicy)
		} else if string(ensuredDefault.Properties.Claim.Mappings.Username.PrefixPolicy) != *stringPtrFromGenerated(externalDefault.Properties.Claim.Mappings.Username.PrefixPolicy) {
			t.Errorf("ensured default PrefixPolicy = %q, versioned default = %q",
				ensuredDefault.Properties.Claim.Mappings.Username.PrefixPolicy,
				*stringPtrFromGenerated(externalDefault.Properties.Claim.Mappings.Username.PrefixPolicy))
		}
	})
}

// TestPreExistingDataExternalAuth verifies that CosmosToInternalExternalAuth
// applies canonical defaults when reading a Cosmos document that predates the
// introduction of the PrefixPolicy field.
func TestPreExistingDataExternalAuth(t *testing.T) {
	resourceID := api.Must(azcorearm.ParseResourceID(
		"/subscriptions/00000000-0000-0000-0000-000000000000/resourceGroups/rg/providers/Microsoft.RedHatOpenShift/hcpOpenShiftClusters/cluster/externalAuths/default",
	))

	preExistingDoc := &ExternalAuth{
		TypedDocument: TypedDocument{
			BaseDocument: BaseDocument{ID: "test-doc-id"},
			ResourceID:   resourceID,
		},
		ExternalAuthProperties: ExternalAuthProperties{
			ResourceDocument: &ResourceDocument{
				ResourceID:        resourceID,
				InternalID:        api.Must(api.NewInternalID("/api/aro_hcp/v1alpha1/clusters/test-cluster/external_auth_config/external_auths/default")),
				ProvisioningState: arm.ProvisioningStateSucceeded,
			},
			InternalState: ExternalAuthInternalState{
				InternalAPI: api.HCPOpenShiftClusterExternalAuth{
					// PrefixPolicy is intentionally zero-valued to simulate
					// a pre-existing document that predates the field.
				},
			},
		},
	}

	internalExternalAuth, err := CosmosToInternalExternalAuth(preExistingDoc)
	if err != nil {
		t.Fatalf("CosmosToInternalExternalAuth failed: %v", err)
	}

	if internalExternalAuth.Properties.Claim.Mappings.Username.PrefixPolicy != api.UsernameClaimPrefixPolicyNone {
		t.Errorf("got PrefixPolicy = %q, want %q",
			internalExternalAuth.Properties.Claim.Mappings.Username.PrefixPolicy,
			api.UsernameClaimPrefixPolicyNone)
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
