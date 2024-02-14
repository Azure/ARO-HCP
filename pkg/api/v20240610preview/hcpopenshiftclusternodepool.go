package v20240610preview

// Copyright (c) Microsoft Corporation.
// Licensed under the Apache License 2.0.

import (
	"github.com/Azure/ARO-HCP/pkg/api"
	"github.com/Azure/ARO-HCP/pkg/api/arm"
)

// HCPOpenShiftClusterNodePool represents a node pool resource for ARO HCP
// OpenShift clusters.
type HCPOpenShiftClusterNodePool struct {
	arm.TrackedResource
	Properties HCPOpenShiftClusterNodePoolProperties `json:":properties,omitempty"`
}

// HCPOpenShiftClusterNodePoolProperties represents the property bag of a
// HCPOpenShiftClusterNodePool resource.
type HCPOpenShiftClusterNodePoolProperties struct {
	ProvisioningState arm.ProvisioningState        `json:"provisioningState,omitempty" visibility:"read"`
	Profile           api.VersionedNodePoolProfile `json:profile,omitempty" visibility:"read,create,update"`
}
