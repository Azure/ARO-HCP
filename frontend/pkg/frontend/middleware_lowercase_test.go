package frontend

// Copyright (c) Microsoft Corporation.
// Licensed under the Apache License 2.0.

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestMiddlewareLowercase(t *testing.T) {
	writer := httptest.NewRecorder()

	request, err := http.NewRequest(http.MethodGet, "/TEST", nil)
	require.NoError(t, err)

	next := func(w http.ResponseWriter, r *http.Request) {
		request = r // capture modified request
	}

	MiddlewareLowercase(writer, request, next)

	assert.Equal(t, "/test", request.URL.Path)

	originalPath, err := OriginalPathFromContext(request.Context())
	if assert.NoError(t, err) {
		assert.Equal(t, "/TEST", originalPath)
	}
}
