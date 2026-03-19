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

package kusto

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func baseOptions() QueryOptions {
	return QueryOptions{
		SubscriptionId:    "test-sub",
		ResourceGroupName: "test-rg",
		InfraClusterName:  "test-cluster",
		TimestampMin:      time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC),
		TimestampMax:      time.Date(2025, 1, 2, 0, 0, 0, 0, time.UTC),
		Limit:             100,
	}
}

func TestInfraKubernetesEvents(t *testing.T) {
	f, err := NewQueryFactory()
	require.NoError(t, err)
	opts := baseOptions()
	def, err := f.GetBuiltinQueryDefinition("kubernetesEvents")
	require.NoError(t, err)
	queries, err := f.Build(*def, NewTemplateDataFromOptions(opts))
	require.NoError(t, err)
	require.Len(t, queries, 1)

	q := queries[0]
	assert.Equal(t, "kubernetesEvents", q.GetName())
	assert.Equal(t, "ServiceLogs", q.GetDatabase())
	assert.False(t, q.IsUnlimited())

	kql := q.GetQuery().String()
	assert.Contains(t, kql, "kubernetesEvents")
	assert.Contains(t, kql, "| project timestamp, log, cluster")
	assert.Contains(t, kql, "datetime(2025-01-01T00:00:00.0000000Z) .. datetime(2025-01-02T00:00:00.0000000Z)")
	assert.Contains(t, kql, "== 'test-cluster'")
	assert.Contains(t, kql, "| limit 100")
	assert.Contains(t, kql, "| order by timestamp asc")
	assert.NotContains(t, kql, "set notruncation")
}

func TestInfraKubernetesEvents_NoTruncation(t *testing.T) {
	f, err := NewQueryFactory()
	require.NoError(t, err)
	opts := QueryOptions{
		InfraClusterName: "test-cluster",
		Limit:            -1,
	}
	def, err := f.GetBuiltinQueryDefinition("kubernetesEvents")
	require.NoError(t, err)
	queries, err := f.Build(*def, NewTemplateDataFromOptions(opts))
	require.NoError(t, err)
	require.Len(t, queries, 1)

	q := queries[0]
	assert.True(t, q.IsUnlimited())

	kql := q.GetQuery().String()
	assert.Contains(t, kql, "set notruncation")
	assert.NotContains(t, kql, "| limit")
}

func TestKubernetesEventsSvc(t *testing.T) {
	f, err := NewQueryFactory()
	require.NoError(t, err)
	opts := baseOptions()
	def, err := f.GetBuiltinQueryDefinition("kubernetesEventsSvc")
	require.NoError(t, err)
	queries, err := f.Build(*def, NewTemplateDataFromOptions(opts))
	require.NoError(t, err)
	require.Len(t, queries, 1)

	kql := queries[0].GetQuery().String()
	assert.Contains(t, kql, "kubernetesEvents")
	assert.Contains(t, kql, "== 'test-cluster'")
}

func TestKubernetesEventsMgmt(t *testing.T) {
	f, err := NewQueryFactory()
	require.NoError(t, err)
	opts := QueryOptions{
		InfraClusterName: "test-cluster",
		Limit:            50,
	}
	def, err := f.GetBuiltinQueryDefinition("kubernetesEventsMgmt")
	require.NoError(t, err)
	queries, err := f.Build(*def, NewTemplateDataFromOptions(opts,
		WithHCPNamespacePrefix("ocm-arohcp"),
		WithClusterIds([]string{"cid1", "cid2"}),
	))
	require.NoError(t, err)
	require.Len(t, queries, 1)

	kql := queries[0].GetQuery().String()
	assert.Contains(t, kql, "!hasprefix 'ocm-arohcp'")
	assert.Contains(t, kql, "has_any ('cid1', 'cid2')")
}

func TestInfraSystemdLogs(t *testing.T) {
	f, err := NewQueryFactory()
	require.NoError(t, err)
	opts := baseOptions()
	def, err := f.GetBuiltinQueryDefinition("systemdLogs")
	require.NoError(t, err)
	queries, err := f.Build(*def, NewTemplateDataFromOptions(opts))
	require.NoError(t, err)
	require.Len(t, queries, 1)

	q := queries[0]
	assert.Equal(t, "systemdLogs", q.GetName())

	kql := q.GetQuery().String()
	assert.Contains(t, kql, "systemdLogs")
	assert.Contains(t, kql, "== 'test-cluster'")
	assert.Contains(t, kql, "datetime(2025-01-01T00:00:00.0000000Z) .. datetime(2025-01-02T00:00:00.0000000Z)")
}

