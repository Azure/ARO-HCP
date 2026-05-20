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

package controllerutils

import (
	"fmt"

	azcorearm "github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"

	"github.com/Azure/ARO-HCP/internal/api"
)

// NewInitialManagementClusterContent returns a new ManagementClusterContent with
// the given full managementClusterContents ARM resource ID.
// The returned value can be used to consistently initialize a new ManagementClusterContent
func NewInitialManagementClusterContent(managementClusterContentResourceID *azcorearm.ResourceID) *api.ManagementClusterContent {
	return &api.ManagementClusterContent{
		CosmosMetadata: api.CosmosMetadata{
			ResourceID: managementClusterContentResourceID,
		},
	}
}

// ManagementClusterContentResourceIDFromParentResourceID returns the resource ID for the
// ManagementClusterContent nested under parentResourceID with the given
// maestro bundle internal name.
func ManagementClusterContentResourceIDFromParentResourceID(parentResourceID *azcorearm.ResourceID, maestroBundleInternalName api.MaestroBundleInternalName) *azcorearm.ResourceID {
	return api.Must(azcorearm.ParseResourceID(fmt.Sprintf("%s/%s/%s", parentResourceID.String(), api.ManagementClusterContentResourceTypeName, maestroBundleInternalName)))
}
