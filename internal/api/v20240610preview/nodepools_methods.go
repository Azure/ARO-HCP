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

func SetDefaultValuesNodePool(obj *NodePool) {
	if obj.Properties == nil {
		obj.Properties = &generated.NodePoolProperties{}
	}
	if obj.Properties.Version == nil {
		obj.Properties.Version = &generated.NodePoolVersionProfile{}
	}
	if obj.Properties.Version.ChannelGroup == nil {
		obj.Properties.Version.ChannelGroup = ptr.To(api.DefaultNodePoolVersionChannelGroup)
	}
	if obj.Properties.Platform == nil {
		obj.Properties.Platform = &generated.NodePoolPlatformProfile{}
	}
	if obj.Properties.Platform.OSDisk == nil {
		obj.Properties.Platform.OSDisk = &generated.OsDiskProfile{}
	}
	if obj.Properties.Platform.OSDisk.SizeGiB == nil {
		obj.Properties.Platform.OSDisk.SizeGiB = ptr.To(api.DefaultNodePoolOSDiskSizeGiB)
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

// ZeroOwnedFields zeros the fields of 'internal' that this API version (v20240610preview) owns.
// OSDisk.DiskType is NOT zeroed — it was added in v20251223preview and must be preserved
// from the existing Cosmos document verbatim.
func (h *NodePool) ZeroOwnedFields(internal *api.HCPOpenShiftClusterNodePool) {
	internal.ID = nil
	internal.Name = ""
	internal.Type = ""
	internal.Location = ""
	internal.Tags = nil
	internal.Identity = nil
	internal.SystemData = nil

	internal.Properties.AutoRepair = false
	internal.Properties.Replicas = 0
	internal.Properties.NodeDrainTimeoutMinutes = nil
	internal.Properties.Version = api.NodePoolVersionProfile{}

	// Platform: zero only v20240610preview-known fields.
	// OSDisk.DiskType is NOT zeroed — it was added in v20251223preview.
	internal.Properties.Platform.VMSize = ""
	internal.Properties.Platform.AvailabilityZone = ""
	internal.Properties.Platform.EnableEncryptionAtHost = false
	internal.Properties.Platform.SubnetID = nil
	internal.Properties.Platform.OSDisk.SizeGiB = nil
	internal.Properties.Platform.OSDisk.DiskStorageAccountType = ""
	internal.Properties.Platform.OSDisk.EncryptionSetID = nil
	// OSDisk.DiskType is intentionally NOT zeroed — preserved from Cosmos.

	internal.Properties.AutoScaling = nil
	internal.Properties.Labels = nil
	internal.Properties.Taints = nil
}

// ApplyOwnedFields copies values from the receiver (external object populated from the request
// body) into 'internal', for all fields this v20240610preview version owns.
// Null checks on required fields are at the TOP before any normalization.
func (h *NodePool) ApplyOwnedFields(internal *api.HCPOpenShiftClusterNodePool) error {
	errs := field.ErrorList{}

	// No v20240610preview-specific null rejection checks for required fields
	// beyond SetDefaultValuesNodePool guarantees.

	if h.ID != nil {
		internal.ID = api.Must(azcorearm.ParseResourceID(strings.ToLower(*h.ID)))
	}
	if h.Name != nil {
		internal.Name = *h.Name
	}
	if h.Type != nil {
		internal.Type = *h.Type
	}
	if h.SystemData != nil {
		internal.SystemData = &arm.SystemData{
			CreatedAt:      h.SystemData.CreatedAt,
			LastModifiedAt: h.SystemData.LastModifiedAt,
		}
		if h.SystemData.CreatedBy != nil {
			internal.SystemData.CreatedBy = *h.SystemData.CreatedBy
		}
		if h.SystemData.CreatedByType != nil {
			internal.SystemData.CreatedByType = arm.CreatedByType(*h.SystemData.CreatedByType)
		}
		if h.SystemData.LastModifiedBy != nil {
			internal.SystemData.LastModifiedBy = *h.SystemData.LastModifiedBy
		}
		if h.SystemData.LastModifiedByType != nil {
			internal.SystemData.LastModifiedByType = arm.CreatedByType(*h.SystemData.LastModifiedByType)
		}
	}
	if h.Location != nil {
		internal.Location = *h.Location
	}
	// Per RPC-Patch-V1-04, the Tags field does NOT follow
	// JSON merge-patch (RFC 7396) semantics:
	//
	//   When Tags are patched, the tags from the request
	//   replace all existing tags for the resource
	//
	internal.Tags = api.StringPtrMapToStringMap(h.Tags)
	if h.Properties != nil {
		if h.Properties.AutoRepair != nil {
			internal.Properties.AutoRepair = *h.Properties.AutoRepair
		}
		if h.Properties.Version != nil {
			normalizeNodePoolVersion(h.Properties.Version, &internal.Properties.Version)
		}
		if h.Properties.Replicas != nil {
			internal.Properties.Replicas = *h.Properties.Replicas
		}
		if h.Properties.Platform != nil {
			errs = append(errs, normalizeNodePoolPlatform(field.NewPath("properties", "platform"), h.Properties.Platform, &internal.Properties.Platform)...)
		}
		if h.Properties.AutoScaling != nil {
			internal.Properties.AutoScaling = &api.NodePoolAutoScaling{}
			if h.Properties.AutoScaling.Max != nil {
				internal.Properties.AutoScaling.Max = *h.Properties.AutoScaling.Max
			}
			if h.Properties.AutoScaling.Min != nil {
				internal.Properties.AutoScaling.Min = *h.Properties.AutoScaling.Min
			}
		}
		if h.Properties.Labels != nil {
			internal.Properties.Labels = make(map[string]string)
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
				internal.Properties.Labels[key] = value
			}
		}

		if h.Properties.Taints != nil {
			internal.Properties.Taints = make([]api.Taint, len(h.Properties.Taints))
			for i := range h.Properties.Taints {
				if h.Properties.Taints[i].Effect != nil {
					internal.Properties.Taints[i].Effect = api.Effect(*h.Properties.Taints[i].Effect)
				}
				if h.Properties.Taints[i].Key != nil {
					internal.Properties.Taints[i].Key = *h.Properties.Taints[i].Key
				}
				if h.Properties.Taints[i].Value != nil {
					internal.Properties.Taints[i].Value = *h.Properties.Taints[i].Value
				}
			}
		}

		internal.Properties.NodeDrainTimeoutMinutes = h.Properties.NodeDrainTimeoutMinutes
	}

	internal.Identity = normalizeManagedIdentity(h.Identity)

	// Field preservation is structural: OSDisk.DiskType (added in v20251223preview)
	// is preserved verbatim from the base via ZeroOwnedFields leaving it untouched.

	return arm.CloudErrorFromFieldErrors(errs)
}

func normalizeNodePoolVersion(p *generated.NodePoolVersionProfile, out *api.NodePoolVersionProfile) {
	if p.ID != nil {
		out.ID = *p.ID
	}
	if p.ChannelGroup != nil {
		out.ChannelGroup = *p.ChannelGroup
	}
}

func normalizeNodePoolPlatform(fldPath *field.Path, p *generated.NodePoolPlatformProfile, out *api.NodePoolPlatformProfile) field.ErrorList {
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

func normalizeOSDiskProfile(fldPath *field.Path, p *generated.OsDiskProfile, out *api.OSDiskProfile) field.ErrorList {
	errs := field.ErrorList{}

	if p.SizeGiB != nil {
		out.SizeGiB = p.SizeGiB
	}
	if p.DiskStorageAccountType != nil {
		out.DiskStorageAccountType = api.DiskStorageAccountType(*p.DiskStorageAccountType)
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
		VMSize:           api.PtrOrNil(from.VMSize),
		AvailabilityZone: api.PtrOrNil(from.AvailabilityZone),
		// Use Ptr (not PtrOrNil) to ensure boolean is always present in JSON response, even when false
		EnableEncryptionAtHost: api.Ptr(from.EnableEncryptionAtHost),
		OSDisk:                 api.PtrOrNil(newOSDiskProfile(&from.OSDisk)),
		SubnetID:               api.ResourceIDToStringPtr(from.SubnetID),
	}
}

func newOSDiskProfile(from *api.OSDiskProfile) generated.OsDiskProfile {
	if from == nil {
		return generated.OsDiskProfile{}
	}
	return generated.OsDiskProfile{
		SizeGiB:                from.SizeGiB,
		DiskStorageAccountType: api.PtrOrNil(generated.DiskStorageAccountType(from.DiskStorageAccountType)),
		EncryptionSetID:        api.ResourceIDToStringPtr(from.EncryptionSetID),
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

// NewHCPOpenShiftClusterNodePool converts an internal representation to this API version.
// If from is nil, returns a defaulted external object for use on the write path
// where defaults are applied before unmarshaling the request body.
func (v version) NewHCPOpenShiftClusterNodePool(from *api.HCPOpenShiftClusterNodePool) api.VersionedHCPOpenShiftClusterNodePool {
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
			ID:         api.PtrOrNil(idString),
			Name:       api.PtrOrNil(from.Name),
			Type:       api.PtrOrNil(from.Type),
			SystemData: api.PtrOrNil(newSystemData(from.SystemData)),
			Location:   api.PtrOrNil(from.Location),
			Tags:       api.StringMapToStringPtrMap(from.Tags),
			Properties: &generated.NodePoolProperties{
				ProvisioningState: api.PtrOrNil(generated.ProvisioningState(from.Properties.ProvisioningState)),
				Platform:          api.PtrOrNil(newNodePoolPlatformProfile(&from.Properties.Platform)),
				Version:           api.PtrOrNil(newNodePoolVersionProfile(&from.Properties.Version)),
				// PtrOrNil retained for backward compatibility in this shipped API version.
				// Note: AutoRepair=false is omitted from GET responses, causing GET-then-PUT
				// data loss. Fixed in v20251223preview via Ptr. See docs/api-version-defaults-and-storage.md.
				AutoRepair:              api.PtrOrNil(from.Properties.AutoRepair),
				AutoScaling:             api.PtrOrNil(newNodePoolAutoScaling(from.Properties.AutoScaling)),
				Replicas:                api.PtrOrNil(from.Properties.Replicas),
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
			Key:   api.PtrOrNil(k),
			Value: api.PtrOrNil(v),
		})
	}

	if from.Properties.Taints != nil {
		out.Properties.Taints = make([]*generated.Taint, 0, len(from.Properties.Taints))
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
