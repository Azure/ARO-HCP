package frontend

// Copyright (c) Microsoft Corporation.
// Licensed under the Apache License 2.0.

import (
	"io"
	"net/http"
	"strings"

	"github.com/Azure/ARO-HCP/internal/api/arm"
)

const megabyte int64 = (1 << 20)

// MiddlewareBody ensures that the request's body doesn't exceed the maximum size of 4MB.
func MiddlewareBody(w http.ResponseWriter, r *http.Request, next http.HandlerFunc) {
	switch r.Method {
	case http.MethodPatch, http.MethodPost, http.MethodPut:
		// Max request body size accepted by ARM is 4 MB (assuming units in powers of 2).
		// See https://github.com/Azure/azure-resource-manager-rpc/blob/master/v1.0/common-api-details.md#max-request-body-size
		body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, 4*megabyte))
		if err != nil {
			arm.WriteError(
				w, http.StatusBadRequest,
				arm.CloudErrorCodeInvalidResource, "",
				"The resource definition is invalid.")
			return
		}

		contentType := strings.SplitN(r.Header.Get("Content-Type"), ";", 2)[0]

		if !strings.EqualFold(contentType, "application/json") && (len(body) > 0 || contentType != "") {
			arm.WriteError(
				w, http.StatusUnsupportedMediaType,
				arm.CloudErrorCodeUnsupportedMediaType, "",
				"The content media type '%s' is not supported. Only 'application/json' is supported.",
				r.Header.Get("Content-Type"))
			return
		}

		ctx := ContextWithBody(r.Context(), body)
		r = r.WithContext(ctx)
	}

	next(w, r)
}
