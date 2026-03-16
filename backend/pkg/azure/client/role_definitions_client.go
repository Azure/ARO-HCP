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

	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/authorization/armauthorization/v2"
)

// RoleDefinitionsClient is an interface that defines the methods that we want to use
// from the RoleDefinitionsClient type in the Azure Go SDK
// (https://github.com/Azure/azure-sdk-for-go/tree/main/sdk/resourcemanager/authorization/armauthorization/v2).
// If you need to use a method provided by the Azure Go SDK RoleDefinitionsClient
// but it is not defined in this interface then it has to be added here.
type RoleDefinitionsClient interface {
	// GetByID gets a role definition by role definition resource ID.
	// Use the format /providers/Microsoft.Authorization/roleDefinitions/{roleDefinitionId} for tenant level role definitions.
	GetByID(ctx context.Context, roleDefinitionResourceId string,
		options *armauthorization.RoleDefinitionsClientGetByIDOptions,
	) (armauthorization.RoleDefinitionsClientGetByIDResponse, error)
}

var _ RoleDefinitionsClient = (*armauthorization.RoleDefinitionsClient)(nil)
