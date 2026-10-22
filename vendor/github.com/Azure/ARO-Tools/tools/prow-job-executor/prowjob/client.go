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

// Package prowjob provides functionality for interacting with OpenShift Prow jobs,
// including job submission, status monitoring, and authentication via Azure Key Vault.
package prowjob

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"time"

	"github.com/go-logr/logr"

	"k8s.io/apimachinery/pkg/util/wait"

	prowjobs "sigs.k8s.io/prow/pkg/apis/prowjobs/v1"
	prowgangway "sigs.k8s.io/prow/pkg/gangway"
	"sigs.k8s.io/yaml"

	"github.com/Azure/ARO-Tools/tools/prow-job-executor/internal/retry"
)

// jobSubmissionResponse represents the minimal JSON response from Gangway API for job submission
type jobSubmissionResponse struct {
	ID string `json:"id"`
}

// Client handles Prow API interactions
type Client struct {
	token         string
	client        *http.Client
	gangwayURL    string
	prowURL       string
	submitBackoff wait.Backoff
	statusBackoff wait.Backoff
}

// NewClient creates a new Prow API client with the provided authentication token and API URLs.
func NewClient(token, gangwayURL, prowURL string) *Client {
	return &Client{
		token:      token,
		gangwayURL: gangwayURL,
		prowURL:    prowURL,
		client: &http.Client{
			Timeout: 30 * time.Second,
		},
		// Exponential backoff with jitter for transient job-submission failures.
		// Delays between attempts grow 1, 2, 4, 8, 16, 32 minutes over up to 7
		// attempts, for a worst-case cumulative wait of ~63 minutes before giving
		// up. The parent context still bounds the total runtime.
		//
		// Note: apimachinery's Backoff.Cap not only clamps an individual delay but
		// also stops all further retries once the cap is reached, so the schedule
		// is deliberately bounded by Steps rather than Cap.
		submitBackoff: wait.Backoff{
			Duration: time.Minute, // Initial delay
			Factor:   2.0,         // Exponential factor
			Jitter:   0.1,         // 10% jitter to de-sync concurrent submitters
			Steps:    7,           // Maximum attempts (~63m worst-case cumulative wait)
		},
		// Exponential backoff with jitter for transient GetJobStatus failures - a
		// freshly submitted job's status may 404 until it propagates, and transport
		// errors are transient. This only absorbs that short propagation delay; a 404
		// that outlives it is handled separately by waitForCompletion's
		// notFoundGracePeriod, which tolerates a longer outage (e.g. a Prow/OCP
		// maintenance window) across repeated polls.
		statusBackoff: wait.Backoff{
			Duration: time.Second,      // Initial delay
			Factor:   2.0,              // Exponential factor
			Jitter:   0.1,              // 10% jitter
			Steps:    3,                // Maximum attempts
			Cap:      10 * time.Second, // Maximum delay cap
		},
	}
}

// SubmitJob submits a job to Prow and returns the job execution ID, retrying on
// transient errors with exponential backoff.
//
// The Gangway API enforces a low request-rate limit (~9 requests/minute per client
// IP) and rejects excess requests immediately with HTTP 429 rather than queueing
// them. Without retries a momentary rate-limit fails the whole EV2 gating step,
// forcing the entire deployment job to be restarted. Only transient failures
// (HTTP 429, 5xx, and network errors) are retried; everything else fails fast.
func (c *Client) SubmitJob(ctx context.Context, request *prowgangway.CreateJobExecutionRequest) (string, error) {
	return retry.WithValue(ctx, c.submitBackoff, isRetryableError, func(ctx context.Context) (string, error) {
		return c.submitJobOnce(ctx, request)
	})
}

