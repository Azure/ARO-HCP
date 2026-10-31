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

package providers

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/policy"
)

type credential struct{}

func (credential) GetToken(context.Context, policy.TokenRequestOptions) (azcore.AccessToken, error) {
	return azcore.AccessToken{Token: "test", ExpiresOn: time.Now().Add(time.Hour)}, nil
}

type transport func(*http.Request) (*http.Response, error)

func (f transport) Do(request *http.Request) (*http.Response, error) { return f(request) }

func testProvider(t *testing.T, do transport) *IstioProvider {
	t.Helper()
	options := azureDiscoveryOptions()
	options.Transport = do
	options.Retry = policy.RetryOptions{MaxRetries: 1, RetryDelay: time.Millisecond, MaxRetryDelay: time.Millisecond}
	provider, err := newIstioProvider("test-sub", credential{}, options)
	if err != nil {
		t.Fatal(err)
	}
	return provider
}

func response(request *http.Request, status int, body string) *http.Response {
	return &http.Response{Request: request, StatusCode: status, Header: http.Header{"Content-Type": {"application/json"}}, Body: io.NopCloser(strings.NewReader(body))}
}

func profile(upgrades, compatible string) string {
	return profileWithCompatibility(upgrades, fmt.Sprintf(`[
		{"name":"KubernetesOfficial","versions":%s},
		{"name":"AKSLongTermSupport","versions":%s}
	]`, compatible, compatible))
}

func profileWithCompatibility(upgrades, compatible string) string {
	return fmt.Sprintf(`{"value":[{"name":"istio","properties":{"meshRevisions":[
		{"revision":"asm-1-29","upgrades":%s,"compatibleWith":[{"name":"KubernetesOfficial","versions":["1.35"]}]},
		{"revision":"asm-1-30","compatibleWith":%s}
	]}}]}`, upgrades, compatible)
}

func meshLocations(locations string) string {
	return fmt.Sprintf(`{"resourceTypes":[{"resourceType":"locations/meshRevisionProfiles","locations":%s}]}`, locations)
}

func TestIstioDiscovery(t *testing.T) {
	tests := []struct {
		name, body, current, kubernetes, want string
		wantError                             bool
	}{
		{"eligible", profile(`["asm-1-30"]`, `["1.35"]`), "asm-1-29", "1.35", "asm-1-30", false},
		{"patch version", profile(`["asm-1-30"]`, `["1.35"]`), "asm-1-29", "1.35.2", "asm-1-30", false},
		{"incompatible", profile(`["asm-1-30"]`, `["1.36"]`), "asm-1-29", "1.35", "", false},
		{"official only", profileWithCompatibility(`["asm-1-30"]`, `[{"name":"KubernetesOfficial","versions":["1.35"]}]`), "asm-1-29", "1.35", "asm-1-30", false},
		{"LTS only", profileWithCompatibility(`["asm-1-30"]`, `[{"name":"AKSLongTermSupport","versions":["1.35"]}]`), "asm-1-29", "1.35", "", false},
		{"LTS cannot override official incompatibility", profileWithCompatibility(`["asm-1-30"]`, `[{"name":"KubernetesOfficial","versions":["1.36"]},{"name":"AKSLongTermSupport","versions":["1.35"]}]`), "asm-1-29", "1.35", "", false},
		{"unknown category", profileWithCompatibility(`["asm-1-30"]`, `[{"name":"kubernetes","versions":["1.35"]}]`), "asm-1-29", "1.35", "", false},
		{"missing compatibility", profileWithCompatibility(`["asm-1-30"]`, `[]`), "asm-1-29", "1.35", "", false},
		{"unadvertised upgrade", profile(`[]`, `["1.35"]`), "asm-1-29", "1.35", "", false},
		{"no next version", profile(`["asm-1-30"]`, `["1.35"]`), "asm-1-30", "1.35", "", false},
		{"invalid revision", profile(`[]`, `[]`), "not-a-revision", "1.35", "", true},
		{"invalid Kubernetes", profile(`[]`, `[]`), "asm-1-29", "invalid", "", true},
		{"current unavailable", profile(`["asm-1-30"]`, `["1.35"]`), "asm-1-28", "1.35", "", true},
		{"current incompatible", profile(`["asm-1-30"]`, `["1.36"]`), "asm-1-29", "1.36", "", true},
		{"advertised two-minor jump", `{"value":[{"name":"istio","properties":{"meshRevisions":[
			{"revision":"asm-1-29","upgrades":["asm-1-31"],"compatibleWith":[{"name":"KubernetesOfficial","versions":["1.35"]}]},
			{"revision":"asm-1-31","compatibleWith":[{"name":"KubernetesOfficial","versions":["1.35"]}]}
		]}}]}`, "asm-1-29", "1.35", "asm-1-31", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p := testProvider(t, func(request *http.Request) (*http.Response, error) {
				return response(request, 200, tt.body), nil
			})
			result, err := p.Check(context.Background(), CheckRequest{Current: tt.current, Locations: []string{"uksouth"}, ConstraintVersion: tt.kubernetes})
			if (err != nil) != tt.wantError || result.Next != tt.want {
				t.Fatalf("got %+v, %v; want next %q, error %v", result, err, tt.want, tt.wantError)
			}
			if err == nil && (result.Detail == "" || result.Compatibility != "AKS "+tt.kubernetes) {
				t.Fatalf("missing provider-specific compatibility context: %+v", result)
			}
		})
	}
}

