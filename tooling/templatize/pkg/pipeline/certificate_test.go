// Copyright 2025 Microsoft Corporation
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

package pipeline

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/go-logr/logr"
	"github.com/google/go-cmp/cmp"

	configtypes "github.com/Azure/ARO-Tools/config/types"
	"github.com/Azure/ARO-Tools/pipelines/types"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/policy"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/to"
	"github.com/Azure/azure-sdk-for-go/sdk/security/keyvault/azcertificates"
)

func TestEffectiveCertificatePolicy(t *testing.T) {
	const vault = "https://aro-hcp-dev-svc-kv.vault.azure.net"
	tests := []struct {
		name, cloud, environment, vault, certificate string
		manualRenewal                                bool
	}{
		{"frontend ci00", "dev", "ci00", vault, "frontend-cert-ci00-j1234567", true},
		{"admin ci01", "dev", "ci01", vault, "admin-api-cert-ci01-j1234567", true},
		{"sessiongate ci00", "dev", "ci00", vault, "sessiongate-cert-ci00-j1234567", true},
		{"maestro ci00", "dev", "ci00", vault, "maestro-server-j1234567", true},
		{"maestro ci01 trailing slash", "dev", "ci01", vault + "/", "maestro-server-j1234567", true},
		{"public", "public", "ci00", vault, "frontend-cert-ci00-j1234567", false},
		{"Azure cloud name", "Public", "ci00", vault, "maestro-server-j1234567", false},
		{"unknown cloud", "", "ci00", vault, "maestro-server-j1234567", false},
		{"personal", "dev", "pers", vault, "maestro-server-j1234567", false},
		{"shared dev", "dev", "dev", vault, "maestro-server-j1234567", false},
		{"cspr", "dev", "cspr", vault, "maestro-server-j1234567", false},
		{"legacy prow", "dev", "prow", vault, "maestro-server-j1234567", false},
		{"unknown environment", "dev", "", vault, "maestro-server-j1234567", false},
		{"other CI environment", "dev", "ci00", vault, "frontend-cert-ci01-j1234567", false},
		{"persistent regional", "dev", "ci00", vault, "frontend-cert-ci00-usw3", false},
		{"persistent self-signed", "dev", "ci00", vault, "firstPartyCert2", false},
		{"personal name", "dev", "ci00", vault, "frontend-cert-pers-j1234567", false},
		{"legacy name", "dev", "ci00", vault, "frontend-cert-prow-j1234567", false},
		{"unknown service", "dev", "ci00", vault, "other-cert-ci00-j1234567", false},
		{"maestro agent", "dev", "ci00", vault, "hcp-underlay-j1234567-mgmt-1", false},
		{"short job", "dev", "ci00", vault, "maestro-server-j123456", false},
		{"long job", "dev", "ci00", vault, "maestro-server-j12345678", false},
		{"non-digit job", "dev", "ci00", vault, "maestro-server-j123456a", false},
		{"suffix", "dev", "ci00", vault, "maestro-server-j1234567-extra", false},
		{"prefix", "dev", "ci00", vault, "other-maestro-server-j1234567", false},
		{"uppercase", "dev", "ci00", vault, "MAESTRO-server-j1234567", false},
		{"newline", "dev", "ci00", vault, "maestro-server-j1234567\n", false},
		{"other vault", "dev", "ci00", "https://other.vault.azure.net", "maestro-server-j1234567", false},
		{"http", "dev", "ci00", "http://aro-hcp-dev-svc-kv.vault.azure.net", "maestro-server-j1234567", false},
		{"host suffix", "dev", "ci00", vault + ".evil.example", "maestro-server-j1234567", false},
		{"userinfo", "dev", "ci00", "https://user@aro-hcp-dev-svc-kv.vault.azure.net", "maestro-server-j1234567", false},
		{"host as userinfo", "dev", "ci00", vault + "@evil.example", "maestro-server-j1234567", false},
		{"port", "dev", "ci00", vault + ":443", "maestro-server-j1234567", false},
		{"path", "dev", "ci00", vault + "/certificates", "maestro-server-j1234567", false},
		{"encoded path", "dev", "ci00", vault + "/%2f", "maestro-server-j1234567", false},
		{"query", "dev", "ci00", vault + "?foo=bar", "maestro-server-j1234567", false},
		{"empty query", "dev", "ci00", vault + "?", "maestro-server-j1234567", false},
		{"fragment", "dev", "ci00", vault + "#fragment", "maestro-server-j1234567", false},
		{"malformed URL", "dev", "ci00", "://%", "maestro-server-j1234567", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			options := &StepRunOptions{BaseRunOptions: BaseRunOptions{Cloud: tt.cloud}, Environment: tt.environment}
			got, transient := effectiveCertificatePolicy(options, tt.vault, tt.certificate, "test.com", "x-pkcs12", "test.com", "Self")
			if transient != tt.manualRenewal {
				t.Errorf("transient = %v, want %v", transient, tt.manualRenewal)
			}
			want := certificatePolicy("test.com", "x-pkcs12", "test.com", "Self")
			if tt.manualRenewal {
				want.LifetimeActions[0].Action.ActionType = to.Ptr(azcertificates.CertificatePolicyActionEmailContacts)
			}
			if diff := cmp.Diff(want, got); diff != "" {
				t.Errorf("effective policy mismatch (-want +got):\n%s", diff)
			}
			// Exercise the SDK serializer, not marshalCertPolicy's hand-built wire format.
			wire := mustMarshal(t, got)
			if !strings.Contains(wire, `"action_type":"`+string(*want.LifetimeActions[0].Action.ActionType)+`"`) ||
				!strings.Contains(wire, `"lifetime_percentage":50`) || !strings.Contains(wire, `"validity_months":6`) {
				t.Errorf("unexpected policy serialization: %s", wire)
			}
		})
	}
}

