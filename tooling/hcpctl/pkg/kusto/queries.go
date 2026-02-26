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

	"github.com/Azure/azure-kusto-go/azkustodata/kql"
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
