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

package amaprune

import (
	"context"
	"testing"

	networkingv1 "k8s.io/api/networking/v1"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	dynamicfake "k8s.io/client-go/dynamic/fake"
	kubefake "k8s.io/client-go/kubernetes/fake"
	k8stesting "k8s.io/client-go/testing"

	"github.com/Azure/ARO-HCP/mgmt-agent/pkg/controller/amanetpolicy"
	"github.com/Azure/ARO-HCP/mgmt-agent/pkg/controller/monitortranslator"
)

func gvrToListKinds() map[schema.GroupVersionResource]string {
	return map[schema.GroupVersionResource]string{
		monitortranslator.TargetServiceMonitorGVR: "ServiceMonitorList",
		monitortranslator.TargetPodMonitorGVR:     "PodMonitorList",
	}
}

// newMonitor builds an unstructured azmonitoring monitor with a single owner
// reference of the given apiVersion.
func newMonitor(gvr schema.GroupVersionResource, namespace, name, ownerAPIVersion, ownerKind string) *unstructured.Unstructured {
	kind := "ServiceMonitor"
	if gvr.Resource == "podmonitors" {
		kind = "PodMonitor"
	}
	return &unstructured.Unstructured{
		Object: map[string]any{
			"apiVersion": gvr.Group + "/" + gvr.Version,
			"kind":       kind,
			"metadata": map[string]any{
				"name":      name,
				"namespace": namespace,
				"ownerReferences": []any{
					map[string]any{
						"apiVersion": ownerAPIVersion,
						"kind":       ownerKind,
						"name":       "owner",
						"uid":        "owner-uid",
					},
				},
			},
		},
	}
}

func newNetworkPolicy(namespace, name string, labels map[string]string) *networkingv1.NetworkPolicy {
	return &networkingv1.NetworkPolicy{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: namespace,
			Labels:    labels,
		},
	}
}

func listMonitorNames(t *testing.T, c *Controller, gvr schema.GroupVersionResource) []string {
	t.Helper()
	list, err := c.dynamicClient.Resource(gvr).Namespace(metav1.NamespaceAll).List(context.Background(), metav1.ListOptions{})
	if err != nil {
		t.Fatalf("failed to list %s: %v", gvr.Resource, err)
	}
	var names []string
	for _, item := range list.Items {
		names = append(names, item.GetName())
	}
	return names
}

// TestPruneDeletesTranslatorMonitorsOnly verifies that monitors owned by a
// monitoring.coreos.com/v1 source (translator copies) are deleted, while the
// ksmhcp monitor owned by a HostedControlPlane is left alone.
func TestPruneDeletesTranslatorMonitorsOnly(t *testing.T) {
	smGVR := monitortranslator.TargetServiceMonitorGVR
	pmGVR := monitortranslator.TargetPodMonitorGVR

	translatorSM := newMonitor(smGVR, "ocm-test", "translated-sm", "monitoring.coreos.com/v1", "ServiceMonitor")
	ksmSM := newMonitor(smGVR, "ocm-test", "kube-state-metrics-hcp", "hypershift.openshift.io/v1beta1", "HostedControlPlane")
	translatorPM := newMonitor(pmGVR, "ocm-test", "translated-pm", "monitoring.coreos.com/v1", "PodMonitor")

	scheme := runtime.NewScheme()
	fakeDyn := dynamicfake.NewSimpleDynamicClientWithCustomListKinds(scheme, gvrToListKinds(), translatorSM, ksmSM, translatorPM)
	kubeClient := kubefake.NewSimpleClientset()

	c := NewController(fakeDyn, kubeClient)
	if err := c.pruneOnce(context.Background()); err != nil {
		t.Fatalf("pruneOnce() error: %v", err)
	}

	smNames := listMonitorNames(t, c, smGVR)
	if len(smNames) != 1 || smNames[0] != "kube-state-metrics-hcp" {
		t.Errorf("expected only the HCP-owned ServiceMonitor to remain, got %v", smNames)
	}
	if pmNames := listMonitorNames(t, c, pmGVR); len(pmNames) != 0 {
		t.Errorf("expected all translator PodMonitors deleted, got %v", pmNames)
	}
}

