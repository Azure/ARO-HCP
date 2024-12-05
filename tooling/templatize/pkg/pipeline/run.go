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
	DryRun                bool
	Step                  string
	Region                string
	Vars                  config.Variables
	SubsciptionLookupFunc subsciptionLookup
}

type armOutput map[string]any

type output interface {
	GetValue(key string) (*outPutValue, error)
}

type outPutValue struct {
	Type  string `yaml:"type"`
	Value any    `yaml:"value"`
}

func (o armOutput) GetValue(key string) (*outPutValue, error) {
	if v, ok := o[key]; ok {
		if innerValue, innerConversionOk := v.(map[string]any); innerConversionOk {
			returnValue := outPutValue{
				Type:  innerValue["type"].(string),
				Value: innerValue["value"],
			}
			return &returnValue, nil
		}
	}
	return nil, fmt.Errorf("key %q not found", key)
}

func (p *Pipeline) Run(ctx context.Context, options *PipelineRunOptions) error {
	logger := logr.FromContextOrDiscard(ctx)

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
		subscriptionID, err := options.SubsciptionLookupFunc(ctx, rg.Subscription)
		if err != nil {
			return fmt.Errorf("failed to lookup subscription ID for %q: %w", rg.Subscription, err)
		}
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

	outPuts := make(map[string]output)

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
		output, err := step.run(
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
			outPuts,
		)
		if err != nil {
			return err
		}
		if output != nil {
			outPuts[step.Name] = output
		}
	}
	return nil
}

func (s *Step) run(ctx context.Context, kubeconfigFile string, executionTarget ExecutionTarget, options *PipelineRunOptions, outPuts map[string]output) (output, error) {
	if options.Step != "" && s.Name != options.Step {
		// skip steps that don't match the specified step name
		return nil, nil
	}
	fmt.Println("\n---------------------")
	if options.DryRun {
		fmt.Println("This is a dry run!")
	}
	fmt.Println(s.description())
	fmt.Print("\n")

	switch s.Action {
	case "Shell":
		return nil, s.runShellStep(ctx, kubeconfigFile, options, outPuts)
	case "ARM":
		a := newArmClient(executionTarget.GetSubscriptionID(), executionTarget.GetRegion())
		if a == nil {
			return nil, fmt.Errorf("failed to create ARM client")
		}
		output, err := a.runArmStep(ctx, options, executionTarget.GetResourceGroup(), s, outPuts)
		if err != nil {
			return nil, fmt.Errorf("failed to run ARM step: %w", err)
		}
		return output, nil
	default:
		return nil, fmt.Errorf("unsupported action type %q", s.Action)
	}
}

func (s *Step) description() string {
	var details []string
	switch s.Action {
	case "Shell":
		details = append(details, fmt.Sprintf("Command: %s", s.Command))
	case "ARM":
		details = append(details, fmt.Sprintf("Template: %s", s.Template))
		details = append(details, fmt.Sprintf("Parameters: %s", s.Parameters))
	}
	return fmt.Sprintf("Step %s\n  Kind: %s\n  %s", s.Name, s.Action, strings.Join(details, "\n  "))
}

func (p *Pipeline) Validate() error {
	// collect all steps from all resourcegroups and fail if there are duplicates
	stepMap := make(map[string]*Step)
	for _, rg := range p.ResourceGroups {
		for _, step := range rg.Steps {
			if _, ok := stepMap[step.Name]; ok {
				return fmt.Errorf("duplicate step name %q", step.Name)
			}
			stepMap[step.Name] = step
		}
	}

	// validate dependsOn for a step exists
	for _, step := range stepMap {
		for _, dep := range step.DependsOn {
			if _, ok := stepMap[dep]; !ok {
				return fmt.Errorf("invalid dependency on step %s: dependency %s does not exist", step.Name, dep)
			}
		}
	}

	// todo check for circular dependencies

	// validate resource groups
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
	return nil
}

func getInputValues(configuredVariables []Variable, inputs map[string]output) (map[string]any, error) {
	values := make(map[string]any)
	for _, i := range configuredVariables {
		if i.Input != nil {
			if v, found := inputs[i.Input.Step]; found {
				value, err := v.GetValue(i.Input.Name)
				if err != nil {
					return nil, fmt.Errorf("failed to get value for input %s.%s: %w", i.Input.Step, i.Input.Name, err)
				}
				values[i.Name] = value.Value
			} else {
				return nil, fmt.Errorf("step %s not found in provided outputs", i.Input.Step)
			}
		}
	}
	return values, nil
}
