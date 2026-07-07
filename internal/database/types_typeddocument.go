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

package database

import (
	"encoding/json"
	"log/slog"
	"strings"

	azcorearm "github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"

	"github.com/Azure/ARO-HCP/internal/api"
)

// TypedDocument is a BaseDocument with a ResourceType field to
// help distinguish heterogeneous items in a Cosmos DB container.
// The Properties field can be unmarshalled to any type that
// implements the DocumentProperties interface.
type TypedDocument struct {
	BaseDocument
	PartitionKey string                `json:"partitionKey"`
	ResourceID   *azcorearm.ResourceID `json:"resourceID"`
	ResourceType string                `json:"resourceType"`
	Properties   json.RawMessage       `json:"properties"`
}

// rawTypedDocument is an alias used to log TypedDocument without recursing
// back into LogValue.
type rawTypedDocument TypedDocument

// LogValue implements slog.LogValuer. For HCP cluster documents, Properties is
// deserialized into the typed struct so that SystemData.LogValue() can redact
// createdBy/lastModifiedBy. All other resource types are logged as-is.
func (d TypedDocument) LogValue() slog.Value {
	if strings.EqualFold(d.ResourceType, api.ClusterResourceType.String()) {
		var cluster api.HCPOpenShiftCluster
		if err := json.Unmarshal(d.Properties, &cluster); err != nil {
			return slog.GroupValue(
				slog.String("resourceType", d.ResourceType),
				slog.Any("resourceID", d.ResourceID),
				slog.String("unmarshalError", err.Error()),
			)
		}
		return slog.AnyValue(cluster)
	}
	return slog.AnyValue(rawTypedDocument(d))
}
