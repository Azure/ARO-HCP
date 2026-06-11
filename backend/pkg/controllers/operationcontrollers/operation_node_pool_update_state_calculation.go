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
	"time"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/openshift/hypershift/api/hypershift/v1beta1"

	"github.com/Azure/ARO-HCP/backend/pkg/maestrohelpers"
	"github.com/Azure/ARO-HCP/internal/api"
	"github.com/Azure/ARO-HCP/internal/api/arm"
	"github.com/Azure/ARO-HCP/internal/api/kubeapplier"
	"github.com/Azure/ARO-HCP/internal/database"
	"github.com/Azure/ARO-HCP/internal/utils"
)

func (c *operationNodePoolUpdate) hypershiftNodePoolOperationState(ctx context.Context, nodePool *api.HCPOpenShiftClusterNodePool) (*operationState, error) {
	logger := utils.LoggerFromContext(ctx)

	readDesire, err := c.readDesireLister.GetForNodePool(
		ctx,
		nodePool.ID.SubscriptionID,
		nodePool.ID.ResourceGroupName,
		nodePool.ID.Parent.Name,
		nodePool.ID.Name,
		maestrohelpers.ReadDesireNameReadonlyNodePool,
	)
	if database.IsNotFoundError(err) {
		return newOperationState(arm.ProvisioningStateUpdating, "ReadDesire for NodePool has not been created yet"), nil
	}
	if err != nil {
		return nil, utils.TrackError(err)
	}

	if !meta.IsStatusConditionTrue(readDesire.Status.Conditions, kubeapplier.ConditionTypeSuccessful) {
		message := "ReadDesire has not yet successfully observed the NodePool"
		if successfulCondition := meta.FindStatusCondition(readDesire.Status.Conditions, kubeapplier.ConditionTypeSuccessful); successfulCondition != nil {
			message = fmt.Sprintf("ReadDesire is not successful: %s: %s", successfulCondition.Reason, successfulCondition.Message)
		}
		logger.Info("ReadDesire is not successful", "readDesire.Status.Conditions", readDesire.Status.Conditions)
		return newOperationState(arm.ProvisioningStateUpdating, message), nil
	}

	observedHypershiftNodePool, err := maestrohelpers.NodePoolFromReadDesire(readDesire)
	if err != nil {
		return nil, utils.TrackError(err)
	}
	if observedHypershiftNodePool == nil {
		return newOperationState(arm.ProvisioningStateUpdating, "ReadDesire has no NodePool kube content"), nil
	}

	// TODO should we check this condition? might there be updates on the nodepool on hypershift that are not related
	// to the updates triggered by us?
	if c.conditionIsTrue(observedHypershiftNodePool.Status.Conditions, v1beta1.NodePoolUpdatingConfigConditionType) {
		message := "hypershift NodePool config update in progress"
		if updatingConfig := c.findCondition(observedHypershiftNodePool.Status.Conditions, v1beta1.NodePoolUpdatingConfigConditionType); updatingConfig != nil {
			message = fmt.Sprintf("hypershift NodePool config update in progress: %s: %s", updatingConfig.Reason, updatingConfig.Message)
		}
		logger.Info("hypershift NodePool is updating config", "nodePool.Status.Conditions", observedHypershiftNodePool.Status.Conditions)
		return newOperationState(arm.ProvisioningStateUpdating, message), nil
	}

	// We first check Hypershift's NodePool spec to see if it matches the desired cosmos configuration
	if matches, message := c.hypershiftNodePoolSpecMatchesCosmosDesired(nodePool.Properties, observedHypershiftNodePool.Spec); !matches {
		logger.Info("hypershift NodePool spec does not match cosmos desired configuration", "message", message)
		return newOperationState(arm.ProvisioningStateUpdating, message), nil
	}

	// We then check Hypershift's NodePool status to see if it matches the desired cosmos configuration
	if matches, message := c.hypershiftNodePoolStatusMatchesCosmosDesired(nodePool.Properties, observedHypershiftNodePool.Status); !matches {
		logger.Info("hypershift NodePool status does not match desired configuration", "message", message)
		return newOperationState(arm.ProvisioningStateUpdating, message), nil
	}

	return newOperationState(arm.ProvisioningStateSucceeded, ""), nil
}

