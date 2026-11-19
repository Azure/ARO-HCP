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

package mockidentities

import (
	"context"
	"crypto/sha1"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"net/http"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/security/keyvault/azcertificates"

	"github.com/Azure/ARO-HCP/tooling/entra-app-credentials/pkg/pinning"
)

func preparationCertificateAPI(t *testing.T, name string, status int, body string) *azcertificates.Client {
	t.Helper()
	transport := ownerTransport(func(request *http.Request) (*http.Response, error) {
		if request.Method != http.MethodGet || strings.TrimSuffix(request.URL.Path, "/") != "/certificates/"+name {
			t.Fatalf("identity preparation unexpectedly mutated Key Vault: %s %s", request.Method, request.URL)
		}
		return &http.Response{StatusCode: status, Header: http.Header{"Content-Type": {"application/json"}}, Body: io.NopCloser(strings.NewReader(body)), Request: request}, nil
	})
	client, err := azcertificates.NewClient(VaultURL, testCredential{}, &azcertificates.ClientOptions{ClientOptions: azcore.ClientOptions{Transport: transport}})
	if err != nil {
		t.Fatal(err)
	}
	return client
}

func TestInitialPreparationPreflightRejectsUnsafeStatesWithoutWrites(t *testing.T) {
	name := "aro-hcp-slot-preparation-msi"
	applicationID, clientID := uuid.NewString(), uuid.NewString()
	ownerID := uuid.NewString()
	applicationPath := "applications(uniqueName='" + name + "')"
	principalPath := "servicePrincipals(appId='" + clientID + "')"
	application := fmt.Sprintf(`{"id":%q,"appId":%q,"displayName":%q,"keyCredentials":[]}`, applicationID, clientID, name)
	missing := `{"error":{"code":"Request_ResourceNotFound","message":"missing initial object"}}`
	lookup := graphExchange{method: "GET", path: applicationPath, response: application, query: map[string]string{"$select": "id,appId,displayName,keyCredentials"}}
	missingApplication := graphExchange{method: "GET", path: applicationPath, response: missing, status: http.StatusNotFound}
	matches := graphExchange{method: "GET", path: "applications", response: fmt.Sprintf(`{"value":[{"id":%q}]}`, applicationID)}
	missingPrincipal := graphExchange{method: "GET", path: principalPath, response: missing, status: http.StatusNotFound}
	emptyOwners := graphExchange{method: "GET", path: "applications/" + applicationID + "/owners", response: `{"value":[]}`}
	for _, test := range []struct {
		name        string
		exchanges   []graphExchange
		certificate int
		initialize  bool
		wantError   string
	}{
		{name: "new identity", exchanges: []graphExchange{missingApplication, {method: "GET", path: "applications", response: `{"value":[]}`}}, certificate: 404, initialize: true},
		{name: "absent application with missing collision collection", exchanges: []graphExchange{missingApplication, {method: "GET", path: "applications", response: `{}`}}, certificate: 404, wantError: "no collection"},
		{name: "absent application with null collision collection", exchanges: []graphExchange{missingApplication, {method: "GET", path: "applications", response: `{"value":null}`}}, certificate: 404, wantError: "no collection"},
		{name: "partial initial application", exchanges: []graphExchange{lookup, matches, missingPrincipal, emptyOwners}, certificate: 404, initialize: true},
		{name: "existing owner is preserved", exchanges: []graphExchange{lookup, matches, missingPrincipal, {method: "GET", path: "applications/" + applicationID + "/owners", response: fmt.Sprintf(`{"value":[{"id":%q}]}`, uuid.NewString())}}, certificate: 404, wantError: "never removes owners"},
		{name: "orphaned certificate", exchanges: []graphExchange{missingApplication}, certificate: 200, wantError: "certificate exists without its application"},
		{name: "duplicate display name on later page", exchanges: []graphExchange{missingApplication,
			{method: "GET", path: "applications", response: `{"value":[],"@odata.nextLink":"https://graph.microsoft.com/v1.0/applications?$skiptoken=later"}`},
			{method: "GET", path: "applications", response: fmt.Sprintf(`{"value":[{"id":%q}]}`, uuid.NewString())}}, certificate: 404, wantError: "another uniqueName"},
		{name: "Key Vault read denied", exchanges: []graphExchange{lookup}, certificate: 403, wantError: "read certificate"},
	} {
		t.Run(test.name, func(t *testing.T) {
			body := `{"error":{"code":"CertificateNotFound","message":"missing initial certificate"}}`
			if test.certificate == 200 {
				body = `{"attributes":{"enabled":true}}`
			}
			store := &azureStore{graph: graphTestClient(t, test.exchanges), certificateAPI: preparationCertificateAPI(t, name, test.certificate, body)}
			initialize, err := store.PreflightIdentity(t.Context(), name, []string{ownerID})
			if test.wantError != "" {
				if err == nil || !strings.Contains(err.Error(), test.wantError) {
					t.Fatalf("initial preparation error = %v, want %q", err, test.wantError)
				}
				return
			}
			if err != nil || initialize != test.initialize {
				t.Fatalf("initialization allowed = %t, error %v", initialize, err)
			}
		})
	}
}

