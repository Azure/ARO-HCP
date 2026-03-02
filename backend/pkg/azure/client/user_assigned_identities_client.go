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

	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/msi/armmsi"
)

// UserAssignedIdentitiesClient is an interface that defines the
// methods that we want to use from the UserAssignedIdentitiesClient type in
// the Azure Go SDK (https://github.com/Azure/azure-sdk-for-go/tree/main/sdk/resourcemanager/msi/armmsi).
// The aim is to only contain methods that are defined in the Azure Go SDK
// UserAssignedIdentitiesClient client.
// If you need to use a method provided by the Azure Go SDK UserAssignedIdentitiesClient
// client but it is not defined in this interface then it has to be added here and all
// the types implementing this interface have to implement the new method.
type UserAssignedIdentitiesClient interface {
	CreateOrUpdate(ctx context.Context,
		resourceGroupName string, resourceName string,
		parameters armmsi.Identity,
		options *armmsi.UserAssignedIdentitiesClientCreateOrUpdateOptions,
	) (armmsi.UserAssignedIdentitiesClientCreateOrUpdateResponse, error)

	Delete(ctx context.Context,
		resourceGroupName string, resourceName string,
		options *armmsi.UserAssignedIdentitiesClientDeleteOptions,
	) (armmsi.UserAssignedIdentitiesClientDeleteResponse, error)

	Get(ctx context.Context,
		resourceGroupName string, resourceName string,
		options *armmsi.UserAssignedIdentitiesClientGetOptions,
	) (armmsi.UserAssignedIdentitiesClientGetResponse, error)
}

// interface guard to ensure that all methods defined in the UserAssignedIdentitiesClient
// interface are implemented by the real Azure Go SDK UserAssignedIdentitiesClient
// client. This interface guard should always compile
var _ UserAssignedIdentitiesClient = (*armmsi.UserAssignedIdentitiesClient)(nil)
