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

//go:generate $MOCKGEN -typed -source=role_assignments_client.go -destination=mock_role_assignments_client.go -package client RoleAssignmentsClient

import (
	"context"

	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/authorization/armauthorization/v2"
)

// RoleAssignmentsClient is an interface that mirrors armauthorization.RoleAssignmentsClient.
// (https://github.com/Azure/azure-sdk-for-go/tree/main/sdk/resourcemanager/authorization/armauthorization/v2).
// Only the methods the backend needs are declared. If the backend needs more of the
// SDK client's methods, add them here to keep parity.
type RoleAssignmentsClient interface {
	// GetByID gets a role assignment by its fully qualified role assignment resource ID
	// (for example "{scope}/providers/Microsoft.Authorization/roleAssignments/{name}").
	GetByID(ctx context.Context, roleAssignmentID string, options *armauthorization.RoleAssignmentsClientGetByIDOptions) (armauthorization.RoleAssignmentsClientGetByIDResponse, error)
	Create(ctx context.Context, scope string, roleAssignmentName string, parameters armauthorization.RoleAssignmentCreateParameters, options *armauthorization.RoleAssignmentsClientCreateOptions) (armauthorization.RoleAssignmentsClientCreateResponse, error)
}

var _ RoleAssignmentsClient = (*armauthorization.RoleAssignmentsClient)(nil)
