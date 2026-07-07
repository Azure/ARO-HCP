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
	"testing"

	azcorearm "github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"
	"github.com/stretchr/testify/assert"
)

func TestTypedDocumentClone_NilReceiver(t *testing.T) {
	var d *TypedDocument
	assert.Nil(t, d.Clone())
}

func TestTypedDocumentClone_DeepCopy(t *testing.T) {
	resourceID, err := azcorearm.ParseResourceID("/subscriptions/test-sub/resourceGroups/test-rg/providers/Microsoft.RedHatOpenShift/hcpOpenShiftClusters/test-cluster")
	assert.NoError(t, err)

	doc := &TypedDocument{
		BaseDocument: BaseDocument{
			ID:                 "test-id",
			TimeToLive:         60,
			CosmosResourceID:   "rid-1",
			CosmosSelf:         "self-1",
			CosmosAttachments:  "attachments-1",
			CosmosTimestamp:    1234,
		},
		PartitionKey: "test-sub",
		ResourceID:   resourceID,
		ResourceType: "Operation",
		Properties:   json.RawMessage(`{"status":"Succeeded"}`),
	}

	clone := doc.Clone()
	assert.NotNil(t, clone)
	assert.Equal(t, *doc, *clone)

	assert.NotSame(t, doc, clone)
	assert.NotSame(t, doc.ResourceID, clone.ResourceID)
	if len(doc.Properties) > 0 && len(clone.Properties) > 0 {
		assert.NotEqual(t, &doc.Properties[0], &clone.Properties[0])
	}

	clone.ID = "updated-id"
	clone.PartitionKey = "other-sub"
	clone.ResourceType = "Cluster"
	clone.ResourceID.Name = "updated-cluster"
	clone.Properties[0] = 'X'

	assert.Equal(t, "test-id", doc.ID)
	assert.Equal(t, "test-sub", doc.PartitionKey)
	assert.Equal(t, "Operation", doc.ResourceType)
	assert.Equal(t, "test-cluster", doc.ResourceID.Name)
	assert.Equal(t, json.RawMessage(`{"status":"Succeeded"}`), doc.Properties)
}
