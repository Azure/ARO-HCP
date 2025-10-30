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
	"path/filepath"
	"runtime"
	"sync"
	"time"

	"github.com/go-logr/logr"

	"k8s.io/apimachinery/pkg/util/sets"

	"sigs.k8s.io/yaml"

	"github.com/Azure/ARO-Tools/pkg/config"
	"github.com/Azure/ARO-Tools/pkg/graph"
	"github.com/Azure/ARO-Tools/pkg/topology"
	"github.com/Azure/ARO-Tools/pkg/types"

	"github.com/Azure/ARO-HCP/tooling/templatize/bicep"
)

var DefaultDeploymentTimeoutSeconds = 30 * 6

type PipelineRunOptions struct {
	BaseRunOptions

	Step                  string
	Region                string
	SubsciptionLookupFunc SubscriptionLookup

	TopologyDir string
	Concurrency int

	TimingOutputFile string
}

type BaseRunOptions struct {
	DryRun                   bool
	Cloud                    string
	Configuration            config.Configuration
	NoPersist                bool
	DeploymentTimeoutSeconds int
	StepCacheDir             string
	BicepClient              *bicep.LSPClient
}

type StepRunOptions struct {
	BaseRunOptions
	PipelineDirectory string
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

// Outputs stores output values indexed by service group, resource group and step name.
type Outputs map[string]map[string]map[string]Output

type Executor func(id graph.Identifier, s types.Step, ctx context.Context, executionTarget ExecutionTarget, options *StepRunOptions, state *ExecutionState) (Output, error)

type ExecutionState struct {
	*sync.RWMutex

	Executed sets.Set[graph.Identifier]
	Queued   sets.Set[graph.Identifier]
	Outputs  Outputs

	Timing map[graph.Identifier]*ExecutionInfo
}

type ExecutionInfo struct {
	QueuedAt   string `json:"queuedAt"`
	StartedAt  string `json:"startedAt"`
	FinishedAt string `json:"finishedAt"`
}

type NodeInfo struct {
	Identifier Identifier    `json:"identifier"`
	Info       ExecutionInfo `json:"info"`
}

type Identifier struct {
	ServiceGroup  string `json:"serviceGroup"`
	ResourceGroup string `json:"resourceGroup"`
	Step          string `json:"step"`
}

func RunPipeline(service *topology.Service, pipeline *types.Pipeline, ctx context.Context, options *PipelineRunOptions, executor Executor) (Outputs, error) {
	logger, err := logr.FromContext(ctx)
	if err != nil {
		return nil, err
	}

	logger.Info("Generating execution graph.")
	executionGraph, err := graph.ForPipeline(service, pipeline)
	if err != nil {
		return nil, fmt.Errorf("failed to generate execution graph: %w", err)
	}

	return runGraph(ctx, logger, executionGraph, options, executor)
}

func RunEntrypoint(topo *topology.Topology, entrypoint *topology.Entrypoint, pipelines map[string]*types.Pipeline, ctx context.Context, options *PipelineRunOptions, executor Executor) (Outputs, error) {
	logger, err := logr.FromContext(ctx)
	if err != nil {
		return nil, err
	}

	logger.Info("Generating execution graph.")
	executionGraph, err := graph.ForEntrypoint(topo, entrypoint, pipelines)
	if err != nil {
		return nil, fmt.Errorf("failed to generate execution graph: %w", err)
	}

	return runGraph(ctx, logger, executionGraph, options, executor)
}

func runGraph(ctx context.Context, logger logr.Logger, executionGraph *graph.Graph, options *PipelineRunOptions, executor Executor) (Outputs, error) {
	if options.StepCacheDir != "" {
		if err := os.MkdirAll(options.StepCacheDir, 0755); err != nil {
			return nil, err
		}
	}

	state := &ExecutionState{
		RWMutex:  &sync.RWMutex{},
		Executed: sets.Set[graph.Identifier]{},
		Queued:   sets.Set[graph.Identifier]{},
		Timing:   make(map[graph.Identifier]*ExecutionInfo),
		Outputs:  make(Outputs),
	}

	state.Lock()
	for _, node := range executionGraph.Nodes {
		state.Timing[node.Identifier] = &ExecutionInfo{}
	}
	state.Unlock()

	queue := make(chan graph.Identifier, len(executionGraph.Nodes))
	checkForStepsToExecute := make(chan struct{}, len(executionGraph.Nodes))
	errs := make(chan error, len(executionGraph.Nodes))
	producerWg := &sync.WaitGroup{}
	producerWg.Add(1)
	// producer routine checks to see if we can queue more steps when we finish executing one
	go func() {
		thisLogger := logger.WithValues("routine", "producer")
		defer func() {
			close(queue)
			producerWg.Done()
			thisLogger.V(4).Info("Producer thread shutting down.")
		}()
		for {
			select {
			case _, open := <-checkForStepsToExecute:
				if !open {
					thisLogger.V(4).Info("Done channel closed.")
					return
				}
				thisLogger.V(4).Info("Processing queue after step finished executing.")
				state.RLock()
				for _, node := range executionGraph.Nodes {
					if state.Queued.Has(node.Identifier) {
						continue
					}
					if state.Executed.HasAll(node.Parents...) {
						thisLogger.V(4).Info("Queueing step to run.", "serviceGroup", node.ServiceGroup, "resourceGroup", node.ResourceGroup, "step", node.Step)
						state.Queued.Insert(node.Identifier)
						state.Timing[node.Identifier].QueuedAt = time.Now().Format(time.RFC3339)
						queue <- node.Identifier
					}
				}
				thisLogger.V(4).Info("Execution status.", "nodes", len(executionGraph.Nodes), "queued", len(state.Queued), "executed", len(state.Executed))
				if len(state.Queued) == len(executionGraph.Nodes) {
					thisLogger.V(4).Info("Queued all nodes.")
					state.RUnlock()
					return
				}
				state.RUnlock()
			case <-ctx.Done():
				thisLogger.V(4).Info("Context cancelled.")
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
				thisLogger.V(4).Info("Consumer thread shutting down.")
			}()
			for {
				select {
				case step, open := <-queue:
					if !open {
						thisLogger.V(4).Info("Queue channel closed.")
						return
					}
					stepLogger := thisLogger.WithValues("serviceGroup", step.ServiceGroup, "resourceGroup", step.ResourceGroup, "step", step.Step)
					stepLogger.V(4).Info("Executing step.")
					state.Lock()
					state.Timing[step].StartedAt = time.Now().Format(time.RFC3339)
					state.Unlock()
					err := executeNode(stepLogger, executor, executionGraph, step, consumerCtx, options, state)
					state.Lock()
					state.Timing[step].FinishedAt = time.Now().Format(time.RFC3339)
					state.Unlock()
					if err != nil {
						stepLogger.V(4).Error(err, "Step errored.")
						errs <- err
						consumerCancel()
						return
					}
					stepLogger.V(4).Info("Finished step.")
					checkForStepsToExecute <- struct{}{}
				case <-consumerCtx.Done():
					thisLogger.V(4).Info("Context cancelled.")
					return
				}
			}
		}()
	}

	// bootstrap the process with a signal to queue
	checkForStepsToExecute <- struct{}{}

	logger.V(4).Info("Waiting for consumers to finish.")
	consumerWg.Wait()

	close(checkForStepsToExecute)
	close(errs)

	logger.V(4).Info("Waiting for execution to finish.")
	producerWg.Wait()
	logger.V(4).Info("Execution finished.")
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
	timing := state.Timing
	state.RUnlock()

	if options.TimingOutputFile != "" {
		var times []NodeInfo
		for id, info := range timing {
			times = append(times, NodeInfo{
				Identifier: Identifier{
					ServiceGroup:  id.ServiceGroup,
					ResourceGroup: id.ResourceGroup,
					Step:          id.Step,
				},
				Info: *info,
			})
		}
		encodedTiming, err := yaml.Marshal(times)
		if err != nil {
			return nil, fmt.Errorf("error marshalling timing: %v", err)
		}
		if os.WriteFile(options.TimingOutputFile, encodedTiming, 0644) != nil {
			return nil, fmt.Errorf("error writing timing: %v", err)
		}
	}

	return outputs, nil
}

func executeNode(logger logr.Logger, executor Executor, graphCtx *graph.Graph, node graph.Identifier, ctx context.Context, options *PipelineRunOptions, state *ExecutionState) error {
	state.RLock()
	alreadyDone := state.Executed.Has(node)
	state.RUnlock()
	if alreadyDone {
		logger.V(4).Info("Skipping execution, as it has already happened.")
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

	var output Output
	var stepRunErr error
	if options.Step != "" && step.StepName() != options.Step {
		// skip steps that don't match the specified step name
		output = nil
		stepRunErr = nil
	} else {
		output, stepRunErr = executor(node, step, logr.NewContext(ctx, logger), target, &StepRunOptions{
			BaseRunOptions:    options.BaseRunOptions,
			PipelineDirectory: filepath.Join(options.TopologyDir, filepath.Dir(graphCtx.Services[node.ServiceGroup].PipelinePath)),
		}, state)
	}
	if stepRunErr != nil {
		return stepRunErr
	}

	state.Lock()
	if output != nil {
		logger.V(4).Info("Recording step output.")
		if _, recorded := state.Outputs[node.ServiceGroup]; !recorded {
			state.Outputs[node.ServiceGroup] = map[string]map[string]Output{}
		}
		if _, recorded := state.Outputs[node.ServiceGroup][node.ResourceGroup]; !recorded {
			state.Outputs[node.ServiceGroup][node.ResourceGroup] = map[string]Output{}
		}
		state.Outputs[node.ServiceGroup][node.ResourceGroup][node.Step] = output
	}
	state.Executed.Insert(node)
	state.Unlock()
	return nil
}

func RunStep(id graph.Identifier, s types.Step, ctx context.Context, executionTarget ExecutionTarget, options *StepRunOptions, state *ExecutionState) (Output, error) {
	logger := logr.FromContextOrDiscard(ctx)
	if options.DryRun {
		logger.Info("This is a dry run!")
	}
	logger.Info("Running step.", "description", s.Description())

	switch step := s.(type) {
	case *types.ImageMirrorStep:
		var buf bytes.Buffer

		if step.CopyFrom == "oci-layout" {
			logger.Info("OCI layout image copy is not supported for run step, skipping")
			return nil, nil
		}

		err := runImageMirrorStep(id, ctx, step, options, state, &buf)
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

		err = runShellStep(id, step, ctx, kubeconfigFile, options, state, &buf)
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
	case *types.HelmStep:
		if err := runHelmStep(id, step, ctx, options, executionTarget, state); err != nil {
			return nil, fmt.Errorf("error running Helm release deployment Step, %v", err)
		}
		return nil, nil
	case *types.ARMStep:
		a, err := newArmClient(executionTarget.GetSubscriptionID(), executionTarget.GetRegion(), options.BicepClient)
		if err != nil {
			return nil, fmt.Errorf("failed to create ARM clients: %w", err)
		}
		output, err := a.runArmStep(ctx, options, executionTarget.GetResourceGroup(), id, step, state)
		if err != nil {
			return nil, fmt.Errorf("failed to run ARM step: %w", err)
		}
		return output, nil
	case *types.ARMStackStep:
		output, err := runArmStackStep(ctx, options, executionTarget, id, step, state)
		if err != nil {
			return nil, fmt.Errorf("failed to run ARM step: %w", err)
		}
		return output, nil
	default:
		logger.Info("No implementation for action type - skip", "actionType", s.ActionType())
		return nil, nil
	}
}

func getInputValues(serviceGroup string, configuredVariables []types.Variable, cfg config.Configuration, inputs Outputs) (map[string]any, error) {
	values := make(map[string]any)
	for _, i := range configuredVariables {
		if i.Input != nil {
			value, err := resolveInput(serviceGroup, *i.Input, inputs)
			if err != nil {
				return nil, err
			}
			values[i.Name] = value
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

func resolveInput(serviceGroup string, input types.Input, outputs Outputs) (any, error) {
	sg, exists := outputs[serviceGroup]
	if !exists {
		return nil, fmt.Errorf("variable invalid: refers to missing service group %q", serviceGroup)
	}
	group, exists := sg[input.ResourceGroup]
	if !exists {
		return nil, fmt.Errorf("variable invalid: refers to missing group %q", input.ResourceGroup)
	}
	if v, found := group[input.Step]; found {
		value, err := v.GetValue(input.Name)
		if err != nil {
			return nil, fmt.Errorf("variable invalid: failed to get value for input %s.%s: %w", input.Step, input.Name, err)
		}
		return value.Value, nil
	} else {
		return nil, fmt.Errorf("variable invalid: resource group %s has no step %s", input.ResourceGroup, input.Step)
	}
}
