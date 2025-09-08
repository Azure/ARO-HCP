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

	"github.com/google/uuid"
	"github.com/spf13/cobra"
	"k8s.io/apimachinery/pkg/api/validation"
	k8svalidation "k8s.io/apimachinery/pkg/util/validation"
	"k8s.io/apimachinery/pkg/util/validation/field"
	prowgangway "sigs.k8s.io/prow/pkg/gangway"

	"github.com/Azure/ARO-HCP/test/pkg/prowjob"
	"github.com/Azure/ARO-HCP/test/util/log"
)

const (
	// Default URLs for Prow API endpoints
	defaultGangwayURL = "https://gangway-ci.apps.ci.l2s4.p1.openshiftapps.com/v1/executions"
	defaultProwURL    = "https://prow.ci.openshift.org/prowjob"

	// EV2 rollout annotation prefix
	ev2RolloutPrefix = "ev2.rollout/"
)

// validateKubernetesLabel validates a Kubernetes label key-value pair using official Kubernetes validation
func validateKubernetesLabel(key, value string) error {
	// Validate label key using IsQualifiedName - this is the correct function for Kubernetes label keys
	if errs := k8svalidation.IsQualifiedName(key); len(errs) > 0 {
		return fmt.Errorf("label key %q is invalid: %s", key, strings.Join(errs, "; "))
	}

	// Validate value using official Kubernetes validation
	if errs := k8svalidation.IsValidLabelValue(value); len(errs) > 0 {
		return fmt.Errorf("label value %q is invalid: %s", value, strings.Join(errs, "; "))
	}

	return nil
}

// validateAnnotationsMap validates a complete set of annotations using official Kubernetes validation
func validateAnnotationsMap(annotations map[string]string) error {
	// Use official Kubernetes validation that checks both individual annotation format and total size
	if errs := validation.ValidateAnnotations(annotations, field.NewPath("annotations")); len(errs) > 0 {
		var errMessages []string
		for _, err := range errs {
			errMessages = append(errMessages, err.Error())
		}
		return fmt.Errorf("annotations validation failed: %s", strings.Join(errMessages, "; "))
	}

	return nil
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

func DefaultExecuteOptions() *RawExecuteOptions {
	return &RawExecuteOptions{
		RawProwTokenOptions: NewDefaultRawProwTokenOptions(),
		PollInterval:        60 * time.Second,
		Timeout:             2 * time.Hour,
		GangwayURL:          defaultGangwayURL,
		ProwURL:             defaultProwURL,
	}
}

func (o *RawExecuteOptions) BindFlags(cmd *cobra.Command) error {
	cmd.Flags().StringVar(&o.Region, "region", o.Region, "Target Azure region for the job execution")
	cmd.Flags().StringVar(&o.ProwJobName, "job-name", o.ProwJobName, "Name of the specific ProwJob to execute")
	cmd.Flags().StringArrayVar(&o.Labels, "label", o.Labels, "Kubernetes labels to apply to the job pod in k=v format (can be specified multiple times)")
	cmd.Flags().StringArrayVar(&o.Annotations, "annotation", o.Annotations, "Kubernetes annotations to apply to the job pod in k=v format (can be specified multiple times)")
	cmd.Flags().StringArrayVar(&o.EnvironmentVars, "environment-variable", o.EnvironmentVars, "Environment variables to pass to the job in k=v format (can be specified multiple times)")
	cmd.Flags().StringVar(&o.EV2RolloutVersion, "ev2-rollout-version", o.EV2RolloutVersion, fmt.Sprintf("EV2 rollout version (format: tag.value.tag.value...) - will be provided as %stag=value annotations to the job", ev2RolloutPrefix))
	cmd.Flags().DurationVar(&o.PollInterval, "poll-interval", o.PollInterval, "Status polling interval")
	cmd.Flags().DurationVar(&o.Timeout, "timeout", o.Timeout, "Maximum wait time for job completion")
	cmd.Flags().StringVar(&o.GangwayURL, "gangway-url", o.GangwayURL, "Gangway API URL for job execution")
	cmd.Flags().StringVar(&o.ProwURL, "prow-url", o.ProwURL, "Prow API URL for job status monitoring")

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
	Labels            []string
	Annotations       []string
	EnvironmentVars   []string
	EV2RolloutVersion string
	PollInterval      time.Duration
	Timeout           time.Duration
	GangwayURL        string
	ProwURL           string
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

	// Parse and validate labels
	labels, err := parseLabels(o.Labels)
	if err != nil {
		return nil, fmt.Errorf("failed to parse labels: %w", err)
	}

	// Parse and validate annotations
	annotations, err := parseAnnotations(o.Annotations)
	if err != nil {
		return nil, fmt.Errorf("failed to parse annotations: %w", err)
	}

	// Add region as annotation
	annotations[ev2RolloutPrefix+"region"] = o.Region

	// Parse EV2 rollout version and add to annotations
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

	// Parse and validate environment variables
	envVars, err := parseEnvironmentVars(o.EnvironmentVars)
	if err != nil {
		return nil, fmt.Errorf("failed to parse environment variables: %w", err)
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
			ParsedLabels:              labels,
			ParsedAnnotations:         allAnnotations,
			ParsedEnvironmentVars:     envVars,
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
		},
	}, nil
}

