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
	"github.com/Azure/azure-sdk-for-go/sdk/data/azcosmos"
	csarhcpv1alpha1 "github.com/openshift-online/ocm-api-model/clientapi/arohcp/v1alpha1"
	arohcpv1alpha1 "github.com/openshift-online/ocm-sdk-go/arohcp/v1alpha1"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"
	"k8s.io/apimachinery/pkg/util/rand"
	"k8s.io/utils/ptr"

	hcpsdk20240610preview "github.com/Azure/ARO-HCP/test/sdk/v20240610preview/resourcemanager/redhatopenshifthcp/armredhatopenshifthcp"

	"github.com/Azure/ARO-HCP/internal/api/arm"
	"github.com/Azure/ARO-HCP/internal/ocm"

	"github.com/Azure/ARO-HCP/internal/api"
)

func TestFrontendClusterMutation(t *testing.T) {
	SkipIfNotSimulationTesting(t)

	ctx := context.Background()
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	frontend, testInfo, err := NewFrontendFromTestingEnv(ctx, t)
	require.NoError(t, err)
	defer testInfo.Cleanup(context.Background())

	go frontend.Run(ctx, ctx.Done())

	subscriptionID := "0465bc32-c654-41b8-8d87-9815d7abe8f6" // TODO could read from JSON
	resourceGroupName := "some-resource-group"
	err = testInfo.CreateInitialCosmosContent(ctx, api.Must(fs.Sub(artifacts, "artifacts/ClusterMutation/initial-cosmos-state")))
	require.NoError(t, err)

	// create anything and round trip anything for cluster-service
	internalIDToCluster := map[string][]*csarhcpv1alpha1.Cluster{}
	require.NoError(t, testInfo.AddMockData(t.Name(), internalIDToCluster))
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
		ret := internalIDToCluster[id.String()]
		if ret != nil {
			// this looks insane, but cluster-service has some of the toughest API and client constructs to manage.
			// we need to merge the history together, but the CS types resist that, so taking it all back to maps is easier.
			dest := map[string]any{}
			for _, curr := range ret {
				buf := &bytes.Buffer{}
				if err := csarhcpv1alpha1.MarshalCluster(curr, buf); err != nil {
					return nil, fmt.Errorf("failed to marshal cluster: %w", err)
				}
				currMap := map[string]any{}
				if err := json.Unmarshal(buf.Bytes(), &currMap); err != nil {
					return nil, fmt.Errorf("failed to unmarshal cluster: %w", err)
				}
				if err := mergo.Merge(&dest, currMap, mergo.WithOverride); err != nil {
					return nil, fmt.Errorf("failed to merge cluster: %w", err)
				}
			}
			mergedJSON, err := json.Marshal(dest)
			if err != nil {
				return nil, fmt.Errorf("failed to marshal merged cluster: %w", err)
			}
			final, err := csarhcpv1alpha1.UnmarshalCluster(mergedJSON)
			if err != nil {
				return nil, fmt.Errorf("failed to unmarshal merged cluster: %w", err)
			}

			return final, nil
		}
		return nil, fmt.Errorf("not found: %q", id.String())
	}).AnyTimes()

	dirContent := api.Must(artifacts.ReadDir("artifacts/ClusterMutation"))
	for _, dirEntry := range dirContent {
		if dirEntry.Name() == "initial-cosmos-state" {
			continue
		}
		createTestDir, err := fs.Sub(artifacts, "artifacts/ClusterMutation/"+dirEntry.Name())
		require.NoError(t, err)
		currTest := newClusterMutationTest(ctx, createTestDir, testInfo, subscriptionID, resourceGroupName)
		t.Run(dirEntry.Name(), currTest.runTest)
	}
}

type clusterMutationTest struct {
	ctx               context.Context
	testDir           fs.FS
	testInfo          *SimulationTestInfo
	subscriptionID    string
	resourceGroupName string
}

func newClusterMutationTest(ctx context.Context, testDir fs.FS, testInfo *SimulationTestInfo, subscriptionID, resourceGroupName string) *clusterMutationTest {
	return &clusterMutationTest{
		ctx:               ctx,
		testDir:           testDir,
		testInfo:          testInfo,
		subscriptionID:    subscriptionID,
		resourceGroupName: resourceGroupName,
	}
}

type expectedFieldError struct {
	code    string
	field   string
	message string
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

func (tt *clusterMutationTest) runTest(t *testing.T) {
	ctx := tt.ctx

	isUpdateTest := false
	if _, err := fs.ReadFile(tt.testDir, "update.json"); err == nil {
		isUpdateTest = true
	}

	var err error
	expectedErrors := []expectedFieldError{}

	expectedJSON, err := fs.ReadFile(tt.testDir, "expected.json")
	switch {
	case os.IsNotExist(err):
		expectedErrorBytes, err := fs.ReadFile(tt.testDir, "expected-errors.txt")
		require.NoError(t, err)
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

	case err != nil:
		t.Fatal(err)

	default:

	}

	toCreate := &hcpsdk20240610preview.HcpOpenShiftCluster{}
	require.NoError(t, json.Unmarshal(api.Must(fs.ReadFile(tt.testDir, "create.json")), toCreate))
	clusterClient := tt.testInfo.Get20240610ClientFactory(tt.subscriptionID).NewHcpOpenShiftClustersClient()
	_, err = clusterClient.BeginCreateOrUpdate(ctx, tt.resourceGroupName, *toCreate.Name, *toCreate, nil)

	if isUpdateTest {
		require.NoError(t, err)

		operationsIterator := tt.testInfo.DBClient.ListActiveOperationDocs(azcosmos.NewPartitionKeyString(tt.subscriptionID), nil)
		for _, operation := range operationsIterator.Items(ctx) {
			if operation.ExternalID.Name != ptr.Deref(toCreate.Name, "") {
				continue
			}
			err := tt.testInfo.UpdateClusterOperationStatus(ctx, operation, arm.ProvisioningStateSucceeded, nil)
			require.NoError(t, err)
		}
		require.NoError(t, operationsIterator.GetError())

		toUpdate := &hcpsdk20240610preview.HcpOpenShiftCluster{}
		require.NoError(t, json.Unmarshal(api.Must(fs.ReadFile(tt.testDir, "update.json")), toUpdate))
		_, err = clusterClient.BeginCreateOrUpdate(ctx, tt.resourceGroupName, *toCreate.Name, *toCreate, nil)

	}

	if len(expectedErrors) > 0 {
		require.Error(t, err)

		azureErr, ok := err.(*azcore.ResponseError)
		if !ok {
			t.Fatal(err)
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
			for _, expectedErr := range expectedErrors {
				if err := expectedErr.matches(actualError); err == nil {
					found = true
					break
				}
			}
			if !found {
				t.Errorf("unexpected error: %v", actualError)
			}
		}

		for _, expectedErr := range expectedErrors {
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

		return
	}
	require.NoError(t, err)

	// polling the result will never complete because we aren't actually working on the operation.  We want to do a GET to see
	// if the data we read back matches what we expect.
	actualCreated, err := clusterClient.Get(ctx, tt.resourceGroupName, *toCreate.Name, nil)
	require.NoError(t, err)

	actualJSON, err := json.MarshalIndent(actualCreated, "", "    ")
	require.NoError(t, err)
	actualMap := map[string]any{}
	require.NoError(t, json.Unmarshal(actualJSON, &actualMap))
	expectedMap := map[string]any{}
	require.NoError(t, json.Unmarshal(expectedJSON, &expectedMap))

	require.Equal(t, expectedMap, actualMap)
}