// submitJobOnce performs a single job submission request without retry logic.
func (c *Client) submitJobOnce(ctx context.Context, request *prowgangway.CreateJobExecutionRequest) (string, error) {
	logger, err := logr.FromContext(ctx)
	if err != nil {
		return "", err
	}

	data, err := json.Marshal(request)
	if err != nil {
		return "", fmt.Errorf("failed to marshal job request: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, "POST", c.gangwayURL, bytes.NewBuffer(data))
	if err != nil {
		return "", fmt.Errorf("failed to create HTTP request: %w", err)
	}

	req.Header.Set("Authorization", "Bearer "+c.token)
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.client.Do(req)
	if err != nil {
		return "", fmt.Errorf("failed to submit job: %w", err)
	}
	defer func() {
		if err := resp.Body.Close(); err != nil {
			logger.Error(err, "failed to close body")
		}
	}()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		err := fmt.Errorf("job submission failed with status %d: %s", resp.StatusCode, string(body))
		return "", &httpStatusError{statusCode: resp.StatusCode, err: err}
	}

	var response jobSubmissionResponse
	if err := json.NewDecoder(resp.Body).Decode(&response); err != nil {
		return "", fmt.Errorf("failed to decode job response: %w", err)
	}

	return response.ID, nil
}

// GetJobStatus retrieves the full job information by Prow execution ID with retry logic
func (c *Client) GetJobStatus(ctx context.Context, prowExecutionID string) (*prowjobs.ProwJob, error) {
	// Everything except a non-retryable HTTP status (e.g. 401/403) is retried: a
	// freshly submitted job's status may 404 until it propagates, and transport
	// errors are transient.
	isRetryable := func(err error) bool {
		return !isNonRetryableHTTPError(err)
	}

	return retry.WithValue(ctx, c.statusBackoff, isRetryable, func(ctx context.Context) (*prowjobs.ProwJob, error) {
		return c.getJobStatusOnce(ctx, prowExecutionID)
	})
}

// getJobStatusOnce performs a single job status request without retry logic
func (c *Client) getJobStatusOnce(ctx context.Context, prowExecutionID string) (*prowjobs.ProwJob, error) {
	logger, err := logr.FromContext(ctx)
	if err != nil {
		return nil, err
	}

	url := fmt.Sprintf("%s?prowjob=%s", c.prowURL, prowExecutionID)
	req, err := http.NewRequestWithContext(ctx, "GET", url, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create HTTP request: %w", err)
	}

	resp, err := c.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("failed to get job status: %w", err)
	}
	defer func() {
		if err := resp.Body.Close(); err != nil {
			logger.Error(err, "failed to close body")
		}
	}()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		err := fmt.Errorf("failed to get job status, status code: %d, body: %s", resp.StatusCode, string(body))
		return nil, &httpStatusError{statusCode: resp.StatusCode, err: err}
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read response body: %w", err)
	}

	var prowJob prowjobs.ProwJob
	if err := yaml.Unmarshal(body, &prowJob); err != nil {
		bodyStr := fmt.Sprintf("%.1000s", string(body))
		return nil, fmt.Errorf("failed to decode job status as YAML: %w, response body: %s", err, bodyStr)
	}

	return &prowJob, nil
}

// httpStatusError wraps errors with HTTP status code information
type httpStatusError struct {
	statusCode int
	err        error
}

func (e *httpStatusError) Error() string {
	return e.err.Error()
}

func (e *httpStatusError) Unwrap() error {
	return e.err
}

// isNonRetryableHTTPError checks if an error represents a non-retryable HTTP status
func isNonRetryableHTTPError(err error) bool {
	var httpErr *httpStatusError
	if errors.As(err, &httpErr) {
		return !isRetryableStatusCode(httpErr.statusCode)
	}
	return false
}

// IsNotFoundError reports whether err is a GetJobStatus failure carrying an HTTP 404 -
// i.e. Prow's status endpoint has no record of the job at all, rather than a transient
// or auth failure. A freshly submitted job can 404 briefly before it propagates (which
// GetJobStatus's own retry already absorbs), but a 404 that survives those retries means
// the job's status page will never appear (e.g. it was garbage-collected), so callers
// should treat it as a definitive failure rather than keep polling until the overall
// timeout.
func IsNotFoundError(err error) bool {
	var httpErr *httpStatusError
	return errors.As(err, &httpErr) && httpErr.statusCode == http.StatusNotFound
}

// isRetryableError reports whether a job-submission error is transient and worth
// retrying. Only HTTP 429, 5xx responses and network-level errors are retried;
// deterministic failures (request marshaling/construction, response decoding, and
// other 4xx such as 400/404/409) are treated as permanent and fail fast.
func isRetryableError(err error) bool {
	var httpErr *httpStatusError
	if errors.As(err, &httpErr) {
		code := httpErr.statusCode
		return code == http.StatusTooManyRequests || (code >= 500 && code <= 599)
	}

	// Transport/network errors (timeouts, connection resets, etc.) are transient.
	var netErr net.Error
	return errors.As(err, &netErr)
}

// isRetryableStatusCode determines if an HTTP status code should be retried
func isRetryableStatusCode(statusCode int) bool {
	switch statusCode {
	case http.StatusUnauthorized, // 401
		http.StatusForbidden: // 403
		return false
	default:
		return true
	}
}
