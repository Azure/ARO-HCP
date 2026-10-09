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
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/Azure/azure-kusto-go/azkustodata"
	"github.com/Azure/azure-kusto-go/azkustoingest"
)

func TestKustoLifetimeTransportCancellation(t *testing.T) {
	for _, callerCancels := range []bool{true, false} {
		lifetime, cancelLifetime := context.WithCancel(t.Context())
		defer cancelLifetime()
		caller, cancelCaller := context.WithCancel(t.Context())
		defer cancelCaller()
		entered := make(chan struct{})
		transport := lifetimeTransport{lifetime: lifetime, base: ingestionTransport(func(r *http.Request) (*http.Response, error) {
			close(entered)
			<-r.Context().Done()
			return nil, r.Context().Err()
		})}
		request, err := http.NewRequestWithContext(caller, http.MethodGet, "https://kusto.invalid", nil)
		require.NoError(t, err)
		done := make(chan error, 1)
		go func() { _, err := transport.RoundTrip(request); done <- err }()
		<-entered
		if callerCancels {
			cancelCaller()
		} else {
			cancelLifetime()
		}
		select {
		case err := <-done:
			require.ErrorIs(t, err, context.Canceled)
		case <-time.After(time.Second):
			t.Fatal("request goroutine did not exit on cancellation")
		}
	}
}

func TestKustoAuthMetadataCancellation(t *testing.T) {
	for _, stallBody := range []bool{false, true} {
		lifetime, cancel := context.WithCancel(t.Context())
		entered, exited := make(chan struct{}), make(chan struct{})
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			defer close(exited)
			if stallBody {
				w.WriteHeader(http.StatusOK)
				w.(http.Flusher).Flush()
			}
			close(entered)
			<-r.Context().Done()
		}))
		client := server.Client()
		client.Transport = lifetimeTransport{lifetime: lifetime, base: http.DefaultTransport}
		done := make(chan error, 1)
		go func() {
			_, err := azkustodata.GetMetadata(server.URL, client)
			done <- err
		}()
		select {
		case <-entered:
		case <-time.After(time.Second):
			cancel()
			server.Close()
			t.Fatal("SDK metadata request did not start")
		}
		cancel()
		select {
		case err := <-done:
			require.Error(t, err)
		case <-time.After(time.Second):
			t.Error("SDK auth metadata call did not exit")
		}
		server.Close()
		<-exited
	}
}

func TestKustoResourceRequestCancellation(t *testing.T) {
	lifetime, cancel := context.WithCancel(t.Context())
	defer cancel()
	entered, exited := make(chan struct{}), make(chan struct{})
	client := &http.Client{Transport: lifetimeTransport{lifetime: lifetime, base: ingestionTransport(func(r *http.Request) (*http.Response, error) {
		if r.Method == http.MethodGet {
			return &http.Response{StatusCode: http.StatusNotFound, Body: http.NoBody}, nil
		}
		var request struct{ CSL string }
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			return nil, err
		}
		switch request.CSL {
		case ".get ingestion resources":
			close(entered)
			<-r.Context().Done()
			close(exited)
			return nil, r.Context().Err()
		case ".get kusto identity token":
			body := `{"Tables":[{"TableName":"Table","Columns":[{"ColumnName":"AuthorizationContext","ColumnType":"string"}],"Rows":[["test"]]}]}`
			return &http.Response{StatusCode: http.StatusOK, Header: http.Header{"Content-Type": []string{"application/json"}}, Body: io.NopCloser(strings.NewReader(body))}, nil
		default:
			return nil, fmt.Errorf("unexpected request: %s", request.CSL)
		}
	})}}
	ingestor, err := azkustoingest.New(azkustodata.NewConnectionStringBuilder("http://localhost"), azkustoingest.WithHttpClient(client), azkustoingest.WithDefaultDatabase("ServiceLogs"), azkustoingest.WithDefaultTable("ciJobOutcomes"))
	require.NoError(t, err)
	done := make(chan error, 1)
	go func() {
		_, err := ingestor.FromReader(lifetime, bytes.NewBufferString("{}\n"), azkustoingest.FileFormat(azkustoingest.MultiJSON))
		done <- err
	}()
	select {
	case <-entered:
	case <-time.After(time.Second):
		cancel()
		_ = ingestor.Close()
		t.Fatal("resource request did not start")
	}
	cancel()
	select {
	case <-exited:
	case <-time.After(time.Second):
		t.Error("background SDK resource HTTP request was not cancelled")
	}
	require.NoError(t, ingestor.Close())
	// The SDK's unconditional retry sleep is not interruptible. Join the call
	// rather than hiding that limitation behind a leaked cancellation goroutine.
	select {
	case err := <-done:
		require.Error(t, err)
	case <-time.After(15 * time.Second):
		t.Fatal("ingestion call did not exit after SDK retry sleep")
	}
}

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
	go func() { done <- w.RunOnce(ctx, OneShotOptions{BuildIDs: []BuildID{"123"}}, io.Discard) }()
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
	_, wrapped := w.client.Transport.(lifetimeTransport)
	require.False(t, wrapped, "artifact client must not be replaced by the Kusto-only wrapper")
}

func TestKustoLifetimeTransportKeepsBodyReadableAndCleansUp(t *testing.T) {
	lifetime, cancel := context.WithCancel(t.Context())
	defer cancel()
	var forwarded context.Context
	transport := lifetimeTransport{lifetime: lifetime, base: ingestionTransport(func(r *http.Request) (*http.Response, error) {
		forwarded = r.Context()
		return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader("response"))}, nil
	})}
	request, err := http.NewRequest(http.MethodGet, "https://kusto.invalid", nil)
	require.NoError(t, err)
	response, err := transport.RoundTrip(request)
	require.NoError(t, err)
	require.NoError(t, forwarded.Err(), "headers must not cancel response body reads")
	body, err := io.ReadAll(response.Body)
	require.NoError(t, err)
	require.Equal(t, "response", string(body))
	require.ErrorIs(t, forwarded.Err(), context.Canceled, "EOF must release the combined context")
	require.NoError(t, response.Body.Close())
	cancel()
	_, err = transport.RoundTrip(request)
	require.ErrorIs(t, err, context.Canceled, "already cancelled lifetime must reject new SDK retries")
}
