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
	"encoding/json"
	"testing"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	dynamicfake "k8s.io/client-go/dynamic/fake"
	clienttesting "k8s.io/client-go/testing"
)

func ownershipCleanupListKinds() map[schema.GroupVersionResource]string {
	return map[schema.GroupVersionResource]string{
		AppliedManifestWorksGVR:  "AppliedManifestWorkList",
		ManifestWorksGVR:         "ManifestWorkList",
		HostedClustersGVR:        "HostedClusterList",
		NodePoolsGVR:             "NodePoolList",
		PodNetworksGVR:           "PodNetworkList",
		PodNetworkInstancesGVR:   "PodNetworkInstanceList",
		ConfigMapsGVR:            "ConfigMapList",
		SecretsGVR:               "SecretList",
		SecretSyncsGVR:           "SecretSyncList",
		SecretProviderClassesGVR: "SecretProviderClassList",
		{Group: "", Version: "v1", Resource: "namespaces"}: "NamespaceList",
	}
}

func hostedClusterWithAMWOwner(name, namespace, appliedManifestWorkName string) *unstructured.Unstructured {
	controller := true
	return &unstructured.Unstructured{Object: map[string]interface{}{
		"apiVersion": "hypershift.openshift.io/v1beta1",
		"kind":       "HostedCluster",
		"metadata": map[string]interface{}{
			"name":      name,
			"namespace": namespace,
			"ownerReferences": []interface{}{
				map[string]interface{}{
					"apiVersion": "work.open-cluster-management.io/v1",
					"kind":       "AppliedManifestWork",
					"name":       appliedManifestWorkName,
					"uid":        "amw-uid",
					"controller": controller,
				},
			},
		},
	}}
}

func TestOwnershipCleanupProducerEnqueuesCandidatesOnce(t *testing.T) {
	ctx := context.Background()
	hcNS := &unstructured.Unstructured{Object: map[string]interface{}{
		"apiVersion": "v1",
		"kind":       "Namespace",
		"metadata":   map[string]interface{}{"name": "clusters"},
	}}
	hc := hostedClusterWithAMWOwner("demo", "clusters", "hubhash-demo-mw")

	client := dynamicfake.NewSimpleDynamicClientWithCustomListKinds(runtime.NewScheme(), ownershipCleanupListKinds(), hcNS, hc)
	controller, err := NewOwnershipCleanupController(client)
	if err != nil {
		t.Fatal(err)
	}
	defer controller.queue.ShutDown()

	if err := controller.produce(ctx); err != nil {
		t.Fatal(err)
	}

	key, shutdown := controller.queue.Get()
	if shutdown {
		t.Fatal("queue shut down unexpectedly")
	}
	controller.queue.Done(key)

	if key.gvr != HostedClustersGVR || key.namespace != "clusters" || key.name != "demo" {
		t.Fatalf("unexpected resource key: %#v", key)
	}
	if controller.queue.Len() != 0 {
		t.Fatalf("expected producer to enqueue candidates once, queue len=%d", controller.queue.Len())
	}
}

