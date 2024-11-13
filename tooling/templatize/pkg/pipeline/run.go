package pipeline

import (
	"context"
	"fmt"
	"os"
	"path/filepath"

	"gopkg.in/yaml.v3"

	"github.com/Azure/ARO-HCP/tooling/templatize/pkg/config"
)

func NewPipelineFromFile(pipelineFilePath string, vars config.Variables) (*Pipeline, error) {
	bytes, err := config.PreprocessFile(pipelineFilePath, vars)
	if err != nil {
		return nil, err
	}

	pipeline := &Pipeline{
		pipelineFilePath: pipelineFilePath,
	}
	err = yaml.Unmarshal(bytes, pipeline)
	if err != nil {
		return nil, err
	}
	return pipeline, nil
}

func (p *Pipeline) Run(ctx context.Context, vars config.Variables) error {
	// set working directory to the pipeline file directory for the duration of
	// the execution
	originalDir, err := os.Getwd()
	if err != nil {
		return err
	}
	dir := filepath.Dir(p.pipelineFilePath)
	err = os.Chdir(dir)
	if err != nil {
		return err
	}
	defer func() {
		err := os.Chdir(originalDir)
		if err != nil {
			fmt.Printf("failed to reset directory: %v\n", err)
		}
	}()

	for _, step := range p.Steps {
		err := step.run(ctx, vars)
		if err != nil {
			return err
		}
	}
	return nil
}

func (s *step) executionTarget(ctx context.Context) (*StepExecutionTarget, error) {
	subscriptionID, err := lookupSubscriptionID(ctx, s.Subscription)
	if err != nil {
		return nil, err
	}
	return &StepExecutionTarget{
		SubscriptionName: s.Subscription,
		SubscriptionID:   subscriptionID,
		ResourceGroup:    s.ResourceGroup,
		AKSClusterName:   s.AKSClusterName,
	}, nil
}

func (s *step) run(ctx context.Context, vars config.Variables) error {
	executionTarget, err := s.executionTarget(ctx)
	if err != nil {
		return err
	}
	switch s.Action.Type {
	case "Shell":
		return s.runShellStep(ctx, executionTarget, vars)
	default:
		return fmt.Errorf("unsupported action type %q", s.Action.Type)
	}
}
