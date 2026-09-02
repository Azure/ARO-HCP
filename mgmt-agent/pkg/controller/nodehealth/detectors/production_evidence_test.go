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
	"testing"
	"time"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
)

// This file pins the detector against evidence captured from a real wedged
// production node during the uksouth SWIFT incident (AROSLSRE-1585). The failure
// strings and the node's label and readiness shape are taken verbatim from that
// capture, so a future edit to the signatures or the applicability label that
// would have missed the real incident fails here. Node names, namespaces and
// sandbox IDs are redacted to example values, since no detector logic depends on
// them.

// productionSandboxMessages are verbatim kubelet FailedCreatePodSandBox messages
// from the captured incident, one per signature family the detector matches.
// wantSignature pins which signature classifies each message, so the failure mode
// reported for triage stays discriminating: a signature broadened until it
// swallows a neighbouring family fails here.
var productionSandboxMessages = []struct {
	family        string
	message       string
	wantSignature string
}{
	{
		family:        "route/no-such-network-interface",
		wantSignature: `no such network interface`,
		message:       `Failed to create pod sandbox: rpc error: code = Unknown desc = failed to setup network for sandbox "0000000000000000000000000000000000000000000000000000000000000001": plugin type="azure-vnet" failed (add): failed to create endpoint: SecondaryEndpointClient Error: route ip+net: no such network interface`,
	},
	{
		family:        "network-unreachable",
		wantSignature: `network is unreachable`,
		message:       `Failed to create pod sandbox: rpc error: code = Unknown desc = failed to setup network for sandbox "0000000000000000000000000000000000000000000000000000000000000002": plugin type="azure-vnet" failed (add): failed to create endpoint: network is unreachable`,
	},
	{
		family:        "mtpnc-not-ready",
		wantSignature: `mtpnc is not ready`,
		message:       `Failed to create pod sandbox: rpc error: code = Unknown desc = failed to setup network for sandbox "0000000000000000000000000000000000000000000000000000000000000004": plugin type="azure-vnet" failed (add): failed to add ipam invoker: Failed to get IP address from CNS: network is not ready - mtpnc is not ready`,
	},
	{
		family:        "dhcp-discover-timeout",
		wantSignature: `dhcp discover.*timed out`,
		message:       `Failed to create pod sandbox: rpc error: code = Unknown desc = failed to setup network for sandbox "0000000000000000000000000000000000000000000000000000000000000003": plugin type="azure-vnet" failed (add): failed to create endpoint: network is not ready - failed to issue dhcp discover packet to create mapping in host: timed out waiting for replies`,
	},
}

func TestProductionSandboxMessagesMatchSignatures(t *testing.T) {
	for _, tc := range productionSandboxMessages {
		t.Run(tc.family, func(t *testing.T) {
			idx, ok := swiftVFTeardown.matchSignature(tc.message)
			if !ok {
				t.Fatalf("captured production message was not matched by any signature:\n%s", tc.message)
			}
			if got := swiftVFTeardown.signatures[idx].String(); got != tc.wantSignature {
				t.Errorf("classified as %q, want %q:\n%s", got, tc.wantSignature, tc.message)
			}
		})
	}
}

// productionNodeLabels are the SWIFT-related labels carried by the captured
// wedged node. The node was Ready throughout, which is the premise the whole
// controller rests on: node lifecycle never sees these nodes as broken.
func productionWedgedNode() *corev1.Node {
	return &corev1.Node{
		ObjectMeta: metav1.ObjectMeta{
			Name: "aks-userswift0-00000000-vmss000000",
			Labels: map[string]string{
				"kubernetes.azure.com/podnetwork-swiftv2-enabled":       "true",
				"kubernetes.azure.com/podnetwork-multi-tenancy-enabled": "true",
				"kubernetes.azure.com/podnetwork-type":                  "vnet",
				"kubernetes.azure.com/podnetwork-name":                  "aks-net",
			},
		},
		Status: corev1.NodeStatus{
			Conditions: []corev1.NodeCondition{{Type: corev1.NodeReady, Status: corev1.ConditionTrue}},
		},
	}
}

