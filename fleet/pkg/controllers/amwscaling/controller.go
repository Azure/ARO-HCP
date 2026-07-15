// Copyright 2026 Microsoft Corporation
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

// Package amwscaling implements a controller that dynamically scales Azure Monitor
// Workspace (AMW) ingestion limits based on current utilization.
//
// The controller reads utilization metrics via the standard Azure Monitor Metrics API
// (armmonitor.MetricsClient) and updates ingestion limits via the
// Microsoft.Monitor/accounts/metricsContainers sub-resource. The metricsContainers
// API is a preview ARM endpoint documented at:
//
//	https://learn.microsoft.com/en-us/azure/azure-monitor/metrics/azure-monitor-workspace-monitor-ingest-limits?tabs=portal#request-for-an-increase-in-ingestion-limits-preview
//
// The upstream ARM template for this API is at:
//
//	https://github.com/Azure/prometheus-collector/blob/main/internal/docs/AMWLimitIncrease-Template.json
//
// As of July 2026, no published version of the Azure SDK for Go includes a client
// for the Microsoft.Monitor/accounts/metricsContainers resource type. The controller
// therefore uses raw REST calls via the azcore pipeline for reading and writing
// metricsContainers, while using the standard armmonitor SDK for workspace metadata
// and platform metrics.
package amwscaling

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"time"

	"github.com/go-logr/logr"

	utilruntime "k8s.io/apimachinery/pkg/util/runtime"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	azcorearm "github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/policy"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/monitor/armmonitor"

	"github.com/Azure/ARO-HCP/internal/utils"
)

const (
	// controllerName is the well-known name for this controller, used in logging.
	controllerName = "AMWIngestionScaling"

	// defaultUtilizationThreshold is the percentage above which limits are increased.
	// At 85% utilization, requesting 175% of usage yields a limit of ~149% of the
	// current limit — a meaningful increase well within Azure's 200% approval rule.
	defaultUtilizationThreshold = 85.0

	// defaultMaxLimit is the maximum ingestion limit supported by the ARM API without a support ticket.
	// See: https://learn.microsoft.com/en-us/azure/azure-monitor/metrics/azure-monitor-workspace-monitor-ingest-limits?tabs=portal#request-for-an-increase-in-ingestion-limits-preview
	defaultMaxLimit = int64(20_000_000)

	// activeTimeSeriesMetric is the Azure Monitor platform metric name for active time series utilization.
	activeTimeSeriesMetric = "ActiveTimeSeriesPercentUtilization"

	// eventsPerMinuteMetric is the Azure Monitor platform metric name for events per minute utilization.
	eventsPerMinuteMetric = "EventsPerMinuteIngestedPercentUtilization"
)

// Controller periodically checks Azure Monitor Workspace utilization and scales
// ingestion limits when utilization exceeds a threshold.
type Controller struct {
	pollInterval            time.Duration
	utilizationThreshold    float64
	maxLimit                int64
	workspaceResourceIDs    []string
	metricsContainersClient *MetricsContainersClient
	metricsClientFactory    func(subscriptionID string) (*armmonitor.MetricsClient, error)
	workspaceLocationFunc   func(ctx context.Context, parsed *azcorearm.ResourceID) (string, error)
}

// NewController creates a new AMW scaling controller.
func NewController(
	pollInterval time.Duration,
	workspaceResourceIDs []string,
	credential azcore.TokenCredential,
	clientOptions *policy.ClientOptions,
) *Controller {
	c := &Controller{
		pollInterval:         pollInterval,
		utilizationThreshold: defaultUtilizationThreshold,
		maxLimit:             defaultMaxLimit,
		workspaceResourceIDs: workspaceResourceIDs,
	}

	// When no workspaces are configured the controller is a no-op, so we
	// can skip building the Azure pipeline and metrics client factory which
	// require non-nil clientOptions and credential.
	if len(workspaceResourceIDs) == 0 {
		return c
	}

	if clientOptions == nil {
		clientOptions = &policy.ClientOptions{}
	}

	armClientOptions := &azcorearm.ClientOptions{
		ClientOptions: *clientOptions,
	}

	c.metricsContainersClient = NewMetricsContainersClient(credential, clientOptions)
	c.metricsClientFactory = func(subscriptionID string) (*armmonitor.MetricsClient, error) {
		return armmonitor.NewMetricsClient(subscriptionID, credential, armClientOptions)
	}
	c.workspaceLocationFunc = func(ctx context.Context, parsed *azcorearm.ResourceID) (string, error) {
		client, err := armmonitor.NewAzureMonitorWorkspacesClient(parsed.SubscriptionID, credential, armClientOptions)
		if err != nil {
			return "", fmt.Errorf("creating workspaces client: %w", err)
		}
		resp, err := client.Get(ctx, parsed.ResourceGroupName, parsed.Name, nil)
		if err != nil {
			return "", fmt.Errorf("getting workspace: %w", err)
		}
		if resp.Location == nil {
			return "", fmt.Errorf("workspace %s has no location", parsed.String())
		}
		return *resp.Location, nil
	}

	return c
}

