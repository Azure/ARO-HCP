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

package acmuninstall

import (
	"context"
	"fmt"
	"testing"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	dynamicfake "k8s.io/client-go/dynamic/fake"
	kubernetesfake "k8s.io/client-go/kubernetes/fake"
	clienttesting "k8s.io/client-go/testing"
)

func TestManagedClusterCleanupBeforeMCE(t *testing.T) {
	ctx := context.Background()
	local := &unstructured.Unstructured{Object: map[string]interface{}{
		"apiVersion": "cluster.open-cluster-management.io/v1",
		"kind":       "ManagedCluster",
		"metadata":   map[string]interface{}{"name": "local-cluster"},
	}}
	orphan := local.DeepCopy()
	orphan.SetName("orphan")
	orphan.SetFinalizers([]string{"cluster.open-cluster-management.io/resource-cleanup"})
	terminating := local.DeepCopy()
	terminating.SetName("terminating")
	now := metav1.Now()
	terminating.SetDeletionTimestamp(&now)
	client := dynamicfake.NewSimpleDynamicClientWithCustomListKinds(runtime.NewScheme(),
		map[schema.GroupVersionResource]string{ManagedClustersGVR: "ManagedClusterList"},
		local, orphan, terminating)
	// Keep the deleted object present, like an API server waiting for finalizers.
	client.PrependReactor("delete", "managedclusters", func(action clienttesting.Action) (bool, runtime.Object, error) {
		name := action.(clienttesting.DeleteAction).GetName()
		if name != "orphan" {
			t.Errorf("unexpected deletion of %q", name)
		}
		obj := orphan.DeepCopy()
		obj.SetDeletionTimestamp(&now)
		return true, nil, client.Tracker().Update(ManagedClustersGVR, obj, "")
	})
	c, err := NewController(kubernetesfake.NewClientset(), client)
	if err != nil {
		t.Fatal(err)
	}
	defer c.queue.ShutDown()
	c.prereqsSatisfied = true
	mceCalled := false
	c.steps[1].run = func(context.Context) (stepResult, error) {
		mceCalled = true
		return stepResult{}, fmt.Errorf("stop after reaching MCE")
	}
	delay, err := c.reconcile(ctx)
	if err != nil || delay != recheckInterval || mceCalled {
		t.Fatalf("cleanup did not block MCE: delay=%v err=%v mceCalled=%v", delay, err, mceCalled)
	}
	// A subsequent reconciliation must wait without reissuing deletes.
	client.ClearActions()
	if _, err := c.reconcile(ctx); err != nil {
		t.Fatal(err)
	}
	for _, action := range client.Actions() {
		if action.GetVerb() == "delete" {
			t.Fatal("reissued delete for terminating ManagedCluster")
		}
	}
	obj, err := client.Resource(ManagedClustersGVR).Get(ctx, "orphan", metav1.GetOptions{})
	if err != nil || len(obj.GetFinalizers()) != 1 {
		t.Fatalf("finalizers were not preserved: object=%v err=%v", obj, err)
	}
	for _, name := range []string{"orphan", "terminating"} {
		if err := client.Tracker().Delete(ManagedClustersGVR, "", name); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := c.reconcile(ctx); err == nil || !mceCalled {
		t.Fatal("MCE step was not reached after non-local ManagedClusters disappeared")
	}
	if _, err := client.Resource(ManagedClustersGVR).Get(ctx, "local-cluster", metav1.GetOptions{}); err != nil {
		t.Fatalf("local-cluster was not preserved: %v", err)
	}
}
