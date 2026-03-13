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

// func baseFactory() *QueryFactory {
// 	return NewQueryFactory(&QueryOptions{
// 		SubscriptionId:    "test-sub",
// 		ResourceGroupName: "test-rg",
// 		InfraClusterName:  "test-cluster",
// 		TimestampMin:      time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC),
// 		TimestampMax:      time.Date(2025, 1, 2, 0, 0, 0, 0, time.UTC),
// 		Limit:             100,
// 	}, true)
// }

// func TestInfraKubernetesEvents(t *testing.T) {
// 	f := baseFactory()
// 	queries, err := f.InfraKubernetesEvents()
// 	require.NoError(t, err)
// 	require.Len(t, queries, 1)

// 	q := queries[0]
// 	assert.Equal(t, "kubernetesEvents", q.GetName())
// 	assert.Equal(t, ServicesDatabase, q.GetDatabase())
// 	assert.False(t, q.IsUnlimited())

// 	kql := q.GetQuery().String()
// 	assert.Contains(t, kql, "kubernetesEvents")
// 	assert.Contains(t, kql, "| project timestamp, log, cluster")
// 	assert.Contains(t, kql, "datetime(2025-01-01T00:00:00Z) .. datetime(2025-01-02T00:00:00Z)")
// 	assert.Contains(t, kql, "== 'test-cluster'")
// 	assert.Contains(t, kql, "| limit 100")
// 	assert.Contains(t, kql, "| order by timestamp asc")
// 	assert.NotContains(t, kql, "set notruncation")
// }

// func TestInfraKubernetesEvents_NoTruncation(t *testing.T) {
// 	f := NewQueryFactory(&QueryOptions{
// 		InfraClusterName: "test-cluster",
// 		Limit:            -1,
// 	}, true)
// 	queries, err := f.InfraKubernetesEvents()
// 	require.NoError(t, err)
// 	require.Len(t, queries, 1)

// 	q := queries[0]
// 	assert.True(t, q.IsUnlimited())

// 	kql := q.GetQuery().String()
// 	assert.Contains(t, kql, "set notruncation")
// 	assert.NotContains(t, kql, "| limit")
// }

// func TestKubernetesEventsSvc(t *testing.T) {
// 	f := baseFactory()
// 	queries, err := f.KubernetesEventsSvc()
// 	require.NoError(t, err)
// 	require.Len(t, queries, 1)

// 	kql := queries[0].GetQuery().String()
// 	assert.Contains(t, kql, "kubernetesEvents")
// 	assert.Contains(t, kql, "== 'test-cluster'")
// }

// func TestKubernetesEventsMgmt(t *testing.T) {
// 	f := NewQueryFactory(&QueryOptions{
// 		ClusterIds:       []string{"cid1", "cid2"},
// 		InfraClusterName: "test-cluster",
// 		Limit:            50,
// 	}, true)
// 	queries, err := f.KubernetesEventsMgmt()
// 	require.NoError(t, err)
// 	require.Len(t, queries, 1)

// 	kql := queries[0].GetQuery().String()
// 	assert.Contains(t, kql, "!hasprefix 'ocm-arohcp'")
// 	assert.Contains(t, kql, "has_any ('cid1', 'cid2')")
// }

// func TestKubernetesEventsMgmt_NoClusterIds(t *testing.T) {
// 	f := NewQueryFactory(&QueryOptions{
// 		ClusterIds: []string{},
// 	}, true)
// 	queries, err := f.KubernetesEventsMgmt()
// 	require.NoError(t, err)
// 	assert.Nil(t, queries)
// }

// func TestInfraSystemdLogs(t *testing.T) {
// 	f := baseFactory()
// 	queries, err := f.InfraSystemdLogs()
// 	require.NoError(t, err)
// 	require.Len(t, queries, 1)

// 	q := queries[0]
// 	assert.Equal(t, "systemdLogs", q.GetName())

// 	kql := q.GetQuery().String()
// 	assert.Contains(t, kql, "systemdLogs")
// 	assert.Contains(t, kql, "== 'test-cluster'")
// 	assert.Contains(t, kql, "datetime(2025-01-01T00:00:00Z) .. datetime(2025-01-02T00:00:00Z)")
// }

// func TestInfraServiceLogs(t *testing.T) {
// 	f := baseFactory()
// 	queries, err := f.InfraServiceLogs()
// 	require.NoError(t, err)
// 	assert.Len(t, queries, len(ServicesTables))