// hypershiftNodePoolSpecMatchesCosmosDesired compares Cosmos desired properties against the Hypershift
// NodePool spec fields that are relevant for the update operation.
func (c *operationNodePoolUpdate) hypershiftNodePoolSpecMatchesCosmosDesired(desired api.HCPOpenShiftClusterNodePoolProperties, observed v1beta1.NodePoolSpec) (bool, string) {
	if matches, message := c.scalingSpecMatchesDesired(desired, observed); !matches {
		return false, message
	}
	if matches, message := c.labelsSpecMatchesDesired(desired.Labels, observed.NodeLabels); !matches {
		return false, message
	}
	if matches, message := c.taintsSpecMatchesDesired(desired.Taints, observed.Taints); !matches {
		return false, message
	}
	if matches, message := c.nodeDrainTimeoutSpecMatchesDesired(desired.NodeDrainTimeoutMinutes, observed.NodeDrainTimeout); !matches {
		return false, message
	}
	return true, ""
}

func (c *operationNodePoolUpdate) scalingSpecMatchesDesired(desired api.HCPOpenShiftClusterNodePoolProperties, observed v1beta1.NodePoolSpec) (bool, string) {
	if desired.AutoScaling != nil {
		if observed.AutoScaling == nil {
			return false, "hypershift NodePool has no autoscaling configuration"
		}
		if observed.AutoScaling.Min == nil {
			return false, "hypershift NodePool autoscaling min is unset"
		}
		if *observed.AutoScaling.Min != desired.AutoScaling.Min {
			return false, fmt.Sprintf("hypershift NodePool autoscaling min is %d, want %d", *observed.AutoScaling.Min, desired.AutoScaling.Min)
		}
		if observed.AutoScaling.Max != desired.AutoScaling.Max {
			return false, fmt.Sprintf("hypershift NodePool autoscaling max is %d, want %d", observed.AutoScaling.Max, desired.AutoScaling.Max)
		}
		if observed.Replicas != nil {
			return false, "hypershift NodePool has replicas set while autoscaling is enabled"
		}
		return true, ""
	}

	if observed.AutoScaling != nil {
		return false, "hypershift NodePool still has autoscaling configuration"
	}

	observedReplicas := int32(0)
	if observed.Replicas != nil {
		observedReplicas = *observed.Replicas
	}
	if observedReplicas != desired.Replicas {
		return false, fmt.Sprintf("hypershift NodePool replicas is %d, want %d", observedReplicas, desired.Replicas)
	}
	return true, ""
}

func (c *operationNodePoolUpdate) labelsSpecMatchesDesired(desired map[string]string, observed map[string]string) (bool, string) {
	if len(desired) == 0 && len(observed) == 0 {
		return true, ""
	}
	if !maps.Equal(desired, observed) {
		return false, "hypershift NodePool nodeLabels do not match desired labels"
	}
	return true, ""
}

func (c *operationNodePoolUpdate) taintsSpecMatchesDesired(desired []api.Taint, observed []v1beta1.Taint) (bool, string) {
	if len(desired) == 0 && len(observed) == 0 {
		return true, ""
	}
	if len(desired) != len(observed) {
		return false, fmt.Sprintf("hypershift NodePool has %d taints, want %d", len(observed), len(desired))
	}

	for i := range desired {
		if c.apiTaintToHypershift(desired[i]) != observed[i] {
			return false, "hypershift cluster NodePool taints do not match cosmos desired taints"
		}
	}
	return true, ""
}

