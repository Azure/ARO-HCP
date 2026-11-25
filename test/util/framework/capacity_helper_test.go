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
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"
	"testing/synctest"
	"time"

	"github.com/go-logr/logr"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	azcorearm "github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"
	azfake "github.com/Azure/azure-sdk-for-go/sdk/azcore/fake"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/runtime"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/resources/armresources"

	"github.com/Azure/ARO-HCP/arohcp-cosmos-client/api/coreapi"
	hcpsdk20240610preview "github.com/Azure/ARO-HCP/test/sdk/v20240610preview/resourcemanager/redhatopenshifthcp/armredhatopenshifthcp"
	sdk20251223 "github.com/Azure/ARO-HCP/test/sdk/v20251223preview/resourcemanager/redhatopenshifthcp/armredhatopenshifthcp"
	sdk20260630 "github.com/Azure/ARO-HCP/test/sdk/v20260630preview/resourcemanager/redhatopenshifthcp/armredhatopenshifthcp"
	sdk20260901 "github.com/Azure/ARO-HCP/test/sdk/v20260901preview/resourcemanager/redhatopenshifthcp/armredhatopenshifthcp"
	sdk20261001 "github.com/Azure/ARO-HCP/test/sdk/v20261001/resourcemanager/redhatopenshifthcp/armredhatopenshifthcp"
	"github.com/Azure/ARO-HCP/test/util/timing"
)

func capacityHTTPResponse(req *http.Request, status int, body string) *http.Response {
	return &http.Response{StatusCode: status, Header: http.Header{"Content-Type": {"application/json"}}, Body: io.NopCloser(strings.NewReader(body)), Request: req}
}

func TestClusterCapacityBackoffPreservesProvisioningTimeout(t *testing.T) {
	creates := map[string]func(context.Context, *azcorearm.ClientOptions) error{
		"20240610": func(ctx context.Context, options *azcorearm.ClientOptions) error {
			client, err := hcpsdk20240610preview.NewHcpOpenShiftClustersClient(fakeSubscriptionID, &azfake.TokenCredential{}, options)
			if err != nil {
				return err
			}
			_, err = CreateHCPClusterAndWait20240610(ctx, logr.Discard(), client, "rg", "cluster", hcpsdk20240610preview.HcpOpenShiftCluster{Properties: &hcpsdk20240610preview.HcpOpenShiftClusterProperties{Version: &hcpsdk20240610preview.VersionProfile{}}}, time.Second)
			return err
		},
		"20251223": func(ctx context.Context, options *azcorearm.ClientOptions) error {
			client, err := sdk20251223.NewHcpOpenShiftClustersClient(fakeSubscriptionID, &azfake.TokenCredential{}, options)
			if err != nil {
				return err
			}
			_, err = CreateHCPClusterAndWait20251223(ctx, logr.Discard(), client, "rg", "cluster", sdk20251223.HcpOpenShiftCluster{Properties: &sdk20251223.HcpOpenShiftClusterProperties{Version: &sdk20251223.VersionProfile{}}}, time.Second)
			return err
		},
		"20260630": func(ctx context.Context, options *azcorearm.ClientOptions) error {
			client, err := sdk20260630.NewHcpOpenShiftClustersClient(fakeSubscriptionID, &azfake.TokenCredential{}, options)
			if err != nil {
				return err
			}
			_, err = CreateHCPClusterAndWait20260630(ctx, logr.Discard(), client, "rg", "cluster", sdk20260630.HcpOpenShiftCluster{Properties: &sdk20260630.HcpOpenShiftClusterProperties{Version: &sdk20260630.VersionProfile{}}}, time.Second)
			return err
		},
		"20260901": func(ctx context.Context, options *azcorearm.ClientOptions) error {
			client, err := sdk20260901.NewHcpOpenShiftClustersClient(fakeSubscriptionID, &azfake.TokenCredential{}, options)
			if err != nil {
				return err
			}
			_, err = CreateHCPClusterAndWait20260901(ctx, logr.Discard(), client, "rg", "cluster", sdk20260901.HcpOpenShiftCluster{Properties: &sdk20260901.HcpOpenShiftClusterProperties{Version: &sdk20260901.VersionProfile{}}}, time.Second)
			return err
		},
		"20261001": func(ctx context.Context, options *azcorearm.ClientOptions) error {
			client, err := sdk20261001.NewHcpOpenShiftClustersClient(fakeSubscriptionID, &azfake.TokenCredential{}, options)
			if err != nil {
				return err
			}
			_, err = CreateHCPClusterAndWait20261001(ctx, logr.Discard(), client, "rg", "cluster", sdk20261001.HcpOpenShiftCluster{}, time.Second)
			return err
		},
	}
	for name, create := range creates {
		t.Run(name, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				puts := 0
				start := time.Now()
				transport := &fakeTransport{do: func(req *http.Request) (*http.Response, error) {
					if req.Method == http.MethodPut {
						puts++
						if puts == 1 {
							return capacityHTTPResponse(req, http.StatusBadRequest, `{"error":{"code":"AROHCPCapacityExceeded"}}`), nil
						}
					}
					return capacityHTTPResponse(req, http.StatusOK, `{"name":"cluster","location":"uksouth","properties":{"provisioningState":"Succeeded"}}`), nil
				}}
				if err := create(context.Background(), fakeClientOptions(transport)); err != nil {
					t.Fatalf("create after capacity rejection: %v", err)
				}
				if puts != 2 {
					t.Fatalf("PUTs=%d, want 2", puts)
				}
				if time.Since(start) < time.Minute {
					t.Fatalf("elapsed=%v, want at least the retry interval between the PUTs", time.Since(start))
				}
				if time.Since(start) > 91*time.Second {
					t.Fatalf("elapsed=%v, want at most the retry interval plus jitter between the PUTs", time.Since(start))
				}
			})
		})
	}
}