func (o *ExecuteOptions) Execute(ctx context.Context) error {
	// Create Prow client
	client := prowjob.NewClient(o.ProwToken, o.GangwayURL, o.ProwURL)

	// Create job monitor
	monitor := prowjob.NewMonitor(client, o.PollInterval, o.Timeout)

	// Prepare environment variables, including the region
	envs := make(map[string]string)
	maps.Copy(envs, o.EnvironmentVars)
	envs["MULTISTAGE_PARAM_OVERRIDE_LOCATION"] = o.Region

	return monitor.ExecuteAndWait(ctx, &prowgangway.CreateJobExecutionRequest{
		JobName:          o.ProwJobName,
		JobExecutionType: prowgangway.JobExecutionType_PERIODIC, // hardcode periodic for now
		PodSpecOptions: &prowgangway.PodSpecOptions{
			Envs:        envs,
			Labels:      o.Labels,
			Annotations: o.Annotations,
		},
	})
}

// parseLabels converts slice of "k=v" strings to map[string]string with Kubernetes label validation
func parseLabels(labels []string) (map[string]string, error) {
	result := make(map[string]string)
	for _, label := range labels {
		parts := strings.SplitN(label, "=", 2)
		if len(parts) != 2 {
			return nil, fmt.Errorf("invalid label format %q, expected k=v", label)
		}
		key := strings.TrimSpace(parts[0])
		value := strings.TrimSpace(parts[1])

		if err := validateKubernetesLabel(key, value); err != nil {
			return nil, fmt.Errorf("invalid label %q: %w", label, err)
		}

		result[key] = value
	}
	return result, nil
}

// parseAnnotations converts slice of "k=v" strings to map[string]string (validation happens later on complete map)
func parseAnnotations(annotations []string) (map[string]string, error) {
	result := make(map[string]string)
	for _, annotation := range annotations {
		parts := strings.SplitN(annotation, "=", 2)
		if len(parts) != 2 {
			return nil, fmt.Errorf("invalid annotation format %q, expected k=v", annotation)
		}
		key := strings.TrimSpace(parts[0])
		value := strings.TrimSpace(parts[1])

		if key == "" {
			return nil, fmt.Errorf("annotation key cannot be empty in %q", annotation)
		}

		result[key] = value
	}
	return result, nil
}

// parseEnvironmentVars converts slice of "k=v" strings to map[string]string for environment variables
func parseEnvironmentVars(envVars []string) (map[string]string, error) {
	result := make(map[string]string)
	for _, envVar := range envVars {
		parts := strings.SplitN(envVar, "=", 2)
		if len(parts) != 2 {
			return nil, fmt.Errorf("invalid environment variable format %q, expected k=v", envVar)
		}
		key := strings.TrimSpace(parts[0])
		value := strings.TrimSpace(parts[1])

		if key == "" {
			return nil, fmt.Errorf("environment variable key cannot be empty in %q", envVar)
		}

		result[key] = value
	}
	return result, nil
}

// validateUUID validates that the given string is a valid UUID
func validateUUID(id string) error {
	if _, err := uuid.Parse(id); err != nil {
		return fmt.Errorf("execution ID must be a valid UUID format: %w", err)
	}
	return nil
}

// Monitor command types and functions

func DefaultMonitorOptions() *RawMonitorOptions {
	return &RawMonitorOptions{
		RawProwTokenOptions: NewDefaultRawProwTokenOptions(),
		PollInterval:        60 * time.Second,
		Timeout:             2 * time.Hour,
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

func (o *MonitorOptions) Monitor(ctx context.Context) error {
	// Create Prow client and monitor
	client := prowjob.NewClient(o.ProwToken, o.GangwayURL, o.ProwURL)
	monitor := prowjob.NewMonitor(client, o.PollInterval, o.Timeout)

	// Monitor existing job using shared polling logic
	logger := log.GetLogger()
	logger.WithField("jobExecutionID", o.JobExecutionID).Info("Starting to monitor existing job")

	return monitor.WaitForCompletion(ctx, o.JobExecutionID)
}
