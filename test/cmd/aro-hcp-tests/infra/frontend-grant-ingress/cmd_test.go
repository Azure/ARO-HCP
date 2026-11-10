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

package frontendgrantingress

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/netip"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	azcorearm "github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/cloud"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/policy"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/network/armnetwork/v6"
)

type ingressCredential struct{}

func (ingressCredential) GetToken(context.Context, policy.TokenRequestOptions) (azcore.AccessToken, error) {
	return azcore.AccessToken{Token: "test", ExpiresOn: time.Now().Add(time.Hour)}, nil
}

type ingressTransport func(*http.Request) (*http.Response, error)

func (f ingressTransport) Do(request *http.Request) (*http.Response, error) {
	return f(request)
}

func ingressResponse(request *http.Request, status int, body string) *http.Response {
	return &http.Response{
		Request:    request,
		StatusCode: status,
		Header:     http.Header{"Content-Type": []string{"application/json"}},
		Body:       io.NopCloser(strings.NewReader(body)),
	}
}

func ingressClient(t *testing.T, transport ingressTransport) *armnetwork.SecurityRulesClient {
	t.Helper()
	client, err := armnetwork.NewSecurityRulesClient("explicit-subscription", ingressCredential{}, &azcorearm.ClientOptions{
		ClientOptions: policy.ClientOptions{
			Cloud: cloud.Configuration{
				Services: map[cloud.ServiceName]cloud.ServiceConfiguration{
					cloud.ResourceManager: {
						Endpoint: "https://management.example.test",
						Audience: "https://management.example.test",
					},
				},
			},
			Transport: transport,
			Retry:     policy.RetryOptions{MaxRetries: -1},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	return client
}

const ingressPath = "/subscriptions/explicit-subscription/resourceGroups/frontend-rg/providers/Microsoft.Network/networkSecurityGroups/svc-cluster-node-nsg/securityRules/allow-istio-ingress"

func TestRunCreatesOnlyChildRuleAndWaitsForCompletion(t *testing.T) {
	var out bytes.Buffer
	var requests []string
	client := ingressClient(t, func(request *http.Request) (*http.Response, error) {
		if out.Len() != 0 {
			t.Fatalf("success output appeared before Azure completion: %q", out.String())
		}
		requests = append(requests, request.Method+" "+request.URL.Path)
		switch len(requests) {
		case 1:
			if request.Method != http.MethodPut || request.URL.Path != ingressPath {
				t.Fatalf("unexpected mutation: %s %s", request.Method, request.URL)
			}
			if request.URL.Query().Get("api-version") == "" {
				t.Fatal("SDK request missing API version")
			}
			var payload map[string]any
			if err := json.NewDecoder(request.Body).Decode(&payload); err != nil {
				t.Fatal(err)
			}
			want := map[string]any{"properties": map[string]any{
				"priority":                 float64(1000),
				"protocol":                 "Tcp",
				"access":                   "Allow",
				"direction":                "Inbound",
				"sourceAddressPrefix":      "203.0.113.42",
				"sourcePortRange":          "*",
				"destinationAddressPrefix": "*",
				"destinationPortRange":     "443",
			}}
			if !reflect.DeepEqual(payload, want) {
				t.Fatalf("unexpected rule payload: %#v", payload)
			}
			response := ingressResponse(request, http.StatusCreated, `{"properties":{"provisioningState":"Updating"}}`)
			response.Header.Set("Azure-AsyncOperation", "https://management.example.test/operations/ingress")
			return response, nil
		case 2:
			if request.Method != http.MethodGet || request.URL.Path != "/operations/ingress" {
				t.Fatalf("unexpected polling request: %s %s", request.Method, request.URL)
			}
			return ingressResponse(request, http.StatusOK, `{"status":"Succeeded"}`), nil
		case 3:
			if request.Method != http.MethodGet || request.URL.Path != ingressPath {
				t.Fatalf("unexpected final resource request: %s %s", request.Method, request.URL)
			}
			return ingressResponse(request, http.StatusOK, `{"properties":{"provisioningState":"Succeeded"}}`), nil
		default:
			t.Fatalf("unexpected extra request: %s %s", request.Method, request.URL)
			return nil, errors.New("unexpected request")
		}
	})
	opts := &Options{completedOptions: &completedOptions{
		client: client, resourceGroup: "frontend-rg", source: netip.MustParseAddr("203.0.113.42"), out: &out,
	}}
	if err := opts.Run(t.Context()); err != nil {
		t.Fatal(err)
	}
	if len(requests) != 3 {
		t.Fatalf("did not wait for async operation and final resource: %v", requests)
	}
	var result map[string]any
	if err := json.Unmarshal(out.Bytes(), &result); err != nil {
		t.Fatalf("decode ingress result: %v", err)
	}
	if got := result["resourceGroup"]; got != "frontend-rg" {
		t.Fatalf("resourceGroup = %v, want frontend-rg", got)
	}
}

func TestRunAzureFailuresProduceNoSuccess(t *testing.T) {
	tests := []struct {
		name       string
		failCreate bool
		cancelPoll bool
		wantError  string
	}{
		{name: "create rejected", failCreate: true, wantError: "AuthorizationFailed"},
		{name: "async operation failed", wantError: "RuleUpdateFailed"},
		{name: "canceled while polling", cancelPoll: true, wantError: "context canceled"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			var out bytes.Buffer
			requests := 0
			client := ingressClient(t, func(request *http.Request) (*http.Response, error) {
				requests++
				if requests == 1 {
					if request.Method != http.MethodPut || request.URL.Path != ingressPath {
						t.Fatalf("unexpected mutation: %s %s", request.Method, request.URL)
					}
					if test.failCreate {
						return ingressResponse(request, http.StatusForbidden, `{"error":{"code":"AuthorizationFailed","message":"not allowed"}}`), nil
					}
					response := ingressResponse(request, http.StatusCreated, `{"properties":{"provisioningState":"Updating"}}`)
					response.Header.Set("Azure-AsyncOperation", "https://management.example.test/operations/ingress")
					return response, nil
				}
				if requests != 2 || request.Method != http.MethodGet || request.URL.Path != "/operations/ingress" {
					t.Fatalf("unexpected polling request: %s %s", request.Method, request.URL)
				}
				if test.cancelPoll {
					cancel()
					return nil, request.Context().Err()
				}
				return ingressResponse(request, http.StatusOK, `{"status":"Failed","error":{"code":"RuleUpdateFailed","message":"operation failed"}}`), nil
			})
			opts := &Options{completedOptions: &completedOptions{
				client: client, resourceGroup: "frontend-rg", source: netip.MustParseAddr("203.0.113.42"), out: &out,
			}}
			err := opts.Run(ctx)
			if err == nil || !strings.Contains(err.Error(), test.wantError) {
				t.Fatalf("error = %v, want %s", err, test.wantError)
			}
			if test.cancelPoll && !errors.Is(err, context.Canceled) {
				t.Fatalf("cancellation cause not preserved: %v", err)
			}
			if out.Len() != 0 {
				t.Fatalf("failure emitted success: %q", out.String())
			}
		})
	}
}

func TestCommandRequiresExplicitCredentialSelection(t *testing.T) {
	t.Setenv("AZURE_TOKEN_CREDENTIALS", "")
	command := NewCommand()
	var out, diagnostics bytes.Buffer
	command.SetOut(&out)
	command.SetErr(&diagnostics)
	command.SetArgs([]string{
		"--subscription-id", "explicit-subscription",
		"--resource-group", "frontend-rg",
		"--source-ip", "203.0.113.42",
	})
	err := command.ExecuteContext(t.Context())
	if err == nil || !strings.Contains(err.Error(), "AZURE_TOKEN_CREDENTIALS") {
		t.Fatalf("command without credential selection = %v, want credential selector error", err)
	}
	if out.Len() != 0 {
		t.Fatalf("credential rejection wrote success output: %q", out.String())
	}
}

func TestCommandRejectsInvalidInputBeforeAuthentication(t *testing.T) {
	// Any accidental credential construction fails with this selector, rather
	// than the input error required below. No Azure token or network is needed.
	t.Setenv("AZURE_TOKEN_CREDENTIALS", "not-a-credential")
	tests := []struct {
		name         string
		subscription string
		group        string
		source       string
		wantError    string
	}{
		{name: "empty subscription", group: "rg", source: "203.0.113.42", wantError: "--subscription-id is required"},
		{name: "blank subscription", subscription: " ", group: "rg", source: "203.0.113.42", wantError: "--subscription-id is required"},
		{name: "empty group", subscription: "sub", source: "203.0.113.42", wantError: "--resource-group is required"},
		{name: "empty source", subscription: "sub", group: "rg", wantError: "--source-ip is required"},
		{name: "CIDR", subscription: "sub", group: "rg", source: "203.0.113.42/32", wantError: "invalid --source-ip"},
		{name: "IPv6", subscription: "sub", group: "rg", source: "2001:db8::1", wantError: "invalid --source-ip"},
		{name: "mapped IPv6", subscription: "sub", group: "rg", source: "::ffff:203.0.113.42", wantError: "invalid --source-ip"},
		{name: "whitespace", subscription: "sub", group: "rg", source: " 203.0.113.42", wantError: "invalid --source-ip"},
		{name: "leading zero", subscription: "sub", group: "rg", source: "203.0.113.042", wantError: "invalid --source-ip"},
		{name: "multiple addresses", subscription: "sub", group: "rg", source: "203.0.113.42,203.0.113.43", wantError: "invalid --source-ip"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			command := NewCommand()
			var out, diagnostics bytes.Buffer
			command.SetOut(&out)
			command.SetErr(&diagnostics)
			command.SetArgs([]string{"--subscription-id", test.subscription, "--resource-group", test.group, "--source-ip", test.source})
			err := command.ExecuteContext(t.Context())
			if err == nil || !strings.Contains(err.Error(), test.wantError) {
				t.Fatalf("error = %v, want %s", err, test.wantError)
			}
			if out.Len() != 0 {
				t.Fatalf("invalid input wrote stdout: %q", out.String())
			}
		})
	}
}

func TestCommandRequiresExplicitFlags(t *testing.T) {
	for _, missing := range []string{"subscription-id", "resource-group", "source-ip"} {
		t.Run(missing, func(t *testing.T) {
			command := NewCommand()
			var out, diagnostics bytes.Buffer
			command.SetOut(&out)
			command.SetErr(&diagnostics)
			var args []string
			for _, flag := range []string{"subscription-id", "resource-group", "source-ip"} {
				if flag == missing {
					continue
				}
				value := "explicit"
				if flag == "source-ip" {
					value = "203.0.113.42"
				}
				args = append(args, "--"+flag, value)
			}
			command.SetArgs(args)
			if err := command.ExecuteContext(t.Context()); err == nil || !strings.Contains(err.Error(), missing) {
				t.Fatalf("missing flag %s: %v", missing, err)
			}
			if out.Len() != 0 {
				t.Fatalf("missing flag wrote stdout: %q", out.String())
			}
		})
	}
}
