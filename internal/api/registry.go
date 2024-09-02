package api

// Copyright (c) Microsoft Corporation.
// Licensed under the Apache License 2.0.

import (
	"fmt"

	"github.com/Azure/ARO-HCP/internal/api/arm"
)

const (
	ProviderNamespace        = "Microsoft.RedHatOpenShift"
	ProviderNamespaceDisplay = "Azure Red Hat OpenShift"
	ResourceType             = ProviderNamespace + "/" + ClusterResourceTypeName
	ClusterResourceTypeName  = "hcpOpenShiftClusters"
	NodePoolResourceTypeName = "nodePools"
	ResourceTypeDisplay      = "Hosted Control Plane (HCP) OpenShift Clusters"
)

type VersionedHCPOpenShiftCluster interface {
	Normalize(*HCPOpenShiftCluster)
	ValidateStatic(current VersionedHCPOpenShiftCluster, updating bool, method string) *arm.CloudError
}

type VersionedHCPOpenShiftClusterList struct {
	Value []*VersionedHCPOpenShiftCluster

	// The link to the next page of items
	NextLink *string
}

type VersionedHCPOpenShiftClusterNodePool interface {
	Normalize(*HCPOpenShiftClusterNodePool)
	ValidateStatic(current VersionedHCPOpenShiftClusterNodePool, updating bool, method string) *arm.CloudError
}

type Version interface {
	fmt.Stringer

	// Resource Types
	// Passing a nil pointer creates a resource with default values.
	NewHCPOpenShiftCluster(*HCPOpenShiftCluster) VersionedHCPOpenShiftCluster
	NewHCPOpenShiftClusterNodePool(*HCPOpenShiftClusterNodePool) VersionedHCPOpenShiftClusterNodePool
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
