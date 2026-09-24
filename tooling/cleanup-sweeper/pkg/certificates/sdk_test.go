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

package certificates

import (
	"fmt"
	"io"
	"net/http"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/security/keyvault/azcertificates"
)

type transportFunc func(*http.Request) (*http.Response, error)

func (f transportFunc) Do(r *http.Request) (*http.Response, error) { return f(r) }

// Exercise real SDK paging, latest GET, soft-delete, and deleted-certificate purge HTTP paths without Azure.
func TestSDKHTTP(t *testing.T) {
	for _, dryRun := range []bool{true, false} {
		t.Run(fmt.Sprintf("dryRun=%t", dryRun), func(t *testing.T) {
			var calls []string
			name := "maestro-server-j1234567"
			deletedName := "maestro-server-j2345678"
			client, err := azcertificates.NewClient(VaultURL, nil, &azcertificates.ClientOptions{ClientOptions: azcore.ClientOptions{Transport: transportFunc(func(r *http.Request) (*http.Response, error) {
				calls = append(calls, r.Method+" "+r.URL.Path)
				if r.URL.Host != "aro-hcp-dev-svc-kv.vault.azure.net" {
					t.Fatalf("unexpected host %s", r.URL.Host)
				}
				var body string
				switch {
				case r.Method == http.MethodGet && r.URL.Path == "/certificates":
					if r.URL.Query().Get("page") == "2" {
						body = `{"value":[]}`
					} else {
						body = fmt.Sprintf(`{"value":[{"id":%q,"attributes":{"created":%d,"updated":%d}}],"nextLink":%q}`, VaultURL+"/certificates/"+name, referenceTime.Add(-30*24*time.Hour).Unix(), referenceTime.Add(-8*24*time.Hour).Unix(), VaultURL+"/certificates?page=2")
					}
				case r.Method == http.MethodGet && r.URL.Path == "/certificates/"+name+"/":
					body = fmt.Sprintf(`{"id":%q,"attributes":{"created":%d,"updated":%d}}`, VaultURL+"/certificates/"+name+"/version1", referenceTime.Add(-30*24*time.Hour).Unix(), referenceTime.Add(-8*24*time.Hour).Unix())
				case r.Method == http.MethodGet && r.URL.Path == "/deletedcertificates":
					if r.URL.Query().Get("page") == "2" {
						body = `{"value":[]}`
					} else {
						body = fmt.Sprintf(`{"value":[{"id":%q,"recoveryId":%q,"attributes":{"recoveryLevel":"Recoverable+Purgeable"},"deletedDate":%d,"scheduledPurgeDate":%d}],"nextLink":%q}`,
							VaultURL+"/certificates/"+deletedName+"/version1",
							VaultURL+"/deletedcertificates/"+deletedName,
							referenceTime.Add(-time.Hour).Unix(),
							referenceTime.Add(89*24*time.Hour).Unix(),
							VaultURL+"/deletedcertificates?page=2")
					}
				case r.Method == http.MethodGet && r.URL.Path == "/deletedcertificates/"+deletedName:
					body = fmt.Sprintf(`{"id":%q,"recoveryId":%q,"attributes":{"recoveryLevel":"Recoverable+Purgeable"},"deletedDate":%d,"scheduledPurgeDate":%d}`,
						VaultURL+"/certificates/"+deletedName+"/version1",
						VaultURL+"/deletedcertificates/"+deletedName,
						referenceTime.Add(-time.Hour).Unix(),
						referenceTime.Add(89*24*time.Hour).Unix())
				case r.Method == http.MethodGet && r.URL.Path == "/deletedcertificates/"+name:
					body = fmt.Sprintf(`{"id":%q,"recoveryId":%q,"attributes":{"recoveryLevel":"Recoverable+Purgeable"},"deletedDate":%d,"scheduledPurgeDate":%d}`,
						VaultURL+"/certificates/"+name+"/version1",
						VaultURL+"/deletedcertificates/"+name,
						referenceTime.Unix(),
						referenceTime.Add(90*24*time.Hour).Unix())
				case r.Method == http.MethodDelete && r.URL.Path == "/certificates/"+name:
					body = `{}`
				case r.Method == http.MethodDelete && (r.URL.Path == "/deletedcertificates/"+deletedName || r.URL.Path == "/deletedcertificates/"+name):
					body = `{}`
				default:
					t.Fatalf("unexpected request %s %s", r.Method, r.URL)
				}
				return &http.Response{StatusCode: http.StatusOK, Header: http.Header{"Content-Type": []string{"application/json"}}, Body: io.NopCloser(strings.NewReader(body)), Request: r}, nil
			})}})
			if err != nil {
				t.Fatal(err)
			}
			s, _, _, _ := newTestSweeper()
			s.certificates = client
			if err := s.run(t.Context(), options(dryRun)); err != nil {
				t.Fatal(err)
			}
			want := []string{"GET /certificates", "GET /certificates", "GET /deletedcertificates", "GET /deletedcertificates"}
			if !dryRun {
				want = append(want,
					"GET /deletedcertificates/"+deletedName,
					"DELETE /deletedcertificates/"+deletedName,
					"GET /certificates/"+name+"/",
					"DELETE /certificates/"+name,
					"GET /deletedcertificates/"+name,
					"GET /deletedcertificates/"+name,
					"DELETE /deletedcertificates/"+name,
				)
			}
			if !reflect.DeepEqual(calls, want) {
				t.Fatalf("calls = %v, want %v", calls, want)
			}
		})
	}
}
