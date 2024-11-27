package pipeline

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"gopkg.in/yaml.v3"

	"github.com/go-logr/logr"

	"github.com/Azure/ARO-HCP/tooling/templatize/pkg/config"
)

func NewPipelineFromFile(pipelineFilePath string, vars config.Variables) (*Pipeline, error) {
	bytes, err := config.PreprocessFile(pipelineFilePath, vars)
	if err != nil {
		return nil, fmt.Errorf("failed to preprocess pipeline file %w", err)
	}
	absPath, err := filepath.Abs(pipelineFilePath)
	if err != nil {
		return nil, fmt.Errorf("failed to get absolute path for pipeline file %q: %w", pipelineFilePath, err)
	}

	pipeline := &Pipeline{
		pipelineFilePath: absPath,
	}
	err = yaml.Unmarshal(bytes, pipeline)
	if err != nil {
		return nil, fmt.Errorf("failed to unmarshal pipeline file %w", err)
	}
	err = pipeline.Validate()
	if err != nil {
		return nil, fmt.Errorf("pipeline file failed validation %w", err)
	}
	return pipeline, nil
}

type PipelineRunOptions struct {
	DryRun bool
	Step   string
	Region string
	Vars   config.Variables
}

func (p *Pipeline) Run(ctx context.Context, options *PipelineRunOptions) error {
	logger := logr.FromContextOrDiscard(ctx)

	if p.subsciptionLookupFunc == nil {
		p.subsciptionLookupFunc = lookupSubscriptionID
	}

	// set working directory to the pipeline file directory for the
	// duration of the execution so that all commands and file references
	// within the pipeline file are resolved relative to the pipeline file
	originalDir, err := os.Getwd()
	if err != nil {
		return err
	}
	dir := filepath.Dir(p.pipelineFilePath)
	logger.V(7).Info("switch current dir to pipeline file directory", "path", dir)
	err = os.Chdir(dir)
	if err != nil {
		return err
	}
	defer func() {
		logger.V(7).Info("switch back dir", "path", originalDir)
		err = os.Chdir(originalDir)
		if err != nil {
			logger.Error(err, "failed to switch back to original directory", "path", originalDir)
		}
	}()

	for _, rg := range p.ResourceGroups {
		// prepare execution context
		subscriptionID, err := p.subsciptionLookupFunc(ctx, rg.Subscription)
		executionTarget := executionTargetImpl{
			subscriptionName: rg.Subscription,
			subscriptionID:   subscriptionID,
			region:           options.Region,
			resourceGroup:    rg.Name,
			aksClusterName:   rg.AKSCluster,
		}
		err = rg.run(ctx, options, &executionTarget)
		if err != nil {
			return err
		}
	}
	return nil
}

func (rg *ResourceGroup) run(ctx context.Context, options *PipelineRunOptions, executionTarget ExecutionTarget) error {
	logger := logr.FromContextOrDiscard(ctx)

	kubeconfigFile, err := executionTarget.KubeConfig(ctx)
	if kubeconfigFile != "" {
		defer func() {
			if err := os.Remove(kubeconfigFile); err != nil {
				logger.V(5).Error(err, "failed to delete kubeconfig file", "kubeconfig", kubeconfigFile)
			}
		}()
	} else if err != nil {
		return fmt.Errorf("failed to prepare kubeconfig: %w", err)
	}

	for _, step := range rg.Steps {
		// execute
		err := step.run(
			logr.NewContext(
				ctx,
				logger.WithValues(
					"step", step.Name,
					"subscription", executionTarget.GetSubscriptionID(),
					"resourceGroup", executionTarget.GetResourceGroup(),
					"aksCluster", executionTarget.GetAkSClusterName(),
				),
			),
			kubeconfigFile,
			executionTarget, options,
		)
		if err != nil {
			return err
		}
	}
	return nil
}

func (s *Step) run(ctx context.Context, kubeconfigFile string, executionTarget ExecutionTarget, options *PipelineRunOptions) error {
	if options.Step != "" && s.Name != options.Step {
		// skip steps that don't match the specified step name
		return nil
	}
	fmt.Println("\n---------------------")
	if options.DryRun {
		fmt.Println("This is a dry run!")
	}
	fmt.Println(s.description())
	fmt.Print("\n")

	switch s.Action {
	case "Shell":
		return s.runShellStep(ctx, kubeconfigFile, options)
	case "ARM":
		return s.runArmStep(ctx, executionTarget, options)
	default:
		return fmt.Errorf("unsupported action type %q", s.Action)
	}
}

func (s *Step) description() string {
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

func (p *Pipeline) Validate() error {
	for _, rg := range p.ResourceGroups {
		err := rg.Validate()
		if err != nil {
			return err
		}
	}
	return nil
}

func (rg *ResourceGroup) Validate() error {
	if rg.Name == "" {
		return fmt.Errorf("resource group name is required")
	}
	if rg.Subscription == "" {
		return fmt.Errorf("subscription is required")
	}

	// validate step dependencies
	// todo - check for circular dependencies
	stepMap := make(map[string]bool)
	for _, step := range rg.Steps {
		stepMap[step.Name] = true
	}
	for _, step := range rg.Steps {
		for _, dep := range step.DependsOn {
			if !stepMap[dep] {
				return fmt.Errorf("invalid dependency from step %s to %s", step.Name, dep)
			}
		}
	}
	return nil
}