func TestExplicitPreparationNeverRepinsOrReplacesHealthyOwners(t *testing.T) {
	certificate, validity := publicTestCertificate(t)
	thumbprint := sha1.Sum(certificate)
	name := "aro-hcp-slot-preparation-backend"
	applicationID, clientID, principalID, ownerID := uuid.NewString(), uuid.NewString(), uuid.NewString(), uuid.NewString()
	applicationPath := "applications(uniqueName='" + name + "')"
	principalPath := "servicePrincipals(appId='" + clientID + "')"
	principal := fmt.Sprintf(`{"id":%q,"appId":%q,"accountEnabled":true}`, principalID, clientID)
	for _, test := range []struct {
		name, pin, owner, wantError string
	}{
		{name: "healthy identity needs no writes", pin: base64.StdEncoding.EncodeToString(thumbprint[:]), owner: ownerID},
		{name: "changed certificate cannot be repinned", pin: base64.StdEncoding.EncodeToString([]byte("different pin")), owner: ownerID, wantError: "thumbprint is not pinned"},
		{name: "owners cannot be replaced", pin: base64.StdEncoding.EncodeToString(thumbprint[:]), owner: uuid.NewString(), wantError: "owners differ"},
	} {
		t.Run(test.name, func(t *testing.T) {
			keys := fmt.Sprintf(`[{"customKeyIdentifier":%q,"type":"AsymmetricX509Cert","usage":"Verify","startDateTime":%q,"endDateTime":%q}]`, test.pin, validity.NotBefore.Format(time.RFC3339), validity.NotAfter.Format(time.RFC3339))
			application := fmt.Sprintf(`{"id":%q,"appId":%q,"displayName":%q,"keyCredentials":%s}`, applicationID, clientID, name, keys)
			lookup := graphExchange{method: "GET", path: applicationPath, response: application}
			matches := graphExchange{method: "GET", path: "applications", response: fmt.Sprintf(`{"value":[{"id":%q}]}`, applicationID)}
			servicePrincipal := graphExchange{method: "GET", path: principalPath, response: principal}
			exchanges := []graphExchange{lookup, lookup, matches, servicePrincipal, {method: "GET", path: applicationPath, response: fmt.Sprintf(`{"keyCredentials":%s}`, keys), query: map[string]string{"$select": "keyCredentials"}}}
			if !strings.Contains(test.wantError, "thumbprint") {
				exchanges = append(exchanges, lookup, servicePrincipal,
					graphExchange{method: "GET", path: "applications/" + applicationID + "/owners", response: fmt.Sprintf(`{"value":[{"id":%q}]}`, test.owner)},
					graphExchange{method: "GET", path: "servicePrincipals/" + principalID + "/owners", response: fmt.Sprintf(`{"value":[{"id":%q}]}`, ownerID)})
			}
			if test.wantError == "" {
				exchanges = append(exchanges, lookup, matches, servicePrincipal)
			}
			body := fmt.Sprintf(`{"cer":%q,"attributes":{"enabled":true}}`, base64.StdEncoding.EncodeToString(certificate))
			store := &azureStore{graph: graphTestClient(t, exchanges), certificateAPI: preparationCertificateAPI(t, name, 200, body)}
			resolved, err := store.PrepareIdentity(t.Context(), name, []string{ownerID})
			if test.wantError != "" {
				if err == nil || !strings.Contains(err.Error(), test.wantError) || !strings.Contains(err.Error(), "drain affected slots") {
					t.Fatalf("unsafe preparation error = %v", err)
				}
				return
			}
			if err != nil || resolved.ClientID != clientID || resolved.PrincipalID != principalID {
				t.Fatalf("existing identity changed: %+v, error %v", resolved, err)
			}
		})
	}
}

