// Copyright (c) Microsoft Corporation.
// Licensed under the Apache License 2.0.

package integrationutils

import (
	"context"
	"net/http"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"k8s.io/apimachinery/pkg/util/wait"

	"github.com/Azure/ARO-HCP/internal/utils"
)

// StartTestServers starts the Frontend and AdminAPI servers in background
// goroutines, waits for them to become ready, and returns a cleanup function.
// The caller MUST defer the returned cleanup function to stop the servers
// and check for errors.
//
// Usage:
//
//	cleanup := integrationutils.StartTestServers(ctx, t, testInfo)
//	defer cleanup()
func StartTestServers(ctx context.Context, t *testing.T, testInfo *IntegrationTestInfo) func() {
	t.Helper()

	ctx, cancel := context.WithCancel(ctx)
	logger := utils.LoggerFromContext(ctx)

	frontendStarted := atomic.Bool{}
	frontendErrCh := make(chan error, 1)
	adminAPIStarted := atomic.Bool{}
	adminAPIErrCh := make(chan error, 1)

	go func() {
		frontendStarted.Store(true)
		frontendErrCh <- testInfo.Frontend.Run(ctx)
	}()

	go func() {
		adminAPIStarted.Store(true)
		adminAPIErrCh <- testInfo.AdminAPI.Run(ctx)
	}()

	// Wait for both servers to become ready
	err := wait.PollUntilContextCancel(ctx, 100*time.Millisecond, true, func(ctx context.Context) (bool, error) {
		for _, url := range []string{testInfo.FrontendURL, testInfo.AdminURL} {
			resp, err := http.Get(url)
			if err != nil {
				return false, nil
			}
			if closeErr := resp.Body.Close(); closeErr != nil {
				logger.Error(closeErr, "failed to close response body")
			}
		}
		return true, nil
	})
	require.NoError(t, err)

	// Return cleanup function that stops servers and checks for errors
	return func() {
		cancel()
		if frontendStarted.Load() {
			require.NoError(t, <-frontendErrCh)
		}
		if adminAPIStarted.Load() {
			require.NoError(t, <-adminAPIErrCh)
		}
	}
}
