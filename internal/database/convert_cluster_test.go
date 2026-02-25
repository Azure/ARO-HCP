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
	"bytes"
	"encoding/json"
	"math/rand"
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/stretchr/testify/require"

	"k8s.io/apimachinery/pkg/api/equality"

	"sigs.k8s.io/randfill"

	azcorearm "github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"

	"github.com/Azure/ARO-HCP/internal/api"
	"github.com/Azure/ARO-HCP/internal/api/arm"
)

// fuzzerFor can randomly populate api objects that are destined for version.
func fuzzerFor(funcs []interface{}, src rand.Source) *randfill.Filler {
	f := randfill.New().NilChance(.5).NumElements(0, 1)
	if src != nil {
		f.RandSource(src)
	}
	f.Funcs(funcs...)
	return f
}

func TestRoundTripClusterInternalCosmosInternal(t *testing.T) {
	seed := rand.Int63()
	t.Logf("seed: %d", seed)

	fuzzer := fuzzerFor([]interface{}{
		func(j *azcorearm.ResourceID, c randfill.Continue) {
			*j = *api.Must(azcorearm.ParseResourceID("/subscriptions/00000000-0000-0000-0000-000000000000/resourceGroups/myRg"))
		},
		func(j *arm.Resource, c randfill.Continue) {
			c.FillNoCustom(j)
			j.ID = api.Must(azcorearm.ParseResourceID("/subscriptions/0465bc32-c654-41b8-8d87-9815d7abe8f6/resourceGroups/some-resource-group/providers/Microsoft.RedHatOpenShift/hcpOpenShiftClusters/change-channel"))
			j.Name = "change-channel"
			j.Type = "Microsoft.RedHatOpenShift/hcpOpenShiftClusters"
		},
		func(j *api.HCPOpenShiftClusterServiceProviderProperties, c randfill.Continue) {
			c.FillNoCustom(j)
			if j == nil {
				return
			}
			// we must always have an internal ID
			foo := api.Must(api.NewInternalID("/api/clusters_mgmt/v1/clusters/r" + strings.ReplaceAll(c.String(10), "/", "-")))
			j.ClusterServiceID = foo
		},
		func(j *api.HCPOpenShiftCluster, c randfill.Continue) {
			c.FillNoCustom(j)
			if j == nil {
				return
			}
			j.ServiceProviderProperties.ExistingCosmosUID = ""
			// Storage defaults are applied on Cosmos read, so ensure
			// defaulted fields are never zero during round-trip testing.
			if len(j.CustomerProperties.Network.NetworkType) == 0 {
				j.CustomerProperties.Network.NetworkType = api.NetworkTypeOVNKubernetes
			}
			if len(j.CustomerProperties.API.Visibility) == 0 {
				j.CustomerProperties.API.Visibility = api.VisibilityPublic
			}
			if len(j.CustomerProperties.Platform.OutboundType) == 0 {
				j.CustomerProperties.Platform.OutboundType = api.OutboundTypeLoadBalancer
			}
			if len(j.CustomerProperties.ClusterImageRegistry.State) == 0 {
				j.CustomerProperties.ClusterImageRegistry.State = api.ClusterImageRegistryProfileStateEnabled
			}
			if len(j.CustomerProperties.Etcd.DataEncryption.KeyManagementMode) == 0 {
				j.CustomerProperties.Etcd.DataEncryption.KeyManagementMode = api.EtcdDataEncryptionKeyManagementModeTypePlatformManaged
			}
		},
		func(j *arm.ManagedServiceIdentity, c randfill.Continue) {
			c.FillNoCustom(j)

			// we only round trip keys, so only fill in keys
			if j != nil && j.UserAssignedIdentities != nil {
				for k := range j.UserAssignedIdentities {
					j.UserAssignedIdentities[k] = nil
				}
			}
		},
	}, rand.NewSource(seed))

	// Try a few times, since runTest uses random values.
	for i := 0; i < 20; i++ {
		original := &api.HCPOpenShiftCluster{}
		fuzzer.Fill(original)
		roundTripInternalToCosmosToInternal[api.HCPOpenShiftCluster, HCPCluster](t, original)
	}
}

