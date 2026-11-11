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

package verifiers

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"k8s.io/client-go/rest"
)

// TestVerifyCiliumOperationalContextExpirationMidRequest covers the case where
// the polling context's deadline fires while a pod List is in flight. client-go
// surfaces that as "client rate limiter Wait returned an error: context
// deadline exceeded" even though no throttling occurred, and before the fix
// that message replaced the verifier's last real observation in the timeout
// report. The verifier must report the context expiration itself instead.
func TestVerifyCiliumOperationalContextExpirationMidRequest(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()

	config := &rest.Config{
		Host: "https://api.example",
		Transport: imagePullRoundTripper(func(request *http.Request) (*http.Response, error) {
			if strings.HasSuffix(request.URL.Path, "/pods") {
				// Simulate client-go when the deadline fires mid-request:
				// the rate limiter returns the expired context error before
				// reserving any tokens.
				<-request.Context().Done()
				return nil, fmt.Errorf("client rate limiter Wait returned an error: %w", request.Context().Err())
			}
			return &http.Response{
				StatusCode: http.StatusOK,
				Header:     http.Header{"Content-Type": {"application/json"}},
				Body:       io.NopCloser(strings.NewReader(`{"apiVersion":"v1","kind":"Namespace","metadata":{"name":"kube-system"}}`)),
			}, nil
		}),
	}

	err := VerifyCiliumOperational("kube-system", "").Verify(ctx, config)
	require.Error(t, err)
	require.NotContains(t, err.Error(), "rate limiter")
	require.ErrorIs(t, err, context.DeadlineExceeded)
}
