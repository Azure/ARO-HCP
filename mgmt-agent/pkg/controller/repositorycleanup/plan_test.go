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
	"crypto/sha3"
	"encoding/json"
	"fmt"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/managedfields"
	"k8s.io/apimachinery/pkg/util/managedfields/managedfieldstest"
	"k8s.io/kube-openapi/pkg/validation/spec"

	"github.com/Azure/ARO-HCP/mgmt-agent/pkg/controller/repositorycleanup/storage"
)

const testVolume = "ocm-arohcpint-0123456789abcdef0123456789abcdef-cluster"

func object(gvr schema.GroupVersionResource, kind, name string) *unstructured.Unstructured {
	u := baseApply(gvr, kind, name)
	u.SetUID(types.UID(name + "-uid"))
	u.SetResourceVersion("10")
	return u
}

func owned(u *unstructured.Unstructured, manager string) {
	u.SetFinalizers([]string{Finalizer, "another.example/finalizer"})
	fields, _ := json.Marshal(map[string]interface{}{"f:metadata": map[string]interface{}{"f:finalizers": map[string]interface{}{`v:"` + Finalizer + `"`: map[string]interface{}{}}}})
	u.SetManagedFields([]metav1.ManagedFieldsEntry{{Manager: manager, Operation: metav1.ManagedFieldsOperationApply, APIVersion: u.GetAPIVersion(), FieldsType: "FieldsV1", FieldsV1: &metav1.FieldsV1{Raw: fields}}})
}

func fixture(t *testing.T) Observations {
	t.Helper()
	r := object(BackupRepositoriesGVR, "BackupRepository", "repository")
	r.Object["spec"] = map[string]interface{}{"repositoryType": "kopia", "volumeNamespace": testVolume, "backupStorageLocation": "azure"}
	bsl := object(BackupStorageLocationsGVR, "BackupStorageLocation", "azure")
	bsl.Object["spec"] = map[string]interface{}{"provider": "azure", "config": map[string]interface{}{"storageAccount": "account"}, "objectStorage": map[string]interface{}{"bucket": "container", "prefix": "base"}}
	o := Observations{Repository: r, Objects: map[schema.GroupVersionResource][]unstructured.Unstructured{}, DependenciesComplete: true, Now: time.Unix(1234, 0)}
	for _, gvr := range RequiredResources() {
		o.Objects[gvr] = []unstructured.Unstructured{}
	}
	o.Objects[BackupRepositoriesGVR] = []unstructured.Unstructured{*r}
	o.Objects[BackupStorageLocationsGVR] = []unstructured.Unstructured{*bsl}
	target, err := resolve(r, o.Objects)
	if err != nil {
		t.Fatal(err)
	}
	o.Cleanup = intentApply(BackupRepositoryCleanupSpec{Repository: identity(r), Storage: target}).Object
	o.Cleanup.SetUID("cleanup-uid")
	o.Cleanup.SetResourceVersion("20")
	owned(o.Cleanup, CleanupFieldManager)
	return o
}

func retired(t *testing.T) Observations {
	o := fixture(t)
	o.Repository = nil
	o.Objects[BackupRepositoriesGVR] = nil
	return o
}

func terminalMaintenance() (*unstructured.Unstructured, *unstructured.Unstructured) {
	job := object(JobsGVR, "Job", "maintenance")
	job.SetLabels(map[string]string{repoNameLabel: RepositoryLabel("repository")})
	container := map[string]interface{}{"name": "velero-repo-maintenance-container", "command": []interface{}{"/velero"}, "args": []interface{}{"repo-maintenance", "--repo-name=" + testVolume, "--repo-type=kopia", "--backup-storage-location=azure"}}
	podSpec := map[string]interface{}{"containers": []interface{}{container}}
	job.Object["spec"] = map[string]interface{}{"template": map[string]interface{}{"spec": podSpec}}
	job.Object["status"] = map[string]interface{}{"conditions": []interface{}{map[string]interface{}{"type": "Complete", "status": "True"}}}
	pod := object(PodsGVR, "Pod", "maintenance-pod")
	pod.SetLabels(job.GetLabels())
	pod.SetOwnerReferences([]metav1.OwnerReference{{APIVersion: "batch/v1", Kind: "Job", Name: job.GetName(), UID: job.GetUID()}})
	pod.Object["spec"] = podSpec
	pod.Object["status"] = map[string]interface{}{"phase": "Succeeded"}
	return job, pod
}

