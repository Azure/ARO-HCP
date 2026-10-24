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

package e2e

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/stretchr/testify/require"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	discoveryv1 "k8s.io/api/discovery/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	dynamicfake "k8s.io/client-go/dynamic/fake"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
)

func TestCollectIngressCertificateDiagnosticsSafeArtifacts(t *testing.T) {
	for _, componentLabel := range []bool{true, false} {
		name := "deployment-selector-fallback"
		if componentLabel {
			name = "controller-component-label"
		}
		t.Run(name, func(t *testing.T) {
			client, requests := ingressDiagnosticsTestClient(t, componentLabel, false)
			dynamicClient := ingressDiagnosticsDynamicClient()
			directory := t.TempDir()
			require.NoError(t, collectIngressCertificateDiagnostics(context.Background(), client, dynamicClient, directory))
			artifacts := filepath.Join(directory, "ingress-certificate-diagnostics")
			entries, err := os.ReadDir(artifacts)
			require.NoError(t, err)
			for _, entry := range entries {
				data, readErr := os.ReadFile(filepath.Join(artifacts, entry.Name()))
				require.NoError(t, readErr)
				require.NotContains(t, string(data), "DO-NOT-EXPORT")
				info, statErr := entry.Info()
				require.NoError(t, statErr)
				require.Equal(t, os.FileMode(0600), info.Mode().Perm())
			}
			pods, err := os.ReadFile(filepath.Join(artifacts, "openshift-dns-pods.json"))
			require.NoError(t, err)
			require.Contains(t, string(pods), "ContainersNotReady")
			require.Contains(t, string(pods), "dns-node")
			issuer, err := os.ReadFile(filepath.Join(artifacts, "clusterissuer.json"))
			require.NoError(t, err)
			require.Contains(t, string(issuer), "ErrInitIssuer")
			require.Contains(t, string(issuer), "lookup acme-staging")
			endpoints, err := os.ReadFile(filepath.Join(artifacts, "dns-endpointslices.json"))
			require.NoError(t, err)
			require.Contains(t, string(endpoints), "10.0.0.2")
			require.Contains(t, string(endpoints), "\"ready\": false")
			_, err = os.Stat(filepath.Join(artifacts, "controller.log"))
			require.NoError(t, err)
			for _, action := range dynamicClient.Actions() {
				require.NotEqual(t, "secrets", action.GetResource().Resource)
			}
			seen := requests()
			for _, request := range seen {
				require.NotContains(t, request, "secrets")
				require.NotContains(t, request, "webhook/log")
			}
		})
	}
}

func TestCollectIngressCertificateDiagnosticsPreservesPartialArtifacts(t *testing.T) {
	client, _ := ingressDiagnosticsTestClient(t, true, true)
	dynamicClient := ingressDiagnosticsDynamicClient()
	require.NoError(t, dynamicClient.Resource(schema.GroupVersionResource{Group: "cert-manager.io", Version: "v1", Resource: "clusterissuers"}).Delete(context.Background(), "letsencrypt-staging", metav1.DeleteOptions{}))
	directory := t.TempDir()
	// A directory at one artifact filename forces a write failure without blocking others.
	require.NoError(t, os.MkdirAll(filepath.Join(directory, "ingress-certificate-diagnostics", "cert-manager-pods.json"), 0700))
	err := collectIngressCertificateDiagnostics(context.Background(), client, dynamicClient, directory)
	require.ErrorContains(t, err, "list cert-manager deployments")
	require.ErrorContains(t, err, "write cert-manager-pods.json")
	require.ErrorContains(t, err, "get clusterissuers/letsencrypt-staging")
	for _, name := range []string{"controller.log", "cert-manager-events.json", "dns-clusteroperator.json", "dns-service.json", "dns-endpointslices.json"} {
		_, statErr := os.Stat(filepath.Join(directory, "ingress-certificate-diagnostics", name))
		require.NoError(t, statErr)
	}
}

func TestCollectIngressCertificateDiagnosticsDirectoryFailure(t *testing.T) {
	directory := filepath.Join(t.TempDir(), "file")
	require.NoError(t, os.WriteFile(directory, nil, 0600))
	// No API access is needed when the destination cannot be created.
	err := collectIngressCertificateDiagnostics(context.Background(), nil, nil, directory)
	require.ErrorContains(t, err, "create ingress certificate diagnostics directory")
}

func ingressDiagnosticsDynamicClient() *dynamicfake.FakeDynamicClient {
	issuer := &unstructured.Unstructured{Object: map[string]any{
		"apiVersion": "cert-manager.io/v1", "kind": "ClusterIssuer",
		"metadata": map[string]any{"name": "letsencrypt-staging", "annotations": map[string]any{"credential": "DO-NOT-EXPORT-annotation"}},
		"spec":     map[string]any{"acme": map[string]any{"privateKeySecretRef": map[string]any{"name": "DO-NOT-EXPORT-key"}}},
		"status": map[string]any{
			"conditions":          []any{map[string]any{"type": "Ready", "status": "False", "reason": "ErrInitIssuer", "message": "lookup acme-staging: no such host"}},
			"unexpectedExtension": "DO-NOT-EXPORT-extension",
		},
	}}
	operator := &unstructured.Unstructured{Object: map[string]any{
		"apiVersion": "config.openshift.io/v1", "kind": "ClusterOperator", "metadata": map[string]any{"name": "dns"},
		"status": map[string]any{"conditions": []any{map[string]any{"type": "Available", "status": "False", "reason": "DNSNotReady"}}},
	}}
	return dynamicfake.NewSimpleDynamicClient(runtime.NewScheme(), issuer, operator)
}

