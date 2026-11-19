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

package slotmanager

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"sync"
	"testing"
	"testing/fstest"
	"time"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/cloud"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/policy"
	msgraph "github.com/microsoftgraph/msgraph-sdk-go"
	graphauth "github.com/microsoftgraph/msgraph-sdk-go-core/authentication"

	"github.com/Azure/ARO-HCP/test/cmd/aro-hcp-tests/slot-manager/slots"
)

const externalSDKAppID = "44444444-4444-4444-8444-444444444444"
const externalSDKObjectID = "33333333-3333-4333-8333-333333333333"

// This endpoint exercises the real subscription/provider pagers and deployment
// pollers, including typed request serialization and response decoding. It never
// invokes a subprocess or contacts Azure.
type externalSDKEndpoint struct {
	t                  *testing.T
	server             *httptest.Server
	preview            string
	asyncPreview       bool
	previewPolls       int
	previewStatus      int
	applyState         string
	asyncApply         bool
	applyPolls         int
	tenant             string
	targetID           string
	duplicate          bool
	missingApplication bool
	missingProvider    bool
	registering        bool
	providerReads      int
	writes             int
	listPages          int
	validations        []string
	applications       []string
}

type externalSDKCredential struct {
	mu     sync.Mutex
	scopes []string
}

func (c *externalSDKCredential) GetToken(_ context.Context, options policy.TokenRequestOptions) (azcore.AccessToken, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.scopes = append(c.scopes, options.Scopes...)
	return azcore.AccessToken{Token: "endpoint-token", ExpiresOn: time.Now().Add(time.Hour)}, nil
}

func newExternalSDKEndpoint(t *testing.T) *externalSDKEndpoint {
	e := &externalSDKEndpoint{t: t, preview: `{"status":"Succeeded","properties":{"changes":[]}}`, applyState: "Succeeded", tenant: externalSubscriptionTenant, targetID: externalTestSubscription}
	e.server = httptest.NewTLSServer(http.HandlerFunc(e.serveHTTP))
	t.Cleanup(e.server.Close)
	return e
}

func (e *externalSDKEndpoint) configure(o *ReconcileExternalSubscriptionOptions) {
	o.clientOptions = &arm.ClientOptions{ClientOptions: azcore.ClientOptions{
		Transport: e.server.Client(),
		Cloud: cloud.Configuration{Services: map[cloud.ServiceName]cloud.ServiceConfiguration{
			cloud.ResourceManager: {Endpoint: e.server.URL, Audience: "https://management.azure.com"},
		}},
		Retry: policy.RetryOptions{MaxRetries: -1},
	}}
}

func (e *externalSDKEndpoint) clients(t *testing.T, o *ReconcileExternalSubscriptionOptions, credential azcore.TokenCredential) {
	t.Helper()
	e.configure(o)
	if err := o.newClients(credential); err != nil {
		t.Fatal(err)
	}
	auth, err := graphauth.NewAzureIdentityAuthenticationProviderWithScopesAndValidHosts(credential, []string{"https://graph.microsoft.com/.default"}, []string{"127.0.0.1"})
	if err != nil {
		t.Fatal(err)
	}
	adapter, err := msgraph.NewGraphRequestAdapterWithParseNodeFactoryAndSerializationWriterFactoryAndHttpClient(auth, nil, nil, e.server.Client())
	if err != nil {
		t.Fatal(err)
	}
	adapter.SetBaseUrl(e.server.URL + "/v1.0")
	o.graph = msgraph.NewGraphServiceClient(adapter)
}

