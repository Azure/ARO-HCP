// Copyright 2026 Microsoft Corporation
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

package kubeapplierhelpers

import (
	"encoding/json"
	"testing"

	"github.com/openshift/hypershift/api/hypershift/v1beta1"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/utils/ptr"

	"github.com/Azure/ARO-HCP/internal/api/kubeapplierapi"
)

func TestHostedClusterIdentityHandoffGates(t *testing.T) {
	base := &kubeapplierapi.ApplyDesire{
		Spec: kubeapplierapi.ApplyDesireSpec{
			Type: kubeapplierapi.ApplyDesireTypeServerSideApply,
			ServerSideApply: &kubeapplierapi.ServerSideApplyConfig{
				KubeContent: &runtime.RawExtension{Raw: []byte(`{"spec":{"platform":{"azure":{"azureAuthenticationConfig":{"managedIdentities":{"dataPlane":{"diskMSIClientID":"old"}}}}}}}`)},
			},
		},
		Status: kubeapplierapi.ApplyDesireStatus{
			AppliedKubeGeneration: ptr.To(int64(1)),
			Conditions:            []metav1.Condition{{Type: kubeapplierapi.ConditionTypeSuccessfullyApplied, Status: metav1.ConditionTrue}},
		},
	}
	assert.True(t, ApplyDesireSuccessfullyApplied(base))
	omits, err := HostedClusterBaseDesireOmitsDataPlane(base)
	require.NoError(t, err)
	assert.False(t, omits)
	base.Spec.ServerSideApply.KubeContent.Raw = []byte(`{"spec":{"platform":{"azure":{"azureAuthenticationConfig":{"managedIdentities":{}}}}}}`)
	omits, err = HostedClusterBaseDesireOmitsDataPlane(base)
	require.NoError(t, err)
	assert.True(t, omits)
	base.Status.AppliedKubeGeneration = nil
	assert.False(t, ApplyDesireSuccessfullyApplied(base))
}

func TestHostedClusterNoLongerUsesClientID(t *testing.T) {
	var hc v1beta1.HostedCluster
	require.NoError(t, json.Unmarshal([]byte(`{"spec":{"platform":{"azure":{"azureAuthenticationConfig":{"azureAuthenticationConfigType":"ManagedIdentities","managedIdentities":{"dataPlane":{"imageRegistryMSIClientID":"old","diskMSIClientID":"disk","fileMSIClientID":"file"}}}}}}}`), &hc))
	assert.False(t, HostedClusterNoLongerUsesClientID(nil, "image-registry", "old"))
	assert.False(t, HostedClusterNoLongerUsesClientID(&hc, "image-registry", "old"))
	assert.False(t, HostedClusterNoLongerUsesClientID(&hc, "image-registry", "OLD"))
	assert.True(t, HostedClusterNoLongerUsesClientID(&hc, "image-registry", "previous"))
	assert.True(t, HostedClusterNoLongerUsesClientID(&hc, "disk-csi-driver", "previous"))
	assert.True(t, HostedClusterNoLongerUsesClientID(&hc, "file-csi-driver", "previous"))
	assert.False(t, HostedClusterNoLongerUsesClientID(&hc, "legacy-operator", "old"))
	assert.True(t, HostedClusterNoLongerUsesClientID(&hc, "legacy-operator", "previous"))
	hc.Spec.Platform.Azure.AzureAuthenticationConfig.ManagedIdentities.DataPlane.DiskMSIClientID = "old"
	assert.False(t, HostedClusterNoLongerUsesClientID(&hc, "file-csi-driver", "old"))
}
