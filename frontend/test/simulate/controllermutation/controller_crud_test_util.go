package controllermutation

import (
	"context"
	"encoding/json"
	"fmt"
	"io/fs"
	"sort"
	"strconv"
	"strings"
	"testing"

	"github.com/Azure/ARO-HCP/frontend/test/simulate/integrationutils"
	"github.com/Azure/ARO-HCP/internal/api"
	"github.com/Azure/ARO-HCP/internal/database"
	azcorearm "github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"
	"github.com/Azure/azure-sdk-for-go/sdk/data/azcosmos"
	"github.com/davecgh/go-spew/spew"
	"github.com/stretchr/testify/require"
	"k8s.io/apimachinery/pkg/api/equality"
)

type controllerMutationTest struct {
	name            string
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
			return nil, fmt.Errorf("failed to create step %q %q: %w", dirEntry.Name(), err)
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

type loadStep struct {
	stepID stepID

	cosmosContainer *azcosmos.ContainerClient
	content         []byte
}

func newLoadStep(stepID stepID, cosmosContainer *azcosmos.ContainerClient, content []byte) *loadStep {
	return &loadStep{
		stepID:          stepID,
		cosmosContainer: cosmosContainer,
		content:         content,
	}
}

var _ controllerMutationStep = &loadStep{}

func (l *loadStep) StepID() stepID {
	return l.stepID
}

func (l *loadStep) RunTest(ctx context.Context, t *testing.T) {
	err := integrationutils.CreateInitialCosmosContent(ctx, l.cosmosContainer, l.content)
	require.NoError(t, err, "failed to load cosmos content")
}

type createStep struct {
	stepID stepID
	key    ControllerCRUDKey

	cosmosContainer *azcosmos.ContainerClient
	controller      *api.Controller
}

type ControllerCRUDKey struct {
	SubscriptionID     string `json:"subscriptionID"`
	ParentResourceType string `json:"parentResourceType"`
	ResourceGroup      string `json:"resourceGroupName"`
	ParentName         string `json:"parentName"`
}

func newCreateStep(stepID stepID, cosmosContainer *azcosmos.ContainerClient, stepDir fs.FS) (*createStep, error) {
	keyBytes, err := fs.ReadFile(stepDir, "00-key.json")
	if err != nil {
		return nil, fmt.Errorf("failed to read key.json: %w", err)
	}
	var key ControllerCRUDKey
	if err := json.Unmarshal(keyBytes, &key); err != nil {
		return nil, fmt.Errorf("failed to unmarshal key.json: %w", err)
	}

	content, err := fs.ReadFile(stepDir, "instance.json")
	if err != nil {
		return nil, fmt.Errorf("failed to read expected.json: %w", err)
	}
	var controller api.Controller
	if err := json.Unmarshal(content, &controller); err != nil {
		return nil, fmt.Errorf("failed to unmarshal instance.json: %w", err)
	}

	return &createStep{
		stepID:          stepID,
		key:             key,
		cosmosContainer: cosmosContainer,
		controller:      &controller,
	}, nil
}

var _ controllerMutationStep = &createStep{}

func (l *createStep) StepID() stepID {
	return l.stepID
}

func (l *createStep) RunTest(ctx context.Context, t *testing.T) {
	parentResourceType, err := azcorearm.ParseResourceType(l.key.ParentResourceType)
	require.NoError(t, err)

	controllerCRUDClient := database.NewControllerCRUD(l.cosmosContainer, parentResourceType, l.key.SubscriptionID, l.key.ResourceGroup, l.key.ParentName)
	_, err = controllerCRUDClient.Upsert(ctx, l.controller, nil)
	require.NoError(t, err, "failed to create controller")
}

type getStep struct {
	stepID stepID
	key    ControllerCRUDKey

	cosmosContainer    *azcosmos.ContainerClient
	expectedController *api.Controller
}

func newGetStep(stepID stepID, cosmosContainer *azcosmos.ContainerClient, stepDir fs.FS) (*getStep, error) {
	keyBytes, err := fs.ReadFile(stepDir, "00-key.json")
	if err != nil {
		return nil, fmt.Errorf("failed to read key.json: %w", err)
	}
	var key ControllerCRUDKey
	if err := json.Unmarshal(keyBytes, &key); err != nil {
		return nil, fmt.Errorf("failed to unmarshal key.json: %w", err)
	}

	content, err := fs.ReadFile(stepDir, "instance.json")
	if err != nil {
		return nil, fmt.Errorf("failed to read expected.json: %w", err)
	}
	var controller api.Controller
	if err := json.Unmarshal(content, &controller); err != nil {
		return nil, fmt.Errorf("failed to unmarshal instance.json: %w", err)
	}

	return &getStep{
		stepID:             stepID,
		key:                key,
		cosmosContainer:    cosmosContainer,
		expectedController: &controller,
	}, nil
}

var _ controllerMutationStep = &getStep{}

func (l *getStep) StepID() stepID {
	return l.stepID
}

func (l *getStep) RunTest(ctx context.Context, t *testing.T) {
	parentResourceType, err := azcorearm.ParseResourceType(l.key.ParentResourceType)
	require.NoError(t, err)

	controllerCRUDClient := database.NewControllerCRUD(l.cosmosContainer, parentResourceType, l.key.SubscriptionID, l.key.ResourceGroup, l.key.ParentName)
	actualController, err := controllerCRUDClient.Get(ctx, l.expectedController.ControllerName)
	require.NoError(t, err)

	if !controllersEqual(l.expectedController, actualController) {
		// cmpdiff doesn't handle private fields gracefully
		require.Equal(t, l.expectedController, actualController)
		t.Fatal("unexpected")
	}
}

func controllersEqual(expected, actual *api.Controller) bool {
	temp := *actual
	// clear the fields that don't compare
	temp.CosmosUID = ""
	return equality.Semantic.DeepEqual(*expected, temp)
}

type listStep struct {
	stepID stepID
	key    ControllerCRUDKey

	cosmosContainer     *azcosmos.ContainerClient
	expectedControllers []*api.Controller
}

func newListStep(stepID stepID, cosmosContainer *azcosmos.ContainerClient, stepDir fs.FS) (*listStep, error) {
	keyBytes, err := fs.ReadFile(stepDir, "00-key.json")
	if err != nil {
		return nil, fmt.Errorf("failed to read key.json: %w", err)
	}
	var key ControllerCRUDKey
	if err := json.Unmarshal(keyBytes, &key); err != nil {
		return nil, fmt.Errorf("failed to unmarshal key.json: %w", err)
	}

	expectedControllers := []*api.Controller{}
	testContent := api.Must(fs.ReadDir(stepDir, "."))
	for _, dirEntry := range testContent {
		if dirEntry.Name() == "00-key.json" {
			continue
		}

		content, err := fs.ReadFile(stepDir, dirEntry.Name())
		if err != nil {
			return nil, fmt.Errorf("failed to read expected.json: %w", err)
		}
		var controller api.Controller
		if err := json.Unmarshal(content, &controller); err != nil {
			return nil, fmt.Errorf("failed to unmarshal instance.json: %w", err)
		}
		expectedControllers = append(expectedControllers, &controller)
	}

	return &listStep{
		stepID:              stepID,
		key:                 key,
		cosmosContainer:     cosmosContainer,
		expectedControllers: expectedControllers,
	}, nil
}

var _ controllerMutationStep = &listStep{}

func (l *listStep) StepID() stepID {
	return l.stepID
}

func (l *listStep) RunTest(ctx context.Context, t *testing.T) {
	parentResourceType, err := azcorearm.ParseResourceType(l.key.ParentResourceType)
	require.NoError(t, err)

	controllerCRUDClient := database.NewControllerCRUD(l.cosmosContainer, parentResourceType, l.key.SubscriptionID, l.key.ResourceGroup, l.key.ParentName)
	actualControllersIterator, err := controllerCRUDClient.List(ctx, nil)
	require.NoError(t, err)

	actualControllers := []*api.Controller{}
	for _, actual := range actualControllersIterator.Items(ctx) {
		actualControllers = append(actualControllers, actual)
	}
	require.NoError(t, actualControllersIterator.GetError())

	require.Equal(t, len(l.expectedControllers), len(actualControllers), "unexpected number of controllers")
	// all the expected must be present
	for _, expected := range l.expectedControllers {
		found := false
		for _, actual := range actualControllers {
			if controllersEqual(expected, actual) {
				found = true
				break
			}
		}
		require.True(t, found, "expected controller not found", spew.Sdump(expected))
	}

	// all the actual must be expected
	for _, actual := range actualControllers {
		found := false
		for _, expected := range l.expectedControllers {
			if controllersEqual(expected, actual) {
				found = true
				break
			}
		}
		require.True(t, found, "actual controller not found", spew.Sdump(actual))
	}
}
