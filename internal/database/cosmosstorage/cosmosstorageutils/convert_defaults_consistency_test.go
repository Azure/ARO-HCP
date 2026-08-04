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

package cosmosstorageutils

import (
	"testing"

	"k8s.io/utils/ptr"

	azcorearm "github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"

	"github.com/Azure/ARO-HCP/internal/api/coreapi"
	"github.com/Azure/ARO-HCP/internal/api/metadataapi"
	v20240610preview "github.com/Azure/ARO-HCP/internal/azureapi/v20240610preview"
	v20251223preview "github.com/Azure/ARO-HCP/internal/azureapi/v20251223preview"
	v20260630preview "github.com/Azure/ARO-HCP/internal/azureapi/v20260630preview"
	v20260901preview "github.com/Azure/ARO-HCP/internal/azureapi/v20260901preview"
	v20261003preview "github.com/Azure/ARO-HCP/internal/azureapi/v20261003preview"
)

// TestEnsureDefaultsConsistencyNodePool verifies that the defaults applied by
// EnsureDefaults match the corresponding defaults in
// NewDefaultHCPOpenShiftClusterNodePool and the versioned constructors.
// This catches drift between the defaulting layers described in
// docs/api-version-defaults-and-storage.md.
func TestEnsureDefaultsConsistencyNodePool(t *testing.T) {
	// 1. Internal API constructor defaults
	resourceID := metadataapi.Must(azcorearm.ParseResourceID(
		"/subscriptions/00000000-0000-0000-0000-000000000000/resourceGroups/rg/providers/Microsoft.RedHatOpenShift/hcpOpenShiftClusters/cluster/nodePools/np",
	))
	internalDefault := coreapi.NewDefaultHCPOpenShiftClusterNodePool(resourceID, "eastus")

	// 3. EnsureDefaults
	ensuredDefault := &coreapi.HCPOpenShiftClusterNodePool{}
	ensuredDefault.EnsureDefaults()

	// Verify DiskStorageAccountType against internal constructor
	if ensuredDefault.Properties.Platform.OSDisk.DiskStorageAccountType != internalDefault.Properties.Platform.OSDisk.DiskStorageAccountType {
		t.Errorf("ensured default DiskStorageAccountType = %q, internal constructor = %q",
			ensuredDefault.Properties.Platform.OSDisk.DiskStorageAccountType,
			internalDefault.Properties.Platform.OSDisk.DiskStorageAccountType)
	}

	// Verify DiskType against internal constructor
	if ensuredDefault.Properties.Platform.OSDisk.DiskType != internalDefault.Properties.Platform.OSDisk.DiskType {
		t.Errorf("ensured default DiskType = %q, internal constructor = %q",
			ensuredDefault.Properties.Platform.OSDisk.DiskType,
			internalDefault.Properties.Platform.OSDisk.DiskType)
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
		// DiskType not in v2024 — skip versioned check
	})
	t.Run("v20251223preview", func(t *testing.T) {
		externalDefault := &v20251223preview.NodePool{}
		v20251223preview.SetDefaultValuesNodePool(externalDefault)

		if string(ensuredDefault.Properties.Platform.OSDisk.DiskStorageAccountType) != string(ptr.Deref(externalDefault.Properties.Platform.OSDisk.DiskStorageAccountType, "")) {
			t.Errorf("ensured default DiskStorageAccountType = %q, versioned default = %q",
				ensuredDefault.Properties.Platform.OSDisk.DiskStorageAccountType,
				ptr.Deref(externalDefault.Properties.Platform.OSDisk.DiskStorageAccountType, ""))
		}
		if string(ensuredDefault.Properties.Platform.OSDisk.DiskType) != string(ptr.Deref(externalDefault.Properties.Platform.OSDisk.DiskType, "")) {
			t.Errorf("ensured default DiskType = %q, versioned default = %q",
				ensuredDefault.Properties.Platform.OSDisk.DiskType,
				ptr.Deref(externalDefault.Properties.Platform.OSDisk.DiskType, ""))
		}
	})
	t.Run("v20260630preview", func(t *testing.T) {
		externalDefault := &v20260630preview.NodePool{}
		v20260630preview.SetDefaultValuesNodePool(externalDefault)

		if string(ensuredDefault.Properties.Platform.OSDisk.DiskStorageAccountType) != string(ptr.Deref(externalDefault.Properties.Platform.OSDisk.DiskStorageAccountType, "")) {
			t.Errorf("ensured default DiskStorageAccountType = %q, versioned default = %q",
				ensuredDefault.Properties.Platform.OSDisk.DiskStorageAccountType,
				ptr.Deref(externalDefault.Properties.Platform.OSDisk.DiskStorageAccountType, ""))
		}
		if string(ensuredDefault.Properties.Platform.OSDisk.DiskType) != string(ptr.Deref(externalDefault.Properties.Platform.OSDisk.DiskType, "")) {
			t.Errorf("ensured default DiskType = %q, versioned default = %q",
				ensuredDefault.Properties.Platform.OSDisk.DiskType,
				ptr.Deref(externalDefault.Properties.Platform.OSDisk.DiskType, ""))
		}
	})
	t.Run("v20260901preview", func(t *testing.T) {
		externalDefault := &v20260901preview.NodePool{}
		v20260901preview.SetDefaultValuesNodePool(externalDefault)

		if string(ensuredDefault.Properties.Platform.OSDisk.DiskStorageAccountType) != string(ptr.Deref(externalDefault.Properties.Platform.OSDisk.DiskStorageAccountType, "")) {
			t.Errorf("ensured default DiskStorageAccountType = %q, versioned default = %q",
				ensuredDefault.Properties.Platform.OSDisk.DiskStorageAccountType,
				ptr.Deref(externalDefault.Properties.Platform.OSDisk.DiskStorageAccountType, ""))
		}
		if string(ensuredDefault.Properties.Platform.OSDisk.DiskType) != string(ptr.Deref(externalDefault.Properties.Platform.OSDisk.DiskType, "")) {
			t.Errorf("ensured default DiskType = %q, versioned default = %q",
				ensuredDefault.Properties.Platform.OSDisk.DiskType,
				ptr.Deref(externalDefault.Properties.Platform.OSDisk.DiskType, ""))
		}
	})
	t.Run("v20261003preview", func(t *testing.T) {
		externalDefault := &v20261003preview.NodePool{}
		v20261003preview.SetDefaultValuesNodePool(externalDefault)

		if string(ensuredDefault.Properties.Platform.OSDisk.DiskStorageAccountType) != string(ptr.Deref(externalDefault.Properties.Platform.OSDisk.DiskStorageAccountType, "")) {
			t.Errorf("ensured default DiskStorageAccountType = %q, versioned default = %q",
				ensuredDefault.Properties.Platform.OSDisk.DiskStorageAccountType,
				ptr.Deref(externalDefault.Properties.Platform.OSDisk.DiskStorageAccountType, ""))
		}
		if string(ensuredDefault.Properties.Platform.OSDisk.DiskType) != string(ptr.Deref(externalDefault.Properties.Platform.OSDisk.DiskType, "")) {
			t.Errorf("ensured default DiskType = %q, versioned default = %q",
				ensuredDefault.Properties.Platform.OSDisk.DiskType,
				ptr.Deref(externalDefault.Properties.Platform.OSDisk.DiskType, ""))
		}
	})
}

