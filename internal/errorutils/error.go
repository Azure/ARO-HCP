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

package errorutils

import (
	"context"
	"errors"
	"net/http"

	ocmerrors "github.com/openshift-online/ocm-sdk-go/errors"

	"github.com/Azure/ARO-HCP/internal/api/arm"
	"github.com/Azure/ARO-HCP/internal/database"
	"github.com/Azure/ARO-HCP/internal/ocm"
	"github.com/Azure/ARO-HCP/internal/utils"
)

// erroringHTTPHandler is an http handler that leaves error reporting to a higher layer
// This allows the function itself to return errors for consistent logging and returning to users instead of
// leaving the error handling itself where we see problems with inconsistent logging, forgotten returns, and
// missing metadata.
type ErroringHTTPHandlerFunc func(w http.ResponseWriter, r *http.Request) error

// ReportError allows an http handler to have an error handling flow that is "normal" where encountered errors are
// returned.  If the error is non-nil, then the standard error reporting (special cases baked in for known types of errors)
// are logged and then reported to a client with an appropriate http code.
func ReportError(delegate ErroringHTTPHandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		err := delegate(w, r)
		if err == nil {
			return
		}

		ctx := r.Context()
		_ = writeError(ctx, w, err) // return is always nil
	}
}

// writeError handles any error correctly with the response writer and logs the failure to stdout.
// errors that are not cloud errors are assumed to be internal errors.
// The return value is always nil.  This allows direct usage in an http handler to local context
// and allows the same handler function to return an error
func writeError(ctx context.Context, w http.ResponseWriter, err error) error {
	logger := utils.LoggerFromContext(ctx)

	predictedResponseStatus := predictedResponseStatus(err)
	switch {
	case predictedResponseStatus >= 400 && predictedResponseStatus < 500:
		logger.Info("caller request error", "err", err)
	default:
		logger.Error(err, "server request error")
	}

	var ocmError *ocmerrors.Error
	if errors.As(err, &ocmError) {
		resourceID, _ := utils.ResourceIDFromContext(ctx) // used for error reporting
		cloudErr := ocm.CSErrorToCloudError(err, resourceID)
		arm.WriteCloudError(w, cloudErr)
		return nil
	}

	var cloudErr *arm.CloudError
	if err != nil && errors.As(err, &cloudErr) {
		if cloudErr != nil { // difference between interface is nil and the content is nil
			arm.WriteCloudError(w, cloudErr)
			return nil
		}
	}

	if database.IsResponseError(err, http.StatusNotFound) {
		resourceID, err := utils.ResourceIDFromContext(ctx) // used for error reporting
		if err != nil {
			arm.WriteInternalServerError(w)
			return nil
		}
		arm.WriteCloudError(w, arm.NewResourceNotFoundError(resourceID))
		return nil
	}

	arm.WriteInternalServerError(w)
	return nil
}

// predictedResponseStatus needs to be mostly right, but not perfect.  We use it to control the log level of the error we print.
func predictedResponseStatus(err error) int {
	var ocmError *ocmerrors.Error
	if errors.As(err, &ocmError) {
		cloudErr := ocm.CSErrorToCloudError(err, nil)
		return cloudErr.StatusCode
	}

	var cloudErr *arm.CloudError
	if err != nil && errors.As(err, &cloudErr) {
		if cloudErr != nil { // difference between interface is nil and the content is nil
			return cloudErr.StatusCode
		}
	}

	if database.IsResponseError(err, http.StatusNotFound) {
		return http.StatusNotFound
	}

	return http.StatusInternalServerError
}
