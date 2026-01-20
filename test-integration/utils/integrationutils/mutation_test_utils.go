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

package integrationutils

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"io/fs"
	"os"
	"strings"
	"testing"

	"dario.cat/mergo"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/util/rand"
	"k8s.io/apimachinery/pkg/util/sets"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"

	csarhcpv1alpha1 "github.com/openshift-online/ocm-api-model/clientapi/arohcp/v1alpha1"
	arohcpv1alpha1 "github.com/openshift-online/ocm-sdk-go/arohcp/v1alpha1"

	"github.com/Azure/ARO-HCP/internal/api/arm"
	"github.com/Azure/ARO-HCP/internal/ocm"
)

func TrivialPassThroughClusterServiceMock(t *testing.T, testInfo *FrontendIntegrationTestInfo, initialDataDir fs.FS) error {
	internalIDToCluster := testInfo.GetOrCreateMockData(t.Name() + "_clusters")
	internalIDToExternalAuth := testInfo.GetOrCreateMockData(t.Name() + "_externalAuths")
	internalIDToNodePool := testInfo.GetOrCreateMockData(t.Name() + "_nodePools")
	internalIDToAutoscaler := testInfo.GetOrCreateMockData(t.Name() + "_autoscalers")

	if initialDataDir != nil {
		dirContent, err := fs.ReadDir(initialDataDir, ".")
		if err != nil {
			return fmt.Errorf("failed to read dir: %w", err)
		}

		for _, dirEntry := range dirContent {
			if dirEntry.IsDir() {
				return fmt.Errorf("dir %s is not a file", dirEntry.Name())
			}
			fileReader, err := initialDataDir.Open(dirEntry.Name())
			if err != nil {
				return fmt.Errorf("failed to open file %s: %w", dirEntry.Name(), err)
			}
			fileContent, err := io.ReadAll(fileReader)
			if err != nil {
				return fmt.Errorf("failed to read file %s: %w", dirEntry.Name(), err)
			}

			switch {
			case strings.HasSuffix(dirEntry.Name(), "-cluster.json"):
				obj, err := arohcpv1alpha1.UnmarshalCluster(fileContent)
				if err != nil {
					return fmt.Errorf("failed to unmarshal cluster: %w", err)
				}
				if _, exists := internalIDToCluster[obj.HREF()]; exists {
					return fmt.Errorf("duplicate cluster: %s", obj.HREF())
				}
				internalIDToCluster[obj.HREF()] = []any{obj}

			case strings.HasSuffix(dirEntry.Name(), "-externalauth.json"):
				obj, err := arohcpv1alpha1.UnmarshalExternalAuth(fileContent)
				if err != nil {
					return fmt.Errorf("failed to unmarshal nodepool: %w", err)
				}
				if _, exists := internalIDToExternalAuth[obj.HREF()]; exists {
					return fmt.Errorf("duplicate nodepool: %s", obj.HREF())
				}
				internalIDToExternalAuth[obj.HREF()] = []any{obj}

			case strings.HasSuffix(dirEntry.Name(), "-nodepool.json"):
				obj, err := arohcpv1alpha1.UnmarshalNodePool(fileContent)
				if err != nil {
					return fmt.Errorf("failed to unmarshal nodepool: %w", err)
				}
				if _, exists := internalIDToNodePool[obj.HREF()]; exists {
					return fmt.Errorf("duplicate nodepool: %s", obj.HREF())
				}
				internalIDToNodePool[obj.HREF()] = []any{obj}

			case strings.HasSuffix(dirEntry.Name(), "-autoscaler.json"):
				obj, err := arohcpv1alpha1.UnmarshalClusterAutoscaler(fileContent)
				if err != nil {
					return fmt.Errorf("failed to unmarshal cluster: %w", err)
				}
				if _, exists := internalIDToAutoscaler[obj.HREF()]; exists {
					return fmt.Errorf("duplicate autoscaler: %s", obj.HREF())
				}
				internalIDToAutoscaler[obj.HREF()] = []any{obj}

			default:
				return fmt.Errorf("unknown file %s", dirEntry.Name())
			}
		}
	}

	testInfo.MockClusterServiceClient.EXPECT().PostCluster(gomock.Any(), gomock.Any(), gomock.Any()).DoAndReturn(func(ctx context.Context, clusterBuilder *csarhcpv1alpha1.ClusterBuilder, autoscalerBuilder *csarhcpv1alpha1.ClusterAutoscalerBuilder) (*csarhcpv1alpha1.Cluster, error) {
		justID := rand.String(10)
		internalID := "/api/clusters_mgmt/v1/clusters/" + justID

		if autoscalerBuilder != nil {
			autoscaler, err := autoscalerBuilder.HREF(internalID).Build()
			if err != nil {
				return nil, err
			}

			internalIDToAutoscaler[internalID] = append(internalIDToAutoscaler[internalID], autoscaler)
		}

		ret, err := clusterBuilder.ID(justID).HREF(internalID).Build()
		if err != nil {
			return nil, err
		}

		// these values are normally looked up directly from azure inside of cluster-service.  For mocks we do it here.
		ret, err = addFakeAzureIdentityData(ret)
		if err != nil {
			return nil, err
		}

		internalIDToCluster[internalID] = append(internalIDToCluster[internalID], ret)
		return ret, nil
	}).AnyTimes()
	testInfo.MockClusterServiceClient.EXPECT().UpdateCluster(gomock.Any(), gomock.Any(), gomock.Any()).DoAndReturn(func(ctx context.Context, id ocm.InternalID, builder *arohcpv1alpha1.ClusterBuilder) (*arohcpv1alpha1.Cluster, error) {
		ret, err := builder.Build()
		if err != nil {
			return nil, err
		}

		internalIDToCluster[id.String()] = append(internalIDToCluster[id.String()], ret)
		return ret, nil
	}).AnyTimes()
	testInfo.MockClusterServiceClient.EXPECT().UpdateClusterAutoscaler(gomock.Any(), gomock.Any(), gomock.Any()).DoAndReturn(func(ctx context.Context, internalID ocm.InternalID, builder *arohcpv1alpha1.ClusterAutoscalerBuilder) (*arohcpv1alpha1.ClusterAutoscaler, error) {
		ret, err := builder.HREF(internalID.String()).Build()
		if err != nil {
			return nil, err
		}

		internalIDToAutoscaler[internalID.String()] = append(internalIDToAutoscaler[internalID.String()], ret)
		return ret, nil
	}).AnyTimes()
	testInfo.MockClusterServiceClient.EXPECT().GetCluster(gomock.Any(), gomock.Any()).DoAndReturn(func(ctx context.Context, id ocm.InternalID) (*csarhcpv1alpha1.Cluster, error) {
		return mergeClusterServiceClusterAndAutoscaler(internalIDToCluster[id.String()], internalIDToAutoscaler[id.String()])
	}).AnyTimes()
	testInfo.MockClusterServiceClient.EXPECT().ListClusters(gomock.Any()).DoAndReturn(func(s string) ocm.ClusterListIterator {
		allObjs := []*csarhcpv1alpha1.Cluster{}
		for _, key := range sets.StringKeySet(internalIDToCluster).List() {
			obj, err := mergeClusterServiceClusterAndAutoscaler(internalIDToCluster[key], internalIDToAutoscaler[key])
			if err != nil {
				panic(err)
			}
			allObjs = append(allObjs, obj)
		}
		return ocm.NewSimpleClusterListIterator(allObjs, nil)
	}).AnyTimes()

	testInfo.MockClusterServiceClient.EXPECT().PostExternalAuth(gomock.Any(), gomock.Any(), gomock.Any()).DoAndReturn(func(ctx context.Context, clusterID ocm.InternalID, builder *arohcpv1alpha1.ExternalAuthBuilder) (*arohcpv1alpha1.ExternalAuth, error) {
		justID := rand.String(10)
		builder.ID(justID)
		externalAuthInternalID := clusterID.String() + "/external_auth_config/external_auths/" + justID
		builder = builder.HREF(externalAuthInternalID)
		ret, err := builder.Build()
		if err != nil {
			return nil, err
		}

		internalIDToExternalAuth[externalAuthInternalID] = append(internalIDToExternalAuth[externalAuthInternalID], ret)
		return ret, nil
	}).AnyTimes()
	testInfo.MockClusterServiceClient.EXPECT().UpdateExternalAuth(gomock.Any(), gomock.Any(), gomock.Any()).DoAndReturn(func(ctx context.Context, id ocm.InternalID, builder *arohcpv1alpha1.ExternalAuthBuilder) (*arohcpv1alpha1.ExternalAuth, error) {
		ret, err := builder.Build()
		if err != nil {
			return nil, err
		}

		internalIDToExternalAuth[id.String()] = append(internalIDToExternalAuth[id.String()], ret)
		return ret, nil
	}).AnyTimes()
	testInfo.MockClusterServiceClient.EXPECT().GetExternalAuth(gomock.Any(), gomock.Any()).DoAndReturn(func(ctx context.Context, id ocm.InternalID) (*arohcpv1alpha1.ExternalAuth, error) {
		return mergeClusterServiceInstance[csarhcpv1alpha1.ExternalAuth](internalIDToExternalAuth[id.String()])
	}).AnyTimes()
	testInfo.MockClusterServiceClient.EXPECT().ListExternalAuths(gomock.Any(), gomock.Any()).DoAndReturn(func(id ocm.InternalID, s string) ocm.ExternalAuthListIterator {
		clusterIDString := id.String()
		allObjs := []*csarhcpv1alpha1.ExternalAuth{}
		for _, key := range sets.StringKeySet(internalIDToExternalAuth).List() {
			if !strings.Contains(key, clusterIDString) {
				// only include for the right cluster
				continue
			}
			obj, err := mergeClusterServiceInstance[csarhcpv1alpha1.ExternalAuth](internalIDToExternalAuth[key])
			if err != nil {
				panic(err)
			}
			allObjs = append(allObjs, obj)
		}
		return ocm.NewSimpleExternalAuthListIterator(allObjs, nil)
	}).AnyTimes()

	testInfo.MockClusterServiceClient.EXPECT().PostNodePool(gomock.Any(), gomock.Any(), gomock.Any()).DoAndReturn(func(ctx context.Context, clusterID ocm.InternalID, builder *arohcpv1alpha1.NodePoolBuilder) (*arohcpv1alpha1.NodePool, error) {
		justID := rand.String(10)
		nodePoolInternalID := clusterID.String() + "/node_pools/" + justID

		ret, err := builder.ID(justID).HREF(nodePoolInternalID).Build()
		if err != nil {
			return nil, err
		}

		internalIDToNodePool[nodePoolInternalID] = append(internalIDToNodePool[nodePoolInternalID], ret)
		return ret, nil
	}).AnyTimes()
	testInfo.MockClusterServiceClient.EXPECT().UpdateNodePool(gomock.Any(), gomock.Any(), gomock.Any()).DoAndReturn(func(ctx context.Context, id ocm.InternalID, builder *arohcpv1alpha1.NodePoolBuilder) (*arohcpv1alpha1.NodePool, error) {
		ret, err := builder.Build()
		if err != nil {
			return nil, err
		}

		internalIDToNodePool[id.String()] = append(internalIDToNodePool[id.String()], ret)
		return ret, nil
	}).AnyTimes()
	testInfo.MockClusterServiceClient.EXPECT().GetNodePool(gomock.Any(), gomock.Any()).DoAndReturn(func(ctx context.Context, id ocm.InternalID) (*arohcpv1alpha1.NodePool, error) {
		return mergeClusterServiceInstance[csarhcpv1alpha1.NodePool](internalIDToNodePool[id.String()])
	}).AnyTimes()
	testInfo.MockClusterServiceClient.EXPECT().ListNodePools(gomock.Any(), gomock.Any()).DoAndReturn(func(id ocm.InternalID, s string) ocm.NodePoolListIterator {
		clusterIDString := id.String()
		allObjs := []*csarhcpv1alpha1.NodePool{}
		for _, key := range sets.StringKeySet(internalIDToNodePool).List() {
			if !strings.Contains(key, clusterIDString) {
				// only include for the right cluster
				continue
			}
			obj, err := mergeClusterServiceInstance[csarhcpv1alpha1.NodePool](internalIDToNodePool[key])
			if err != nil {
				panic(err)
			}
			allObjs = append(allObjs, obj)
		}
		return ocm.NewSimpleNodePoolListIterator(allObjs, nil)
	}).AnyTimes()

	return nil
}

