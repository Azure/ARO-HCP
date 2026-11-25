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

package admission

import (
	"context"
	"errors"

	"github.com/Azure/ARO-HCP/arohcp-cosmos-client/api/coreapi"
	"github.com/Azure/ARO-HCP/internal/utils"
)

// e2eMinimalSwiftCapacityExhaustedMessage is sent to the customer verbatim.
const e2eMinimalSwiftCapacityExhaustedMessage = "capacity for minimal clusters with VNet integration is currently exhausted; retry later"

// ClusterCapacityAdmissionContext carries the server-side state that cluster
// capacity admission needs beyond the cluster object itself.
type ClusterCapacityAdmissionContext struct {
	// Clusters lists the cluster documents of all subscriptions. The frontend
	// is a regional service, so they are all in its region. It is only
	// populated when a limit is set.
	Clusters []*coreapi.Cluster
	// MaxE2EMinimalSwiftClusters limits the SWIFT-enabled minimal clusters.
	// Zero means no limit.
	MaxE2EMinimalSwiftClusters int
}

// AdmitClusterCapacity decides whether the service has capacity for a new
// cluster. It runs on cluster creation only, after validation and admission
// have passed.
//
// The checks are best-effort: they work on the snapshot in the admission
// context and reserve nothing, so concurrent creates can exceed a limit.
//
// A rejection is returned as an error describing the exhausted capacity, and
// the caller turns it into the response. The error message is sent to the
// customer verbatim, so it must be written for them and must not contain
// internal details. The function therefore only decides on the admission
// context: work that can fail belongs where the context is built.
//
// Checks:
//   - e2e_minimal SWIFT clusters: a SWIFT-enabled cluster with minimal control
//     plane pod sizing, which maps to the e2e_minimal cluster sizing
//     configuration, is rejected once MaxE2EMinimalSwiftClusters such clusters
//     exist. Every matching cluster counts, including failed, provisioning and
//     deleting ones. Other clusters are not limited. A cluster that only
//     becomes such a cluster through an update is not checked, but counts from
//     then on.
func AdmitClusterCapacity(ctx context.Context, admissionContext *ClusterCapacityAdmissionContext, newCluster *coreapi.Cluster) error {
	if admissionContext.MaxE2EMinimalSwiftClusters == 0 ||
		newCluster.ServiceProviderProperties.ExperimentalFeatures.ControlPlanePodSizing != coreapi.MinimalControlPlanePodSizing ||
		newCluster.CustomerProperties.Platform.VnetIntegrationSubnetID == nil {
		return nil
	}

	count := 0
	for _, cluster := range admissionContext.Clusters {
		if cluster.ServiceProviderProperties.ExperimentalFeatures.ControlPlanePodSizing == coreapi.MinimalControlPlanePodSizing &&
			cluster.CustomerProperties.Platform.VnetIntegrationSubnetID != nil {
			count++
		}
	}
	if count >= admissionContext.MaxE2EMinimalSwiftClusters {
		utils.LoggerFromContext(ctx).Info("rejecting cluster creation: e2e_minimal SWIFT cluster limit reached", "count", count, "limit", admissionContext.MaxE2EMinimalSwiftClusters)
		return errors.New(e2eMinimalSwiftCapacityExhaustedMessage)
	}
	return nil
}
