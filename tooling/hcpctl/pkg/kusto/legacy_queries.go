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

	"github.com/Azure/azure-kusto-go/kusto/kql"
)

var servicesDatabaseLegacy = "HCPServiceLogs"

// NewLegacyClusterIDQuery creates a new KQL query for obtaining cluster IDs
// this works for the old kusto infrastructure setup that uses the HCPServiceLogs database
func NewLegacyClusterIdQuery(clusterServiceLogsTable, subscriptionId, resourceGroup string) *ConfigurableQuery {
	builder := kql.New("").AddTable(clusterServiceLogsTable)
	// TODO: the 2 day timestamp is not being honored for timestamps, but the query will timeout without scoping it.
	builder.AddLiteral(`
| where TIMESTAMP > ago(2d)
| where namespace_name == "aro-hcp"
| where container_name startswith "aro-hcp-"
| extend d = parse_json(log)
| project d
| evaluate bag_unpack(d)
| where resource_id has subResourceGroupId
| where isnotempty(internal_id)
| extend cid=extract(cidRegex, 1, internal_id)
| distinct cid`)

	parameters := kql.NewParameters()
	parameters.AddString("subResourceGroupId", fmt.Sprintf("/subscriptions/%s/resourceGroups/%s", subscriptionId, resourceGroup))
	parameters.AddString("cidRegex", "/api/aro_hcp/v1alpha1/clusters/([^/]+)")

	return &ConfigurableQuery{
		Name:       "Cluster ID",
		Database:   servicesDatabaseLegacy,
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
		builder.AddLiteral("\n| where log has subResourceGroupId or log has_any (clusterId)")
		parameters.AddString("clusterId", strings.Join(clusterIds, ","))
	} else {
		builder.AddLiteral("\n| where log has subResourceGroupId")
	}
	builder.AddLiteral("\n| project log, Role, namespace_name, container_name, timestamp, kubernetes ")

	parameters.AddString("subResourceGroupId", fmt.Sprintf("/subscriptions/%s/resourceGroups/%s", subscriptionId, resourceGroupName))

	return &ConfigurableQuery{
		Name:       "KubeSystem Service Logs",
		Database:   servicesDatabaseLegacy,
		Query:      builder,
		Parameters: parameters,
	}
}
