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

package controllermutation

import (
	"context"
	"encoding/json"
	"fmt"
	"io/fs"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"k8s.io/apimachinery/pkg/api/equality"

	azcorearm "github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"
	"github.com/Azure/azure-sdk-for-go/sdk/data/azcosmos"

	"github.com/Azure/ARO-HCP/internal/api"
	"github.com/Azure/ARO-HCP/internal/database"
)

type controllerMutationTest struct {
	testDir         fs.FS
	cosmosContainer *azcosmos.ContainerClient

	steps []controllerMutationStep
}

type controllerMutationStep interface {
	StepID() stepID
	RunTest(ctx context.Context, t *testing.T)
}

func NewControllerMutationTest(ctx context.Context, cosmosContainer *azcosmos.ContainerClient, testName string, testDir fs.FS) (*controllerMutationTest, error) {
	steps, err := readSteps(ctx, testDir, cosmosContainer)
	if err != nil {
		return nil, fmt.Errorf("failed to read steps for test %q: %w", testName, err)
	}
	return &controllerMutationTest{
		testDir:         testDir,
		cosmosContainer: cosmosContainer,
		steps:           steps,
	}, nil
}

func readSteps(ctx context.Context, testDir fs.FS, cosmosContainer *azcosmos.ContainerClient) ([]controllerMutationStep, error) {
	steps := []controllerMutationStep{}

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

		testStep, err := newStep(index, stepType, stepName, testDir, dirEntry.Name(), cosmosContainer)
		if err != nil {
			return nil, fmt.Errorf("failed to upsert step %q: %w", dirEntry.Name(), err)
		}
		steps = append(steps, testStep)
	}

	sort.Sort(byIndex(steps))
	return steps, nil
}

func (tt *controllerMutationTest) RunTest(t *testing.T) {
	for _, step := range tt.steps {
		t.Logf("Running step %s", step.StepID())
		step.RunTest(t.Context(), t)
	}
}

func newStep(indexString, stepType, stepName string, testDir fs.FS, path string, cosmosContainer *azcosmos.ContainerClient) (controllerMutationStep, error) {
	itoInt, err := strconv.Atoi(indexString)
	if err != nil {
		return nil, fmt.Errorf("failed to convert %s to int: %w", indexString, err)
	}
	stepID := stepID{index: itoInt, stepType: stepType, stepName: stepName}

	switch stepType {
	case "load":
		content, err := fs.ReadFile(testDir, path)
		if err != nil {
			return nil, fmt.Errorf("failed to read %s: %w", path, err)
		}
		return newLoadStep(stepID, cosmosContainer, content), nil

	case "create":
		stepDir, err := fs.Sub(testDir, path)
		if err != nil {
			return nil, fmt.Errorf("failed to read %s: %w", path, err)
		}
		return newCreateStep(stepID, cosmosContainer, stepDir)

	case "replace":
		stepDir, err := fs.Sub(testDir, path)
		if err != nil {
			return nil, fmt.Errorf("failed to read %s: %w", path, err)
		}
		return newReplaceStep(stepID, cosmosContainer, stepDir)

	case "get":
		stepDir, err := fs.Sub(testDir, path)
		if err != nil {
			return nil, fmt.Errorf("failed to read %s: %w", path, err)
		}
		return newGetStep(stepID, cosmosContainer, stepDir)

	case "list":
		stepDir, err := fs.Sub(testDir, path)
		if err != nil {
			return nil, fmt.Errorf("failed to read %s: %w", path, err)
		}
		return newListStep(stepID, cosmosContainer, stepDir)

	default:
		return nil, fmt.Errorf("unknown step type: %s", stepType)
	}
}

type stepID struct {
	index    int
	stepType string
	stepName string
}

func (s stepID) String() string {
	return fmt.Sprintf("%d-%s-%s", s.index, s.stepType, s.stepName)
}

type byIndex []controllerMutationStep

func (s byIndex) Len() int           { return len(s) }
func (s byIndex) Less(i, j int) bool { return s[i].StepID().index < s[j].StepID().index }
func (s byIndex) Swap(i, j int)      { s[i], s[j] = s[j], s[i] }

func controllersEqual(expected, actual *api.Controller) bool {
	temp := *actual
	// clear the fields that don't compare
	temp.CosmosUID = ""
	return equality.Semantic.DeepEqual(*expected, temp)
}

func stringifyController(controller *api.Controller) string {
	return string(api.Must(json.MarshalIndent(controller, "", "\t")))
}

func stringifyControllers(controllers []*api.Controller) string {
	return string(api.Must(json.MarshalIndent(controllers, "", "\t")))
}

type ControllerCRUDKey struct {
	ParentResourceID string `json:"parentResourceId"`
}

func controllerCRUDFromKey(t *testing.T, cosmosContainer *azcosmos.ContainerClient, key ControllerCRUDKey) database.ResourceCRUD[api.Controller] {
	parentResourceID, err := azcorearm.ParseResourceID(key.ParentResourceID)
	require.NoError(t, err)
	controllerResourceType, err := azcorearm.ParseResourceType(filepath.Join(parentResourceID.ResourceType.String(), api.ControllerResourceTypeName))
	require.NoError(t, err)

	return database.NewControllerCRUD(cosmosContainer, parentResourceID, controllerResourceType)
}
