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
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"testing/synctest"
	"time"

	"github.com/go-logr/logr"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	azcorearm "github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"
	azfake "github.com/Azure/azure-sdk-for-go/sdk/azcore/fake"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/compute/armcompute/v5"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/network/armnetwork/v6"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/privatedns/armprivatedns"
)

func azureBundleResponse(req *http.Request, status int, body string) (*http.Response, error) {
	return &http.Response{
		StatusCode: status,
		Header:     http.Header{"Content-Type": {"application/json"}},
		Body:       io.NopCloser(strings.NewReader(body)),
		Request:    req,
	}, nil
}

func readAzureBundleManifest(t *testing.T, directory string) azureFailureManifest {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(directory, "manifest.json"))
	require.NoError(t, err, "read persisted manifest")
	var manifest azureFailureManifest
	require.NoError(t, json.Unmarshal(data, &manifest), "manifest must remain valid JSON")
	return manifest
}

func azureBundleOperation(t *testing.T, manifest azureFailureManifest, rg, operation, resource string) azureFailureOperation {
	t.Helper()
	var matches []azureFailureOperation
	for _, op := range manifest.Operations {
		if op.ResourceGroup == rg && op.Operation == operation && op.Resource == resource {
			matches = append(matches, op)
		}
	}
	require.Len(t, matches, 1, "expected exactly one operation for %s/%s/%s", rg, operation, resource)
	return matches[0]
}

func readAzureBundleArtifact(t *testing.T, directory, path string) string {
	t.Helper()
	require.True(t, filepath.IsLocal(path), "artifact must stay inside its bundle: %s", path)
	data, err := os.ReadFile(filepath.Join(directory, path))
	require.NoError(t, err, "read artifact %s", path)
	info, err := os.Stat(filepath.Join(directory, path))
	require.NoError(t, err)
	assert.Equal(t, os.FileMode(0600), info.Mode().Perm(), "artifact permissions: %s", path)
	return string(data)
}

func TestAzureFailureBundlePages(t *testing.T) {
	for _, scenario := range []struct {
		name   string
		status string
		count  int
		pages  int
		calls  int
	}{
		{name: "multiple pages", status: "success", count: 2, pages: 2, calls: 2},
		{name: "empty", status: "empty", pages: 1, calls: 1},
		{name: "second page error", status: "error", count: 1, pages: 1, calls: 2},
		{name: "deadline during second page", status: "error", count: 1, pages: 1, calls: 2},
		{name: "deadline before collection", status: "skipped-deadline"},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				ctx, cancel := context.WithTimeout(t.Context(), time.Minute)
				defer cancel()
				b, err := newAzureFailureBundle(ctx, t.TempDir(), "customer", "managed", "cluster", "pool")
				require.NoError(t, err)
				calls := 0
				path := "/subscriptions/" + fakeSubscriptionID + "/resourceGroups/managed/providers/Microsoft.Network/virtualNetworks"
				transport := &fakeTransport{do: func(req *http.Request) (*http.Response, error) {
					calls++
					assert.Equal(t, http.MethodGet, req.Method)
					assert.Equal(t, path, req.URL.Path)
					if calls == 1 {
						if scenario.name == "empty" {
							return azureBundleResponse(req, http.StatusOK, `{}`)
						}
						return azureBundleResponse(req, http.StatusOK, `{"value":[{"name":"first"}],"nextLink":"https://management.azure.com`+path+`?page=2"}`)
					}
					assert.Equal(t, "2", req.URL.Query().Get("page"))
					// Inspect disk before the second request finishes, not just after collection.
					manifest := readAzureBundleManifest(t, b.directory)
					op := azureBundleOperation(t, manifest, "managed", "virtual-networks", "")
					assert.Equal(t, "running", op.Status)
					assert.Equal(t, 1, op.Count)
					require.Len(t, op.Artifacts, 1)
					assert.JSONEq(t, `[{"name":"first"}]`, readAzureBundleArtifact(t, b.directory, op.Artifacts[0]))
					switch scenario.name {
					case "second page error":
						return azureBundleResponse(req, http.StatusForbidden, `{"error":{"code":"AuthorizationFailed","message":"https://blob.example/log?sig=PAGE_SECRET"}}`)
					case "deadline during second page":
						<-req.Context().Done()
						return nil, req.Context().Err()
					default:
						return azureBundleResponse(req, http.StatusOK, `{"value":[{"name":"second"}]}`)
					}
				}}
				options := fakeClientOptions(transport)
				options.Retry.MaxRetries = -1
				network, err := armnetwork.NewClientFactory(fakeSubscriptionID, &azfake.TokenCredential{}, options)
				require.NoError(t, err)
				if scenario.name == "deadline before collection" {
					time.Sleep(time.Minute)
				}
				items := collectAzurePages(ctx, b, "managed", "virtual-networks", "", network.NewVirtualNetworksClient().NewListPager("managed", nil), func(page armnetwork.VirtualNetworksClientListResponse) []*armnetwork.VirtualNetwork {
					return page.Value
				})
				assert.Len(t, items, scenario.count, "retain resources from successful pages for child collection")
				if len(items) > 0 {
					assert.Equal(t, "first", *items[0].Name)
				}
				assert.Equal(t, scenario.calls, calls)
				op := azureBundleOperation(t, readAzureBundleManifest(t, b.directory), "managed", "virtual-networks", "")
				assert.Equal(t, scenario.status, op.Status)
				assert.Equal(t, scenario.count, op.Count)
				require.Len(t, op.Artifacts, scenario.pages)
				for i, artifact := range op.Artifacts {
					assert.Equal(t, fmt.Sprintf("page-%04d.json", i+1), filepath.Base(artifact))
				}
				if scenario.name == "empty" {
					assert.JSONEq(t, `[]`, readAzureBundleArtifact(t, b.directory, op.Artifacts[0]))
				}
				if scenario.name == "multiple pages" {
					assert.JSONEq(t, `[{"name":"second"}]`, readAzureBundleArtifact(t, b.directory, op.Artifacts[1]))
				}
				if strings.Contains(scenario.name, "deadline") {
					assert.Equal(t, "collection deadline exceeded", op.Error)
				} else if scenario.name == "second page error" {
					assert.Equal(t, "Azure HTTP 403 (AuthorizationFailed)", op.Error)
				} else {
					assert.Empty(t, op.Error)
				}
				assert.False(t, op.FinishedAt.Before(op.StartedAt))
			})
		})
	}
}

