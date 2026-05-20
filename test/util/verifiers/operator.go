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

package verifiers

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/util/sets"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
)

var (
	subscriptionGVR = schema.GroupVersionResource{
		Group:    "operators.coreos.com",
		Version:  "v1alpha1",
		Resource: "subscriptions",
	}
	catalogSourceGVR = schema.GroupVersionResource{
		Group:    "operators.coreos.com",
		Version:  "v1alpha1",
		Resource: "catalogsources",
	}
)

var _ DiagnosticVerifier = verifyOperatorInstalled{}

// imagePullErrorReasons lists all container waiting reasons that indicate an
// image pull problem.  Values come from k8s.io/kubernetes/pkg/kubelet/images/types.go.
var imagePullErrorReasons = sets.New(
	"ImagePullBackOff",
	"ErrImagePull",
	"ImageInspectError",
	"ErrImageNeverPull",
	"InvalidImageName",
)

// checkPodImagePullErrors checks if a pod has image pull errors in any container
func checkPodImagePullErrors(pod corev1.Pod, contextName string) error {
	for _, cs := range append(pod.Status.ContainerStatuses, pod.Status.InitContainerStatuses...) {
		if cs.State.Waiting != nil && imagePullErrorReasons.Has(cs.State.Waiting.Reason) {
			return fmt.Errorf("%s %s, container %s: %s (%s)",
				contextName, pod.Name, cs.Name, cs.State.Waiting.Reason, cs.State.Waiting.Message)
		}
	}
	return nil
}

// checkResourceConditions checks unstructured resource conditions for errors
func checkResourceConditions(obj map[string]any, resourceType, namespace, name string) error {
	conditions, found, err := unstructured.NestedSlice(obj, "status", "conditions")
	if err != nil {
		return fmt.Errorf("failed to read conditions from %s %s/%s: %w", resourceType, namespace, name, err)
	}
	if !found {
		return nil
	}

	for _, cond := range conditions {
		condMap, ok := cond.(map[string]any)
		if !ok {
			continue
		}
		condType, _ := condMap["type"].(string)
		status, _ := condMap["status"].(string)
		reason, _ := condMap["reason"].(string)
		message, _ := condMap["message"].(string)

		// Look for error conditions
		if strings.ToLower(status) == "false" &&
			(strings.Contains(strings.ToLower(reason), "error") ||
				strings.Contains(strings.ToLower(reason), "fail")) {
			return fmt.Errorf("%s %s/%s has error condition: type=%s, reason=%s, message=%s",
				resourceType, namespace, name, condType, reason, message)
		}
	}

	return nil
}

// catalogSourceHealthCheck contains shared logic for checking catalog source health.
// It takes an already-fetched CatalogSource object to avoid redundant API calls.
func catalogSourceHealthCheck(ctx context.Context, cs *unstructured.Unstructured, namespace, catalogSource string,
	kubeClient kubernetes.Interface, requirePod bool) error {

	// Check for error conditions in CatalogSource
	if err := checkResourceConditions(cs.Object, "catalog source", namespace, catalogSource); err != nil {
		return err
	}

	// Check catalog source pod health
	pods, err := kubeClient.CoreV1().Pods(namespace).List(ctx, metav1.ListOptions{})
	if err != nil {
		if requirePod {
			return fmt.Errorf("failed to list pods in namespace %s: %w", namespace, err)
		}
		return nil // Don't fail if pods can't be listed (when not required)
	}

	podFound := false
	for _, pod := range pods.Items {
		if strings.Contains(pod.Name, catalogSource) {
			podFound = true
			if requirePod && pod.Status.Phase != corev1.PodRunning {
				return fmt.Errorf("catalog source pod %s is in phase %s, expected Running", pod.Name, pod.Status.Phase)
			}

			// Check for image pull errors
			if err := checkPodImagePullErrors(pod, "catalog source pod"); err != nil {
				return fmt.Errorf("check pull secret configuration: %w", err)
			}
		}
	}

	if requirePod && !podFound {
		return fmt.Errorf("no pod found for catalog source %s", catalogSource)
	}

	return nil
}

