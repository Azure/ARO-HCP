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

package v20251223preview

import (
	"github.com/Azure/ARO-HCP/internal/api/v20251223preview/generated"
	resourcesapi "github.com/Azure/ARO-HCP/internal/apis/resources"
)

type HcpOpenShiftVersion struct {
	generated.HcpOpenShiftVersion
}

func (v version) NewHCPOpenShiftVersion(from *resourcesapi.HCPOpenShiftVersion) resourcesapi.VersionedHCPOpenShiftVersion {
	idString := ""
	if from.ID != nil {
		idString = from.ID.String()
	}

	return &HcpOpenShiftVersion{
		generated.HcpOpenShiftVersion{
			ID:   resourcesapi.PtrOrNil(idString),
			Name: resourcesapi.PtrOrNil(from.Name),
			Type: resourcesapi.PtrOrNil(from.Type),
			Properties: &generated.HcpOpenShiftVersionProperties{
				ChannelGroup: resourcesapi.PtrOrNil(from.Properties.ChannelGroup),
				// Use Ptr (not PtrOrNil) to ensure boolean is always present in JSON response, even when false
				Enabled:            resourcesapi.Ptr(from.Properties.Enabled),
				EndOfLifeTimestamp: resourcesapi.PtrOrNil(from.Properties.EndOfLifeTimestamp),
			},
		},
	}
}

func (v *HcpOpenShiftVersion) GetVersion() resourcesapi.Version {
	return versionedInterface
}
