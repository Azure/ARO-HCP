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

package slots

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	azcorearm "github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/policy"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/to"
	"github.com/Azure/azure-sdk-for-go/sdk/azidentity"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/resources/armsubscriptions"
)

type subscriptionTestTransport struct {
	t             *testing.T
	subscriptions []*armsubscriptions.Subscription
	tokenRequests int
	listRequests  int
}

func (s *subscriptionTestTransport) Do(request *http.Request) (*http.Response, error) {
	var payload any
	switch {
	case request.URL.Host == "login.microsoftonline.com" && strings.HasSuffix(request.URL.Path, "/.well-known/openid-configuration"):
		payload = map[string]string{
			"authorization_endpoint": "https://login.microsoftonline.com/test-tenant/oauth2/v2.0/authorize",
			"token_endpoint":         "https://login.microsoftonline.com/test-tenant/oauth2/v2.0/token",
			"issuer":                 "https://login.microsoftonline.com/test-tenant/v2.0",
		}
	case request.URL.Host == "login.microsoftonline.com" && request.URL.Path == "/test-tenant/oauth2/v2.0/token":
		s.tokenRequests++
		if err := request.ParseForm(); err != nil {
			return nil, err
		}
		if request.Form.Get("client_id") != "test-client" || request.Form.Get("client_secret") != "test-secret" {
			s.t.Fatalf("subscription resolver did not use selected profile credentials")
		}
		payload = map[string]any{"access_token": "unit-test", "token_type": "Bearer", "expires_in": 3600}
	case request.URL.Host == "management.azure.com" && request.Method == http.MethodGet && request.URL.Path == "/subscriptions":
		s.listRequests++
		if request.Header.Get("Authorization") != "Bearer unit-test" {
			s.t.Fatal("subscription list did not use the selected profile token")
		}
		payload = map[string]any{"value": s.subscriptions}
	default:
		return nil, fmt.Errorf("unexpected SDK request %s %s", request.Method, request.URL)
	}
	data, err := json.Marshal(payload)
	if err != nil {
		return nil, err
	}
	return &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{"Content-Type": {"application/json"}},
		Body:       io.NopCloser(strings.NewReader(string(data))),
		Request:    request,
	}, nil
}