type verifyOperatorInstalled struct {
	namespace        string
	subscriptionName string
}

func (v verifyOperatorInstalled) Name() string {
	return "VerifyOperatorInstalled"
}

func (v verifyOperatorInstalled) Verify(ctx context.Context, adminRESTConfig *rest.Config) error {
	dynamicClient, err := dynamic.NewForConfig(adminRESTConfig)
	if err != nil {
		return fmt.Errorf("failed to create dynamic client: %w", err)
	}

	kubeClient, err := kubernetes.NewForConfig(adminRESTConfig)
	if err != nil {
		return fmt.Errorf("failed to create kubernetes client: %w", err)
	}

	subscription, err := dynamicClient.Resource(subscriptionGVR).Namespace(v.namespace).Get(ctx, v.subscriptionName, metav1.GetOptions{})
	if err != nil {
		return fmt.Errorf("failed to get subscription %s/%s: %w", v.namespace, v.subscriptionName, err)
	}

	// Check subscription state
	state, found, err := unstructured.NestedString(subscription.Object, "status", "state")
	if err != nil {
		return fmt.Errorf("failed to get subscription state: %w", err)
	}

	// When state is not ready, enumerate all error conditions, sort them
	// deterministically, and report the full set.  Deterministic sorting means
	// repeated polls that observe the same errors produce an identical error
	// string, letting the caller's delta-tracking skip duplicate log lines.
	if !found || state != "AtLatestKnown" {
		var allErrors []string

		// Collect ALL pod error conditions (not just image-pull)
		allErrors = append(allErrors, collectPodErrors(ctx, kubeClient, v.namespace)...)

		// Collect catalog source errors
		allErrors = append(allErrors, v.collectCatalogSourceErrors(ctx, subscription, kubeClient, dynamicClient)...)

		// Collect subscription condition errors
		allErrors = append(allErrors, collectResourceConditionErrors(subscription.Object, "subscription", v.namespace, v.subscriptionName)...)

		sort.Strings(allErrors)

		if len(allErrors) > 0 {
			return fmt.Errorf("subscription %s/%s not ready (state=%q):\n  - %s",
				v.namespace, v.subscriptionName, state,
				strings.Join(allErrors, "\n  - "))
		}

		return fmt.Errorf("subscription %s/%s not ready, state=%q (OLM still processing, no errors detected)",
			v.namespace, v.subscriptionName, state)
	}

	// Get InstallPlan reference
	installPlanRef, found, err := unstructured.NestedString(subscription.Object, "status", "installplan", "name")
	if err != nil {
		return fmt.Errorf("failed to get installplan reference: %w", err)
	}
	if !found {
		return fmt.Errorf("installplan reference not found in subscription")
	}

	// Check InstallPlan status
	installPlanGVR := schema.GroupVersionResource{
		Group:    "operators.coreos.com",
		Version:  "v1alpha1",
		Resource: "installplans",
	}

	installPlan, err := dynamicClient.Resource(installPlanGVR).Namespace(v.namespace).Get(ctx, installPlanRef, metav1.GetOptions{})
	if err != nil {
		return fmt.Errorf("failed to get installplan %s/%s: %w", v.namespace, installPlanRef, err)
	}

	phase, found, err := unstructured.NestedString(installPlan.Object, "status", "phase")
	if err != nil {
		return fmt.Errorf("failed to get installplan phase: %w", err)
	}
	if !found {
		return fmt.Errorf("installplan phase not found")
	}
	if phase != "Complete" {
		// Include InstallPlan conditions for better diagnostics
		conditions, _, _ := unstructured.NestedSlice(installPlan.Object, "status", "conditions")
		condStr := formatInstallPlanConditions(conditions)
		return fmt.Errorf("installplan phase is %q, expected Complete\n%s", phase, condStr)
	}

	return nil
}