func TestProductionNodeIsADetectionCandidate(t *testing.T) {
	node := productionWedgedNode()
	if !swiftVFTeardown.Applies(node) {
		t.Fatal("the captured wedged production node must be a detection candidate")
	}
	if !isNodeReady(node) {
		t.Fatal("the captured node was Ready while wedged; the Ready premise must hold")
	}
}

// TestProductionCompletedPodsDoNotCount replays the pod population found on the
// captured node. Alongside the wedge, the node carried four completed Job and
// CronJob pods (featuregate-generator and olm-collect-profiles), all in phase
// Succeeded with PodReadyToStartContainers=False because their sandboxes were
// torn down when they finished, days earlier.
//
// Those pods are ordinary cluster turnover, not evidence of a wedge. If they
// counted toward the floor, routine CronJob churn on any SWIFT node would drift
// the detector toward firing on its own.
func TestProductionCompletedPodsDoNotCount(t *testing.T) {
	now := time.Date(2026, 7, 27, 9, 0, 43, 0, time.UTC)
	ns := "ocm-example-hosted-cluster"

	completed := []struct {
		name string
		at   time.Time
	}{
		{"featuregate-generator-655jv", time.Date(2026, 7, 22, 11, 27, 34, 0, time.UTC)},
		{"olm-collect-profiles-29749045-588fv", time.Date(2026, 7, 25, 1, 25, 4, 0, time.UTC)},
		{"olm-collect-profiles-29750485-zkpm5", time.Date(2026, 7, 26, 1, 25, 4, 0, time.UTC)},
		{"olm-collect-profiles-29751925-8hhjp", time.Date(2026, 7, 27, 1, 25, 36, 0, time.UTC)},
	}

	var pods []*corev1.Pod
	var events []*corev1.Event
	for _, c := range completed {
		uid := types.UID("uid-" + c.name)
		pods = append(pods, &corev1.Pod{
			ObjectMeta: metav1.ObjectMeta{Namespace: ns, Name: c.name, UID: uid},
			Status: corev1.PodStatus{
				Phase: corev1.PodSucceeded,
				Conditions: []corev1.PodCondition{{
					Type:               corev1.PodReadyToStartContainers,
					Status:             corev1.ConditionFalse,
					LastTransitionTime: metav1.NewTime(c.at),
				}},
			},
		})
		// Worst case: each completed pod also has a correlating in-window sandbox
		// failure Event, so only the terminal phase can rule it out.
		events = append(events, &corev1.Event{
			ObjectMeta:     metav1.ObjectMeta{Namespace: ns, Name: c.name + ".evt"},
			Reason:         reasonFailedCreatePodSandBox,
			Message:        productionSandboxMessages[0].message,
			InvolvedObject: corev1.ObjectReference{Kind: "Pod", Namespace: ns, Name: c.name, UID: uid},
			Source:         corev1.EventSource{Host: "aks-userswift0-00000000-vmss000000"},
			LastTimestamp:  metav1.NewTime(now.Add(-1 * time.Minute)),
		})
	}

	snap := swiftVFTeardown.Evaluate(events, pods, now)
	if snap.Pods.SustainedCount != 0 {
		t.Errorf("completed pods counted toward the floor: SustainedCount = %d, want 0", snap.Pods.SustainedCount)
	}
	if snap.Pods.FailureCount != 0 {
		t.Errorf("completed pods counted as failures: FailureCount = %d, want 0", snap.Pods.FailureCount)
	}

	// The node must not be declared wedged on completed-pod turnover alone.
	if got, _ := Decide(productionWedgedNode(), events, pods, now); got == DecisionWedged {
		t.Error("completed Job and CronJob turnover must not produce a wedge")
	}
}

