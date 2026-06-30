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

package istio

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/kubernetes/fake"
	k8stesting "k8s.io/client-go/testing"
	"k8s.io/utils/ptr"
)

func TestGetMeshNamespaces(t *testing.T) {
	client := fake.NewSimpleClientset(
		&corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: "app-ns", Labels: map[string]string{"istio.io/rev": "asm-1-28"}}},
		&corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: "other-ns", Labels: map[string]string{"team": "infra"}}},
		&corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: "mesh-ns", Labels: map[string]string{"istio.io/rev": "asm-1-29"}}},
	)

	namespaces, err := GetMeshNamespaces(context.Background(), client)
	require.NoError(t, err)
	assert.Len(t, namespaces, 2)
	revisions := []string{namespaces[0].RevisionLabel, namespaces[1].RevisionLabel}
	assert.ElementsMatch(t, []string{"asm-1-28", "asm-1-29"}, revisions)
}

func TestGetControlPlaneStatus(t *testing.T) {
	client := fake.NewSimpleClientset(
		&appsv1.Deployment{
			ObjectMeta: metav1.ObjectMeta{Name: "istiod-asm-1-28", Namespace: "aks-istio-system"},
			Spec:       appsv1.DeploymentSpec{Replicas: ptr.To[int32](2)},
			Status:     appsv1.DeploymentStatus{AvailableReplicas: 2},
		},
		&appsv1.Deployment{
			ObjectMeta: metav1.ObjectMeta{Name: "istiod-asm-1-29", Namespace: "aks-istio-system"},
			Spec:       appsv1.DeploymentSpec{Replicas: ptr.To[int32](2)},
			Status:     appsv1.DeploymentStatus{AvailableReplicas: 1},
		},
		&appsv1.Deployment{
			ObjectMeta: metav1.ObjectMeta{Name: "other-deploy", Namespace: "aks-istio-system"},
			Spec:       appsv1.DeploymentSpec{Replicas: ptr.To[int32](1)},
			Status:     appsv1.DeploymentStatus{AvailableReplicas: 1},
		},
	)

	status, err := GetControlPlaneStatus(context.Background(), client)
	require.NoError(t, err)
	assert.Len(t, status, 2)
	assert.True(t, status[0].Ready)
	assert.Equal(t, "asm-1-28", status[0].Revision)
	assert.False(t, status[1].Ready)
	assert.Equal(t, "asm-1-29", status[1].Revision)
}

func TestGetIngressGatewayStatus(t *testing.T) {
	client := fake.NewSimpleClientset(
		&corev1.Service{
			ObjectMeta: metav1.ObjectMeta{Name: "aks-istio-ingressgateway-external", Namespace: "aks-istio-ingress"},
			Spec: corev1.ServiceSpec{
				Type:     corev1.ServiceTypeLoadBalancer,
				Selector: map[string]string{"app": "ingress"},
			},
			Status: corev1.ServiceStatus{
				LoadBalancer: corev1.LoadBalancerStatus{
					Ingress: []corev1.LoadBalancerIngress{{IP: "10.0.0.1"}},
				},
			},
		},
		&corev1.Pod{
			ObjectMeta: metav1.ObjectMeta{Name: "ingress-pod-1", Namespace: "aks-istio-ingress", Labels: map[string]string{"app": "ingress"}},
			Status:     corev1.PodStatus{Conditions: []corev1.PodCondition{{Type: corev1.PodReady, Status: corev1.ConditionTrue}}},
		},
		&corev1.Pod{
			ObjectMeta: metav1.ObjectMeta{Name: "ingress-pod-2", Namespace: "aks-istio-ingress", Labels: map[string]string{"app": "ingress"}},
			Status:     corev1.PodStatus{Conditions: []corev1.PodCondition{{Type: corev1.PodReady, Status: corev1.ConditionFalse}}},
		},
	)

	statuses, err := GetIngressGatewayStatus(context.Background(), client)
	require.NoError(t, err)
	require.Len(t, statuses, 1)
	assert.Equal(t, "10.0.0.1", statuses[0].ExternalIP)
	assert.Equal(t, 1, statuses[0].HealthyPods)
}

