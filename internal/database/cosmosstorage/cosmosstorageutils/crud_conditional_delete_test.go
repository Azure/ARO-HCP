// Copyright 2026 Microsoft Corporation
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://www.apache.org/licenses/LICENSE-2.0
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package cosmosstorageutils

import (
	"context"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/policy"
	"github.com/Azure/azure-sdk-for-go/sdk/data/azcosmos"

	"github.com/Azure/ARO-HCP/internal/api/fleetapi"
	"github.com/Azure/ARO-HCP/internal/apihelpers/fleetapihelpers"
)

type deleteTestCredential struct{}

func (deleteTestCredential) GetToken(context.Context, policy.TokenRequestOptions) (azcore.AccessToken, error) {
	return azcore.AccessToken{Token: "test"}, nil
}

func TestConditionalDeletePreservesObservedETag(t *testing.T) {
	for _, conditional := range []bool{false, true} {
		t.Run(map[bool]string{false: "default", true: "observed-owner"}[conditional], func(t *testing.T) {
			writes := 0
			observed := azcore.ETag("observed-owner-etag")
			wantETag := "current-etag"
			if conditional {
				wantETag = string(observed)
			}
			client, err := azcosmos.NewClient("https://cosmos.test", deleteTestCredential{}, &azcosmos.ClientOptions{ClientOptions: azcore.ClientOptions{
				Retry: policy.RetryOptions{MaxRetries: -1},
				Transport: layerTestTransport(func(req *http.Request) (*http.Response, error) {
					response := &http.Response{StatusCode: http.StatusOK, Header: http.Header{}, Request: req}
					switch req.Method {
					case http.MethodGet:
						response.Header.Set("ETag", "current-etag")
						response.Body = io.NopCloser(strings.NewReader(`{"id":"claim","resourceID":"/providers/Microsoft.RedHatOpenShift/dnsZones/example.com/dnsClaims/abcde","content":{"cosmosMetadata":{"instanceVersion":1}}}`))
					case http.MethodPut:
						writes++
						require.Equal(t, wantETag, req.Header.Get("If-Match"))
						response.Body = io.NopCloser(strings.NewReader(`{}`))
					default:
						t.Fatalf("unexpected method %s", req.Method)
					}
					return response, nil
				}),
			}})
			require.NoError(t, err)
			database, err := client.NewDatabase("db")
			require.NoError(t, err)
			container, err := database.NewContainer("Fleet")
			require.NoError(t, err)
			id, err := fleetapihelpers.DNSClaimResourceID("abcde.example.com")
			require.NoError(t, err)
			crud := NewCosmosResourceCRUDWithStrategies[fleetapi.DNSClaim, *fleetapi.DNSClaim, GenericDocument[fleetapi.DNSClaim]](container, id.Parent, fleetapi.DNSClaimResourceType, FleetPartitionKeyDeriver{}, FleetResourceIDBuilder{})
			if conditional {
				err = crud.Delete(t.Context(), "abcde", &azcosmos.ItemOptions{IfMatchEtag: &observed})
			} else {
				err = crud.Delete(t.Context(), "abcde")
			}
			require.NoError(t, err)
			require.Equal(t, 1, writes)
		})
	}
}
