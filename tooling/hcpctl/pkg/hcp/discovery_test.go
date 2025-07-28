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
	"testing"

	hypershiftv1beta1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	"github.com/stretchr/testify/assert"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// Helper functions to create test HostedCluster objects
func createHostedCluster(name, namespace, clusterID, subscriptionID, resourceGroup string) *hypershiftv1beta1.HostedCluster {
	if name == "" {
		panic("createHostedCluster: name is required")
	}
	if namespace == "" {
		panic("createHostedCluster: namespace is required")
	}

	labels := map[string]string{
		"api.openshift.com/name": name,
	}

	if clusterID != "" {
		labels["api.openshift.com/id"] = clusterID
	}

	var azurePlatform *hypershiftv1beta1.AzurePlatformSpec
	if subscriptionID != "" || resourceGroup != "" {
		azurePlatform = &hypershiftv1beta1.AzurePlatformSpec{
			SubscriptionID:    subscriptionID,
			ResourceGroupName: resourceGroup,
		}
	}

	return &hypershiftv1beta1.HostedCluster{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: namespace,
			Labels:    labels,
		},
		Spec: hypershiftv1beta1.HostedClusterSpec{
			Platform: hypershiftv1beta1.PlatformSpec{
				Azure: azurePlatform,
			},
		},
	}
}

// Helper function for creating HostedCluster objects with only basic fields for specific tests
func createBasicHostedCluster(name, namespace, clusterID string) *hypershiftv1beta1.HostedCluster {
	if name == "" {
		panic("createBasicHostedCluster: name is required")
	}
	if namespace == "" {
		panic("createBasicHostedCluster: namespace is required")
	}
	if clusterID == "" {
		panic("createBasicHostedCluster: clusterID is required")
	}

	return &hypershiftv1beta1.HostedCluster{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: namespace,
			Labels: map[string]string{
				"api.openshift.com/id":   clusterID,
				"api.openshift.com/name": name,
			},
		},
		Spec: hypershiftv1beta1.HostedClusterSpec{
			Platform: hypershiftv1beta1.PlatformSpec{
				Azure: &hypershiftv1beta1.AzurePlatformSpec{
					SubscriptionID:    "test-sub",
					ResourceGroupName: "test-rg",
				},
			},
		},
	}
}

func TestConstructAzureResourceID(t *testing.T) {
	testCases := []struct {
		name           string
		subscriptionID string
		resourceGroup  string
		clusterName    string
		expectedID     string
		expectedError  string
	}{
		{
			name:           "constructs valid resource ID",
			subscriptionID: "sub-123",
			resourceGroup:  "rg-test",
			clusterName:    "test-cluster",
			expectedID:     "/subscriptions/sub-123/resourceGroups/rg-test/providers/Microsoft.RedHatOpenshift/hcpOpenShiftClusters/test-cluster",
		},
		{
			name:           "empty subscription ID returns error",
			subscriptionID: "",
			resourceGroup:  "rg-test",
			clusterName:    "test-cluster",
			expectedError:  "subscription ID cannot be empty",
		},
		{
			name:           "empty resource group returns error",
			subscriptionID: "sub-123",
			resourceGroup:  "",
			clusterName:    "test-cluster",
			expectedError:  "resource group cannot be empty",
		},
		{
			name:           "empty cluster name returns error",
			subscriptionID: "sub-123",
			resourceGroup:  "rg-test",
			clusterName:    "",
			expectedError:  "cluster name cannot be empty",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			result, err := constructAzureResourceID(tc.subscriptionID, tc.resourceGroup, tc.clusterName)

			if tc.expectedError != "" {
				assert.Error(t, err)
				assert.Contains(t, err.Error(), tc.expectedError)
				assert.Empty(t, result)
			} else {
				assert.NoError(t, err)
				assert.Equal(t, tc.expectedID, result)
			}
		})
	}
}