func ingressDiagnosticsTestClient(t *testing.T, componentLabel, failDeployments bool) (kubernetes.Interface, func() []string) {
	t.Helper()
	ready := false
	port := int32(5353)
	udp := corev1.ProtocolUDP
	podLabels := map[string]string{"app": "cert-manager"}
	if componentLabel {
		podLabels["app.kubernetes.io/component"] = "controller"
	}
	controller := corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{Name: "controller", Namespace: "cert-manager", Labels: podLabels, Annotations: map[string]string{"credential": "DO-NOT-EXPORT-annotation"}},
		Spec: corev1.PodSpec{
			Containers: []corev1.Container{{Name: "cert-manager-controller", Env: []corev1.EnvVar{{Name: "TOKEN", Value: "DO-NOT-EXPORT-env"}}}},
			Volumes:    []corev1.Volume{{Name: "token", VolumeSource: corev1.VolumeSource{Projected: &corev1.ProjectedVolumeSource{Sources: []corev1.VolumeProjection{{ServiceAccountToken: &corev1.ServiceAccountTokenProjection{Path: "DO-NOT-EXPORT-token"}}}}}}},
		},
	}
	responses := map[string]any{
		"/apis/apps/v1/namespaces/cert-manager/deployments": &appsv1.DeploymentList{Items: []appsv1.Deployment{{
			ObjectMeta: metav1.ObjectMeta{Name: "cert-manager", Namespace: "cert-manager"},
			Spec:       appsv1.DeploymentSpec{Selector: &metav1.LabelSelector{MatchLabels: map[string]string{"app": "cert-manager"}}, Template: corev1.PodTemplateSpec{Spec: controller.Spec}},
		}}},
		"/api/v1/namespaces/cert-manager/pods":                              &corev1.PodList{Items: []corev1.Pod{controller, {ObjectMeta: metav1.ObjectMeta{Name: "webhook", Labels: map[string]string{"app": "webhook"}}}}},
		"/api/v1/namespaces/openshift-dns/pods":                             &corev1.PodList{Items: []corev1.Pod{{ObjectMeta: metav1.ObjectMeta{Name: "dns-node"}, Status: corev1.PodStatus{Conditions: []corev1.PodCondition{{Type: corev1.PodReady, Status: corev1.ConditionFalse, Reason: "ContainersNotReady"}}}}}},
		"/api/v1/namespaces/cert-manager/events":                            &corev1.EventList{Items: []corev1.Event{{ObjectMeta: metav1.ObjectMeta{Name: "mount", Annotations: map[string]string{"credential": "DO-NOT-EXPORT-event"}}, Reason: "FailedMount", Message: "secret dns-default-metrics-tls not found"}}},
		"/api/v1/namespaces/openshift-dns/events":                           &corev1.EventList{},
		"/api/v1/events":                                                    &corev1.EventList{Items: []corev1.Event{{Reason: "ErrInitIssuer", Message: "issuer DNS lookup failed"}}},
		"/api/v1/namespaces/openshift-dns/services/dns-default":             &corev1.Service{ObjectMeta: metav1.ObjectMeta{Name: "dns-default"}, Spec: corev1.ServiceSpec{ClusterIP: "172.30.0.10", Ports: []corev1.ServicePort{{Name: "dns", Port: 53, Protocol: udp}}}},
		"/apis/discovery.k8s.io/v1/namespaces/openshift-dns/endpointslices": &discoveryv1.EndpointSliceList{Items: []discoveryv1.EndpointSlice{{ObjectMeta: metav1.ObjectMeta{Name: "dns-default-slice"}, AddressType: discoveryv1.AddressTypeIPv4, Ports: []discoveryv1.EndpointPort{{Port: &port, Protocol: &udp}}, Endpoints: []discoveryv1.Endpoint{{Addresses: []string{"10.0.0.2"}, Conditions: discoveryv1.EndpointConditions{Ready: &ready}}}}}},
	}
	var mutex sync.Mutex
	var requests []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mutex.Lock()
		requests = append(requests, r.URL.RequestURI())
		mutex.Unlock()
		if r.URL.Path == "/api/v1/namespaces/cert-manager/pods/controller/log" {
			_, _ = w.Write([]byte("controller: lookup acme-staging: no such host\n"))
			return
		}
		w.Header().Set("Content-Type", "application/json")
		if failDeployments && strings.Contains(r.URL.Path, "/deployments") {
			w.WriteHeader(http.StatusForbidden)
			_ = json.NewEncoder(w).Encode(metav1.Status{Status: "Failure", Reason: metav1.StatusReasonForbidden, Code: 403, Message: "deployment status denied"})
			return
		}
		response, ok := responses[r.URL.Path]
		if !ok {
			t.Errorf("unexpected diagnostic request: %s", r.URL.RequestURI())
			w.WriteHeader(http.StatusNotFound)
			return
		}
		_ = json.NewEncoder(w).Encode(response)
	}))
	t.Cleanup(server.Close)
	client, err := kubernetes.NewForConfig(&rest.Config{Host: server.URL})
	require.NoError(t, err)
	return client, func() []string {
		mutex.Lock()
		defer mutex.Unlock()
		return append([]string(nil), requests...)
	}
}
