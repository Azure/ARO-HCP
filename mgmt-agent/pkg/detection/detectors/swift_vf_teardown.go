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
	"slices"
	"time"

	"github.com/Azure/ARO-HCP/mgmt-agent/pkg/detection"
)

// NewSwiftVFTeardown detects the SWIFT v2 delegated-NIC teardown wedge: on a
// SWIFT-v2 node, a sustained FailedCreatePodSandBox storm in the route /
// network-unreachable / mtpnc / dhcp-timeout family with zero successful pod
// starts across the window. Signatures, thresholds, and the applicability label
// are constants, not config. It carries only the specifics; every evaluation
// primitive comes from detection.Signature.
func NewSwiftVFTeardown() detection.Signature {
	return detection.Signature{
		DetectorName: "swift-vf-teardown",
		ReasonText:   "SWIFT delegated-NIC teardown: sustained FailedCreatePodSandBox storm with zero successful pod starts",
		AppliesTo:    isSwiftV2Node,
		EventReason:  reasonFailedCreatePodSandBox,
		Signatures:   slices.Clone(swiftSignatures),
		// failuresFloor is 2, not 3, because a hard wedge presents with very few
		// distinct pods. Once a node cannot build a sandbox, almost nothing new is
		// placed on it, so the same handful of pods retry indefinitely rather than a
		// crowd of pods each failing once. The captured uksouth wedge
		// (TestProductionHardWedgeShapeFires) produced 1193 failure events from only
		// 2 distinct pods over 58 hours, so a floor of 3 would never have fired on it.
		// False positives are held off by dwell and requireZeroSuccess, not by this
		// floor: a flapping node's pods succeed on retry, so they never reach the
		// dwell, and the node's other successes trip requireZeroSuccess anyway.
		FailuresFloor:      2,
		EvaluationWindow:   10 * time.Minute,
		Dwell:              10 * time.Minute,
		RequireZeroSuccess: true,
		SuccessScope:       podRequestsSwiftNIC,
	}
}
