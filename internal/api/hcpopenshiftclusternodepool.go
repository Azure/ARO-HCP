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
	Properties HCPOpenShiftClusterNodePoolProperties `json:":properties,omitempty"`
}

// HCPOpenShiftClusterNodePoolProperties represents the property bag of a
// HCPOpenShiftClusterNodePool resource.
type HCPOpenShiftClusterNodePoolProperties struct {
	ProvisioningState arm.ProvisioningState `json:"provisioningState,omitempty" visibility:"read"`
	Profile           NodePoolProfile       `json:"profile,omitempty" visibility:"read,create,update"`
}

// NodePoolProfile represents a worker node pool configuration.
// Visibility for the entire struct is "read".
type NodePoolProfile struct {
	Name                   string              `json:"name,omitempty"`
	Version                string              `json:"version,omitempty"`
	Labels                 []string            `json:"labels,omitempty"`
	Taints                 []string            `json:"taints,omitempty"`
	DiskSize               int32               `json:"diskSize,omitempty"`
	EphemeralOSDisk        bool                `json:"ephemeralOsDisk,omitempty"`
	Replicas               int32               `json:"replicas,omitempty"`
	SubnetID               string              `json:"subnetId,omitempty"`
	EncryptionAtHost       bool                `json:"encryptionAtHost,omitempty"`
	AutoRepair             bool                `json:"autoRepair,omitempty"`
	DiskEncryptionSetID    string              `json:"diskEncryptionSetId,omitempty"`
	TuningConfigs          []string            `json:"tuningConfigs,omitempty"`
	AvailabilityZone       string              `json:"availabilityZone,omitempty"`
	DiskStorageAccountType string              `json:"diskStorageAccountType,omitempty"`
	VMSize                 string              `json:"vmSize,omitempty"`
	Autoscaling            NodePoolAutoscaling `json:"autoscaling,omitempty"`
}

// NodePoolAutoscaling represents a node pool autoscaling configuration.
// Visibility for the entire struct is "read".
type NodePoolAutoscaling struct {
	MinReplicas int32 `json:"minReplicas,omitempty"`
	MaxReplicas int32 `json:"maxReplicas,omitempty"`
}