// TestEnsureDefaultsConsistencyCluster verifies that the defaults applied by
// EnsureDefaults match the corresponding defaults in
// NewDefaultHCPOpenShiftCluster and the versioned constructors.
func TestEnsureDefaultsConsistencyCluster(t *testing.T) {
	// 1. Internal API constructor defaults
	resourceID := metadataapi.Must(azcorearm.ParseResourceID(
		"/subscriptions/00000000-0000-0000-0000-000000000000/resourceGroups/rg/providers/Microsoft.RedHatOpenShift/hcpOpenShiftClusters/cluster",
	))
	internalDefault := coreapi.NewDefaultHCPOpenShiftCluster(resourceID, "eastus")

	// 3. EnsureDefaults
	ensuredDefault := &coreapi.HCPOpenShiftCluster{}
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
			name:         "Ingress.Type",
			canonicalVal: string(ensuredDefault.CustomerProperties.Ingress.Type),
			internalVal:  string(internalDefault.CustomerProperties.Ingress.Type),
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
	t.Run("v20260630preview", func(t *testing.T) {
		externalDefault := &v20260630preview.HcpOpenShiftCluster{}
		v20260630preview.SetDefaultValuesCluster(externalDefault)

		checks := []struct {
			name           string
			canonicalVal   string
			externalPtrVal *string
		}{
			{"NetworkType", string(ensuredDefault.CustomerProperties.Network.NetworkType), stringPtrFromGenerated(externalDefault.Properties.Network.NetworkType)},
			{"Visibility", string(ensuredDefault.CustomerProperties.API.Visibility), stringPtrFromGenerated(externalDefault.Properties.API.Visibility)},
			{"OutboundType", string(ensuredDefault.CustomerProperties.Platform.OutboundType), stringPtrFromGenerated(externalDefault.Properties.Platform.OutboundType)},
			{"ClusterImageRegistry.State", string(ensuredDefault.CustomerProperties.ClusterImageRegistry.State), stringPtrFromGenerated(externalDefault.Properties.ClusterImageRegistry.State)},
			{"Ingress.Type", string(ensuredDefault.CustomerProperties.Ingress.Type), stringPtrFromGenerated(externalDefault.Properties.Ingress.Type)},
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
	for _, tc := range []struct {
		name    string
		cluster func() (*string, *string, *string, *string, *string)
	}{
		{"v20260901preview", func() (*string, *string, *string, *string, *string) {
			d := &v20260901preview.HcpOpenShiftCluster{}
			v20260901preview.SetDefaultValuesCluster(d)
			return stringPtrFromGenerated(d.Properties.Network.NetworkType),
				stringPtrFromGenerated(d.Properties.API.Visibility),
				stringPtrFromGenerated(d.Properties.Platform.OutboundType),
				stringPtrFromGenerated(d.Properties.ClusterImageRegistry.State),
				stringPtrFromGenerated(d.Properties.Ingress.Type)
		}},
		{"v20261003preview", func() (*string, *string, *string, *string, *string) {
			d := &v20261003preview.HcpOpenShiftCluster{}
			v20261003preview.SetDefaultValuesCluster(d)
			return stringPtrFromGenerated(d.Properties.Network.NetworkType),
				stringPtrFromGenerated(d.Properties.API.Visibility),
				stringPtrFromGenerated(d.Properties.Platform.OutboundType),
				stringPtrFromGenerated(d.Properties.ClusterImageRegistry.State),
				stringPtrFromGenerated(d.Properties.Ingress.Type)
		}},
	} {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			networkType, visibility, outboundType, imageRegState, ingressType := tc.cluster()
			checks := []struct {
				name           string
				canonicalVal   string
				externalPtrVal *string
			}{
				{"NetworkType", string(ensuredDefault.CustomerProperties.Network.NetworkType), networkType},
				{"Visibility", string(ensuredDefault.CustomerProperties.API.Visibility), visibility},
				{"OutboundType", string(ensuredDefault.CustomerProperties.Platform.OutboundType), outboundType},
				{"ClusterImageRegistry.State", string(ensuredDefault.CustomerProperties.ClusterImageRegistry.State), imageRegState},
				{"Ingress.Type", string(ensuredDefault.CustomerProperties.Ingress.Type), ingressType},
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
}

// TestPreExistingDataCluster verifies that CosmosToInternalCluster applies
// canonical defaults when reading a Cosmos document that predates the
// introduction of canonically-defaulted fields.
func TestPreExistingDataCluster(t *testing.T) {
	resourceID := metadataapi.Must(azcorearm.ParseResourceID(
		"/subscriptions/00000000-0000-0000-0000-000000000000/resourceGroups/rg/providers/Microsoft.RedHatOpenShift/hcpOpenShiftClusters/cluster",
	))

	// Simulate a pre-existing Cosmos document: all canonically-defaulted fields
	// are zero-valued (empty strings), as if the document was created before
	// these fields were added to the API.
	preExistingDoc := &GenericDocument[coreapi.HCPOpenShiftCluster]{
		TypedDocument: TypedDocument{
			BaseDocument: BaseDocument{ID: "test-doc-id"},
			ResourceID:   resourceID,
		},
		Content: coreapi.HCPOpenShiftCluster{
			// All canonically-defaulted fields are intentionally zero-valued:
			// NetworkType, Visibility, OutboundType,
			// ClusterImageRegistry.State, Etcd.DataEncryption.KeyManagementMode
			CosmosMetadata: coreapi.CosmosMetadata{
				ResourceID: resourceID,
			},
			ServiceProviderProperties: coreapi.HCPOpenShiftClusterServiceProviderProperties{
				ClusterServiceID:  ptr.To(metadataapi.Must(metadataapi.NewInternalID("/api/aro_hcp/v1alpha1/clusters/test-cluster"))),
				ProvisioningState: coreapi.ProvisioningStateSucceeded,
			},
		},
	}

	internalCluster, err := CosmosGenericToInternal(preExistingDoc)
	if err != nil {
		t.Fatalf("CosmosToInternalCluster failed: %v", err)
	}

	// Verify every canonically-defaulted field was filled in.
	checks := []struct {
		name string
		got  string
		want string
	}{
		{"NetworkType", string(internalCluster.CustomerProperties.Network.NetworkType), string(metadataapi.NetworkTypeOVNKubernetes)},
		{"Visibility", string(internalCluster.CustomerProperties.API.Visibility), string(metadataapi.VisibilityPublic)},
		{"OutboundType", string(internalCluster.CustomerProperties.Platform.OutboundType), string(metadataapi.OutboundTypeLoadBalancer)},
		{"ClusterImageRegistry.State", string(internalCluster.CustomerProperties.ClusterImageRegistry.State), string(metadataapi.ClusterImageRegistryStateEnabled)},
		{"Ingress.Type", string(internalCluster.CustomerProperties.Ingress.Type), string(metadataapi.IngressTypePublic)},
	}
	for _, c := range checks {
		t.Run(c.name, func(t *testing.T) {
			if c.got != c.want {
				t.Errorf("got %q, want %q", c.got, c.want)
			}
		})
	}
}

// TestKMSVisibilityDefaultsToPublic verifies that KMS Visibility defaults to
// Public when a cluster has KMS encryption configured but no visibility set.
// This scenario occurs when clusters are created via v2024_06_10_preview, which
// doesn't expose the visibility field and assumes public KeyVaults.
func TestKMSVisibilityDefaultsToPublic(t *testing.T) {
	resourceID := metadataapi.Must(azcorearm.ParseResourceID(
		"/subscriptions/00000000-0000-0000-0000-000000000000/resourceGroups/rg/providers/Microsoft.RedHatOpenShift/hcpOpenShiftClusters/cluster",
	))

	// Simulate a cluster created via v2024_06_10_preview with KMS encryption
	// but no visibility field (since that version doesn't have it).
	preExistingDoc := &GenericDocument[coreapi.HCPOpenShiftCluster]{
		TypedDocument: TypedDocument{
			BaseDocument: BaseDocument{ID: "test-doc-id"},
			ResourceID:   resourceID,
		},
		Content: coreapi.HCPOpenShiftCluster{
			CosmosMetadata: coreapi.CosmosMetadata{
				ResourceID: resourceID,
			},
			CustomerProperties: coreapi.HCPOpenShiftClusterCustomerProperties{
				Etcd: coreapi.EtcdProfile{
					DataEncryption: coreapi.EtcdDataEncryptionProfile{
						KeyManagementMode: metadataapi.EtcdDataEncryptionKeyManagementModeTypeCustomerManaged,
						CustomerManaged: &coreapi.CustomerManagedEncryptionProfile{
							EncryptionType: metadataapi.CustomerManagedEncryptionTypeKMS,
							Kms: &coreapi.KmsEncryptionProfile{
								ActiveKey: coreapi.KmsKey{
									Name:      "test-key",
									VaultName: "test-vault",
									Version:   "v1",
								},
								// Visibility intentionally not set (empty string)
								Visibility: "",
							},
						},
					},
				},
			},
			ServiceProviderProperties: coreapi.HCPOpenShiftClusterServiceProviderProperties{
				ClusterServiceID:  ptr.To(metadataapi.Must(metadataapi.NewInternalID("/api/aro_hcp/v1alpha1/clusters/test-cluster"))),
				ProvisioningState: coreapi.ProvisioningStateSucceeded,
			},
		},
	}

	internalCluster, err := CosmosGenericToInternal(preExistingDoc)
	if err != nil {
		t.Fatalf("CosmosToInternalCluster failed: %v", err)
	}

	// Verify KMS Visibility was defaulted to Public
	if internalCluster.CustomerProperties.Etcd.DataEncryption.CustomerManaged == nil {
		t.Fatal("CustomerManaged is nil")
	}
	if internalCluster.CustomerProperties.Etcd.DataEncryption.CustomerManaged.Kms == nil {
		t.Fatal("Kms is nil")
	}
	if internalCluster.CustomerProperties.Etcd.DataEncryption.CustomerManaged.Kms.Visibility != metadataapi.KeyVaultVisibilityPublic {
		t.Errorf("got Visibility = %q, want %q",
			internalCluster.CustomerProperties.Etcd.DataEncryption.CustomerManaged.Kms.Visibility,
			metadataapi.KeyVaultVisibilityPublic)
	}
}

// TestKeyEncryptionKeyURLNotBackfilledByEnsureDefaults verifies that EnsureDefaults
// does NOT backfill KeyEncryptionKeyURL — this is handled by the cosmosmigration
// controller instead, to avoid running the construction on every Cosmos read.
func TestKeyEncryptionKeyURLNotBackfilledByEnsureDefaults(t *testing.T) {
	resourceID := metadataapi.Must(azcorearm.ParseResourceID(
		"/subscriptions/00000000-0000-0000-0000-000000000000/resourceGroups/rg/providers/Microsoft.RedHatOpenShift/hcpOpenShiftClusters/cluster",
	))

	preExistingDoc := &GenericDocument[coreapi.HCPOpenShiftCluster]{
		TypedDocument: TypedDocument{
			BaseDocument: BaseDocument{ID: "test-doc-id"},
			ResourceID:   resourceID,
		},
		Content: coreapi.HCPOpenShiftCluster{
			CosmosMetadata: coreapi.CosmosMetadata{
				ResourceID: resourceID,
			},
			CustomerProperties: coreapi.HCPOpenShiftClusterCustomerProperties{
				Etcd: coreapi.EtcdProfile{
					DataEncryption: coreapi.EtcdDataEncryptionProfile{
						KeyManagementMode: metadataapi.EtcdDataEncryptionKeyManagementModeTypeCustomerManaged,
						CustomerManaged: &coreapi.CustomerManagedEncryptionProfile{
							EncryptionType: metadataapi.CustomerManagedEncryptionTypeKMS,
							Kms: &coreapi.KmsEncryptionProfile{
								Visibility: metadataapi.KeyVaultVisibilityPublic,
								ActiveKey: coreapi.KmsKey{
									Name:      "test-key",
									VaultName: "test-vault",
									Version:   "v1",
								},
							},
						},
					},
				},
			},
			ServiceProviderProperties: coreapi.HCPOpenShiftClusterServiceProviderProperties{
				ClusterServiceID:  ptr.To(metadataapi.Must(metadataapi.NewInternalID("/api/aro_hcp/v1alpha1/clusters/test-cluster"))),
				ProvisioningState: coreapi.ProvisioningStateSucceeded,
			},
		},
	}

	internalCluster, err := CosmosGenericToInternal(preExistingDoc)
	if err != nil {
		t.Fatalf("CosmosToInternalCluster failed: %v", err)
	}

	kms := internalCluster.CustomerProperties.Etcd.DataEncryption.CustomerManaged.Kms
	if kms == nil {
		t.Fatal("Kms is nil")
	}

	// KeyEncryptionKeyURL should NOT be backfilled by EnsureDefaults —
	// that is the responsibility of the cosmosmigration controller.
	if kms.KeyEncryptionKeyURL != "" {
		t.Errorf("expected KeyEncryptionKeyURL to be empty (not backfilled by EnsureDefaults), got %q", kms.KeyEncryptionKeyURL)
	}
}

// TestPreExistingDataNodePool verifies that CosmosGenericToInternal applies
// canonical defaults when reading a Cosmos document that predates the
// introduction of DiskStorageAccountType.
func TestPreExistingDataNodePool(t *testing.T) {
	resourceID := metadataapi.Must(azcorearm.ParseResourceID(
		"/subscriptions/00000000-0000-0000-0000-000000000000/resourceGroups/rg/providers/Microsoft.RedHatOpenShift/hcpOpenShiftClusters/cluster/nodePools/np",
	))

	// Simulate a pre-existing Cosmos document missing DiskStorageAccountType.
	preExistingDoc := &GenericDocument[coreapi.HCPOpenShiftClusterNodePool]{
		TypedDocument: TypedDocument{
			BaseDocument: BaseDocument{ID: "test-doc-id"},
			ResourceID:   resourceID,
		},
		Content: coreapi.HCPOpenShiftClusterNodePool{
			// DiskStorageAccountType is intentionally zero-valued
			CosmosMetadata: coreapi.CosmosMetadata{
				ResourceID: resourceID,
			},
			Properties: coreapi.HCPOpenShiftClusterNodePoolProperties{
				ProvisioningState: coreapi.ProvisioningStateSucceeded,
				Platform: coreapi.NodePoolPlatformProfile{
					OSDisk: coreapi.OSDiskProfile{
						// DiskStorageAccountType: "" — simulates pre-existing document
					},
				},
			},
			ServiceProviderProperties: coreapi.HCPOpenShiftClusterNodePoolServiceProviderProperties{
				ClusterServiceID: ptr.To(metadataapi.Must(metadataapi.NewInternalID("/api/aro_hcp/v1alpha1/clusters/test-cluster/node_pools/test-np"))),
			},
		},
	}

	internalNodePool, err := CosmosGenericToInternal(preExistingDoc)
	if err != nil {
		t.Fatalf("CosmosGenericToInternal failed: %v", err)
	}

	if internalNodePool.Properties.Platform.OSDisk.DiskStorageAccountType != metadataapi.DiskStorageAccountTypePremium_LRS {
		t.Errorf("got DiskStorageAccountType = %q, want %q",
			internalNodePool.Properties.Platform.OSDisk.DiskStorageAccountType,
			metadataapi.DiskStorageAccountTypePremium_LRS)
	}
	if internalNodePool.Properties.Platform.OSDisk.DiskType != metadataapi.OsDiskTypeManaged {
		t.Errorf("got DiskType = %q, want %q",
			internalNodePool.Properties.Platform.OSDisk.DiskType,
			metadataapi.OsDiskTypeManaged)
	}
}

// TestCanonicalDefaultsConsistencyCluster verifies that the internal constructor
// defaults match the canonical coreapi.Default* constants. This provides compile-time
// linkage between the constants and the actual defaulting behavior.
func TestCanonicalDefaultsConsistencyCluster(t *testing.T) {
	resourceID := metadataapi.Must(azcorearm.ParseResourceID(
		"/subscriptions/00000000-0000-0000-0000-000000000000/resourceGroups/rg/providers/Microsoft.RedHatOpenShift/hcpOpenShiftClusters/cluster",
	))
	internalDefault := coreapi.NewDefaultHCPOpenShiftCluster(resourceID, "eastus")

	// Non-enum defaults (from defaults.go)
	if internalDefault.CustomerProperties.Version.ChannelGroup != coreapi.DefaultClusterVersionChannelGroup {
		t.Errorf("ChannelGroup = %q, want %q", internalDefault.CustomerProperties.Version.ChannelGroup, coreapi.DefaultClusterVersionChannelGroup)
	}
	if internalDefault.CustomerProperties.Network.PodCIDR != coreapi.DefaultClusterNetworkPodCIDR {
		t.Errorf("PodCIDR = %q, want %q", internalDefault.CustomerProperties.Network.PodCIDR, coreapi.DefaultClusterNetworkPodCIDR)
	}
	if internalDefault.CustomerProperties.Network.ServiceCIDR != coreapi.DefaultClusterNetworkServiceCIDR {
		t.Errorf("ServiceCIDR = %q, want %q", internalDefault.CustomerProperties.Network.ServiceCIDR, coreapi.DefaultClusterNetworkServiceCIDR)
	}
	if internalDefault.CustomerProperties.Network.MachineCIDR != coreapi.DefaultClusterNetworkMachineCIDR {
		t.Errorf("MachineCIDR = %q, want %q", internalDefault.CustomerProperties.Network.MachineCIDR, coreapi.DefaultClusterNetworkMachineCIDR)
	}
	if internalDefault.CustomerProperties.Network.HostPrefix != coreapi.DefaultClusterNetworkHostPrefix {
		t.Errorf("HostPrefix = %d, want %d", internalDefault.CustomerProperties.Network.HostPrefix, coreapi.DefaultClusterNetworkHostPrefix)
	}
	if internalDefault.CustomerProperties.Autoscaling.MaxPodGracePeriodSeconds != coreapi.DefaultClusterMaxPodGracePeriodSeconds {
		t.Errorf("MaxPodGracePeriodSeconds = %d, want %d", internalDefault.CustomerProperties.Autoscaling.MaxPodGracePeriodSeconds, coreapi.DefaultClusterMaxPodGracePeriodSeconds)
	}
	if internalDefault.CustomerProperties.Autoscaling.MaxNodeProvisionTimeSeconds != coreapi.DefaultClusterMaxNodeProvisionTimeSeconds {
		t.Errorf("MaxNodeProvisionTimeSeconds = %d, want %d", internalDefault.CustomerProperties.Autoscaling.MaxNodeProvisionTimeSeconds, coreapi.DefaultClusterMaxNodeProvisionTimeSeconds)
	}
	if internalDefault.CustomerProperties.Autoscaling.PodPriorityThreshold != coreapi.DefaultClusterPodPriorityThreshold {
		t.Errorf("PodPriorityThreshold = %d, want %d", internalDefault.CustomerProperties.Autoscaling.PodPriorityThreshold, coreapi.DefaultClusterPodPriorityThreshold)
	}

	// Enum defaults (from enums.go — verify compile-time linkage)
	if internalDefault.CustomerProperties.Network.NetworkType != metadataapi.NetworkTypeOVNKubernetes {
		t.Errorf("NetworkType = %q, want %q", internalDefault.CustomerProperties.Network.NetworkType, metadataapi.NetworkTypeOVNKubernetes)
	}
	if internalDefault.CustomerProperties.API.Visibility != metadataapi.VisibilityPublic {
		t.Errorf("Visibility = %q, want %q", internalDefault.CustomerProperties.API.Visibility, metadataapi.VisibilityPublic)
	}
	if internalDefault.CustomerProperties.Platform.OutboundType != metadataapi.OutboundTypeLoadBalancer {
		t.Errorf("OutboundType = %q, want %q", internalDefault.CustomerProperties.Platform.OutboundType, metadataapi.OutboundTypeLoadBalancer)
	}
	if internalDefault.CustomerProperties.ClusterImageRegistry.State != metadataapi.ClusterImageRegistryStateEnabled {
		t.Errorf("ClusterImageRegistryState = %q, want %q", internalDefault.CustomerProperties.ClusterImageRegistry.State, metadataapi.ClusterImageRegistryStateEnabled)
	}
	if internalDefault.CustomerProperties.Ingress.Type != metadataapi.IngressTypePublic {
		t.Errorf("Ingress.Type = %q, want %q", internalDefault.CustomerProperties.Ingress.Type, metadataapi.IngressTypePublic)
	}
}

// TestCanonicalDefaultsConsistencyNodePool verifies that the internal constructor
// defaults match the canonical coreapi.Default* constants for node pools.
func TestCanonicalDefaultsConsistencyNodePool(t *testing.T) {
	resourceID := metadataapi.Must(azcorearm.ParseResourceID(
		"/subscriptions/00000000-0000-0000-0000-000000000000/resourceGroups/rg/providers/Microsoft.RedHatOpenShift/hcpOpenShiftClusters/cluster/nodePools/np",
	))
	internalDefault := coreapi.NewDefaultHCPOpenShiftClusterNodePool(resourceID, "eastus")

	if internalDefault.Properties.Version.ChannelGroup != coreapi.DefaultNodePoolVersionChannelGroup {
		t.Errorf("ChannelGroup = %q, want %q", internalDefault.Properties.Version.ChannelGroup, coreapi.DefaultNodePoolVersionChannelGroup)
	}
	if ptr.Deref(internalDefault.Properties.Platform.OSDisk.SizeGiB, 0) != coreapi.DefaultNodePoolOSDiskSizeGiB {
		t.Errorf("OSDiskSizeGiB = %d, want %d", ptr.Deref(internalDefault.Properties.Platform.OSDisk.SizeGiB, 0), coreapi.DefaultNodePoolOSDiskSizeGiB)
	}
	if internalDefault.Properties.AutoRepair != true {
		t.Errorf("AutoRepair = %v, want %v", internalDefault.Properties.AutoRepair, true)
	}
	if internalDefault.Properties.Platform.OSDisk.DiskStorageAccountType != metadataapi.DiskStorageAccountTypePremium_LRS {
		t.Errorf("DiskStorageAccountType = %q, want %q", internalDefault.Properties.Platform.OSDisk.DiskStorageAccountType, metadataapi.DiskStorageAccountTypePremium_LRS)
	}
	if internalDefault.Properties.Platform.OSDisk.DiskType != metadataapi.OsDiskTypeManaged {
		t.Errorf("DiskType = %q, want %q", internalDefault.Properties.Platform.OSDisk.DiskType, metadataapi.OsDiskTypeManaged)
	}
}

// TestEnsureDefaultsConsistencyExternalAuth verifies that the defaults applied
// by EnsureDefaults match the corresponding defaults in the
// versioned constructors. This catches drift between the defaulting layers
// described in docs/api-version-defaults-and-storage.md.
func TestEnsureDefaultsConsistencyExternalAuth(t *testing.T) {
	ensuredDefault := &coreapi.HCPOpenShiftClusterExternalAuth{}
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
	t.Run("v20260630preview", func(t *testing.T) {
		externalDefault := &v20260630preview.ExternalAuth{}
		v20260630preview.SetDefaultValuesExternalAuth(externalDefault)

		if stringPtrFromGenerated(externalDefault.Properties.Claim.Mappings.Username.PrefixPolicy) == nil {
			t.Errorf("versioned default PrefixPolicy is nil, expected %q", ensuredDefault.Properties.Claim.Mappings.Username.PrefixPolicy)
		} else if string(ensuredDefault.Properties.Claim.Mappings.Username.PrefixPolicy) != *stringPtrFromGenerated(externalDefault.Properties.Claim.Mappings.Username.PrefixPolicy) {
			t.Errorf("ensured default PrefixPolicy = %q, versioned default = %q",
				ensuredDefault.Properties.Claim.Mappings.Username.PrefixPolicy,
				*stringPtrFromGenerated(externalDefault.Properties.Claim.Mappings.Username.PrefixPolicy))
		}
	})
	t.Run("v20260901preview", func(t *testing.T) {
		externalDefault := &v20260901preview.ExternalAuth{}
		v20260901preview.SetDefaultValuesExternalAuth(externalDefault)

		if stringPtrFromGenerated(externalDefault.Properties.Claim.Mappings.Username.PrefixPolicy) == nil {
			t.Errorf("versioned default PrefixPolicy is nil, expected %q", ensuredDefault.Properties.Claim.Mappings.Username.PrefixPolicy)
		} else if string(ensuredDefault.Properties.Claim.Mappings.Username.PrefixPolicy) != *stringPtrFromGenerated(externalDefault.Properties.Claim.Mappings.Username.PrefixPolicy) {
			t.Errorf("ensured default PrefixPolicy = %q, versioned default = %q",
				ensuredDefault.Properties.Claim.Mappings.Username.PrefixPolicy,
				*stringPtrFromGenerated(externalDefault.Properties.Claim.Mappings.Username.PrefixPolicy))
		}
	})
	t.Run("v20261003preview", func(t *testing.T) {
		externalDefault := &v20261003preview.ExternalAuth{}
		v20261003preview.SetDefaultValuesExternalAuth(externalDefault)

		if stringPtrFromGenerated(externalDefault.Properties.Claim.Mappings.Username.PrefixPolicy) == nil {
			t.Errorf("versioned default PrefixPolicy is nil, expected %q", ensuredDefault.Properties.Claim.Mappings.Username.PrefixPolicy)
		} else if string(ensuredDefault.Properties.Claim.Mappings.Username.PrefixPolicy) != *stringPtrFromGenerated(externalDefault.Properties.Claim.Mappings.Username.PrefixPolicy) {
			t.Errorf("ensured default PrefixPolicy = %q, versioned default = %q",
				ensuredDefault.Properties.Claim.Mappings.Username.PrefixPolicy,
				*stringPtrFromGenerated(externalDefault.Properties.Claim.Mappings.Username.PrefixPolicy))
		}
	})
}

// TestPreExistingDataExternalAuth verifies that CosmosGenericToInternal
// applies canonical defaults when reading a Cosmos document that predates the
// introduction of the PrefixPolicy field.
func TestPreExistingDataExternalAuth(t *testing.T) {
	resourceID := metadataapi.Must(azcorearm.ParseResourceID(
		"/subscriptions/00000000-0000-0000-0000-000000000000/resourceGroups/rg/providers/Microsoft.RedHatOpenShift/hcpOpenShiftClusters/cluster/externalAuths/default",
	))

	internalID := metadataapi.Must(metadataapi.NewInternalID("/api/aro_hcp/v1alpha1/clusters/test-cluster/external_auth_config/external_auths/default"))
	preExistingDoc := &GenericDocument[coreapi.HCPOpenShiftClusterExternalAuth]{
		TypedDocument: TypedDocument{
			BaseDocument: BaseDocument{ID: "test-doc-id"},
			ResourceID:   resourceID,
		},
		Content: coreapi.HCPOpenShiftClusterExternalAuth{
			// PrefixPolicy is intentionally zero-valued to simulate
			// a pre-existing document that predates the field.
			CosmosMetadata: coreapi.CosmosMetadata{
				ResourceID: resourceID,
			},
			Properties: coreapi.HCPOpenShiftClusterExternalAuthProperties{
				ProvisioningState: coreapi.ProvisioningStateSucceeded,
			},
			ServiceProviderProperties: coreapi.HCPOpenShiftClusterExternalAuthServiceProviderProperties{
				ClusterServiceID: &internalID,
			},
		},
	}

	internalExternalAuth, err := CosmosGenericToInternal(preExistingDoc)
	if err != nil {
		t.Fatalf("CosmosGenericToInternal failed: %v", err)
	}

	if internalExternalAuth.Properties.Claim.Mappings.Username.PrefixPolicy != metadataapi.UsernameClaimPrefixPolicyNone {
		t.Errorf("got PrefixPolicy = %q, want %q",
			internalExternalAuth.Properties.Claim.Mappings.Username.PrefixPolicy,
			metadataapi.UsernameClaimPrefixPolicyNone)
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
