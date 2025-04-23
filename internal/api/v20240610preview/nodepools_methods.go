// Copyright 2025 Microsoft Corporation
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package v20240610preview

import (
	"net/http"

	"github.com/Azure/ARO-HCP/internal/api"
	"github.com/Azure/ARO-HCP/internal/api/arm"
	"github.com/Azure/ARO-HCP/internal/api/v20240610preview/generated"
)

type NodePool struct {
	generated.NodePool
}

func (h *NodePool) Normalize(out *api.HCPOpenShiftClusterNodePool) {
	if h.ID != nil {
		out.ID = *h.ID
	}
	if h.Name != nil {
		out.Name = *h.Name
	}
	if h.Type != nil {
		out.Type = *h.Type
	}
	if h.SystemData != nil {
		out.SystemData = &arm.SystemData{
			CreatedAt:      h.SystemData.CreatedAt,
			LastModifiedAt: h.SystemData.LastModifiedAt,
		}
		if h.SystemData.CreatedBy != nil {
			out.SystemData.CreatedBy = *h.SystemData.CreatedBy
		}
		if h.SystemData.CreatedByType != nil {
			out.SystemData.CreatedByType = arm.CreatedByType(*h.SystemData.CreatedByType)
		}
		if h.SystemData.LastModifiedBy != nil {
			out.SystemData.LastModifiedBy = *h.SystemData.LastModifiedBy
		}
		if h.SystemData.LastModifiedByType != nil {
			out.SystemData.LastModifiedByType = arm.CreatedByType(*h.SystemData.LastModifiedByType)
		}
	}
	if h.Location != nil {
		out.Location = *h.Location
	}
	// Per RPC-Patch-V1-04, the Tags field does NOT follow
	// JSON merge-patch (RFC 7396) semantics:
	//
	//   When Tags are patched, the tags from the request
	//   replace all existing tags for the resource
	//
	out.Tags = api.StringPtrMapToStringMap(h.Tags)
	if h.Properties != nil {
		if h.Properties.ProvisioningState != nil {
			out.Properties.ProvisioningState = arm.ProvisioningState(*h.Properties.ProvisioningState)
		}
		if h.Properties != nil {
			if h.Properties.AutoRepair != nil {
				out.Properties.AutoRepair = *h.Properties.AutoRepair
			}
			if h.Properties.Version != nil {
				normalizeNodePoolVersion(h.Properties.Version, &out.Properties.Version)
			}
			if h.Properties.Replicas != nil {
				out.Properties.Replicas = *h.Properties.Replicas
			}
		}
		if h.Properties.Platform != nil {
			normalizeNodePoolPlatform(h.Properties.Platform, &out.Properties.Platform)
		}
		if h.Properties.AutoScaling != nil {
			out.Properties.AutoScaling = &api.NodePoolAutoScaling{}
			if h.Properties.AutoScaling.Max != nil {
				out.Properties.AutoScaling.Max = *h.Properties.AutoScaling.Max
			}
			if h.Properties.AutoScaling.Min != nil {
				out.Properties.AutoScaling.Min = *h.Properties.AutoScaling.Min
			}
		}
		out.Properties.Labels = make(map[string]string)
		for _, v := range h.Properties.Labels {
			if v != nil {
				out.Properties.Labels[*v.Key] = *v.Value
			}
		}
		out.Properties.Taints = make([]*api.Taint, len(h.Properties.Taints))
		for i := range h.Properties.Taints {
			out.Properties.Taints[i] = &api.Taint{}
			if h.Properties.Taints[i].Effect != nil {
				out.Properties.Taints[i].Effect = api.Effect(*h.Properties.Taints[i].Effect)
			}
			if h.Properties.Taints[i].Key != nil {
				out.Properties.Taints[i].Key = *h.Properties.Taints[i].Key
			}
			if h.Properties.Taints[i].Value != nil {
				out.Properties.Taints[i].Value = *h.Properties.Taints[i].Value
			}
		}
	}
}

func normalizeNodePoolVersion(p *generated.NodePoolVersionProfile, out *api.NodePoolVersionProfile) {
	if p.ID != nil {
		out.ID = *p.ID
	}
	if p.ChannelGroup != nil {
		out.ChannelGroup = *p.ChannelGroup
	}
	out.AvailableUpgrades = api.StringPtrSliceToStringSlice(p.AvailableUpgrades)
}

func normalizeNodePoolPlatform(p *generated.NodePoolPlatformProfile, out *api.NodePoolPlatformProfile) {
	if p.VMSize != nil {
		out.VMSize = *p.VMSize
	}
	if p.AvailabilityZone != nil {
		out.AvailabilityZone = *p.AvailabilityZone
	}
	if p.DiskSizeGiB != nil {
		out.DiskSizeGiB = *p.DiskSizeGiB
	}
	if p.DiskStorageAccountType != nil {
		out.DiskStorageAccountType = api.DiskStorageAccountType(*p.DiskStorageAccountType)
	}
	if p.SubnetID != nil {
		out.SubnetID = *p.SubnetID
	}

}

