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
	"time"

	"github.com/Azure/ARO-HCP/tooling/hcpctl/pkg/kusto"
)

var servicesDatabase = "ServiceLogs"
var hostedControlPlaneLogsDatabase = "HostedControlPlaneLogs"

var servicesDatabaseLegacy = "HCPServiceLogs"

var servicesTables = []string{
	"containerLogs",
	"frontendLogs",
	"backendLogs",
}

var containerLogsTable = servicesTables[0]

// RowWithClusterId represents a row in the query result with a cluster id
type ClusterIdRow struct {
	ClusterId string `kusto:"cid"`
}

type QueryOptions struct {
	ClusterIds        []string
	SubscriptionId    string
	ResourceGroupName string
	TimestampMin      time.Time
	TimestampMax      time.Time
	Limit             int
}

func getServicesQueries(opts QueryOptions) []*kusto.ConfigurableQuery {
	queries := []*kusto.ConfigurableQuery{}
	for _, table := range servicesTables {
		query := kusto.NewConfigurableQuery(table, servicesDatabase)
		if opts.Limit < 0 {
			query.WithNoTruncation()
		}
		query.WithTable(table).WithDefaultFields()

		query.WithTimestampMinAndMax(getTimeMinMax(opts.TimestampMin, opts.TimestampMax))
		query.WithClusterIdOrSubscriptionAndResourceGroup(opts.ClusterIds, opts.SubscriptionId, opts.ResourceGroupName)
		if opts.Limit > 0 {
			query.WithLimit(opts.Limit)
		}
		queries = append(queries, query)
	}
	return queries
}

func getHostedControlPlaneLogsQuery(opts QueryOptions) []*kusto.ConfigurableQuery {
	queries := []*kusto.ConfigurableQuery{}
	for _, clusterId := range opts.ClusterIds {
		query := kusto.NewConfigurableQuery("hostedControlPlaneLogs", hostedControlPlaneLogsDatabase)
		if opts.Limit < 0 {
			query.WithNoTruncation()
		}
		query.WithTable(containerLogsTable).WithDefaultFields()

		query.WithTimestampMinAndMax(getTimeMinMax(opts.TimestampMin, opts.TimestampMax))
		query.WithClusterId(clusterId)
		if opts.Limit > 0 {
			query.WithLimit(opts.Limit)
		}
		queries = append(queries, query)
	}
	return queries
}

func getClusterIdQuery(subscriptionId, resourceGroupName string) *kusto.ConfigurableQuery {
	return kusto.NewClusterIdQuery(servicesDatabase, containerLogsTable, subscriptionId, resourceGroupName)
}

func getTimeMinMax(timestampMin, timestampMax time.Time) (time.Time, time.Time) {
	if timestampMin.IsZero() {
		timestampMin = time.Now().Add(-24 * time.Hour)
	}
	if timestampMax.IsZero() {
		timestampMax = time.Now()
	}
	return timestampMin, timestampMax
}

// --------------------------------------------------------------------------------------------------
// Legacy single table queries

// Row represents a row in the query result
type KubesystemLogsRow struct {
	Log           string `kusto:"log"`
	Cluster       string `kusto:"Role"`
	Namespace     string `kusto:"namespace_name"`
	ContainerName string `kusto:"container_name"`
	Timestamp     string `kusto:"timestamp"`
	Kubernetes    string `kusto:"kubernetes"`
}

func getKubeSystemClusterIdQuery(subscriptionId, resourceGroupName string) *kusto.ConfigurableQuery {
	return kusto.NewClusterIdQuery(servicesDatabaseLegacy, "kubesystem", subscriptionId, resourceGroupName)
}

func getKubeSystemQuery(subscriptionId, resourceGroupName string, clusterIds []string) *kusto.ConfigurableQuery {
	return kusto.NewKubeSystemQuery(subscriptionId, resourceGroupName, clusterIds)
}

func getKubeSystemHostedControlPlaneLogsQuery(opts QueryOptions) []*kusto.ConfigurableQuery {
	queries := []*kusto.ConfigurableQuery{}
	for _, clusterId := range opts.ClusterIds {
		query := kusto.NewCustomerKubeSystemQuery(clusterId, opts.Limit)
		queries = append(queries, query)
	}
	return queries
}
