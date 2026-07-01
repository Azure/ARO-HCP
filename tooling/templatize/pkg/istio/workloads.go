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

package istio

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/go-logr/logr"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/client-go/kubernetes"
	"k8s.io/utils/ptr"
)

const (
	istioSystemNamespace  = "aks-istio-system"
	istioIngressNamespace = "aks-istio-ingress"
)

type MeshNamespace struct {
	Name          string
	RevisionLabel string
}

func GetMeshNamespaces(ctx context.Context, client kubernetes.Interface) ([]MeshNamespace, error) {
	nsList, err := client.CoreV1().Namespaces().List(ctx, metav1.ListOptions{
		LabelSelector: "istio.io/rev",
	})
	if err != nil {
		return nil, fmt.Errorf("failed to list mesh namespaces: %w", err)
	}
	var namespaces []MeshNamespace
	for _, ns := range nsList.Items {
		namespaces = append(namespaces, MeshNamespace{
			Name:          ns.Name,
			RevisionLabel: ns.Labels["istio.io/rev"],
		})
	}
	return namespaces, nil
}

func UpdateMeshNamespaceLabels(ctx context.Context, client kubernetes.Interface, newRevision string) (int, error) {
	logger := logr.FromContextOrDiscard(ctx).WithName("namespace-labels")
	namespaces, err := GetMeshNamespaces(ctx, client)
	if err != nil {
		return 0, err
	}
	revJSON, err := json.Marshal(newRevision)
	if err != nil {
		return 0, fmt.Errorf("failed to marshal revision label: %w", err)
	}
	updated := 0
	for _, ns := range namespaces {
		if ns.RevisionLabel == newRevision {
			continue
		}
		patch := fmt.Appendf(nil, `{"metadata":{"labels":{"istio.io/rev":%s}}}`, revJSON)
		if _, err := client.CoreV1().Namespaces().Patch(ctx, ns.Name, types.MergePatchType, patch, metav1.PatchOptions{}); err != nil {
			return updated, fmt.Errorf("failed to update label on namespace %s: %w", ns.Name, err)
		}
		logger.Info("Updated revision label", "namespace", ns.Name, "from", ns.RevisionLabel, "to", newRevision)
		updated++
	}
	return updated, nil
}

type ControlPlaneStatus struct {
	Revision  string
	Ready     bool
	Replicas  int32
	Available int32
}

func GetControlPlaneStatus(ctx context.Context, client kubernetes.Interface) ([]ControlPlaneStatus, error) {
	deps, err := client.AppsV1().Deployments(istioSystemNamespace).List(ctx, metav1.ListOptions{})
	if err != nil {
		return nil, fmt.Errorf("failed to list istiod deployments: %w", err)
	}
	var results []ControlPlaneStatus
	for _, d := range deps.Items {
		if !strings.HasPrefix(d.Name, "istiod-") {
			continue
		}
		replicas := int32(1)
		if d.Spec.Replicas != nil {
			replicas = *d.Spec.Replicas
		}
		results = append(results, ControlPlaneStatus{
			Revision:  strings.TrimPrefix(d.Name, "istiod-"),
			Replicas:  replicas,
			Available: d.Status.AvailableReplicas,
			Ready:     d.Status.AvailableReplicas >= replicas,
		})
	}
	return results, nil
}

type IngressGatewayStatus struct {
	ServiceName string
	ExternalIP  string
	HealthyPods int
	Annotations map[string]string
}

func GetIngressGatewayStatus(ctx context.Context, client kubernetes.Interface) ([]IngressGatewayStatus, error) {
	svcs, err := client.CoreV1().Services(istioIngressNamespace).List(ctx, metav1.ListOptions{})
	if err != nil {
		return nil, fmt.Errorf("failed to list ingress services: %w", err)
	}
	pods, err := client.CoreV1().Pods(istioIngressNamespace).List(ctx, metav1.ListOptions{})
	if err != nil {
		return nil, fmt.Errorf("failed to list ingress pods: %w", err)
	}

	var results []IngressGatewayStatus
	for _, svc := range svcs.Items {
		if svc.Spec.Type != corev1.ServiceTypeLoadBalancer {
			continue
		}
		status := IngressGatewayStatus{
			ServiceName: svc.Name,
			Annotations: svc.Annotations,
		}
		if len(svc.Status.LoadBalancer.Ingress) > 0 {
			status.ExternalIP = svc.Status.LoadBalancer.Ingress[0].IP
		}
		for _, pod := range pods.Items {
			if matchesSelector(pod.Labels, svc.Spec.Selector) && isPodReady(pod) {
				status.HealthyPods++
			}
		}
		results = append(results, status)
	}
	return results, nil
}

