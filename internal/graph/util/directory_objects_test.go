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

	"k8s.io/apimachinery/pkg/util/wait"

	"github.com/Azure/ARO-HCP/internal/graph/graphsdk"
)

// testConfirmBackoff keeps confirmation retries fast in tests.
var testConfirmBackoff = wait.Backoff{Duration: time.Millisecond, Steps: 3}

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
	return newTestGraphClientWithHTTPClient(t, server, server.Client())
}

func newTestGraphClientWithHTTPClient(t *testing.T, server *httptest.Server, httpClient *http.Client) *Client {
	t.Helper()

	adapter, err := kiotahttp.NewNetHttpRequestAdapterWithParseNodeFactoryAndSerializationWriterFactoryAndHttpClient(
		&noAuthProvider{},
		nil,
		nil,
		httpClient,
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

// answerApplicationGetAsDeleted answers the post-delete confirmation GET for
// app-object with 404, the normal state once the application is deleted.
func answerApplicationGetAsDeleted(t *testing.T, writer http.ResponseWriter, request *http.Request) bool {
	t.Helper()
	if request.Method != http.MethodGet || request.URL.Path != "/applications/app-object" {
		return false
	}
	writeGraphError(t, writer, http.StatusNotFound)
	return true
}

func TestDeleteApplicationPermanentlyPurgesApplicationAndServicePrincipal(t *testing.T) {
	var requests []string
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		requests = append(requests, request.Method+" "+request.URL.Path)
		if answerApplicationGetAsDeleted(t, writer, request) {
			return
		}
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
		"GET /applications/app-object",
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
		"GET /applications/app-object",
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
		if answerApplicationGetAsDeleted(t, writer, request) {
			return
		}
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
		"GET /applications/app-object",
		"DELETE /directory/deletedItems/app-object",
		"POST /directory/deletedItems/app-object/restore",
	}, requests)
}

func TestDeleteApplicationPermanentlySkipsRestoreWhenServicePrincipalPurgeIsForbidden(t *testing.T) {
	var requests []string
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		requests = append(requests, request.Method+" "+request.URL.Path)
		if answerApplicationGetAsDeleted(t, writer, request) {
			return
		}
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
		"GET /applications/app-object",
		"DELETE /directory/deletedItems/app-object",
	}, requests)
}

func TestDeleteApplicationPermanentlySkipsRestoreWhenApplicationPurgeIsForbidden(t *testing.T) {
	var requests []string
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		requests = append(requests, request.Method+" "+request.URL.Path)
		if answerApplicationGetAsDeleted(t, writer, request) {
			return
		}
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
		"GET /applications/app-object",
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

// Graph has answered the application DELETE with 404 right after the service
// principal was deleted while the application stayed active, which leaked the
// application. Cleanup must not take that 404 as proof of deletion.
func TestDeleteApplicationAndConfirmRetriesWhenApplicationStillExists(t *testing.T) {
	var requests []string
	deletes, gets := 0, 0
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		requests = append(requests, request.Method+" "+request.URL.Path)
		switch request.Method {
		case http.MethodDelete:
			deletes++
			if deletes == 1 {
				writeGraphError(t, writer, http.StatusNotFound)
				return
			}
			writer.WriteHeader(http.StatusNoContent)
		case http.MethodGet:
			gets++
			if gets == 1 {
				writer.Header().Set("Content-Type", "application/json")
				_, err := fmt.Fprint(writer, `{"id":"app-object"}`)
				require.NoError(t, err)
				return
			}
			writeGraphError(t, writer, http.StatusNotFound)
		default:
			t.Errorf("unexpected request: %s %s", request.Method, request.URL.Path)
			writer.WriteHeader(http.StatusInternalServerError)
		}
	}))
	defer server.Close()

	client := newTestGraphClient(t, server)
	deleted, err := client.deleteApplicationAndConfirm(context.Background(), "app-object", testConfirmBackoff)
	require.NoError(t, err)
	assert.True(t, deleted, "the second DELETE succeeded, so the purge must wait for the object to appear")
	assert.Equal(t, []string{
		"DELETE /applications/app-object",
		"GET /applications/app-object",
		"DELETE /applications/app-object",
		"GET /applications/app-object",
	}, requests)
}

func TestDeleteApplicationAndConfirmFailsWhenApplicationNeverDisappears(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.Method == http.MethodGet {
			writer.Header().Set("Content-Type", "application/json")
			_, err := fmt.Fprint(writer, `{"id":"app-object"}`)
			require.NoError(t, err)
			return
		}
		writeGraphError(t, writer, http.StatusNotFound)
	}))
	defer server.Close()

	client := newTestGraphClient(t, server)
	deleted, err := client.deleteApplicationAndConfirm(context.Background(), "app-object", testConfirmBackoff)
	require.ErrorIs(t, err, errApplicationStillExists)
	assert.Contains(t, err.Error(), "not confirmed deleted after 3 attempts")
	assert.False(t, deleted, "no DELETE call succeeded")
}

func TestDeleteApplicationAndConfirmStopsWaitingWhenContextIsCanceled(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.Method == http.MethodGet {
			writer.Header().Set("Content-Type", "application/json")
			_, err := fmt.Fprint(writer, `{"id":"app-object"}`)
			require.NoError(t, err)
			return
		}
		writeGraphError(t, writer, http.StatusNotFound)
	}))
	defer server.Close()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	time.AfterFunc(50*time.Millisecond, cancel)

	client := newTestGraphClient(t, server)
	start := time.Now()
	// An hour between attempts: only a context-aware wait returns in time.
	_, err := client.deleteApplicationAndConfirm(ctx, "app-object", wait.Backoff{Duration: time.Hour, Steps: 3})
	require.ErrorIs(t, err, context.Canceled)
	assert.Less(t, time.Since(start), 10*time.Second, "the backoff wait must end when the context is canceled")
}

// The HTTP client's own timeout matches context.DeadlineExceeded while the
// cleanup context is still live; that request failure must surface as itself,
// not as "the application still exists".
func TestDeleteApplicationAndConfirmReportsHTTPClientTimeout(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		<-request.Context().Done()
	}))
	defer server.Close()

	httpClient := server.Client()
	httpClient.Timeout = 50 * time.Millisecond
	client := newTestGraphClientWithHTTPClient(t, server, httpClient)

	_, err := client.deleteApplicationAndConfirm(context.Background(), "app-object", testConfirmBackoff)
	require.ErrorIs(t, err, context.DeadlineExceeded)
	assert.NotErrorIs(t, err, errApplicationStillExists)
	assert.Contains(t, err.Error(), "delete application")
}

// A failed confirmation leaves the application where it is: still active, or
// soft-deleted, where Entra purges it after 30 days. Restoring it would turn a
// soft-deleted application back into an active one that only the DEV sweeper
// would ever remove.
func TestDeleteApplicationPermanentlyDoesNotRestoreWhenConfirmationFails(t *testing.T) {
	var requests []string
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		requests = append(requests, request.Method+" "+request.URL.Path)
		switch request.Method + " " + request.URL.Path {
		case "DELETE /servicePrincipals/sp-object",
			"DELETE /directory/deletedItems/sp-object",
			"DELETE /applications/app-object":
			writer.WriteHeader(http.StatusNoContent)
		case "GET /applications/app-object":
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
	require.Error(t, err)
	assert.Contains(t, err.Error(), "confirm deletion of application")
	assert.Equal(t, []string{
		"DELETE /servicePrincipals/sp-object",
		"DELETE /directory/deletedItems/sp-object",
		"DELETE /applications/app-object",
		"GET /applications/app-object",
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
