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

package main

import (
	"context"
	"fmt"
	"maps"
	"strings"
	"time"

	"github.com/go-logr/logr"
	"github.com/spf13/cobra"

	k8svalidation "k8s.io/apimachinery/pkg/util/validation"

	prowgangway "sigs.k8s.io/prow/pkg/gangway"

	"github.com/Azure/ARO-HCP/test/pkg/prowjob"
)

const (
	// Default URLs for Prow API endpoints
	defaultGangwayURL = "https://gangway-ci.apps.ci.l2s4.p1.openshiftapps.com/v1/executions"
	defaultProwURL    = "https://prow.ci.openshift.org/prowjob"

	// EV2 rollout annotation prefix
	ev2RolloutPrefix           = "ev2.rollout/"
	ev2RolloutRegionAnnotation = ev2RolloutPrefix + "region"
)

//
// Execute command types and functions
//

func DefaultExecuteOptions() *RawExecuteOptions {
	return &RawExecuteOptions{
		RawProwTokenOptions: NewDefaultRawProwTokenOptions(),
		Labels:              make(map[string]string),
		Annotations:         make(map[string]string),
		EnvironmentVars:     make(map[string]string),
		PollInterval:        300 * time.Second,
		Timeout:             3 * time.Hour,
		GangwayURL:          defaultGangwayURL,
		ProwURL:             defaultProwURL,
	}
}

func (o *RawExecuteOptions) BindFlags(cmd *cobra.Command) error {
	cmd.Flags().StringVar(&o.Region, "region", o.Region, "Target Azure region for the job execution")
	cmd.Flags().StringVar(&o.ProwJobName, "job-name", o.ProwJobName, "Name of the specific ProwJob to execute")
	cmd.Flags().StringToStringVar(&o.Labels, "label", o.Labels, "Kubernetes labels to apply to the job pod in k=v format (can be specified multiple times)")
	cmd.Flags().StringToStringVar(&o.Annotations, "annotation", o.Annotations, "Kubernetes annotations to apply to the job pod in k=v format (can be specified multiple times)")
	cmd.Flags().StringToStringVar(&o.EnvironmentVars, "environment-variable", o.EnvironmentVars, "Environment variables to pass to the job in k=v format (can be specified multiple times)")
	cmd.Flags().StringVar(&o.EV2RolloutVersion, "ev2-rollout-version", o.EV2RolloutVersion, fmt.Sprintf("EV2 rollout version (format: tag.value.tag.value...) - will be provided as %stag=value annotations to the job", ev2RolloutPrefix))
	cmd.Flags().DurationVar(&o.PollInterval, "poll-interval", o.PollInterval, "Status polling interval")
	cmd.Flags().DurationVar(&o.Timeout, "timeout", o.Timeout, "Maximum wait time for job completion")
	cmd.Flags().StringVar(&o.GangwayURL, "gangway-url", o.GangwayURL, "Gangway API URL for job execution")
	cmd.Flags().StringVar(&o.ProwURL, "prow-url", o.ProwURL, "Prow API URL for job status monitoring")
	cmd.Flags().BoolVar(&o.DryRun, "dry-run", o.DryRun, "Print which job would be started, but do not start one.")
	cmd.Flags().BoolVar(&o.GatePromotion, "gate-promotion", o.GatePromotion, "Exit with an error code if the job fails.")

	// Mark required flags
	for _, flag := range []string{
		"region",
		"job-name",
	} {
		if err := cmd.MarkFlagRequired(flag); err != nil {
			return fmt.Errorf("failed to mark flag %q as required: %w", flag, err)
		}
	}

	return o.RawProwTokenOptions.BindFlags(cmd)
}

// RawExecuteOptions holds input values from CLI/env
type RawExecuteOptions struct {
	*RawProwTokenOptions

	Region            string
	ProwJobName       string
	Labels            map[string]string
	Annotations       map[string]string
	EnvironmentVars   map[string]string
	EV2RolloutVersion string
	PollInterval      time.Duration
	Timeout           time.Duration
	GangwayURL        string
	ProwURL           string
	DryRun            bool
	GatePromotion     bool
}