func TestInitialOwnersNeverRemoveAndPreflightEveryObject(t *testing.T) {
	for _, unexpected := range []bool{false, true} {
		t.Run(map[bool]string{false: "add only", true: "reject later object before writes"}[unexpected], func(t *testing.T) {
			desired := []string{uuid.NewString(), uuid.NewString()}
			current := [][]string{{desired[0]}, {}}
			if unexpected {
				current[1] = []string{uuid.NewString()}
			}
			writes := 0
			plans := make([]ownerPlan, 0, len(current))
			for index := range current {
				plans = append(plans, ownerPlan{name: fmt.Sprint(index), relations: ownerRelations{
					list: func(context.Context) ([]string, error) { return slices.Clone(current[index]), nil },
					add: func(_ context.Context, id string) error {
						writes++
						current[index] = append(current[index], id)
						return nil
					},
					remove: func(context.Context, string) error {
						t.Fatal("initial preparation removed an owner")
						return errors.New("owner removal forbidden")
					},
				}})
			}
			err := initializeOwnerSets(t.Context(), plans, desired, true)
			if unexpected {
				if err == nil || writes != 0 {
					t.Fatalf("unexpected owners did not prevent all writes: %v, writes %d", err, writes)
				}
				return
			}
			if err != nil || writes != 3 {
				t.Fatalf("initial owner additions = %d, error %v", writes, err)
			}
			if err := initializeOwnerSets(t.Context(), plans, desired, true); err != nil || writes != 3 {
				t.Fatalf("repeat initial owner preparation wrote: %d, error %v", writes, err)
			}
		})
	}
}

type initialPinApplications struct {
	application  pinning.Application
	replacements int
}

func (client *initialPinApplications) FindApplication(context.Context, string) (*pinning.Application, error) {
	result := client.application
	return &result, nil
}

func (client *initialPinApplications) GetApplication(context.Context, string) (*pinning.Application, error) {
	result := client.application
	return &result, nil
}

func (client *initialPinApplications) ReplaceKeyCredentials(_ context.Context, _, _ string, certificate []byte) error {
	client.replacements++
	thumbprint := sha1.Sum(certificate)
	client.application.KeyCredentials = []pinning.KeyCredential{{CustomKeyIdentifier: thumbprint[:], Type: "AsymmetricX509Cert", Usage: "Verify"}}
	return nil
}

type initialPinCertificates struct {
	certificate pinning.Certificate
	creates     int
}

func (client *initialPinCertificates) GetCertificate(context.Context, string) (pinning.Certificate, error) {
	return client.certificate, nil
}

func (client *initialPinCertificates) CreateCertificate(context.Context, string, string, []byte) ([]byte, error) {
	client.creates++
	return nil, errors.New("retry must reuse the existing certificate")
}

func TestInterruptedInitialPinReusesExistingCertificate(t *testing.T) {
	certificate, validity := publicTestCertificate(t)
	name := "aro-hcp-slot-retry-msi"
	applicationID, clientID, principalID, ownerID := uuid.NewString(), uuid.NewString(), uuid.NewString(), uuid.NewString()
	applicationPath := "applications(uniqueName='" + name + "')"
	lookup := graphExchange{method: "GET", path: applicationPath, response: fmt.Sprintf(`{"id":%q,"appId":%q,"displayName":%q,"keyCredentials":[]}`, applicationID, clientID, name)}
	matches := graphExchange{method: "GET", path: "applications", response: fmt.Sprintf(`{"value":[{"id":%q}]}`, applicationID)}
	principal := graphExchange{method: "GET", path: "servicePrincipals(appId='" + clientID + "')", response: fmt.Sprintf(`{"id":%q,"appId":%q,"accountEnabled":true}`, principalID, clientID)}
	applicationOwners := graphExchange{method: "GET", path: "applications/" + applicationID + "/owners", response: fmt.Sprintf(`{"value":[{"id":%q}]}`, ownerID)}
	principalOwners := graphExchange{method: "GET", path: "servicePrincipals/" + principalID + "/owners", response: fmt.Sprintf(`{"value":[{"id":%q}]}`, ownerID)}
	preflight := []graphExchange{lookup, matches, principal, applicationOwners, principalOwners}
	exchanges := slices.Clone(preflight)
	exchanges = append(exchanges, lookup, matches, principal)
	exchanges = append(exchanges, preflight...)
	exchanges = append(exchanges, lookup, applicationOwners, principalOwners, applicationOwners, principalOwners)
	exchanges = append(exchanges, preflight...)
	thumbprint := sha1.Sum(certificate)
	keys := fmt.Sprintf(`{"keyCredentials":[{"customKeyIdentifier":%q,"type":"AsymmetricX509Cert","usage":"Verify","startDateTime":%q,"endDateTime":%q}]}`, base64.StdEncoding.EncodeToString(thumbprint[:]), validity.NotBefore.Format(time.RFC3339), validity.NotAfter.Format(time.RFC3339))
	exchanges = append(exchanges, graphExchange{method: "GET", path: applicationPath, response: keys, query: map[string]string{"$select": "keyCredentials"}})
	applications := &initialPinApplications{application: pinning.Application{ID: applicationID, AppID: clientID, DisplayName: name}}
	certificates := &initialPinCertificates{certificate: pinning.Certificate{CER: certificate, NotAfter: validity.NotAfter}}
	store := &azureStore{
		graph:          graphTestClient(t, exchanges),
		certificateAPI: preparationCertificateAPI(t, name, 200, fmt.Sprintf(`{"cer":%q,"attributes":{"enabled":true}}`, base64.StdEncoding.EncodeToString(certificate))),
		applications:   applications,
		certificates:   certificates,
	}
	result, err := store.PrepareIdentity(t.Context(), name, []string{ownerID})
	if err != nil || result.ClientID != clientID || result.PrincipalID != principalID {
		t.Fatalf("interrupted initial preparation retry = %+v, error %v", result, err)
	}
	if applications.replacements != 1 || certificates.creates != 0 {
		t.Fatalf("initial retry replaced existing material: initial pin count %d, new certificates %d", applications.replacements, certificates.creates)
	}
}

