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
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/go-logr/logr"
	"github.com/stretchr/testify/require"

	"k8s.io/utils/ptr"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	azcorearm "github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"
	azfake "github.com/Azure/azure-sdk-for-go/sdk/azcore/fake"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/policy"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/runtime"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/to"

	hcpsdk20240610preview "github.com/Azure/ARO-HCP/test/sdk/v20240610preview/resourcemanager/redhatopenshifthcp/armredhatopenshifthcp"
	hcpfake20240610preview "github.com/Azure/ARO-HCP/test/sdk/v20240610preview/resourcemanager/redhatopenshifthcp/armredhatopenshifthcp/fake"
	hcpsdk20251223preview "github.com/Azure/ARO-HCP/test/sdk/v20251223preview/resourcemanager/redhatopenshifthcp/armredhatopenshifthcp"
)

const fakeSubscriptionID = "00000000-0000-0000-0000-000000000000"

func clusterCreationDiagnosticPoller(t *testing.T, transport *fakeTransport) *runtime.Poller[hcpsdk20251223preview.HcpOpenShiftClustersClientCreateOrUpdateResponse] {
	t.Helper()
	request, err := http.NewRequest(http.MethodPut, "https://example.com/cluster", nil)
	require.NoError(t, err)
	response := clusterCreationDiagnosticResponse(request, `{"properties":{"provisioningState":"Accepted"}}`)
	response.StatusCode = http.StatusCreated
	response.Header.Set("Azure-AsyncOperation", "https://example.com/operation")
	pipeline := runtime.NewPipeline("test", "v0.0.0", runtime.PipelineOptions{}, &policy.ClientOptions{
		Transport: transport,
		Retry:     policy.RetryOptions{MaxRetries: -1},
	})
	poller, err := runtime.NewPoller(response, pipeline, &runtime.NewPollerOptions[hcpsdk20251223preview.HcpOpenShiftClustersClientCreateOrUpdateResponse]{
		FinalStateVia: runtime.FinalStateViaAzureAsyncOp,
	})
	require.NoError(t, err)
	return poller
}

func clusterCreationDiagnosticResponse(request *http.Request, body string) *http.Response {
	return &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{"Content-Type": []string{"application/json"}},
		Body:       io.NopCloser(strings.NewReader(body)),
		Request:    request,
	}
}

func TestClusterCreationOperationErrors20251223(t *testing.T) {
	for _, test := range []struct {
		name string
		body string
		code string
		want string
	}{
		{
			name: "nested validation detail",
			body: `{"status":"Failed","error":{"code":"InvalidResource","message":"validation failed","details":[{"code":"MissingPermissions","message":"role assignments may still be propagating","details":[{"code":"Permission","message":"required grant is missing"}]}]}}`,
			code: "InvalidResource",
			want: "required grant is missing",
		},
		{
			name: "deadline with validation code",
			body: `{"status":"Failed","error":{"code":"InvalidResource","message":"cluster creation did not complete before the deadline: validation pending"}}`,
			code: "InvalidResource",
			want: "cluster creation did not complete before the deadline: validation pending",
		},
		{
			name: "deadline",
			body: `{"status":"Failed","error":{"code":"DeadlineExceeded","message":"cluster creation did not complete before the deadline"}}`,
			code: "DeadlineExceeded",
			want: "cluster creation did not complete before the deadline",
		},
		{
			name: "backend cancellation",
			body: `{"status":"Canceled","error":{"code":"OperationCanceled","message":"operation was canceled"}}`,
			code: "OperationCanceled",
			want: "operation was canceled",
		},
	} {
		for _, early := range []bool{false, true} {
			phase := "completion"
			if early {
				phase = "early diagnostics"
			}
			t.Run(test.name+"/"+phase, func(t *testing.T) {
				calls := 0
				poller := clusterCreationDiagnosticPoller(t, &fakeTransport{do: func(request *http.Request) (*http.Response, error) {
					calls++
					require.Equal(t, "/operation", request.URL.Path)
					return clusterCreationDiagnosticResponse(request, test.body), nil
				}})
				var err error
				if early {
					primaryErr := errors.New("unexpected terminal resource state during role delay")
					err = DiagnoseHCPClusterCreation20251223(t.Context(), poller, primaryErr, time.Second)
					require.ErrorIs(t, err, primaryErr)
				} else {
					_, err = poller.PollUntilDone(t.Context(), &runtime.PollUntilDoneOptions{Frequency: StandardPollInterval})
				}
				var responseErr *azcore.ResponseError
				require.ErrorAs(t, err, &responseErr)
				require.Equal(t, test.code, responseErr.ErrorCode)
				require.ErrorContains(t, err, test.want)
				require.Equal(t, 1, calls)
			})
		}
	}
}