func TestEnsureIngressAnnotations(t *testing.T) {
	svc := &corev1.Service{
		ObjectMeta: metav1.ObjectMeta{Name: "aks-istio-ingressgateway-external", Namespace: "aks-istio-ingress"},
		Spec:       corev1.ServiceSpec{Type: corev1.ServiceTypeLoadBalancer},
	}
	client := fake.NewSimpleClientset(svc)

	applied, err := EnsureIngressAnnotations(context.Background(), client, "my-rg", map[string]string{
		"aks-istio-ingressgateway-external": "my-pip",
	})
	require.NoError(t, err)
	assert.True(t, applied)

	updated, err := client.CoreV1().Services("aks-istio-ingress").Get(context.Background(), "aks-istio-ingressgateway-external", metav1.GetOptions{})
	require.NoError(t, err)
	assert.Equal(t, "my-rg", updated.Annotations["service.beta.kubernetes.io/azure-load-balancer-resource-group"])
	assert.Equal(t, "my-pip", updated.Annotations["service.beta.kubernetes.io/azure-pip-name"])

	applied2, err := EnsureIngressAnnotations(context.Background(), client, "my-rg", map[string]string{
		"aks-istio-ingressgateway-external": "my-pip",
	})
	require.NoError(t, err)
	assert.False(t, applied2)
}

func TestExecuteRestart(t *testing.T) {
	client := fake.NewSimpleClientset(
		&corev1.Pod{
			ObjectMeta: metav1.ObjectMeta{
				Name: "bare-pod", Namespace: "app-ns",
				Annotations: map[string]string{"sidecar.istio.io/status": `{"revision":"asm-1-28"}`},
			},
			Status: corev1.PodStatus{Phase: corev1.PodRunning},
		},
		&corev1.Pod{
			ObjectMeta: metav1.ObjectMeta{
				Name: "bare-pod-current", Namespace: "app-ns",
				Annotations: map[string]string{"sidecar.istio.io/status": `{"revision":"asm-1-29"}`},
			},
			Status: corev1.PodStatus{Phase: corev1.PodRunning},
		},
		&corev1.Pod{
			ObjectMeta: metav1.ObjectMeta{
				Name: "owned-pod", Namespace: "app-ns",
				OwnerReferences: []metav1.OwnerReference{{Name: "web-rs", Kind: "ReplicaSet", APIVersion: "apps/v1", Controller: ptr.To(true)}},
				Annotations:     map[string]string{"sidecar.istio.io/status": `{"revision":"asm-1-28"}`},
			},
			Status: corev1.PodStatus{Phase: corev1.PodRunning},
		},
		&corev1.Pod{
			ObjectMeta: metav1.ObjectMeta{
				Name: "current-pod", Namespace: "app-ns",
				OwnerReferences: []metav1.OwnerReference{{Name: "api-rs", Kind: "ReplicaSet", APIVersion: "apps/v1", Controller: ptr.To(true)}},
				Annotations:     map[string]string{"sidecar.istio.io/status": `{"revision":"asm-1-29"}`},
			},
			Status: corev1.PodStatus{Phase: corev1.PodRunning},
		},
		&corev1.Pod{
			ObjectMeta: metav1.ObjectMeta{
				Name: "cache-0", Namespace: "app-ns",
				OwnerReferences: []metav1.OwnerReference{{Name: "cache", Kind: "StatefulSet", APIVersion: "apps/v1", Controller: ptr.To(true)}},
				Annotations:     map[string]string{"sidecar.istio.io/status": `{"revision":"asm-1-28"}`},
			},
			Status: corev1.PodStatus{Phase: corev1.PodRunning},
		},
		&appsv1.ReplicaSet{
			ObjectMeta: metav1.ObjectMeta{
				Name: "web-rs", Namespace: "app-ns",
				OwnerReferences: []metav1.OwnerReference{{Name: "web", Kind: "Deployment", APIVersion: "apps/v1", Controller: ptr.To(true)}},
			},
		},
		&appsv1.ReplicaSet{
			ObjectMeta: metav1.ObjectMeta{
				Name: "api-rs", Namespace: "app-ns",
				OwnerReferences: []metav1.OwnerReference{{Name: "api", Kind: "Deployment", APIVersion: "apps/v1", Controller: ptr.To(true)}},
			},
		},
		&appsv1.Deployment{
			ObjectMeta: metav1.ObjectMeta{Name: "web", Namespace: "app-ns"},
			Spec:       appsv1.DeploymentSpec{Replicas: ptr.To[int32](2)},
		},
		&appsv1.Deployment{
			ObjectMeta: metav1.ObjectMeta{Name: "api", Namespace: "app-ns"},
			Spec:       appsv1.DeploymentSpec{Replicas: ptr.To[int32](1)},
		},
		&appsv1.StatefulSet{
			ObjectMeta: metav1.ObjectMeta{Name: "cache", Namespace: "app-ns"},
			Spec:       appsv1.StatefulSetSpec{Replicas: ptr.To[int32](1)},
		},
	)

	result, err := executeRestart(context.Background(), client, "app-ns", "asm-1-29")
	require.NoError(t, err)

	assert.Contains(t, result.Restarted, "pod/bare-pod")
	assert.NotContains(t, result.Restarted, "pod/bare-pod-current")
	assert.Contains(t, result.Restarted, "deployment/web")
	assert.NotContains(t, result.Restarted, "deployment/api")
	assert.Contains(t, result.Restarted, "statefulset/cache")

	pods, err := client.CoreV1().Pods("app-ns").List(context.Background(), metav1.ListOptions{})
	require.NoError(t, err)
	for _, p := range pods.Items {
		assert.NotEqual(t, "bare-pod", p.Name, "stale bare pod should have been deleted")
	}
}