func TestDiscoveredCandidatesStayWithinTwoMinors(t *testing.T) {
	catalog := func(upgrades string, revisions ...string) string {
		profiles := []string{fmt.Sprintf(`{"revision":"asm-1-30","upgrades":%s,"compatibleWith":[{"name":"KubernetesOfficial","versions":["1.35"]}]}`, upgrades)}
		for _, revision := range revisions {
			profiles = append(profiles, fmt.Sprintf(`{"revision":%q,"compatibleWith":[{"name":"KubernetesOfficial","versions":["1.35"]}]}`, revision))
		}
		return fmt.Sprintf(`{"value":[{"name":"istio","properties":{"meshRevisions":[%s]}}]}`, strings.Join(profiles, ","))
	}
	for _, tt := range []struct {
		name, westus3, uksouth, candidate, next string
	}{
		{"prefer n+1", catalog(`["asm-1-31","asm-1-32"]`, "asm-1-31", "asm-1-32"), "", "asm-1-31", "asm-1-31"},
		{"n+2 when n+1 is regionally blocked", catalog(`["asm-1-31","asm-1-32"]`, "asm-1-31", "asm-1-32"), catalog(`["asm-1-32"]`, "asm-1-32"), "asm-1-32", "asm-1-32"},
		{"skipped n+1", catalog(`["asm-1-32"]`, "asm-1-32"), "", "asm-1-32", "asm-1-32"},
		{"n+2 requires every upgrade path", catalog(`["asm-1-32"]`, "asm-1-32"), catalog(`[]`, "asm-1-32"), "asm-1-32", ""},
		{"n+3 excluded even when advertised", catalog(`["asm-1-33"]`, "asm-1-33"), "", "", ""},
		{"major change excluded", catalog(`["asm-2-0"]`, "asm-2-0"), "", "", ""},
		{"announced target awaits catalog availability", catalog(`["asm-1-31"]`), "", "asm-1-31", ""},
	} {
		t.Run(tt.name, func(t *testing.T) {
			p := testProvider(t, func(request *http.Request) (*http.Response, error) {
				body := tt.westus3
				if strings.Contains(request.URL.Path, "/uksouth/") && tt.uksouth != "" {
					body = tt.uksouth
				}
				return response(request, 200, body), nil
			})
			result, err := p.Check(context.Background(), CheckRequest{Current: "asm-1-30", Locations: []string{"westus3", "uksouth"}, ConstraintVersion: "1.35"})
			if err != nil || result.Candidate != tt.candidate || result.Next != tt.next {
				t.Fatalf("unexpected discovered selection: %+v, %v; want candidate %q, next %q", result, err, tt.candidate, tt.next)
			}
			for _, region := range result.Regions {
				if result.Next != "" && region.Candidate != "READY" {
					t.Fatalf("selected upgrade must be ready everywhere: %+v", result)
				}
			}
		})
	}
}

