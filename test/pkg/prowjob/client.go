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
	"net/http"
	"time"

	"k8s.io/apimachinery/pkg/util/wait"
	prowjobs "sigs.k8s.io/prow/pkg/apis/prowjobs/v1"
	prowgangway "sigs.k8s.io/prow/pkg/gangway"
	"sigs.k8s.io/yaml"
)

// jobSubmissionResponse represents the minimal JSON response from Gangway API for job submission
type jobSubmissionResponse struct {
	ID string `json:"id"`
}

// Client handles Prow API interactions
type Client struct {
	token      string
	client     *http.Client
	gangwayURL string
	prowURL    string
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
	}
}

// SubmitJob submits a job to Prow and returns the job execution ID
func (c *Client) SubmitJob(ctx context.Context, request *prowgangway.CreateJobExecutionRequest) (string, error) {

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
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return "", fmt.Errorf("job submission failed with status %d: %s", resp.StatusCode, string(body))
	}

	var response jobSubmissionResponse
	if err := json.NewDecoder(resp.Body).Decode(&response); err != nil {
		return "", fmt.Errorf("failed to decode job response: %w", err)
	}

	return response.ID, nil
}

// GetJobStatus retrieves the full job information by Prow execution ID with retry logic
func (c *Client) GetJobStatus(ctx context.Context, prowExecutionID string) (*prowjobs.ProwJob, error) {
	var result *prowjobs.ProwJob

	// Configure exponential backoff with jitter
	backoff := wait.Backoff{
		Duration: time.Second,      // Initial delay
		Factor:   2.0,              // Exponential factor
		Jitter:   0.1,              // 10% jitter
		Steps:    3,                // Maximum retries
		Cap:      10 * time.Second, // Maximum delay cap
	}

	condition := func(ctx context.Context) (bool, error) {
		job, err := c.getJobStatusOnce(ctx, prowExecutionID)
		if err != nil {
			// Check if this is a non-retryable error (e.g., 403 Forbidden)
			if isNonRetryableHTTPError(err) {
				return false, err // Stop retrying and propagate the error
			}

			// For retryable errors continue
			return false, nil
		}

		result = job
		return true, nil // Success, stop retrying
	}

	err := wait.ExponentialBackoffWithContext(ctx, backoff, condition)
	if err != nil {
		return nil, err
	}

	return result, nil
}

// getJobStatusOnce performs a single job status request without retry logic
func (c *Client) getJobStatusOnce(ctx context.Context, prowExecutionID string) (*prowjobs.ProwJob, error) {
	url := fmt.Sprintf("%s?prowjob=%s", c.prowURL, prowExecutionID)
	req, err := http.NewRequestWithContext(ctx, "GET", url, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create HTTP request: %w", err)
	}

	resp, err := c.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("failed to get job status: %w", err)
	}
	defer resp.Body.Close()

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
