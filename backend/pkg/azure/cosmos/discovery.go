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
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/cosmos/armcosmos/v4"
)

type accountClient interface {
	Get(context.Context, string, string, *armcosmos.DatabaseAccountsClientGetOptions) (armcosmos.DatabaseAccountsClientGetResponse, error)
}

type sqlClient interface {
	GetSQLDatabase(context.Context, string, string, string, *armcosmos.SQLResourcesClientGetSQLDatabaseOptions) (armcosmos.SQLResourcesClientGetSQLDatabaseResponse, error)
	GetSQLContainerThroughput(context.Context, string, string, string, string, *armcosmos.SQLResourcesClientGetSQLContainerThroughputOptions) (armcosmos.SQLResourcesClientGetSQLContainerThroughputResponse, error)
}

type Account struct {
	id       *arm.ResourceID
	accounts accountClient
	sql      sqlClient
}

func ParseAccountID(value string) (*arm.ResourceID, error) {
	id, err := arm.ParseResourceID(value)
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

func NewAccount(value string, credential azcore.TokenCredential, options *arm.ClientOptions) (*Account, error) {
	id, err := ParseAccountID(value)
	if err != nil {
		return nil, err
	}
	accounts, err := armcosmos.NewDatabaseAccountsClient(id.SubscriptionID, credential, options)
	if err != nil {
		return nil, err
	}
	sql, err := armcosmos.NewSQLResourcesClient(id.SubscriptionID, credential, options)
	if err != nil {
		return nil, err
	}
	return &Account{id: id, accounts: accounts, sql: sql}, nil
}

func (account *Account) Discover(ctx context.Context) (endpoint, database string, err error) {
	ctx, cancel := context.WithTimeout(ctx, time.Minute)
	defer cancel()
	response, err := account.accounts.Get(ctx, account.id.ResourceGroupName, account.id.Name, nil)
	if err != nil {
		return "", "", fmt.Errorf("get Cosmos account %s: %w", account.id, err)
	}
	if response.Properties == nil || response.Properties.DocumentEndpoint == nil {
		return "", "", fmt.Errorf("Cosmos account %s has no document endpoint", account.id)
	}
	endpoint = *response.Properties.DocumentEndpoint
	parsed, err := url.Parse(endpoint)
	if err != nil || parsed.Scheme != "https" || parsed.Host == "" || parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" {
		return "", "", fmt.Errorf("Cosmos account %s has an invalid document endpoint", account.id)
	}
	result, err := account.sql.GetSQLDatabase(ctx, account.id.ResourceGroupName, account.id.Name, account.id.Name, nil)
	if err != nil {
		return "", "", fmt.Errorf("get Cosmos database %s (must match account name): %w", account.id.Name, err)
	}
	if result.Properties == nil || result.Properties.Resource == nil || result.Properties.Resource.ID == nil || *result.Properties.Resource.ID != account.id.Name {
		return "", "", fmt.Errorf("Cosmos database must match account name %q", account.id.Name)
	}
	return endpoint, *result.Properties.Resource.ID, nil
}

func (account *Account) ContainerThroughput(ctx context.Context, container string) (float64, error) {
	ctx, cancel := context.WithTimeout(ctx, time.Minute)
	defer cancel()
	result, err := account.sql.GetSQLContainerThroughput(ctx, account.id.ResourceGroupName, account.id.Name, account.id.Name, container, nil)
	if err != nil {
		return 0, fmt.Errorf("get Cosmos container %q throughput: %w", container, err)
	}
	if result.Properties != nil && result.Properties.Resource != nil {
		resource := result.Properties.Resource
		throughput := resource.Throughput
		if resource.AutoscaleSettings != nil {
			throughput = resource.AutoscaleSettings.MaxThroughput
		}
		if throughput != nil && *throughput > 0 {
			return float64(*throughput), nil
		}
	}
	return 0, fmt.Errorf("Cosmos container %q has no positive configured throughput", container)
}
