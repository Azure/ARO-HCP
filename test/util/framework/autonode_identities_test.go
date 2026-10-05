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
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	azcorearm "github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/policy"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/msi/armmsi"
)

func TestCleanupAutoNodeIdentityFICs(t *testing.T) {
	for _, tt := range []struct {
		name     string
		status   int
		optional bool
		wantErr  bool
	}{
		{name: "present optional identity", status: http.StatusOK, optional: true},
		{name: "absent optional identity", status: http.StatusNotFound, optional: true},
		{name: "absent required identity", status: http.StatusNotFound, wantErr: true},
		{name: "optional identity access denied", status: http.StatusForbidden, optional: true, wantErr: true},
	} {
		t.Run(tt.name, func(t *testing.T) {
			deletes := 0
			transport := &fakeTransport{do: func(req *http.Request) (*http.Response, error) {
				status := tt.status
				body := `{"error":{"code":"ResourceNotFound","message":"identity not found"}}`
				if req.Method == http.MethodDelete {
					deletes++
					status = http.StatusNoContent
					body = ""
				} else if status == http.StatusOK {
					body = `{"value":[{"name":"autonode-fic"}]}`
				}
				return &http.Response{StatusCode: status, Header: http.Header{"Content-Type": []string{"application/json"}}, Body: io.NopCloser(strings.NewReader(body)), Request: req}, nil
			}}
			client, err := armmsi.NewFederatedIdentityCredentialsClient("00000000-0000-0000-0000-000000000000", stubTokenCredential{token: "test"}, &azcorearm.ClientOptions{
				ClientOptions: policy.ClientOptions{Transport: transport, Retry: policy.RetryOptions{MaxRetries: -1}},
			})
			require.NoError(t, err)
			err = cleanupIdentityFICs(context.Background(), client, "leased-container", DpAutoNodeMiName, tt.optional)
			if tt.wantErr {
				require.Error(t, err)
			} else {
				require.NoError(t, err)
			}
			if tt.status == http.StatusOK {
				require.Equal(t, 1, deletes, "the AutoNode federated credential must be deleted")
			} else {
				require.Zero(t, deletes)
			}
		})
	}
}

func TestAutoNodeIdentityRemainsOptional(t *testing.T) {
	identities := NewDefaultIdentities()
	require.Equal(t, DpAutoNodeMiName, identities.DpAutoNodeMiName)
	require.NotContains(t, identities.ToSlice(), DpAutoNodeMiName)
	require.Contains(t, OptionalIdentityNames(), DpAutoNodeMiName)
}
