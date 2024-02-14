package v20240610preview

// Copyright (c) Microsoft Corporation.
// Licensed under the Apache License 2.0.

import (
	"slices"

	"github.com/Azure/ARO-HCP/pkg/api"
)

func (v *version) NewNodePoolProfile(from *api.NodePoolProfile) api.VersionedNodePoolProfile {
	return &NodePoolProfile{
		Name:                   from.Name,
		Version:                from.Version,
		Labels:                 slices.Clone(from.Labels),
		Taints:                 slices.Clone(from.Taints),
		DiskSize:               from.DiskSize,
		EphemeralOSDisk:        from.EphemeralOSDisk,
		Replicas:               from.Replicas,
		SubnetID:               from.SubnetID,
		EncryptionAtHost:       from.EncryptionAtHost,
		AutoRepair:             from.AutoRepair,
		DiscEncryptionSetID:    from.DiscEncryptionSetID,
		TuningConfigs:          slices.Clone(from.TuningConfigs),
		AvailabilityZone:       from.AvailabilityZone,
		DiscStorageAccountType: from.DiscStorageAccountType,
		VMSize:                 from.VMSize,
		Autoscaling: NodePoolAutoscaling{
			MinReplicas: from.Autoscaling.MinReplicas,
			MaxReplicas: from.Autoscaling.MaxReplicas,
		},
	}
}

func (npp *NodePoolProfile) Normalize(out *api.NodePoolProfile) {
	out.Name = npp.Name
	out.Version = npp.Version
	out.Labels = slices.Clone(npp.Labels)
	out.Taints = slices.Clone(npp.Taints)
	out.DiskSize = npp.DiskSize
	out.EphemeralOSDisk = npp.EphemeralOSDisk
	out.Replicas = npp.Replicas
	out.SubnetID = npp.SubnetID
	out.EncryptionAtHost = npp.EncryptionAtHost
	out.AutoRepair = npp.AutoRepair
	out.DiscEncryptionSetID = npp.DiscEncryptionSetID
	out.TuningConfigs = slices.Clone(npp.TuningConfigs)
	out.AvailabilityZone = npp.AvailabilityZone
	out.DiscStorageAccountType = npp.DiscStorageAccountType
	out.VMSize = npp.VMSize
	out.Autoscaling = api.NodePoolAutoscaling{
		MinReplicas: npp.Autoscaling.MinReplicas,
		MaxReplicas: npp.Autoscaling.MaxReplicas,
	}
}

func (npp *NodePoolProfile) ValidateStatic() error {
	return nil
}
