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

package repositorycleanup

import (
	"context"
	"encoding/json"
	"fmt"
	"reflect"
	"testing"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/dynamic/fake"
	clienttesting "k8s.io/client-go/testing"
	"k8s.io/client-go/tools/cache"

	"github.com/Azure/ARO-HCP/mgmt-agent/pkg/controller/repositorycleanup/storage"
)

func fakeClient(t *testing.T, o Observations) *fake.FakeDynamicClient {
	t.Helper()
	listKinds := map[schema.GroupVersionResource]string{}
	var objects []runtime.Object
	for _, gvr := range RequiredResources() {
		listKinds[gvr] = gvr.Resource + "List"
		for _, obj := range o.Objects[gvr] {
			objects = append(objects, obj.DeepCopy())
		}
	}
	if o.Cleanup != nil {
		objects = append(objects, o.Cleanup.DeepCopy())
	}
	return fake.NewSimpleDynamicClientWithCustomListKinds(runtime.NewScheme(), listKinds, objects...)
}

// Only status merge is emulated here; ownership semantics are covered with the
// real field manager in plan_test.go, rather than a sprawling fake API server.
func statusReactor(t *testing.T, client *fake.FakeDynamicClient) {
	t.Helper()
	client.PrependReactor("patch", "backuprepositorycleanups", func(action clienttesting.Action) (bool, runtime.Object, error) {
		a := action.(clienttesting.PatchAction)
		if a.GetSubresource() != "status" {
			return false, nil, nil
		}
		options := a.(clienttesting.PatchActionImpl).GetPatchOptions()
		if a.GetPatchType() != types.ApplyPatchType || options.FieldManager != StatusFieldManager || options.Force != nil {
			t.Fatalf("unexpected status patch options: %#v", options)
		}
		var patch map[string]interface{}
		if err := json.Unmarshal(a.GetPatch(), &patch); err != nil {
			t.Fatal(err)
		}
		raw, err := client.Tracker().Get(a.GetResource(), Namespace, a.GetName())
		if err != nil {
			return true, nil, err
		}
		u := raw.(*unstructured.Unstructured).DeepCopy()
		u.Object["status"] = patch["status"]
		u.SetResourceVersion(u.GetResourceVersion() + "1")
		err = client.Tracker().Update(a.GetResource(), u, Namespace)
		return true, u, err
	})
}

func TestAdapterSweepsAndRechecksCompletedTombstone(t *testing.T) {
	o := retired(t)
	client := fakeClient(t, o)
	statusReactor(t, client)
	sweeps := 0
	c := &Controller{client: client, deletePrefix: func(_ context.Context, target storage.Target) error {
		sweeps++
		if target.Prefix != "base/kopia/"+testVolume+"/" {
			t.Fatalf("unexpected target: %#v", target)
		}
		return nil
	}}
	k := key{Resource: BackupRepositoryCleanupsGVR, Name: o.Cleanup.GetName()}
	for i := 0; i < 2; i++ {
		delay, err := c.reconcile(context.Background(), k)
		if err != nil || delay == 0 {
			t.Fatalf("reconcile %d: delay=%v error=%v", i, delay, err)
		}
	}
	if sweeps != 2 {
		t.Fatalf("Complete status incorrectly skipped a sweep: %d", sweeps)
	}
	u, err := client.Resource(BackupRepositoryCleanupsGVR).Namespace(Namespace).Get(context.Background(), k.Name, metav1.GetOptions{})
	if err != nil {
		t.Fatal(err)
	}
	conditions, _, _ := unstructured.NestedSlice(u.Object, "status", "conditions")
	if len(conditions) != 1 || conditions[0].(map[string]interface{})["status"] != "True" {
		t.Fatalf("expected Complete status: %#v", conditions)
	}
	job, _ := terminalMaintenance()
	delete(job.Object, "status")
	if err := client.Tracker().Create(JobsGVR, job, Namespace); err != nil {
		t.Fatal(err)
	}
	if _, err := c.reconcile(context.Background(), k); err != nil {
		t.Fatal(err)
	}
	if sweeps != 2 {
		t.Fatal("late active maintenance Job did not block sweep")
	}
}