func TestResolvePoolSubscriptionsWithProfileCredentials(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		name, infraName, binding, e2eID, want string
		visibleInfra, unreadableBinding       bool
	}{
		{name: "public E2E only without infra file or visible subscription", e2eID: "e2e-id"},
		{name: "E2E only ignores unreadable infra binding", e2eID: "e2e-id", unreadableBinding: true},
		{name: "E2E only does not validate unrelated infrastructure subscription", e2eID: "e2e-id", visibleInfra: true},
		{name: "E2E only requires visible E2E subscription", want: `subscription with name "customer" was not visible`},
		{name: "E2E only rejects blank E2E ID", e2eID: " ", want: "blank display name or ID"},
		{name: "required infra resolves matching binding", e2eID: "e2e-id", infraName: "infra", binding: "INFRA-ID", visibleInfra: true},
		{name: "required infra rejects binding mismatch", e2eID: "e2e-id", infraName: "infra", binding: "other-id", visibleInfra: true, want: "binds deploy environment"},
		{name: "required infra rejects missing binding", e2eID: "e2e-id", infraName: "infra", visibleInfra: true, want: "infra-stg-subscription-id"},
		{name: "required infra rejects blank binding", e2eID: "e2e-id", infraName: "infra", binding: " ", visibleInfra: true, want: "is empty"},
		{name: "required infra must be visible", e2eID: "e2e-id", infraName: "infra", binding: "infra-id", want: `subscription with name "infra" was not visible`},
		{name: "required infra does not replace missing E2E", infraName: "infra", binding: "infra-id", visibleInfra: true, want: `subscription with name "customer" was not visible`},
	} {
		t.Run(test.name, func(t *testing.T) {
			dir := t.TempDir()
			for name, value := range map[string]string{"tenant": "test-tenant", "client-id": "test-client", "client-secret": "test-secret"} {
				if err := os.WriteFile(filepath.Join(dir, name), []byte(value), 0o600); err != nil {
					t.Fatal(err)
				}
			}
			bindingPath := filepath.Join(dir, "infra-stg-subscription-id")
			if test.binding != "" {
				if err := os.WriteFile(bindingPath, []byte(test.binding), 0o600); err != nil {
					t.Fatal(err)
				}
			}
			if test.unreadableBinding {
				if err := os.Mkdir(bindingPath, 0o700); err != nil {
					t.Fatal(err)
				}
			}
			transport := &subscriptionTestTransport{t: t}
			if test.e2eID != "" {
				transport.subscriptions = append(transport.subscriptions, &armsubscriptions.Subscription{DisplayName: to.Ptr("customer"), SubscriptionID: to.Ptr(test.e2eID)})
			}
			if test.visibleInfra {
				transport.subscriptions = append(transport.subscriptions, &armsubscriptions.Subscription{DisplayName: to.Ptr("infra"), SubscriptionID: to.Ptr("infra-id")})
				if test.infraName == "" {
					transport.subscriptions = append(transport.subscriptions, &armsubscriptions.Subscription{DisplayName: to.Ptr("infra"), SubscriptionID: to.Ptr("conflicting-infra-id")})
				}
			}
			resolved, err := resolvePoolSubscriptions(t.Context(), dir, "stg", "customer", test.infraName,
				&azidentity.ClientSecretCredentialOptions{ClientOptions: policy.ClientOptions{Transport: transport}, DisableInstanceDiscovery: true},
				&azcorearm.ClientOptions{ClientOptions: policy.ClientOptions{Transport: transport}})
			if transport.tokenRequests != 1 || transport.listRequests != 1 {
				t.Fatalf("expected actual profile credential and SDK list resolution, got token=%d list=%d: %v", transport.tokenRequests, transport.listRequests, err)
			}
			if test.want != "" {
				if err == nil || !strings.Contains(err.Error(), test.want) {
					t.Fatalf("expected %q, got %+v, %v", test.want, resolved, err)
				}
				return
			}
			if err != nil || resolved.E2E != (ResolvedSubscription{Name: "customer", ID: "e2e-id"}) {
				t.Fatalf("E2E resolution failed: %+v, %v", resolved, err)
			}
			if test.infraName == "" {
				if resolved.Infrastructure != (ResolvedSubscription{}) {
					t.Fatalf("E2E-only resolution returned infrastructure: %+v", resolved)
				}
			} else if resolved.Infrastructure != (ResolvedSubscription{Name: "infra", ID: "infra-id"}) {
				t.Fatalf("required infrastructure was not resolved: %+v", resolved)
			}
		})
	}
}

func TestResolvePoolSubscriptionsRequiresE2EName(t *testing.T) {
	t.Parallel()
	for _, infra := range []string{"", "infra"} {
		_, err := ResolvePoolSubscriptions(t.Context(), "", "prod", " ", infra)
		if err == nil || !strings.Contains(err.Error(), "E2E subscription name is empty") {
			t.Fatalf("missing E2E name must fail before credential access: %v", err)
		}
	}
}

func TestReadRequiredProfileFile(t *testing.T) {
	t.Parallel()
	for _, value := range []string{"", " \t\n", " profile-value \n"} {
		t.Run(value, func(t *testing.T) {
			dir := t.TempDir()
			if err := os.WriteFile(filepath.Join(dir, "tenant"), []byte(value), 0o600); err != nil {
				t.Fatal(err)
			}
			got, err := ReadRequiredProfileFile(dir, "tenant")
			if strings.TrimSpace(value) == "" {
				if err == nil || !strings.Contains(err.Error(), "is empty") {
					t.Fatalf("expected empty profile value error, got %q, %v", got, err)
				}
			} else if err != nil || got != "profile-value" {
				t.Fatalf("expected trimmed profile value, got %q, %v", got, err)
			}
		})
	}
	if _, err := ReadRequiredProfileFile(" \t", "tenant"); err == nil || !strings.Contains(err.Error(), "cluster profile dir is empty") {
		t.Fatalf("expected empty directory error, got %v", err)
	}
	if _, err := ReadRequiredProfileFile(t.TempDir(), "tenant"); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("expected missing profile file error, got %v", err)
	}
}

