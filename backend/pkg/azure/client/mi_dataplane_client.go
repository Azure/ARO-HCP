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

	"github.com/Azure/msi-dataplane/pkg/dataplane"
)

// ManagedIdentitiesDataplaneClient is the interface to interact with Azure's Managed Identity
// Data Plane service. The Managed Identities Data Plane service is only
// available in Azure tenants where Microsoft's First Party Application (FPA)
// integration is available. For the environments where the FPA integration is not available
// we cannot communicate with the Managed Identities Data Plane service, so
// instead we use a mock implementation of the ManagedIdentitiesDataplaneClient that
// always returns a single Azure Service Principal identity representing
// a Managed Identity. This mock implementation and details on it can be found
// in the hardcodedIdentityManagedIdentitiesDataplaneClient Go type.
// This client is different than Azure Go SDK's armmsi.UserAssignedIdentitiesClient/armmsiSystemAssignedIdentitiesClient
// clients, which are used to interact with the control plane side of the Managed Identities service.
type ManagedIdentitiesDataplaneClient interface {
	GetUserAssignedIdentitiesCredentials(
		ctx context.Context, request dataplane.UserAssignedIdentitiesRequest,
	) (*dataplane.ManagedIdentityCredentials, error)
}

var _ ManagedIdentitiesDataplaneClient = (dataplane.Client)(nil)