func TestDiscoveredRegionsMustAgree(t *testing.T) {
	p := testProvider(t, func(request *http.Request) (*http.Response, error) {
		if strings.HasSuffix(request.URL.Path, "/providers/Microsoft.ContainerService") {
			return response(request, 200, meshLocations(`["eastus2euap","uksouth","westus3"]`)), nil
		}
		if strings.HasSuffix(request.URL.Path, "/locations") {
			return response(request, 200, `{"value":[{"name":"eastus2euap"},{"name":"uksouth"},{"name":"westus3"}]}`), nil
		}
		body := `{"value":[{"name":"istio","properties":{"meshRevisions":[
			{"revision":"asm-1-30","upgrades":["asm-1-31"],"compatibleWith":[{"name":"KubernetesOfficial","versions":["1.35"]}]}
		]}}]}`
		if strings.Contains(request.URL.Path, "/eastus2euap/") {
			body = `{"value":[{"name":"istio","properties":{"meshRevisions":[
				{"revision":"asm-1-30","upgrades":["asm-1-31"],"compatibleWith":[{"name":"KubernetesOfficial","versions":["1.35"]}]},
				{"revision":"asm-1-31","compatibleWith":[{"name":"KubernetesOfficial","versions":["1.35"]}]}
			]}}]}`
		}
		return response(request, 200, body), nil
	})
	result, err := p.Check(context.Background(), CheckRequest{Current: "asm-1-30", Locations: []string{"all"}, ConstraintVersion: "1.35"})
	if err != nil || result.Next != "" || strings.Join(result.Available, ",") != "asm-1-30" {
		t.Fatalf("EUAP-only revision should not be proposed: %+v, %v", result, err)
	}
	if len(result.Regions) != 3 {
		t.Fatalf("expected every discovered region in the report: %+v", result)
	}
	if result.Detail != "update deferred: asm-1-31 is not ready in every region" {
		t.Fatalf("expected a concise summary without repeated regional details: %q", result.Detail)
	}
	for _, region := range result.Regions {
		want := "BLOCKED"
		if region.Location == "eastus2euap" {
			want = "READY"
		}
		if region.Candidate != want {
			t.Fatalf("incorrect candidate status for %s: %+v", region.Location, region)
		}
		if want == "BLOCKED" && region.Detail != "next revision unavailable" {
			t.Fatalf("missing regional blocker detail for %s: %+v", region.Location, region)
		}
	}
}

func TestRetryAndErrors(t *testing.T) {
	for _, status := range []int{429, 503, 403} {
		t.Run(fmt.Sprint(status), func(t *testing.T) {
			calls := 0
			p := testProvider(t, func(request *http.Request) (*http.Response, error) {
				calls++
				if calls == 1 {
					result := response(request, status, `{"error":{"code":"TestError","message":"test"}}`)
					result.Header.Set("Retry-After", "0")
					return result, nil
				}
				return response(request, 200, profile(`["asm-1-30"]`, `["1.35"]`)), nil
			})
			result, err := p.Check(context.Background(), CheckRequest{Current: "asm-1-29", Locations: []string{"uksouth"}, ConstraintVersion: "1.35"})
			if status == 403 {
				if err == nil || calls != 1 {
					t.Fatalf("permission error was retried or swallowed: calls=%d, err=%v", calls, err)
				}
			} else if err != nil || calls != 2 || result.Next != "asm-1-30" {
				t.Fatalf("transient recovery failed: calls=%d, result=%+v, err=%v", calls, result, err)
			}
		})
	}
}

