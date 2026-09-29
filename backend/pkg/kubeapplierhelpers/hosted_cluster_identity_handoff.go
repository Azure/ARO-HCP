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
	"fmt"
	"strings"

	"github.com/openshift/hypershift/api/hypershift/v1beta1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"

	"github.com/Azure/ARO-HCP/internal/api/kubeapplierapi"
	"github.com/Azure/ARO-HCP/internal/apihelpers/kubeapplierapihelpers"
	"github.com/Azure/ARO-HCP/internal/azure"
)

const (
	HostedClusterBaseDesireName                = "HostedCluster"
	HostedClusterDataPlaneIdentityDesireName   = "HostedClusterDataPlaneIdentities"
	HostedClusterDataPlaneIdentityFieldManager = "aro-hcp-mi-controller"
)

// ApplyDesireSuccessfullyApplied is true only after kube-applier accepted the
// current desire spec. EnsureApplyDesire clears status whenever it replaces the
// spec, so an earlier successful apply cannot satisfy the handoff gate.
func ApplyDesireSuccessfullyApplied(desire *kubeapplierapi.ApplyDesire) bool {
	return desire != nil && desire.Spec.Type == kubeapplierapi.ApplyDesireTypeServerSideApply &&
		desire.Status.AppliedKubeGeneration != nil &&
		kubeapplierapihelpers.IsConditionTruePreferring(
			desire.Status.Conditions,
			kubeapplierapi.ConditionTypeSuccessfullyApplied,
			kubeapplierapi.ConditionTypeSuccessful,
		)
}

// HostedClusterNoLongerUsesClientID requires a real observed HostedCluster
// before an old identity's credentials can be retired. All three ClientID
// fields must have moved off the old identity. Operators with no HostedCluster
// field can drain once the mirror is available.
func HostedClusterNoLongerUsesClientID(hc *v1beta1.HostedCluster, operator, oldClientID string) bool {
	if hc == nil || oldClientID == "" || hc.Spec.Platform.Azure == nil ||
		hc.Spec.Platform.Azure.AzureAuthenticationConfig.ManagedIdentities == nil {
		return false
	}
	dp := hc.Spec.Platform.Azure.AzureAuthenticationConfig.ManagedIdentities.DataPlane
	var observed string
	switch operator {
	case string(azure.ClusterOperatorIdentifierImageRegistry):
		observed = string(dp.ImageRegistryMSIClientID)
	case string(azure.ClusterOperatorIdentifierDiskCSIDriver):
		observed = string(dp.DiskMSIClientID)
	case string(azure.ClusterOperatorIdentifierFileCSIDriver):
		observed = string(dp.FileMSIClientID)
	default:
		// An old operator has no own slot, but its identity may still be
		// shared with a current operator.
		return !strings.EqualFold(string(dp.ImageRegistryMSIClientID), oldClientID) &&
			!strings.EqualFold(string(dp.DiskMSIClientID), oldClientID) &&
			!strings.EqualFold(string(dp.FileMSIClientID), oldClientID)
	}
	return observed != "" && !strings.EqualFold(observed, oldClientID) &&
		!strings.EqualFold(string(dp.ImageRegistryMSIClientID), oldClientID) &&
		!strings.EqualFold(string(dp.DiskMSIClientID), oldClientID) &&
		!strings.EqualFold(string(dp.FileMSIClientID), oldClientID)
}

// HostedClusterBaseDesireOmitsDataPlane reports what the base manager currently
// declares. It does not imply the current spec has been applied successfully.
func HostedClusterBaseDesireOmitsDataPlane(desire *kubeapplierapi.ApplyDesire) (bool, error) {
	if desire == nil || desire.Spec.ServerSideApply == nil || desire.Spec.ServerSideApply.KubeContent == nil {
		return false, nil
	}
	obj := &unstructured.Unstructured{}
	if err := json.Unmarshal(desire.Spec.ServerSideApply.KubeContent.Raw, &obj.Object); err != nil {
		return false, fmt.Errorf("decode base HostedCluster desire: %w", err)
	}
	_, found, err := unstructured.NestedFieldNoCopy(obj.Object,
		"spec", "platform", "azure", "azureAuthenticationConfig", "managedIdentities", "dataPlane")
	return !found, err
}