func noDestruction(t *testing.T, p Plan) {
	t.Helper()
	if len(p.Deletes) != 0 || p.DeletePrefix != nil {
		t.Fatalf("unexpected destructive plan: %#v", p)
	}
	for _, a := range p.Applies {
		if a.Subresource == "" && !hasFinalizer(a.Object) {
			t.Fatalf("unexpected finalizer release: %#v", a)
		}
	}
}

func TestNamespaceAndMaintenanceLabel(t *testing.T) {
	base := "ocm-arohcpint-0123456789abcdef0123456789abcdef"
	for _, tc := range []struct{ input, want string }{
		{base, base}, {testVolume, base}, {base + "-a-b", base},
		{base + "-bad.", ""}, {base + "-", ""}, {base + "-" + strings.Repeat("a", 30), ""},
		{"ocm-1bad-0123456789abcdef0123456789abcdef", ""}, {"other", ""},
	} {
		if got := HostedClusterNamespace(tc.input); got != tc.want {
			t.Errorf("namespace %q: got %q want %q", tc.input, got, tc.want)
		}
	}
	long := strings.Repeat("repository", 8)
	if got := RepositoryLabel(long); got != fmt.Sprintf("%x", sha3.Sum224([]byte(long))) {
		t.Fatalf("incorrect maintenance hash: %s", got)
	}
	if RepositoryLabel("short") != "short" {
		t.Fatal("short names should not be hashed")
	}
}

func TestRepositoryLifecycle(t *testing.T) {
	o := fixture(t)
	o.Cleanup = nil
	p := PlanRepository(o)
	if len(p.Applies) != 1 || !reflect.DeepEqual(p.Applies[0].Object.GetFinalizers(), []string{Finalizer}) || p.Applies[0].FieldManager != RepositoryFieldManager {
		t.Fatalf("expected SSA of only own finalizer: %#v", p)
	}
	owned(o.Repository, RepositoryFieldManager)
	p = PlanRepository(o)
	if !reflect.DeepEqual(p.Deletes, []Delete{deletion(o.Repository, BackupRepositoriesGVR)}) {
		t.Fatalf("expected UID/RV guarded repo deletion: %#v", p)
	}
	now := metav1.NewTime(o.Now)
	o.Repository.SetDeletionTimestamp(&now)
	p = PlanRepository(o)
	if len(p.Applies) != 1 || p.Applies[0].Resource != BackupRepositoryCleanupsGVR || !hasFinalizer(p.Applies[0].Object) {
		t.Fatalf("expected durable intent apply: %#v", p)
	}
	if len(p.Applies[0].Object.GetOwnerReferences()) != 0 {
		t.Fatal("intent must have no owner references")
	}
	o.Cleanup = p.Applies[0].Object.DeepCopy()
	p = PlanRepository(o)
	if len(p.Applies) != 1 || p.Applies[0].Resource != BackupRepositoriesGVR || hasFinalizer(p.Applies[0].Object) {
		t.Fatalf("expected SSA omission releasing only repo finalizer: %#v", p)
	}
	if _, exists, _ := unstructured.NestedFieldNoCopy(p.Applies[0].Object.Object, "metadata", "finalizers"); exists {
		t.Fatal("release must omit finalizers entirely")
	}
	if !slices.Contains(o.Repository.GetFinalizers(), "another.example/finalizer") {
		t.Fatal("planner mutated input finalizers")
	}
}