// 	for i, q := range queries {
// 		assert.Equal(t, ServicesTables[i], q.GetName())
// 		assert.Equal(t, ServicesDatabase, q.GetDatabase())

// 		kql := q.GetQuery().String()
// 		assert.Contains(t, kql, ServicesTables[i])
// 		assert.Contains(t, kql, "| project timestamp, log, cluster, namespace_name, container_name")
// 		assert.Contains(t, kql, "== 'test-cluster'")
// 	}
// }

// func TestServiceLogs(t *testing.T) {
// 	f := NewQueryFactory(&QueryOptions{
// 		SubscriptionId:    "sub",
// 		ResourceGroupName: "rg",
// 		ClusterIds:        []string{"cid1"},
// 		Limit:             10,
// 	}, true)
// 	queries, err := f.ServiceLogs()
// 	require.NoError(t, err)
// 	assert.Len(t, queries, len(ServicesTables))

// 	kql := queries[0].GetQuery().String()
// 	assert.Contains(t, kql, "log has '/subscriptions/sub/resourceGroups/rg'")
// 	assert.Contains(t, kql, "has_any ('cid1')")
// }

// func TestServiceLogs_NoClusterIds(t *testing.T) {
// 	f := NewQueryFactory(&QueryOptions{
// 		SubscriptionId:    "sub",
// 		ResourceGroupName: "rg",
// 		Limit:             10,
// 	}, true)
// 	queries, err := f.ServiceLogs()
// 	require.NoError(t, err)

// 	kql := queries[0].GetQuery().String()
// 	assert.Contains(t, kql, "log has '/subscriptions/sub/resourceGroups/rg'")
// 	assert.NotContains(t, kql, "has_any")
// }

// func TestHostedControlPlaneLogs(t *testing.T) {
// 	f := NewQueryFactory(&QueryOptions{
// 		ClusterIds: []string{"cid1", "cid2"},
// 		Limit:      100,
// 	}, true)
// 	queries, err := f.HostedControlPlaneLogs()
// 	require.NoError(t, err)
// 	assert.Len(t, queries, 2)

// 	for i, q := range queries {
// 		assert.Equal(t, "hostedControlPlaneLogs", q.GetName())
// 		assert.Equal(t, HostedControlPlaneLogsDatabase, q.GetDatabase())

// 		kql := q.GetQuery().String()
// 		assert.Contains(t, kql, ContainerLogsTable)
// 		assert.Contains(t, kql, fmt.Sprintf("namespace_name has '%s'", f.queryOptions.ClusterIds[i]))
// 	}
// }

// func TestHostedControlPlaneLogs_Empty(t *testing.T) {
// 	f := NewQueryFactory(&QueryOptions{
// 		ClusterIds: []string{},
// 	}, true)
// 	queries, err := f.HostedControlPlaneLogs()
// 	require.NoError(t, err)
// 	assert.Len(t, queries, 0)
// }

// func TestClusterIdQuery(t *testing.T) {
// 	f := baseFactory()
// 	q, err := f.ClusterIdQuery()
// 	require.NoError(t, err)
// 	assert.Equal(t, "Cluster ID", q.GetName())
// 	assert.Equal(t, ServicesDatabase, q.GetDatabase())
// 	assert.False(t, q.IsUnlimited())

// 	kql := q.GetQuery().String()
// 	assert.Contains(t, kql, ClustersServiceLogsTable)
// 	assert.Contains(t, kql, "resource_id has '/subscriptions/test-sub/resourceGroups/test-rg'")
// 	assert.Contains(t, kql, "| distinct cid")
// }

// func TestClusterNamesQueries(t *testing.T) {
// 	f := baseFactory()
// 	queries, err := f.ClusterNamesQueries()
// 	require.NoError(t, err)
// 	assert.Len(t, queries, 2)

// 	assert.Equal(t, ServicesDatabase, queries[0].GetDatabase())
// 	assert.Equal(t, HostedControlPlaneLogsDatabase, queries[1].GetDatabase())

// 	for _, q := range queries {
// 		kql := q.GetQuery().String()
// 		assert.Contains(t, kql, ContainerLogsTable)
// 		assert.Contains(t, kql, "log has '/subscriptions/test-sub/resourceGroups/test-rg'")
// 		assert.Contains(t, kql, "| distinct cluster")
// 	}
// }