func TestAzureFailureBundleScopesAndIndependentCategories(t *testing.T) {
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()
	b, err := newAzureFailureBundle(ctx, t.TempDir(), "customer", "managed", "cluster", "pool")
	require.NoError(t, err)
	const lb = `[{"name":"lb","properties":{
		"frontendIPConfigurations":[{"name":"front","properties":{"privateIPAddress":"10.0.0.4"}}],
		"backendAddressPools":[{"name":"back","properties":{"loadBalancerBackendAddresses":[{"name":"worker","properties":{"ipAddress":"10.0.0.5"}}]}}],
		"loadBalancingRules":[{"name":"https","properties":{"frontendPort":443,"backendPort":6443,"protocol":"Tcp","backendAddressPool":{"id":"/pools/back"},"probe":{"id":"/probes/ready"}}}],
		"probes":[{"name":"ready","properties":{"port":6443,"protocol":"Https","requestPath":"/readyz"}}],
		"inboundNatRules":[{"name":"ssh","properties":{"frontendPort":2222,"backendPort":22}}],
		"inboundNatPools":[{"name":"ssh-pool","properties":{"frontendPortRangeStart":2200,"frontendPortRangeEnd":2299,"backendPort":22}}],
		"outboundRules":[{"name":"egress","properties":{"allocatedOutboundPorts":1024,"protocol":"All","backendAddressPool":{"id":"/pools/back"}}}]
	}}]`
	// The map is also the exact request allowlist: subscription-wide or unrelated RG reads fail.
	fixtures := map[string]string{
		"loadBalancers": lb,
		"networkInterfaces": `[{"name":"attached","properties":{"virtualMachine":{"id":"/subscriptions/` + fakeSubscriptionID + `/resourceGroups/MANAGED/providers/Microsoft.Compute/virtualMachines/worker"}}},
			{"name":"foreign","properties":{"virtualMachine":{"id":"/subscriptions/` + fakeSubscriptionID + `/resourceGroups/other/providers/Microsoft.Compute/virtualMachines/worker"}}},
			{"name":"unattached"},{"name":"missing-vm-id","properties":{"virtualMachine":{}}},{"name":"bad-id","properties":{"virtualMachine":{"id":"invalid"}}},null,{}]`,
		"networkInterfaces/attached/effectiveRouteTable":            `{"value":[{"name":"default","addressPrefix":["0.0.0.0/0"],"nextHopType":"Internet"}]}`,
		"networkInterfaces/attached/effectiveNetworkSecurityGroups": `{"value":[{"networkSecurityGroup":{"id":"/nsgs/worker"},"effectiveSecurityRules":[{"name":"allow-dns","access":"Allow","destinationPortRange":"53"}]}]}`,
		"virtualNetworks":              `[{"name":"vnet"},null,{}]`,
		"virtualNetworks/vnet/subnets": `[{"name":"workers","properties":{"addressPrefix":"10.0.0.0/24","networkSecurityGroup":{"id":"/nsgs/worker"},"routeTable":{"id":"/routes/worker"}}}]`,
		"networkSecurityGroups":        `[{"name":"nsg","properties":{"securityRules":[{"name":"allow","properties":{"access":"Allow","priority":100}}]}}]`,
		"routeTables":                  `[{"name":"routes","properties":{"routes":[{"name":"default","properties":{"addressPrefix":"0.0.0.0/0","nextHopType":"Internet"}}]}}]`,
		"natGateways":                  `[{"name":"nat","properties":{"publicIpAddresses":[{"id":"/publicIPs/egress"}]}}]`,
		"publicIPAddresses":            `[{"name":"egress","properties":{"ipAddress":"192.0.2.1"}}]`,
		"privateEndpoints":             `[{"name":"endpoint"},null,{}]`,
		"privateEndpoints/endpoint/privateDnsZoneGroups": `[{"name":"zones","properties":{"privateDnsZoneConfigs":[{"name":"config","properties":{"privateDnsZoneId":"/zones/private.example"}}]}}]`,
		"privateDnsZones":                                     `[{"name":"private.example"},null,{}]`,
		"privateDnsZones/private.example/ALL":                 `[{"name":"api","type":"Microsoft.Network/privateDnsZones/A","properties":{"ttl":60,"aRecords":[{"ipv4Address":"10.0.0.4"}]}}]`,
		"privateDnsZones/private.example/virtualNetworkLinks": `[{"name":"link","properties":{"virtualNetwork":{"id":"/vnets/vnet"},"registrationEnabled":false}}]`,
	}
	requests := map[string]int{}
	var lock sync.Mutex
	natStarted := make(chan struct{})
	transport := &fakeTransport{do: func(req *http.Request) (*http.Response, error) {
		key := req.Method + " " + req.URL.Path
		if req.URL.Query().Get("page") != "" {
			key += "?page=" + req.URL.Query().Get("page")
		}
		lock.Lock()
		requests[key]++
		lock.Unlock()
		for _, rg := range []string{"customer", "managed"} {
			prefix := "/subscriptions/" + fakeSubscriptionID + "/resourceGroups/" + rg + "/providers/"
			if req.URL.Path == prefix+"Microsoft.Compute/virtualMachines" {
				assert.Equal(t, http.MethodGet, req.Method)
				return azureBundleResponse(req, http.StatusOK, `{"value":[]}`)
			}
			if !strings.HasPrefix(req.URL.Path, prefix+"Microsoft.Network/") {
				continue
			}
			resource := strings.TrimPrefix(req.URL.Path, prefix+"Microsoft.Network/")
			body, ok := fixtures[resource]
			if !ok {
				break
			}
			method := http.MethodGet
			if strings.Contains(resource, "/effective") {
				method = http.MethodPost
			}
			assert.Equal(t, method, req.Method, resource)
			if rg == "customer" && resource == "loadBalancers" {
				// A slow category must not prevent an unrelated category from starting.
				select {
				case <-natStarted:
				case <-req.Context().Done():
					return nil, req.Context().Err()
				}
			}
			if rg == "customer" && resource == "natGateways" {
				close(natStarted)
			}
			if rg == "customer" && resource == "privateDnsZones/private.example/ALL" || req.URL.Query().Get("page") == "2" {
				return azureBundleResponse(req, http.StatusForbidden, `{"error":{"code":"AuthorizationFailed","message":"https://blob.example/log?sig=SECRET"}}`)
			}
			if strings.Contains(resource, "/effective") {
				return azureBundleResponse(req, http.StatusOK, body)
			}
			next := ""
			if resource == "virtualNetworks" || resource == "privateDnsZones" || resource == "networkInterfaces" || resource == "privateEndpoints" {
				next = `,"nextLink":"https://management.azure.com` + req.URL.Path + `?page=2"`
			}
			return azureBundleResponse(req, http.StatusOK, `{"value":`+body+next+`}`)
		}
		t.Errorf("unexpected Azure request: %s", key)
		return azureBundleResponse(req, http.StatusBadRequest, `{"error":{"code":"UnexpectedRequest"}}`)
	}}
	options := fakeClientOptions(transport)
	options.Retry.MaxRetries = -1
	compute, err := armcompute.NewClientFactory(fakeSubscriptionID, &azfake.TokenCredential{}, options)
	require.NoError(t, err)
	network, err := armnetwork.NewClientFactory(fakeSubscriptionID, &azfake.TokenCredential{}, options)
	require.NoError(t, err)
	dns, err := armprivatedns.NewClientFactory(fakeSubscriptionID, &azfake.TokenCredential{}, options)
	require.NoError(t, err)
	err = b.collect(ctx, compute, network, dns)
	require.Error(t, err)
	assert.NotContains(t, err.Error(), "SECRET")
	manifest := readAzureBundleManifest(t, b.directory)
	require.Len(t, manifest.Operations, 32)
	assert.False(t, manifest.FinishedAt.IsZero())
	assert.False(t, manifest.FinishedAt.Before(manifest.StartedAt))
	wantRequests := map[string]int{}
	for _, rg := range []string{"customer", "managed"} {
		prefix := "/subscriptions/" + fakeSubscriptionID + "/resourceGroups/" + rg + "/providers/"
		wantRequests["GET "+prefix+"Microsoft.Compute/virtualMachines"] = 1
		for resource := range fixtures {
			method := "GET "
			if strings.Contains(resource, "/effective") {
				method = "POST "
			}
			wantRequests[method+prefix+"Microsoft.Network/"+resource] = 1
		}
		for _, resource := range []string{"virtualNetworks", "privateDnsZones", "networkInterfaces", "privateEndpoints"} {
			wantRequests["GET "+prefix+"Microsoft.Network/"+resource+"?page=2"] = 1
		}
		for _, check := range []struct{ operation, resource, fixture string }{
			{"load-balancers", "", "loadBalancers"},
			{"subnets", "vnet", "virtualNetworks/vnet/subnets"},
			{"network-security-groups", "", "networkSecurityGroups"},
			{"route-tables", "", "routeTables"},
			{"nat-gateways", "", "natGateways"},
			{"public-ips", "", "publicIPAddresses"},
			{"private-dns-zone-groups", "endpoint", "privateEndpoints/endpoint/privateDnsZoneGroups"},
			{"private-dns-vnet-links", "private.example", "privateDnsZones/private.example/virtualNetworkLinks"},
			{"effective-routes", "attached", "networkInterfaces/attached/effectiveRouteTable"},
			{"effective-network-security-groups", "attached", "networkInterfaces/attached/effectiveNetworkSecurityGroups"},
		} {
			op := azureBundleOperation(t, manifest, rg, check.operation, check.resource)
			assert.Equal(t, "success", op.Status, "%s/%s", rg, check.operation)
			assert.Equal(t, 1, op.Count)
			require.Len(t, op.Artifacts, 1)
			assert.JSONEq(t, fixtures[check.fixture], readAzureBundleArtifact(t, b.directory, op.Artifacts[0]), "%s/%s", rg, check.operation)
		}
		for _, operation := range []string{"virtual-networks", "private-dns-zones", "network-interfaces", "private-endpoints"} {
			op := azureBundleOperation(t, manifest, rg, operation, "")
			assert.Equal(t, "error", op.Status)
			assert.Equal(t, "Azure HTTP 403 (AuthorizationFailed)", op.Error)
			require.Len(t, op.Artifacts, 1, "partial parent page survives and supplies child names")
		}
		assert.Equal(t, "empty", azureBundleOperation(t, manifest, rg, "virtual-machines", "").Status)
	}
	assert.Equal(t, wantRequests, requests, "all and only requested RG scopes, child reads, and attached-NIC actions")
	assert.Equal(t, "error", azureBundleOperation(t, manifest, "customer", "private-dns-records", "private.example").Status)
	op := azureBundleOperation(t, manifest, "managed", "private-dns-records", "private.example")
	assert.Equal(t, "success", op.Status)
	require.Len(t, op.Artifacts, 1)
	assert.JSONEq(t, fixtures["privateDnsZones/private.example/ALL"], readAzureBundleArtifact(t, b.directory, op.Artifacts[0]))
}