// validatedExecuteOptions is a private wrapper that enforces a call of Validate() before Complete() can be invoked.
type validatedExecuteOptions struct {
	*RawExecuteOptions
	*ValidatedProwTokenOptions
	ParsedLabels          map[string]string
	ParsedAnnotations     map[string]string
	ParsedEnvironmentVars map[string]string
}

type ValidatedExecuteOptions struct {
	// Embed a private pointer that cannot be instantiated outside of this package.
	*validatedExecuteOptions
}

// completedExecuteOptions is a private wrapper that enforces a call of Complete() before execution can be invoked.
type completedExecuteOptions struct {
	Region          string
	ProwJobName     string
	Labels          map[string]string
	Annotations     map[string]string
	EnvironmentVars map[string]string
	PollInterval    time.Duration
	Timeout         time.Duration
	ProwToken       string
	GangwayURL      string
	ProwURL         string
	DryRun          bool
	GatePromotion   bool
}

type ExecuteOptions struct {
	// Embed a private pointer that cannot be instantiated outside of this package.
	*completedExecuteOptions
}

func (o *RawExecuteOptions) Validate(ctx context.Context) (*ValidatedExecuteOptions, error) {
	validated, err := o.RawProwTokenOptions.Validate(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to validate prow token options: %w", err)
	}

	for _, item := range []struct {
		flag  string
		name  string
		value *string
	}{
		{flag: "region", name: "region", value: &o.Region},
		{flag: "job-name", name: "Prow job name", value: &o.ProwJobName},
	} {
		if item.value == nil || *item.value == "" {
			return nil, fmt.Errorf("the %s must be provided with --%s", item.name, item.flag)
		}
	}

	// Validate labels
	for key, value := range o.Labels {
		if err := validateKubernetesLabel(key, value); err != nil {
			return nil, fmt.Errorf("invalid label %s=%s: %w", key, value, err)
		}
	}

	// Start with user-provided annotations
	annotations := make(map[string]string)
	maps.Copy(annotations, o.Annotations)

	// Add region as annotation
	annotations[ev2RolloutRegionAnnotation] = o.Region

	// Parse EV2 rollout version
	ev2Annotations, err := parseEV2RolloutVersionAsAnnotations(o.EV2RolloutVersion)
	if err != nil {
		return nil, fmt.Errorf("failed to parse EV2 rollout version: %w", err)
	}

	// Combine EV2 annotations with user-provided annotations (user annotations take precedence)
	allAnnotations := make(map[string]string)
	maps.Copy(allAnnotations, ev2Annotations)
	maps.Copy(allAnnotations, annotations)

	// Validate the complete annotations map using official Kubernetes validation
	if err := validateAnnotationsMap(allAnnotations); err != nil {
		return nil, fmt.Errorf("annotations validation failed: %w", err)
	}

	// Validate environment variables
	for key := range o.EnvironmentVars {
		if err := validateEnvVarKey(key); err != nil {
			return nil, fmt.Errorf("invalid environment variable key %q: %w", key, err)
		}
	}

	if o.PollInterval <= 0 {
		return nil, fmt.Errorf("poll-interval must be greater than 0")
	}

	if o.Timeout <= 0 {
		return nil, fmt.Errorf("timeout must be greater than 0")
	}

	return &ValidatedExecuteOptions{
		validatedExecuteOptions: &validatedExecuteOptions{
			RawExecuteOptions:         o,
			ParsedLabels:              o.Labels,
			ParsedAnnotations:         allAnnotations,
			ParsedEnvironmentVars:     o.EnvironmentVars,
			ValidatedProwTokenOptions: validated,
		},
	}, nil
}

