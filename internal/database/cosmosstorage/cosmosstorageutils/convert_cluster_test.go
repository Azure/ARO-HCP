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

	"github.com/Azure/ARO-HCP/internal/api/coreapi"
	"github.com/Azure/ARO-HCP/internal/api/metadataapi"
)

func TestClusterEnsureDefaults(t *testing.T) {
	tests := []struct {
		name               string
		networkType        metadataapi.NetworkType
		visibility         metadataapi.Visibility
		outboundType       metadataapi.OutboundType
		imageRegistryState metadataapi.ClusterImageRegistryState
		keyManagementMode  metadataapi.EtcdDataEncryptionKeyManagementModeType
		wantNetworkType    metadataapi.NetworkType
		wantVisibility     metadataapi.Visibility
		wantOutboundType   metadataapi.OutboundType
		wantImageRegState  metadataapi.ClusterImageRegistryState
		wantKeyMgmtMode    metadataapi.EtcdDataEncryptionKeyManagementModeType
	}{
		{
			name:              "zero values get defaults",
			wantNetworkType:   metadataapi.NetworkTypeOVNKubernetes,
			wantVisibility:    metadataapi.VisibilityPublic,
			wantOutboundType:  metadataapi.OutboundTypeLoadBalancer,
			wantImageRegState: metadataapi.ClusterImageRegistryStateEnabled,
		},
		{
			name:               "explicit values preserved",
			networkType:        metadataapi.NetworkTypeOVNKubernetes,
			visibility:         metadataapi.VisibilityPrivate,
			outboundType:       metadataapi.OutboundTypeLoadBalancer,
			imageRegistryState: metadataapi.ClusterImageRegistryStateDisabled,
			keyManagementMode:  metadataapi.EtcdDataEncryptionKeyManagementModeTypeCustomerManaged,
			wantNetworkType:    metadataapi.NetworkTypeOVNKubernetes,
			wantVisibility:     metadataapi.VisibilityPrivate,
			wantOutboundType:   metadataapi.OutboundTypeLoadBalancer,
			wantImageRegState:  metadataapi.ClusterImageRegistryStateDisabled,
			wantKeyMgmtMode:    metadataapi.EtcdDataEncryptionKeyManagementModeTypeCustomerManaged,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cluster := &coreapi.HCPOpenShiftCluster{}
			cluster.CustomerProperties.Network.NetworkType = tt.networkType
			cluster.CustomerProperties.API.Visibility = tt.visibility
			cluster.CustomerProperties.Platform.OutboundType = tt.outboundType
			cluster.CustomerProperties.ClusterImageRegistry.State = tt.imageRegistryState
			cluster.CustomerProperties.Etcd.DataEncryption.KeyManagementMode = tt.keyManagementMode

			cluster.EnsureDefaults()

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
