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
	"fmt"
	"strings"
	"time"

	"github.com/Azure/azure-kusto-go/azkustodata/kql"
)

type ConfigurableQuery struct {
	Name       string
	Database   string
	Query      *kql.Builder
	Parameters *kql.Parameters
	Unlimited  bool
}

func NewConfigurableQuery(name string, database string) *ConfigurableQuery {
	return &ConfigurableQuery{
		Name:       name,
		Database:   database,
		Query:      kql.New(""),
		Parameters: kql.NewParameters(),
		Unlimited:  false,
	}
}

func (q *ConfigurableQuery) WithTable(tableName string) *ConfigurableQuery {
	q.Query.AddTable(tableName)
	return q
}

func (q *ConfigurableQuery) WithInfraFields() *ConfigurableQuery {
	q.Query.AddLiteral("\n| project timestamp, log, cluster")
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
	q.Unlimited = true
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
	q.Query.AddLiteral("\n| where timestamp between(timestampMin .. timestampMax)")
	q.Parameters.AddDateTime("timestampMin", timestampMin)
	q.Parameters.AddDateTime("timestampMax", timestampMax)
	return q
}

func (q *ConfigurableQuery) WithResourceIdHasResourceGroup(resourceGroup string) *ConfigurableQuery {
	q.Query.AddLiteral("\n| where resource_id has resourceGroupName")
	q.Parameters.AddString("resourceGroupName", resourceGroup)
	return q
}

func (q *ConfigurableQuery) WithEventNamespaceHasAny(clusterIds []string) *ConfigurableQuery {
	q.Query.AddLiteral("\n| where eventNamespace has_any (clusterIds)")
	q.Parameters.AddString("clusterIds", strings.Join(clusterIds, ","))
	return q
}

func (q *ConfigurableQuery) WithEventNamespaceExcluding(clusterIds []string) *ConfigurableQuery {
	q.Query.AddLiteral("\n| where not(eventNamespace has_any (clusterIds))")
	q.Parameters.AddString("clusterIds", strings.Join(clusterIds, ","))
	return q
}

func (q *ConfigurableQuery) WithClusterIdOrSubscriptionAndResourceGroup(clusterIds []string, subscriptionId string, resourceGroup string) *ConfigurableQuery {
	if len(clusterIds) != 0 {
		q.Query.AddLiteral("\n| where log has subResourceGroupId or log has_any (clusterId)")
		q.Parameters.AddString("clusterId", strings.Join(clusterIds, ","))
	} else {
		q.Query.AddLiteral("\n| where log has subResourceGroupId")
	}
	q.Parameters.AddString("subResourceGroupId", fmt.Sprintf("/subscriptions/%s/resourceGroups/%s", subscriptionId, resourceGroup))
	return q
}

func (q *ConfigurableQuery) WithCluster(clusterName string) *ConfigurableQuery {
	q.Query.AddLiteral("\n| where cluster == clusterName")
	q.Parameters.AddString("clusterName", clusterName)
	return q
}
