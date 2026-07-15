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

	admissionregistrationv1 "k8s.io/api/admissionregistration/v1"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes/fake"
	"k8s.io/utils/ptr"
)

func TestHealthCheck_Healthy(t *testing.T) {
	client := fake.NewSimpleClientset(
		&corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: "aks-istio-system"}},
		&corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: "aks-istio-ingress"}},
		&appsv1.Deployment{
			ObjectMeta: metav1.ObjectMeta{Name: "istiod-asm-1-28", Namespace: "aks-istio-system"},
			Spec:       appsv1.DeploymentSpec{Replicas: ptr.To[int32](2)},
			Status:     appsv1.DeploymentStatus{AvailableReplicas: 2},
		},
		&corev1.Service{
			ObjectMeta: metav1.ObjectMeta{Name: "aks-istio-ingressgateway-external", Namespace: "aks-istio-ingress"},
			Spec:       corev1.ServiceSpec{Type: corev1.ServiceTypeLoadBalancer, Selector: map[string]string{"app": "gw"}},
			Status:     corev1.ServiceStatus{LoadBalancer: corev1.LoadBalancerStatus{Ingress: []corev1.LoadBalancerIngress{{IP: "10.0.0.1"}}}},
		},
		&corev1.Pod{
			ObjectMeta: metav1.ObjectMeta{Name: "gw-pod", Namespace: "aks-istio-ingress", Labels: map[string]string{"app": "gw"}},
			Status:     corev1.PodStatus{Conditions: []corev1.PodCondition{{Type: corev1.PodReady, Status: corev1.ConditionTrue}}},
		},
	)

	health, err := HealthCheck(context.Background(), client)
	require.NoError(t, err)
	assert.True(t, health.Passed)
	assert.Empty(t, health.Issues)
}

func TestHealthCheck_Unhealthy(t *testing.T) {
	client := fake.NewSimpleClientset(
		&corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: "aks-istio-system"}},
		&corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: "aks-istio-ingress"}},
		&appsv1.Deployment{
			ObjectMeta: metav1.ObjectMeta{Name: "istiod-asm-1-28", Namespace: "aks-istio-system"},
			Spec:       appsv1.DeploymentSpec{Replicas: ptr.To[int32](2)},
			Status:     appsv1.DeploymentStatus{AvailableReplicas: 0},
		},
		&corev1.Service{
			ObjectMeta: metav1.ObjectMeta{Name: "aks-istio-ingressgateway-external", Namespace: "aks-istio-ingress"},
			Spec:       corev1.ServiceSpec{Type: corev1.ServiceTypeLoadBalancer, Selector: map[string]string{"app": "gw"}},
		},
	)

	health, err := HealthCheck(context.Background(), client)
	require.NoError(t, err)
	assert.False(t, health.Passed)
	assert.Len(t, health.Issues, 3)
}

func TestHealthCheck_NoControlPlane(t *testing.T) {
	client := fake.NewSimpleClientset(
		&corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: "aks-istio-system"}},
		&corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: "aks-istio-ingress"}},
		&corev1.Service{
			ObjectMeta: metav1.ObjectMeta{Name: "aks-istio-ingressgateway-external", Namespace: "aks-istio-ingress"},
			Spec:       corev1.ServiceSpec{Type: corev1.ServiceTypeLoadBalancer, Selector: map[string]string{"app": "gw"}},
			Status:     corev1.ServiceStatus{LoadBalancer: corev1.LoadBalancerStatus{Ingress: []corev1.LoadBalancerIngress{{IP: "10.0.0.1"}}}},
		},
		&corev1.Pod{
			ObjectMeta: metav1.ObjectMeta{Name: "gw-pod", Namespace: "aks-istio-ingress", Labels: map[string]string{"app": "gw"}},
			Status:     corev1.PodStatus{Conditions: []corev1.PodCondition{{Type: corev1.PodReady, Status: corev1.ConditionTrue}}},
		},
	)

	health, err := HealthCheck(context.Background(), client)
	require.NoError(t, err)
	assert.False(t, health.Passed)
	assert.Contains(t, health.Issues, "no istiod deployments found")
}

func TestHealthCheck_NoGateways(t *testing.T) {
	client := fake.NewSimpleClientset(
		&corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: "aks-istio-system"}},
		&corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: "aks-istio-ingress"}},
		&appsv1.Deployment{
			ObjectMeta: metav1.ObjectMeta{Name: "istiod-asm-1-29", Namespace: "aks-istio-system"},
			Spec:       appsv1.DeploymentSpec{Replicas: ptr.To[int32](2)},
			Status:     appsv1.DeploymentStatus{AvailableReplicas: 2},
		},
	)

	health, err := HealthCheck(context.Background(), client)
	require.NoError(t, err)
	assert.False(t, health.Passed)
	assert.Contains(t, health.Issues, "no ingress gateway services found")
}

