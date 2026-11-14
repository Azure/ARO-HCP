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
	"time"

	corev1 "k8s.io/api/core/v1"
)

// PodRequestsSwiftNIC identifies Pods that use the delegated-network path.
func PodRequestsSwiftNIC(pod *corev1.Pod) bool { return podRequestsSwiftNIC(pod) }

// StuckSince reads the durable initial-sandbox condition, excluding terminal Pods.
func StuckSince(pod *corev1.Pod) (time.Time, bool) { return stuckSince(pod) }

// EventLastTime reads the latest activity timestamp of a core Event.
func EventLastTime(event *corev1.Event) time.Time { return eventLastTime(event) }

// MatchesSwiftSignature uses the node-health SWIFT failure signatures.
func MatchesSwiftSignature(message string) bool {
	_, matches := swiftVFTeardown.matchSignature(message)
	return matches
}