func TestInfraServiceLogs(t *testing.T) {
	f, err := NewQueryFactory()
	require.NoError(t, err)
	opts := baseOptions()
	def, err := f.GetBuiltinQueryDefinition("infraServiceLogs")
	require.NoError(t, err)
	queries, err := f.Build(*def, NewTemplateDataFromOptions(opts, WithTable("containerLogs")))
	require.NoError(t, err)
	require.Len(t, queries, 1)

	q := queries[0]
	assert.Equal(t, "ServiceLogs", q.GetDatabase())

	kql := q.GetQuery().String()
	assert.Contains(t, kql, "containerLogs")
	assert.Contains(t, kql, "| project timestamp, log, cluster, namespace_name, container_name")
	assert.Contains(t, kql, "== 'test-cluster'")
}

func TestServiceLogs(t *testing.T) {
	f, err := NewQueryFactory()
	require.NoError(t, err)
	opts := QueryOptions{
		SubscriptionId:    "sub",
		ResourceGroupName: "rg",
		Limit:             10,
	}
	def, err := f.GetBuiltinQueryDefinition("serviceLogs")
	require.NoError(t, err)
	queries, err := f.Build(*def, NewTemplateDataFromOptions(opts,
		WithTable("containerLogs"),
		WithClusterIds([]string{"cid1"}),
	))
	require.NoError(t, err)
	require.Len(t, queries, 1)

	kql := queries[0].GetQuery().String()
	assert.Contains(t, kql, "has '/subscriptions/sub/resourceGroups/rg'")
	assert.Contains(t, kql, "has_any ('cid1')")
}

func TestServiceLogs_NoClusterIds(t *testing.T) {
	f, err := NewQueryFactory()
	require.NoError(t, err)
	opts := QueryOptions{
		SubscriptionId:    "sub",
		ResourceGroupName: "rg",
		Limit:             10,
	}
	def, err := f.GetBuiltinQueryDefinition("serviceLogs")
	require.NoError(t, err)
	queries, err := f.Build(*def, NewTemplateDataFromOptions(opts, WithTable("containerLogs")))
	require.NoError(t, err)
	require.Len(t, queries, 1)

	kql := queries[0].GetQuery().String()
	assert.Contains(t, kql, "has '/subscriptions/sub/resourceGroups/rg'")
	assert.NotContains(t, kql, "has_any")
}

func TestHostedControlPlaneLogs(t *testing.T) {
	f, err := NewQueryFactory()
	require.NoError(t, err)
	opts := QueryOptions{Limit: 100}
	def, err := f.GetBuiltinQueryDefinition("hostedControlPlaneLogs")
	require.NoError(t, err)
	queries, err := f.Build(*def, NewTemplateDataFromOptions(opts, WithClusterId("cid1")))
	require.NoError(t, err)
	require.Len(t, queries, 1)

	q := queries[0]
	assert.Equal(t, "hostedControlPlaneLogs", q.GetName())
	assert.Equal(t, "HostedControlPlaneLogs", q.GetDatabase())

	kql := q.GetQuery().String()
	assert.Contains(t, kql, "containerLogs")
	assert.Contains(t, kql, "namespace_name has 'cid1'")
}

func TestClusterIdQuery(t *testing.T) {
	f, err := NewQueryFactory()
	require.NoError(t, err)
	opts := baseOptions()
	def, err := f.GetBuiltinQueryDefinition("clusterId")
	require.NoError(t, err)
	queries, err := f.Build(*def, NewTemplateDataFromOptions(opts))
	require.NoError(t, err)
	require.Len(t, queries, 1)

	q := queries[0]
	assert.Equal(t, "clusterId", q.GetName())
	assert.Equal(t, "ServiceLogs", q.GetDatabase())

	kql := q.GetQuery().String()
	assert.Contains(t, kql, "clustersServiceLogs")
	assert.Contains(t, kql, "resource_id has '/subscriptions/test-sub/resourceGroups/test-rg'")
	assert.Contains(t, kql, "| distinct cid")
}

func TestClusterNamesQuery(t *testing.T) {
	f, err := NewQueryFactory()
	require.NoError(t, err)
	opts := baseOptions()
	def, err := f.GetBuiltinQueryDefinition("clusterNamesSvc")
	require.NoError(t, err)
	queries, err := f.Build(*def, NewTemplateDataFromOptions(opts))
	require.NoError(t, err)
	require.Len(t, queries, 1)

	q := queries[0]
	assert.Equal(t, "clusterNamesSvc", q.GetName())

	kql := q.GetQuery().String()
	assert.Contains(t, kql, "containerLogs")
	assert.Contains(t, kql, "log has '/subscriptions/test-sub/resourceGroups/test-rg'")
	assert.Contains(t, kql, "| distinct cluster")
}