func TestDroppedConnectionUsesNewConnection(t *testing.T) {
	for _, recover := range []bool{true, false} {
		t.Run(fmt.Sprint(recover), func(t *testing.T) {
			var requests, connections atomic.Int32
			server := httptest.NewUnstartedServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
				if requests.Add(1) == 1 || !recover {
					hijacker, ok := writer.(http.Hijacker)
					if !ok {
						t.Error("test server does not support connection hijacking")
						return
					}
					connection, _, err := hijacker.Hijack()
					if err != nil {
						t.Errorf("hijacking test connection: %v", err)
						return
					}
					if err := connection.Close(); err != nil {
						t.Errorf("closing test connection: %v", err)
					}
					return
				}
				writer.Header().Set("Content-Type", "application/json")
				if _, err := io.WriteString(writer, profile(`["asm-1-30"]`, `["1.35"]`)); err != nil {
					t.Errorf("writing test catalog: %v", err)
				}
			}))
			server.Config.ConnState = func(_ net.Conn, state http.ConnState) {
				if state == http.StateNew {
					connections.Add(1)
				}
			}
			server.StartTLS()
			t.Cleanup(server.Close)
			client := server.Client()
			p := testProvider(t, func(request *http.Request) (*http.Response, error) {
				clone := request.Clone(request.Context())
				clone.URL.Host = server.Listener.Addr().String()
				clone.Host = clone.URL.Host
				return client.Do(clone)
			})
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			result, err := p.Check(ctx, CheckRequest{Current: "asm-1-29", Locations: []string{"uksouth"}, ConstraintVersion: "1.35"})
			if (err == nil) != recover || connections.Load() != 2 || requests.Load() != 2 {
				t.Fatalf("broken connection recovery failed: connections=%d, requests=%d, result=%+v, err=%v", connections.Load(), requests.Load(), result, err)
			}
			if recover && result.Next != "asm-1-30" || !recover && result.Next != "" {
				t.Fatalf("unsafe proposal after connection failure: %+v", result)
			}
		})
	}
}

func TestDefaultRetryBudgetIsBounded(t *testing.T) {
	options := azureDiscoveryOptions()
	options.Retry.RetryDelay = time.Millisecond
	options.Retry.MaxRetryDelay = time.Millisecond
	calls := 0
	options.Transport = transport(func(request *http.Request) (*http.Response, error) {
		calls++
		return response(request, 503, `{"error":{"code":"Unavailable","message":"test"}}`), nil
	})
	p, err := newIstioProvider("test-sub", credential{}, options)
	if err != nil {
		t.Fatal(err)
	}
	result, err := p.Check(context.Background(), CheckRequest{Current: "asm-1-29", Locations: []string{"uksouth"}, ConstraintVersion: "1.35"})
	if err == nil || result.Next != "" || calls != 4 {
		t.Fatalf("expected one attempt plus three retries, then no proposal: calls=%d, result=%+v, err=%v", calls, result, err)
	}
}

func TestPerTryTimeoutCanRecover(t *testing.T) {
	options := azureDiscoveryOptions()
	options.Retry = policy.RetryOptions{MaxRetries: 1, RetryDelay: time.Millisecond, MaxRetryDelay: time.Millisecond, TryTimeout: 20 * time.Millisecond}
	calls := 0
	options.Transport = transport(func(request *http.Request) (*http.Response, error) {
		calls++
		if calls == 1 {
			<-request.Context().Done()
			return nil, request.Context().Err()
		}
		return response(request, 200, profile(`["asm-1-30"]`, `["1.35"]`)), nil
	})
	p, err := newIstioProvider("test-sub", credential{}, options)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	result, err := p.Check(ctx, CheckRequest{Current: "asm-1-29", Locations: []string{"uksouth"}, ConstraintVersion: "1.35"})
	if err != nil || result.Next != "asm-1-30" || calls != 2 {
		t.Fatalf("per-try timeout did not recover within the run deadline: calls=%d, result=%+v, err=%v", calls, result, err)
	}
}

