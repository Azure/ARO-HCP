package api

// Copyright (c) Microsoft Corporation.
// Licensed under the Apache License 2.0.

import (
	"github.com/Azure/ARO-HCP/internal/api/arm"
)

// HCPOpenShiftClusterNodePool represents a node pool resource for ARO HCP
// OpenShift clusters.
type HCPOpenShiftClusterNodePool struct {
	arm.TrackedResource
	Properties HCPOpenShiftClusterNodePoolProperties `json:"properties,omitempty" validate:"required_for_put"`
}

// HCPOpenShiftClusterNodePoolProperties represents the property bag of a
// HCPOpenShiftClusterNodePool resource.
type HCPOpenShiftClusterNodePoolProperties struct {
	ProvisioningState arm.ProvisioningState `json:"provisioningState,omitempty" visibility:"read" validate:"omitempty,enum_provisioningstate"`
	Spec              NodePoolSpec          `json:"spec,omitempty" visibility:"read,create,update" validate:"required_for_put"`
}

type NodePoolSpec struct {
	Version       VersionProfile          `json:"version,omitempty" visibility:"read create" validate:"required_for_put"`
	Platform      NodePoolPlatformProfile `json:"platform,omitempty" visibility:"read create" validate:"required_for_put"`
	Replicas      int32                   `json:"replicas,omitempty" visibility:"read create update"`
	AutoRepair    bool                    `json:"autoRepair,omitempty" visibility:"read create"`
	Autoscaling   NodePoolAutoscaling     `json:"autoScaling,omitempty" visibility:"read create update"`
	Labels        map[string]string       `json:"labels,omitempty" visibility:"read create update"`
	Taints        []*Taint                `json:"taints,omitempty" visibility:"read create update"`
	TuningConfigs []string                `json:"tuningConfigs,omitempty" visibility:"read create update"`
}

// NodePoolPlatformProfile represents a worker node pool configuration.
// Visibility for the entire struct is "read create".
type NodePoolPlatformProfile struct {
	SubnetID               string `json:"subnetId,omitempty"`
	VMSize                 string `json:"vmSize,omitempty" validate:"required_for_put"`
	DiskSizeGiB            int32  `json:"diskSizeGiB,omitempty"`
	DiskStorageAccountType string `json:"diskStorageAccountType,omitempty"`
	AvailabilityZone       string `json:"availabilityZone,omitempty"`
	EncryptionAtHost       bool   `json:"encryptionAtHost,omitempty"`
	DiskEncryptionSetID    string `json:"diskEncryptionSetId,omitempty"`
	EphemeralOSDisk        bool   `json:"ephemeralOsDisk,omitempty"`
}

// NodePoolAutoscaling represents a node pool autoscaling configuration.
// Visibility for the entire struct is "read create update".
type NodePoolAutoscaling struct {
	Min int32 `json:"min,omitempty" validate:"required_for_put"`
	Max int32 `json:"max,omitempty" validate:"required_for_put"`
}

type Taint struct {
	Effect Effect `json:"effect,omitempty" validate:"required_for_put,enum_effect"`
	Key    string `json:"key,omitempty" validate:"required_for_put"`
	Value  string `json:"value,omitempty"`
}

func NewDefaultHCPOpenShiftClusterNodePool() *HCPOpenShiftClusterNodePool {
	return &HCPOpenShiftClusterNodePool{
		Properties: HCPOpenShiftClusterNodePoolProperties{
			Spec: NodePoolSpec{},
		},
	}
}