func addFakeAzureIdentityData(clusterServiceCluster any) (*csarhcpv1alpha1.Cluster, error) {
	// the API is so hard to work with that we'll make it a map[string]any to manipulate it
	inJSON, err := marshalClusterServiceAny(clusterServiceCluster)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal cluster-service type: %w", err)
	}
	content := map[string]any{}
	if err := json.Unmarshal(inJSON, &content); err != nil {
		return nil, fmt.Errorf("failed to unmarshal cluster-service type: %w", err)
	}

	controlPlaneManagedIdentities, _, _ := unstructured.NestedMap(content, "azure", "operators_authentication", "managed_identities", "control_plane_operators_managed_identities")
	if len(controlPlaneManagedIdentities) > 0 {
		for key, clusterServiceManagedIdentityInfo := range controlPlaneManagedIdentities {
			setFakeAzureIdentityFields(key, clusterServiceManagedIdentityInfo)
		}
		err := unstructured.SetNestedMap(content, controlPlaneManagedIdentities, "azure", "operators_authentication", "managed_identities", "control_plane_operators_managed_identities")
		if err != nil {
			return nil, fmt.Errorf("failed to set nested map: %w", err)
		}
	}

	dataPlaneManagedIdentities, _, _ := unstructured.NestedMap(content, "azure", "operators_authentication", "managed_identities", "data_plane_operators_managed_identities")
	if len(dataPlaneManagedIdentities) > 0 {
		for key, clusterServiceManagedIdentityInfo := range dataPlaneManagedIdentities {
			setFakeAzureIdentityFields(key, clusterServiceManagedIdentityInfo)
		}
		err := unstructured.SetNestedMap(content, dataPlaneManagedIdentities, "azure", "operators_authentication", "managed_identities", "data_plane_operators_managed_identities")
		if err != nil {
			return nil, fmt.Errorf("failed to set nested map: %w", err)
		}
	}

	serviceManagedIdentity, _, _ := unstructured.NestedMap(content, "azure", "operators_authentication", "managed_identities", "service_managed_identity")
	if serviceManagedIdentity != nil {
		setFakeAzureIdentityFields("service-managed-identity", serviceManagedIdentity)
		err = unstructured.SetNestedMap(content, serviceManagedIdentity, "azure", "operators_authentication", "managed_identities", "service_managed_identity")
		if err != nil {
			return nil, fmt.Errorf("failed to set nested map: %w", err)
		}
	}

	outJSON, err := json.MarshalIndent(content, "", "    ")
	if err != nil {
		return nil, fmt.Errorf("failed to marshal cluster-service type: %w", err)
	}
	return csarhcpv1alpha1.UnmarshalCluster(outJSON)
}