func TestSubscriptionDiscovery(t *testing.T) {
	tests := []struct {
		name, catalog string
		status        int
		wantError     bool
	}{
		{"supported", profile(`["asm-1-30"]`, `["1.35"]`), 200, false},
		{"missing catalog", `{"value":[]}`, 200, true},
		{"empty catalog", `{"value":[{"name":"istio","properties":{"meshRevisions":[]}}]}`, 200, true},
		{"permission failure", `{"error":{"code":"Forbidden","message":"test"}}`, 403, true},
		{"server failure after retries", `{"error":{"code":"Unavailable","message":"test"}}`, 503, true},
		{"malformed catalog", `{`, 200, true},
		{"missing properties", `{"value":[{"name":"istio"}]}`, 200, true},
		{"missing profile name", `{"value":[{}]}`, 200, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p := testProvider(t, func(request *http.Request) (*http.Response, error) {
				if strings.HasSuffix(request.URL.Path, "/providers/Microsoft.ContainerService") {
					return response(request, 200, meshLocations(`["uksouth","westus3"]`)), nil
				}
				if strings.HasSuffix(request.URL.Path, "/locations") {
					return response(request, 200, `{"value":[{"name":"westus3"},{"name":"uksouth"},{"name":"uksouth"}]}`), nil
				}
				if strings.Contains(request.URL.Path, "/westus3/") {
					return response(request, tt.status, tt.catalog), nil
				}
				return response(request, 200, profile(`["asm-1-30"]`, `["1.35"]`)), nil
			})
			result, err := p.Check(context.Background(), CheckRequest{Current: "asm-1-29", Locations: []string{"all"}, ConstraintVersion: "1.35"})
			if (err != nil) != tt.wantError || len(result.Regions) != 2 {
				t.Fatalf("unexpected subscription discovery: %+v, %v", result, err)
			}
			if tt.wantError && result.Next != "" || !tt.wantError && result.Next != "asm-1-30" {
				t.Fatalf("unsafe proposal: %+v", result)
			}
			if tt.wantError {
				if result.Regions[1].Current != "ERROR" || result.Regions[1].Candidate != "BLOCKED" {
					t.Fatalf("failed region must block rather than be excluded: %+v", result)
				}
			}
		})
	}
}

func TestSubscriptionDiscoveryFailsClosed(t *testing.T) {
	for _, body := range []string{`{"value":[]}`, `{`, `{"value":[{}]}`} {
		t.Run(body, func(t *testing.T) {
			p := testProvider(t, func(request *http.Request) (*http.Response, error) {
				return response(request, 200, body), nil
			})
			if result, err := p.Check(context.Background(), CheckRequest{Current: "asm-1-29", Locations: []string{"all"}, ConstraintVersion: "1.35"}); err == nil || result.Next != "" {
				t.Fatalf("invalid discovery should fail: %+v, %v", result, err)
			}
		})
	}
}