func EnsureIngressAnnotations(ctx context.Context, client kubernetes.Interface, resourceGroup string, annotationMap map[string]string) (bool, error) {
	logger := logr.FromContextOrDiscard(ctx).WithName("ingress-annotations")

	svcs, err := client.CoreV1().Services(istioIngressNamespace).List(ctx, metav1.ListOptions{})
	if err != nil {
		return false, fmt.Errorf("failed to list ingress services: %w", err)
	}

	applied := false
	for _, svc := range svcs.Items {
		if svc.Spec.Type != corev1.ServiceTypeLoadBalancer {
			continue
		}
		pipName, ok := annotationMap[svc.Name]
		if !ok {
			continue
		}

		currentRG := svc.Annotations["service.beta.kubernetes.io/azure-load-balancer-resource-group"]
		currentPIP := svc.Annotations["service.beta.kubernetes.io/azure-pip-name"]
		if currentRG == resourceGroup && currentPIP == pipName {
			continue
		}

		logger.Info("Applying annotations", "service", svc.Name)
		patch := fmt.Appendf(nil,
			`{"metadata":{"annotations":{"service.beta.kubernetes.io/azure-load-balancer-resource-group":%q,"service.beta.kubernetes.io/azure-pip-name":%q}}}`,
			resourceGroup, pipName,
		)
		if _, err := client.CoreV1().Services(svc.Namespace).Patch(ctx, svc.Name, types.MergePatchType, patch, metav1.PatchOptions{}); err != nil {
			return applied, fmt.Errorf("failed to patch annotations on %s: %w", svc.Name, err)
		}
		applied = true
	}
	return applied, nil
}

type RestartResult struct {
	Namespace string
	Restarted []string
	Errors    []string
}

func migrateWorkloads(ctx context.Context, kubeClient kubernetes.Interface, opts UpgradeOptions, toRevision string) error {
	logger := logr.FromContextOrDiscard(ctx).WithName("migrate-workloads")

	if opts.Tag != "" {
		if err := EnsureRevisionTag(ctx, kubeClient, opts.Tag, toRevision); err != nil {
			return fmt.Errorf("failed to flip revision tag %s → %s: %w", opts.Tag, toRevision, err)
		}
	} else {
		if _, err := UpdateMeshNamespaceLabels(ctx, kubeClient, toRevision); err != nil {
			return fmt.Errorf("failed to update namespace labels to %s: %w", toRevision, err)
		}
	}

	restartResults, err := ExecuteRestartAllNamespaces(ctx, kubeClient, toRevision)
	if err != nil {
		return fmt.Errorf("workload restart failed: %w", err)
	}
	restarted := 0
	for _, r := range restartResults {
		restarted += len(r.Restarted)
	}
	if restarted > 0 {
		logger.Info("Restarted workloads with stale sidecars", "revision", toRevision, "count", restarted)
	}

	if err := WaitForRolloutAllNamespaces(ctx, kubeClient, opts.RolloutTimeout, opts.RolloutPollInterval); err != nil {
		return fmt.Errorf("rollout convergence failed: %w", err)
	}

	return nil
}

func ExecuteRestartAllNamespaces(ctx context.Context, client kubernetes.Interface, targetRevision string) ([]RestartResult, error) {
	namespaces, err := GetMeshNamespaces(ctx, client)
	if err != nil {
		return nil, err
	}

	type restartOutcome struct {
		result *RestartResult
		err    error
	}
	outcomes := make([]restartOutcome, len(namespaces))

	var wg sync.WaitGroup
	for i, ns := range namespaces {
		wg.Add(1)
		go func(idx int, namespace string) {
			defer wg.Done()
			result, err := executeRestart(ctx, client, namespace, targetRevision)
			outcomes[idx] = restartOutcome{result: result, err: err}
		}(i, ns.Name)
	}
	wg.Wait()

	var results []RestartResult
	var errs []error
	for _, o := range outcomes {
		if o.err != nil {
			errs = append(errs, o.err)
		}
		if o.result != nil {
			results = append(results, *o.result)
		}
	}
	return results, errors.Join(errs...)
}

func buildRestartPatch() []byte {
	return fmt.Appendf(nil,
		`{"spec":{"template":{"metadata":{"annotations":{"kubectl.kubernetes.io/restartedAt":"%s"}}}}}`,
		time.Now().Format(time.RFC3339),
	)
}

func controllingOwner(refs []metav1.OwnerReference) *metav1.OwnerReference {
	for i := range refs {
		if ptr.Deref(refs[i].Controller, false) {
			return &refs[i]
		}
	}
	return nil
}

