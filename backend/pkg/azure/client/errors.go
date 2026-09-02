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
	"errors"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
)

// IsResourceGroupNotFoundErr is used to determine if we are failing to find a resource group within azure.
func IsResourceGroupNotFoundErr(err error) bool {
	var azErr *azcore.ResponseError
	return errors.As(err, &azErr) && azErr.ErrorCode == "ResourceGroupNotFound"
}

// IsResourceNotFoundErr is used to determine if we are failing to find a resource within azure.
// *WARNING* Not all azure API operations return the `ResourceNotFound` error code when the resource
// is not found, and more specific error codes are returned for some of them e.g `RoleAssignmentNotFound`
// is returned when a role assignement is not found
func IsResourceNotFoundErr(err error) bool {
	var azErr *azcore.ResponseError
	return errors.As(err, &azErr) && azErr.ErrorCode == "ResourceNotFound"
}

// IsRoleAssignmentNotFoundErr is used to determine if we are failing to find a role assignment
// within azure. The role assignments API returns the more specific `RoleAssignmentNotFound`
// error code (not `ResourceNotFound`) when a role assignment does not exist.
func IsRoleAssignmentNotFoundErr(err error) bool {
	var azErr *azcore.ResponseError
	return errors.As(err, &azErr) && azErr.ErrorCode == "RoleAssignmentNotFound"
}

// IsRoleAssignmentAlreadyExistsErr is used to determine if a role assignment create failed
// because an equivalent role assignment (same principal + role definition at the same scope)
// already exists. Azure returns the `RoleAssignmentExists` error code in that case. Because
// the managed-resource-group-scoped role assignment names are deterministic and Cluster
// Service creates the same assignments in parallel, a create racing with Cluster Service (or
// a retried create) is expected and should be treated as success.
func IsRoleAssignmentAlreadyExistsErr(err error) bool {
	var azErr *azcore.ResponseError
	return errors.As(err, &azErr) && azErr.ErrorCode == "RoleAssignmentExists"
}
