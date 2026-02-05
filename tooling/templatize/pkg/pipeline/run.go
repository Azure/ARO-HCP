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
	"compress/gzip"
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/go-logr/logr"

	"k8s.io/apimachinery/pkg/util/sets"

	"sigs.k8s.io/yaml"

	configtypes "github.com/Azure/ARO-Tools/pkg/config/types"
	"github.com/Azure/ARO-Tools/pkg/graph"
	"github.com/Azure/ARO-Tools/pkg/topology"
	"github.com/Azure/ARO-Tools/pkg/types"

	"github.com/Azure/ARO-HCP/tooling/templatize/bicep"
	"github.com/Azure/ARO-HCP/tooling/templatize/pkg/junit"
)

var DefaultDeploymentTimeoutSeconds = 30 * 6

func compressTimingMetadata() bool {
	ret, _ := strconv.ParseBool(os.Getenv("COMPRESS_TIMING_METADATA"))
	return ret
}

type PipelineRunOptions struct {
	BaseRunOptions

	Step                  string
	Region                string
	SubsciptionLookupFunc SubscriptionLookup

	TopologyDir string
	Concurrency int

	TimingOutputFile string
	JUnitOutputFile  string
}

type BaseRunOptions struct {
	DryRun                   bool
	Cloud                    string
	Configuration            configtypes.Configuration
	NoPersist                bool
	DeploymentTimeoutSeconds int
	StepCacheDir             string
	BicepClient              *bicep.LSPClient

	SubscriptionIdToAzureConfigDirectory map[string]string
}

