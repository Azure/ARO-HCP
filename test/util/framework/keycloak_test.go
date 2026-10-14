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
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestBuildKeycloakRealm(t *testing.T) {
	options := KeycloakTestProviderOptions{
		Realm:               "test-realm",
		Username:            "test-user",
		Password:            "test-password",
		Group:               "test-group",
		ConsoleClientID:     "console-client",
		ConsoleClientSecret: "console-secret",
		ConsoleRedirectURI:  "https://console.example.test/auth/callback",
		ConsoleWebOrigin:    "https://console.example.test",
		CommandLineClientID: "cli-client",
	}

	realm := buildKeycloakRealm(options)

	assert.Equal(t, "test-realm", realm.Realm)
	require.Len(t, realm.Users, 1)
	assert.Equal(t, "test-user", realm.Users[0].Username)
	assert.Equal(t, []string{"/test-group"}, realm.Users[0].Groups)
	require.Len(t, realm.Clients, 2)

	consoleClient := realm.Clients[0]
	assert.Equal(t, "console-client", consoleClient.ClientID)
	assert.False(t, consoleClient.PublicClient)
	assert.Equal(t, "console-secret", consoleClient.Secret)
	assert.Equal(t, []string{"https://console.example.test/auth/callback"}, consoleClient.RedirectURIs)
	assert.Contains(t, consoleClient.DefaultClientScopes, "profile")

	commandLineClient := realm.Clients[1]
	assert.Equal(t, "cli-client", commandLineClient.ClientID)
	assert.True(t, commandLineClient.PublicClient)
	assert.True(t, commandLineClient.DirectAccessGrantsEnabled)
	assert.Contains(t, commandLineClient.DefaultClientScopes, "profile")
	require.Len(t, commandLineClient.ProtocolMappers, 2)
	assert.Equal(t, "cli-client", commandLineClient.ProtocolMappers[0].Config["included.client.audience"])
	assert.Equal(t, "groups", commandLineClient.ProtocolMappers[1].Config["claim.name"])
}

func TestCompleteKeycloakConsoleLogin(t *testing.T) {
	var server *httptest.Server
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/console/auth/login":
			http.Redirect(w, r, server.URL+"/realms/e2e/protocol/openid-connect/auth?client_id=console-client&redirect_uri="+
				url.QueryEscape(server.URL+"/console/auth/callback")+"&response_type=code&scope=openid+profile", http.StatusFound)
		case "/realms/e2e/protocol/openid-connect/auth":
			_, _ = fmt.Fprint(w, `<html><body><form id="kc-form-login" action="/realms/e2e/login-actions/authenticate" method="post"><input name="username"><input name="password"></form></body></html>`)
		case "/realms/e2e/login-actions/authenticate":
			require.NoError(t, r.ParseForm())
			assert.Equal(t, "test-user", r.Form.Get("username"))
			assert.Equal(t, "test-password", r.Form.Get("password"))
			http.SetCookie(w, &http.Cookie{Name: "KEYCLOAK_SESSION", Value: "session", Path: "/"})
			http.Redirect(w, r, server.URL+"/console/auth/callback?code=test-code", http.StatusFound)
		case "/console/auth/callback":
			http.SetCookie(w, &http.Cookie{Name: "console-session", Value: "session", Path: "/"})
			http.Redirect(w, r, server.URL+"/console/", http.StatusFound)
		case "/console/":
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte("console"))
		default:
			http.NotFound(w, r)
		}
	})
	server = httptest.NewTLSServer(handler)
	defer server.Close()

	result, err := CompleteKeycloakConsoleLogin(
		context.Background(),
		server.URL+"/console/auth/login",
		server.URL+"/realms/e2e",
		"test-user",
		"test-password",
	)

	require.NoError(t, err)
	require.NotNil(t, result.AuthorizationRequest)
	assert.Equal(t, "console-client", result.AuthorizationRequest.Query().Get("client_id"))
	require.NotNil(t, result.FinalURL)
	assert.Equal(t, "/console/", result.FinalURL.Path)
	assert.True(t, containsCookie(result.ConsoleCookies, "console-session"))
}

func TestCheckConsoleLoginRedirect(t *testing.T) {
	var server *httptest.Server
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/ready/auth/login":
			http.Redirect(w, r, server.URL+"/realms/e2e/protocol/openid-connect/auth?client_id=console-client", http.StatusFound)
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

	authorizationEndpoint := server.URL + "/realms/e2e/protocol/openid-connect/auth"

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
		require.EqualError(t, err, "console login endpoint redirected to \""+server.URL+"/unexpected\"; expected Keycloak authorization endpoint \""+authorizationEndpoint+"\"")
	})
}

func TestRequestKeycloakPasswordToken(t *testing.T) {
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.NoError(t, r.ParseForm())
		assert.Equal(t, "password", r.Form.Get("grant_type"))
		assert.Equal(t, "cli-client", r.Form.Get("client_id"))
		assert.Equal(t, "test-user", r.Form.Get("username"))
		assert.Equal(t, "test-password", r.Form.Get("password"))
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"access_token":"test-token","token_type":"Bearer"}`))
	}))
	defer server.Close()

	token, err := RequestKeycloakPasswordToken(context.Background(), &KeycloakTestProvider{
		TokenEndpoint: server.URL,
	}, "cli-client", "test-user", "test-password")

	require.NoError(t, err)
	assert.Equal(t, "test-token", token)
}

func containsCookie(cookies []*http.Cookie, name string) bool {
	for _, cookie := range cookies {
		if strings.EqualFold(cookie.Name, name) {
			return true
		}
	}
	return false
}