// func TestSetClusterIds(t *testing.T) {
// 	f := baseFactory()
// 	f.SetClusterIds([]string{"new-id"})
// 	queries, err := f.KubernetesEventsMgmt()
// 	require.NoError(t, err)
// 	require.Len(t, queries, 1)

// 	kql := queries[0].GetQuery().String()
// 	assert.Contains(t, kql, "has_any ('new-id')")
// }

// func TestSetInfraClusterName(t *testing.T) {
// 	f := baseFactory()
// 	f.SetInfraClusterName("new-infra")
// 	queries, err := f.InfraKubernetesEvents()
// 	require.NoError(t, err)
// 	require.Len(t, queries, 1)

// 	kql := queries[0].GetQuery().String()
// 	assert.Contains(t, kql, "== 'new-infra'")
// }

// func safeFactory() *QueryFactory {
// 	return NewQueryFactory(&QueryOptions{
// 		SubscriptionId:    "test-sub",
// 		ResourceGroupName: "test-rg",
// 		InfraClusterName:  "test-cluster",
// 		TimestampMin:      time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC),
// 		TimestampMax:      time.Date(2025, 1, 2, 0, 0, 0, 0, time.UTC),
// 		Limit:             100,
// 	}, false)
// }

// func TestUnsafeMode_InfraKubernetesEvents(t *testing.T) {
// 	f := baseFactory()
// 	queries, err := f.InfraKubernetesEvents()
// 	require.NoError(t, err)
// 	require.Len(t, queries, 1)

// 	q := queries[0]
// 	assert.Nil(t, q.GetParameters())

// 	kql := q.GetQuery().String()
// 	assert.Contains(t, kql, "datetime(2025-01-01T00:00:00Z) .. datetime(2025-01-02T00:00:00Z)")
// 	assert.Contains(t, kql, "== 'test-cluster'")
// }

// func TestUnsafeMode_ServiceLogs(t *testing.T) {
// 	f := NewQueryFactory(&QueryOptions{
// 		SubscriptionId:    "sub",
// 		ResourceGroupName: "rg",
// 		ClusterIds:        []string{"cid1"},
// 		Limit:             10,
// 	}, true)
// 	queries, err := f.ServiceLogs()
// 	require.NoError(t, err)
// 	require.NotEmpty(t, queries)

// 	q := queries[0]
// 	assert.Nil(t, q.GetParameters())

// 	kql := q.GetQuery().String()
// 	assert.Contains(t, kql, "/subscriptions/sub/resourceGroups/rg")
// }

// func TestSafeMode_InfraKubernetesEvents(t *testing.T) {
// 	f := safeFactory()
// 	queries, err := f.InfraKubernetesEvents()
// 	require.NoError(t, err)
// 	require.Len(t, queries, 1)

// 	q := queries[0]
// 	assert.NotNil(t, q.GetParameters())

// 	kql := q.GetQuery().String()
// 	assert.Contains(t, kql, "timestampMin .. timestampMax")
// 	assert.NotContains(t, kql, "datetime(timestampMin)")
// 	assert.Contains(t, kql, "== 'clusterName'")
// 	assert.NotContains(t, kql, "2025-01-01T00:00:00Z")
// 	assert.NotContains(t, kql, "test-cluster")
// 	// Non-parameterized fields should still render actual values
// 	assert.Contains(t, kql, "| limit 100")
// }

// func TestSafeMode_ServiceLogs(t *testing.T) {
// 	f := NewQueryFactory(&QueryOptions{
// 		SubscriptionId:    "sub",
// 		ResourceGroupName: "rg",
// 		ClusterIds:        []string{"cid1"},
// 		Limit:             10,
// 	}, false)
// 	queries, err := f.ServiceLogs()
// 	require.NoError(t, err)
// 	require.NotEmpty(t, queries)

// 	q := queries[0]
// 	assert.NotNil(t, q.GetParameters())

// 	kql := q.GetQuery().String()
// 	assert.Contains(t, kql, "subResourceGroupId")
// 	assert.NotContains(t, kql, "/subscriptions/sub/resourceGroups/rg")
// }

