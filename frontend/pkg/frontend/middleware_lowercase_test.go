package frontend

// Copyright (c) Microsoft Corporation.
// Licensed under the Apache License 2.0.

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestMiddlewareLowercase(t *testing.T) {
	writer := httptest.NewRecorder()

	request, err := http.NewRequest(http.MethodGet, "/TEST", nil)
	if err != nil {
		t.Fatal(err)
	}

	next := func(w http.ResponseWriter, r *http.Request) {
		request = r // capture modified request
	}

	MiddlewareLowercase(writer, request, next)

	if request.URL.Path != "/test" {
		t.Error(request.URL.Path)
	}

	originalPath, err := OriginalPathFromContext(request.Context())
	if err != nil {
		t.Fatal(err)
	}
	if originalPath != "/TEST" {
		t.Error(originalPath)
	}
}
