package frontend

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/google/uuid"

	"github.com/Azure/ARO-HCP/internal/api/arm"
)

// Copyright (c) Microsoft Corporation.
// Licensed under the Apache License 2.0.

func TestRequestIDPropagator(t *testing.T) {
	const testRequestID = "00000000-0000-0000-0000-000000000000"

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(r.Header.Get(clusterServiceRequestIDHeader)))
	}))
	defer ts.Close()

	do := func(c *http.Client) string {
		t.Helper()

		ctx := context.Background()
		r, err := http.NewRequestWithContext(ctx, http.MethodGet, ts.URL, nil)
		if err != nil {
			t.Fatalf("unexpected error: %s", err)
		}
		correlationData := arm.NewCorrelationData(r)
		correlationData.RequestID = uuid.MustParse(testRequestID)
		r = r.WithContext(ContextWithCorrelationData(ctx, correlationData))

		rs, err := c.Do(r)
		if err != nil {
			t.Fatalf("unexpected error from server: %s", err)
		}

		if rs.StatusCode != http.StatusOK {
			t.Fatalf("unexpected status code: %d", rs.StatusCode)
		}

		b, err := io.ReadAll(rs.Body)
		if err != nil {
			t.Fatalf("unexpected error reading response: %s", err)
		}

		return string(b)
	}

	// Without the transport wrapper, the request ID isn't echoed.
	c := ts.Client()
	if ret := do(c); ret != "" {
		t.Fatalf("expecting an empty response, got %q", ret)
	}

	// With the transport wrapper, the request ID is echoed.
	c.Transport = RequestIDPropagator(c.Transport)
	if ret := do(c); ret != testRequestID {
		t.Fatalf("expecting %q, got %q", testRequestID, ret)
	}
}