func TestRepositoryProtectionGates(t *testing.T) {
	for _, tc := range []struct {
		name   string
		change func(*Observations)
	}{
		{"HC including terminating", func(o *Observations) {
			hc := object(HostedClustersGVR, "HostedCluster", "hc")
			now := metav1.NewTime(o.Now)
			hc.SetDeletionTimestamp(&now)
			o.HostedClusters = []unstructured.Unstructured{*hc}
		}},
		{"preserved", func(o *Observations) {
			o.Repository.SetAnnotations(map[string]string{PreserveBackupAnnotation: "false"})
		}},
		{"unknown BSL", func(o *Observations) { o.Objects[BackupStorageLocationsGVR] = nil }},
		{"unsupported repo", func(o *Observations) {
			_ = unstructured.SetNestedField(o.Repository.Object, "restic", "spec", "repositoryType")
		}},
		{"foreign finalizer manager", func(o *Observations) { owned(o.Repository, "other") }},
		{"intent target collision", func(o *Observations) {
			now := metav1.NewTime(o.Now)
			o.Repository.SetDeletionTimestamp(&now)
			_ = unstructured.SetNestedField(o.Cleanup.Object, "different", "spec", "storage", "container")
		}},
		{"intent extra spec field", func(o *Observations) {
			now := metav1.NewTime(o.Now)
			o.Repository.SetDeletionTimestamp(&now)
			_ = unstructured.SetNestedField(o.Cleanup.Object, "unexpected", "spec", "extra")
		}},
		{"intent terminating", func(o *Observations) {
			now := metav1.NewTime(o.Now)
			o.Repository.SetDeletionTimestamp(&now)
			o.Cleanup.SetDeletionTimestamp(&now)
		}},
		{"intent no finalizer", func(o *Observations) {
			now := metav1.NewTime(o.Now)
			o.Repository.SetDeletionTimestamp(&now)
			o.Cleanup.SetFinalizers(nil)
		}},
		{"external deletion no finalizer", func(o *Observations) {
			now := metav1.NewTime(o.Now)
			o.Repository.SetDeletionTimestamp(&now)
			o.Repository.SetFinalizers(nil)
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			o := fixture(t)
			owned(o.Repository, RepositoryFieldManager)
			tc.change(&o)
			p := PlanRepository(o)
			noDestruction(t, p)
		})
	}
}

func TestCleanupConsumerGates(t *testing.T) {
	for _, tc := range []struct {
		name   string
		change func(*Observations)
	}{
		{"replacement any UID", func(o *Observations) {
			o.Repository = object(BackupRepositoriesGVR, "BackupRepository", "repository")
			o.Repository.SetUID("replacement")
		}},
		{"same physical prefix alias", func(o *Observations) {
			f := fixture(t)
			alias := f.Repository.DeepCopy()
			alias.SetName("alias")
			alias.SetUID("alias-uid")
			o.Objects[BackupRepositoriesGVR] = []unstructured.Unstructured{*alias}
		}},
		{"missing alias BSL", func(o *Observations) {
			f := fixture(t)
			alias := f.Repository.DeepCopy()
			alias.SetName("alias")
			_ = unstructured.SetNestedField(alias.Object, "missing", "spec", "backupStorageLocation")
			o.Objects[BackupRepositoriesGVR] = []unstructured.Unstructured{*alias}
		}},
		{"unknown Backup namespaces", func(o *Observations) {
			o.Objects[BackupsGVR] = []unstructured.Unstructured{*object(BackupsGVR, "Backup", "b")}
		}},
		{"wildcard Backup", func(o *Observations) {
			b := object(BackupsGVR, "Backup", "b")
			_ = unstructured.SetNestedStringSlice(b.Object, []string{"*"}, "spec", "includedNamespaces")
			o.Objects[BackupsGVR] = []unstructured.Unstructured{*b}
		}},
		{"matching Schedule", func(o *Observations) {
			s := object(SchedulesGVR, "Schedule", "s")
			_ = unstructured.SetNestedStringSlice(s.Object, []string{testVolume}, "spec", "template", "includedNamespaces")
			o.Objects[SchedulesGVR] = []unstructured.Unstructured{*s}
		}},
		{"active Restore", func(o *Observations) {
			o.Objects[RestoresGVR] = []unstructured.Unstructured{*object(RestoresGVR, "Restore", "r")}
		}},
		{"preserved tombstone", func(o *Observations) { o.Cleanup.SetAnnotations(map[string]string{PreserveBackupAnnotation: ""}) }},
		{"missing observation", func(o *Observations) { delete(o.Objects, PodsGVR) }},
		{"prefix namespace mismatch", func(o *Observations) {
			_ = unstructured.SetNestedField(o.Cleanup.Object, "other", "spec", "repository", "volumeNamespace")
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			o := retired(t)
			tc.change(&o)
			p := PlanCleanup(o)
			noDestruction(t, p)
			if p.Condition == nil || p.Condition.Status != metav1.ConditionFalse {
				t.Fatalf("expected blocked condition: %#v", p)
			}
		})
	}
	for _, gvr := range []schema.GroupVersionResource{DataUploadsGVR, DataDownloadsGVR, PodVolumeBackupsGVR, PodVolumeRestoresGVR} {
		t.Run(gvr.Resource, func(t *testing.T) {
			o := retired(t)
			u := object(gvr, "Transfer", "transfer")
			u.Object["spec"] = map[string]interface{}{"sourceNamespace": testVolume}
			o.Objects[gvr] = []unstructured.Unstructured{*u}
			noDestruction(t, PlanCleanup(o))
			_ = unstructured.SetNestedField(u.Object, "Completed", "status", "phase")
			o.Objects[gvr] = []unstructured.Unstructured{*u}
			if PlanCleanup(o).DeletePrefix == nil {
				t.Fatal("terminal transfer should allow sweep")
			}
		})
	}
}