// Run starts the controller loop. It blocks until the context is cancelled.
func (c *Controller) Run(ctx context.Context) {
	defer utilruntime.HandleCrash()

	ctx = utils.ContextWithControllerName(ctx, controllerName)
	logger := utils.LoggerFromContext(ctx)
	logger = logger.WithValues(utils.LogValues{}.AddControllerName(controllerName)...)
	ctx = utils.ContextWithLogger(ctx, logger)

	if len(c.workspaceResourceIDs) == 0 {
		logger.Info("No AMW workspace resource IDs configured, controller will not run")
		return
	}

	logger.Info("Starting",
		"pollInterval", c.pollInterval,
		"workspaces", len(c.workspaceResourceIDs),
		"threshold", c.utilizationThreshold,
		"maxLimit", c.maxLimit,
	)

	// Reconcile immediately on start, then on ticker.
	c.reconcileAll(ctx, logger)

	ticker := time.NewTicker(c.pollInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			logger.Info("Stopped")
			return
		case <-ticker.C:
			c.reconcileAll(ctx, logger)
		}
	}
}

func (c *Controller) reconcileAll(ctx context.Context, logger logr.Logger) {
	for _, resourceID := range c.workspaceResourceIDs {
		if err := c.reconcileOne(ctx, logger, resourceID); err != nil {
			logger.Error(err, "Failed to reconcile AMW workspace", "resourceID", resourceID)
		}
	}
}

func (c *Controller) reconcileOne(ctx context.Context, logger logr.Logger, resourceID string) error {
	logger = logger.WithValues("resourceID", resourceID)

	parsed, err := azcorearm.ParseResourceID(resourceID)
	if err != nil {
		return fmt.Errorf("parsing resource ID: %w", err)
	}

	utilization, err := c.readUtilization(ctx, parsed.SubscriptionID, resourceID)
	if err != nil {
		return fmt.Errorf("reading utilization: %w", err)
	}

	currentLimits, err := c.metricsContainersClient.GetLimits(ctx, resourceID)
	if err != nil {
		return fmt.Errorf("reading current limits: %w", err)
	}

	proposed := ProposeLimits(*currentLimits, *utilization, c.utilizationThreshold, c.maxLimit)
	if proposed == nil {
		return nil
	}

	location, err := c.workspaceLocationFunc(ctx, parsed)
	if err != nil {
		return fmt.Errorf("reading workspace location: %w", err)
	}

	logger = logger.WithValues(
		"utilization", utilization,
		"currentLimits", currentLimits,
		"proposedLimits", proposed,
	)

	if err := c.metricsContainersClient.SetLimits(ctx, resourceID, location, proposed); err != nil {
		var respErr *azcore.ResponseError
		if errors.As(err, &respErr) && respErr.ErrorCode == "InvalidRequest" && respErr.StatusCode == http.StatusBadRequest {
			// Usage fluctuated below the required threshold between when we
			// read metrics and when we issued the PUT. This is expected and
			// will be retried on the next reconcile.
			logger.Error(err, "Utilization dropped below required threshold for limit increase, will retry.")
			return nil
		}
		logger.Error(err, "Failed to scale AMW ingestion limits")
		return fmt.Errorf("setting new limits: %w", err)
	}

	logger.Info("Scaled AMW ingestion limits")
	return nil
}

func (c *Controller) readUtilization(ctx context.Context, subscriptionID, resourceID string) (*AMWUtilization, error) {
	client, err := c.metricsClientFactory(subscriptionID)
	if err != nil {
		return nil, fmt.Errorf("creating metrics client: %w", err)
	}

	metricNames := activeTimeSeriesMetric + "," + eventsPerMinuteMetric
	// Query for 2x the poll interval to avoid dead spots between reconcile cycles.
	timespan := fmt.Sprintf("PT%dS", int((2 * c.pollInterval).Seconds()))
	aggregation := "Maximum"

	resp, err := client.List(ctx, resourceID, &armmonitor.MetricsClientListOptions{
		Metricnames: &metricNames,
		Timespan:    &timespan,
		Aggregation: &aggregation,
	})
	if err != nil {
		return nil, fmt.Errorf("listing metrics: %w", err)
	}

	// Take the maximum value across all data points to capture peak usage.
	util := &AMWUtilization{}
	for _, metric := range resp.Value {
		if metric.Name == nil || metric.Name.Value == nil {
			continue
		}
		name := *metric.Name.Value
		for _, ts := range metric.Timeseries {
			for _, dp := range ts.Data {
				if dp.Maximum == nil {
					continue
				}
				switch name {
				case activeTimeSeriesMetric:
					if *dp.Maximum > util.ActiveTimeSeriesPercent {
						util.ActiveTimeSeriesPercent = *dp.Maximum
					}
				case eventsPerMinuteMetric:
					if *dp.Maximum > util.EventsPerMinutePercent {
						util.EventsPerMinutePercent = *dp.Maximum
					}
				}
			}
		}
	}

	return util, nil
}
