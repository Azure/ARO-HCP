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
	"net"
	"net/http"
	"net/url"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"
	"github.com/stretchr/testify/require"

	"k8s.io/utils/ptr"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	azcorearm "github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"
	azfake "github.com/Azure/azure-sdk-for-go/sdk/azcore/fake"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/policy"

	"github.com/Azure/ARO-HCP/internal/api/coreapi"
	hcpsdk20240610preview "github.com/Azure/ARO-HCP/test/sdk/v20240610preview/resourcemanager/redhatopenshifthcp/armredhatopenshifthcp"
)

func TestValidateAvailableOpenShiftVersion20240610(t *testing.T) {
	for _, group := range []string{"stable", "fast", "candidate"} {
		name := "4.21"
		if group != "stable" {
			name += "-" + group
		}
		for _, tt := range []struct {
			name   string
			mutate func(*hcpsdk20240610preview.HcpOpenShiftVersion) *hcpsdk20240610preview.HcpOpenShiftVersion
			want   string
		}{
			{name: "valid"},
			{name: "nil version", mutate: func(v *hcpsdk20240610preview.HcpOpenShiftVersion) *hcpsdk20240610preview.HcpOpenShiftVersion {
				return nil
			}, want: `name="<nil>" channelGroup="<nil>" enabled=<nil>`},
			{name: "nil name", mutate: func(v *hcpsdk20240610preview.HcpOpenShiftVersion) *hcpsdk20240610preview.HcpOpenShiftVersion {
				v.Name = nil
				return v
			}, want: fmt.Sprintf(`name="<nil>" channelGroup=%q enabled=true`, group)},
			{name: "wrong name", mutate: func(v *hcpsdk20240610preview.HcpOpenShiftVersion) *hcpsdk20240610preview.HcpOpenShiftVersion {
				v.Name = ptr.To("4.20")
				return v
			}, want: fmt.Sprintf(`name="4.20" channelGroup=%q enabled=true`, group)},
			{name: "nil properties", mutate: func(v *hcpsdk20240610preview.HcpOpenShiftVersion) *hcpsdk20240610preview.HcpOpenShiftVersion {
				v.Properties = nil
				return v
			}, want: fmt.Sprintf(`name=%q channelGroup="<nil>" enabled=<nil>`, name)},
			{name: "nil group", mutate: func(v *hcpsdk20240610preview.HcpOpenShiftVersion) *hcpsdk20240610preview.HcpOpenShiftVersion {
				v.Properties.ChannelGroup = nil
				return v
			}, want: fmt.Sprintf(`name=%q channelGroup="<nil>" enabled=true`, name)},
			{name: "wrong group", mutate: func(v *hcpsdk20240610preview.HcpOpenShiftVersion) *hcpsdk20240610preview.HcpOpenShiftVersion {
				v.Properties.ChannelGroup = ptr.To("other")
				return v
			}, want: fmt.Sprintf(`name=%q channelGroup="other" enabled=true`, name)},
			{name: "nil enabled", mutate: func(v *hcpsdk20240610preview.HcpOpenShiftVersion) *hcpsdk20240610preview.HcpOpenShiftVersion {
				v.Properties.Enabled = nil
				return v
			}, want: fmt.Sprintf(`name=%q channelGroup=%q enabled=<nil>`, name, group)},
			{name: "disabled", mutate: func(v *hcpsdk20240610preview.HcpOpenShiftVersion) *hcpsdk20240610preview.HcpOpenShiftVersion {
				v.Properties.Enabled = ptr.To(false)
				return v
			}, want: fmt.Sprintf(`name=%q channelGroup=%q enabled=false`, name, group)},
		} {
			t.Run(group+"/"+tt.name, func(t *testing.T) {
				version := &hcpsdk20240610preview.HcpOpenShiftVersion{
					Name: ptr.To(name),
					Properties: &hcpsdk20240610preview.HcpOpenShiftVersionProperties{
						ChannelGroup: ptr.To(group), Enabled: ptr.To(true),
					},
				}
				if tt.mutate != nil {
					version = tt.mutate(version)
				}
				var got string
				if err := validateAvailableOpenShiftVersion20240610(version, name, group); err != nil {
					got = err.Error()
				}
				want := tt.want
				if want != "" {
					want = fmt.Sprintf("expected name=%q channelGroup=%q enabled=true; observed %s", name, group, want)
				}
				require.Equal(t, want, got, "validation error")
			})
		}
	}
}

type versionAvailabilityTransport func(*http.Request) (*http.Response, error)