func TestAdapterFreshReplanPreventsDeletion(t *testing.T) {
	o := retired(t)
	client := fakeClient(t, o)
	statusReactor(t, client)
	lists := 0
	client.PrependReactor("list", "hostedclusters", func(action clienttesting.Action) (bool, runtime.Object, error) {
		lists++
		if lists < 2 {
			return false, nil, nil
		}
		hc := object(HostedClustersGVR, "HostedCluster", "replacement")
		hc.SetNamespace(HostedClusterNamespace(testVolume))
		return true, &unstructured.UnstructuredList{Items: []unstructured.Unstructured{*hc}}, nil
	})
	c := &Controller{client: client, deletePrefix: func(context.Context, storage.Target) error {
		t.Fatal("must not sweep after HC appeared on fresh observation")
		return nil
	}}
	if _, err := c.reconcile(context.Background(), key{Resource: BackupRepositoryCleanupsGVR, Name: o.Cleanup.GetName()}); err != nil {
		t.Fatal(err)
	}
}

func TestAdapterSweepFailureIsNotComplete(t *testing.T) {
	o := retired(t)
	client := fakeClient(t, o)
	statusReactor(t, client)
	c := &Controller{client: client, deletePrefix: func(context.Context, storage.Target) error { return fmt.Errorf("Azure unavailable") }}
	if _, err := c.reconcile(context.Background(), key{Resource: BackupRepositoryCleanupsGVR, Name: o.Cleanup.GetName()}); err == nil {
		t.Fatal("expected Azure failure")
	}
	u, err := client.Resource(BackupRepositoryCleanupsGVR).Namespace(Namespace).Get(context.Background(), o.Cleanup.GetName(), metav1.GetOptions{})
	if err != nil {
		t.Fatal(err)
	}
	conditions, _, _ := unstructured.NestedSlice(u.Object, "status", "conditions")
	if conditions[0].(map[string]interface{})["status"] != "False" {
		t.Fatal("failed sweep marked complete")
	}
}

func TestRepeatedSweepFailureDoesNotGenerateStatusEvents(t *testing.T) {
	o := retired(t)
	client := fakeClient(t, o)
	statusReactor(t, client)
	c := &Controller{client: client, deletePrefix: func(context.Context, storage.Target) error { return fmt.Errorf("Azure 403") }}
	k := key{Resource: BackupRepositoryCleanupsGVR, Name: o.Cleanup.GetName()}
	for attempt := 0; attempt < 4; attempt++ {
		client.ClearActions()
		if _, err := c.reconcile(context.Background(), k); err == nil {
			t.Fatal("expected failed sweep")
		}
		patches := 0
		for _, action := range client.Actions() {
			if action.GetVerb() == "patch" {
				patches++
			}
		}
		if attempt > 0 && patches != 0 {
			t.Fatalf("retry %d generated %d status events, bypassing backoff", attempt, patches)
		}
	}
	u, err := client.Resource(BackupRepositoryCleanupsGVR).Namespace(Namespace).Get(context.Background(), k.Name, metav1.GetOptions{})
	if err != nil {
		t.Fatal(err)
	}
	conditions, _, _ := unstructured.NestedSlice(u.Object, "status", "conditions")
	if conditions[0].(map[string]interface{})["reason"] != SweepFailed {
		t.Fatalf("expected stable failure reason: %#v", conditions)
	}
	c.deletePrefix = func(context.Context, storage.Target) error { return nil }
	if _, err := c.reconcile(context.Background(), k); err != nil {
		t.Fatal(err)
	}
	u, err = client.Resource(BackupRepositoryCleanupsGVR).Namespace(Namespace).Get(context.Background(), k.Name, metav1.GetOptions{})
	if err != nil {
		t.Fatal(err)
	}
	conditions, _, _ = unstructured.NestedSlice(u.Object, "status", "conditions")
	if conditions[0].(map[string]interface{})["status"] != "True" {
		t.Fatal("successful retry must replace failure with completion")
	}
}

