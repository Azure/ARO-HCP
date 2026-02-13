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

package databasemutationhelpers

import (
	"context"
	"encoding/json"
	"fmt"
	"io/fs"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"k8s.io/apimachinery/pkg/util/wait"

	azcorearm "github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"
	"github.com/Azure/azure-sdk-for-go/sdk/data/azcosmos"

	"github.com/Azure/ARO-HCP/internal/api"
	"github.com/Azure/ARO-HCP/internal/database"
	"github.com/Azure/ARO-HCP/internal/utils"
	"github.com/Azure/ARO-HCP/test-integration/utils/integrationutils"
)

type ResourceMutationTest struct {
	testDir  fs.FS
	withMock bool

	steps []IntegrationTestStep
}

type IntegrationTestStep interface {
	StepID() StepID
	RunTest(ctx context.Context, t *testing.T, stepInput StepInput)
}

func NewResourceMutationTest[InternalAPIType any](ctx context.Context, testName string, testDir fs.FS, withMock bool) (*ResourceMutationTest, error) {
	steps, err := readSteps[InternalAPIType](ctx, testDir)
	if err != nil {
		return nil, fmt.Errorf("failed to read steps for test %q: %w", testName, err)
	}
	return &ResourceMutationTest{
		testDir:  testDir,
		withMock: withMock,
		steps:    steps,
	}, nil
}

func readSteps[InternalAPIType any](ctx context.Context, testDir fs.FS) ([]IntegrationTestStep, error) {
	steps := []IntegrationTestStep{}

	numLoadClusterServiceSteps := 0
	testContent := api.Must(fs.ReadDir(testDir, "."))
	for _, dirEntry := range testContent {
		filenameParts := strings.SplitN(dirEntry.Name(), "-", 3)
		switch len(filenameParts) {
		case 1:
			return nil, fmt.Errorf("step name %q is missing step type: <number>-<type>-<name>", dirEntry.Name())
		case 2:
			return nil, fmt.Errorf("step name %q is missing step name: <number>-<type>-<name>", dirEntry.Name())
		case 3:
			// all good
		}
		index := filenameParts[0]
		stepType := filenameParts[1]
		stepName, _ := strings.CutSuffix(filenameParts[2], ".json")
		if stepType == "loadClusterService" { // eventually this goes away, so we'll be able to remove the ugly. for now prevent mistakes.
			numLoadClusterServiceSteps++
		}

		testStep, err := NewStep[InternalAPIType](index, stepType, stepName, testDir, dirEntry.Name())
		if err != nil {
			return nil, fmt.Errorf("failed to create new step %q: %w", dirEntry.Name(), err)
		}
		steps = append(steps, testStep)
	}
	if numLoadClusterServiceSteps > 1 {
		return nil, fmt.Errorf("more than one step found for loadClusterService.  Refactor to do it once or make it possible to load more than once")
	}

	sort.Sort(byIndex(steps))
	return steps, nil
}

