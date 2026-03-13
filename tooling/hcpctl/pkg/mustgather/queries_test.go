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

// func TestGetServicesQueries(t *testing.T) {
// 	opts := &kusto.QueryOptions{
// 		SubscriptionId:    "test-sub",
// 		ResourceGroupName: "test-rg",
// 	}

// 	queries, err := kusto.NewQueryFactory(opts, true).ServiceLogs()
// 	require.NoError(t, err)
// 	assert.Len(t, queries, 4)
// 	for _, query := range queries {
// 		assert.NotNil(t, query)
// 		assert.Equal(t, kusto.ServicesDatabase, query.GetDatabase())
// 	}
// }

// func TestGetHostedControlPlaneLogsQuery(t *testing.T) {
// 	// With cluster IDs
// 	opts := &kusto.QueryOptions{ClusterIds: []string{"cluster1", "cluster2"}}
// 	queries, err := kusto.NewQueryFactory(opts, true).HostedControlPlaneLogs()
// 	require.NoError(t, err)
// 	assert.Len(t, queries, 2)

// 	// Empty cluster IDs
// 	opts = &kusto.QueryOptions{ClusterIds: []string{}}
// 	queries, err = kusto.NewQueryFactory(opts, true).HostedControlPlaneLogs()
// 	require.NoError(t, err)
// 	assert.Len(t, queries, 0)
// }

// func TestGetClusterIdQuery(t *testing.T) {
// 	opts := &kusto.QueryOptions{
// 		SubscriptionId:    "test-sub",
// 		ResourceGroupName: "test-rg",
// 	}

// 	query, err := kusto.NewQueryFactory(opts, true).ClusterIdQuery()
// 	require.NoError(t, err)
// 	assert.NotNil(t, query)
// }
