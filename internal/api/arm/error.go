package arm

// Copyright (c) Microsoft Corporation.
// Licensed under the Apache License 2.0.

import (
	"encoding/json"
	"fmt"
	"net/http"

	azcorearm "github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"
)

// CloudError codes
const (
	CloudErrorCodeInternalServerError    = "InternalServerError"
	CloudErrorCodeInvalidParameter       = "InvalidParameter"
	CloudErrorCodeInvalidRequestContent  = "InvalidRequestContent"
	CloudErrorCodeInvalidResource        = "InvalidResource"
	CloudErrorCodeInvalidResourceType    = "InvalidResourceType"
	CloudErrorCodeMultipleErrorsOccurred = "MultipleErrorsOccurred"
	CloudErrorCodeUnsupportedMediaType   = "UnsupportedMediaType"
	CloudErrorCodeNotFound               = "NotFound"
	CloudErrorInvalidSubscriptionState   = "InvalidSubscriptionState"
	CloudErrorCodeResourceNotFound       = "ResourceNotFound"
	CloudErrorCodeResourceGroupNotFound  = "ResourceGroupNotFound"
	CloudErrorCodeInvalidSubscriptionID  = "InvalidSubscriptionID"
	CloudErrorInvalidResourceName        = "InvalidResourceName"
	CloudErrorInvalidResourceGroupName   = "InvalidResourceGroupName"
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
		body = ": " + err.CloudErrorBody.String()
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
	w.Header()["Content-Type"] = []string{"application/json"}
	w.Header()[HeaderNameErrorCode] = []string{err.Code}
	w.WriteHeader(err.StatusCode)
	encoder := json.NewEncoder(w)
	encoder.SetIndent("", "    ")
	_ = encoder.Encode(err)
}

func WriteResourceNotFoundError(w http.ResponseWriter, originalPath string) {
	resource, err := azcorearm.ParseResourceID(originalPath)
	if err != nil {
		// Unable to identify the resource from originalPath
		WriteCloudError(w, NewCloudError(
			http.StatusNotFound, CloudErrorCodeResourceNotFound, "",
			"The resource was not found."))
		return
	}

	WriteCloudError(w, NewCloudError(
		http.StatusNotFound, CloudErrorCodeResourceGroupNotFound, "",
		"The resource '%s/%s' under resource group '%s' was not found.", resource.ResourceType.Type, resource.Name, resource.ResourceGroupName))
}

// WriteInternalServerError writes an internal server error to the given ResponseWriter
func WriteInternalServerError(w http.ResponseWriter) {
	WriteError(
		w, http.StatusInternalServerError,
		CloudErrorCodeInternalServerError, "",
		"Internal server error.")
}

// NewUnmarshalCloudError creates an appropriate CloudError for JSON unmarshaling errors
func NewUnmarshalCloudError(err error) *CloudError {
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
