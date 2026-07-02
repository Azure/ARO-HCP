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

package operationcontrollers

import (
	"context"
	"fmt"
	"time"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	arohcpv1alpha1 "github.com/openshift-online/ocm-sdk-go/arohcp/v1alpha1"
	"github.com/openshift/hypershift/api/hypershift/v1beta1"

	"github.com/Azure/ARO-HCP/backend/pkg/maestrohelpers"
	"github.com/Azure/ARO-HCP/internal/api"
	"github.com/Azure/ARO-HCP/internal/api/arm"
	"github.com/Azure/ARO-HCP/internal/ocm"
	"github.com/Azure/ARO-HCP/internal/utils"
)

// hypershiftNodePoolOperationState checks that the Hypershift NodePool spec and status
// on the management cluster matches the desired configuration from Cosmos.
func (c *operationNodePoolUpdate) hypershiftNodePoolOperationState(ctx context.Context, nodePool *api.HCPOpenShiftClusterNodePool) (*operationState, error) {
	logger := utils.LoggerFromContext(ctx)

	csNodePool, err := c.clusterServiceClient.GetNodePool(ctx, *nodePool.ServiceProviderProperties.ClusterServiceID)
	if err != nil {
		return nil, utils.TrackError(fmt.Errorf("failed to get node pool from Cluster Service: %w", err))
	}

	hsNodePool, err := maestrohelpers.GetCachedNodePoolForNodePool(
		ctx,
		c.readDesireLister,
		nodePool.ID.SubscriptionID,
		nodePool.ID.ResourceGroupName,
		nodePool.ID.Parent.Name,
		nodePool.ID.Name,
	)
	if err != nil {
		return nil, utils.TrackError(err)
	}
	if hsNodePool == nil {
		return newOperationState(arm.ProvisioningStateUpdating, "Hypershift NodePool has not been observed yet"), nil
	}

	if matches, message := c.hypershiftNodePoolSpecMatchesCosmosDesired(nodePool, csNodePool, hsNodePool); !matches {
		logger.Info("hypershift NodePool spec does not match cosmos desired configuration", "message", message)
		return newOperationState(arm.ProvisioningStateUpdating, message), nil
	}

	if matches, message := hypershiftNodePoolStatusMatchesCosmosDesired(nodePool, hsNodePool.Status); !matches {
		logger.Info("hypershift NodePool status does not match cosmos desired configuration", "message", message)
		return newOperationState(arm.ProvisioningStateUpdating, message), nil
	}

	return newOperationState(arm.ProvisioningStateSucceeded, ""), nil
}

func (c *operationNodePoolUpdate) hypershiftNodePoolSpecMatchesCosmosDesired(desired *api.HCPOpenShiftClusterNodePool, csNodePool *arohcpv1alpha1.NodePool, observed *v1beta1.NodePool) (bool, string) {
	if matches, message := hypershiftLabelsMatchDesired(desired.Properties.Labels, observed.Spec.NodeLabels); !matches {
		return false, message
	}
	if matches, message := hypershiftReplicasOrAutoscalingMatchDesired(desired, observed.Spec); !matches {
		return false, message
	}
	if matches, message := hypershiftTaintsMatchDesired(desired.Properties.Taints, observed.Spec.Taints); !matches {
		return false, message
	}
	if matches, message := hypershiftNodeDrainTimeoutMatchDesired(desired, csNodePool, observed.Spec.NodeDrainTimeout); !matches {
		return false, message
	}
	return true, ""
}

// TODO: This is a subset check -- it verifies desired labels are present but
// cannot detect label removals, because Spec.NodeLabels may contain labels
// managed by other controllers that we should not interfere with.
func hypershiftLabelsMatchDesired(desired map[string]string, observed map[string]string) (bool, string) {
	for k, v := range desired {
		if observed[k] != v {
			return false, fmt.Sprintf("hypershift NodePool nodeLabels are %v, want at least %v", observed, desired)
		}
	}
	return true, ""
}

func hypershiftReplicasOrAutoscalingMatchDesired(desired *api.HCPOpenShiftClusterNodePool, observed v1beta1.NodePoolSpec) (bool, string) {
	if desired.Properties.AutoScaling != nil {
		if observed.AutoScaling == nil {
			return false, fmt.Sprintf("hypershift NodePool autoscaling is unset, want min=%d max=%d", desired.Properties.AutoScaling.Min, desired.Properties.AutoScaling.Max)
		}
		observedMin := int32(0)
		if observed.AutoScaling.Min != nil {
			observedMin = *observed.AutoScaling.Min
		}
		observedMax := observed.AutoScaling.Max
		if observedMin != desired.Properties.AutoScaling.Min || observedMax != desired.Properties.AutoScaling.Max {
			return false, fmt.Sprintf("hypershift NodePool autoscaling is min=%d max=%d, want min=%d max=%d", observedMin, observedMax, desired.Properties.AutoScaling.Min, desired.Properties.AutoScaling.Max)
		}
		return true, ""
	}

	if observed.AutoScaling != nil {
		observedMin := int32(0)
		if observed.AutoScaling.Min != nil {
			observedMin = *observed.AutoScaling.Min
		}
		return false, fmt.Sprintf("hypershift NodePool autoscaling is set (min=%d max=%d), want replicas=%d", observedMin, observed.AutoScaling.Max, desired.Properties.Replicas)
	}

	observedReplicas := int32(0)
	if observed.Replicas != nil {
		observedReplicas = *observed.Replicas
	}
	if observedReplicas != desired.Properties.Replicas {
		return false, fmt.Sprintf("hypershift NodePool replicas is %d, want %d", observedReplicas, desired.Properties.Replicas)
	}
	return true, ""
}

