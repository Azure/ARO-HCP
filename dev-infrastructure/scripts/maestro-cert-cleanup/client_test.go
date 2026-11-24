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

package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/policy"
	"github.com/Azure/azure-sdk-for-go/sdk/security/keyvault/azcertificates"
)

type fakeCred struct{}

func (fakeCred) GetToken(context.Context, policy.TokenRequestOptions) (azcore.AccessToken, error) {
	return azcore.AccessToken{Token: "x"}, nil
}

func TestListCertificatesFollowsPages(t *testing.T) {
	var srv *httptest.Server
	srv = httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/certificates" {
			http.NotFound(w, r)
			return
		}
		var body map[string]any
		if r.URL.Query().Get("$skiptoken") == "p2" {
			body = map[string]any{"value": []any{map[string]any{"id": srv.URL + "/certificates/hcp-underlay-usw3-mgmt-2"}}}
		} else {
			body = map[string]any{
				"value":    []any{map[string]any{"id": srv.URL + "/certificates/maestro-server-usw3"}},
				"nextLink": srv.URL + "/certificates?$skiptoken=p2",
			}
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(body)
	}))
	defer srv.Close()

	client, err := azcertificates.NewClient(srv.URL, fakeCred{}, &azcertificates.ClientOptions{
		ClientOptions: azcore.ClientOptions{Transport: srv.Client()},
	})
	if err != nil {
		t.Fatalf("client: %v", err)
	}
	certs, err := listCertificates(context.Background(), client)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	got := fmt.Sprint(len(certs))
	if got != "2" {
		t.Fatalf("expected both pages, got %d certs: %+v", len(certs), certs)
	}
	if certs[0].Name != "maestro-server-usw3" || certs[1].Name != "hcp-underlay-usw3-mgmt-2" {
		t.Fatalf("unexpected names: %+v", certs)
	}
}
