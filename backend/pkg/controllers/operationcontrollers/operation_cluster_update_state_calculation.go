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

	arohcpv1alpha1 "github.com/openshift-online/ocm-sdk-go/arohcp/v1alpha1"
	"github.com/openshift/hypershift/api/hypershift/v1beta1"

	"github.com/Azure/ARO-HCP/backend/pkg/maestrohelpers"
	"github.com/Azure/ARO-HCP/internal/api"
	"github.com/Azure/ARO-HCP/internal/api/arm"
	"github.com/Azure/ARO-HCP/internal/ocm"
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

	if matches, message := c.hypershiftClusterSpecMatchesCosmosDesired(cluster, hostedCluster); !matches {
		logger.Info("hypershift HostedCluster spec does not match cosmos desired configuration", "message", message)
		return newOperationState(arm.ProvisioningStateUpdating, message), nil
	}

	return newOperationState(arm.ProvisioningStateSucceeded, ""), nil
}

// hypershiftClusterSpecMatchesCosmosDesired compares Cosmos desired properties against the Hypershift
// HostedCluster fields that are relevant for the update operation.
func (c *operationClusterUpdate) hypershiftClusterSpecMatchesCosmosDesired(desired *api.HCPOpenShiftCluster, observed *v1beta1.HostedCluster) (bool, string) {
	if matches, message := c.allowedCIDRBlocksMatchDesired(desired.CustomerProperties.API.AuthorizedCIDRs, observed.Spec); !matches {
		return false, message
	}
	if matches, message := c.availabilityPoliciesMatchDesired(desired.ServiceProviderProperties.ExperimentalFeatures.ControlPlaneAvailability, observed.Spec); !matches {
		return false, message
	}
	if matches, message := c.clusterSizeOverrideAnnotationMatchesDesired(desired.ServiceProviderProperties.ExperimentalFeatures.ControlPlanePodSizing, observed.Annotations); !matches {
		return false, message
	}
	if matches, message := c.controlPlaneOperatorImageAnnotationMatchesDesired(desired.ServiceProviderProperties.ExperimentalFeatures.ControlPlaneOperatorImage, observed.Annotations); !matches {
		return false, message
	}
	if matches, message := c.autoscalingSpecMatchesDesired(desired.CustomerProperties.Autoscaling, observed.Spec.Autoscaling); !matches {
		return false, message
	}
	if matches, message := c.imageContentSourcesMatchDesired(desired.CustomerProperties.ImageDigestMirrors, observed.Spec.ImageContentSources); !matches {
		return false, message
	}
	return true, ""
}

// clusterServiceClusterSpecOperationState is used to compare the cosmos cluster state with the cluster
// service state outside of the CS Cluster's `.status` section.
// Checks that can only be performed against Cluster Service instead of checking from the management cluster side directly can be added here.
// Prefer calculating the state from the management cluster side directly when possible.
func (c *operationClusterUpdate) clusterServiceClusterSpecOperationState(desired *api.HCPOpenShiftCluster, observed *arohcpv1alpha1.Cluster) (*operationState, error) {
	if matches, message := c.clusterServiceClusterSpecMatchesCosmosDesired(desired, observed); !matches {
		return newOperationState(arm.ProvisioningStateUpdating, message), nil
	}
	return newOperationState(arm.ProvisioningStateSucceeded, ""), nil
}

// clusterServiceClusterSpecMatchesCosmosDesired compares Cosmos desired properties against the
// Cluster Service cluster fields that are relevant for the update operation.
func (c *operationClusterUpdate) clusterServiceClusterSpecMatchesCosmosDesired(desired *api.HCPOpenShiftCluster, observed *arohcpv1alpha1.Cluster) (bool, string) {
	// TODO for now we calculate authorized CIDR against CS because we cannot calculate the difference on
	// the Hypershift HostedCluster because there are internal IPs associated to the Node Pools egress LB that we
	// do not track on the RP side yet. Once that is tracked we should remove this and update the logic that calculates
	// state from the Hypershift HostedCluster instead.
	if matches, message := c.clusterServiceAuthorizedCIDRsMatchDesired(desired.CustomerProperties.API.AuthorizedCIDRs, observed); !matches {
		return false, message
	}
	if matches, message := c.nodeDrainTimeoutMinutesMatchDesired(desired.CustomerProperties.NodeDrainTimeoutMinutes, observed); !matches {
		return false, message
	}
	return true, ""
}