func TestAzureFailureBundleEffectiveNetworkErrors(t *testing.T) {
	for _, scenario := range []string{"begin error", "poll error", "empty", "deadline", "NSG begin error", "NSG poll error"} {
		t.Run(scenario, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				ctx, cancel := context.WithTimeout(t.Context(), time.Minute)
				defer cancel()
				b, err := newAzureFailureBundle(ctx, t.TempDir(), "customer", "managed", "cluster", "pool")
				require.NoError(t, err)
				var requests []string
				var lock sync.Mutex
				prefix := "/subscriptions/" + fakeSubscriptionID + "/resourceGroups/managed/providers/Microsoft.Network/networkInterfaces/nic/"
				transport := &fakeTransport{do: func(req *http.Request) (*http.Response, error) {
					lock.Lock()
					requests = append(requests, req.Method+" "+req.URL.Path)
					lock.Unlock()
					switch req.URL.Path {
					case prefix + "effectiveRouteTable":
						assert.Equal(t, http.MethodPost, req.Method)
						switch scenario {
						case "begin error":
							return azureBundleResponse(req, http.StatusForbidden, `{"error":{"code":"AuthorizationFailed"}}`)
						case "poll error", "deadline":
							resp, err := azureBundleResponse(req, http.StatusAccepted, `{}`)
							resp.Header.Set("Location", "https://management.azure.com/poll")
							return resp, err
						default:
							return azureBundleResponse(req, http.StatusOK, `{"value":[]}`)
						}
					case "/poll":
						assert.Equal(t, http.MethodGet, req.Method)
						if scenario == "deadline" {
							<-req.Context().Done()
							return nil, req.Context().Err()
						}
						return azureBundleResponse(req, http.StatusBadRequest, `{"error":{"code":"NicNotReady"}}`)
					case prefix + "effectiveNetworkSecurityGroups":
						assert.Equal(t, http.MethodPost, req.Method)
						if scenario == "NSG begin error" {
							return azureBundleResponse(req, http.StatusForbidden, `{"error":{"code":"AuthorizationFailed"}}`)
						}
						if scenario == "NSG poll error" {
							resp, err := azureBundleResponse(req, http.StatusAccepted, `{}`)
							resp.Header.Set("Location", "https://management.azure.com/poll")
							return resp, err
						}
						return azureBundleResponse(req, http.StatusOK, `{"value":[]}`)
					default:
						t.Errorf("unexpected effective-network request: %s", req.URL)
						return nil, errors.New("unexpected request")
					}
				}}
				options := fakeClientOptions(transport)
				options.Retry.MaxRetries = -1
				network, err := armnetwork.NewClientFactory(fakeSubscriptionID, &azfake.TokenCredential{}, options)
				require.NoError(t, err)
				b.collectEffectiveNetwork(ctx, network, "managed", "nic")
				manifest := readAzureBundleManifest(t, b.directory)
				routes := azureBundleOperation(t, manifest, "managed", "effective-routes", "nic")
				nsg := azureBundleOperation(t, manifest, "managed", "effective-network-security-groups", "nic")
				wantRequests := []string{"POST " + prefix + "effectiveRouteTable"}
				if scenario == "poll error" || scenario == "deadline" {
					wantRequests = append(wantRequests, "GET /poll")
				}
				wantRequests = append(wantRequests, "POST "+prefix+"effectiveNetworkSecurityGroups")
				if strings.HasPrefix(scenario, "NSG") {
					assert.Equal(t, "error", nsg.Status)
					assert.Empty(t, nsg.Artifacts)
					if scenario == "NSG poll error" {
						wantRequests = append(wantRequests, "GET /poll")
						assert.Equal(t, "Azure HTTP 400 (NicNotReady)", nsg.Error)
					} else {
						assert.Equal(t, "Azure HTTP 403 (AuthorizationFailed)", nsg.Error)
					}
				} else {
					assert.Equal(t, "empty", nsg.Status, "NSG action is independent of route action errors")
					require.Len(t, nsg.Artifacts, 1)
					assert.JSONEq(t, `{"value":[]}`, readAzureBundleArtifact(t, b.directory, nsg.Artifacts[0]))
				}
				assert.ElementsMatch(t, wantRequests, requests)
				if scenario == "empty" || strings.HasPrefix(scenario, "NSG") {
					assert.Equal(t, "empty", routes.Status)
					require.Len(t, routes.Artifacts, 1)
					assert.JSONEq(t, `{"value":[]}`, readAzureBundleArtifact(t, b.directory, routes.Artifacts[0]))
				} else {
					assert.Equal(t, "error", routes.Status)
					assert.Empty(t, routes.Artifacts)
					wantError := map[string]string{"begin error": "Azure HTTP 403 (AuthorizationFailed)", "poll error": "Azure HTTP 400 (NicNotReady)", "deadline": "collection deadline exceeded"}
					assert.Equal(t, wantError[scenario], routes.Error)
				}
				assert.Zero(t, routes.Count)
				assert.Zero(t, nsg.Count)
			})
		})
	}
}

