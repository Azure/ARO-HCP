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
	"errors"
	"strings"

	azcorearm "github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"

	"github.com/Azure/ARO-HCP/internal/api/arm"
)

type CosmosPersistable interface {
	GetCosmosData() CosmosData
	SetCosmosDocumentData(cosmosUID string)
}

// CosmosData contains the information that persisted resources must have for us to support CRUD against them.
// These are not (currently) all stored in the same place in our various types.
type CosmosData = arm.CosmosData

func ResourceIDToCosmosID(resourceID *azcorearm.ResourceID) (string, error) {
	if resourceID == nil {
		return "", errors.New("resource ID is nil")
	}
	return ResourceIDStringToCosmosID(resourceID.String())
}

func ResourceIDStringToCosmosID(resourceID string) (string, error) {
	if len(resourceID) == 0 {
		return "", errors.New("resource ID is empty")
	}
	// cosmos uses a REST API, which means that IDs that contain slashes cause problems with URL handling.
	// We chose | because that is a delimiter that is not allowed inside of an ARM resource ID because it is a separator
	// for multiple resource IDs.
	return strings.ReplaceAll(strings.ToLower(resourceID), "/", "|"), nil
}

func CosmosIDToResourceID(resourceID string) (*azcorearm.ResourceID, error) {
	return azcorearm.ParseResourceID(strings.ReplaceAll(resourceID, "|", "/"))
}
