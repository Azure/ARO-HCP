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
	"time"

	azcorearm "github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"

	"github.com/Azure/ARO-HCP/tooling/hcpctl/pkg/kusto"
)

type QueryType string

const (
	QueryTypeServices           QueryType = "services"
	QueryTypeHostedControlPlane QueryType = "hosted-control-plane"
	QueryTypeClusterId          QueryType = "cluster-id"
	QueryTypeKubernetesEvents   QueryType = "kubernetes-events"
	QueryTypeSystemdLogs        QueryType = "systemd-logs"
)

var servicesDatabase = "ServiceLogs"
var hostedControlPlaneLogsDatabase = "HostedControlPlaneLogs"

var servicesTables = []string{
	"containerLogs",
	"clustersServiceLogs",
	"frontendLogs",
	"backendLogs",
}

var hcpNamespacePrefix = "ocm-arohcp"

var containerLogsTable = servicesTables[0]
var clustersServiceLogsTable = servicesTables[1]

// RowWithClusterId represents a row in the query result with a cluster id
type ClusterIdRow struct {
	ClusterId string `kusto:"cid"`
}

type ClusterNameRow struct {
	ClusterName string `kusto:"cluster"`
}

type QueryOptions struct {
	ClusterIds        []string
	SubscriptionId    string
	ResourceGroupName string
	InfraClusterName  string
	TimestampMin      time.Time
	TimestampMax      time.Time
	Limit             int
}

func NewInfraQueryOptions(infraClusterName string, timestampMin, timestampMax time.Time, limit int) (*QueryOptions, error) {
	return &QueryOptions{
		InfraClusterName: infraClusterName,
		TimestampMin:     timestampMin,
		TimestampMax:     timestampMax,
		Limit:            limit,
	}, nil
}

func NewQueryOptions(subscriptionID, resourceGroupName, resourceId string, timestampMin, timestampMax time.Time, limit int) (*QueryOptions, error) {
	if resourceId != "" {
		res, err := azcorearm.ParseResourceID(resourceId)
		if err != nil {
			return nil, fmt.Errorf("failed to parse resourceID: %w", err)
		}
		subscriptionID = res.SubscriptionID
		resourceGroupName = res.ResourceGroupName
	}

	return &QueryOptions{
		SubscriptionId:    subscriptionID,
		ResourceGroupName: resourceGroupName,
		TimestampMin:      timestampMin,
		TimestampMax:      timestampMax,
		Limit:             limit,
	}, nil
}

func (opts *QueryOptions) GetInfraKubernetesEventsQuery() []*kusto.ConfigurableQuery {
	query := kusto.NewConfigurableQuery("kubernetesEvents", servicesDatabase)
	if opts.Limit < 0 {
		query.WithNoTruncation()
	}
	query.WithTable("kubernetesEvents")
	query.WithTimestampMinAndMax(opts.TimestampMin, opts.TimestampMax)
	query.WithCluster(opts.InfraClusterName)
	if opts.Limit > 0 {
		query.WithLimit(opts.Limit)
	}
	query.WithInfraFields()
	query.WithOrderByTimestampAsc()
	return []*kusto.ConfigurableQuery{query}
}

func (opts *QueryOptions) GetKubernetesEventsExcludingHCPQuery() []*kusto.ConfigurableQuery {
	query := kusto.NewConfigurableQuery("kubernetesEvents", servicesDatabase)
	if opts.Limit < 0 {
		query.WithNoTruncation()
	}
	query.WithTable("kubernetesEvents")
	query.WithTimestampMinAndMax(opts.TimestampMin, opts.TimestampMax)
	query.WithCluster(opts.InfraClusterName)
	query.WithEventNamespaceExcluding([]string{hcpNamespacePrefix})
	if opts.Limit > 0 {
		query.WithLimit(opts.Limit)
	}
	query.WithInfraFields()
	query.WithOrderByTimestampAsc()
	return []*kusto.ConfigurableQuery{query}
}

func (opts *QueryOptions) GetKubernetesEventsHCPQuery() []*kusto.ConfigurableQuery {
	if len(opts.ClusterIds) == 0 {
		return nil
	}
	query := kusto.NewConfigurableQuery("kubernetesEvents", servicesDatabase)
	if opts.Limit < 0 {
		query.WithNoTruncation()
	}
	query.WithTable("kubernetesEvents")
	query.WithTimestampMinAndMax(opts.TimestampMin, opts.TimestampMax)
	query.WithCluster(opts.InfraClusterName)
	query.WithEventNamespaceHasAny(opts.ClusterIds)
	if opts.Limit > 0 {
		query.WithLimit(opts.Limit)
	}
	query.WithInfraFields()
	query.WithOrderByTimestampAsc()
	return []*kusto.ConfigurableQuery{query}
}

func (opts *QueryOptions) GetInfraSystemdLogsQuery() []*kusto.ConfigurableQuery {
	query := kusto.NewConfigurableQuery("systemdLogs", servicesDatabase)
	if opts.Limit < 0 {
		query.WithNoTruncation()
	}
	query.WithTable("systemdLogs").WithInfraFields()
	query.WithCluster(opts.InfraClusterName)
	query.WithTimestampMinAndMax(opts.TimestampMin, opts.TimestampMax)
	if opts.Limit > 0 {
		query.WithLimit(opts.Limit)
	}
	query.WithOrderByTimestampAsc()
	return []*kusto.ConfigurableQuery{query}
}

func (opts *QueryOptions) GetInfraServicesQueries() []*kusto.ConfigurableQuery {
	queries := []*kusto.ConfigurableQuery{}
	for _, table := range servicesTables {
		query := kusto.NewConfigurableQuery(table, servicesDatabase)
		if opts.Limit < 0 {
			query.WithNoTruncation()
		}
		query.WithTable(table).WithDefaultFields()
		query.WithTimestampMinAndMax(opts.TimestampMin, opts.TimestampMax)
		query.WithCluster(opts.InfraClusterName)
		if opts.Limit > 0 {
			query.WithLimit(opts.Limit)
		}
		query.WithOrderByTimestampAsc()
		queries = append(queries, query)
	}
	return queries
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

func (opts *QueryOptions) GetClusterNamesQueries() []*kusto.ConfigurableQuery {
	return []*kusto.ConfigurableQuery{
		kusto.NewClusterNamesQuery(servicesDatabase, containerLogsTable, opts.SubscriptionId, opts.ResourceGroupName),
		kusto.NewClusterNamesQuery(hostedControlPlaneLogsDatabase, containerLogsTable, opts.SubscriptionId, opts.ResourceGroupName),
	}
}
