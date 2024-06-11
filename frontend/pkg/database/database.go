package database

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/Azure/azure-sdk-for-go/sdk/azidentity"
	"github.com/Azure/azure-sdk-for-go/sdk/data/azcosmos"
)

const (
	clustersContainer  = "Clusters"
	nodePoolsContainer = "NodePools"
	subsContainer      = "Subscriptions"
	billingContainer   = "Billing"
	asyncContainer     = "AsyncOperations"
)

var ErrNotFound = errors.New("DocumentNotFound")

// DBClient is a document store for frontend to perform required CRUD operations against
type DBClient interface {
	// DBConnectionTest is used to health check the database. If the database is not reachable or otherwise not ready
	// to be used, an error should be returned.
	DBConnectionTest(ctx context.Context) error

	// GetClusterDoc retrieves an HCPOpenShiftClusterDocument from the database given its resourceID and containing
	// subscriptionID. ErrNotFound is returned if an associated HCPOpenShiftClusterDocument cannot be found.
	GetClusterDoc(ctx context.Context, resourceID string, subscriptionID string) (*HCPOpenShiftClusterDocument, error)
	SetClusterDoc(ctx context.Context, doc *HCPOpenShiftClusterDocument) error
	// DeleteClusterDoc deletes an HCPOpenShiftClusterDocument from the database given the resourceID and containing
	// subscriptionID of a Microsoft.RedHatOpenshift/HcpOpenShiftClusters resource.
	DeleteClusterDoc(ctx context.Context, resourceID string, subscriptionID string) error

	GetNodePoolDoc(ctx context.Context, resourceID string) (*NodePoolDocument, error)
	SetNodePoolDoc(ctx context.Context, doc *NodePoolDocument) error
	DeleteNodePoolDoc(ctx context.Context, resourceID string) error

	// GetSubscriptionDoc retrieves a SubscriptionDocument from the database given the subscriptionID.
	// ErrNotFound is returned if an associated SubscriptionDocument cannot be found.
	GetSubscriptionDoc(ctx context.Context, subscriptionID string) (*SubscriptionDocument, error)
	SetSubscriptionDoc(ctx context.Context, doc *SubscriptionDocument) error
}

var _ DBClient = &CosmosDBClient{}

// CosmosDBClient defines the needed values to perform CRUD operations against the async DB
type CosmosDBClient struct {
	client *azcosmos.Client
	config *CosmosDBConfig
}

// CosmosDBConfig stores database and client configuration data
type CosmosDBConfig struct {
	DBName        string
	DBUrl         string
	ClientOptions *azidentity.DefaultAzureCredentialOptions
}

// NewCosmosDBConfig configures database configuration values for access
func NewCosmosDBConfig(dbName, dbURL string) *CosmosDBConfig {
	opt := &azidentity.DefaultAzureCredentialOptions{}
	c := &CosmosDBConfig{
		DBName:        dbName,
		DBUrl:         dbURL,
		ClientOptions: opt,
	}
	return c
}

// NewCosmosDBClient instantiates a Cosmos DatabaseClient targeting Frontends async DB
func NewCosmosDBClient(config *CosmosDBConfig) (DBClient, error) {
	cred, err := azidentity.NewDefaultAzureCredential(config.ClientOptions)
	if err != nil {
		return nil, err
	}

	d := &CosmosDBClient{
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
func (d *CosmosDBClient) DBConnectionTest(ctx context.Context) error {
	database, err := d.client.NewDatabase(d.config.DBName)
	if err != nil {
		return fmt.Errorf("failed to create Cosmos database client during healthcheck: %v", err)
	}

	if _, err := database.Read(ctx, nil); err != nil {
		return fmt.Errorf("failed to read Cosmos database information during healthcheck: %v", err)
	}

	return nil
}

// GetClusterDoc retrieves a cluster document from async DB using resource ID
func (d *CosmosDBClient) GetClusterDoc(ctx context.Context, resourceID string, subscriptionID string) (*HCPOpenShiftClusterDocument, error) {
	container, err := d.client.NewContainer(d.config.DBName, clustersContainer)
	if err != nil {
		return nil, err
	}

	query := "SELECT * FROM c WHERE c.key = @key"
	opt := azcosmos.QueryOptions{
		PageSizeHint:    1,
		QueryParameters: []azcosmos.QueryParameter{{Name: "@key", Value: resourceID}},
	}

	pk := azcosmos.NewPartitionKeyString(subscriptionID)
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
func (d *CosmosDBClient) SetClusterDoc(ctx context.Context, doc *HCPOpenShiftClusterDocument) error {
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

// DeleteClusterDoc removes a cluster document from the async DB using resource ID
func (d *CosmosDBClient) DeleteClusterDoc(ctx context.Context, resourceID string, subscriptionID string) error {
	doc, err := d.GetClusterDoc(ctx, resourceID, subscriptionID)
	if err != nil {
		if errors.Is(err, ErrNotFound) {
			return nil
		}
		return fmt.Errorf("while attempting to delete the cluster, failed to get cluster document: %w", err)
	}

	container, err := d.client.NewContainer(d.config.DBName, clustersContainer)
	if err != nil {
		return err
	}

	_, err = container.DeleteItem(ctx, azcosmos.NewPartitionKeyString(subscriptionID), doc.ID, nil)
	if err != nil {
		return err
	}
	return nil
}

func (d *CosmosDBClient) GetNodePoolDoc(ctx context.Context, resourceID string) (*NodePoolDocument, error) {
	panic("implement me")
}

func (d *CosmosDBClient) SetNodePoolDoc(ctx context.Context, doc *NodePoolDocument) error {
	panic("implement me")
}

// DeleteNodePoolDoc removes a NodePool document from the DB using resource ID
func (d *CosmosDBClient) DeleteNodePoolDoc(ctx context.Context, resourceID string) error {
	panic("implement me")
}

// GetSubscriptionDoc retreives a subscription document from async DB using the subscription ID
func (d *CosmosDBClient) GetSubscriptionDoc(ctx context.Context, subscriptionID string) (*SubscriptionDocument, error) {
	container, err := d.client.NewContainer(d.config.DBName, subsContainer)
	if err != nil {
		return nil, err
	}

	query := "SELECT * FROM c WHERE c.partitionKey = @partitionKey"
	opt := azcosmos.QueryOptions{
		PageSizeHint:    1,
		QueryParameters: []azcosmos.QueryParameter{{Name: "@partitionKey", Value: subscriptionID}},
	}

	pk := azcosmos.NewPartitionKeyString(subscriptionID)
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

// SetSubscriptionDoc creates/updates a subscription document in the async DB during cluster creation/patching
func (d *CosmosDBClient) SetSubscriptionDoc(ctx context.Context, doc *SubscriptionDocument) error {
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
