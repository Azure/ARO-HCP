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

package release

import (
	"context"
	"fmt"
	"strings"
	"time"

	"helm.sh/helm/v3/pkg/action"
	"helm.sh/helm/v3/pkg/release"
	appsv1 "k8s.io/api/apps/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"sigs.k8s.io/yaml"
)

// DiscoverReleases discovers Helm releases based on the provided criteria
func DiscoverReleases(ctx context.Context, helmClient *action.Configuration, releaseName, namespace string) ([]ReleaseInfo, error) {
	listAction := action.NewList(helmClient)

	// Configure list action
	listAction.AllNamespaces = namespace == ""
	if namespace != "" {
		listAction.Filter = ""
	}

	// Get all releases
	releases, err := listAction.Run()
	if err != nil {
		return nil, fmt.Errorf("failed to list Helm releases: %w", err)
	}

	var results []ReleaseInfo
	for _, rel := range releases {
		// Filter by release name if specified
		if releaseName != "" && rel.Name != releaseName {
			continue
		}

		// Filter by namespace if specified
		if namespace != "" && rel.Namespace != namespace {
			continue
		}

		results = append(results, ReleaseInfo{
			Name:      rel.Name,
			Namespace: rel.Namespace,
			Chart:     getChartName(rel),
		})
	}

	return results, nil
}

// GenerateReports generates image promotion reports for the provided releases
func GenerateReports(ctx context.Context, helmClient *action.Configuration, kubeClient kubernetes.Interface, releases []ReleaseInfo, aroHcpCommit, sdpPipelinesCommit string) ([]ComponentRelease, error) {
	var reports []ComponentRelease

	for _, releaseInfo := range releases {
		report, err := generateReportForRelease(ctx, helmClient, kubeClient, releaseInfo)
		if err != nil {
			return nil, fmt.Errorf("failed to generate report for release %s/%s: %w", releaseInfo.Namespace, releaseInfo.Name, err)
		}

		// Only include components that have workloads
		if len(report.Workloads) > 0 {
			reports = append(reports, *report)
		}
	}

	return reports, nil
}

// generateReportForRelease generates an image promotion report for a single release
func generateReportForRelease(ctx context.Context, helmClient *action.Configuration, kubeClient kubernetes.Interface, releaseInfo ReleaseInfo) (*ComponentRelease, error) {
	var workloads []WorkloadInfo
	var deploymentTime time.Time

	// Extract workloads from Helm
	if helmClient != nil {
		statusAction := action.NewStatus(helmClient)
		rel, err := statusAction.Run(releaseInfo.Name)
		if err != nil {
			return nil, fmt.Errorf("failed to get status for release %s: %w", releaseInfo.Name, err)
		}

		helmWorkloads, err := extractWorkloadsFromManifest(rel.Manifest, rel.Namespace)
		if err != nil {
			return nil, fmt.Errorf("failed to extract workloads from manifest: %w", err)
		}
		workloads = helmWorkloads
		deploymentTime = getDeploymentTimestamp(rel)
	} else {
		deploymentTime = time.Now().UTC()
	}

	// Enhance workloads with current Kubernetes images
	if kubeClient != nil {
		for i := range workloads {
			currentImage, err := queryActualWorkloadImage(ctx, kubeClient, &workloads[i])
			if err != nil {
				workloads[i].CurrentImage = "NOT_FOUND"
			} else {
				workloads[i].CurrentImage = currentImage
			}
		}
	}

	report := NewComponentRelease(releaseInfo.Name, deploymentTime, workloads)

	return &report, nil
}

// extractWorkloadsFromManifest extracts workload information from Helm manifest YAML
func extractWorkloadsFromManifest(manifest string, releaseNamespace string) ([]WorkloadInfo, error) {
	var workloads []WorkloadInfo

	// Split manifest by document separator
	documents := strings.Split(manifest, "\n---\n")

	for _, doc := range documents {
		doc = strings.TrimSpace(doc)
		if doc == "" {
			continue
		}

		// Parse each YAML document
		var obj map[string]interface{}
		if err := yaml.Unmarshal([]byte(doc), &obj); err != nil {
			// Skip invalid YAML documents
			return nil, fmt.Errorf("failed to unmarshal YAML document: %w", err)
		}

		// Check if this is a workload (Deployment, DaemonSet, StatefulSet, etc.)
		workload := extractWorkloadInfoFromYAML(doc, releaseNamespace)
		if workload != nil {
			workloads = append(workloads, *workload)
		}
	}

	return workloads, nil
}

