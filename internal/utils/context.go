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

package utils

import (
	"context"
	"fmt"
	"strings"

	"github.com/go-logr/logr"

	azcorearm "github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"
)

type ContextError struct {
	got any
	key contextKey
}

func (c *ContextError) Error() string {
	return fmt.Sprintf(
		"error retrieving value for key %q from context, value obtained was '%v' and type obtained was '%T'",
		c.key,
		c.got,
		c.got)
}

type contextKey int

func (c contextKey) String() string {
	switch c {
	case contextKeyResourceID:
		return "resourceID"
	}
	return "<unknown>"
}

const (
	// Keys for request-scoped data in http.Request contexts
	contextKeyResourceID contextKey = iota
)

func ContextWithLogger(ctx context.Context, logger logr.Logger) context.Context {
	return logr.NewContext(ctx, logger)
}

func LoggerFromContext(ctx context.Context) logr.Logger {
	logger, err := logr.FromContext(ctx)
	if err != nil {
		// Return the default logger as a fail-safe, but log
		// the failure to obtain the logger from the context.
		logger = DefaultLogger()
		logger.Error(err, "failed to get logger from context")
	}
	return logger
}

func ContextWithResourceID(ctx context.Context, resourceID *azcorearm.ResourceID) context.Context {
	return context.WithValue(ctx, contextKeyResourceID, resourceID)
}

func ResourceIDFromContext(ctx context.Context) (*azcorearm.ResourceID, error) {
	resourceID, ok := ctx.Value(contextKeyResourceID).(*azcorearm.ResourceID)
	if !ok {
		err := &ContextError{
			got: resourceID,
			key: contextKeyResourceID,
		}
		return resourceID, err
	}
	return resourceID, nil
}

// LogValues is a slice of key/value pairs for use with logger.WithValues.
// It supports method chaining for a fluent API:
//
//	logger.WithValues(
//	    utils.LogValues{}.
//	        AddOperation(val).
//	        AddResourceGroup(val).
//	        AddResourceName(val)...)
//
// This allows us keep our kusto indexing in sync, allow the evolution of keys, and
// allows for consistent redaction of values.
type LogValues []any

func (lv LogValues) AddAPIVersion(value string) LogValues {
	return append(lv, "api_version", strings.ToLower(value))
}

func (lv LogValues) AddRequestID(value string) LogValues {
	return append(lv, "request_id", value)
}

// AddClientRequestID adds the "client_request_id" key with the lowercased value.
func (lv LogValues) AddClientRequestID(value string) LogValues {
	return append(lv, "client_request_id", value)
}

// AddCloudErrorCode adds the "cloud_error_code" key with the lowercased value.
func (lv LogValues) AddCloudErrorCode(value string) LogValues {
	return append(lv, "cloud_error_code", strings.ToLower(value))
}

// AddCloudErrorMessage adds the "cloud_error_message" key with the lowercased value.
func (lv LogValues) AddCloudErrorMessage(value string) LogValues {
	return append(lv, "cloud_error_message", strings.ToLower(value))
}

// AddControllerName adds the "controller_name" key with the lowercased value.
func (lv LogValues) AddControllerName(value string) LogValues {
	return append(lv, "controller_name", strings.ToLower(value))
}

// AddCorrelationRequestID adds the "correlation_request_id" key with the lowercased value.
func (lv LogValues) AddCorrelationRequestID(value string) LogValues {
	return append(lv, "correlation_request_id", value)
}

// AddCosmosResourceID adds the "cosmos_resource_id" key with the lowercased value.
func (lv LogValues) AddCosmosResourceID(value string) LogValues {
	return append(lv, "cosmos_resource_id", strings.ToLower(value))
}

// AddHCPClusterName adds the "hcp_cluster_name" key with the lowercased value.
func (lv LogValues) AddHCPClusterName(value string) LogValues {
	return append(lv, "hcp_cluster_name", strings.ToLower(value))
}

// AddInternalID adds the "internal_id" key with the lowercased value.
func (lv LogValues) AddInternalID(value string) LogValues {
	return append(lv, "internal_id", strings.ToLower(value))
}

