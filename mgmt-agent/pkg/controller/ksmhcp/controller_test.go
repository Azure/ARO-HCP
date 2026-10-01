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
	"context"
	"testing"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	dynamicfake "k8s.io/client-go/dynamic/fake"

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

func TestBuildConfigMap(t *testing.T) {
	cm := buildConfigMap("ocm-arohcppers-abc123-xyz", metav1.OwnerReference{
		APIVersion: "hypershift.openshift.io/v1beta1",
		Kind:       "HostedControlPlane",
		Name:       "test-hcp",
		UID:        "uid-123",
	})

	testutil.CompareWithFixture(t, cm)
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
	// Pass non-empty region/environment to prove OSS mode ignores them (output
	// must stay byte-identical with main).
	sm, err := buildServiceMonitor("ocm-arohcppers-abc123-xyz", DefaultMonitoringAPIGroup, "westus3", "dev", metav1.OwnerReference{
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

// TestBuildServiceMonitorAMAGroup verifies that in AMA mode the KSM monitor is
// emitted directly as the azmonitoring.coreos.com type AMA discovers, so it does
// not have to be created as monitoring.coreos.com and translated. It also
// verifies the per-series routing label is microsoft_metrics_account (the label
// the HCP DCR's labelIncludeFilter keys on), not the DCR filter key
// microsoft_metrics_include_label.
func TestBuildServiceMonitorAMAGroup(t *testing.T) {
	const amaGroup = "azmonitoring.coreos.com"
	sm, err := buildServiceMonitor("ocm-arohcppers-abc123-xyz", amaGroup, "westus3", "dev", metav1.OwnerReference{
		APIVersion: "hypershift.openshift.io/v1beta1",
		Kind:       "HostedControlPlane",
		Name:       "test-hcp",
		UID:        "uid-123",
	})
	if err != nil {
		t.Fatalf("buildServiceMonitor() error: %v", err)
	}

	if got, want := sm.GetAPIVersion(), amaGroup+"/v1"; got != want {
		t.Errorf("apiVersion = %q, want %q", got, want)
	}
	if got, want := ServiceMonitorGVRForGroup(amaGroup).Group, amaGroup; got != want {
		t.Errorf("ServiceMonitorGVRForGroup group = %q, want %q", got, want)
	}
	if got := routingTargetLabels(t, sm); !got["microsoft_metrics_account"] {
		t.Errorf("AMA monitor missing microsoft_metrics_account routing relabel; got labels %v", got)
	} else if got["microsoft_metrics_include_label"] {
		t.Errorf("AMA monitor must not use the DCR filter key microsoft_metrics_include_label as a series label; got labels %v", got)
	}
}

// TestBuildServiceMonitorOSSRoutingLabel verifies that in OSS mode the KSM
// monitor keeps emitting the historical microsoft_metrics_include_label relabel,
// byte-identical with main. Routing is done by namespace in the agent's
// remote-write config, so the label is a harmless no-op there (see Q7).
func TestBuildServiceMonitorOSSRoutingLabel(t *testing.T) {
	// Pass non-empty region/environment to prove OSS mode ignores them (output
	// must stay byte-identical with main).
	sm, err := buildServiceMonitor("ocm-arohcppers-abc123-xyz", DefaultMonitoringAPIGroup, "westus3", "dev", metav1.OwnerReference{
		APIVersion: "hypershift.openshift.io/v1beta1",
		Kind:       "HostedControlPlane",
		Name:       "test-hcp",
		UID:        "uid-123",
	})
	if err != nil {
		t.Fatalf("buildServiceMonitor() error: %v", err)
	}

	got := routingTargetLabels(t, sm)
	if !got["microsoft_metrics_include_label"] {
		t.Errorf("OSS monitor missing microsoft_metrics_include_label routing relabel; got labels %v", got)
	}
	if got["microsoft_metrics_account"] {
		t.Errorf("OSS monitor must stay byte-identical with main and not emit microsoft_metrics_account; got labels %v", got)
	}
	// OSS mode must ignore region/environment entirely; the Prometheus agent
	// supplies them via externalLabels, so the KSM monitor must not stamp them.
	if got["region"] || got["environment"] {
		t.Errorf("OSS monitor must not stamp region/environment relabels; got labels %v", got)
	}
}

// routingTargetLabels returns the set of targetLabel values found in the first
// endpoint's metricRelabelings of a KSM ServiceMonitor.
func routingTargetLabels(t *testing.T, sm *unstructured.Unstructured) map[string]bool {
	t.Helper()
	endpoints, _, err := unstructured.NestedSlice(sm.Object, "spec", "endpoints")
	if err != nil || len(endpoints) == 0 {
		t.Fatalf("unexpected endpoints %v (err %v)", endpoints, err)
	}
	relabelings, _, err := unstructured.NestedSlice(endpoints[0].(map[string]any), "metricRelabelings")
	if err != nil {
		t.Fatalf("failed to read metricRelabelings: %v", err)
	}
	labels := map[string]bool{}
	for _, rc := range relabelings {
		if target, _ := rc.(map[string]any)["targetLabel"].(string); target != "" {
			labels[target] = true
		}
	}
	return labels
}

// TestDeleteStaleServiceMonitor verifies that when the controller runs in AMA
// mode it removes the leftover monitoring.coreos.com kube-state-metrics monitor
// created before the monitoringApiGroup switch, so only a single active monitor
// remains and the translator does not collide with it.
func TestDeleteStaleServiceMonitor(t *testing.T) {
	ossGVR := ServiceMonitorGVRForGroup(DefaultMonitoringAPIGroup)
	gvrToListKind := map[schema.GroupVersionResource]string{
		ossGVR: "ServiceMonitorList",
		ServiceMonitorGVRForGroup(AMAMonitoringAPIGroup): "ServiceMonitorList",
	}

	stale := &unstructured.Unstructured{Object: map[string]any{
		"apiVersion": DefaultMonitoringAPIGroup + "/v1",
		"kind":       "ServiceMonitor",
		"metadata": map[string]any{
			"name":      resourceName,
			"namespace": "ocm-test",
		},
	}}

	dc := dynamicfake.NewSimpleDynamicClientWithCustomListKinds(runtime.NewScheme(), gvrToListKind, stale)
	c := &KSMHCPController{dynamicClient: dc, monitoringAPIGroup: AMAMonitoringAPIGroup}

	if err := c.deleteStaleServiceMonitor(context.Background(), "ocm-test"); err != nil {
		t.Fatalf("deleteStaleServiceMonitor() error: %v", err)
	}

	_, err := dc.Resource(ossGVR).Namespace("ocm-test").Get(context.Background(), resourceName, metav1.GetOptions{})
	if !apierrors.IsNotFound(err) {
		t.Errorf("expected stale monitor to be deleted, got err %v", err)
	}

	// Idempotent: deleting again when nothing is left is not an error.
	if err := c.deleteStaleServiceMonitor(context.Background(), "ocm-test"); err != nil {
		t.Errorf("deleteStaleServiceMonitor() second call error: %v", err)
	}
}
