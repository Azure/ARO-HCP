package pipeline

import (
	"context"
	"testing"

	"gotest.tools/v3/assert"
)

func TestStepRun(t *testing.T) {
	s := &Step{
		Name:    "test",
		Action:  "Shell",
		Command: []string{"echo", "hello"},
		outputFunc: func(output string) {
			assert.Equal(t, output, "hello\n")
		},
	}
	err := s.run(context.Background(), "", &executionTargetImpl{}, &PipelineRunOptions{})
	assert.NilError(t, err)
}

func TestStepRunSkip(t *testing.T) {
	s := &Step{
		Name: "step",
	}
	// this should skip
	err := s.run(context.Background(), "", &executionTargetImpl{}, &PipelineRunOptions{Step: "skip"})
	assert.NilError(t, err)

	// this should fail
	err = s.run(context.Background(), "", &executionTargetImpl{}, &PipelineRunOptions{Step: "step"})
	assert.Error(t, err, "unsupported action type \"\"")
}

func TestRGValidate(t *testing.T) {
	testCases := []struct {
		name string
		rg   *ResourceGroup
		err  string
	}{
		{
			name: "missing name",
			rg:   &ResourceGroup{},
			err:  "resource group name is required",
		},
		{
			name: "missing subscription",
			rg:   &ResourceGroup{Name: "test"},
			err:  "subscription is required",
		},
		{
			name: "missing dependency",
			rg: &ResourceGroup{
				Name:         "test",
				Subscription: "test",
				Steps: []*Step{
					{
						Name:      "step2",
						DependsOn: []string{"step"},
					},
				},
			},
			err: "invalid dependency from step step2 to step",
		},
	}
	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			err := tc.rg.Validate()
			assert.Error(t, err, tc.err)
		})
	}

}

func TestPipelineValidate(t *testing.T) {
	p := &Pipeline{
		ResourceGroups: []*ResourceGroup{{}},
	}
	err := p.Validate()
	assert.Error(t, err, "resource group name is required")
}
