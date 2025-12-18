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

package mustgather

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestParseResourceId(t *testing.T) {
	// Valid case
	subscriptionId, resourceGroupName, err := parseResourceId("/subscriptions/test-sub/resourceGroups/test-rg")
	assert.NoError(t, err)
	assert.Equal(t, "test-sub", subscriptionId)
	assert.Equal(t, "test-rg", resourceGroupName)

	// Invalid case
	_, _, err = parseResourceId("/invalid")
	assert.Error(t, err)
}

func TestNewQueryOptions(t *testing.T) {
	now := time.Now()

	// With resource ID
	opts, err := NewQueryOptions("", "", "/subscriptions/test-sub/resourceGroups/test-rg", now, now, 100)
	require.NoError(t, err)
	assert.Equal(t, "test-sub", opts.SubscriptionId)
	assert.Equal(t, "test-rg", opts.ResourceGroupName)

	// With subscription/resource group
	opts, err = NewQueryOptions("sub", "rg", "", now, now, 100)
	require.NoError(t, err)
	assert.Equal(t, "sub", opts.SubscriptionId)
	assert.Equal(t, "rg", opts.ResourceGroupName)

	// Invalid resource ID
	_, err = NewQueryOptions("", "", "/invalid", now, now, 100)
	assert.Error(t, err)
}

func TestGetTimeMinMax(t *testing.T) {
	now := time.Now()

	// Both provided - should pass through
	min, max := getTimeMinMax(now.Add(-1*time.Hour), now)
	assert.Equal(t, now.Add(-1*time.Hour), min)
	assert.Equal(t, now, max)

	// Zero timestamps - should get defaults
	min, max = getTimeMinMax(time.Time{}, time.Time{})
	assert.WithinDuration(t, now.Add(-24*time.Hour), min, time.Minute)
	assert.WithinDuration(t, now, max, time.Minute)
}

func TestQueryOptions_GetServicesQueries(t *testing.T) {
	opts := &QueryOptions{
		SubscriptionId:    "test-sub",
		ResourceGroupName: "test-rg",
	}

	queries := opts.GetServicesQueries()
	assert.Len(t, queries, 3) // Should match servicesTables length
	for _, query := range queries {
		assert.NotNil(t, query)
		assert.Equal(t, servicesDatabase, query.Database)
	}
}

func TestQueryOptions_GetHostedControlPlaneLogsQuery(t *testing.T) {
	// With cluster IDs
	opts := &QueryOptions{ClusterIds: []string{"cluster1", "cluster2"}}
	queries := opts.GetHostedControlPlaneLogsQuery()
	assert.Len(t, queries, 2)

	// Empty cluster IDs
	opts = &QueryOptions{ClusterIds: []string{}}
	queries = opts.GetHostedControlPlaneLogsQuery()
	assert.Len(t, queries, 0)
}

func TestQueryOptions_GetClusterIdQuery(t *testing.T) {
	opts := &QueryOptions{
		SubscriptionId:    "test-sub",
		ResourceGroupName: "test-rg",
	}

	query := opts.GetClusterIdQuery()
	assert.NotNil(t, query)
}
