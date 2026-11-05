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

package kusto

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/Azure/azure-kusto-go/azkustodata"
	"github.com/Azure/azure-kusto-go/azkustodata/kql"
)

func TestQueryCancelsStalledSDKMetadata(t *testing.T) {
	for _, api := range []string{"materialized", "streaming"} {
		for _, mode := range []string{"cancel", "deadline"} {
			t.Run(api+"/"+mode, func(t *testing.T) {
				started := make(chan struct{})
				stopped := make(chan struct{})
				release := make(chan struct{})
				var once sync.Once
				server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					if r.URL.Path != "/v1/rest/auth/metadata" {
						t.Errorf("query reached %s while metadata was stalled", r.URL.Path)
						w.WriteHeader(http.StatusBadRequest)
						return
					}
					once.Do(func() { close(started) })
					select {
					case <-r.Context().Done():
						close(stopped)
					case <-release:
						w.WriteHeader(http.StatusNotFound)
					}
				}))
				defer server.Close()
				defer close(release)
				sdk, err := azkustodata.New(azkustodata.NewConnectionStringBuilder(server.URL))
				if err != nil {
					t.Fatal(err)
				}
				defer sdk.Close()
				client := &Client{kustoClient: sdk, QueryTimeout: 10 * time.Second}
				execute := func(ctx context.Context) error {
					if api == "streaming" {
						_, err := client.ExecutePreconfiguredQuery(ctx, &templateQuery{database: "db", query: kql.New("print 1")}, make(chan TaggedRow))
						return err
					}
					_, err := client.Query(ctx, "db", kql.New("print 1"))
					return err
				}
				ctx, cancel := context.WithCancel(context.Background())
				defer cancel()
				if mode == "deadline" {
					ctx, cancel = context.WithTimeout(ctx, 200*time.Millisecond)
					defer cancel()
				}
				done := make(chan error, 1)
				go func() {
					done <- execute(ctx)
				}()
				select {
				case <-started:
				case <-time.After(5 * time.Second):
					t.Fatal("SDK did not request metadata")
				}
				if mode != "deadline" {
					cancel()
				}
				select {
				case err := <-done:
					if err == nil || ctx.Err() == nil {
						t.Errorf("query returned without a cancellation error: %v", err)
					}
				case <-time.After(2 * time.Second):
					t.Error("query ignored cancellation while metadata was stalled")
				}
				select {
				case <-stopped:
				case <-time.After(2 * time.Second):
					t.Error("metadata HTTP request was not canceled")
				}
			})
		}
	}
}