func (e *externalSDKEndpoint) serveHTTP(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	path := r.URL.Path
	if r.Header.Get("Authorization") != "Bearer endpoint-token" {
		e.t.Errorf("SDK request did not use supplied credential: %s", path)
	}
	switch {
	case r.Method == http.MethodGet && path == "/operations/whatif":
		e.previewPolls++
		fmt.Fprint(w, e.preview)
	case r.Method == http.MethodGet && path == "/operations/deployment":
		e.applyPolls++
		fmt.Fprintf(w, `{"status":%q}`, e.applyState)
	case r.Method == http.MethodGet && strings.Contains(path, "/deployments/external-"):
		fmt.Fprintf(w, `{"id":%q,"properties":{"provisioningState":%q}}`, path, e.applyState)
	case r.Method == http.MethodGet && strings.Contains(path, "/applications"):
		if e.missingApplication {
			w.WriteHeader(http.StatusNotFound)
			fmt.Fprint(w, `{"error":{"code":"Request_ResourceNotFound","message":"shared application absent"}}`)
			return
		}
		name := ""
		for _, candidate := range []string{"bot", "fp", "arm", "msi", "shared-app"} {
			if strings.Contains(path, "uniqueName='"+candidate+"'") || strings.Contains(r.URL.Query().Get("$filter"), "displayName eq '"+strings.ReplaceAll(candidate, "-", " ")+"'") {
				name = candidate
			}
		}
		if name == "" {
			e.t.Errorf("unexpected Graph application request: %s", r.URL)
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		e.applications = append(e.applications, name)
		application := map[string]any{"id": externalSDKObjectID, "appId": externalSDKAppID, "displayName": strings.ReplaceAll(name, "-", " "), "uniqueName": name}
		if path == "/v1.0/applications" {
			json.NewEncoder(w).Encode(map[string]any{"value": []any{application}})
			return
		}
		json.NewEncoder(w).Encode(application)
	case r.Method == http.MethodGet && strings.Contains(path, "/servicePrincipals"):
		fmt.Fprintf(w, `{"id":"55555555-5555-4555-8555-555555555555","appId":%q,"accountEnabled":true}`, externalSDKAppID)
	case r.Method == http.MethodGet && path == "/subscriptions/"+externalTestSubscription:
		fmt.Fprintf(w, `{"subscriptionId":%q,"displayName":"Hypershift Managed Azure","tenantId":%q,"state":"Enabled"}`, e.targetID, e.tenant)
	case r.Method == http.MethodGet && path == "/subscriptions":
		e.listPages++
		if r.URL.Query().Get("page") == "2" {
			id := externalTestSubscription
			if e.duplicate {
				id = "ece8c944-782e-4519-a4e3-2ba5b0fa9950"
			}
			fmt.Fprintf(w, `{"value":[{"subscriptionId":%q,"displayName":"Hypershift Managed Azure"}]}`, id)
			return
		}
		fmt.Fprintf(w, `{"value":[{"subscriptionId":%q,"displayName":"Hypershift Managed Azure"}],"nextLink":%q}`, externalTestSubscription, e.server.URL+"/subscriptions?page=2")
	case r.Method == http.MethodGet && strings.HasSuffix(path, "/providers"):
		e.providerReads++
		if r.URL.Query().Get("page") == "2" {
			state := "Registered"
			if e.missingProvider {
				state = "NotRegistered"
			}
			if e.registering {
				state = "Registering"
				e.registering = false
			}
			fmt.Fprintf(w, `{"value":[{"namespace":"Microsoft.Authorization","registrationState":%q}]}`, state)
			return
		}
		fmt.Fprintf(w, `{"value":[{"namespace":"Microsoft.Compute","registrationState":"Registered"}],"nextLink":%q}`, e.server.URL+path+"?page=2")
	case r.Method == http.MethodPost && strings.HasSuffix(path, "/register"):
		e.writes++
		e.missingProvider = false
		e.registering = true
		fmt.Fprint(w, `{"namespace":"Microsoft.Authorization","registrationState":"Registering"}`)
	case r.Method == http.MethodPost && strings.HasSuffix(path, "/whatIf"):
		var body struct {
			Properties struct {
				Mode            string
				ValidationLevel string
				Template        map[string]any
				Parameters      map[string]any
				WhatIfSettings  struct{ ResultFormat string }
			}
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			e.t.Error(err)
		}
		if body.Properties.Mode != "Incremental" || body.Properties.WhatIfSettings.ResultFormat != "FullResourcePayloads" {
			e.t.Errorf("unsafe what-if request: %+v", body)
		}
		e.validations = append(e.validations, body.Properties.ValidationLevel)
		if len(body.Properties.Parameters) > 0 {
			e.assertParameters(path, body.Properties.Parameters)
		}
		if e.previewStatus != 0 {
			w.WriteHeader(e.previewStatus)
			fmt.Fprint(w, `{"error":{"code":"AuthorizationFailed","message":"preview denied"}}`)
			return
		}
		if e.asyncPreview {
			w.Header().Set("Location", e.server.URL+"/operations/whatif")
			w.WriteHeader(http.StatusAccepted)
			return
		}
		fmt.Fprint(w, e.preview)
	case r.Method == http.MethodPut && strings.Contains(path, "/deployments/external-"):
		e.writes++
		var body struct {
			Properties struct {
				Mode       string
				Template   map[string]any
				Parameters map[string]any
			}
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			e.t.Error(err)
		}
		if body.Properties.Mode != "Incremental" || len(body.Properties.Template) == 0 || len(body.Properties.Parameters) == 0 {
			e.t.Errorf("unsafe deployment request: %+v", body)
		}
		e.assertParameters(path, body.Properties.Parameters)
		if e.asyncApply {
			w.Header().Set("Azure-AsyncOperation", e.server.URL+"/operations/deployment")
			w.WriteHeader(http.StatusCreated)
			fmt.Fprintf(w, `{"id":%q,"properties":{"provisioningState":"Running"}}`, path)
			return
		}
		fmt.Fprintf(w, `{"id":%q,"properties":{"provisioningState":%q}}`, path, e.applyState)
	default:
		e.t.Errorf("unexpected SDK request: %s %s", r.Method, r.URL)
		w.WriteHeader(http.StatusBadRequest)
		fmt.Fprint(w, `{"error":{"code":"UnexpectedRequest","message":"unexpected SDK operation"}}`)
	}
}

func TestExternalSubscriptionSDKScopeAndPagination(t *testing.T) {
	for _, test := range []struct {
		name                string
		uuidOnly, duplicate bool
		tenant, target      string
		wantError           bool
	}{
		{name: "all subscription pages", tenant: externalSubscriptionTenant, target: externalTestSubscription},
		{name: "duplicate on later page", duplicate: true, tenant: externalSubscriptionTenant, target: externalTestSubscription, wantError: true},
		{name: "UUID skips duplicate names", uuidOnly: true, duplicate: true, tenant: externalSubscriptionTenant, target: externalTestSubscription},
		{name: "tenant guard", tenant: "other", target: externalTestSubscription, wantError: true},
		{name: "immutable target guard", tenant: externalSubscriptionTenant, target: "ece8c944-782e-4519-a4e3-2ba5b0fa9950", wantError: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			e := newExternalSDKEndpoint(t)
			e.tenant, e.targetID, e.duplicate = test.tenant, test.target, test.duplicate
			pool := externalTestPool("target")
			if test.uuidOnly {
				pool.Subscriptions.E2E = externalTestSubscription
			}
			o := &ReconcileExternalSubscriptionOptions{completedReconcileExternalSubscriptionOptions: &completedReconcileExternalSubscriptionOptions{SubscriptionID: externalTestSubscription, catalog: &slots.Catalog{Environments: map[string]slots.Environment{"dev": {Pools: []slots.Pool{pool}}}}}}
			e.clients(t, o, &externalSDKCredential{})
			_, _, err := o.selectSubscription(t.Context())
			if (err != nil) != test.wantError {
				t.Fatalf("got %v, wantError %v", err, test.wantError)
			}
			if test.uuidOnly && e.listPages != 0 {
				t.Fatal("UUID binding performed name lookup")
			}
			if !test.uuidOnly && test.tenant == externalSubscriptionTenant && test.target == externalTestSubscription && e.listPages != 2 {
				t.Fatalf("list did not follow every page: %d", e.listPages)
			}
			if e.writes != 0 {
				t.Fatal("scope admission mutated Azure")
			}
		})
	}
}

