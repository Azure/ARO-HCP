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

package databasemutationhelpers

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/Azure/ARO-HCP/internal/api"
	"github.com/Azure/ARO-HCP/internal/utils"
)

type HTTPTestAccessor interface {
	Get(ctx context.Context, resourceIDString string) (any, error)
	List(ctx context.Context, parentResourceIDString string) ([]any, error)
	CreateOrUpdate(ctx context.Context, resourceIDString string, content []byte) error
	Patch(ctx context.Context, resourceIDString string, content []byte) error
	Delete(ctx context.Context, resourceIDString string) error
}

type httpHTTPTestAccessor struct {
	url        string
	headers    map[string]string
	apiVersion string
}

func newHTTPTestAccessor(url string, headers map[string]string) *httpHTTPTestAccessor {
	return &httpHTTPTestAccessor{
		url:     url,
		headers: headers,
	}
}

// NewVersionedHTTPTestAccessor creates an HTTP test accessor that sends requests
// with the given api-version query parameter and the required ARM headers.
func NewVersionedHTTPTestAccessor(url, apiVersion string) *httpHTTPTestAccessor {
	return &httpHTTPTestAccessor{
		url:        url,
		apiVersion: apiVersion,
		headers: map[string]string{
			"X-Ms-Arm-Resource-System-Data": "{}",
			"X-Ms-Home-Tenant-Id":           api.TestTenantID,
			"Content-Type":                  "application/json",
		},
	}
}

var _ HTTPTestAccessor = &httpHTTPTestAccessor{}

func (a *httpHTTPTestAccessor) Get(ctx context.Context, resourceIDString string) (any, error) {
	return a.doRequest(ctx, http.MethodGet, resourceIDString, nil)
}

func (a *httpHTTPTestAccessor) List(ctx context.Context, exemplarResourceIDString string) ([]any, error) {
	// The exemplar resource ID includes a dummy name (e.g., ".../nodePools/dummy").
	// Strip the last path segment to get the collection URL (e.g., ".../nodePools").
	lastSlash := strings.LastIndex(exemplarResourceIDString, "/")
	if lastSlash <= 0 {
		return nil, utils.TrackError(fmt.Errorf("invalid exemplar resource ID for listing: %s", exemplarResourceIDString))
	}
	collectionPath := exemplarResourceIDString[:lastSlash]

	result, err := a.doRequest(ctx, http.MethodGet, collectionPath, nil)
	if err != nil {
		return nil, err
	}
	resultMap, ok := result.(map[string]any)
	if !ok {
		return nil, utils.TrackError(fmt.Errorf("unexpected response type: %T", result))
	}
	valueRaw, ok := resultMap["value"]
	if !ok {
		return nil, utils.TrackError(fmt.Errorf("response missing 'value' field"))
	}
	valueSlice, ok := valueRaw.([]any)
	if !ok {
		return nil, utils.TrackError(fmt.Errorf("'value' field is not an array: %T", valueRaw))
	}
	return valueSlice, nil
}

func (a *httpHTTPTestAccessor) CreateOrUpdate(ctx context.Context, resourceIDString string, content []byte) error {
	_, err := a.doRequest(ctx, http.MethodPut, resourceIDString, content)
	return err
}

func (a *httpHTTPTestAccessor) Patch(ctx context.Context, resourceIDString string, content []byte) error {
	_, err := a.doRequest(ctx, http.MethodPatch, resourceIDString, content)
	return err
}

func (a *httpHTTPTestAccessor) Delete(ctx context.Context, resourceIDString string) error {
	_, err := a.doRequest(ctx, http.MethodDelete, resourceIDString, nil)
	return err
}

// operationStatusLocation is the location used in integration tests.
// Operation status resources require a location segment in the URL that
// is not present in the stored resource ID.
const operationStatusLocation = "fake-location"

func (a *httpHTTPTestAccessor) doRequest(ctx context.Context, method, path string, body []byte) (any, error) {
	logger := utils.LoggerFromContext(ctx)

	var reqBody io.Reader
	if len(body) > 0 {
		reqBody = bytes.NewReader(body)
	}

	// Operation status resources require a location segment in the URL.
	// The stored resource ID is .../providers/Microsoft.RedHatOpenShift/hcpOperationStatuses/{id}
	// but the API route expects .../providers/Microsoft.RedHatOpenShift/locations/{location}/hcpOperationStatuses/{id}
	if len(a.apiVersion) != 0 {
		opStatusSegment := "/" + api.OperationStatusResourceTypeName + "/"
		if idx := strings.Index(strings.ToLower(path), strings.ToLower(opStatusSegment)); idx >= 0 {
			path = path[:idx] + "/locations/" + operationStatusLocation + path[idx:]
		}
	}

	fullURL := a.url + path
	if len(a.apiVersion) != 0 {
		sep := "?"
		if strings.Contains(fullURL, "?") {
			sep = "&"
		}
		fullURL = fullURL + sep + "api-version=" + a.apiVersion
	}

	req, err := http.NewRequestWithContext(ctx, method, fullURL, reqBody)
	if err != nil {
		return nil, utils.TrackError(err)
	}

	for key, value := range a.headers {
		req.Header.Set(key, value)
	}

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, utils.TrackError(err)
	}
	defer func() {
		if err := resp.Body.Close(); err != nil {
			logger.Error(err, "failed to close response body")
		}
	}()

	bodyBytes, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, utils.TrackError(err)
	}

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		// Re-indent the response body with 2-space indentation to match the
		// format that the Azure SDK's ResponseError.Error() produces (via
		// json.Indent with no prefix and "  " indent). The expected-error.txt
		// fixture files are written against this format.
		var indented bytes.Buffer
		if err := json.Indent(&indented, bodyBytes, "", "  "); err == nil {
			return nil, utils.TrackError(fmt.Errorf("HTTP %d: %s", resp.StatusCode, indented.String()))
		}
		return nil, utils.TrackError(fmt.Errorf("HTTP %d: %s", resp.StatusCode, string(bodyBytes)))
	}

	if len(bodyBytes) == 0 {
		return nil, nil
	}

	var result map[string]any
	if err := json.Unmarshal(bodyBytes, &result); err != nil {
		return nil, utils.TrackError(err)
	}

	return result, nil
}
