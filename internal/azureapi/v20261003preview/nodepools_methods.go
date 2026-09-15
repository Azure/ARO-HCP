// Copyright 2026 Microsoft Corporation
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

package v20261003preview

import (
	"strings"

	"k8s.io/apimachinery/pkg/util/validation/field"
	"k8s.io/utils/ptr"

	azcorearm "github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"

	"github.com/Azure/ARO-HCP/internal/api/coreapi"
	"github.com/Azure/ARO-HCP/internal/api/metadataapi"
	"github.com/Azure/ARO-HCP/internal/azureapi/v20261003preview/generated"
)

type NodePool struct {
	generated.NodePool
}

var _ coreapi.VersionedCreatableResource[coreapi.HCPOpenShiftClusterNodePool] = &NodePool{}

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
		obj.Properties.Version.ChannelGroup = ptr.To(coreapi.DefaultNodePoolVersionChannelGroup)
	}
	if obj.Properties.Platform == nil {
		obj.Properties.Platform = &generated.NodePoolPlatformProfile{}
	}
	if obj.Properties.Platform.OSDisk == nil {
		obj.Properties.Platform.OSDisk = &generated.OsDiskProfile{}
	}
	if obj.Properties.Platform.OSDisk.SizeGiB == nil {
		obj.Properties.Platform.OSDisk.SizeGiB = ptr.To(coreapi.DefaultNodePoolOSDiskSizeGiB)
	}
	if obj.Properties.Platform.OSDisk.DiskStorageAccountType == nil {
		obj.Properties.Platform.OSDisk.DiskStorageAccountType = ptr.To(generated.DiskStorageAccountTypePremiumLRS)
	}
	if obj.Properties.Platform.OSDisk.DiskType == nil {
		obj.Properties.Platform.OSDisk.DiskType = ptr.To(generated.OsDiskTypeManaged)
	}
	if obj.Properties.AutoRepair == nil {
		obj.Properties.AutoRepair = ptr.To(true)
	}
}

func (h *NodePool) GetVersion() coreapi.Version {
	return versionedInterface
}