func TestReconcileCertificate(t *testing.T) {
	const vault = "https://aro-hcp-dev-svc-kv.vault.azure.net"
	const name = "frontend-cert-ci00-j1234567"
	const certPath = "/certificates/" + name
	tests := []struct {
		name          string
		cloud         string
		environment   string
		emailContacts bool
		mutate        func(*azcertificates.CertificatePolicy)
		getStatus     int
		updateStatus  int
		createStatus  int
		wantRequests  []string
		wantError     string
	}{
		{
			name:         "matching policy",
			wantRequests: []string{"GET " + certPath + "/", "GET " + certPath + "/", "PATCH " + certPath + "/"},
		},
		{
			name: "public matching policy", cloud: "public",
			wantRequests: []string{"GET " + certPath + "/", "GET " + certPath + "/", "PATCH " + certPath + "/"},
		},
		{
			name: "public lifetime mismatch recreates", cloud: "public",
			mutate:       func(p *azcertificates.CertificatePolicy) { p.LifetimeActions = nil },
			wantRequests: []string{"GET " + certPath + "/", "POST " + certPath + "/create", "GET " + certPath + "/", "PATCH " + certPath + "/"},
		},
		{
			name: "persistent lifetime mismatch recreates", environment: "pers",
			mutate:       func(p *azcertificates.CertificatePolicy) { p.LifetimeActions = nil },
			wantRequests: []string{"GET " + certPath + "/", "POST " + certPath + "/create", "GET " + certPath + "/", "PATCH " + certPath + "/"},
		},
		{
			name: "persistent EmailContacts lifetime mismatch recreates", environment: "pers", emailContacts: true,
			mutate:       func(p *azcertificates.CertificatePolicy) { p.LifetimeActions = nil },
			wantRequests: []string{"GET " + certPath + "/", "POST " + certPath + "/create", "GET " + certPath + "/", "PATCH " + certPath + "/"},
		},
		{
			name: "persistent recreate failure", environment: "pers", createStatus: http.StatusForbidden,
			mutate:       func(p *azcertificates.CertificatePolicy) { p.LifetimeActions = nil },
			wantRequests: []string{"GET " + certPath + "/", "POST " + certPath + "/create"}, wantError: "failed to create certificate",
		},
		{
			name: "renewal only updates policy",
			mutate: func(p *azcertificates.CertificatePolicy) {
				p.LifetimeActions = certificatePolicy("", "", "", "").LifetimeActions
			},
			wantRequests: []string{"GET " + certPath + "/", "PATCH " + certPath + "/policy", "GET " + certPath + "/", "PATCH " + certPath + "/"},
		},
		{
			name:         "omitted lifetime actions updates policy",
			mutate:       func(p *azcertificates.CertificatePolicy) { p.LifetimeActions = nil },
			wantRequests: []string{"GET " + certPath + "/", "PATCH " + certPath + "/policy", "GET " + certPath + "/", "PATCH " + certPath + "/"},
		},
		{
			name: "subject mismatch recreates",
			mutate: func(p *azcertificates.CertificatePolicy) {
				p.X509CertificateProperties.Subject = to.Ptr("CN=other.com")
			},
			wantRequests: []string{"GET " + certPath + "/", "POST " + certPath + "/create", "GET " + certPath + "/", "PATCH " + certPath + "/"},
		},
		{
			name: "renewal and key mismatch recreates",
			mutate: func(p *azcertificates.CertificatePolicy) {
				p.LifetimeActions = nil
				p.KeyProperties.KeySize = to.Ptr(int32(4096))
			},
			wantRequests: []string{"GET " + certPath + "/", "POST " + certPath + "/create", "GET " + certPath + "/", "PATCH " + certPath + "/"},
		},
		{
			name: "missing certificate creates", getStatus: http.StatusNotFound,
			wantRequests: []string{"GET " + certPath + "/", "POST " + certPath + "/create", "GET " + certPath + "/", "PATCH " + certPath + "/"},
		},
		{
			name: "read failure does not write", getStatus: http.StatusForbidden,
			wantRequests: []string{"GET " + certPath + "/"}, wantError: "failed to get existing certificate",
		},
		{
			name: "policy update failure does not create", updateStatus: http.StatusForbidden,
			mutate:       func(p *azcertificates.CertificatePolicy) { p.LifetimeActions = nil },
			wantRequests: []string{"GET " + certPath + "/", "PATCH " + certPath + "/policy"}, wantError: "failed to update certificate policy",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			options := &StepRunOptions{BaseRunOptions: BaseRunOptions{Cloud: "dev"}, Environment: "ci00"}
			if tt.cloud != "" {
				options.Cloud = tt.cloud
			}
			if tt.environment != "" {
				options.Environment = tt.environment
			}
			desired, transient := effectiveCertificatePolicy(options, vault, name, "test.com", "x-pkcs12", "test.com", "Self")
			if tt.emailContacts {
				// A future persistent EmailContacts policy must not imply CI ownership.
				desired.LifetimeActions[0].Action.ActionType = to.Ptr(azcertificates.CertificatePolicyActionEmailContacts)
			}
			var existing azcertificates.CertificatePolicy
			if err := json.Unmarshal([]byte(mustMarshal(t, desired)), &existing); err != nil {
				t.Fatal(err)
			}
			if tt.mutate != nil {
				tt.mutate(&existing)
			}
			var requests []string
			transport := &fakeTransport{interceptor: func(req *http.Request) (*http.Response, error) {
				// Key Vault first sends a bodyless request to obtain an auth challenge.
				if req.Header.Get("Authorization") == "" {
					return &http.Response{
						StatusCode: http.StatusUnauthorized,
						Header:     http.Header{"Www-Authenticate": {`Bearer authorization="https://login.microsoftonline.com/tenant", resource="https://vault.azure.net"`}},
						Body:       io.NopCloser(strings.NewReader("")), Request: req,
					}, nil
				}
				requests = append(requests, req.Method+" "+req.URL.Path)
				status, body := http.StatusOK, `{}`
				switch req.Method + " " + req.URL.Path {
				case "GET " + certPath + "/":
					body = `{"policy":` + mustMarshal(t, existing) + `,"x5t":"AQID","tags":{"existing":"preserved"}}`
					if len(requests) == 1 && tt.getStatus != 0 {
						status = tt.getStatus
					}
				case "PATCH " + certPath + "/policy", "POST " + certPath + "/create":
					var sent azcertificates.CertificatePolicy
					if req.Method == http.MethodPatch {
						if err := json.NewDecoder(req.Body).Decode(&sent); err != nil {
							t.Fatal(err)
						}
						if tt.updateStatus != 0 {
							status = tt.updateStatus
						}
					} else {
						var params azcertificates.CreateCertificateParameters
						if err := json.NewDecoder(req.Body).Decode(&params); err != nil {
							t.Fatal(err)
						}
						if params.CertificatePolicy == nil {
							t.Fatal("create request missing policy")
						}
						sent = *params.CertificatePolicy
						status, body = http.StatusAccepted, `{"status":"completed"}`
						if tt.createStatus != 0 {
							status = tt.createStatus
						}
					}
					if diff := cmp.Diff(desired, sent); diff != "" {
						t.Errorf("request policy mismatch (-want +got):\n%s", diff)
					}
				case "PATCH " + certPath + "/":
					var params azcertificates.UpdateCertificateParameters
					if err := json.NewDecoder(req.Body).Decode(&params); err != nil {
						t.Fatal(err)
					}
					want := map[string]*string{"existing": to.Ptr("preserved"), "thumbprint": to.Ptr("010203")}
					if diff := cmp.Diff(want, params.Tags); diff != "" {
						t.Errorf("thumbprint tags mismatch (-want +got):\n%s", diff)
					}
				default:
					t.Fatalf("unexpected request: %s %s", req.Method, req.URL)
				}
				return &http.Response{StatusCode: status, Header: http.Header{"Content-Type": {"application/json"}}, Body: io.NopCloser(strings.NewReader(body)), Request: req}, nil
			}}
			client, err := azcertificates.NewClient(vault, &fakeCredential{}, &azcertificates.ClientOptions{
				ClientOptions: azcore.ClientOptions{Transport: transport, Retry: policy.RetryOptions{MaxRetries: -1}},
			})
			if err != nil {
				t.Fatal(err)
			}
			err = reconcileCertificate(context.Background(), logr.Discard(), client, vault, name, desired, transient)
			if tt.wantError == "" && err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if tt.wantError != "" && (err == nil || !strings.Contains(err.Error(), tt.wantError)) {
				t.Errorf("error = %v, want %q", err, tt.wantError)
			}
			if diff := cmp.Diff(tt.wantRequests, requests); diff != "" {
				t.Errorf("requests mismatch (-want +got):\n%s", diff)
			}
		})
	}
}

