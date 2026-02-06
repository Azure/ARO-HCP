package utils

import (
	"fmt"

	azcorearm "github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"
)

// ParseResourceID parses an Azure resource ID and returns the ResourceID struct
func ParseResourceID(resourceID string) (*azcorearm.ResourceID, error) {
	if resourceID == "" {
		return nil, fmt.Errorf("resourceID cannot be empty")
	}

	parsedID, err := azcorearm.ParseResourceID(resourceID)
	if err != nil {
		return nil, fmt.Errorf("invalid Azure resource ID format: %w", err)
	}

	if parsedID.Name == "" {
		return nil, fmt.Errorf("resource name cannot be empty in resource ID")
	}

	return parsedID, nil
}
