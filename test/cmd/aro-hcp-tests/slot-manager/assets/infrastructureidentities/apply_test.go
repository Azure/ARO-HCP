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

package infrastructureidentities

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"testing"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	azcorearm "github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/policy"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/msi/armmsi"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/resources/armresources"

	"github.com/Azure/ARO-HCP/test/cmd/aro-hcp-tests/slot-manager/assets"
	"github.com/Azure/ARO-HCP/test/cmd/aro-hcp-tests/slot-manager/slots"
)

type poolTransport struct {
	mu         sync.Mutex
	scenario   string
	puts       []string
	groups     map[string][]string
	missing    map[string]bool
	listExtras bool
}

func (p *poolTransport) Do(request *http.Request) (*http.Response, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	path := request.URL.Path
	lower := strings.ToLower(path)
	status := http.StatusOK
	var payload any
	resourceGroup := resourceGroupFromPath(path)
	switch {
	case request.Method == http.MethodPut && strings.Contains(lower, "/userassignedidentities/"):
		p.puts = append(p.puts, path)
		_, name, _ := strings.Cut(lower, "/userassignedidentities/")
		payload = map[string]any{"name": name, "location": "westus3"}
	case request.Method == http.MethodPut:
		p.puts = append(p.puts, path)
		payload = map[string]any{"name": resourceGroup, "location": "westus3"}
	case strings.HasSuffix(lower, "/userassignedidentities"):
		names := p.groups[strings.ToLower(resourceGroup)]
		if names == nil && !p.missing[strings.ToLower(resourceGroup)] {
			names = identitiesForResourceGroup(resourceGroup)
			if p.listExtras {
				names = append(names, "unexpected")
			}
		}
		identities := []map[string]any{}
		for _, name := range names {
			identities = append(identities, map[string]any{"name": name})
		}
		payload = map[string]any{"value": identities}
	case request.Method == http.MethodGet:
		if p.missing[strings.ToLower(resourceGroup)] {
			status = http.StatusNotFound
			payload = map[string]any{"error": map[string]string{"code": "ResourceGroupNotFound", "message": "missing"}}
			break
		}
		if p.scenario == "get failure" {
			return nil, fmt.Errorf("fake resource group get failure")
		}
		payload = map[string]any{"name": resourceGroup, "location": "westus3"}
	default:
		return nil, fmt.Errorf("unexpected SDK request %s %s", request.Method, path)
	}
	data, err := json.Marshal(payload)
	if err != nil {
		return nil, err
	}
	return &http.Response{
		StatusCode: status, Header: http.Header{"Content-Type": []string{"application/json"}},
		Body: io.NopCloser(bytes.NewReader(data)), Request: request,
	}, nil
}

func identitiesForResourceGroup(resourceGroup string) []string {
	if strings.Contains(resourceGroup, "-mgmt-") {
		return DefaultTopology().ManagementIdentityNames
	}
	return DefaultTopology().ServiceIdentityNames
}

func testPoolClients(t *testing.T, transport *poolTransport) *poolClients {
	t.Helper()
	options := &azcorearm.ClientOptions{ClientOptions: policy.ClientOptions{
		Transport: transport, Retry: policy.RetryOptions{MaxRetries: -1},
	}}
	groups, err := armresources.NewResourceGroupsClient("sub", admissionCredential{}, options)
	if err != nil {
		t.Fatal(err)
	}
	factory, err := armmsi.NewClientFactory("sub", admissionCredential{}, options)
	if err != nil {
		t.Fatal(err)
	}
	return &poolClients{resourceGroups: groups, identities: factory.NewUserAssignedIdentitiesClient()}
}

func testInventory() slots.AssetInventory {
	return slots.AssetInventory{
		AssetPool: slots.AssetPool{
			Name: "dev-infrastructure-identities", Kind: slots.KindInfrastructureIdentities,
			ResourceNamePrefix:       "bundle",
			InfrastructureIdentities: &slots.InfrastructureIdentitiesConfig{ProvisioningRegion: "westus3"},
		},
		Capacity:         1,
		SubscriptionName: "infra",
	}
}

func TestApplyCreatesExpectedBundleResources(t *testing.T) {
	t.Parallel()
	transport := &poolTransport{}
	handler := NewHandler(
		WithPoolDependencies(func() (azcore.TokenCredential, subscriptionIDResolverFunc, error) {
			return admissionCredential{}, func(context.Context, string) (string, error) { return "sub", nil }, nil
		}),
		WithPoolClients(func(string, azcore.TokenCredential) (*poolClients, error) {
			return testPoolClients(t, transport), nil
		}),
	)
	err := handler.ApplyPools(context.Background(), assets.PoolRequest{
		Environment: "dev", AssetInventories: []slots.AssetInventory{testInventory()},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(transport.puts) != 3+30 {
		t.Fatalf("expected 3 resource groups and 30 identities, got %d puts: %v", len(transport.puts), transport.puts)
	}
}

func TestApplyRequiresProvisioningRegion(t *testing.T) {
	t.Parallel()
	inventory := testInventory()
	inventory.AssetPool.InfrastructureIdentities = nil
	handler := NewHandler(
		WithPoolDependencies(func() (azcore.TokenCredential, subscriptionIDResolverFunc, error) {
			return admissionCredential{}, func(context.Context, string) (string, error) { return "sub", nil }, nil
		}),
		WithPoolClients(func(string, azcore.TokenCredential) (*poolClients, error) {
			return nil, errors.New("clients should not be created")
		}),
	)
	err := handler.ApplyPools(context.Background(), assets.PoolRequest{
		Environment: "dev", AssetInventories: []slots.AssetInventory{inventory},
	})
	if err == nil || !strings.Contains(err.Error(), "provisioning_region") {
		t.Fatalf("expected provisioning region error, got %v", err)
	}
}

func TestValidateReportsMissingAndUnexpectedInventory(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name    string
		missing map[string]bool
		groups  map[string][]string
		extras  bool
		want    string
	}{
		{name: "complete", want: ""},
		{name: "missing group", missing: map[string]bool{"bundle-00-svc": true}, want: "missing resource group bundle-00-svc"},
		{name: "missing identity", groups: map[string][]string{"bundle-00-svc": DefaultTopology().ServiceIdentityNames[1:]}, want: "missing identity bundle-00-svc/admin-api"},
		{name: "unexpected identity", extras: true, want: "unexpected identity bundle-00-svc/unexpected"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			transport := &poolTransport{missing: tc.missing, groups: tc.groups, listExtras: tc.extras}
			handler := NewHandler(
				WithPoolDependencies(func() (azcore.TokenCredential, subscriptionIDResolverFunc, error) {
					return admissionCredential{}, func(context.Context, string) (string, error) { return "sub", nil }, nil
				}),
				WithPoolClients(func(string, azcore.TokenCredential) (*poolClients, error) {
					return testPoolClients(t, transport), nil
				}),
			)
			var out bytes.Buffer
			err := handler.ValidatePools(context.Background(), assets.PoolRequest{
				Environment: "dev", AssetInventories: []slots.AssetInventory{testInventory()}, Out: &out,
			})
			if tc.want == "" {
				if err != nil {
					t.Fatalf("complete inventory must pass: %v\n%s", err, out.String())
				}
				return
			}
			if err == nil || !strings.Contains(out.String(), tc.want) {
				t.Fatalf("expected report %q, got err=%v output=%s", tc.want, err, out.String())
			}
		})
	}
}
