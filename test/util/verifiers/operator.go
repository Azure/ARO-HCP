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
	"strings"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
)

// imagePullErrorReasons lists all container waiting reasons that indicate an
// image pull problem.  Values come from k8s.io/kubernetes/pkg/kubelet/images/types.go.
var imagePullErrorReasons = map[string]bool{
	"ImagePullBackOff":  true,
	"ErrImagePull":      true,
	"ImageInspectError": true,
	"ErrImageNeverPull": true,
	"InvalidImageName":  true,
}

// checkPodImagePullErrors checks if a pod has image pull errors in any container
func checkPodImagePullErrors(pod corev1.Pod, contextName string) error {
	for _, cs := range append(pod.Status.ContainerStatuses, pod.Status.InitContainerStatuses...) {
		if cs.State.Waiting != nil && imagePullErrorReasons[cs.State.Waiting.Reason] {
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

	// Check if Subscription exists
	subscriptionGVR := schema.GroupVersionResource{
		Group:    "operators.coreos.com",
		Version:  "v1alpha1",
		Resource: "subscriptions",
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

	// When state is not ready, check for actual errors before returning a generic message
	if !found || state != "AtLatestKnown" {
		// Check for image pull errors in operator namespace
		if imagePullErr := v.checkImagePullErrors(ctx, kubeClient); imagePullErr != nil {
			return imagePullErr
		}

		// Check catalog source health
		if catalogErr := v.checkCatalogSourceHealth(ctx, subscription, kubeClient, dynamicClient); catalogErr != nil {
			return catalogErr
		}

		// Check subscription conditions for specific errors
		if condErr := v.checkSubscriptionConditions(subscription); condErr != nil {
			return condErr
		}

		// No actual errors detected - OLM is likely still processing
		return fmt.Errorf("subscription not ready (OLM still processing)")
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

// checkImagePullErrors checks for image pull failures in the operator namespace.
// This is a best-effort diagnostic: if pods cannot be listed, the error is
// intentionally suppressed so that the caller falls through to other checks.
func (v verifyOperatorInstalled) checkImagePullErrors(ctx context.Context, kubeClient kubernetes.Interface) error {
	pods, err := kubeClient.CoreV1().Pods(v.namespace).List(ctx, metav1.ListOptions{})
	if err != nil {
		return nil
	}

	var pullErrors []string
	for _, pod := range pods.Items {
		if err := checkPodImagePullErrors(pod, "operator pod"); err != nil {
			pullErrors = append(pullErrors, fmt.Sprintf("  - %s", err.Error()))
		}
	}

	if len(pullErrors) > 0 {
		return fmt.Errorf("operator installation blocked by image pull errors (check pull secret configuration):\n%s",
			strings.Join(pullErrors, "\n"))
	}
	return nil
}

// checkCatalogSourceHealth verifies the catalog source is healthy
func (v verifyOperatorInstalled) checkCatalogSourceHealth(ctx context.Context, subscription *unstructured.Unstructured,
	kubeClient kubernetes.Interface, dynamicClient dynamic.Interface) error {

	catalogSource, _, err := unstructured.NestedString(subscription.Object, "spec", "source")
	if err != nil {
		return fmt.Errorf("failed to read spec.source from subscription: %w", err)
	}
	catalogSourceNS, _, err := unstructured.NestedString(subscription.Object, "spec", "sourceNamespace")
	if err != nil {
		return fmt.Errorf("failed to read spec.sourceNamespace from subscription: %w", err)
	}

	if catalogSource == "" || catalogSourceNS == "" {
		return nil
	}

	catalogSourceGVR := schema.GroupVersionResource{
		Group:    "operators.coreos.com",
		Version:  "v1alpha1",
		Resource: "catalogsources",
	}

	cs, err := dynamicClient.Resource(catalogSourceGVR).Namespace(catalogSourceNS).Get(ctx, catalogSource, metav1.GetOptions{})
	if err != nil {
		return fmt.Errorf("failed to get catalog source %s/%s: %w", catalogSourceNS, catalogSource, err)
	}

	return catalogSourceHealthCheck(ctx, cs, catalogSourceNS, catalogSource, kubeClient, false)
}

// checkSubscriptionConditions examines subscription conditions for errors
func (v verifyOperatorInstalled) checkSubscriptionConditions(subscription *unstructured.Unstructured) error {
	return checkResourceConditions(subscription.Object, "subscription", v.namespace, v.subscriptionName)
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

	// Check connection state
	catalogSourceGVR := schema.GroupVersionResource{
		Group:    "operators.coreos.com",
		Version:  "v1alpha1",
		Resource: "catalogsources",
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