// productionWedgeEvidence builds the failing side of the captured incident:
// pending pods whose sandbox never came up, each stuck well past the dwell, with
// a correlating in-window kubelet Event.
func productionWedgeEvidence(now time.Time) ([]*corev1.Event, []*corev1.Pod) {
	const ns = "openshift-monitoring"
	const host = "aks-userswift0-00000000-vmss000000"

	var pods []*corev1.Pod
	var events []*corev1.Event
	for i, name := range []string{"ovnkube-node-x1", "prometheus-k8s-0", "router-default-abc"} {
		uid := types.UID("uid-" + name)
		pods = append(pods, &corev1.Pod{
			ObjectMeta: metav1.ObjectMeta{Namespace: ns, Name: name, UID: uid},
			Status: corev1.PodStatus{
				Phase: corev1.PodPending,
				Conditions: []corev1.PodCondition{{
					Type:               corev1.PodReadyToStartContainers,
					Status:             corev1.ConditionFalse,
					LastTransitionTime: metav1.NewTime(now.Add(-30 * time.Minute)),
				}},
			},
		})
		events = append(events, &corev1.Event{
			ObjectMeta:     metav1.ObjectMeta{Namespace: ns, Name: name + ".evt"},
			Reason:         reasonFailedCreatePodSandBox,
			Message:        productionSandboxMessages[i%len(productionSandboxMessages)].message,
			InvolvedObject: corev1.ObjectReference{Kind: "Pod", Namespace: ns, Name: name, UID: uid},
			Source:         corev1.EventSource{Host: host},
			LastTimestamp:  metav1.NewTime(now.Add(-1 * time.Minute)),
		})
	}
	return events, pods
}

// TestProductionStaleSuccessesDoNotSuppressDetection guards the success signal's
// window scoping, which the captured incident shows is load-bearing.
//
// The wedged node was still running 46 non-host-network pods whose
// PodReadyToStartContainers condition read True: they were started long before
// the VF disappeared and kept running afterwards, because an established sandbox
// survives the teardown. The freshest of those transitions was over 8 hours old.
//
// So "has this node ever started a pod successfully" is always true on a
// long-lived node and would suppress detection forever. Only a success inside
// the detector window is evidence the node can still attach a NIC right now.
func TestProductionStaleSuccessesDoNotSuppressDetection(t *testing.T) {
	now := time.Date(2026, 7, 27, 9, 0, 43, 0, time.UTC)
	node := productionWedgedNode()
	events, pods := productionWedgeEvidence(now)

	// Freshest success actually observed on the captured node: 500.6 minutes old,
	// far outside the 10 minute window. It is still sitting in the LIST, exactly
	// as it was on the real node, because the pod is still running.
	staleSuccess := now.Add(-500*time.Minute - 36*time.Second)
	got, snap := Decide(node, events, append(pods, withSwiftNIC(startedPod("long-running", staleSuccess, false))), now)
	if got != DecisionWedged {
		t.Fatalf("stale success suppressed detection of the real wedge: Decide = %v (%s)", got, snap.ReasonString())
	}
	if snap.Pods.RecentSuccess {
		t.Error("an 8 hour old success must not count as recent")
	}

	// A success inside the window is real proof the NIC still attaches, so the
	// same evidence must no longer read as a hard wedge. Decide reports recovery
	// as Healthy and returns an empty snapshot on that path, so the decision is
	// the assertion here.
	freshSuccess := now.Add(-2 * time.Minute)
	if got, _ = Decide(node, events, append(pods, withSwiftNIC(startedPod("just-started", freshSuccess, false))), now); got != DecisionHealthy {
		t.Errorf("a success inside the window must rule out a hard wedge: Decide = %v, want %v", got, DecisionHealthy)
	}
}