func TestHealthCheck_MissingNamespaces(t *testing.T) {
	client := fake.NewSimpleClientset()

	health, err := HealthCheck(context.Background(), client)
	require.NoError(t, err)
	assert.False(t, health.Passed)
	assert.Len(t, health.Issues, 2)
	assert.Contains(t, health.Issues[0], "namespace aks-istio-system does not exist")
	assert.Contains(t, health.Issues[1], "namespace aks-istio-ingress does not exist")
}

func TestVerifyUpgrade_Passed(t *testing.T) {
	client := fake.NewSimpleClientset(
		&corev1.ConfigMap{
			ObjectMeta: metav1.ObjectMeta{Name: "istio-shared-configmap-asm-1-29", Namespace: "aks-istio-system"},
		},
		&corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: "app-ns", Labels: map[string]string{"istio.io/rev": "asm-1-29"}}},
		&corev1.Pod{
			ObjectMeta: metav1.ObjectMeta{
				Name: "web-pod", Namespace: "app-ns",
				Annotations: map[string]string{"sidecar.istio.io/status": `{"revision":"asm-1-29"}`},
			},
			Status: corev1.PodStatus{Phase: corev1.PodRunning},
		},
	)

	v, err := VerifyUpgrade(context.Background(), client, "asm-1-29", "")
	require.NoError(t, err)
	assert.True(t, v.Passed)
	assert.Empty(t, v.Issues)
}

func TestVerifyUpgrade_TagBased(t *testing.T) {
	client := fake.NewSimpleClientset(
		&corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: "app-ns", Labels: map[string]string{"istio.io/rev": "prod-stable"}}},
		&corev1.ConfigMap{ObjectMeta: metav1.ObjectMeta{Name: "istio-shared-configmap-asm-1-29", Namespace: "aks-istio-system"}},
		&admissionregistrationv1.MutatingWebhookConfiguration{
			ObjectMeta: metav1.ObjectMeta{Name: "istio-revision-tag-prod-stable-aks-istio-system"},
			Webhooks: []admissionregistrationv1.MutatingWebhook{{
				Name:                    "rev-tag.istio.io",
				ClientConfig:            admissionregistrationv1.WebhookClientConfig{Service: &admissionregistrationv1.ServiceReference{Name: "istiod-asm-1-29"}},
				AdmissionReviewVersions: []string{"v1"},
				SideEffects: func() *admissionregistrationv1.SideEffectClass {
					s := admissionregistrationv1.SideEffectClassNone
					return &s
				}(),
			}},
		},
		&corev1.Pod{
			ObjectMeta: metav1.ObjectMeta{
				Name: "web-pod", Namespace: "app-ns",
				Annotations: map[string]string{"sidecar.istio.io/status": `{"revision":"asm-1-29"}`},
			},
			Status: corev1.PodStatus{Phase: corev1.PodRunning},
		},
	)

	v, err := VerifyUpgrade(context.Background(), client, "asm-1-29", "prod-stable")
	require.NoError(t, err)
	assert.True(t, v.Passed, "tag-based namespace label should be accepted: %v", v.Issues)
}

func TestVerifyUpgrade_Failed(t *testing.T) {
	client := fake.NewSimpleClientset(
		&corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: "app-ns", Labels: map[string]string{"istio.io/rev": "asm-1-28"}}},
		&corev1.Pod{
			ObjectMeta: metav1.ObjectMeta{
				Name: "stale-pod", Namespace: "app-ns",
				Annotations: map[string]string{"sidecar.istio.io/status": `{"revision":"asm-1-28"}`},
			},
			Status: corev1.PodStatus{Phase: corev1.PodRunning},
		},
	)

	v, err := VerifyUpgrade(context.Background(), client, "asm-1-29", "")
	require.NoError(t, err)
	assert.False(t, v.Passed)
	assert.Len(t, v.Issues, 3)
}

func TestVerifyUpgrade_TagWebhookMissing(t *testing.T) {
	client := fake.NewSimpleClientset(
		&corev1.ConfigMap{ObjectMeta: metav1.ObjectMeta{Name: "istio-shared-configmap-asm-1-29", Namespace: "aks-istio-system"}},
		&corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: "app-ns", Labels: map[string]string{"istio.io/rev": "prod-stable"}}},
		&corev1.Pod{
			ObjectMeta: metav1.ObjectMeta{
				Name: "web-pod", Namespace: "app-ns",
				Annotations: map[string]string{"sidecar.istio.io/status": `{"revision":"asm-1-29"}`},
			},
			Status: corev1.PodStatus{Phase: corev1.PodRunning},
		},
	)

	v, err := VerifyUpgrade(context.Background(), client, "asm-1-29", "prod-stable")
	require.NoError(t, err)
	assert.False(t, v.Passed, "should fail when tag webhook is missing")
	assert.Contains(t, v.Issues[0], "tag webhook")
}

