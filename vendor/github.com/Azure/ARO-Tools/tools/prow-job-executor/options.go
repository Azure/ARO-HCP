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

package prowjobexecutor

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

	"github.com/Azure/ARO-Tools/tools/prow-job-executor/prowjob"
)

const (
	// Default URLs for Prow API endpoints
	defaultGangwayURL = "https://gangway-ci.apps.ci.l2s4.p1.openshiftapps.com/v1/executions"
	defaultProwURL    = "https://prow.ci.openshift.org/prowjob"

	// EV2 rollout annotation prefix
	ev2RolloutPrefix                = "ev2.rollout/"
	ev2RolloutRegionAnnotation      = ev2RolloutPrefix + "region"
	ev2RolloutCloudAnnotation       = ev2RolloutPrefix + "cloud"
	ev2RolloutEnvironmentAnnotation = ev2RolloutPrefix + "environment"

	// Default git ref values for postsubmit execution
	defaultBaseRef = "main"
	defaultOrg     = "Azure"
	defaultRepo    = "ARO-HCP"
)

//
// Execute command types and functions
//

func DefaultExecuteOptions() *RawExecuteOptions {
	return &RawExecuteOptions{
		RawProwTokenOptions:     NewDefaultRawProwTokenOptions(),
		Labels:                  make(map[string]string),
		Annotations:             make(map[string]string),
		EnvironmentVars:         make(map[string]string),
		PollInterval:            300 * time.Second,
		Timeout:                 4 * time.Hour,
		NotFoundGracePeriod:     prowjob.DefaultNotFoundGracePeriod,
		GangwayURL:              defaultGangwayURL,
		ProwURL:                 defaultProwURL,
		BaseRef:                 defaultBaseRef,
		Org:                     defaultOrg,
		Repo:                    defaultRepo,
		MaxEV2AutoRetryFailures: prowjob.DefaultMaxEV2AutoRetryFailures,
	}
}

