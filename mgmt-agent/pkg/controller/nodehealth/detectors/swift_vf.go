// Copyright 2025 Microsoft Corporation
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
	"time"

	corev1 "k8s.io/api/core/v1"
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
)

// swiftVFTeardown detects the SWIFT v2 delegated-NIC teardown wedge: on a
// SWIFT-v2 node, a sustained FailedCreatePodSandBox storm in the route /
// network-unreachable / mtpnc / dhcp-timeout family with zero successful pod
// starts across the window. Signatures, thresholds, and the applicability label
// are constants, not config. It carries only the specifics; every evaluation
// primitive comes from signatureDetector (see signature_detector.go).
var swiftVFTeardown = signatureDetector{
	name:        "swift-vf-teardown",
	reason:      "SWIFT delegated-NIC teardown: sustained FailedCreatePodSandBox storm with zero successful pod starts",
	appliesTo:   isSwiftV2Node,
	eventReason: reasonFailedCreatePodSandBox,
	signatures: mustCompileSignatures(
		`no such network interface`,
		`network is unreachable`,
		`mtpnc is not ready`,
		`dhcp discover.*timed out`,
	),
	// failuresFloor is 2, not 3, because a hard wedge presents with very few
	// distinct pods. Once a node cannot build a sandbox, almost nothing new is
	// placed on it, so the same handful of pods retry indefinitely rather than a
	// crowd of pods each failing once. The captured uksouth wedge
	// (TestProductionHardWedgeShapeFires) produced 1193 failure events from only
	// 2 distinct pods over 58 hours, so a floor of 3 would never have fired on it.
	// False positives are held off by dwell and requireZeroSuccess, not by this
	// floor: a flapping node's pods succeed on retry, so they never reach the
	// dwell, and the node's other successes trip requireZeroSuccess anyway.
	failuresFloor:      2,
	window:             10 * time.Minute,
	dwell:              10 * time.Minute,
	requireZeroSuccess: true,
}

func isSwiftV2Node(node *corev1.Node) bool {
	return node != nil && node.Labels[SwiftV2LabelKey] == SwiftV2LabelValue
}
