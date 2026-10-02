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

package detectors

import (
	corev1 "k8s.io/api/core/v1"

	"github.com/Azure/ARO-HCP/internal/kuberesources"
	"github.com/Azure/ARO-HCP/mgmt-agent/pkg/detection"
)

const (
	// SwiftV2LabelKey is the AKS-managed label marking a node as SWIFT-v2
	// delegated-NIC capable. A node without a delegated secondary NIC cannot
	// suffer a VF teardown, so this label scopes the swift-vf-teardown detector.
	// It is exported because the controller uses it to scope its node informer to
	// the only nodes any detector can apply to today.
	SwiftV2LabelKey = "kubernetes.azure.com/podnetwork-swiftv2-enabled"
	// SwiftV2LabelValue is the value SwiftV2LabelKey carries on a SWIFT-v2 node.
	SwiftV2LabelValue = "true"

	// reasonFailedCreatePodSandBox is the kubelet Event reason emitted when a pod
	// sandbox cannot be created. It is the failure signal for the SWIFT wedge.
	reasonFailedCreatePodSandBox = "FailedCreatePodSandBox"

	// swiftNICResourceName is the extended resource a pod requests to be given a
	// SWIFT v2 delegated NIC. The shared kuberesources constant keeps detection
	// and node resource accounting aligned without a controller dependency.
	swiftNICResourceName corev1.ResourceName = kuberesources.SwiftNICResourceName
)

var swiftSignatures = detection.MustCompileSignatures(
	`no such network interface`,
	`network is unreachable`,
	`mtpnc is not ready`,
	`dhcp discover.*timed out`,
)

func matchesSwiftSignature(message string) bool {
	for _, signature := range swiftSignatures {
		if signature.MatchString(message) {
			return true
		}
	}
	return false
}

// podRequestsSwiftNIC reports whether a pod asks for a SWIFT v2 delegated NIC,
// which is what makes its start evidence about the delegated-NIC path.
//
// Only these pods traverse the path this detector watches. A mgmt node runs the
// overwhelming majority of its pods on the ordinary overlay, which keeps working
// while the delegated-NIC path is dead, so counting them as success lets a node
// that cannot attach a single NIC look healthy indefinitely. Observed on CI node
// aks-userswft2-17575576-vmss000003 on 2026-09-01: 8 router pods across 7 hosted
// control planes hung for 16 minutes on dhcp-discover timeouts while the node
// created 110 other pods and brought 107 of them to Running.
//
// Extended resources are only schedulable when requested as a limit, and the
// kubelet copies the limit into requests, so either field carrying the resource
// means the pod needed a NIC.
func podRequestsSwiftNIC(p *corev1.Pod) bool {
	return kuberesources.PodRequestsSwiftNIC(p)
}

func isSwiftV2Node(node *corev1.Node) bool {
	return node != nil && node.Labels[SwiftV2LabelKey] == SwiftV2LabelValue
}
