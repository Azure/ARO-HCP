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

// Package cluster provides functionality for discovering and managing different types of clusters.
//
// This package includes discovery capabilities for both HCP (HostedCluster) resources via
// Kubernetes API and AKS clusters via Azure SDK. The AKS discovery supports filtering
// by cluster type tags (e.g., management clusters, service clusters).
package cluster

import (
	"context"
	"fmt"
	"log"
	"sync"
	"time"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/resources/armresources"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/subscription/armsubscription"
)

// Removed pageSizePolicy since Azure AKS API ignores $top parameter

// AKSCluster represents an AKS cluster with its metadata
type AKSCluster struct {
	Name           string
	ResourceGroup  string
	SubscriptionID string
	Subscription   string // Subscription display name
	Location       string
	State          string
	Tags           map[string]string // Full tag map for flexibility
}

// ClusterTypeFilter defines filtering criteria for AKS cluster discovery
type ClusterTypeFilter struct {
	TagKey   string // e.g., "clusterType"
	TagValue string // e.g., "mgmt-cluster" or "svc-cluster"
}

// AKSDiscovery handles AKS cluster discovery operations
type AKSDiscovery struct {
	credential azcore.TokenCredential
}

// NewAKSDiscovery creates a new AKS discovery instance
func NewAKSDiscovery(cred azcore.TokenCredential) *AKSDiscovery {
	return &AKSDiscovery{
		credential: cred,
	}
}

// DiscoverClusters searches all subscriptions for AKS clusters matching the optional filter
func (d *AKSDiscovery) DiscoverClusters(ctx context.Context, filter *ClusterTypeFilter) ([]AKSCluster, error) {
	return d.discoverClustersWithFilter(ctx, filter)
}

// discoverClustersWithFilter performs the actual discovery logic with optional filtering
func (d *AKSDiscovery) discoverClustersWithFilter(ctx context.Context, filter *ClusterTypeFilter) ([]AKSCluster, error) {
	// Create subscription client
	subClient, err := armsubscription.NewSubscriptionsClient(d.credential, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create subscription client: %w", err)
	}

	// List all subscriptions
	var subscriptions []*armsubscription.Subscription
	pager := subClient.NewListPager(nil)
	for pager.More() {
		page, err := pager.NextPage(ctx)
		if err != nil {
			return nil, fmt.Errorf("failed to list subscriptions: %w", err)
		}
		subscriptions = append(subscriptions, page.Value...)
	}

	// Search for clusters in parallel - use one worker per subscription for I/O bound operations
	maxWorkers := len(subscriptions)

	// Create work channels
	subChan := make(chan *armsubscription.Subscription, len(subscriptions))
	resultChan := make(chan []AKSCluster, len(subscriptions))

	// Start workers
	var wg sync.WaitGroup
	for i := 0; i < maxWorkers; i++ {
		wg.Add(1)
		go func(workerID int) {
			defer wg.Done()
			for sub := range subChan {
				clusters := d.discoverInSubscription(ctx, sub, filter, workerID)
				resultChan <- clusters
			}
		}(i)
	}

	// Send work to workers
	go func() {
		defer close(subChan)
		for _, sub := range subscriptions {
			if sub.SubscriptionID != nil && sub.DisplayName != nil {
				subChan <- sub
			}
		}
	}()

	// Collect results
	go func() {
		wg.Wait()
		close(resultChan)
	}()

	var allClusters []AKSCluster
	for clusters := range resultChan {
		allClusters = append(allClusters, clusters...)
	}

	return allClusters, nil
}

// discoverInSubscription discovers clusters in a single subscription using Resource Manager API
func (d *AKSDiscovery) discoverInSubscription(ctx context.Context, sub *armsubscription.Subscription, filter *ClusterTypeFilter, workerID int) []AKSCluster {
	subID := *sub.SubscriptionID
	subName := *sub.DisplayName

	// Add timeout for individual subscription processing
	subCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()

	// Use Azure Resource Manager API for faster AKS discovery
	resourcesClient, err := armresources.NewClient(subID, d.credential, nil)
	if err != nil {
		return nil
	}

	var clusters []AKSCluster

	// List AKS clusters using generic resource API
	filterQuery := "resourceType eq 'Microsoft.ContainerService/managedClusters'"
	listOptions := &armresources.ClientListOptions{
		Filter: &filterQuery,
	}

	pager := resourcesClient.NewListPager(listOptions)

	for pager.More() {
		page, err := pager.NextPage(subCtx)
		if err != nil {
			break
		}

		for _, resource := range page.Value {
			if resource.ID == nil || resource.Name == nil || resource.Location == nil {
				continue
			}

			// Apply tag filtering in memory
			if filter != nil && filter.TagKey != "" && filter.TagValue != "" {
				if resource.Tags == nil {
					continue
				}
				tagValue, exists := resource.Tags[filter.TagKey]
				if !exists || tagValue == nil || *tagValue != filter.TagValue {
					continue
				}
			}

			aksCluster := AKSCluster{
				Name:           *resource.Name,
				SubscriptionID: subID,
				Subscription:   subName,
				Location:       *resource.Location,
				Tags:           make(map[string]string),
			}

			// Extract resource group from ID using Azure SDK
			if parsedID, err := arm.ParseResourceID(*resource.ID); err != nil {
				log.Printf("[WARN] Worker %d: Failed to parse resource ID %s: %v", workerID, *resource.ID, err)
				aksCluster.ResourceGroup = "" // Fallback to empty string
			} else {
				aksCluster.ResourceGroup = parsedID.ResourceGroupName
			}

			// Copy all tags
			if resource.Tags != nil {
				for key, value := range resource.Tags {
					if value != nil {
						aksCluster.Tags[key] = *value
					}
				}
			}

			clusters = append(clusters, aksCluster)
		}
	}

	return clusters
}

// Factory functions for specific cluster types

// NewManagementClusterDiscovery creates an AKS discovery instance for management clusters
func NewManagementClusterDiscovery(cred azcore.TokenCredential) *AKSDiscovery {
	return NewAKSDiscovery(cred)
}

// NewServiceClusterDiscovery creates an AKS discovery instance for service clusters
func NewServiceClusterDiscovery(cred azcore.TokenCredential) *AKSDiscovery {
	return NewAKSDiscovery(cred)
}
