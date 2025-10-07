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
	"fmt"

	"k8s.io/utils/ptr"

	"github.com/Azure/ARO-HCP/internal/api"
	"github.com/Azure/ARO-HCP/internal/api/arm"
	"github.com/Azure/ARO-HCP/internal/api/v20240610preview/generated"
)

type NodePool struct {
	generated.NodePool
}

var _ api.VersionedCreatableResource[api.HCPOpenShiftClusterNodePool] = &NodePool{}

func (h *NodePool) NewExternal() any {
	return &NodePool{}
}

func (h *NodePool) SetDefaultValues(uncast any) error {
	obj, ok := uncast.(*NodePool)
	if !ok {
		return fmt.Errorf("unexpected type %T", uncast)
	}

	SetDefaultValuesNodePool(obj)
	return nil
}

func SetDefaultValuesNodePool(obj *NodePool) {
	if obj.Properties == nil {
		obj.Properties = &generated.NodePoolProperties{}
	}
	if obj.Properties.Version == nil {
		obj.Properties.Version = &generated.NodePoolVersionProfile{}
	}
	if obj.Properties.Version.ChannelGroup == nil {
		obj.Properties.Version.ChannelGroup = ptr.To("stable")
	}
	if obj.Properties.Platform == nil {
		obj.Properties.Platform = &generated.NodePoolPlatformProfile{}
	}
	if obj.Properties.Platform.OSDisk == nil {
		obj.Properties.Platform.OSDisk = &generated.OsDiskProfile{}
	}
	if obj.Properties.Platform.OSDisk.SizeGiB == nil {
		obj.Properties.Platform.OSDisk.SizeGiB = ptr.To(int32(64))
	}
	if obj.Properties.Platform.OSDisk.DiskStorageAccountType == nil {
		obj.Properties.Platform.OSDisk.DiskStorageAccountType = ptr.To(generated.DiskStorageAccountTypePremiumLRS)
	}
	if obj.Properties.AutoRepair == nil {
		obj.Properties.AutoRepair = ptr.To(true)
	}
}

func (h *NodePool) GetVersion() api.Version {
	return versionedInterface
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
			if v != nil && v.Key != nil {
				var value string

				if v.Value != nil {
					value = *v.Value
				}

				out.Properties.Labels[*v.Key] = value
			}
		}
		out.Properties.Taints = make([]api.Taint, len(h.Properties.Taints))
		for i := range h.Properties.Taints {
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
		out.Properties.NodeDrainTimeoutMinutes = h.Properties.NodeDrainTimeoutMinutes
	}
}

func normalizeNodePoolVersion(p *generated.NodePoolVersionProfile, out *api.NodePoolVersionProfile) {
	if p.ID != nil {
		out.ID = *p.ID
	}
	if p.ChannelGroup != nil {
		out.ChannelGroup = *p.ChannelGroup
	}
}

func normalizeNodePoolPlatform(p *generated.NodePoolPlatformProfile, out *api.NodePoolPlatformProfile) {
	if p.VMSize != nil {
		out.VMSize = *p.VMSize
	}
	if p.AvailabilityZone != nil {
		out.AvailabilityZone = *p.AvailabilityZone
	}
	if p.EnableEncryptionAtHost != nil {
		out.EnableEncryptionAtHost = *p.EnableEncryptionAtHost
	}
	if p.OSDisk != nil {
		normalizeOSDiskProfile(p.OSDisk, &out.OSDisk)
	}
	if p.SubnetID != nil {
		out.SubnetID = *p.SubnetID
	}
}

func normalizeOSDiskProfile(p *generated.OsDiskProfile, out *api.OSDiskProfile) {
	if p.SizeGiB != nil {
		out.SizeGiB = *p.SizeGiB
	}
	if p.DiskStorageAccountType != nil {
		out.DiskStorageAccountType = api.DiskStorageAccountType(*p.DiskStorageAccountType)
	}
	if p.EncryptionSetID != nil {
		out.EncryptionSetID = *p.EncryptionSetID
	}
	if p.Persistence != nil {
		out.Persistence = api.PersistenceType(*p.Persistence)
	}
}

func (h *NodePool) GetVisibility(path string) (api.VisibilityFlags, bool) {
	flags, ok := nodePoolVisibilityMap[path]
	return flags, ok
}