func TestExecuteRestartAllNamespaces(t *testing.T) {
	client := fake.NewSimpleClientset(
		&corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: "ns-a", Labels: map[string]string{"istio.io/rev": "asm-1-29"}}},
		&corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: "ns-b", Labels: map[string]string{"istio.io/rev": "asm-1-29"}}},
		// ns-a: stale pod owned by deployment
		&corev1.Pod{
			ObjectMeta: metav1.ObjectMeta{
				Name: "pod-a", Namespace: "ns-a",
				Annotations:     map[string]string{"sidecar.istio.io/status": `{"revision":"asm-1-28"}`},
				OwnerReferences: []metav1.OwnerReference{{Name: "rs-a", Kind: "ReplicaSet", APIVersion: "apps/v1", Controller: ptr.To(true)}},
			},
			Status: corev1.PodStatus{Phase: corev1.PodRunning},
		},
		&appsv1.ReplicaSet{
			ObjectMeta: metav1.ObjectMeta{
				Name: "rs-a", Namespace: "ns-a",
				OwnerReferences: []metav1.OwnerReference{{Name: "deploy-a", Kind: "Deployment", APIVersion: "apps/v1", Controller: ptr.To(true)}},
			},
		},
		&appsv1.Deployment{
			ObjectMeta: metav1.ObjectMeta{Name: "deploy-a", Namespace: "ns-a"},
			Spec:       appsv1.DeploymentSpec{Replicas: ptr.To[int32](1)},
		},
		// ns-b: already current — no stale pods
		&corev1.Pod{
			ObjectMeta: metav1.ObjectMeta{
				Name: "pod-b", Namespace: "ns-b",
				Annotations:     map[string]string{"sidecar.istio.io/status": `{"revision":"asm-1-29"}`},
				OwnerReferences: []metav1.OwnerReference{{Name: "rs-b", Kind: "ReplicaSet", APIVersion: "apps/v1", Controller: ptr.To(true)}},
			},
			Status: corev1.PodStatus{Phase: corev1.PodRunning},
		},
	)

	results, err := ExecuteRestartAllNamespaces(context.Background(), client, "asm-1-29")
	require.NoError(t, err)

	require.Len(t, results, 2, "should return a result per mesh namespace")

	var restarted int
	for _, r := range results {
		if r.Namespace == "ns-a" {
			assert.Contains(t, r.Restarted, "deployment/deploy-a")
		}
		restarted += len(r.Restarted)
	}
	assert.Equal(t, 1, restarted, "only ns-a had stale workloads to restart")
}

func TestExecuteRestartAllNamespaces_NoNamespaces(t *testing.T) {
	client := fake.NewSimpleClientset()

	results, err := ExecuteRestartAllNamespaces(context.Background(), client, "asm-1-29")
	require.NoError(t, err)
	assert.Empty(t, results, "no mesh namespaces should produce empty results")
}

