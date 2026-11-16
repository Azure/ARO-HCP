// Copyright 2026 Microsoft Corporation
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

// Cosmos discovery reads the account endpoint and container throughput through ARM
// during backend startup to configure storage clients and their rate limits.

package cmd

import (
	"context"
	"fmt"
	"net/url"
	"strings"
	"time"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	azcorearm "github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/cosmos/armcosmos/v4"
)

type databaseAccountsClient interface {
	Get(ctx context.Context, resourceGroupName string, accountName string, options *armcosmos.DatabaseAccountsClientGetOptions) (armcosmos.DatabaseAccountsClientGetResponse, error)
}

var _ databaseAccountsClient = (*armcosmos.DatabaseAccountsClient)(nil)

type sqlResourcesClient interface {
	GetSQLContainerThroughput(ctx context.Context, resourceGroupName string, accountName string, databaseName string, containerName string, options *armcosmos.SQLResourcesClientGetSQLContainerThroughputOptions) (armcosmos.SQLResourcesClientGetSQLContainerThroughputResponse, error)
	GetSQLDatabase(ctx context.Context, resourceGroupName string, accountName string, databaseName string, options *armcosmos.SQLResourcesClientGetSQLDatabaseOptions) (armcosmos.SQLResourcesClientGetSQLDatabaseResponse, error)
}

var _ sqlResourcesClient = (*armcosmos.SQLResourcesClient)(nil)

type CosmosAccountDiscoverer struct {
	id                     *azcorearm.ResourceID
	databaseAccountsClient databaseAccountsClient
	sqlResourcesClient     sqlResourcesClient
}

type DiscoveredAccount struct {
	resourceGroupName  string
	sqlResourcesClient sqlResourcesClient
	DatabaseName       string
	Endpoint           string
}

func ParseCosmosAccountResourceID(resourceID string) (*azcorearm.ResourceID, error) {
	id, err := azcorearm.ParseResourceID(resourceID)
	if err != nil {
		return nil, fmt.Errorf("invalid Cosmos account resource ID: %w", err)
	}
	if id.SubscriptionID == "" || id.ResourceGroupName == "" || id.Name == "" ||
		!strings.EqualFold(id.ResourceType.String(), "Microsoft.DocumentDB/databaseAccounts") ||
		len(strings.Split(resourceID, "/")) != 9 || strings.ContainsAny(resourceID, "?#") {
		return nil, fmt.Errorf("expected a subscription/resource-group scoped Cosmos database account resource ID, got %q", resourceID)
	}
	return id, nil
}

func NewCosmosAccountDiscoverer(resourceID *azcorearm.ResourceID, credential azcore.TokenCredential, options *azcorearm.ClientOptions) (*CosmosAccountDiscoverer, error) {
	databaseAccountsClient, err := armcosmos.NewDatabaseAccountsClient(resourceID.SubscriptionID, credential, options)
	if err != nil {
		return nil, err
	}
	sqlResourcesClient, err := armcosmos.NewSQLResourcesClient(resourceID.SubscriptionID, credential, options)
	if err != nil {
		return nil, err
	}
	return &CosmosAccountDiscoverer{id: resourceID, databaseAccountsClient: databaseAccountsClient, sqlResourcesClient: sqlResourcesClient}, nil
}

func (account *CosmosAccountDiscoverer) Discover(ctx context.Context) (*DiscoveredAccount, error) {
	ctx, cancel := context.WithTimeout(ctx, time.Minute)
	defer cancel()
	response, err := account.databaseAccountsClient.Get(ctx, account.id.ResourceGroupName, account.id.Name, nil)
	if err != nil {
		return nil, fmt.Errorf("get Cosmos account %s: %w", account.id, err)
	}
	if response.Name == nil || *response.Name == "" || !strings.EqualFold(*response.Name, account.id.Name) {
		return nil, fmt.Errorf("cosmos account response must identify account %q", account.id.Name)
	}
	if response.ID == nil || !strings.EqualFold(*response.ID, account.id.String()) {
		return nil, fmt.Errorf("cosmos account response must match resource ID %s", account.id)
	}
	accountName := *response.Name
	if response.Properties == nil || response.Properties.DocumentEndpoint == nil {
		return nil, fmt.Errorf("cosmos account %s has no document endpoint", account.id)
	}
	endpoint := *response.Properties.DocumentEndpoint
	parsed, err := url.Parse(endpoint)
	if err != nil || parsed.Scheme != "https" || parsed.Host == "" || parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" {
		return nil, fmt.Errorf("cosmos account %s has an invalid document endpoint", account.id)
	}
	result, err := account.sqlResourcesClient.GetSQLDatabase(ctx, account.id.ResourceGroupName, accountName, accountName, nil)
	if err != nil {
		return nil, fmt.Errorf("get Cosmos database %s (must match account name): %w", accountName, err)
	}
	if result.Properties == nil || result.Properties.Resource == nil || result.Properties.Resource.ID == nil || *result.Properties.Resource.ID != accountName {
		return nil, fmt.Errorf("cosmos database must match account name %q", accountName)
	}
	return &DiscoveredAccount{resourceGroupName: account.id.ResourceGroupName, sqlResourcesClient: account.sqlResourcesClient, DatabaseName: *result.Properties.Resource.ID, Endpoint: endpoint}, nil
}

func (account *DiscoveredAccount) ContainerThroughput(ctx context.Context, container string) (float64, error) {
	ctx, cancel := context.WithTimeout(ctx, time.Minute)
	defer cancel()
	result, err := account.sqlResourcesClient.GetSQLContainerThroughput(ctx, account.resourceGroupName, account.DatabaseName, account.DatabaseName, container, nil)
	if err != nil {
		return 0, fmt.Errorf("get Cosmos container %q throughput: %w", container, err)
	}
	if result.Properties == nil || result.Properties.Resource == nil {
		return 0, fmt.Errorf("cosmos container %q has no positive configured throughput", container)
	}
	resource := result.Properties.Resource
	throughput := resource.Throughput
	// For autoscale containers, budget against the configured maximum RU/s.
	// For manual throughput containers, use the provisioned RU/s.
	// Manual throughput is fixed; autoscale throughput varies with demand up to its configured maximum.
	if resource.AutoscaleSettings != nil {
		throughput = resource.AutoscaleSettings.MaxThroughput
	}
	if throughput != nil && *throughput > 0 {
		return float64(*throughput), nil
	}
	return 0, fmt.Errorf("cosmos container %q has no positive configured throughput", container)
}
