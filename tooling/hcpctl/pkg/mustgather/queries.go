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
	"fmt"
	"strings"
	"time"

	"github.com/Azure/ARO-HCP/tooling/hcpctl/pkg/kusto"
)

type QueryType string

const (
	QueryTypeServices           QueryType = "services"
	QueryTypeHostedControlPlane QueryType = "hosted-control-plane"
	QueryTypeClusterId          QueryType = "cluster-id"
)

var servicesDatabase = "ServiceLogs"
var hostedControlPlaneLogsDatabase = "HostedControlPlaneLogs"

var servicesTables = []string{
	"containerLogs",
	"clustersServiceLogs",
	"frontendLogs",
	"backendLogs",
}

var containerLogsTable = servicesTables[0]
var clustersServiceLogsTable = servicesTables[1]

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

func NewQueryOptions(subscriptionID, resourceGroupName, resourceId string, timestampMin, timestampMax time.Time, limit int) (*QueryOptions, error) {
	var subId, rgName string
	var err error
	if resourceId != "" {
		subId, rgName, err = parseResourceId(resourceId)
		if err != nil {
			return nil, fmt.Errorf("failed to parse resourceId: %w", err)
		}
	} else {
		subId = subscriptionID
		rgName = resourceGroupName
	}

	return &QueryOptions{
		SubscriptionId:    subId,
		ResourceGroupName: rgName,
		TimestampMin:      timestampMin,
		TimestampMax:      timestampMax,
		Limit:             limit,
	}, nil
}

func parseResourceId(resourceId string) (string, string, error) {
	// /subscriptions/1d3378d3-5a3f-4712-85a1-2485495dfc4b/resourceGroups/hcp-kusto-us
	parts := strings.Split(resourceId, "/")
	if len(parts) < 4 {
		return "", "", fmt.Errorf("invalid resourceId: %s", resourceId)
	}
	subscriptionId := parts[2]
	resourceGroupName := parts[4]

	if subscriptionId == "" || resourceGroupName == "" {
		return "", "", fmt.Errorf("invalid resourceId: %s", resourceId)
	}

	return subscriptionId, resourceGroupName, nil
}

func (opts *QueryOptions) GetServicesQueries() []*kusto.ConfigurableQuery {
	queries := []*kusto.ConfigurableQuery{}
	for _, table := range servicesTables {
		query := kusto.NewConfigurableQuery(table, servicesDatabase)
		if opts.Limit < 0 {
			query.WithNoTruncation()
		}
		query.WithTable(table).WithDefaultFields()

		query.WithTimestampMinAndMax(opts.TimestampMin, opts.TimestampMax)
		query.WithClusterIdOrSubscriptionAndResourceGroup(opts.ClusterIds, opts.SubscriptionId, opts.ResourceGroupName)
		if opts.Limit > 0 {
			query.WithLimit(opts.Limit)
		}
		query.WithOrderByTimestampAsc()
		queries = append(queries, query)
	}
	return queries
}

func (opts *QueryOptions) GetHostedControlPlaneLogsQuery() []*kusto.ConfigurableQuery {
	queries := []*kusto.ConfigurableQuery{}
	for _, clusterId := range opts.ClusterIds {
		query := kusto.NewConfigurableQuery("hostedControlPlaneLogs", hostedControlPlaneLogsDatabase)
		if opts.Limit < 0 {
			query.WithNoTruncation()
		}
		query.WithTable(containerLogsTable).WithDefaultFields()

		query.WithTimestampMinAndMax(opts.TimestampMin, opts.TimestampMax)
		query.WithClusterId(clusterId)
		if opts.Limit > 0 {
			query.WithLimit(opts.Limit)
		}
		query.WithOrderByTimestampAsc()
		queries = append(queries, query)
	}
	return queries
}

func (opts *QueryOptions) GetClusterIdQuery() *kusto.ConfigurableQuery {
	return kusto.NewClusterIdQuery(servicesDatabase, clustersServiceLogsTable, opts.SubscriptionId, opts.ResourceGroupName)
}
