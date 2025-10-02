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

package simulate

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
	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	csarhcpv1alpha1 "github.com/openshift-online/ocm-api-model/clientapi/arohcp/v1alpha1"
	arohcpv1alpha1 "github.com/openshift-online/ocm-sdk-go/arohcp/v1alpha1"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"
	"k8s.io/apimachinery/pkg/util/rand"

	"github.com/Azure/ARO-HCP/internal/api/arm"
	"github.com/Azure/ARO-HCP/internal/ocm"
)

func trivialPassThroughClusterServiceMock(t *testing.T, testInfo *SimulationTestInfo) {
	internalIDToCluster := map[string][]any{}
	require.NoError(t, testInfo.AddMockData(t.Name()+"_clusters", internalIDToCluster))
	testInfo.MockClusterServiceClient.EXPECT().PostCluster(gomock.Any(), gomock.Any()).DoAndReturn(func(ctx context.Context, builder *csarhcpv1alpha1.ClusterBuilder) (*csarhcpv1alpha1.Cluster, error) {
		internalID := "/api/clusters_mgmt/v1/clusters/" + rand.String(10)
		builder = builder.HREF(internalID)
		ret, err := builder.Build()
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
	testInfo.MockClusterServiceClient.EXPECT().GetCluster(gomock.Any(), gomock.Any()).DoAndReturn(func(ctx context.Context, id ocm.InternalID) (*csarhcpv1alpha1.Cluster, error) {
		history := internalIDToCluster[id.String()]
		if len(history) == 0 {
			return nil, fmt.Errorf("not found: %q", id.String())
		}
		mergedJSON, err := mergeClusterServiceReturn(history)
		if err != nil {
			return nil, fmt.Errorf("failed to marshal merged cluster-service type: %w", err)
		}
		return csarhcpv1alpha1.UnmarshalCluster(mergedJSON)
	}).AnyTimes()

	internalIDToExternalAuth := map[string][]any{}
	require.NoError(t, testInfo.AddMockData(t.Name()+"_externalAuths", internalIDToExternalAuth))
	testInfo.MockClusterServiceClient.EXPECT().PostExternalAuth(gomock.Any(), gomock.Any(), gomock.Any()).DoAndReturn(func(ctx context.Context, clusterID ocm.InternalID, builder *arohcpv1alpha1.ExternalAuthBuilder) (*arohcpv1alpha1.ExternalAuth, error) {
		externalAuthInternalID := clusterID.String() + "/external_auth_config/external_auths/" + rand.String(10)
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
		history := internalIDToExternalAuth[id.String()]
		if len(history) == 0 {
			return nil, fmt.Errorf("not found: %q", id.String())
		}
		mergedJSON, err := mergeClusterServiceReturn(history)
		if err != nil {
			return nil, fmt.Errorf("failed to marshal merged cluster-service type: %w", err)
		}
		return csarhcpv1alpha1.UnmarshalExternalAuth(mergedJSON)
	}).AnyTimes()
}

func mergeClusterServiceReturn(history []any) ([]byte, error) {
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

// cluster service types fight the standard golang stack and don't conform to standard json interfaces.
func marshalClusterServiceAny(clusterServiceData any) ([]byte, error) {
	switch castObj := clusterServiceData.(type) {
	case *csarhcpv1alpha1.Cluster:
		buf := &bytes.Buffer{}
		if err := csarhcpv1alpha1.MarshalCluster(castObj, buf); err != nil {
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

func readGenericMutationTest(testDir fs.FS) (*genericMutationTest, error) {
	createJSON, err := fs.ReadFile(testDir, "create.json")
	if err != nil {
		return nil, fmt.Errorf("failed to read create.json: %w", err)
	}

	updateJSON, err := fs.ReadFile(testDir, "update.json")
	if err != nil && !os.IsNotExist(err) {
		return nil, fmt.Errorf("failed to read update.json: %w", err)
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

	return &genericMutationTest{
		initialCosmosState: initialCosmosState,
		createJSON:         createJSON,
		updateJSON:         updateJSON,
		expectedJSON:       expectedJSON,
		expectedErrors:     expectedErrors,
	}, nil
}

type genericMutationTest struct {
	initialCosmosState fs.FS
	createJSON         []byte
	updateJSON         []byte
	expectedJSON       []byte
	expectedErrors     []expectedFieldError
}

func (h *genericMutationTest) initialize(ctx context.Context, testInfo *SimulationTestInfo) error {
	if h.initialCosmosState != nil {
		err := testInfo.CreateInitialCosmosContent(ctx, h.initialCosmosState)
		if err != nil {
			return err
		}
	}
	return nil
}

func (h *genericMutationTest) isUpdateTest() bool {
	return len(h.updateJSON) > 0
}

func (h *genericMutationTest) expectsResult() bool {
	return len(h.expectedJSON) > 0
}

func (h *genericMutationTest) verifyActualError(t *testing.T, actualErr error) {
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

func (h *genericMutationTest) verifyActualResult(t *testing.T, actualCreated any) {
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
