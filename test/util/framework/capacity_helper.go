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
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"time"

	"k8s.io/apimachinery/pkg/util/wait"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/runtime"

	"github.com/Azure/ARO-HCP/arohcp-cosmos-client/api/coreapi"
	"github.com/Azure/ARO-HCP/test/util/timing"
)

// capacityBackoffInterval is the minimum wait before a rejected cluster create is sent again.
const capacityBackoffInterval = time.Minute

// errCapacityBackoffLimitReached reports that the backoffs of one operation
// added up to its backoff limit.
var errCapacityBackoffLimitReached = errors.New("capacity backoff limit reached")

// isCapacityRejection reports whether the RP rejected a cluster create because
// regional capacity is exhausted. Nothing was persisted, so the create is safe
// to send again.
func isCapacityRejection(err error) bool {
	var responseError *azcore.ResponseError
	return errors.As(err, &responseError) && responseError.StatusCode == http.StatusBadRequest && responseError.ErrorCode == coreapi.CloudErrorCodeCapacityExceeded
}

// operationDeadline is the timeout of one operation as a point in time. It
// covers request submission, SDK retries, recovery and polling. A rejected
// attempt and the backoff after it extend it, so waiting for capacity does not
// count against the timeout. It is owned by one synchronous helper, never
// shared between goroutines.
type operationDeadline struct {
	// at is zero when the operation has no timeout.
	at time.Time
	// cause is reported when the deadline passes. It names the operation and
	// its timeout.
	cause error
}

// newOperationDeadline starts the timeout now. A non-positive timeout means no
// deadline. cause is reported when the deadline passes.
func newOperationDeadline(timeout time.Duration, cause error) *operationDeadline {
	deadline := &operationDeadline{cause: cause}
	if timeout > 0 {
		deadline.at = time.Now().Add(timeout)
	}
	return deadline
}

// context returns a context that ends at the deadline.
func (d *operationDeadline) context(ctx context.Context) (context.Context, context.CancelFunc) {
	if d.at.IsZero() {
		return context.WithCancel(ctx)
	}
	return context.WithDeadlineCause(ctx, d.at, d.cause)
}

// expired returns an error once the deadline has passed or ctx has ended.
func (d *operationDeadline) expired(ctx context.Context) error {
	if err := contextError(ctx); err != nil {
		return err
	}
	if !d.at.IsZero() && !time.Now().Before(d.at) {
		return errors.Join(context.DeadlineExceeded, d.cause)
	}
	return nil
}

// extend moves the deadline out by the time spent waiting for capacity.
func (d *operationDeadline) extend(wait time.Duration) {
	if !d.at.IsZero() {
		d.at = d.at.Add(wait)
	}
}

// contextError returns nil while ctx is live. Once ctx has ended it returns
// ctx.Err() together with the cause, when one was set.
func contextError(ctx context.Context) error {
	cause := context.Cause(ctx)
	if errors.Is(cause, ctx.Err()) {
		return cause
	}
	return errors.Join(ctx.Err(), cause)
}

// sleepWithContext waits for d or until ctx ends.
func sleepWithContext(ctx context.Context, d time.Duration) error {
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-ctx.Done():
	case <-timer.C:
	}
	return contextError(ctx)
}