func (c *operationClusterUpdate) clusterServiceAuthorizedCIDRsMatchDesired(desired []string, observed *arohcpv1alpha1.Cluster) (bool, string) {
	csClusterAPI := observed.API()

	formatClusterServiceCIDRBlockAllowAccess := func(mode string, values []string) string {
		switch mode {
		case ocm.CSCIDRBlockAllowAccessModeAllowAll:
			return ocm.CSCIDRBlockAllowAccessModeAllowAll
		case ocm.CSCIDRBlockAllowAccessModeAllowList:
			return fmt.Sprintf("%s %v", ocm.CSCIDRBlockAllowAccessModeAllowList, values)
		case "":
			return "unset"
		default:
			return fmt.Sprintf("%q with %v", mode, values)
		}
	}

	csObservedAuthorizedCIDRs := ocm.ClusterUpdateDispatchConfigAuthorizedCIDRsFromCS(csClusterAPI)
	csObservedAllowMode := ""
	if allow := csClusterAPI.CIDRBlockAccess().Allow(); allow != nil {
		csObservedAllowMode, _ = allow.GetMode()
	}
	csObservedMessage := formatClusterServiceCIDRBlockAllowAccess(csObservedAllowMode, csObservedAuthorizedCIDRs)

	if desired == nil {
		if csObservedAllowMode != ocm.CSCIDRBlockAllowAccessModeAllowAll {
			return false, fmt.Sprintf(
				"Cluster Service k8sAPIServerAuthorizedCIDRs is %s, want %s",
				csObservedMessage, ocm.CSCIDRBlockAllowAccessModeAllowAll,
			)
		}
		if len(csObservedAuthorizedCIDRs) > 0 {
			return false, fmt.Sprintf(
				"Cluster Service k8sAPIServerAuthorizedCIDRs is %s, want %s",
				csObservedMessage, ocm.CSCIDRBlockAllowAccessModeAllowAll,
			)
		}
		return true, ""
	}

	if csObservedAllowMode != ocm.CSCIDRBlockAllowAccessModeAllowList {
		return false, fmt.Sprintf(
			"Cluster Service k8sAPIServerAuthorizedCIDRs is %s, want %s",
			csObservedMessage, ocm.CSCIDRBlockAllowAccessModeAllowList,
		)
	}
	if !slices.Equal(desired, csObservedAuthorizedCIDRs) {
		return false, fmt.Sprintf(
			"Cluster Service k8sAPIServerAuthorizedCIDRs allow_list is %v, want %v",
			csObservedAuthorizedCIDRs,
			desired,
		)
	}

	return true, ""
}

func (c *operationClusterUpdate) nodeDrainTimeoutMinutesMatchDesired(desired int32, observed *arohcpv1alpha1.Cluster) (bool, string) {
	got := ocm.ClusterUpdateDispatchConfigNodeDrainTimeoutFromCS(observed)
	if got != desired {
		return false, fmt.Sprintf("Cluster Service nodeDrainGracePeriod is %d minutes, want %d", got, desired)
	}
	return true, ""
}

func (c *operationClusterUpdate) allowedCIDRBlocksMatchDesired(desired []string, observed v1beta1.HostedClusterSpec) (bool, string) {
	var observedCIDRs []string
	if observed.Networking.APIServer != nil && len(observed.Networking.APIServer.AllowedCIDRBlocks) > 0 {
		observedCIDRs = make([]string, len(observed.Networking.APIServer.AllowedCIDRBlocks))
		for i, block := range observed.Networking.APIServer.AllowedCIDRBlocks {
			observedCIDRs[i] = string(block)
		}
	}

	if desired == nil {
		if len(observedCIDRs) > 0 {
			return false, fmt.Sprintf("hypershift HostedCluster apiServer allowedCIDRBlocks is %v, want unset (allow all)", observedCIDRs)
		}
		return true, ""
	}

	if len(observedCIDRs) == 0 {
		return false, fmt.Sprintf("hypershift HostedCluster apiServer allowedCIDRBlocks is unset, want %v", desired)
	}

	// When restrictions are enabled, Cluster Service adds internal CIDR blocks that
	// are not surfaced in customer authorizedCidrs. Require every customer CIDR
	// to be present in observed allowedCIDRBlocks, but ignore extra entries.
	//
	// TODO This subset check is incomplete for now: it can confirm additions but cannot
	// detect removal of a previously configured customer CIDR, because extras
	// cannot be distinguished from internal blocks. Clearing all restrictions
	// (nil desired) is handled above by requiring allowedCIDRBlocks to be unset.
	// TODO Revisit when we have the information of the nodes egress lb IPs in he RP
	// (or can read the internally set allow-list from Cluster Service) so stale customer entries can be rejected.
	observedSet := make(map[string]struct{}, len(observedCIDRs))
	for _, cidr := range observedCIDRs {
		observedSet[cidr] = struct{}{}
	}
	for _, want := range desired {
		if _, ok := observedSet[want]; !ok {
			return false, fmt.Sprintf("hypershift HostedCluster apiServer allowedCIDRBlocks is missing %q, want %v", want, desired)
		}
	}
	return true, ""
}

func (c *operationClusterUpdate) availabilityPoliciesMatchDesired(desired api.ControlPlaneAvailability, observed v1beta1.HostedClusterSpec) (bool, string) {
	expectedAvailability := v1beta1.HighlyAvailable
	if desired == api.SingleReplicaControlPlane {
		expectedAvailability = v1beta1.SingleReplica
	}

	if observed.ControllerAvailabilityPolicy != expectedAvailability {
		return false, fmt.Sprintf(
			"hypershift HostedCluster controllerAvailabilityPolicy is %q, want %q",
			formatAvailabilityPolicy(observed.ControllerAvailabilityPolicy),
			expectedAvailability,
		)
	}

	if observed.InfrastructureAvailabilityPolicy != expectedAvailability {
		return false, fmt.Sprintf(
			"hypershift HostedCluster infrastructureAvailabilityPolicy is %q, want %q",
			formatAvailabilityPolicy(observed.InfrastructureAvailabilityPolicy),
			expectedAvailability,
		)
	}

	return true, ""
}

