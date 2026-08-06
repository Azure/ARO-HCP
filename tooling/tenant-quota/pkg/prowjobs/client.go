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

package prowjobs

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

const maxResponseErrorBytes = 2048

type Metadata struct {
	Name string `json:"name,omitempty"`
}

type Refs struct {
	Org  string `json:"org,omitempty"`
	Repo string `json:"repo,omitempty"`
}

type Spec struct {
	Type      string `json:"type,omitempty"`
	Job       string `json:"job,omitempty"`
	Refs      *Refs  `json:"refs,omitempty"`
	ExtraRefs []Refs `json:"extra_refs,omitempty"`
}

type Status struct {
	State          string    `json:"state,omitempty"`
	StartTime      time.Time `json:"startTime,omitempty"`
	CompletionTime time.Time `json:"completionTime,omitempty"`
	URL            string    `json:"url,omitempty"`
	BuildID        string    `json:"build_id,omitempty"`
}

type Job struct {
	Metadata Metadata `json:"metadata,omitempty"`
	Spec     Spec     `json:"spec,omitempty"`
	Status   Status   `json:"status,omitempty"`
}

type Client interface {
	ListJobs(ctx context.Context) ([]Job, error)
}

type HTTPClient struct {
	baseURL    string
	httpClient *http.Client
}

func NewHTTPClient(baseURL string, timeout time.Duration) *HTTPClient {
	return &HTTPClient{
		baseURL: strings.TrimRight(strings.TrimSpace(baseURL), "/"),
		httpClient: &http.Client{
			Timeout: timeout,
		},
	}
}

func (c *HTTPClient) ListJobs(ctx context.Context) ([]Job, error) {
	endpoint, err := c.endpoint()
	if err != nil {
		return nil, err
	}

	const maxAttempts = 3
	var lastErr error
	for attempt := 1; attempt <= maxAttempts; attempt++ {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
		if err != nil {
			return nil, fmt.Errorf("build Prow jobs request: %w", err)
		}
		req.Header.Set("Accept", "application/json, application/javascript, text/javascript")

		resp, err := c.httpClient.Do(req)
		if err != nil {
			lastErr = fmt.Errorf("prow jobs request failed: %w", err)
		} else {
			if resp.StatusCode == http.StatusOK {
				jobs, err := decodeJobList(resp.Body)
				resp.Body.Close()
				if err != nil {
					return nil, fmt.Errorf("decode Prow jobs response: %w", err)
				}
				return jobs, nil
			}
			body, readErr := io.ReadAll(io.LimitReader(resp.Body, maxResponseErrorBytes))
			resp.Body.Close()
			if readErr != nil {
				return nil, fmt.Errorf("read Prow jobs error response: %w", readErr)
			}
			lastErr = fmt.Errorf(
				"prow jobs request returned status %d: %s",
				resp.StatusCode,
				strings.TrimSpace(string(body)),
			)
			if !isRetryableStatus(resp.StatusCode) {
				return nil, lastErr
			}
		}

		if attempt < maxAttempts {
			timer := time.NewTimer(time.Duration(attempt) * time.Second)
			select {
			case <-ctx.Done():
				timer.Stop()
				return nil, ctx.Err()
			case <-timer.C:
			}
		}
	}

	return nil, lastErr
}

func (c *HTTPClient) endpoint() (string, error) {
	if c.baseURL == "" {
		return "", fmt.Errorf("prow base URL is required")
	}
	parsed, err := url.Parse(c.baseURL)
	if err != nil {
		return "", fmt.Errorf("parse Prow base URL: %w", err)
	}
	if parsed.Scheme != "http" && parsed.Scheme != "https" {
		return "", fmt.Errorf("prow base URL must use http or https")
	}
	if strings.HasSuffix(strings.ToLower(parsed.Path), ".js") {
		return parsed.String(), nil
	}
	parsed.Path = strings.TrimRight(parsed.Path, "/") + "/prowjobs.js"
	parsed.RawQuery = ""
	parsed.Fragment = ""
	return parsed.String(), nil
}

func decodeJobList(reader io.Reader) ([]Job, error) {
	jsonReader, err := readerFromJSONObject(reader)
	if err != nil {
		return nil, err
	}

	decoder := json.NewDecoder(jsonReader)
	token, err := decoder.Token()
	if err != nil {
		return nil, err
	}
	if delimiter, ok := token.(json.Delim); !ok || delimiter != '{' {
		return nil, fmt.Errorf("response root is not a JSON object")
	}

	jobs := make([]Job, 0, 2048)
	for decoder.More() {
		keyToken, err := decoder.Token()
		if err != nil {
			return nil, err
		}
		key, ok := keyToken.(string)
		if !ok {
			return nil, fmt.Errorf("response object key is not a string")
		}
		if key != "items" {
			var ignored json.RawMessage
			if err := decoder.Decode(&ignored); err != nil {
				return nil, err
			}
			continue
		}

		token, err := decoder.Token()
		if err != nil {
			return nil, err
		}
		if delimiter, ok := token.(json.Delim); !ok || delimiter != '[' {
			return nil, fmt.Errorf("items is not a JSON array")
		}
		for decoder.More() {
			var job Job
			if err := decoder.Decode(&job); err != nil {
				return nil, err
			}
			jobs = append(jobs, job)
		}
		if _, err := decoder.Token(); err != nil {
			return nil, err
		}
	}

	if _, err := decoder.Token(); err != nil {
		return nil, err
	}
	return jobs, nil
}

func readerFromJSONObject(reader io.Reader) (io.Reader, error) {
	buffered := bufio.NewReader(reader)
	const maxPrefixBytes = 1024 * 1024
	for i := 0; i < maxPrefixBytes; i++ {
		value, err := buffered.ReadByte()
		if err != nil {
			if err == io.EOF {
				return nil, fmt.Errorf("response body does not contain a JSON object")
			}
			return nil, err
		}
		if value == '{' {
			return io.MultiReader(bytes.NewReader([]byte{'{'}), buffered), nil
		}
	}
	return nil, fmt.Errorf("response JSON object starts after the %d byte prefix limit", maxPrefixBytes)
}

func IsTerminalState(state string) bool {
	switch NormalizeState(state) {
	case "success", "failure", "error":
		return true
	default:
		return false
	}
}

func NormalizeState(state string) string {
	return strings.ToLower(strings.TrimSpace(state))
}

func isRetryableStatus(statusCode int) bool {
	return statusCode == http.StatusRequestTimeout ||
		statusCode == http.StatusTooManyRequests ||
		statusCode >= http.StatusInternalServerError
}