func TestCertificatePolicy(t *testing.T) {
	// Golden pattern from sdp-pipelines — 100% parity
	policyPattern := `{"key_props":{"exportable":true,"kty":"RSA","key_size":2048},"secret_props":{"contentType":"application/%s"},"x509_props":{"subject":"CN=%s","sans":{"dns_names":[%s]},"validity_months":6},"lifetime_actions":[{"trigger":{"lifetime_percentage":50},"action":{"action_type":"AutoRenew"}}],"issuer":{"name":"%s"}}`

	tests := []struct {
		name        string
		commonName  string
		contentType string
		san         string
		issuer      string
		expected    string
	}{
		{
			name:        "matches sdp-pipelines policy",
			commonName:  "test.com",
			contentType: "x-pkcs12",
			san:         "test.com",
			issuer:      "Self",
			expected:    fmt.Sprintf(policyPattern, "x-pkcs12", "test.com", `"test.com"`, "Self"),
		},
		{
			name:        "OneCert issuer",
			commonName:  "admin.eastus.svc.aro.azure.com",
			contentType: "x-pkcs12",
			san:         "admin.eastus.svc.aro.azure.com",
			issuer:      "OneCertV2-PrivateCA",
			expected:    fmt.Sprintf(policyPattern, "x-pkcs12", "admin.eastus.svc.aro.azure.com", `"admin.eastus.svc.aro.azure.com"`, "OneCertV2-PrivateCA"),
		},
		{
			name:        "PKCS12 content type",
			commonName:  "rp.westus3.svc.aro.azure.com",
			contentType: "x-pkcs12",
			san:         "rp.westus3.svc.aro.azure.com",
			issuer:      "Self",
			expected:    fmt.Sprintf(policyPattern, "x-pkcs12", "rp.westus3.svc.aro.azure.com", `"rp.westus3.svc.aro.azure.com"`, "Self"),
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			policy := certificatePolicy(tt.commonName, tt.contentType, tt.san, tt.issuer)

			got, err := marshalCertPolicy(policy)
			if err != nil {
				t.Fatalf("failed to marshal policy: %v", err)
			}

			if diff := cmp.Diff(tt.expected, got); diff != "" {
				t.Errorf("certificatePolicy() mismatch (-expected +got):\n%s", diff)
			}
		})
	}
}