func setFakeAzureIdentityFields(key string, uncastManagedIdentity any) any {
	managedIdentity := uncastManagedIdentity.(map[string]any)
	managedIdentity["client_id"] = key + "_fake-client-id"
	managedIdentity["principal_id"] = key + "_fake-principal-id"
	return managedIdentity
}

func mergeClusterServiceReturn(history []any) ([]byte, error) {
	if len(history) == 0 {
		return nil, fmt.Errorf("no history provided")
	}
	// this looks insane, but cluster-service has some of the toughest API and client constructs to manage.
	// we need to merge the history together, but the CS types resist that, so taking it all back to maps is easier.
	dest := map[string]any{}
	for _, curr := range history {
		clusterServiceJSON, err := marshalClusterServiceAny(curr)
		if err != nil {
			return nil, fmt.Errorf("failed to marshal cluster-service type: %w", err)
		}
		currMap := map[string]any{}
		if err := json.Unmarshal(clusterServiceJSON, &currMap); err != nil {
			return nil, fmt.Errorf("failed to unmarshal cluster-service type: %w", err)
		}
		if err := mergo.Merge(&dest, currMap, mergo.WithOverride); err != nil {
			return nil, fmt.Errorf("failed to merge cluster-service type: %w", err)
		}
	}
	return json.Marshal(dest)
}

