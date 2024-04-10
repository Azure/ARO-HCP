package api

// Copyright (c) Microsoft Corporation.
// Licensed under the Apache License 2.0.

import (
	"fmt"
	"net/http"
	"reflect"
	"strings"

	validator "github.com/go-playground/validator/v10"
)

const (
	ProviderNamespace        = "Microsoft.RedHatOpenShift"
	ProviderNamespaceDisplay = "Azure Red Hat OpenShift"
	ResourceType             = "hcpOpenShiftClusters"
	ResourceTypeDisplay      = "Hosted Control Plane (HCP) OpenShift Clusters"
)

type VersionedHCPOpenShiftCluster interface {
	Normalize(*HCPOpenShiftCluster)
	ValidateStatic() error
}

type VersionedHCPOpenShiftClusterNodePool interface {
	Normalize(*HCPOpenShiftClusterNodePool)
	ValidateStatic() error
}

type Version interface {
	fmt.Stringer

	// Resource Types
	NewHCPOpenShiftCluster(*HCPOpenShiftCluster) VersionedHCPOpenShiftCluster
	// FIXME Disable until we have generated structs for node pools.
	//NewHCPOpenShiftClusterNodePool(*HCPOpenShiftClusterNodePool) VersionedHCPOpenShiftClusterNodePool

	UnmarshalHCPOpenShiftCluster([]byte, *HCPOpenShiftCluster, string, bool) error
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

func NewValidator() *validator.Validate {
	validate := validator.New(validator.WithRequiredStructEnabled())

	// Use "json" struct tags for alternate field names.
	// Alternate field names will be used in validation errors.
	validate.RegisterTagNameFunc(func(field reflect.StructField) string {
		name := strings.SplitN(field.Tag.Get("json"), ",", 2)[0]
		// skip if tag key says it should be ignored
		if name == "-" {
			return ""
		}
		return name
	})

	// Use this for fields required in PUT requests. Do not apply to read-only fields.
	err := validate.RegisterValidation("required_for_put", func(fl validator.FieldLevel) bool {
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
	Updating bool
	Resource any
}

func ValidateRequest(validate *validator.Validate, method string, updating bool, resource any) error {
	return validate.Struct(validateContext{Method: method, Updating: updating, Resource: resource})
}