// marshalCertPolicy serializes a CertificatePolicy to JSON matching the Key Vault
// REST API wire format (snake_case field names).
func marshalCertPolicy(p azcertificates.CertificatePolicy) (string, error) {
	type keyProps struct {
		Exportable bool   `json:"exportable"`
		Kty        string `json:"kty"`
		KeySize    int32  `json:"key_size"`
	}
	type secretProps struct {
		ContentType string `json:"contentType"`
	}
	type sans struct {
		DNSNames []string `json:"dns_names"`
	}
	type x509Props struct {
		Subject        string `json:"subject"`
		SANs           sans   `json:"sans"`
		ValidityMonths int32  `json:"validity_months"`
	}
	type trigger struct {
		LifetimePercentage int32 `json:"lifetime_percentage"`
	}
	type action struct {
		ActionType string `json:"action_type"`
	}
	type lifetimeAction struct {
		Trigger trigger `json:"trigger"`
		Action  action  `json:"action"`
	}
	type issuer struct {
		Name string `json:"name"`
	}
	type certPolicyJSON struct {
		KeyProps        keyProps         `json:"key_props"`
		SecretProps     secretProps      `json:"secret_props"`
		X509Props       x509Props        `json:"x509_props"`
		LifetimeActions []lifetimeAction `json:"lifetime_actions"`
		Issuer          issuer           `json:"issuer"`
	}

	dnsNames := make([]string, len(p.X509CertificateProperties.SubjectAlternativeNames.DNSNames))
	for i, n := range p.X509CertificateProperties.SubjectAlternativeNames.DNSNames {
		dnsNames[i] = *n
	}

	var actions []lifetimeAction
	for _, la := range p.LifetimeActions {
		actions = append(actions, lifetimeAction{
			Trigger: trigger{LifetimePercentage: *la.Trigger.LifetimePercentage},
			Action:  action{ActionType: string(*la.Action.ActionType)},
		})
	}

	out := certPolicyJSON{
		KeyProps: keyProps{
			Exportable: *p.KeyProperties.Exportable,
			Kty:        string(*p.KeyProperties.KeyType),
			KeySize:    *p.KeyProperties.KeySize,
		},
		SecretProps: secretProps{
			ContentType: *p.SecretProperties.ContentType,
		},
		X509Props: x509Props{
			Subject: *p.X509CertificateProperties.Subject,
			SANs: sans{
				DNSNames: dnsNames,
			},
			ValidityMonths: *p.X509CertificateProperties.ValidityInMonths,
		},
		LifetimeActions: actions,
		Issuer: issuer{
			Name: *p.IssuerParameters.Name,
		},
	}

	data, err := json.Marshal(out)
	if err != nil {
		return "", err
	}
	return string(data), nil
}

