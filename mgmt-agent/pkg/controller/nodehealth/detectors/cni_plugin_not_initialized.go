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

import "time"

// reasonNetworkNotReady is the kubelet Event reason emitted when the
// container runtime network is not ready (e.g. CNI plugin not initialized).
const reasonNetworkNotReady = "NetworkNotReady"

// cniPluginNotInitialized detects a SWIFT-v2 node whose kubelet remains Ready
// while its CNI plugin cannot initialize. This is distinct from delegated-NIC
// teardown: kubelet emits pod-scoped NetworkNotReady Events (InvolvedObject.Kind
// = Pod) rather than FailedCreatePodSandBox, but the durable symptom and
// recovery discriminator are the same.
var cniPluginNotInitialized = signatureDetector{
	name:        "cni-plugin-not-initialized",
	reason:      "CNI plugin not initialized: sustained NetworkNotReady failures with zero successful pod starts",
	appliesTo:   isSwiftV2Node,
	eventReason: reasonNetworkNotReady,
	signatures: mustCompileSignatures(
		`cni plugin not initialized`,
	),
	failuresFloor:      3,
	window:             10 * time.Minute,
	dwell:              10 * time.Minute,
	requireZeroSuccess: true,
}