// func TestSafeMode_HCPLogs(t *testing.T) {
// 	f := NewQueryFactory(&QueryOptions{
// 		ClusterIds: []string{"cid1"},
// 		Limit:      100,
// 	}, false)
// 	queries, err := f.HostedControlPlaneLogs()
// 	require.NoError(t, err)
// 	require.Len(t, queries, 1)

// 	q := queries[0]
// 	assert.NotNil(t, q.GetParameters())

// 	kql := q.GetQuery().String()
// 	assert.Contains(t, kql, "clusterId")
// 	assert.Contains(t, kql, "timestampMin .. timestampMax")
// 	assert.NotContains(t, kql, "datetime(timestampMin)")
// }

// func TestMergeProjectFields(t *testing.T) {
// 	assert.Equal(t, "timestamp, msg, log", mergeProjectFields([]string{"timestamp", "msg", "log"}, false))
// 	assert.Equal(t, "timestamp, log, cluster, namespace_name, container_name, msg",
// 		mergeProjectFields([]string{"timestamp", "msg", "log"}, true))
// 	assert.Equal(t, "timestamp, log, cluster, namespace_name, container_name",
// 		mergeProjectFields([]string{"timestamp", "log"}, true))
// }

// func componentFactory() *QueryFactory {
// 	return NewQueryFactory(&QueryOptions{
// 		InfraClusterName:  "test-cluster",
// 		ResourceGroupName: "test-rg",
// 		TimestampMin:      time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC),
// 		TimestampMax:      time.Date(2025, 1, 2, 0, 0, 0, 0, time.UTC),
// 		Limit:             -1,
// 	}, true)
// }

// func TestBackendLogs(t *testing.T) {
// 	f := componentFactory()
// 	q, err := f.BackendLogs()
// 	require.NoError(t, err)
// 	assert.Equal(t, "backendLogs", q.GetName())
// 	assert.Equal(t, ServicesDatabase, q.GetDatabase())

// 	kql := q.GetQuery().String()
// 	assert.Contains(t, kql, "database('ServiceLogs').table('backendLogs')")
// 	assert.Contains(t, kql, "== 'test-cluster'")
// 	assert.Contains(t, kql, "container_name == 'aro-hcp-backend'")
// 	assert.Contains(t, kql, "| project timestamp, msg, log")
// }

// func TestBackendLogs_MergedProject(t *testing.T) {
// 	f := componentFactory()
// 	f.MergeStandardProject = true
// 	q, err := f.BackendLogs()
// 	require.NoError(t, err)

// 	kql := q.GetQuery().String()
// 	assert.Contains(t, kql, "| project timestamp, log, cluster, namespace_name, container_name, msg")
// }

// func TestBackendControllerConditions(t *testing.T) {
// 	f := componentFactory()
// 	q, err := f.BackendControllerConditions()
// 	require.NoError(t, err)
// 	assert.Equal(t, "backendControllerConditions", q.GetName())

// 	kql := q.GetQuery().String()
// 	assert.Contains(t, kql, "database('ServiceLogs').table('backendLogs')")
// 	assert.Contains(t, kql, "hcpOpenShiftControllers")
// 	assert.Contains(t, kql, "mv-expand condition")
// }

// func TestFrontendLogs(t *testing.T) {
// 	f := componentFactory()
// 	q, err := f.FrontendLogs()
// 	require.NoError(t, err)

// 	kql := q.GetQuery().String()
// 	assert.Contains(t, kql, "database('ServiceLogs').table('frontendLogs')")
// 	assert.Contains(t, kql, "container_name == 'aro-hcp-frontend'")
// 	assert.Contains(t, kql, "| project timestamp, msg, log")
// }

// func TestClustersServiceLogs(t *testing.T) {
// 	f := componentFactory()
// 	q, err := f.ClustersServiceLogs()
// 	require.NoError(t, err)

// 	kql := q.GetQuery().String()
// 	assert.Contains(t, kql, "database('ServiceLogs').table('containerLogs')")
// 	assert.Contains(t, kql, "container_name == 'clusters-service-server'")
// 	assert.Contains(t, kql, "| project timestamp, log")
// }

// func TestClustersServicePhases(t *testing.T) {
// 	f := componentFactory()
// 	q, err := f.ClustersServicePhases()
// 	require.NoError(t, err)

// 	kql := q.GetQuery().String()
// 	assert.Contains(t, kql, "log has 'state to' or log has 'now in'")
// }

