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
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/dynamic"
	"k8s.io/utils/ptr"

	"sigs.k8s.io/controller-runtime/pkg/envtest"

	"github.com/Azure/ARO-HCP/mgmt-agent/pkg/controller/repositorycleanup/storage"
)

// Uses the chart's actual CRD, not a duplicated schema or fake field manager.
// Run with KUBEBUILDER_ASSETS pointing to kube-apiserver and etcd (see make envtest-setup).
func TestRepositoryCleanupAPI(t *testing.T) {
	assets := os.Getenv("KUBEBUILDER_ASSETS")
	if assets == "" {
		t.Skip("API-server tests require envtest assets: set KUBEBUILDER_ASSETS=$(make -s envtest-setup) from the repository root")
	}
	for _, binary := range []string{"kube-apiserver", "etcd"} {
		if info, err := os.Stat(filepath.Join(assets, binary)); err != nil || info.IsDir() || info.Mode()&0111 == 0 {
			t.Fatalf("KUBEBUILDER_ASSETS must contain executable %s: %v", binary, err)
		}
		// Do not allow per-binary environment overrides to change the selected assets.
		t.Setenv("TEST_ASSET_"+strings.ToUpper(strings.ReplaceAll(binary, "-", "_")), filepath.Join(assets, binary))
	}
	testEnv := &envtest.Environment{
		UseExistingCluster:    ptr.To(false), // Never consult the default kubeconfig, even if USE_EXISTING_CLUSTER is set.
		BinaryAssetsDirectory: assets,
		CRDDirectoryPaths:     []string{filepath.Join("..", "..", "..", "deploy", "templates", "backuprepositorycleanup-crd.yaml")},
		ErrorIfCRDPathMissing: true,
	}
	// Only metadata semantics and live GET/LIST are needed from these dependencies.
	// Jobs and Pods use the built-in APIs; BackupRepository is a minimal Velero stub.
	for gvr, kind := range map[schema.GroupVersionResource]string{
		BackupRepositoriesGVR: "BackupRepository", BackupStorageLocationsGVR: "BackupStorageLocation",
		BackupsGVR: "Backup", SchedulesGVR: "Schedule", RestoresGVR: "Restore",
		DataUploadsGVR: "DataUpload", DataDownloadsGVR: "DataDownload",
		PodVolumeBackupsGVR: "PodVolumeBackup", PodVolumeRestoresGVR: "PodVolumeRestore",
		HostedClustersGVR: "HostedCluster",
	} {
		testEnv.CRDs = append(testEnv.CRDs, &apiextensionsv1.CustomResourceDefinition{
			ObjectMeta: metav1.ObjectMeta{Name: gvr.Resource + "." + gvr.Group},
			Spec: apiextensionsv1.CustomResourceDefinitionSpec{
				Group: gvr.Group, Scope: apiextensionsv1.NamespaceScoped,
				Names: apiextensionsv1.CustomResourceDefinitionNames{Plural: gvr.Resource, Singular: strings.ToLower(kind), Kind: kind, ListKind: kind + "List"},
				Versions: []apiextensionsv1.CustomResourceDefinitionVersion{{
					Name: gvr.Version, Served: true, Storage: true,
					Schema: &apiextensionsv1.CustomResourceValidation{OpenAPIV3Schema: &apiextensionsv1.JSONSchemaProps{
						Type: "object", Properties: map[string]apiextensionsv1.JSONSchemaProps{
							"spec":   {Type: "object", XPreserveUnknownFields: ptr.To(true)},
							"status": {Type: "object", XPreserveUnknownFields: ptr.To(true)},
						},
					}},
				}},
			},
		})
	}
	t.Cleanup(func() {
		if err := testEnv.Stop(); err != nil {
			t.Errorf("stop envtest: %v", err)
		}
	})
	config, err := testEnv.Start()
	if err != nil {
		t.Fatalf("start local envtest and install CRDs: %v", err)
	}
	config.Timeout = 10 * time.Second
	client, err := dynamic.NewForConfig(config)
	if err != nil {
		t.Fatal(err)
	}
	ctx := t.Context()
	ns := &unstructured.Unstructured{Object: map[string]interface{}{
		"apiVersion": "v1", "kind": "Namespace", "metadata": map[string]interface{}{"name": Namespace},
	}}
	if _, err := client.Resource(schema.GroupVersionResource{Version: "v1", Resource: "namespaces"}).Create(ctx, ns, metav1.CreateOptions{}); err != nil {
		t.Fatal(err)
	}

	t.Run("CRDValidation", func(t *testing.T) { testCleanupAPIValidation(t, client) })
	t.Run("SSAFinalizersAndUIDPreconditions", func(t *testing.T) { testCleanupAPIFinalizers(t, client) })
	t.Run("DurableHandoffAndTombstone", func(t *testing.T) { testCleanupAPILifecycle(t, client) })
}

