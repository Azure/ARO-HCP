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

// TestVerifyAllClusterOperatorsAvailableContextExpirationMidRequest covers the
// case where the polling context's deadline fires while a ClusterOperator List
// is in flight. client-go surfaces that as "client rate limiter Wait returned
// an error: context deadline exceeded" even though no throttling occurred, and
// before the fix that message replaced the verifier's last real observation in
// the timeout report. The verifier must report the context expiration itself.
func TestVerifyAllClusterOperatorsAvailableContextExpirationMidRequest(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()

	config := &rest.Config{
		Host: "https://api.example",
		Transport: imagePullRoundTripper(func(request *http.Request) (*http.Response, error) {
			// Simulate client-go when the deadline fires mid-request: the rate
			// limiter returns the expired context error before reserving any
			// tokens.
			<-request.Context().Done()
			return nil, fmt.Errorf("client rate limiter Wait returned an error: %w", request.Context().Err())
		}),
	}

	err := VerifyAllClusterOperatorsAvailable().Verify(ctx, config)
	require.Error(t, err)
	require.NotContains(t, err.Error(), "rate limiter")
	require.ErrorIs(t, err, context.DeadlineExceeded)
}

// TestVerifyAllClusterOperatorsAvailableContextExpirationAfterObservation
// covers the case where the polling context's deadline fires mid-request after
// an earlier poll already recorded a real observation. The final
// errors.Join must keep the observation while the context expiration stays
// identifiable via errors.Is.
func TestVerifyAllClusterOperatorsAvailableContextExpirationAfterObservation(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()

	unavailableOperators := `{"apiVersion":"config.openshift.io/v1","kind":"ClusterOperatorList","items":[{"metadata":{"name":"kube-apiserver"},"status":{"conditions":[{"type":"Available","status":"False","reason":"Test","message":"unavailable"}]}}]}`
	listAttempts := 0
	config := &rest.Config{
		Host: "https://api.example",
		Transport: imagePullRoundTripper(func(request *http.Request) (*http.Response, error) {
			listAttempts++
			if listAttempts == 1 {
				return &http.Response{
					StatusCode: http.StatusOK,
					Header:     http.Header{"Content-Type": {"application/json"}},
					Body:       io.NopCloser(strings.NewReader(unavailableOperators)),
				}, nil
			}
			<-request.Context().Done()
			return nil, fmt.Errorf("client rate limiter Wait returned an error: %w", request.Context().Err())
		}),
	}

	verifier := &verifyAllClusterOperatorsAvailableImpl{
		pollInterval: time.Millisecond,
		pollTimeout:  time.Minute,
	}
	err := verifier.Verify(ctx, config)
	require.Error(t, err)
	require.Contains(t, err.Error(), "cluster operators not available")
	require.Contains(t, err.Error(), "kube-apiserver")
	require.NotContains(t, err.Error(), "rate limiter")
	require.ErrorIs(t, err, context.DeadlineExceeded)
	require.Equal(t, 2, listAttempts)
}