func TestAdapterRepoApplyAndDeleteOptions(t *testing.T) {
	for _, finalized := range []bool{false, true} {
		t.Run(fmt.Sprint(finalized), func(t *testing.T) {
			o := fixture(t)
			o.Cleanup = nil
			if finalized {
				owned(o.Repository, RepositoryFieldManager)
			}
			o.Objects[BackupRepositoriesGVR] = []unstructured.Unstructured{*o.Repository}
			client := fakeClient(t, o)
			mutated := false
			client.PrependReactor("patch", "backuprepositories", func(action clienttesting.Action) (bool, runtime.Object, error) {
				mutated = true
				a := action.(clienttesting.PatchAction)
				options := a.(clienttesting.PatchActionImpl).GetPatchOptions()
				var patch unstructured.Unstructured
				if err := json.Unmarshal(a.GetPatch(), &patch.Object); err != nil {
					t.Fatal(err)
				}
				if a.GetPatchType() != types.ApplyPatchType || options.FieldManager != RepositoryFieldManager || options.Force != nil || !reflect.DeepEqual(patch.GetFinalizers(), []string{Finalizer}) {
					t.Fatalf("unsafe finalizer apply: %#v %#v", options, patch.Object)
				}
				return true, &patch, nil
			})
			client.PrependReactor("delete", "backuprepositories", func(action clienttesting.Action) (bool, runtime.Object, error) {
				mutated = true
				options := action.(clienttesting.DeleteAction).GetDeleteOptions()
				if !finalized || options.Preconditions == nil || *options.Preconditions.UID != o.Repository.GetUID() || *options.Preconditions.ResourceVersion != o.Repository.GetResourceVersion() {
					t.Fatalf("unsafe repository delete: %#v", options)
				}
				return true, nil, nil
			})
			c := &Controller{client: client}
			if _, err := c.reconcile(context.Background(), key{Resource: BackupRepositoriesGVR, Name: o.Repository.GetName()}); err != nil {
				t.Fatal(err)
			}
			if !mutated {
				t.Fatal("expected finalizer attach or preconditioned deletion")
			}
		})
	}
}

func TestRoutingFanoutAtDequeue(t *testing.T) {
	o := fixture(t)
	informers := map[schema.GroupVersionResource]cache.SharedIndexInformer{}
	for _, gvr := range RequiredResources() {
		informers[gvr] = cache.NewSharedIndexInformer(&cache.ListWatch{}, &unstructured.Unstructured{}, 0, cache.Indexers{})
	}
	c, err := NewController(fakeClient(t, o), func(context.Context, storage.Target) error { return nil }, informers)
	if err != nil {
		t.Fatal(err)
	}
	defer c.queue.ShutDown()
	hc := object(HostedClustersGVR, "HostedCluster", "hc")
	hc.SetNamespace(HostedClusterNamespace(testVolume))
	c.enqueue(HostedClustersGVR, hc)
	// Populate target indexes after the event but before dequeue: fanout must
	// consult the current index, not freeze an event-time eligibility decision.
	if err := informers[BackupRepositoriesGVR].GetIndexer().Add(o.Repository); err != nil {
		t.Fatal(err)
	}
	if err := informers[BackupRepositoryCleanupsGVR].GetIndexer().Add(o.Cleanup); err != nil {
		t.Fatal(err)
	}
	k, _ := c.queue.Get()
	c.queue.Done(k)
	if k.Relation != "hc/"+hc.GetNamespace() {
		t.Fatalf("HC handler did not enqueue routing key: %#v", k)
	}
	if err := c.fanout(k.Relation); err != nil {
		t.Fatal(err)
	}
	if c.queue.Len() != 2 {
		t.Fatalf("expected repo and intent fanout, got %d", c.queue.Len())
	}
	for c.queue.Len() > 0 {
		k, _ := c.queue.Get()
		c.queue.Done(k)
	}
	job := object(JobsGVR, "Job", "unrelated")
	c.enqueue(JobsGVR, job)
	if c.queue.Len() != 0 {
		t.Fatal("unrelated Job caused fanout")
	}
	job, _ = terminalMaintenance()
	c.enqueue(JobsGVR, job)
	for c.queue.Len() > 0 {
		k, _ := c.queue.Get()
		c.queue.Done(k)
		if k.Relation == "all" {
			t.Fatal("maintenance event caused global rescan")
		}
	}
}