func TestClusterCreationDiagnosticsPreservePrimary20251223(t *testing.T) {
	secondaryErr := errors.New("diagnostic transport unavailable")
	for _, test := range []struct {
		name string
		body string
		err  error
	}{
		{name: "transport failure", err: secondaryErr},
		{name: "nonterminal operation", body: `{"status":"InProgress"}`},
		{name: "successful operation", body: `{"status":"Succeeded"}`},
	} {
		t.Run(test.name, func(t *testing.T) {
			calls := 0
			poller := clusterCreationDiagnosticPoller(t, &fakeTransport{do: func(request *http.Request) (*http.Response, error) {
				calls++
				return clusterCreationDiagnosticResponse(request, test.body), test.err
			}})
			primaryErr := errors.New("resource entered unexpected terminal state")
			err := DiagnoseHCPClusterCreation20251223(t.Context(), poller, primaryErr, time.Second)
			require.ErrorIs(t, err, primaryErr)
			if test.err != nil {
				require.ErrorIs(t, err, test.err)
			}
			require.LessOrEqual(t, calls, 2)
		})
	}
}

func TestClusterCreationCancellation20251223(t *testing.T) {
	for _, canceled := range []bool{false, true} {
		for _, early := range []bool{false, true} {
			name := "timeout"
			if canceled {
				name = "cancellation"
			}
			if early {
				name += "/diagnostics"
			}
			t.Run(name, func(t *testing.T) {
				ctx, cancel := context.WithTimeout(t.Context(), time.Second)
				defer cancel()
				want := context.DeadlineExceeded
				if canceled {
					cancel()
					want = context.Canceled
				}
				calls := 0
				poller := clusterCreationDiagnosticPoller(t, &fakeTransport{do: func(request *http.Request) (*http.Response, error) {
					calls++
					<-request.Context().Done()
					return nil, request.Context().Err()
				}})
				var err error
				if early {
					primaryErr := errors.New("resource failed during role delay")
					err = DiagnoseHCPClusterCreation20251223(ctx, poller, primaryErr, 10*time.Millisecond)
					require.ErrorIs(t, err, primaryErr)
					if !canceled {
						require.NoError(t, ctx.Err(), "diagnostic deadline must expire before the parent")
					}
				} else {
					_, err = poller.PollUntilDone(ctx, &runtime.PollUntilDoneOptions{Frequency: StandardPollInterval})
				}
				require.ErrorIs(t, err, want)
				require.LessOrEqual(t, calls, 1)
			})
		}
	}
}

func TestClusterCreationLateSuccess20251223(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	calls := 0
	poller := clusterCreationDiagnosticPoller(t, &fakeTransport{do: func(request *http.Request) (*http.Response, error) {
		calls++
		if request.URL.Path == "/operation" {
			return clusterCreationDiagnosticResponse(request, `{"status":"Succeeded"}`), nil
		}
		return clusterCreationDiagnosticResponse(request, `{"properties":{"provisioningState":"Succeeded"}}`), nil
	}})
	result, err := poller.PollUntilDone(ctx, &runtime.PollUntilDoneOptions{Frequency: StandardPollInterval})
	require.NoError(t, err)
	cancel()
	require.ErrorIs(t, ctx.Err(), context.Canceled)
	require.NotNil(t, result.Properties)
	require.NotNil(t, result.Properties.ProvisioningState)
	require.Equal(t, hcpsdk20251223preview.ProvisioningStateSucceeded, *result.Properties.ProvisioningState)
	require.Equal(t, 2, calls, "verify the returned resource without another GET using the expired context")
}

// fakeClientOptions mirrors the E2E client options that matter here: the
// attempt tracker policy and SDK retries, with short delays.
func fakeClientOptions(transport interface {
	Do(*http.Request) (*http.Response, error)
}) *azcorearm.ClientOptions {
	return &azcorearm.ClientOptions{ClientOptions: azcore.ClientOptions{
		Transport:        transport,
		PerRetryPolicies: []policy.Policy{&requestAttemptTrackerPolicy{}},
		Retry:            policy.RetryOptions{RetryDelay: time.Millisecond, MaxRetryDelay: 10 * time.Millisecond},
	}}
}

