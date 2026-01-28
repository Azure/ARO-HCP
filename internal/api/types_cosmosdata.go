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

package api

import (
	"strings"

	azcorearm "github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"

	"github.com/Azure/ARO-HCP/internal/api/arm"
)

type CosmosMetadata = arm.CosmosMetadata
type CosmosMetadataAccessor = arm.CosmosMetadataAccessor
type CosmosPersistable = arm.CosmosPersistable
type CosmosData = arm.CosmosMetadata

var (
	ResourceIDToCosmosID       = arm.ResourceIDToCosmosID
	ResourceIDStringToCosmosID = arm.ResourceIDStringToCosmosID
)

func CosmosIDToResourceID(resourceID string) (*azcorearm.ResourceID, error) {
	return azcorearm.ParseResourceID(strings.ReplaceAll(resourceID, "|", "/"))
}
