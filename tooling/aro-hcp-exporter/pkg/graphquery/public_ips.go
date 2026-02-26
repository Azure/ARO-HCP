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

package graphquery

import (
	"context"
	"fmt"

	"github.com/go-viper/mapstructure/v2"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/resourcegraph/armresourcegraph"
)

// ResourceGraphClient is a client for the Azure Resource Graph API
type ResourceGraphClient struct {
	client          *armresourcegraph.Client
	subscriptionIDs []*string
}

// ResourceGraphRequest is a request for the Azure Resource Graph API
// Query is the query to execute
// Output is the type to convert the result to
type ResourceGraphRequest struct {
	Query  *string
	Output any
}

func NewResourceGraphClient(credential azcore.TokenCredential, subscriptionIDs []*string) (*ResourceGraphClient, error) {
	client, err := armresourcegraph.NewClient(credential, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create Resource Graph client: %w", err)
	}
	return &ResourceGraphClient{client: client, subscriptionIDs: subscriptionIDs}, nil
}

// ExecuteConvertRequest executes a Resource Graph request and converts the result to the type specified in the request
func (c *ResourceGraphClient) ExecuteConvertRequest(ctx context.Context, request ResourceGraphRequest) error {
	result, err := c.client.Resources(ctx,
		armresourcegraph.QueryRequest{
			Query:         request.Query,
			Subscriptions: c.subscriptionIDs,
		},
		nil,
	)
	if err != nil {
		return fmt.Errorf("failed to execute Resource Graph request: %w", err)
	}

	err = mapstructure.Decode(result.Data, &request.Output)
	if err != nil {
		return fmt.Errorf("failed to decode Resource Graph result: %w", err)
	}
	return nil
}
