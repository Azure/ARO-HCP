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
	"time"

	"github.com/go-logr/logr"
	"golang.org/x/sync/errgroup"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/sets"
	"k8s.io/apimachinery/pkg/util/wait"
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

func UpdateMeshNamespaceLabels(ctx context.Context, kubeClient *KubeClient, newRevision string) (int, error) {
	logger := logr.FromContextOrDiscard(ctx).WithName("namespace-labels")
	namespaces, err := kubeClient.GetMeshNamespaces(ctx)
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
		if _, err := kubeClient.client.CoreV1().Namespaces().Patch(ctx, ns.Name, types.MergePatchType, patch, metav1.PatchOptions{}); err != nil {
			return updated, fmt.Errorf("failed to update label on namespace %s: %w", ns.Name, err)
		}
		logger.Info("Updated revision label", "namespace", ns.Name, "from", ns.RevisionLabel, "to", newRevision)
		updated++
	}
	kubeClient.InvalidateNamespaceCache()
	return updated, nil
}

type ControlPlaneStatus struct {
	Revision  string
	Ready     bool
	Replicas  int32
	Available int32
}

func GetControlPlaneStatus(ctx context.Context, kubeClient *KubeClient) ([]ControlPlaneStatus, error) {
	deps, err := kubeClient.client.AppsV1().Deployments(istioSystemNamespace).List(ctx, metav1.ListOptions{})
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

func GetIngressGatewayStatus(ctx context.Context, kubeClient *KubeClient) ([]IngressGatewayStatus, error) {
	svcs, err := kubeClient.client.CoreV1().Services(istioIngressNamespace).List(ctx, metav1.ListOptions{})
	if err != nil {
		return nil, fmt.Errorf("failed to list ingress services: %w", err)
	}
	pods, err := kubeClient.client.CoreV1().Pods(istioIngressNamespace).List(ctx, metav1.ListOptions{})
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

func EnsureIngressAnnotations(ctx context.Context, kubeClient *KubeClient, resourceGroup string, annotationMap map[string]string) (bool, error) {
	logger := logr.FromContextOrDiscard(ctx).WithName("ingress-annotations")

	svcs, err := kubeClient.client.CoreV1().Services(istioIngressNamespace).List(ctx, metav1.ListOptions{})
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
		// json.Marshal on a string value cannot fail
		rgJSON, _ := json.Marshal(resourceGroup)
		pipJSON, _ := json.Marshal(pipName)
		patch := fmt.Appendf(nil,
			`{"metadata":{"annotations":{"service.beta.kubernetes.io/azure-load-balancer-resource-group":%s,"service.beta.kubernetes.io/azure-pip-name":%s}}}`,
			rgJSON, pipJSON,
		)
		if _, err := kubeClient.client.CoreV1().Services(svc.Namespace).Patch(ctx, svc.Name, types.MergePatchType, patch, metav1.PatchOptions{}); err != nil {
			return applied, fmt.Errorf("failed to patch annotations on %s: %w", svc.Name, err)
		}
		applied = true
	}
	return applied, nil
}

type RestartResult struct {
	Namespace string
	Restarted []string
	Errors    []error
}

func migrateWorkloads(ctx context.Context, kubeClient *KubeClient, opts UpgradeOptions, toRevision string) error {
	logger := logr.FromContextOrDiscard(ctx).WithName("migrate-workloads")

	if opts.Tag != "" {
		if err := EnsureRevisionTag(ctx, kubeClient, opts.Tag, toRevision); err != nil {
			return fmt.Errorf("failed to flip revision tag %s -> %s: %w", opts.Tag, toRevision, err)
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

const namespaceConcurrencyLimit = 10

func ExecuteRestartAllNamespaces(ctx context.Context, kubeClient *KubeClient, targetRevision string) ([]RestartResult, error) {
	namespaces, err := kubeClient.GetMeshNamespaces(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to list mesh namespaces for restart: %w", err)
	}

	type restartOutcome struct {
		result *RestartResult
		err    error
	}
	outcomes := make([]restartOutcome, len(namespaces))

	g, gCtx := errgroup.WithContext(ctx)
	g.SetLimit(namespaceConcurrencyLimit)
	for i, ns := range namespaces {
		g.Go(func() error {
			result, err := executeRestart(gCtx, kubeClient, ns.Name, targetRevision)
			outcomes[i] = restartOutcome{result: result, err: err}
			// Always return nil so errgroup runs all namespaces; errors are collected and joined below.
			return nil
		})
	}
	_ = g.Wait() // always nil — goroutines collect errors in outcomes slice

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
	if len(errs) > 0 {
		logger := logr.FromContextOrDiscard(ctx).WithName("restart-summary")
		var succeeded, failed []string
		for _, o := range outcomes {
			if o.err != nil && o.result != nil {
				failed = append(failed, o.result.Namespace)
			} else if o.result != nil && len(o.result.Restarted) > 0 {
				succeeded = append(succeeded, o.result.Namespace)
			}
		}
		logger.Info("Restart completed with errors", "succeeded", succeeded, "failed", failed)
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
	Deployments  sets.Set[string]
	StatefulSets sets.Set[string]
	DaemonSets   sets.Set[string]
}

func identifyStaleWorkloads(ctx context.Context, kubeClient *KubeClient, namespace, targetRevision string) (*staleWorkloads, error) {
	podInfos, err := listRunningPodsWithSidecar(ctx, kubeClient, namespace)
	if err != nil {
		return nil, err
	}

	rsOwners, err := buildReplicaSetOwnerMap(ctx, kubeClient, namespace)
	if err != nil {
		return nil, err
	}

	result := &staleWorkloads{
		Deployments:  sets.New[string](),
		StatefulSets: sets.New[string](),
		DaemonSets:   sets.New[string](),
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
				result.Deployments.Insert(depName)
			} else {
				result.OrphanPods = append(result.OrphanPods, pi.Pod.Name)
			}
		case "StatefulSet":
			result.StatefulSets.Insert(controller.Name)
		case "DaemonSet":
			result.DaemonSets.Insert(controller.Name)
		default:
			result.OrphanPods = append(result.OrphanPods, pi.Pod.Name)
		}
	}
	return result, nil
}

func buildReplicaSetOwnerMap(ctx context.Context, kubeClient *KubeClient, namespace string) (map[string]string, error) {
	rsList, err := kubeClient.client.AppsV1().ReplicaSets(namespace).List(ctx, metav1.ListOptions{})
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

func executeRestart(ctx context.Context, kubeClient *KubeClient, namespace, targetRevision string) (*RestartResult, error) {
	logger := logr.FromContextOrDiscard(ctx).WithName("restart").WithValues("namespace", namespace)
	result := &RestartResult{Namespace: namespace}

	stale, err := identifyStaleWorkloads(ctx, kubeClient, namespace, targetRevision)
	if err != nil {
		return nil, err
	}

	for _, podName := range stale.OrphanPods {
		if err := kubeClient.client.CoreV1().Pods(namespace).Delete(ctx, podName, metav1.DeleteOptions{}); err != nil {
			result.Errors = append(result.Errors, fmt.Errorf("pod/%s: %w", podName, err))
			continue
		}
		result.Restarted = append(result.Restarted, "pod/"+podName)
	}

	for name := range stale.Deployments {
		if _, err := kubeClient.client.AppsV1().Deployments(namespace).Patch(ctx, name,
			types.StrategicMergePatchType, buildRestartPatch(), metav1.PatchOptions{}); err != nil {
			result.Errors = append(result.Errors, fmt.Errorf("deployment/%s: %w", name, err))
			continue
		}
		result.Restarted = append(result.Restarted, "deployment/"+name)
	}

	for name := range stale.StatefulSets {
		if _, err := kubeClient.client.AppsV1().StatefulSets(namespace).Patch(ctx, name,
			types.StrategicMergePatchType, buildRestartPatch(), metav1.PatchOptions{}); err != nil {
			result.Errors = append(result.Errors, fmt.Errorf("statefulset/%s: %w", name, err))
			continue
		}
		result.Restarted = append(result.Restarted, "statefulset/"+name)
	}

	for name := range stale.DaemonSets {
		if _, err := kubeClient.client.AppsV1().DaemonSets(namespace).Patch(ctx, name,
			types.StrategicMergePatchType, buildRestartPatch(), metav1.PatchOptions{}); err != nil {
			result.Errors = append(result.Errors, fmt.Errorf("daemonset/%s: %w", name, err))
			continue
		}
		result.Restarted = append(result.Restarted, "daemonset/"+name)
	}

	if len(result.Restarted) > 0 || len(result.Errors) > 0 {
		logger.Info("Restart complete", "restarted", len(result.Restarted), "errors", len(result.Errors))
	}
	if len(result.Errors) > 0 {
		return result, fmt.Errorf("restart errors in %s: %w", namespace, errors.Join(result.Errors...))
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

func listRunningPodsWithSidecar(ctx context.Context, kubeClient *KubeClient, namespace string) ([]PodSidecarInfo, error) {
	pods, err := kubeClient.client.CoreV1().Pods(namespace).List(ctx, metav1.ListOptions{
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

func WaitForRolloutAllNamespaces(ctx context.Context, kubeClient *KubeClient, timeout, pollInterval time.Duration) error {
	namespaces, err := kubeClient.GetMeshNamespaces(ctx)
	if err != nil {
		return fmt.Errorf("failed to get mesh namespaces: %w", err)
	}

	errs := make([]error, len(namespaces))
	g, ctx := errgroup.WithContext(ctx)
	g.SetLimit(namespaceConcurrencyLimit)
	for i, ns := range namespaces {
		g.Go(func() error {
			errs[i] = WaitForRollout(ctx, kubeClient, ns.Name, timeout, pollInterval)
			// Always return nil so errgroup runs all namespaces; errors are collected and joined below.
			return nil
		})
	}
	_ = g.Wait() // always nil — goroutines collect errors in errs slice

	return errors.Join(errs...)
}

func checkWorkloadPending(kind, name string, desired, updated, ready int32, generation, observedGeneration int64) string {
	if desired == 0 {
		return ""
	}
	if observedGeneration < generation {
		return fmt.Sprintf("%s/%s(generation-lag)", kind, name)
	}
	if updated < desired || ready < desired {
		return fmt.Sprintf("%s/%s(%d/%d)", kind, name, ready, desired)
	}
	return ""
}

func WaitForRollout(ctx context.Context, kubeClient *KubeClient, namespace string, timeout, pollInterval time.Duration) error {
	logger := logr.FromContextOrDiscard(ctx).WithName("rollout-wait").WithValues("namespace", namespace)
	var lastPending string
	waited := false

	err := wait.PollUntilContextTimeout(ctx, pollInterval, timeout, true, func(ctx context.Context) (bool, error) {
		var pending []string

		deps, err := kubeClient.client.AppsV1().Deployments(namespace).List(ctx, metav1.ListOptions{})
		if err != nil {
			return false, fmt.Errorf("failed to list deployments: %w", err)
		}
		for _, d := range deps.Items {
			if d.Spec.Template.Annotations["sidecar.istio.io/inject"] == "false" {
				continue
			}
			desired := ptr.Deref(d.Spec.Replicas, 1)
			if s := checkWorkloadPending("deploy", d.Name, desired, d.Status.UpdatedReplicas, d.Status.ReadyReplicas, d.Generation, d.Status.ObservedGeneration); s != "" {
				pending = append(pending, s)
			}
		}

		sts, err := kubeClient.client.AppsV1().StatefulSets(namespace).List(ctx, metav1.ListOptions{})
		if err != nil {
			return false, fmt.Errorf("failed to list statefulsets: %w", err)
		}
		for _, s := range sts.Items {
			if s.Spec.Template.Annotations["sidecar.istio.io/inject"] == "false" {
				continue
			}
			desired := ptr.Deref(s.Spec.Replicas, 1)
			if p := checkWorkloadPending("sts", s.Name, desired, s.Status.UpdatedReplicas, s.Status.ReadyReplicas, s.Generation, s.Status.ObservedGeneration); p != "" {
				pending = append(pending, p)
			}
		}

		dss, err := kubeClient.client.AppsV1().DaemonSets(namespace).List(ctx, metav1.ListOptions{})
		if err != nil {
			return false, fmt.Errorf("failed to list daemonsets: %w", err)
		}
		for _, ds := range dss.Items {
			if ds.Spec.Template.Annotations["sidecar.istio.io/inject"] == "false" {
				continue
			}
			if p := checkWorkloadPending("ds", ds.Name, ds.Status.DesiredNumberScheduled, ds.Status.UpdatedNumberScheduled, ds.Status.NumberReady, ds.Generation, ds.Status.ObservedGeneration); p != "" {
				pending = append(pending, p)
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
		return fmt.Errorf("rollout did not converge in %s: %w", namespace, err)
	}
	return nil
}

func matchesSelector(labels, selector map[string]string) bool {
	if len(selector) == 0 {
		return false
	}
	for k, v := range selector {
		if labels[k] != v {
			return false
		}
	}
	return true
}

func isPodReady(pod corev1.Pod) bool {
	for _, c := range pod.Status.Conditions {
		if c.Type == corev1.PodReady && c.Status == corev1.ConditionTrue {
			return true
		}
	}
	return false
}
