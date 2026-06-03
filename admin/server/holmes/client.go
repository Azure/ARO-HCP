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

package holmes

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"

	"k8s.io/client-go/rest"
)

const maxResponseSize = 10 * 1024 * 1024 // 10MB

type chatRequest struct {
	Ask   string `json:"ask"`
	Model string `json:"model,omitempty"`
}

type chatResponse struct {
	Analysis string `json:"analysis"`
}

func AskHolmes(ctx context.Context, endpoint, question, model string, w http.ResponseWriter) error {
	return AskHolmesWithClient(ctx, http.DefaultClient, endpoint, question, model, w)
}

func AskHolmesWithClient(ctx context.Context, httpClient *http.Client, endpoint, question, model string, w http.ResponseWriter) error {
	reqBody, err := json.Marshal(chatRequest{Ask: question, Model: model})
	if err != nil {
		return fmt.Errorf("failed to marshal chat request: %w", err)
	}

	url := strings.TrimRight(endpoint, "/") + "/api/chat"
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, strings.NewReader(string(reqBody)))
	if err != nil {
		return fmt.Errorf("failed to create request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("failed to call Holmes service: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 1024))
		return fmt.Errorf("Holmes service returned HTTP %d: %s", resp.StatusCode, string(body))
	}

	body, err := io.ReadAll(io.LimitReader(resp.Body, maxResponseSize))
	if err != nil {
		return fmt.Errorf("failed to read Holmes response: %w", err)
	}

	var chatResp chatResponse
	if err := json.Unmarshal(body, &chatResp); err == nil && chatResp.Analysis != "" {
		body = []byte(chatResp.Analysis)
	}

	w.Header().Set("Content-Type", "text/plain")
	w.Header().Set("Cache-Control", "no-cache")
	if _, err := w.Write(body); err != nil {
		return fmt.Errorf("failed to write response: %w", err)
	}

	return nil
}

func ServiceProxyURL(restConfig *rest.Config, namespace, serviceName string) string {
	host := strings.TrimRight(restConfig.Host, "/")
	return fmt.Sprintf("%s/api/v1/namespaces/%s/services/%s:80/proxy", host, namespace, serviceName)
}

func HTTPClientForRESTConfig(restConfig *rest.Config) (*http.Client, error) {
	transport, err := rest.TransportFor(restConfig)
	if err != nil {
		return nil, fmt.Errorf("failed to create transport from REST config: %w", err)
	}
	return &http.Client{Transport: transport}, nil
}
