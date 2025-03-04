package arm

// Copyright (c) Microsoft Corporation.
// Licensed under the Apache License 2.0.

import (
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/google/go-cmp/cmp"
)

func TestWriteJSONResponse(t *testing.T) {
	resourceStruct := &TrackedResource{
		Resource: Resource{
			ID:   "00000000-0000-0000-0000-000000000000",
			Name: "testCluster",
			Type: "Microsoft.RedHatOpenShift/hcpOpenShiftClusters",
		},
		Location: "eastus1",
		Tags: map[string]string{
			"nameA": "valueA",
			"nameB": "valueB",
		},
	}

	resourceBytes, err := MarshalJSON(resourceStruct)
	if err != nil {
		t.Fatal(err)
	}

	tests := []struct {
		name       string
		statusCode int
		body       any
	}{
		{
			name:       "Write structured data",
			statusCode: http.StatusAccepted,
			body:       resourceStruct,
		},
		{
			name:       "Write byte slice",
			statusCode: http.StatusCreated,
			body:       resourceBytes,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			recorder := httptest.NewRecorder()

			_, err := WriteJSONResponse(recorder, tt.statusCode, tt.body)
			if err != nil {
				t.Fatal(err)
			}

			result := recorder.Result()

			if result.StatusCode != tt.statusCode {
				t.Errorf("Got status code %d, expected %d", result.StatusCode, tt.statusCode)
			}

			contentType := result.Header.Get("Content-Type")
			if contentType == "" {
				t.Errorf("Response is missing a Content-Type header")
			} else if contentType != "application/json" {
				t.Errorf("Got Content-Type %s, expected application/json", contentType)
			}

			expectBody, err := MarshalJSON(resourceStruct)
			if err != nil {
				t.Fatal(err)
			}
			actualBody, err := io.ReadAll(result.Body)
			if err != nil {
				t.Fatal(err)
			}
			if string(actualBody) != string(expectBody) {
				t.Error("Response body had unexpected variations:\n" + cmp.Diff(string(expectBody), string(actualBody)))
			}

		})
	}
}