func TestProcessListAllClustersResults(t *testing.T) {
	testCases := []struct {
		name             string
		items            []hypershiftv1beta1.HostedCluster
		expectedClusters []HCPInfo
	}{
		{
			name: "returns all clusters with IDs",
			items: []hypershiftv1beta1.HostedCluster{
				*createHostedCluster("cluster-1", "ns-1", "id-1", "sub-1", "rg-1"),
				*createHostedCluster("cluster-2", "ns-2", "id-2", "sub-2", "rg-2"),
			},
			expectedClusters: []HCPInfo{
				{
					ID:                "id-1",
					Name:              "cluster-1",
					Namespace:         "ns-1-cluster-1",
					SubscriptionID:    "sub-1",
					ResourceGroupName: "rg-1",
					ResourceID:        "/subscriptions/sub-1/resourceGroups/rg-1/providers/Microsoft.RedHatOpenshift/hcpOpenShiftClusters/cluster-1",
				},
				{
					ID:                "id-2",
					Name:              "cluster-2",
					Namespace:         "ns-2-cluster-2",
					SubscriptionID:    "sub-2",
					ResourceGroupName: "rg-2",
					ResourceID:        "/subscriptions/sub-2/resourceGroups/rg-2/providers/Microsoft.RedHatOpenshift/hcpOpenShiftClusters/cluster-2",
				},
			},
		},
		{
			name:             "empty items returns empty slice",
			items:            []hypershiftv1beta1.HostedCluster{},
			expectedClusters: nil,
		},
		{
			name: "skips clusters with missing required fields",
			items: []hypershiftv1beta1.HostedCluster{
				*createHostedCluster("cluster-valid", "ns-1", "id-1", "sub-1", "rg-1"),
				*createHostedCluster("cluster-missing-id", "ns-2", "", "sub-2", "rg-2"), // Missing cluster ID
				*createHostedCluster("cluster-valid-2", "ns-3", "id-3", "sub-3", "rg-3"),
			},
			expectedClusters: []HCPInfo{
				{
					ID:                "id-1",
					Name:              "cluster-valid",
					Namespace:         "ns-1-cluster-valid",
					SubscriptionID:    "sub-1",
					ResourceGroupName: "rg-1",
					ResourceID:        "/subscriptions/sub-1/resourceGroups/rg-1/providers/Microsoft.RedHatOpenshift/hcpOpenShiftClusters/cluster-valid",
				},
				{
					ID:                "id-3",
					Name:              "cluster-valid-2",
					Namespace:         "ns-3-cluster-valid-2",
					SubscriptionID:    "sub-3",
					ResourceGroupName: "rg-3",
					ResourceID:        "/subscriptions/sub-3/resourceGroups/rg-3/providers/Microsoft.RedHatOpenshift/hcpOpenShiftClusters/cluster-valid-2",
				},
			},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			ctx := context.Background()
			clusters, err := processListAllClustersResults(ctx, tc.items)

			assert.NoError(t, err)
			assert.Equal(t, tc.expectedClusters, clusters)
		})
	}
}

func TestFindHCPInfoByPredicate(t *testing.T) {
	testCases := []struct {
		name            string
		items           []hypershiftv1beta1.HostedCluster
		predicate       func(HCPInfo) bool
		expectedCluster HCPInfo
		expectedError   string
	}{
		{
			name: "finds matching cluster",
			items: []hypershiftv1beta1.HostedCluster{
				*createHostedCluster("test-cluster-1", "base-ns", "cluster-id-1", "sub-123", "rg-test"),
				*createHostedCluster("test-cluster-2", "base-ns", "cluster-id-2", "sub-456", "rg-test"),
			},
			predicate: func(hcpInfo HCPInfo) bool {
				return hcpInfo.ID == "cluster-id-2"
			},
			expectedCluster: HCPInfo{
				ID:                "cluster-id-2",
				Name:              "test-cluster-2",
				Namespace:         "base-ns-test-cluster-2",
				SubscriptionID:    "sub-456",
				ResourceGroupName: "rg-test",
				ResourceID:        "/subscriptions/sub-456/resourceGroups/rg-test/providers/Microsoft.RedHatOpenshift/hcpOpenShiftClusters/test-cluster-2",
			},
		},
		{
			name:  "empty items returns error",
			items: []hypershiftv1beta1.HostedCluster{},
			predicate: func(hcpInfo HCPInfo) bool {
				return true
			},
			expectedError: "no cluster found matching the criteria",
		},
		{
			name: "no matching clusters returns error",
			items: []hypershiftv1beta1.HostedCluster{
				*createHostedCluster("test-cluster", "base-ns", "cluster-id", "sub-123", "rg-test"),
			},
			predicate: func(hcpInfo HCPInfo) bool {
				return hcpInfo.ID == "non-existent-id"
			},
			expectedError: "no cluster found matching the criteria",
		},
		{
			name: "multiple matching clusters returns error",
			items: []hypershiftv1beta1.HostedCluster{
				*createHostedCluster("test-cluster-1", "ns-1", "id-1", "sub-123", "rg-test"),
				*createHostedCluster("test-cluster-2", "ns-2", "id-2", "sub-123", "rg-test"),
			},
			predicate: func(hcpInfo HCPInfo) bool {
				return hcpInfo.SubscriptionID == "sub-123"
			},
			expectedError: "multiple clusters found matching the criteria",
		},
		{
			name: "skips clusters with missing required fields",
			items: []hypershiftv1beta1.HostedCluster{
				*createHostedCluster("test-cluster-1", "base-ns", "", "sub-123", "rg-test"), // Missing cluster ID
				*createHostedCluster("test-cluster-2", "base-ns", "cluster-id-2", "sub-456", "rg-test"),
			},
			predicate: func(hcpInfo HCPInfo) bool {
				return hcpInfo.SubscriptionID == "sub-456"
			},
			expectedCluster: HCPInfo{
				ID:                "cluster-id-2",
				Name:              "test-cluster-2",
				Namespace:         "base-ns-test-cluster-2",
				SubscriptionID:    "sub-456",
				ResourceGroupName: "rg-test",
				ResourceID:        "/subscriptions/sub-456/resourceGroups/rg-test/providers/Microsoft.RedHatOpenshift/hcpOpenShiftClusters/test-cluster-2",
			},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			ctx := context.Background()
			clusterInfo, err := findHCPInfoByPredicate(ctx, tc.items, tc.predicate)

			if tc.expectedError != "" {
				assert.Error(t, err)
				assert.Contains(t, err.Error(), tc.expectedError)
				assert.Equal(t, HCPInfo{}, clusterInfo)
			} else {
				assert.NoError(t, err)
				assert.Equal(t, tc.expectedCluster, clusterInfo)
			}
		})
	}
}

