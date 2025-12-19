package databasemutationhelpers

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/Azure/ARO-HCP/internal/api"
	azcorearm "github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"
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

		currResourceType, _, typedDocumentationErr := unstructured.NestedString(currMap, "resourceType")
		if len(currResourceType) == 0 {
			// try for the resource ID location in our internalObjs
			currResourceIDString, _, internalObjErr := unstructured.NestedString(currMap, "resourceID")
			if len(currResourceIDString) == 0 {
				require.NoError(t, errors.Join(typedDocumentationErr, internalObjErr))
			}
			resourceID, err := azcorearm.ParseResourceID(currResourceIDString)
			require.NoError(t, err)
			currResourceType = resourceID.ResourceType.String()
		}

		// this loops handles the cosmosObj possibility and the internalObj possibility
		for _, possiblePrepend := range []string{"", "properties"} {
			unstructured.RemoveNestedField(currMap, prepend(possiblePrepend, "cosmosUID")...)
			unstructured.RemoveNestedField(currMap, prepend(possiblePrepend, "serviceProviderProperties", "cosmosUID")...)

			switch strings.ToLower(currResourceType) {
			case strings.ToLower(api.ClusterControllerResourceType.String()),
				strings.ToLower(api.NodePoolControllerResourceType.String()),
				strings.ToLower(api.ExternalAuthControllerResourceType.String()):

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
			default:
				// just do nothing and let it work itself out
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
