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
