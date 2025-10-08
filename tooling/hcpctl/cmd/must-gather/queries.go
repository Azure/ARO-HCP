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

func getServicesQueries(clusterIds []string, subscriptionId, resourceGroupName string) []*kusto.ConfigurableQuery {
	queries := []*kusto.ConfigurableQuery{}
	for _, table := range servicesTables {
		query := kusto.NewConfigurableQuery(table, servicesDatabase).
			WithTable(table).
			WithDefaultFields().
			WithClusterIdOrSubscriptionAndResourceGroup(clusterIds, subscriptionId, resourceGroupName)

		queries = append(queries, query)
	}
	return queries
}

func getCustomerLogsQuery(clusterIds []string) []*kusto.ConfigurableQuery {
	queries := []*kusto.ConfigurableQuery{}
	for _, clusterId := range clusterIds {
		query := kusto.NewConfigurableQuery("customerLogs", customerLogsDatabase).
			WithTable(containerLogsTable).
			WithDefaultFields().
			WithClusterId(clusterId).
			WithLimit(10000)

		queries = append(queries, query)
	}
	return queries
}

func getClusterIdQuery(subscriptionId, resourceGroupName string) *kusto.ConfigurableQuery {
	return kusto.NewClusterIdQuery(containerLogsTable, subscriptionId, resourceGroupName)
}
