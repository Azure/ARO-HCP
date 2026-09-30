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
	"io"
	"net/http"
	"time"

	"github.com/onsi/ginkgo/v2"

	"k8s.io/apimachinery/pkg/util/wait"
)

// DownloadManifest downloads a manifest, retrying network/read errors, HTTP 429,
// and HTTP 5xx responses with at most five attempts within a five-minute total budget.
func DownloadManifest(ctx context.Context, url string) ([]byte, error) {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Minute)
	defer cancel()
	return downloadManifest(ctx, &http.Client{Timeout: 2 * time.Minute}, url, wait.Backoff{
		Duration: 5 * time.Second,
		Factor:   2,
		Steps:    5,
	})
}

func downloadManifest(ctx context.Context, client *http.Client, url string, backoff wait.Backoff) ([]byte, error) {
	var body []byte
	var lastErr error
	var attempts int
	err := wait.ExponentialBackoffWithContext(ctx, backoff, func(ctx context.Context) (bool, error) {
		attempts++
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
		if err != nil {
			return false, err
		}
		resp, err := client.Do(req)
		if err == nil {
			defer resp.Body.Close()
			if resp.StatusCode != http.StatusOK {
				err = fmt.Errorf("HTTP %d", resp.StatusCode)
				if resp.StatusCode != http.StatusTooManyRequests && (resp.StatusCode < 500 || resp.StatusCode >= 600) {
					return false, err
				}
			} else {
				body, err = io.ReadAll(resp.Body)
				if err == nil {
					return true, nil
				}
			}
		}
		if ctx.Err() != nil {
			return false, ctx.Err()
		}
		if lastErr == nil || lastErr.Error() != err.Error() {
			ginkgo.GinkgoLogr.Info("manifest download failed; retrying", "url", url, "attempt", attempts, "error", err.Error())
		}
		lastErr = err
		return false, nil
	})
	if err != nil {
		if wait.Interrupted(err) && lastErr != nil {
			err = errors.Join(err, fmt.Errorf("last download error: %w", lastErr))
		}
		return nil, fmt.Errorf("fetching manifest %s after %d attempts: %w", url, attempts, err)
	}
	return body, nil
}
