package pipeline

import (
	"context"
	"fmt"
	"os"
	"path/filepath"

	"github.com/go-logr/logr"

	"github.com/Azure/ARO-Tools/pkg/config"
)

var DefaultDeploymentTimeoutSeconds = 30 * 60

func NewPipelineFromFile(pipelineFilePath string, vars config.Variables) (*Pipeline, error) {
	bytes, err := config.PreprocessFile(pipelineFilePath, vars)
	if err != nil {
		return nil, fmt.Errorf("failed to preprocess pipeline file %w", err)
	}

	err = ValidatePipelineSchema(bytes)
	if err != nil {
		return nil, fmt.Errorf("failed to validate pipeline schema: %w", err)
	}

	absPath, err := filepath.Abs(pipelineFilePath)
	if err != nil {
		return nil, fmt.Errorf("failed to get absolute path for pipeline file %q: %w", pipelineFilePath, err)
	}

	pipeline, err := NewPlainPipelineFromBytes(absPath, bytes)
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
	DryRun                   bool
	Step                     string
	Region                   string
	Vars                     config.Variables
	SubsciptionLookupFunc    subsciptionLookup
	NoPersist                bool
	DeploymentTimeoutSeconds int
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

func RunPipeline(pipeline *Pipeline, ctx context.Context, options *PipelineRunOptions) (map[string]output, error) {
	logger := logr.FromContextOrDiscard(ctx)

	outPuts := make(map[string]output)

	// set working directory to the pipeline file directory for the
	// duration of the execution so that all commands and file references
	// within the pipeline file are resolved relative to the pipeline file
	originalDir, err := os.Getwd()
	if err != nil {
		return nil, err
	}
	dir := filepath.Dir(pipeline.pipelineFilePath)
	logger.V(7).Info("switch current dir to pipeline file directory", "path", dir)
	err = os.Chdir(dir)
	if err != nil {
		return nil, err
	}
	defer func() {
		logger.V(7).Info("switch back dir", "path", originalDir)
		err = os.Chdir(originalDir)
		if err != nil {
			logger.Error(err, "failed to switch back to original directory", "path", originalDir)
		}
	}()

	for _, rg := range pipeline.ResourceGroups {
		// prepare execution context
		subscriptionID, err := options.SubsciptionLookupFunc(ctx, rg.Subscription)
		if err != nil {
			return nil, fmt.Errorf("failed to lookup subscription ID for %q: %w", rg.Subscription, err)
		}
		executionTarget := executionTargetImpl{
			subscriptionName: rg.Subscription,
			subscriptionID:   subscriptionID,
			region:           options.Region,
			resourceGroup:    rg.Name,
			aksClusterName:   rg.AKSCluster,
		}
		err = RunResourceGroup(rg, ctx, options, &executionTarget, outPuts)
		if err != nil {
			return nil, err
		}
	}
	return outPuts, nil
}

func RunResourceGroup(rg *ResourceGroup, ctx context.Context, options *PipelineRunOptions, executionTarget ExecutionTarget, outputs map[string]output) error {
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
		output, err := RunStep(
			step,
			logr.NewContext(
				ctx,
				logger.WithValues(
					"step", step.StepName(),
					"subscription", executionTarget.GetSubscriptionID(),
					"resourceGroup", executionTarget.GetResourceGroup(),
					"aksCluster", executionTarget.GetAkSClusterName(),
				),
			),
			kubeconfigFile,
			executionTarget, options,
			outputs,
		)
		if err != nil {
			return err
		}
		if output != nil {

			outputs[step.StepName()] = output
		}
	}
	return nil
}

func RunStep(s Step, ctx context.Context, kubeconfigFile string, executionTarget ExecutionTarget, options *PipelineRunOptions, outPuts map[string]output) (output, error) {
	if options.Step != "" && s.StepName() != options.Step {
		// skip steps that don't match the specified step name
		return nil, nil
	}
	fmt.Println("\n---------------------")
	if options.DryRun {
		fmt.Println("This is a dry run!")
	}
	fmt.Println(s.Description())
	fmt.Print("\n")

	switch step := s.(type) {
	case *ShellStep:
		return nil, runShellStep(step, ctx, kubeconfigFile, options, outPuts)
	case *ARMStep:
		a := newArmClient(executionTarget.GetSubscriptionID(), executionTarget.GetRegion())
		if a == nil {
			return nil, fmt.Errorf("failed to create ARM client")
		}
		output, err := a.runArmStep(ctx, options, executionTarget.GetResourceGroup(), step, outPuts)
		if err != nil {
			return nil, fmt.Errorf("failed to run ARM step: %w", err)
		}
		return output, nil
	default:
		fmt.Println("No implementation for action type - skip", s.ActionType())
		return nil, nil
	}
}

func getInputValues(configuredVariables []Variable, cfg config.Variables, inputs map[string]output) (map[string]any, error) {
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
		} else if i.ConfigRef != "" {
			value, found := cfg.GetByPath(i.ConfigRef)
			if !found {
				return nil, fmt.Errorf("failed to lookup config reference %s for %s", i.ConfigRef, i.Name)
			}
			values[i.Name] = value
		} else {
			values[i.Name] = i.Value
		}
	}
	return values, nil
}