func mergeClusterServiceInstance[T any](history []any) (*T, error) {
	mergedJSON, err := mergeClusterServiceReturn(history)
	if err != nil {
		return nil, err
	}

	return unmarshalClusterServiceAny[T](mergedJSON)
}

func mergeClusterServiceClusterAndAutoscaler(clusterHistory []any, autoscalerHistory []any) (*arohcpv1alpha1.Cluster, error) {
	cluster, err := mergeClusterServiceInstance[csarhcpv1alpha1.Cluster](clusterHistory)
	if err != nil {
		return nil, err
	}

	clusterBuilder := csarhcpv1alpha1.NewCluster().Copy(cluster)

	if len(autoscalerHistory) > 0 {
		autoscaler, err := mergeClusterServiceInstance[csarhcpv1alpha1.ClusterAutoscaler](autoscalerHistory)
		if err != nil {
			return nil, err
		}

		clusterBuilder.Autoscaler(csarhcpv1alpha1.NewClusterAutoscaler().Copy(autoscaler))
	}

	return clusterBuilder.Build()
}

func unmarshalClusterServiceAny[T any](mergedJSON []byte) (*T, error) {
	var obj T
	switch any(&obj).(type) {
	case *csarhcpv1alpha1.Cluster:
		ret, err := csarhcpv1alpha1.UnmarshalCluster(mergedJSON)
		if err != nil {
			return nil, err
		}
		return any(ret).(*T), err
	case *csarhcpv1alpha1.ClusterAutoscaler:
		ret, err := csarhcpv1alpha1.UnmarshalClusterAutoscaler(mergedJSON)
		if err != nil {
			return nil, err
		}
		return any(ret).(*T), err
	case *csarhcpv1alpha1.NodePool:
		ret, err := csarhcpv1alpha1.UnmarshalNodePool(mergedJSON)
		if err != nil {
			return nil, err
		}
		return any(ret).(*T), err
	case *csarhcpv1alpha1.ExternalAuth:
		ret, err := csarhcpv1alpha1.UnmarshalExternalAuth(mergedJSON)
		if err != nil {
			return nil, err
		}
		return any(ret).(*T), err
	default:
		return nil, fmt.Errorf("unknown type: %T", &obj)
	}
}

