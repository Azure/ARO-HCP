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

	appsv1 "k8s.io/api/apps/v1"
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
