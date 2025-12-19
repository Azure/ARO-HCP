package databasemutationhelpers

import (
	"encoding/json"
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/stretchr/testify/require"
	"k8s.io/apimachinery/pkg/api/equality"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
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

		// this loops handles the cosmosObj possibility and the internalObj possibility
		for _, possiblePrepend := range []string{"", "properties"} {
			unstructured.RemoveNestedField(currMap, prepend(possiblePrepend, "lastTransitionTime")...)                     // operations
			unstructured.RemoveNestedField(currMap, prepend(possiblePrepend, "startTime")...)                              // operations
			unstructured.RemoveNestedField(currMap, prepend(possiblePrepend, "operationId")...)                            // operations
			unstructured.RemoveNestedField(currMap, prepend(possiblePrepend, "activeOperationId")...)                      // cluster, nodepool, externalauth
			unstructured.RemoveNestedField(currMap, prepend(possiblePrepend, "internalId")...)                             // cluster, nodepool, externalauth
			unstructured.RemoveNestedField(currMap, prepend(possiblePrepend, "cosmosUID")...)                              // controllers
			unstructured.RemoveNestedField(currMap, prepend(possiblePrepend, "serviceProviderProperties", "cosmosUID")...) // cluster, nodepool, externalauth

			// for controllers
			expectedConditions, found, err := unstructured.NestedSlice(currMap, prepend(possiblePrepend, "internalState", "status", "conditions")...)
			if found && err == nil {
				for i := range expectedConditions {
					delete(expectedConditions[i].(map[string]any), "lastTransitionTime")
				}
				if err := unstructured.SetNestedSlice(currMap, expectedConditions, prepend(possiblePrepend, "internalState", "status", "conditions")...); err != nil {
					panic(err)
				}
			}

			actualConditions, found, err := unstructured.NestedSlice(currMap, prepend(possiblePrepend, "internalState", "status", "conditions")...)
			if found && err == nil {
				for i := range actualConditions {
					delete(actualConditions[i].(map[string]any), "lastTransitionTime")
				}
				if err := unstructured.SetNestedSlice(currMap, actualConditions, prepend(possiblePrepend, "internalState", "status", "conditions")...); err != nil {
					panic(err)
				}
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
