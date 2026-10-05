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

package engine

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/go-logr/logr"
	"github.com/microsoft/kiota-abstractions-go/authentication"
	msgraphsdk "github.com/microsoftgraph/msgraph-sdk-go"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore/runtime"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/authorization/armauthorization/v3"

	"github.com/Azure/ARO-HCP/tooling/cleanup-sweeper/pkg/engine/runner"
	roleassignmentsteps "github.com/Azure/ARO-HCP/tooling/cleanup-sweeper/pkg/engine/steps/roleassignments"
)

type auditTransport func(*http.Request) (*http.Response, error)

func (f auditTransport) Do(r *http.Request) (*http.Response, error)        { return f(r) }
func (f auditTransport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func auditResponse(req *http.Request, status int, body string) *http.Response {
	return &http.Response{
		Request: req, StatusCode: status,
		ContentLength: int64(len(body)),
		Header:        http.Header{"Content-Type": []string{"application/json"}},
		Body:          io.NopCloser(strings.NewReader(body)),
	}
}

func auditEntries(t *testing.T, logs *bytes.Buffer, message string) []map[string]any {
	t.Helper()
	var entries []map[string]any
	decoder := json.NewDecoder(strings.NewReader(logs.String()))
	for decoder.More() {
		var entry map[string]any
		if err := decoder.Decode(&entry); err != nil {
			t.Fatal(err)
		}
		if entry["msg"] == message {
			entries = append(entries, entry)
		}
	}
	return entries
}

func TestRoleAssignmentDeletionAudit(t *testing.T) {
	// Graph client construction writes to the SDK's global backing-store factory.
	const (
		subscriptionID = "00000000-0000-0000-0000-000000000001"
		principalID    = "00000000-0000-0000-0000-000000000002"
		scope          = "/subscriptions/" + subscriptionID + "/resourceGroups/test-rg"
		assignmentID   = scope + "/providers/Microsoft.Authorization/roleAssignments/00000000-0000-0000-0000-000000000003"
	)
	assignment := fmt.Sprintf(`{"id":%q,"name":"assignment","type":"Microsoft.Authorization/roleAssignments","properties":{"principalId":%q,"scope":%q}}`, assignmentID, principalID, scope)
	for _, tc := range []struct {
		name              string
		getStatus         int
		deleteStatus      int
		graphStatus       int
		principalRestored bool
		dryRun            bool
		retry             bool
		stepRetry         bool
		continueOnError   bool
		wantDeletes       int
		wantMessage       string
		wantError         bool
	}{
		{name: "deleted", wantDeletes: 1, wantMessage: "Deleted resource"},
		{name: "absent at reread", getStatus: 404, wantMessage: "Resource already absent"},
		{name: "absent at delete", deleteStatus: 404, wantDeletes: 1, wantMessage: "Resource already absent"},
		{name: "principal restored", principalRestored: true, wantMessage: "Retained resource after revalidation"},
		{name: "Graph revalidation forbidden", graphStatus: 403, wantError: true},
		{name: "ARM delete forbidden", deleteStatus: 403, wantDeletes: 1, wantError: true},
		{name: "best effort failure", deleteStatus: 403, continueOnError: true, wantDeletes: 1, wantMessage: "Deletion failed but continuing"},
		{name: "dry run", dryRun: true, wantMessage: "Dry-run deletion target"},
		{name: "SDK retry", retry: true, wantDeletes: 2, wantMessage: "Deleted resource"},
		{name: "step retry", stepRetry: true, wantDeletes: 2, wantMessage: "Deleted resource"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var logs bytes.Buffer
			logger := logr.FromSlogHandler(slog.NewJSONHandler(&logs, nil)).WithValues("runID", "test-run")
			ctx := logr.NewContext(context.Background(), logger)
			var requestIDs []string
			getCalls, graphCalls := 0, 0
			opts := DefaultARMClientOptions()
			opts.DisableRPRegistration = true
			opts.Retry.MaxRetries = 1
			opts.Retry.RetryDelay = time.Millisecond
			opts.Transport = auditTransport(func(req *http.Request) (*http.Response, error) {
				if req.Method == http.MethodGet {
					if req.URL.Path != assignmentID {
						return auditResponse(req, 200, `{"value":[`+assignment+`]}`), nil
					}
					getCalls++
					if tc.getStatus != 0 {
						return auditResponse(req, tc.getStatus, `{"error":{"code":"NotFound"}}`), nil
					}
					return auditResponse(req, 200, assignment), nil
				}
				if req.Method != http.MethodDelete {
					return nil, fmt.Errorf("unexpected ARM method %s", req.Method)
				}
				requestIDs = append(requestIDs, req.Header.Get("x-ms-client-request-id"))
				if len(auditEntries(t, &logs, "Sending ARM DELETE request")) != len(requestIDs) {
					t.Error("DELETE must be logged before the transport sends it")
				}
				status := tc.deleteStatus
				if status == 0 {
					status = http.StatusOK
				}
				if tc.retry && len(requestIDs) == 1 {
					status = http.StatusInternalServerError
				}
				if tc.stepRetry && len(requestIDs) == 1 {
					status = http.StatusBadRequest
				}
				body := assignment
				if status >= 400 {
					body = `{"error":{"code":"RequestFailed"}}`
				}
				response := auditResponse(req, status, body)
				response.Header.Set("x-ms-request-id", fmt.Sprintf("azure-%d", len(requestIDs)))
				response.Header.Set("x-ms-correlation-request-id", "azure-correlation")
				response.Header.Set("x-ms-routing-request-id", "azure-routing")
				return response, nil
			})
			armClient, err := armauthorization.NewRoleAssignmentsClient(subscriptionID, workflowsTestCredential{}, opts)
			if err != nil {
				t.Fatal(err)
			}
			graphTransport := auditTransport(func(req *http.Request) (*http.Response, error) {
				if req.Method == http.MethodGet && strings.HasSuffix(req.URL.Path, "/groups") {
					return auditResponse(req, 200, `{"value":[{"id":"known-group"}]}`), nil
				}
				if req.Method != http.MethodPost || !strings.HasSuffix(req.URL.Path, "/directoryObjects/getByIds") {
					return nil, fmt.Errorf("unexpected Graph request %s %s", req.Method, req.URL.Path)
				}
				graphCalls++
				if graphCalls > 1 {
					if tc.graphStatus != 0 {
						return auditResponse(req, tc.graphStatus, `{"error":{"code":"Authorization_RequestDenied","message":"forbidden"}}`), nil
					}
					if tc.principalRestored {
						return auditResponse(req, 200, fmt.Sprintf(`{"value":[{"id":%q}]}`, principalID)), nil
					}
				}
				return auditResponse(req, 200, `{"value":[]}`), nil
			})
			adapter, err := msgraphsdk.NewGraphRequestAdapterWithParseNodeFactoryAndSerializationWriterFactoryAndHttpClient(
				&authentication.AnonymousAuthenticationProvider{}, nil, nil, &http.Client{Transport: graphTransport})
			if err != nil {
				t.Fatal(err)
			}
			stepRetries := 1
			if tc.stepRetry {
				stepRetries = 2
			}
			step, err := roleassignmentsteps.NewDeleteOrphanedStep(roleassignmentsteps.DeleteOrphanedStepConfig{
				SubscriptionID: subscriptionID, RoleAssignmentsClient: armClient,
				GraphClient:                 msgraphsdk.NewGraphServiceClient(adapter),
				Retries:                     stepRetries,
				ContinueOnTargetDeleteError: tc.continueOnError,
			})
			if err != nil {
				t.Fatal(err)
			}
			engine := runner.Engine{Steps: []runner.Step{step}, DryRun: tc.dryRun, Parallelism: 1}
			err = engine.Run(ctx)
			if (err != nil) != tc.wantError {
				t.Fatalf("unexpected run error: %v", err)
			}
			if len(requestIDs) != tc.wantDeletes {
				t.Fatalf("DELETE count: got %d, want %d", len(requestIDs), tc.wantDeletes)
			}
			if tc.dryRun && (getCalls != 0 || graphCalls != 1) {
				t.Fatal("dry-run performed deletion revalidation")
			}
			if tc.wantMessage != "" && len(auditEntries(t, &logs, tc.wantMessage)) != 1 {
				t.Fatalf("missing outcome %q:\n%s", tc.wantMessage, logs.String())
			}
			if tc.wantMessage != "Deleted resource" && len(auditEntries(t, &logs, "Deleted resource")) != 0 {
				t.Fatal("reported a deletion that did not succeed")
			}
			selected := auditEntries(t, &logs, "Selected orphaned role assignment")
			if len(selected) != 1 || selected[0]["assignmentID"] != assignmentID || selected[0]["principalID"] != principalID {
				t.Fatalf("missing discovery evidence: %v", selected)
			}
			sent := auditEntries(t, &logs, "Sending ARM DELETE request")
			received := auditEntries(t, &logs, "Received ARM DELETE response")
			if len(sent) != tc.wantDeletes || len(received) != tc.wantDeletes {
				t.Fatalf("missing request/response logs:\n%s", logs.String())
			}
			for i, entry := range sent {
				wantAttempt := float64(i + 1)
				wantStatus := tc.deleteStatus
				if wantStatus == 0 {
					wantStatus = http.StatusOK
				}
				if tc.retry && i == 0 {
					wantStatus = http.StatusInternalServerError
				}
				if tc.stepRetry {
					wantAttempt = 1
					if i == 0 {
						wantStatus = http.StatusBadRequest
					}
				}
				if requestIDs[i] == "" || entry["clientRequestID"] != requestIDs[i] ||
					entry["attempt"] != wantAttempt || entry["runID"] != "test-run" ||
					entry["principalID"] != principalID || entry["scope"] != scope || entry["requestPath"] != assignmentID {
					t.Fatalf("incorrect request attribution: %v", entry)
				}
				if !tc.stepRetry && requestIDs[i] != requestIDs[0] {
					t.Fatal("SDK retries did not retain the logical request ID")
				}
				if received[i]["azureRequestID"] != fmt.Sprintf("azure-%d", i+1) ||
					received[i]["azureCorrelationRequestID"] != "azure-correlation" ||
					received[i]["azureRoutingRequestID"] != "azure-routing" ||
					received[i]["clientRequestID"] != requestIDs[i] ||
					received[i]["statusCode"] != float64(wantStatus) {
					t.Fatalf("missing response IDs: %v", received[i])
				}
			}
			if tc.stepRetry && (requestIDs[0] == requestIDs[1] || getCalls != 2 || graphCalls != 3) {
				t.Fatal("step retry must revalidate and create a new logical request")
			}
		})
	}
}

