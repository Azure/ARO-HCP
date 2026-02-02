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

package prowjob

import (
	"context"
	"fmt"
	"time"

	"github.com/go-logr/logr"

	prowjobs "sigs.k8s.io/prow/pkg/apis/prowjobs/v1"
	prowgangway "sigs.k8s.io/prow/pkg/gangway"
)

// Monitor handles job execution and monitoring
type Monitor struct {
	client        *Client
	pollInterval  time.Duration
	timeout       time.Duration
	dryRun        bool
	gatePromotion bool
}

// NewMonitor creates a new job monitor with the specified polling interval and timeout.
func NewMonitor(client *Client, pollInterval, timeout time.Duration, dryRun, gatePromotion bool) *Monitor {
	return &Monitor{
		client:        client,
		pollInterval:  pollInterval,
		timeout:       timeout,
		dryRun:        dryRun,
		gatePromotion: gatePromotion,
	}
}

// WaitForCompletion polls job status until completion
func (m *Monitor) WaitForCompletion(ctx context.Context, logger logr.Logger, prowExecutionID string) error {
	ctx, cancel := context.WithTimeout(ctx, m.timeout)
	defer cancel()

	// Create ticker for polling interval
	ticker := time.NewTicker(m.pollInterval)
	defer ticker.Stop()

	// Check status immediately, then poll at intervals
	for {
		job, err := m.client.GetJobStatus(ctx, prowExecutionID)
		if err != nil {
			logger.Error(err, "Failed to get job status after retries, will continue polling")
		} else {
			status := string(job.Status.State)
			logger = logger.WithValues(
				"prowExecutionID", prowExecutionID,
				"status", status,
				"jobName", job.Spec.Job,
				"prowUrl", job.Status.URL,
			)
			logger.Info("Job status update")

			switch status {
			case string(prowjobs.SuccessState):
				logger.Info("Job completed successfully")
				return nil
			case string(prowjobs.FailureState):
				if m.gatePromotion {
					return fmt.Errorf("job %s failed - check the Prow UI for detailed logs: %s", prowExecutionID, job.Status.URL)
				} else {
					logger.Error(err, "Unexpected job state, but gating is not requested.")
					return nil
				}
			case string(prowjobs.ErrorState):
				if m.gatePromotion {
					return fmt.Errorf("job %s encountered an error - check Prow status page and job logs for details: %s", prowExecutionID, job.Status.URL)
				} else {
					logger.Error(err, "Unexpected job state, but gating is not requested.")
					return nil
				}
			case string(prowjobs.AbortedState):
				if m.gatePromotion {
					return fmt.Errorf("job %s was aborted - this may be due to timeout or manual cancellation", prowExecutionID)
				} else {
					logger.Error(err, "Unexpected job state, but gating is not requested.")
					return nil
				}
			}
		}

		select {
		case <-ctx.Done():
			if job != nil {
				return fmt.Errorf("job monitoring timed out after %v - job %s may still be running, check Prow UI: %s", m.timeout, prowExecutionID, job.Status.URL)
			}
			return fmt.Errorf("job monitoring timed out after %v - job %s may still be running (unable to retrieve job status)", m.timeout, prowExecutionID)
		case <-ticker.C:
			// Continue to next iteration
		}
	}
}

// ExecuteAndWait submits a job and waits for completion
func (m *Monitor) ExecuteAndWait(ctx context.Context, logger logr.Logger, request *prowgangway.CreateJobExecutionRequest) error {
	// Submit job
	logger.Info("Submitting Prow job", "jobName", request.JobName)
	if m.dryRun {
		logger.Info("Dry-run is set, exiting.")
	}
	prowExecutionID, err := m.client.SubmitJob(ctx, request)
	if err != nil {
		return fmt.Errorf("failed to submit job: %w", err)
	}

	logger.Info("Job submitted successfully", "prowExecutionID", prowExecutionID, "jobName", request.JobName)

	// Wait for completion using shared logic
	return m.WaitForCompletion(ctx, logger, prowExecutionID)
}
