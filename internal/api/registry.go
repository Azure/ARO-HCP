package api

// Copyright (c) Microsoft Corporation.
// Licensed under the Apache License 2.0.

import (
	"fmt"
)

const (
	ProviderNamespace        = "Microsoft.RedHatOpenShift"
	ProviderNamespaceDisplay = "Azure Red Hat OpenShift"
	ResourceType             = "hcpOpenShiftClusters"
	ResourceTypeDisplay      = "Hosted Control Plane (HCP) OpenShift Clusters"
)

type VersionedHCPOpenShiftCluster interface {
	Normalize(*HCPOpenShiftCluster) error
	ValidateStatic() error
}

type VersionedHCPOpenShiftClusterNodePool interface {
	Normalize(*HCPOpenShiftClusterNodePool) error
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
