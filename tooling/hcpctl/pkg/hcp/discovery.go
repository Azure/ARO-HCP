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

package cluster

import (
	"context"
	"fmt"

	azcorearm "github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"
	"github.com/go-logr/logr"
	hypershiftv1beta1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	client "sigs.k8s.io/controller-runtime/pkg/client"
)

// HCPInfo contains information about a discovered HCP cluster
type HCPInfo struct {
	ID                string `json:"id" yaml:"id"`                               // Cluster ID from api.openshift.com/id label
	Name              string `json:"name" yaml:"name"`                           // Cluster name from metadata.name
	Namespace         string `json:"namespace" yaml:"namespace"`                 // Base namespace from metadata.namespace
	ResourceID        string `json:"resourceId" yaml:"resourceId"`               // Azure resource ID if available
	SubscriptionID    string `json:"subscriptionId" yaml:"subscriptionId"`       // Azure subscription ID from spec.platform.azure.subscriptionID
	ResourceGroupName string `json:"resourceGroupName" yaml:"resourceGroupName"` // Azure resource group from spec.platform.azure.resourceGroup
}

// Discovery handles cluster discovery operations.
// It uses the controller-runtime client to query HostedCluster resources
// and construct namespace names based on HyperShift conventions.
type Discovery struct {
	ctrlClient client.Client
}

// NewDiscovery creates a new cluster discovery instance.
// The controller-runtime client is used to query HostedCluster resources.
func NewDiscovery(ctrlClient client.Client) *Discovery {
	return &Discovery{
		ctrlClient: ctrlClient,
	}
}

// DiscoverClusterByID finds cluster information by its ID.
// This is the preferred method for cluster discovery as it provides complete
// information about the cluster's location and identity.
//
// The method queries HostedCluster resources using the cluster ID label
// and returns the full HCPInfo struct.
func (d *Discovery) DiscoverClusterByID(ctx context.Context, clusterID string) (HCPInfo, error) {
	// Find the HostedCluster resource
	labelSelector := client.MatchingLabels{"api.openshift.com/id": clusterID}
	hcList := &hypershiftv1beta1.HostedClusterList{}
	if err := d.ctrlClient.List(ctx, hcList, labelSelector); err != nil {
		return HCPInfo{}, fmt.Errorf("failed to list HostedClusters: %w", err)
	}

	if len(hcList.Items) == 0 {
		return HCPInfo{}, fmt.Errorf("no cluster found")
	}
	if len(hcList.Items) > 1 {
		return HCPInfo{}, fmt.Errorf("multiple clusters found")
	}

	clusterInfo, err := hostedClusterToHCPInfo(&hcList.Items[0])
	if err != nil {
		return HCPInfo{}, fmt.Errorf("failed to convert HostedCluster to HCPInfo: %w", err)
	}
	return clusterInfo, nil
}

// ListAllClusters returns all available HostedClusters with their details.
// This method queries all HostedCluster resources across all namespaces
// and extracts relevant information for cluster selection.
func (d *Discovery) ListAllClusters(ctx context.Context) ([]HCPInfo, error) {
	// List all HostedClusters across all namespaces
	hcList := &hypershiftv1beta1.HostedClusterList{}
	if err := d.ctrlClient.List(ctx, hcList); err != nil {
		return nil, fmt.Errorf("failed to list HostedClusters: %w", err)
	}

	return processListAllClustersResults(ctx, hcList.Items)
}

// processListAllClustersResults processes the results from listing all HostedClusters
func processListAllClustersResults(ctx context.Context, items []hypershiftv1beta1.HostedCluster) ([]HCPInfo, error) {
	logger := logr.FromContextOrDiscard(ctx)
	var clusters []HCPInfo
	for _, item := range items {
		clusterInfo, err := hostedClusterToHCPInfo(&item)
		if err != nil {
			// Log clusters that are missing required fields instead of silently skipping them
			logger.V(1).Info("Skipping cluster with missing required fields",
				"name", item.Name,
				"namespace", item.Namespace,
				"error", err.Error())
			continue
		}

		clusters = append(clusters, clusterInfo)
	}

	return clusters, nil
}

