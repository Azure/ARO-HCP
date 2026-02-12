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

var servicesDatabaseLegacy = "HCPServiceLogs"
var kubeSystemTable = "kubesystem"

// NewLegacyClusterIDQuery creates a new KQL query for obtaining cluster IDs
// this works for the old kusto infrastructure setup that uses the HCPServiceLogs database
func NewLegacyClusterIdQuery(subscriptionId, resourceGroup string, timestampMin, timestampMax time.Time, limit int) *ConfigurableQuery {
	builder := kql.New("").AddTable(kubeSystemTable)
	builder.AddLiteral(`
| where TIMESTAMP between(timestampMin .. timestampMax)
| order by TIMESTAMP asc
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
	parameters.AddDateTime("timestampMin", timestampMin)
	parameters.AddDateTime("timestampMax", timestampMax)
	parameters.AddString("subResourceGroupId", fmt.Sprintf("/subscriptions/%s/resourceGroups/%s", subscriptionId, resourceGroup))
	parameters.AddString("cidRegex", "/api/aro_hcp/v1alpha1/clusters/([^/]+)")

	// do not take limit when looking for clusterIDs
	return &ConfigurableQuery{
		Name:       "Cluster ID",
		Database:   servicesDatabaseLegacy,
		Query:      builder,
		Parameters: parameters,
	}
}

// NewKubeSystemQuery creates a new KQL query for the kubesystem table
// This is part of legacy support for the kubesystem table
func NewKubeSystemQuery(subscriptionId, resourceGroupName string, clusterIds []string, timestampMin, timestampMax time.Time, limit int) *ConfigurableQuery {
	builder := kql.New("").AddTable(kubeSystemTable)
	builder.AddLiteral(`
	| where TIMESTAMP between(timestampMin .. timestampMax)
	| order by TIMESTAMP asc
	| project log, Role, namespace_name, container_name, timestamp, kubernetes`)

	parameters := kql.NewParameters()
	if len(clusterIds) != 0 {
		builder.AddLiteral("\n| where log has subResourceGroupId or log has_any (clusterId)")
		parameters.AddString("clusterId", strings.Join(clusterIds, ","))
	} else {
		builder.AddLiteral("\n| where log has subResourceGroupId")
	}

	if limit > 0 {
		builder.AddLiteral("\n| limit ").AddInt(int32(limit))
	}

	parameters.AddDateTime("timestampMin", timestampMin)
	parameters.AddDateTime("timestampMax", timestampMax)
	parameters.AddString("subResourceGroupId", fmt.Sprintf("/subscriptions/%s/resourceGroups/%s", subscriptionId, resourceGroupName))

	return &ConfigurableQuery{
		Name:       "KubeSystem Service Logs",
		Database:   servicesDatabaseLegacy,
		Query:      builder,
		Parameters: parameters,
	}
}

// NewCustomerKubeSystemQuery creates a new KQL query for the customerLogs table
func NewCustomerKubeSystemQuery(clusterId string, timestampMin, timestampMax time.Time, limit int) *ConfigurableQuery {
	builder := kql.New("").AddTable(kubeSystemTable)
	builder.AddLiteral(`
	| where TIMESTAMP between(minTimestamp .. maxTimestamp)
	| order by TIMESTAMP asc
	| where log has clusterId or kubernetes has clusterId
	| project log, Role, namespace_name, container_name, timestamp, kubernetes`)

	if limit > 0 {
		builder.AddLiteral("\n| limit ").AddInt(int32(limit))
	}

	parameters := kql.NewParameters()
	parameters.AddString("clusterId", clusterId)
	parameters.AddDateTime("minTimestamp", timestampMin)
	parameters.AddDateTime("maxTimestamp", timestampMax)

	return &ConfigurableQuery{
		Name:       "KubeSystem Hosted Control Plane Logs",
		Database:   "HCPCustomerLogs",
		Query:      builder,
		Parameters: parameters,
	}
}
