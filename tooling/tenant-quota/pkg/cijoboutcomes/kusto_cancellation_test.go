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

package cijoboutcomes

import (
	"context"
	"io"
	"net/http"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/Azure/ARO-HCP/tooling/tenant-quota/pkg/kusto"
)

func TestOneShotCancelsSDKMetadataRequest(t *testing.T) {
	w := newTestWriter(t)
	w.config.CIJobOutcomes.ClusterURI = "https://oneshot-cancellation.kusto.windows.net"
	w.initializeReadOnly = func(ctx context.Context) error { return w.initializeKustoWithCredential(ctx, false, nil) }
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	entered, exited := make(chan struct{}), make(chan struct{})
	w.client.Transport = ingestionTransport(func(r *http.Request) (*http.Response, error) {
		close(entered)
		defer close(exited)
		<-r.Context().Done()
		return nil, r.Context().Err()
	})
	done := make(chan error, 1)
	go func() { done <- w.RunOnce(ctx, OneShotOptions{JobURIs: []JobURI{testJobURI}}, io.Discard) }()
	select {
	case <-entered:
	case <-time.After(time.Second):
		t.Fatal("metadata request did not start")
	}
	cancel()
	select {
	case err := <-done:
		require.ErrorIs(t, err, context.Canceled)
	case <-time.After(time.Second):
		t.Fatal("one-shot worker did not join after metadata cancellation")
	}
	<-exited
	_, wrapped := w.client.Transport.(kusto.OwnerLifetimeTransport)
	require.False(t, wrapped, "artifact client must not be replaced by the Kusto-only wrapper")
}