func TestCapacityBackoffBoundsAndCancellation(t *testing.T) {
	for _, tc := range []struct {
		name             string
		backoffLimit     time.Duration
		parentTimeout    time.Duration
		cancelDuringWait bool
		wantAttempts     int
		wantElapsed      time.Duration
		wantErr          error
	}{
		{name: "backoff limit reached", backoffLimit: 12 * time.Second, wantAttempts: 2, wantElapsed: 12 * time.Second, wantErr: errCapacityBackoffLimitReached},
		{name: "parent deadline", backoffLimit: time.Hour, parentTimeout: 3 * time.Second, wantAttempts: 1, wantElapsed: 3 * time.Second, wantErr: context.DeadlineExceeded},
		{name: "parent cancellation", backoffLimit: time.Hour, cancelDuringWait: true, wantAttempts: 1, wantElapsed: 2 * time.Second, wantErr: context.Canceled},
	} {
		t.Run(tc.name, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				ctx, cancel := context.WithCancel(context.Background())
				defer cancel()
				if tc.parentTimeout > 0 {
					var deadlineCancel context.CancelFunc
					ctx, deadlineCancel = context.WithTimeout(ctx, tc.parentTimeout)
					defer deadlineCancel()
				}
				if tc.cancelDuringWait {
					go func() { time.Sleep(2 * time.Second); cancel() }()
				}
				attempts := 0
				start := time.Now()
				_, err := retryOnCapacityRejection(ctx, newOperationDeadline(0, nil), tc.backoffLimit, isCapacityRejection, func(context.Context) (int, error) {
					attempts++
					return 0, &azcore.ResponseError{StatusCode: http.StatusBadRequest, ErrorCode: coreapi.CloudErrorCodeCapacityExceeded}
				})
				if !errors.Is(err, tc.wantErr) {
					t.Fatalf("error=%v, want %v", err, tc.wantErr)
				}
				if attempts != tc.wantAttempts {
					t.Fatalf("attempts=%d, want %d", attempts, tc.wantAttempts)
				}
				if time.Since(start) != tc.wantElapsed {
					t.Fatalf("elapsed=%v, want %v", time.Since(start), tc.wantElapsed)
				}
			})
		})
	}
}