type StepRunOptions struct {
	BaseRunOptions
	PipelineDirectory string
	RetryAttempt      int // 0 for first attempt, >0 for retries
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

type Executor func(id graph.Identifier, s types.Step, ctx context.Context, executionTarget ExecutionTarget, options *StepRunOptions, state *ExecutionState) (Output, DetailsProducer, error)
type DetailsProducer func(ctx context.Context) (*ExecutionDetails, error)

type ExecutionState struct {
	*sync.RWMutex

	Executed sets.Set[graph.Identifier]
	Queued   sets.Set[graph.Identifier]
	Outputs  Outputs

	Timing  map[graph.Identifier]*ExecutionInfo
	Details map[graph.Identifier]*ExecutionDetails
	Logging map[graph.Identifier][]byte
}

type ExecutionInfo struct {
	QueuedAt   string `json:"queuedAt"`
	StartedAt  string `json:"startedAt"`
	FinishedAt string `json:"finishedAt"`
	Preempted  bool   `json:"preempted"`
	RunCount   int    `json:"runCount"`
	State      string `json:"state"`
}
type NodeInfo struct {
	Identifier Identifier        `json:"identifier"`
	Info       ExecutionInfo     `json:"info"`
	Details    *ExecutionDetails `json:"details,omitempty"`
}

type ExecutionDetails struct {
	ARM *ARMExecutionDetails `json:"arm,omitempty"`
}

type ARMExecutionDetails struct {
	Operations []Operation `json:"operations,omitempty"`
}

type Identifier struct {
	ServiceGroup  string `json:"serviceGroup"`
	ResourceGroup string `json:"resourceGroup"`
	Step          string `json:"step"`
}

func (i Identifier) String() string {
	return fmt.Sprintf("%s/%s/%s", i.ServiceGroup, i.ResourceGroup, i.Step)
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
		Details:  make(map[graph.Identifier]*ExecutionDetails),
		Logging:  make(map[graph.Identifier][]byte),
		Outputs:  make(Outputs),
	}

	if options.TimingOutputFile != "" {
		if err := os.MkdirAll(filepath.Dir(options.TimingOutputFile), 0755); err != nil {
			return nil, fmt.Errorf("failed to create timing output dir: %w", err)
		}
		defer func() {
			state.RLock()
			timing := state.Timing
			details := state.Details
			state.RUnlock()
			var times []NodeInfo
			for id, info := range timing {
				times = append(times, NodeInfo{
					Identifier: Identifier{
						ServiceGroup:  id.ServiceGroup,
						ResourceGroup: id.ResourceGroup,
						Step:          id.Step,
					},
					Info:    *info,
					Details: details[id],
				})
			}
			encodedTiming, err := yaml.Marshal(times)
			if err != nil {
				logger.Error(err, "error marshalling timing")
			}

			var outputData []byte
			outputFile := options.TimingOutputFile
			compressed := compressTimingMetadata()

			if compressed {
				// Gzip the encoded data
				var gzipBuffer bytes.Buffer
				gzipWriter := gzip.NewWriter(&gzipBuffer)
				if _, err := gzipWriter.Write(encodedTiming); err != nil {
					logger.Error(err, "Failed to gzip timing metadata")
					return
				}
				if err := gzipWriter.Close(); err != nil {
					logger.Error(err, "Failed to close gzip writer")
					return
				}
				outputData = gzipBuffer.Bytes()
				// Change file extension to .yaml.gz if not already
				if strings.HasSuffix(outputFile, ".yaml") {
					outputFile = outputFile + ".gz"
				}
			} else {
				outputData = encodedTiming
			}

			logger.Info("Writing timing report.", "file", outputFile, "size", len(outputData), "compressed", compressed)
			if err := os.WriteFile(outputFile, outputData, 0644); err != nil {
				logger.Error(err, "error writing timing")
			}
		}()
	}

	if options.JUnitOutputFile != "" {
		if err := os.MkdirAll(filepath.Dir(options.JUnitOutputFile), 0755); err != nil {
			return nil, fmt.Errorf("failed to create jUnit output dir: %w", err)
		}
		suiteStart := time.Now()
		defer func() {
			suiteEnd := time.Now()
			state.RLock()
			timing := state.Timing
			logging := state.Logging
			state.RUnlock()
			suites := &junit.TestSuites{
				Suites: []*junit.TestSuite{
					{
						Name: "step graph",
					},
				},
			}
			suite := suites.Suites[0]
			for id, info := range timing {
				thisLogger := logger.WithValues("id", id)
				startedAt, err := time.Parse(time.RFC3339, info.StartedAt)
				if err != nil {
					thisLogger.Error(err, "error parsing started at")
					continue
				}
				finishedAt, err := time.Parse(time.RFC3339, info.FinishedAt)
				if err != nil {
					thisLogger.Error(err, "error parsing finished at")
					continue
				}

				testCase := &junit.TestCase{Name: fmt.Sprintf("Run pipeline step %s", id.String()), Duration: finishedAt.Sub(startedAt).Seconds()}
				if info.State == "failed" {
					log := string(logging[id])
					if info.Preempted {
						testCase.SkipMessage = &junit.SkipMessage{Message: log}
					} else {
						testCase.FailureOutput = &junit.FailureOutput{Output: string(logging[id])}
					}
				}

				for _, test := range []*junit.TestCase{testCase} {
					switch {
					case test.FailureOutput != nil:
						suite.NumFailed++
					case test.SkipMessage != nil:
						suite.NumSkipped++
					}
					suite.NumTests++
					suite.TestCases = append(suite.TestCases, test)
				}
			}
			suite.Duration = suiteEnd.Sub(suiteStart).Seconds()

			logger.Info("Writing jUnit report.", "file", options.JUnitOutputFile)
			if err := junit.Write(options.JUnitOutputFile, suites); err != nil {
				logger.Error(err, "error writing jUnit")
			}
		}()
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
					originalSink := thisLogger.GetSink()
					stepLogs := bytes.Buffer{}
					stepHandler := logr.FromSlogHandler(slog.NewTextHandler(&stepLogs, &slog.HandlerOptions{}))
					sink := multiSink{sinks: []logr.LogSink{originalSink, stepHandler.GetSink()}}
					stepLogger := thisLogger.WithSink(&sink).WithValues("serviceGroup", step.ServiceGroup, "resourceGroup", step.ResourceGroup, "step", step.Step)
					stepLogger.V(4).Info("Executing step.")
					state.Lock()
					state.Timing[step].StartedAt = time.Now().Format(time.RFC3339)
					state.Unlock()
					stepCtx, stepCtxCancel := context.WithTimeoutCause(consumerCtx, 30*time.Minute, errors.New("exceeded the single-step timeout for sanity"))
					details, runCount, err := executeNode(stepLogger, executor, executionGraph, step, stepCtx, options, state)
					stepCtxCancel()
					if details != nil {
						consumerWg.Add(1)
						go func(step graph.Identifier, logger logr.Logger) {
							defer func() {
								consumerWg.Done()
								stepLogger.V(4).Info("Finished fetching execution details.")
							}()
							stepLogger.V(4).Info("Fetching execution details.")
							d, err := details(consumerCtx)
							if err != nil {
								logger.Error(err, "error fetching execution details")
							}
							state.Lock()
							state.Details[step] = d
							state.Unlock()
						}(step, stepLogger)
					}
					if err != nil {
						stepLogger.V(4).Error(err, "Step errored.")
					} else {
						stepLogger.V(4).Info("Finished step.")
					}
					state.Lock()
					state.Logging[step] = stepLogs.Bytes()
					state.Timing[step].FinishedAt = time.Now().Format(time.RFC3339)
					state.Timing[step].RunCount = runCount
					if consumerCtx.Err() != nil {
						state.Timing[step].Preempted = true
					}
					s := "succeeded"
					if err != nil {
						s = "failed"
					}
					state.Timing[step].State = s
					state.Unlock()
					if err != nil {
						errs <- err
						consumerCancel()
						return
					}
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
	state.RUnlock()

	return outputs, nil
}

func executeNode(logger logr.Logger, executor Executor, graphCtx *graph.Graph, node graph.Identifier, ctx context.Context, options *PipelineRunOptions, state *ExecutionState) (DetailsProducer, int, error) {
	state.RLock()
	alreadyDone := state.Executed.Has(node)
	state.RUnlock()
	if alreadyDone {
		logger.V(4).Info("Skipping execution, as it has already happened.")
		// our graph may converge, where many children need one parent - no need to re-execute then
		return nil, 0, nil
	}

	resourceGroup, exists := graphCtx.ResourceGroups[node.ResourceGroup]
	if !exists {
		return nil, 0, fmt.Errorf("could not find resource group %s", node.ResourceGroup)
	}

	step, exists := graphCtx.Steps[node.ServiceGroup][node.ResourceGroup][node.Step]
	if !exists {
		return nil, 0, fmt.Errorf("could not find step %s/%s/%s", node.ServiceGroup, node.ResourceGroup, node.Step)
	}

	subscriptionID, err := options.SubsciptionLookupFunc(ctx, resourceGroup.Subscription)
	if err != nil {
		return nil, 0, fmt.Errorf("failed to lookup subscription ID for %q: %w", resourceGroup.Subscription, err)
	}
	target := &executionTargetImpl{
		subscriptionName: resourceGroup.Subscription,
		subscriptionID:   subscriptionID,
		region:           options.Region,
		resourceGroup:    resourceGroup.ResourceGroup,
	}

	var output Output
	var details DetailsProducer
	var stepRunErr error
	var runCount = 0
	if options.Step != "" && step.StepName() != options.Step {
		// skip steps that don't match the specified step name
		output = nil
		stepRunErr = nil
	} else {
		for shouldExecuteStep(step, runCount) {
			output, details, stepRunErr = executor(node, step, logr.NewContext(ctx, logger), target, &StepRunOptions{
				BaseRunOptions:    options.BaseRunOptions,
				PipelineDirectory: filepath.Join(options.TopologyDir, filepath.Dir(graphCtx.Services[node.ServiceGroup].PipelinePath)),
				RetryAttempt:      runCount,
			}, state)
			runCount++
			if shouldRetryError(logger, step, stepRunErr) {
				duration, err := time.ParseDuration(step.AutomatedRetries().DurationBetweenRetries)
				if err != nil {
					return nil, 0, fmt.Errorf("failed to parse duration between retries: %w", err)
				}
				time.Sleep(duration)
			} else {
				break
			}
		}
	}
	if stepRunErr != nil {
		return details, runCount, stepRunErr
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
	return details, runCount, nil
}

func shouldExecuteStep(step types.Step, runCount int) bool {
	// Default, no retries, execute the step
	if step.AutomatedRetries() == nil && runCount == 0 {
		return true
	}
	return runCount < step.AutomatedRetries().MaximumRetryCount
}

func shouldRetryError(logger logr.Logger, step types.Step, err error) bool {
	if step.AutomatedRetries() == nil || err == nil {
		return false
	}
	for _, retry := range step.AutomatedRetries().ErrorContainsAny {
		if strings.Contains(err.Error(), retry) {
			logger.Info("Retrying step", "step", step.StepName(), "error", err.Error())
			return true
		}
	}
	return false
}

func RunStep(id graph.Identifier, s types.Step, ctx context.Context, executionTarget ExecutionTarget, options *StepRunOptions, state *ExecutionState) (Output, DetailsProducer, error) {
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
			return nil, nil, nil
		}

		err := runImageMirrorStep(id, ctx, step, options, state, &buf)
		if err != nil {
			return nil, nil, fmt.Errorf("error running Image Mirror Step, %v", err)
		}
		output := buf.String()
		fmt.Println(output)
		return ShellOutput(output), nil, nil
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
			return nil, nil, fmt.Errorf("failed to prepare kubeconfig: %w", err)
		}

		azureConfigDir, exists := options.SubscriptionIdToAzureConfigDirectory[executionTarget.GetSubscriptionID()]
		if !exists {
			return nil, nil, fmt.Errorf("azure client not configured for subscription: %s", executionTarget.GetSubscriptionID())
		}

		err = runShellStep(id, step, ctx, azureConfigDir, kubeconfigFile, options, state, &buf)
		if err != nil {
			return nil, nil, fmt.Errorf("error running Shell Step, %v", err)
		}
		output := buf.String()
		fmt.Println(output)
		return ShellOutput(output), nil, nil
	case *types.SecretSyncStep:
		if err := runSecretSyncStep(step, ctx, options); err != nil {
			return nil, nil, fmt.Errorf("error running secret sync Step, %v", err)
		}
		return nil, nil, nil
	case *types.ProviderFeatureRegistrationStep:
		if err := runRegistrationStep(step, ctx, options, executionTarget); err != nil {
			return nil, nil, fmt.Errorf("error running provider and feature registration Step, %v", err)
		}
		return nil, nil, nil
	case *types.HelmStep:
		if err := runHelmStep(id, step, ctx, options, executionTarget, state); err != nil {
			return nil, nil, fmt.Errorf("error running Helm release deployment Step, %v", err)
		}
		return nil, nil, nil
	case *types.ARMStep:
		a, err := newArmClient(executionTarget.GetSubscriptionID(), executionTarget.GetRegion(), options.BicepClient)
		if err != nil {
			return nil, nil, fmt.Errorf("failed to create ARM clients: %w", err)
		}
		output, details, err := a.runArmStep(ctx, options, executionTarget.GetResourceGroup(), id, step, state)
		if err != nil {
			return nil, nil, fmt.Errorf("failed to run ARM step: %w", err)
		}
		return output, details, nil
	case *types.ARMStackStep:
		output, details, err := runArmStackStep(ctx, options, executionTarget, id, step, state)
		if err != nil {
			return nil, nil, fmt.Errorf("failed to run ARM step: %w", err)
		}
		return output, details, nil
	default:
		logger.Info("No implementation for action type - skip", "actionType", s.ActionType())
		return nil, nil, nil
	}
}

