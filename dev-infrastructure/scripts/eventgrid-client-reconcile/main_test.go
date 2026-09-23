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
	"errors"
	"net/http"
	"os"
	"testing"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/eventgrid/armeventgrid/v2"
)

type fakeClients struct {
	getResp   armeventgrid.ClientsClientGetResponse
	getErr    error
	deleteErr error
	deleted   int
}

func (f *fakeClients) Get(context.Context, string, string, string, *armeventgrid.ClientsClientGetOptions) (armeventgrid.ClientsClientGetResponse, error) {
	return f.getResp, f.getErr
}

func (f *fakeClients) Delete(context.Context, string, string, string) error {
	if f.deleteErr != nil {
		return f.deleteErr
	}
	f.deleted++
	return nil
}

func clientWithAuthName(name string) armeventgrid.ClientsClientGetResponse {
	return armeventgrid.ClientsClientGetResponse{
		Client: armeventgrid.Client{
			Properties: &armeventgrid.ClientProperties{AuthenticationName: &name},
		},
	}
}

func responseError(status int) error {
	return &azcore.ResponseError{StatusCode: status}
}

func TestReconcile(t *testing.T) {
	cfg := config{
		resourceGroup:      "rg",
		namespaceName:      "ns",
		clientName:         "client",
		authenticationName: "want.example.com",
	}

	for _, tc := range []struct {
		name        string
		clients     *fakeClients
		wantDeleted int
		wantErr     bool
	}{
		{
			name:    "absent client is left to the ARM deployment",
			clients: &fakeClients{getErr: responseError(http.StatusNotFound)},
		},
		{
			name:    "matching authenticationName is a no-op",
			clients: &fakeClients{getResp: clientWithAuthName("want.example.com")},
		},
		{
			name:        "drifted authenticationName is deleted",
			clients:     &fakeClients{getResp: clientWithAuthName("stale.example.com")},
			wantDeleted: 1,
		},
		{
			// A permissions failure must never be mistaken for an absent
			// client: that would silently skip reconciliation.
			name:    "forbidden read is fatal",
			clients: &fakeClients{getErr: responseError(http.StatusForbidden)},
			wantErr: true,
		},
		{
			name:    "non-response error is fatal",
			clients: &fakeClients{getErr: errors.New("dial tcp: connection refused")},
			wantErr: true,
		},
		{
			name:    "delete failure is fatal",
			clients: &fakeClients{getResp: clientWithAuthName("stale.example.com"), deleteErr: errors.New("conflict")},
			wantErr: true,
		},
		{
			name:        "missing properties is treated as drift",
			clients:     &fakeClients{getResp: armeventgrid.ClientsClientGetResponse{}},
			wantDeleted: 1,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := reconcile(context.Background(), tc.clients, cfg, os.Stdout)
			if tc.wantErr && err == nil {
				t.Fatal("expected an error, got nil")
			}
			if !tc.wantErr && err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if tc.clients.deleted != tc.wantDeleted {
				t.Errorf("deleted = %d, want %d", tc.clients.deleted, tc.wantDeleted)
			}
		})
	}
}

func TestLoadConfig(t *testing.T) {
	const nsID = "/subscriptions/sub1/resourceGroups/rg1/providers/Microsoft.EventGrid/namespaces/ns1"

	for _, tc := range []struct {
		name    string
		env     map[string]string
		want    config
		wantErr bool
	}{
		{
			name: "namespace resource ID is parsed into its parts",
			env: map[string]string{
				"ClientName": "c", "AuthenticationName": "a", "EventGridNamespaceId": nsID,
			},
			want: config{subscriptionID: "sub1", resourceGroup: "rg1", namespaceName: "ns1", clientName: "c", authenticationName: "a"},
		},
		{
			name: "separate parts are accepted",
			env: map[string]string{
				"ClientName": "c", "AuthenticationName": "a",
				"EventGridSubscriptionId": "sub2", "EventGridResourceGroup": "rg2", "EventGridNamespaceName": "ns2",
			},
			want: config{subscriptionID: "sub2", resourceGroup: "rg2", namespaceName: "ns2", clientName: "c", authenticationName: "a"},
		},
		{
			name:    "malformed resource ID is rejected",
			env:     map[string]string{"ClientName": "c", "AuthenticationName": "a", "EventGridNamespaceId": "not-an-id"},
			wantErr: true,
		},
		{
			name: "resource ID of the wrong type is rejected",
			env: map[string]string{
				"ClientName": "c", "AuthenticationName": "a",
				"EventGridNamespaceId": "/subscriptions/sub1/resourceGroups/rg1/providers/Microsoft.Storage/storageAccounts/sa1",
			},
			wantErr: true,
		},
		{
			name:    "missing ClientName is rejected",
			env:     map[string]string{"AuthenticationName": "a", "EventGridNamespaceId": nsID},
			wantErr: true,
		},
		{
			name:    "missing AuthenticationName is rejected",
			env:     map[string]string{"ClientName": "c", "EventGridNamespaceId": nsID},
			wantErr: true,
		},
		{
			name:    "incomplete parts are rejected",
			env:     map[string]string{"ClientName": "c", "AuthenticationName": "a", "EventGridSubscriptionId": "sub2"},
			wantErr: true,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := loadConfig(func(k string) string { return tc.env[k] })
			if tc.wantErr {
				if err == nil {
					t.Fatal("expected an error, got nil")
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got != tc.want {
				t.Errorf("got %+v, want %+v", got, tc.want)
			}
		})
	}
}