func TestCapacityBackoffReturnsUnrelatedErrors(t *testing.T) {
	for _, tc := range []struct {
		name     string
		original error
	}{
		{name: "throttled", original: &azcore.ResponseError{StatusCode: http.StatusTooManyRequests, ErrorCode: "Throttled"}},
		{name: "capacity heavy use", original: &azcore.ResponseError{StatusCode: http.StatusBadRequest, ErrorCode: coreapi.CloudErrorCodeCapacityHeavyUse}},
		{name: "invalid request content", original: &azcore.ResponseError{StatusCode: http.StatusBadRequest, ErrorCode: coreapi.CloudErrorCodeInvalidRequestContent}},
		{name: "connection lost", original: errors.New("connection lost")},
	} {
		t.Run(tc.name, func(t *testing.T) {
			attempts := 0
			_, err := retryOnCapacityRejection(context.Background(), newOperationDeadline(0, nil), time.Minute, isCapacityRejection, func(context.Context) (int, error) {
				attempts++
				return 0, tc.original
			})
			if err != tc.original {
				t.Fatalf("error=%v, want the original error", err)
			}
			if attempts != 1 {
				t.Fatalf("attempts=%d, want no resubmission", attempts)
			}
		})
	}
}

func TestCapacityBackoffRetriesAfterUnknownOutcome(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		ctx, tracker := withRequestAttemptTracker(context.Background())
		tracker.outcomeUnknown.Store(true)
		attempts := 0
		result, err := retryOnCapacityRejection(ctx, newOperationDeadline(0, nil), 2*time.Minute, isCapacityRejection, func(context.Context) (int, error) {
			attempts++
			if attempts == 1 {
				return 0, &azcore.ResponseError{StatusCode: http.StatusBadRequest, ErrorCode: coreapi.CloudErrorCodeCapacityExceeded}
			}
			return 1, nil
		})
		if err != nil {
			t.Fatalf("error=%v, want capacity backoff to continue after an earlier unknown outcome", err)
		}
		if result != 1 {
			t.Fatalf("result=%d, want 1", result)
		}
		if attempts != 2 {
			t.Fatalf("attempts=%d, want 2", attempts)
		}
	})
}

