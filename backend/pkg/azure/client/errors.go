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
