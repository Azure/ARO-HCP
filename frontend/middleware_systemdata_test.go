package main

// Copyright (c) Microsoft Corporation.
// Licensed under the Apache License 2.0.

import (
	"bytes"
	"context"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"reflect"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"

	"github.com/Azure/ARO-HCP/internal/api/arm"
)

func TestMiddlewareSystemData(t *testing.T) {
	const systemDataRaw = `
{
	"createdBy": "foo@bar.com",
	"createdByType": "Application",
	"createdAt": "2024-01-01T12:34:54.0000000Z",
	"lastModifiedBy": "00000000-0000-0000-0000-000000000000",
	"lastModifiedByType": "Application",
	"lastModifiedAt": "2024-01-01T12:34:54.0000000Z"
}`

	timestamp, err := time.Parse(time.RFC3339, "2024-01-01T12:34:54.0000000Z")
	if err != nil {
		t.Fatal(err)
	}

	tests := []struct {
		name       string
		systemData string
		expect     *arm.SystemData
	}{
		{
			name:       "systemData provided",
			systemData: systemDataRaw,
			expect: &arm.SystemData{
				CreatedBy:          "foo@bar.com",
				CreatedByType:      arm.CreatedByTypeApplication,
				CreatedAt:          &timestamp,
				LastModifiedByType: arm.CreatedByTypeApplication,
				LastModifiedBy:     "00000000-0000-0000-0000-000000000000",
				LastModifiedAt:     &timestamp,
			},
		},
		{
			name:       "systemData not provided",
			systemData: "",
			expect:     nil,
		},
		{
			name:       "invalid",
			systemData: "im_a_potato_not_a_json",
			expect:     nil,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			writer := httptest.NewRecorder()

			request, err := http.NewRequest(http.MethodPut, "", bytes.NewReader([]byte("")))
			if err != nil {
				t.Fatal(err)
			}

			if tt.systemData != "" {
				request.Header = http.Header{
					arm.HeaderNameARMResourceSystemData: []string{tt.systemData},
				}
			}

			// Add a logger to the context so parsing errors will be logged.
			request = request.WithContext(context.WithValue(request.Context(), ContextKeyLogger, slog.Default()))

			next := func(w http.ResponseWriter, r *http.Request) {
				request = r // capture modified request
				w.WriteHeader(http.StatusOK)
			}

			MiddlewareSystemData(writer, request, next)

			result, ok := request.Context().Value(ContextKeySystemData).(*arm.SystemData)
			if ok {
				if !reflect.DeepEqual(result, tt.expect) {
					t.Error(cmp.Diff(result, tt.expect))
				}
			} else if tt.expect != nil {
				t.Error("Expected SystemData in request context")
			}
		})
	}
}