func TestAzureFailureBundleStalledParentsAndChildren(t *testing.T) {
	// Fake time makes deadline starvation deterministic; the console transport
	// stays in-process so a successful download can finish before that deadline.
	synctest.Test(t, func(t *testing.T) {
		ctx, cancel := context.WithTimeout(t.Context(), time.Minute)
		defer cancel()
		original := http.DefaultTransport
		http.DefaultTransport = manifestRoundTripper(func(req *http.Request) (*http.Response, error) {
			return azureBundleResponse(req, http.StatusOK, "console survived\n")
		})
		defer func() { http.DefaultTransport = original }()
		b, err := newAzureFailureBundle(ctx, t.TempDir(), "managed", "managed", "cluster", "pool")
		require.NoError(t, err)
		prefix := "/subscriptions/" + fakeSubscriptionID + "/resourceGroups/managed/providers/"
		transport := &fakeTransport{do: func(req *http.Request) (*http.Response, error) {
			resource := strings.TrimPrefix(req.URL.Path, prefix)
			if req.URL.Query().Get("page") == "2" || resource == "Microsoft.Compute/virtualMachines/slow/instanceView" || resource == "Microsoft.Network/networkInterfaces/slow/effectiveRouteTable" || resource == "Microsoft.Network/privateDnsZones/zone/ALL" {
				<-req.Context().Done()
				return nil, req.Context().Err()
			}
			var items string
			switch resource {
			case "Microsoft.Compute/virtualMachines":
				items = `[{"name":"slow"},{"name":"fast"}]`
			case "Microsoft.Network/networkInterfaces":
				items = `[{"name":"slow","properties":{"virtualMachine":{"id":"` + prefix + `Microsoft.Compute/virtualMachines/slow"}}},{"name":"fast","properties":{"virtualMachine":{"id":"` + prefix + `Microsoft.Compute/virtualMachines/fast"}}}]`
			case "Microsoft.Network/virtualNetworks":
				items = `[{"name":"vnet"}]`
			case "Microsoft.Network/privateEndpoints":
				items = `[{"name":"endpoint"}]`
			case "Microsoft.Network/privateDnsZones":
				items = `[{"name":"zone"}]`
			default:
				if strings.HasSuffix(resource, "/retrieveBootDiagnosticsData") {
					return azureBundleResponse(req, http.StatusOK, `{"serialConsoleLogBlobUri":"https://blob.example/log?sig=SECRET"}`)
				}
				return azureBundleResponse(req, http.StatusOK, `{"value":[]}`)
			}
			return azureBundleResponse(req, http.StatusOK, `{"value":`+items+`,"nextLink":"https://management.azure.com`+req.URL.Path+`?page=2"}`)
		}}
		options := fakeClientOptions(transport)
		options.Retry.MaxRetries = -1
		compute, err := armcompute.NewClientFactory(fakeSubscriptionID, &azfake.TokenCredential{}, options)
		require.NoError(t, err)
		network, err := armnetwork.NewClientFactory(fakeSubscriptionID, &azfake.TokenCredential{}, options)
		require.NoError(t, err)
		dns, err := armprivatedns.NewClientFactory(fakeSubscriptionID, &azfake.TokenCredential{}, options)
		require.NoError(t, err)
		require.Error(t, b.collect(ctx, compute, network, dns))
		manifest := readAzureBundleManifest(t, b.directory)
		for _, check := range []struct{ operation, resource, status string }{
			{"virtual-machines", "", "error"}, {"network-interfaces", "", "error"},
			{"vm-instance-view", "slow", "error"}, {"vm-instance-view", "fast", "success"},
			{"vm-console", "slow", "success"}, {"vm-console", "fast", "success"},
			{"effective-routes", "slow", "error"}, {"effective-routes", "fast", "empty"},
			{"effective-network-security-groups", "slow", "empty"}, {"effective-network-security-groups", "fast", "empty"},
			{"subnets", "vnet", "empty"}, {"private-dns-zone-groups", "endpoint", "empty"},
			{"private-dns-records", "zone", "error"}, {"private-dns-vnet-links", "zone", "empty"},
		} {
			op := azureBundleOperation(t, manifest, "managed", check.operation, check.resource)
			assert.Equal(t, check.status, op.Status, "%s/%s must be independent", check.operation, check.resource)
			if check.operation == "vm-console" {
				require.Len(t, op.Artifacts, 1)
				assert.Equal(t, "console survived\n", readAzureBundleArtifact(t, b.directory, op.Artifacts[0]))
			}
		}
	})
}