func TestVerifyUpgrade_TagWebhookWrongTarget(t *testing.T) {
	client := fake.NewSimpleClientset(
		&corev1.ConfigMap{ObjectMeta: metav1.ObjectMeta{Name: "istio-shared-configmap-asm-1-29", Namespace: "aks-istio-system"}},
		&corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: "app-ns", Labels: map[string]string{"istio.io/rev": "prod-stable"}}},
		&admissionregistrationv1.MutatingWebhookConfiguration{
			ObjectMeta: metav1.ObjectMeta{Name: "istio-revision-tag-prod-stable-aks-istio-system"},
			Webhooks: []admissionregistrationv1.MutatingWebhook{{
				Name:                    "rev-tag.istio.io",
				ClientConfig:            admissionregistrationv1.WebhookClientConfig{Service: &admissionregistrationv1.ServiceReference{Name: "istiod-asm-1-28"}},
				AdmissionReviewVersions: []string{"v1"},
				SideEffects: func() *admissionregistrationv1.SideEffectClass {
					s := admissionregistrationv1.SideEffectClassNone
					return &s
				}(),
			}},
		},
		&corev1.Pod{
			ObjectMeta: metav1.ObjectMeta{
				Name: "web-pod", Namespace: "app-ns",
				Annotations: map[string]string{"sidecar.istio.io/status": `{"revision":"asm-1-29"}`},
			},
			Status: corev1.PodStatus{Phase: corev1.PodRunning},
		},
	)

	v, err := VerifyUpgrade(context.Background(), client, "asm-1-29", "prod-stable")
	require.NoError(t, err)
	assert.False(t, v.Passed, "should fail when tag webhook points at wrong revision")
	assert.Contains(t, v.Issues[0], "istiod-asm-1-28")
}

func TestCheckOrphanedWorkloads_NoneOrphaned(t *testing.T) {
	client := fake.NewSimpleClientset(
		&corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: "app-ns", Labels: map[string]string{"istio.io/rev": "asm-1-29"}}},
		&corev1.Pod{
			ObjectMeta: metav1.ObjectMeta{
				Name: "web-pod", Namespace: "app-ns",
				Annotations: map[string]string{"sidecar.istio.io/status": `{"revision":"asm-1-29"}`},
			},
			Status: corev1.PodStatus{Phase: corev1.PodRunning},
		},
	)

	orphaned, err := CheckOrphanedWorkloads(context.Background(), client, "asm-1-29", []string{"asm-1-28", "asm-1-29"})
	require.NoError(t, err)
	assert.Empty(t, orphaned)
}

func TestCheckOrphanedWorkloads_StalePodsDetected(t *testing.T) {
	client := fake.NewSimpleClientset(
		&corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: "app-ns", Labels: map[string]string{"istio.io/rev": "asm-1-28"}}},
		&corev1.Pod{
			ObjectMeta: metav1.ObjectMeta{
				Name: "stale-pod", Namespace: "app-ns",
				Annotations: map[string]string{"sidecar.istio.io/status": `{"revision":"asm-1-28"}`},
			},
			Status: corev1.PodStatus{Phase: corev1.PodRunning},
		},
		&corev1.Pod{
			ObjectMeta: metav1.ObjectMeta{
				Name: "good-pod", Namespace: "app-ns",
				Annotations: map[string]string{"sidecar.istio.io/status": `{"revision":"asm-1-29"}`},
			},
			Status: corev1.PodStatus{Phase: corev1.PodRunning},
		},
	)

	orphaned, err := CheckOrphanedWorkloads(context.Background(), client, "asm-1-29", []string{"asm-1-28", "asm-1-29"})
	require.NoError(t, err)
	assert.Len(t, orphaned, 1)
	assert.Contains(t, orphaned[0], "stale-pod")
	assert.Contains(t, orphaned[0], "asm-1-28")
}

func TestCheckOrphanedWorkloads_NoRetiringRevisions(t *testing.T) {
	client := fake.NewSimpleClientset()

	orphaned, err := CheckOrphanedWorkloads(context.Background(), client, "asm-1-29", []string{"asm-1-29"})
	require.NoError(t, err)
	assert.Empty(t, orphaned)
}

func TestMatchesSelector(t *testing.T) {
	tests := []struct {
		name     string
		labels   map[string]string
		selector map[string]string
		want     bool
	}{
		{
			name:     "nil selector matches nothing",
			labels:   map[string]string{"app": "gw"},
			selector: nil,
			want:     false,
		},
		{
			name:     "empty selector matches nothing",
			labels:   map[string]string{"app": "gw"},
			selector: map[string]string{},
			want:     false,
		},
		{
			name:     "matching selector",
			labels:   map[string]string{"app": "gw", "env": "prod"},
			selector: map[string]string{"app": "gw"},
			want:     true,
		},
		{
			name:     "non-matching selector",
			labels:   map[string]string{"app": "web"},
			selector: map[string]string{"app": "gw"},
			want:     false,
		},
		{
			name:     "nil labels with selector",
			labels:   nil,
			selector: map[string]string{"app": "gw"},
			want:     false,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.want, matchesSelector(tc.labels, tc.selector))
		})
	}
}
