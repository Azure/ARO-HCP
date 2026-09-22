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
	"fmt"
	"testing"
	"time"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/watch"
	"k8s.io/client-go/kubernetes/fake"
	ktesting "k8s.io/client-go/testing"
	clocktesting "k8s.io/utils/clock/testing"

	"github.com/Azure/ARO-HCP/mgmt-agent/pkg/controller/nodehealth/detectors"
)

func historyNode() *corev1.Node {
	return &corev1.Node{ObjectMeta: metav1.ObjectMeta{Name: "bootstrap", UID: "uid", ResourceVersion: "1",
		CreationTimestamp: metav1.NewTime(historyCreatedAt().Add(time.Minute))},
		Status: corev1.NodeStatus{NodeInfo: corev1.NodeSystemInfo{SystemUUID: "machine"},
			Conditions: []corev1.NodeCondition{{Type: corev1.NodeReady, Status: corev1.ConditionFalse}}}}
}

func historyCreatedAt() time.Time {
	return time.Date(2026, 9, 21, 11, 55, 0, 0, time.UTC)
}

func testReadinessHistory(client *fake.Clientset) *ReadinessHistory {
	clock := clocktesting.NewFakeClock(historyCreatedAt().Add(-time.Minute))
	h := newReadinessHistory(client, clock.Now)
	clock.Step(time.Hour)
	return h
}

func TestReadinessHistoryExcludesBriefReady(t *testing.T) {
	node := historyNode()
	created := time.Date(2026, 9, 21, 12, 0, 0, 0, time.UTC)
	node.CreationTimestamp = metav1.NewTime(created)
	node.Labels = map[string]string{detectors.SwiftV2LabelKey: detectors.SwiftV2LabelValue}
	node.Status.Conditions[0].LastTransitionTime = metav1.NewTime(created)
	client := fake.NewClientset(node)
	h := testReadinessHistory(client)
	ctx := context.Background()
	if err := h.observe(ctx, node, true, true); err != nil {
		t.Fatal(err)
	}
	if err := h.Check(node, historyCreatedAt()); err != nil {
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
	if err := h.Check(node, historyCreatedAt()); err == nil {
		t.Fatal("Ready -> NotReady lost intermediate Ready evidence")
	}
	if decision, fault := detectors.Decide(node, nil, nil, created.Add(31*time.Minute)); decision != detectors.DecisionWedged || fault.DetectorName != "never-ready" {
		t.Fatal("test did not reproduce the detector's lifetime-history ambiguity")
	}
	saved, err := client.CoreV1().Nodes().Get(ctx, node.Name, metav1.GetOptions{})
	if err != nil || saved.Annotations[EverReadyAnnotation] != string(node.UID) {
		t.Fatalf("monotonic marker missing: node=%+v err=%v", saved, err)
	}
	restarted := newReadinessHistory(client, h.clock)
	if err := restarted.Check(saved, historyCreatedAt()); err == nil {
		t.Fatal("restart ignored durable everReady marker")
	}
	node.UID = "replacement"
	node.Status.NodeInfo.SystemUUID = "new-machine"
	node.ResourceVersion = "2"
	if err := h.observe(ctx, node, true, true); err != nil {
		t.Fatal(err)
	}
	if err := h.Check(node, historyCreatedAt()); err != nil {
		t.Fatalf("new VM inherited old lifetime: %v", err)
	}
}

func TestReadinessHistoryFailsClosed(t *testing.T) {
	for _, test := range []string{"initial list", "gap", "restart", "disabled", "behind live read", "missing identity", "marker write failed"} {
		t.Run(test, func(t *testing.T) {
			node := historyNode()
			client := fake.NewClientset(node)
			h := testReadinessHistory(client)
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
				h = newReadinessHistory(client, h.clock)
			case "behind live read":
				node.ResourceVersion = "2"
			case "marker write failed":
				node.Status.Conditions[0].Status = corev1.ConditionFalse
			}
			if err := h.Check(node, historyCreatedAt()); err == nil {
				t.Fatal("incomplete or positive readiness evidence authorized deletion")
			}
		})
	}
}

