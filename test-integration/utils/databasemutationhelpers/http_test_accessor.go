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
	"fmt"
	"io"
	"net/http"

	"sigs.k8s.io/yaml"

	"github.com/Azure/ARO-HCP/internal/utils"
)

type HTTPTestAccessor interface {
	Get(ctx context.Context, resourceIDString string) (any, error)
	List(ctx context.Context, parentResourceIDString string) ([]any, error)
	CreateOrUpdate(ctx context.Context, resourceIDString string, content []byte) error
	Post(ctx context.Context, resourceIDString string, content []byte) error
	Patch(ctx context.Context, resourceIDString string, content []byte) error
	Delete(ctx context.Context, resourceIDString string) error
}

type httpHTTPTestAccessor struct {
	url     string
	headers map[string]string
}

func newHTTPTestAccessor(url string, headers map[string]string) *httpHTTPTestAccessor {
	return &httpHTTPTestAccessor{
		url:     url,
		headers: headers,
	}
}

var _ HTTPTestAccessor = &httpHTTPTestAccessor{}

func (a *httpHTTPTestAccessor) Get(ctx context.Context, resourceIDString string) (any, error) {
	return a.doRequest(ctx, http.MethodGet, resourceIDString, nil)
}

func (a *httpHTTPTestAccessor) List(ctx context.Context, parentResourceIDString string) ([]any, error) {
	return nil, utils.TrackError(fmt.Errorf("not implemented yet"))
}

func (a *httpHTTPTestAccessor) CreateOrUpdate(ctx context.Context, resourceIDString string, content []byte) error {
	_, err := a.doRequest(ctx, http.MethodPut, resourceIDString, content)
	return err
}

func (a *httpHTTPTestAccessor) Post(ctx context.Context, resourceIDString string, content []byte) error {
	_, err := a.doRequest(ctx, http.MethodPost, resourceIDString, content)
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

func (a *httpHTTPTestAccessor) doRequest(ctx context.Context, method, path string, body []byte) (any, error) {
	logger := utils.LoggerFromContext(ctx)

	var reqBody io.Reader
	if len(body) > 0 {
		reqBody = bytes.NewReader(body)
	}

	req, err := http.NewRequestWithContext(ctx, method, a.url+path, reqBody)
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
		return nil, utils.TrackError(fmt.Errorf("HTTP %d: %s", resp.StatusCode, string(bodyBytes)))
	}

	if len(bodyBytes) == 0 {
		return nil, nil
	}

	var result map[string]any

	// handles both JSON and YAML
	if err := yaml.Unmarshal(bodyBytes, &result); err != nil {
		return nil, utils.TrackError(err)
	}

	return result, nil
}
