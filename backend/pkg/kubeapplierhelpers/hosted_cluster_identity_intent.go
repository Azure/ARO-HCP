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
	"fmt"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"

	"github.com/openshift/hypershift/api/hypershift/v1beta1"
)

// HostedClusterDataPlaneClientIDs is the complete identity intent owned by
// HostedClusterDataPlaneIdentities. Other DesiredHostedCluster leaves belong
// to their respective controllers.
type HostedClusterDataPlaneClientIDs struct {
	ImageRegistryMSIClientID string
	DiskMSIClientID          string
	FileMSIClientID          string
}

func (ids HostedClusterDataPlaneClientIDs) Complete() bool {
	return ids.ImageRegistryMSIClientID != "" && ids.DiskMSIClientID != "" && ids.FileMSIClientID != ""
}

func DataPlaneClientIDsFromHostedCluster(hc *v1beta1.HostedCluster) HostedClusterDataPlaneClientIDs {
	if hc == nil || hc.Spec.Platform.Azure == nil ||
		hc.Spec.Platform.Azure.AzureAuthenticationConfig.ManagedIdentities == nil {
		return HostedClusterDataPlaneClientIDs{}
	}
	dp := hc.Spec.Platform.Azure.AzureAuthenticationConfig.ManagedIdentities.DataPlane
	return HostedClusterDataPlaneClientIDs{
		ImageRegistryMSIClientID: string(dp.ImageRegistryMSIClientID),
		DiskMSIClientID:          string(dp.DiskMSIClientID),
		FileMSIClientID:          string(dp.FileMSIClientID),
	}
}

// SetDesiredHostedClusterDataPlaneClientIDs changes only the three owned
// leaves; it preserves certificates and any other partial intent.
func SetDesiredHostedClusterDataPlaneClientIDs(hc *v1beta1.HostedCluster, ids HostedClusterDataPlaneClientIDs) error {
	if hc == nil || !ids.Complete() {
		return fmt.Errorf("HostedCluster identity intent requires all three ClientIDs")
	}
	if hc.Spec.Platform.Azure == nil {
		hc.Spec.Platform.Azure = &v1beta1.AzurePlatformSpec{}
	}
	if hc.Spec.Platform.Azure.AzureAuthenticationConfig.ManagedIdentities == nil {
		hc.Spec.Platform.Azure.AzureAuthenticationConfig.ManagedIdentities = &v1beta1.AzureResourceManagedIdentities{}
	}
	dp := &hc.Spec.Platform.Azure.AzureAuthenticationConfig.ManagedIdentities.DataPlane
	dp.ImageRegistryMSIClientID = ids.ImageRegistryMSIClientID
	dp.DiskMSIClientID = ids.DiskMSIClientID
	dp.FileMSIClientID = ids.FileMSIClientID
	return nil
}

// OverlayHostedClusterDataPlaneClientIDs mutates only the three ClientID
// leaves of a full Cluster Service HostedCluster manifest.
func OverlayHostedClusterDataPlaneClientIDs(hc *unstructured.Unstructured, ids HostedClusterDataPlaneClientIDs) error {
	if hc == nil || !ids.Complete() {
		return fmt.Errorf("HostedCluster identity overlay requires all three ClientIDs")
	}
	path := []string{"spec", "platform", "azure", "azureAuthenticationConfig"}
	configType, found, err := unstructured.NestedString(hc.Object, append(path, "azureAuthenticationConfigType")...)
	if err != nil {
		return fmt.Errorf("read Cluster Service HostedCluster authentication type: %w", err)
	}
	if !found || configType != string(v1beta1.AzureAuthenticationTypeManagedIdentities) {
		return fmt.Errorf("cluster service HostedCluster is not configured for managed identities: type=%q, found=%t", configType, found)
	}
	path = append(path, "managedIdentities", "dataPlane")
	for field, value := range map[string]string{
		"imageRegistryMSIClientID": ids.ImageRegistryMSIClientID,
		"diskMSIClientID":          ids.DiskMSIClientID,
		"fileMSIClientID":          ids.FileMSIClientID,
	} {
		if err := unstructured.SetNestedField(hc.Object, value, append(path, field)...); err != nil {
			return fmt.Errorf("set HostedCluster %s: %w", field, err)
		}
	}
	return nil
}