// ClusterSizeOverrideE2EMinimal is the value for the cluster size override annotation
// that configures minimal resource requests suitable for e2e testing environments.
// The actual resource configuration is managed by the Hypershift operator's ClusterSizingConfig.
// See: https://github.com/openshift/hypershift/blob/main/api/hypershift/v1beta1/hostedcluster_types.go
// ARO HCP config: https://github.com/Azure/ARO-HCP/blob/main/hypershiftoperator/deploy/templates/cluster.clustersizingconfiguration.yaml
const minimalClusterSizeOverride = "e2e_minimal"

func (c *operationClusterUpdate) clusterSizeOverrideAnnotationMatchesDesired(desired api.ControlPlanePodSizing, observed map[string]string) (bool, string) {
	observedValue, ok := observed[v1beta1.ClusterSizeOverrideAnnotation]

	if desired == api.MinimalControlPlanePodSizing {
		if !ok {
			return false, fmt.Sprintf(
				"hypershift HostedCluster annotation %q is unset, want %q",
				v1beta1.ClusterSizeOverrideAnnotation,
				minimalClusterSizeOverride,
			)
		}
		if observedValue != minimalClusterSizeOverride {
			return false, fmt.Sprintf(
				"hypershift HostedCluster annotation %q is %q, want %q",
				v1beta1.ClusterSizeOverrideAnnotation,
				observedValue,
				minimalClusterSizeOverride,
			)
		}
		return true, ""
	}

	if ok {
		return false, fmt.Sprintf(
			"hypershift HostedCluster annotation %q is %q, want unset",
			v1beta1.ClusterSizeOverrideAnnotation,
			observedValue,
		)
	}
	return true, ""
}

func (c *operationClusterUpdate) controlPlaneOperatorImageAnnotationMatchesDesired(desired string, observed map[string]string) (bool, string) {
	observedValue, ok := observed[v1beta1.ControlPlaneOperatorImageAnnotation]

	if desired != "" {
		if !ok {
			return false, fmt.Sprintf(
				"hypershift HostedCluster annotation %q is unset, want %q",
				v1beta1.ControlPlaneOperatorImageAnnotation,
				desired,
			)
		}
		if observedValue != desired {
			return false, fmt.Sprintf(
				"hypershift HostedCluster annotation %q is %q, want %q",
				v1beta1.ControlPlaneOperatorImageAnnotation,
				observedValue,
				desired,
			)
		}
		return true, ""
	}

	if ok {
		return false, fmt.Sprintf(
			"hypershift HostedCluster annotation %q is %q, want unset",
			v1beta1.ControlPlaneOperatorImageAnnotation,
			observedValue,
		)
	}
	return true, ""
}

func formatAvailabilityPolicy(policy v1beta1.AvailabilityPolicy) string {
	if policy == "" {
		return "unset"
	}
	return string(policy)
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

// platformImageContentSources are HostedCluster imageContentSources set and
// managed internally by our service,  not customer imageDigestMirrors. We track that
// to ignore them when checking for imageContentSources match.
var platformImageContentSources = map[string]struct{}{
	"quay.io/openshift-release-dev/ocp-v4.0-art-dev":    {},
	"quay.io/openshift-release-dev/ocp-v5.0-art-dev":    {},
	"quay.io/openshift-release-dev/ocp-release":         {},
	"quay.io/openshift-release-dev/ocp-release-nightly": {},
}

func isPlatformImageContentSource(source string) bool {
	_, ok := platformImageContentSources[source]
	return ok
}

func (c *operationClusterUpdate) imageContentSourcesMatchDesired(desired []api.ImageDigestMirror, observed []v1beta1.ImageContentSource) (bool, string) {
	desiredBySource := make(map[string]api.ImageDigestMirror, len(desired))
	for _, want := range desired {
		desiredBySource[want.Source] = want
	}

	observedBySource := make(map[string]v1beta1.ImageContentSource, len(observed))
	for _, ics := range observed {
		observedBySource[ics.Source] = ics
	}

	for source, want := range desiredBySource {
		got, ok := observedBySource[source]
		if !ok {
			return false, fmt.Sprintf("hypershift HostedCluster imageContentSources is missing source %q", source)
		}
		if !slices.Equal(want.Mirrors, got.Mirrors) {
			return false, fmt.Sprintf("hypershift HostedCluster imageContentSources for source %q mirrors do not match", source)
		}
	}

	for source := range observedBySource {
		if _, ok := desiredBySource[source]; ok {
			continue
		}
		if isPlatformImageContentSource(source) {
			continue
		}
		return false, fmt.Sprintf("hypershift HostedCluster has unexpected imageContentSource %q", source)
	}

	return true, ""
}
