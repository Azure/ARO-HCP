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

// Package cluster provides functionality for validating cluster identifiers and Azure resource IDs.
package cluster

import (
	"fmt"
	"regexp"
	"strings"

	azcorearm "github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"
)

var (
	// clusterIDRegex validates cluster ID format - 32 characters of lowercase letters and digits
	clusterIDRegex = regexp.MustCompile(`^[a-z0-9]{32}$`)
)

const (
	// Azure resource constants
	azureRedHatOpenShiftProvider = "Microsoft.RedHatOpenShift"
	azureHCPResourceType         = "hcpOpenShiftClusters"
)

// ValidationError represents a validation error with context
type ValidationError struct {
	Field   string
	Value   string
	Message string
	Cause   error
}

func (e *ValidationError) Error() string {
	if e.Cause != nil {
		return fmt.Sprintf("validation failed for field '%s' with value '%s': %s (caused by: %v)", e.Field, e.Value, e.Message, e.Cause)
	}
	return fmt.Sprintf("validation failed for field '%s' with value '%s': %s", e.Field, e.Value, e.Message)
}

func (e *ValidationError) Unwrap() error {
	return e.Cause
}

// NewValidationError creates a new validation error
func NewValidationError(field, value, message string, cause error) *ValidationError {
	return &ValidationError{
		Field:   field,
		Value:   value,
		Message: message,
		Cause:   cause,
	}
}

// ParsedIdentifier represents the result of parsing a cluster identifier
type ParsedIdentifier struct {
	ClusterID  string                // Direct cluster ID (if provided)
	ResourceID *azcorearm.ResourceID // Azure resource ID (if provided)
}

// ParseClusterIdentifier parses a cluster identifier which can be either:
// 1. A cluster ID (e.g., "2jesjug41iavg27inj078ssjidn20clk")
// 2. An Azure resource ID (e.g., "/subscriptions/.../providers/Microsoft.RedHatOpenShift/hcpOpenShiftClusters/my-cluster-name")
// Returns a ParsedIdentifier with either ClusterID or ResourceID populated.
// Note: Empty identifiers are not allowed - caller should handle this case.
func ParseClusterIdentifier(identifier string) (*ParsedIdentifier, error) {
	switch {
	case strings.HasPrefix(identifier, "/subscriptions/"):
		// Looks like Azure resource ID - use Azure SDK to parse
		parsedID, err := azcorearm.ParseResourceID(identifier)
		if err != nil {
			return nil, fmt.Errorf("invalid Azure resource ID: %w", err)
		}

		// Validate it's the correct type
		expectedFullType := fmt.Sprintf("%s/%s", azureRedHatOpenShiftProvider, azureHCPResourceType)
		if !strings.EqualFold(parsedID.ResourceType.String(), expectedFullType) {
			return nil, fmt.Errorf("invalid Azure resource type: expected '%s', got '%s'", expectedFullType, parsedID.ResourceType.String())
		}

		if parsedID.Name == "" {
			return nil, fmt.Errorf("cluster name cannot be empty in resource ID")
		}

		return &ParsedIdentifier{ResourceID: parsedID}, nil

	case clusterIDRegex.MatchString(identifier):
		// Matches cluster ID pattern
		return &ParsedIdentifier{ClusterID: identifier}, nil

	default:
		return nil, fmt.Errorf("identifier '%s' is neither a valid cluster ID nor a valid Azure resource ID", identifier)
	}
}
