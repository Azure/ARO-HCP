package pipeline

import (
	"context"
	"testing"

	"gotest.tools/v3/assert"
)

func TestStepRun(t *testing.T) {
	foundOutput := ""
	s := NewShellStep("step", "echo hello").WithOutputFunc(
		func(output string) {
			foundOutput = output
		},
	)
	_, err := RunStep(s, context.Background(), "", &executionTargetImpl{}, &PipelineRunOptions{}, nil)
	assert.NilError(t, err)
	assert.Equal(t, foundOutput, "hello\n")
}

func TestResourceGroupRun(t *testing.T) {
	foundOutput := ""
	rg := &ResourceGroup{
		Steps: []Step{
			NewShellStep("step", "echo hello").WithOutputFunc(
				func(output string) {
					foundOutput = output
				},
			),
		},
	}
	err := RunResourceGroup(rg, context.Background(), &PipelineRunOptions{}, &executionTargetImpl{}, make(map[string]output))
	assert.NilError(t, err)
	assert.Equal(t, foundOutput, "hello\n")
}

func TestResourceGroupError(t *testing.T) {
	tmpVals := make([]string, 0)
	outputFunc := func(output string) {
		tmpVals = append(tmpVals, output)
	}
	rg := &ResourceGroup{
		Steps: []Step{
			NewShellStep("step1", "echo hello").WithOutputFunc(outputFunc),
			NewShellStep("step2", "faaaaafffaa").WithOutputFunc(outputFunc),
			NewShellStep("step3", "echo hallo").WithOutputFunc(outputFunc),
		},
	}
	err := RunResourceGroup(rg, context.Background(), &PipelineRunOptions{}, &executionTargetImpl{}, make(map[string]output))
	assert.ErrorContains(t, err, "faaaaafffaa: command not found\n exit status 127")
	// Test processing ends after first error
	assert.Equal(t, len(tmpVals), 1)
}

type testExecutionTarget struct{}

func (t *testExecutionTarget) KubeConfig(_ context.Context) (string, error) {
	return "", nil
}
func (t *testExecutionTarget) GetSubscriptionID() string { return "test" }
func (t *testExecutionTarget) GetAkSClusterName() string { return "test" }
func (t *testExecutionTarget) GetResourceGroup() string  { return "test" }
func (t *testExecutionTarget) GetRegion() string         { return "test" }

func TestResourceGroupRunRequireKubeconfig(t *testing.T) {
	rg := &ResourceGroup{Steps: []Step{}}
	err := RunResourceGroup(rg, context.Background(), &PipelineRunOptions{}, &testExecutionTarget{}, make(map[string]output))
	assert.NilError(t, err)
}

func TestPipelineRun(t *testing.T) {
	foundOutput := ""
	pipeline := &Pipeline{
		ResourceGroups: []*ResourceGroup{
			{
				Name:         "test",
				Subscription: "test",
				Steps: []Step{
					NewShellStep("step", "echo hello").WithOutputFunc(
						func(output string) {
							foundOutput = output
						},
					),
				},
			},
		},
	}

	err := RunPipeline(pipeline, context.Background(), &PipelineRunOptions{
		SubsciptionLookupFunc: func(_ context.Context, _ string) (string, error) {
			return "test", nil
		},
	})

	assert.NilError(t, err)
	assert.Equal(t, foundOutput, "hello\n")
}

func TestArmGetValue(t *testing.T) {
	output := armOutput{
		"zoneName": map[string]any{
			"type":  "String",
			"value": "test",
		},
	}

	value, err := output.GetValue("zoneName")
	assert.Equal(t, value.Value, "test")
	assert.Equal(t, value.Type, "String")
	assert.NilError(t, err)
}

func TestAddInputVars(t *testing.T) {
	mapOutput := map[string]output{}
	mapOutput["step1"] = armOutput{
		"output1": map[string]any{
			"type":  "String",
			"value": "value1",
		},
	}
	s := &ShellStep{
		Variables: []Variable{{
			Name: "input1",
			Input: &Input{
				Name: "output1",
				Step: "step1",
			},
		},
		}}

	envVars, err := getInputValues(s.Variables, mapOutput)
	assert.NilError(t, err)
	assert.DeepEqual(t, envVars, map[string]any{"input1": "value1"})

	_, err = getInputValues([]Variable{
		{
			Input: &Input{Step: "foobar"},
		},
	}, mapOutput)
	assert.Error(t, err, "step foobar not found in provided outputs")
}