func (o *ValidatedExecuteOptions) Complete(ctx context.Context) (*ExecuteOptions, error) {
	completed, err := o.ValidatedProwTokenOptions.Complete(ctx)
	if err != nil {
		return nil, err
	}

	return &ExecuteOptions{
		completedExecuteOptions: &completedExecuteOptions{
			Region:          o.Region,
			ProwJobName:     o.ProwJobName,
			Labels:          o.ParsedLabels,
			Annotations:     o.ParsedAnnotations,
			EnvironmentVars: o.ParsedEnvironmentVars,
			PollInterval:    o.PollInterval,
			Timeout:         o.Timeout,
			ProwToken:       completed.ProwToken,
			GangwayURL:      o.GangwayURL,
			ProwURL:         o.ProwURL,
			DryRun:          o.DryRun,
			GatePromotion:   o.GatePromotion,
		},
	}, nil
}

func (o *ExecuteOptions) Execute(ctx context.Context) error {
	logger, err := logr.FromContext(ctx)
	if err != nil {
		return err
	}

	// Create Prow client
	client := prowjob.NewClient(o.ProwToken, o.GangwayURL, o.ProwURL)

	// Create job monitor
	monitor := prowjob.NewMonitor(client, o.PollInterval, o.Timeout, o.DryRun, o.GatePromotion)

	// Prepare environment variables, including the region
	envs := make(map[string]string)
	maps.Copy(envs, o.EnvironmentVars)
	envs["MULTISTAGE_PARAM_OVERRIDE_LOCATION"] = o.Region

	return monitor.ExecuteAndWait(ctx, logger, &prowgangway.CreateJobExecutionRequest{
		JobName:          o.ProwJobName,
		JobExecutionType: prowgangway.JobExecutionType_PERIODIC, // hardcode periodic for now
		PodSpecOptions: &prowgangway.PodSpecOptions{
			Envs:        envs,
			Labels:      o.Labels,
			Annotations: o.Annotations,
		},
	})
}

// parseEV2RolloutVersionAsAnnotations parses a flexible tag.value.tag.value format and returns annotations
// Expected format: tag1.value1.tag2.value2.tag3.value3, e.g. build.NUMBER.sdp-pipelines.COMMIT.ARO-HCP.COMMIT
// Converts to annotations with "ev2.rollout/" prefix
func parseEV2RolloutVersionAsAnnotations(version string) (map[string]string, error) {
	if version == "" {
		return map[string]string{}, nil
	}

	// Split by dots
	parts := strings.Split(version, ".")

	// Must have even number of parts (tag.value pairs)
	if len(parts)%2 != 0 {
		return nil, fmt.Errorf("invalid rollout version format: %s (expected: tag.value.tag.value...)", version)
	}

	if len(parts) == 0 {
		return map[string]string{}, nil
	}

	annotations := make(map[string]string)
	for i := 0; i < len(parts); i += 2 {
		tag := parts[i]
		value := parts[i+1]

		// Convert to annotation key with prefix
		annotationKey := ev2RolloutPrefix + tag

		// Validate the annotation key format using IsQualifiedName
		if errs := k8svalidation.IsQualifiedName(annotationKey); len(errs) > 0 {
			return nil, fmt.Errorf("invalid EV2 rollout annotation key %q in %q: %s", annotationKey, version, strings.Join(errs, "; "))
		}

		annotations[annotationKey] = value
	}

	return annotations, nil
}

//
// Monitor command types and functions
//

func DefaultMonitorOptions() *RawMonitorOptions {
	return &RawMonitorOptions{
		RawProwTokenOptions: NewDefaultRawProwTokenOptions(),
		PollInterval:        300 * time.Second,
		Timeout:             3 * time.Hour,
		GangwayURL:          defaultGangwayURL,
		ProwURL:             defaultProwURL,
	}
}