func (c *operationNodePoolUpdate) nodeDrainTimeoutSpecMatchesDesired(desiredMinutes *int32, observed *metav1.Duration) (bool, string) {
	if desiredMinutes == nil {
		if observed == nil {
			return true, ""
		}

		return false, fmt.Sprintf("hypershift NodePool nodeDrainTimeout is %s, want unset", observed.Duration)
	}

	want := time.Duration(*desiredMinutes) * time.Minute

	// In Hypershift NodeDrainTimeout is a *metav1.Duration. According to its documentation (as of 2026-06-10):
	// "The default value is 0, meaning that the node can retry drain without any time limitations."
	// We treat Hypershift's side nil as the same as zero because when CS receives a desired value of zero for it on the
	// PATCH call we set the Hypershift side to nil.
	if observed == nil {
		if want == 0 {
			return true, ""
		}
		return false, fmt.Sprintf("hypershift NodePool nodeDrainTimeout is unset, want %s", want)
	}
	if observed.Duration != want {
		return false, fmt.Sprintf("hypershift NodePool nodeDrainTimeout is %s, want %s", observed.Duration, want)
	}
	return true, ""
}

func (c *operationNodePoolUpdate) apiTaintToHypershift(t api.Taint) v1beta1.Taint {
	return v1beta1.Taint{
		Key:    t.Key,
		Value:  t.Value,
		Effect: corev1.TaintEffect(t.Effect),
	}
}

// hypershiftNodePoolStatusMatchesCosmosDesired compares elements of Hypershift's NodePool status against Cosmos desired.
func (c *operationNodePoolUpdate) hypershiftNodePoolStatusMatchesCosmosDesired(desired api.HCPOpenShiftClusterNodePoolProperties, observed v1beta1.NodePoolStatus) (bool, string) {
	observedReplicas := observed.Replicas
	if desired.AutoScaling != nil {
		if observedReplicas < desired.AutoScaling.Min {
			return false, fmt.Sprintf(
				"hypershift NodePool status replicas is %d, want at least %d",
				observedReplicas, desired.AutoScaling.Min,
			)
		}
		if observedReplicas > desired.AutoScaling.Max {
			return false, fmt.Sprintf(
				"hypershift NodePool status replicas is %d, want at most %d",
				observedReplicas, desired.AutoScaling.Max,
			)
		}
		if !c.conditionIsTrue(observed.Conditions, v1beta1.NodePoolAutoscalingEnabledConditionType) {
			message := "hypershift NodePool autoscaling is not enabled"
			if autoscalingCondition := c.findCondition(observed.Conditions, v1beta1.NodePoolAutoscalingEnabledConditionType); autoscalingCondition != nil {
				message = fmt.Sprintf("hypershift NodePool autoscaling is not enabled: %s: %s", autoscalingCondition.Reason, autoscalingCondition.Message)
			}
			return false, message
		}
	} else if observedReplicas != desired.Replicas {
		return false, fmt.Sprintf(
			"hypershift NodePool status replicas is %d, want %d",
			observed.Replicas, desired.Replicas,
		)
	}

	// TODO should we check this condition? might we have the hypershift nodepool not ready because of changes non triggered
	// by us?
	if !c.conditionIsTrue(observed.Conditions, v1beta1.NodePoolReadyConditionType) {
		message := "hypershift NodePool is not ready"
		if readyCondition := c.findCondition(observed.Conditions, v1beta1.NodePoolReadyConditionType); readyCondition != nil {
			message = fmt.Sprintf("hypershift NodePool is not ready: %s: %s", readyCondition.Reason, readyCondition.Message)
		}
		return false, message
	}

	return true, ""
}

func (c *operationNodePoolUpdate) conditionIsTrue(conditions []v1beta1.NodePoolCondition, conditionType string) bool {
	for i := range conditions {
		if conditions[i].Type == conditionType {
			return conditions[i].Status == corev1.ConditionTrue
		}
	}
	return false
}

func (c *operationNodePoolUpdate) findCondition(conditions []v1beta1.NodePoolCondition, conditionType string) *v1beta1.NodePoolCondition {
	for i := range conditions {
		if conditions[i].Type == conditionType {
			return &conditions[i]
		}
	}
	return nil
}
