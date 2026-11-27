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

package util

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"time"

	"github.com/go-logr/logr"
	abstractions "github.com/microsoft/kiota-abstractions-go"

	"k8s.io/apimachinery/pkg/util/wait"

	"github.com/Azure/ARO-HCP/internal/graph/graphsdk/models/odataerrors"
)

const (
	deletedItemPollInterval       = time.Second
	deletedItemPropagationTimeout = 30 * time.Second
	deletedItemRestoreTimeout     = 30 * time.Second
)

// applicationDeleteConfirmBackoff paces the DELETE and confirmation GET while
// Graph still returns the application: 6 attempts with about 31s of waiting
// between them, to stay inside Graph's low throttling budget. There is no
// separate deadline: app cleanup is the last cleanup step, and giving up early
// under throttling would only leak the application.
var applicationDeleteConfirmBackoff = wait.Backoff{
	Duration: time.Second,
	Factor:   2,
	Jitter:   0.1,
	Steps:    6,
}

var errApplicationStillExists = errors.New("application still exists after delete")

// ApplicationCleanupTarget identifies an application registration and its
// associated service principal by their directory object IDs.
type ApplicationCleanupTarget struct {
	ApplicationObjectID      string
	ServicePrincipalObjectID string
}

// CleanupApplications removes application registrations and their associated
// service principals, permanently when the caller uses app-only credentials.
func (c *Client) CleanupApplications(ctx context.Context, targets []ApplicationCleanupTarget) error {
	var errs []error
	for _, target := range targets {
		if err := c.DeleteApplicationPermanently(ctx, target); err != nil {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}

// DeleteApplicationPermanently removes the service principal first, then the
// application, and purges both objects from deletedItems.
func (c *Client) DeleteApplicationPermanently(ctx context.Context, target ApplicationCleanupTarget) error {
	if target.ApplicationObjectID == "" {
		return fmt.Errorf("application object ID is required")
	}

	if c.isUser {
		err := c.DeleteApplication(ctx, target.ApplicationObjectID)
		if isODataStatus(err, http.StatusNotFound) {
			return nil
		}
		if err != nil {
			return err
		}
		logr.FromContextOrDiscard(ctx).V(1).Info(
			"Skipping permanent deletion because delegated credentials may not have deletedItems permissions",
			"applicationObjectID", target.ApplicationObjectID,
			"servicePrincipalObjectID", target.ServicePrincipalObjectID,
		)
		return nil
	}

	if target.ServicePrincipalObjectID != "" {
		servicePrincipalWasDeleted := true
		if err := c.DeleteServicePrincipal(ctx, target.ServicePrincipalObjectID); err != nil {
			if !isODataStatus(err, http.StatusNotFound) {
				return err
			}
			servicePrincipalWasDeleted = false
		}

		if err := c.permanentlyDeleteDirectoryObject(
			ctx,
			target.ServicePrincipalObjectID,
			deletedItemPollInterval,
			deletedItemPropagationTimeout,
			servicePrincipalWasDeleted,
		); err != nil {
			if isInsufficientPrivileges(err) {
				logr.FromContextOrDiscard(ctx).V(1).Info(
					"Skipping permanent deletion because this app-only credential lacks deletedItems purge permissions; leaving item soft-deleted",
					"servicePrincipalObjectID", target.ServicePrincipalObjectID,
				)
			} else {
				purgeErr := fmt.Errorf("purge service principal %q: %w", target.ServicePrincipalObjectID, err)
				if restoreErr := c.restoreDeletedDirectoryObject(ctx, target.ServicePrincipalObjectID); restoreErr != nil {
					return errors.Join(
						purgeErr,
						fmt.Errorf("restore service principal %q after failed purge: %w", target.ServicePrincipalObjectID, restoreErr),
					)
				}
				return purgeErr
			}
		}
	}

	applicationWasDeleted, err := c.deleteApplicationAndConfirm(ctx, target.ApplicationObjectID, applicationDeleteConfirmBackoff)
	if err != nil {
		return err
	}

	if err := c.permanentlyDeleteDirectoryObject(
		ctx,
		target.ApplicationObjectID,
		deletedItemPollInterval,
		deletedItemPropagationTimeout,
		applicationWasDeleted,
	); err != nil {
		if isInsufficientPrivileges(err) {
			logr.FromContextOrDiscard(ctx).V(1).Info(
				"Skipping permanent deletion because this app-only credential lacks deletedItems purge permissions; leaving item soft-deleted",
				"applicationObjectID", target.ApplicationObjectID,
			)
			return nil
		}
		purgeErr := fmt.Errorf("purge application %q: %w", target.ApplicationObjectID, err)
		if restoreErr := c.restoreDeletedDirectoryObject(ctx, target.ApplicationObjectID); restoreErr != nil {
			return errors.Join(
				purgeErr,
				fmt.Errorf("restore application %q after failed purge: %w", target.ApplicationObjectID, restoreErr),
			)
		}
		return purgeErr
	}

	return nil
}

// deleteApplicationAndConfirm deletes an application and confirms with a GET
// that it is gone. Right after its service principal is deleted, Graph can
// answer the application DELETE with 404 while the application stays active,
// so neither the DELETE response nor a 404 is proof of deletion. It repeats the
// DELETE, with backoff, until the GET returns 404 or the attempts run out. The
// returned bool reports whether any DELETE call succeeded, which tells the
// purge step to wait for the object to appear in deletedItems.
func (c *Client) deleteApplicationAndConfirm(ctx context.Context, objectID string, backoff wait.Backoff) (bool, error) {
	logger := logr.FromContextOrDiscard(ctx)
	deleted := false
	// requestErr records a DELETE/GET failure, so that a request error that
	// looks like a timeout (the HTTP client's own timeout matches
	// context.DeadlineExceeded) is never reported as the attempts running out.
	var requestErr error
	err := wait.ExponentialBackoffWithContext(ctx, backoff, func(ctx context.Context) (bool, error) {
		deleteErr := c.DeleteApplication(ctx, objectID)
		if deleteErr == nil {
			deleted = true
		} else if !isODataStatus(deleteErr, http.StatusNotFound) {
			requestErr = deleteErr
			return false, requestErr
		}

		_, getErr := c.graphClient.Applications().ByApplicationId(objectID).Get(ctx, nil)
		if isODataStatus(getErr, http.StatusNotFound) {
			return true, nil
		}
		if getErr != nil {
			requestErr = fmt.Errorf("confirm deletion of application %q: %w", objectID, odataErrorWithDiagnostics(getErr))
			return false, requestErr
		}

		logger.Info(
			"Application still exists after delete; retrying",
			"applicationObjectID", objectID,
			"deleteReturnedNotFound", deleteErr != nil,
		)
		return false, nil
	})
	// Without a request error or a canceled context, an error means every
	// attempt ran and the application was still there.
	if err != nil && requestErr == nil && ctx.Err() == nil {
		return deleted, fmt.Errorf("application %q not confirmed deleted after %d attempts: %w", objectID, backoff.Steps, errApplicationStillExists)
	}
	return deleted, err
}

func (c *Client) restoreDeletedDirectoryObject(ctx context.Context, objectID string) error {
	restoreCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), deletedItemRestoreTimeout)
	defer cancel()

	requestInfo := abstractions.NewRequestInformationWithMethodAndUrlTemplateAndPathParameters(
		abstractions.POST,
		"{+baseurl}/directory/deletedItems/{directoryObjectId}/restore",
		map[string]string{
			"baseurl":           c.graphClient.RequestAdapter.GetBaseUrl(),
			"directoryObjectId": objectID,
		},
	)
	requestInfo.Headers.TryAdd("Accept", "application/json")

	err := c.graphClient.RequestAdapter.SendNoContent(
		restoreCtx,
		requestInfo,
		abstractions.ErrorMappings{
			"XXX": odataerrors.CreateODataErrorFromDiscriminatorValue,
		},
	)
	if err != nil {
		return odataErrorWithDiagnostics(err)
	}
	return nil
}