func roundTripInternalToCosmosToInternal[InternalAPIType, CosmosAPIType any](t *testing.T, original *InternalAPIType) {
	originalBeforeJSON, _ := json.MarshalIndent(original, "", "    ")

	intermediate, err := InternalToCosmos[InternalAPIType, CosmosAPIType](original)
	require.NoError(t, err)
	intermediateBeforeJSON, _ := json.MarshalIndent(intermediate, "", "    ")

	final, err := CosmosToInternal[InternalAPIType, CosmosAPIType](intermediate)
	require.NoError(t, err)

	// this value is set during conversion, so we need clear for comparison
	switch cast := any(final).(type) {
	case *api.HCPOpenShiftCluster:
		cast.ServiceProviderProperties.ExistingCosmosUID = ""
	case *api.HCPOpenShiftClusterNodePool:
		cast.ServiceProviderProperties.ExistingCosmosUID = ""
	case *api.HCPOpenShiftClusterExternalAuth:
		cast.ServiceProviderProperties.ExistingCosmosUID = ""
	}
	//finalJSON, _ := json.MarshalIndent(final, "", "    ")

	// we compare the JSON here because many of these types have private fields that cannot be introspected
	if !equality.Semantic.DeepEqual(original, final) {
		//t.Logf("original\n%s", string(originalBeforeJSON))
		//t.Logf("intermediate\n%s", string(intermediateBeforeJSON))
		//t.Logf("final\n%s", string(finalJSON))
		t.Errorf("Round trip failed: %v", cmp.Diff(original, final, api.CmpDiffOptions...))
	}

	// now check to be sure we didn't mutate the originals.  The copies still aren't deep, but at least we didn't nuke the inputs
	originalAfterJSON, _ := json.MarshalIndent(original, "", "    ")
	if !bytes.Equal(originalBeforeJSON, originalAfterJSON) {
		t.Logf("original\n%s", string(originalBeforeJSON))
		t.Logf("originalAfter\n%s", string(originalAfterJSON))
		t.Errorf("original was modified: %v", cmp.Diff(originalBeforeJSON, originalAfterJSON))
	}

	// now check to be sure we didn't mutate the originals.  The copies still aren't deep, but at least we didn't nuke the inputs
	intermediateAfterJSON, _ := json.MarshalIndent(intermediate, "", "    ")
	if !bytes.Equal(intermediateBeforeJSON, intermediateAfterJSON) {
		t.Logf("intermediate\n%s", string(intermediateBeforeJSON))
		t.Logf("intermediateAfter\n%s", string(intermediateAfterJSON))
		t.Errorf("intermediate was modified: %v", cmp.Diff(intermediateBeforeJSON, intermediateAfterJSON))
	}
}

func TestApplyClusterStorageDefaults(t *testing.T) {
	tests := []struct {
		name               string
		networkType        api.NetworkType
		visibility         api.Visibility
		outboundType       api.OutboundType
		imageRegistryState api.ClusterImageRegistryProfileState
		keyManagementMode  api.EtcdDataEncryptionKeyManagementModeType
		wantNetworkType    api.NetworkType
		wantVisibility     api.Visibility
		wantOutboundType   api.OutboundType
		wantImageRegState  api.ClusterImageRegistryProfileState
		wantKeyMgmtMode    api.EtcdDataEncryptionKeyManagementModeType
	}{
		{
			name:              "zero values get defaults",
			wantNetworkType:   api.NetworkTypeOVNKubernetes,
			wantVisibility:    api.VisibilityPublic,
			wantOutboundType:  api.OutboundTypeLoadBalancer,
			wantImageRegState: api.ClusterImageRegistryProfileStateEnabled,
			wantKeyMgmtMode:   api.EtcdDataEncryptionKeyManagementModeTypePlatformManaged,
		},
		{
			name:               "explicit values preserved",
			networkType:        api.NetworkTypeOVNKubernetes,
			visibility:         api.VisibilityPrivate,
			outboundType:       api.OutboundTypeLoadBalancer,
			imageRegistryState: api.ClusterImageRegistryProfileStateDisabled,
			keyManagementMode:  api.EtcdDataEncryptionKeyManagementModeTypeCustomerManaged,
			wantNetworkType:    api.NetworkTypeOVNKubernetes,
			wantVisibility:     api.VisibilityPrivate,
			wantOutboundType:   api.OutboundTypeLoadBalancer,
			wantImageRegState:  api.ClusterImageRegistryProfileStateDisabled,
			wantKeyMgmtMode:    api.EtcdDataEncryptionKeyManagementModeTypeCustomerManaged,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cluster := &api.HCPOpenShiftCluster{}
			cluster.CustomerProperties.Network.NetworkType = tt.networkType
			cluster.CustomerProperties.API.Visibility = tt.visibility
			cluster.CustomerProperties.Platform.OutboundType = tt.outboundType
			cluster.CustomerProperties.ClusterImageRegistry.State = tt.imageRegistryState
			cluster.CustomerProperties.Etcd.DataEncryption.KeyManagementMode = tt.keyManagementMode

			applyClusterStorageDefaults(cluster)

			if cluster.CustomerProperties.Network.NetworkType != tt.wantNetworkType {
				t.Errorf("NetworkType = %q, want %q",
					cluster.CustomerProperties.Network.NetworkType, tt.wantNetworkType)
			}
			if cluster.CustomerProperties.API.Visibility != tt.wantVisibility {
				t.Errorf("Visibility = %q, want %q",
					cluster.CustomerProperties.API.Visibility, tt.wantVisibility)
			}
			if cluster.CustomerProperties.Platform.OutboundType != tt.wantOutboundType {
				t.Errorf("OutboundType = %q, want %q",
					cluster.CustomerProperties.Platform.OutboundType, tt.wantOutboundType)
			}
			if cluster.CustomerProperties.ClusterImageRegistry.State != tt.wantImageRegState {
				t.Errorf("ClusterImageRegistry.State = %q, want %q",
					cluster.CustomerProperties.ClusterImageRegistry.State, tt.wantImageRegState)
			}
			if cluster.CustomerProperties.Etcd.DataEncryption.KeyManagementMode != tt.wantKeyMgmtMode {
				t.Errorf("Etcd.DataEncryption.KeyManagementMode = %q, want %q",
					cluster.CustomerProperties.Etcd.DataEncryption.KeyManagementMode, tt.wantKeyMgmtMode)
			}
		})
	}
}