func TestOwnershipCleanupStripsDanglingAMWOwnerRefsLast(t *testing.T) {
	ctx := context.Background()
	hcNS := &unstructured.Unstructured{Object: map[string]interface{}{
		"apiVersion": "v1",
		"kind":       "Namespace",
		"metadata":   map[string]interface{}{"name": "clusters"},
	}}
	hc := hostedClusterWithAMWOwner("demo", "clusters", "hubhash-demo-mw")

	client := dynamicfake.NewSimpleDynamicClientWithCustomListKinds(runtime.NewScheme(), ownershipCleanupListKinds(), hcNS, hc)
	controller, err := NewOwnershipCleanupController(client)
	if err != nil {
		t.Fatal(err)
	}
	defer controller.queue.ShutDown()

	resourceKey := ownershipCleanupKey{gvr: HostedClustersGVR, namespace: "clusters", name: "demo"}
	if err := controller.syncResource(ctx, resourceKey); err != nil {
		t.Fatal(err)
	}

	updated, err := client.Resource(HostedClustersGVR).Namespace("clusters").Get(ctx, "demo", metav1.GetOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if len(updated.GetOwnerReferences()) != 0 {
		t.Fatalf("expected dangling ownerReferences removed, got %#v", updated.GetOwnerReferences())
	}
}

func TestOwnershipCleanupSetsManifestWorkOrphanThenClearsAMWFinalizers(t *testing.T) {
	ctx := context.Background()
	appliedManifestWorkName := "hubhash-demo-mw"
	hcNS := &unstructured.Unstructured{Object: map[string]interface{}{
		"apiVersion": "v1",
		"kind":       "Namespace",
		"metadata":   map[string]interface{}{"name": "clusters"},
	}}
	hc := hostedClusterWithAMWOwner("demo", "clusters", appliedManifestWorkName)
	manifestWork := &unstructured.Unstructured{Object: map[string]interface{}{
		"apiVersion": "work.open-cluster-management.io/v1",
		"kind":       "ManifestWork",
		"metadata": map[string]interface{}{
			"name":      "demo-mw",
			"namespace": "local-cluster",
		},
		"spec": map[string]interface{}{
			"deleteOption": map[string]interface{}{
				"propagationPolicy": "Foreground",
			},
		},
	}}
	appliedManifestWork := &unstructured.Unstructured{Object: map[string]interface{}{
		"apiVersion": "work.open-cluster-management.io/v1",
		"kind":       "AppliedManifestWork",
		"metadata": map[string]interface{}{
			"name":       appliedManifestWorkName,
			"finalizers": []interface{}{"cluster.open-cluster-management.io/applied-manifest-work-cleanup"},
		},
		"spec": map[string]interface{}{
			"hubHash":          "hubhash",
			"manifestWorkName": "demo-mw",
		},
	}}

	client := dynamicfake.NewSimpleDynamicClientWithCustomListKinds(
		runtime.NewScheme(),
		ownershipCleanupListKinds(),
		hcNS, hc, manifestWork, appliedManifestWork,
	)
	// dynamic fake cannot apply unstructured SSA patches; merge deleteOption into the tracker.
	client.PrependReactor("patch", "manifestworks", func(action clienttesting.Action) (bool, runtime.Object, error) {
		patchAction := action.(clienttesting.PatchAction)
		if patchAction.GetPatchType() != types.ApplyPatchType {
			return false, nil, nil
		}
		var applied unstructured.Unstructured
		if err := json.Unmarshal(patchAction.GetPatch(), &applied.Object); err != nil {
			return true, nil, err
		}
		live, err := client.Tracker().Get(ManifestWorksGVR, patchAction.GetNamespace(), patchAction.GetName())
		if err != nil {
			return true, nil, err
		}
		liveObject := live.(*unstructured.Unstructured)
		propagationPolicy, _, _ := unstructured.NestedString(applied.Object, "spec", "deleteOption", "propagationPolicy")
		if err := unstructured.SetNestedField(liveObject.Object, propagationPolicy, "spec", "deleteOption", "propagationPolicy"); err != nil {
			return true, nil, err
		}
		if err := client.Tracker().Update(ManifestWorksGVR, liveObject, patchAction.GetNamespace()); err != nil {
			return true, nil, err
		}
		return true, liveObject, nil
	})
	controller, err := NewOwnershipCleanupController(client)
	if err != nil {
		t.Fatal(err)
	}
	defer controller.queue.ShutDown()

	resourceKey := ownershipCleanupKey{gvr: HostedClustersGVR, namespace: "clusters", name: "demo"}

	// Step 1 sets Orphan and requeues the resource.
	if err := controller.syncResource(ctx, resourceKey); err != nil {
		t.Fatal(err)
	}
	updatedManifestWork, err := client.Resource(ManifestWorksGVR).Namespace("local-cluster").Get(ctx, "demo-mw", metav1.GetOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if got := manifestWorkPropagationPolicy(updatedManifestWork); got != manifestWorkDeletePropagationOrphan {
		t.Fatalf("expected Orphan propagationPolicy, got %q", got)
	}

	// Mark AppliedManifestWork deleting so step 2 can clear finalizers.
	now := metav1.Now()
	appliedManifestWork.SetDeletionTimestamp(&now)
	if err := client.Tracker().Update(AppliedManifestWorksGVR, appliedManifestWork, ""); err != nil {
		t.Fatal(err)
	}

	if err := controller.syncResource(ctx, resourceKey); err != nil {
		t.Fatal(err)
	}
	updatedAppliedManifestWork, err := client.Resource(AppliedManifestWorksGVR).Get(ctx, appliedManifestWorkName, metav1.GetOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if len(updatedAppliedManifestWork.GetFinalizers()) != 0 {
		t.Fatalf("expected AppliedManifestWork finalizers cleared, got %#v", updatedAppliedManifestWork.GetFinalizers())
	}
}
