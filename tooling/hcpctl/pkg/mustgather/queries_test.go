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

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/Azure/ARO-HCP/tooling/hcpctl/pkg/kusto"
)

func TestGetServicesQueries(t *testing.T) {
	factory := kusto.NewQueryFactory()
	opts := kusto.QueryOptions{
		SubscriptionId:    "test-sub",
		ResourceGroupName: "test-rg",
	}

	queries, err := serviceLogs(factory, opts, []string{"cluster1"})
	require.NoError(t, err)
	assert.Len(t, queries, 4)
}

func TestGetHostedControlPlaneLogsQuery(t *testing.T) {
	factory := kusto.NewQueryFactory()
	opts := kusto.QueryOptions{}

	// With cluster IDs
	queries, err := hostedControlPlaneLogs(factory, opts, []string{"cluster1", "cluster2"})
	require.NoError(t, err)
	assert.Len(t, queries, 2)

	// Empty cluster IDs
	queries, err = hostedControlPlaneLogs(factory, opts, []string{})
	require.NoError(t, err)
	assert.Len(t, queries, 0)
}

func TestGetClusterIdQuery(t *testing.T) {
	factory := kusto.NewQueryFactory()
	opts := kusto.QueryOptions{
		SubscriptionId:    "test-sub",
		ResourceGroupName: "test-rg",
	}

	clusterIdDef, err := factory.GetBuiltinQueryDefinition("clusterId")
	require.NoError(t, err)
	queries, err := factory.Build(*clusterIdDef, kusto.NewTemplateDataFromOptions(opts))
	require.NoError(t, err)
	assert.Len(t, queries, 1)
}
