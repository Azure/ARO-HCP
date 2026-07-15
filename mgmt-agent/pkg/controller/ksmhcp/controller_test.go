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

	"github.com/Azure/ARO-Tools/testutil"

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
		metav1.OwnerReference{
			APIVersion: "hypershift.openshift.io/v1beta1",
			Kind:       "HostedControlPlane",
			Name:       "test-hcp",
			UID:        "uid-123",
		},
	)

	testutil.CompareWithFixture(t, dep)
}

func TestBuildService(t *testing.T) {
	svc := buildService("ocm-arohcppers-abc123-xyz", metav1.OwnerReference{
		APIVersion: "hypershift.openshift.io/v1beta1",
		Kind:       "HostedControlPlane",
		Name:       "test-hcp",
		UID:        "uid-123",
	})

	testutil.CompareWithFixture(t, svc)
}

func TestBuildServiceMonitor(t *testing.T) {
	sm, err := buildServiceMonitor("ocm-arohcppers-abc123-xyz", metav1.OwnerReference{
		APIVersion: "hypershift.openshift.io/v1beta1",
		Kind:       "HostedControlPlane",
		Name:       "test-hcp",
		UID:        "uid-123",
	})
	if err != nil {
		t.Fatalf("buildServiceMonitor() error: %v", err)
	}

	testutil.CompareWithFixture(t, sm)
}
