package v20240610preview

// Copyright (c) Microsoft Corporation.
// Licensed under the Apache License 2.0.

import (
	"github.com/Azure/ARO-HCP/internal/api"
)

func (v *version) NewHCPOpenShiftClusterNodePool(from *api.HCPOpenShiftClusterNodePool) api.VersionedHCPOpenShiftClusterNodePool {
	out := &HCPOpenShiftClusterNodePool{
		Properties: HCPOpenShiftClusterNodePoolProperties{
			ProvisioningState: from.Properties.ProvisioningState,
			Profile:           v.NewNodePoolProfile(&from.Properties.Profile),
		},
	}

	out.TrackedResource.Copy(&from.TrackedResource)

	return out
}

func (np *HCPOpenShiftClusterNodePool) Normalize(out *api.HCPOpenShiftClusterNodePool) {
	out.Properties.ProvisioningState = np.Properties.ProvisioningState
	np.Properties.Profile.Normalize(&out.Properties.Profile)
}

func (np *HCPOpenShiftClusterNodePool) ValidateStatic() error {
	return nil
}