// firstPUTTransportError makes retryableFirstPUT fail the first PUT at the
// transport level instead of answering it.
const firstPUTTransportError = -1

// retryableFirstPUT answers the first PUT with status, which the SDK retries,
// and passes every other request to next. A 503, a 408, or a transport error
// stands for a request that may have been applied although the client saw a
// failure; a 429 stands for a request the service definitively did not apply.
// A fake server error cannot be used for this because the SDK never retries
// those.
type retryableFirstPUT struct {
	next interface {
		Do(*http.Request) (*http.Response, error)
	}
	status int
	sent   bool
}

func (t *retryableFirstPUT) Do(req *http.Request) (*http.Response, error) {
	if req.Method == http.MethodPut && !t.sent {
		t.sent = true
		if t.status == firstPUTTransportError {
			return nil, errors.New("connection reset by peer")
		}
		return &http.Response{StatusCode: t.status, Header: http.Header{}, Body: http.NoBody, Request: req}, nil
	}
	return t.next.Do(req)
}

// fakeNodePoolsServer rejects creates with createStatus, after answering the
// first PUT with firstPUTStatus when it is not 0, and serves GETs from states,
// repeating the last state.
func fakeNodePoolsServer(createStatus int, firstPUTStatus int, states ...string) (*hcpsdk20240610preview.NodePoolsClient, *int, error) {
	gets := 0
	srv := hcpfake20240610preview.NodePoolsServer{
		BeginCreateOrUpdate: func(ctx context.Context, resourceGroupName, hcpOpenShiftClusterName, nodePoolName string, resource hcpsdk20240610preview.NodePool, options *hcpsdk20240610preview.NodePoolsClientBeginCreateOrUpdateOptions) (resp azfake.PollerResponder[hcpsdk20240610preview.NodePoolsClientCreateOrUpdateResponse], errResp azfake.ErrorResponder) {
			errResp.SetResponseError(createStatus, http.StatusText(createStatus))
			return
		},
		Get: func(ctx context.Context, resourceGroupName, hcpOpenShiftClusterName, nodePoolName string, options *hcpsdk20240610preview.NodePoolsClientGetOptions) (resp azfake.Responder[hcpsdk20240610preview.NodePoolsClientGetResponse], errResp azfake.ErrorResponder) {
			gets++
			if len(states) == 0 {
				errResp.SetResponseError(http.StatusNotFound, "NotFound")
				return
			}
			state := states[len(states)-1]
			if gets <= len(states) {
				state = states[gets-1]
			}
			resp.SetResponse(http.StatusOK, hcpsdk20240610preview.NodePoolsClientGetResponse{NodePool: hcpsdk20240610preview.NodePool{
				Name:       to.Ptr(nodePoolName),
				Location:   to.Ptr("uksouth"),
				Properties: &hcpsdk20240610preview.NodePoolProperties{ProvisioningState: to.Ptr(hcpsdk20240610preview.ProvisioningState(state))},
			}}, nil)
			return
		},
	}
	var transport interface {
		Do(*http.Request) (*http.Response, error)
	} = hcpfake20240610preview.NewNodePoolsServerTransport(&srv)
	if firstPUTStatus != 0 {
		transport = &retryableFirstPUT{next: transport, status: firstPUTStatus}
	}
	client, err := hcpsdk20240610preview.NewNodePoolsClient(fakeSubscriptionID, &azfake.TokenCredential{}, fakeClientOptions(transport))
	return client, &gets, err
}

func TestCreateNodePoolAndWaitResumesInFlightCreateAfterConflict(t *testing.T) {
	// The first PUT fails in a way that leaves its outcome unknown, the SDK
	// retries, and the retry gets a 409 because the first PUT was accepted.
	for name, firstPUTStatus := range map[string]int{
		"503":             http.StatusServiceUnavailable,
		"408":             http.StatusRequestTimeout,
		"transport error": firstPUTTransportError,
	} {
		t.Run(name, func(t *testing.T) {
			client, gets, err := fakeNodePoolsServer(http.StatusConflict, firstPUTStatus, "Provisioning", "Succeeded")
			if err != nil {
				t.Fatalf("failed to create fake client: %v", err)
			}

			nodePool, err := CreateNodePoolAndWait20240610(context.Background(), client, "rg", "cluster", "np-1", hcpsdk20240610preview.NodePool{}, time.Minute)
			if err != nil {
				t.Fatalf("CreateNodePoolAndWait20240610() unexpected error: %v", err)
			}
			if nodePool == nil || ptr.Deref(nodePool.Name, "") != "np-1" {
				t.Errorf("CreateNodePoolAndWait20240610() = %+v, want node pool np-1", nodePool)
			}
			if *gets < 2 {
				t.Errorf("node pool GET called %d times, want the conflict check and at least one poll", *gets)
			}
		})
	}
}