// cluster service types fight the standard golang stack and don't conform to standard json interfaces.
func marshalClusterServiceAny(clusterServiceData any) ([]byte, error) {
	switch castObj := clusterServiceData.(type) {
	case *csarhcpv1alpha1.Cluster:
		buf := &bytes.Buffer{}
		if err := csarhcpv1alpha1.MarshalCluster(castObj, buf); err != nil {
			return nil, err
		}
		return buf.Bytes(), nil
	case *csarhcpv1alpha1.ClusterAutoscaler:
		buf := &bytes.Buffer{}
		if err := csarhcpv1alpha1.MarshalClusterAutoscaler(castObj, buf); err != nil {
			return nil, err
		}
		return buf.Bytes(), nil
	case *csarhcpv1alpha1.ExternalAuth:
		buf := &bytes.Buffer{}
		if err := csarhcpv1alpha1.MarshalExternalAuth(castObj, buf); err != nil {
			return nil, err
		}
		return buf.Bytes(), nil
	case *csarhcpv1alpha1.NodePool:
		buf := &bytes.Buffer{}
		if err := csarhcpv1alpha1.MarshalNodePool(castObj, buf); err != nil {
			return nil, err
		}
		return buf.Bytes(), nil
	default:
		return nil, fmt.Errorf("unknown type: %T", castObj)
	}
}

func ReadGenericMutationTest(testDir fs.FS) (*GenericMutationTest, error) {
	createJSON, err := fs.ReadFile(testDir, "create.json")
	if err != nil {
		return nil, fmt.Errorf("failed to read create.json: %w", err)
	}

	updateJSON, err := fs.ReadFile(testDir, "update.json")
	if err != nil && !os.IsNotExist(err) {
		return nil, fmt.Errorf("failed to read update.json: %w", err)
	}

	patchJSON, err := fs.ReadFile(testDir, "patch.json")
	if err != nil && !os.IsNotExist(err) {
		return nil, fmt.Errorf("failed to read patch.json: %w", err)
	}

	expectedErrors := []expectedFieldError{}
	expectedJSON, err := fs.ReadFile(testDir, "expected.json")
	switch {
	case os.IsNotExist(err):
		expectedErrors, err = readExpectedErrors(testDir)
		if err != nil {
			return nil, err
		}

	case err != nil:
		return nil, fmt.Errorf("failed to read expected.json: %w", err)
	}

	var initialCosmosState fs.FS
	if _, err := fs.ReadDir(testDir, "cosmos-state"); err == nil {
		if cosmosState, err := fs.Sub(testDir, "cosmos-state"); err == nil {
			initialCosmosState = cosmosState
		}
	}

	return &GenericMutationTest{
		initialCosmosState: initialCosmosState,
		CreateJSON:         createJSON,
		UpdateJSON:         updateJSON,
		PatchJSON:          patchJSON,
		expectedJSON:       expectedJSON,
		expectedErrors:     expectedErrors,
	}, nil
}

type GenericMutationTest struct {
	initialCosmosState fs.FS
	CreateJSON         []byte
	UpdateJSON         []byte
	PatchJSON          []byte
	expectedJSON       []byte
	expectedErrors     []expectedFieldError
}

