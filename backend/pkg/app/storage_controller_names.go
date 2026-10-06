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

package app

import (
	"slices"
	"strings"

	"github.com/Azure/ARO-HCP/backend/pkg/azure/cachedreader"
	"github.com/Azure/ARO-HCP/backend/pkg/controllers/billing"
	credentialrequestdeletion "github.com/Azure/ARO-HCP/backend/pkg/controllers/cluster/credentialrequest/deletion"
	clusterdeletion "github.com/Azure/ARO-HCP/backend/pkg/controllers/cluster/deletion"
	clusterplacement "github.com/Azure/ARO-HCP/backend/pkg/controllers/cluster/placement"
	"github.com/Azure/ARO-HCP/backend/pkg/controllers/cluster/version/rollout"
	"github.com/Azure/ARO-HCP/backend/pkg/controllers/metrics"
	"github.com/Azure/ARO-HCP/backend/pkg/controllers/mismatch"
	unionkubeapplierinformers "github.com/Azure/ARO-HCP/internal/database/unioninformers/kubeapplier"
)

const (
	BackendInformersStorageName = "BackendInformers"
	FleetInformersStorageName   = "FleetInformers"
)

// controllersWithoutStorage only use cached data or Azure APIs.
func controllersWithoutStorage() map[string]ControllerRegistration {
	registry := map[string]ControllerRegistration{}
	metrics.Register(registry)
	registry[strings.ToLower(cachedreader.FPAVirtualMachineResourceSKUsCachedReaderControllerName)] = ControllerRegistration{}
	return registry
}

// BackendStorageControllerNames derives storage consumers from the launch registry,
// including shared informers and excluding controllers that only use cached data.
func BackendStorageControllerNames(hasRealFPA bool) []string {
	names := []string{BackendInformersStorageName, FleetInformersStorageName}
	withoutStorage := controllersWithoutStorage()
	for name, entry := range newControllerRegistry() {
		if _, skip := withoutStorage[name]; skip {
			continue
		}
		if entry.Enabled != nil && !entry.Enabled(ControllerContext{HasRealFPA: hasRealFPA}) {
			continue
		}
		names = append(names, name)
	}
	slices.Sort(names)
	return names
}

// BackendCleanupControllerFractions reduces background cleanup work's RU share.
// Controllers on the customer deletion path retain the normal budget.
func BackendCleanupControllerFractions(fraction float64) map[string]float64 {
	return map[string]float64{
		strings.ToLower(rollout.RolloutRetirementControllerName):                                fraction,
		strings.ToLower(mismatch.DeleteOrphanedCosmosResourcesControllerName):                   fraction,
		strings.ToLower(billing.OrphanedBillingCleanupControllerName):                           fraction,
		strings.ToLower(clusterplacement.PendingCleanupControllerName):                          fraction,
		strings.ToLower(clusterdeletion.CleanOrphanedClusterManagedResourceGroupControllerName): fraction,
		strings.ToLower(credentialrequestdeletion.SystemAdminCredentialRevokedGCControllerName): fraction,
	}
}

// BackendStorageFactoryOptions assigns independent RU budgets to each physical
// container. Resources, Billing, and Fleet use 80% of their configured maxima.
// Each MC container reserves 30% for the backend and 50% for kube-applier, leaving
// 20% headroom. Cleanup controllers get 10% of a normal controller's share.
// Shared backend informers use unlimited clients outside these allocations.
func BackendStorageFactoryOptions(hasRealFPA bool) StorageFactoryOptions {
	return StorageFactoryOptions{
		ResourcesRUsPerSecond:   19000,
		BillingRUsPerSecond:     4000,
		FleetRUsPerSecond:       4000,
		KubeApplierRUsPerSecond: 19000,
		KubeApplierUtilization:  0.3,
		ControllerNames:         BackendStorageControllerNames(hasRealFPA),
		ControllerFractions:     BackendCleanupControllerFractions(0.1),
		UnlimitedControllerNames: []string{
			BackendInformersStorageName,
			FleetInformersStorageName,
			unionkubeapplierinformers.ControllerName,
		},
	}
}