func TestCreateNodePoolAndWaitReturnsConflictWhenNotResumable(t *testing.T) {
	for name, tc := range map[string]struct {
		firstPUTStatus int
		states         []string
		wantGets       int
	}{
		"node pool does not exist":             {firstPUTStatus: http.StatusServiceUnavailable, wantGets: 1},
		"node pool already succeeded":          {firstPUTStatus: http.StatusServiceUnavailable, states: []string{"Succeeded"}, wantGets: 1},
		"node pool is deleting":                {firstPUTStatus: http.StatusServiceUnavailable, states: []string{"Deleting"}, wantGets: 1},
		"conflict on the first PUT attempt":    {states: []string{"Provisioning"}, wantGets: 0},
		"conflict after a throttled first PUT": {firstPUTStatus: http.StatusTooManyRequests, states: []string{"Provisioning"}, wantGets: 0},
	} {
		t.Run(name, func(t *testing.T) {
			client, gets, err := fakeNodePoolsServer(http.StatusConflict, tc.firstPUTStatus, tc.states...)
			if err != nil {
				t.Fatalf("failed to create fake client: %v", err)
			}
			_, err = CreateNodePoolAndWait20240610(context.Background(), client, "rg", "cluster", "np-1", hcpsdk20240610preview.NodePool{}, time.Minute)
			if err == nil || !strings.Contains(err.Error(), "failed starting nodepool creation") || !strings.Contains(err.Error(), "409") {
				t.Fatalf("CreateNodePoolAndWait20240610() error = %v, want the original 409", err)
			}
			if *gets != tc.wantGets {
				t.Errorf("node pool GET called %d times, want %d", *gets, tc.wantGets)
			}
		})
	}
}

func TestCreateNodePoolAndWaitDoesNotResumeOnOtherErrors(t *testing.T) {
	client, gets, err := fakeNodePoolsServer(http.StatusBadRequest, http.StatusServiceUnavailable, "Provisioning")
	if err != nil {
		t.Fatalf("failed to create fake client: %v", err)
	}
	_, err = CreateNodePoolAndWait20240610(context.Background(), client, "rg", "cluster", "np-1", hcpsdk20240610preview.NodePool{}, time.Minute)
	if err == nil || !strings.Contains(err.Error(), "400") {
		t.Fatalf("CreateNodePoolAndWait20240610() error = %v, want the original 400", err)
	}
	if *gets != 0 {
		t.Errorf("node pool GET called %d times, want 0 for a non-conflict error", *gets)
	}
}

