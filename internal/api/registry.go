package api

// Copyright (c) Microsoft Corporation.
// Licensed under the Apache License 2.0.

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
	// Resource Types
	NewHCPOpenShiftCluster(*HCPOpenShiftCluster) VersionedHCPOpenShiftCluster
	NewHCPOpenShiftClusterNodePool(*HCPOpenShiftClusterNodePool) VersionedHCPOpenShiftClusterNodePool

	// Component Types
	NewNodePoolProfile(*NodePoolProfile) VersionedNodePoolProfile
}

// APIs is the map of registered API versions
var APIs = map[string]Version{}