func (tt *ResourceMutationTest) RunTest(t *testing.T) {
	defer integrationutils.VerifyNoNewGoLeaks(t)

	ctx := t.Context()
	ctx, cancel := context.WithCancel(ctx)
	frontendStarted := atomic.Bool{}
	frontendErrCh := make(chan error, 1)
	defer func() {
		if frontendStarted.Load() {
			require.NoError(t, <-frontendErrCh)
		}
	}()
	adminAPIStarted := atomic.Bool{}
	adminAPIErrCh := make(chan error, 1)
	defer func() {
		if adminAPIStarted.Load() {
			require.NoError(t, <-adminAPIErrCh)
		}
	}()
	defer cancel()
	ctx = utils.ContextWithLogger(ctx, integrationutils.DefaultLogger(t))
	logger := utils.LoggerFromContext(ctx)

	testInfo, err := integrationutils.NewIntegrationTestInfoFromEnv(ctx, t, tt.withMock)
	require.NoError(t, err)
	cleanupCtx := context.Background()
	cleanupCtx = utils.ContextWithLogger(cleanupCtx, integrationutils.DefaultLogger(t))
	defer testInfo.Cleanup(cleanupCtx)
	go func() {
		frontendStarted.Store(true)
		frontendErrCh <- testInfo.Frontend.Run(ctx)
	}()
	go func() {
		adminAPIStarted.Store(true)
		adminAPIErrCh <- testInfo.AdminAPI.Run(ctx)
	}()

	// wait for migration to complete to eliminate races with our test's second call migrateCosmos and to ensure the server is ready for testing
	serverUrls := []string{testInfo.FrontendURL, testInfo.AdminURL}
	err = wait.PollUntilContextCancel(ctx, 1*time.Second, true, func(ctx context.Context) (bool, error) {
		for _, url := range serverUrls {
			resp, err := http.Get(url)
			if err != nil {
				t.Log(err)
				return false, nil
			}
			if err := resp.Body.Close(); err != nil {
				logger.Error(err, "failed to close response body")
			}
		}
		return true, nil
	})
	require.NoError(t, err)

	stepInput := NewCosmosStepInput(testInfo)
	stepInput.FrontendURL = testInfo.FrontendURL
	stepInput.AdminURL = testInfo.AdminURL
	stepInput.ClusterServiceMockInfo = testInfo.ClusterServiceMock

	for _, step := range tt.steps {
		t.Logf("Running step %s", step.StepID())
		ctx := t.Context()
		ctx = utils.ContextWithLogger(ctx, integrationutils.DefaultLogger(t))

		step.RunTest(ctx, t, *stepInput)
	}
}

func NewStep[InternalAPIType any](indexString, stepType, stepName string, testDir fs.FS, path string) (IntegrationTestStep, error) {
	itoInt, err := strconv.Atoi(indexString)
	if err != nil {
		return nil, fmt.Errorf("failed to convert %s to int: %w", indexString, err)
	}
	stepID := StepID{index: itoInt, stepType: stepType, stepName: stepName}
	stepDir, err := fs.Sub(testDir, path)
	if err != nil {
		return nil, fmt.Errorf("failed to read %s: %w", path, err)
	}

	switch stepType {
	case "load", "loadCosmos":
		return NewLoadCosmosStep(stepID, stepDir)

	case "loadClusterService":
		return NewLoadClusterServiceStep(stepID, stepDir)

	case "cosmosCompare":
		return NewCosmosCompareStep(stepID, stepDir)

	case "create":
		return newCreateStep[InternalAPIType](stepID, stepDir)

	case "replace":
		return newReplaceStep[InternalAPIType](stepID, stepDir)

	case "replaceWithETag":
		return newReplaceWithETagStep[InternalAPIType](stepID, stepDir)

	case "get":
		return newGetStep[InternalAPIType](stepID, stepDir)

	case "getByID":
		return newGetByIDStep[InternalAPIType](stepID, stepDir)

	case "untypedGet":
		return newUntypedGetStep(stepID, stepDir)

	case "list":
		return newListStep[InternalAPIType](stepID, stepDir)

	case "listActiveOperations":
		return newListActiveOperationsStep(stepID, stepDir)

	case "untypedListRecursive":
		return newUntypedListRecursiveStep(stepID, stepDir)

	case "untypedList":
		return newUntypedListStep(stepID, stepDir)

	case "delete":
		return newDeleteStep[InternalAPIType](stepID, stepDir)

	case "untypedDelete":
		return newUntypedDeleteStep(stepID, stepDir)

	case "httpGet":
		return newHTTPGetStep(stepID, stepDir)

	case "httpList":
		return newHTTPListStep(stepID, stepDir)

	case "httpCreate", "httpReplace":
		return newHTTPCreateStep(stepID, stepDir)

	case "httpPatch":
		return newHTTPPatchStep(stepID, stepDir)

	case "httpDelete":
		return newHTTPDeleteStep(stepID, stepDir)

	case "completeOperation":
		return newCompleteOperationStep(stepID, stepDir)

	case "clusterServiceCompare":
		return newClusterServiceCompareStep(stepID, stepDir)

	case "migrateCosmos":
		return newMigrateCosmosStep(stepID, stepDir)

	default:
		return nil, fmt.Errorf("unknown step type: %s", stepType)
	}
}

type StepID struct {
	index    int
	stepType string
	stepName string
}

func NewStepID(index int, stepType, stepName string) StepID {
	return StepID{
		index:    index,
		stepType: stepType,
		stepName: stepName,
	}
}

