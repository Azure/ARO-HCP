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

package databasemutationhelpers

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/stretchr/testify/require"

	"k8s.io/apimachinery/pkg/api/equality"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"

	azcorearm "github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"

	"github.com/Azure/ARO-HCP/internal/api"
)

func ResourceInstanceEquals(t *testing.T, expected, actual any) (string, bool) {
	expectedBytes, err := json.Marshal(expected)
	require.NoError(t, err)
	actualBytes, err := json.Marshal(actual)
	require.NoError(t, err)

	expectedMap := map[string]any{}
	require.NoError(t, json.Unmarshal(expectedBytes, &expectedMap))
	actualMap := map[string]any{}
	require.NoError(t, json.Unmarshal(actualBytes, &actualMap))

	// clear the fields from TypedDocument (wrapper) that don't compare
	for _, currMap := range []map[string]any{expectedMap, actualMap} {
		unstructured.RemoveNestedField(currMap, "id") // TODO restore when names are predictable
		unstructured.RemoveNestedField(currMap, "_rid")
		unstructured.RemoveNestedField(currMap, "_self")
		unstructured.RemoveNestedField(currMap, "_etag")
		unstructured.RemoveNestedField(currMap, "_attachments")
		unstructured.RemoveNestedField(currMap, "_ts")
		unstructured.RemoveNestedField(currMap, "endTime") // for arm.Operation

		resourceType, ok := currMap["resourceType"].(string)
		if !ok || len(resourceType) == 0 {
			// this happens when not working directly against cosmos data
			if resourceIDString, ok := currMap["resourceId"].(string); ok { // usually where we hold it
				resourceID, err := azcorearm.ParseResourceID(resourceIDString)
				if err == nil {
					resourceType = resourceID.ResourceType.String()
				}
			} else {
				// otherwise start checking. operations are common
				if _, ok := currMap["operationId"].(string); ok {
					resourceType = api.OperationStatusResourceType.String()
				}
			}
		}

		// this loops handles the cosmosObj possibility and the internalObj possibility
		for _, possiblePrepend := range []string{"", "properties"} {
			unstructured.RemoveNestedField(currMap, prepend(possiblePrepend, "lastTransitionTime")...)                     // operations
			unstructured.RemoveNestedField(currMap, prepend(possiblePrepend, "startTime")...)                              // operations
			unstructured.RemoveNestedField(currMap, prepend(possiblePrepend, "operationId")...)                            // operations
			unstructured.RemoveNestedField(currMap, prepend(possiblePrepend, "cosmosUID")...)                              // controllers
			unstructured.RemoveNestedField(currMap, prepend(possiblePrepend, "serviceProviderProperties", "cosmosUID")...) // cluster, nodepool, externalauth

			for _, nestedPossiblePrepend := range []string{"", "intermediateResourceDoc"} {
				unstructured.RemoveNestedField(currMap, prepend(possiblePrepend, prepend(nestedPossiblePrepend, "activeOperationId")...)...) // cluster, nodepool, externalauth
				unstructured.RemoveNestedField(currMap, prepend(possiblePrepend, prepend(nestedPossiblePrepend, "internalId")...)...)        // cluster, nodepool, externalauth
			}

			// for controllers
			for _, nestedPossiblePrepend := range []string{"", "internalState"} {
				expectedConditions, found, err := unstructured.NestedSlice(currMap, prepend(possiblePrepend, prepend(nestedPossiblePrepend, "status", "conditions")...)...)
				if found && err == nil {
					for i := range expectedConditions {
						delete(expectedConditions[i].(map[string]any), "lastTransitionTime")
					}
					if err := unstructured.SetNestedSlice(currMap, expectedConditions, prepend(possiblePrepend, prepend(nestedPossiblePrepend, "status", "conditions")...)...); err != nil {
						panic(err)
					}
				}
			}

			switch {
			case strings.EqualFold(resourceType, api.OperationStatusResourceType.String()):
				// this field is UUID generated, so usually cannot be compared for operations, but CAN be compared for everything else.
				unstructured.RemoveNestedField(currMap, prepend(possiblePrepend, "resourceId")...)
			}
		}
	}

	return cmp.Diff(expectedMap, actualMap), equality.Semantic.DeepEqual(expectedMap, actualMap)
}

func prepend(first string, rest ...string) []string {
	if len(first) == 0 {
		return rest
	}
	return append([]string{first}, rest...)
}