const apiVolume = "ocm-arohcpint-0123456789abcdef0123456789abcdef-cluster"

func apiCleanupSpec(uid types.UID) BackupRepositoryCleanupSpec {
	return BackupRepositoryCleanupSpec{
		Repository: RepositoryIdentity{Name: "api-repository", UID: uid, VolumeNamespace: apiVolume},
		Storage:    storage.Target{AccountURL: "https://account123.blob.core.windows.net", Container: "backups", Prefix: "base/kopia/" + apiVolume + "/"},
	}
}

func apiGet(t *testing.T, client dynamic.Interface, gvr schema.GroupVersionResource, name string) *unstructured.Unstructured {
	t.Helper()
	obj, err := client.Resource(gvr).Namespace(Namespace).Get(t.Context(), name, metav1.GetOptions{})
	if err != nil {
		t.Fatalf("GET %s/%s: %v", gvr.Resource, name, err)
	}
	return obj
}

func apiApply(t *testing.T, c *Controller, a Apply) {
	t.Helper()
	if err := c.apply(t.Context(), a); err != nil {
		t.Fatalf("apply %s/%s (%s): %v", a.Resource.Resource, a.Object.GetName(), a.FieldManager, err)
	}
}

func testCleanupAPIValidation(t *testing.T, client dynamic.Interface) {
	ctx := t.Context()
	resource := client.Resource(BackupRepositoryCleanupsGVR).Namespace(Namespace)
	c := &Controller{client: client}
	for _, path := range []string{
		"spec", "spec.repository", "spec.storage", "spec.repository.name", "spec.repository.uid",
		"spec.repository.volumeNamespace", "spec.storage.accountURL", "spec.storage.container", "spec.storage.prefix",
	} {
		t.Run("required/"+path, func(t *testing.T) {
			obj := intentApply(apiCleanupSpec("required")).Object
			unstructured.RemoveNestedField(obj.Object, strings.Split(path, ".")...)
			_, err := resource.Create(ctx, obj, metav1.CreateOptions{})
			if !apierrors.IsInvalid(err) || !strings.Contains(err.Error(), path+": Required value") {
				t.Fatalf("missing %s must be rejected as required, got %v", path, err)
			}
		})
	}
	for path, value := range map[string]string{
		"spec.repository.name": "other", "spec.repository.uid": "other-uid",
		"spec.repository.volumeNamespace": strings.Replace(apiVolume, "-cluster", "-other", 1),
		"spec.storage.accountURL":         "https://other123.blob.core.windows.net",
		"spec.storage.container":          "other", "spec.storage.prefix": "other/kopia/" + apiVolume + "/",
	} {
		for _, method := range []string{"update", "apply"} {
			t.Run("immutable/"+method+"/"+path, func(t *testing.T) {
				a := intentApply(apiCleanupSpec(types.UID(method + "-" + strings.ToLower(strings.ReplaceAll(path, ".", "-")))))
				apiApply(t, c, a)
				before := apiGet(t, client, a.Resource, a.Object.GetName())
				changed := before.DeepCopy()
				changed.SetManagedFields(nil)
				if err := unstructured.SetNestedField(changed.Object, value, strings.Split(path, ".")...); err != nil {
					t.Fatal(err)
				}
				var err error
				if method == "update" {
					_, err = resource.Update(ctx, changed, metav1.UpdateOptions{})
				} else {
					a.Object = changed
					err = c.apply(ctx, a)
				}
				if !apierrors.IsInvalid(err) || !strings.Contains(err.Error(), "Cleanup identity and storage target are immutable") {
					t.Fatalf("%s must fail CEL validation, not merely SSA ownership: %v", path, err)
				}
				after := apiGet(t, client, a.Resource, before.GetName())
				if !reflect.DeepEqual(before.Object["spec"], after.Object["spec"]) || before.GetResourceVersion() != after.GetResourceVersion() {
					t.Fatal("rejected spec mutation changed persisted object")
				}
			})
		}
	}
}