func TestMeshSupportedLocationDiscovery(t *testing.T) {
	for _, tt := range []struct {
		name, metadata  string
		networkFailures int
		wantError       bool
	}{
		{"supported regions exclude staging", meshLocations(`["UK South","West US 3"]`), 0, false},
		{"metadata retry recovers", meshLocations(`["UK South","West US 3"]`), 1, false},
		{"metadata retries exhausted", meshLocations(`["UK South","West US 3"]`), 2, true},
		{"missing mesh resource type", `{"resourceTypes":[{"resourceType":"managedClusters"}]}`, 0, true},
		{"empty supported locations", meshLocations(`[]`), 0, true},
		{"unmapped supported region", meshLocations(`["UK South","East US 2 EUAP"]`), 0, true},
		{"malformed metadata", `{`, 0, true},
	} {
		t.Run(tt.name, func(t *testing.T) {
			metadataCalls := 0
			var catalogCalls, stagingCalls atomic.Int32
			p := testProvider(t, func(request *http.Request) (*http.Response, error) {
				switch {
				case strings.HasSuffix(request.URL.Path, "/locations"):
					return response(request, 200, `{"value":[
						{"name":"uksouth","displayName":"UK South"},
						{"name":"westus3","displayName":"West US 3"},
						{"name":"westus2stage","displayName":"West US 2 Stage"},
						{"name":"westusstage","displayName":"West US Stage"}
					]}`), nil
				case strings.HasSuffix(request.URL.Path, "/providers/Microsoft.ContainerService"):
					metadataCalls++
					if metadataCalls <= tt.networkFailures {
						return nil, io.ErrUnexpectedEOF
					}
					return response(request, 200, tt.metadata), nil
				default:
					catalogCalls.Add(1)
					if strings.Contains(request.URL.Path, "stage/") {
						stagingCalls.Add(1)
						return response(request, 400, `{"error":{"code":"NoRegisteredProviderFound","message":"unsupported location"}}`), nil
					}
					return response(request, 200, profile(`["asm-1-30"]`, `["1.35"]`)), nil
				}
			})
			result, err := p.Check(context.Background(), CheckRequest{Current: "asm-1-29", Locations: []string{"all"}, ConstraintVersion: "1.35"})
			wantNext, wantCatalogs, wantMetadataCalls := "asm-1-30", int32(2), 1
			if tt.wantError {
				wantNext, wantCatalogs = "", 0
			}
			if tt.networkFailures > 0 {
				wantMetadataCalls = 2
			}
			if (err != nil) != tt.wantError || result.Next != wantNext || catalogCalls.Load() != wantCatalogs || stagingCalls.Load() != 0 || metadataCalls != wantMetadataCalls {
				t.Fatalf("unsafe mesh discovery: metadata calls=%d, catalog calls=%d, staging calls=%d, result=%+v, err=%v", metadataCalls, catalogCalls.Load(), stagingCalls.Load(), result, err)
			}
		})
	}
}

func TestSubscriptionLocationRetries(t *testing.T) {
	for _, recover := range []bool{true, false} {
		t.Run(fmt.Sprint(recover), func(t *testing.T) {
			locationCalls := 0
			var catalogCalls atomic.Int32
			p := testProvider(t, func(request *http.Request) (*http.Response, error) {
				if strings.HasSuffix(request.URL.Path, "/providers/Microsoft.ContainerService") {
					return response(request, 200, meshLocations(`["uksouth"]`)), nil
				}
				if strings.HasSuffix(request.URL.Path, "/locations") {
					locationCalls++
					if locationCalls == 1 || !recover {
						return nil, io.ErrUnexpectedEOF
					}
					return response(request, 200, `{"value":[{"name":"uksouth"}]}`), nil
				}
				catalogCalls.Add(1)
				return response(request, 200, profile(`["asm-1-30"]`, `["1.35"]`)), nil
			})
			result, err := p.Check(context.Background(), CheckRequest{Current: "asm-1-29", Locations: []string{"all"}, ConstraintVersion: "1.35"})
			wantNext, wantCatalogs := "", int32(0)
			if recover {
				wantNext, wantCatalogs = "asm-1-30", 1
			}
			if (err == nil) != recover || result.Next != wantNext || catalogCalls.Load() != wantCatalogs || locationCalls != 2 {
				t.Fatalf("location retries must recover completely or stop before catalog queries: location calls=%d, catalog calls=%d, result=%+v, err=%v", locationCalls, catalogCalls.Load(), result, err)
			}
		})
	}
}

func TestMeshCatalogPagination(t *testing.T) {
	calls := 0
	p := testProvider(t, func(request *http.Request) (*http.Response, error) {
		calls++
		body := `{"value":[{"name":"istio","properties":{"meshRevisions":[
			{"revision":"asm-1-29","upgrades":["asm-1-30"],"compatibleWith":[{"name":"KubernetesOfficial","versions":["1.35"]}]}
		]}}],"nextLink":"https://management.azure.com/subscriptions/test-sub/providers/Microsoft.ContainerService/locations/uksouth/meshRevisionProfiles?page=2"}`
		if request.URL.Query().Get("page") == "2" {
			body = `{"value":[{"name":"istio","properties":{"meshRevisions":[
				{"revision":"asm-1-30","compatibleWith":[{"name":"KubernetesOfficial","versions":["1.35"]}]}
			]}}]}`
		}
		return response(request, 200, body), nil
	})
	result, err := p.Check(context.Background(), CheckRequest{Current: "asm-1-29", Locations: []string{"uksouth"}, ConstraintVersion: "1.35"})
	if err != nil || calls != 2 || result.Next != "asm-1-30" {
		t.Fatalf("paginated catalog was not fully read: calls=%d, result=%+v, err=%v", calls, result, err)
	}
}

