package v20240610preview

// Copyright (c) Microsoft Corporation.
// Licensed under the Apache License 2.0.

import (
	"slices"

	"github.com/Azure/ARO-HCP/internal/api"
)

func (v version) NewHCPOpenShiftClusterNodePool(from *api.HCPOpenShiftClusterNodePool) api.VersionedHCPOpenShiftClusterNodePool {
	out := &HCPOpenShiftClusterNodePool{
		Properties: HCPOpenShiftClusterNodePoolProperties{
			ProvisioningState: from.Properties.ProvisioningState,
			Profile: NodePoolProfile{
				Name:                   from.Properties.Profile.Name,
				Version:                from.Properties.Profile.Version,
				Labels:                 slices.Clone(from.Properties.Profile.Labels),
				Taints:                 slices.Clone(from.Properties.Profile.Taints),
				DiskSize:               from.Properties.Profile.DiskSize,
				EphemeralOSDisk:        from.Properties.Profile.EphemeralOSDisk,
				Replicas:               from.Properties.Profile.Replicas,
				SubnetID:               from.Properties.Profile.SubnetID,
				EncryptionAtHost:       from.Properties.Profile.EncryptionAtHost,
				AutoRepair:             from.Properties.Profile.AutoRepair,
				DiscEncryptionSetID:    from.Properties.Profile.DiscEncryptionSetID,
				TuningConfigs:          slices.Clone(from.Properties.Profile.TuningConfigs),
				AvailabilityZone:       from.Properties.Profile.AvailabilityZone,
				DiscStorageAccountType: from.Properties.Profile.DiscStorageAccountType,
				VMSize:                 from.Properties.Profile.VMSize,
				Autoscaling: NodePoolAutoscaling{
					MinReplicas: from.Properties.Profile.Autoscaling.MinReplicas,
					MaxReplicas: from.Properties.Profile.Autoscaling.MaxReplicas,
				},
			},
		},
	}

	out.TrackedResource.Copy(&from.TrackedResource)

	return out
}

func (np *HCPOpenShiftClusterNodePool) Normalize(out *api.HCPOpenShiftClusterNodePool) {
	out.Properties.ProvisioningState = np.Properties.ProvisioningState
	out.Properties.Profile.Name = np.Properties.Profile.Name
	out.Properties.Profile.Version = np.Properties.Profile.Version
	out.Properties.Profile.Labels = slices.Clone(np.Properties.Profile.Labels)
	out.Properties.Profile.Taints = slices.Clone(np.Properties.Profile.Taints)
	out.Properties.Profile.DiskSize = np.Properties.Profile.DiskSize
	out.Properties.Profile.EphemeralOSDisk = np.Properties.Profile.EphemeralOSDisk
	out.Properties.Profile.Replicas = np.Properties.Profile.Replicas
	out.Properties.Profile.SubnetID = np.Properties.Profile.SubnetID
	out.Properties.Profile.EncryptionAtHost = np.Properties.Profile.EncryptionAtHost
	out.Properties.Profile.AutoRepair = np.Properties.Profile.AutoRepair
	out.Properties.Profile.DiscEncryptionSetID = np.Properties.Profile.DiscEncryptionSetID
	out.Properties.Profile.TuningConfigs = slices.Clone(np.Properties.Profile.TuningConfigs)
	out.Properties.Profile.AvailabilityZone = np.Properties.Profile.AvailabilityZone
	out.Properties.Profile.DiscStorageAccountType = np.Properties.Profile.DiscStorageAccountType
	out.Properties.Profile.VMSize = np.Properties.Profile.VMSize
	out.Properties.Profile.Autoscaling = api.NodePoolAutoscaling{
		MinReplicas: np.Properties.Profile.Autoscaling.MinReplicas,
		MaxReplicas: np.Properties.Profile.Autoscaling.MaxReplicas,
	}
}

func (np *HCPOpenShiftClusterNodePool) ValidateStatic() error {
	return nil
}
