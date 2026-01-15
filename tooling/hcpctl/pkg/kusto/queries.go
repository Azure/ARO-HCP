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
	"strings"
	"time"

	"github.com/Azure/azure-kusto-go/kusto/kql"
)

type ConfigurableQuery struct {
	Name       string
	Database   string
	Query      *kql.Builder
	Parameters *kql.Parameters
}

func NewConfigurableQuery(name string, database string) *ConfigurableQuery {
	return &ConfigurableQuery{
		Name:       name,
		Database:   database,
		Query:      kql.New(""),
		Parameters: kql.NewParameters(),
	}
}

func (q *ConfigurableQuery) WithTable(tableName string) *ConfigurableQuery {
	q.Query.AddTable(tableName)
	return q
}

func (q *ConfigurableQuery) WithDefaultFields() *ConfigurableQuery {
	q.Query.AddLiteral("\n| project timestamp, log, cluster, namespace_name, container_name")
	return q
}

func (q *ConfigurableQuery) WithClusterId(clusterId string) *ConfigurableQuery {
	q.Query.AddLiteral("\n| where namespace_name has clusterId")
	q.Parameters.AddString("clusterId", clusterId)
	return q
}

func (q *ConfigurableQuery) WithNoTruncation() *ConfigurableQuery {
	q.Query.AddLiteral("set notruncation;\n")
	return q
}

func (q *ConfigurableQuery) WithLimit(limit int) *ConfigurableQuery {
	q.Query.AddLiteral("\n| limit ").AddInt(int32(limit))
	return q
}

func (q *ConfigurableQuery) WithOrderByTimestampAsc() *ConfigurableQuery {
	q.Query.AddLiteral("\n| order by timestamp asc")
	return q
}

func (q *ConfigurableQuery) WithTimestampMinAndMax(timestampMin time.Time, timestampMax time.Time) *ConfigurableQuery {
	q.Query.AddLiteral("\n| where timestamp >= timestampMin and timestamp <= timestampMax")
	q.Parameters.AddDateTime("timestampMin", timestampMin)
	q.Parameters.AddDateTime("timestampMax", timestampMax)
	return q
}

func (q *ConfigurableQuery) WithResourceIdHasResourceGroup(resourceGroup string) *ConfigurableQuery {
	q.Query.AddLiteral("\n| where resource_id has resourceGroupName")
	q.Parameters.AddString("resourceGroupName", resourceGroup)
	return q
}

func (q *ConfigurableQuery) WithClusterIdOrSubscriptionAndResourceGroup(clusterIds []string, subscriptionId string, resourceGroup string) *ConfigurableQuery {
	if len(clusterIds) != 0 {
		q.Query.AddLiteral("\n| where log has subscriptionId  and log has resourceGroupName or log has_any (clusterId)")
		q.Parameters.AddString("clusterId", strings.Join(clusterIds, ","))
	} else {
		q.Query.AddLiteral("\n| where log has subscriptionId  and log has resourceGroupName")
	}
	q.Parameters.AddString("subscriptionId", subscriptionId)
	q.Parameters.AddString("resourceGroupName", resourceGroup)
	return q
}

func NewClusterIdQuery(database, clusterServiceLogsTable, subscriptionId, resourceGroup string) *ConfigurableQuery {
	builder := kql.New("").AddTable(clusterServiceLogsTable)
	builder.AddLiteral("\n| where resource_id has subscriptionId and resource_id has resourceGroupName")
	builder.AddLiteral("\n| distinct cid")

	parameters := kql.NewParameters()
	parameters.AddString("subscriptionId", subscriptionId)
	parameters.AddString("resourceGroupName", resourceGroup)

	return &ConfigurableQuery{
		Name:       "Cluster ID",
		Database:   database,
		Query:      builder,
		Parameters: parameters,
	}
}

// NewKubeSystemQuery creates a new KQL query for the kubesystem table
// This is part of legacy support for the kubesystem table
func NewKubeSystemQuery(subscriptionId, resourceGroupName string, clusterIds []string) *ConfigurableQuery {
	builder := kql.New("").AddTable("kubesystem")
	parameters := kql.NewParameters()

	if len(clusterIds) != 0 {
		builder.AddLiteral("\n| where log has subscriptionId  and log has resourceGroupName or log has_any (clusterId)")
		parameters.AddString("clusterId", strings.Join(clusterIds, ","))
	} else {
		builder.AddLiteral("\n| where log has subscriptionId  and log has resourceGroupName")
	}
	builder.AddLiteral("\n| project log, Role, namespace_name, container_name, timestamp, kubernetes ")

	parameters.AddString("subscriptionId", subscriptionId)
	parameters.AddString("resourceGroupName", resourceGroupName)

	return &ConfigurableQuery{
		Name:       "KubeSystem Service Logs",
		Database:   "HCPServiceLogs",
		Query:      builder,
		Parameters: parameters,
	}
}

// NewCustomerKubeSystemQuery creates a new KQL query for the customerLogs table
func NewCustomerKubeSystemQuery(clusterId string, limit int) *ConfigurableQuery {
	builder := kql.New("").AddTable("kubesystem")
	parameters := kql.NewParameters()

	builder.AddLiteral("\n| where log has clusterId or kubernetes has clusterId")
	parameters.AddString("clusterId", clusterId)

	builder.AddLiteral("\n| project log, Role, namespace_name, container_name, timestamp, kubernetes ")
	if limit > 0 {
		builder.AddLiteral("\n| limit ").AddInt(int32(limit))
	}
	return &ConfigurableQuery{
		Name:       "KubeSystem Hosted Control Plane Logs",
		Database:   "HCPCustomerLogs",
		Query:      builder,
		Parameters: parameters,
	}
}
