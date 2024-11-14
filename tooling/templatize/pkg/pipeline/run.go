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

	for _, rg := range p.ResourceGroups {
		err := rg.run(ctx, vars)
		if err != nil {
			return err
		}
	}
	return nil
}

func (rg *resourceGroup) run(ctx context.Context, vars config.Variables) error {
	subscriptionID, err := lookupSubscriptionID(ctx, rg.Subscription)
	if err != nil {
		return err
	}
	executionTarget := &ExecutionTarget{
		SubscriptionName: rg.Subscription,
		SubscriptionID:   subscriptionID,
		ResourceGroup:    rg.Name,
		AKSClusterName:   rg.AKSCluster,
	}

	for _, step := range rg.Steps {
		err := step.run(ctx, executionTarget, vars)
		if err != nil {
			return err
		}
	}
	return nil
}

func (s *step) run(ctx context.Context, executionTarget *ExecutionTarget, vars config.Variables) error {
	switch s.Action {
	case "Shell":
		return s.runShellStep(ctx, executionTarget, vars)
	case "ARM":
		return fmt.Errorf("not implemented %q", s.Action)
	default:
		return fmt.Errorf("unsupported action type %q", s.Action)
	}
}