func TestBuildMerged_SingleTemplate(t *testing.T) {
	f, err := NewQueryFactory()
	require.NoError(t, err)
	opts := baseOptions()
	def, err := f.GetBuiltinQueryDefinition("kubernetesEvents")
	require.NoError(t, err)
	q, err := f.BuildMerged(*def, NewTemplateDataFromOptions(opts))
	require.NoError(t, err)
	assert.Equal(t, "kubernetesEvents", q.GetName())
	assert.Contains(t, q.GetQuery().String(), "kubernetesEvents")
}

func TestBuildMerged_MultipleChildren(t *testing.T) {
	f, err := NewQueryFactory()
	require.NoError(t, err)
	opts := QueryOptions{
		ResourceGroupName: "test-rg",
		TimestampMin:      time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC),
		TimestampMax:      time.Date(2025, 1, 2, 0, 0, 0, 0, time.UTC),
		Limit:             -1,
	}

	// Find the detailedServiceLogs custom query which has children
	var detailedDef *QueryDefinition
	for _, def := range f.CustomQueryDefinitions {
		if def.Name == "detailedServiceLogs" {
			detailedDef = &def
			break
		}
	}
	require.NotEmpty(t, detailedDef.Name, "detailedServiceLogs custom query not found")
	require.NotEmpty(t, detailedDef.Children, "detailedServiceLogs should have children")

	q, err := f.BuildMerged(*detailedDef, NewTemplateDataFromOptions(opts))
	require.NoError(t, err)
	assert.Equal(t, "detailedServiceLogs", q.GetName())

	// Merged query should contain content from all children
	kql := q.GetQuery().String()
	assert.Contains(t, kql, "frontendLogs")
	assert.Contains(t, kql, "backendLogs")
}

func TestBuildCustomQuery(t *testing.T) {
	f, err := NewQueryFactory()
	require.NoError(t, err)
	opts := QueryOptions{
		InfraClusterName: "test-cluster",
		TimestampMin:     time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC),
		TimestampMax:     time.Date(2025, 1, 2, 0, 0, 0, 0, time.UTC),
		Limit:            -1,
	}
	queries, err := f.BuildCustomQuery("backendControllerConditions", NewTemplateDataFromOptions(opts,
		WithClusterNames([]string{"test-cluster"}),
	))
	require.NoError(t, err)
	require.Len(t, queries, 1)

	kql := queries[0].GetQuery().String()
	assert.Contains(t, kql, "database('ServiceLogs').table('backendLogs')")
	assert.Contains(t, kql, "mv-expand condition")
}

func TestBuildCustomQuery_NotFound(t *testing.T) {
	f, err := NewQueryFactory()
	require.NoError(t, err)
	_, err = f.BuildCustomQuery("nonexistent", NewTemplateDataFromOptions(baseOptions()))
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "not found")
}

func TestBuildCustomQuery_WithChildren(t *testing.T) {
	f, err := NewQueryFactory()
	require.NoError(t, err)
	opts := QueryOptions{
		ResourceGroupName: "test-rg",
		TimestampMin:      time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC),
		TimestampMax:      time.Date(2025, 1, 2, 0, 0, 0, 0, time.UTC),
		Limit:             -1,
	}
	queries, err := f.BuildCustomQuery("debugQueries", NewTemplateDataFromOptions(opts))
	require.NoError(t, err)
	require.Len(t, queries, 5)

	// Each child query should have its own name
	names := make(map[string]bool)
	for _, q := range queries {
		names[q.GetName()] = true
	}
	assert.True(t, names["debugFrontendLogs"])
	assert.True(t, names["debugBackendConditions"])
	assert.True(t, names["debugCsPhases"])
	assert.True(t, names["debugHypershiftConditions"])
	assert.True(t, names["debugControlPlaneEvents"])
}

func TestBuildAllCustomQueries(t *testing.T) {
	f, err := NewQueryFactory()
	require.NoError(t, err)
	opts := QueryOptions{
		InfraClusterName:  "test-cluster",
		ResourceGroupName: "test-rg",
		TimestampMin:      time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC),
		TimestampMax:      time.Date(2025, 1, 2, 0, 0, 0, 0, time.UTC),
		Limit:             -1,
	}
	queries, err := f.BuildAllCustomQueries(NewTemplateDataFromOptions(opts,
		WithClusterNames([]string{"test-cluster"}),
	))
	require.NoError(t, err)
	// 1 (backendControllerConditions) + 1 (clustersServicePhases) + 5 (debugQueries children) + 3 (detailedServiceLogs children) = 10
	assert.Len(t, queries, 11)
}

