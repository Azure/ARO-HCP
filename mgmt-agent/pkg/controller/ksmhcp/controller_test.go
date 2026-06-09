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

package ksmhcp

import (
	"testing"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	hypershiftv1beta1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
)

func TestIsKubeAPIServerAvailable(t *testing.T) {
	tests := []struct {
		name       string
		conditions []metav1.Condition
		want       bool
	}{
		{
			name: "HCP ready with KubeAPIServer available",
			conditions: []metav1.Condition{
				{Type: "EtcdAvailable", Status: metav1.ConditionTrue},
				{Type: "KubeAPIServerAvailable", Status: metav1.ConditionTrue},
				{Type: "Available", Status: metav1.ConditionTrue},
			},
			want: true,
		},
		{
			name: "HCP provisioning, KubeAPIServer not yet available",
			conditions: []metav1.Condition{
				{Type: "EtcdAvailable", Status: metav1.ConditionTrue},
				{Type: "KubeAPIServerAvailable", Status: metav1.ConditionFalse},
			},
			want: false,
		},
		{
			name: "HCP early provisioning, no KubeAPIServer condition yet",
			conditions: []metav1.Condition{
				{Type: "InfrastructureReady", Status: metav1.ConditionTrue},
			},
			want: false,
		},
		{
			name: "empty status, freshly created HCP",
			want: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			hcp := &hypershiftv1beta1.HostedControlPlane{
				Status: hypershiftv1beta1.HostedControlPlaneStatus{
					Conditions: tt.conditions,
				},
			}
			if got := isKubeAPIServerAvailable(hcp); got != tt.want {
				t.Errorf("isKubeAPIServerAvailable() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestBuildDeployment(t *testing.T) {
	dep := buildDeployment(
		"ocm-arohcppers-abc123-xyz",
		"mcr.microsoft.com/oss/v2/kubernetes/kube-state-metrics@sha256:abc",
		serviceNetworkKubeconfigSecret,
		serviceNetworkKubeconfigKey,
		testOwnerRef(),
	)

	if dep.APIVersion != "apps/v1" || dep.Kind != "Deployment" {
		t.Errorf("TypeMeta not set for SSA: got %s/%s", dep.APIVersion, dep.Kind)
	}
	if dep.Namespace != "ocm-arohcppers-abc123-xyz" {
		t.Errorf("namespace = %q, want ocm-arohcppers-abc123-xyz", dep.Namespace)
	}
	if dep.Name != resourceName {
		t.Errorf("name = %q, want %q", dep.Name, resourceName)
	}

	container := dep.Spec.Template.Spec.Containers[0]
	if container.Image != "mcr.microsoft.com/oss/v2/kubernetes/kube-state-metrics@sha256:abc" {
		t.Errorf("wrong image: %s", container.Image)
	}

	vol := dep.Spec.Template.Spec.Volumes[0]
	if vol.Secret.SecretName != serviceNetworkKubeconfigSecret {
		t.Errorf("kubeconfig secret = %q, want %q", vol.Secret.SecretName, serviceNetworkKubeconfigSecret)
	}
	if vol.Secret.Items[0].Key != serviceNetworkKubeconfigKey {
		t.Errorf("kubeconfig key = %q, want %q", vol.Secret.Items[0].Key, serviceNetworkKubeconfigKey)
	}

	if dep.Spec.Template.Spec.AutomountServiceAccountToken == nil || *dep.Spec.Template.Spec.AutomountServiceAccountToken {
		t.Error("automountServiceAccountToken should be false")
	}
	if container.LivenessProbe == nil || container.ReadinessProbe == nil {
		t.Error("probes not set")
	}
}

func TestBuildServiceMonitorInjectsLabels(t *testing.T) {
	sm, err := buildServiceMonitor("ocm-arohcppers-abc123-xyz", testOwnerRef())
	if err != nil {
		t.Fatalf("buildServiceMonitor() error: %v", err)
	}

	spec := sm.Object["spec"].(map[string]interface{})
	endpoints := spec["endpoints"].([]interface{})
	ep := endpoints[0].(map[string]interface{})
	relabelings := ep["metricRelabelings"].([]interface{})

	if len(relabelings) != 1 {
		t.Fatalf("expected 1 relabeling (namespace), got %d", len(relabelings))
	}

	nsRelabel := relabelings[0].(map[string]interface{})
	if nsRelabel["targetLabel"] != "namespace" || nsRelabel["replacement"] != "ocm-arohcppers-abc123-xyz" {
		t.Errorf("namespace relabel incorrect: %v", nsRelabel)
	}
}

func TestBuildServiceMonitorTypeMeta(t *testing.T) {
	sm, err := buildServiceMonitor("ocm-test", testOwnerRef())
	if err != nil {
		t.Fatalf("buildServiceMonitor() error: %v", err)
	}
	if sm.GetAPIVersion() != "monitoring.coreos.com/v1" || sm.GetKind() != "ServiceMonitor" {
		t.Errorf("TypeMeta = %s/%s, want monitoring.coreos.com/v1/ServiceMonitor", sm.GetAPIVersion(), sm.GetKind())
	}
}


func testOwnerRef() metav1.OwnerReference {
	return metav1.OwnerReference{
		APIVersion: "hypershift.openshift.io/v1beta1",
		Kind:       "HostedControlPlane",
		Name:       "test-hcp",
		UID:        "uid-123",
	}
}
