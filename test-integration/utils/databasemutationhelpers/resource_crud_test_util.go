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
	"sort"
	"strconv"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/Azure/azure-sdk-for-go/sdk/data/azcosmos"

	"github.com/Azure/ARO-HCP/internal/api"
	"github.com/Azure/ARO-HCP/internal/utils"
	"github.com/Azure/ARO-HCP/test-integration/utils/integrationutils"
)

type ResourceMutationTest struct {
	testDir fs.FS

	steps []IntegrationTestStep
}

type IntegrationTestStep interface {
	StepID() StepID
	RunTest(ctx context.Context, t *testing.T, cosmosContainer *azcosmos.ContainerClient)
}

func NewResourceMutationTest[InternalAPIType any](ctx context.Context, specializer ResourceCRUDTestSpecializer[InternalAPIType], testName string, testDir fs.FS) (*ResourceMutationTest, error) {
	steps, err := readSteps(ctx, testDir, specializer)
	if err != nil {
		return nil, fmt.Errorf("failed to read steps for test %q: %w", testName, err)
	}
	return &ResourceMutationTest{
		testDir: testDir,
		steps:   steps,
	}, nil
}

func readSteps[InternalAPIType any](ctx context.Context, testDir fs.FS, specializer ResourceCRUDTestSpecializer[InternalAPIType]) ([]IntegrationTestStep, error) {
	steps := []IntegrationTestStep{}

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

		testStep, err := newStep(index, stepType, stepName, testDir, dirEntry.Name(), specializer)
		if err != nil {
			return nil, fmt.Errorf("failed to create new step %q: %w", dirEntry.Name(), err)
		}
		steps = append(steps, testStep)
	}

	sort.Sort(byIndex(steps))
	return steps, nil
}

func (tt *ResourceMutationTest) RunTest(t *testing.T) {
	testInfo, err := integrationutils.NewCosmosFromTestingEnv(t.Context())
	require.NoError(t, err)
	defer testInfo.Cleanup(context.Background())

	for _, step := range tt.steps {
		t.Logf("Running step %s", step.StepID())
		step.RunTest(t.Context(), t, testInfo.CosmosResourcesContainer())
	}
}

func newStep[InternalAPIType any](indexString, stepType, stepName string, testDir fs.FS, path string, specializer ResourceCRUDTestSpecializer[InternalAPIType]) (IntegrationTestStep, error) {
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
	case "load":
		return NewLoadStep(stepID, stepDir)

	case "cosmosCompare":
		return NewCosmosCompareStep(stepID, stepDir)

	case "create":
		return newCreateStep(stepID, specializer, stepDir)

	case "replace":
		return newReplaceStep(stepID, specializer, stepDir)

	case "get":
		return newGetStep(stepID, specializer, stepDir)

	case "getByID":
		return newGetByIDStep(stepID, specializer, stepDir)

	case "untypedGet":
		return newUntypedGetStep(stepID, stepDir)

	case "list":
		return newListStep(stepID, specializer, stepDir)

	case "listActiveOperations":
		return newListActiveOperationsStep(stepID, stepDir)

	case "untypedListRecursive":
		return newUntypedListRecursiveStep(stepID, stepDir)

	case "untypedList":
		return newUntypedListStep(stepID, stepDir)

	case "delete":
		return newDeleteStep(stepID, specializer, stepDir)

	case "untypedDelete":
		return newUntypedDeleteStep(stepID, stepDir)

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
	ParentResourceID string `json:"parentResourceId"`
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