func TestMaintenanceDrainAndLateJobs(t *testing.T) {
	o := retired(t)
	job, pod := terminalMaintenance()
	o.Objects[JobsGVR] = []unstructured.Unstructured{*job}
	o.Objects[PodsGVR] = []unstructured.Unstructured{*pod}
	_ = unstructured.SetNestedField(pod.Object, "Running", "status", "phase")
	now := metav1.NewTime(o.Now)
	pod.SetDeletionTimestamp(&now)
	o.Objects[PodsGVR] = []unstructured.Unstructured{*pod}
	noDestruction(t, PlanCleanup(o))
	_ = unstructured.SetNestedField(pod.Object, "Succeeded", "status", "phase")
	pod.SetDeletionTimestamp(nil)
	o.Objects[PodsGVR] = []unstructured.Unstructured{*pod}
	p := PlanCleanup(o)
	if len(p.Deletes) != 1 || p.Deletes[0].Resource != JobsGVR || p.DeletePrefix != nil {
		t.Fatalf("must delete completed Job first: %#v", p)
	}
	o.Objects[JobsGVR] = nil
	p = PlanCleanup(o)
	if len(p.Deletes) != 1 || p.Deletes[0].Resource != PodsGVR || p.DeletePrefix != nil {
		t.Fatalf("must remove terminal orphan Pod before sweep: %#v", p)
	}
	o.Objects[PodsGVR] = nil
	p = PlanCleanup(o)
	if p.DeletePrefix == nil || p.Condition.Reason != DeletingBlobs {
		t.Fatalf("expected sweep: %#v", p)
	}
	o.Swept = p.DeletePrefix
	p = PlanCleanup(o)
	if p.Condition.Status != metav1.ConditionTrue || p.RequeueAfter != 10*time.Minute {
		t.Fatalf("expected monitored tombstone: %#v", p)
	}
	o.Cleanup.Object["status"] = p.Applies[0].Object.Object["status"]
	o.Swept = nil
	p = PlanCleanup(o)
	if p.DeletePrefix == nil || len(p.Applies) != 0 {
		t.Fatalf("Complete status must neither skip sweep nor generate status churn: %#v", p)
	}
	delete(job.Object, "status")
	o.Objects[JobsGVR] = []unstructured.Unstructured{*job}
	p = PlanCleanup(o)
	noDestruction(t, p)
	if p.Condition.Reason != DrainingMaintenance {
		t.Fatalf("late Job must reopen draining: %#v", p)
	}
}

