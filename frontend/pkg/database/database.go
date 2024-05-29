package database

import (
	"context"
	"encoding/json"
	"errors"

	"github.com/Azure/azure-sdk-for-go/sdk/azidentity"
	"github.com/Azure/azure-sdk-for-go/sdk/data/azcosmos"
)

const (
	clustersContainer = "Clusters"
	subsContainer     = "Subscriptions"
	billingContainer  = "Billing"
	asyncContainer    = "AsyncOperations"
)

var ErrNotFound = errors.New("DocumentNotFound")

// DBClient defines the needed values to perform CRUD operations against the async DB
type DBClient struct {
	client *azcosmos.Client
	config *DBConfig
}

// DBConfig stores database and client configuration data
type DBConfig struct {
	DBName        string
	DBUrl         string
	ClientOptions *azidentity.DefaultAzureCredentialOptions
}

// NewDatabaseConfig configures database configuration values for access
func NewDatabaseConfig(dbName, dbURL string) *DBConfig {
	opt := &azidentity.DefaultAzureCredentialOptions{}
	c := &DBConfig{
		DBName:        dbName,
		DBUrl:         dbURL,
		ClientOptions: opt,
	}
	return c
}

// NewDatabaseClient instantiates a Cosmos DatabaseClient targeting Frontends async DB
func NewDatabaseClient(config *DBConfig) (*DBClient, error) {
	cred, err := azidentity.NewDefaultAzureCredential(config.ClientOptions)
	if err != nil {
		return nil, err
	}

	d := &DBClient{
		config: config,
	}

	client, err := azcosmos.NewClient(d.config.DBUrl, cred, nil)
	if err != nil {
		return nil, err
	}

	d.client = client
	return d, nil
}

// DBConnectionTest checks the async database is accessible on startup
func (d *DBClient) DBConnectionTest(ctx context.Context) (string, error) {
	if d.config.DBName == "none" || d.config.DBName == "" {
		return "No database configured, skipping", nil
	}

	database, err := d.client.NewDatabase(d.config.DBName)
	if err != nil {
		return "", err
	}
	result, err := database.Read(ctx, nil)
	if err != nil {
		return "", err
	}
	return result.DatabaseProperties.ID, nil
}

// GetClusterDoc retreives a cluster document from async DB using resource ID
func (d *DBClient) GetClusterDoc(ctx context.Context, resourceID string, partitionKey string) (*HCPOpenShiftClusterDocument, error) {
	container, err := d.client.NewContainer(d.config.DBName, clustersContainer)
	if err != nil {
		return nil, err
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
			return nil, err
		}

		for _, item := range queryResponse.Items {
			err = json.Unmarshal(item, &doc)
			if err != nil {
				return nil, err
			}
		}
	}
	if doc != nil {
		return doc, nil
	}
	return nil, ErrNotFound
}

// SetClusterDoc creates/updates a cluster document in the async DB during cluster creation/patching
func (d *DBClient) SetClusterDoc(ctx context.Context, doc *HCPOpenShiftClusterDocument) error {
	data, err := json.Marshal(doc)
	if err != nil {
		return err
	}

	container, err := d.client.NewContainer(d.config.DBName, clustersContainer)
	if err != nil {
		return err
	}

	_, err = container.UpsertItem(ctx, azcosmos.NewPartitionKeyString(doc.PartitionKey), data, nil)
	if err != nil {
		return err
	}

	return nil
}

// DeleteClusterDoc removes a cluter document from the async DB using resource ID
func (d *DBClient) DeleteClusterDoc(ctx context.Context, resourceID string, partitionKey string) error {
	doc, err := d.GetClusterDoc(ctx, resourceID, partitionKey)
	if err != nil {
		return err
	}

	container, err := d.client.NewContainer(d.config.DBName, clustersContainer)
	if err != nil {
		return err
	}

	_, err = container.DeleteItem(ctx, azcosmos.NewPartitionKeyString(partitionKey), doc.ID, nil)
	if err != nil {
		return err
	}
	return nil
}

// GetSubscriptionDoc retreives a subscription document from async DB using the subscription ID
func (d *DBClient) GetSubscriptionDoc(ctx context.Context, partitionKey string) (*SubscriptionDocument, error) {
	container, err := d.client.NewContainer(d.config.DBName, subsContainer)
	if err != nil {
		return nil, err
	}

	query := "SELECT * FROM c WHERE c.partitionKey = @partitionKey"
	opt := azcosmos.QueryOptions{
		PageSizeHint:    1,
		QueryParameters: []azcosmos.QueryParameter{{Name: "@partitionKey", Value: partitionKey}},
	}

	pk := azcosmos.NewPartitionKeyString(partitionKey)
	queryPager := container.NewQueryItemsPager(query, pk, &opt)

	var doc *SubscriptionDocument
	for queryPager.More() {
		queryResponse, err := queryPager.NextPage(ctx)
		if err != nil {
			return nil, err
		}

		for _, item := range queryResponse.Items {
			err = json.Unmarshal(item, &doc)
			if err != nil {
				return nil, err
			}
		}
	}
	if doc != nil {
		return doc, nil
	}
	return nil, ErrNotFound
}

// SetClusterDoc creates/updates a subscription document in the async DB during cluster creation/patching
func (d *DBClient) SetSubscriptionDoc(ctx context.Context, doc *SubscriptionDocument) error {
	data, err := json.Marshal(doc)
	if err != nil {
		return err
	}

	container, err := d.client.NewContainer(d.config.DBName, subsContainer)
	if err != nil {
		return err
	}

	_, err = container.UpsertItem(ctx, azcosmos.NewPartitionKeyString(doc.PartitionKey), data, nil)
	if err != nil {
		return err
	}
	return nil
}