func TestDeleteAuditTransportFailureDoesNotLogSecrets(t *testing.T) {
	t.Parallel()
	var logs bytes.Buffer
	ctx := logr.NewContext(context.Background(), logr.FromSlogHandler(slog.NewJSONHandler(&logs, nil)))
	transportError := errors.New("sensitive-transport-error")
	options := DefaultARMClientOptions()
	options.Retry.MaxRetries = -1
	options.Transport = auditTransport(func(req *http.Request) (*http.Response, error) {
		if req.Header.Get("Authorization") != "secret-credential" {
			t.Error("changed authorization")
		}
		if req.Header.Get("x-ms-client-request-id") != "caller-request-id" {
			t.Error("changed caller request ID")
		}
		return nil, transportError
	})
	pipeline := runtime.NewPipeline("audit-test", "v1.0.0", runtime.PipelineOptions{}, &options.ClientOptions)
	req, err := runtime.NewRequest(ctx, http.MethodDelete, "https://example.invalid/resource?sig=secret-query")
	if err != nil {
		t.Fatal(err)
	}
	req.Raw().Header.Set("Authorization", "secret-credential")
	req.Raw().Header.Set("x-ms-client-request-id", "caller-request-id")
	_, err = pipeline.Do(req)
	if !errors.Is(err, transportError) {
		t.Fatalf("transport error was not preserved: %v", err)
	}
	if len(auditEntries(t, &logs, "ARM DELETE attempt failed")) != 1 {
		t.Fatal("missing failed attempt")
	}
	for _, secret := range []string{"secret-credential", "secret-query", "sensitive-transport-error"} {
		if strings.Contains(logs.String(), secret) {
			t.Fatalf("audit leaked %q", secret)
		}
	}
}