func TestBeginCreateHCPClusterResumesInFlightCreateAfterConflict(t *testing.T) {
	gets := 0
	srv := hcpfake20240610preview.HcpOpenShiftClustersServer{
		BeginCreateOrUpdate: func(ctx context.Context, resourceGroupName, hcpOpenShiftClusterName string, resource hcpsdk20240610preview.HcpOpenShiftCluster, options *hcpsdk20240610preview.HcpOpenShiftClustersClientBeginCreateOrUpdateOptions) (resp azfake.PollerResponder[hcpsdk20240610preview.HcpOpenShiftClustersClientCreateOrUpdateResponse], errResp azfake.ErrorResponder) {
			errResp.SetResponseError(http.StatusConflict, "Conflict")
			return
		},
		Get: func(ctx context.Context, resourceGroupName, hcpOpenShiftClusterName string, options *hcpsdk20240610preview.HcpOpenShiftClustersClientGetOptions) (resp azfake.Responder[hcpsdk20240610preview.HcpOpenShiftClustersClientGetResponse], errResp azfake.ErrorResponder) {
			state := hcpsdk20240610preview.ProvisioningStateAccepted
			if gets > 0 {
				state = hcpsdk20240610preview.ProvisioningStateSucceeded
			}
			gets++
			resp.SetResponse(http.StatusOK, hcpsdk20240610preview.HcpOpenShiftClustersClientGetResponse{HcpOpenShiftCluster: hcpsdk20240610preview.HcpOpenShiftCluster{
				Name:       to.Ptr(hcpOpenShiftClusterName),
				Location:   to.Ptr("uksouth"),
				Properties: &hcpsdk20240610preview.HcpOpenShiftClusterProperties{ProvisioningState: to.Ptr(state)},
			}}, nil)
			return
		},
	}
	client, err := hcpsdk20240610preview.NewHcpOpenShiftClustersClient(fakeSubscriptionID, &azfake.TokenCredential{}, fakeClientOptions(&retryableFirstPUT{next: hcpfake20240610preview.NewHcpOpenShiftClustersServerTransport(&srv), status: http.StatusServiceUnavailable}))
	if err != nil {
		t.Fatalf("failed to create fake client: %v", err)
	}

	poller, err := BeginCreateHCPCluster20240610(context.Background(), logr.Discard(), client, "rg", "cluster-1", ClusterParams20240610{}, "uksouth")
	if err != nil {
		t.Fatalf("BeginCreateHCPCluster20240610() unexpected error: %v", err)
	}
	result, err := poller.PollUntilDone(context.Background(), &runtime.PollUntilDoneOptions{Frequency: time.Second})
	if err != nil {
		t.Fatalf("PollUntilDone() unexpected error: %v", err)
	}
	if ptr.Deref(result.Name, "") != "cluster-1" {
		t.Errorf("PollUntilDone() result name = %q, want cluster-1", ptr.Deref(result.Name, ""))
	}
}

func TestCreateHCPClusterAndWaitWithoutTimeoutReportsResumedFailure(t *testing.T) {
	gets := 0
	srv := hcpfake20240610preview.HcpOpenShiftClustersServer{
		BeginCreateOrUpdate: func(ctx context.Context, resourceGroupName, hcpOpenShiftClusterName string, resource hcpsdk20240610preview.HcpOpenShiftCluster, options *hcpsdk20240610preview.HcpOpenShiftClustersClientBeginCreateOrUpdateOptions) (resp azfake.PollerResponder[hcpsdk20240610preview.HcpOpenShiftClustersClientCreateOrUpdateResponse], errResp azfake.ErrorResponder) {
			errResp.SetResponseError(http.StatusConflict, "Conflict")
			return
		},
		Get: func(ctx context.Context, resourceGroupName, hcpOpenShiftClusterName string, options *hcpsdk20240610preview.HcpOpenShiftClustersClientGetOptions) (resp azfake.Responder[hcpsdk20240610preview.HcpOpenShiftClustersClientGetResponse], errResp azfake.ErrorResponder) {
			state := hcpsdk20240610preview.ProvisioningStateAccepted
			if gets > 0 {
				state = hcpsdk20240610preview.ProvisioningStateFailed
			}
			gets++
			resp.SetResponse(http.StatusOK, hcpsdk20240610preview.HcpOpenShiftClustersClientGetResponse{HcpOpenShiftCluster: hcpsdk20240610preview.HcpOpenShiftCluster{
				Name:       to.Ptr(hcpOpenShiftClusterName),
				Location:   to.Ptr("uksouth"),
				Properties: &hcpsdk20240610preview.HcpOpenShiftClusterProperties{ProvisioningState: to.Ptr(state)},
			}}, nil)
			return
		},
	}
	client, err := hcpsdk20240610preview.NewHcpOpenShiftClustersClient(fakeSubscriptionID, &azfake.TokenCredential{}, fakeClientOptions(&retryableFirstPUT{next: hcpfake20240610preview.NewHcpOpenShiftClustersServerTransport(&srv), status: http.StatusServiceUnavailable}))
	if err != nil {
		t.Fatalf("failed to create fake client: %v", err)
	}

	// A timeout of 0 makes the helper check the operation once instead of
	// waiting for it; a terminal failure seen by that check must be returned.
	cluster := BuildHCPClusterFromParams20240610(ClusterParams20240610{}, "uksouth")
	_, err = CreateHCPClusterAndWait20240610(context.Background(), logr.Discard(), client, "rg", "cluster-1", cluster, 0)
	if err == nil || !strings.Contains(err.Error(), `provisioning state "Failed"`) {
		t.Fatalf("CreateHCPClusterAndWait20240610() error = %v, want the resumed operation's Failed state", err)
	}
}
