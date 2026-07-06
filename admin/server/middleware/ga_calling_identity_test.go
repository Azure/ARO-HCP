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

package middleware

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/go-logr/logr/funcr"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/Azure/ARO-HCP/internal/utils"
)

// TestMiddlewareClientPrincipalDoesNotLogHeaderValues ensures the middleware logs
// only the names of the x-ms-client-principal-* headers and never their
// credential-bearing values.
func TestMiddlewareClientPrincipalDoesNotLogHeaderValues(t *testing.T) {
	const (
		principalName = "test-user@example.com"
		objectID      = "11111111-1111-1111-1111-111111111111"
	)

	var logOutput strings.Builder
	logger := funcr.New(func(prefix, args string) {
		logOutput.WriteString(prefix)
		logOutput.WriteString(args)
	}, funcr.Options{})
	ctx := utils.ContextWithLogger(context.Background(), logger)

	req := httptest.NewRequest(http.MethodGet, "/", nil).WithContext(ctx)
	req.Header.Set(ClientPrincipalNameHeader, principalName)
	req.Header.Set(ClientAADTypeHeader, "User")
	req.Header.Set("X-Ms-Client-Principal-Id", objectID)

	nextCalled := false
	next := func(w http.ResponseWriter, r *http.Request) {
		nextCalled = true
	}

	rec := httptest.NewRecorder()
	MiddlewareClientPrincipal(rec, req, next)

	require.True(t, nextCalled, "next handler should have been called")

	output := logOutput.String()

	// Header names must be present in the log.
	assert.Contains(t, output, ClientPrincipalNameHeader, "log should contain the principal name header name")
	assert.Contains(t, output, ClientAADTypeHeader, "log should contain the principal type header name")
	assert.Contains(t, output, "X-Ms-Client-Principal-Id", "log should contain the principal id header name")

	// Header values must never appear in the log.
	assert.NotContains(t, output, principalName, "log must not contain the principal name value")
	assert.NotContains(t, output, objectID, "log must not contain the principal id value")
}

// TestMiddlewareClientPrincipalHeaderNamesAreSorted ensures the logged header
// names are deterministically ordered regardless of map iteration order.
func TestMiddlewareClientPrincipalHeaderNamesAreSorted(t *testing.T) {
	var logOutput strings.Builder
	logger := funcr.New(func(prefix, args string) {
		logOutput.WriteString(prefix)
		logOutput.WriteString(args)
	}, funcr.Options{})
	ctx := utils.ContextWithLogger(context.Background(), logger)

	req := httptest.NewRequest(http.MethodGet, "/", nil).WithContext(ctx)
	req.Header.Set(ClientPrincipalNameHeader, "test-user@example.com")
	req.Header.Set(ClientAADTypeHeader, "User")
	req.Header.Set("X-Ms-Client-Principal-Id", "object-id")

	MiddlewareClientPrincipal(httptest.NewRecorder(), req, func(w http.ResponseWriter, r *http.Request) {})

	output := logOutput.String()
	assert.Contains(t, output,
		strings.Join([]string{"X-Ms-Client-Principal-Id", ClientPrincipalNameHeader, ClientAADTypeHeader}, "; "),
		"logged header names should be sorted alphabetically")
}
