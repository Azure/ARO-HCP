package controllermutation

import (
	"context"
	"fmt"
	"io/fs"
	"sort"
	"strconv"
	"strings"
	"testing"

	"github.com/Azure/ARO-HCP/frontend/test/simulate/integrationutils"
	"github.com/Azure/ARO-HCP/internal/api"
	"github.com/Azure/azure-sdk-for-go/sdk/data/azcosmos"
	"github.com/stretchr/testify/require"
)

type controllerMutationTest struct {
	name    string
	testDir fs.FS

	steps []controllerMutationStep
}

type controllerMutationStep interface {
	StepID() stepID
	RunTest(ctx context.Context, t *testing.T)
}

func newControllerMutationTest(ctx context.Context, testName string, testDir fs.FS) (*controllerMutationTest, error) {
	steps, err := readSteps(ctx, testDir)
	if err != nil {
		return nil, fmt.Errorf("failed to read steps for test %q: %w", testName, err)
	}
	return &controllerMutationTest{
		testDir: testDir,
		steps:   steps,
	}, nil
}

func readSteps(ctx context.Context, testDir fs.FS) ([]controllerMutationStep, error) {
	steps := []controllerMutationStep{}

	testContent := api.Must(fs.ReadDir(testDir, "."))
	for _, dirEntry := range testContent {
		filenameParts := strings.SplitN(dirEntry.Name(), "-", 3)
		index := filenameParts[0]
		stepType := filenameParts[1]
		stepName, _ := strings.CutSuffix(filenameParts[2], ".json")

		testStep, err := createStep(index, stepType, stepName, testDir, dirEntry.Name())
		if err != nil {
			return nil, fmt.Errorf("failed to create step %q %q: %w", dirEntry.Name(), err)
		}
		steps = append(steps, testStep)
	}

	sort.Sort(byIndex(steps))
	return steps, nil
}

func (tt *controllerMutationTest) runTest(t *testing.T) {
	for _, step := range tt.steps {
		t.Logf("Running step %s", step.StepID())
		step.RunTest(t.Context(), t)
	}
}

func createStep(indexString, stepType, stepName string, testDir fs.FS, path string) (controllerMutationStep, error) {
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
		return newLoadStep(stepID, nil, content), nil

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
