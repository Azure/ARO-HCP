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
	"reflect"
	"regexp"
	"testing"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	dynamicfake "k8s.io/client-go/dynamic/fake"

	"sigs.k8s.io/yaml"

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

func TestKarpenterCustomResourceStateConfig(t *testing.T) {
	type metricConfig struct {
		Name string `yaml:"name"`
		Each struct {
			Type  string `yaml:"type"`
			Gauge struct {
				Path           []string            `yaml:"path"`
				LabelsFromPath map[string][]string `yaml:"labelsFromPath"`
				ValueFrom      []string            `yaml:"valueFrom"`
			} `yaml:"gauge"`
		} `yaml:"each"`
	}
	type resourceConfig struct {
		GroupVersionKind struct {
			Group   string `yaml:"group"`
			Version string `yaml:"version"`
			Kind    string `yaml:"kind"`
		} `yaml:"groupVersionKind"`
		LabelsFromPath map[string][]string `yaml:"labelsFromPath"`
		Metrics        []metricConfig      `yaml:"metrics"`
	}
	var config struct {
		Spec struct {
			Resources []resourceConfig `yaml:"resources"`
		} `yaml:"spec"`
	}

	if err := yaml.Unmarshal([]byte(customResourceStateConfig), &config); err != nil {
		t.Fatalf("failed to parse custom resource state config: %v", err)
	}

	findResource := func(kind string) resourceConfig {
		t.Helper()
		for _, resource := range config.Spec.Resources {
			if resource.GroupVersionKind.Kind == kind {
				return resource
			}
		}
		t.Fatalf("custom resource state config does not contain %s", kind)
		return resourceConfig{}
	}

	wantResources := map[string]string{
		"NodePool":     "karpenter.sh/v1",
		"NodeClaim":    "karpenter.sh/v1",
		"AKSNodeClass": "karpenter.azure.com/v1beta1",
	}
	for kind, groupVersion := range wantResources {
		resource := findResource(kind)
		if got := resource.GroupVersionKind.Group + "/" + resource.GroupVersionKind.Version; got != groupVersion {
			t.Errorf("%s group/version = %q, want %q", kind, got, groupVersion)
		}
		if got, want := resource.LabelsFromPath, map[string][]string{"name": {"metadata", "name"}}; !reflect.DeepEqual(got, want) {
			t.Errorf("%s labelsFromPath = %#v, want %#v", kind, got, want)
		}
	}

	wantConditionLabels := map[string][]string{
		"condition": {"type"},
		"status":    {"status"},
		"reason":    {"reason"},
	}
	for _, kind := range []string{"NodePool", "NodeClaim", "AKSNodeClass"} {
		resource := findResource(kind)
		var conditionMetric *metricConfig
		for i := range resource.Metrics {
			if resource.Metrics[i].Name == "status_condition_transition_time_seconds" {
				conditionMetric = &resource.Metrics[i]
				break
			}
		}
		if conditionMetric == nil {
			t.Errorf("%s does not define status_condition_transition_time_seconds", kind)
			continue
		}
		if conditionMetric.Each.Type != "Gauge" {
			t.Errorf("%s condition metric type = %q, want Gauge", kind, conditionMetric.Each.Type)
		}
		if got, want := conditionMetric.Each.Gauge.Path, []string{"status", "conditions"}; !reflect.DeepEqual(got, want) {
			t.Errorf("%s condition path = %#v, want %#v", kind, got, want)
		}
		if got, want := conditionMetric.Each.Gauge.ValueFrom, []string{"lastTransitionTime"}; !reflect.DeepEqual(got, want) {
			t.Errorf("%s condition valueFrom = %#v, want %#v", kind, got, want)
		}
		if got := conditionMetric.Each.Gauge.LabelsFromPath; !reflect.DeepEqual(got, wantConditionLabels) {
			t.Errorf("%s condition labels = %#v, want %#v", kind, got, wantConditionLabels)
		}
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
	sm, err := buildServiceMonitor("ocm-arohcppers-abc123-xyz", DefaultMonitoringAPIGroup, metav1.OwnerReference{
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

func TestBuildServiceMonitorFiltersKarpenterConditions(t *testing.T) {
	sm, err := buildServiceMonitor("ocm-arohcppers-abc123-xyz", DefaultMonitoringAPIGroup, metav1.OwnerReference{
		APIVersion: "hypershift.openshift.io/v1beta1",
		Kind:       "HostedControlPlane",
		Name:       "test-hcp",
		UID:        "uid-123",
	})
	if err != nil {
		t.Fatalf("buildServiceMonitor() error: %v", err)
	}

	endpoints, found, err := unstructured.NestedSlice(sm.Object, "spec", "endpoints")
	if err != nil {
		t.Fatalf("failed to read ServiceMonitor endpoints: %v", err)
	}
	if !found || len(endpoints) != 1 {
		t.Fatalf("ServiceMonitor endpoints = %#v, want one endpoint", endpoints)
	}
	endpoint, ok := endpoints[0].(map[string]interface{})
	if !ok {
		t.Fatalf("ServiceMonitor endpoint has type %T, want map[string]interface{}", endpoints[0])
	}
	relabelings, found, err := unstructured.NestedSlice(endpoint, "metricRelabelings")
	if err != nil {
		t.Fatalf("failed to read metric relabelings: %v", err)
	}
	if !found {
		t.Fatal("ServiceMonitor has no metric relabelings")
	}

	foundMarkerRule := false
	foundTrueDropRule := false
	foundMarkerCleanupRule := false
	markerRuleIndex := -1
	trueDropRuleIndex := -1
	markerCleanupRuleIndex := -1
	for i, relabeling := range relabelings {
		rule, ok := relabeling.(map[string]interface{})
		if !ok {
			continue
		}
		switch {
		case rule["action"] == "replace" && rule["regex"] == retainedTrueNodeClaimConditionRE:
			if got, want := rule["sourceLabels"], []interface{}{"__name__", "condition", "status"}; !reflect.DeepEqual(got, want) {
				t.Errorf("retained True condition marker sourceLabels = %#v, want %#v", got, want)
			}
			if got, want := rule["targetLabel"], retainedTrueConditionMarkerLabel; got != want {
				t.Errorf("retained True condition marker targetLabel = %#v, want %#v", got, want)
			}
			if got, want := rule["replacement"], "true"; got != want {
				t.Errorf("retained True condition marker replacement = %#v, want %#v", got, want)
			}
			foundMarkerRule = true
			markerRuleIndex = i
		case rule["action"] == "drop" && rule["regex"] == unmarkedTrueKarpenterConditionRE:
			if got, want := rule["sourceLabels"], []interface{}{"__name__", "status", retainedTrueConditionMarkerLabel}; !reflect.DeepEqual(got, want) {
				t.Errorf("True condition drop sourceLabels = %#v, want %#v", got, want)
			}
			foundTrueDropRule = true
			trueDropRuleIndex = i
		case rule["action"] == "labeldrop" && rule["regex"] == retainedTrueConditionMarkerLabel:
			foundMarkerCleanupRule = true
			markerCleanupRuleIndex = i
		}
	}
	if !foundMarkerRule {
		t.Fatalf("ServiceMonitor is missing retained True condition marker rule %q", retainedTrueNodeClaimConditionRE)
	}
	if !foundTrueDropRule {
		t.Fatalf("ServiceMonitor is missing unmarked True Karpenter condition drop rule %q", unmarkedTrueKarpenterConditionRE)
	}
	if !foundMarkerCleanupRule {
		t.Fatalf("ServiceMonitor is missing temporary marker cleanup rule %q", retainedTrueConditionMarkerLabel)
	}
	if !(markerRuleIndex < trueDropRuleIndex && trueDropRuleIndex < markerCleanupRuleIndex) {
		t.Errorf(
			"condition relabel order = marker %d, drop %d, cleanup %d; want marker < drop < cleanup",
			markerRuleIndex,
			trueDropRuleIndex,
			markerCleanupRuleIndex,
		)
	}

	retainedTrueConditionRE := regexp.MustCompile("^(?:" + retainedTrueNodeClaimConditionRE + ")$")
	unmarkedTrueConditionRE := regexp.MustCompile("^(?:" + unmarkedTrueKarpenterConditionRE + ")$")
	tests := []struct {
		name       string
		metricName string
		condition  string
		status     string
		wantDrop   bool
	}{
		{
			name:       "completed launch",
			metricName: nodeClaimConditionMetricName,
			condition:  "Launched",
			status:     "True",
			wantDrop:   true,
		},
		{
			name:       "failed launch",
			metricName: nodeClaimConditionMetricName,
			condition:  "Launched",
			status:     "False",
		},
		{
			name:       "unresolved launch",
			metricName: nodeClaimConditionMetricName,
			condition:  "Launched",
			status:     "Unknown",
		},
		{
			name:       "active instance termination",
			metricName: nodeClaimConditionMetricName,
			condition:  "InstanceTerminating",
			status:     "True",
		},
		{
			name:       "drift lifecycle signal",
			metricName: nodeClaimConditionMetricName,
			condition:  "Drifted",
			status:     "True",
			wantDrop:   true,
		},
		{
			name:       "healthy NodePool",
			metricName: "karpenter_nodepool_status_condition_transition_time_seconds",
			condition:  "Ready",
			status:     "True",
			wantDrop:   true,
		},
		{
			name:       "unresolved NodePool",
			metricName: "karpenter_nodepool_status_condition_transition_time_seconds",
			condition:  "Ready",
			status:     "Unknown",
		},
		{
			name:       "healthy AKSNodeClass",
			metricName: "karpenter_aksnodeclass_status_condition_transition_time_seconds",
			condition:  "Ready",
			status:     "True",
			wantDrop:   true,
		},
		{
			name:       "unhealthy AKSNodeClass",
			metricName: "karpenter_aksnodeclass_status_condition_transition_time_seconds",
			condition:  "Ready",
			status:     "False",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			marker := ""
			if retainedTrueConditionRE.MatchString(tt.metricName + ";" + tt.condition + ";" + tt.status) {
				marker = "true"
			}
			gotDrop := unmarkedTrueConditionRE.MatchString(tt.metricName + ";" + tt.status + ";" + marker)
			if gotDrop != tt.wantDrop {
				t.Errorf("drop decision = %t, want %t", gotDrop, tt.wantDrop)
			}
		})
	}
}

// TestBuildServiceMonitorAMAGroup verifies that in AMA mode the KSM monitor is
// emitted directly as the azmonitoring.coreos.com type AMA discovers, so it does
// not have to be created as monitoring.coreos.com and translated (which would
// duplicate the microsoft_metrics_include_label relabel rule).
func TestBuildServiceMonitorAMAGroup(t *testing.T) {
	const amaGroup = "azmonitoring.coreos.com"
	sm, err := buildServiceMonitor("ocm-arohcppers-abc123-xyz", amaGroup, metav1.OwnerReference{
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