// normalWaitingReasons lists container waiting reasons that are expected during
// normal startup and should not be reported as errors.
var normalWaitingReasons = sets.New(
	"ContainerCreating",
	"PodInitializing",
)

// collectPodErrors enumerates ALL error conditions from pods in a namespace.
// This includes image-pull errors, crash loops, OOM kills, container config
// errors, and any other non-normal waiting/terminated state.  The returned
// list is suitable for deterministic sorting by the caller.
func collectPodErrors(ctx context.Context, kubeClient kubernetes.Interface, namespace string) []string {
	pods, err := kubeClient.CoreV1().Pods(namespace).List(ctx, metav1.ListOptions{})
	if err != nil {
		return nil // best-effort: if pods can't be listed, return nothing
	}

	var errs []string
	for _, pod := range pods.Items {
		for _, cs := range append(pod.Status.ContainerStatuses, pod.Status.InitContainerStatuses...) {
			if cs.State.Waiting != nil && cs.State.Waiting.Reason != "" && !normalWaitingReasons.Has(cs.State.Waiting.Reason) {
				errs = append(errs, fmt.Sprintf("pod %s container %s: %s (%s)",
					pod.Name, cs.Name, cs.State.Waiting.Reason, cs.State.Waiting.Message))
			}
			if cs.State.Terminated != nil && cs.State.Terminated.ExitCode != 0 {
				errs = append(errs, fmt.Sprintf("pod %s container %s: terminated with exit code %d, reason=%s (%s)",
					pod.Name, cs.Name, cs.State.Terminated.ExitCode, cs.State.Terminated.Reason, cs.State.Terminated.Message))
			}
		}
	}
	return errs
}

// collectResourceConditionErrors returns all error conditions from an
// unstructured resource's status.conditions.  Unlike checkResourceConditions
// (which returns the first error), this collects every matching condition.
func collectResourceConditionErrors(obj map[string]any, resourceType, namespace, name string) []string {
	conditions, found, err := unstructured.NestedSlice(obj, "status", "conditions")
	if err != nil || !found {
		return nil
	}

	var errs []string
	for _, cond := range conditions {
		condMap, ok := cond.(map[string]any)
		if !ok {
			continue
		}
		condType, _ := condMap["type"].(string)
		status, _ := condMap["status"].(string)
		reason, _ := condMap["reason"].(string)
		message, _ := condMap["message"].(string)

		if strings.ToLower(status) == "false" &&
			(strings.Contains(strings.ToLower(reason), "error") ||
				strings.Contains(strings.ToLower(reason), "fail")) {
			errs = append(errs, fmt.Sprintf("%s %s/%s condition %s: reason=%s, message=%s",
				resourceType, namespace, name, condType, reason, message))
		}
	}
	return errs
}

// collectCatalogSourceErrors gathers error information from the catalog source
// referenced by the subscription.
func (v verifyOperatorInstalled) collectCatalogSourceErrors(ctx context.Context, subscription *unstructured.Unstructured,
	kubeClient kubernetes.Interface, dynamicClient dynamic.Interface) []string {

	catalogSource, _, err := unstructured.NestedString(subscription.Object, "spec", "source")
	if err != nil {
		return []string{fmt.Sprintf("failed to read spec.source from subscription: %s", err)}
	}
	catalogSourceNS, _, err := unstructured.NestedString(subscription.Object, "spec", "sourceNamespace")
	if err != nil {
		return []string{fmt.Sprintf("failed to read spec.sourceNamespace from subscription: %s", err)}
	}

	if catalogSource == "" || catalogSourceNS == "" {
		return nil
	}

	cs, err := dynamicClient.Resource(catalogSourceGVR).Namespace(catalogSourceNS).Get(ctx, catalogSource, metav1.GetOptions{})
	if err != nil {
		return []string{fmt.Sprintf("failed to get catalog source %s/%s: %v", catalogSourceNS, catalogSource, err)}
	}

	var errs []string
	errs = append(errs, collectResourceConditionErrors(cs.Object, "catalog source", catalogSourceNS, catalogSource)...)

	// Check catalog source pods for errors
	pods, listErr := kubeClient.CoreV1().Pods(catalogSourceNS).List(ctx, metav1.ListOptions{})
	if listErr == nil {
		for _, pod := range pods.Items {
			if strings.Contains(pod.Name, catalogSource) {
				if pullErr := checkPodImagePullErrors(pod, "catalog source pod"); pullErr != nil {
					errs = append(errs, pullErr.Error())
				}
			}
		}
	}

	return errs
}

