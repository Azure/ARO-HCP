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

// Cluster update operation state calculation for the cluster update operation controller.

// hypershiftHostedClusterOperationState contains the cluster update operation state calculation comparing desired state
// against Hypershift's HostedCluster in the management cluster.
func (c *operationClusterUpdate) hypershiftHostedClusterOperationState(ctx context.Context, cluster *api.HCPOpenShiftCluster, spc *api.ServiceProviderCluster) (*operationState, error) {
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
		return newOperationState(arm.ProvisioningStateUpdating, "Hypershift HostedCluster has not been observed yet"), nil
	}

	if matches, message := c.hypershiftHostedClusterSpecMatchesDesired(cluster, spc, hostedCluster); !matches {
		return newOperationState(arm.ProvisioningStateUpdating, message), nil
	}

	// TODO: add hypershiftHostedClusterStatusMatchesDesired to perform checks against Hypershift's HostedCluster status.

	return newOperationState(arm.ProvisioningStateSucceeded, ""), nil
}

// hypershiftHostedClusterSpecMatchesDesired reports whether Hypershift HostedCluster .Spec fields
// and other non status configuration matches desired state. Returns false and a diagnostic message
// when any leaf check fails. HostedCluster .status is not checked here.
func (c *operationClusterUpdate) hypershiftHostedClusterSpecMatchesDesired(cluster *api.HCPOpenShiftCluster, spc *api.ServiceProviderCluster, hostedCluster *v1beta1.HostedCluster) (bool, string) {
	if matches, message := c.hypershiftHostedClusterAllowedCIDRBlocksSpecMatchesDesired(cluster.CustomerProperties.API.AuthorizedCIDRs, &hostedCluster.Spec); !matches {
		return false, message
	}
	if matches, message := c.hypershiftHostedClusterAvailabilityPoliciesSpecMatchesDesired(cluster.ServiceProviderProperties.ExperimentalFeatures.ControlPlaneAvailability, &hostedCluster.Spec); !matches {
		return false, message
	}
	if matches, message := c.hypershiftHostedClusterSizeOverrideAnnotationMatchesDesired(cluster.ServiceProviderProperties.ExperimentalFeatures.ControlPlanePodSizing, spc.Spec.DesiredHostedClusterControlPlaneSize, hostedCluster.Annotations); !matches {
		return false, message
	}
	if matches, message := c.hypershiftHostedClusterControlPlaneOperatorImageAnnotationMatchesDesired(cluster.ServiceProviderProperties.ExperimentalFeatures.ControlPlaneOperatorImage, hostedCluster.Annotations); !matches {
		return false, message
	}
	if matches, message := c.hypershiftHostedClusterAutoscalingSpecMatchesDesired(cluster.CustomerProperties.Autoscaling, &hostedCluster.Spec.Autoscaling); !matches {
		return false, message
	}
	if matches, message := c.hypershiftHostedClusterImageContentSourcesSpecMatchesDesired(cluster.CustomerProperties.ImageDigestMirrors, hostedCluster.Spec.ImageContentSources); !matches {
		return false, message
	}
	return true, ""
}

