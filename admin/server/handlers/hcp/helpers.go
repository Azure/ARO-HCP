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

package hcp

import (
	"errors"
	"fmt"
	"net/http"

	ocmerrors "github.com/openshift-online/ocm-sdk-go/errors"

	"github.com/Azure/ARO-HCP/internal/api/arm"
)

// ClusterServiceError checks if err is an OCM not-found error and returns a
// specific CloudError. This prevents ReportError from misinterpreting it as
// "HCP resource not found" (the HCP was already found in the database).
// Non-OCM errors are wrapped for ReportError to handle as internal errors.
func ClusterServiceError(err error, what string) error {
	var ocmErr *ocmerrors.Error
	if errors.As(err, &ocmErr) && ocmErr.Status() == http.StatusNotFound {
		return arm.NewCloudError(http.StatusNotFound, arm.CloudErrorCodeNotFound, "",
			"%s not found in cluster service", what)
	}
	return fmt.Errorf("failed to get %s from cluster service: %w", what, err)
}