func TestDeploymentEnvironmentName(t *testing.T) {
	t.Parallel()
	for _, name := range []string{"ci01", "dev-xs", "dev_xs", "INT", "0"} {
		if err := validateDeploymentEnvironmentName(name); err != nil {
			t.Errorf("valid deployment environment %q rejected: %v", name, err)
		}
	}
	for _, name := range []string{"", "../outside", "../../outside", `..\..\outside`, "/absolute", `C:\outside`, ".", "..", "ci.01", "ci 01", "ci01\n", "ci01\x00", "-ci01"} {
		t.Run(name, func(t *testing.T) {
			// No profile directory is supplied: reject the name before reading
			// credentials or making an Azure request.
			_, err := ResolvePoolSubscriptions(context.Background(), "", name, "customer", "infra")
			if err == nil || !strings.Contains(err.Error(), "invalid deployment environment name") {
				t.Fatalf("expected early deployment name rejection for %q, got %v", name, err)
			}
		})
	}
}

func TestAddSubscriptionIDs(t *testing.T) {
	t.Parallel()

	subscription := func(name, id string) *armsubscriptions.Subscription {
		return &armsubscriptions.Subscription{DisplayName: to.Ptr(name), SubscriptionID: to.Ptr(id)}
	}
	for _, test := range []struct {
		name  string
		pages [][]*armsubscriptions.Subscription
		want  string
	}{
		{"distinct names", [][]*armsubscriptions.Subscription{{subscription("customer", "id-a")}, {subscription("infra", "id-b")}}, ""},
		{"same ID repeated", [][]*armsubscriptions.Subscription{{subscription("customer", "ID-A")}, {subscription("customer", "id-a")}}, ""},
		{"same page conflict", [][]*armsubscriptions.Subscription{{subscription("customer", "id-a"), subscription("customer", "id-b")}}, "ambiguous"},
		{"later page conflict", [][]*armsubscriptions.Subscription{{subscription("infra", "id-a")}, {subscription("infra", "id-b")}}, "ambiguous"},
		{"normalized name conflict", [][]*armsubscriptions.Subscription{{subscription("infra", "id-a")}, {subscription(" infra ", "id-b")}}, "ambiguous"},
		{"nil entry", [][]*armsubscriptions.Subscription{{nil}}, "without display name or ID"},
		{"missing name", [][]*armsubscriptions.Subscription{{{SubscriptionID: to.Ptr("id-a")}}}, "without display name or ID"},
		{"missing ID", [][]*armsubscriptions.Subscription{{{DisplayName: to.Ptr("customer")}}}, "without display name or ID"},
		{"empty name", [][]*armsubscriptions.Subscription{{subscription("", "id-a")}}, "blank display name or ID"},
		{"blank name", [][]*armsubscriptions.Subscription{{subscription(" \t", "id-a")}}, "blank display name or ID"},
		{"empty ID", [][]*armsubscriptions.Subscription{{subscription("customer", "")}}, "blank display name or ID"},
		{"blank ID", [][]*armsubscriptions.Subscription{{subscription("customer", " \t")}}, "blank display name or ID"},
	} {
		t.Run(test.name, func(t *testing.T) {
			ids := map[string]string{}
			var err error
			for _, page := range test.pages {
				if err = addSubscriptionIDs(ids, page); err != nil {
					break
				}
			}
			if test.want != "" {
				if err == nil || !strings.Contains(err.Error(), test.want) {
					t.Fatalf("expected %q, got %v", test.want, err)
				}
			} else if err != nil {
				t.Fatalf("valid subscription inventory failed: %v", err)
			} else if ids["customer"] != "id-a" {
				t.Fatalf("customer subscription was not indexed: %v", ids)
			}
		})
	}
}

