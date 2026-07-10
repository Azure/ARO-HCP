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

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	kubeinformers "k8s.io/client-go/informers"
	"k8s.io/client-go/kubernetes/fake"
)

func TestLogConfigMapEvent(t *testing.T) {
	tests := []struct {
		name      string
		eventType string
		cm        *corev1.ConfigMap
	}{
		{
			name:      "Add event",
			eventType: "Add",
			cm: &corev1.ConfigMap{
				ObjectMeta: metav1.ObjectMeta{
					Name:      RouterConfigMapName,
					Namespace: "ocm-hcp-test",
				},
				Data: map[string]string{
					"haproxy.cfg": "global\n  maxconn 4096",
				},
			},
		},
		{
			name:      "Update event",
			eventType: "Update",
			cm: &corev1.ConfigMap{
				ObjectMeta: metav1.ObjectMeta{
					Name:      RouterConfigMapName,
					Namespace: "ocm-hcp-test",
				},
			},
		},
		{
			name:      "Delete event",
			eventType: "Delete",
			cm: &corev1.ConfigMap{
				ObjectMeta: metav1.ObjectMeta{
					Name:      RouterConfigMapName,
					Namespace: "ocm-hcp-test",
				},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			logConfigMapEvent(tt.eventType, tt.cm)
		})
	}
}

func TestNewConfigMapWatcher(t *testing.T) {
	clientset := fake.NewSimpleClientset()
	factory := kubeinformers.NewSharedInformerFactory(clientset, 0)

	w, err := NewConfigMapWatcher(factory.Core().V1().ConfigMaps())
	if err != nil {
		t.Fatalf("NewConfigMapWatcher() returned error: %v", err)
	}
	if w == nil {
		t.Fatal("NewConfigMapWatcher() returned nil")
	}
	if w.cmSynced == nil {
		t.Fatal("NewConfigMapWatcher() did not set cmSynced")
	}
}
