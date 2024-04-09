package subscription

// Copyright (c) Microsoft Corporation.
// Licensed under the Apache License 2.0.

import (
	"github.com/Azure/ARO-HCP/internal/api"
)

type version struct{}

// NewHCPOpenShiftCluster implements api.Version.
func (v version) NewHCPOpenShiftCluster(*api.HCPOpenShiftCluster) api.VersionedHCPOpenShiftCluster {
	panic("'System Version 2.0' is not supported for HCP Cluster objects")
}

// UnmarshalHCPOpenShiftCluster implements api.Version.
func (v version) UnmarshalHCPOpenShiftCluster([]byte, bool, *api.HCPOpenShiftCluster) error {
	panic("'System Version 2.0' is not supported for HCP Cluster objects")
}

// String returns the api-version parameter value for this API.
func (v version) String() string {
	return "2.0"
}

func init() {
	api.Register(version{})
}