func TestDeploymentCapacityRejectionClassification(t *testing.T) {
	for _, tc := range []struct {
		name, body string
		want       bool
	}{
		{"terminal", `{"error":{"code":"DeploymentFailed","details":[{"code":"ResourceDeploymentFailure","details":[{"code":"AROHCPCapacityExceeded"}]}]}}`, true},
		{"multiple resources", `{"error":{"code":"DeploymentFailed","details":[{"code":"MultipleErrorsOccurred","details":[{"code":"AROHCPCapacityExceeded"},{"code":"AROHCPCapacityExceeded"}]}]}}`, true},
		{"unrecognized wrapper", `{"error":{"code":"UnknownFailure","details":[{"code":"AROHCPCapacityExceeded"}]}}`, true},
		{"mixed", `{"error":{"code":"DeploymentFailed","details":[{"code":"AROHCPCapacityExceeded"},{"code":"AuthorizationFailed"}]}}`, false},
		{"mixed nested", `{"error":{"code":"DeploymentFailed","details":[{"code":"MultipleErrorsOccurred","details":[{"code":"AROHCPCapacityExceeded"},{"code":"InvalidRequestContent"}]}]}}`, false},
		{"backend capacity", `{"error":{"code":"DeploymentFailed","details":[{"code":"AROHCPCapacityHeavyUse"}]}}`, false},
		{"module", `{"status":"Failed","error":{"code":"DeploymentFailed","message":"At least one resource deployment operation failed.","details":[{"code":"Conflict","message":"{\r\n  \"status\": \"Failed\",\r\n  \"error\": {\r\n    \"code\": \"ResourceDeploymentFailure\",\r\n    \"details\": [\r\n      {\r\n        \"code\": \"DeploymentFailed\",\r\n        \"details\": [\r\n          {\r\n            \"code\": \"AROHCPCapacityExceeded\"\r\n          }\r\n        ]\r\n      }\r\n    ]\r\n  }\r\n}"}]}}`, true},
		{"module with another failure", `{"status":"Failed","error":{"code":"DeploymentFailed","details":[{"code":"Conflict","message":"{\"status\":\"Failed\",\"error\":{\"code\":\"ResourceDeploymentFailure\",\"details\":[{\"code\":\"DeploymentFailed\",\"details\":[{\"code\":\"PrincipalNotFound\"}]}]}}"}]}}`, false},
		{"module among other failures", `{"status":"Failed","error":{"code":"DeploymentFailed","details":[{"code":"Conflict","message":"{\"status\":\"Failed\",\"error\":{\"code\":\"ResourceDeploymentFailure\",\"details\":[{\"code\":\"AROHCPCapacityExceeded\"}]}}"},{"code":"Conflict","message":"another deployment is active"}]}}`, false},
		{"message only", `{"error":{"code":"DeploymentFailed","message":"AROHCPCapacityExceeded"}}`, false},
		{"malformed", `{`, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := fmt.Errorf("failed waiting for deployment to finish: %w", runtime.NewResponseError(capacityHTTPResponse(nil, http.StatusOK, tc.body)))
			if got := isDeploymentCapacityRejection(err); got != tc.want {
				t.Fatalf("classification=%v, want %v: %v", got, tc.want, err)
			}
		})
	}
}

func TestDeploymentCapacityBackoff(t *testing.T) {
	for _, tc := range []struct {
		name             string
		mixed, transient bool
		wantPuts         int
		wantSuccess      bool
	}{
		{name: "terminal capacity then success", wantPuts: 2, wantSuccess: true},
		{name: "mixed terminal failure", mixed: true, wantPuts: 1},
		{name: "transient polling capacity", transient: true, wantPuts: 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				puts := 0
				transport := &fakeTransport{do: func(req *http.Request) (*http.Response, error) {
					failure := `{"code":"DeploymentFailed","details":[{"code":"AROHCPCapacityExceeded"}]}`
					if tc.mixed {
						failure = `{"code":"DeploymentFailed","details":[{"code":"AROHCPCapacityExceeded"},{"code":"AuthorizationFailed"}]}`
					}
					if req.Method == http.MethodPut {
						puts++
						if puts > 1 {
							return capacityHTTPResponse(req, http.StatusOK, `{"name":"deployment","properties":{"provisioningState":"Succeeded"}}`), nil
						}
						resp := capacityHTTPResponse(req, http.StatusCreated, `{"properties":{"provisioningState":"Accepted"}}`)
						resp.Header.Set("Azure-AsyncOperation", "https://management.azure.com/operations/deployment")
						return resp, nil
					}
					if tc.transient {
						return capacityHTTPResponse(req, http.StatusBadRequest, `{"error":`+failure+`}`), nil
					}
					return capacityHTTPResponse(req, http.StatusOK, `{"status":"Failed","error":`+failure+`}`), nil
				}}
				client, err := armresources.NewDeploymentsClient(fakeSubscriptionID, &azfake.TokenCredential{}, fakeClientOptions(transport))
				if err != nil {
					t.Fatal(err)
				}
				_, err = deployWithCapacityRetry(context.Background(), time.Second, "deployment", func(ctx context.Context) (*runtime.Poller[armresources.DeploymentsClientCreateOrUpdateResponse], error) {
					return client.BeginCreateOrUpdate(ctx, "rg", "deployment", armresources.Deployment{}, nil)
				}, func(context.Context) (string, error) { t.Fatal("unexpected DeploymentActive recovery"); return "", nil })
				if (err == nil) != tc.wantSuccess {
					t.Fatalf("error=%v, want success=%v", err, tc.wantSuccess)
				}
				if puts != tc.wantPuts {
					t.Fatalf("PUTs=%d, want %d", puts, tc.wantPuts)
				}
			})
		})
	}
}