func TestExternalSubscriptionSDKProviderRegistrationReadiness(t *testing.T) {
	e := newExternalSDKEndpoint(t)
	e.missingProvider = true
	o := &ReconcileExternalSubscriptionOptions{completedReconcileExternalSubscriptionOptions: &completedReconcileExternalSubscriptionOptions{SubscriptionID: externalTestSubscription}}
	e.clients(t, o, &externalSDKCredential{})
	providers := externalProviders("Microsoft.Compute,Microsoft.Compute")
	if !reflect.DeepEqual(providers, []string{"Microsoft.Authorization", "Microsoft.Compute"}) {
		t.Fatal(providers)
	}
	missing, err := o.missingProviders(t.Context(), providers)
	if err != nil || !reflect.DeepEqual(missing, []string{"Microsoft.Authorization"}) {
		t.Fatalf("missing=%v err=%v", missing, err)
	}
	if e.writes != 0 {
		t.Fatal("provider check registered provider")
	}
	if _, err := o.providers.Register(t.Context(), missing[0], nil); err != nil {
		t.Fatal(err)
	}
	if err := o.checkProviders(t.Context(), providers); err == nil {
		t.Fatal("Registering was treated as ready")
	}
	if err := o.checkProviders(t.Context(), providers); err != nil {
		t.Fatal(err)
	}
	if e.providerReads != 6 || e.writes != 1 {
		t.Fatalf("reads=%d writes=%d", e.providerReads, e.writes)
	}
}

