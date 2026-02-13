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
	"encoding/json"

	azcorearm "github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"
)

// ManagementClusterContent represents the content of a K8s resource in the
// management cluster.
type ManagementClusterContent struct {
	// CosmosMetadata ResourceID is nested under the cluster so that association and cleanup work as expected
	CosmosMetadata `json:"cosmosMetadata"`

	// resourceID exists to match cosmosMetadata.resourceID until we're able to transition all types to use cosmosMetadata,
	// at which point we will stop using properties.resourceId in our queries. That will be about a month from now.
	ResourceID azcorearm.ResourceID `json:"resourceId"`

	// TODO is this correct and what we want? do we prefer runtime.RawExtension? how would marshal and unmarshal work in that case? considering Obj or Raw might be nil?
	// TODO are we concerned about the size? Max document size of Cosmos is a hard limit of 2MB.
	// Because we use Maestro with StatusFeedback of "@" it is quite big as it is twice the length of the original
	// resource in the bundle.
	// Content is stored as raw JSON so it can be round-tripped without the JSON unmarshaler needing a concrete client.Object type.
	Content json.RawMessage `json:"content"`
}