func TestClusterCapacityDoesNotResubmitAcceptedCreate(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		puts := 0
		transport := &fakeTransport{do: func(req *http.Request) (*http.Response, error) {
			if req.Method == http.MethodPut {
				puts++
				resp := capacityHTTPResponse(req, http.StatusCreated, `{"properties":{"provisioningState":"Accepted"}}`)
				resp.Header.Set("Azure-AsyncOperation", "https://management.azure.com/operations/cluster")
				return resp, nil
			}
			return capacityHTTPResponse(req, http.StatusOK, `{"status":"Failed","error":{"code":"AROHCPCapacityExceeded"}}`), nil
		}}
		client, err := sdk20261001.NewHcpOpenShiftClustersClient(fakeSubscriptionID, &azfake.TokenCredential{}, fakeClientOptions(transport))
		if err != nil {
			t.Fatal(err)
		}
		_, err = CreateHCPClusterAndWait20261001(context.Background(), logr.Discard(), client, "rg", "cluster", sdk20261001.HcpOpenShiftCluster{}, time.Minute)
		var responseError *azcore.ResponseError
		if !errors.As(err, &responseError) {
			t.Fatalf("error=%v, want the original terminal failure", err)
		}
		if responseError.ErrorCode != coreapi.CloudErrorCodeCapacityExceeded {
			t.Fatalf("error code=%q, want %q", responseError.ErrorCode, coreapi.CloudErrorCodeCapacityExceeded)
		}
		if puts != 1 {
			t.Fatalf("PUTs=%d, want no resubmission", puts)
		}
	})
}

func TestCapacityBackoffKeepsResultFinishedAtDeadline(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		result, err := retryOnCapacityRejection(context.Background(), newOperationDeadline(time.Second, nil), time.Minute, isCapacityRejection, func(ctx context.Context) (int, error) {
			<-ctx.Done()
			return 1, nil
		})
		if err != nil {
			t.Fatalf("error=%v, want the result of the attempt that succeeded as the deadline passed", err)
		}
		if result != 1 {
			t.Fatalf("result=%d, want 1", result)
		}
	})
}

func TestCapacityBackoffAlreadyCanceled(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := retryOnCapacityRejection(ctx, newOperationDeadline(0, nil), time.Minute, isCapacityRejection, func(context.Context) (int, error) {
		t.Fatal("create called after parent cancellation")
		return 0, nil
	})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("error=%v, want parent cancellation", err)
	}
}

func TestCapacityBackoffDoesNotResetLimit(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		attempts := 0
		start := time.Now()
		_, err := retryOnCapacityRejection(context.Background(), newOperationDeadline(0, nil), 100*time.Second, isCapacityRejection, func(context.Context) (int, error) {
			attempts++
			return 0, &azcore.ResponseError{StatusCode: http.StatusBadRequest, ErrorCode: coreapi.CloudErrorCodeCapacityExceeded}
		})
		if !errors.Is(err, errCapacityBackoffLimitReached) {
			t.Fatalf("error=%v, want the capacity backoff limit", err)
		}
		if attempts != 3 {
			t.Fatalf("attempts=%d, want an attempt after each of two backoffs", attempts)
		}
		if time.Since(start) != 100*time.Second {
			t.Fatalf("elapsed=%v, want one 100s backoff limit", time.Since(start))
		}
	})
}