func (h *NodePool) ConvertToInternal(existing *coreapi.HCPOpenShiftClusterNodePool) (*coreapi.HCPOpenShiftClusterNodePool, error) {
	out := &coreapi.HCPOpenShiftClusterNodePool{}
	errs := field.ErrorList{}

	// Reject null on required fields. On the PATCH path, JSON merge-patch
	// converts explicit null to a nil pointer. On the PUT path, defaults
	// are applied before the request body so nil here means the user
	// explicitly sent null (mergo does not override with nil).
	if h.Properties != nil {
		if h.Properties.AutoRepair == nil {
			errs = append(errs, field.Required(field.NewPath("properties", "autoRepair"), "field cannot be null"))
		}
	}

	if h.ID != nil {
		out.ID = metadataapi.Must(azcorearm.ParseResourceID(strings.ToLower(*h.ID)))
		out.ResourceID = metadataapi.Must(azcorearm.ParseResourceID(strings.ToLower(*h.ID)))
	}
	if h.Name != nil {
		out.Name = *h.Name
	}
	if h.Type != nil {
		out.Type = *h.Type
	}
	if h.SystemData != nil {
		out.SystemData = &coreapi.SystemData{
			CreatedAt:      h.SystemData.CreatedAt,
			LastModifiedAt: h.SystemData.LastModifiedAt,
		}
		if h.SystemData.CreatedBy != nil {
			out.SystemData.CreatedBy = *h.SystemData.CreatedBy
		}
		if h.SystemData.CreatedByType != nil {
			out.SystemData.CreatedByType = coreapi.CreatedByType(*h.SystemData.CreatedByType)
		}
		if h.SystemData.LastModifiedBy != nil {
			out.SystemData.LastModifiedBy = *h.SystemData.LastModifiedBy
		}
		if h.SystemData.LastModifiedByType != nil {
			out.SystemData.LastModifiedByType = coreapi.CreatedByType(*h.SystemData.LastModifiedByType)
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
	out.Tags = metadataapi.StringPtrMapToStringMap(h.Tags)
	if h.Properties != nil {
		if h.Properties.ProvisioningState != nil {
			out.Properties.ProvisioningState = coreapi.ProvisioningState(*h.Properties.ProvisioningState)
		}
		out.Properties.AutoRepair = metadataapi.Deref(h.Properties.AutoRepair)
		out.Properties.Replicas = metadataapi.Deref(h.Properties.Replicas)
		out.Properties.NodeDrainTimeoutMinutes = h.Properties.NodeDrainTimeoutMinutes
		if h.Properties.Version != nil {
			normalizeNodePoolVersion(h.Properties.Version, &out.Properties.Version)
		}
		if h.Properties.Platform != nil {
			errs = append(errs, normalizeNodePoolPlatform(field.NewPath("properties", "platform"), h.Properties.Platform, &out.Properties.Platform)...)
		}
		if h.Properties.AutoScaling != nil {
			out.Properties.AutoScaling = &coreapi.NodePoolAutoScaling{
				Max: metadataapi.Deref(h.Properties.AutoScaling.Max),
				Min: metadataapi.Deref(h.Properties.AutoScaling.Min),
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
			out.Properties.Taints = make([]coreapi.Taint, len(h.Properties.Taints))
			for i := range h.Properties.Taints {
				out.Properties.Taints[i].Effect = metadataapi.Effect(metadataapi.Deref(h.Properties.Taints[i].Effect))
				out.Properties.Taints[i].Key = metadataapi.Deref(h.Properties.Taints[i].Key)
				out.Properties.Taints[i].Value = metadataapi.Deref(h.Properties.Taints[i].Value)
			}
		}
	}

	out.Identity = normalizeManagedIdentity(h.Identity)

	if existing != nil {
		preserveUnknownNodePoolFields(existing, out)
	}

	return out, coreapi.CloudErrorFromFieldErrors(errs)
}

// preserveUnknownNodePoolFields copies customer-facing fields from existing that
// this API version doesn't know about. Currently empty — no cross-version
// customer fields exist yet between v20240610preview and v20260630preview.
func preserveUnknownNodePoolFields(from, to *coreapi.HCPOpenShiftClusterNodePool) {
}

func normalizeNodePoolVersion(p *generated.NodePoolVersionProfile, out *coreapi.NodePoolVersionProfile) {
	out.ID = metadataapi.Deref(p.ID)
	out.ChannelGroup = metadataapi.Deref(p.ChannelGroup)
}

func normalizeNodePoolPlatform(fldPath *field.Path, p *generated.NodePoolPlatformProfile, out *coreapi.NodePoolPlatformProfile) field.ErrorList {
	errs := field.ErrorList{}

	out.VMSize = metadataapi.Deref(p.VMSize)
	out.AvailabilityZone = metadataapi.Deref(p.AvailabilityZone)
	out.EnableEncryptionAtHost = metadataapi.Deref(p.EnableEncryptionAtHost)
	if p.OSDisk != nil {
		errs = append(errs, normalizeOSDiskProfile(fldPath.Child("osDisk"), p.OSDisk, &out.OSDisk)...)
	} else {
		out.OSDisk = coreapi.OSDiskProfile{}
	}
	if p.SubnetID != nil && len(*p.SubnetID) > 0 {
		if resourceID, err := azcorearm.ParseResourceID(*p.SubnetID); err != nil {
			errs = append(errs, field.Invalid(fldPath.Child("subnetID"), *p.SubnetID, err.Error()))
		} else {
			out.SubnetID = resourceID
		}
	} else {
		out.SubnetID = nil
	}
	return errs
}

func normalizeOSDiskProfile(fldPath *field.Path, p *generated.OsDiskProfile, out *coreapi.OSDiskProfile) field.ErrorList {
	errs := field.ErrorList{}

	out.SizeGiB = p.SizeGiB
	out.DiskStorageAccountType = metadataapi.DiskStorageAccountType(metadataapi.Deref(p.DiskStorageAccountType))
	if p.EncryptionSetID != nil && len(*p.EncryptionSetID) > 0 {
		if resourceID, err := azcorearm.ParseResourceID(*p.EncryptionSetID); err != nil {
			errs = append(errs, field.Invalid(fldPath.Child("encryptionSetID"), *p.EncryptionSetID, err.Error()))
		} else {
			out.EncryptionSetID = resourceID
		}
	} else {
		out.EncryptionSetID = nil
	}
	out.DiskType = metadataapi.OsDiskType(metadataapi.Deref(p.DiskType))
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

func newNodePoolVersionProfile(from *coreapi.NodePoolVersionProfile) generated.NodePoolVersionProfile {
	if from == nil {
		return generated.NodePoolVersionProfile{}
	}
	return generated.NodePoolVersionProfile{
		ID:           metadataapi.PtrOrNil(from.ID),
		ChannelGroup: metadataapi.PtrOrNil(from.ChannelGroup),
	}
}

func newNodePoolPlatformProfile(from *coreapi.NodePoolPlatformProfile) generated.NodePoolPlatformProfile {
	if from == nil {
		return generated.NodePoolPlatformProfile{}
	}
	return generated.NodePoolPlatformProfile{
		VMSize:           metadataapi.PtrOrNil(from.VMSize),
		AvailabilityZone: metadataapi.PtrOrNil(from.AvailabilityZone),
		// Use Ptr (not PtrOrNil) to ensure boolean is always present in JSON response, even when false
		EnableEncryptionAtHost: metadataapi.Ptr(from.EnableEncryptionAtHost),
		OSDisk:                 metadataapi.PtrOrNil(newOSDiskProfile(&from.OSDisk)),
		SubnetID:               metadataapi.ResourceIDToStringPtr(from.SubnetID),
	}
}

func newOSDiskProfile(from *coreapi.OSDiskProfile) generated.OsDiskProfile {
	if from == nil {
		return generated.OsDiskProfile{}
	}
	return generated.OsDiskProfile{
		SizeGiB:                from.SizeGiB,
		DiskStorageAccountType: metadataapi.PtrOrNil(generated.DiskStorageAccountType(from.DiskStorageAccountType)),
		EncryptionSetID:        metadataapi.ResourceIDToStringPtr(from.EncryptionSetID),
		DiskType:               metadataapi.Ptr(generated.OsDiskType(from.DiskType)),
	}
}

func newNodePoolAutoScaling(from *coreapi.NodePoolAutoScaling) generated.NodePoolAutoScaling {
	if from == nil {
		return generated.NodePoolAutoScaling{}
	}
	return generated.NodePoolAutoScaling{
		// Use Ptr (not PtrOrNil) to ensure int32 zero values are preserved in JSON response.
		Max: metadataapi.Ptr(from.Max),
		Min: metadataapi.Ptr(from.Min),
	}
}

// NewHCPOpenShiftClusterNodePool converts an internal representation to this API version.
// If from is nil, returns a defaulted external object for use on the write path
// where defaults are applied before unmarshaling the request body.
func (v version) NewHCPOpenShiftClusterNodePool(from *coreapi.HCPOpenShiftClusterNodePool) coreapi.VersionedHCPOpenShiftClusterNodePool {
	if from == nil {
		ret := &NodePool{}
		SetDefaultValuesNodePool(ret)
		return ret
	}

	idString := ""
	if from.ResourceID != nil {
		idString = from.ResourceID.String()
	}

	out := &NodePool{
		generated.NodePool{
			ID:         metadataapi.PtrOrNil(idString),
			Name:       metadataapi.PtrOrNil(from.Name),
			Type:       metadataapi.PtrOrNil(from.Type),
			SystemData: metadataapi.PtrOrNil(newSystemData(from.SystemData)),
			Location:   metadataapi.PtrOrNil(from.Location),
			Tags:       metadataapi.StringMapToStringPtrMap(from.Tags),
			Properties: &generated.NodePoolProperties{
				ProvisioningState: metadataapi.PtrOrNil(generated.ProvisioningState(from.Properties.ProvisioningState)),
				Platform:          metadataapi.PtrOrNil(newNodePoolPlatformProfile(&from.Properties.Platform)),
				Version:           metadataapi.PtrOrNil(newNodePoolVersionProfile(&from.Properties.Version)),
				// Use Ptr to preserve explicit false values in JSON responses (solves GET-then-PUT data loss).
				// See docs/api-version-defaults-and-storage.md for details.
				AutoRepair:  metadataapi.Ptr(from.Properties.AutoRepair),
				AutoScaling: metadataapi.PtrOrNil(newNodePoolAutoScaling(from.Properties.AutoScaling)),
				// Use Ptr (not PtrOrNil) to ensure int32 zero value is preserved in JSON response.
				Replicas:                metadataapi.Ptr(from.Properties.Replicas),
				NodeDrainTimeoutMinutes: from.Properties.NodeDrainTimeoutMinutes,
				Status:                  metadataapi.PtrOrNil(newNodePoolResourceStatus(&from.Status)),
			},
			Identity: newManagedServiceIdentity(from.Identity),
		},
	}

	if from.Properties.Labels != nil {
		out.Properties.Labels = make([]*generated.Label, 0, len(from.Properties.Labels))
	}
	for k, v := range from.Properties.Labels {
		out.Properties.Labels = append(out.Properties.Labels, &generated.Label{
			Key:   metadataapi.PtrOrNil(k),
			Value: metadataapi.PtrOrNil(v),
		})
	}

	if from.Properties.Taints != nil {
		out.Properties.Taints = make([]*generated.Taint, 0, len(from.Properties.Taints))
	}
	for _, t := range from.Properties.Taints {
		out.Properties.Taints = append(out.Properties.Taints, &generated.Taint{
			Effect: metadataapi.PtrOrNil(generated.Effect(t.Effect)),
			Key:    metadataapi.PtrOrNil(t.Key),
			Value:  metadataapi.PtrOrNil(t.Value),
		})
	}

	return out
}

func newNodePoolResourceStatus(from *coreapi.HCPOpenShiftClusterNodePoolStatus) generated.ResourceStatus {
	if from == nil {
		return generated.ResourceStatus{}
	}
	return generated.ResourceStatus{
		Conditions: newConditions(from.UserFacingConditions),
	}
}
