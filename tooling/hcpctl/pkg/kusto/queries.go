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

	"github.com/Azure/azure-kusto-go/kusto/kql"
)

func NewClusterIdQuery(database, clusterServiceLogsTable, subscriptionId, resourceGroup string) *ConfigurableQuery {
	builder := kql.New("").AddTable(clusterServiceLogsTable)
	builder.AddLiteral("\n| where resource_id has subResourceGroupId")
	builder.AddLiteral("\n| distinct cid")

	parameters := kql.NewParameters()
	parameters.AddString("subResourceGroupId", fmt.Sprintf("/subscriptions/%s/resourceGroups/%s", subscriptionId, resourceGroup))

	return &ConfigurableQuery{
		Name:       "Cluster ID",
		Database:   database,
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
