package adminapi

// Copyright (c) Microsoft Corporation.
// Licensed under the Apache License 2.0.

import (
	"github.com/Azure/ARO-HCP/internal/api"
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
	Version       VersionProfile          `json:"version,omitempty" visibility:"read create update" validate:"required_for_put"`
	Platform      NodePoolPlatformProfile `json:"platform,omitempty" visibility:"read create" validate:"required_for_put"`
	Replicas      int32                   `json:"replicas,omitempty" visibility:"read create update"`
	AutoRepair    bool                    `json:"autoRepair,omitempty" visibility:"read create"`
	Autoscaling   NodePoolAutoscaling     `json:"autoScaling,omitempty" visibility:"read create update"`
	Labels        map[string]string       `json:"labels,omitempty" visibility:"read create update"`
	Taints        []*Taint                `json:"taints,omitempty" visibility:"read create update"`
	TuningConfigs []string                `json:"tuningConfigs,omitempty" visibility:"read create update"`
}

// NodePoolPlatformProfile represents a worker node pool configuration.
// Visibility for the entire struct is "read".
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
// Visibility for the entire struct is "read".
type NodePoolAutoscaling struct {
	Min int32 `json:"min,omitempty" validate:"required_for_put"`
	Max int32 `json:"max,omitempty" validate:"required_for_put"`
}

type Taint struct {
	Effect Effect `json:"effect,omitempty" validate:"required_for_put,enum_effect"`
	Key    string `json:"key,omitempty" validate:"required_for_put"`
	Value  string `json:"value,omitempty"`
}

func NewDefaultHCPOpenShiftClusterNodepool() *HCPOpenShiftClusterNodePool {
	return &HCPOpenShiftClusterNodePool{
		Properties: HCPOpenShiftClusterNodePoolProperties{
			Spec: NodePoolSpec{},
		},
	}
}

func (h *HCPOpenShiftClusterNodePool) Normalize(out *api.HCPOpenShiftClusterNodePool) {
	if &h.ID != nil {
		out.ID = h.ID
	}
	if &h.Name != nil {
		out.Resource.Name = h.Name
	}
	if &h.Type != nil {
		out.Resource.Type = h.Type
	}
	if h.SystemData != nil {
		out.Resource.SystemData = &arm.SystemData{
			CreatedAt:      h.SystemData.CreatedAt,
			LastModifiedAt: h.SystemData.LastModifiedAt,
		}
		if &h.SystemData.CreatedBy != nil {
			out.Resource.SystemData.CreatedBy = h.SystemData.CreatedBy
		}
		if &h.SystemData.CreatedByType != nil {
			out.Resource.SystemData.CreatedByType = arm.CreatedByType(h.SystemData.CreatedByType)
		}
		if &h.SystemData.LastModifiedBy != nil {
			out.Resource.SystemData.LastModifiedBy = h.SystemData.LastModifiedBy
		}
		if &h.SystemData.LastModifiedByType != nil {
			out.Resource.SystemData.LastModifiedByType = arm.CreatedByType(h.SystemData.LastModifiedByType)
		}
	}
	if &h.Location != nil {
		out.TrackedResource.Location = h.Location
	}
	out.Tags = make(map[string]string)
	for k, v := range h.Tags {
		if v != "" {
			out.Tags[k] = v
		}
	}
	if &h.Properties != nil {
		if &h.Properties.ProvisioningState != nil {
			out.Properties.ProvisioningState = arm.ProvisioningState(h.Properties.ProvisioningState)
		}
		if &h.Properties.Spec != nil {
			if &h.Properties.Spec.AutoRepair != nil {
				out.Properties.Spec.AutoRepair = h.Properties.Spec.AutoRepair
			}
			if &h.Properties.Spec.Version != nil {
				normalizeVersion(&h.Properties.Spec.Version, &out.Properties.Spec.Version)
			}
			if &h.Properties.Spec.Replicas != nil {
				out.Properties.Spec.Replicas = h.Properties.Spec.Replicas
			}
		}
		if &h.Properties.Spec.Platform != nil {
			normalizeNodePoolPlatform(&h.Properties.Spec.Platform, &out.Properties.Spec.Platform)
		}
		if &h.Properties.Spec.Autoscaling != nil {
			if &h.Properties.Spec.Autoscaling.Max != nil {
				out.Properties.Spec.Autoscaling.Max = h.Properties.Spec.Autoscaling.Max
			}
			if &h.Properties.Spec.Autoscaling.Min != nil {
				out.Properties.Spec.Autoscaling.Min = h.Properties.Spec.Autoscaling.Min
			}
		}
		out.Properties.Spec.Labels = make(map[string]string)
		for _, v := range h.Properties.Spec.Labels {
			if v != "" {
				out.Properties.Spec.Labels[v] = h.Properties.Spec.Labels[v]
			}
		}
		out.Properties.Spec.Taints = make([]*api.Taint, len(h.Properties.Spec.Taints))
		for i := range h.Properties.Spec.Taints {
			out.Properties.Spec.Taints[i] = &api.Taint{}
			if &h.Properties.Spec.Taints[i].Effect != nil {
				out.Properties.Spec.Taints[i].Effect = api.Effect(h.Properties.Spec.Taints[i].Effect)
			}
			if &h.Properties.Spec.Taints[i].Key != nil {
				out.Properties.Spec.Taints[i].Key = h.Properties.Spec.Taints[i].Key
			}
			if &h.Properties.Spec.Taints[i].Value != nil {
				out.Properties.Spec.Taints[i].Value = h.Properties.Spec.Taints[i].Value
			}
		}

		out.Properties.Spec.TuningConfigs = make([]string, len(h.Properties.Spec.TuningConfigs))
		for i := range h.Properties.Spec.TuningConfigs {
			out.Properties.Spec.TuningConfigs[i] = h.Properties.Spec.TuningConfigs[i]
		}
	}
}

func normalizeNodePoolPlatform(p *NodePoolPlatformProfile, out *api.NodePoolPlatformProfile) {
	if &p.VMSize != nil {
		out.VMSize = p.VMSize
	}
	if &p.AvailabilityZone != nil {
		out.AvailabilityZone = p.AvailabilityZone
	}
	if &p.DiskEncryptionSetID != nil {
		out.DiskEncryptionSetID = p.DiskEncryptionSetID
	}
	if &p.DiskSizeGiB != nil {
		out.DiskSizeGiB = p.DiskSizeGiB
	}
	if &p.DiskStorageAccountType != nil {
		out.DiskStorageAccountType = p.DiskStorageAccountType
	}
	if &p.EncryptionAtHost != nil {
		out.EncryptionAtHost = p.EncryptionAtHost
	}
	if &p.EphemeralOSDisk != nil {
		out.EphemeralOSDisk = p.EphemeralOSDisk
	}
	if &p.SubnetID != nil {
		out.SubnetID = p.SubnetID
	}

}

func (h *HCPOpenShiftClusterNodePool) ValidateStatic() *arm.CloudError {
	//TODO implement me
	return nil
}