type staleWorkloads struct {
	OrphanPods   []string
	Deployments  map[string]bool
	StatefulSets map[string]bool
}

func identifyStaleWorkloads(ctx context.Context, client kubernetes.Interface, namespace, targetRevision string) (*staleWorkloads, error) {
	podInfos, err := listRunningPodsWithSidecar(ctx, client, namespace)
	if err != nil {
		return nil, err
	}

	rsOwners, err := buildReplicaSetOwnerMap(ctx, client, namespace)
	if err != nil {
		return nil, err
	}

	result := &staleWorkloads{
		Deployments:  make(map[string]bool),
		StatefulSets: make(map[string]bool),
	}
	for _, pi := range podInfos {
		if pi.Revision == targetRevision {
			continue
		}
		controller := controllingOwner(pi.Pod.OwnerReferences)
		if controller == nil {
			result.OrphanPods = append(result.OrphanPods, pi.Pod.Name)
			continue
		}
		switch controller.Kind {
		case "ReplicaSet":
			if depName, ok := rsOwners[controller.Name]; ok {
				result.Deployments[depName] = true
			}
		case "StatefulSet":
			result.StatefulSets[controller.Name] = true
		}
	}
	return result, nil
}

func buildReplicaSetOwnerMap(ctx context.Context, client kubernetes.Interface, namespace string) (map[string]string, error) {
	rsList, err := client.AppsV1().ReplicaSets(namespace).List(ctx, metav1.ListOptions{})
	if err != nil {
		return nil, fmt.Errorf("failed to list replicasets in %s: %w", namespace, err)
	}
	owners := make(map[string]string, len(rsList.Items))
	for _, rs := range rsList.Items {
		if ctrl := controllingOwner(rs.OwnerReferences); ctrl != nil && ctrl.Kind == "Deployment" {
			owners[rs.Name] = ctrl.Name
		}
	}
	return owners, nil
}

func executeRestart(ctx context.Context, client kubernetes.Interface, namespace, targetRevision string) (*RestartResult, error) {
	logger := logr.FromContextOrDiscard(ctx).WithName("restart").WithValues("namespace", namespace)
	result := &RestartResult{Namespace: namespace}

	stale, err := identifyStaleWorkloads(ctx, client, namespace, targetRevision)
	if err != nil {
		return nil, err
	}

	for _, podName := range stale.OrphanPods {
		if err := client.CoreV1().Pods(namespace).Delete(ctx, podName, metav1.DeleteOptions{}); err != nil {
			result.Errors = append(result.Errors, fmt.Sprintf("pod/%s: %v", podName, err))
			continue
		}
		result.Restarted = append(result.Restarted, "pod/"+podName)
	}

	for name := range stale.Deployments {
		if _, err := client.AppsV1().Deployments(namespace).Patch(ctx, name,
			types.StrategicMergePatchType, buildRestartPatch(), metav1.PatchOptions{}); err != nil {
			result.Errors = append(result.Errors, fmt.Sprintf("deployment/%s: %v", name, err))
			continue
		}
		result.Restarted = append(result.Restarted, "deployment/"+name)
	}

	for name := range stale.StatefulSets {
		if _, err := client.AppsV1().StatefulSets(namespace).Patch(ctx, name,
			types.StrategicMergePatchType, buildRestartPatch(), metav1.PatchOptions{}); err != nil {
			result.Errors = append(result.Errors, fmt.Sprintf("statefulset/%s: %v", name, err))
			continue
		}
		result.Restarted = append(result.Restarted, "statefulset/"+name)
	}

	if len(result.Restarted) > 0 || len(result.Errors) > 0 {
		logger.Info("Restart complete", "restarted", len(result.Restarted), "errors", len(result.Errors))
	}
	if len(result.Errors) > 0 {
		var errs []error
		for _, e := range result.Errors {
			errs = append(errs, fmt.Errorf("%s", e))
		}
		return result, fmt.Errorf("restart errors in %s: %w", namespace, errors.Join(errs...))
	}
	return result, nil
}

type istioSidecarStatus struct {
	Revision string `json:"revision"`
}

func parseSidecarRevision(pod corev1.Pod) (string, bool) {
	raw, ok := pod.Annotations["sidecar.istio.io/status"]
	if ok {
		var status istioSidecarStatus
		if err := json.Unmarshal([]byte(raw), &status); err == nil && status.Revision != "" {
			return status.Revision, true
		}
	}
	return parseSidecarRevisionFromImage(pod)
}

