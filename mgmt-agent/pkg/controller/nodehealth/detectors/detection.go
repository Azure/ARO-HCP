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
	"k8s.io/apimachinery/pkg/types"
)

// Detection is scoped fault evidence, not authorization to mutate a resource.
type Detection struct {
	Detector     string
	Scope        Scope
	NodeUID      types.UID
	PodUIDs      []types.UID
	Since        time.Time
	LastEvidence time.Time
}

// PodScopedDetector evaluates an individual Pod, not its node's health.
type PodScopedDetector interface {
	Detector
	EvaluatePod(pod *corev1.Pod, events []*corev1.Event, now time.Time) (since, latest time.Time, matches bool)
}

// CollectDetections returns the node-wide verdict and independent Pod faults.
// Node-wide precedence and recovery are owned by Decide.
func CollectDetections(node *corev1.Node, events []*corev1.Event, pods []*corev1.Pod, now time.Time) []Detection {
	if node == nil || node.UID == "" || node.DeletionTimestamp != nil {
		return nil
	}
	var result []Detection
	if decision, snapshot := Decide(node, events, pods, now); decision == DecisionWedged {
		result = append(result, Detection{
			Detector: snapshot.DetectorName, Scope: NodeScope, NodeUID: node.UID,
			Since: snapshot.StuckSince, LastEvidence: now,
		})
	}
	for _, detector := range registeredDetectors {
		d, ok := detector.(PodScopedDetector)
		if !ok || d.Scope() != PodScope || !d.Applies(node) {
			continue
		}
		for _, pod := range pods {
			if pod == nil || pod.Spec.NodeName != node.Name {
				continue
			}
			if since, latest, matches := d.EvaluatePod(pod, events, now); matches {
				result = append(result, Detection{
					Detector: d.Name(), Scope: PodScope, NodeUID: node.UID,
					PodUIDs: []types.UID{pod.UID}, Since: since, LastEvidence: latest,
				})
			}
		}
	}
	return result
}
