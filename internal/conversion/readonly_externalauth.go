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

package conversion

import (
	"github.com/Azure/ARO-HCP/internal/api"
	"github.com/Azure/ARO-HCP/internal/api/arm"
)

func CopyReadOnlyProxyResourceValues(dest, src *arm.ProxyResource) {
	dest.ID = src.ID
	dest.Name = src.Name
	dest.Type = src.Type
	dest.SystemData = src.SystemData.DeepCopy()
}

func CopyReadOnlyExternalAuthValues(dest, src *api.HCPOpenShiftClusterExternalAuth) {
	CopyReadOnlyProxyResourceValues(&dest.ProxyResource, &src.ProxyResource)

	dest.Properties.ProvisioningState = src.Properties.ProvisioningState
	src.Properties.Condition.DeepCopyInto(&dest.Properties.Condition)
	src.ServiceProviderProperties.DeepCopyInto(&dest.ServiceProviderProperties)
	dest.CosmosETag = src.CosmosETag
	src.Status.DeepCopyInto(&dest.Status)
}
