package pipeline

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

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

type PipelineRunOptions struct {
	DryRun bool
	Region string
	Vars   config.Variables
}

func (p *Pipeline) Run(ctx context.Context, options *PipelineRunOptions) error {
	// set working directory to the pipeline file directory for the
	// duration of the execution so that all commands and file references
	// within the pipeline file are resolved relative to the pipeline file
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
		err := rg.run(ctx, options)
		if err != nil {
			return err
		}
	}
	return nil
}

func (rg *resourceGroup) run(ctx context.Context, options *PipelineRunOptions) error {
	subscriptionID, err := lookupSubscriptionID(ctx, rg.Subscription)
	if err != nil {
		return err
	}
	executionTarget := &ExecutionTarget{
		SubscriptionName: rg.Subscription,
		SubscriptionID:   subscriptionID,
		Region:           options.Region,
		ResourceGroup:    rg.Name,
		AKSClusterName:   rg.AKSCluster,
	}

	for _, step := range rg.Steps {
		fmt.Println("\n---------------------")
		fmt.Println(step.description())
		fmt.Print("\n")
		err := step.run(ctx, executionTarget, options)
		if err != nil {
			return err
		}
	}
	return nil
}

func (s *step) run(ctx context.Context, executionTarget *ExecutionTarget, options *PipelineRunOptions) error {
	switch s.Action {
	case "Shell":
		return s.runShellStep(ctx, executionTarget, options)
	case "ARM":
		return s.runArmStep(ctx, executionTarget, options)
	default:
		return fmt.Errorf("unsupported action type %q", s.Action)
	}
}

func (s *step) description() string {
	var details []string
	switch s.Action {
	case "Shell":
		details = append(details, fmt.Sprintf("Command: %v", strings.Join(s.Command, " ")))
	case "ARM":
		details = append(details, fmt.Sprintf("Template: %s", s.Template))
		details = append(details, fmt.Sprintf("Parameters: %s", s.Parameters))
	}
	return fmt.Sprintf("Step %s\n  Kind: %s\n  %s", s.Name, s.Action, strings.Join(details, "\n  "))
}
