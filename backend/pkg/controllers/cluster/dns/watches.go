// Copyright 2026 Microsoft Corporation
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://www.apache.org/licenses/LICENSE-2.0
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package dns

import (
	"k8s.io/client-go/tools/cache"

	"github.com/Azure/ARO-HCP/backend/pkg/utils/controllerutils"
	"github.com/Azure/ARO-HCP/internal/api/fleetapi"
)

func enqueueClusterForDNSClaim(queue controllerutils.Enqueuer) func(any) {
	return func(obj any) {
		if key, ok := claimClusterKey(obj); ok {
			queue.Enqueue(key)
		}
	}
}

// claimClusterKey maps a reservation event, including a deletion tombstone, to its owner.
func claimClusterKey(obj any) (controllerutils.HCPClusterKey, bool) {
	if tombstone, ok := obj.(cache.DeletedFinalStateUnknown); ok {
		obj = tombstone.Obj
	}
	claim, ok := obj.(*fleetapi.DNSClaim)
	if !ok {
		return controllerutils.HCPClusterKey{}, false
	}
	id := claim.ClusterResourceID
	if id == nil {
		return controllerutils.HCPClusterKey{}, false
	}
	return controllerutils.HCPClusterKey{
		SubscriptionID:    id.SubscriptionID,
		ResourceGroupName: id.ResourceGroupName,
		HCPClusterName:    id.Name,
	}, true
}
