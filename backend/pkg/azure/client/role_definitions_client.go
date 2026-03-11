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

package client

import (
	"context"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/authorization/armauthorization/v2"
	"github.com/go-logr/logr"
)

type azureRoleDefinitionsAPIClient interface {
	GetByID(ctx context.Context, roleID string, options *armauthorization.RoleDefinitionsClientGetByIDOptions) (armauthorization.RoleDefinitionsClientGetByIDResponse, error)
}

var _ azureRoleDefinitionsAPIClient = (*armauthorization.RoleDefinitionsClient)(nil)

// RoleDefinitionsClient contains the GetByID method for the RoleDefinitions group.
//
//go:generate $MOCKGEN -source=role_definitions_client.go -package=client -destination=mock_role_definitions_client.go
type RoleDefinitionsClient interface {
	// GetByID gets a role definition by role definition resource ID.
	// If the operation fails it returns an *azcore.ResponseError type.
	//
	//   - roleDefinitionResourceId - The fully qualified role definition resource ID. Use the format
	//     /providers/Microsoft.Authorization/roleDefinitions/{roleDefinitionId} for tenant level role definitions.
	//   - options - RoleDefinitionsClientGetByIDOptions contains the optional parameters for the
	//     RoleDefinitionsClient.GetByID method.
	GetByID(ctx context.Context, roleDefinitionResourceId string,
		options *armauthorization.RoleDefinitionsClientGetByIDOptions,
	) (armauthorization.RoleDefinitionsClientGetByIDResponse, error)
}

type roleDefinitionsClient struct {
	client azureRoleDefinitionsAPIClient
	logger logr.Logger
}

// NewRoleDefinitionsClient creates a new RoleDefinitionsClient.
func NewRoleDefinitionsClient(logger logr.Logger, credential azcore.TokenCredential,
	options *arm.ClientOptions) (RoleDefinitionsClient, error) {
	client, err := armauthorization.NewRoleDefinitionsClient(credential, options)
	if err != nil {
		return nil, err
	}

	return &roleDefinitionsClient{
		client: client,
		logger: logger,
	}, nil
}

func (c *roleDefinitionsClient) GetByID(ctx context.Context, roleDefinitionResourceId string,
	options *armauthorization.RoleDefinitionsClientGetByIDOptions) (armauthorization.RoleDefinitionsClientGetByIDResponse, error) {
	if c.logger.GetSink() != nil {
		c.logger.Info("Getting role definition", "roleDefinitionResourceId", roleDefinitionResourceId)
	}
	response, err := c.client.GetByID(ctx, roleDefinitionResourceId, options)
	if err != nil {
		return armauthorization.RoleDefinitionsClientGetByIDResponse{}, err
	}

	return response, nil
}