func TestParallelDiscoveryIsBounded(t *testing.T) {
	var active, peak atomic.Int32
	started := make(chan struct{}, discoveryParallelism*2)
	release := make(chan struct{})
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	p := testProvider(t, func(request *http.Request) (*http.Response, error) {
		n := active.Add(1)
		defer active.Add(-1)
		for old := peak.Load(); n > old; old = peak.Load() {
			if peak.CompareAndSwap(old, n) {
				break
			}
		}
		started <- struct{}{}
		select {
		case <-release:
			return response(request, 200, profile(`["asm-1-30"]`, `["1.35"]`)), nil
		case <-request.Context().Done():
			return nil, request.Context().Err()
		}
	})
	locations := make([]string, discoveryParallelism*2)
	for i := range locations {
		locations[i] = fmt.Sprintf("region-%02d", i)
	}
	done := make(chan error, 1)
	go func() {
		result, err := p.Check(ctx, CheckRequest{Current: "asm-1-29", Locations: locations, ConstraintVersion: "1.35"})
		if err == nil && (result.Next != "asm-1-30" || len(result.Regions) != len(locations)) {
			err = fmt.Errorf("unexpected discovery: %+v", result)
		}
		if err == nil {
			for i, region := range result.Regions {
				if region.Location != locations[i] {
					err = fmt.Errorf("nondeterministic region ordering: %+v", result.Regions)
				}
			}
		}
		done <- err
	}()
	for range discoveryParallelism {
		select {
		case <-started:
		case <-ctx.Done():
			t.Fatal("discovery did not run concurrently")
		}
	}
	close(release)
	if err := <-done; err != nil || peak.Load() != discoveryParallelism {
		t.Fatalf("parallel discovery failed or exceeded limit: peak=%d, err=%v", peak.Load(), err)
	}
}

func TestDiscoveryCancellationStopsRetries(t *testing.T) {
	calls := 0
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	p := testProvider(t, func(request *http.Request) (*http.Response, error) {
		calls++
		cancel()
		return nil, request.Context().Err()
	})
	result, err := p.Check(ctx, CheckRequest{Current: "asm-1-29", Locations: []string{"uksouth"}, ConstraintVersion: "1.35"})
	if !errors.Is(err, context.Canceled) || calls != 1 || result.Next != "" {
		t.Fatalf("canceled discovery must stop retries and proposals: calls=%d, result=%+v, err=%v", calls, result, err)
	}
}

func TestRetryAfterRespectsDeadline(t *testing.T) {
	calls := 0
	options := azureDiscoveryOptions()
	options.Transport = transport(func(request *http.Request) (*http.Response, error) {
		calls++
		result := response(request, 429, `{"error":{"code":"Throttled","message":"test"}}`)
		result.Header.Set("Retry-After", "10")
		return result, nil
	})
	p, err := newIstioProvider("test-sub", credential{}, options)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	if _, err := p.Check(ctx, CheckRequest{Current: "asm-1-29", Locations: []string{"uksouth"}, ConstraintVersion: "1.35"}); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("expected deadline while respecting Retry-After: %v", err)
	}
	if calls != 1 {
		t.Fatalf("retried before Retry-After elapsed: %d calls", calls)
	}
}

func TestSortASMRevisions(t *testing.T) {
	revisions := []string{"asm-2-1", "asm-1-10", "asm-1-9"}
	sortASMRevisions(revisions)
	if !slices.Equal(revisions, []string{"asm-1-9", "asm-1-10", "asm-2-1"}) {
		t.Fatalf("revisions must sort numerically by major and minor: %v", revisions)
	}
}