// hypershiftHostedClusterAllowedCIDRBlocksSpecMatchesDesired reports whether HostedCluster's apiserver allowedCIDRBlocks spec
// reflects desired state's authorizedCIDRs. Nil desired means allow-all (no blocks). When restrictions are
// enabled, every customer CIDR must appear in the observed list.
// Note: For now this check is incomplete: it can confirm additions but cannot
// detect removal of a previously configured customer CIDR. See TODO below on details, why and what
// needs to be done to fix this. For now we partially compensate by checking state from CS too in its corresponding
// state calculation.
func (c *operationClusterUpdate) hypershiftHostedClusterAllowedCIDRBlocksSpecMatchesDesired(desired []string, observedSpec *v1beta1.HostedClusterSpec) (bool, string) {
	var observedCIDRs []string
	if observedSpec.Networking.APIServer != nil && len(observedSpec.Networking.APIServer.AllowedCIDRBlocks) > 0 {
		observedCIDRs = make([]string, len(observedSpec.Networking.APIServer.AllowedCIDRBlocks))
		for i, block := range observedSpec.Networking.APIServer.AllowedCIDRBlocks {
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
	// TODO Revisit when we have the information of the nodes egress lb IPs in the RP
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

// hypershiftHostedClusterAvailabilityPoliciesSpecMatchesDesired reports whether HostedCluster's
// controller and infrastructure availability policies match the desired state's control plane availability setting.
func (c *operationClusterUpdate) hypershiftHostedClusterAvailabilityPoliciesSpecMatchesDesired(desired api.ControlPlaneAvailability, observedSpec *v1beta1.HostedClusterSpec) (bool, string) {
	expectedAvailability := v1beta1.HighlyAvailable
	if desired == api.SingleReplicaControlPlane {
		expectedAvailability = v1beta1.SingleReplica
	}

	if observedSpec.ControllerAvailabilityPolicy != expectedAvailability {
		return false, fmt.Sprintf(
			"hypershift HostedCluster controllerAvailabilityPolicy is %q, want %q",
			string(observedSpec.ControllerAvailabilityPolicy),
			expectedAvailability,
		)
	}

	if observedSpec.InfrastructureAvailabilityPolicy != expectedAvailability {
		return false, fmt.Sprintf(
			"hypershift HostedCluster infrastructureAvailabilityPolicy is %q, want %q",
			string(observedSpec.InfrastructureAvailabilityPolicy),
			expectedAvailability,
		)
	}

	return true, ""
}

// hypershiftHostedClusterSizeOverrideAnnotationMatchesDesired reports whether HostedCluster's
// cluster size override annotation matches desired state of control plane sizing from cluster experimental
// features and/or spc.Spec.DesiredHostedClusterControlPlaneSize.
func (c *operationClusterUpdate) hypershiftHostedClusterSizeOverrideAnnotationMatchesDesired(desiredClusterControlPlanePodSizing api.ControlPlanePodSizing, desiredSPCControlPlanePodSizing *string, observedAnnotations map[string]string) (bool, string) {
	annotationKey := v1beta1.ClusterSizeOverrideAnnotation
	observedValue, ok := observedAnnotations[annotationKey]

	if desiredSPCControlPlanePodSizing == nil &&
		desiredClusterControlPlanePodSizing != "" &&
		desiredClusterControlPlanePodSizing != api.MinimalControlPlanePodSizing {
		return false, fmt.Sprintf("unrecognized cluster-level control plane pod sizing: %q", desiredClusterControlPlanePodSizing)
	}

	expected, wantSet := ocm.ConvertHostedClusterSizeOverrideToCS(desiredClusterControlPlanePodSizing, desiredSPCControlPlanePodSizing)
	if wantSet {
		if !ok {
			return false, fmt.Sprintf(
				"hypershift HostedCluster annotation %q is unset, want %q",
				annotationKey,
				expected,
			)
		}
		if observedValue != expected {
			return false, fmt.Sprintf(
				"hypershift HostedCluster annotation %q is %q, want %q",
				annotationKey,
				observedValue,
				expected,
			)
		}
		return true, ""
	}

	if ok {
		return false, fmt.Sprintf(
			"hypershift HostedCluster annotation %q is %q, want unset",
			annotationKey,
			observedValue,
		)
	}
	return true, ""
}

// hypershiftHostedClusterControlPlaneOperatorImageAnnotationMatchesDesired reports whether the
// HostedCluster's control plane operator image annotation matches the desired state's experimental feature override.
// An empty desired value requires the annotation to be absent.
func (c *operationClusterUpdate) hypershiftHostedClusterControlPlaneOperatorImageAnnotationMatchesDesired(desired string, observedAnnotations map[string]string) (bool, string) {
	observedValue, ok := observedAnnotations[v1beta1.ControlPlaneOperatorImageAnnotation]

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

// hypershiftHostedClusterAutoscalingSpecMatchesDesired reports whether HostedCluster's autoscaling spec
// matches the desired state's cluster autoscaling profile.
func (c *operationClusterUpdate) hypershiftHostedClusterAutoscalingSpecMatchesDesired(desired api.ClusterAutoscalingProfile, observed *v1beta1.ClusterAutoscaling) (bool, string) {
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
		return false, fmt.Sprintf("hypershift HostedCluster autoscaling maxNodeProvisionTime has an invalid duration: %q, want %q", observed.MaxNodeProvisionTime, wantDisplay)
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

// platformImageContentSources lists HostedCluster imageContentSources managed internally by the
// service. These are ignored when comparing customer imageDigestMirrors propagation.
var platformImageContentSources = map[string]struct{}{
	"quay.io/openshift-release-dev/ocp-v4.0-art-dev":    {},
	"quay.io/openshift-release-dev/ocp-v5.0-art-dev":    {},
	"quay.io/openshift-release-dev/ocp-release":         {},
	"quay.io/openshift-release-dev/ocp-release-nightly": {},
}

// isPlatformImageContentSource reports whether source is a service-managed platform image source.
func isPlatformImageContentSource(source string) bool {
	_, ok := platformImageContentSources[source]
	return ok
}

// hypershiftHostedClusterImageContentSourcesSpecMatchesDesired reports whether HostedCluster
// imageContentSources spec matches desired state's imageDigestMirrors. Platform-managed sources may be
// present on the HostedCluster without matching a customer desired entry.
func (c *operationClusterUpdate) hypershiftHostedClusterImageContentSourcesSpecMatchesDesired(desired []api.ImageDigestMirror, observed []v1beta1.ImageContentSource) (bool, string) {
	desiredBySource := make(map[string]api.ImageDigestMirror, len(desired))
	for _, want := range desired {
		desiredBySource[want.Source] = want
	}

	observedBySource := make(map[string]v1beta1.ImageContentSource, len(observed))
	for _, ics := range observed {
		observedBySource[ics.Source] = ics
	}

	// We check that the desired imageContentSources are present in the observed imageContentSources.
	for source, want := range desiredBySource {
		got, ok := observedBySource[source]
		if !ok {
			return false, fmt.Sprintf("hypershift HostedCluster imageContentSources is missing source %q", source)
		}
		if !slices.Equal(want.Mirrors, got.Mirrors) {
			return false, fmt.Sprintf("hypershift HostedCluster imageContentSources for source %q mirrors do not match", source)
		}
	}

	// We check that there are no unexpected observed imageContentSources (excluding platform-managed sources)
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

// clusterServiceClusterSpecOperationState reports whether Cluster Service cluster spec fields
// match desired state intent for the cluster update operation. Only checks outside CS .status.
// Checks that can only be performed against Cluster Service instead of the management cluster
// directly can be added here
// Add checks against the management cluster state when possible instead of here, to reduce the number of checks against Cluster Service, as
// CS will be removed in the future.
func (c *operationClusterUpdate) clusterServiceClusterSpecOperationState(cluster *api.HCPOpenShiftCluster, csCluster *arohcpv1alpha1.Cluster) (*operationState, error) {
	if matches, message := c.clusterServiceClusterSpecMatchesDesired(cluster, csCluster); !matches {
		return newOperationState(arm.ProvisioningStateUpdating, message), nil
	}
	return newOperationState(arm.ProvisioningStateSucceeded, ""), nil
}

// clusterServiceClusterSpecMatchesDesired reports whether Cluster Service cluster spec fields
// relevant to the cluster update operation match desired state. Returns false and a diagnostic
// message when any leaf check fails.
func (c *operationClusterUpdate) clusterServiceClusterSpecMatchesDesired(cluster *api.HCPOpenShiftCluster, csCluster *arohcpv1alpha1.Cluster) (bool, string) {
	// TODO for now we calculate authorized CIDR against CS because we cannot calculate the difference on
	// the Hypershift HostedCluster because there are internal IPs associated to the Node Pools egress LB that we
	// do not track on the RP side yet. Once that is tracked we should remove this and update the logic that calculates
	// state from the Hypershift HostedCluster instead.
	if matches, message := c.clusterServiceClusterAuthorizedCIDRsSpecMatchesDesired(cluster.CustomerProperties.API.AuthorizedCIDRs, csCluster); !matches {
		return false, message
	}
	if matches, message := c.clusterServiceClusterNodeDrainTimeoutSpecMatchesDesired(cluster.CustomerProperties.NodeDrainTimeoutMinutes, csCluster); !matches {
		return false, message
	}
	return true, ""
}

// clusterServiceClusterAuthorizedCIDRsSpecMatchesDesired reports whether Cluster Service
// k8sAPIServerAuthorizedCIDRs matches desired state's authorizedCIDRs. Nil desired requires allow-all mode
// with no CIDR entries. Non-nil desired requires allow-list mode with an exact CIDR match.
func (c *operationClusterUpdate) clusterServiceClusterAuthorizedCIDRsSpecMatchesDesired(desired []string, csCluster *arohcpv1alpha1.Cluster) (bool, string) {
	csClusterAPI := csCluster.API()

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

// clusterServiceClusterNodeDrainTimeoutSpecMatchesDesired reports whether Cluster Service
// nodeDrainGracePeriod matches desired state's nodeDrainTimeoutMinutes.
func (c *operationClusterUpdate) clusterServiceClusterNodeDrainTimeoutSpecMatchesDesired(desired int32, csCluster *arohcpv1alpha1.Cluster) (bool, string) {
	got := ocm.ClusterUpdateDispatchConfigNodeDrainTimeoutFromCS(csCluster)
	if got != desired {
		return false, fmt.Sprintf("Cluster Service nodeDrainGracePeriod is %d minutes, want %d", got, desired)
	}
	return true, ""
}
