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
	"runtime"
	"sync"

	"github.com/Azure/ARO-Tools/pkg/graph"
	"github.com/Azure/ARO-Tools/pkg/topology"
	"github.com/go-logr/logr"
	"k8s.io/apimachinery/pkg/util/sets"

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
	Concurrency              int
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

// Outputs stores output values indexed by resource group name and step name.
type Outputs map[string]map[string]Output

type Executor func(s types.Step, ctx context.Context, executionTarget ExecutionTarget, options *PipelineRunOptions, state *ExecutionState) (Output, error)

type ExecutionState struct {
	*sync.RWMutex

	Executed sets.Set[graph.Dependency]
	Queued   sets.Set[graph.Dependency]
	Outputs  Outputs
}

func RunPipeline(service *topology.Service, pipeline *types.Pipeline, ctx context.Context, options *PipelineRunOptions, executor Executor) (Outputs, error) {
	logger, err := logr.FromContext(ctx)
	if err != nil {
		return nil, err
	}

	logger.Info("Generating execution graph.")
	graphCtx, err := graph.ForPipeline(service, pipeline)
	if err != nil {
		return nil, fmt.Errorf("failed to generate execution graph: %w", err)
	}

	state := &ExecutionState{
		RWMutex:  &sync.RWMutex{},
		Executed: sets.Set[graph.Dependency]{},
		Queued:   sets.Set[graph.Dependency]{},
		Outputs:  make(Outputs),
	}

	queue := make(chan graph.Dependency, len(graphCtx.Nodes))
	done := make(chan struct{}, len(graphCtx.Nodes))
	errs := make(chan error, len(graphCtx.Nodes))
	producerWg := &sync.WaitGroup{}
	producerWg.Add(1)
	// producer routine checks to see if we can queue more steps when we finish executing one
	go func() {
		thisLogger := logger.WithValues("routine", "producer")
		defer func() {
			producerWg.Done()
			thisLogger.Info("Producer thread shutting down.")
		}()
		for {
			select {
			case _, open := <-done:
				if !open {
					thisLogger.Info("Done channel closed.")
					close(queue)
					return
				}
				thisLogger.Info("Processing queue after step finished executing.")
				state.RLock()
				for _, node := range graphCtx.Nodes {
					if state.Queued.Has(node.Dependency) {
						continue
					}
					if state.Executed.HasAll(node.Parents...) {
						thisLogger.Info("Queueing step to run.", "serviceGroup", node.ServiceGroup, "resourceGroup", node.ResourceGroup, "step", node.Step)
						state.Queued.Insert(node.Dependency)
						queue <- node.Dependency
					}
				}
				thisLogger.Info("Execution status.", "nodes", len(graphCtx.Nodes), "queued", len(state.Queued), "executed", len(state.Executed))
				if len(state.Queued) == len(graphCtx.Nodes) {
					thisLogger.Info("Queued all nodes.")
					close(queue)
					state.RUnlock()
					return
				}
				state.RUnlock()
			case <-ctx.Done():
				thisLogger.Info("Context cancelled.")
				close(queue)
				return
			}
		}
	}()
	// consumer routines pop steps off the execution queue and signal that they finished
	// consumers have a couple different conditions on which they'll exit:
	// - if the producer has finished queueing, and consumers hit the end of the queue
	// - if any consumer hits an error, the context is cancelled
	// - if the parent context is cancelled
	consumerWg := &sync.WaitGroup{}
	consumerCtx, consumerCancel := context.WithCancel(ctx)
	defer func() {
		consumerCancel()
	}()
	maxConcurrency := options.Concurrency
	if maxConcurrency == 0 {
		maxConcurrency = runtime.NumCPU()
	}
	for i := 0; i < maxConcurrency; i++ {
		consumerWg.Add(1)
		go func() {
			thisLogger := logger.WithValues("routine", fmt.Sprintf("consumer-%d", i))
			defer func() {
				consumerWg.Done()
				thisLogger.Info("Consumer thread shutting down.")
			}()
			for {
				select {
				case step, open := <-queue:
					if !open {
						thisLogger.Info("Queue channel closed.")
						return
					}
					stepLogger := thisLogger.WithValues("serviceGroup", step.ServiceGroup, "resourceGroup", step.ResourceGroup, "step", step.Step)
					stepLogger.Info("Executing step.")
					if err := executeNode(executor, graphCtx, step, consumerCtx, options, state); err != nil {
						stepLogger.Info("Step errored.")
						errs <- err
						consumerCancel()
						return
					}
					stepLogger.Info("Finished step.")
					done <- struct{}{}
				case <-consumerCtx.Done():
					thisLogger.Info("Context cancelled.")
					return
				}
			}
		}()
	}

	// bootstrap the process with a signal to queue
	done <- struct{}{}

	logger.Info("Waiting for consumers to finish.")
	consumerWg.Wait()

	close(done)
	close(errs)

	logger.Info("Waiting for execution to finish.")
	producerWg.Wait()
	logger.Info("Execution finished.")
	var executionErrors []error
	for err := range errs {
		if err != nil {
			executionErrors = append(executionErrors, err)
		}
	}
	if len(executionErrors) > 0 {
		return nil, fmt.Errorf("errors occurred during execution: %v", executionErrors)
	}
	state.RLock()
	outputs := state.Outputs
	state.RUnlock()
	return outputs, nil
}