func TestVerifyCustomerSubscriptionName(t *testing.T) {
	t.Parallel()

	clusterProfileDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(clusterProfileDir, "customer-dev-subscription-name"), []byte("customer-dev\n"), 0o644); err != nil {
		t.Fatalf("expected write to succeed: %v", err)
	}
	if err := os.WriteFile(filepath.Join(clusterProfileDir, "customer-other-subscription-name"), []byte("customer-other\n"), 0o644); err != nil {
		t.Fatalf("expected write to succeed: %v", err)
	}

	resolved, matchedDir, err := VerifyCustomerSubscriptionName([]string{clusterProfileDir}, "customer-dev")
	if err != nil {
		t.Fatalf("expected subscription verification to succeed: %v", err)
	}
	if resolved != "customer-dev" {
		t.Fatalf("expected verified subscription %q, got %q", "customer-dev", resolved)
	}
	if matchedDir != clusterProfileDir {
		t.Fatalf("expected matched dir %q, got %q", clusterProfileDir, matchedDir)
	}
}

func TestVerifyCustomerSubscriptionNameResolvesAcrossDirs(t *testing.T) {
	t.Parallel()

	rhDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(rhDir, "customer-shard0-subscription-name"), []byte("rh-sub\n"), 0o644); err != nil {
		t.Fatalf("expected write to succeed: %v", err)
	}
	testTenantDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(testTenantDir, "customer-shard0-subscription-name"), []byte("test-tenant-sub\n"), 0o644); err != nil {
		t.Fatalf("expected write to succeed: %v", err)
	}

	resolved, matchedDir, err := VerifyCustomerSubscriptionName([]string{rhDir, testTenantDir}, "test-tenant-sub")
	if err != nil {
		t.Fatalf("expected subscription verification to succeed: %v", err)
	}
	if resolved != "test-tenant-sub" {
		t.Fatalf("expected verified subscription %q, got %q", "test-tenant-sub", resolved)
	}
	if matchedDir != testTenantDir {
		t.Fatalf("expected matched dir %q, got %q", testTenantDir, matchedDir)
	}
}

func TestVerifyCustomerSubscriptionNameRejectsMatchInMultipleDirs(t *testing.T) {
	t.Parallel()

	dirA := t.TempDir()
	dirB := t.TempDir()
	for _, dir := range []string{dirA, dirB} {
		if err := os.WriteFile(filepath.Join(dir, "customer-shard0-subscription-name"), []byte("dup-sub\n"), 0o644); err != nil {
			t.Fatalf("expected write to succeed: %v", err)
		}
	}

	_, _, err := VerifyCustomerSubscriptionName([]string{dirA, dirB}, "dup-sub")
	if err == nil {
		t.Fatal("expected cross-dir duplicate match verification to fail")
	}
	if !strings.Contains(err.Error(), "multiple customer subscription name files matched") {
		t.Fatalf("expected duplicate match error, got %v", err)
	}
}

func TestVerifyCustomerSubscriptionNameRejectsDuplicateMatches(t *testing.T) {
	t.Parallel()

	clusterProfileDir := t.TempDir()
	for _, fileName := range []string{
		"customer-dev-1-subscription-name",
		"customer-dev-2-subscription-name",
	} {
		if err := os.WriteFile(filepath.Join(clusterProfileDir, fileName), []byte("customer-dev\n"), 0o644); err != nil {
			t.Fatalf("expected write to succeed: %v", err)
		}
	}

	_, _, err := VerifyCustomerSubscriptionName([]string{clusterProfileDir}, "customer-dev")
	if err == nil {
		t.Fatal("expected duplicate match verification to fail")
	}
	if !strings.Contains(err.Error(), "multiple customer subscription name files matched") {
		t.Fatalf("expected duplicate match error, got %v", err)
	}
}
