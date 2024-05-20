package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"

	"github.com/Azure/azure-sdk-for-go/sdk/azidentity"
	"github.com/Azure/azure-sdk-for-go/sdk/data/azcosmos"
)

const (
	clustersContainer = "Clusters"
)

// DBClient defines the needed values to perform CRUD operations against the async DB
type DBClient struct {
	client *azcosmos.Client
	DBName string
	DBUrl  string
}

// NewDatabaseClient instanstiates a Cosmos DatabaseClient targeting Frontends async DB
func NewDatabaseClient() *DBClient {
	cred, _ := azidentity.NewDefaultAzureCredential(nil)
	d := &DBClient{
		DBName: os.Getenv("DB_NAME"),
		DBUrl:  os.Getenv("DB_URL"),
	}
	client, _ := azcosmos.NewClient(d.DBUrl, cred, nil)
	d.client = client

	return d
}

// DBConnectionTest checks the async database is accessible on startup
func (d *DBClient) DBConnectionTest(ctx context.Context) (string, error) {
	if d.DBName == "none" || d.DBName == "" {
		return "No database configured, skipping", nil
	}

	database, err := d.client.NewDatabase(d.DBName)
	if err != nil {
		return "", err
	}
	result, err := database.Read(ctx, nil)
	if err != nil {
		return "", err
	}
	return result.DatabaseProperties.ID, nil
}

// GetCluster retreives a cluster document from async DB using resource ID
func (d *DBClient) GetClusterDoc(ctx context.Context, resourceID string, partitionKey string) (*HCPOpenShiftClusterDocument, bool, error) {
	container, err := d.client.NewContainer(d.DBName, clustersContainer)
	if err != nil {
		return nil, false, err
	}

	query := "SELECT * FROM c WHERE c.key = @key"
	opt := azcosmos.QueryOptions{
		PageSizeHint:    1,
		QueryParameters: []azcosmos.QueryParameter{{Name: "@key", Value: resourceID}},
	}

	pk := azcosmos.NewPartitionKeyString(partitionKey)
	queryPager := container.NewQueryItemsPager(query, pk, &opt)

	var doc *HCPOpenShiftClusterDocument
	for queryPager.More() {
		queryResponse, err := queryPager.NextPage(ctx)
		if err != nil {
			return nil, false, err
		}

		for _, item := range queryResponse.Items {
			err = json.Unmarshal(item, &doc)
			if err != nil {
				return nil, false, err
			}
		}
	}
	if doc != nil {
		return doc, true, nil
	}
	return nil, false, nil

}

// SetCluster creates/updates a cluster document in the async DB during cluster creation/patching
func (d *DBClient) SetClusterDoc(ctx context.Context, doc *HCPOpenShiftClusterDocument) error {
	data, err := json.Marshal(doc)
	if err != nil {
		return err
	}

	container, err := d.client.NewContainer(d.DBName, clustersContainer)
	if err != nil {
		return err
	}

	_, err = container.UpsertItem(ctx, azcosmos.NewPartitionKeyString(doc.PartitionKey), data, nil)
	if err != nil {
		return err
	}

	return nil
}

// DeleteCluster removes a cluter document from the async DB using resource ID
func (d *DBClient) DeleteClusterDoc(ctx context.Context, resourceID string, partitionKey string) error {
	doc, found, err := d.GetClusterDoc(ctx, resourceID, partitionKey)
	if !found {
		return fmt.Errorf("document with key %s not found", partitionKey)
	}
	if err != nil {
		return err
	}

	container, err := d.client.NewContainer(d.DBName, clustersContainer)
	if err != nil {
		return err
	}

	_, err = container.DeleteItem(ctx, azcosmos.NewPartitionKeyString(partitionKey), doc.ID, nil)
	if err != nil {
		return err
	}
	return nil
}
