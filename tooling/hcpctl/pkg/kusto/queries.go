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
	q.Query.AddLiteral("\n| project timestamp, log, cluster, namespace, containerName")
	return q
}

func (q *ConfigurableQuery) WithClusterId(clusterId string) *ConfigurableQuery {
	q.Query.AddLiteral("\n| where namespace has clusterId")
	q.Parameters.AddString("clusterId", clusterId)
	return q
}

func (q *ConfigurableQuery) WithLimit(limit int) *ConfigurableQuery {
	q.Query.AddLiteral("\n| limit ").AddInt(int32(limit))
	return q
}

func (q *ConfigurableQuery) WithTimestampMinAndMax(timestampMin time.Time, timestampMax time.Time) *ConfigurableQuery {
	q.Query.AddLiteral("\n| where timestamp >= timestampMin and timestamp <= timestampMax")
	q.Parameters.AddDateTime("timestampMin", timestampMin)
	q.Parameters.AddDateTime("timestampMax", timestampMax)
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

func NewClusterIdQuery(clusterServiceLogsTable, subscriptionId, resourceGroup string) *ConfigurableQuery {
	builder := kql.New("").AddTable(clusterServiceLogsTable)
	builder.AddLiteral("\n| where log has subscriptionId  and log has resourceGroupName")
	builder.AddLiteral("\n| extend cid=extract(@\"cid='([a-v0-9]{32})'\", 1, tostring(log))")
	builder.AddLiteral("\n| distinct cid")

	parameters := kql.NewParameters()
	parameters.AddString("subscriptionId", subscriptionId)
	parameters.AddString("resourceGroupName", resourceGroup)

	return &ConfigurableQuery{
		Name:       "Cluster ID",
		Database:   "HCPServiceLogs",
		Query:      builder,
		Parameters: parameters,
	}
}