func TestMaintenanceWrongShapeAndStatus(t *testing.T) {
	for _, change := range []func(*unstructured.Unstructured, *unstructured.Unstructured){
		func(j, p *unstructured.Unstructured) { j.Object["status"] = map[string]interface{}{"failed": int64(1)} },
		func(j, p *unstructured.Unstructured) {
			_ = unstructured.SetNestedSlice(j.Object, []interface{}{}, "spec", "template", "spec", "containers")
		},
		func(j, p *unstructured.Unstructured) {
			_ = unstructured.SetNestedSlice(p.Object, []interface{}{}, "spec", "containers")
		},
		func(j, p *unstructured.Unstructured) {
			_ = unstructured.SetNestedField(p.Object, "Unknown", "status", "phase")
		},
	} {
		o := retired(t)
		job, pod := terminalMaintenance()
		change(job, pod)
		o.Objects[JobsGVR], o.Objects[PodsGVR] = []unstructured.Unstructured{*job}, []unstructured.Unstructured{*pod}
		noDestruction(t, PlanCleanup(o))
	}
}

func TestExplicitTombstoneDeletionRequiresSweep(t *testing.T) {
	o := retired(t)
	now := metav1.NewTime(o.Now)
	o.Cleanup.SetDeletionTimestamp(&now)
	p := PlanCleanup(o)
	if p.DeletePrefix == nil {
		t.Fatal("terminating intent requires fresh sweep")
	}
	o.Swept = p.DeletePrefix
	p = PlanCleanup(o)
	if len(p.Applies) != 1 || p.Applies[0].Subresource != "status" {
		t.Fatalf("must persist completion before releasing finalizer: %#v", p)
	}
	o.Cleanup.Object["status"] = p.Applies[0].Object.Object["status"]
	p = PlanCleanup(o)
	if len(p.Applies) != 1 || p.Applies[0].Subresource != "" || hasFinalizer(p.Applies[0].Object) {
		t.Fatalf("expected finalizer omission: %#v", p)
	}
	if !reflect.DeepEqual(p.Applies[0].Object.Object["spec"], o.Cleanup.Object["spec"]) {
		t.Fatal("must retain immutable spec when releasing cleanup finalizer")
	}
}

// Use the real structured-merge field manager with metadata.finalizers modeled
// as Kubernetes' set. The dynamic fake alone does not implement SSA ownership.
func testFieldManager(t *testing.T, gvr schema.GroupVersionResource, kind string) managedfieldstest.TestFieldManager {
	t.Helper()
	var s spec.Schema
	definition := fmt.Sprintf(`{"type":"object","x-kubernetes-group-version-kind":[{"group":%q,"version":%q,"kind":%q}],"properties":{"apiVersion":{"type":"string"},"kind":{"type":"string"},"metadata":{"type":"object","properties":{"name":{"type":"string"},"namespace":{"type":"string"},"uid":{"type":"string"},"resourceVersion":{"type":"string"},"finalizers":{"type":"array","x-kubernetes-list-type":"set","items":{"type":"string"}}}},"spec":{"type":"object","x-kubernetes-preserve-unknown-fields":true}}}`, gvr.Group, gvr.Version, kind)
	if err := json.Unmarshal([]byte(definition), &s); err != nil {
		t.Fatal(err)
	}
	converter, err := managedfields.NewTypeConverter(map[string]*spec.Schema{"test.Object": &s}, true)
	if err != nil {
		t.Fatal(err)
	}
	return managedfieldstest.NewTestFieldManager(converter, gvr.GroupVersion().WithKind(kind))
}

func TestSSAFinalizerOwnership(t *testing.T) {
	for _, tc := range []struct {
		gvr           schema.GroupVersionResource
		kind, manager string
	}{
		{BackupRepositoriesGVR, "BackupRepository", RepositoryFieldManager},
		{BackupRepositoryCleanupsGVR, "BackupRepositoryCleanup", CleanupFieldManager},
	} {
		t.Run(tc.kind, func(t *testing.T) {
			f := testFieldManager(t, tc.gvr, tc.kind)
			other := baseApply(tc.gvr, tc.kind, "object")
			other.SetFinalizers([]string{"another.example/finalizer"})
			if err := f.Apply(other, "other", false); err != nil {
				t.Fatal(err)
			}
			u := object(tc.gvr, tc.kind, "object")
			u.Object["spec"] = map[string]interface{}{"immutable": "value"}
			add := finalizerApply(u, tc.gvr, tc.manager, true)
			if err := f.Apply(add.Object, tc.manager, false); err != nil {
				t.Fatal(err)
			}
			live := f.Live().(*unstructured.Unstructured)
			if !hasFinalizer(live) || !slices.Contains(live.GetFinalizers(), "another.example/finalizer") {
				t.Fatalf("SSA must preserve foreign finalizers: %v", live.GetFinalizers())
			}
			ours, another := finalizerOwnership(live, tc.manager)
			if !ours || another {
				t.Fatalf("incorrect managedFields interpretation: %v", live.GetManagedFields())
			}
			release := finalizerApply(live, tc.gvr, tc.manager, false)
			if err := f.Apply(release.Object, tc.manager, false); err != nil {
				t.Fatal(err)
			}
			live = f.Live().(*unstructured.Unstructured)
			if !reflect.DeepEqual(live.GetFinalizers(), []string{"another.example/finalizer"}) {
				t.Fatalf("release affected foreign finalizers: %v", live.GetFinalizers())
			}
			if tc.gvr == BackupRepositoryCleanupsGVR && !reflect.DeepEqual(live.Object["spec"], u.Object["spec"]) {
				t.Fatal("cleanup release lost immutable spec")
			}
		})
	}
}