func (transport versionAvailabilityTransport) Do(req *http.Request) (*http.Response, error) {
	return transport(req)
}

func newVersionAvailabilityClient(t *testing.T, transport versionAvailabilityTransport, retries int32) *hcpsdk20240610preview.HcpOpenShiftVersionsClient {
	t.Helper()
	client, err := hcpsdk20240610preview.NewHcpOpenShiftVersionsClient(fakeSubscriptionID, &azfake.TokenCredential{}, &azcorearm.ClientOptions{
		ClientOptions: azcore.ClientOptions{
			Transport: transport,
			Retry:     policy.RetryOptions{MaxRetries: retries, RetryDelay: time.Millisecond},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	return client
}

const versionAvailabilityPath = "/subscriptions/" + fakeSubscriptionID + "/providers/Microsoft.RedHatOpenShift/locations/uksouth/hcpOpenShiftVersions"
const versionAvailabilityQuery = "?api-version=2024-06-10-preview"
const availableVersionJSON = `{"name":"4.21","properties":{"channelGroup":"stable","enabled":true}}`
const availableVersionsJSON = `{"value":[` + availableVersionJSON + `]}`

func TestWaitForOpenShiftVersionAvailablePaginationAndRetries(t *testing.T) {
	list := versionAvailabilityPath + versionAvailabilityQuery
	get := versionAvailabilityPath + "/4.21" + versionAvailabilityQuery
	next := versionAvailabilityPath + versionAvailabilityQuery + "&page=2"
	for _, tt := range []struct {
		name   string
		status int
		err    error
	}{
		{name: "not found", status: http.StatusNotFound},
		{name: "service unavailable", status: http.StatusServiceUnavailable},
		{name: "throttled", status: http.StatusTooManyRequests},
		{name: "server error", status: http.StatusInternalServerError},
		{name: "transport", err: &net.OpError{Op: "read", Net: "tcp", Err: syscall.ECONNRESET}},
		{name: "HTTP transport", err: &url.Error{Op: "Get", URL: "https://management.azure.com", Err: syscall.ECONNRESET}},
		{name: "body read", err: fmt.Errorf("reading body: %w", io.ErrUnexpectedEOF)},
	} {
		for _, operation := range []string{"LIST", "LIST page 2", "GET"} {
			t.Run(tt.name+"/"+operation, func(t *testing.T) {
				type step struct {
					uri    string
					body   string
					status int
					err    error
				}
				// First poll: absence across two pages. Second poll: transient error.
				steps := []step{
					{uri: list, body: `{"value":[{"name":"4.20"}],"nextLink":"https://management.azure.com` + next + `"}`, status: 200},
					{uri: next, body: `{"value":[]}`, status: 200},
				}
				failedURI := list
				switch operation {
				case "GET":
					steps = append(steps, step{uri: list, body: availableVersionsJSON, status: 200})
					failedURI = get
				case "LIST page 2":
					steps = append(steps, step{uri: list, body: `{"value":[` + availableVersionJSON + `],"nextLink":"https://management.azure.com` + next + `"}`, status: 200})
					failedURI = next
				}
				steps = append(steps, step{uri: failedURI, status: tt.status, err: tt.err})
				// A fresh pager must start at page one and still traverse page two
				// when the expected version is already on page one.
				steps = append(steps,
					step{uri: list, body: `{"value":[` + availableVersionJSON + `],"nextLink":"https://management.azure.com` + next + `"}`, status: 200},
					step{uri: next, body: `{"value":[{"name":"4.22"}]}`, status: 200},
					step{uri: get, body: availableVersionJSON, status: 200},
				)
				var gotURIs []string
				client := newVersionAvailabilityClient(t, func(req *http.Request) (*http.Response, error) {
					i := len(gotURIs)
					gotURIs = append(gotURIs, req.URL.RequestURI())
					if i >= len(steps) {
						t.Fatalf("unexpected request: %s", req.URL)
					}
					s := steps[i]
					if s.err != nil {
						return nil, s.err
					}
					return &http.Response{StatusCode: s.status, Header: http.Header{}, Body: io.NopCloser(strings.NewReader(s.body)), Request: req}, nil
				}, -1)
				ctx, cancel := context.WithTimeout(context.Background(), time.Second)
				defer cancel()
				err := waitForOpenShiftVersionAvailable20240610(ctx, client, "uksouth", coreapi.VersionProfile{ID: "4.21", ChannelGroup: "stable"}, time.Millisecond)
				if err != nil {
					t.Fatal(err)
				}
				var wantURIs []string
				for _, s := range steps {
					wantURIs = append(wantURIs, s.uri)
				}
				require.Empty(t, cmp.Diff(wantURIs, gotURIs), "requests (-want +got)")
			})
		}
	}
}

func TestWaitForOpenShiftVersionAvailableRejectsInvalidResponses(t *testing.T) {
	for _, tt := range []struct {
		name   string
		status int
		body   string
		want   string
	}{
		{name: "bad request", status: 400, want: "HTTP 400"},
		{name: "unauthorized", status: 401, want: "HTTP 401"},
		{name: "forbidden", status: 403, want: "HTTP 403"},
		{name: "wrong name", status: 200, body: `{"name":"4.20"}`, want: `observed name="4.20" channelGroup="<nil>" enabled=<nil>`},
		{name: "wrong group", status: 200, body: `{"name":"4.21","properties":{"channelGroup":"fast","enabled":true}}`, want: `observed name="4.21" channelGroup="fast" enabled=true`},
		{name: "disabled", status: 200, body: `{"name":"4.21","properties":{"channelGroup":"stable","enabled":false}}`, want: `observed name="4.21" channelGroup="stable" enabled=false`},
		{name: "disagreement", status: 200, body: `{"id":"different","name":"4.21","properties":{"channelGroup":"stable","enabled":true}}`, want: "LIST and GET disagree"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			requests := 0
			client := newVersionAvailabilityClient(t, func(req *http.Request) (*http.Response, error) {
				requests++
				status, body := tt.status, tt.body
				if requests == 1 {
					status, body = 200, availableVersionsJSON
				}
				return &http.Response{StatusCode: status, Header: http.Header{}, Body: io.NopCloser(strings.NewReader(body)), Request: req}, nil
			}, -1)
			ctx, cancel := context.WithTimeout(context.Background(), time.Second)
			defer cancel()
			err := waitForOpenShiftVersionAvailable20240610(ctx, client, "uksouth", coreapi.VersionProfile{ID: "4.21", ChannelGroup: "stable"}, time.Millisecond)
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("error = %v, want %q", err, tt.want)
			}
			if tt.name == "disagreement" {
				require.ErrorContains(t, err, "(-LIST +GET)")
				require.ErrorContains(t, err, `"different"`)
				require.ErrorContains(t, err, "ID:")
			}
			require.Equal(t, 2, requests, "requests")
		})
	}
}

func TestWaitForOpenShiftVersionAvailableNonStableLaterPage(t *testing.T) {
	const version = `{"name":"4.21-candidate","properties":{"channelGroup":"candidate","enabled":true}}`
	list := versionAvailabilityPath + versionAvailabilityQuery
	next := list + "&page=2"
	get := versionAvailabilityPath + "/4.21-candidate" + versionAvailabilityQuery
	var requests []string
	client := newVersionAvailabilityClient(t, func(req *http.Request) (*http.Response, error) {
		uri := req.URL.RequestURI()
		requests = append(requests, uri)
		var body string
		switch uri {
		case list:
			body = `{"value":[` + availableVersionJSON + `],"nextLink":"https://management.azure.com` + next + `"}`
		case next:
			body = `{"value":[` + version + `]}`
		case get:
			body = version
		default:
			t.Fatalf("unexpected request: %s", uri)
		}
		return &http.Response{StatusCode: 200, Header: http.Header{}, Body: io.NopCloser(strings.NewReader(body)), Request: req}, nil
	}, -1)
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	err := WaitForOpenShiftVersionAvailable20240610(ctx, client, "uksouth", coreapi.VersionProfile{ID: "4.21", ChannelGroup: "candidate"})
	if err != nil {
		t.Fatal(err)
	}
	require.Empty(t, cmp.Diff([]string{list, next, get}, requests), "requests (-want +got)")
}

func TestWaitForOpenShiftVersionAvailableMalformedResponses(t *testing.T) {
	for _, tt := range []struct {
		name string
		body string
	}{
		{name: "syntax", body: `{"name":!}`},
		{name: "truncated JSON", body: `{"name":`},
		{name: "wrong object type", body: `[]`},
		{name: "wrong property type", body: `{"name":"4.21","properties":{"enabled":"true"}}`},
	} {
		for _, operation := range []string{"LIST", "GET"} {
			t.Run(tt.name+"/"+operation, func(t *testing.T) {
				requests := 0
				wantRequests := 1
				if operation == "GET" {
					wantRequests = 2
				}
				client := newVersionAvailabilityClient(t, func(req *http.Request) (*http.Response, error) {
					requests++
					require.LessOrEqual(t, requests, wantRequests, "malformed responses must not be polled again")
					body := tt.body
					if operation == "LIST" {
						body = `{"value":[` + body + `]}`
					} else if requests == 1 {
						body = availableVersionsJSON
					}
					return &http.Response{StatusCode: 200, Header: http.Header{}, Body: io.NopCloser(strings.NewReader(body)), Request: req}, nil
				}, 3)
				ctx, cancel := context.WithTimeout(context.Background(), time.Second)
				defer cancel()
				err := waitForOpenShiftVersionAvailable20240610(ctx, client, "uksouth", coreapi.VersionProfile{ID: "4.21", ChannelGroup: "stable"}, time.Millisecond)
				require.ErrorContains(t, err, operation+": unmarshalling")
				require.NoError(t, ctx.Err(), "decoding errors must fail before the polling budget expires")
				require.Equal(t, wantRequests, requests)
			})
		}
	}
}

func TestWaitForOpenShiftVersionAvailableInvalidListedVersion(t *testing.T) {
	for _, properties := range []string{
		`null`,
		`{"channelGroup":"fast","enabled":true}`,
		`{"channelGroup":"stable","enabled":false}`,
		`{"channelGroup":"stable"}`,
	} {
		t.Run(properties, func(t *testing.T) {
			var requests []string
			client := newVersionAvailabilityClient(t, func(req *http.Request) (*http.Response, error) {
				requests = append(requests, req.URL.RequestURI())
				require.Len(t, requests, 1, "invalid LIST entry must fail without GET or another poll")
				body := `{"value":[{"name":"4.21","properties":` + properties + `}]}`
				return &http.Response{StatusCode: 200, Header: http.Header{}, Body: io.NopCloser(strings.NewReader(body)), Request: req}, nil
			}, -1)
			ctx, cancel := context.WithTimeout(context.Background(), time.Second)
			defer cancel()
			err := waitForOpenShiftVersionAvailable20240610(ctx, client, "uksouth", coreapi.VersionProfile{ID: "4.21", ChannelGroup: "stable"}, time.Millisecond)
			require.ErrorContains(t, err, `LIST HTTP 200; expected name="4.21" channelGroup="stable" enabled=true; observed`)
			require.NoError(t, ctx.Err())
			require.Empty(t, cmp.Diff([]string{versionAvailabilityPath + versionAvailabilityQuery}, requests), "requests (-want +got)")
		})
	}
}

func TestWaitForOpenShiftVersionAvailableRequestErrors(t *testing.T) {
	for _, scenario := range []string{"empty location", "invalid next link", "unclassified error"} {
		t.Run(scenario, func(t *testing.T) {
			requests := 0
			client := newVersionAvailabilityClient(t, func(req *http.Request) (*http.Response, error) {
				requests++
				require.Equal(t, 1, requests, "deterministic errors must not be retried")
				if scenario == "unclassified error" {
					return nil, errors.New("invalid client configuration")
				}
				return &http.Response{StatusCode: 200, Header: http.Header{}, Body: io.NopCloser(strings.NewReader(`{"value":[],"nextLink":"https://invalid/%zz"}`)), Request: req}, nil
			}, -1)
			location, want, wantRequests := "uksouth", "invalid URL escape", 1
			switch scenario {
			case "empty location":
				location, want, wantRequests = "", "parameter location cannot be empty", 0
			case "unclassified error":
				want = "invalid client configuration"
			}
			ctx, cancel := context.WithTimeout(context.Background(), time.Second)
			defer cancel()
			err := waitForOpenShiftVersionAvailable20240610(ctx, client, location, coreapi.VersionProfile{ID: "4.21", ChannelGroup: "stable"}, time.Millisecond)
			require.ErrorContains(t, err, want)
			require.NoError(t, ctx.Err())
			require.Equal(t, wantRequests, requests)
		})
	}
}

func TestWaitForOpenShiftVersionAvailableSDKRetry(t *testing.T) {
	list := versionAvailabilityPath + versionAvailabilityQuery
	get := versionAvailabilityPath + "/4.21" + versionAvailabilityQuery
	for _, status := range []int{http.StatusServiceUnavailable, http.StatusTooManyRequests} {
		for _, operation := range []string{"LIST", "GET"} {
			t.Run(fmt.Sprintf("%d/%s", status, operation), func(t *testing.T) {
				wantRequests := []string{list, list, get}
				failedRequest := 1
				if operation == "GET" {
					wantRequests = []string{list, get, get}
					failedRequest = 2
				}
				var requests []string
				client := newVersionAvailabilityClient(t, func(req *http.Request) (*http.Response, error) {
					requests = append(requests, req.URL.RequestURI())
					require.LessOrEqual(t, len(requests), len(wantRequests))
					response := &http.Response{StatusCode: 200, Header: http.Header{}, Request: req}
					body := availableVersionJSON
					if req.URL.RequestURI() == list {
						body = availableVersionsJSON
					}
					if len(requests) == failedRequest {
						response.StatusCode = status
						response.Header.Set("x-ms-retry-after-ms", "1")
						body = ""
					}
					response.Body = io.NopCloser(strings.NewReader(body))
					return response, nil
				}, 3)
				ctx, cancel := context.WithTimeout(context.Background(), time.Second)
				defer cancel()
				// The polling interval exceeds the budget: success requires the SDK
				// to retry within the first poll, not a new polling iteration.
				err := waitForOpenShiftVersionAvailable20240610(ctx, client, "uksouth", coreapi.VersionProfile{ID: "4.21", ChannelGroup: "stable"}, StandardPollInterval)
				require.NoError(t, err)
				require.Empty(t, cmp.Diff(wantRequests, requests), "requests (-want +got)")
			})
		}
	}
}

type cancelVersionBody struct{ cancel context.CancelFunc }

func (body cancelVersionBody) Read([]byte) (int, error) {
	body.cancel()
	return 0, io.ErrUnexpectedEOF
}

func (cancelVersionBody) Close() error { return nil }

func TestWaitForOpenShiftVersionAvailableCancellation(t *testing.T) {
	for _, scenario := range []string{"poll", "transport", "LIST body", "GET body", "Retry-After"} {
		t.Run(scenario, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
			defer cancel()
			requests := 0
			client := newVersionAvailabilityClient(t, func(req *http.Request) (*http.Response, error) {
				requests++
				response := &http.Response{StatusCode: 200, Header: http.Header{}, Body: io.NopCloser(strings.NewReader(`{"value":[{"name":"4.20"}]}`)), Request: req}
				switch scenario {
				case "transport":
					<-req.Context().Done()
					return nil, req.Context().Err()
				case "LIST body":
					response.Body = cancelVersionBody{cancel: cancel}
				case "GET body":
					response.Body = io.NopCloser(strings.NewReader(availableVersionsJSON))
					if requests == 2 {
						response.Body = cancelVersionBody{cancel: cancel}
					}
				case "Retry-After":
					response.StatusCode = 503
					response.Header.Set("Retry-After", "59")
					response.Body = http.NoBody
				}
				return response, nil
			}, 3)
			err := waitForOpenShiftVersionAvailable20240610(ctx, client, "uksouth", coreapi.VersionProfile{ID: "4.21", ChannelGroup: "stable"}, StandardPollInterval)
			if !errors.Is(err, ctx.Err()) || err == nil {
				t.Fatalf("error = %v, want context error %v", err, ctx.Err())
			}
			for _, diagnostic := range []string{`minor="4.21"`, `channelGroup="stable"`, `location "uksouth"`, "last status:", "observed names="} {
				if !strings.Contains(err.Error(), diagnostic) {
					t.Errorf("error %q missing diagnostic %q", err, diagnostic)
				}
			}
			if scenario == "poll" && !strings.Contains(err.Error(), "LIST HTTP 200; expected version absent; observed names=[4.20]") {
				t.Errorf("missing last observation: %v", err)
			}
			wantRequests := 1
			if scenario == "GET body" {
				wantRequests = 2
			}
			require.Equal(t, wantRequests, requests, "requests")
		})
	}
}

func TestWaitForOpenShiftVersionAvailableBudget(t *testing.T) {
	parent, cancel := context.WithCancel(context.Background())
	defer cancel()
	client := newVersionAvailabilityClient(t, func(req *http.Request) (*http.Response, error) {
		deadline, ok := req.Context().Deadline()
		if !ok || time.Until(deadline) > 15*time.Minute || time.Until(deadline) < 14*time.Minute {
			t.Error("SDK request must have a separate 15-minute availability budget")
		}
		return &http.Response{StatusCode: 403, Header: http.Header{}, Body: http.NoBody, Request: req}, nil
	}, -1)
	err := WaitForOpenShiftVersionAvailable20240610(parent, client, "uksouth", coreapi.VersionProfile{ID: "4.21", ChannelGroup: "stable"})
	if err == nil || !strings.Contains(err.Error(), "LIST HTTP 403") {
		t.Fatalf("error = %v, want immediate LIST HTTP 403", err)
	}
	if parent.Err() != nil {
		t.Fatalf("availability wait cancelled parent: %v", parent.Err())
	}
}
