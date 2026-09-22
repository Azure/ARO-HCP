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

package nodehealth

import (
	"context"
	"errors"
	"testing"
	"time"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/watch"
	"k8s.io/client-go/kubernetes/fake"
	ktesting "k8s.io/client-go/testing"

	"github.com/Azure/ARO-HCP/mgmt-agent/pkg/controller/nodehealth/detectors"
)

func historyNode() *corev1.Node {
	return &corev1.Node{ObjectMeta: metav1.ObjectMeta{Name: "bootstrap", UID: "uid", ResourceVersion: "1"},
		Status: corev1.NodeStatus{Conditions: []corev1.NodeCondition{{Type: corev1.NodeReady, Status: corev1.ConditionFalse}}}}
}

func TestReadinessHistoryExcludesBriefReady(t *testing.T) {
	node := historyNode()
	created := time.Date(2026, 9, 21, 12, 0, 0, 0, time.UTC)
	node.CreationTimestamp = metav1.NewTime(created)
	node.Labels = map[string]string{detectors.SwiftV2LabelKey: detectors.SwiftV2LabelValue}
	node.Status.Conditions[0].LastTransitionTime = metav1.NewTime(created)
	client := fake.NewClientset(node)
	h := newReadinessHistory(client)
	ctx := context.Background()
	if err := h.observe(ctx, node, true, true); err != nil {
		t.Fatal(err)
	}
	if err := h.Check(node); err != nil {
		t.Fatal(err)
	}
	node.Status.Conditions[0].Status = corev1.ConditionTrue
	node.Status.Conditions[0].LastTransitionTime = metav1.NewTime(created.Add(time.Minute))
	if err := h.observe(ctx, node, false, true); err != nil {
		t.Fatal(err)
	}
	node.Status.Conditions[0].Status = corev1.ConditionFalse
	node.Status.Conditions[0].LastTransitionTime = metav1.NewTime(created.Add(90 * time.Second))
	if err := h.observe(ctx, node, false, true); err != nil {
		t.Fatal(err)
	}
	if err := h.Check(node); err == nil {
		t.Fatal("Ready -> NotReady lost intermediate Ready evidence")
	}
	if decision, fault := detectors.Decide(node, nil, nil, created.Add(31*time.Minute)); decision != detectors.DecisionWedged || fault.DetectorName != "never-ready" {
		t.Fatal("test did not reproduce the detector's lifetime-history ambiguity")
	}
	saved, err := client.CoreV1().Nodes().Get(ctx, node.Name, metav1.GetOptions{})
	if err != nil || saved.Annotations[EverReadyAnnotation] != string(node.UID) {
		t.Fatalf("monotonic marker missing: node=%+v err=%v", saved, err)
	}
	restarted := newReadinessHistory(client)
	if err := restarted.Check(saved); err == nil {
		t.Fatal("restart ignored durable everReady marker")
	}
	node.UID = "replacement"
	node.ResourceVersion = "2"
	if err := h.observe(ctx, node, true, true); err != nil {
		t.Fatal(err)
	}
	if err := h.Check(node); err != nil {
		t.Fatalf("new UID inherited old lifetime: %v", err)
	}
}

func TestReadinessHistoryFailsClosed(t *testing.T) {
	for _, test := range []string{"initial list", "gap", "restart", "disabled", "behind live read", "missing identity", "marker write failed"} {
		t.Run(test, func(t *testing.T) {
			node := historyNode()
			client := fake.NewClientset(node)
			h := newReadinessHistory(client)
			if test == "marker write failed" {
				client.PrependReactor("patch", "nodes", func(ktesting.Action) (bool, runtime.Object, error) {
					return true, nil, errors.New("write rejected")
				})
				node.Status.Conditions[0].Status = corev1.ConditionTrue
			}
			if test == "missing identity" {
				node.UID = ""
			}
			err := h.observe(context.Background(), node, test != "initial list", test != "disabled")
			if (err != nil) != (test == "missing identity" || test == "marker write failed") {
				t.Fatal(err)
			}
			switch test {
			case "gap":
				h.invalidate()
			case "restart":
				h = newReadinessHistory(client)
			case "behind live read":
				node.ResourceVersion = "2"
			case "marker write failed":
				node.Status.Conditions[0].Status = corev1.ConditionFalse
			}
			if err := h.Check(node); err == nil {
				t.Fatal("incomplete or positive readiness evidence authorized deletion")
			}
		})
	}
}

func TestReadinessWatchPreservesTransitionsAndInvalidatesGap(t *testing.T) {
	initial := historyNode()
	client := fake.NewClientset(initial)
	client.PrependReactor("list", "nodes", func(ktesting.Action) (bool, runtime.Object, error) {
		return true, &corev1.NodeList{ListMeta: metav1.ListMeta{ResourceVersion: "10"}, Items: []corev1.Node{*initial}}, nil
	})
	stream := watch.NewRaceFreeFake()
	started := make(chan struct{})
	client.PrependWatchReactor("nodes", func(action ktesting.Action) (bool, watch.Interface, error) {
		if action.(ktesting.WatchAction).GetWatchRestrictions().ResourceVersion != "10" {
			return true, nil, errors.New("watch did not use list resourceVersion")
		}
		close(started)
		return true, stream, nil
	})
	h := newReadinessHistory(client)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- h.session(ctx, func() bool { return true }) }()
	select {
	case <-started:
	case <-ctx.Done():
		t.Fatal("readiness watch did not start")
	}
	if err := h.Check(initial); err == nil {
		t.Fatal("initial list authorized deletion")
	}
	node := historyNode()
	node.Name, node.UID, node.ResourceVersion = "new", "new", "11"
	if err := client.Tracker().Add(node); err != nil {
		t.Fatal(err)
	}
	stream.Add(node.DeepCopy())
	for h.Check(node) != nil {
		select {
		case <-ctx.Done():
			t.Fatal("watch creation never became eligible")
		case <-time.After(time.Millisecond):
		}
	}
	node.Status.Conditions[0].Status = corev1.ConditionTrue
	stream.Modify(node.DeepCopy())
	node.Status.Conditions[0].Status = corev1.ConditionFalse
	node.ResourceVersion = "12"
	stream.Modify(node.DeepCopy())
	for {
		h.mu.RLock()
		record := h.records[node.UID]
		h.mu.RUnlock()
		if record.version == "12" {
			if !record.everReady || h.Check(node) == nil {
				t.Fatal("watch coalesced the intermediate Ready transition")
			}
			break
		}
		select {
		case <-ctx.Done():
			t.Fatal("watch did not consume transitions")
		case <-time.After(time.Millisecond):
		}
	}
	never := historyNode()
	never.Name, never.UID, never.ResourceVersion = "never", "never", "13"
	stream.Add(never)
	for h.Check(never) != nil {
		select {
		case <-ctx.Done():
			t.Fatal("second watch creation never became eligible")
		case <-time.After(time.Millisecond):
		}
	}
	stream.Stop()
	select {
	case err := <-done:
		if err == nil {
			t.Fatal("closed watch was not reported")
		}
	case <-ctx.Done():
		t.Fatal("readiness session did not stop")
	}
	if err := h.Check(never); err == nil {
		t.Fatal("observation gap preserved deletion eligibility")
	}
}
