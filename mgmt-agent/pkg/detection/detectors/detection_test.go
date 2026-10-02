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
	"testing"
	"time"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/Azure/ARO-HCP/mgmt-agent/pkg/detection"
)

func sandboxFault(name string) (*corev1.Pod, *corev1.Event) {
	pod := withSwiftNIC(stuckPod(name, ago(15*time.Minute)))
	pod.CreationTimestamp = metav1.NewTime(ago(20 * time.Minute))
	pod.Status.Conditions = append(pod.Status.Conditions, corev1.PodCondition{
		Type: corev1.PodScheduled, Status: corev1.ConditionTrue,
		LastTransitionTime: metav1.NewTime(ago(15 * time.Minute)),
	})
	event := failEventFor(name, testNow, sig)
	event.FirstTimestamp = metav1.NewTime(ago(15 * time.Minute))
	return pod, event
}

func TestCollectDetectionsKeepsScopesIndependent(t *testing.T) {
	for _, test := range []struct {
		name       string
		stuck      int
		success    bool
		decision   detection.Decision
		nodeFaults int
	}{
		{"isolated pod", 1, false, detection.DecisionUnknown, 0},
		{"healthy neighbor", 2, true, detection.DecisionHealthy, 0},
		{"node and pod faults", 2, false, detection.DecisionWedged, 1},
	} {
		t.Run(test.name, func(t *testing.T) {
			node := testNode(true, true)
			node.UID = "node-uid"
			var pods []*corev1.Pod
			var events []*corev1.Event
			for _, name := range []string{"first", "second"}[:test.stuck] {
				pod, event := sandboxFault(name)
				pods = append(pods, pod)
				events = append(events, event)
			}
			if test.success {
				pods = append(pods, withSwiftNIC(startedPod("healthy", ago(time.Minute), false)))
			}
			decision, _ := testRegistry(t).Decide(node, events, pods, testNow)
			if decision != test.decision {
				t.Fatalf("node verdict %v, want %v", decision, test.decision)
			}
			got := testRegistry(t).CollectDetections(node, events, pods, testNow)
			if len(got) != test.nodeFaults+test.stuck {
				t.Fatalf("detections: %+v", got)
			}
			for i, fault := range got {
				if fault.NodeUID != node.UID || fault.Since != ago(15*time.Minute) || fault.LastEvidence != testNow {
					t.Fatalf("incorrect identity or timestamps: %+v", fault)
				}
				if i < test.nodeFaults {
					if fault.Scope != detection.NodeScope || fault.Detector != swiftVFTeardown.Name() || len(fault.PodUIDs) != 0 {
						t.Fatalf("incorrect node-wide detection: %+v", fault)
					}
				} else if fault.Scope != detection.PodScope || fault.Detector != SwiftPodSandboxStalled ||
					len(fault.PodUIDs) != 1 || fault.PodUIDs[0] != pods[i-test.nodeFaults].UID {
					t.Fatalf("incorrect pod-scoped detection: %+v", fault)
				}
			}
		})
	}
}

func TestCollectDetectionsScopeGuards(t *testing.T) {
	for _, test := range []struct {
		name   string
		change func(**corev1.Node, *corev1.Pod)
	}{
		{"nil node", func(n **corev1.Node, _ *corev1.Pod) { *n = nil }},
		{"missing node uid", func(n **corev1.Node, _ *corev1.Pod) { (*n).UID = "" }},
		{"deleting node", func(n **corev1.Node, _ *corev1.Pod) { (*n).DeletionTimestamp = &metav1.Time{Time: testNow} }},
		{"non swift node", func(n **corev1.Node, _ *corev1.Pod) { (*n).Labels = nil }},
		{"not ready", func(n **corev1.Node, _ *corev1.Pod) { (*n).Status.Conditions[0].Status = corev1.ConditionFalse }},
		{"pod assigned elsewhere", func(_ **corev1.Node, p *corev1.Pod) { p.Spec.NodeName = "other" }},
		{"recovered pod", func(_ **corev1.Node, p *corev1.Pod) { p.Status.Conditions[0].Status = corev1.ConditionTrue }},
	} {
		t.Run(test.name, func(t *testing.T) {
			node := testNode(true, true)
			node.UID = "node-uid"
			pod, event := sandboxFault("stalled")
			test.change(&node, pod)
			if got := testRegistry(t).CollectDetections(node, []*corev1.Event{event}, []*corev1.Pod{nil, pod}, testNow); len(got) != 0 {
				t.Fatalf("unexpected detections: %+v", got)
			}
		})
	}
}

type testPodScopedDetector struct {
	swiftPodSandboxStalledDetector
}

func (testPodScopedDetector) Name() string              { return "test-pod-fault" }
func (testPodScopedDetector) Applies(*corev1.Node) bool { return true }

func TestConsumersUseRegisteredScope(t *testing.T) {
	registry, err := detection.NewRegistry(testPodScopedDetector{})
	if err != nil {
		t.Fatal(err)
	}
	node := testNode(true, true)
	node.UID = "node-uid"
	pod, event := sandboxFault("stalled")
	events, pods := []*corev1.Event{event}, []*corev1.Pod{pod}
	if registry.AnyApplies(node) {
		t.Fatal("pod-scoped detector claimed node-label ownership")
	}
	if decision, _ := registry.Decide(node, events, pods, testNow); decision != detection.DecisionNotApplicable {
		t.Fatalf("pod-scoped detector changed node verdict: %v", decision)
	}
	got := registry.CollectDetections(node, events, pods, testNow)
	if len(got) != 1 || got[0].Detector != "test-pod-fault" || got[0].Scope != detection.PodScope {
		t.Fatalf("collector did not use registered detector: %+v", got)
	}
}

func TestCollectNeverReadyDetection(t *testing.T) {
	node := testNode(true, false)
	node.UID = "node-uid"
	node.CreationTimestamp = metav1.NewTime(ago(31 * time.Minute))
	node.Status.Conditions[0].LastTransitionTime = node.CreationTimestamp
	got := testRegistry(t).CollectDetections(node, nil, nil, testNow)
	if len(got) != 1 || got[0].Detector != neverReady.Name() || got[0].Scope != detection.NodeScope ||
		got[0].NodeUID != node.UID || len(got[0].PodUIDs) != 0 || got[0].Since != node.CreationTimestamp.Time {
		t.Fatalf("incorrect never-ready detection: %+v", got)
	}
}
