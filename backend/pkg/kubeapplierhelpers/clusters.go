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
	"context"
	"fmt"
	"strings"

	"github.com/google/uuid"

	"k8s.io/apimachinery/pkg/api/meta"
	"k8s.io/apimachinery/pkg/util/json"

	"github.com/openshift/hypershift/api/hypershift/v1beta1"

	"github.com/Azure/ARO-HCP/internal/api/coreapi"
	"github.com/Azure/ARO-HCP/internal/api/kubeapplierapi"
	"github.com/Azure/ARO-HCP/internal/database/cosmosstorage/cosmosstorageutils"
	"github.com/Azure/ARO-HCP/internal/database/listers/kubeapplierlisters"
	"github.com/Azure/ARO-HCP/internal/utils"
)

// ReadDesireNameReadonlyHostedCluster is the well-known ReadDesire name the
// backend writes the per-cluster HostedCluster mirror under. Consumers look
// this up via a ReadDesireLister.GetForCluster call.
//
// The value is the lowercased form of
// coreapi.MaestroBundleInternalNameReadonlyHypershiftHostedCluster — the same
// derivation the writer (create_cluster_scoped_read_desires_controller.go)
// uses for its desired ReadDesire name. Lowercased so the resourceID path
// reduces to a stable Cosmos key regardless of case.
var ReadDesireNameReadonlyHostedCluster = strings.ToLower(string(coreapi.MaestroBundleInternalNameReadonlyHypershiftHostedCluster))

// HostedClusterObservation distinguishes an unavailable ReadDesire from a
// successful observation that found no HostedCluster.
type HostedClusterObservation struct {
	HostedCluster *v1beta1.HostedCluster
	Observed      bool
}

// GetHostedClusterObservationForCluster reads the HostedCluster mirror from
// the per-cluster ReadDesire. Observed is false when the ReadDesire is absent,
// has not completed successfully, or contains no current observation. An
// observed ReadDesire with empty KubeContent has Observed=true and a nil
// HostedCluster: the management cluster was successfully checked and the
// HostedCluster is absent.
func GetHostedClusterObservationForCluster(
	ctx context.Context,
	readDesireLister kubeapplierlisters.ReadDesireLister,
	subscriptionName, resourceGroupName, clusterName string,
) (HostedClusterObservation, error) {
	readDesire, err := readDesireLister.GetForCluster(ctx, subscriptionName, resourceGroupName, clusterName, ReadDesireNameReadonlyHostedCluster)
	if cosmosstorageutils.IsNotFoundError(err) {
		return HostedClusterObservation{}, nil
	}
	if err != nil {
		return HostedClusterObservation{}, utils.TrackError(fmt.Errorf("failed to get ReadDesire for HostedCluster: %w", err))
	}
	if !meta.IsStatusConditionTrue(readDesire.Status.Conditions, kubeapplierapi.ConditionTypeSuccessful) {
		return HostedClusterObservation{}, nil
	}
	if readDesire.Status.KubeContent == nil || len(readDesire.Status.KubeContent.Raw) == 0 {
		return HostedClusterObservation{Observed: true}, nil
	}
	hostedCluster := &v1beta1.HostedCluster{}
	if err := json.Unmarshal(readDesire.Status.KubeContent.Raw, hostedCluster); err != nil {
		return HostedClusterObservation{}, utils.TrackError(fmt.Errorf("failed to unmarshal HostedCluster from ReadDesire kubeContent: %w", err))
	}
	return HostedClusterObservation{HostedCluster: hostedCluster, Observed: true}, nil
}

// GetCachedHostedClusterForCluster preserves the existing helper contract for
// callers that only need whatever HostedCluster the kube-applier last recorded.
//
// Returns (nil, nil) when the ReadDesire has not been created yet (NotFound) or
// carries no kubeContent. It deliberately does not inspect the "Successful"
// condition, so a ReadDesire whose latest read failed still yields the content
// from the last read that worked. Callers that must tell "we have not looked"
// apart from "we looked and found nothing" want
// GetHostedClusterObservationForCluster instead.
//
// Do not reimplement this as a wrapper that discards
// GetHostedClusterObservationForCluster's HostedCluster: that helper reports
// nothing at all while the latest read is unsuccessful, which would turn a
// transient read failure into "the HostedCluster is gone" for callers such as
// hostedClusterDeletionStatus, which reads nil as "deletion complete".
//
// Returns a non-nil error only for hard failures: a non-NotFound lister
// error, or unmarshal failure.
func GetCachedHostedClusterForCluster(
	ctx context.Context,
	readDesireLister kubeapplierlisters.ReadDesireLister,
	subscriptionName, resourceGroupName, clusterName string,
) (*v1beta1.HostedCluster, error) {
	readDesire, err := readDesireLister.GetForCluster(ctx, subscriptionName, resourceGroupName, clusterName, ReadDesireNameReadonlyHostedCluster)
	if cosmosstorageutils.IsNotFoundError(err) {
		return nil, nil
	}
	if err != nil {
		return nil, utils.TrackError(fmt.Errorf("failed to get ReadDesire for HostedCluster: %w", err))
	}
	if readDesire.Status.KubeContent == nil || len(readDesire.Status.KubeContent.Raw) == 0 {
		return nil, nil
	}
	hostedCluster := &v1beta1.HostedCluster{}
	if err := json.Unmarshal(readDesire.Status.KubeContent.Raw, hostedCluster); err != nil {
		return nil, utils.TrackError(fmt.Errorf("failed to unmarshal HostedCluster from ReadDesire kubeContent: %w", err))
	}
	return hostedCluster, nil
}

// GetCachedHostedClusterUUIDForCluster resolves the cluster UUID parsed from the cached HostedCluster's
// Spec.ClusterID for the given cluster.
//
// Returns (uuid, true, nil) on success.
//
// Returns (uuid.Nil, false, nil) for transient situations the caller should treat as a silent skip:
// the ReadDesire has not been observed yet, the kubeContent does not yet hold the HostedCluster,
// or the HostedCluster's Spec.ClusterID is empty. The reason is logged via the context logger so
// callers don't need to.
//
// Returns a non-nil error only for hard failures: a non-NotFound lister error, malformed kubecontent,
// or an unparseable UUID.
func GetCachedHostedClusterUUIDForCluster(
	ctx context.Context,
	readDesireLister kubeapplierlisters.ReadDesireLister,
	subscriptionName, resourceGroupName, clusterName string,
) (uuid.UUID, bool, error) {
	logger := utils.LoggerFromContext(ctx)
	hostedCluster, err := GetCachedHostedClusterForCluster(ctx, readDesireLister, subscriptionName, resourceGroupName, clusterName)
	if err != nil {
		return uuid.Nil, false, err
	}
	if hostedCluster == nil {
		logger.Info("hosted cluster not found")
		return uuid.Nil, false, nil
	}
	if len(hostedCluster.Spec.ClusterID) == 0 {
		logger.Info("missing cluster UUID")
		return uuid.Nil, false, nil
	}
	clusterUUID, err := uuid.Parse(hostedCluster.Spec.ClusterID)
	if err != nil {
		return uuid.Nil, false, utils.TrackError(fmt.Errorf("failed to parse cluster UUID: %w", err))
	}
	return clusterUUID, true, nil
}