func TestAzureFailureBundleVMProjectionAndIndependentDiagnostics(t *testing.T) {
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()
	var consoleReads atomic.Int32
	console := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		consoleReads.Add(1)
		assert.Equal(t, http.MethodGet, req.Method)
		assert.Equal(t, "BOOT_SECRET", req.URL.Query().Get("sig"))
		_, _ = io.WriteString(w, "booting\nfetch https://blob.example/config?sig=CONSOLE_SECRET\nready without trailing newline")
	}))
	defer console.Close()
	const safeVM = `{"id":"/vms/worker","name":"worker","type":"Microsoft.Compute/virtualMachines","location":"uksouth","zones":["1"],
		"identity":{"type":"UserAssigned","userAssignedIdentities":{"/identities/worker":{"clientId":"client","principalId":"principal"}}},
		"properties":{"vmId":"vm-id","provisioningState":"Failed","hardwareProfile":{"vmSize":"Standard_D4s_v5"},"priority":"Spot","evictionPolicy":"Deallocate",
		"networkProfile":{"networkInterfaces":[{"id":"/nics/worker","properties":{"primary":true}}]},
		"storageProfile":{"imageReference":{"publisher":"RedHat","offer":"rh-ocp-worker","sku":"worker","version":"latest"},
		"osDisk":{"name":"os","osType":"Linux","diskSizeGB":128,"caching":"ReadOnly","createOption":"FromImage","diffDiskSettings":{"option":"Local","placement":"ResourceDisk"}}}}}`
	// Start with the expected allowlist and inject secrets into supported SDK fields.
	var vm map[string]any
	require.NoError(t, json.Unmarshal([]byte(safeVM), &vm))
	vm["tags"] = map[string]any{"private": "TAG_SECRET"}
	properties := vm["properties"].(map[string]any)
	properties["osProfile"] = map[string]any{"adminUsername": "ADMIN_SECRET", "adminPassword": "PASSWORD_SECRET", "customData": "CUSTOM_DATA_SECRET", "secrets": []any{map[string]any{"sourceVault": map[string]any{"id": "/vaults/VAULT_SECRET"}}}}
	properties["userData"] = "USER_DATA_SECRET"
	properties["diagnosticsProfile"] = map[string]any{"bootDiagnostics": map[string]any{"enabled": true, "storageUri": "https://blob.example/?sig=DIAGNOSTICS_SECRET"}}
	properties["applicationProfile"] = map[string]any{"galleryApplications": []any{map[string]any{"packageReferenceId": "APP_SECRET"}}}
	properties["networkProfile"].(map[string]any)["networkInterfaceConfigurations"] = []any{map[string]any{"name": "NETWORK_CONFIG_SECRET"}}
	storage := properties["storageProfile"].(map[string]any)
	storage["dataDisks"] = []any{map[string]any{"name": "DATA_DISK_SECRET", "lun": 0, "createOption": "Attach"}}
	disk := storage["osDisk"].(map[string]any)
	disk["vhd"] = map[string]any{"uri": "https://blob.example/disk?sig=VHD_SECRET"}
	disk["image"] = map[string]any{"uri": "https://blob.example/image?sig=IMAGE_SECRET"}
	disk["managedDisk"] = map[string]any{"id": "/disks/MANAGED_DISK_SECRET"}
	disk["encryptionSettings"] = map[string]any{"enabled": true, "diskEncryptionKey": map[string]any{"secretUrl": "https://vault.example/secrets/DISK_KEY_SECRET"}}
	vmJSON, err := json.Marshal(vm)
	require.NoError(t, err)
	var sdkVM armcompute.VirtualMachine
	require.NoError(t, json.Unmarshal(vmJSON, &sdkVM))
	sdkJSON, err := json.Marshal(sdkVM)
	require.NoError(t, err)
	for _, secret := range []string{"TAG_SECRET", "ADMIN_SECRET", "PASSWORD_SECRET", "CUSTOM_DATA_SECRET", "VAULT_SECRET", "USER_DATA_SECRET", "DIAGNOSTICS_SECRET", "APP_SECRET", "NETWORK_CONFIG_SECRET", "DATA_DISK_SECRET", "VHD_SECRET", "IMAGE_SECRET", "MANAGED_DISK_SECRET", "DISK_KEY_SECRET"} {
		require.Contains(t, string(sdkJSON), secret, "fixture must reach the collector, not be discarded by SDK decoding")
	}
	const safeView = `{"computerName":"worker","osName":"Linux","osVersion":"9","statuses":[{"code":"ProvisioningState/failed","message":"fetch [credential URL omitted]"}],"bootDiagnostics":{"status":{"code":"Unavailable"}},"disks":[{"name":"os","statuses":[{"code":"ProvisioningState/succeeded"}]}],"vmAgent":{"vmAgentVersion":"1"}}`
	const view = `{"computerName":"worker","osName":"Linux","osVersion":"9","statuses":[{"code":"ProvisioningState/failed","message":"fetch https://blob.example/log?sig=STATUS_SECRET"}],
		"vmAgent":{"vmAgentVersion":"1","extensionHandlers":[{"type":"EXTENSION_HANDLER_SECRET"}]},
		"disks":[{"name":"os","statuses":[{"code":"ProvisioningState/succeeded"}],"encryptionSettings":[{"diskEncryptionKey":{"secretUrl":"https://vault.example/secrets/DISK_VIEW_SECRET"},"keyEncryptionKey":{"keyUrl":"https://vault.example/keys/KEY_VIEW_SECRET"}}]}],
		"extensions":[{"name":"EXTENSION_SECRET","statuses":[{"message":"EXTENSION_OUTPUT_SECRET"}]}],
		"bootDiagnostics":{"serialConsoleLogBlobUri":"https://blob.example/log?sig=VIEW_SECRET","consoleScreenshotBlobUri":"https://blob.example/screen?sig=SCREEN_SECRET","status":{"code":"Unavailable"}}}`
	var lock sync.Mutex
	requests := map[string]int{}
	managedPath := "/subscriptions/" + fakeSubscriptionID + "/resourceGroups/managed/providers/Microsoft.Compute/virtualMachines"
	customerPath := strings.Replace(managedPath, "/managed/", "/customer/", 1)
	transport := &fakeTransport{do: func(req *http.Request) (*http.Response, error) {
		key := req.Method + " " + req.URL.Path
		if req.URL.Query().Get("page") == "2" {
			key += "?page=2"
		}
		lock.Lock()
		requests[key]++
		lock.Unlock()
		switch req.URL.Path {
		case customerPath:
			return azureBundleResponse(req, http.StatusOK, `{"value":[]}`)
		case managedPath:
			if req.URL.Query().Get("page") == "2" {
				return azureBundleResponse(req, http.StatusForbidden, `{"error":{"code":"AuthorizationFailed","message":"PAGE_SECRET"}}`)
			}
			return azureBundleResponse(req, http.StatusOK, `{"value":[`+string(vmJSON)+`,{"name":"no-console"},{"name":"no-view"},null,{}],"nextLink":"https://management.azure.com`+managedPath+`?page=2"}`)
		case managedPath + "/worker/instanceView", managedPath + "/no-console/instanceView":
			return azureBundleResponse(req, http.StatusOK, view)
		case managedPath + "/no-view/instanceView", managedPath + "/no-console/retrieveBootDiagnosticsData":
			return azureBundleResponse(req, http.StatusForbidden, `{"error":{"code":"AuthorizationFailed","message":"https://blob.example/?sig=ERROR_SECRET"}}`)
		case managedPath + "/worker/retrieveBootDiagnosticsData", managedPath + "/no-view/retrieveBootDiagnosticsData":
			return azureBundleResponse(req, http.StatusOK, `{"serialConsoleLogBlobUri":"`+console.URL+`?sig=BOOT_SECRET","consoleScreenshotBlobUri":"https://blob.example/screen?sig=SCREEN_SECRET"}`)
		default:
			t.Errorf("unexpected VM request: %s", key)
			return nil, errors.New("unexpected VM request")
		}
	}}
	options := fakeClientOptions(transport)
	options.Retry.MaxRetries = -1
	compute, err := armcompute.NewClientFactory(fakeSubscriptionID, &azfake.TokenCredential{}, options)
	require.NoError(t, err)
	b, err := newAzureFailureBundle(ctx, t.TempDir(), "customer", "managed", "cluster", "pool")
	require.NoError(t, err)
	err = b.collect(ctx, compute, nil, nil)
	require.Error(t, err)
	assert.NotContains(t, err.Error(), "SECRET")
	manifest := readAzureBundleManifest(t, b.directory)
	list := azureBundleOperation(t, manifest, "managed", "virtual-machines", "")
	assert.Equal(t, "error", list.Status)
	assert.Equal(t, 4, list.Count, "nil VM skipped, nameless VM retained without child requests")
	require.Len(t, list.Artifacts, 1)
	assert.JSONEq(t, `[`+safeVM+`,{"name":"no-console"},{"name":"no-view"},{}]`, readAzureBundleArtifact(t, b.directory, list.Artifacts[0]))
	wantRequests := map[string]int{"GET " + customerPath: 1, "GET " + managedPath: 1, "GET " + managedPath + "?page=2": 1}
	for _, name := range []string{"worker", "no-console", "no-view"} {
		wantRequests["GET "+managedPath+"/"+name+"/instanceView"] = 1
		wantRequests["POST "+managedPath+"/"+name+"/retrieveBootDiagnosticsData"] = 1
		instanceView := azureBundleOperation(t, manifest, "managed", "vm-instance-view", name)
		consoleLog := azureBundleOperation(t, manifest, "managed", "vm-console", name)
		if name == "no-view" {
			assert.Equal(t, "error", instanceView.Status)
			assert.Equal(t, "Azure HTTP 403 (AuthorizationFailed)", instanceView.Error)
			assert.Empty(t, instanceView.Artifacts)
		} else {
			assert.Equal(t, "success", instanceView.Status)
			assert.Equal(t, 1, instanceView.Count)
			require.Len(t, instanceView.Artifacts, 1)
			assert.JSONEq(t, safeView, readAzureBundleArtifact(t, b.directory, instanceView.Artifacts[0]))
		}
		if name == "no-console" {
			assert.Equal(t, "error", consoleLog.Status)
			assert.Equal(t, "Azure HTTP 403 (AuthorizationFailed)", consoleLog.Error)
			assert.Empty(t, consoleLog.Artifacts)
		} else {
			assert.Equal(t, "success", consoleLog.Status)
			assert.Equal(t, 1, consoleLog.Count)
			require.Len(t, consoleLog.Artifacts, 1)
			assert.Equal(t, "booting\nfetch [credential URL omitted]\nready without trailing newline", readAzureBundleArtifact(t, b.directory, consoleLog.Artifacts[0]))
		}
	}
	assert.Equal(t, wantRequests, requests)
	assert.Equal(t, int32(2), consoleReads.Load())
	for _, rg := range []string{"customer", "managed"} {
		for _, category := range []string{"networking", "private-dns"} {
			assert.Equal(t, "skipped-client-unavailable", azureBundleOperation(t, manifest, rg, category, "").Status)
		}
	}
	assert.NotContains(t, readAzureBundleArtifact(t, b.directory, "manifest.json"), "SECRET")
}

