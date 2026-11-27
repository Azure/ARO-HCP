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
	"errors"
	"fmt"
	"net/http"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"

	"github.com/Azure/ARO-HCP/arohcp-cosmos-client/api/coreapi"
)

// listProbeNoRegisteredProviderFoundCode is the ARM error code for a resource
// provider that does not serve the requested API version.
const listProbeNoRegisteredProviderFoundCode = "NoRegisteredProviderFound"

// ClassifyListBySubscriptionProbeError classifies the error from the first page
// of a subscription-scoped list call. The call probes whether the service
// serves an API version. served is false only for HTTP 400 with the exact code
// InvalidResourceType or NoRegisteredProviderFound. The function returns all
// other errors, and every 404. A subscription-scoped list has no resource name.
// Thus a 404 shows a subscription or configuration problem, not an API version
// that is not served.
func ClassifyListBySubscriptionProbeError(err error) (served bool, _ error) {
	if err == nil {
		return true, nil
	}
	var respErr *azcore.ResponseError
	if errors.As(err, &respErr) && respErr.StatusCode == http.StatusBadRequest {
		switch respErr.ErrorCode {
		case coreapi.CloudErrorCodeInvalidResourceType, listProbeNoRegisteredProviderFoundCode:
			return false, nil
		}
	}
	return false, fmt.Errorf("failed to probe whether the API version is served: %w", err)
}
