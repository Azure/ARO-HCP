// Copyright 2025 Microsoft Corporation
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

package common

import (
	"fmt"
	"regexp"
	"strings"

	azcorearm "github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"
)

var (
	// Common regex patterns
	clusterIDRegex = regexp.MustCompile(`^[a-z0-9]{32}$`)
)

const (
	azureRedHatOpenShiftProvider = "Microsoft.RedHatOpenShift"
	azureHCPResourceType         = "hcpOpenShiftClusters"
)

// ValidateClusterID validates that a string matches the expected cluster ID format
func ValidateClusterID(clusterID string) error {
	if clusterID == "" {
		return fmt.Errorf("clusterID cannot be empty")
	}
	if !clusterIDRegex.MatchString(clusterID) {
		return fmt.Errorf("clusterID must be 32 characters of lowercase letters and digits")
	}
	return nil
}

// ValidateAzureResourceID validates an Azure resource ID and ensures it's an HCP cluster resource
func ValidateAzureResourceID(resourceID string) (*azcorearm.ResourceID, error) {
	if resourceID == "" {
		return nil, fmt.Errorf("resourceID cannot be empty")
	}

	parsedID, err := azcorearm.ParseResourceID(resourceID)
	if err != nil {
		return nil, fmt.Errorf("invalid Azure resource ID format: %w", err)
	}

	expectedFullType := fmt.Sprintf("%s/%s", azureRedHatOpenShiftProvider, azureHCPResourceType)
	if !strings.EqualFold(parsedID.ResourceType.String(), expectedFullType) {
		return nil, fmt.Errorf("invalid Azure resource type: expected '%s', got '%s'", expectedFullType, parsedID.ResourceType.String())
	}

	if parsedID.Name == "" {
		return nil, fmt.Errorf("cluster name cannot be empty in resource ID")
	}

	return parsedID, nil
}

type HCPIdentifier struct {
	ClusterID  string                // Direct cluster ID (if provided)
	ResourceID *azcorearm.ResourceID // Azure resource ID (if provided)
}

// ParseHCPIdentifier parses a cluster identifier which can be either:
// 1. A cluster ID (e.g., "2jesjug41iavg27inj078ssjidn20clk")
// 2. An Azure resource ID (e.g., "/subscriptions/.../providers/Microsoft.RedHatOpenShift/hcpOpenShiftClusters/my-cluster-name")
// Returns a parsed identifier with either ClusterID or ResourceID populated.
func ParseHCPIdentifier(identifier string) (*HCPIdentifier, error) {
	if strings.TrimSpace(identifier) == "" {
		return nil, fmt.Errorf("HCP identifier cannot be empty")
	}

	switch {
	case strings.HasPrefix(identifier, "/subscriptions/"):
		resourceID, err := ValidateAzureResourceID(identifier)
		if err != nil {
			return nil, err
		}
		return &HCPIdentifier{ResourceID: resourceID}, nil

	case clusterIDRegex.MatchString(identifier):
		if err := ValidateClusterID(identifier); err != nil {
			return nil, err
		}
		return &HCPIdentifier{ClusterID: identifier}, nil

	default:
		return nil, fmt.Errorf("identifier must be either a valid cluster ID or a valid Azure resource ID")
	}
}