// Follow every nested model, including references back to other networking
// objects. An SDK change introducing credential fields needs an explicit review.
func TestAzureFailureBundleNetworkingModelCredentialFields(t *testing.T) {
	seen := map[reflect.Type]bool{}
	var inspect func(reflect.Type, string)
	inspect = func(typ reflect.Type, path string) {
		for typ.Kind() == reflect.Pointer || typ.Kind() == reflect.Slice || typ.Kind() == reflect.Map {
			typ = typ.Elem()
		}
		if typ.Kind() != reflect.Struct || seen[typ] {
			return
		}
		seen[typ] = true
		for field := range typ.Fields() {
			name := strings.ToLower(field.Name)
			for _, credential := range []string{"password", "secret", "token", "credential", "privatekey", "sharedkey", "keyvault", "encryptionkey"} {
				assert.NotContains(t, name, credential, "credential field reachable through %s.%s", path, field.Name)
			}
			inspect(field.Type, path+"."+field.Name)
		}
	}
	for _, model := range []any{
		armnetwork.LoadBalancer{}, armnetwork.Interface{}, armnetwork.VirtualNetwork{}, armnetwork.Subnet{},
		armnetwork.SecurityGroup{}, armnetwork.RouteTable{}, armnetwork.NatGateway{}, armnetwork.PublicIPAddress{},
		armnetwork.PrivateEndpoint{}, armnetwork.PrivateDNSZoneGroup{}, armnetwork.EffectiveRoute{}, armnetwork.EffectiveNetworkSecurityGroup{},
		armprivatedns.PrivateZone{}, armprivatedns.RecordSet{}, armprivatedns.VirtualNetworkLink{},
	} {
		typ := reflect.TypeOf(model)
		inspect(typ, typ.Name())
	}
}

