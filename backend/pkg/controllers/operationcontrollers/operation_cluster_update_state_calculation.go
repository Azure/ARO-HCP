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
	"slices"
	"time"

	"github.com/openshift/hypershift/api/hypershift/v1beta1"

	"github.com/Azure/ARO-HCP/backend/pkg/maestrohelpers"
	"github.com/Azure/ARO-HCP/internal/api"
	"github.com/Azure/ARO-HCP/internal/api/arm"
	"github.com/Azure/ARO-HCP/internal/utils"
)

func (c *operationClusterUpdate) hypershiftClusterOperationState(ctx context.Context, cluster *api.HCPOpenShiftCluster) (*operationState, error) {
	logger := utils.LoggerFromContext(ctx)

	hostedCluster, err := maestrohelpers.GetCachedHostedClusterForCluster(
		ctx,
		c.readDesireLister,
		cluster.ID.SubscriptionID,
		cluster.ID.ResourceGroupName,
		cluster.ID.Name,
	)
	if err != nil {
		return nil, utils.TrackError(err)
	}
	if hostedCluster == nil {
		return newOperationState(arm.ProvisioningStateUpdating, "HostedCluster has not been observed yet"), nil
	}

	if matches, message := c.hypershiftClusterSpecMatchesCosmosDesired(cluster.CustomerProperties, hostedCluster.Spec); !matches {
		logger.Info("hypershift HostedCluster spec does not match cosmos desired configuration", "message", message)
		return newOperationState(arm.ProvisioningStateUpdating, message), nil
	}

	return newOperationState(arm.ProvisioningStateSucceeded, ""), nil
}

// hypershiftClusterSpecMatchesCosmosDesired compares Cosmos desired properties against the Hypershift
// HostedCluster spec fields that are relevant for the update operation.
func (c *operationClusterUpdate) hypershiftClusterSpecMatchesCosmosDesired(desired api.HCPOpenShiftClusterCustomerProperties, observed v1beta1.HostedClusterSpec) (bool, string) {
	if matches, message := c.autoscalingSpecMatchesDesired(desired.Autoscaling, observed.Autoscaling); !matches {
		return false, message
	}
	if matches, message := c.imageContentSourcesMatchDesired(desired.ImageDigestMirrors, observed.ImageContentSources); !matches {
		return false, message
	}
	return true, ""
}

func (c *operationClusterUpdate) autoscalingSpecMatchesDesired(desired api.ClusterAutoscalingProfile, observed v1beta1.ClusterAutoscaling) (bool, string) {
	observedMaxNodesStr := "unset"
	if observed.MaxNodesTotal != nil {
		observedMaxNodesStr = fmt.Sprintf("%d", *observed.MaxNodesTotal)
	}
	if observed.MaxNodesTotal == nil || *observed.MaxNodesTotal != desired.MaxNodesTotal {
		return false, fmt.Sprintf("hypershift HostedCluster autoscaling maxNodesTotal is %s, want %d", observedMaxNodesStr, desired.MaxNodesTotal)
	}

	observedMaxPodGraceStr := "unset"
	if observed.MaxPodGracePeriod != nil {
		observedMaxPodGraceStr = fmt.Sprintf("%d", *observed.MaxPodGracePeriod)
	}
	if observed.MaxPodGracePeriod == nil || *observed.MaxPodGracePeriod != desired.MaxPodGracePeriodSeconds {
		return false, fmt.Sprintf("hypershift HostedCluster autoscaling maxPodGracePeriod is %s, want %d", observedMaxPodGraceStr, desired.MaxPodGracePeriodSeconds)
	}

	wantDuration := time.Duration(desired.MaxNodeProvisionTimeSeconds) * time.Second
	wantDisplay := fmt.Sprint(wantDuration.Minutes(), "m")
	if observed.MaxNodeProvisionTime == "" {
		return false, fmt.Sprintf("hypershift HostedCluster autoscaling maxNodeProvisionTime is unset, want %q", wantDisplay)
	}
	observedDuration, err := time.ParseDuration(observed.MaxNodeProvisionTime)
	if err != nil {
		return false, fmt.Sprintf("hypershift HostedCluster autoscaling maxNodeProvisionTime is %q, which is not a valid duration, want %q", observed.MaxNodeProvisionTime, wantDisplay)
	}
	if observedDuration != wantDuration {
		return false, fmt.Sprintf("hypershift HostedCluster autoscaling maxNodeProvisionTime is %q, want %q", observed.MaxNodeProvisionTime, wantDisplay)
	}

	observedPodPriorityThresholdStr := "unset"
	if observed.PodPriorityThreshold != nil {
		observedPodPriorityThresholdStr = fmt.Sprintf("%d", *observed.PodPriorityThreshold)
	}
	if observed.PodPriorityThreshold == nil || *observed.PodPriorityThreshold != desired.PodPriorityThreshold {
		return false, fmt.Sprintf("hypershift HostedCluster autoscaling podPriorityThreshold is %s, want %d", observedPodPriorityThresholdStr, desired.PodPriorityThreshold)
	}

	return true, ""
}

func (c *operationClusterUpdate) imageContentSourcesMatchDesired(desired []api.ImageDigestMirror, observed []v1beta1.ImageContentSource) (bool, string) {
	if len(desired) == 0 && len(observed) == 0 {
		return true, ""
	}
	if len(desired) != len(observed) {
		return false, fmt.Sprintf("hypershift HostedCluster has %d imageContentSources, want %d", len(observed), len(desired))
	}

	for i := range desired {
		if i >= len(observed) {
			break
		}
		if desired[i].Source != observed[i].Source {
			return false, fmt.Sprintf("hypershift HostedCluster imageContentSources[%d].source is %q, want %q", i, observed[i].Source, desired[i].Source)
		}
		if !slices.Equal(desired[i].Mirrors, observed[i].Mirrors) {
			return false, fmt.Sprintf("hypershift HostedCluster imageContentSources[%d].mirrors do not match", i)
		}
	}
	return true, ""
}
