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

package framework

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"

	"github.com/onsi/ginkgo/v2"

	"k8s.io/apimachinery/pkg/util/wait"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/runtime"
)

// The Azure SDK clients retry a PUT that fails with a retryable status such as
// 503. When the first attempt was actually accepted and only its response was
// lost (for example a 503 from the Azure front-end edge or ARM after the request
// had already been applied), the retry arrives while that first operation is
// still running and the service rejects it with 409 Conflict. The helpers in
// this file recognise that situation, using the attempts recorded by
// requestAttemptTrackerPolicy, and wait for the in-flight operation instead of
// failing the test on a conflict with its own earlier request.

// isConflictError reports whether err is an Azure 409 Conflict response.
func isConflictError(err error) bool {
	var respErr *azcore.ResponseError
	return errors.As(err, &respErr) && respErr.StatusCode == http.StatusConflict
}

// isTerminalProvisioningState reports whether an ARM provisioning state is final.
func isTerminalProvisioningState(state string) bool {
	switch strings.ToLower(state) {
	case "succeeded", "failed", "canceled":
		return true
	}
	return false
}

// isInFlightProvisioningState reports whether a resource in this provisioning
// state has an operation that was accepted and has not finished. RP operations,
// creates and updates alike, start in Accepted. States such as AwaitingSecret
// are excluded because they do not finish on their own.
func isInFlightProvisioningState(state string) bool {
	switch strings.ToLower(state) {
	case "accepted", "provisioning", "updating":
		return true
	}
	return false
}

// resumeInFlightCreate is called when a create or update PUT for an RP resource
// fails. If the failure is a 409 Conflict, the Azure SDK re-sent the PUT after
// an attempt whose outcome was unknown (a transport error, 408, or 5xx), and the
// resource exists with an operation still in flight, it returns a poller that
// tracks that operation to completion by reading the resource, so the caller
// can continue exactly as if its own PUT had been accepted. Otherwise it
// returns createErr unchanged.
//
// Requiring a retry after an unknown outcome limits this to a conflict with an
// earlier attempt of the same call that the service may have applied. A 409 on
// the first attempt, or after attempts the service definitively rejected, is
// always returned.
//
// get must return the resource wrapped in the create response type T, together
// with the resource's current provisioning state.
func resumeInFlightCreate[T any](
	ctx context.Context,
	createErr error,
	attempts *requestAttemptTracker,
	resourceDescription string,
	get func(ctx context.Context) (T, string, error),
) (*runtime.Poller[T], error) {
	if !isConflictError(createErr) || !attempts.RetriedAfterUnknownOutcome() {
		return nil, createErr
	}

	logger := ginkgo.GinkgoLogr
	_, state, err := get(ctx)
	if err != nil {
		logger.Info("create request was rejected with 409 Conflict and the resource could not be read to check for an in-flight operation",
			"resource", resourceDescription, "attempts", attempts.Attempts(), "error", err.Error())
		return nil, createErr
	}
	if !isInFlightProvisioningState(state) {
		return nil, createErr
	}

	logger.Info("create request was rejected with 409 Conflict by an Azure SDK retry because the earlier attempt of the same request was accepted; waiting for that operation instead of failing",
		"resource", resourceDescription, "attempts", attempts.Attempts(), "provisioningState", state, "conflict", createErr.Error())

	return runtime.NewPoller(nil, runtime.Pipeline{}, &runtime.NewPollerOptions[T]{
		Handler: &inFlightCreateHandler[T]{
			resourceDescription: resourceDescription,
			conflictErr:         createErr,
			get:                 get,
		},
	})
}

// inFlightCreateHandler is a runtime.PollingHandler that follows an operation
// through the resource's provisioning state, for when the operation's own
// polling URL is unknown because the response that carried it was lost.
type inFlightCreateHandler[T any] struct {
	resourceDescription string
	conflictErr         error
	get                 func(ctx context.Context) (T, string, error)

	done   bool
	result T
	err    error
}

func (h *inFlightCreateHandler[T]) Done() bool {
	return h.done
}

// Poll reads the resource once. A terminal failure is returned from Poll as
// well as from Result, so callers that only Poll, without waiting for the
// result, still see it.
func (h *inFlightCreateHandler[T]) Poll(ctx context.Context) (*http.Response, error) {
	resource, state, err := h.get(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed reading %s while waiting for its in-flight operation: %w", h.resourceDescription, err)
	}
	if isTerminalProvisioningState(state) {
		h.done = true
		h.result = resource
		if !strings.EqualFold(state, "succeeded") {
			h.err = fmt.Errorf("in-flight operation on %s, resumed after the create request was rejected with 409 Conflict, ended in provisioning state %q (the operation's own error is not available because its response was lost; see the resource's operations for details); original conflict: %w",
				h.resourceDescription, state, h.conflictErr)
			return nil, h.err
		}
	}
	return &http.Response{StatusCode: http.StatusOK, Header: http.Header{}, Body: http.NoBody}, nil
}