func TestResolveValue(t *testing.T) {
	cfg := configtypes.Configuration{
		"vault": map[string]any{
			"url": "https://test.vault.azure.net",
		},
		"simple": "simple-value",
	}

	outputs := Outputs{
		"svc-group": {
			"regional": {
				"deploy": ArmOutput(map[string]any{
					"kvUrl": map[string]any{
						"type":  "string",
						"value": "https://output.vault.azure.net",
					},
				}),
			},
		},
	}

	tests := []struct {
		name         string
		value        types.Value
		serviceGroup string
		expected     string
		expectErr    bool
	}{
		{
			name:     "direct value",
			value:    types.Value{Value: "direct-val"},
			expected: "direct-val",
		},
		{
			name:     "config ref simple",
			value:    types.Value{ConfigRef: "simple"},
			expected: "simple-value",
		},
		{
			name:     "config ref nested",
			value:    types.Value{ConfigRef: "vault.url"},
			expected: "https://test.vault.azure.net",
		},
		{
			name: "input chaining",
			value: types.Value{
				Input: &types.Input{
					StepDependency: types.StepDependency{
						ResourceGroup: "regional",
						Step:          "deploy",
					},
					Name: "kvUrl",
				},
			},
			serviceGroup: "svc-group",
			expected:     "https://output.vault.azure.net",
		},
		{
			name:      "empty value",
			value:     types.Value{},
			expectErr: true,
		},
		{
			name:      "missing config ref",
			value:     types.Value{ConfigRef: "nonexistent.path"},
			expectErr: true,
		},
		{
			name: "missing input step",
			value: types.Value{
				Input: &types.Input{
					StepDependency: types.StepDependency{
						ResourceGroup: "regional",
						Step:          "missing",
					},
					Name: "kvUrl",
				},
			},
			serviceGroup: "svc-group",
			expectErr:    true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := resolveValue(tt.value, cfg, outputs, tt.serviceGroup)
			if tt.expectErr {
				if err == nil {
					t.Fatalf("expected error, got nil")
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got != tt.expected {
				t.Errorf("resolveValue() = %q, want %q", got, tt.expected)
			}
		})
	}
}

func TestPolicyMatches(t *testing.T) {
	baseline := certificatePolicy("test.com", "x-pkcs12", "test.com", "Self")

	tests := []struct {
		name     string
		existing *azcertificates.CertificatePolicy
		desired  *azcertificates.CertificatePolicy
		expected bool
	}{
		{
			name:     "identical policies match",
			existing: &baseline,
			desired:  ptrPolicy(certificatePolicy("test.com", "x-pkcs12", "test.com", "Self")),
			expected: true,
		},
		{
			name:     "different issuer",
			existing: &baseline,
			desired:  ptrPolicy(certificatePolicy("test.com", "x-pkcs12", "test.com", "OneCertV2-PrivateCA")),
			expected: false,
		},
		{
			name:     "different subject",
			existing: &baseline,
			desired:  ptrPolicy(certificatePolicy("other.com", "x-pkcs12", "test.com", "Self")),
			expected: false,
		},
		{
			name:     "different SAN",
			existing: &baseline,
			desired:  ptrPolicy(certificatePolicy("test.com", "x-pkcs12", "other.com", "Self")),
			expected: false,
		},
		{
			name:     "different content type",
			existing: &baseline,
			desired:  ptrPolicy(certificatePolicy("test.com", "x-pem-bundle", "test.com", "Self")),
			expected: false,
		},
		{
			name:     "nil existing",
			existing: nil,
			desired:  ptrPolicy(certificatePolicy("test.com", "x-pkcs12", "test.com", "Self")),
			expected: false,
		},
		{
			name:     "nil desired",
			existing: &baseline,
			desired:  nil,
			expected: false,
		},
		{
			name:     "different key size",
			existing: &baseline,
			desired: func() *azcertificates.CertificatePolicy {
				p := certificatePolicy("test.com", "x-pkcs12", "test.com", "Self")
				p.KeyProperties.KeySize = to.Ptr(int32(4096))
				return &p
			}(),
			expected: false,
		},
		{
			name:     "different validity months",
			existing: &baseline,
			desired: func() *azcertificates.CertificatePolicy {
				p := certificatePolicy("test.com", "x-pkcs12", "test.com", "Self")
				p.X509CertificateProperties.ValidityInMonths = to.Ptr(int32(12))
				return &p
			}(),
			expected: false,
		},
		{
			name: "both SANs nil",
			existing: &azcertificates.CertificatePolicy{
				IssuerParameters:          &azcertificates.IssuerParameters{Name: to.Ptr("Self")},
				X509CertificateProperties: &azcertificates.X509CertificateProperties{Subject: to.Ptr("CN=test.com"), ValidityInMonths: to.Ptr(int32(6))},
				SecretProperties:          &azcertificates.SecretProperties{ContentType: to.Ptr("application/x-pkcs12")},
				KeyProperties:             &azcertificates.KeyProperties{KeySize: to.Ptr(int32(2048)), KeyType: to.Ptr(azcertificates.KeyTypeRSA), Exportable: to.Ptr(true)},
			},
			desired: &azcertificates.CertificatePolicy{
				IssuerParameters:          &azcertificates.IssuerParameters{Name: to.Ptr("Self")},
				X509CertificateProperties: &azcertificates.X509CertificateProperties{Subject: to.Ptr("CN=test.com"), ValidityInMonths: to.Ptr(int32(6))},
				SecretProperties:          &azcertificates.SecretProperties{ContentType: to.Ptr("application/x-pkcs12")},
				KeyProperties:             &azcertificates.KeyProperties{KeySize: to.Ptr(int32(2048)), KeyType: to.Ptr(azcertificates.KeyTypeRSA), Exportable: to.Ptr(true)},
			},
			expected: true,
		},
		{
			name: "both SANs nil still checks key properties",
			existing: func() *azcertificates.CertificatePolicy {
				p := certificatePolicy("test.com", "x-pkcs12", "test.com", "Self")
				p.X509CertificateProperties.SubjectAlternativeNames = nil
				p.KeyProperties.KeySize = to.Ptr(int32(4096))
				return &p
			}(),
			desired: func() *azcertificates.CertificatePolicy {
				p := certificatePolicy("test.com", "x-pkcs12", "test.com", "Self")
				p.X509CertificateProperties.SubjectAlternativeNames = nil
				return &p
			}(),
			expected: false,
		},
		{
			name:     "existing has SANs, desired nil",
			existing: &baseline,
			desired: &azcertificates.CertificatePolicy{
				IssuerParameters:          &azcertificates.IssuerParameters{Name: to.Ptr("Self")},
				X509CertificateProperties: &azcertificates.X509CertificateProperties{Subject: to.Ptr("CN=test.com"), ValidityInMonths: to.Ptr(int32(6))},
				SecretProperties:          &azcertificates.SecretProperties{ContentType: to.Ptr("application/x-pkcs12")},
				KeyProperties:             &azcertificates.KeyProperties{KeySize: to.Ptr(int32(2048)), KeyType: to.Ptr(azcertificates.KeyTypeRSA), Exportable: to.Ptr(true)},
			},
			expected: false,
		},
		{
			name: "desired has SANs, existing nil",
			existing: &azcertificates.CertificatePolicy{
				IssuerParameters:          &azcertificates.IssuerParameters{Name: to.Ptr("Self")},
				X509CertificateProperties: &azcertificates.X509CertificateProperties{Subject: to.Ptr("CN=test.com"), ValidityInMonths: to.Ptr(int32(6))},
				SecretProperties:          &azcertificates.SecretProperties{ContentType: to.Ptr("application/x-pkcs12")},
				KeyProperties:             &azcertificates.KeyProperties{KeySize: to.Ptr(int32(2048)), KeyType: to.Ptr(azcertificates.KeyTypeRSA), Exportable: to.Ptr(true)},
			},
			desired:  &baseline,
			expected: false,
		},
		{
			name:     "different lifetime action trigger percentage",
			existing: &baseline,
			desired: func() *azcertificates.CertificatePolicy {
				p := certificatePolicy("test.com", "x-pkcs12", "test.com", "Self")
				p.LifetimeActions[0].Trigger.LifetimePercentage = to.Ptr(int32(80))
				return &p
			}(),
			expected: false,
		},
		{
			name:     "different lifetime action type",
			existing: &baseline,
			desired: func() *azcertificates.CertificatePolicy {
				p := certificatePolicy("test.com", "x-pkcs12", "test.com", "Self")
				p.LifetimeActions[0].Action.ActionType = to.Ptr(azcertificates.CertificatePolicyActionEmailContacts)
				return &p
			}(),
			expected: false,
		},
		{
			name: "different lifetime trigger type days vs percentage",
			existing: func() *azcertificates.CertificatePolicy {
				p := certificatePolicy("test.com", "x-pkcs12", "test.com", "Self")
				p.LifetimeActions[0].Trigger = &azcertificates.LifetimeActionTrigger{
					DaysBeforeExpiry: to.Ptr(int32(30)),
				}
				return &p
			}(),
			desired:  &baseline,
			expected: false,
		},
		{
			name:     "different lifetime action count",
			existing: &baseline,
			desired: func() *azcertificates.CertificatePolicy {
				p := certificatePolicy("test.com", "x-pkcs12", "test.com", "Self")
				p.LifetimeActions = append(p.LifetimeActions, &azcertificates.LifetimeAction{
					Trigger: &azcertificates.LifetimeActionTrigger{DaysBeforeExpiry: to.Ptr(int32(30))},
					Action:  &azcertificates.LifetimeActionType{ActionType: to.Ptr(azcertificates.CertificatePolicyActionEmailContacts)},
				})
				return &p
			}(),
			expected: false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := policyMatches(tt.existing, tt.desired)
			if got != tt.expected {
				t.Errorf("policyMatches() = %v, want %v", got, tt.expected)
			}
		})
	}
}

