package frontend

// Copyright (c) Microsoft Corporation.
// Licensed under the Apache License 2.0.

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/Azure/ARO-HCP/internal/api/arm"
)

func TestMiddlewareBody(t *testing.T) {
	tests := []struct {
		name    string
		methods []string
		header  http.Header
		body    []byte
		wantErr string
	}{
		{
			name:    "GET request - valid",
			methods: []string{http.MethodGet},
		},
		{
			name:    "large body",
			methods: []string{http.MethodPatch, http.MethodPost, http.MethodPut},
			body:    bytes.Repeat([]byte{0}, int(5*megabyte)),
			wantErr: "400: InvalidResource: The resource definition is invalid.",
		},
		{
			name:    "invalid media type",
			methods: []string{http.MethodPatch, http.MethodPost, http.MethodPut},
			header: http.Header{
				"Content-Type": []string{"invalid"},
			},
			wantErr: "415: UnsupportedMediaType: The content media type 'invalid' is not supported. Only 'application/json' is supported.",
		},
		{
			name:    "empty media type allowed with empty body",
			methods: []string{http.MethodPatch, http.MethodPost, http.MethodPut},
		},
		{
			name:    "empty media type not allowed with non-empty body",
			methods: []string{http.MethodPatch, http.MethodPost, http.MethodPut},
			body:    []byte("body"),
			wantErr: "415: UnsupportedMediaType: The content media type '' is not supported. Only 'application/json' is supported.",
		},
		{
			name:    "valid media type allowed with empty body",
			methods: []string{http.MethodPatch, http.MethodPost, http.MethodPut},
			header: http.Header{
				"Content-Type": []string{"application/json"},
			},
		},
		{
			name:    "valid media type allowed with non-empty body",
			methods: []string{http.MethodPatch, http.MethodPost, http.MethodPut},
			header: http.Header{
				"Content-Type": []string{"application/json"},
			},
			body: []byte("body"),
		},
		{
			name:    "upper-case valid media type allowed with non-empty body",
			methods: []string{http.MethodPatch, http.MethodPost, http.MethodPut},
			header: http.Header{
				"Content-Type": []string{"APPLICATION/JSON"},
			},
			body: []byte("body"),
		},
	}

	for _, tt := range tests {
		for _, method := range tt.methods {
			t.Run(tt.name+"/"+method, func(t *testing.T) {
				writer := httptest.NewRecorder()

				request, err := http.NewRequest(method, "", bytes.NewReader(tt.body))
				assert.NoError(t, err)
				request.Header = tt.header

				next := func(w http.ResponseWriter, r *http.Request) {
					request = r // capture modified request
					w.WriteHeader(http.StatusOK)
				}

				MiddlewareBody(writer, request, next)

				res := writer.Result()
				b, err := io.ReadAll(res.Body)
				assert.NoError(t, err)

				if tt.wantErr == "" {
					assert.Equal(t, http.StatusOK, res.StatusCode)
					assert.Empty(t, string(b))

					if method != http.MethodGet {
						body, err := BodyFromContext(request.Context())
						assert.NoError(t, err)
						assert.Equal(t, string(tt.body), string(body))
					}

					return
				}

				var cloudErr arm.CloudError
				err = json.Unmarshal(b, &cloudErr)
				assert.NoError(t, err)

				cloudErr.StatusCode = res.StatusCode
				assert.Equal(t, tt.wantErr, cloudErr.Error())
			})
		}
	}
}