func TestAzureFailureBundleEffectiveNetworkPagination(t *testing.T) {
	for _, operation := range []string{"effective-routes", "effective-network-security-groups"} {
		for _, scenario := range []string{"success", "error", "deadline", "outside scope", "other subscription", "other resource group", "no client"} {
			t.Run(operation+"/"+scenario, func(t *testing.T) {
				synctest.Test(t, func(t *testing.T) {
					ctx, cancel := context.WithTimeout(t.Context(), time.Minute)
					defer cancel()
					b, err := newAzureFailureBundle(ctx, t.TempDir(), "managed", "managed", "cluster", "pool")
					require.NoError(t, err)
					path := "/subscriptions/" + fakeSubscriptionID + "/resourceGroups/managed/providers/Microsoft.Network/networkInterfaces/nic/"
					action := "effectiveRouteTable"
					if operation != "effective-routes" {
						action = "effectiveNetworkSecurityGroups"
					}
					var continuations atomic.Int32
					transport := &fakeTransport{do: func(req *http.Request) (*http.Response, error) {
						if req.URL.Path != path+action {
							return azureBundleResponse(req, http.StatusOK, `{"value":[]}`)
						}
						if req.Method == http.MethodPost {
							next := "https://management.azure.com" + path + action + "?page=2"
							if scenario == "outside scope" {
								next = "https://evil.example/steal?sig=NEXT_SECRET"
							}
							if scenario == "other subscription" {
								next = strings.Replace(next, fakeSubscriptionID, "other", 1)
							}
							if scenario == "other resource group" {
								next = strings.Replace(next, "/managed/", "/other/", 1)
							}
							return azureBundleResponse(req, http.StatusOK, `{"value":[{}],"nextLink":"`+next+`"}`)
						}
						continuations.Add(1)
						assert.Equal(t, "2", req.URL.Query().Get("page"))
						op := azureBundleOperation(t, readAzureBundleManifest(t, b.directory), "managed", operation, "nic")
						assert.Equal(t, "running", op.Status)
						require.Len(t, op.Artifacts, 1, "first effective-state page saved before continuation")
						switch scenario {
						case "error":
							return azureBundleResponse(req, http.StatusForbidden, `{"error":{"code":"Forbidden","message":"sig=ERROR_SECRET"}}`)
						case "deadline":
							<-req.Context().Done()
							return nil, req.Context().Err()
						default:
							return azureBundleResponse(req, http.StatusOK, `{"value":[{}]}`)
						}
					}}
					options := fakeClientOptions(transport)
					options.Retry.MaxRetries = -1
					network, err := armnetwork.NewClientFactory(fakeSubscriptionID, &azfake.TokenCredential{}, options)
					require.NoError(t, err)
					if scenario != "no client" {
						b.subscriptionID = fakeSubscriptionID
						b.effectiveClient, err = azcorearm.NewClient("azure-failure-bundle", "v0.0.0", &azfake.TokenCredential{}, options)
						require.NoError(t, err)
					}
					b.collectEffectiveNetwork(ctx, network, "managed", "nic")
					op := azureBundleOperation(t, readAzureBundleManifest(t, b.directory), "managed", operation, "nic")
					switch scenario {
					case "success":
						assert.Equal(t, "success", op.Status)
						assert.Equal(t, 2, op.Count)
						assert.Len(t, op.Artifacts, 2)
					case "outside scope", "other subscription", "other resource group", "no client":
						assert.Equal(t, "incomplete", op.Status)
						assert.Zero(t, continuations.Load())
						assert.Equal(t, errEffectiveNetworkIncomplete.Error(), op.Error)
					default:
						assert.Equal(t, "error", op.Status)
						assert.Equal(t, 1, op.Count)
						assert.Len(t, op.Artifacts, 1)
					}
					assert.NotContains(t, readAzureBundleArtifact(t, b.directory, "manifest.json"), "SECRET")
				})
			})
		}
	}
}

func TestAzureFailureBundleErrorAndURLHygiene(t *testing.T) {
	signedURL := "https://blob.example/log?sv=1&sig=ERROR_SECRET"
	response := &azcore.ResponseError{StatusCode: http.StatusForbidden, ErrorCode: "AuthorizationFailed", RawResponse: &http.Response{
		StatusCode: http.StatusForbidden,
		Header:     http.Header{"Authorization": {"Bearer HEADER_SECRET"}},
		Body:       io.NopCloser(strings.NewReader(signedURL)),
	}}
	for _, test := range []struct {
		name string
		err  error
		want string
	}{
		{"response", fmt.Errorf("request %s: %w", signedURL, response), "Azure HTTP 403 (AuthorizationFailed)"},
		{"malicious code", &azcore.ResponseError{StatusCode: 400, ErrorCode: signedURL}, "Azure HTTP 400 (redacted)"},
		{"safe code", &azcore.ResponseError{StatusCode: 400, ErrorCode: "Code_123.sub"}, "Azure HTTP 400 (Code_123.sub)"},
		{"URL error", &url.Error{Op: "Get", URL: signedURL, Err: errors.New("BODY_SECRET")}, "*url.Error (details omitted to protect credentials)"},
		{"unknown error", errors.New(signedURL), "*errors.errorString (details omitted to protect credentials)"},
		{"wrapped deadline", fmt.Errorf("%s: %w", signedURL, context.DeadlineExceeded), "collection deadline exceeded"},
		{"wrapped cancellation", fmt.Errorf("%s: %w", signedURL, context.Canceled), "collection canceled"},
	} {
		t.Run(test.name, func(t *testing.T) {
			b, err := newAzureFailureBundle(t.Context(), t.TempDir(), "rg", "rg", "cluster", "pool")
			require.NoError(t, err)
			b.operation(t.Context(), "rg", "failed", "", func(*azureFailureOperation) error { return test.err })
			err = b.collect(t.Context(), nil, nil, nil)
			require.Error(t, err)
			assert.Contains(t, err.Error(), test.want)
			assert.NotContains(t, err.Error(), "SECRET")
			op := azureBundleOperation(t, readAzureBundleManifest(t, b.directory), "rg", "failed", "")
			assert.Equal(t, test.want, op.Error)
			assert.NotContains(t, readAzureBundleArtifact(t, b.directory, "manifest.json"), "SECRET")
		})
	}
	b, err := newAzureFailureBundle(t.Context(), t.TempDir(), "rg", "rg", "cluster", "pool")
	require.NoError(t, err)
	input := map[string]string{
		"signed":    "fetch \"https://blob.example/log?sv=1&sig=SIGNED_SECRET\"\nnext line",
		"encoded":   "https://blob.example/log?SIG%3dENCODED_SECRET",
		"signature": "https://blob.example/log?Signature=SIGNATURE_SECRET",
		"token":     "https://blob.example/log?Token=TOKEN_SECRET",
		"secret":    "http://blob.example/log?secret=QUERY_SECRET",
		"password":  "https://blob.example/log?password=PASSWORD_SECRET",
		"userinfo":  "https://user:USERINFO_SECRET@blob.example/log",
		"safe":      "GET https://management.azure.com/resources?api-version=2024-05-01\nplain text",
	}
	b.operation(t.Context(), "rg", "json", "", func(op *azureFailureOperation) error { return b.writeJSON(op, "result.json", input) })
	op := azureBundleOperation(t, readAzureBundleManifest(t, b.directory), "rg", "json", "")
	assert.Equal(t, "success", op.Status)
	require.Len(t, op.Artifacts, 1)
	data := readAzureBundleArtifact(t, b.directory, op.Artifacts[0])
	var got map[string]string
	require.NoError(t, json.Unmarshal([]byte(data), &got), "redaction must preserve valid escaped JSON")
	assert.Equal(t, "fetch \"[credential URL omitted]\"\nnext line", got["signed"])
	assert.Equal(t, input["safe"], got["safe"])
	for _, key := range []string{"encoded", "signature", "token", "secret", "password", "userinfo"} {
		assert.Equal(t, "[credential URL omitted]", got[key], key)
	}
	assert.NotContains(t, data, "SECRET")
}