func TestExternalSubscriptionSDKMissingPrerequisiteBlocksWrites(t *testing.T) {
	e := newExternalSDKEndpoint(t)
	e.missingApplication = true
	o := &ReconcileExternalSubscriptionOptions{completedReconcileExternalSubscriptionOptions: &completedReconcileExternalSubscriptionOptions{SubscriptionID: externalTestSubscription, Apply: true, Out: io.Discard}}
	o.config.Clouds.Dev.Defaults.CI.Dev.Bot.ApplicationName = "bot"
	e.clients(t, o, &externalSDKCredential{})
	if err := o.reconcile(t.Context(), &externalSDKCredential{}, externalSelection{}); err == nil || !strings.Contains(err.Error(), "shared identity preflight") {
		t.Fatalf("missing prerequisite: %v", err)
	}
	if e.writes != 0 {
		t.Fatal("missing shared application caused mutation")
	}
}

func TestExternalSubscriptionEmbeddedRBACPrerequisite(t *testing.T) {
	for _, data := range []string{"", "null", "{}", "not JSON"} {
		artifacts := fstest.MapFS{"test-artifacts/generated-test-artifacts/external-subscription/ci-bot-rbac-subscription.json": {Data: []byte(data)}}
		if _, err := readExternalRBACTemplate(artifacts, "ci-bot-rbac-subscription"); err == nil {
			t.Fatalf("accepted missing/malformed template %q", data)
		}
	}
	if _, err := readExternalRBACTemplate(fstest.MapFS{}, "ci-bot-rbac-subscription"); err == nil {
		t.Fatal("missing embedded template accepted")
	}
}

// Parent-executable no-cloud smoke: go test ./cmd/aro-hcp-tests/slot-manager
// -run '^TestExternalSubscriptionSDKSmoke$' -count=1 (after Make generates artifacts).
func TestExternalSubscriptionSDKSmoke(t *testing.T) {
	for _, apply := range []bool{false, true} {
		t.Run(fmt.Sprintf("apply=%t", apply), func(t *testing.T) {
			e := newExternalSDKEndpoint(t)
			e.asyncPreview = true
			e.asyncApply = true
			credential := &externalSDKCredential{}
			state := &externalTestAssets{e2eReady: true, mockReady: true, credential: credential}
			o := &ReconcileExternalSubscriptionOptions{completedReconcileExternalSubscriptionOptions: &completedReconcileExternalSubscriptionOptions{SubscriptionID: externalTestSubscription, Apply: apply, Out: io.Discard, e2e: externalTestE2E{state}, mock: externalTestMock{state}}}
			o.config.Clouds.Dev.Defaults.CI.Providers = "Microsoft.Compute"
			dev := &o.config.Clouds.Dev.Defaults.CI.Dev
			dev.Bot.ApplicationName = "bot"
			dev.MockIdentities.FirstParty.ApplicationName = "fp"
			dev.MockIdentities.ARMHelper.ApplicationName = "arm"
			dev.MockIdentities.MSIMock.ApplicationName = "msi"
			e.clients(t, o, credential)
			if err := o.reconcile(t.Context(), credential, externalSelection{}); err != nil {
				t.Fatal(err)
			}
			wantValidation, wantWrites := "ProviderNoRbac", 0
			if apply {
				wantValidation, wantWrites = "Provider", 2
			}
			if e.writes != wantWrites {
				t.Fatalf("writes=%d, want %d", e.writes, wantWrites)
			}
			for _, validation := range e.validations {
				if validation != wantValidation {
					t.Fatalf("validation=%q want %q", validation, wantValidation)
				}
			}
			if e.previewPolls == 0 {
				t.Fatal("SDK did not poll the asynchronous what-if endpoint")
			}
			if apply && e.applyPolls != 2 {
				t.Fatalf("SDK did not poll both deployments: %d", e.applyPolls)
			}
			scopes := map[string]bool{}
			for _, scope := range credential.scopes {
				scopes[scope] = true
				if scope != "https://management.azure.com/.default" && scope != "https://graph.microsoft.com/.default" {
					t.Fatalf("unexpected credential scope %q", scope)
				}
			}
			if !scopes["https://management.azure.com/.default"] || !scopes["https://graph.microsoft.com/.default"] {
				t.Fatalf("missing ARM or Graph credential scope: %v", scopes)
			}
		})
	}
}