// formatInstallPlanConditions formats InstallPlan conditions for error messages
func formatInstallPlanConditions(conditions []any) string {
	if len(conditions) == 0 {
		return "No InstallPlan conditions available"
	}

	var formatted []string
	for _, cond := range conditions {
		if condMap, ok := cond.(map[string]any); ok {
			condType, _ := condMap["type"].(string)
			status, _ := condMap["status"].(string)
			reason, _ := condMap["reason"].(string)
			message, _ := condMap["message"].(string)
			formatted = append(formatted, fmt.Sprintf("  - Type=%s, Status=%s, Reason=%s, Message=%s",
				condType, status, reason, message))
		}
	}
	return strings.Join(formatted, "\n")
}

func (v verifyOperatorInstalled) DiagnoseFailure(ctx context.Context, restConfig *rest.Config) string {
	var sections []string

	dynamicClient, err := dynamic.NewForConfig(restConfig)
	if err != nil {
		return fmt.Sprintf("failed to create dynamic client for diagnostics: %v", err)
	}

	kubeClient, err := kubernetes.NewForConfig(restConfig)
	if err != nil {
		return fmt.Sprintf("failed to create kubernetes client for diagnostics: %v", err)
	}

	sub, err := dynamicClient.Resource(subscriptionGVR).Namespace(v.namespace).Get(ctx, v.subscriptionName, metav1.GetOptions{})
	if err != nil {
		sections = append(sections, fmt.Sprintf("[subscription] failed to get: %v", err))
	} else {
		sections = append(sections, formatSubscriptionStatus(sub))
	}

	// Catalog source health re-check
	if sub != nil {
		sections = append(sections, v.diagnoseCatalogSource(ctx, sub, dynamicClient)...)
	}

	// OLM pod health
	sections = append(sections, diagnoseOLMPods(ctx, kubeClient)...)

	// Recent events in subscription namespace and marketplace
	for _, ns := range []string{v.namespace, "openshift-marketplace"} {
		sections = append(sections, diagnoseEvents(ctx, kubeClient, ns)...)
	}

	return strings.Join(sections, "\n")
}

func formatSubscriptionStatus(sub *unstructured.Unstructured) string {
	status, _, _ := unstructured.NestedMap(sub.Object, "status")
	if status == nil {
		return "[subscription] status: <nil>"
	}

	state, _ := status["state"].(string)
	currentCSV, _ := status["currentCSV"].(string)
	installedCSV, _ := status["installedCSV"].(string)

	var lines []string
	lines = append(lines, fmt.Sprintf("[subscription] state=%q currentCSV=%q installedCSV=%q", state, currentCSV, installedCSV))

	if planRef, ok, _ := unstructured.NestedString(sub.Object, "status", "installplan", "name"); ok {
		lines = append(lines, fmt.Sprintf("[subscription] installPlanRef=%q", planRef))
	}

	conditions, found, _ := unstructured.NestedSlice(sub.Object, "status", "conditions")
	if found {
		for _, cond := range conditions {
			condMap, ok := cond.(map[string]any)
			if !ok {
				continue
			}
			lines = append(lines, fmt.Sprintf("[subscription] condition type=%s status=%s reason=%s message=%s lastTransitionTime=%s",
				condMap["type"], condMap["status"], condMap["reason"], condMap["message"], condMap["lastTransitionTime"]))
		}
	}

	catalogHealth, found, _ := unstructured.NestedSlice(sub.Object, "status", "catalogHealth")
	if found {
		for _, ch := range catalogHealth {
			chMap, ok := ch.(map[string]any)
			if !ok {
				continue
			}
			ref, _ := chMap["catalogSourceRef"].(map[string]any)
			if ref == nil {
				continue
			}
			healthy, _ := chMap["healthy"].(bool)
			lastUpdated, _ := chMap["lastUpdated"].(string)
			lines = append(lines, fmt.Sprintf("[subscription] catalogHealth source=%s/%s healthy=%t lastUpdated=%s",
				ref["namespace"], ref["name"], healthy, lastUpdated))
		}
	}

	return strings.Join(lines, "\n")
}

