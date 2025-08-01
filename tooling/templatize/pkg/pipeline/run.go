// Copyright 2025 Microsoft Corporation
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package pipeline

import (
	"bytes"
	"context"
	"fmt"
	"os"

	"github.com/go-logr/logr"

	"github.com/Azure/ARO-Tools/pkg/config"
	"github.com/Azure/ARO-Tools/pkg/types"
)

var DefaultDeploymentTimeoutSeconds = 30 * 60

type subsciptionLookup func(context.Context, string) (string, error)

type PipelineRunOptions struct {
	DryRun                   bool
	Step                     string
	Region                   string
	Cloud                    string
	Configuration            config.Configuration
	SubsciptionLookupFunc    subsciptionLookup
	NoPersist                bool
	DeploymentTimeoutSeconds int
	PipelineFilePath         string
}

type Output interface {
	GetValue(key string) (*OutPutValue, error)
}

type OutPutValue struct {
	Type  string `json:"type"`
	Value any    `json:"value"`
}

type ArmOutput map[string]any

func (o ArmOutput) GetValue(key string) (*OutPutValue, error) {
	if v, ok := o[key]; ok {
		if innerValue, innerConversionOk := v.(map[string]any); innerConversionOk {
			returnValue := OutPutValue{
				Type:  innerValue["type"].(string),
				Value: innerValue["value"],
			}
			return &returnValue, nil
		}
	}
	return nil, fmt.Errorf("key %q not found", key)
}

type ShellOutput string

func (o ShellOutput) GetValue(_ string) (*OutPutValue, error) {
	return &OutPutValue{
		Type:  "string",
		Value: string(o),
	}, nil
}

func RunPipeline(pipeline *types.Pipeline, ctx context.Context, options *PipelineRunOptions) (map[string]Output, error) {
	outPuts := make(map[string]Output)
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
		}
		err = RunResourceGroup(rg, ctx, options, &executionTarget, outPuts)
		if err != nil {
			return nil, err
		}
	}
	return outPuts, nil
}

func RunResourceGroup(rg *types.ResourceGroup, ctx context.Context, options *PipelineRunOptions, executionTarget ExecutionTarget, outputs map[string]Output) error {
	logger := logr.FromContextOrDiscard(ctx)

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
				),
			),
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

func RunStep(s types.Step, ctx context.Context, executionTarget ExecutionTarget, options *PipelineRunOptions, outPuts map[string]Output) (Output, error) {
	logger := logr.FromContextOrDiscard(ctx)

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
	case *types.ImageMirrorStep:
		var buf bytes.Buffer

		err := runImageMirrorStep(ctx, step, options, outPuts, &buf)
		if err != nil {
			return nil, fmt.Errorf("error running Image Mirror Step, %v", err)
		}
		output := buf.String()
		fmt.Println(output)
		return ShellOutput(output), nil
	case *types.ShellStep:
		var buf bytes.Buffer

		kubeconfigFile, err := KubeConfig(ctx, executionTarget.GetSubscriptionID(), executionTarget.GetResourceGroup(), step.AKSCluster)
		if kubeconfigFile != "" {
			defer func() {
				if err := os.Remove(kubeconfigFile); err != nil {
					logger.V(5).Error(err, "failed to delete kubeconfig file", "kubeconfig", kubeconfigFile)
				}
			}()
		} else if err != nil {
			return nil, fmt.Errorf("failed to prepare kubeconfig: %w", err)
		}

		err = runShellStep(step, ctx, kubeconfigFile, options, outPuts, &buf)
		if err != nil {
			return nil, fmt.Errorf("error running Shell Step, %v", err)
		}
		output := buf.String()
		fmt.Println(output)
		return ShellOutput(output), nil
	case *types.SecretSyncStep:
		if err := runSecretSyncStep(step, ctx, options); err != nil {
			return nil, fmt.Errorf("error running secret sync Step, %v", err)
		}
		return nil, nil
	case *types.ProviderFeatureRegistrationStep:
		if err := runRegistrationStep(step, ctx, options, executionTarget); err != nil {
			return nil, fmt.Errorf("error running provider and feature registration Step, %v", err)
		}
		return nil, nil
	case *types.ARMStep:
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

func getInputValues(configuredVariables []types.Variable, cfg config.Configuration, inputs map[string]Output) (map[string]any, error) {
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
			value, err := cfg.GetByPath(i.ConfigRef)
			if err != nil {
				return nil, fmt.Errorf("failed to lookup config reference %s for %s: %w", i.ConfigRef, i.Name, err)
			}
			values[i.Name] = value
		} else {
			values[i.Name] = i.Value.Value
		}
	}
	return values, nil
}