// TestPruneToleratesMissingCRD verifies that a missing azmonitoring CRD is not an
// error (pure-OSS cluster that never ran AMA) and NetworkPolicy pruning still
// proceeds.
func TestPruneToleratesMissingCRD(t *testing.T) {
	scheme := runtime.NewScheme()
	fakeDyn := dynamicfake.NewSimpleDynamicClientWithCustomListKinds(scheme, gvrToListKinds())
	// Simulate the azmonitoring CRD being absent: List returns a NoMatch error.
	noMatch := func(action k8stesting.Action) (bool, runtime.Object, error) {
		return true, nil, &meta.NoResourceMatchError{PartialResource: action.GetResource()}
	}
	fakeDyn.PrependReactor("list", "servicemonitors", noMatch)
	fakeDyn.PrependReactor("list", "podmonitors", noMatch)

	np := newNetworkPolicy("ocm-test", "ama-metrics-allow", map[string]string{
		"app.kubernetes.io/managed-by": "mgmt-agent-ama-netpolicy",
	})
	kubeClient := kubefake.NewSimpleClientset(np)

	c := NewController(fakeDyn, kubeClient)
	if err := c.pruneOnce(context.Background()); err != nil {
		t.Fatalf("pruneOnce() must tolerate missing CRD, got error: %v", err)
	}

	remaining, err := kubeClient.NetworkingV1().NetworkPolicies(metav1.NamespaceAll).List(context.Background(), metav1.ListOptions{})
	if err != nil {
		t.Fatalf("failed to list NetworkPolicies: %v", err)
	}
	if len(remaining.Items) != 0 {
		t.Errorf("expected AMA NetworkPolicy to be pruned despite missing CRD, got %d remaining", len(remaining.Items))
	}
}

// TestPruneNetworkPoliciesByLabel verifies only NetworkPolicies carrying the
// amanetpolicy managed-by label are deleted.
func TestPruneNetworkPoliciesByLabel(t *testing.T) {
	amaNP := newNetworkPolicy("ocm-test", "ama-metrics-allow", map[string]string{
		"app.kubernetes.io/managed-by": "mgmt-agent-ama-netpolicy",
	})
	otherNP := newNetworkPolicy("ocm-test", "unrelated", map[string]string{
		"app.kubernetes.io/managed-by": "someone-else",
	})

	scheme := runtime.NewScheme()
	fakeDyn := dynamicfake.NewSimpleDynamicClientWithCustomListKinds(scheme, gvrToListKinds())
	kubeClient := kubefake.NewSimpleClientset(amaNP, otherNP)

	c := NewController(fakeDyn, kubeClient)
	if err := c.pruneNetworkPolicies(context.Background()); err != nil {
		t.Fatalf("pruneNetworkPolicies() error: %v", err)
	}

	remaining, err := kubeClient.NetworkingV1().NetworkPolicies(metav1.NamespaceAll).List(context.Background(), metav1.ListOptions{})
	if err != nil {
		t.Fatalf("failed to list NetworkPolicies: %v", err)
	}
	if len(remaining.Items) != 1 || remaining.Items[0].Name != "unrelated" {
		var names []string
		for _, np := range remaining.Items {
			names = append(names, np.Name)
		}
		t.Errorf("expected only the unrelated NetworkPolicy to remain, got %v", names)
	}
}

// TestLabelSelectorMatchesAMANetPolicy guards against the pruner's selector
// drifting from the amanetpolicy controller's own label.
func TestLabelSelectorMatchesAMANetPolicy(t *testing.T) {
	if amanetpolicy.LabelSelector != "app.kubernetes.io/managed-by=mgmt-agent-ama-netpolicy" {
		t.Errorf("unexpected amanetpolicy.LabelSelector %q; update the pruner tests", amanetpolicy.LabelSelector)
	}
}
