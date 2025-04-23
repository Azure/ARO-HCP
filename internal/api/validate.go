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

package api

import (
	"crypto/x509"
	"fmt"
	"net/http"
	"reflect"
	"strings"
	"unicode"
	"unicode/utf8"

	azcorearm "github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"
	validator "github.com/go-playground/validator/v10"

	"github.com/Azure/ARO-HCP/internal/api/arm"
)

// GetJSONTagName extracts the JSON field name from the "json" key in
// a struct tag. Returns an empty string if no "json" key is present,
// or if the value is "-".
func GetJSONTagName(tag reflect.StructTag) string {
	tagValue := tag.Get("json")
	if tagValue == "-" {
		return ""
	}
	fieldName, _, _ := strings.Cut(tagValue, ",")
	return fieldName
}

// EnumValidateTag generates a string suitable for use with the "validate"
// struct tag. The intent is to convert a set of valid values for a string
// subtype into a "oneof=" expression for the purpose of static validation.
func EnumValidateTag[S ~string](values ...S) string {
	s := make([]string, len(values))
	for i, e := range values {
		s[i] = string(e)
		// Replace special characters with the UTF-8 hex representation.
		// https://pkg.go.dev/github.com/go-playground/validator/v10#hdr-Using_Validator_Tags
		s[i] = strings.ReplaceAll(s[i], ",", "0x2C")
		s[i] = strings.ReplaceAll(s[i], "|", "0x7C")
	}
	return fmt.Sprintf("oneof=%s", strings.Join(s, " "))
}

func NewValidator() *validator.Validate {
	var err error

	validate := validator.New(validator.WithRequiredStructEnabled())

	// Use "json" struct tags for alternate field names.
	// Alternate field names will be used in validation errors.
	validate.RegisterTagNameFunc(func(field reflect.StructField) string {
		return GetJSONTagName(field.Tag)
	})

	// Register ARM-mandated enumeration types.
	validate.RegisterAlias("enum_managedserviceidentitytype", EnumValidateTag(
		arm.ManagedServiceIdentityTypeNone,
		arm.ManagedServiceIdentityTypeSystemAssigned,
		arm.ManagedServiceIdentityTypeSystemAssignedUserAssigned,
		arm.ManagedServiceIdentityTypeUserAssigned))
	validate.RegisterAlias("enum_subscriptionstate", EnumValidateTag(
		arm.SubscriptionStateRegistered,
		arm.SubscriptionStateUnregistered,
		arm.SubscriptionStateWarned,
		arm.SubscriptionStateDeleted,
		arm.SubscriptionStateSuspended))

	// Use this for string fields specifying an ARO-HCP API version.
	err = validate.RegisterValidation("api_version", func(fl validator.FieldLevel) bool {
		field := fl.Field()
		if field.Kind() != reflect.String {
			panic("String type required for api_version")
		}
		_, ok := Lookup(field.String())
		return ok
	})
	if err != nil {
		panic(err)
	}

	// Use this for string fields providing PEM encoded certificates.
	err = validate.RegisterValidation("pem_certificates", func(fl validator.FieldLevel) bool {
		field := fl.Field()
		if field.Kind() != reflect.String {
			panic("String type required for pem_certificates")
		}
		return x509.NewCertPool().AppendCertsFromPEM([]byte(field.String()))
	})
	if err != nil {
		panic(err)
	}

	// Use this for fields required in PUT requests. Do not apply to read-only fields.
	err = validate.RegisterValidation("required_for_put", func(fl validator.FieldLevel) bool {
		val := fl.Top().FieldByName("Method")
		if val.IsZero() {
			panic("Method field not found for required_for_put")
		}
		if val.String() != http.MethodPut {
			return true
		}

		// This is replicating the implementation of "required".
		// See https://github.com/go-playground/validator/issues/492
		// Sounds like "hasValue" is unlikely to be exported and
		// "validate.Var" does not seem like a safe alternative.
		field := fl.Field()
		_, kind, nullable := fl.ExtractType(field)
		switch kind {
		case reflect.Slice, reflect.Map, reflect.Ptr, reflect.Interface, reflect.Chan, reflect.Func:
			return !field.IsNil()
		default:
			if nullable && field.Interface() != nil {
				return true
			}
			return field.IsValid() && !field.IsZero()
		}
	})
	if err != nil {
		panic(err)
	}

	// Use this for string fields specifying an Azure resource ID.
	// The optional argument further enforces a specific resource type.
	err = validate.RegisterValidation("resource_id", func(fl validator.FieldLevel) bool {
		field := fl.Field()
		param := fl.Param()
		if field.Kind() != reflect.String {
			panic("String type required for resource_id")
		}
		resourceID, err := azcorearm.ParseResourceID(field.String())
		if err != nil {
			return false
		}
		resourceType := resourceID.ResourceType.String()
		return param == "" || strings.EqualFold(resourceType, param)
	})
	if err != nil {
		panic(err)
	}

	return validate
}

type validateContext struct {
	// Fields must be exported so valdator can access.
	Method   string
	Resource any
}

// approximateJSONName approximates the JSON name for a struct field name by
// lowercasing the first letter. This is not always accurate in general but
// works for the small set of cases where we need it.
func approximateJSONName(s string) string {
	r, size := utf8.DecodeRuneInString(s)
	if r == utf8.RuneError && size <= 1 {
		return s
	}
	lc := unicode.ToLower(r)
	if r == lc {
		return s
	}
	return string(lc) + s[size:]
}