func getInputValues(serviceGroup string, configuredVariables []types.Variable, cfg configtypes.Configuration, inputs Outputs) (map[string]any, error) {
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

// multiSink implements logr.Sink and sends logs to multiple sinks
type multiSink struct {
	sinks []logr.LogSink
}

var _ logr.LogSink = (*multiSink)(nil)

// Enabled checks if logging is enabled for any sink
func (m *multiSink) Enabled(level int) bool {
	for _, s := range m.sinks {
		if s.Enabled(level) {
			return true
		}
	}
	return false
}

// Info logs an info message to all sinks
func (m *multiSink) Info(level int, msg string, keysAndValues ...interface{}) {
	for _, s := range m.sinks {
		s.Info(level, msg, keysAndValues...)
	}
}

// Error logs an error message to all sinks
func (m *multiSink) Error(err error, msg string, keysAndValues ...interface{}) {
	for _, s := range m.sinks {
		s.Error(err, msg, keysAndValues...)
	}
}

// Init initializes all sinks
func (m *multiSink) Init(info logr.RuntimeInfo) {
	for _, s := range m.sinks {
		s.Init(info)
	}
}

// WithValues adds key-value pairs to all sinks
func (m *multiSink) WithValues(keysAndValues ...interface{}) logr.LogSink {
	newSinks := make([]logr.LogSink, len(m.sinks))
	for i, s := range m.sinks {
		newSinks[i] = s.WithValues(keysAndValues...)
	}
	return &multiSink{sinks: newSinks}
}

// WithName adds a name to all sinks
func (m *multiSink) WithName(name string) logr.LogSink {
	newSinks := make([]logr.LogSink, len(m.sinks))
	for i, s := range m.sinks {
		newSinks[i] = s.WithName(name)
	}
	return &multiSink{sinks: newSinks}
}

func getAllSubscriptions(pipelines map[string]*types.Pipeline) []string {
	subscriptions := make(map[string]bool, 0)
	for _, pipeline := range pipelines {
		for _, resourceGroup := range pipeline.ResourceGroups {
			subscriptions[resourceGroup.Subscription] = true
		}
	}
	allSubs := make([]string, 0)
	for k := range subscriptions {
		allSubs = append(allSubs, k)
	}
	return allSubs
}

func GetAllRequiredAzureClients(ctx context.Context, pipelines map[string]*types.Pipeline, subscriptions map[string]string) (map[string]string, error) {
	subscriptionIdToAzureConfigDirectory := make(map[string]string)
	for _, subscription := range getAllSubscriptions(pipelines) {
		subscriptionID, err := LookupSubscriptionID(subscriptions)(ctx, subscription)
		if err != nil {
			return nil, fmt.Errorf("failed to lookup subscription ID for %q: %w", subscription, err)
		}
		azureConfigDir, err := configureAzureCLILogin(ctx, subscriptionID)
		if err != nil {
			return nil, fmt.Errorf("failed to setup Azure CLI config directory: %w", err)
		}
		subscriptionIdToAzureConfigDirectory[subscriptionID] = azureConfigDir
	}
	return subscriptionIdToAzureConfigDirectory, nil
}