func executeNode(executor Executor, graphCtx *graph.Context, node graph.Dependency, ctx context.Context, options *PipelineRunOptions, state *ExecutionState) error {
	logger, err := logr.FromContext(ctx)
	if err != nil {
		return err
	}

	logger = logger.WithValues("serviceGroup", node.ServiceGroup, "resourceGroup", node.ResourceGroup, "step", node.Step)
	state.RLock()
	alreadyDone := state.Executed.Has(node)
	state.RUnlock()
	if alreadyDone {
		logger.Info("Skipping execution, as it has already happened.")
		// our graph may converge, where many children need one parent - no need to re-execute then
		return nil
	}

	resourceGroup, exists := graphCtx.ResourceGroups[node.ResourceGroup]
	if !exists {
		return fmt.Errorf("could not find resource group %s", node.ResourceGroup)
	}

	step, exists := graphCtx.Steps[node.ServiceGroup][node.ResourceGroup][node.Step]
	if !exists {
		return fmt.Errorf("could not find step %s/%s/%s", node.ServiceGroup, node.ResourceGroup, node.Step)
	}

	subscriptionID, err := options.SubsciptionLookupFunc(ctx, resourceGroup.Subscription)
	if err != nil {
		return fmt.Errorf("failed to lookup subscription ID for %q: %w", resourceGroup.Subscription, err)
	}
	target := &executionTargetImpl{
		subscriptionName: resourceGroup.Subscription,
		subscriptionID:   subscriptionID,
		region:           options.Region,
		resourceGroup:    resourceGroup.ResourceGroup,
	}

	output, err := executor(step, ctx, target, options, state)
	if err != nil {
		return err
	}

	state.Lock()
	if output != nil {
		logger.Info("Recording step output.")
		if _, recorded := state.Outputs[node.ResourceGroup]; !recorded {
			state.Outputs[node.ResourceGroup] = map[string]Output{}
		}
		state.Outputs[node.ResourceGroup][node.Step] = output
	}
	state.Executed.Insert(node)
	state.Unlock()
	return nil
}

func RunStep(s types.Step, ctx context.Context, executionTarget ExecutionTarget, options *PipelineRunOptions, state *ExecutionState) (Output, error) {
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

		err := runImageMirrorStep(ctx, step, options, state, &buf)
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

		err = runShellStep(step, ctx, kubeconfigFile, options, state, &buf)
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
		output, err := a.runArmStep(ctx, options, executionTarget.GetResourceGroup(), step, state)
		if err != nil {
			return nil, fmt.Errorf("failed to run ARM step: %w", err)
		}
		return output, nil
	default:
		fmt.Println("No implementation for action type - skip", s.ActionType())
		return nil, nil
	}
}

func getInputValues(configuredVariables []types.Variable, cfg config.Configuration, inputs Outputs) (map[string]any, error) {
	values := make(map[string]any)
	for _, i := range configuredVariables {
		if i.Input != nil {
			group, exists := inputs[i.Input.ResourceGroup]
			if !exists {
				return nil, fmt.Errorf("variable %s invalid: refers to missing group %q", i.Name, i.Input.ResourceGroup)
			}
			if v, found := group[i.Input.Step]; found {
				value, err := v.GetValue(i.Input.Name)
				if err != nil {
					return nil, fmt.Errorf("variable %s invalid: failed to get value for input %s.%s: %w", i.Name, i.Input.Step, i.Input.Name, err)
				}
				values[i.Name] = value.Value
			} else {
				return nil, fmt.Errorf("variable %s invalid: resource group %s has no step %s", i.Name, i.Input.ResourceGroup, i.Input.Step)
			}
		} else if i.ConfigRef != "" {
			value, err := cfg.GetByPath(i.ConfigRef)
			if err != nil {
				return nil, fmt.Errorf("variable %s invalid: failed to lookup config reference %s for %s: %w", i.Name, i.ConfigRef, i.Name, err)
			}
			values[i.Name] = value
		} else {
			values[i.Name] = i.Value.Value
		}
	}
	return values, nil
}
