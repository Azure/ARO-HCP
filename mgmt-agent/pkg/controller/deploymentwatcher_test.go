// Copyright 2025 Microsoft Corporation
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

package controller

import (
	"testing"
	"time"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	kubeinformers "k8s.io/client-go/informers"
	"k8s.io/client-go/kubernetes/fake"
)

func TestLogDeploymentEvent(t *testing.T) {
	tests := []struct {
		name      string
		eventType string
		deploy    *appsv1.Deployment
	}{
		{
			name:      "Add event",
			eventType: "Add",
			deploy: &appsv1.Deployment{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "operator",
					Namespace: "hypershift",
				},
			},
		},
		{
			name:      "Update event",
			eventType: "Update",
			deploy: &appsv1.Deployment{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "control-plane-operator",
					Namespace: "ocm-hcp-test",
				},
			},
		},
		{
			name:      "Delete event",
			eventType: "Delete",
			deploy: &appsv1.Deployment{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "operator",
					Namespace: "hypershift",
				},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			logDeploymentEvent(tt.eventType, tt.deploy)
		})
	}
}

func TestIsWatchedNamespace(t *testing.T) {
	tests := []struct {
		namespace string
		expected  bool
	}{
		{"hypershift", true},
		{"ocm-hcp-test", true},
		{"ocm-arohcp-12345", true},
		{"ocm-", true},
		{"kube-system", false},
		{"default", false},
		{"openshift-operators", false},
		{"", false},
	}

	for _, tt := range tests {
		t.Run(tt.namespace, func(t *testing.T) {
			if got := isWatchedNamespace(tt.namespace); got != tt.expected {
				t.Errorf("isWatchedNamespace(%q) = %v, want %v", tt.namespace, got, tt.expected)
			}
		})
	}
}

func TestDeploymentStateChanged(t *testing.T) {
	baseConditions := []appsv1.DeploymentCondition{
		{
			Type:           appsv1.DeploymentAvailable,
			Status:         corev1.ConditionTrue,
			LastUpdateTime: metav1.NewTime(time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC)),
		},
		{
			Type:           appsv1.DeploymentProgressing,
			Status:         corev1.ConditionTrue,
			LastUpdateTime: metav1.NewTime(time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC)),
		},
	}

	baseDeploy := &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{
			Name:            "operator",
			Namespace:       "hypershift",
			ResourceVersion: "100",
		},
		Spec: appsv1.DeploymentSpec{
			Template: corev1.PodTemplateSpec{
				Spec: corev1.PodSpec{
					Containers: []corev1.Container{
						{Name: "operator", Image: "quay.io/hypershift/operator:v1.0"},
					},
				},
			},
		},
		Status: appsv1.DeploymentStatus{
			ObservedGeneration:  1,
			Replicas:            2,
			UpdatedReplicas:     2,
			ReadyReplicas:       2,
			AvailableReplicas:   2,
			UnavailableReplicas: 0,
			Conditions:          baseConditions,
		},
	}

	tests := []struct {
		name     string
		modify   func(d *appsv1.Deployment)
		expected bool
	}{
		{
			name:     "no changes",
			modify:   func(_ *appsv1.Deployment) {},
			expected: false,
		},
		{
			name: "only resourceVersion changes",
			modify: func(d *appsv1.Deployment) {
				d.ResourceVersion = "200"
			},
			expected: false,
		},
		{
			name: "only managedFields changes",
			modify: func(d *appsv1.Deployment) {
				d.ManagedFields = []metav1.ManagedFieldsEntry{
					{Manager: "kube-controller-manager", Operation: metav1.ManagedFieldsOperationUpdate},
				}
			},
			expected: false,
		},
		{
			name: "only condition timestamps change",
			modify: func(d *appsv1.Deployment) {
				d.Status.Conditions = []appsv1.DeploymentCondition{
					{
						Type:           appsv1.DeploymentAvailable,
						Status:         corev1.ConditionTrue,
						LastUpdateTime: metav1.NewTime(time.Date(2025, 6, 1, 0, 0, 0, 0, time.UTC)),
					},
					{
						Type:           appsv1.DeploymentProgressing,
						Status:         corev1.ConditionTrue,
						LastUpdateTime: metav1.NewTime(time.Date(2025, 6, 1, 0, 0, 0, 0, time.UTC)),
					},
				}
			},
			expected: false,
		},
		{
			name: "readyReplicas changes",
			modify: func(d *appsv1.Deployment) {
				d.Status.ReadyReplicas = 1
			},
			expected: true,
		},
		{
			name: "availableReplicas changes",
			modify: func(d *appsv1.Deployment) {
				d.Status.AvailableReplicas = 1
			},
			expected: true,
		},
		{
			name: "unavailableReplicas changes",
			modify: func(d *appsv1.Deployment) {
				d.Status.UnavailableReplicas = 1
			},
			expected: true,
		},
		{
			name: "observedGeneration changes",
			modify: func(d *appsv1.Deployment) {
				d.Status.ObservedGeneration = 2
			},
			expected: true,
		},
		{
			name: "condition status changes",
			modify: func(d *appsv1.Deployment) {
				d.Status.Conditions = []appsv1.DeploymentCondition{
					{Type: appsv1.DeploymentAvailable, Status: corev1.ConditionFalse},
					{Type: appsv1.DeploymentProgressing, Status: corev1.ConditionTrue},
				}
			},
			expected: true,
		},
		{
			name: "new condition added",
			modify: func(d *appsv1.Deployment) {
				d.Status.Conditions = append(d.Status.Conditions,
					appsv1.DeploymentCondition{Type: appsv1.DeploymentReplicaFailure, Status: corev1.ConditionTrue},
				)
			},
			expected: true,
		},
		{
			name: "spec template image changes",
			modify: func(d *appsv1.Deployment) {
				d.Spec.Template.Spec.Containers[0].Image = "quay.io/hypershift/operator:v2.0"
			},
			expected: true,
		},
		{
			name: "spec template env var added",
			modify: func(d *appsv1.Deployment) {
				d.Spec.Template.Spec.Containers[0].Env = []corev1.EnvVar{
					{Name: "FOO", Value: "bar"},
				}
			},
			expected: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			newDeploy := baseDeploy.DeepCopy()
			tt.modify(newDeploy)
			if got := deploymentStateChanged(baseDeploy, newDeploy); got != tt.expected {
				t.Errorf("deploymentStateChanged() = %v, want %v", got, tt.expected)
			}
		})
	}
}

func TestNewDeploymentWatcher(t *testing.T) {
	clientset := fake.NewSimpleClientset()
	factory := kubeinformers.NewSharedInformerFactory(clientset, 0)

	w, err := NewDeploymentWatcher(factory.Apps().V1().Deployments())
	if err != nil {
		t.Fatalf("NewDeploymentWatcher() returned error: %v", err)
	}
	if w == nil {
		t.Fatal("NewDeploymentWatcher() returned nil")
	}
	if w.deploymentSynced == nil {
		t.Fatal("NewDeploymentWatcher() did not set deploymentSynced")
	}
}