// func TestMaestroLogs(t *testing.T) {
// 	f := componentFactory()
// 	q, err := f.MaestroLogs()
// 	require.NoError(t, err)

// 	kql := q.GetQuery().String()
// 	assert.Contains(t, kql, "namespace_name has \"maestro\"")
// }

// func TestHypershiftLogs(t *testing.T) {
// 	f := componentFactory()
// 	q, err := f.HypershiftLogs()
// 	require.NoError(t, err)

// 	kql := q.GetQuery().String()
// 	assert.Contains(t, kql, "namespace_name has \"hypershift\"")
// }

// func TestACMLogs(t *testing.T) {
// 	f := componentFactory()
// 	q, err := f.ACMLogs()
// 	require.NoError(t, err)

// 	kql := q.GetQuery().String()
// 	assert.Contains(t, kql, "namespace_name has \"open-cluster-management-agent\"")
// }

// func TestHostedControlPlane(t *testing.T) {
// 	f := NewQueryFactory(&QueryOptions{
// 		ResourceGroupName: "test-rg",
// 		TimestampMin:      time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC),
// 		TimestampMax:      time.Date(2025, 1, 2, 0, 0, 0, 0, time.UTC),
// 		Limit:             -1,
// 	}, true)
// 	q, err := f.HostedControlPlane()
// 	require.NoError(t, err)

// 	kql := q.GetQuery().String()
// 	assert.Contains(t, kql, "let _resource_group = 'test-rg'")
// 	assert.Contains(t, kql, "database('ServiceLogs').table('clustersServiceLogs')")
// 	assert.Contains(t, kql, "database('HostedControlPlaneLogs').table('containerLogs')")
// 	assert.Contains(t, kql, "| project timestamp, log")
// }

// func TestDetailedServiceLogs(t *testing.T) {
// 	f := NewQueryFactory(&QueryOptions{
// 		ResourceGroupName: "test-rg",
// 		TimestampMin:      time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC),
// 		TimestampMax:      time.Date(2025, 1, 2, 0, 0, 0, 0, time.UTC),
// 		Limit:             -1,
// 	}, true)
// 	q, err := f.DetailedServiceLogs()
// 	require.NoError(t, err)

// 	kql := q.GetQuery().String()
// 	assert.Contains(t, kql, "let _resource_group = 'test-rg'")
// 	assert.Contains(t, kql, "database('ServiceLogs').table('frontendLogs')")
// 	assert.Contains(t, kql, "database('ServiceLogs').table('backendLogs')")
// 	assert.Contains(t, kql, "| project timestamp, container_name, msg, log")
// }

// func TestQueryDefinitions_TemplatePathsExist(t *testing.T) {
// 	for _, def := range AllQueryDefinitions {
// 		t.Run(def.TemplatePath, func(t *testing.T) {
// 			assert.NotPanics(t, func() {
// 				content := GetTemplate(def.TemplatePath)
// 				assert.NotEmpty(t, content, "template %s is empty", def.TemplatePath)
// 			}, "template %s does not exist", def.TemplatePath)
// 		})
// 	}
// }

// func TestQueryDefinitions_NoDanglingTemplates(t *testing.T) {
// 	allTemplates, err := ListTemplatePaths()
// 	require.NoError(t, err)

// 	referenced := make(map[string]bool)
// 	for _, def := range AllQueryDefinitions {
// 		referenced[def.TemplatePath] = true
// 	}

// 	for _, tmpl := range allTemplates {
// 		assert.True(t, referenced[tmpl], "template %s is not referenced by any QueryDefinition", tmpl)
// 	}
// }

// func TestQueryDefinitions_NoDuplicateTemplatePaths(t *testing.T) {
// 	seen := make(map[string]bool)
// 	for _, def := range AllQueryDefinitions {
// 		if seen[def.TemplatePath] {
// 			continue // multiple definitions may share a template (e.g. InfraServiceLogs/ServiceLogs)
// 		}
// 		seen[def.TemplatePath] = true
// 	}

// 	// Verify the count of unique template paths matches the count of actual templates
// 	allTemplates, err := ListTemplatePaths()
// 	require.NoError(t, err)
// 	assert.Equal(t, len(allTemplates), len(seen),
// 		"number of unique template paths in AllQueryDefinitions (%d) does not match number of template files (%d)",
// 		len(seen), len(allTemplates))
// }