func (h *NodePool) ValidateStatic(current api.VersionedHCPOpenShiftClusterNodePool, updating bool, method string) *arm.CloudError {
	var normalized api.HCPOpenShiftClusterNodePool
	var errorDetails []arm.CloudErrorBody

	cloudError := arm.NewCloudError(
		http.StatusBadRequest,
		arm.CloudErrorCodeMultipleErrorsOccurred, "",
		"Content validation filed on multiple fields")
	cloudError.Details = make([]arm.CloudErrorBody, 0)

	// Pass the embedded NodePool so
	// the struct field names match the nodePoolStructTagMap keys.
	errorDetails = api.ValidateVisibility(
		h.NodePool,
		current.(*NodePool).NodePool,
		nodePoolStructTagMap, updating)
	if errorDetails != nil {
		cloudError.Details = append(cloudError.Details, errorDetails...)
	}

	h.Normalize(&normalized)

	errorDetails = api.ValidateRequest(validate, method, &normalized)
	if errorDetails != nil {
		cloudError.Details = append(cloudError.Details, errorDetails...)
	}

	switch len(cloudError.Details) {
	case 0:
		cloudError = nil
	case 1:
		// Promote a single validation error out of details.
		cloudError.CloudErrorBody = &cloudError.Details[0]
	}

	return cloudError
}

type NodePoolVersionProfile struct {
	generated.NodePoolVersionProfile
}

type NodePoolPlatformProfile struct {
	generated.NodePoolPlatformProfile
}

type NodePoolAutoScaling struct {
	generated.NodePoolAutoScaling
}

func newNodePoolVersionProfile(from *api.NodePoolVersionProfile) *generated.NodePoolVersionProfile {
	return &generated.NodePoolVersionProfile{
		ID:                api.Ptr(from.ID),
		ChannelGroup:      api.Ptr(from.ChannelGroup),
		AvailableUpgrades: api.StringSliceToStringPtrSlice(from.AvailableUpgrades),
	}
}

func newNodePoolPlatformProfile(from *api.NodePoolPlatformProfile) *generated.NodePoolPlatformProfile {
	return &generated.NodePoolPlatformProfile{
		VMSize:                 api.Ptr(from.VMSize),
		AvailabilityZone:       api.Ptr(from.AvailabilityZone),
		DiskSizeGiB:            api.Ptr(from.DiskSizeGiB),
		DiskStorageAccountType: api.Ptr(generated.DiskStorageAccountType(from.DiskStorageAccountType)),
		SubnetID:               api.Ptr(from.SubnetID),
	}
}

func newNodePoolAutoScaling(from *api.NodePoolAutoScaling) *generated.NodePoolAutoScaling {
	var autoScaling *generated.NodePoolAutoScaling

	if from != nil {
		autoScaling = &generated.NodePoolAutoScaling{
			Max: api.Ptr(from.Max),
			Min: api.Ptr(from.Min),
		}
	}

	return autoScaling
}

func newNodePoolTaint(from *api.Taint) *generated.Taint {
	return &generated.Taint{
		Effect: api.Ptr(generated.Effect(from.Effect)),
		Key:    api.Ptr(from.Key),
		Value:  api.Ptr(from.Value),
	}
}

func (v version) NewHCPOpenShiftClusterNodePool(from *api.HCPOpenShiftClusterNodePool) api.VersionedHCPOpenShiftClusterNodePool {
	if from == nil {
		from = api.NewDefaultHCPOpenShiftClusterNodePool()
	}

	out := &NodePool{
		generated.NodePool{
			ID:       api.Ptr(from.ID),
			Name:     api.Ptr(from.Name),
			Type:     api.Ptr(from.Type),
			Location: api.Ptr(from.Location),
			Tags:     api.StringMapToStringPtrMap(from.Tags),
			Properties: &generated.NodePoolProperties{
				ProvisioningState: api.Ptr(generated.ProvisioningState(from.Properties.ProvisioningState)),
				Platform:          newNodePoolPlatformProfile(&from.Properties.Platform),
				Version:           newNodePoolVersionProfile(&from.Properties.Version),
				AutoRepair:        api.Ptr(from.Properties.AutoRepair),
				AutoScaling:       newNodePoolAutoScaling(from.Properties.AutoScaling),
				Labels:            []*generated.Label{},
				Replicas:          api.Ptr(from.Properties.Replicas),
				Taints:            make([]*generated.Taint, len(from.Properties.Taints)),
			},
		},
	}

	if from.SystemData != nil {
		out.SystemData = &generated.SystemData{
			CreatedBy:          api.Ptr(from.SystemData.CreatedBy),
			CreatedByType:      api.Ptr(generated.CreatedByType(from.SystemData.CreatedByType)),
			CreatedAt:          from.SystemData.CreatedAt,
			LastModifiedBy:     api.Ptr(from.SystemData.LastModifiedBy),
			LastModifiedByType: api.Ptr(generated.CreatedByType(from.SystemData.LastModifiedByType)),
			LastModifiedAt:     from.SystemData.LastModifiedAt,
		}
	}

	for k, v := range from.Properties.Labels {
		out.Properties.Labels = append(out.Properties.Labels, &generated.Label{
			Key:   api.Ptr(k),
			Value: api.Ptr(v),
		})
	}

	for i := range from.Properties.Taints {
		out.Properties.Taints[i] = newNodePoolTaint(from.Properties.Taints[i])
	}

	return out
}

func (v version) MarshalHCPOpenShiftClusterNodePool(from *api.HCPOpenShiftClusterNodePool) ([]byte, error) {
	return arm.MarshalJSON(v.NewHCPOpenShiftClusterNodePool(from))
}
