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
	"strings"

	"k8s.io/apimachinery/pkg/util/validation/field"
	"k8s.io/utils/ptr"

	azcorearm "github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"

	"github.com/Azure/ARO-HCP/internal/api/v20240610preview/generated"
	resourcesapi "github.com/Azure/ARO-HCP/internal/apis/resources"
	armresourcesapi "github.com/Azure/ARO-HCP/internal/apis/resources/arm"
)

type NodePool struct {
	generated.NodePool
}

var _ resourcesapi.VersionedCreatableResource[resourcesapi.HCPOpenShiftClusterNodePool] = &NodePool{}

func (h *NodePool) NewExternal() any {
	return &NodePool{}
}

func SetDefaultValuesNodePool(obj *NodePool) {
	if obj.Properties == nil {
		obj.Properties = &generated.NodePoolProperties{}
	}
	if obj.Properties.Version == nil {
		obj.Properties.Version = &generated.NodePoolVersionProfile{}
	}
	if obj.Properties.Version.ChannelGroup == nil {
		obj.Properties.Version.ChannelGroup = ptr.To(resourcesapi.DefaultNodePoolVersionChannelGroup)
	}
	if obj.Properties.Platform == nil {
		obj.Properties.Platform = &generated.NodePoolPlatformProfile{}
	}
	if obj.Properties.Platform.OSDisk == nil {
		obj.Properties.Platform.OSDisk = &generated.OsDiskProfile{}
	}
	if obj.Properties.Platform.OSDisk.SizeGiB == nil {
		obj.Properties.Platform.OSDisk.SizeGiB = ptr.To(resourcesapi.DefaultNodePoolOSDiskSizeGiB)
	}
	if obj.Properties.Platform.OSDisk.DiskStorageAccountType == nil {
		obj.Properties.Platform.OSDisk.DiskStorageAccountType = ptr.To(generated.DiskStorageAccountTypePremiumLRS)
	}
	if obj.Properties.AutoRepair == nil {
		obj.Properties.AutoRepair = ptr.To(true)
	}
}

func (h *NodePool) GetVersion() resourcesapi.Version {
	return versionedInterface
}