func TestRepositoryEventDoesNotFanOutSharedBSLOrHCSiblings(t *testing.T) {
	o := fixture(t)
	informers := map[schema.GroupVersionResource]cache.SharedIndexInformer{}
	for _, gvr := range RequiredResources() {
		informers[gvr] = cache.NewSharedIndexInformer(&cache.ListWatch{}, &unstructured.Unstructured{}, 0, cache.Indexers{})
	}
	c, err := NewController(fakeClient(t, o), func(context.Context, storage.Target) error { return nil }, informers)
	if err != nil {
		t.Fatal(err)
	}
	defer c.queue.ShutDown()
	for _, name := range []string{"repository", "sibling"} {
		repo := o.Repository.DeepCopy()
		repo.SetName(name)
		if name == "sibling" {
			if err := unstructured.SetNestedField(repo.Object, HostedClusterNamespace(testVolume), "spec", "volumeNamespace"); err != nil {
				t.Fatal(err)
			}
		}
		if err := informers[BackupRepositoriesGVR].GetIndexer().Add(repo); err != nil {
			t.Fatal(err)
		}
	}
	c.enqueue(BackupRepositoriesGVR, o.Repository)
	for c.queue.Len() > 0 {
		k, _ := c.queue.Get()
		c.queue.Done(k)
		if k.Relation != "" {
			if k.Relation != "repo/repository" && k.Relation != "maintenance/repository" && k.Relation != "volume/"+testVolume {
				t.Fatalf("repository event emitted broad route: %#v", k)
			}
			if err := c.fanout(k.Relation); err != nil {
				t.Fatal(err)
			}
		} else if k.Name != "repository" {
			t.Fatalf("repository update fanned out to HC sibling: %#v", k)
		}
	}
}

func TestLivePreflightAvoidsDependencyLists(t *testing.T) {
	for _, scenario := range []string{"preserved", "unsupported", "unsupported-bsl", "live-hc", "cleanup-waits-for-repo"} {
		t.Run(scenario, func(t *testing.T) {
			o := fixture(t)
			owned(o.Repository, RepositoryFieldManager)
			k := key{Resource: BackupRepositoriesGVR, Name: o.Repository.GetName()}
			switch scenario {
			case "preserved":
				o.Repository.SetAnnotations(map[string]string{PreserveBackupAnnotation: ""})
			case "unsupported":
				if err := unstructured.SetNestedField(o.Repository.Object, "restic", "spec", "repositoryType"); err != nil {
					t.Fatal(err)
				}
			case "unsupported-bsl":
				if err := unstructured.SetNestedField(o.Objects[BackupStorageLocationsGVR][0].Object, "aws", "spec", "provider"); err != nil {
					t.Fatal(err)
				}
			case "cleanup-waits-for-repo":
				k = key{Resource: BackupRepositoryCleanupsGVR, Name: o.Cleanup.GetName()}
			}
			o.Objects[BackupRepositoriesGVR] = []unstructured.Unstructured{*o.Repository}
			client := fakeClient(t, o)
			client.PrependReactor("list", "*", func(action clienttesting.Action) (bool, runtime.Object, error) {
				if scenario != "live-hc" || action.GetResource() != HostedClustersGVR {
					t.Fatalf("cheap preflight unexpectedly listed %s", action.GetResource())
				}
				return true, &unstructured.UnstructuredList{Items: []unstructured.Unstructured{*object(HostedClustersGVR, "HostedCluster", "hc")}}, nil
			})
			c := &Controller{client: client}
			observed, err := c.observe(context.Background(), k)
			if err != nil {
				t.Fatal(err)
			}
			if observed.DependenciesComplete {
				t.Fatal("partial preflight must not claim complete observations")
			}
			p := plan(k, observed)
			noDestruction(t, p)
			if p.Condition == nil {
				t.Fatal("cheap preflight must explain why retirement is blocked")
			}
		})
	}
}

func TestIntentCollisionOnFreshReadPreventsApplyAndRelease(t *testing.T) {
	o := fixture(t)
	collision := o.Cleanup.DeepCopy()
	if err := unstructured.SetNestedField(collision.Object, "different-container", "spec", "storage", "container"); err != nil {
		t.Fatal(err)
	}
	owned(o.Repository, RepositoryFieldManager)
	now := metav1.NewTime(o.Now)
	o.Repository.SetDeletionTimestamp(&now)
	o.Objects[BackupRepositoriesGVR] = []unstructured.Unstructured{*o.Repository}
	o.Cleanup = nil
	client := fakeClient(t, o)
	gets := 0
	client.PrependReactor("get", "backuprepositorycleanups", func(action clienttesting.Action) (bool, runtime.Object, error) {
		gets++
		if gets == 1 {
			return false, nil, nil
		}
		return true, collision, nil
	})
	client.PrependReactor("patch", "*", func(action clienttesting.Action) (bool, runtime.Object, error) {
		t.Fatalf("collision must prevent both intent apply and repository release: %#v", action)
		return true, nil, nil
	})
	c := &Controller{client: client}
	if _, err := c.reconcile(context.Background(), key{Resource: BackupRepositoriesGVR, Name: o.Repository.GetName()}); err != nil {
		t.Fatal(err)
	}
	if gets < 2 {
		t.Fatal("intent was not rechecked before initial apply")
	}
}
