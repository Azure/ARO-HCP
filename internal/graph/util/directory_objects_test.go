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

package util

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"slices"
	"testing"
	"time"

	abstractions "github.com/microsoft/kiota-abstractions-go"
	kiotahttp "github.com/microsoft/kiota-http-go"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/Azure/ARO-HCP/internal/graph/graphsdk"
)

type noAuthProvider struct{}

func (*noAuthProvider) AuthenticateRequest(
	context.Context,
	*abstractions.RequestInformation,
	map[string]interface{},
) error {
	return nil
}

func newTestGraphClient(t *testing.T, server *httptest.Server) *Client {
	t.Helper()

	adapter, err := kiotahttp.NewNetHttpRequestAdapterWithParseNodeFactoryAndSerializationWriterFactoryAndHttpClient(
		&noAuthProvider{},
		nil,
		nil,
		server.Client(),
	)
	require.NoError(t, err)
	adapter.SetBaseUrl(server.URL)

	return &Client{
		graphClient: graphsdk.NewGraphBaseServiceClient(adapter, nil),
	}
}

func writeGraphError(t *testing.T, writer http.ResponseWriter, statusCode int) {
	t.Helper()
	writeGraphErrorCode(t, writer, statusCode, "Request_ResourceNotFound")
}

func writeGraphErrorCode(t *testing.T, writer http.ResponseWriter, statusCode int, code string) {
	t.Helper()
	writer.Header().Set("Content-Type", "application/json")
	writer.WriteHeader(statusCode)
	_, err := fmt.Fprintf(
		writer,
		`{"error":{"code":"%s","message":"status %d"}}`,
		code,
		statusCode,
	)
	require.NoError(t, err)
}

func TestDeleteApplicationPermanentlyPurgesApplicationAndServicePrincipal(t *testing.T) {
	var requests []string
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		requests = append(requests, request.Method+" "+request.URL.Path)
		switch request.URL.Path {
		case "/applications/app-object",
			"/servicePrincipals/sp-object",
			"/directory/deletedItems/app-object",
			"/directory/deletedItems/sp-object":
			writer.WriteHeader(http.StatusNoContent)
		default:
			writeGraphError(t, writer, http.StatusNotFound)
		}
	}))
	defer server.Close()

	client := newTestGraphClient(t, server)
	err := client.DeleteApplicationPermanently(context.Background(), ApplicationCleanupTarget{
		ApplicationObjectID:      "app-object",
		ServicePrincipalObjectID: "sp-object",
	})
	require.NoError(t, err)
	assert.Equal(t, []string{
		"DELETE /servicePrincipals/sp-object",
		"DELETE /directory/deletedItems/sp-object",
		"DELETE /applications/app-object",
		"DELETE /directory/deletedItems/app-object",
	}, requests)
}

func TestPermanentlyDeleteDirectoryObjectRetriesPropagationNotFound(t *testing.T) {
	attempts := 0
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		attempts++
		if attempts < 3 {
			writeGraphError(t, writer, http.StatusNotFound)
			return
		}
		writer.WriteHeader(http.StatusNoContent)
	}))
	defer server.Close()

	client := newTestGraphClient(t, server)
	err := client.permanentlyDeleteDirectoryObject(
		context.Background(),
		"app-object",
		time.Millisecond,
		time.Second,
		true,
	)
	require.NoError(t, err)
	assert.Equal(t, 3, attempts)
}

func TestPermanentlyDeleteDirectoryObjectReturnsNonNotFoundError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		writeGraphError(t, writer, http.StatusForbidden)
	}))
	defer server.Close()

	client := newTestGraphClient(t, server)
	err := client.permanentlyDeleteDirectoryObject(
		context.Background(),
		"app-object",
		time.Millisecond,
		time.Second,
		true,
	)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "HTTP 403")
}

func TestDeleteApplicationPermanentlyResumesWhenApplicationIsAlreadyDeleted(t *testing.T) {
	var requests []string
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		requests = append(requests, request.Method+" "+request.URL.Path)
		if request.URL.Path == "/applications/app-object" || request.URL.Path == "/servicePrincipals/sp-object" {
			writeGraphError(t, writer, http.StatusNotFound)
			return
		}
		writer.WriteHeader(http.StatusNoContent)
	}))
	defer server.Close()

	client := newTestGraphClient(t, server)
	err := client.DeleteApplicationPermanently(context.Background(), ApplicationCleanupTarget{
		ApplicationObjectID:      "app-object",
		ServicePrincipalObjectID: "sp-object",
	})
	require.NoError(t, err)
	assert.Equal(t, []string{
		"DELETE /servicePrincipals/sp-object",
		"DELETE /directory/deletedItems/sp-object",
		"DELETE /applications/app-object",
		"DELETE /directory/deletedItems/app-object",
	}, requests)
}