// AddMethod adds the "method" key with the lowercased value.
func (lv LogValues) AddMethod(value string) LogValues {
	return append(lv, "method", strings.ToLower(value))
}

// AddOperation adds the "operation" key with the lowercased value.
func (lv LogValues) AddOperation(value string) LogValues {
	return append(lv, "operation", strings.ToLower(value))
}

// AddOperationID adds the "operation_id" key with the lowercased value.
func (lv LogValues) AddOperationID(value string) LogValues {
	return append(lv, "operation_id", strings.ToLower(value))
}

// AddPath adds the "path" key with the lowercased value.
func (lv LogValues) AddPath(value string) LogValues {
	return append(lv, "path", strings.ToLower(value))
}

// AddResourceGroup adds the "resource_group" key with the lowercased value.
func (lv LogValues) AddResourceGroup(value string) LogValues {
	return append(lv, "resource_group", strings.ToLower(value))
}

// AddResourceID adds the "resource_id" key with the lowercased value.
func (lv LogValues) AddResourceID(value string) LogValues {
	return append(lv, "resource_id", strings.ToLower(value))
}

// AddResourceName adds the "resource_name" key with the lowercased value.
func (lv LogValues) AddResourceName(value string) LogValues {
	return append(lv, "resource_name", strings.ToLower(value))
}

func (lv LogValues) AddResourceType(value string) LogValues {
	return append(lv, "resourceType", strings.ToLower(value))
}

// AddSubscriptionID adds the "subscription_id" key with the lowercased value.
func (lv LogValues) AddSubscriptionID(value string) LogValues {
	return append(lv, "subscription_id", strings.ToLower(value))
}

// Composite helper functions for parsing resource IDs

// hcpClusterNameFromResourceID walks up the resource ID parent chain to find an HCP cluster ancestor.
// Returns the cluster's resource ID string if found, empty string otherwise.
func hcpClusterNameFromResourceID(resourceID *azcorearm.ResourceID) string {
	if resourceID == nil {
		return ""
	}

	// Check if this resource is in our provider namespace
	if !strings.EqualFold(resourceID.ResourceType.Namespace, "Microsoft.RedHatOpenShift") { // can't use constant due to import cycle. need to move functions out of api
		return ""
	}
	// Check if this is an HCP cluster resource type
	if strings.EqualFold(resourceID.ResourceType.Type, "hcpOpenShiftClusters") { // can't use constant due to import cycle. need to move functions out of api
		return resourceID.String()
	}
	// Walk up the parent chain
	return hcpClusterNameFromResourceID(resourceID.Parent)
}

// AddLogValuesForResourceID adds common logging key/value pairs from a resource ID.
// It adds: subscription_id, resource_group, resource_name, resource_id, and hcp_cluster_name (if applicable).
func (lv LogValues) AddLogValuesForResourceID(resourceID *azcorearm.ResourceID) LogValues {
	if resourceID == nil {
		return lv
	}
	lv = lv.AddSubscriptionID(resourceID.SubscriptionID).
		AddResourceGroup(resourceID.ResourceGroupName).
		AddResourceType(resourceID.ResourceType.String()).
		AddResourceName(resourceID.Name).
		AddResourceID(resourceID.String())

	if hcpClusterName := hcpClusterNameFromResourceID(resourceID); hcpClusterName != "" {
		lv = lv.AddHCPClusterName(hcpClusterName)
	}
	return lv
}

// AddLogValuesForResourceIDString parses a resource ID string and adds common logging key/value pairs.
// It adds: subscription_id, resource_group, resource_name, and resource_id.
// If parsing fails, only the resource_id is added.
func (lv LogValues) AddLogValuesForResourceIDString(resourceIDString string) LogValues {
	resourceID, err := azcorearm.ParseResourceID(resourceIDString)
	if err != nil {
		return lv.AddResourceID(resourceIDString)
	}
	return lv.AddLogValuesForResourceID(resourceID)
}
