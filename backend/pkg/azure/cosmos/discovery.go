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

package cosmos

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
	Get(context.Context, string, string, *armcosmos.DatabaseAccountsClientGetOptions) (armcosmos.DatabaseAccountsClientGetResponse, error)
}

type sqlResourcesClient interface {
	GetSQLDatabase(context.Context, string, string, string, *armcosmos.SQLResourcesClientGetSQLDatabaseOptions) (armcosmos.SQLResourcesClientGetSQLDatabaseResponse, error)
}

type Account struct {
	id                     *azcorearm.ResourceID
	databaseAccountsClient databaseAccountsClient
	sqlResourcesClient     sqlResourcesClient
}

type DiscoveredAccount struct {
	resourceGroupName  string
	sqlResourcesClient sqlResourcesClient
	databaseName       string
	endpoint           string
}

func (account *DiscoveredAccount) Endpoint() string { return account.endpoint }

func (account *DiscoveredAccount) DatabaseName() string { return account.databaseName }

func ParseAccountID(value string) (*azcorearm.ResourceID, error) {
	id, err := azcorearm.ParseResourceID(value)
	if err != nil {
		return nil, fmt.Errorf("invalid Cosmos account resource ID: %w", err)
	}
	if id.SubscriptionID == "" || id.ResourceGroupName == "" || id.Name == "" ||
		!strings.EqualFold(id.ResourceType.String(), "Microsoft.DocumentDB/databaseAccounts") ||
		len(strings.Split(value, "/")) != 9 || strings.ContainsAny(value, "?#") {
		return nil, fmt.Errorf("expected a subscription/resource-group scoped Cosmos database account resource ID, got %q", value)
	}
	return id, nil
}

func NewAccount(value string, credential azcore.TokenCredential, options *azcorearm.ClientOptions) (*Account, error) {
	id, err := ParseAccountID(value)
	if err != nil {
		return nil, err
	}
	databaseAccountsClient, err := armcosmos.NewDatabaseAccountsClient(id.SubscriptionID, credential, options)
	if err != nil {
		return nil, err
	}
	sqlResourcesClient, err := armcosmos.NewSQLResourcesClient(id.SubscriptionID, credential, options)
	if err != nil {
		return nil, err
	}
	return &Account{id: id, databaseAccountsClient: databaseAccountsClient, sqlResourcesClient: sqlResourcesClient}, nil
}

func (account *Account) Discover(ctx context.Context) (*DiscoveredAccount, error) {
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
	return &DiscoveredAccount{resourceGroupName: account.id.ResourceGroupName, sqlResourcesClient: account.sqlResourcesClient, databaseName: *result.Properties.Resource.ID, endpoint: endpoint}, nil
}
