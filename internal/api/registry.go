package api

// Copyright (c) Microsoft Corporation.
// Licensed under the Apache License 2.0.

import (
	"fmt"
)

type VersionedHCPOpenShiftCluster interface {
	Normalize(*HCPOpenShiftCluster)
	ValidateStatic() error
}

type VersionedHCPOpenShiftClusterNodePool interface {
	Normalize(*HCPOpenShiftClusterNodePool)
	ValidateStatic() error
}

type VersionedNodePoolProfile interface {
	Normalize(*NodePoolProfile)
	ValidateStatic() error
}

type Version interface {
	fmt.Stringer

	// Resource Types
	NewHCPOpenShiftCluster(*HCPOpenShiftCluster) VersionedHCPOpenShiftCluster
	NewHCPOpenShiftClusterNodePool(*HCPOpenShiftClusterNodePool) VersionedHCPOpenShiftClusterNodePool

	// Component Types
	NewNodePoolProfile(*NodePoolProfile) VersionedNodePoolProfile
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