func TestExecuteRestartAllNamespaces_PartialFailure(t *testing.T) {
	client := fake.NewSimpleClientset(
		&corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: "ns-ok", Labels: map[string]string{"istio.io/rev": "asm-1-29"}}},
		&corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: "ns-fail", Labels: map[string]string{"istio.io/rev": "asm-1-29"}}},
		// ns-ok: stale bare pod (will succeed)
		&corev1.Pod{
			ObjectMeta: metav1.ObjectMeta{
				Name: "bare-ok", Namespace: "ns-ok",
				Annotations: map[string]string{"sidecar.istio.io/status": `{"revision":"asm-1-28"}`},
			},
			Status: corev1.PodStatus{Phase: corev1.PodRunning},
		},
		// ns-fail: stale pod owned by deployment that will fail to patch
		&corev1.Pod{
			ObjectMeta: metav1.ObjectMeta{
				Name: "pod-fail", Namespace: "ns-fail",
				Annotations:     map[string]string{"sidecar.istio.io/status": `{"revision":"asm-1-28"}`},
				OwnerReferences: []metav1.OwnerReference{{Name: "rs-fail", Kind: "ReplicaSet", APIVersion: "apps/v1", Controller: ptr.To(true)}},
			},
			Status: corev1.PodStatus{Phase: corev1.PodRunning},
		},
		&appsv1.ReplicaSet{
			ObjectMeta: metav1.ObjectMeta{
				Name: "rs-fail", Namespace: "ns-fail",
				OwnerReferences: []metav1.OwnerReference{{Name: "deploy-fail", Kind: "Deployment", APIVersion: "apps/v1", Controller: ptr.To(true)}},
			},
		},
		// Intentionally no Deployment object for deploy-fail — patch will fail
	)

	client.PrependReactor("patch", "deployments", func(action k8stesting.Action) (bool, runtime.Object, error) {
		pa := action.(k8stesting.PatchAction)
		if pa.GetNamespace() == "ns-fail" {
			return true, nil, fmt.Errorf("simulated patch failure")
		}
		return false, nil, nil
	})

	results, err := ExecuteRestartAllNamespaces(context.Background(), client, "asm-1-29")
	assert.Error(t, err, "should return aggregated error from ns-fail")
	assert.ErrorContains(t, err, "ns-fail")

	var foundOK bool
	for _, r := range results {
		if r.Namespace == "ns-ok" {
			foundOK = true
			assert.Contains(t, r.Restarted, "pod/bare-ok", "ns-ok restart should have succeeded")
		}
	}
	assert.True(t, foundOK, "successful namespace result should still be included")
}

func TestWaitForRollout_AllReady(t *testing.T) {
	client := fake.NewSimpleClientset(
		&appsv1.Deployment{
			ObjectMeta: metav1.ObjectMeta{Name: "web", Namespace: "app-ns", Generation: 1},
			Spec:       appsv1.DeploymentSpec{Replicas: ptr.To[int32](2)},
			Status:     appsv1.DeploymentStatus{ObservedGeneration: 1, UpdatedReplicas: 2, ReadyReplicas: 2},
		},
		&appsv1.StatefulSet{
			ObjectMeta: metav1.ObjectMeta{Name: "cache", Namespace: "app-ns", Generation: 1},
			Spec:       appsv1.StatefulSetSpec{Replicas: ptr.To[int32](1)},
			Status:     appsv1.StatefulSetStatus{ObservedGeneration: 1, UpdatedReplicas: 1, ReadyReplicas: 1},
		},
	)

	err := WaitForRollout(context.Background(), client, "app-ns", 5*time.Second, 100*time.Millisecond)
	require.NoError(t, err)
}

func TestWaitForRollout_SkipsInjectFalse(t *testing.T) {
	client := fake.NewSimpleClientset(
		&appsv1.Deployment{
			ObjectMeta: metav1.ObjectMeta{Name: "no-sidecar", Namespace: "app-ns", Generation: 1},
			Spec: appsv1.DeploymentSpec{
				Replicas: ptr.To[int32](1),
				Template: corev1.PodTemplateSpec{
					ObjectMeta: metav1.ObjectMeta{
						Annotations: map[string]string{"sidecar.istio.io/inject": "false"},
					},
				},
			},
			Status: appsv1.DeploymentStatus{ObservedGeneration: 1, UpdatedReplicas: 0, ReadyReplicas: 0},
		},
	)

	err := WaitForRollout(context.Background(), client, "app-ns", 5*time.Second, 100*time.Millisecond)
	require.NoError(t, err, "inject-false deployment should be skipped even when not ready")
}

