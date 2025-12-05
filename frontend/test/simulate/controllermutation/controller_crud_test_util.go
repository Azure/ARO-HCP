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
	"github.com/stretchr/testify/require"
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
	key    CreateKey

	cosmosContainer *azcosmos.ContainerClient
	controller      *api.Controller
}

type CreateKey struct {
	SubscriptionID     string `json:"subscriptionId"`
	ParentResourceType string `json:"parentResourceType"`
	ResourceGroup      string `json:"resourceGroupName"`
	ParentName         string `json:"parentName"`
}

func newCreateStep(stepID stepID, cosmosContainer *azcosmos.ContainerClient, stepDir fs.FS) (*createStep, error) {
	keyBytes, err := fs.ReadFile(stepDir, "00-key.json")
	if err != nil {
		return nil, fmt.Errorf("failed to read key.json: %w", err)
	}
	var key CreateKey
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
