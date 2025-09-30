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

	prowjobs "sigs.k8s.io/prow/pkg/apis/prowjobs/v1"
	prowgangway "sigs.k8s.io/prow/pkg/gangway"

	"github.com/Azure/ARO-HCP/test/util/log"
)

// Monitor handles job execution and monitoring
type Monitor struct {
	client       *Client
	pollInterval time.Duration
	timeout      time.Duration
}

// NewMonitor creates a new job monitor with the specified polling interval and timeout.
func NewMonitor(client *Client, pollInterval, timeout time.Duration) *Monitor {
	return &Monitor{
		client:       client,
		pollInterval: pollInterval,
		timeout:      timeout,
	}
}

// WaitForCompletion polls job status until completion
func (m *Monitor) WaitForCompletion(ctx context.Context, prowExecutionID string) error {
	logger := log.GetLogger()

	ctx, cancel := context.WithTimeout(ctx, m.timeout)
	defer cancel()

	// Create ticker for polling interval
	ticker := time.NewTicker(m.pollInterval)
	defer ticker.Stop()

	// Check status immediately, then poll at intervals
	for {
		job, err := m.client.GetJobStatus(ctx, prowExecutionID)
		if err != nil {
			logger.WithError(err).Warn("Failed to get job status after retries, will continue polling")
		} else {
			status := string(job.Status.State)
			logger.WithFields(map[string]interface{}{
				"prowExecutionID": prowExecutionID,
				"status":          status,
				"jobName":         job.Spec.Job,
			}).Info("Job status update")

			switch status {
			case string(prowjobs.SuccessState):
				logger.WithField("prowExecutionID", prowExecutionID).Info("Job completed successfully")
				return nil
			case string(prowjobs.FailureState):
				return fmt.Errorf("job %s failed - check the Prow UI for detailed logs: %s", prowExecutionID, job.Status.URL)
			case string(prowjobs.ErrorState):
				return fmt.Errorf("job %s encountered an error - check Prow status page and job logs for details: %s", prowExecutionID, job.Status.URL)
			case string(prowjobs.AbortedState):
				return fmt.Errorf("job %s was aborted - this may be due to timeout or manual cancellation", prowExecutionID)
			}
		}

		select {
		case <-ctx.Done():
			return fmt.Errorf("job monitoring timed out after %v - job %s may still be running, check Prow UI: %s", m.timeout, prowExecutionID, job.Status.URL)
		case <-ticker.C:
			// Continue to next iteration
		}
	}
}

// ExecuteAndWait submits a job and waits for completion
func (m *Monitor) ExecuteAndWait(ctx context.Context, request *prowgangway.CreateJobExecutionRequest) error {
	logger := log.GetLogger()

	// Submit job
	logger.WithField("jobName", request.JobName).Info("Submitting Prow job")
	prowExecutionID, err := m.client.SubmitJob(ctx, request)
	if err != nil {
		return fmt.Errorf("failed to submit job: %w", err)
	}

	logger.WithFields(map[string]interface{}{
		"prowExecutionID": prowExecutionID,
		"jobName":         request.JobName,
	}).Info("Job submitted successfully")

	// Wait for completion using shared logic
	return m.WaitForCompletion(ctx, prowExecutionID)
}