func TestDeleteApplicationPermanentlyPreservesApplicationWhenServicePrincipalPurgeFails(t *testing.T) {
	var requests []string
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		requests = append(requests, request.Method+" "+request.URL.Path)
		switch request.URL.Path {
		case "/servicePrincipals/sp-object":
			writer.WriteHeader(http.StatusNoContent)
		case "/directory/deletedItems/sp-object":
			writeGraphError(t, writer, http.StatusForbidden)
		case "/directory/deletedItems/sp-object/restore":
			writer.WriteHeader(http.StatusOK)
		default:
			t.Errorf("unexpected request: %s %s", request.Method, request.URL.Path)
			writer.WriteHeader(http.StatusInternalServerError)
		}
	}))
	defer server.Close()

	client := newTestGraphClient(t, server)
	err := client.DeleteApplicationPermanently(context.Background(), ApplicationCleanupTarget{
		ApplicationObjectID:      "app-object",
		ServicePrincipalObjectID: "sp-object",
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "purge service principal")
	assert.Equal(t, []string{
		"DELETE /servicePrincipals/sp-object",
		"DELETE /directory/deletedItems/sp-object",
		"POST /directory/deletedItems/sp-object/restore",
	}, requests)
}

func TestDeleteApplicationPermanentlyRestoresApplicationWhenPurgeFails(t *testing.T) {
	var requests []string
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		requests = append(requests, request.Method+" "+request.URL.Path)
		switch request.Method + " " + request.URL.Path {
		case "DELETE /servicePrincipals/sp-object",
			"DELETE /directory/deletedItems/sp-object",
			"DELETE /applications/app-object":
			writer.WriteHeader(http.StatusNoContent)
		case "DELETE /directory/deletedItems/app-object":
			writeGraphError(t, writer, http.StatusForbidden)
		case "POST /directory/deletedItems/app-object/restore":
			writer.WriteHeader(http.StatusOK)
		default:
			t.Errorf("unexpected request: %s %s", request.Method, request.URL.Path)
			writer.WriteHeader(http.StatusInternalServerError)
		}
	}))
	defer server.Close()

	client := newTestGraphClient(t, server)
	err := client.DeleteApplicationPermanently(context.Background(), ApplicationCleanupTarget{
		ApplicationObjectID:      "app-object",
		ServicePrincipalObjectID: "sp-object",
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "purge application")
	assert.Equal(t, []string{
		"DELETE /servicePrincipals/sp-object",
		"DELETE /directory/deletedItems/sp-object",
		"DELETE /applications/app-object",
		"DELETE /directory/deletedItems/app-object",
		"POST /directory/deletedItems/app-object/restore",
	}, requests)
}

func TestDeleteApplicationPermanentlySkipsRestoreWhenServicePrincipalPurgeIsForbidden(t *testing.T) {
	var requests []string
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		requests = append(requests, request.Method+" "+request.URL.Path)
		switch request.URL.Path {
		case "/servicePrincipals/sp-object", "/applications/app-object", "/directory/deletedItems/app-object":
			writer.WriteHeader(http.StatusNoContent)
		case "/directory/deletedItems/sp-object":
			writeGraphErrorCode(t, writer, http.StatusForbidden, "Authorization_RequestDenied")
		default:
			t.Errorf("unexpected request: %s %s", request.Method, request.URL.Path)
			writer.WriteHeader(http.StatusInternalServerError)
		}
	}))
	defer server.Close()

	client := newTestGraphClient(t, server)
	err := client.DeleteApplicationPermanently(context.Background(), ApplicationCleanupTarget{
		ApplicationObjectID:      "app-object",
		ServicePrincipalObjectID: "sp-object",
	})
	require.NoError(t, err)
	assert.Equal(t, []string{
		"DELETE /servicePrincipals/sp-object",
		"DELETE /directory/deletedItems/sp-object",
		"DELETE /applications/app-object",
		"DELETE /directory/deletedItems/app-object",
	}, requests)
}