func (h *inFlightCreateHandler[T]) Result(_ context.Context, out *T) error {
	if !h.done {
		return fmt.Errorf("in-flight operation on %s has not completed", h.resourceDescription)
	}
	if h.err != nil {
		return h.err
	}
	*out = h.result
	return nil
}

// isDeploymentActiveError reports whether err is the 409 ARM returns when a
// deployment PUT would overwrite a deployment of the same name that is still
// running.
func isDeploymentActiveError(err error) bool {
	var respErr *azcore.ResponseError
	return errors.As(err, &respErr) && respErr.StatusCode == http.StatusConflict && respErr.ErrorCode == "DeploymentActive"
}

// waitForActiveDeployment waits for the deployment that blocked an Azure SDK
// retry of a deployment PUT with DeploymentActive to reach a terminal state, so
// the PUT can be sent again. The active deployment is the earlier attempt of the
// same PUT. It is waited for and the PUT re-sent, rather than adopted, so the
// caller always gets the result of a deployment it submitted and observed.
func waitForActiveDeployment(
	ctx context.Context,
	deploymentDescription string,
	getProvisioningState func(ctx context.Context) (string, error),
) error {
	logger := ginkgo.GinkgoLogr
	logger.Info("deployment request was rejected with DeploymentActive by an Azure SDK retry because the earlier attempt of the same request was accepted; waiting for that deployment to finish before deploying again",
		"deployment", deploymentDescription)

	var lastState string
	var lastErr error
	err := wait.PollUntilContextCancel(ctx, StandardPollInterval, true, func(ctx context.Context) (bool, error) {
		state, err := getProvisioningState(ctx)
		if err != nil {
			var respErr *azcore.ResponseError
			if errors.As(err, &respErr) && respErr.StatusCode == http.StatusNotFound {
				lastState, lastErr = "NotFound", nil
				return true, nil
			}
			lastErr = err
			return false, nil
		}
		lastState, lastErr = state, nil
		return isTerminalProvisioningState(state), nil
	})
	if err != nil {
		return fmt.Errorf("failed waiting for active deployment %s to finish (last provisioning state %q, last error: %v), caused by: %w, error: %w",
			deploymentDescription, lastState, lastErr, context.Cause(ctx), err)
	}
	logger.Info("active deployment finished, deploying again", "deployment", deploymentDescription, "provisioningState", lastState)
	return nil
}

// maxDeploymentResubmissions bounds how many times beginDeploymentWithResubmit
// sends a deployment PUT again after DeploymentActive.
const maxDeploymentResubmissions = 3

// beginDeploymentWithResubmit sends a deployment PUT through begin. When an
// Azure SDK retry of that PUT, sent after an attempt whose outcome was unknown,
// is rejected with DeploymentActive because that earlier attempt was accepted,
// it waits for the active deployment to finish and sends the PUT again, at most
// maxDeploymentResubmissions times. Any other error, and DeploymentActive that
// no unknown-outcome attempt of this PUT can explain, is returned without
// resubmitting.
func beginDeploymentWithResubmit[T any](
	ctx context.Context,
	deploymentDescription string,
	begin func(ctx context.Context) (*runtime.Poller[T], error),
	getProvisioningState func(ctx context.Context) (string, error),
) (*runtime.Poller[T], error) {
	var conflicts []error
	for resubmissions := 0; ; resubmissions++ {
		createCtx, attempts := withRequestAttemptTracker(ctx)
		poller, err := begin(createCtx)
		if err == nil {
			return poller, nil
		}
		if !isDeploymentActiveError(err) || !attempts.RetriedAfterUnknownOutcome() {
			return nil, errors.Join(append(conflicts, err)...)
		}
		conflicts = append(conflicts, err)
		if resubmissions == maxDeploymentResubmissions {
			return nil, fmt.Errorf("deployment %s was still rejected with DeploymentActive after %d resubmissions: %w",
				deploymentDescription, maxDeploymentResubmissions, errors.Join(conflicts...))
		}
		if waitErr := waitForActiveDeployment(ctx, deploymentDescription, getProvisioningState); waitErr != nil {
			return nil, errors.Join(append(conflicts, waitErr)...)
		}
	}
}

// provisioningStateString returns an SDK provisioning state enum as a string,
// or "" if it is unset.
func provisioningStateString[S ~string](state *S) string {
	if state == nil {
		return ""
	}
	return string(*state)
}