func TestClusterCreateSharesOperationTimeout(t *testing.T) {
	for _, tc := range []struct {
		name         string
		rejected     bool
		requestDelay time.Duration
		pollDelay    time.Duration
	}{
		{name: "submission is bounded", requestDelay: 2 * time.Second},
		{name: "submission and polling share timeout", requestDelay: 700 * time.Millisecond, pollDelay: 700 * time.Millisecond},
		{name: "capacity backoff alone is excluded", rejected: true, requestDelay: 700 * time.Millisecond, pollDelay: 700 * time.Millisecond},
	} {
		t.Run(tc.name, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				puts := 0
				var active time.Duration
				transport := &fakeTransport{do: func(req *http.Request) (*http.Response, error) {
					if req.Method == http.MethodPut {
						puts++
						if tc.rejected && puts == 1 {
							return capacityHTTPResponse(req, http.StatusBadRequest, `{"error":{"code":"AROHCPCapacityExceeded"}}`), nil
						}
					}
					delay := tc.pollDelay
					if req.Method == http.MethodPut {
						delay = tc.requestDelay
					}
					start := time.Now()
					defer func() { active += time.Since(start) }()
					select {
					case <-req.Context().Done():
						return nil, req.Context().Err()
					case <-time.After(delay):
					}
					if req.Method == http.MethodPut {
						resp := capacityHTTPResponse(req, http.StatusCreated, `{"properties":{"provisioningState":"Accepted"}}`)
						resp.Header.Set("Azure-AsyncOperation", "https://management.azure.com/operations/cluster")
						return resp, nil
					}
					return capacityHTTPResponse(req, http.StatusOK, `{"status":"Succeeded","properties":{"provisioningState":"Succeeded"}}`), nil
				}}
				client, err := sdk20261001.NewHcpOpenShiftClustersClient(fakeSubscriptionID, &azfake.TokenCredential{}, fakeClientOptions(transport))
				if err != nil {
					t.Fatal(err)
				}
				start := time.Now()
				_, err = CreateHCPClusterAndWait20261001(context.Background(), logr.Discard(), client, "rg", "cluster", sdk20261001.HcpOpenShiftCluster{}, time.Second)
				if !errors.Is(err, context.DeadlineExceeded) {
					t.Fatalf("error=%v, want the deadline", err)
				}
				if active != time.Second {
					t.Fatalf("active=%v elapsed=%v, want the deadline after 1s of operation work", active, time.Since(start))
				}
				if tc.rejected && time.Since(start) < 60*time.Second {
					t.Fatalf("capacity backoff consumed operation time: elapsed=%v", time.Since(start))
				}
			})
		})
	}
}

func TestDeploymentRejectedForCapacityDoesNotConsumeOperationTimeout(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		puts := 0
		var active time.Duration
		transport := &fakeTransport{do: func(req *http.Request) (*http.Response, error) {
			start := time.Now()
			defer func() { active += time.Since(start) }()
			if req.Method == http.MethodPut {
				puts++
			}
			select {
			case <-req.Context().Done():
				return nil, req.Context().Err()
			case <-time.After(350 * time.Millisecond):
			}
			if req.Method == http.MethodPut {
				resp := capacityHTTPResponse(req, http.StatusCreated, `{"properties":{"provisioningState":"Accepted"}}`)
				resp.Header.Set("Azure-AsyncOperation", "https://management.azure.com/operations/deployment")
				return resp, nil
			}
			if puts == 1 {
				return capacityHTTPResponse(req, http.StatusOK, `{"status":"Failed","error":{"code":"DeploymentFailed","details":[{"code":"AROHCPCapacityExceeded"}]}}`), nil
			}
			return capacityHTTPResponse(req, http.StatusOK, `{"status":"Succeeded","properties":{"provisioningState":"Succeeded"}}`), nil
		}}
		client, err := armresources.NewDeploymentsClient(fakeSubscriptionID, &azfake.TokenCredential{}, fakeClientOptions(transport))
		if err != nil {
			t.Fatal(err)
		}
		_, err = deployWithCapacityRetry(context.Background(), 1500*time.Millisecond, "deployment",
			func(ctx context.Context) (*runtime.Poller[armresources.DeploymentsClientCreateOrUpdateResponse], error) {
				return client.BeginCreateOrUpdate(ctx, "rg", "deployment", armresources.Deployment{}, nil)
			}, func(context.Context) (string, error) {
				t.Fatal("unexpected deployment recovery")
				return "", nil
			})
		if err != nil {
			t.Fatalf("error=%v; want the 1050ms deployment to fit the 1.5s operation timeout after a 700ms rejected one", err)
		}
		if active != 1750*time.Millisecond {
			t.Fatalf("active=%v; want a 700ms rejected and a 1050ms successful deployment", active)
		}
		if puts != 2 {
			t.Fatalf("PUTs=%d; want 2", puts)
		}
	})
}

