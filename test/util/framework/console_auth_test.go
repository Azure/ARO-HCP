// Copyright 2026 Microsoft Corporation
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//	http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package framework

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestCheckConsoleLoginRedirect(t *testing.T) {
	var server *httptest.Server
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/ready/auth/login":
			http.Redirect(w, r, server.URL+"/tenant/oauth2/v2.0/authorize?client_id=console-client", http.StatusFound)
		case "/unavailable/auth/login":
			http.Error(w, "authentication configuration is not ready", http.StatusServiceUnavailable)
		case "/wrong/auth/login":
			http.Redirect(w, r, server.URL+"/unexpected", http.StatusFound)
		default:
			http.NotFound(w, r)
		}
	})
	server = httptest.NewTLSServer(handler)
	defer server.Close()

	authorizationEndpoint := server.URL + "/tenant/oauth2/v2.0/authorize"

	t.Run("ready", func(t *testing.T) {
		err := CheckConsoleLoginRedirect(context.Background(), server.URL+"/ready/auth/login", authorizationEndpoint)
		require.NoError(t, err)
	})

	t.Run("unavailable", func(t *testing.T) {
		err := CheckConsoleLoginRedirect(context.Background(), server.URL+"/unavailable/auth/login", authorizationEndpoint)
		require.EqualError(t, err, "console login endpoint returned HTTP 503; expected HTTP 302 redirect to \""+authorizationEndpoint+"\"")
	})

	t.Run("unexpected redirect", func(t *testing.T) {
		err := CheckConsoleLoginRedirect(context.Background(), server.URL+"/wrong/auth/login", authorizationEndpoint)
		require.EqualError(t, err, "console login endpoint redirected to \""+server.URL+"/unexpected\"; expected authorization endpoint \""+authorizationEndpoint+"\"")
	})
}