// extractWorkloadInfoFromYAML extracts workload information from YAML document using Kubernetes structs
func extractWorkloadInfoFromYAML(yamlDoc, releaseNamespace string) *WorkloadInfo {
	// First, parse to get the kind
	var typeMeta metav1.TypeMeta
	if err := yaml.Unmarshal([]byte(yamlDoc), &typeMeta); err != nil {
		return nil
	}

	switch typeMeta {
	case metav1.TypeMeta{APIVersion: "apps/v1", Kind: "Deployment"}:
		return extractWorkloadFromDeployment(yamlDoc, releaseNamespace)
	case metav1.TypeMeta{APIVersion: "apps/v1", Kind: "DaemonSet"}:
		return extractWorkloadFromDaemonSet(yamlDoc, releaseNamespace)
	case metav1.TypeMeta{APIVersion: "apps/v1", Kind: "StatefulSet"}:
		return extractWorkloadFromStatefulSet(yamlDoc, releaseNamespace)
	default:
		return nil
	}
}

// extractWorkloadFromDeployment extracts workload info from a Deployment
func extractWorkloadFromDeployment(yamlDoc, releaseNamespace string) *WorkloadInfo {
	var deployment appsv1.Deployment
	if err := yaml.Unmarshal([]byte(yamlDoc), &deployment); err != nil {
		return nil
	}

	// Check if this workload is managed by AKS and skip it
	if isAKSManagedManifest(deployment.Labels) {
		return nil
	}

	namespace := deployment.Namespace
	if namespace == "" {
		namespace = releaseNamespace
	}

	if deployment.Name == "" {
		return nil
	}

	// Use the shared function from kubernetes.go
	image := extractFirstContainerImageFromPodSpec(deployment.Spec.Template.Spec)
	if image == "" {
		return nil
	}

	return &WorkloadInfo{
		Name:         deployment.Name,
		Namespace:    namespace,
		Kind:         "Deployment",
		DesiredImage: image,
		CurrentImage: "", // Will be populated later
	}
}

// extractWorkloadFromDaemonSet extracts workload info from a DaemonSet
func extractWorkloadFromDaemonSet(yamlDoc, releaseNamespace string) *WorkloadInfo {
	var daemonSet appsv1.DaemonSet
	if err := yaml.Unmarshal([]byte(yamlDoc), &daemonSet); err != nil {
		return nil
	}

	// Check if this workload is managed by AKS and skip it
	if isAKSManagedManifest(daemonSet.Labels) {
		return nil
	}

	namespace := daemonSet.Namespace
	if namespace == "" {
		namespace = releaseNamespace
	}

	if daemonSet.Name == "" {
		return nil
	}

	// Use the shared function from kubernetes.go
	image := extractFirstContainerImageFromPodSpec(daemonSet.Spec.Template.Spec)
	if image == "" {
		return nil
	}

	return &WorkloadInfo{
		Name:         daemonSet.Name,
		Namespace:    namespace,
		Kind:         "DaemonSet",
		DesiredImage: image,
		CurrentImage: "", // Will be populated later
	}
}

// extractWorkloadFromStatefulSet extracts workload info from a StatefulSet
func extractWorkloadFromStatefulSet(yamlDoc, releaseNamespace string) *WorkloadInfo {
	var statefulSet appsv1.StatefulSet
	if err := yaml.Unmarshal([]byte(yamlDoc), &statefulSet); err != nil {
		return nil
	}

	// Check if this workload is managed by AKS and skip it
	if isAKSManagedManifest(statefulSet.Labels) {
		return nil
	}

	namespace := statefulSet.Namespace
	if namespace == "" {
		namespace = releaseNamespace
	}

	if statefulSet.Name == "" {
		return nil
	}

	// Use the shared function from kubernetes.go
	image := extractFirstContainerImageFromPodSpec(statefulSet.Spec.Template.Spec)
	if image == "" {
		return nil
	}

	return &WorkloadInfo{
		Name:         statefulSet.Name,
		Namespace:    namespace,
		Kind:         "StatefulSet",
		DesiredImage: image,
	}
}

// isAKSManagedManifest checks if a workload is managed by AKS using structured labels
func isAKSManagedManifest(labels map[string]string) bool {
	if labels == nil {
		return false
	}

	managedBy, ok := labels["kubernetes.azure.com/managedby"]
	if !ok {
		return false
	}

	return managedBy == "aks"
}

// getChartName extracts chart name from Helm release
func getChartName(rel *release.Release) string {
	if rel != nil && rel.Chart != nil && rel.Chart.Metadata != nil {
		return rel.Chart.Metadata.Name
	}
	return "unknown"
}

// getDeploymentTimestamp extracts the deployment timestamp from Helm release
func getDeploymentTimestamp(rel *release.Release) time.Time {
	if rel != nil && rel.Info != nil && !rel.Info.LastDeployed.IsZero() {
		return rel.Info.LastDeployed.Time
	}
	// Fallback to first deployed if last deployed is not available
	if rel != nil && rel.Info != nil && !rel.Info.FirstDeployed.IsZero() {
		return rel.Info.FirstDeployed.Time
	}
	// Final fallback to current time (shouldn't happen in normal cases)
	return time.Now().UTC()
}