func TestCapacityBackoffLimitIncludesRejectedAttempts(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		start := time.Now()
		_, err := retryOnCapacityRejection(context.Background(), newOperationDeadline(0, nil), 3*time.Second, isCapacityRejection, func(ctx context.Context) (int, error) {
			select {
			case <-ctx.Done():
				return 0, ctx.Err()
			case <-time.After(2 * time.Second):
				return 0, &azcore.ResponseError{StatusCode: http.StatusBadRequest, ErrorCode: coreapi.CloudErrorCodeCapacityExceeded}
			}
		})
		if !errors.Is(err, errCapacityBackoffLimitReached) {
			t.Fatalf("error=%v; want the capacity backoff limit", err)
		}
		if errors.Is(err, context.DeadlineExceeded) {
			t.Fatalf("error=%v; want no timeout error", err)
		}
		if time.Since(start) != 5*time.Second {
			t.Fatalf("elapsed=%v; want two 2s rejected requests around the 1s of backoff left of the 3s limit", time.Since(start))
		}
	})
}

func TestCapacityBackoffTiming(t *testing.T) {
	for _, tt := range []struct {
		name        string
		cancelAfter time.Duration
		wantOutcome string
	}{
		{name: "retry accepted", wantOutcome: timing.OutcomeSucceeded},
		{name: "caller cancels backoff", cancelAfter: 3 * time.Second, wantOutcome: timing.OutcomeFailed},
	} {
		t.Run(tt.name, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				tc := &perItOrDescribeTestContext{}
				activeTestContext.Store(tc)
				defer activeTestContext.Store(nil)
				ctx, cancel := context.WithCancel(context.Background())
				defer cancel()
				if tt.cancelAfter > 0 {
					time.AfterFunc(tt.cancelAfter, cancel)
				}
				attempts := 0
				var requestFinished time.Time
				_, err := retryOnCapacityRejection(ctx, newOperationDeadline(time.Minute, nil), 2*time.Minute, isCapacityRejection, func(context.Context) (int, error) {
					attempts++
					time.Sleep(2 * time.Second)
					if attempts == 1 {
						requestFinished = time.Now()
						return 0, &azcore.ResponseError{StatusCode: http.StatusBadRequest, ErrorCode: coreapi.CloudErrorCodeCapacityExceeded}
					}
					return 1, nil
				})
				if (err != nil) != (tt.cancelAfter > 0) {
					t.Fatalf("unexpected result: %v", err)
				}
				if len(tc.timingMetadata.Steps) != 1 {
					t.Fatalf("expected one capacity backoff span, got %+v", tc.timingMetadata.Steps)
				}
				span := tc.timingMetadata.Steps[0]
				if span.Kind != timing.KindCapacityBackoff {
					t.Fatalf("span kind=%q, want %q", span.Kind, timing.KindCapacityBackoff)
				}
				if span.Outcome != tt.wantOutcome {
					t.Fatalf("span outcome=%q, want %q", span.Outcome, tt.wantOutcome)
				}
				if span.StartedAt != requestFinished.Format(time.RFC3339Nano) {
					t.Fatalf("backoff included request time: %+v", span)
				}
			})
		})
	}
}