func TestInitialPinGuardRejectsCredentialsAppearingAfterPreflight(t *testing.T) {
	name, applicationID, clientID := "aro-hcp-slot-guard-msi", uuid.NewString(), uuid.NewString()
	application := pinning.Application{ID: applicationID, AppID: clientID, DisplayName: name}
	for _, test := range []struct {
		name   string
		mutate func(*pinning.Application)
	}{
		{name: "existing key", mutate: func(application *pinning.Application) {
			application.KeyCredentials = []pinning.KeyCredential{{Type: "AsymmetricX509Cert"}}
		}},
		{name: "replaced application", mutate: func(application *pinning.Application) { application.ID = uuid.NewString() }},
	} {
		t.Run(test.name, func(t *testing.T) {
			underlying := &initialPinApplications{application: application}
			client := initialApplicationClient{ApplicationClient: underlying, applicationID: applicationID, clientID: clientID, name: name}
			if _, err := client.FindApplication(t.Context(), name); err != nil {
				t.Fatal(err)
			}
			test.mutate(&underlying.application)
			if err := client.ReplaceKeyCredentials(t.Context(), applicationID, name, []byte("new certificate")); err == nil || underlying.replacements != 0 {
				t.Fatalf("initial pin guard permitted disruptive replacement: error %v, writes %d", err, underlying.replacements)
			}
		})
	}
}

func TestInterruptedInitialPinRequiresValidCertificateAndExactOwners(t *testing.T) {
	certificate, _ := publicTestCertificate(t)
	name := "aro-hcp-slot-initial-owner-msi"
	applicationID, clientID, principalID, ownerID := uuid.NewString(), uuid.NewString(), uuid.NewString(), uuid.NewString()
	for _, test := range []struct {
		name, certificate, owners, wantError string
		enabled                              bool
	}{
		{name: "missing initial owner", certificate: base64.StdEncoding.EncodeToString(certificate), enabled: true, owners: `{"value":[]}`, wantError: "owners differ"},
		{name: "different initial owner", certificate: base64.StdEncoding.EncodeToString(certificate), enabled: true, owners: fmt.Sprintf(`{"value":[{"id":%q}]}`, uuid.NewString()), wantError: "owners differ"},
		{name: "disabled initial certificate", certificate: base64.StdEncoding.EncodeToString(certificate), owners: "", wantError: "disabled"},
		{name: "invalid initial certificate", certificate: base64.StdEncoding.EncodeToString([]byte("not a certificate")), enabled: true, owners: "", wantError: "invalid public certificate"},
	} {
		t.Run(test.name, func(t *testing.T) {
			exchanges := []graphExchange{
				{method: "GET", path: "applications(uniqueName='" + name + "')", response: fmt.Sprintf(`{"id":%q,"appId":%q,"displayName":%q,"keyCredentials":[]}`, applicationID, clientID, name)},
				{method: "GET", path: "applications", response: fmt.Sprintf(`{"value":[{"id":%q}]}`, applicationID)},
				{method: "GET", path: "servicePrincipals(appId='" + clientID + "')", response: fmt.Sprintf(`{"id":%q,"appId":%q,"accountEnabled":true}`, principalID, clientID)},
			}
			if test.owners != "" {
				exchanges = append(exchanges,
					graphExchange{method: "GET", path: "applications/" + applicationID + "/owners", response: test.owners},
					graphExchange{method: "GET", path: "servicePrincipals/" + principalID + "/owners", response: fmt.Sprintf(`{"value":[{"id":%q}]}`, ownerID)})
			}
			store := &azureStore{
				graph:          graphTestClient(t, exchanges),
				certificateAPI: preparationCertificateAPI(t, name, 200, fmt.Sprintf(`{"cer":%q,"attributes":{"enabled":%t}}`, test.certificate, test.enabled)),
			}
			if _, err := store.PrepareIdentity(t.Context(), name, []string{ownerID}); err == nil || !strings.Contains(err.Error(), test.wantError) {
				t.Fatalf("unsafe interrupted pin was accepted: %v", err)
			}
		})
	}
}
