package pipeline

import (
	"context"
	"testing"

	"gotest.tools/v3/assert"
)

func TestStepRun(t *testing.T) {
	fooundOutput := ""
	s := &Step{
		Name:    "test",
		Action:  "Shell",
		Command: "echo hello",
		outputFunc: func(output string) {
			fooundOutput = output
		},
	}
	_, err := s.run(context.Background(), "", &executionTargetImpl{}, &PipelineRunOptions{}, nil)
	assert.NilError(t, err)
	assert.Equal(t, fooundOutput, "hello\n")
}

func TestStepRunSkip(t *testing.T) {
	s := &Step{
		Name: "step",
	}
	// this should skip
	_, err := s.run(context.Background(), "", &executionTargetImpl{}, &PipelineRunOptions{Step: "skip"}, nil)
	assert.NilError(t, err)

	// this should fail
	_, err = s.run(context.Background(), "", &executionTargetImpl{}, &PipelineRunOptions{Step: "step"}, nil)
	assert.Error(t, err, "unsupported action type \"\"")
}

func TestResourceGroupRun(t *testing.T) {
	foundOutput := ""
	rg := &ResourceGroup{
		Steps: []*Step{
			{
				Name:    "step",
				Action:  "Shell",
				Command: "echo hello",
				outputFunc: func(output string) {
					foundOutput = output
				},
			},
		},
	}
	err := rg.run(context.Background(), &PipelineRunOptions{}, &executionTargetImpl{}, make(map[string]output))
	assert.NilError(t, err)
	assert.Equal(t, foundOutput, "hello\n")
}

func TestResourceGroupError(t *testing.T) {
	tmpVals := make([]string, 0)
	rg := &ResourceGroup{
		Steps: []*Step{
			{
				Name:    "step",
				Action:  "Shell",
				Command: "echo hello",
				outputFunc: func(output string) {
					tmpVals = append(tmpVals, output)
				},
			},
			{
				Name:    "step",
				Action:  "Shell",
				Command: "faaaaafffaa",
				outputFunc: func(output string) {
					tmpVals = append(tmpVals, output)
				},
			},
			{
				Name:    "step",
				Action:  "Shell",
				Command: "echo hallo",
				outputFunc: func(output string) {
					tmpVals = append(tmpVals, output)
				},
			},
		},
	}
	err := rg.run(context.Background(), &PipelineRunOptions{}, &executionTargetImpl{}, make(map[string]output))
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

	rg := &ResourceGroup{Steps: []*Step{}}
	err := rg.run(context.Background(), &PipelineRunOptions{}, &testExecutionTarget{}, make(map[string]output))
	assert.NilError(t, err)
}

func TestPipelineRun(t *testing.T) {
	foundOutput := ""
	pipeline := &Pipeline{
		ResourceGroups: []*ResourceGroup{
			{
				Name:         "test",
				Subscription: "test",
				Steps: []*Step{
					{
						Name:    "step",
						Action:  "Shell",
						Command: "echo hello",
						outputFunc: func(output string) {
							foundOutput = output
						},
					},
				},
			},
		},
	}

	err := pipeline.Run(context.Background(), &PipelineRunOptions{
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
	s := &Step{
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