// DiscoverClusterByResourceID finds cluster information by its Azure resource ID.
// It uses the cluster name from the resource ID to filter HostedClusters, then
// validates the subscription ID and resource group to ensure an exact match.
func (d *Discovery) DiscoverClusterByResourceID(ctx context.Context, resourceID *azcorearm.ResourceID) (HCPInfo, error) {
	clusterName := resourceID.Name
	if clusterName == "" {
		return HCPInfo{}, fmt.Errorf("cluster name cannot be empty in resource ID")
	}

	// List all HostedClusters with the matching name
	labelSelector := client.MatchingLabels{"api.openshift.com/name": clusterName}
	hcList := &hypershiftv1beta1.HostedClusterList{}
	if err := d.ctrlClient.List(ctx, hcList, labelSelector); err != nil {
		return HCPInfo{}, fmt.Errorf("failed to list HostedClusters: %w", err)
	}

	return findHCPInfoByPredicate(ctx, hcList.Items, func(hcpInfo HCPInfo) bool {
		return hcpInfo.ResourceID == resourceID.String()
	})
}

func findHCPInfoByPredicate(ctx context.Context, items []hypershiftv1beta1.HostedCluster, predicate func(HCPInfo) bool) (HCPInfo, error) {
	var matchingClusters []HCPInfo
	logger := logr.FromContextOrDiscard(ctx)
	for _, item := range items {
		clusterInfo, err := hostedClusterToHCPInfo(&item)
		if err != nil {
			logger.V(1).Info("Skipping cluster",
				"name", item.Name,
				"namespace", item.Namespace,
				"error", err.Error())
			continue
		}

		if predicate(clusterInfo) {
			matchingClusters = append(matchingClusters, clusterInfo)
		}
	}

	if len(matchingClusters) == 0 {
		return HCPInfo{}, fmt.Errorf("no cluster found matching the criteria")
	}

	if len(matchingClusters) > 1 {
		return HCPInfo{}, fmt.Errorf("multiple clusters found matching the criteria")
	}

	return matchingClusters[0], nil
}

// hostedClusterToHCPInfo converts a HostedCluster typed object to HCPInfo
func hostedClusterToHCPInfo(hostedCluster *hypershiftv1beta1.HostedCluster) (HCPInfo, error) {
	baseNamespace := hostedCluster.Namespace
	clusterName := hostedCluster.Name

	// Construct the full namespace as baseNamespace-clusterName
	fullNamespace := fmt.Sprintf("%s-%s", baseNamespace, clusterName)

	// Get cluster ID from label - fail if not present
	clusterID, ok := hostedCluster.Labels["api.openshift.com/id"]
	if !ok {
		return HCPInfo{}, fmt.Errorf("object %s/%s is missing required label 'api.openshift.com/id'", baseNamespace, clusterName)
	}

	// Azure-specific fields - ensure Azure platform is configured
	if hostedCluster.Spec.Platform.Azure == nil {
		return HCPInfo{}, fmt.Errorf("HostedCluster %s/%s does not have Azure platform configuration", baseNamespace, clusterName)
	}

	subscriptionID := hostedCluster.Spec.Platform.Azure.SubscriptionID
	resourceGroup := hostedCluster.Spec.Platform.Azure.ResourceGroupName

	resourceID, err := constructAzureResourceID(subscriptionID, resourceGroup, clusterName)
	if err != nil {
		return HCPInfo{}, fmt.Errorf("failed to construct Azure resource ID: %w", err)
	}

	return HCPInfo{
		Name:              clusterName,
		Namespace:         fullNamespace,
		ID:                clusterID,
		ResourceID:        resourceID,
		SubscriptionID:    subscriptionID,
		ResourceGroupName: resourceGroup,
	}, nil
}

func constructAzureResourceID(subscriptionID, resourceGroup, clusterName string) (string, error) {
	if subscriptionID == "" {
		return "", fmt.Errorf("subscription ID cannot be empty")
	}
	if resourceGroup == "" {
		return "", fmt.Errorf("resource group cannot be empty")
	}
	if clusterName == "" {
		return "", fmt.Errorf("cluster name cannot be empty")
	}
	return fmt.Sprintf(
		"/subscriptions/%s/resourceGroups/%s/providers/Microsoft.RedHatOpenshift/hcpOpenShiftClusters/%s",
		subscriptionID, resourceGroup, clusterName,
	), nil
}
