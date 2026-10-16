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

package operations

import (
	"context"
	"fmt"
	"strings"

	corev1 "k8s.io/api/core/v1"

	"github.com/openshift/hypershift/api/hypershift/v1beta1"

	"github.com/Azure/ARO-HCP/backend/pkg/kubeapplierhelpers"
	operationbase "github.com/Azure/ARO-HCP/backend/pkg/utils/operationutils"
	"github.com/Azure/ARO-HCP/internal/api/coreapi"
	"github.com/Azure/ARO-HCP/internal/utils"
)

// Node pool create operation state calculation for the node pool create operation controller.

// hypershiftNodePoolOperationState reports node pool create progress by comparing the Hypershift
// NodePool mirrored from the management cluster against desired configuration. It only ever returns
// ProvisioningStateProvisioning or ProvisioningStateSucceeded, never ProvisioningStateFailed; deciding
// the create operation failed remains the job of Cluster Service's own status or the
// CreateOperationCompletionDeadline timeout.
func (c *operationNodePoolCreate) hypershiftNodePoolOperationState(ctx context.Context, nodePool *coreapi.NodePool) (*operationbase.OperationState, error) {
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
		return operationbase.NewOperationState(coreapi.ProvisioningStateProvisioning, "hypershift node pool not cached yet"), nil
	}

	if matches, message := c.hypershiftNodePoolSpecMatchesDesired(nodePool, hypershiftNodePool.Spec); !matches {
		return operationbase.NewOperationState(coreapi.ProvisioningStateProvisioning, message), nil
	}

	if matches, message := c.hypershiftNodePoolStatusMatchesDesired(nodePool, hypershiftNodePool.Status); !matches {
		return operationbase.NewOperationState(coreapi.ProvisioningStateProvisioning, message), nil
	}

	return operationbase.NewOperationState(coreapi.ProvisioningStateSucceeded, ""), nil
}

// hypershiftNodePoolSpecMatchesDesired reports whether Hypershift NodePool .Spec fields match desired
// configuration. Returns false and a diagnostic message when any leaf check fails.
//
// TODO: Improvement | add a node drain timeout spec check here
func (c *operationNodePoolCreate) hypershiftNodePoolSpecMatchesDesired(nodePool *coreapi.NodePool, observed v1beta1.NodePoolSpec) (bool, string) {
	if matches, message := c.hypershiftNodePoolLabelsSpecMatchesDesired(nodePool.Properties.Labels, observed.NodeLabels); !matches {
		return false, message
	}
	if matches, message := c.hypershiftNodePoolReplicasOrAutoscalingSpecMatchesDesired(nodePool, observed); !matches {
		return false, message
	}
	if matches, message := c.hypershiftNodePoolTaintsSpecMatchesDesired(nodePool.Properties.Taints, observed.Taints); !matches {
		return false, message
	}
	return true, ""
}

// hypershiftNodePoolLabelsSpecMatchesDesired reports whether Hypershift NodePool nodeLabels spec
// reflects desired state's labels.
func (c *operationNodePoolCreate) hypershiftNodePoolLabelsSpecMatchesDesired(desired map[string]string, observed map[string]string) (bool, string) {
	for k, v := range desired {
		if observed[k] != v {
			return false, fmt.Sprintf("hypershift NodePool nodeLabels are %v, want at least %v", observed, desired)
		}
	}
	return true, ""
}

// hypershiftNodePoolReplicasOrAutoscalingSpecMatchesDesired reports whether Hypershift NodePool
// replicas or autoscaling spec matches desired state.
func (c *operationNodePoolCreate) hypershiftNodePoolReplicasOrAutoscalingSpecMatchesDesired(desired *coreapi.NodePool, observed v1beta1.NodePoolSpec) (bool, string) {
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
func (c *operationNodePoolCreate) hypershiftNodePoolTaintsSpecMatchesDesired(desired []coreapi.Taint, observed []v1beta1.Taint) (bool, string) {
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

// hypershiftNodePoolStatusMatchesDesired reports whether Hypershift NodePool .Status fields match
// desired state. All applicable sub-checks (replicas, AllNodesHealthy, AllMachinesReady) are
// evaluated and every failing one is included in the diagnostic message.
func (c *operationNodePoolCreate) hypershiftNodePoolStatusMatchesDesired(nodePool *coreapi.NodePool, observed v1beta1.NodePoolStatus) (bool, string) {
	var messages []string

	if matches, message := c.hypershiftNodePoolStatusReplicasMatchesDesired(nodePool, observed.Replicas); !matches {
		messages = append(messages, message)
	}

	// Skip machine health condition checks when scaling to zero -- there are no machines to be ready.
	if nodePool.Properties.Replicas > 0 || nodePool.Properties.AutoScaling != nil {
		if matches, message := c.hypershiftNodePoolConditionStatusMatchesDesired(observed.Conditions, v1beta1.NodePoolAllNodesHealthyConditionType); !matches {
			messages = append(messages, message)
		}
		if matches, message := c.hypershiftNodePoolConditionStatusMatchesDesired(observed.Conditions, v1beta1.NodePoolAllMachinesReadyConditionType); !matches {
			messages = append(messages, message)
		}
	}

	if len(messages) > 0 {
		return false, strings.Join(messages, "; ")
	}
	return true, ""
}

// hypershiftNodePoolStatusReplicasMatchesDesired reports whether Hypershift NodePool status replicas
// match desired state's replicas or autoscaling bounds.
func (c *operationNodePoolCreate) hypershiftNodePoolStatusReplicasMatchesDesired(desired *coreapi.NodePool, observedReplicas int32) (bool, string) {
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

// hypershiftNodePoolConditionStatusMatchesDesired reports whether the named Hypershift NodePool
// condition is reporting a healthy (ConditionTrue) status.
func (c *operationNodePoolCreate) hypershiftNodePoolConditionStatusMatchesDesired(conditions []v1beta1.NodePoolCondition, conditionType string) (bool, string) {
	for _, condition := range conditions {
		if condition.Type != conditionType {
			continue
		}
		if condition.Status != corev1.ConditionTrue {
			return false, fmt.Sprintf("node pool condition %s is %s: %s", condition.Type, condition.Status, condition.Message)
		}
		return true, ""
	}
	return false, fmt.Sprintf("node pool condition %s not yet reported", conditionType)
}
