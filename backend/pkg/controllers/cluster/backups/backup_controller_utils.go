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

package backups

import "github.com/Azure/ARO-HCP/internal/api/coreapi"

// needsWork returns true when backup desires should be reconciled for the cluster.
// Clusters being deleted or that have never reached Succeeded state are skipped.
func needsWork(existingCluster coreapi.HCPOpenShiftCluster, serviceProviderCluster coreapi.ServiceProviderCluster) bool {
	if existingCluster.ServiceProviderProperties.DeletionTimestamp != nil {
		return false
	}
	if existingCluster.ServiceProviderProperties.BillingDocumentCosmosID == "" {
		return false
	}
	if existingCluster.ResourceID == nil {
		return false
	}
	if serviceProviderCluster.Status.ManagementClusterResourceID == nil {
		return false
	}
	if serviceProviderCluster.Status.ControlPlaneNamespace == "" {
		return false
	}
	if serviceProviderCluster.Status.HostedClusterNamespace == "" {
		return false
	}

	return true
}
