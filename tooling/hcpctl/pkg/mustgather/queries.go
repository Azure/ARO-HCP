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

type QueryType string

var ServicesDatabase = "ServiceLogs"
var HostedControlPlaneLogsDatabase = "HostedControlPlaneLogs"

var ServicesTables = []string{
	"containerLogs",
	"clustersServiceLogs",
	"frontendLogs",
	"backendLogs",
}

var HCPNamespacePrefix = "ocm-arohcp"

var ContainerLogsTable = ServicesTables[0]
var ClustersServiceLogsTable = ServicesTables[1]

const (
	QueryTypeServices           QueryType = "services"
	QueryTypeHostedControlPlane QueryType = "hosted-control-plane"
	QueryTypeClusterId          QueryType = "cluster-id"
	QueryTypeKubernetesEvents   QueryType = "kubernetes-events"
	QueryTypeSystemdLogs        QueryType = "systemd-logs"
	QueryTypeCustomLogs         QueryType = "custom-logs"
)

// ClusterIdRow represents a row in the query result with a cluster id
type ClusterIdRow struct {
	ClusterId string `kusto:"cid"`
}

type ClusterNameRow struct {
	ClusterName string `kusto:"cluster"`
}

func serviceLogs(f *kusto.QueryFactory, options kusto.QueryOptions, clusterIds []string) ([]kusto.Query, error) {
	queries := make([]kusto.Query, 0, len(ServicesTables))
	for _, table := range ServicesTables {
		def := kusto.ServiceLogsQueryDef
		q, err := f.Build(def, kusto.NewTemplateDataFromOptions(options, kusto.WithTable(table), kusto.WithClusterIds(clusterIds)))
		if err != nil {
			return nil, err
		}
		queries = append(queries, q...)
	}
	return queries, nil
}

func clusterNamesQueries(f *kusto.QueryFactory, options kusto.QueryOptions) ([]kusto.Query, error) {
	databases := []string{ServicesDatabase, HostedControlPlaneLogsDatabase}
	queries := make([]kusto.Query, 0, len(databases))
	for _, db := range databases {
		def := kusto.ClusterNamesQueryDef
		def.Database = db
		q, err := f.Build(def, kusto.NewTemplateDataFromOptions(options))
		if err != nil {
			return nil, err
		}
		queries = append(queries, q...)
	}
	return queries, nil
}

func hostedControlPlaneLogs(f *kusto.QueryFactory, options kusto.QueryOptions, clusterIds []string) ([]kusto.Query, error) {
	queries := make([]kusto.Query, 0, len(clusterIds))
	for _, clusterId := range clusterIds {
		def := kusto.HostedControlPlaneLogsQuery
		q, err := f.Build(def, kusto.NewTemplateDataFromOptions(options, kusto.WithClusterId(clusterId)))
		if err != nil {
			return nil, err
		}
		queries = append(queries, q...)
	}
	return queries, nil
}