func (h *NodePool) ConvertToInternal(existing *resourcesapi.HCPOpenShiftClusterNodePool) (*resourcesapi.HCPOpenShiftClusterNodePool, error) {
	out := &resourcesapi.HCPOpenShiftClusterNodePool{}
	errs := field.ErrorList{}

	if h.ID != nil {
		out.ID = resourcesapi.Must(azcorearm.ParseResourceID(strings.ToLower(*h.ID)))
	}
	if h.Name != nil {
		out.Name = *h.Name
	}
	if h.Type != nil {
		out.Type = *h.Type
	}
	if h.SystemData != nil {
		out.SystemData = &armresourcesapi.SystemData{
			CreatedAt:      h.SystemData.CreatedAt,
			LastModifiedAt: h.SystemData.LastModifiedAt,
		}
		if h.SystemData.CreatedBy != nil {
			out.SystemData.CreatedBy = *h.SystemData.CreatedBy
		}
		if h.SystemData.CreatedByType != nil {
			out.SystemData.CreatedByType = armresourcesapi.CreatedByType(*h.SystemData.CreatedByType)
		}
		if h.SystemData.LastModifiedBy != nil {
			out.SystemData.LastModifiedBy = *h.SystemData.LastModifiedBy
		}
		if h.SystemData.LastModifiedByType != nil {
			out.SystemData.LastModifiedByType = armresourcesapi.CreatedByType(*h.SystemData.LastModifiedByType)
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
	out.Tags = resourcesapi.StringPtrMapToStringMap(h.Tags)
	if h.Properties != nil {
		if h.Properties.ProvisioningState != nil {
			out.Properties.ProvisioningState = armresourcesapi.ProvisioningState(*h.Properties.ProvisioningState)
		}
		if h.Properties.AutoRepair != nil {
			out.Properties.AutoRepair = *h.Properties.AutoRepair
		}
		if h.Properties.Version != nil {
			normalizeNodePoolVersion(h.Properties.Version, &out.Properties.Version)
		}
		if h.Properties.Replicas != nil {
			out.Properties.Replicas = *h.Properties.Replicas
		}
		if h.Properties.Platform != nil {
			errs = append(errs, normalizeNodePoolPlatform(field.NewPath("properties", "platform"), h.Properties.Platform, &out.Properties.Platform)...)
		}
		if h.Properties.AutoScaling != nil {
			out.Properties.AutoScaling = &resourcesapi.NodePoolAutoScaling{}
			if h.Properties.AutoScaling.Max != nil {
				out.Properties.AutoScaling.Max = *h.Properties.AutoScaling.Max
			}
			if h.Properties.AutoScaling.Min != nil {
				out.Properties.AutoScaling.Min = *h.Properties.AutoScaling.Min
			}
		}
		if h.Properties.Labels != nil {
			out.Properties.Labels = make(map[string]string)
			for _, v := range h.Properties.Labels {
				if v == nil {
					continue
				}
				var value string

				if v.Value != nil {
					value = *v.Value
				}

				// "" becomes nil when going internal -> external
				// that means to round trip, we must go "" -> nil -> ""
				key := ptr.Deref(v.Key, "")
				out.Properties.Labels[key] = value
			}
		}

		if h.Properties.Taints != nil {
			out.Properties.Taints = make([]resourcesapi.Taint, len(h.Properties.Taints))
			for i := range h.Properties.Taints {
				if h.Properties.Taints[i].Effect != nil {
					out.Properties.Taints[i].Effect = resourcesapi.Effect(*h.Properties.Taints[i].Effect)
				}
				if h.Properties.Taints[i].Key != nil {
					out.Properties.Taints[i].Key = *h.Properties.Taints[i].Key
				}
				if h.Properties.Taints[i].Value != nil {
					out.Properties.Taints[i].Value = *h.Properties.Taints[i].Value
				}
			}
		}

		out.Properties.NodeDrainTimeoutMinutes = h.Properties.NodeDrainTimeoutMinutes
	}

	out.Identity = normalizeManagedIdentity(h.Identity)

	if existing != nil {
		preserveUnknownNodePoolFields(existing, out)
	}

	return out, armresourcesapi.CloudErrorFromFieldErrors(errs)
}

// preserveUnknownNodePoolFields copies customer-facing fields from existing that
// this API version (2024-06-10-preview) doesn't know about.
func preserveUnknownNodePoolFields(from, to *resourcesapi.HCPOpenShiftClusterNodePool) {
	// DiskType was added in v20251223preview.
	to.Properties.Platform.OSDisk.DiskType = from.Properties.Platform.OSDisk.DiskType
}

func normalizeNodePoolVersion(p *generated.NodePoolVersionProfile, out *resourcesapi.NodePoolVersionProfile) {
	if p.ID != nil {
		out.ID = *p.ID
	}
	if p.ChannelGroup != nil {
		out.ChannelGroup = *p.ChannelGroup
	}
}

func normalizeNodePoolPlatform(fldPath *field.Path, p *generated.NodePoolPlatformProfile, out *resourcesapi.NodePoolPlatformProfile) field.ErrorList {
	errs := field.ErrorList{}

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
		errs = append(errs, normalizeOSDiskProfile(fldPath.Child("osDisk"), p.OSDisk, &out.OSDisk)...)
	}
	if p.SubnetID != nil && len(*p.SubnetID) > 0 {
		if resourceID, err := azcorearm.ParseResourceID(*p.SubnetID); err != nil {
			errs = append(errs, field.Invalid(fldPath.Child("subnetID"), *p.SubnetID, err.Error()))
		} else {
			out.SubnetID = resourceID
		}
	}
	return errs
}

func normalizeOSDiskProfile(fldPath *field.Path, p *generated.OsDiskProfile, out *resourcesapi.OSDiskProfile) field.ErrorList {
	errs := field.ErrorList{}

	if p.SizeGiB != nil {
		out.SizeGiB = p.SizeGiB
	}
	if p.DiskStorageAccountType != nil {
		out.DiskStorageAccountType = resourcesapi.DiskStorageAccountType(*p.DiskStorageAccountType)
	}
	if p.EncryptionSetID != nil && len(*p.EncryptionSetID) > 0 {
		if resourceID, err := azcorearm.ParseResourceID(*p.EncryptionSetID); err != nil {
			errs = append(errs, field.Invalid(fldPath.Child("encryptionSetID"), *p.EncryptionSetID, err.Error()))
		} else {
			out.EncryptionSetID = resourceID
		}
	}

	return errs
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

func newNodePoolVersionProfile(from *resourcesapi.NodePoolVersionProfile) generated.NodePoolVersionProfile {
	if from == nil {
		return generated.NodePoolVersionProfile{}
	}
	return generated.NodePoolVersionProfile{
		ID:           resourcesapi.PtrOrNil(from.ID),
		ChannelGroup: resourcesapi.PtrOrNil(from.ChannelGroup),
	}
}

func newNodePoolPlatformProfile(from *resourcesapi.NodePoolPlatformProfile) generated.NodePoolPlatformProfile {
	if from == nil {
		return generated.NodePoolPlatformProfile{}
	}
	return generated.NodePoolPlatformProfile{
		VMSize:           resourcesapi.PtrOrNil(from.VMSize),
		AvailabilityZone: resourcesapi.PtrOrNil(from.AvailabilityZone),
		// Use Ptr (not PtrOrNil) to ensure boolean is always present in JSON response, even when false
		EnableEncryptionAtHost: resourcesapi.Ptr(from.EnableEncryptionAtHost),
		OSDisk:                 resourcesapi.PtrOrNil(newOSDiskProfile(&from.OSDisk)),
		SubnetID:               resourcesapi.ResourceIDToStringPtr(from.SubnetID),
	}
}

func newOSDiskProfile(from *resourcesapi.OSDiskProfile) generated.OsDiskProfile {
	if from == nil {
		return generated.OsDiskProfile{}
	}
	return generated.OsDiskProfile{
		SizeGiB:                from.SizeGiB,
		DiskStorageAccountType: resourcesapi.PtrOrNil(generated.DiskStorageAccountType(from.DiskStorageAccountType)),
		EncryptionSetID:        resourcesapi.ResourceIDToStringPtr(from.EncryptionSetID),
	}
}

func newNodePoolAutoScaling(from *resourcesapi.NodePoolAutoScaling) generated.NodePoolAutoScaling {
	if from == nil {
		return generated.NodePoolAutoScaling{}
	}
	return generated.NodePoolAutoScaling{
		Max: resourcesapi.PtrOrNil(from.Max),
		Min: resourcesapi.PtrOrNil(from.Min),
	}
}

// NewHCPOpenShiftClusterNodePool converts an internal representation to this API version.
// If from is nil, returns a defaulted external object for use on the write path
// where defaults are applied before unmarshaling the request body.
func (v version) NewHCPOpenShiftClusterNodePool(from *resourcesapi.HCPOpenShiftClusterNodePool) resourcesapi.VersionedHCPOpenShiftClusterNodePool {
	if from == nil {
		ret := &NodePool{}
		SetDefaultValuesNodePool(ret)
		return ret
	}

	idString := ""
	if from.ID != nil {
		idString = from.ID.String()
	}

	out := &NodePool{
		generated.NodePool{
			ID:         resourcesapi.PtrOrNil(idString),
			Name:       resourcesapi.PtrOrNil(from.Name),
			Type:       resourcesapi.PtrOrNil(from.Type),
			SystemData: resourcesapi.PtrOrNil(newSystemData(from.SystemData)),
			Location:   resourcesapi.PtrOrNil(from.Location),
			Tags:       resourcesapi.StringMapToStringPtrMap(from.Tags),
			Properties: &generated.NodePoolProperties{
				ProvisioningState: resourcesapi.PtrOrNil(generated.ProvisioningState(from.Properties.ProvisioningState)),
				Platform:          resourcesapi.PtrOrNil(newNodePoolPlatformProfile(&from.Properties.Platform)),
				Version:           resourcesapi.PtrOrNil(newNodePoolVersionProfile(&from.Properties.Version)),
				// PtrOrNil retained for backward compatibility in this shipped API version.
				// Note: AutoRepair=false is omitted from GET responses, causing GET-then-PUT
				// data loss. Fixed in v20251223preview via Ptr. See docs/api-version-defaults-and-storage.md.
				AutoRepair:              resourcesapi.PtrOrNil(from.Properties.AutoRepair),
				AutoScaling:             resourcesapi.PtrOrNil(newNodePoolAutoScaling(from.Properties.AutoScaling)),
				Replicas:                resourcesapi.PtrOrNil(from.Properties.Replicas),
				NodeDrainTimeoutMinutes: from.Properties.NodeDrainTimeoutMinutes,
			},
			Identity: newManagedServiceIdentity(from.Identity),
		},
	}

	if from.Properties.Labels != nil {
		out.Properties.Labels = make([]*generated.Label, 0, len(from.Properties.Labels))
	}
	for k, v := range from.Properties.Labels {
		out.Properties.Labels = append(out.Properties.Labels, &generated.Label{
			Key:   resourcesapi.PtrOrNil(k),
			Value: resourcesapi.PtrOrNil(v),
		})
	}

	if from.Properties.Taints != nil {
		out.Properties.Taints = make([]*generated.Taint, 0, len(from.Properties.Taints))
	}
	for _, t := range from.Properties.Taints {
		out.Properties.Taints = append(out.Properties.Taints, &generated.Taint{
			Effect: resourcesapi.PtrOrNil(generated.Effect(t.Effect)),
			Key:    resourcesapi.PtrOrNil(t.Key),
			Value:  resourcesapi.PtrOrNil(t.Value),
		})
	}

	return out
}
