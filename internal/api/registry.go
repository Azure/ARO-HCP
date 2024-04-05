package api

// Copyright (c) Microsoft Corporation.
// Licensed under the Apache License 2.0.

import (
	"fmt"
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

	UnmarshalHCPOpenShiftCluster([]byte, bool, *HCPOpenShiftCluster) error
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

	return validate
}
