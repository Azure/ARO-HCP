package arm

// Copyright (c) Microsoft Corporation.
// Licensed under the Apache License 2.0.

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"

	validator "github.com/go-playground/validator/v10"
)

// CloudError codes
const (
	CloudErrorCodeInternalServerError   = "InternalServerError"
	CloudErrorCodeInvalidParameter      = "InvalidParameter"
	CloudErrorCodeInvalidRequestContent = "InvalidRequestContent"
	CloudErrorCodeInvalidResource       = "InvalidResource"
	CloudErrorCodeInvalidResourceType   = "InvalidResourceType"
	CloudErrorCodeUnsupportedMediaType  = "UnsupportedMediaType"
	CloudErrorCodeNotFound              = "NotFound"
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

// WriteInternalServerError writes an internal server error to the given ResponseWriter
func WriteInternalServerError(w http.ResponseWriter) {
	WriteError(
		w, http.StatusInternalServerError,
		CloudErrorCodeInternalServerError, "",
		"Internal server error.")
}

// WriteUnmarshalError writes an appropriate CloudError for JSON unmarshaling or
// static validation errors to the given ResponseWriter
func WriteUnmarshalError(err error, w http.ResponseWriter) {
	switch err := err.(type) {
	case *json.UnmarshalTypeError:
		WriteError(
			w, http.StatusBadRequest,
			CloudErrorCodeInvalidRequestContent,
			err.Field,
			err.Error())
	case validator.ValidationErrors:
		cloudError := NewCloudError(
			http.StatusBadRequest,
			CloudErrorCodeInvalidRequestContent, "",
			"Content validation failed on one or more fields")
		cloudError.CloudErrorBody.Details = make([]CloudErrorBody, len(err))
		for index, fieldErr := range err {
			message := fmt.Sprintf("Invalid value '%s' for field '%s'", fieldErr.Value(), fieldErr.Field())
			// Try to add a corrective suggestion to the message.
			tag := fieldErr.Tag()
			if strings.HasPrefix(tag, "enum_") {
				message += fmt.Sprintf(" (must be one of: %s)", fieldErr.Param())
			} else {
				switch tag {
				case "required_for_put": // custom tag
					message = fmt.Sprintf("Missing required field '%s'", fieldErr.Field())
				case "cidrv4":
					message += " (must be a v4 CIDR address)"
				case "ipv4":
					message += " (must be an IPv4 address)"
				case "url":
					message += " (must be a URL)"
				}
			}
			_, target, _ := strings.Cut(fieldErr.Namespace(), ".")
			cloudError.Details[index] = CloudErrorBody{
				Code:    CloudErrorCodeInvalidRequestContent,
				Message: message,
				Target:  target,
			}
		}
		WriteCloudError(w, cloudError)
	default:
		WriteError(
			w, http.StatusBadRequest,
			CloudErrorCodeInvalidRequestContent,
			"", err.Error())
	}
}
