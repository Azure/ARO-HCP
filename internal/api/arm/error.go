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

package arm

import (
	"encoding/json"
	"fmt"
	"net/http"

	azcorearm "github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"
)

// CloudError codes
const (
	CloudErrorCodeInternalServerError      = "InternalServerError"
	CloudErrorCodeInvalidParameter         = "InvalidParameter"
	CloudErrorCodeInvalidRequestContent    = "InvalidRequestContent"
	CloudErrorCodeInvalidResource          = "InvalidResource"
	CloudErrorCodeInvalidResourceType      = "InvalidResourceType"
	CloudErrorCodeMultipleErrorsOccurred   = "MultipleErrorsOccurred"
	CloudErrorCodeUnsupportedMediaType     = "UnsupportedMediaType"
	CloudErrorCodeCanceled                 = "Canceled"
	CloudErrorCodeConflict                 = "Conflict"
	CloudErrorCodeNotFound                 = "NotFound"
	CloudErrorCodeInvalidSubscriptionState = "InvalidSubscriptionState"
	CloudErrorCodeSubscriptionNotFound     = "SubscriptionNotFound"
	CloudErrorCodeResourceNotFound         = "ResourceNotFound"
	CloudErrorCodeResourceGroupNotFound    = "ResourceGroupNotFound"
	CloudErrorCodeInvalidSubscriptionID    = "InvalidSubscriptionID"
	CloudErrorCodeInvalidResourceName      = "InvalidResourceName"
	CloudErrorCodeInvalidResourceGroupName = "InvalidResourceGroupName"
)

// CloudError represents a complete resource provider error.
type CloudError struct {
	// The HTTP status code
	StatusCode int `json:"-"`

	// The response body to be converted to JSON
	*CloudErrorBody `json:"error,omitempty"`
}

func (err *CloudError) Error() string {
	var body string

	if err.CloudErrorBody != nil {
		body = ": " + err.String()
	}

	return fmt.Sprintf("%d%s", err.StatusCode, body)
}

// CloudErrorBody represents the structure of the response body for a resource provider error.
// See https://github.com/cloud-and-ai-microsoft/resource-provider-contract/blob/master/v1.0/common-api-details.md#error-response-content
type CloudErrorBody struct {
	// An identifier for the error. Codes are invariant and are intended to be consumed programmatically.
	Code string `json:"code,omitempty"`

	// A message describing the error, intended to be suitable for display in a user interface.
	Message string `json:"message,omitempty"`

	// The target of the particular error. For example, the name of the property in error.
	Target string `json:"target,omitempty"`

	// A list of additional details about the error.
	Details []CloudErrorBody `json:"details,omitempty"`
}

func (body *CloudErrorBody) String() string {
	out := fmt.Sprintf("%s: ", body.Code)
	if len(body.Target) > 0 {
		out += fmt.Sprintf("%s: ", body.Target)
	}
	out += body.Message

	if len(body.Details) > 0 {
		out += " Details: "
		for i, innerErr := range body.Details {
			out += innerErr.String()
			if i < len(body.Details)-1 {
				out += ", "
			}
		}
	}

	return out
}

// NewCloudError returns a new CloudError
func NewCloudError(statusCode int, code, target, format string, a ...interface{}) *CloudError {
	return &CloudError{
		StatusCode: statusCode,
		CloudErrorBody: &CloudErrorBody{
			Code:    code,
			Message: fmt.Sprintf(format, a...),
			Target:  target,
		},
	}
}

// WriteError constructs and writes a CloudError to the given ResponseWriter
func WriteError(w http.ResponseWriter, statusCode int, code, target, format string, a ...interface{}) {
	WriteCloudError(w, NewCloudError(statusCode, code, target, format, a...))
}

// WriteCloudError writes a CloudError to the given ResponseWriter
func WriteCloudError(w http.ResponseWriter, err *CloudError) {
	w.Header()[HeaderNameErrorCode] = []string{err.Code}
	_, _ = WriteJSONResponse(w, err.StatusCode, err)
}

// NewInternalServerError creates a CloudError for an internal server error
func NewInternalServerError() *CloudError {
	return NewCloudError(
		http.StatusInternalServerError,
		CloudErrorCodeInternalServerError, "",
		"Internal server error.")
}

// WriteInternalServerError writes an internal server error to the given ResponseWriter
func WriteInternalServerError(w http.ResponseWriter) {
	WriteCloudError(w, NewInternalServerError())
}

// NewConflictError creates a CloudError for a conflict error
func NewConflictError(resourceID *azcorearm.ResourceID, format string, a ...interface{}) *CloudError {
	return NewCloudError(
		http.StatusConflict,
		CloudErrorCodeConflict,
		resourceID.String(),
		format, a...)
}

// WriteConflictError writes a conflict error to the given ResponseWriter
func WriteConflictError(w http.ResponseWriter, resourceID *azcorearm.ResourceID, format string, a ...interface{}) {
	WriteCloudError(w, NewConflictError(resourceID, format, a...))
}

// NewResourceNotFoundError creates a CloudError for a nonexistent resource error
func NewResourceNotFoundError(resourceID *azcorearm.ResourceID) *CloudError {
	var code string
	var message string

	switch resourceID.ResourceType.String() {
	case azcorearm.SubscriptionResourceType.String():
		code = CloudErrorCodeSubscriptionNotFound
		message = fmt.Sprintf(
			"The subscription '%s' was not found.",
			resourceID.SubscriptionID)
	case azcorearm.ResourceGroupResourceType.String():
		code = CloudErrorCodeResourceGroupNotFound
		message = fmt.Sprintf(
			"The resource group '%s' under subscription '%s' was not found.",
			resourceID.ResourceGroupName, resourceID.SubscriptionID)
	default:
		code = CloudErrorCodeResourceNotFound
		message = fmt.Sprintf(
			"The resource '%s/%s' under resource group '%s' was not found.",
			resourceID.ResourceType.Type, resourceID.Name, resourceID.ResourceGroupName)
	}

	return NewCloudError(http.StatusNotFound, code, resourceID.String(), "%s", message)
}

// WriteResourceNotFoundError writes a nonexistent resource error to the given ResponseWriter
func WriteResourceNotFoundError(w http.ResponseWriter, resourceID *azcorearm.ResourceID) {
	WriteCloudError(w, NewResourceNotFoundError(resourceID))
}

// NewInvalidRequestContentError creates a CloudError for an invalid request content error
func NewInvalidRequestContentError(err error) *CloudError {
	const message = "The request content was invalid and could not be deserialized: %q"

	switch err := err.(type) {
	case *CloudError:
		return err
	case *json.UnmarshalTypeError:
		return NewCloudError(
			http.StatusBadRequest,
			CloudErrorCodeInvalidRequestContent,
			err.Field, message, err)
	default:
		return NewCloudError(
			http.StatusBadRequest,
			CloudErrorCodeInvalidRequestContent,
			"", message, err)
	}
}

// WriteInvalidRequestContentError writes an invalid request content error to the given ResponseWriter
func WriteInvalidRequestContentError(w http.ResponseWriter, err error) {
	WriteCloudError(w, NewInvalidRequestContentError(err))
}