func ptrPolicy(p azcertificates.CertificatePolicy) *azcertificates.CertificatePolicy {
	return &p
}

func TestShouldSkipCertificateStep(t *testing.T) {
	cfg := configtypes.Configuration{
		"cert": map[string]any{
			"manage":        types.CertificateManageDisabled,
			"manageEnabled": types.CertificateManageEnabled,
		},
	}
	outputs := Outputs{}

	tests := []struct {
		name       string
		manage     *types.Value
		expectSkip bool
		expectErr  string
	}{
		{
			name:       "nil does not skip",
			manage:     nil,
			expectSkip: false,
		},
		{
			name:       "Enabled does not skip",
			manage:     &types.Value{Value: types.CertificateManageEnabled},
			expectSkip: false,
		},
		{
			name:       "Disabled skips",
			manage:     &types.Value{Value: types.CertificateManageDisabled},
			expectSkip: true,
		},
		{
			name:      "invalid value errors",
			manage:    &types.Value{Value: "bogus"},
			expectErr: `manage field must be "Enabled" or "Disabled", got "bogus"`,
		},
		{
			name:       "config ref Disabled skips",
			manage:     &types.Value{ConfigRef: "cert.manage"},
			expectSkip: true,
		},
		{
			name:       "config ref Enabled does not skip",
			manage:     &types.Value{ConfigRef: "cert.manageEnabled"},
			expectSkip: false,
		},
		{
			name:      "resolve error",
			manage:    &types.Value{ConfigRef: "nonexistent.path"},
			expectErr: "not found",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			skip, err := shouldSkipCertificateStep(tt.manage, cfg, outputs, "test-svc")

			if len(tt.expectErr) > 0 && err == nil {
				t.Fatal("expected error, got nil")
			}
			if len(tt.expectErr) > 0 && !strings.Contains(err.Error(), tt.expectErr) {
				t.Errorf("error %q does not contain %q", err.Error(), tt.expectErr)
			}
			if len(tt.expectErr) == 0 && err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if len(tt.expectErr) == 0 && skip != tt.expectSkip {
				t.Errorf("shouldSkipCertificateStep() = %v, want %v", skip, tt.expectSkip)
			}
		})
	}
}
