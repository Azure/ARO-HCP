package api

// Copyright (c) Microsoft Corporation.
// Licensed under the Apache License 2.0.

import (
	"fmt"
	"net/http"
	"reflect"
	"strings"

	"github.com/Azure/ARO-HCP/internal/api/arm"
	validator "github.com/go-playground/validator/v10"
)

const (
	ProviderNamespace        = "Microsoft.RedHatOpenShift"
	ProviderNamespaceDisplay = "Azure Red Hat OpenShift"
	ResourceType             = ProviderNamespace + "/" + "hcpOpenShiftClusters"
	ResourceTypeDisplay      = "Hosted Control Plane (HCP) OpenShift Clusters"
)

type VersionedHCPOpenShiftCluster interface {
	Normalize(*HCPOpenShiftCluster)
	ValidateStatic(current VersionedHCPOpenShiftCluster, updating bool, method string) *arm.CloudError
}

type VersionedHCPOpenShiftClusterNodePool interface {
	Normalize(*HCPOpenShiftClusterNodePool)
	ValidateStatic() *arm.CloudError
}

type Version interface {
	fmt.Stringer

	// Resource Types
	// Passing a nil pointer creates a resource with default values.
	NewHCPOpenShiftCluster(*HCPOpenShiftCluster) VersionedHCPOpenShiftCluster
	// FIXME Disable until we have generated structs for node pools.
	//NewHCPOpenShiftClusterNodePool(*HCPOpenShiftClusterNodePool) VersionedHCPOpenShiftClusterNodePool
}

// apiRegistry is the map of registered API versions
var apiRegistry = map[string]Version{}

func Register(version Version) {
	apiRegistry[version.String()] = version
}

func Lookup(key string) (version Version, ok bool) {
	version, ok = apiRegistry[key]
	return
}

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

func NewValidator() *validator.Validate {
	var err error

	validate := validator.New(validator.WithRequiredStructEnabled())

	// Use "json" struct tags for alternate field names.
	// Alternate field names will be used in validation errors.
	validate.RegisterTagNameFunc(func(field reflect.StructField) string {
		return GetJSONTagName(field.Tag)
	})

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

	return validate
}

type validateContext struct {
	// Fields must be exported so valdator can access.
	Method   string
	Resource any
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
			message := fmt.Sprintf("Invalid value '%s' for field '%s'", fieldErr.Value(), fieldErr.Field())
			// Try to add a corrective suggestion to the message.
			tag := fieldErr.Tag()
			if strings.HasPrefix(tag, "enum_") {
				message += fmt.Sprintf(" (must be one of: %s)", fieldErr.Param())
			} else {
				switch tag {
				case "api_version": // custom tag
					message = fmt.Sprintf("Unrecognized API version '%s'", fieldErr.Value())
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
