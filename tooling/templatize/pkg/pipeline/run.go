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
	"slices"
	"strings"
	"sync"

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

type Executor func(s types.Step, ctx context.Context, executionTarget ExecutionTarget, options *PipelineRunOptions, outPuts Outputs) (Output, error)

func RunPipeline(pipeline *types.Pipeline, ctx context.Context, options *PipelineRunOptions, executor Executor) (Outputs, error) {
	logger, err := logr.FromContext(ctx)
	if err != nil {
		return nil, err
	}

	logger.Info("Generating execution graph.")
	nodes, err := createExecutionGraph(ctx, pipeline, options.Region, options.SubsciptionLookupFunc)
	if err != nil {
		return nil, err
	}

	lock := &sync.RWMutex{}
	executed := sets.Set[types.StepDependency]{}
	queued := sets.Set[types.StepDependency]{}
	outputs := make(Outputs)

	queue := make(chan *stepExecutionNode, len(nodes))
	done := make(chan struct{}, len(nodes))
	errs := make(chan error, len(nodes))
	wg := &sync.WaitGroup{}
	wg.Add(1)
	// producer routine checks to see if we can queue more steps when we finish executing one
	go func() {
		thisLogger := logger.WithValues("routine", "producer")
		defer func() {
			wg.Done()
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
				lock.RLock()
				for _, node := range nodes {
					thisNode := types.StepDependency{
						ResourceGroup: node.Step.ResourceGroup.Name,
						Step:          node.Step.Delegate.StepName(),
					}
					if queued.Has(thisNode) {
						continue
					}
					if executed.HasAll(contextsToDependencies(node.Parents)...) {
						thisLogger.Info("Queueing step to run.", "resourceGroup", node.Step.ResourceGroup.Name, "step", node.Step.Delegate.StepName())
						queued.Insert(thisNode)
						queue <- node
					}
				}
				lock.RUnlock()
				thisLogger.Info("Execution status.", "nodes", len(nodes), "queued", len(queued), "executed", len(executed))
				if len(queued) == len(nodes) {
					thisLogger.Info("Queued all nodes.")
					close(queue)
					return
				}
			case <-ctx.Done():
				thisLogger.Info("Context cancelled.")
				close(queue)
				return
			}
		}
	}()
	// consumer routines pop steps off the execution queue and signal that they finished
	const maxConcurrency = 1 // TODO: actually do parallel execution and see what breaks
	for i := 0; i < maxConcurrency; i++ {
		wg.Add(1)
		go func() {
			thisLogger := logger.WithValues("routine", fmt.Sprintf("consumer-%d", i))
			defer func() {
				wg.Done()
				thisLogger.Info("Consumer thread shutting down.")
			}()
			for {
				select {
				case step, open := <-queue:
					if !open {
						thisLogger.Info("Queue channel closed.")
						close(done)
						close(errs)
						return
					}
					stepLogger := thisLogger.WithValues("resourceGroup", step.Step.ResourceGroup.Name, "step", step.Step.Delegate.StepName())
					stepLogger.Info("Executing step.")
					if err := executeNode(executor, step, ctx, options, outputs, executed, lock); err != nil {
						stepLogger.Info("Step errored.")
						close(done)
						errs <- err
						close(errs)
						return
					}
					stepLogger.Info("Finished step.")
					done <- struct{}{}
				case <-ctx.Done():
					thisLogger.Info("Context cancelled.")
					close(done)
					close(errs)
					return
				}
			}
		}()
	}

	// bootstrap the process with a signal to queue
	done <- struct{}{}

	logger.Info("Waiting for execution to finish.")
	wg.Wait()
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
	return outputs, nil
}

func contextsToDependencies(contexts []*stepExecutionContext) []types.StepDependency {
	out := make([]types.StepDependency, len(contexts))
	for i, ctx := range contexts {
		out[i] = types.StepDependency{
			ResourceGroup: ctx.ResourceGroup.Name,
			Step:          ctx.Delegate.StepName(),
		}
	}
	return out
}

type stepExecutionContext struct {
	ResourceGroup *types.ResourceGroup
	Delegate      types.Step
	Target        ExecutionTarget
}

type stepExecutionDependency struct {
	// parent must run before child
	Parent, Child *stepExecutionContext
}

type stepExecutionNode struct {
	Step     *stepExecutionContext
	Parents  []*stepExecutionContext
	Children []*stepExecutionContext
}

