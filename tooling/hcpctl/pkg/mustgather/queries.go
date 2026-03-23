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
	"github.com/Azure/ARO-HCP/tooling/hcpctl/pkg/kusto"
)

var ServicesTables = []string{
	"containerLogs",
	"clustersServiceLogs",
	"frontendLogs",
	"backendLogs",
}

var HCPNamespacePrefix = "ocm-arohcp"

var ContainerLogsTable = ServicesTables[0]
var ClustersServiceLogsTable = ServicesTables[1]

// ClusterIdRow represents a row in the query result with a cluster id
type ClusterIdRow struct {
	ClusterId string `kusto:"cid"`
}

type ClusterNameRow struct {
	ClusterName string `kusto:"cluster"`
}

func serviceLogs(f *kusto.QueryFactory, tableDefinition string, options kusto.QueryOptions, clusterIds []string) ([]kusto.Query, error) {
	def, err := f.GetBuiltinQueryDefinition(tableDefinition)
	if err != nil {
		return nil, err
	}
	queries := make([]kusto.Query, 0, len(ServicesTables))
	for _, table := range ServicesTables {
		q, err := f.Build(*def, kusto.NewTemplateDataFromOptions(options, kusto.WithTable(table), kusto.WithClusterIds(clusterIds)))
		if err != nil {
			return nil, err
		}
		queries = append(queries, q...)
	}
	return queries, nil
}

func hostedControlPlaneLogs(f *kusto.QueryFactory, options kusto.QueryOptions, clusterIds []string) ([]kusto.Query, error) {
	def, err := f.GetBuiltinQueryDefinition("hostedControlPlaneLogs")
	if err != nil {
		return nil, err
	}
	queries := make([]kusto.Query, 0, len(clusterIds))
	for _, clusterId := range clusterIds {
		q, err := f.Build(*def, kusto.NewTemplateDataFromOptions(options, kusto.WithClusterId(clusterId)))
		if err != nil {
			return nil, err
		}
		queries = append(queries, q...)
	}
	return queries, nil
}