func TestDeleteApplicationPermanentlySkipsRestoreWhenApplicationPurgeIsForbidden(t *testing.T) {
	var requests []string
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		requests = append(requests, request.Method+" "+request.URL.Path)
		switch request.URL.Path {
		case "/servicePrincipals/sp-object", "/directory/deletedItems/sp-object", "/applications/app-object":
			writer.WriteHeader(http.StatusNoContent)
		case "/directory/deletedItems/app-object":
			writeGraphErrorCode(t, writer, http.StatusForbidden, "Authorization_RequestDenied")
		default:
			t.Errorf("unexpected request: %s %s", request.Method, request.URL.Path)
			writer.WriteHeader(http.StatusInternalServerError)
		}
	}))
	defer server.Close()

	client := newTestGraphClient(t, server)
	err := client.DeleteApplicationPermanently(context.Background(), ApplicationCleanupTarget{
		ApplicationObjectID:      "app-object",
		ServicePrincipalObjectID: "sp-object",
	})
	require.NoError(t, err)
	assert.Equal(t, []string{
		"DELETE /servicePrincipals/sp-object",
		"DELETE /directory/deletedItems/sp-object",
		"DELETE /applications/app-object",
		"DELETE /directory/deletedItems/app-object",
	}, requests)
}

func TestDeleteApplicationPermanentlyWithDelegatedCredentialsOnlySoftDeletes(t *testing.T) {
	var requests []string
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		requests = append(requests, request.Method+" "+request.URL.Path)
		writer.WriteHeader(http.StatusNoContent)
	}))
	defer server.Close()

	client := newTestGraphClient(t, server)
	client.isUser = true
	err := client.DeleteApplicationPermanently(context.Background(), ApplicationCleanupTarget{
		ApplicationObjectID:      "app-object",
		ServicePrincipalObjectID: "sp-object",
	})
	require.NoError(t, err)
	assert.Equal(t, []string{
		"DELETE /applications/app-object",
	}, requests)
}

func TestPermanentlyDeleteDirectoryObjectTreatsAbsentObjectAsAlreadyPurged(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		writeGraphError(t, writer, http.StatusNotFound)
	}))
	defer server.Close()

	client := newTestGraphClient(t, server)
	err := client.permanentlyDeleteDirectoryObject(
		context.Background(),
		"app-object",
		time.Millisecond,
		5*time.Millisecond,
		false,
	)
	require.NoError(t, err)
}

func TestRestoreDeletedDirectoryObjectSurvivesCanceledOperationContext(t *testing.T) {
	requests := 0
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		requests++
		require.Equal(t, http.MethodPost, request.Method)
		require.Equal(t, "/directory/deletedItems/app-object/restore", request.URL.Path)
		writer.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	client := newTestGraphClient(t, server)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	err := client.restoreDeletedDirectoryObject(ctx, "app-object")
	require.NoError(t, err)
	assert.Equal(t, 1, requests)
}

func TestGetServicePrincipalByAppID(t *testing.T) {
	const appID = "11111111-2222-3333-4444-555555555555"

	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		require.Equal(t, http.MethodGet, request.Method)
		require.Equal(t, "/servicePrincipals", request.URL.Path)
		assert.Equal(t, "appId eq '"+appID+"'", request.URL.Query().Get("$filter"))
		assert.True(t, slices.Contains(request.URL.Query()["$select"], "id,appId"))

		writer.Header().Set("Content-Type", "application/json")
		_, err := fmt.Fprint(
			writer,
			`{"value":[{"id":"sp-object","appId":"`+appID+`"}]}`,
		)
		require.NoError(t, err)
	}))
	defer server.Close()

	client := newTestGraphClient(t, server)
	servicePrincipal, err := client.GetServicePrincipalByAppID(context.Background(), appID)
	require.NoError(t, err)
	require.NotNil(t, servicePrincipal)
	assert.Equal(t, "sp-object", servicePrincipal.ID)
	assert.Equal(t, appID, servicePrincipal.AppID)
}

func TestGetServicePrincipalByAppIDReturnsNilWhenMissing(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		writer.Header().Set("Content-Type", "application/json")
		_, err := fmt.Fprint(writer, `{"value":[]}`)
		require.NoError(t, err)
	}))
	defer server.Close()

	client := newTestGraphClient(t, server)
	servicePrincipal, err := client.GetServicePrincipalByAppID(
		context.Background(),
		"11111111-2222-3333-4444-555555555555",
	)
	require.NoError(t, err)
	assert.Nil(t, servicePrincipal)
}