// TODO: This is a subset check -- it verifies desired taints are present but
// cannot detect taint removals, because Spec.Taints may contain taints
// managed by other controllers that we should not interfere with.
func hypershiftTaintsMatchDesired(desired []api.Taint, observed []v1beta1.Taint) (bool, string) {
	for _, want := range desired {
		found := false
		for _, got := range observed {
			if got.Key == want.Key && got.Value == want.Value && string(got.Effect) == string(want.Effect) {
				found = true
				break
			}
		}
		if !found {
			return false, fmt.Sprintf("hypershift NodePool missing desired taint {effect:%s key:%s value:%s}", want.Effect, want.Key, want.Value)
		}
	}
	return true, ""
}

func hypershiftNodeDrainTimeoutMatchDesired(desired *api.HCPOpenShiftClusterNodePool, csNodePool *arohcpv1alpha1.NodePool, observed *metav1.Duration) (bool, string) {
	// If the desired node drain timeout is nil it means that the node pool drain timeout is inherited from the parent
	// cluster's default that was set at the moment of the node pool creation. In that case, we
	// want to compare the observed node drain timeout with cluster's service returned value. This is because CS does not
	// support PATCH omit/null to clear the node pool drain timeout or re-inherit from a more recent cluster default.
	if desired.Properties.NodeDrainTimeoutMinutes == nil &&
		ocm.NodePoolUpdateDispatchConfigNodeDrainTimeoutFromCS(csNodePool) == nil {
		return false, "Cluster Service node pool node_drain_grace_period is unset, want inherited cluster default"
	}

	effectiveDesiredMinutes := ocm.NodePoolUpdateDispatchConfigEffectiveNodeDrainTimeoutMinutes(desired, csNodePool)
	want := time.Duration(*effectiveDesiredMinutes) * time.Minute
	// Fail closed: once effective minutes are known, require Hypershift to report nodeDrainTimeout to either null
	// or a non null non zero duration value before completing. A non null zero duration value means that something
	// unexpected happened because in ARO-HCP we do not expect the node pool drain timeout to be set to an explicit
	// zero duration because CS current logic should never set it to an explicit zero duration. If that occurs for some
	// reason, we want to prevent the operation from completing as it's an unexpected state.
	if observed != nil && observed.Duration == 0 {
		return false, fmt.Sprintf("unexpected hypershift NodePool nodeDrainTimeout set to an explicit zero duration, want %s", want)
	}
	if want == 0 {
		if observed != nil {
			return false, fmt.Sprintf("hypershift NodePool nodeDrainTimeout is %s, want unset", observed.Duration)
		}
		return true, ""
	}
	if observed == nil {
		return false, fmt.Sprintf("hypershift NodePool nodeDrainTimeout is unset, want %s", want)
	}
	if observed.Duration != want {
		return false, fmt.Sprintf("hypershift NodePool nodeDrainTimeout is %s, want %s", observed.Duration, want)
	}
	return true, ""
}

func hypershiftNodePoolStatusMatchesCosmosDesired(desired *api.HCPOpenShiftClusterNodePool, observed v1beta1.NodePoolStatus) (bool, string) {
	if matches, message := hypershiftStatusReplicasMatchDesired(desired, observed.Replicas); !matches {
		return false, message
	}
	// Skip AllMachinesReady check when scaling to zero -- there are no machines to be ready.
	if desired.Properties.Replicas > 0 || desired.Properties.AutoScaling != nil {
		if matches, message := hypershiftAllMachinesReadyConditionMatch(observed.Conditions); !matches {
			return false, message
		}
	}
	return true, ""
}

func hypershiftAllMachinesReadyConditionMatch(conditions []v1beta1.NodePoolCondition) (bool, string) {
	for _, c := range conditions {
		if c.Type == v1beta1.NodePoolAllMachinesReadyConditionType {
			if c.Status != corev1.ConditionTrue {
				return false, fmt.Sprintf("hypershift NodePool condition %s is %s: %s", c.Type, c.Status, c.Message)
			}
			return true, ""
		}
	}
	return false, fmt.Sprintf("hypershift NodePool condition %s not yet reported", v1beta1.NodePoolAllMachinesReadyConditionType)
}

func hypershiftStatusReplicasMatchDesired(desired *api.HCPOpenShiftClusterNodePool, observedReplicas int32) (bool, string) {
	if desired.Properties.AutoScaling != nil {
		if observedReplicas < desired.Properties.AutoScaling.Min {
			return false, fmt.Sprintf("hypershift NodePool status replicas is %d, want >= %d (autoscaling min)", observedReplicas, desired.Properties.AutoScaling.Min)
		}
		if observedReplicas > desired.Properties.AutoScaling.Max {
			return false, fmt.Sprintf("hypershift NodePool status replicas is %d, want <= %d (autoscaling max)", observedReplicas, desired.Properties.AutoScaling.Max)
		}
		return true, ""
	}

	if observedReplicas != desired.Properties.Replicas {
		return false, fmt.Sprintf("hypershift NodePool status replicas is %d, want %d", observedReplicas, desired.Properties.Replicas)
	}
	return true, ""
}