func TestReadinessHistoryMachineLifetime(t *testing.T) {
	for _, test := range []struct {
		name                                               string
		ready, initial, restart, replacement, writeFailure bool
	}{
		{name: "same VM after Ready", ready: true},
		{name: "same VM never Ready"},
		{name: "same VM initially unknown", initial: true},
		{name: "same VM after failed marker", ready: true, writeFailure: true},
		{name: "same VM after restart while Node absent", ready: true, restart: true},
		{name: "different VM reusing name and provider path", ready: true, replacement: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			node := historyNode()
			node.Spec.ProviderID = "azure://reused-slot"
			client := fake.NewClientset(node)
			h := testReadinessHistory(client)
			if test.ready {
				node.Status.Conditions[0].Status = corev1.ConditionTrue
			}
			if test.writeFailure {
				client.PrependReactor("patch", "nodes", func(ktesting.Action) (bool, runtime.Object, error) {
					return true, nil, errors.New("marker rejected")
				})
			}
			if err := h.observe(context.Background(), node, !test.initial, true); (err != nil) != test.writeFailure {
				t.Fatalf("observe: %v", err)
			}
			h.forget(node)
			if test.restart {
				h = newReadinessHistory(client, h.clock)
			}
			node.UID, node.ResourceVersion = "new-uid", "2"
			node.Status.Conditions[0].Status = corev1.ConditionFalse
			node.Status.NodeInfo.SystemUUID = "MACHINE"
			if test.replacement {
				node.Status.NodeInfo.SystemUUID = "different-machine"
			}
			if err := h.observe(context.Background(), node, true, true); err != nil {
				t.Fatal(err)
			}
			if err := h.Check(node, historyCreatedAt()); (err == nil) != test.replacement {
				t.Fatalf("replacement=%v, eligibility error=%v", test.replacement, err)
			}
		})
	}
}

func TestReadinessHistoryRequiresVerifiedMachineBirth(t *testing.T) {
	for _, test := range []string{"missing birth", "before observation", "at boundary", "future birth",
		"birth after Node", "missing machine", "changed machine", "missing Node creation", "unobserved live identity"} {
		t.Run(test, func(t *testing.T) {
			node := historyNode()
			h := testReadinessHistory(fake.NewClientset(node))
			if err := h.observe(context.Background(), node, true, true); err != nil {
				t.Fatal(err)
			}
			birth := historyCreatedAt()
			switch test {
			case "missing birth":
				birth = time.Time{}
			case "before observation":
				birth = h.since.Add(-time.Second)
			case "at boundary":
				birth = h.since
			case "future birth":
				birth = h.clock().Add(time.Second)
			case "birth after Node":
				birth = node.CreationTimestamp.Add(time.Second)
			case "missing machine":
				node.Status.NodeInfo.SystemUUID = ""
			case "changed machine", "unobserved live identity":
				node.Status.NodeInfo.SystemUUID = "other"
				if test == "changed machine" {
					if err := h.observe(context.Background(), node, false, true); err != nil {
						t.Fatal(err)
					}
				}
			case "missing Node creation":
				node.CreationTimestamp = metav1.Time{}
			}
			if err := h.Check(node, birth); err == nil {
				t.Fatal("incomplete machine lifetime evidence authorized deletion")
			}
		})
	}
}

func TestReadinessHistoryRestartOnlyAdmitsNewMachines(t *testing.T) {
	node := historyNode()
	clock := clocktesting.NewFakeClock(node.CreationTimestamp.Add(time.Hour))
	h := newReadinessHistory(fake.NewClientset(node), clock.Now)
	if err := h.observe(context.Background(), node, true, true); err != nil {
		t.Fatal(err)
	}
	if err := h.Check(node, historyCreatedAt()); err == nil {
		t.Fatal("old VM with no pre-restart Node inherited complete history")
	}
	clock.Step(time.Minute)
	birth := clock.Now()
	clock.Step(time.Minute)
	node.UID, node.ResourceVersion = "fresh", "2"
	node.Status.NodeInfo.SystemUUID = "fresh-machine"
	node.CreationTimestamp = metav1.NewTime(clock.Now())
	if err := h.observe(context.Background(), node, true, true); err != nil {
		t.Fatal(err)
	}
	if err := h.Check(node, birth); err != nil {
		t.Fatalf("fresh VM after restart excluded: %v", err)
	}
	h.invalidate()
	if err := h.Check(node, birth); err == nil {
		t.Fatal("gap retained eligibility")
	}
}