func TestSSAAdoptedFinalizerFailsClosed(t *testing.T) {
	f := testFieldManager(t, BackupRepositoriesGVR, "BackupRepository")
	u := baseApply(BackupRepositoriesGVR, "BackupRepository", "repository")
	u.SetFinalizers([]string{Finalizer})
	if err := f.Apply(u, "original-manager", false); err != nil {
		t.Fatal(err)
	}
	if err := f.Apply(u, RepositoryFieldManager, false); err != nil {
		t.Fatal(err)
	}
	live := f.Live().(*unstructured.Unstructured)
	ours, other := finalizerOwnership(live, RepositoryFieldManager)
	if !ours || !other {
		t.Fatalf("must detect co-owned persisted finalizer: %#v", live.GetManagedFields())
	}
	o := fixture(t)
	owned(o.Repository, RepositoryFieldManager)
	o.Repository.SetManagedFields(live.GetManagedFields())
	noDestruction(t, PlanRepository(o))
	// A prior erroneous use of our dedicated manager must not authorize
	// omission of another finalizer that it happens to own.
	u.SetFinalizers([]string{Finalizer, "another.example/finalizer"})
	if err := f.Apply(u, RepositoryFieldManager, false); err != nil {
		t.Fatal(err)
	}
	_, other = finalizerOwnership(f.Live().(*unstructured.Unstructured), RepositoryFieldManager)
	if !other {
		t.Fatal("must reject a manager that owns unrelated finalizers")
	}
}

func TestPartialObservationsCannotAuthorizeDestruction(t *testing.T) {
	o := fixture(t)
	owned(o.Repository, RepositoryFieldManager)
	o.DependenciesComplete = false
	p := PlanRepository(o)
	noDestruction(t, p)
	if !p.ObserveDependencies {
		t.Fatal("eligible repository must request complete dependencies")
	}
	o = retired(t)
	o.DependenciesComplete = false
	p = PlanCleanup(o)
	noDestruction(t, p)
	if !p.ObserveDependencies {
		t.Fatal("cleanup must request complete dependencies")
	}
	delete(o.Objects, JobsGVR)
	o.DependenciesComplete = true
	noDestruction(t, PlanCleanup(o))
}

func TestRepositoryBlockedReasonWithoutIntent(t *testing.T) {
	o := fixture(t)
	o.Cleanup = nil
	o.Repository.SetAnnotations(map[string]string{PreserveBackupAnnotation: ""})
	p := PlanRepository(o)
	if p.Condition == nil || p.Condition.Reason != Blocked || p.Condition.Message != "Cleanup is preserved by annotation" || len(p.Applies) != 0 {
		t.Fatalf("missing observable reason or unexpected status apply: %#v", p)
	}
}

func TestRootPrefixIdentityUsesStorageValidation(t *testing.T) {
	o := retired(t)
	s, err := intentSpec(o.Cleanup)
	if err != nil {
		t.Fatal(err)
	}
	s.Storage.Prefix = "kopia/" + testVolume + "/"
	u := intentApply(s).Object
	_, err = intentSpec(u)
	// Storage owns whether root prefixes are supported. Identity validation must
	// not independently reject that shape when the storage implementation allows it.
	if fmt.Sprint(err) != fmt.Sprint(storage.Validate(s.Storage)) {
		t.Fatalf("controller rejected valid namespace binding independently of storage: %v", err)
	}
}

