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
	"github.com/Azure/ARO-HCP/internal/api/coreapi"
	"github.com/Azure/ARO-HCP/internal/api/metadataapi"
	"github.com/Azure/ARO-HCP/internal/azureapi/v20261003preview/generated"
)

type HcpOpenShiftVersion struct {
	generated.HcpOpenShiftVersion
}

func (v version) NewHCPOpenShiftVersion(from *coreapi.HCPOpenShiftVersion) coreapi.VersionedHCPOpenShiftVersion {
	idString := ""
	if from.ID != nil {
		idString = from.ID.String()
	}

	return &HcpOpenShiftVersion{
		generated.HcpOpenShiftVersion{
			ID:   metadataapi.PtrOrNil(idString),
			Name: metadataapi.PtrOrNil(from.Name),
			Type: metadataapi.PtrOrNil(from.Type),
			Properties: &generated.HcpOpenShiftVersionProperties{
				ChannelGroup: metadataapi.PtrOrNil(from.Properties.ChannelGroup),
				// Use Ptr (not PtrOrNil) to ensure boolean is always present in JSON response, even when false
				Enabled:            metadataapi.Ptr(from.Properties.Enabled),
				EndOfLifeTimestamp: metadataapi.PtrOrNil(from.Properties.EndOfLifeTimestamp),
			},
		},
	}
}

func (v *HcpOpenShiftVersion) GetVersion() coreapi.Version {
	return versionedInterface
}