func TestWaitForRollout_SkipsZeroReplicas(t *testing.T) {
	client := fake.NewSimpleClientset(
		&appsv1.Deployment{
			ObjectMeta: metav1.ObjectMeta{Name: "scaled-down", Namespace: "app-ns", Generation: 1},
			Spec:       appsv1.DeploymentSpec{Replicas: ptr.To[int32](0)},
			Status:     appsv1.DeploymentStatus{ObservedGeneration: 1, UpdatedReplicas: 0, ReadyReplicas: 0},
		},
	)

	err := WaitForRollout(context.Background(), client, "app-ns", 5*time.Second, 100*time.Millisecond)
	require.NoError(t, err, "zero-replica deployment should be skipped")
}

func TestWaitForRollout_Timeout(t *testing.T) {
	client := fake.NewSimpleClientset(
		&appsv1.Deployment{
			ObjectMeta: metav1.ObjectMeta{Name: "stuck", Namespace: "app-ns", Generation: 2},
			Spec:       appsv1.DeploymentSpec{Replicas: ptr.To[int32](1)},
			Status:     appsv1.DeploymentStatus{ObservedGeneration: 1, UpdatedReplicas: 0, ReadyReplicas: 0},
		},
	)

	err := WaitForRollout(context.Background(), client, "app-ns", 200*time.Millisecond, 50*time.Millisecond)
	assert.Error(t, err)
	assert.ErrorContains(t, err, "timeout waiting for rollout in app-ns")
}

func TestCreateRevisionConfigMap(t *testing.T) {
	client := fake.NewSimpleClientset()

	err := CreateRevisionConfigMap(context.Background(), client, "asm-1-29")
	require.NoError(t, err)

	cm, err := client.CoreV1().ConfigMaps("aks-istio-system").Get(context.Background(), "istio-shared-configmap-asm-1-29", metav1.GetOptions{})
	require.NoError(t, err)
	assert.Equal(t, "asm-1-29", cm.Labels["istio.io/rev"])
	assert.Contains(t, cm.Data["mesh"], "ext-authz")

	err = CreateRevisionConfigMap(context.Background(), client, "asm-1-29")
	require.NoError(t, err)
}

func TestCreateRevisionConfigMap_UpdatePreservesExistingLabels(t *testing.T) {
	client := fake.NewSimpleClientset(&corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "istio-shared-configmap-asm-1-29",
			Namespace: "aks-istio-system",
			Labels: map[string]string{
				"istio.io/rev":                 "asm-1-29",
				"app.kubernetes.io/managed-by": "Helm",
				"helm.sh/chart":                "istio-config-0.1.0",
			},
		},
		Data: map[string]string{"mesh": "old-data"},
	})

	err := CreateRevisionConfigMap(context.Background(), client, "asm-1-29")
	require.NoError(t, err)

	cm, err := client.CoreV1().ConfigMaps("aks-istio-system").Get(context.Background(), "istio-shared-configmap-asm-1-29", metav1.GetOptions{})
	require.NoError(t, err)
	assert.Equal(t, "asm-1-29", cm.Labels["istio.io/rev"])
	assert.Equal(t, "Helm", cm.Labels["app.kubernetes.io/managed-by"])
	assert.Equal(t, "istio-config-0.1.0", cm.Labels["helm.sh/chart"])
	assert.Contains(t, cm.Data["mesh"], "ext-authz")
}

func TestDeleteRevisionConfigMap(t *testing.T) {
	client := fake.NewSimpleClientset(&corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "istio-shared-configmap-asm-1-28",
			Namespace: "aks-istio-system",
			Labels:    map[string]string{"istio.io/rev": "asm-1-28"},
		},
		Data: map[string]string{"mesh": "test"},
	})

	err := DeleteRevisionConfigMap(context.Background(), client, "asm-1-28")
	require.NoError(t, err)

	_, err = client.CoreV1().ConfigMaps("aks-istio-system").Get(context.Background(), "istio-shared-configmap-asm-1-28", metav1.GetOptions{})
	assert.True(t, apierrors.IsNotFound(err), "ConfigMap should be deleted")
}

func TestDeleteRevisionConfigMap_NotFoundIsNoop(t *testing.T) {
	client := fake.NewSimpleClientset()

	err := DeleteRevisionConfigMap(context.Background(), client, "asm-1-28")
	require.NoError(t, err, "deleting a non-existent ConfigMap should not error")
}