func (v verifyOperatorInstalled) diagnoseCatalogSource(ctx context.Context, sub *unstructured.Unstructured,
	dynamicClient dynamic.Interface) []string {

	catalogSource, _, _ := unstructured.NestedString(sub.Object, "spec", "source")
	catalogSourceNS, _, _ := unstructured.NestedString(sub.Object, "spec", "sourceNamespace")
	if catalogSource == "" || catalogSourceNS == "" {
		return nil
	}

	cs, err := dynamicClient.Resource(catalogSourceGVR).Namespace(catalogSourceNS).Get(ctx, catalogSource, metav1.GetOptions{})
	if err != nil {
		return []string{fmt.Sprintf("[catalogsource] failed to get %s/%s: %v", catalogSourceNS, catalogSource, err)}
	}

	state, _, _ := unstructured.NestedString(cs.Object, "status", "connectionState", "lastObservedState")
	lastConnect, _, _ := unstructured.NestedString(cs.Object, "status", "connectionState", "lastConnectTime")
	return []string{fmt.Sprintf("[catalogsource] %s/%s connectionState=%q lastConnectTime=%s", catalogSourceNS, catalogSource, state, lastConnect)}
}

func diagnoseOLMPods(ctx context.Context, kubeClient kubernetes.Interface) []string {
	pods, err := kubeClient.CoreV1().Pods("openshift-operator-lifecycle-manager").List(ctx, metav1.ListOptions{})
	if err != nil {
		return []string{fmt.Sprintf("[olm-pods] failed to list: %v", err)}
	}

	var lines []string
	for _, pod := range pods.Items {
		var restarts int32
		allStatuses := append(pod.Status.ContainerStatuses, pod.Status.InitContainerStatuses...)
		for _, cs := range allStatuses {
			restarts += cs.RestartCount
		}
		lines = append(lines, fmt.Sprintf("[olm-pods] %s phase=%s restarts=%d", pod.Name, pod.Status.Phase, restarts))
		for _, cs := range allStatuses {
			if cs.State.Waiting != nil {
				lines = append(lines, fmt.Sprintf("[olm-pods]   container %s: waiting reason=%s message=%s",
					cs.Name, cs.State.Waiting.Reason, cs.State.Waiting.Message))
			}
			if cs.State.Terminated != nil {
				lines = append(lines, fmt.Sprintf("[olm-pods]   container %s: terminated reason=%s exitCode=%d",
					cs.Name, cs.State.Terminated.Reason, cs.State.Terminated.ExitCode))
			}
		}
	}

	if len(lines) == 0 {
		lines = append(lines, "[olm-pods] no pods found in openshift-operator-lifecycle-manager")
	}

	return lines
}

func diagnoseEvents(ctx context.Context, kubeClient kubernetes.Interface, namespace string) []string {
	events, err := kubeClient.CoreV1().Events(namespace).List(ctx, metav1.ListOptions{})
	if err != nil {
		return []string{fmt.Sprintf("[events] failed to list events in %s: %v", namespace, err)}
	}

	cutoff := time.Now().Add(-15 * time.Minute)
	var lines []string
	for _, event := range events.Items {
		eventTime := event.LastTimestamp.Time
		if eventTime.IsZero() {
			eventTime = event.CreationTimestamp.Time
		}
		if eventTime.Before(cutoff) {
			continue
		}
		lines = append(lines, fmt.Sprintf("[events] %s %s/%s: %s (reason=%s, count=%d, age=%s)",
			namespace, event.InvolvedObject.Kind, event.InvolvedObject.Name,
			event.Message, event.Reason, event.Count,
			time.Since(eventTime).Truncate(time.Second)))
	}

	if len(lines) == 0 {
		lines = append(lines, fmt.Sprintf("[events] no recent events in %s", namespace))
	}

	return lines
}

