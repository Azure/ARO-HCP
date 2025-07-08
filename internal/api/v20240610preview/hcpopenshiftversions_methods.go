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
	"github.com/Azure/ARO-HCP/internal/api"
	"github.com/Azure/ARO-HCP/internal/api/arm"
	"github.com/Azure/ARO-HCP/internal/api/v20240610preview/generated"
)

type HcpOpenShiftVersion struct {
	generated.HcpOpenShiftVersion
}

func NewHcpOpenShiftVersion(from *api.HCPOpenShiftVersion) *HcpOpenShiftVersion {

	return &HcpOpenShiftVersion{
		generated.HcpOpenShiftVersion{
			ID:   api.PtrOrNil(from.ID),
			Name: api.PtrOrNil(from.Name),
			Type: api.PtrOrNil(from.Type),
			Properties: &generated.HcpOpenShiftVersionProperties{
				ChannelGroup:       api.PtrOrNil(from.Properties.ChannelGroup),
				Enabled:            api.PtrOrNil(from.Properties.Enabled),
				EndOfLifeTimestamp: api.PtrOrNil(from.Properties.EndOfLifeTimestamp),
			},
		},
	}
}

func (v version) MarshalHCPOpenShiftVersion(from *api.HCPOpenShiftVersion) ([]byte, error) {
	return arm.MarshalJSON(NewHcpOpenShiftVersion(from))
}