func TestExternalSubscriptionSDKProviderWaitBeforeApply(t *testing.T) {
	e := newExternalSDKEndpoint(t)
	e.missingProvider = true
	credential := &externalSDKCredential{}
	state := &externalTestAssets{e2eReady: true, mockReady: true, credential: credential}
	o := &ReconcileExternalSubscriptionOptions{completedReconcileExternalSubscriptionOptions: &completedReconcileExternalSubscriptionOptions{SubscriptionID: externalTestSubscription, Apply: true, Out: io.Discard, e2e: externalTestE2E{state}, mock: externalTestMock{state}}}
	o.config.Clouds.Dev.Defaults.CI.Providers = "Microsoft.Compute"
	dev := &o.config.Clouds.Dev.Defaults.CI.Dev
	dev.Bot.ApplicationName = "bot"
	dev.MockIdentities.FirstParty.ApplicationName = "fp"
	dev.MockIdentities.ARMHelper.ApplicationName = "arm"
	dev.MockIdentities.MSIMock.ApplicationName = "msi"
	e.clients(t, o, credential)
	if err := o.reconcile(t.Context(), credential, externalSelection{}); err != nil {
		t.Fatal(err)
	}
	if e.writes != 3 || e.providerReads < 6 || e.registering {
		t.Fatalf("registration did not wait for ready: writes=%d reads=%d registering=%t", e.writes, e.providerReads, e.registering)
	}
}

func TestExternalSubscriptionSDKDeploymentErrors(t *testing.T) {
	for _, test := range []struct {
		name, applyState string
		previewStatus    int
	}{
		{name: "HTTP preview authorization denied", previewStatus: http.StatusForbidden},
		{name: "failed deployment status", applyState: "Failed"},
	} {
		t.Run(test.name, func(t *testing.T) {
			e := newExternalSDKEndpoint(t)
			e.previewStatus = test.previewStatus
			o := &ReconcileExternalSubscriptionOptions{completedReconcileExternalSubscriptionOptions: &completedReconcileExternalSubscriptionOptions{SubscriptionID: externalTestSubscription, Apply: true}}
			e.clients(t, o, &externalSDKCredential{})
			if test.previewStatus != 0 {
				if err := o.previewDeployment(t.Context(), externalDeployment{name: "ci-bot-rbac-subscription"}, false); err == nil {
					t.Fatal("HTTP failure accepted")
				}
				if e.writes != 0 {
					t.Fatal("failed preview mutated Azure")
				}
				return
			}
			dev := &o.config.Clouds.Dev.Defaults.CI.Dev
			dev.Bot.ApplicationName = "bot"
			dev.MockIdentities.FirstParty.ApplicationName = "fp"
			dev.MockIdentities.ARMHelper.ApplicationName = "arm"
			dev.MockIdentities.MSIMock.ApplicationName = "msi"
			deployments, err := o.sharedDeployments(t.Context())
			if err != nil {
				t.Fatal(err)
			}
			e.applyState = test.applyState
			if err := o.applyDeployment(t.Context(), deployments[0]); err == nil {
				t.Fatal("failed deployment accepted")
			}
		})
	}
}

func (e *externalSDKEndpoint) assertParameters(path string, parameters map[string]any) {
	expected := map[string]any{
		"firstPartyPrincipalId": "55555555-5555-4555-8555-555555555555",
		"armHelperPrincipalId":  "55555555-5555-4555-8555-555555555555",
		"miMockPrincipalId":     "55555555-5555-4555-8555-555555555555",
	}
	if strings.Contains(path, "/external-ci-bot-rbac-subscription") {
		expected = map[string]any{
			"botPrincipalId":       "55555555-5555-4555-8555-555555555555",
			"isGlobalSubscription": false, "grantAksRbac": false, "grantKeyVaultAdmin": false,
		}
	}
	if len(parameters) != len(expected) {
		e.t.Errorf("unexpected parameter count: %v", parameters)
	}
	for name, want := range expected {
		value, ok := parameters[name].(map[string]any)
		if !ok || len(value) != 1 || value["value"] != want {
			e.t.Errorf("parameter %s=%#v, want typed value %#v", name, parameters[name], want)
		}
	}
}

func TestExternalSubscriptionSDKSharedNameNormalization(t *testing.T) {
	e := newExternalSDKEndpoint(t)
	o := &ReconcileExternalSubscriptionOptions{completedReconcileExternalSubscriptionOptions: &completedReconcileExternalSubscriptionOptions{SubscriptionID: externalTestSubscription}}
	e.clients(t, o, &externalSDKCredential{})
	if _, err := o.resolveSharedPrincipal(t.Context(), "shared app"); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(e.applications, []string{"shared-app", "shared-app"}) {
		t.Fatalf("shared name lookup=%v", e.applications)
	}
	if e.writes != 0 {
		t.Fatal("shared name lookup mutated Graph")
	}
}