func (h *NodePool) ValidateVisibility(current api.VersionedCreatableResource[api.HCPOpenShiftClusterNodePool], updating bool) []arm.CloudErrorBody {
	var structTagMap = api.GetStructTagMap[api.HCPOpenShiftClusterNodePool]()
	return api.ValidateVisibility(h, current.(*NodePool), nodePoolVisibilityMap, structTagMap, updating)
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

func newNodePoolVersionProfile(from *api.NodePoolVersionProfile) generated.NodePoolVersionProfile {
	if from == nil {
		return generated.NodePoolVersionProfile{}
	}
	return generated.NodePoolVersionProfile{
		ID:           api.PtrOrNil(from.ID),
		ChannelGroup: api.PtrOrNil(from.ChannelGroup),
	}
}

func newNodePoolPlatformProfile(from *api.NodePoolPlatformProfile) generated.NodePoolPlatformProfile {
	if from == nil {
		return generated.NodePoolPlatformProfile{}
	}
	return generated.NodePoolPlatformProfile{
		VMSize:                 api.PtrOrNil(from.VMSize),
		AvailabilityZone:       api.PtrOrNil(from.AvailabilityZone),
		EnableEncryptionAtHost: api.PtrOrNil(from.EnableEncryptionAtHost),
		OSDisk:                 api.PtrOrNil(newOSDiskProfile(&from.OSDisk)),
		SubnetID:               api.PtrOrNil(from.SubnetID),
	}
}

func newOSDiskProfile(from *api.OSDiskProfile) generated.OsDiskProfile {
	if from == nil {
		return generated.OsDiskProfile{}
	}
	return generated.OsDiskProfile{
		SizeGiB:                api.PtrOrNil(from.SizeGiB),
		DiskStorageAccountType: api.PtrOrNil(generated.DiskStorageAccountType(from.DiskStorageAccountType)),
		EncryptionSetID:        api.PtrOrNil(from.EncryptionSetID),
		Persistence:            api.PtrOrNil(generated.PersistenceType(from.Persistence)),
	}
}

func newNodePoolAutoScaling(from *api.NodePoolAutoScaling) generated.NodePoolAutoScaling {
	if from == nil {
		return generated.NodePoolAutoScaling{}
	}
	return generated.NodePoolAutoScaling{
		Max: api.PtrOrNil(from.Max),
		Min: api.PtrOrNil(from.Min),
	}
}

func (v version) NewHCPOpenShiftClusterNodePool(from *api.HCPOpenShiftClusterNodePool) api.VersionedHCPOpenShiftClusterNodePool {
	if from == nil {
		ret := &NodePool{}
		SetDefaultValuesNodePool(ret)
		return ret
	}

	out := &NodePool{
		generated.NodePool{
			ID:         api.PtrOrNil(from.ID),
			Name:       api.PtrOrNil(from.Name),
			Type:       api.PtrOrNil(from.Type),
			SystemData: api.PtrOrNil(newSystemData(from.SystemData)),
			Location:   api.PtrOrNil(from.Location),
			Tags:       api.StringMapToStringPtrMap(from.Tags),
			Properties: &generated.NodePoolProperties{
				ProvisioningState:       api.PtrOrNil(generated.ProvisioningState(from.Properties.ProvisioningState)),
				Platform:                api.PtrOrNil(newNodePoolPlatformProfile(&from.Properties.Platform)),
				Version:                 api.PtrOrNil(newNodePoolVersionProfile(&from.Properties.Version)),
				AutoRepair:              api.PtrOrNil(from.Properties.AutoRepair),
				AutoScaling:             api.PtrOrNil(newNodePoolAutoScaling(from.Properties.AutoScaling)),
				Replicas:                api.PtrOrNil(from.Properties.Replicas),
				NodeDrainTimeoutMinutes: from.Properties.NodeDrainTimeoutMinutes,
			},
		},
	}

	for k, v := range from.Properties.Labels {
		out.Properties.Labels = append(out.Properties.Labels, &generated.Label{
			Key:   api.PtrOrNil(k),
			Value: api.PtrOrNil(v),
		})
	}

	for _, t := range from.Properties.Taints {
		out.Properties.Taints = append(out.Properties.Taints, &generated.Taint{
			Effect: api.PtrOrNil(generated.Effect(t.Effect)),
			Key:    api.PtrOrNil(t.Key),
			Value:  api.PtrOrNil(t.Value),
		})
	}

	return out
}