func TestAzureFailureBundleDirectoriesAndMissingScopes(t *testing.T) {
	ctx, cancel := context.WithTimeout(t.Context(), time.Minute)
	defer cancel()
	target := t.TempDir()
	var directories []string
	for _, cluster := range []string{"cluster-a", "cluster-b", "cluster-a"} {
		b, err := newAzureFailureBundle(ctx, target, "SameRG", "samerg", cluster, "../same/pool")
		require.NoError(t, err)
		assert.NotContains(t, directories, b.directory, "same-nodepool failures never overwrite a previous bundle")
		directories = append(directories, b.directory)
		assert.Equal(t, target, filepath.Dir(b.directory))
		assert.True(t, strings.HasPrefix(filepath.Base(b.directory), "azure-failure-_same_pool-"))
		initial := readAzureBundleManifest(t, b.directory)
		assert.Equal(t, cluster, initial.Cluster)
		assert.Equal(t, "../same/pool", initial.NodePool)
		assert.Equal(t, "SameRG", initial.CustomerResourceGroup)
		assert.Equal(t, "samerg", initial.ManagedResourceGroup)
		deadline, _ := ctx.Deadline()
		assert.True(t, deadline.Equal(initial.Deadline))
		assert.Empty(t, initial.Operations)
		assert.True(t, initial.FinishedAt.IsZero())
		require.NoError(t, b.collect(ctx, nil, nil, nil))
		manifest := readAzureBundleManifest(t, b.directory)
		require.Len(t, manifest.Operations, 3, "same RG with different casing is collected only once")
		for _, op := range manifest.Operations {
			assert.Equal(t, "SameRG", op.ResourceGroup)
			assert.Equal(t, "skipped-client-unavailable", op.Status)
		}
	}
	for i, directory := range directories {
		wantCluster := "cluster-a"
		if i == 1 {
			wantCluster = "cluster-b"
		}
		assert.Equal(t, wantCluster, readAzureBundleManifest(t, directory).Cluster)
	}
	b, err := newAzureFailureBundle(ctx, target, "customer", "", "cluster", "pool")
	require.NoError(t, err)
	require.NoError(t, b.collect(ctx, nil, nil, nil))
	manifest := readAzureBundleManifest(t, b.directory)
	require.Len(t, manifest.Operations, 4)
	assert.Equal(t, "skipped-missing-resource-group", azureBundleOperation(t, manifest, "", "resource-group", "").Status)
}

func TestAzureFailureBundleHookDetachesCancellation(t *testing.T) {
	// No Ginkgo setup or global test context: inject cached credentials and a local ARM endpoint.
	var requests atomic.Int32
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		requests.Add(1)
		assert.Equal(t, http.MethodGet, req.Method)
		assert.True(t, strings.HasPrefix(req.URL.Path, "/subscriptions/"+fakeSubscriptionID+"/resourceGroups/rg/providers/"), req.URL.Path)
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"value":[]}`)
	}))
	defer server.Close()
	tc := &perItOrDescribeTestContext{
		LogDirPath:     t.TempDir(),
		subscriptionID: fakeSubscriptionID,
		perBinaryInvocationTestContext: &perBinaryInvocationTestContext{
			azureCredentials:         &azfake.TokenCredential{},
			resourceManagerEndpoint:  server.URL,
			isDevelopmentEnvironment: true,
			defaultTransport:         server.Client().Transport.(*http.Transport),
		},
	}
	ctx, cancel := context.WithDeadline(t.Context(), time.Now().Add(-time.Minute))
	defer cancel()
	require.ErrorIs(t, ctx.Err(), context.DeadlineExceeded)
	started := time.Now()
	tc.collectNodePoolFailureArtifacts(ctx, logr.Discard(), "rg", "RG", "cluster", "pool")
	entries, err := os.ReadDir(tc.LogDirPath)
	require.NoError(t, err)
	require.Len(t, entries, 1)
	manifest := readAzureBundleManifest(t, filepath.Join(tc.LogDirPath, entries[0].Name()))
	require.Len(t, manifest.Operations, 13, "three client initializations and ten collection categories")
	assert.Equal(t, int32(10), requests.Load())
	assert.True(t, manifest.Deadline.After(started.Add(4*time.Minute)), "hook must replace the expired parent deadline")
	assert.True(t, manifest.Deadline.Before(time.Now().Add(5*time.Minute)))
	assert.False(t, manifest.FinishedAt.IsZero())
	for _, op := range manifest.Operations {
		assert.Empty(t, op.Error, op.Operation)
		if strings.HasSuffix(op.Operation, "-client") {
			assert.Equal(t, "success", op.Status, op.Operation)
		} else {
			assert.Equal(t, "empty", op.Status, op.Operation)
		}
	}
	// The unconfigured hook must return before accessing a nil invocation context.
	(&perItOrDescribeTestContext{}).collectNodePoolFailureArtifacts(ctx, logr.Discard(), "rg", "RG", "cluster", "pool")
}