func ValidateRequest(validate *validator.Validate, method string, resource any) []arm.CloudErrorBody {
	var errorDetails []arm.CloudErrorBody

	err := validate.Struct(validateContext{Method: method, Resource: resource})

	if err == nil {
		return nil
	}

	// Convert validation errors to cloud error details.
	switch err := err.(type) {
	case validator.ValidationErrors:
		for _, fieldErr := range err {
			message := fmt.Sprintf("Invalid value '%v' for field '%s'", fieldErr.Value(), fieldErr.Field())
			// Try to add a corrective suggestion to the message.
			tag := fieldErr.Tag()
			if strings.HasPrefix(tag, "enum_") {
				if len(strings.Split(fieldErr.Param(), " ")) == 1 {
					message += fmt.Sprintf(" (must be %s)", fieldErr.Param())
				} else {
					message += fmt.Sprintf(" (must be one of: %s)", fieldErr.Param())
				}
			} else {
				switch tag {
				case "api_version": // custom tag
					message = fmt.Sprintf("Unrecognized API version '%s'", fieldErr.Value())
				case "pem_certificates": // custom tag
					message += " (must provide PEM encoded certificates)"
				case "required", "required_for_put": // custom tag
					message = fmt.Sprintf("Missing required field '%s'", fieldErr.Field())
				case "required_unless":
					// The parameter format is pairs of "fieldName fieldValue".
					// Multiple pairs are possible but we currently only use one.
					fields := strings.Fields(fieldErr.Param())
					if len(fields) > 1 {
						// We want to print the JSON name for the field
						// referenced in the parameter, but FieldError does
						// not provide access to the parent reflect.Type from
						// which we could look it up. So approximate the JSON
						// name by lowercasing the first letter.
						message = fmt.Sprintf("Field '%s' is required when '%s' is not '%s'", fieldErr.Field(), approximateJSONName(fields[0]), fields[1])
					}
				case "resource_id": // custom tag
					if fieldErr.Param() != "" {
						message += fmt.Sprintf(" (must be a valid '%s' resource ID)", fieldErr.Param())
					} else {
						message += " (must be a valid Azure resource ID)"
					}
				case "cidrv4":
					message += " (must be a v4 CIDR range)"
				case "dns_rfc1035_label":
					message += " (must be a valid DNS RFC 1035 label)"
				case "excluded_with":
					// We want to print the JSON name for the field
					// referenced in the parameter, but FieldError does
					// not provide access to the parent reflect.Type from
					// which we could look it up. So approximate the JSON
					// name by lowercasing the first letter.
					zero := reflect.Zero(fieldErr.Type()).Interface()
					message = fmt.Sprintf("Field '%s' must be %v when '%s' is specified", fieldErr.Field(), zero, approximateJSONName(fieldErr.Param()))
				case "gtefield":
					// We want to print the JSON name for the field
					// referenced in the parameter, but FieldError does
					// not provide access to the parent reflect.Type from
					// which we could look it up. So approximate the JSON
					// name by lowercasing the first letter.
					message += fmt.Sprintf(" (must be at least the value of '%s')", approximateJSONName(fieldErr.Param()))
				case "ipv4":
					message += " (must be an IPv4 address)"
				case "max":
					switch fieldErr.Kind() {
					case reflect.String:
						message += fmt.Sprintf(" (maximum length is %s)", fieldErr.Param())
					default:
						if fieldErr.Param() == "0" {
							message += " (must be non-positive)"
						} else {
							message += fmt.Sprintf(" (must be at most %s)", fieldErr.Param())
						}
					}
				case "min":
					switch fieldErr.Kind() {
					case reflect.String:
						message += fmt.Sprintf(" (minimum length is %s)", fieldErr.Param())
					default:
						if fieldErr.Param() == "0" {
							message += " (must be non-negative)"
						} else {
							message += fmt.Sprintf(" (must be at least %s)", fieldErr.Param())
						}
					}
				case "startswith":
					message += fmt.Sprintf(" (must start with '%s')", fieldErr.Param())
				case "url":
					message += " (must be a URL)"
				}
			}
			errorDetails = append(errorDetails, arm.CloudErrorBody{
				Code:    arm.CloudErrorCodeInvalidRequestContent,
				Message: message,
				// Split "validateContext.Resource.{REMAINING_FIELDS}"
				Target: strings.SplitN(fieldErr.Namespace(), ".", 3)[2],
			})
		}
	default:
		errorDetails = append(errorDetails, arm.CloudErrorBody{
			Code:    arm.CloudErrorCodeInvalidRequestContent,
			Message: err.Error(),
		})
	}

	return errorDetails
}

// ValidateSubscription validates a subscription request payload.
func ValidateSubscription(subscription *arm.Subscription) *arm.CloudError {
	cloudError := arm.NewCloudError(
		http.StatusBadRequest,
		arm.CloudErrorCodeMultipleErrorsOccurred, "",
		"Content validation failed on multiple fields")
	cloudError.Details = make([]arm.CloudErrorBody, 0)

	validate := NewValidator()
	// There is no PATCH method for subscriptions, so assume PUT.
	errorDetails := ValidateRequest(validate, http.MethodPut, subscription)
	if errorDetails != nil {
		cloudError.Details = append(cloudError.Details, errorDetails...)
	}

	switch len(cloudError.Details) {
	case 0:
		cloudError = nil
	case 1:
		// Promote a single validation error out of details.
		cloudError.CloudErrorBody = &cloudError.Details[0]
	}

	return cloudError
}