func (o *RawExecuteOptions) BindFlags(cmd *cobra.Command) error {
	cmd.Flags().StringVar(&o.Cloud, "cloud", o.Cloud, "Target Azure cloud for the job execution")
	cmd.Flags().StringVar(&o.Environment, "environment", o.Environment, "Target environment for the job execution")
	cmd.Flags().StringVar(&o.Region, "region", o.Region, "Target Azure region for the job execution")
	cmd.Flags().StringVar(&o.AllowedSubscriptions, "allowed-subscriptions", o.AllowedSubscriptions, "Optional slot-manager subscription allowlist override for the job. When empty, the job's baseline ALLOWED_SUBSCRIPTIONS is used.")
	cmd.Flags().StringVar(&o.ProwJobName, "job-name", o.ProwJobName, "Name of the specific ProwJob to execute")
	cmd.Flags().StringToStringVar(&o.Labels, "label", o.Labels, "Kubernetes labels to apply to the job pod in k=v format (can be specified multiple times)")
	cmd.Flags().StringToStringVar(&o.Annotations, "annotation", o.Annotations, "Kubernetes annotations to apply to the job pod in k=v format (can be specified multiple times)")
	cmd.Flags().StringToStringVar(&o.EnvironmentVars, "environment-variable", o.EnvironmentVars, "Environment variables to pass to the job in k=v format (can be specified multiple times)")
	cmd.Flags().StringVar(&o.EV2RolloutVersion, "ev2-rollout-version", o.EV2RolloutVersion, fmt.Sprintf("EV2 rollout version (format: tag.value.tag.value...) - will be provided as %stag=value annotations to the job", ev2RolloutPrefix))
	cmd.Flags().DurationVar(&o.PollInterval, "poll-interval", o.PollInterval, "Status polling interval")
	cmd.Flags().DurationVar(&o.Timeout, "timeout", o.Timeout, "Maximum wait time for job completion")
	cmd.Flags().DurationVar(&o.NotFoundGracePeriod, "not-found-grace-period", o.NotFoundGracePeriod, "How long a consecutive run of 404s from the job status endpoint is tolerated (e.g. a short Prow/OCP maintenance window) before giving up on the job")
	cmd.Flags().StringVar(&o.GangwayURL, "gangway-url", o.GangwayURL, "Gangway API URL for job execution")
	cmd.Flags().StringVar(&o.ProwURL, "prow-url", o.ProwURL, "Prow API URL for job status monitoring")
	cmd.Flags().BoolVar(&o.DryRun, "dry-run", o.DryRun, "Print which job would be started, but do not start one.")
	cmd.Flags().BoolVar(&o.GatePromotion, "gate-promotion", o.GatePromotion, "Exit with an error code if the job fails.")
	cmd.Flags().BoolVar(&o.AllowEV2Retry, "allow-ev2-retry", o.AllowEV2Retry, "When gate-promotion is set and the job fails, fail with a distinct, matchable error if its finished.json metadata marks the failure as narrow enough to safely retry, so the gating step's EV2 automatedRetry re-runs the whole step. Does not resubmit the job itself.")
	cmd.Flags().IntVar(&o.MaxEV2AutoRetryFailures, "max-ev2-auto-retry-failures", o.MaxEV2AutoRetryFailures, "Maximum number of failed tests (all labeled allow-retry) a job may have and still qualify for an automatic EV2 gating retry. Has no effect unless allow-ev2-retry is set.")
	cmd.Flags().StringVar(&o.BaseSha, "base-sha", o.BaseSha, "Git commit SHA to test against. When set, the job is triggered as a postsubmit with this specific commit instead of HEAD.")
	cmd.Flags().StringVar(&o.BaseRef, "base-ref", o.BaseRef, "Git base ref (branch) for the postsubmit job (requires --base-sha)")
	cmd.Flags().StringVar(&o.Org, "org", o.Org, "GitHub org for the postsubmit job (requires --base-sha)")
	cmd.Flags().StringVar(&o.Repo, "repo", o.Repo, "GitHub repo for the postsubmit job (requires --base-sha)")

	// Mark required flags
	for _, flag := range []string{
		"cloud",
		"environment",
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

	Cloud                   string
	Environment             string
	Region                  string
	AllowedSubscriptions    string
	ProwJobName             string
	Labels                  map[string]string
	Annotations             map[string]string
	EnvironmentVars         map[string]string
	EV2RolloutVersion       string
	PollInterval            time.Duration
	Timeout                 time.Duration
	NotFoundGracePeriod     time.Duration
	GangwayURL              string
	ProwURL                 string
	DryRun                  bool
	GatePromotion           bool
	AllowEV2Retry           bool
	MaxEV2AutoRetryFailures int

	// Git ref options for postsubmit execution pinned to a specific commit.
	// When BaseSha is set, the job is triggered as a postsubmit instead of a periodic.
	BaseSha string
	BaseRef string
	Org     string
	Repo    string
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
	Cloud                   string
	Environment             string
	Region                  string
	AllowedSubscriptions    string
	ProwJobName             string
	Labels                  map[string]string
	Annotations             map[string]string
	EnvironmentVars         map[string]string
	PollInterval            time.Duration
	Timeout                 time.Duration
	NotFoundGracePeriod     time.Duration
	ProwToken               string
	GangwayURL              string
	ProwURL                 string
	DryRun                  bool
	GatePromotion           bool
	AllowEV2Retry           bool
	MaxEV2AutoRetryFailures int

	// Git ref options for postsubmit execution
	BaseSha string
	BaseRef string
	Org     string
	Repo    string
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
		{flag: "cloud", name: "cloud", value: &o.Cloud},
		{flag: "environment", name: "environment", value: &o.Environment},
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

	// Add cloud, environment, and region as annotations
	annotations[ev2RolloutCloudAnnotation] = o.Cloud
	annotations[ev2RolloutEnvironmentAnnotation] = o.Environment
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

	// When base-sha is set, org, repo and base-ref are required for postsubmit execution
	if o.BaseSha != "" {
		for _, item := range []struct {
			flag  string
			name  string
			value *string
		}{
			{flag: "base-ref", name: "base ref", value: &o.BaseRef},
			{flag: "org", name: "GitHub org", value: &o.Org},
			{flag: "repo", name: "GitHub repo", value: &o.Repo},
		} {
			if item.value == nil || *item.value == "" {
				return nil, fmt.Errorf("the %s must be provided with --%s when --base-sha is set", item.name, item.flag)
			}
		}
	}

	if o.PollInterval <= 0 {
		return nil, fmt.Errorf("poll-interval must be greater than 0")
	}

	if o.Timeout <= 0 {
		return nil, fmt.Errorf("timeout must be greater than 0")
	}

	if o.NotFoundGracePeriod <= 0 {
		return nil, fmt.Errorf("not-found-grace-period must be greater than 0")
	}

	if o.AllowEV2Retry && !o.GatePromotion {
		return nil, fmt.Errorf("gate-promotion must be set when allow-ev2-retry is set")
	}

	if o.AllowEV2Retry && o.MaxEV2AutoRetryFailures <= 0 {
		return nil, fmt.Errorf("max-ev2-auto-retry-failures must be greater than 0 when allow-ev2-retry is set")
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
			Cloud:                   o.Cloud,
			Environment:             o.Environment,
			Region:                  o.Region,
			AllowedSubscriptions:    o.AllowedSubscriptions,
			ProwJobName:             o.ProwJobName,
			Labels:                  o.ParsedLabels,
			Annotations:             o.ParsedAnnotations,
			EnvironmentVars:         o.ParsedEnvironmentVars,
			PollInterval:            o.PollInterval,
			Timeout:                 o.Timeout,
			NotFoundGracePeriod:     o.NotFoundGracePeriod,
			ProwToken:               completed.ProwToken,
			GangwayURL:              o.GangwayURL,
			ProwURL:                 o.ProwURL,
			DryRun:                  o.DryRun,
			GatePromotion:           o.GatePromotion,
			AllowEV2Retry:           o.AllowEV2Retry,
			MaxEV2AutoRetryFailures: o.MaxEV2AutoRetryFailures,
			BaseSha:                 o.BaseSha,
			BaseRef:                 o.BaseRef,
			Org:                     o.Org,
			Repo:                    o.Repo,
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
	monitor := prowjob.NewMonitor(client, o.PollInterval, o.Timeout, o.NotFoundGracePeriod, o.DryRun, o.GatePromotion, o.AllowEV2Retry, o.MaxEV2AutoRetryFailures)

	// Prepare environment variables, including the region
	envs := make(map[string]string)
	maps.Copy(envs, o.EnvironmentVars)
	envs["MULTISTAGE_PARAM_OVERRIDE_LOCATION"] = o.Region
	if o.AllowedSubscriptions != "" {
		envs["MULTISTAGE_PARAM_OVERRIDE_ALLOWED_SUBSCRIPTIONS"] = o.AllowedSubscriptions
	}

	request := &prowgangway.CreateJobExecutionRequest{
		JobName:          o.ProwJobName,
		JobExecutionType: prowgangway.JobExecutionType_PERIODIC,
		PodSpecOptions: &prowgangway.PodSpecOptions{
			Envs:        envs,
			Labels:      o.Labels,
			Annotations: o.Annotations,
		},
	}

	if o.BaseSha != "" {
		request.JobExecutionType = prowgangway.JobExecutionType_POSTSUBMIT
		request.Refs = &prowgangway.Refs{
			Org:     o.Org,
			Repo:    o.Repo,
			BaseRef: o.BaseRef,
			BaseSha: o.BaseSha,
		}
		logger.Info("Using postsubmit execution with pinned commit", "org", o.Org, "repo", o.Repo, "baseRef", o.BaseRef, "baseSha", o.BaseSha)
	}

	return monitor.ExecuteAndWait(ctx, logger, request)
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
		Timeout:             4 * time.Hour,
		NotFoundGracePeriod: prowjob.DefaultNotFoundGracePeriod,
		GangwayURL:          defaultGangwayURL,
		ProwURL:             defaultProwURL,
	}
}

func (o *RawMonitorOptions) BindFlags(cmd *cobra.Command) error {
	cmd.Flags().StringVar(&o.JobExecutionID, "execution-id", o.JobExecutionID, "Prow job execution ID to monitor")
	cmd.Flags().DurationVar(&o.PollInterval, "poll-interval", o.PollInterval, "Status polling interval")
	cmd.Flags().DurationVar(&o.Timeout, "timeout", o.Timeout, "Maximum wait time for job completion")
	cmd.Flags().DurationVar(&o.NotFoundGracePeriod, "not-found-grace-period", o.NotFoundGracePeriod, "How long a consecutive run of 404s from the job status endpoint is tolerated (e.g. a short Prow/OCP maintenance window) before giving up on the job")
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

	JobExecutionID      string
	PollInterval        time.Duration
	Timeout             time.Duration
	NotFoundGracePeriod time.Duration
	GangwayURL          string
	ProwURL             string
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
	JobExecutionID      string
	PollInterval        time.Duration
	Timeout             time.Duration
	NotFoundGracePeriod time.Duration
	ProwToken           string
	GangwayURL          string
	ProwURL             string
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

	if o.NotFoundGracePeriod <= 0 {
		return nil, fmt.Errorf("not-found-grace-period must be greater than 0")
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
			JobExecutionID:      o.JobExecutionID,
			PollInterval:        o.PollInterval,
			Timeout:             o.Timeout,
			NotFoundGracePeriod: o.NotFoundGracePeriod,
			ProwToken:           completed.ProwToken,
			GangwayURL:          o.GangwayURL,
			ProwURL:             o.ProwURL,
		},
	}, nil
}

func (o *MonitorOptions) Monitor(ctx context.Context, logger logr.Logger) error {
	// Create Prow client and monitor
	client := prowjob.NewClient(o.ProwToken, o.GangwayURL, o.ProwURL)
	monitor := prowjob.NewMonitor(client, o.PollInterval, o.Timeout, o.NotFoundGracePeriod, false, false, false, prowjob.DefaultMaxEV2AutoRetryFailures)

	// Monitor existing job using shared polling logic
	logger.Info("Starting to monitor existing job", "jobExecutionID", o.JobExecutionID)

	return monitor.WaitForCompletion(ctx, logger, o.JobExecutionID)
}