func TestForeignMaintenanceStorageAssociation(t *testing.T) {
	for _, tc := range []struct {
		name                 string
		activeJob, activePod bool
		bslChange            string
		blocked              bool
	}{
		{name: "retained terminal jobs and pods", bslChange: "same"},
		{name: "terminal foreign resources with missing BSL", bslChange: "missing"},
		{name: "active same prefix", activeJob: true, bslChange: "same", blocked: true},
		{name: "terminal job running pod", activePod: true, bslChange: "same", blocked: true},
		{name: "active missing BSL", activeJob: true, bslChange: "missing", blocked: true},
		{name: "orphan running pod missing BSL", activePod: true, bslChange: "missing", blocked: true},
		{name: "active disjoint account", activeJob: true, activePod: true, bslChange: "account", blocked: true},
		{name: "active disjoint container", activeJob: true, activePod: true, bslChange: "container", blocked: true},
		{name: "active disjoint base", activeJob: true, activePod: true, bslChange: "base", blocked: true},
		{name: "terminal job running pod disjoint account", activePod: true, bslChange: "account", blocked: true},
		{name: "terminal job running pod disjoint container", activePod: true, bslChange: "container", blocked: true},
		{name: "terminal job running pod disjoint base", activePod: true, bslChange: "base", blocked: true},
		{name: "terminal disjoint account", bslChange: "account"},
		{name: "terminal disjoint container", bslChange: "container"},
		{name: "terminal disjoint base", bslChange: "base"},
		{name: "active ambiguous endpoint", activeJob: true, bslChange: "endpoint", blocked: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			o := retired(t)
			job, pod := terminalMaintenance()
			job.SetLabels(map[string]string{repoNameLabel: "foreign"})
			pod.SetLabels(job.GetLabels())
			if tc.activeJob {
				delete(job.Object, "status")
			}
			if tc.activePod {
				if err := unstructured.SetNestedField(pod.Object, "Running", "status", "phase"); err != nil {
					t.Fatal(err)
				}
			}
			bsl := &o.Objects[BackupStorageLocationsGVR][0]
			var path []string
			var value string
			switch tc.bslChange {
			case "missing":
				o.Objects[BackupStorageLocationsGVR] = nil
			case "account":
				path, value = []string{"spec", "config", "storageAccount"}, "differentaccount"
			case "container":
				path, value = []string{"spec", "objectStorage", "bucket"}, "different-container"
			case "base":
				path, value = []string{"spec", "objectStorage", "prefix"}, "different/base"
			case "endpoint":
				path, value = []string{"spec", "config", "storageAccountURI"}, "https://untrusted.invalid"
			}
			if path != nil {
				if err := unstructured.SetNestedField(bsl.Object, value, path...); err != nil {
					t.Fatal(err)
				}
			}
			o.Objects[JobsGVR] = []unstructured.Unstructured{*job}
			o.Objects[PodsGVR] = []unstructured.Unstructured{*pod}
			p := PlanCleanup(o)
			if len(p.Deletes) != 0 {
				t.Fatalf("must never delete foreign maintenance: %#v", p.Deletes)
			}
			if tc.blocked {
				noDestruction(t, p)
			} else if p.DeletePrefix == nil {
				t.Fatalf("foreign maintenance unnecessarily blocked sweep: %#v", p.Condition)
			}
		})
	}
}

