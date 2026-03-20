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

	"github.com/Azure/ARO-HCP/tooling/hcpctl/internal/testutil"
)

type queryFixture struct {
	Name      string `json:"name"`
	Database  string `json:"database"`
	Unlimited bool   `json:"unlimited"`
	KQL       string `json:"kql"`
}

func queryToFixture(q Query) queryFixture {
	return queryFixture{
		Name:      q.GetName(),
		Database:  q.GetDatabase(),
		Unlimited: q.IsUnlimited(),
		KQL:       q.GetQuery().String(),
	}
}

func queriesToFixtures(queries []Query) []queryFixture {
	fixtures := make([]queryFixture, len(queries))
	for i, q := range queries {
		fixtures[i] = queryToFixture(q)
	}
	return fixtures
}

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

	testutil.CompareWithFixture(t, queryToFixture(queries[0]))
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

	testutil.CompareWithFixture(t, queryToFixture(queries[0]))
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

	testutil.CompareWithFixture(t, queryToFixture(queries[0]))
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

	testutil.CompareWithFixture(t, queryToFixture(queries[0]))
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

	testutil.CompareWithFixture(t, queryToFixture(queries[0]))
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

	testutil.CompareWithFixture(t, queryToFixture(queries[0]))
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

	testutil.CompareWithFixture(t, queryToFixture(queries[0]))
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

	testutil.CompareWithFixture(t, queryToFixture(queries[0]))
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

	testutil.CompareWithFixture(t, queryToFixture(queries[0]))
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

	testutil.CompareWithFixture(t, queryToFixture(queries[0]))
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

	testutil.CompareWithFixture(t, queryToFixture(queries[0]))
}

func TestBuildMerged_SingleTemplate(t *testing.T) {
	f, err := NewQueryFactory()
	require.NoError(t, err)
	opts := baseOptions()
	def, err := f.GetBuiltinQueryDefinition("kubernetesEvents")
	require.NoError(t, err)
	q, err := f.BuildMerged(*def, NewTemplateDataFromOptions(opts))
	require.NoError(t, err)

	testutil.CompareWithFixture(t, queryToFixture(q))
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

	var detailedDef *QueryDefinition
	for _, def := range f.customQueryDefinitions {
		if def.Name == "detailedServiceLogs" {
			detailedDef = &def
			break
		}
	}
	require.NotEmpty(t, detailedDef.Name, "detailedServiceLogs custom query not found")
	require.NotEmpty(t, detailedDef.Children, "detailedServiceLogs should have children")

	q, err := f.BuildMerged(*detailedDef, NewTemplateDataFromOptions(opts))
	require.NoError(t, err)

	testutil.CompareWithFixture(t, queryToFixture(q))
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

	testutil.CompareWithFixture(t, td)
}

func TestTemplateDataOptions_NoTruncation(t *testing.T) {
	opts := QueryOptions{Limit: -1}
	td := NewTemplateDataFromOptions(opts)

	testutil.CompareWithFixture(t, td)
}

func TestQueryDefinitions_TemplatePathsExist(t *testing.T) {
	f, err := NewQueryFactory()
	require.NoError(t, err)
	allDefs := make([]QueryDefinition, 0, len(f.builtinQueryDefinitions)+len(f.customQueryDefinitions))
	allDefs = append(allDefs, f.builtinQueryDefinitions...)
	allDefs = append(allDefs, f.customQueryDefinitions...)

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
	for _, def := range f.builtinQueryDefinitions {
		if def.TemplatePath != "" {
			referenced[def.TemplatePath] = true
		}
		for _, child := range def.Children {
			referenced[child.TemplatePath] = true
		}
	}
	for _, def := range f.customQueryDefinitions {
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
	for _, def := range f.builtinQueryDefinitions {
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
	for _, def := range f.customQueryDefinitions {
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
