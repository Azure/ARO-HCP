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
	"maps"
	"slices"
	"time"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	arohcpv1alpha1 "github.com/openshift-online/ocm-sdk-go/arohcp/v1alpha1"
	"github.com/openshift/hypershift/api/hypershift/v1beta1"

	"github.com/Azure/ARO-HCP/backend/pkg/kubeapplierhelpers"
	"github.com/Azure/ARO-HCP/internal/api"
	"github.com/Azure/ARO-HCP/internal/api/arm"
	"github.com/Azure/ARO-HCP/internal/ocm"
	"github.com/Azure/ARO-HCP/internal/utils"
)

// Node pool update operation state calculation for the node pool update operation controller.

// hypershiftNodePoolOperationState contains the node pool update operation state calculation comparing desired state
// against Hypershift's NodePool in the management cluster.
func (c *operationNodePoolUpdate) hypershiftNodePoolOperationState(ctx context.Context, nodePool *api.HCPOpenShiftClusterNodePool, csNodePool *arohcpv1alpha1.NodePool) (*operationState, error) {
	logger := utils.LoggerFromContext(ctx)

	hypershiftNodePool, err := kubeapplierhelpers.GetCachedNodePoolForNodePool(
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
	if hypershiftNodePool == nil {
		return newOperationState(arm.ProvisioningStateUpdating, "Hypershift NodePool has not been observed yet"), nil
	}

	if matches, message := c.hypershiftNodePoolSpecMatchesDesired(nodePool, csNodePool, hypershiftNodePool); !matches {
		logger.Info("hypershift NodePool spec does not match desired configuration", "message", message)
		return newOperationState(arm.ProvisioningStateUpdating, message), nil
	}

	if matches, message := c.hypershiftNodePoolStatusMatchesDesired(nodePool, hypershiftNodePool.Status); !matches {
		logger.Info("hypershift NodePool status does not match desired configuration", "message", message)
		return newOperationState(arm.ProvisioningStateUpdating, message), nil
	}

	return newOperationState(arm.ProvisioningStateSucceeded, ""), nil
}

// hypershiftNodePoolSpecMatchesDesired reports whether Hypershift NodePool .Spec fields
// and other non status configuration matches desired state. Returns false and a diagnostic message
// when any leaf check fails. NodePool .status is not checked here.
func (c *operationNodePoolUpdate) hypershiftNodePoolSpecMatchesDesired(nodePool *api.HCPOpenShiftClusterNodePool, csNodePool *arohcpv1alpha1.NodePool, hypershiftNodePool *v1beta1.NodePool) (bool, string) {
	if matches, message := c.hypershiftNodePoolLabelsSpecMatchesDesired(nodePool.Properties.Labels, hypershiftNodePool.Spec.NodeLabels); !matches {
		return false, message
	}
	if matches, message := c.hypershiftNodePoolReplicasOrAutoscalingSpecMatchesDesired(nodePool, hypershiftNodePool.Spec); !matches {
		return false, message
	}
	if matches, message := c.hypershiftNodePoolTaintsSpecMatchesDesired(nodePool.Properties.Taints, hypershiftNodePool.Spec.Taints); !matches {
		return false, message
	}
	if matches, message := c.hypershiftNodePoolNodeDrainTimeoutSpecMatchesDesired(nodePool, csNodePool, hypershiftNodePool.Spec.NodeDrainTimeout); !matches {
		return false, message
	}
	return true, ""
}

// hypershiftNodePoolLabelsSpecMatchesDesired reports whether Hypershift NodePool nodeLabels spec
// reflects desired state's labels.
//
// Note: For now this check is incomplete: it can confirm additions but cannot
// detect removal of a previously configured label, because Spec.NodeLabels may
// contain labels managed by other controllers that we should not interfere with.
// clusterServiceNodePoolSpecMatchesDesired partially compensates with an exact
// Cluster Service labels check.
func (c *operationNodePoolUpdate) hypershiftNodePoolLabelsSpecMatchesDesired(desired map[string]string, observed map[string]string) (bool, string) {
	for k, v := range desired {
		if observed[k] != v {
			return false, fmt.Sprintf("hypershift NodePool nodeLabels are %v, want at least %v", observed, desired)
		}
	}
	return true, ""
}

// hypershiftNodePoolReplicasOrAutoscalingSpecMatchesDesired reports whether Hypershift NodePool
// replicas or autoscaling spec matches desired state.
func (c *operationNodePoolUpdate) hypershiftNodePoolReplicasOrAutoscalingSpecMatchesDesired(desired *api.HCPOpenShiftClusterNodePool, observed v1beta1.NodePoolSpec) (bool, string) {
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

// hypershiftNodePoolTaintsSpecMatchesDesired reports whether Hypershift NodePool taints spec
// reflects desired state's taints.
//
// Note: For now this check is incomplete: it can confirm additions but cannot
// detect removal of a previously configured taint, because Spec.Taints may
// contain taints managed by other controllers that we should not interfere with.
// clusterServiceNodePoolSpecMatchesDesired partially compensates with an exact
// Cluster Service taints check.
func (c *operationNodePoolUpdate) hypershiftNodePoolTaintsSpecMatchesDesired(desired []api.Taint, observed []v1beta1.Taint) (bool, string) {
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

// hypershiftNodePoolNodeDrainTimeoutSpecMatchesDesired reports whether Hypershift NodePool
// nodeDrainTimeout spec matches desired state's nodeDrainTimeoutMinutes.
func (c *operationNodePoolUpdate) hypershiftNodePoolNodeDrainTimeoutSpecMatchesDesired(desired *api.HCPOpenShiftClusterNodePool, csNodePool *arohcpv1alpha1.NodePool, observed *metav1.Duration) (bool, string) {
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

// hypershiftNodePoolStatusMatchesDesired reports whether Hypershift NodePool .Status fields match desired state.
func (c *operationNodePoolUpdate) hypershiftNodePoolStatusMatchesDesired(nodePool *api.HCPOpenShiftClusterNodePool, observed v1beta1.NodePoolStatus) (bool, string) {
	if matches, message := c.hypershiftNodePoolStatusReplicasMatchesDesired(nodePool, observed.Replicas); !matches {
		return false, message
	}
	// Skip AllMachinesReady check when scaling to zero -- there are no machines to be ready.
	if nodePool.Properties.Replicas > 0 || nodePool.Properties.AutoScaling != nil {
		if matches, message := c.hypershiftNodePoolAllMachinesReadyConditionStatusMatchesDesired(observed.Conditions); !matches {
			return false, message
		}
	}
	return true, ""
}

// hypershiftNodePoolAllMachinesReadyConditionStatusMatchesDesired reports whether Hypershift NodePool
// AllMachinesReady condition status reflects that all machines are ready.
func (c *operationNodePoolUpdate) hypershiftNodePoolAllMachinesReadyConditionStatusMatchesDesired(conditions []v1beta1.NodePoolCondition) (bool, string) {
	for _, condition := range conditions {
		if condition.Type == v1beta1.NodePoolAllMachinesReadyConditionType {
			if condition.Status != corev1.ConditionTrue {
				return false, fmt.Sprintf("hypershift NodePool condition %s is %s: %s", condition.Type, condition.Status, condition.Message)
			}
			return true, ""
		}
	}
	return false, fmt.Sprintf("hypershift NodePool condition %s not yet reported", v1beta1.NodePoolAllMachinesReadyConditionType)
}

// hypershiftNodePoolStatusReplicasMatchesDesired reports whether Hypershift NodePool status replicas
// match desired state's replicas or autoscaling bounds.
func (c *operationNodePoolUpdate) hypershiftNodePoolStatusReplicasMatchesDesired(desired *api.HCPOpenShiftClusterNodePool, observedReplicas int32) (bool, string) {
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

// clusterServiceNodePoolSpecOperationState reports whether Cluster Service node pool spec fields
// match desired state intent for the node pool update operation. Only checks outside CS .status.
// Labels and taints are checked here because Hypershift subset checks cannot detect removals.
// Add checks against the management cluster state when possible instead of here, to reduce the
// number of checks against Cluster Service, as CS will be removed in the future.
func (c *operationNodePoolUpdate) clusterServiceNodePoolSpecOperationState(nodePool *api.HCPOpenShiftClusterNodePool, csNodePool *arohcpv1alpha1.NodePool) (*operationState, error) {
	if matches, message := c.clusterServiceNodePoolSpecMatchesDesired(nodePool, csNodePool); !matches {
		return newOperationState(arm.ProvisioningStateUpdating, message), nil
	}
	return newOperationState(arm.ProvisioningStateSucceeded, ""), nil
}

// clusterServiceNodePoolSpecMatchesDesired reports whether Cluster Service node pool spec fields
// relevant to the node pool update operation match desired state. Returns false and a diagnostic
// message when any leaf check fails.
func (c *operationNodePoolUpdate) clusterServiceNodePoolSpecMatchesDesired(nodePool *api.HCPOpenShiftClusterNodePool, csNodePool *arohcpv1alpha1.NodePool) (bool, string) {
	if matches, message := c.clusterServiceNodePoolLabelsSpecMatchesDesired(nodePool.Properties.Labels, csNodePool); !matches {
		return false, message
	}
	if matches, message := c.clusterServiceNodePoolTaintsSpecMatchesDesired(nodePool.Properties.Taints, csNodePool); !matches {
		return false, message
	}
	return true, ""
}

// clusterServiceNodePoolLabelsSpecMatchesDesired reports whether Cluster Service node pool
// labels exactly match RP desired labels.
func (c *operationNodePoolUpdate) clusterServiceNodePoolLabelsSpecMatchesDesired(desired map[string]string, csNodePool *arohcpv1alpha1.NodePool) (bool, string) {
	observed := ocm.NodePoolUpdateDispatchConfigLabelsFromCS(csNodePool)
	if !maps.Equal(desired, observed) {
		return false, fmt.Sprintf("Cluster Service node pool labels are %v, want %v", observed, desired)
	}
	return true, ""
}

// clusterServiceNodePoolTaintsSpecMatchesDesired reports whether Cluster Service node pool
// taints exactly match RP desired taints.
func (c *operationNodePoolUpdate) clusterServiceNodePoolTaintsSpecMatchesDesired(desired []api.Taint, csNodePool *arohcpv1alpha1.NodePool) (bool, string) {
	desiredTaints := ocm.NodePoolUpdateDispatchConfigTaintsFromRP(desired)
	observedTaints := ocm.NodePoolUpdateDispatchConfigTaintsFromCS(csNodePool)
	if !slices.Equal(desiredTaints, observedTaints) {
		return false, fmt.Sprintf("Cluster Service node pool taints are %v, want %v", observedTaints, desiredTaints)
	}
	return true, ""
}
