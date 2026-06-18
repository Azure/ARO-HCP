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
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes/fake"
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
