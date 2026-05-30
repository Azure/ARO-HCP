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

package maestroregistration

import (
	"context"
	"fmt"

	maestroopenapi "github.com/openshift-online/maestro/pkg/api/openapi"
)

type MaestroConsumerClient interface {
	GetConsumer(ctx context.Context, consumerName string) (*maestroopenapi.Consumer, error)
	CreateConsumer(ctx context.Context, consumer maestroopenapi.Consumer) (*maestroopenapi.Consumer, error)
}

type maestroConsumerClient struct {
	api *maestroopenapi.DefaultAPIService
}

func NewMaestroConsumerClient(apiClient *maestroopenapi.APIClient) MaestroConsumerClient {
	return &maestroConsumerClient{
		api: apiClient.DefaultAPI,
	}
}

func (c *maestroConsumerClient) GetConsumer(ctx context.Context, consumerName string) (*maestroopenapi.Consumer, error) {
	search := fmt.Sprintf("name='%s'", consumerName)
	list, _, err := c.api.ApiMaestroV1ConsumersGet(ctx).Search(search).Execute()
	if err != nil {
		return nil, fmt.Errorf("searching for consumer %q: %w", consumerName, err)
	}
	if list.Total == 0 {
		return nil, nil
	}
	return &list.Items[0], nil
}

func (c *maestroConsumerClient) CreateConsumer(ctx context.Context, consumer maestroopenapi.Consumer) (*maestroopenapi.Consumer, error) {
	created, _, err := c.api.ApiMaestroV1ConsumersPost(ctx).Consumer(consumer).Execute()
	if err != nil {
		return nil, fmt.Errorf("creating consumer: %w", err)
	}
	return created, nil
}