func createExecutionGraph(ctx context.Context, pipeline *types.Pipeline, region string, subscription subsciptionLookup) ([]*stepExecutionNode, error) {
	// first, create a registry of steps by their identifier (resource group name, step name)
	stepsByResourceGroupAndName := map[string]map[string]*stepExecutionContext{}
	for _, rg := range pipeline.ResourceGroups {
		// prepare execution context
		subscriptionID, err := subscription(ctx, rg.Subscription)
		if err != nil {
			return nil, fmt.Errorf("failed to lookup subscription ID for %q: %w", rg.Subscription, err)
		}
		executionTarget := executionTargetImpl{
			subscriptionName: rg.Subscription,
			subscriptionID:   subscriptionID,
			region:           region,
			resourceGroup:    rg.ResourceGroup,
		}

		stepsByResourceGroupAndName[rg.Name] = map[string]*stepExecutionContext{}
		for _, step := range rg.Steps {
			stepsByResourceGroupAndName[rg.Name][step.StepName()] = &stepExecutionContext{
				ResourceGroup: rg,
				Delegate:      step,
				Target:        &executionTarget,
			}
		}
	}

	// next, create an adjacency list of edges between these nodes
	var stepDependencies []*stepExecutionDependency
	for _, rg := range pipeline.ResourceGroups {
		for _, step := range rg.Steps {
			dependsOn := append(step.Dependencies(), step.RequiredInputs()...)
			slices.SortFunc(dependsOn, func(a, b types.StepDependency) int {
				if cmp := strings.Compare(a.ResourceGroup, b.ResourceGroup); cmp != 0 {
					return cmp
				}
				return strings.Compare(a.Step, b.Step)
			})
			dependsOn = slices.Compact(dependsOn)

			child, recorded := stepsByResourceGroupAndName[rg.Name][step.StepName()]
			if !recorded {
				return nil, fmt.Errorf("step %s/%s not recorded - this should never happen, programmer error", rg.Name, step.StepName())
			}

			for _, dep := range dependsOn {
				parent, recorded := stepsByResourceGroupAndName[dep.ResourceGroup][dep.Step]
				if !recorded {
					return nil, fmt.Errorf("step %s/%s depends on a step %s/%s that is not recorded - this should never happen with a validated pipeline, programmer error", rg.Name, step.StepName(), dep.ResourceGroup, dep.Step)
				}

				stepDependencies = append(stepDependencies, &stepExecutionDependency{
					Parent: parent,
					Child:  child,
				})
			}
		}
	}

	// record edges as references in nodes for ease of traversal
	var nodes []*stepExecutionNode
	for _, steps := range stepsByResourceGroupAndName {
		for _, step := range steps {
			node := &stepExecutionNode{
				Step:     step,
				Parents:  []*stepExecutionContext{},
				Children: []*stepExecutionContext{},
			}
			for _, edge := range stepDependencies {
				if edge.Child != nil && edge.Child == step {
					node.Parents = append(node.Parents, edge.Parent)
				}
				if edge.Parent != nil && edge.Parent == step {
					node.Children = append(node.Children, edge.Child)
				}
			}
			nodes = append(nodes, node)
		}
	}

	slices.SortFunc(nodes, func(a, b *stepExecutionNode) int {
		if cmp := strings.Compare(a.Step.ResourceGroup.Name, b.Step.ResourceGroup.Name); cmp != 0 {
			return cmp
		}
		return strings.Compare(a.Step.Delegate.StepName(), b.Step.Delegate.StepName())
	})

	// check for cycles
	for _, node := range nodes {
		seen := []*stepExecutionContext{
			node.Step,
		}
		if err := traverse(node, nodes, seen); err != nil {
			return nil, err
		}
	}

	return nodes, nil
}

func traverse(node *stepExecutionNode, all []*stepExecutionNode, seen []*stepExecutionContext) error {
	for _, child := range node.Children {
		for _, previous := range seen {
			if previous == child {
				var cycle []string
				for _, i := range seen {
					cycle = append(cycle, fmt.Sprintf("%s/%s", i.ResourceGroup.Name, i.Delegate.StepName()))
				}
				return fmt.Errorf("cycle detected, reached %s/%s via %s", child.ResourceGroup.Name, child.Delegate.StepName(), strings.Join(cycle, " -> "))
			}
		}
		chain := seen[:]
		chain = append(chain, child)
		var childNode *stepExecutionNode
		for _, candidate := range all {
			if candidate.Step == child {
				childNode = candidate
			}
		}
		if childNode == nil {
			return fmt.Errorf("could not find child node %s/%s - programmer error", child.ResourceGroup.Name, child.Delegate.StepName())
		}
		if err := traverse(childNode, all, chain); err != nil {
			return err
		}
	}
	return nil
}

func executeNode(executor Executor, node *stepExecutionNode, ctx context.Context, options *PipelineRunOptions, outputs Outputs, executed sets.Set[types.StepDependency], lock *sync.RWMutex) error {
	logger, err := logr.FromContext(ctx)
	if err != nil {
		return err
	}
	logger = logger.WithValues("resourceGroup", node.Step.ResourceGroup.Name, "step", node.Step.Delegate.StepName())
	thisNode := types.StepDependency{ResourceGroup: node.Step.ResourceGroup.Name, Step: node.Step.Delegate.StepName()}
	lock.RLock()
	alreadyDone := executed.Has(thisNode)
	lock.RUnlock()
	if alreadyDone {
		logger.Info("Skipping execution, as it has already happened.")
		// our graph may converge, where many children need one parent - no need to re-execute then
		return nil
	}

	output, err := executor(node.Step.Delegate, ctx, node.Step.Target, options, outputs)
	if err != nil {
		return err
	}

	lock.Lock()
	if output != nil {
		logger.Info("Recording step output.")
		if _, recorded := outputs[node.Step.ResourceGroup.Name]; !recorded {
			outputs[node.Step.ResourceGroup.Name] = map[string]Output{}
		}
		outputs[node.Step.ResourceGroup.Name][node.Step.Delegate.StepName()] = output
	}
	executed.Insert(thisNode)
	lock.Unlock()
	return nil
}

func RunStep(s types.Step, ctx context.Context, executionTarget ExecutionTarget, options *PipelineRunOptions, outPuts Outputs) (Output, error) {
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