func VerifyOperatorInstalled(namespace, subscriptionName string) HostedClusterVerifier {
	return verifyOperatorInstalled{
		namespace:        namespace,
		subscriptionName: subscriptionName,
	}
}

type verifyOperatorCSV struct {
	namespace string
	csvName   string
}

func (v verifyOperatorCSV) Name() string {
	return "VerifyOperatorCSV"
}

func (v verifyOperatorCSV) Verify(ctx context.Context, adminRESTConfig *rest.Config) error {
	dynamicClient, err := dynamic.NewForConfig(adminRESTConfig)
	if err != nil {
		return fmt.Errorf("failed to create dynamic client: %w", err)
	}

	csvGVR := schema.GroupVersionResource{
		Group:    "operators.coreos.com",
		Version:  "v1alpha1",
		Resource: "clusterserviceversions",
	}

	csv, err := dynamicClient.Resource(csvGVR).Namespace(v.namespace).Get(ctx, v.csvName, metav1.GetOptions{})
	if err != nil {
		return fmt.Errorf("failed to get CSV %s/%s: %w", v.namespace, v.csvName, err)
	}

	phase, found, err := unstructured.NestedString(csv.Object, "status", "phase")
	if err != nil {
		return fmt.Errorf("failed to get CSV phase: %w", err)
	}
	if !found {
		return fmt.Errorf("CSV phase not found")
	}
	if phase != "Succeeded" {
		return fmt.Errorf("CSV phase is %q, expected Succeeded", phase)
	}

	return nil
}

func VerifyOperatorCSV(namespace, csvName string) HostedClusterVerifier {
	return verifyOperatorCSV{
		namespace: namespace,
		csvName:   csvName,
	}
}

type verifyCatalogSourceReady struct {
	namespace     string
	catalogSource string
}

func (v verifyCatalogSourceReady) Name() string {
	return "VerifyCatalogSourceReady"
}

func (v verifyCatalogSourceReady) Verify(ctx context.Context, adminRESTConfig *rest.Config) error {
	dynamicClient, err := dynamic.NewForConfig(adminRESTConfig)
	if err != nil {
		return fmt.Errorf("failed to create dynamic client: %w", err)
	}

	kubeClient, err := kubernetes.NewForConfig(adminRESTConfig)
	if err != nil {
		return fmt.Errorf("failed to create kubernetes client: %w", err)
	}

	cs, err := dynamicClient.Resource(catalogSourceGVR).Namespace(v.namespace).Get(ctx, v.catalogSource, metav1.GetOptions{})
	if err != nil {
		return fmt.Errorf("failed to get catalog source %s/%s: %w", v.namespace, v.catalogSource, err)
	}

	state, found, err := unstructured.NestedString(cs.Object, "status", "connectionState", "lastObservedState")
	if err != nil {
		return fmt.Errorf("failed to read connection state from catalog source %s/%s: %w", v.namespace, v.catalogSource, err)
	}
	if found && state != "READY" {
		return fmt.Errorf("catalog source connection state is %q, expected READY", state)
	}

	// Use shared catalog source health check (requirePod=false because HCP clusters
	// may serve catalog sources via external gRPC endpoints without a local pod)
	return catalogSourceHealthCheck(ctx, cs, v.namespace, v.catalogSource, kubeClient, false)
}

// VerifyCatalogSourceReady verifies that a catalog source is healthy and ready to serve operators
func VerifyCatalogSourceReady(namespace, catalogSource string) HostedClusterVerifier {
	return verifyCatalogSourceReady{
		namespace:     namespace,
		catalogSource: catalogSource,
	}
}