func (c *Client) permanentlyDeleteDirectoryObject(
	ctx context.Context,
	objectID string,
	pollInterval time.Duration,
	propagationTimeout time.Duration,
	requireAppearance bool,
) error {
	if objectID == "" {
		return fmt.Errorf("directory object ID is required")
	}

	purgeCtx, cancel := context.WithTimeout(ctx, propagationTimeout)
	defer cancel()

	sawNotFound := false
	for {
		requestInfo := abstractions.NewRequestInformationWithMethodAndUrlTemplateAndPathParameters(
			abstractions.DELETE,
			"{+baseurl}/directory/deletedItems/{directoryObjectId}",
			map[string]string{
				"baseurl":           c.graphClient.RequestAdapter.GetBaseUrl(),
				"directoryObjectId": objectID,
			},
		)
		requestInfo.Headers.TryAdd("Accept", "application/json")

		err := c.graphClient.RequestAdapter.SendNoContent(
			purgeCtx,
			requestInfo,
			abstractions.ErrorMappings{
				"XXX": odataerrors.CreateODataErrorFromDiscriminatorValue,
			},
		)
		if err == nil {
			return nil
		}
		if purgeCtx.Err() != nil {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			if sawNotFound && !requireAppearance {
				return nil
			}
			return fmt.Errorf("directory object did not appear in deletedItems before timeout: %w", purgeCtx.Err())
		}
		if !isODataStatus(err, http.StatusNotFound) {
			return odataErrorWithDiagnostics(err)
		}
		sawNotFound = true

		timer := time.NewTimer(pollInterval)
		select {
		case <-purgeCtx.Done():
			timer.Stop()
			if ctx.Err() != nil {
				return ctx.Err()
			}
			if !requireAppearance {
				return nil
			}
			return fmt.Errorf("directory object did not appear in deletedItems before timeout: %w", purgeCtx.Err())
		case <-timer.C:
		}
	}
}

func isODataStatus(err error, statusCode int) bool {
	var odataErr *odataerrors.ODataError
	return errors.As(err, &odataErr) && odataErr.ResponseStatusCode == statusCode
}

// isInsufficientPrivileges reports whether err is a Microsoft Graph 403
// Authorization_RequestDenied response, indicating the calling credential
// lacks the deletedItems purge permission grant rather than encountering a
// transient failure. Some app-only credentials (e.g. per-environment E2E
// service principals) may not have been granted this permission; permanent
// purge is a best-effort optimization on top of the underlying soft-delete,
// so this case must not fail the caller.
func isInsufficientPrivileges(err error) bool {
	var odataErr *odataerrors.ODataError
	if !errors.As(err, &odataErr) || odataErr.ResponseStatusCode != http.StatusForbidden {
		return false
	}
	odataError := odataErr.GetErrorEscaped()
	if odataError == nil {
		return false
	}
	code := odataError.GetCode()
	return code != nil && *code == "Authorization_RequestDenied"
}