func parseSidecarRevisionFromImage(pod corev1.Pod) (string, bool) {
	for _, c := range pod.Spec.Containers {
		if c.Name != "istio-proxy" {
			continue
		}
		lastColon := strings.LastIndex(c.Image, ":")
		if lastColon < 0 {
			return "", false
		}
		tag := c.Image[lastColon+1:]
		segments := strings.Split(tag, "-")
		if len(segments) >= 3 && segments[0] == "asm" {
			return strings.Join(segments[:3], "-"), true
		}
		return "", false
	}
	return "", false
}

type PodSidecarInfo struct {
	Pod      corev1.Pod
	Revision string
}

func listRunningPodsWithSidecar(ctx context.Context, client kubernetes.Interface, namespace string) ([]PodSidecarInfo, error) {
	pods, err := client.CoreV1().Pods(namespace).List(ctx, metav1.ListOptions{
		FieldSelector: "status.phase=Running",
	})
	if err != nil {
		return nil, fmt.Errorf("failed to list pods in %s: %w", namespace, err)
	}
	var result []PodSidecarInfo
	for _, pod := range pods.Items {
		if pod.DeletionTimestamp != nil {
			continue
		}
		rev, ok := parseSidecarRevision(pod)
		if !ok {
			continue
		}
		result = append(result, PodSidecarInfo{Pod: pod, Revision: rev})
	}
	return result, nil
}

func WaitForRolloutAllNamespaces(ctx context.Context, client kubernetes.Interface, timeout, pollInterval time.Duration) error {
	namespaces, err := GetMeshNamespaces(ctx, client)
	if err != nil {
		return fmt.Errorf("failed to get mesh namespaces: %w", err)
	}

	errs := make([]error, len(namespaces))
	var wg sync.WaitGroup
	for i, ns := range namespaces {
		wg.Add(1)
		go func(idx int, namespace string) {
			defer wg.Done()
			errs[idx] = WaitForRollout(ctx, client, namespace, timeout, pollInterval)
		}(i, ns.Name)
	}
	wg.Wait()

	return errors.Join(errs...)
}

func WaitForRollout(ctx context.Context, client kubernetes.Interface, namespace string, timeout, pollInterval time.Duration) error {
	logger := logr.FromContextOrDiscard(ctx).WithName("rollout-wait").WithValues("namespace", namespace)
	var lastPending string
	waited := false

	err := wait.PollUntilContextTimeout(ctx, pollInterval, timeout, true, func(ctx context.Context) (bool, error) {
		var pending []string

		deps, err := client.AppsV1().Deployments(namespace).List(ctx, metav1.ListOptions{})
		if err != nil {
			return false, fmt.Errorf("failed to list deployments: %w", err)
		}
		for _, d := range deps.Items {
			if d.Spec.Template.Annotations["sidecar.istio.io/inject"] == "false" {
				continue
			}
			desired := int32(1)
			if d.Spec.Replicas != nil {
				desired = *d.Spec.Replicas
			}
			if desired == 0 {
				continue
			}
			if d.Status.ObservedGeneration < d.Generation {
				pending = append(pending, fmt.Sprintf("deploy/%s(generation-lag)", d.Name))
				continue
			}
			if d.Status.UpdatedReplicas < desired || d.Status.ReadyReplicas < desired {
				pending = append(pending, fmt.Sprintf("deploy/%s(%d/%d)", d.Name, d.Status.ReadyReplicas, desired))
			}
		}

		sts, err := client.AppsV1().StatefulSets(namespace).List(ctx, metav1.ListOptions{})
		if err != nil {
			return false, fmt.Errorf("failed to list statefulsets: %w", err)
		}
		for _, s := range sts.Items {
			if s.Spec.Template.Annotations["sidecar.istio.io/inject"] == "false" {
				continue
			}
			desired := int32(1)
			if s.Spec.Replicas != nil {
				desired = *s.Spec.Replicas
			}
			if desired == 0 {
				continue
			}
			if s.Status.ObservedGeneration < s.Generation {
				pending = append(pending, fmt.Sprintf("sts/%s(generation-lag)", s.Name))
				continue
			}
			if s.Status.UpdatedReplicas < desired || s.Status.ReadyReplicas < desired {
				pending = append(pending, fmt.Sprintf("sts/%s(%d/%d)", s.Name, s.Status.ReadyReplicas, desired))
			}
		}

		if len(pending) == 0 {
			if waited {
				logger.Info("All workloads ready")
			}
			return true, nil
		}

		waited = true
		currentPending := strings.Join(pending, ", ")
		if currentPending != lastPending {
			logger.Info("Waiting for workloads", "pending", pending)
			lastPending = currentPending
		}
		return false, nil
	})
	if err != nil {
		return fmt.Errorf("timeout waiting for rollout in %s: %w", namespace, err)
	}
	return nil
}
