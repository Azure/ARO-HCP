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

// This file implements a client for the Microsoft.Monitor/accounts/metricsContainers
// ARM resource type using raw REST calls. No Azure SDK for Go client exists for this
// resource type as of July 2026.
//
// When an SDK client becomes available, replace this file with a thin wrapper around
// the SDK type.
//
// API documentation:
//   https://learn.microsoft.com/en-us/azure/azure-monitor/metrics/azure-monitor-workspace-monitor-ingest-limits?tabs=portal#request-for-an-increase-in-ingestion-limits-preview
// Upstream ARM template:
//   https://github.com/Azure/prometheus-collector/blob/main/internal/docs/AMWLimitIncrease-Template.json

package amwscaling

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/cloud"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/policy"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/runtime"
)

const (
	// metricsContainerAPIVersion is the API version for the
	// Microsoft.Monitor/accounts/metricsContainers sub-resource.
	metricsContainerAPIVersion = "2025-10-03-preview"
)

// MetricsContainersClient provides operations for reading and updating ingestion
// limits on Azure Monitor Workspace metricsContainers resources.
//
// This is a hand-rolled client because no Azure SDK for Go package covers this
// resource type yet. When an SDK client ships, this struct can be replaced with
// that client (the method signatures use the same AMWLimits type as the rest of
// this package).
type MetricsContainersClient struct {
	pipeline    runtime.Pipeline
	armEndpoint string
}

// NewMetricsContainersClient creates a client for the metricsContainers sub-resource.
func NewMetricsContainersClient(credential azcore.TokenCredential, clientOptions *policy.ClientOptions) *MetricsContainersClient {
	if clientOptions == nil {
		clientOptions = &policy.ClientOptions{}
	}

	// Derive ARM audience and endpoint from cloud configuration.
	armConfig := clientOptions.Cloud.Services[cloud.ResourceManager]
	armAudience := armConfig.Audience
	if armAudience == "" {
		armAudience = "https://management.azure.com"
	}
	armEndpoint := armConfig.Endpoint
	if armEndpoint == "" {
		armEndpoint = "https://management.azure.com"
	}
	armEndpoint = strings.TrimRight(armEndpoint, "/")

	// Copy PerRetryPolicies to avoid mutating the caller's slice.
	perRetryPolicies := make([]policy.Policy, len(clientOptions.PerRetryPolicies), len(clientOptions.PerRetryPolicies)+1)
	copy(perRetryPolicies, clientOptions.PerRetryPolicies)
	perRetryPolicies = append(perRetryPolicies, runtime.NewBearerTokenPolicy(credential, []string{armAudience + "/.default"}, nil))

	pipeline := runtime.NewPipeline("amwscaling", "v1.0.0", runtime.PipelineOptions{}, &policy.ClientOptions{
		Cloud:                           clientOptions.Cloud,
		Telemetry:                       clientOptions.Telemetry,
		Transport:                       clientOptions.Transport,
		PerCallPolicies:                 clientOptions.PerCallPolicies,
		PerRetryPolicies:                perRetryPolicies,
		InsecureAllowCredentialWithHTTP: clientOptions.InsecureAllowCredentialWithHTTP,
	})

	return &MetricsContainersClient{
		pipeline:    pipeline,
		armEndpoint: armEndpoint,
	}
}

// GetLimits reads the current ingestion limits for the given workspace.
func (c *MetricsContainersClient) GetLimits(ctx context.Context, workspaceResourceID string) (*AMWLimits, error) {
	req, err := runtime.NewRequest(ctx, http.MethodGet, c.url(workspaceResourceID))
	if err != nil {
		return nil, err
	}

	resp, err := c.pipeline.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, runtime.NewResponseError(resp)
	}

	var container metricsContainerResponse
	if err := json.NewDecoder(resp.Body).Decode(&container); err != nil {
		return nil, fmt.Errorf("decoding response: %w", err)
	}

	return &AMWLimits{
		MaxActiveTimeSeries: container.Properties.Limits.MaxActiveTimeSeries,
		MaxEventsPerMinute:  container.Properties.Limits.MaxEventsPerMinute,
	}, nil
}

// SetLimits updates the ingestion limits for the given workspace.
func (c *MetricsContainersClient) SetLimits(ctx context.Context, workspaceResourceID, location string, limits *AMWLimits) error {
	body := metricsContainerRequest{
		Location: location,
		Properties: metricsContainerRequestProps{
			Limits: metricsContainerRequestLimits{
				MaxActiveTimeSeries: limits.MaxActiveTimeSeries,
				MaxEventsPerMinute:  limits.MaxEventsPerMinute,
			},
		},
	}

	bodyBytes, err := json.Marshal(body)
	if err != nil {
		return err
	}

	req, err := runtime.NewRequest(ctx, http.MethodPut, c.url(workspaceResourceID))
	if err != nil {
		return err
	}
	if err := req.SetBody(streaming(bodyBytes), "application/json"); err != nil {
		return err
	}

	resp, err := c.pipeline.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusCreated {
		return runtime.NewResponseError(resp)
	}

	return nil
}

func (c *MetricsContainersClient) url(workspaceResourceID string) string {
	return fmt.Sprintf("%s%s/metricsContainers/default?api-version=%s", c.armEndpoint, workspaceResourceID, metricsContainerAPIVersion)
}

// --- Raw REST types (delete when SDK client is available) ---

type metricsContainerResponse struct {
	Properties struct {
		Limits struct {
			MaxActiveTimeSeries int64 `json:"maxActiveTimeSeries"`
			MaxEventsPerMinute  int64 `json:"maxEventsPerMinute"`
		} `json:"limits"`
	} `json:"properties"`
}

type metricsContainerRequest struct {
	Location   string                       `json:"location"`
	Properties metricsContainerRequestProps `json:"properties"`
}

type metricsContainerRequestProps struct {
	Limits metricsContainerRequestLimits `json:"limits"`
}

type metricsContainerRequestLimits struct {
	MaxActiveTimeSeries int64 `json:"maxActiveTimeSeries"`
	MaxEventsPerMinute  int64 `json:"maxEventsPerMinute"`
}

func streaming(b []byte) io.ReadSeekCloser {
	return &readSeekCloser{reader: bytes.NewReader(b)}
}

type readSeekCloser struct {
	reader *bytes.Reader
}

func (r *readSeekCloser) Read(p []byte) (int, error)         { return r.reader.Read(p) }
func (r *readSeekCloser) Seek(o int64, w int) (int64, error) { return r.reader.Seek(o, w) }
func (r *readSeekCloser) Close() error                       { return nil }