func TestHostedClusterToHCPInfo(t *testing.T) {
	testCases := []struct {
		name            string
		hostedCluster   *hypershiftv1beta1.HostedCluster
		expectedCluster HCPInfo
		expectedError   string
	}{
		{
			name:          "extracts all fields correctly",
			hostedCluster: createHostedCluster("test-cluster", "test-namespace", "cluster-id", "sub-123", "rg-test"),
			expectedCluster: HCPInfo{
				ID:                "cluster-id",
				Name:              "test-cluster",
				Namespace:         "test-namespace-test-cluster",
				SubscriptionID:    "sub-123",
				ResourceGroupName: "rg-test",
				ResourceID:        "/subscriptions/sub-123/resourceGroups/rg-test/providers/Microsoft.RedHatOpenshift/hcpOpenShiftClusters/test-cluster",
			},
		},
		{
			name:          "missing cluster ID returns error",
			hostedCluster: createHostedCluster("test-cluster", "test-namespace", "", "sub-123", "rg-test"),
			expectedError: "object test-namespace/test-cluster is missing required label 'api.openshift.com/id'",
		},
		{
			name:          "missing subscription ID returns error",
			hostedCluster: createHostedCluster("test-cluster", "test-namespace", "cluster-id", "", "rg-test"),
			expectedError: "failed to construct Azure resource ID: subscription ID cannot be empty",
		},
		{
			name:          "missing resource group returns error",
			hostedCluster: createHostedCluster("test-cluster", "test-namespace", "cluster-id", "sub-123", ""),
			expectedError: "failed to construct Azure resource ID: resource group cannot be empty",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			clusterInfo, err := hostedClusterToHCPInfo(tc.hostedCluster)

			if tc.expectedError != "" {
				assert.Error(t, err)
				assert.Contains(t, err.Error(), tc.expectedError)
				assert.Equal(t, HCPInfo{}, clusterInfo)
			} else {
				assert.NoError(t, err)
				assert.Equal(t, tc.expectedCluster, clusterInfo)
			}
		})
	}
}

func TestCreateHostedClusterValidation(t *testing.T) {
	testCases := []struct {
		name                 string
		createFunc           func()
		expectedPanicMessage string
	}{
		{
			name: "createHostedCluster panics on empty name",
			createFunc: func() {
				createHostedCluster("", "namespace", "id", "sub", "rg")
			},
			expectedPanicMessage: "createHostedCluster: name is required",
		},
		{
			name: "createHostedCluster panics on empty namespace",
			createFunc: func() {
				createHostedCluster("name", "", "id", "sub", "rg")
			},
			expectedPanicMessage: "createHostedCluster: namespace is required",
		},
		{
			name: "createBasicHostedCluster panics on empty name",
			createFunc: func() {
				createBasicHostedCluster("", "namespace", "id")
			},
			expectedPanicMessage: "createBasicHostedCluster: name is required",
		},
		{
			name: "createBasicHostedCluster panics on empty namespace",
			createFunc: func() {
				createBasicHostedCluster("name", "", "id")
			},
			expectedPanicMessage: "createBasicHostedCluster: namespace is required",
		},
		{
			name: "createBasicHostedCluster panics on empty clusterID",
			createFunc: func() {
				createBasicHostedCluster("name", "namespace", "")
			},
			expectedPanicMessage: "createBasicHostedCluster: clusterID is required",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			assert.PanicsWithValue(t, tc.expectedPanicMessage, tc.createFunc)
		})
	}
}