func (s StepID) String() string {
	return fmt.Sprintf("%d-%s-%s", s.index, s.stepType, s.stepName)
}

type byIndex []IntegrationTestStep

func (s byIndex) Len() int           { return len(s) }
func (s byIndex) Less(i, j int) bool { return s[i].StepID().index < s[j].StepID().index }
func (s byIndex) Swap(i, j int)      { s[i], s[j] = s[j], s[i] }

func stringifyResource(controller any) string {
	return string(api.Must(json.MarshalIndent(controller, "", "\t")))
}

type CosmosCRUDKey struct {
	ParentResourceID *azcorearm.ResourceID `json:"parentResourceId"`
	ResourceType     ResourceType          `json:"resourceType"`
}

type ResourceType struct {
	azcorearm.ResourceType
}

// MarshalText returns a textual representation of the ResourceID
func (o *ResourceType) MarshalText() ([]byte, error) {
	return []byte(o.String()), nil
}

// UnmarshalText decodes the textual representation of a ResourceID
func (o *ResourceType) UnmarshalText(text []byte) error {
	newType, err := azcorearm.ParseResourceType(string(text))
	if err != nil {
		return err
	}
	o.ResourceType = newType
	return nil
}

func readResourcesInDir[InternalAPIType any](dir fs.FS) ([]*InternalAPIType, error) {
	resources := []*InternalAPIType{}
	testContent, err := fs.ReadDir(dir, ".")
	if err != nil {
		return nil, utils.TrackError(err)
	}
	for _, dirEntry := range testContent {
		if dirEntry.Name() == "00-key.json" { // standard filenames to skip
			continue
		}
		if !strings.HasSuffix(dirEntry.Name(), ".json") {
			continue
		}

		content, err := fs.ReadFile(dir, dirEntry.Name())
		if err != nil {
			return nil, fmt.Errorf("failed to read expected.json: %w", err)
		}
		var resource InternalAPIType
		if err := json.Unmarshal(content, &resource); err != nil {
			return nil, fmt.Errorf("failed to unmarshal instance.json: %w", err)
		}
		resources = append(resources, &resource)
	}

	return resources, nil
}

func readRawBytesInDir(dir fs.FS) ([][]byte, error) {
	contents := [][]byte{}
	testContent := api.Must(fs.ReadDir(dir, "."))
	for _, dirEntry := range testContent {
		if dirEntry.Name() == "00-key.json" { // standard filenames to skip
			continue
		}
		if dirEntry.Name() == "expected-error.txt" { // standard filenames to skip
			continue
		}
		if !strings.HasSuffix(dirEntry.Name(), ".json") { // we can only understand JSON
			continue
		}

		currContent, err := fs.ReadFile(dir, dirEntry.Name())
		if err != nil {
			return nil, fmt.Errorf("failed to read expected.json: %w", err)
		}
		contents = append(contents, currContent)
	}

	return contents, nil
}

type StepInput struct {
	CosmosContainer        *azcosmos.ContainerClient
	ContentLoader          integrationutils.ContentLoader
	DocumentLister         integrationutils.DocumentLister
	DBClient               database.DBClient
	FrontendURL            string
	AdminURL               string
	ClusterServiceMockInfo *integrationutils.ClusterServiceMock
}

func (s StepInput) HTTPTestAccessor(key ResourceKey) HTTPTestAccessor {
	if strings.HasPrefix(key.ResourceID, "/admin/") {
		return newHTTPTestAccessor(s.AdminURL, map[string]string{
			"X-Ms-Client-Principal-Name": "test-user@example.com",
			"Content-Type":               "application/json",
		})
	}
	subscriptionID := api.Must(azcorearm.ParseResourceID(key.ResourceID)).SubscriptionID
	return NewFrontendHTTPTestAccessor(s.FrontendURL, integrationutils.Get20240610ClientFactory(s.FrontendURL, subscriptionID))
}

func NewCosmosStepInput(storageInfo integrationutils.StorageIntegrationTestInfo) *StepInput {
	return &StepInput{
		ContentLoader:  storageInfo,
		DocumentLister: storageInfo,
		DBClient:       storageInfo.CosmosClient(),
	}
}
