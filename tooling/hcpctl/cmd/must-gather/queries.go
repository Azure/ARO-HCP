package mustgather

import (
	"time"

	"github.com/Azure/ARO-HCP/tooling/hcpctl/pkg/kusto"
)

var servicesDatabase = "HCPServiceLogs"
var customerLogsDatabase = "HCPCustomerLogs"

var servicesTables = []string{
	"containerLogs",
	"frontendContainerLogs",
	"backendContainerLogs",
}

var containerLogsTable = servicesTables[0]

// Row represents a row in the query result
type ContainerLogsRow struct {
	Log           []byte    `kusto:"log"`
	Cluster       string    `kusto:"cluster"`
	Namespace     string    `kusto:"namespace"`
	ContainerName string    `kusto:"containerName"`
	Timestamp     time.Time `kusto:"timestamp"`
}

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
		query := kusto.NewConfigurableQuery(table, servicesDatabase).
			WithTable(table).
			WithDefaultFields()

		query.WithTimestampMinAndMax(getTimeMinMax(opts.TimestampMin, opts.TimestampMax))
		query.WithClusterIdOrSubscriptionAndResourceGroup(opts.ClusterIds, opts.SubscriptionId, opts.ResourceGroupName)
		query.WithLimit(opts.Limit)
		queries = append(queries, query)
	}
	return queries
}

func getHostedControlPlaneLogsQuery(opts QueryOptions) []*kusto.ConfigurableQuery {
	queries := []*kusto.ConfigurableQuery{}
	for _, clusterId := range opts.ClusterIds {
		query := kusto.NewConfigurableQuery("customerLogs", customerLogsDatabase).
			WithTable(containerLogsTable).
			WithDefaultFields()

		query.WithTimestampMinAndMax(getTimeMinMax(opts.TimestampMin, opts.TimestampMax))
		query.WithClusterId(clusterId)
		query.WithLimit(opts.Limit)
		queries = append(queries, query)
	}
	return queries
}

func getClusterIdQuery(subscriptionId, resourceGroupName string) *kusto.ConfigurableQuery {
	return kusto.NewClusterIdQuery(containerLogsTable, subscriptionId, resourceGroupName)
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
