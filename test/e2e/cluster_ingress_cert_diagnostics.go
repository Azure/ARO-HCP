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
	"errors"
	"fmt"
	"os"
	"path/filepath"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/kubernetes"
)

// Collects scoped failure evidence without
// fetching secrets or serializing pod specs, issuer specs, or arbitrary metadata.
// The caller supplies the cleanup deadline; every API request shares that budget.
func collectIngressCertificateDiagnostics(ctx context.Context, kubeClient kubernetes.Interface, dynamicClient dynamic.Interface, directory string) error {
	directory = filepath.Join(directory, "ingress-certificate-diagnostics")
	if err := os.MkdirAll(directory, 0700); err != nil {
		return fmt.Errorf("create ingress certificate diagnostics directory: %w", err)
	}
	var failures []error
	recordError := func(operation string, err error) {
		if err != nil {
			failures = append(failures, fmt.Errorf("%s: %w", operation, err))
		}
	}
	writeJSON := func(name string, value any) {
		data, err := json.MarshalIndent(value, "", "  ")
		if err == nil {
			err = os.WriteFile(filepath.Join(directory, name), data, 0600)
		}
		recordError("write "+name, err)
	}

	// Retain deployment status, never the pod template (which may contain credentials).
	deployments, err := kubeClient.AppsV1().Deployments("cert-manager").List(ctx, metav1.ListOptions{})
	recordError("list cert-manager deployments", err)
	controllerSelector := labels.Selector(nil)
	if err == nil {
		summaries := make([]map[string]any, 0, len(deployments.Items))
		for _, deployment := range deployments.Items {
			summaries = append(summaries, map[string]any{
				"name": deployment.Name, "namespace": deployment.Namespace,
				"generation": deployment.Generation, "replicas": deployment.Spec.Replicas,
				"status": deployment.Status,
			})
			// The pinned upstream manifest calls the controller deployment cert-manager.
			// Its own selector is safer than an assumed broad app label fallback.
			if deployment.Name == "cert-manager" && deployment.Spec.Selector != nil {
				selector, selectorErr := metav1.LabelSelectorAsSelector(deployment.Spec.Selector)
				recordError("read cert-manager controller selector", selectorErr)
				if selectorErr == nil && !selector.Empty() {
					controllerSelector = selector
				}
			}
		}
		writeJSON("cert-manager-deployments.json", summaries)
	}

	for _, namespace := range []string{"cert-manager", "openshift-dns"} {
		pods, listErr := kubeClient.CoreV1().Pods(namespace).List(ctx, metav1.ListOptions{})
		recordError("list "+namespace+" pods", listErr)
		if listErr == nil {
			summaries := make([]map[string]any, 0, len(pods.Items))
			for _, pod := range pods.Items {
				summaries = append(summaries, ingressCertificatePodSummary(pod))
			}
			writeJSON(namespace+"-pods.json", summaries)
			if namespace == "cert-manager" {
				var controllers []corev1.Pod
				for _, pod := range pods.Items {
					if pod.Labels["app.kubernetes.io/component"] == "controller" {
						controllers = append(controllers, pod)
					}
				}
				if len(controllers) == 0 && controllerSelector != nil {
					for _, pod := range pods.Items {
						if controllerSelector.Matches(labels.Set(pod.Labels)) {
							controllers = append(controllers, pod)
						}
					}
				}
				for _, pod := range controllers {
					containerName := ""
					for _, container := range pod.Spec.Containers {
						if container.Name == "cert-manager-controller" || len(pod.Spec.Containers) == 1 {
							containerName = container.Name
						}
					}
					if containerName == "" {
						recordError("select controller log container", fmt.Errorf("pod %s has no unambiguous controller container", pod.Name))
						continue
					}
					tailLines, limitBytes := int64(200), int64(128*1024)
					data, logErr := kubeClient.CoreV1().Pods(namespace).GetLogs(pod.Name, &corev1.PodLogOptions{
						Container: containerName, TailLines: &tailLines, LimitBytes: &limitBytes,
					}).Do(ctx).Raw()
					recordError("fetch controller logs "+pod.Name, logErr)
					if logErr == nil {
						recordError("write controller logs "+pod.Name, os.WriteFile(filepath.Join(directory, pod.Name+".log"), data, 0600))
					}
				}
			}
		}
		events, eventErr := kubeClient.CoreV1().Events(namespace).List(ctx, metav1.ListOptions{})
		recordError("list "+namespace+" events", eventErr)
		if eventErr == nil {
			writeJSON(namespace+"-events.json", ingressCertificateEventSummaries(events.Items))
		}
	}

	for _, resource := range []struct {
		gvr  schema.GroupVersionResource
		name string
		file string
	}{
		{schema.GroupVersionResource{Group: "cert-manager.io", Version: "v1", Resource: "clusterissuers"}, "letsencrypt-staging", "clusterissuer.json"},
		{schema.GroupVersionResource{Group: "config.openshift.io", Version: "v1", Resource: "clusteroperators"}, "dns", "dns-clusteroperator.json"},
	} {
		object, getErr := dynamicClient.Resource(resource.gvr).Get(ctx, resource.name, metav1.GetOptions{})
		recordError("get "+resource.gvr.Resource+"/"+resource.name, getErr)
		if getErr == nil {
			// Explicit status fields exclude ACME account keys and arbitrary extensions.
			summary := map[string]any{"name": object.GetName(), "generation": object.GetGeneration()}
			for _, field := range []string{"conditions", "versions", "lastFailureTime", "observedGeneration"} {
				value, found, fieldErr := unstructured.NestedFieldCopy(object.Object, "status", field)
				recordError("read "+resource.name+" status."+field, fieldErr)
				if found && fieldErr == nil {
					summary[field] = value
				}
			}
			writeJSON(resource.file, summary)
		}
	}
	issuerEvents, err := kubeClient.CoreV1().Events(metav1.NamespaceAll).List(ctx, metav1.ListOptions{
		FieldSelector: "involvedObject.kind=ClusterIssuer,involvedObject.name=letsencrypt-staging",
	})
	recordError("list ClusterIssuer events", err)
	if err == nil {
		writeJSON("clusterissuer-events.json", ingressCertificateEventSummaries(issuerEvents.Items))
	}

	service, err := kubeClient.CoreV1().Services("openshift-dns").Get(ctx, "dns-default", metav1.GetOptions{})
	recordError("get DNS service", err)
	if err == nil {
		writeJSON("dns-service.json", map[string]any{
			"name": service.Name, "namespace": service.Namespace,
			"clusterIP": service.Spec.ClusterIP, "clusterIPs": service.Spec.ClusterIPs,
			"ports": service.Spec.Ports, "selector": service.Spec.Selector,
			"publishNotReadyAddresses": service.Spec.PublishNotReadyAddresses,
		})
	}
	slices, err := kubeClient.DiscoveryV1().EndpointSlices("openshift-dns").List(ctx, metav1.ListOptions{
		LabelSelector: "kubernetes.io/service-name=dns-default",
	})
	recordError("list DNS EndpointSlices", err)
	if err == nil {
		summaries := make([]map[string]any, 0, len(slices.Items))
		for _, slice := range slices.Items {
			endpoints := make([]map[string]any, 0, len(slice.Endpoints))
			for _, endpoint := range slice.Endpoints {
				endpoints = append(endpoints, map[string]any{
					"addresses": endpoint.Addresses, "conditions": endpoint.Conditions,
					"nodeName": endpoint.NodeName, "hostname": endpoint.Hostname,
				})
			}
			summaries = append(summaries, map[string]any{
				"name": slice.Name, "addressType": slice.AddressType,
				"ports": slice.Ports, "endpoints": endpoints,
			})
		}
		writeJSON("dns-endpointslices.json", summaries)
	}
	return errors.Join(failures...)
}

// Summarizes pod scheduling and readiness without specs or metadata credentials.
func ingressCertificatePodSummary(pod corev1.Pod) map[string]any {
	return map[string]any{
		"name": pod.Name, "namespace": pod.Namespace, "nodeName": pod.Spec.NodeName,
		"creationTimestamp": pod.CreationTimestamp, "deletionTimestamp": pod.DeletionTimestamp,
		"status": pod.Status,
	}
}

// Retains event observations without arbitrary metadata annotations.
func ingressCertificateEventSummaries(events []corev1.Event) []map[string]any {
	summaries := make([]map[string]any, 0, len(events))
	for _, event := range events {
		summaries = append(summaries, map[string]any{
			"name": event.Name, "namespace": event.Namespace,
			"object": event.InvolvedObject, "type": event.Type,
			"reason": event.Reason, "message": event.Message, "count": event.Count,
			"firstTimestamp": event.FirstTimestamp, "lastTimestamp": event.LastTimestamp,
			"eventTime": event.EventTime, "series": event.Series,
			"reportingController": event.ReportingController, "source": event.Source,
		})
	}
	return summaries
}