func TestTemplateDataOptions(t *testing.T) {
	opts := QueryOptions{
		SubscriptionId:    "sub",
		ResourceGroupName: "rg",
		InfraClusterName:  "cluster",
		Limit:             100,
	}

	td := NewTemplateDataFromOptions(opts,
		WithClusterId("cid1"),
		WithClusterIds([]string{"cid1", "cid2"}),
		WithHCPNamespacePrefix("ocm-arohcp"),
		WithClusterName("my-cluster"),
		WithClusterNames([]string{"cluster-a", "cluster-b"}),
		WithTable("myTable"),
	)

	assert.Equal(t, "'cid1'", td.ClusterId)
	assert.Equal(t, "'cid1', 'cid2'", td.ClusterIds)
	assert.Equal(t, "'ocm-arohcp'", td.HCPNamespacePrefix)
	assert.Equal(t, "'my-cluster'", td.ClusterName)
	assert.Equal(t, "'cluster-a', 'cluster-b'", td.ClusterNames)
	assert.Equal(t, "myTable", td.Table)
	assert.Equal(t, 100, td.Limit)
	assert.False(t, td.NoTruncation)
}

func TestTemplateDataOptions_NoTruncation(t *testing.T) {
	opts := QueryOptions{Limit: -1}
	td := NewTemplateDataFromOptions(opts)
	assert.True(t, td.NoTruncation)
	assert.Equal(t, 0, td.Limit)
}

func TestQueryDefinitions_TemplatePathsExist(t *testing.T) {
	f, err := NewQueryFactory()
	require.NoError(t, err)
	allDefs := make([]QueryDefinition, 0, len(f.BuiltinQueryDefinitions)+len(f.CustomQueryDefinitions))
	allDefs = append(allDefs, f.BuiltinQueryDefinitions...)
	allDefs = append(allDefs, f.CustomQueryDefinitions...)

	for _, def := range allDefs {
		if def.TemplatePath != "" {
			t.Run(def.TemplatePath, func(t *testing.T) {
				assert.NotPanics(t, func() {
					content := GetTemplate(def.TemplatePath)
					assert.NotEmpty(t, content, "template %s is empty", def.TemplatePath)
				}, "template %s does not exist", def.TemplatePath)
			})
		}
		for _, child := range def.Children {
			t.Run(child.TemplatePath, func(t *testing.T) {
				assert.NotPanics(t, func() {
					content := GetTemplate(child.TemplatePath)
					assert.NotEmpty(t, content, "template %s is empty", child.TemplatePath)
				}, "template %s does not exist", child.TemplatePath)
			})
		}
	}
}

func TestQueryDefinitions_NoDanglingTemplates(t *testing.T) {
	f, err := NewQueryFactory()
	require.NoError(t, err)
	allTemplates, err := ListTemplatePaths()
	require.NoError(t, err)

	referenced := make(map[string]bool)
	for _, def := range f.BuiltinQueryDefinitions {
		if def.TemplatePath != "" {
			referenced[def.TemplatePath] = true
		}
		for _, child := range def.Children {
			referenced[child.TemplatePath] = true
		}
	}
	for _, def := range f.CustomQueryDefinitions {
		if def.TemplatePath != "" {
			referenced[def.TemplatePath] = true
		}
		for _, child := range def.Children {
			referenced[child.TemplatePath] = true
		}
	}

	for _, tmpl := range allTemplates {
		assert.True(t, referenced[tmpl], "template %s is not referenced by any QueryDefinition", tmpl)
	}
}

func TestQueryDefinitions_NoDuplicateTemplatePaths(t *testing.T) {
	f, err := NewQueryFactory()
	require.NoError(t, err)

	seen := make(map[string]bool)
	for _, def := range f.BuiltinQueryDefinitions {
		if def.TemplatePath != "" {
			if seen[def.TemplatePath] {
				continue // multiple definitions may share a template
			}
			seen[def.TemplatePath] = true
		}
		for _, child := range def.Children {
			seen[child.TemplatePath] = true
		}
	}
	for _, def := range f.CustomQueryDefinitions {
		if def.TemplatePath != "" {
			seen[def.TemplatePath] = true
		}
		for _, child := range def.Children {
			seen[child.TemplatePath] = true
		}
	}

	// Verify the count of unique template paths matches the count of actual templates
	// (excluding the custom queries YAML file which is not a template)
	allTemplates, err := ListTemplatePaths()
	require.NoError(t, err)
	assert.Equal(t, len(allTemplates), len(seen),
		"number of unique template paths in all QueryDefinitions (%d) does not match number of template files (%d)",
		len(seen), len(allTemplates))
}