// TestFutureDatedSuccessDoesNotSuppressDetection covers clock skew between the
// kubelet that stamps a pod condition and the controller that reads it.
//
// The success timestamp comes from a Pod condition's lastTransitionTime, written
// by the kubelet using its own node's clock. If that clock runs ahead, the
// controller records a success dated in the future. A naive "how long ago was
// it" comparison reads a future timestamp as an arbitrarily small age, so it
// always looks like a fresh success and permanently suppresses wedge detection.
//
// This is the same failure mode as a stale success suppressing detection, but it
// does not age out on its own, so it is worth pinning separately.
func TestFutureDatedSuccessDoesNotSuppressDetection(t *testing.T) {
	now := time.Date(2026, 7, 27, 9, 0, 43, 0, time.UTC)
	node := productionWedgedNode()
	events, pods := productionWedgeEvidence(now)

	// A success stamped an hour into the future by a skewed kubelet clock. It is
	// not evidence the node can attach a NIC now, so it must not rule out a wedge.
	future := now.Add(1 * time.Hour)
	if got, _ := Decide(node, events, append(pods, withSwiftNIC(startedPod("skewed", future, false))), now); got != DecisionWedged {
		t.Errorf("a future-dated success suppressed detection of a real wedge: Decide = %v, want %v", got, DecisionWedged)
	}
}

// TestProductionHardWedgeShapeFires pins the failure floor against the pod count
// the captured incident actually produced.
//
// The other tests in this file replay the wedge with three stuck pods, which is
// comfortably over any floor and so cannot detect a floor set too high. The
// distinguishing property of a hard wedge is the opposite of a storm: once the
// node cannot build a sandbox, the scheduler stops getting new pods to a running
// state there, so a very small number of pods retry indefinitely instead of many
// pods failing once each.
//
// Kusto for the captured uksouth node over the incident, counting distinct pods
// rather than events, shows exactly that:
//
//	node                                distinct pods   events   span
//	aks-userswft2-40171262-vmss000001               2     1193   58.3h
//
// One sampled hour inside the wedge, 2026-07-27 09:00 to 10:00, is the shape
// replayed below: 2 distinct failing pods, no fresh sandbox success at all.
//
// The same query over flapping (not wedged) nodes returns 30 to 70 distinct pods
// per node, with successes throughout, so the flap population is ruled out by
// dwell and requireZeroSuccess rather than by the floor. That is why the floor
// can sit at 2 without letting flaps through: a flapping pod gets its sandbox on
// retry, so it never stays stuck for the dwell.
func TestProductionHardWedgeShapeFires(t *testing.T) {
	now := time.Date(2026, 7, 27, 9, 30, 0, 0, time.UTC)
	const ns = "openshift-monitoring"
	const host = "aks-userswift0-00000000-vmss000000"

	var pods []*corev1.Pod
	var events []*corev1.Event
	for i, name := range []string{"ovnkube-node-x1", "prometheus-k8s-0"} {
		uid := types.UID("uid-" + name)
		pods = append(pods, &corev1.Pod{
			ObjectMeta: metav1.ObjectMeta{Namespace: ns, Name: name, UID: uid},
			Status: corev1.PodStatus{
				Phase: corev1.PodPending,
				Conditions: []corev1.PodCondition{{
					Type:               corev1.PodReadyToStartContainers,
					Status:             corev1.ConditionFalse,
					LastTransitionTime: metav1.NewTime(now.Add(-45 * time.Minute)),
				}},
			},
		})
		events = append(events, &corev1.Event{
			ObjectMeta:     metav1.ObjectMeta{Namespace: ns, Name: name + ".evt"},
			Reason:         reasonFailedCreatePodSandBox,
			Message:        productionSandboxMessages[i%len(productionSandboxMessages)].message,
			InvolvedObject: corev1.ObjectReference{Kind: "Pod", Namespace: ns, Name: name, UID: uid},
			Source:         corev1.EventSource{Host: host},
			LastTimestamp:  metav1.NewTime(now.Add(-1 * time.Minute)),
		})
	}

	snap := swiftVFTeardown.Evaluate(events, pods, now)
	if snap.SustainedCount != 2 {
		t.Fatalf("captured wedge shape did not produce 2 sustained pods: SustainedCount = %d", snap.SustainedCount)
	}
	if snap.RecentSuccess {
		t.Fatal("captured wedge shape must have no fresh sandbox success")
	}
	if got, _ := Decide(productionWedgedNode(), events, pods, now); got != DecisionWedged {
		t.Errorf("the captured hard wedge did not fire: Decide = %v, want %v; the failure floor is above the pod count a real wedge produces", got, DecisionWedged)
	}
}