func TestWaitForRolloutAllNamespaces_ConcurrentSuccess(t *testing.T) {
	client := fake.NewSimpleClientset(
		&corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: "ns-a", Labels: map[string]string{"istio.io/rev": "asm-1-29"}}},
		&corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: "ns-b", Labels: map[string]string{"istio.io/rev": "asm-1-29"}}},
		&corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: "ns-c", Labels: map[string]string{"istio.io/rev": "asm-1-29"}}},
		&appsv1.Deployment{
			ObjectMeta: metav1.ObjectMeta{Name: "web", Namespace: "ns-a", Generation: 1},
			Spec:       appsv1.DeploymentSpec{Replicas: ptr.To[int32](2)},
			Status:     appsv1.DeploymentStatus{ObservedGeneration: 1, UpdatedReplicas: 2, ReadyReplicas: 2},
		},
		&appsv1.Deployment{
			ObjectMeta: metav1.ObjectMeta{Name: "api", Namespace: "ns-b", Generation: 1},
			Spec:       appsv1.DeploymentSpec{Replicas: ptr.To[int32](3)},
			Status:     appsv1.DeploymentStatus{ObservedGeneration: 1, UpdatedReplicas: 3, ReadyReplicas: 3},
		},
		&appsv1.Deployment{
			ObjectMeta: metav1.ObjectMeta{Name: "gate", Namespace: "ns-c", Generation: 1},
			Spec:       appsv1.DeploymentSpec{Replicas: ptr.To[int32](1)},
			Status:     appsv1.DeploymentStatus{ObservedGeneration: 1, UpdatedReplicas: 1, ReadyReplicas: 1},
		},
	)

	err := WaitForRolloutAllNamespaces(context.Background(), client, 5*time.Second, 100*time.Millisecond)
	require.NoError(t, err)
}

func TestWaitForRolloutAllNamespaces_ConcurrentErrors(t *testing.T) {
	client := fake.NewSimpleClientset(
		&corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: "ns-ok", Labels: map[string]string{"istio.io/rev": "asm-1-29"}}},
		&corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: "ns-slow", Labels: map[string]string{"istio.io/rev": "asm-1-29"}}},
		// ns-ok: ready
		&appsv1.Deployment{
			ObjectMeta: metav1.ObjectMeta{Name: "web", Namespace: "ns-ok", Generation: 1},
			Spec:       appsv1.DeploymentSpec{Replicas: ptr.To[int32](1)},
			Status:     appsv1.DeploymentStatus{ObservedGeneration: 1, UpdatedReplicas: 1, ReadyReplicas: 1},
		},
		// ns-slow: stuck — will timeout
		&appsv1.Deployment{
			ObjectMeta: metav1.ObjectMeta{Name: "stuck", Namespace: "ns-slow", Generation: 2},
			Spec:       appsv1.DeploymentSpec{Replicas: ptr.To[int32](1)},
			Status:     appsv1.DeploymentStatus{ObservedGeneration: 1, UpdatedReplicas: 0, ReadyReplicas: 0},
		},
	)

	err := WaitForRolloutAllNamespaces(context.Background(), client, 200*time.Millisecond, 50*time.Millisecond)
	assert.Error(t, err)
	assert.ErrorContains(t, err, "ns-slow")
}

func TestWaitForRolloutAllNamespaces_ContextCancellation(t *testing.T) {
	client := fake.NewSimpleClientset(
		&corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: "ns-a", Labels: map[string]string{"istio.io/rev": "asm-1-29"}}},
		&appsv1.Deployment{
			ObjectMeta: metav1.ObjectMeta{Name: "stuck", Namespace: "ns-a", Generation: 2},
			Spec:       appsv1.DeploymentSpec{Replicas: ptr.To[int32](1)},
			Status:     appsv1.DeploymentStatus{ObservedGeneration: 1, UpdatedReplicas: 0, ReadyReplicas: 0},
		},
	)

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	err := WaitForRolloutAllNamespaces(ctx, client, 5*time.Second, 50*time.Millisecond)
	assert.Error(t, err, "should fail promptly when context is already cancelled")
}