// retryOnCapacityRejection runs attempt and, while it is rejected for lack of
// capacity, backs off and runs it again.
//
// Two limits apply. Each attempt runs against deadline, the timeout of the
// operation. The time lost to capacity, which is every rejected attempt and
// the backoff after it, is not counted against that timeout: it extends
// deadline by its own length. Instead, the time lost to capacity in one call
// may add up to at most backoffLimit. Every backoff is followed by an attempt,
// so the limit is reported by the attempt that is rejected once it is used up.
func retryOnCapacityRejection[T any](ctx context.Context, deadline *operationDeadline, backoffLimit time.Duration, isRejection func(error) bool, attempt func(context.Context) (T, error)) (T, error) {
	recorder := beginOperation()
	backoffLeft := backoffLimit
	var zero T
	var rejection error
	for {
		if err := deadline.expired(ctx); err != nil {
			return zero, errors.Join(err, rejection)
		}
		attemptStart := time.Now()
		attemptCtx, cancelAttempt := deadline.context(ctx)
		result, err := attempt(attemptCtx)
		attemptCtxErr := contextError(attemptCtx)
		cancelAttempt()
		if err == nil {
			return result, nil
		}
		if attemptCtxErr != nil {
			return zero, errors.Join(attemptCtxErr, err)
		}
		if !isRejection(err) {
			return result, err
		}
		rejection = err
		backoffLeft -= time.Since(attemptStart)
		if backoffLeft <= 0 {
			return zero, fmt.Errorf("waiting for regional HCP cluster capacity: %w", errors.Join(errCapacityBackoffLimitReached, rejection))
		}

		// Up to 50% jitter, so that creates rejected together do not return in
		// one wave and overshoot the limit together.
		start := time.Now()
		backoffErr := sleepWithContext(ctx, min(wait.Jitter(capacityBackoffInterval, 0.5), backoffLeft))
		backoff := time.Since(start)
		backoffLeft -= backoff
		deadline.extend(time.Since(attemptStart))
		recorder.Record(timing.KindCapacityBackoff, "Backoff for regional SWIFT/minimal cluster capacity", start, start.Add(backoff), backoffErr)
		if backoffErr != nil {
			return zero, fmt.Errorf("waiting for regional HCP cluster capacity: %w", errors.Join(backoffErr, rejection))
		}
	}
}

// capacityOnly reports whether every leaf of the error tree is a capacity
// rejection. ARM reports the failure of a nested deployment, which is what a
// Bicep module is, as a leaf whose message is the JSON of that deployment's
// operation status, so such a leaf is followed into its message.
func capacityOnly(e coreapi.CloudErrorBody) bool {
	if len(e.Details) == 0 {
		var nested struct {
			Error coreapi.CloudErrorBody `json:"error"`
		}
		if json.Unmarshal([]byte(e.Message), &nested) == nil && len(nested.Error.Code) > 0 {
			return capacityOnly(nested.Error)
		}
		return e.Code == coreapi.CloudErrorCodeCapacityExceeded
	}
	for _, detail := range e.Details {
		if !capacityOnly(detail) {
			return false
		}
	}
	return true
}

// A failed deployment wraps the RP rejection in the error of its operation
// status. Require a structured error tree whose leaves are all capacity
// rejections; messages mentioning capacity and mixed failures are not
// sufficient.
func isDeploymentCapacityRejection(err error) bool {
	var responseError *azcore.ResponseError
	if !errors.As(err, &responseError) {
		return false
	}
	payload, err := runtime.Payload(responseError.RawResponse)
	if err != nil {
		return false
	}
	var operationStatus struct {
		Error coreapi.CloudErrorBody `json:"error"`
	}
	if json.Unmarshal(payload, &operationStatus) != nil {
		return false
	}
	return capacityOnly(operationStatus.Error)
}

// deployWithCapacityRetry runs a Bicep deployment and resubmits it while it
// fails for lack of capacity. A transient polling error does not mean the
// deployment failed: only a deployment that finished, with nothing but
// capacity rejections, is submitted again.
func deployWithCapacityRetry[T any](ctx context.Context, timeout time.Duration, description string, begin func(context.Context) (*runtime.Poller[T], error), getState func(context.Context) (string, error)) (T, error) {
	finished := false
	isRejection := func(err error) bool {
		return finished && isDeploymentCapacityRejection(err)
	}
	deadline := newOperationDeadline(timeout, fmt.Errorf("timeout '%f' minutes exceeded during CreateBicepTemplateAndWait for deployment %s", timeout.Minutes(), description))
	return retryOnCapacityRejection(ctx, deadline, ClusterCapacityBackoffLimit, isRejection, func(attemptCtx context.Context) (T, error) {
		var zero T
		finished = false
		poller, err := beginDeploymentWithResubmit(attemptCtx, description, begin, getState)
		if err != nil {
			return zero, fmt.Errorf("failed creating deployment %s: %w", description, err)
		}
		result, err := poller.PollUntilDone(attemptCtx, &runtime.PollUntilDoneOptions{Frequency: StandardPollInterval})
		finished = poller.Done()
		if err != nil {
			return zero, fmt.Errorf("failed waiting for deployment %s to finish: %w", description, err)
		}
		return result, nil
	})
}
