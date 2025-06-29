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

// Package cluster provides functionality for discovering and managing HyperShift-managed clusters.
//
// This package handles the discovery of HostedCluster resources and the construction
// of their associated namespaces based on cluster IDs. It abstracts the complexity
// of HostedCluster resource queries and namespace naming conventions.
//
// Example usage:
//
//	discovery := cluster.NewDiscovery(dynamicClient)
//	clusterInfo, err := discovery.DiscoverClusterByID(ctx, "my-cluster-id")
//	if err != nil {
//		log.Fatal(err)
//	}
//
//	// Use cluster information for further operations
//	fmt.Printf("Cluster %s is in namespace %s\n", clusterInfo.Name, clusterInfo.Namespace)
package cluster

import (
	"context"
	"fmt"

	azcorearm "github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/dynamic"
)

// HostedClusterGVR is the GroupVersionResource for HostedCluster
var HostedClusterGVR = schema.GroupVersionResource{
	Group:    "hypershift.openshift.io",
	Version:  "v1beta1",
	Resource: "hostedclusters",
}

// DynamicClient is an interface for dynamic Kubernetes API operations.
// This interface wraps the standard dynamic.Interface to enable testing
// with mock implementations for custom resources like HostedClusters.
type DynamicClient interface {
	dynamic.Interface
}

// Discovery is an interface for cluster discovery operations.
// This interface abstracts HostedCluster discovery and namespace construction
// to enable testing and potential alternative cluster discovery mechanisms.
type Discovery interface {

	// DiscoverClusterByID finds cluster information by its ID
	DiscoverClusterByID(ctx context.Context, clusterID string) (ClusterInfo, error)

	// ListAllClusters returns all available HostedClusters with their details
	ListAllClusters(ctx context.Context) ([]ClusterInfo, error)

	// DiscoverClusterByResourceID finds cluster information by its Azure resource ID
	DiscoverClusterByResourceID(ctx context.Context, resourceID *azcorearm.ResourceID) (ClusterInfo, error)
}

// ClusterInfo contains information about a discovered cluster
type ClusterInfo struct {
	ID                string // Cluster ID from api.openshift.com/id label
	Name              string // Cluster name from metadata.name
	Namespace         string // Base namespace from metadata.namespace
	ResourceID        string // Azure resource ID if available
	SubscriptionID    string // Azure subscription ID from spec.platform.azure.subscriptionID
	ResourceGroupName string // Azure resource group from spec.platform.azure.resourceGroup
}

// DefaultDiscovery is the default implementation of Discovery.
// It uses the Kubernetes dynamic client to query HostedCluster resources
// and construct namespace names based on HyperShift conventions.
type DefaultDiscovery struct {
	dynamicClient DynamicClient
}

// NewDiscovery creates a new cluster discovery instance.
// The dynamic client is used to query HostedCluster resources.
func NewDiscovery(dynamicClient DynamicClient) *DefaultDiscovery {
	return &DefaultDiscovery{
		dynamicClient: dynamicClient,
	}
}

// DiscoverClusterByID finds cluster information by its ID.
// This is the preferred method for cluster discovery as it provides complete
// information about the cluster's location and identity.
//
// The method queries HostedCluster resources using the cluster ID label
// and returns the full ClusterInfo struct.
func (d *DefaultDiscovery) DiscoverClusterByID(ctx context.Context, clusterID string) (ClusterInfo, error) {
	// Find the HostedCluster resource
	labelSelector := fmt.Sprintf("api.openshift.com/id=%s", clusterID)
	hcList, err := d.dynamicClient.Resource(HostedClusterGVR).List(ctx, metav1.ListOptions{
		LabelSelector: labelSelector,
	})
	if err != nil {
		return ClusterInfo{}, fmt.Errorf("failed to list HostedClusters: %w", err)
	}

	if len(hcList.Items) == 0 {
		return ClusterInfo{}, fmt.Errorf("no HostedCluster found with ID %s", clusterID)
	}
	if len(hcList.Items) > 1 {
		return ClusterInfo{}, fmt.Errorf("multiple HostedClusters found with ID %s", clusterID)
	}

	hostedCluster := &hcList.Items[0]
	clusterInfo := hostedClusterToClusterInfo(hostedCluster)

	// Ensure the cluster ID is set (it should come from the label, but we know what it is)
	if clusterInfo.ID == "" {
		clusterInfo.ID = clusterID
	}

	return clusterInfo, nil
}

// ListAllClusters returns all available HostedClusters with their details.
// This method queries all HostedCluster resources across all namespaces
// and extracts relevant information for cluster selection.
func (d *DefaultDiscovery) ListAllClusters(ctx context.Context) ([]ClusterInfo, error) {
	// List all HostedClusters across all namespaces
	hcList, err := d.dynamicClient.Resource(HostedClusterGVR).List(ctx, metav1.ListOptions{})
	if err != nil {
		return nil, fmt.Errorf("failed to list HostedClusters: %w", err)
	}

	var clusters []ClusterInfo
	for _, item := range hcList.Items {
		clusterInfo := hostedClusterToClusterInfo(&item)

		// Only include clusters that have an ID
		if clusterInfo.ID != "" {
			clusters = append(clusters, clusterInfo)
		}
	}

	return clusters, nil
}

