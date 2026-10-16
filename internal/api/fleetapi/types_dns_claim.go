// Copyright 2026 Microsoft Corporation
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://www.apache.org/licenses/LICENSE-2.0
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package fleetapi

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/Azure/ARO-HCP/internal/api/coreapi"
)

// DNSClaim is an exclusive allocation of one slug beneath a public parent zone.
// Its storage identity is independent of customer subscription and management cluster.
// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object
type DNSClaim struct {
	// CosmosMetadata identifies the zone/slug claim. PartitionKey is the parent DNS zone.
	coreapi.CosmosMetadata `json:"cosmosMetadata"`
	// BaseDomain is the allocated slug plus its public parent zone.
	// Written by: ClusterDNSAllocation, ClusterDNSBackfill
	BaseDomain string `json:"baseDomain"`
	// Owner is the ARO cluster UID, distinguishing incarnations at the same ARM ID.
	// Written by: ClusterDNSAllocation, ClusterDNSBackfill
	Owner string `json:"owner"`
	// CreatedAt is when this reservation was created, including legacy backfills.
	// Cleanup uses its age to tolerate skew between the cluster and claim caches.
	// Written by: ClusterDNSAllocation, ClusterDNSBackfill
	CreatedAt metav1.Time `json:"createdAt"`
	// ClusterResourceID locates the owning cluster for orphan cleanup; Owner
	// distinguishes different incarnations created at the same ARM resource ID.
	// Written by: ClusterDNSAllocation, ClusterDNSBackfill
	ClusterResourceID string `json:"clusterResourceID"`
}