func (o *RawMonitorOptions) BindFlags(cmd *cobra.Command) error {
	cmd.Flags().StringVar(&o.JobExecutionID, "execution-id", o.JobExecutionID, "Prow job execution ID to monitor")
	cmd.Flags().DurationVar(&o.PollInterval, "poll-interval", o.PollInterval, "Status polling interval")
	cmd.Flags().DurationVar(&o.Timeout, "timeout", o.Timeout, "Maximum wait time for job completion")
	cmd.Flags().StringVar(&o.GangwayURL, "gangway-url", o.GangwayURL, "Gangway API URL for job execution")
	cmd.Flags().StringVar(&o.ProwURL, "prow-url", o.ProwURL, "PROW API URL for job status monitoring")

	// Mark required flags
	for _, flag := range []string{
		"execution-id",
	} {
		if err := cmd.MarkFlagRequired(flag); err != nil {
			return fmt.Errorf("failed to mark flag %q as required: %w", flag, err)
		}
	}

	return o.RawProwTokenOptions.BindFlags(cmd)
}

// RawMonitorOptions holds input values from CLI/env
type RawMonitorOptions struct {
	*RawProwTokenOptions

	JobExecutionID string
	PollInterval   time.Duration
	Timeout        time.Duration
	GangwayURL     string
	ProwURL        string
}

// validatedMonitorOptions is a private wrapper that enforces a call of Validate() before Complete() can be invoked.
type validatedMonitorOptions struct {
	*RawMonitorOptions
	*ValidatedProwTokenOptions
}

type ValidatedMonitorOptions struct {
	// Embed a private pointer that cannot be instantiated outside of this package.
	*validatedMonitorOptions
}

// completedMonitorOptions is a private wrapper that enforces a call of Complete() before execution can be invoked.
type completedMonitorOptions struct {
	JobExecutionID string
	PollInterval   time.Duration
	Timeout        time.Duration
	ProwToken      string
	GangwayURL     string
	ProwURL        string
}

type MonitorOptions struct {
	// Embed a private pointer that cannot be instantiated outside of this package.
	*completedMonitorOptions
}

func (o *RawMonitorOptions) Validate(ctx context.Context) (*ValidatedMonitorOptions, error) {
	validated, err := o.RawProwTokenOptions.Validate(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to validate prow token options: %w", err)
	}

	for _, item := range []struct {
		flag  string
		name  string
		value *string
	}{
		{flag: "execution-id", name: "job execution ID", value: &o.JobExecutionID},
	} {
		if item.value == nil || *item.value == "" {
			return nil, fmt.Errorf("the %s must be provided with --%s", item.name, item.flag)
		}
	}

	// Validate execution ID is a valid UUID
	if err := validateUUID(o.JobExecutionID); err != nil {
		return nil, fmt.Errorf("invalid execution ID format: %w", err)
	}

	if o.PollInterval <= 0 {
		return nil, fmt.Errorf("poll-interval must be greater than 0")
	}

	if o.Timeout <= 0 {
		return nil, fmt.Errorf("timeout must be greater than 0")
	}

	return &ValidatedMonitorOptions{
		validatedMonitorOptions: &validatedMonitorOptions{
			RawMonitorOptions:         o,
			ValidatedProwTokenOptions: validated,
		},
	}, nil
}

func (o *ValidatedMonitorOptions) Complete(ctx context.Context) (*MonitorOptions, error) {
	completed, err := o.ValidatedProwTokenOptions.Complete(ctx)
	if err != nil {
		return nil, err
	}

	return &MonitorOptions{
		completedMonitorOptions: &completedMonitorOptions{
			JobExecutionID: o.JobExecutionID,
			PollInterval:   o.PollInterval,
			Timeout:        o.Timeout,
			ProwToken:      completed.ProwToken,
			GangwayURL:     o.GangwayURL,
			ProwURL:        o.ProwURL,
		},
	}, nil
}

func (o *MonitorOptions) Monitor(ctx context.Context, logger logr.Logger) error {
	// Create Prow client and monitor
	client := prowjob.NewClient(o.ProwToken, o.GangwayURL, o.ProwURL)
	monitor := prowjob.NewMonitor(client, o.PollInterval, o.Timeout, false, false)

	// Monitor existing job using shared polling logic
	logger.Info("Starting to monitor existing job", "jobExecutionID", o.JobExecutionID)

	return monitor.WaitForCompletion(ctx, logger, o.JobExecutionID)
}