// DiscoverClusterByResourceID finds cluster information by its Azure resource ID.
// It uses the cluster name from the resource ID to filter HostedClusters, then
// validates the subscription ID and resource group to ensure an exact match.
func (d *DefaultDiscovery) DiscoverClusterByResourceID(ctx context.Context, resourceID *azcorearm.ResourceID) (ClusterInfo, error) {
	clusterName := resourceID.Name
	if clusterName == "" {
		return ClusterInfo{}, fmt.Errorf("cluster name cannot be empty in resource ID")
	}

	// List all HostedClusters with the matching name
	labelSelector := fmt.Sprintf("api.openshift.com/name=%s", clusterName)
	hcList, err := d.dynamicClient.Resource(HostedClusterGVR).List(ctx, metav1.ListOptions{
		LabelSelector: labelSelector,
	})
	if err != nil {
		return ClusterInfo{}, fmt.Errorf("failed to list HostedClusters: %w", err)
	}

	if len(hcList.Items) == 0 {
		return ClusterInfo{}, fmt.Errorf("no HostedCluster found with name %s", clusterName)
	}

	// Convert all matching clusters to ClusterInfo and filter by subscription and resource group
	var matchingClusters []ClusterInfo
	for _, item := range hcList.Items {
		clusterInfo := hostedClusterToClusterInfo(&item)

		// Check if subscription ID and resource group match
		if clusterInfo.SubscriptionID == resourceID.SubscriptionID &&
			clusterInfo.ResourceGroupName == resourceID.ResourceGroupName {
			matchingClusters = append(matchingClusters, clusterInfo)
		}
	}

	if len(matchingClusters) == 0 {
		return ClusterInfo{}, fmt.Errorf("no HostedCluster found with name %s matching subscription %s and resource group %s",
			clusterName, resourceID.SubscriptionID, resourceID.ResourceGroupName)
	}

	if len(matchingClusters) > 1 {
		return ClusterInfo{}, fmt.Errorf("multiple HostedClusters found with name %s matching subscription %s and resource group %s",
			clusterName, resourceID.SubscriptionID, resourceID.ResourceGroupName)
	}

	return matchingClusters[0], nil
}

// hostedClusterToClusterInfo converts a HostedCluster unstructured object to ClusterInfo
func hostedClusterToClusterInfo(hostedCluster *unstructured.Unstructured) ClusterInfo {
	baseNamespace := hostedCluster.GetNamespace()
	clusterName := hostedCluster.GetName()
	labels := hostedCluster.GetLabels()

	// Construct the full namespace as baseNamespace-clusterName
	fullNamespace := fmt.Sprintf("%s-%s", baseNamespace, clusterName)

	clusterInfo := ClusterInfo{
		Name:      clusterName,
		Namespace: fullNamespace,
	}

	// Get cluster ID from label
	if id, ok := labels["api.openshift.com/id"]; ok {
		clusterInfo.ID = id
	}

	// Extract Azure subscription ID and resource group
	subscriptionID, _, _ := unstructured.NestedString(hostedCluster.Object, "spec", "platform", "azure", "subscriptionID")
	resourceGroup, _, _ := unstructured.NestedString(hostedCluster.Object, "spec", "platform", "azure", "resourceGroup")

	clusterInfo.SubscriptionID = subscriptionID
	clusterInfo.ResourceGroupName = resourceGroup

	// Get Azure resource ID from annotations if available
	clusterInfo.ResourceID = constructAzureResourceID(hostedCluster)

	return clusterInfo
}

// constructAzureResourceID builds the Azure resource ID from HostedCluster spec fields
func constructAzureResourceID(hostedCluster *unstructured.Unstructured) string {
	// Extract subscription ID from spec.platform.azure.subscriptionID
	subscriptionID, found, err := unstructured.NestedString(hostedCluster.Object, "spec", "platform", "azure", "subscriptionID")
	if err != nil || !found || subscriptionID == "" {
		return ""
	}

	// Extract resource group from spec.platform.azure.resourceGroup
	resourceGroup, found, err := unstructured.NestedString(hostedCluster.Object, "spec", "platform", "azure", "resourceGroup")
	if err != nil || !found || resourceGroup == "" {
		return ""
	}

	// Extract cluster name from metadata.name
	clusterName := hostedCluster.GetName()
	if clusterName == "" {
		return ""
	}

	// Construct the Azure resource ID
	return fmt.Sprintf("/subscriptions/%s/resourceGroups/%s/providers/Microsoft.RedHatOpenshift/hcpOpenShiftClusters/%s",
		subscriptionID, resourceGroup, clusterName)
}