func TestUnrelatedUnsupportedRepositoryDisjointness(t *testing.T) {
	for _, repoType := range []string{"restic", "kopia"} {
		for _, location := range []string{"account", "container", "base", "same", "missing", "unknown-endpoint", "unknown-config", "invalid-account"} {
			t.Run(repoType+"/"+location, func(t *testing.T) {
				o := retired(t)
				repo := object(BackupRepositoriesGVR, "BackupRepository", "unrelated")
				repo.Object["spec"] = map[string]interface{}{"repositoryType": repoType, "volumeNamespace": "non-aro-namespace", "backupStorageLocation": "azure"}
				o.Objects[BackupRepositoriesGVR] = []unstructured.Unstructured{*repo}
				bsl := &o.Objects[BackupStorageLocationsGVR][0]
				var path []string
				var value string
				switch location {
				case "account":
					path, value = []string{"spec", "config", "storageAccount"}, "otheraccount"
				case "container":
					path, value = []string{"spec", "objectStorage", "bucket"}, "other-container"
				case "base":
					path, value = []string{"spec", "objectStorage", "prefix"}, "other/base"
				case "missing":
					o.Objects[BackupStorageLocationsGVR] = nil
				case "unknown-endpoint":
					path, value = []string{"spec", "config", "storageAccountURI"}, "https://otheraccount.blob.core.windows.net"
				case "unknown-config":
					path, value = []string{"spec", "config", "endpoint"}, "https://otheraccount.blob.core.windows.net"
				case "invalid-account":
					path, value = []string{"spec", "config", "storageAccount"}, "../../account"
				}
				if path != nil {
					if err := unstructured.SetNestedField(bsl.Object, value, path...); err != nil {
						t.Fatal(err)
					}
				}
				p := PlanCleanup(o)
				if location == "account" || location == "container" || location == "base" {
					if p.DeletePrefix == nil {
						t.Fatalf("disjoint unsupported repo blocks cleanup: %#v", p.Condition)
					}
				} else {
					noDestruction(t, p)
				}
			})
		}
	}
}

func TestTwoTombstonesDrainOnlyTheirOwnRetainedJobs(t *testing.T) {
	o := retired(t)
	first, err := intentSpec(o.Cleanup)
	if err != nil {
		t.Fatal(err)
	}
	second := first
	second.Repository.Name, second.Repository.UID = "second", "second-uid"
	second.Storage.Container = "second-container"
	secondBSL := o.Objects[BackupStorageLocationsGVR][0].DeepCopy()
	secondBSL.SetName("second-bsl")
	if err := unstructured.SetNestedField(secondBSL.Object, second.Storage.Container, "spec", "objectStorage", "bucket"); err != nil {
		t.Fatal(err)
	}
	o.Objects[BackupStorageLocationsGVR] = append(o.Objects[BackupStorageLocationsGVR], *secondBSL)
	for _, s := range []BackupRepositoryCleanupSpec{first, second} {
		job, pod := terminalMaintenance()
		job.SetName(s.Repository.Name + "-job")
		job.SetUID(types.UID(job.GetName()))
		job.SetLabels(map[string]string{repoNameLabel: RepositoryLabel(s.Repository.Name)})
		pod.SetName(s.Repository.Name + "-pod")
		pod.SetLabels(job.GetLabels())
		pod.SetOwnerReferences([]metav1.OwnerReference{{APIVersion: "batch/v1", Kind: "Job", Name: job.GetName(), UID: job.GetUID()}})
		if s == second {
			containers, _, _ := unstructured.NestedSlice(job.Object, "spec", "template", "spec", "containers")
			container := containers[0].(map[string]interface{})
			args, _, _ := unstructured.NestedStringSlice(container, "args")
			args[len(args)-1] = "--backup-storage-location=second-bsl"
			if err := unstructured.SetNestedStringSlice(container, args, "args"); err != nil {
				t.Fatal(err)
			}
			if err := unstructured.SetNestedSlice(job.Object, containers, "spec", "template", "spec", "containers"); err != nil {
				t.Fatal(err)
			}
			if err := unstructured.SetNestedSlice(pod.Object, containers, "spec", "containers"); err != nil {
				t.Fatal(err)
			}
		}
		o.Objects[JobsGVR] = append(o.Objects[JobsGVR], *job)
		o.Objects[PodsGVR] = append(o.Objects[PodsGVR], *pod)
	}
	for _, s := range []BackupRepositoryCleanupSpec{first, second} {
		o.Cleanup = intentApply(s).Object
		owned(o.Cleanup, CleanupFieldManager)
		p := PlanCleanup(o)
		if len(p.Deletes) != 1 || p.Deletes[0].Name != s.Repository.Name+"-job" {
			t.Fatalf("tombstone %s deadlocked or selected foreign job: %#v", s.Repository.Name, p)
		}
	}
}
