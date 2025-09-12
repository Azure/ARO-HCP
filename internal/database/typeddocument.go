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
	"fmt"
	"strings"

	azcorearm "github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"
	"github.com/Azure/azure-sdk-for-go/sdk/data/azcosmos"
)

// typedDocumentError signifies a mismatched Type field and Properties type
// when attempting to unmarshal JSON-encoded data.
type typedDocumentError struct {
	invalidType    string
	propertiesType string
}

func (e typedDocumentError) Error() string {
	if e.invalidType == "" {
		return "missing type"
	}

	return fmt.Sprintf("invalid type '%s' for %s", e.invalidType, e.propertiesType)
}

// TypedDocument is a BaseDocument with a ResourceType field to
// help distinguish heterogeneous items in a Cosmos DB container.
// The Properties field can be unmarshalled to any type that
// implements the DocumentProperties interface.
type TypedDocument struct {
	BaseDocument
	PartitionKey string `json:"partitionKey"`
	ResourceType string `json:"resourceType"`
	//Properties   json.RawMessage `json:"properties"`
}

const typedDocumentJSONPathProperties = "/properties"

// NewTypedDocument returns a TypedDocument from a ResourceType.
func NewTypedDocument(partitionKey string, resourceType azcorearm.ResourceType) *TypedDocument {
	return &TypedDocument{
		BaseDocument: NewBaseDocument(),
		PartitionKey: strings.ToLower(partitionKey),
		ResourceType: strings.ToLower(resourceType.String()),
	}
}

// getPartitionKey returns an azcosmos.PartitionKey.
func (td *TypedDocument) getPartitionKey() azcosmos.PartitionKey {
	return azcosmos.NewPartitionKeyString(td.PartitionKey)
}

// typedDocumentMarshal returns the JSON encoding of typedDoc with innerDoc
// as the properties value. First, however, typedDocumentMarshal validates
// the type field in typeDoc against innerDoc to ensure compatibility. If
// validation fails, typedDocumentMarshal returns a typedDocumentError.
func typedDocumentMarshal(obj any) ([]byte, error) {
	return json.Marshal(obj)
}

// typedDocumentUnmarshal parses JSON-encoded data into a TypedDocument,
// validates the type field against the type parameter T, and then parses
// the JSON-encoded properties data into an instance of type parameter T.
// If validation fails, typedDocumentUnmarshal returns a typedDocumentError.
func typedDocumentUnmarshal[T any](data []byte) (*T, error) {
	var ret T

	err := json.Unmarshal(data, &ret)
	if err != nil {
		return nil, err
	}

	return &ret, nil
}