func TestExecuteRestartAllNamespaces_ConcurrentSuccess(t *testing.T) {
	client := fake.NewSimpleClientset(
		&corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: "ns-a", Labels: map[string]string{"istio.io/rev": "asm-1-29"}}},
		&corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: "ns-b", Labels: map[string]string{"istio.io/rev": "asm-1-29"}}},
		// ns-a: stale pod
		&corev1.Pod{
			ObjectMeta: metav1.ObjectMeta{
				Name: "pod-a", Namespace: "ns-a",
				Annotations:     map[string]string{"sidecar.istio.io/status": `{"revision":"asm-1-28"}`},
				OwnerReferences: []metav1.OwnerReference{{Name: "rs-a", Kind: "ReplicaSet", APIVersion: "apps/v1", Controller: ptr.To(true)}},
			},
			Status: corev1.PodStatus{Phase: corev1.PodRunning},
		},
		&appsv1.ReplicaSet{
			ObjectMeta: metav1.ObjectMeta{
				Name: "rs-a", Namespace: "ns-a",
				OwnerReferences: []metav1.OwnerReference{{Name: "deploy-a", Kind: "Deployment", APIVersion: "apps/v1", Controller: ptr.To(true)}},
			},
		},
		&appsv1.Deployment{ObjectMeta: metav1.ObjectMeta{Name: "deploy-a", Namespace: "ns-a"}, Spec: appsv1.DeploymentSpec{Replicas: ptr.To[int32](1)}},
		// ns-b: stale pod
		&corev1.Pod{
			ObjectMeta: metav1.ObjectMeta{
				Name: "pod-b", Namespace: "ns-b",
				Annotations:     map[string]string{"sidecar.istio.io/status": `{"revision":"asm-1-28"}`},
				OwnerReferences: []metav1.OwnerReference{{Name: "rs-b", Kind: "ReplicaSet", APIVersion: "apps/v1", Controller: ptr.To(true)}},
			},
			Status: corev1.PodStatus{Phase: corev1.PodRunning},
		},
		&appsv1.ReplicaSet{
			ObjectMeta: metav1.ObjectMeta{
				Name: "rs-b", Namespace: "ns-b",
				OwnerReferences: []metav1.OwnerReference{{Name: "deploy-b", Kind: "Deployment", APIVersion: "apps/v1", Controller: ptr.To(true)}},
			},
		},
		&appsv1.Deployment{ObjectMeta: metav1.ObjectMeta{Name: "deploy-b", Namespace: "ns-b"}, Spec: appsv1.DeploymentSpec{Replicas: ptr.To[int32](1)}},
	)

	results, err := ExecuteRestartAllNamespaces(context.Background(), client, "asm-1-29")
	require.NoError(t, err)
	require.Len(t, results, 2)

	restartedByNS := map[string][]string{}
	for _, r := range results {
		restartedByNS[r.Namespace] = r.Restarted
	}
	assert.Contains(t, restartedByNS["ns-a"], "deployment/deploy-a")
	assert.Contains(t, restartedByNS["ns-b"], "deployment/deploy-b")
}

func TestUpdateMeshNamespaceLabels(t *testing.T) {
	client := fake.NewSimpleClientset(
		&corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: "app-ns", Labels: map[string]string{"istio.io/rev": "asm-1-28"}}},
		&corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: "other-ns", Labels: map[string]string{"istio.io/rev": "asm-1-28"}}},
		&corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: "already-correct", Labels: map[string]string{"istio.io/rev": "asm-1-29"}}},
		&corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: "no-mesh"}},
	)

	updated, err := UpdateMeshNamespaceLabels(context.Background(), client, "asm-1-29")
	require.NoError(t, err)
	assert.Equal(t, 2, updated)

	ns1, err := client.CoreV1().Namespaces().Get(context.Background(), "app-ns", metav1.GetOptions{})
	require.NoError(t, err)
	assert.Equal(t, "asm-1-29", ns1.Labels["istio.io/rev"])

	ns2, err := client.CoreV1().Namespaces().Get(context.Background(), "other-ns", metav1.GetOptions{})
	require.NoError(t, err)
	assert.Equal(t, "asm-1-29", ns2.Labels["istio.io/rev"])

	ns3, err := client.CoreV1().Namespaces().Get(context.Background(), "no-mesh", metav1.GetOptions{})
	require.NoError(t, err)
	assert.Empty(t, ns3.Labels["istio.io/rev"])
}