func testCleanupAPIFinalizers(t *testing.T, client dynamic.Interface) {
	c := &Controller{client: client}
	ctx := t.Context()
	const foreign = "example.com/foreign"
	for _, tc := range []struct {
		gvr     schema.GroupVersionResource
		kind    string
		manager string
	}{
		{BackupRepositoriesGVR, "BackupRepository", RepositoryFieldManager},
		{BackupRepositoryCleanupsGVR, "BackupRepositoryCleanup", CleanupFieldManager},
	} {
		t.Run(tc.kind, func(t *testing.T) {
			resource := client.Resource(tc.gvr).Namespace(Namespace)
			obj := baseApply(tc.gvr, tc.kind, "repo-api-finalizers")
			if tc.gvr == BackupRepositoryCleanupsGVR {
				obj = intentApply(apiCleanupSpec("api-finalizers")).Object
			}
			obj.SetFinalizers([]string{foreign})
			if _, err := resource.Create(ctx, obj, metav1.CreateOptions{FieldManager: "foreign-controller"}); err != nil {
				t.Fatal(err)
			}
			before := apiGet(t, client, tc.gvr, obj.GetName())
			apiApply(t, c, finalizerApply(before, tc.gvr, tc.manager, true))
			protected := apiGet(t, client, tc.gvr, obj.GetName())
			if got := protected.GetFinalizers(); len(got) != 2 || !slices.Contains(got, foreign) || !slices.Contains(got, Finalizer) {
				t.Fatalf("SSA add lost finalizers: %v", got)
			}
			if ours, other := finalizerOwnership(protected, tc.manager); !ours || other {
				t.Fatalf("actual managedFields do not give exclusive ownership of our finalizer: ours=%v other=%v", ours, other)
			}
			if err := resource.Delete(ctx, obj.GetName(), metav1.DeleteOptions{}); err != nil {
				t.Fatal(err)
			}
			terminating := apiGet(t, client, tc.gvr, obj.GetName())
			if terminating.GetDeletionTimestamp() == nil {
				t.Fatal("finalizers did not hold deletion")
			}
			stale := finalizerApply(terminating, tc.gvr, tc.manager, false)
			apiApply(t, c, stale)
			released := apiGet(t, client, tc.gvr, obj.GetName())
			if !reflect.DeepEqual(released.GetFinalizers(), []string{foreign}) || !reflect.DeepEqual(before.Object["spec"], released.Object["spec"]) {
				t.Fatalf("SSA release changed foreign finalizer or spec: %#v", released.Object)
			}
			// The foreign controller finishes deletion, then a new object reuses the name.
			released.SetFinalizers(nil)
			if _, err := resource.Update(ctx, released, metav1.UpdateOptions{FieldManager: "foreign-controller"}); err != nil {
				t.Fatal(err)
			}
			if _, err := resource.Get(ctx, obj.GetName(), metav1.GetOptions{}); !apierrors.IsNotFound(err) {
				t.Fatalf("old object not deleted: %v", err)
			}
			replacement, err := resource.Create(ctx, obj, metav1.CreateOptions{FieldManager: "foreign-controller"})
			if err != nil {
				t.Fatal(err)
			}
			if replacement.GetUID() == before.GetUID() {
				t.Fatal("replacement reused UID")
			}
			// Use the *new* RV to prove the UID, rather than an RV conflict, fences the write.
			stale.Object.SetResourceVersion(replacement.GetResourceVersion())
			if err := c.apply(ctx, stale); !apierrors.IsConflict(err) && !apierrors.IsInvalid(err) {
				t.Fatalf("stale UID apply must fail: %v", err)
			}
			oldUID, newRV := before.GetUID(), replacement.GetResourceVersion()
			if err := resource.Delete(ctx, obj.GetName(), metav1.DeleteOptions{Preconditions: &metav1.Preconditions{UID: &oldUID, ResourceVersion: &newRV}}); !apierrors.IsConflict(err) {
				t.Fatalf("stale UID delete must conflict: %v", err)
			}
			after := apiGet(t, client, tc.gvr, obj.GetName())
			if after.GetUID() != replacement.GetUID() || after.GetResourceVersion() != newRV || after.GetDeletionTimestamp() != nil || !reflect.DeepEqual(after.GetFinalizers(), []string{foreign}) {
				t.Fatal("stale operation mutated replacement")
			}
			after.SetFinalizers(nil)
			if _, err := resource.Update(ctx, after, metav1.UpdateOptions{FieldManager: "foreign-controller"}); err != nil {
				t.Fatal(err)
			}
			if err := resource.Delete(ctx, obj.GetName(), metav1.DeleteOptions{}); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func testCleanupAPILifecycle(t *testing.T, client dynamic.Interface) {
	ctx := t.Context()
	repo := baseApply(BackupRepositoriesGVR, "BackupRepository", "api-repository")
	repo.Object["spec"] = map[string]interface{}{"repositoryType": "kopia", "volumeNamespace": apiVolume, "backupStorageLocation": "api-bsl"}
	bsl := baseApply(BackupStorageLocationsGVR, "BackupStorageLocation", "api-bsl")
	bsl.Object["spec"] = map[string]interface{}{
		"provider": "azure", "config": map[string]interface{}{"storageAccount": "account123"},
		"objectStorage": map[string]interface{}{"bucket": "backups", "prefix": "base"},
	}
	for gvr, obj := range map[schema.GroupVersionResource]*unstructured.Unstructured{BackupRepositoriesGVR: repo, BackupStorageLocationsGVR: bsl} {
		if _, err := client.Resource(gvr).Namespace(Namespace).Create(ctx, obj, metav1.CreateOptions{}); err != nil {
			t.Fatal(err)
		}
	}
	repo = apiGet(t, client, BackupRepositoriesGVR, repo.GetName())
	expected := apiCleanupSpec(repo.GetUID())
	cleanupName := CleanupName(repo.GetUID())
	sweeps := 0
	c := &Controller{client: client, deletePrefix: func(ctx context.Context, target storage.Target) error {
		if target != expected.Storage {
			t.Fatalf("incorrect prefix target: %#v", target)
		}
		if _, err := client.Resource(BackupRepositoriesGVR).Namespace(Namespace).Get(ctx, repo.GetName(), metav1.GetOptions{}); !apierrors.IsNotFound(err) {
			t.Fatalf("sweep occurred before repository disappeared: %v", err)
		}
		intent := apiGet(t, client, BackupRepositoryCleanupsGVR, cleanupName)
		if spec, err := intentSpec(intent); err != nil || spec != expected || !hasFinalizer(intent) {
			t.Fatalf("sweep lacks protected durable intent: spec=%#v error=%v", spec, err)
		}
		sweeps++
		return nil
	}}
	reconcile := func(gvr schema.GroupVersionResource, name string) time.Duration {
		t.Helper()
		delay, err := c.reconcile(ctx, key{Resource: gvr, Name: name})
		if err != nil {
			t.Fatalf("reconcile %s/%s: %v", gvr.Resource, name, err)
		}
		return delay
	}
	reconcile(BackupRepositoriesGVR, repo.GetName())
	protected := apiGet(t, client, BackupRepositoriesGVR, repo.GetName())
	if !hasFinalizer(protected) || protected.GetDeletionTimestamp() != nil {
		t.Fatal("first reconcile must persist protection before deletion")
	}
	reconcile(BackupRepositoriesGVR, repo.GetName())
	terminating := apiGet(t, client, BackupRepositoriesGVR, repo.GetName())
	if !hasFinalizer(terminating) || terminating.GetDeletionTimestamp() == nil {
		t.Fatal("repository deletion must wait on protection")
	}
	if _, err := client.Resource(BackupRepositoryCleanupsGVR).Namespace(Namespace).Get(ctx, cleanupName, metav1.GetOptions{}); !apierrors.IsNotFound(err) {
		t.Fatalf("intent unexpectedly exists before handoff: %v", err)
	}
	reconcile(BackupRepositoriesGVR, repo.GetName())
	intent := apiGet(t, client, BackupRepositoryCleanupsGVR, cleanupName)
	if spec, err := intentSpec(intent); err != nil || spec != expected || !hasFinalizer(intent) || len(intent.GetOwnerReferences()) != 0 {
		t.Fatalf("invalid durable handoff: spec=%#v error=%v", spec, err)
	}
	if !hasFinalizer(apiGet(t, client, BackupRepositoriesGVR, repo.GetName())) {
		t.Fatal("repository protection released in same reconcile as intent creation")
	}
	reconcile(BackupRepositoryCleanupsGVR, cleanupName)
	apiAssertCondition(t, apiGet(t, client, BackupRepositoryCleanupsGVR, cleanupName), "False", WaitingForRepository)
	if sweeps != 0 {
		t.Fatal("cleanup swept before repository deletion")
	}
	// A new controller must recover the obligation solely from persisted API state.
	c = &Controller{client: client, deletePrefix: c.deletePrefix}
	reconcile(BackupRepositoriesGVR, repo.GetName())
	if _, err := client.Resource(BackupRepositoriesGVR).Namespace(Namespace).Get(ctx, repo.GetName(), metav1.GetOptions{}); !apierrors.IsNotFound(err) {
		t.Fatalf("repository was not deleted after durable handoff: %v", err)
	}
	if delay := reconcile(BackupRepositoryCleanupsGVR, cleanupName); delay != tombstoneRecheckInterval {
		t.Fatalf("completed tombstone requeue = %v", delay)
	}
	tombstone := apiGet(t, client, BackupRepositoryCleanupsGVR, cleanupName)
	apiAssertCondition(t, tombstone, "True", CleanupComplete)
	if sweeps != 1 || tombstone.GetUID() != intent.GetUID() || !hasFinalizer(tombstone) || tombstone.GetDeletionTimestamp() != nil || !reflect.DeepEqual(tombstone.Object["spec"], intent.Object["spec"]) {
		t.Fatalf("expected one sweep and retained protected immutable tombstone; sweeps=%d object=%#v", sweeps, tombstone.Object)
	}
	reconcile(BackupRepositoryCleanupsGVR, cleanupName)
	if sweeps != 2 {
		t.Fatal("persisted Complete incorrectly substituted for a fresh sweep")
	}
	apiAssertCondition(t, apiGet(t, client, BackupRepositoryCleanupsGVR, cleanupName), "True", CleanupComplete)
	// A same-name replacement blocks even a previously completed tombstone.
	replacement := baseApply(BackupRepositoriesGVR, "BackupRepository", repo.GetName())
	replacement.Object["spec"] = repo.Object["spec"]
	replacement, err := client.Resource(BackupRepositoriesGVR).Namespace(Namespace).Create(ctx, replacement, metav1.CreateOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if replacement.GetUID() == repo.GetUID() {
		t.Fatal("replacement reused retired repository UID")
	}
	reconcile(BackupRepositoryCleanupsGVR, cleanupName)
	if sweeps != 2 {
		t.Fatal("cleanup swept storage belonging to a same-name replacement")
	}
	apiAssertCondition(t, apiGet(t, client, BackupRepositoryCleanupsGVR, cleanupName), "False", WaitingForRepository)
	if got := apiGet(t, client, BackupRepositoriesGVR, repo.GetName()); got.GetUID() != replacement.GetUID() || got.GetDeletionTimestamp() != nil {
		t.Fatal("cleanup mutated the replacement repository")
	}
	if err := client.Resource(BackupRepositoriesGVR).Namespace(Namespace).Delete(ctx, repo.GetName(), metav1.DeleteOptions{}); err != nil {
		t.Fatal(err)
	}
	reconcile(BackupRepositoryCleanupsGVR, cleanupName)
	if sweeps != 3 {
		t.Fatal("cleanup did not resume sweeping after replacement disappeared")
	}
	apiAssertCondition(t, apiGet(t, client, BackupRepositoryCleanupsGVR, cleanupName), "True", CleanupComplete)
}

func apiAssertCondition(t *testing.T, obj *unstructured.Unstructured, status, reason string) {
	t.Helper()
	conditions, _, err := unstructured.NestedSlice(obj.Object, "status", "conditions")
	if err != nil || len(conditions) != 1 {
		t.Fatalf("expected one persisted condition: %#v, %v", conditions, err)
	}
	condition, ok := conditions[0].(map[string]interface{})
	if !ok || condition["type"] != Complete || condition["status"] != status || condition["reason"] != reason || condition["observedGeneration"] != obj.GetGeneration() {
		t.Fatalf("unexpected persisted condition: %#v", condition)
	}
}
