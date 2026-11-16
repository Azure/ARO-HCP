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
	"io"
	"net/http"
)

// OwnerLifetimeTransport binds SDK background requests to their owner's lifetime
// through response body consumption, without changing the caller's HTTP client.
type OwnerLifetimeTransport struct {
	Lifetime context.Context
	Base     http.RoundTripper
}

func (t OwnerLifetimeTransport) CloseIdleConnections() {
	if closer, ok := t.Base.(interface{ CloseIdleConnections() }); ok {
		closer.CloseIdleConnections()
	}
}

func (t OwnerLifetimeTransport) RoundTrip(request *http.Request) (*http.Response, error) {
	ctx, cancel := context.WithCancel(request.Context())
	stop := context.AfterFunc(t.Lifetime, cancel)
	cleanup := func() { stop(); cancel() }
	keep := false
	defer func() {
		if !keep {
			cleanup()
		}
	}()
	if t.Lifetime.Err() != nil {
		cancel()
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	response, err := t.Base.RoundTrip(request.Clone(ctx))
	if err != nil {
		return response, err
	}
	if response.Body != nil {
		response.Body = &lifetimeBody{ReadCloser: response.Body, cleanup: cleanup}
		keep = true
	}
	return response, nil
}

type lifetimeBody struct {
	io.ReadCloser
	cleanup func()
}

func (b *lifetimeBody) Read(p []byte) (int, error) {
	n, err := b.ReadCloser.Read(p)
	if err != nil {
		b.cleanup()
	}
	return n, err
}

func (b *lifetimeBody) Close() error {
	defer b.cleanup()
	return b.ReadCloser.Close()
}