func TestReadinessHistoryUnidentifiedDeletionAndCapacityFailClosed(t *testing.T) {
	for _, test := range []string{"unidentified deletion", "capacity", "clock regression"} {
		t.Run(test, func(t *testing.T) {
			node := historyNode()
			h := testReadinessHistory(fake.NewClientset(node))
			since := h.since
			switch test {
			case "unidentified deletion":
				h.forget(&corev1.Node{})
			case "capacity":
				for i := 0; i < maxMachineBindings; i++ {
					h.machines[fmt.Sprintf("vm-%d", i)] = "old"
				}
			case "clock regression":
				h.clock = func() time.Time { return since.Add(-time.Hour) }
				h.invalidate()
			}
			if err := h.observe(context.Background(), node, true, true); err != nil {
				t.Fatal(err)
			}
			if err := h.Check(node, historyCreatedAt()); err == nil {
				t.Fatal("lost identity evidence authorized deletion")
			}
			if h.since.Before(since) || len(h.machines) > maxMachineBindings {
				t.Fatal("observation boundary regressed or identity storage exceeded limit")
			}
		})
	}
}

func TestReadinessHistoryBirthDuringInitialListIsUnknown(t *testing.T) {
	birth := historyCreatedAt()
	clock := clocktesting.NewFakeClock(birth.Add(-time.Minute))
	client := fake.NewClientset()
	client.PrependReactor("list", "nodes", func(ktesting.Action) (bool, runtime.Object, error) {
		clock.Step(2 * time.Minute)
		return true, &corev1.NodeList{ListMeta: metav1.ListMeta{ResourceVersion: "10"}}, nil
	})
	stream := watch.NewRaceFreeFake()
	started := make(chan struct{})
	client.PrependWatchReactor("nodes", func(ktesting.Action) (bool, watch.Interface, error) {
		close(started)
		return true, stream, nil
	})
	h := newReadinessHistory(client, clock.Now)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- h.session(ctx, func() bool { return true }) }()
	select {
	case <-started:
	case <-ctx.Done():
		t.Fatal("watch did not start")
	}
	node := historyNode()
	stream.Add(node)
	for {
		h.mu.RLock()
		_, found := h.records[node.UID]
		h.mu.RUnlock()
		if found {
			break
		}
		select {
		case <-ctx.Done():
			t.Fatal("watch did not consume registration")
		case <-time.After(time.Millisecond):
		}
	}
	if err := h.Check(node, birth); err == nil {
		t.Fatal("VM born during LIST inherited an unobserved registration lifetime")
	}
	stream.Stop()
	select {
	case <-done:
	case <-ctx.Done():
		t.Fatal("watch did not stop")
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
	clock := clocktesting.NewFakeClock(historyCreatedAt().Add(-time.Minute))
	h := newReadinessHistory(client, clock.Now)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- h.session(ctx, func() bool { return true }) }()
	select {
	case <-started:
	case <-ctx.Done():
		t.Fatal("readiness watch did not start")
	}
	clock.Step(time.Hour)
	if err := h.Check(initial, historyCreatedAt()); err == nil {
		t.Fatal("initial list authorized deletion")
	}
	node := historyNode()
	node.Name, node.UID, node.ResourceVersion = "new", "new", "11"
	node.Status.NodeInfo.SystemUUID = "new-machine"
	if err := client.Tracker().Add(node); err != nil {
		t.Fatal(err)
	}
	stream.Add(node.DeepCopy())
	for h.Check(node, historyCreatedAt()) != nil {
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
			if !record.everReady || h.Check(node, historyCreatedAt()) == nil {
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
	never.Status.NodeInfo.SystemUUID = "never-machine"
	stream.Add(never)
	for h.Check(never, historyCreatedAt()) != nil {
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
	if err := h.Check(never, historyCreatedAt()); err == nil {
		t.Fatal("observation gap preserved deletion eligibility")
	}
}
