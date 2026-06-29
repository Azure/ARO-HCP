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

package maestrohelpers

import (
	"context"
	"fmt"
	"strings"

	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/json"

	"github.com/openshift/hypershift/api/hypershift/v1beta1"

	"github.com/Azure/ARO-HCP/internal/api"
	"github.com/Azure/ARO-HCP/internal/database"
	dblisters "github.com/Azure/ARO-HCP/internal/database/listers"
	"github.com/Azure/ARO-HCP/internal/utils"
)

// ReadDesireNameReadonlyControlPlaneClusterAutoscaler is the well-known ReadDesire name the
// backend writes the per-cluster cluster-autoscaler ControlPlaneComponent mirror under.
var ReadDesireNameReadonlyControlPlaneClusterAutoscaler = strings.ToLower(string(api.MaestroBundleInternalNameReadonlyControlPlaneClusterAutoscaler))

// GetCachedControlPlaneClusterAutoscalerForCluster reads the cluster-autoscaler
// ControlPlaneComponent mirror from the per-cluster ReadDesire.
func GetCachedControlPlaneClusterAutoscalerForCluster(
	ctx context.Context,
	readDesireLister dblisters.ReadDesireLister,
	subscriptionName, resourceGroupName, clusterName string,
) (*v1beta1.ControlPlaneComponent, error) {
	readDesire, err := readDesireLister.GetForCluster(ctx, subscriptionName, resourceGroupName, clusterName, ReadDesireNameReadonlyControlPlaneClusterAutoscaler)
	if database.IsNotFoundError(err) {
		return nil, nil
	}
	if err != nil {
		return nil, utils.TrackError(fmt.Errorf("failed to get ReadDesire for cluster-autoscaler ControlPlaneComponent: %w", err))
	}
	if readDesire.Status.KubeContent == nil || len(readDesire.Status.KubeContent.Raw) == 0 {
		return nil, nil
	}
	cpc := &v1beta1.ControlPlaneComponent{}
	if err := json.Unmarshal(readDesire.Status.KubeContent.Raw, cpc); err != nil {
		return nil, utils.TrackError(fmt.Errorf("failed to unmarshal ControlPlaneComponent from ReadDesire kubeContent: %w", err))
	}
	return cpc, nil
}

// IsControlPlaneClusterAutoscalerReady reports whether the cluster-autoscaler
// ControlPlaneComponent has completed rollout and is available.
func IsControlPlaneClusterAutoscalerReady(cpc *v1beta1.ControlPlaneComponent) bool {
	if cpc == nil {
		return false
	}
	return meta.IsStatusConditionTrue(cpc.Status.Conditions, string(v1beta1.ControlPlaneComponentAvailable)) &&
		meta.IsStatusConditionTrue(cpc.Status.Conditions, string(v1beta1.ControlPlaneComponentRolloutComplete))
}

// ControlPlaneClusterAutoscalerNotReadyMessage returns a human-readable reason when
// the cluster-autoscaler ControlPlaneComponent is not ready.
func ControlPlaneClusterAutoscalerNotReadyMessage(cpc *v1beta1.ControlPlaneComponent) string {
	if cpc == nil {
		return "cluster autoscaler ControlPlaneComponent is absent"
	}
	available := meta.FindStatusCondition(cpc.Status.Conditions, string(v1beta1.ControlPlaneComponentAvailable))
	rollout := meta.FindStatusCondition(cpc.Status.Conditions, string(v1beta1.ControlPlaneComponentRolloutComplete))
	if available == nil || available.Status != metav1.ConditionTrue {
		if available != nil && len(available.Message) > 0 {
			return fmt.Sprintf("cluster autoscaler not available: %s: %s", available.Reason, available.Message)
		}
		return "cluster autoscaler not available"
	}
	if rollout == nil || rollout.Status != metav1.ConditionTrue {
		if rollout != nil && len(rollout.Message) > 0 {
			return fmt.Sprintf("cluster autoscaler rollout not complete: %s: %s", rollout.Reason, rollout.Message)
		}
		return "cluster autoscaler rollout not complete"
	}
	return "cluster autoscaler not ready"
}