func (h *GenericMutationTest) Initialize(ctx context.Context, testInfo *FrontendIntegrationTestInfo) error {
	if h.initialCosmosState != nil {
		err := testInfo.CreateInitialCosmosContent(ctx, h.initialCosmosState)
		if err != nil {
			return err
		}
	}
	return nil
}

func (h *GenericMutationTest) IsUpdateTest() bool {
	return len(h.UpdateJSON) > 0
}

func (h *GenericMutationTest) IsPatchTest() bool {
	return len(h.PatchJSON) > 0
}

func (h *GenericMutationTest) ExpectsResult() bool {
	return len(h.expectedJSON) > 0
}

func (h *GenericMutationTest) VerifyActualError(t *testing.T, actualErr error) {
	if len(h.expectedErrors) == 0 {
		require.NoError(t, actualErr)

		return
	}

	require.Error(t, actualErr)

	azureErr, ok := actualErr.(*azcore.ResponseError)
	if !ok {
		t.Fatal(actualErr)
	}

	actualErrors := &arm.CloudError{}
	body, err := io.ReadAll(azureErr.RawResponse.Body)
	require.NoError(t, err)
	require.NoError(t, json.Unmarshal(body, actualErrors))
	if len(actualErrors.Details) == 0 { // if we have details, then simulate one so the checking code works easily
		actualErrors.Details = []arm.CloudErrorBody{
			{
				Code:    actualErrors.Code,
				Message: actualErrors.Message,
				Target:  actualErrors.Target,
			},
		}
	}

	for _, actualError := range actualErrors.Details {
		found := false
		for _, expectedErr := range h.expectedErrors {
			if err := expectedErr.matches(actualError); err == nil {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("unexpected error: %s: %s: %s", actualError.Code, actualError.Target, actualError.Message)
		}
	}

	for _, expectedErr := range h.expectedErrors {
		found := false
		for _, actualError := range actualErrors.Details {
			if err := expectedErr.matches(actualError); err == nil {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("missing expected error: %#v", expectedErr)
		}
	}

	if t.Failed() {
		t.Logf("Actual errors: %v", actualErrors)
	}
}

func (h *GenericMutationTest) VerifyActualResult(t *testing.T, actualCreated any) {
	actualJSON, err := json.MarshalIndent(actualCreated, "", "    ")
	require.NoError(t, err)
	actualMap := map[string]any{}
	require.NoError(t, json.Unmarshal(actualJSON, &actualMap))
	expectedMap := map[string]any{}
	require.NoError(t, json.Unmarshal(h.expectedJSON, &expectedMap))

	t.Logf("Actual: %s", actualJSON)
	require.Equal(t, expectedMap, actualMap)
}

func readExpectedErrors(testDir fs.FS) ([]expectedFieldError, error) {
	expectedErrorBytes, err := fs.ReadFile(testDir, "expected-errors.txt")
	if err != nil {
		return nil, fmt.Errorf("failed to read expected-errors.txt: %w", err)
	}

	expectedErrors := []expectedFieldError{}
	expectedErrorLines := strings.Split(string(expectedErrorBytes), "\n")
	for _, currLine := range expectedErrorLines {
		if len(strings.TrimSpace(currLine)) == 0 {
			continue
		}
		tokens := strings.SplitN(currLine, ":", 3)
		currExpected := expectedFieldError{
			code:    strings.TrimSpace(tokens[0]),
			field:   strings.TrimSpace(tokens[1]),
			message: strings.TrimSpace(tokens[2]),
		}
		expectedErrors = append(expectedErrors, currExpected)
	}

	if len(expectedErrors) == 0 {
		return nil, fmt.Errorf("no expected errors found")
	}

	return expectedErrors, nil
}

type expectedFieldError struct {
	code    string
	field   string
	message string
}

func (e expectedFieldError) String() string {
	return fmt.Sprintf("%s: %s: %s", e.code, e.field, e.message)
}

func (e expectedFieldError) matches(actualError arm.CloudErrorBody) error {
	if actualError.Code != e.code {
		return fmt.Errorf("expected code %q, got %q", e.code, actualError.Code)
	}
	if actualError.Target != e.field {
		return fmt.Errorf("expected target %q, got %q", e.field, actualError.Target)
	}
	if !strings.Contains(actualError.Message, e.message) {
		return fmt.Errorf("expected message %q, got %q", e.message, actualError.Message)
	}
	return nil
}
